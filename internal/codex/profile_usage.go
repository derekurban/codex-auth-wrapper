package codex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	chatGPTUsageURL        = "https://chatgpt.com/backend-api/wham/usage"
	chatGPTTokenRefreshURL = "https://auth.openai.com/oauth/token"
	chatGPTTokenRefreshCID = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultRefreshTimeout  = 8 * time.Second
)

type ProfileIdentity struct {
	Email           string
	PlanType        string
	LinkedAccountID string
	LinkedUserID    string
}

type UsageWindow struct {
	UsedPercent        int
	WindowDurationMins int
	ResetAfterSeconds  int
	ResetsAt           time.Time
}

type AdditionalRateLimit struct {
	LimitID   string
	LimitName string
	Primary   *UsageWindow
	Secondary *UsageWindow
}

type CreditsSnapshot struct {
	HasCredits          bool
	Unlimited           bool
	OverageLimitReached bool
	Balance             string
}

type ProfileUsageSnapshot struct {
	Identity             ProfileIdentity
	FiveHour             *UsageWindow
	Weekly               *UsageWindow
	AdditionalRateLimits []AdditionalRateLimit
	Credits              *CreditsSnapshot
	FetchedAt            time.Time
}

type authRecord struct {
	AuthMode     string       `json:"auth_mode"`
	OpenAIAPIKey string       `json:"OPENAI_API_KEY"`
	Tokens       *tokenRecord `json:"tokens"`
	LastRefresh  *time.Time   `json:"last_refresh"`
}

type tokenRecord struct {
	RawIDToken   string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

type jwtClaims struct {
	Email   string `json:"email"`
	Profile struct {
		Email string `json:"email"`
	} `json:"https://api.openai.com/profile"`
	Auth struct {
		PlanType  string `json:"chatgpt_plan_type"`
		UserID    string `json:"chatgpt_user_id"`
		LegacyID  string `json:"user_id"`
		AccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
}

type usageResponse struct {
	UserID               string                    `json:"user_id"`
	AccountID            string                    `json:"account_id"`
	Email                string                    `json:"email"`
	PlanType             string                    `json:"plan_type"`
	RateLimit            *rateLimitStatus          `json:"rate_limit"`
	AdditionalRateLimits []additionalLimitResponse `json:"additional_rate_limits"`
	Credits              *creditsResponse          `json:"credits"`
}

type rateLimitStatus struct {
	Allowed         bool            `json:"allowed"`
	LimitReached    bool            `json:"limit_reached"`
	PrimaryWindow   *usageWindowDTO `json:"primary_window"`
	SecondaryWindow *usageWindowDTO `json:"secondary_window"`
}

type usageWindowDTO struct {
	UsedPercent        int   `json:"used_percent"`
	LimitWindowSeconds int   `json:"limit_window_seconds"`
	ResetAfterSeconds  int   `json:"reset_after_seconds"`
	ResetAt            int64 `json:"reset_at"`
}

type additionalLimitResponse struct {
	MeteredFeature string           `json:"metered_feature"`
	LimitName      string           `json:"limit_name"`
	RateLimit      *rateLimitStatus `json:"rate_limit"`
}

type creditsResponse struct {
	HasCredits          bool   `json:"has_credits"`
	Unlimited           bool   `json:"unlimited"`
	OverageLimitReached bool   `json:"overage_limit_reached"`
	Balance             string `json:"balance"`
}

type refreshRequest struct {
	ClientID     string `json:"client_id"`
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
}

type refreshResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func RefreshProfileUsage(ctx context.Context, authPath string) (ProfileUsageSnapshot, error) {
	data, auth, err := loadAuthRecord(authPath)
	if err != nil {
		return ProfileUsageSnapshot{}, err
	}
	if auth.Tokens == nil {
		return ProfileUsageSnapshot{}, fmt.Errorf("profile auth at %s is missing ChatGPT tokens", authPath)
	}
	if auth.OpenAIAPIKey != "" || strings.EqualFold(auth.AuthMode, "api_key") {
		return ProfileUsageSnapshot{}, fmt.Errorf("profile auth at %s is API-key based, not ChatGPT auth", authPath)
	}

	identity, err := identityFromTokens(auth.Tokens)
	if err != nil {
		return ProfileUsageSnapshot{}, err
	}

	usage, err := fetchUsage(ctx, auth.Tokens.AccessToken, identity.LinkedAccountID)
	if err != nil {
		var statusErr *httpStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized && auth.Tokens.RefreshToken != "" {
			if ctx.Err() != nil {
				return ProfileUsageSnapshot{}, ctx.Err()
			}
			refreshed, refreshErr := refreshTokens(ctx, auth.Tokens.RefreshToken)
			if refreshErr != nil {
				return ProfileUsageSnapshot{}, refreshErr
			}
			if refreshed.AccessToken != "" {
				auth.Tokens.AccessToken = refreshed.AccessToken
			}
			if refreshed.IDToken != "" {
				auth.Tokens.RawIDToken = refreshed.IDToken
			}
			if refreshed.RefreshToken != "" {
				auth.Tokens.RefreshToken = refreshed.RefreshToken
			}
			identity, err = identityFromTokens(auth.Tokens)
			if err != nil {
				return ProfileUsageSnapshot{}, err
			}
			now := time.Now()
			auth.LastRefresh = &now
			if err := persistRefreshedAuth(authPath, data, auth); err != nil {
				return ProfileUsageSnapshot{}, err
			}
			usage, err = fetchUsage(ctx, auth.Tokens.AccessToken, identity.LinkedAccountID)
		}
		if err != nil {
			return ProfileUsageSnapshot{}, err
		}
	}

	if usage.Email != "" {
		identity.Email = usage.Email
	}
	if usage.PlanType != "" {
		identity.PlanType = usage.PlanType
	}
	if usage.UserID != "" {
		identity.LinkedUserID = usage.UserID
	}

	return ProfileUsageSnapshot{
		Identity:             identity,
		FiveHour:             convertUsageWindow(usage.RateLimit, true),
		Weekly:               convertUsageWindow(usage.RateLimit, false),
		AdditionalRateLimits: convertAdditionalRateLimits(usage.AdditionalRateLimits),
		Credits:              convertCredits(usage.Credits),
		FetchedAt:            time.Now(),
	}, nil
}

type httpStatusError struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("GET %s failed: %d: %s", e.URL, e.StatusCode, e.Body)
}

func loadAuthRecord(authPath string) ([]byte, authRecord, error) {
	data, err := os.ReadFile(authPath)
	if err != nil {
		return nil, authRecord{}, err
	}
	var auth authRecord
	if err := json.Unmarshal(data, &auth); err != nil {
		return nil, authRecord{}, fmt.Errorf("parse %s: %w", authPath, err)
	}
	return data, auth, nil
}

func identityFromTokens(tokens *tokenRecord) (ProfileIdentity, error) {
	if tokens == nil {
		return ProfileIdentity{}, fmt.Errorf("missing token data")
	}
	claims, err := decodeJWTClaims(tokens.RawIDToken)
	if err != nil {
		return ProfileIdentity{}, fmt.Errorf("decode profile JWT: %w", err)
	}
	email := firstNonEmpty(claims.Email, claims.Profile.Email)
	userID := firstNonEmpty(claims.Auth.UserID, claims.Auth.LegacyID)
	accountID := firstNonEmpty(tokens.AccountID, claims.Auth.AccountID)
	return ProfileIdentity{
		Email:           email,
		PlanType:        claims.Auth.PlanType,
		LinkedAccountID: accountID,
		LinkedUserID:    userID,
	}, nil
}

func decodeJWTClaims(raw string) (jwtClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return jwtClaims{}, fmt.Errorf("invalid JWT format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtClaims{}, err
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return jwtClaims{}, err
	}
	return claims, nil
}

func fetchUsage(ctx context.Context, accessToken, accountID string) (usageResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, defaultRefreshTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, chatGPTUsageURL, nil)
	if err != nil {
		return usageResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "caw")
	if accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return usageResponse{}, err
	}
	defer resp.Body.Close()

	var payload usageResponse
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return usageResponse{}, &httpStatusError{
			StatusCode: resp.StatusCode,
			URL:        chatGPTUsageURL,
			Body:       string(body),
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return usageResponse{}, err
	}
	return payload, nil
}

func refreshTokens(ctx context.Context, refreshToken string) (refreshResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, defaultRefreshTimeout)
	defer cancel()

	body, err := json.Marshal(refreshRequest{
		ClientID:     chatGPTTokenRefreshCID,
		GrantType:    "refresh_token",
		RefreshToken: refreshToken,
	})
	if err != nil {
		return refreshResponse{}, err
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, chatGPTTokenRefreshURL, bytes.NewReader(body))
	if err != nil {
		return refreshResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "caw")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return refreshResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		return refreshResponse{}, fmt.Errorf("token refresh failed: %d: %s", resp.StatusCode, string(payload))
	}

	var out refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return refreshResponse{}, err
	}
	return out, nil
}

func persistRefreshedAuth(authPath string, original []byte, auth authRecord) error {
	var root map[string]any
	if err := json.Unmarshal(original, &root); err != nil {
		return fmt.Errorf("parse %s for refresh persistence: %w", authPath, err)
	}

	var tokens map[string]any
	if existing, ok := root["tokens"].(map[string]any); ok && existing != nil {
		tokens = existing
	} else {
		tokens = map[string]any{}
	}
	tokens["id_token"] = auth.Tokens.RawIDToken
	tokens["access_token"] = auth.Tokens.AccessToken
	tokens["refresh_token"] = auth.Tokens.RefreshToken
	if auth.Tokens.AccountID != "" {
		tokens["account_id"] = auth.Tokens.AccountID
	}
	root["tokens"] = tokens
	if auth.LastRefresh != nil {
		root["last_refresh"] = auth.LastRefresh.Format(time.RFC3339Nano)
	}

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	tmp := authPath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(authPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, authPath)
}

func convertUsageWindow(rateLimit *rateLimitStatus, primary bool) *UsageWindow {
	if rateLimit == nil {
		return nil
	}
	var window *usageWindowDTO
	if primary {
		window = rateLimit.PrimaryWindow
	} else {
		window = rateLimit.SecondaryWindow
	}
	if window == nil {
		return nil
	}
	return &UsageWindow{
		UsedPercent:        window.UsedPercent,
		WindowDurationMins: secondsToRoundedMinutes(window.LimitWindowSeconds),
		ResetAfterSeconds:  window.ResetAfterSeconds,
		ResetsAt:           time.Unix(window.ResetAt, 0),
	}
}

func convertAdditionalRateLimits(in []additionalLimitResponse) []AdditionalRateLimit {
	if len(in) == 0 {
		return nil
	}
	out := make([]AdditionalRateLimit, 0, len(in))
	for _, item := range in {
		out = append(out, AdditionalRateLimit{
			LimitID:   item.MeteredFeature,
			LimitName: item.LimitName,
			Primary:   convertUsageWindow(item.RateLimit, true),
			Secondary: convertUsageWindow(item.RateLimit, false),
		})
	}
	return out
}

func convertCredits(in *creditsResponse) *CreditsSnapshot {
	if in == nil {
		return nil
	}
	return &CreditsSnapshot{
		HasCredits:          in.HasCredits,
		Unlimited:           in.Unlimited,
		OverageLimitReached: in.OverageLimitReached,
		Balance:             in.Balance,
	}
}

func secondsToRoundedMinutes(seconds int) int {
	if seconds <= 0 {
		return 0
	}
	return (seconds + 59) / 60
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

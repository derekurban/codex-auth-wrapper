package codex

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestDecodeJWTClaims(t *testing.T) {
	t.Helper()

	payload, err := json.Marshal(map[string]any{
		"email": "user@example.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_plan_type":  "plus",
			"chatgpt_user_id":    "user_123",
			"chatgpt_account_id": "workspace_456",
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	token := "header." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
	claims, err := decodeJWTClaims(token)
	if err != nil {
		t.Fatalf("decodeJWTClaims: %v", err)
	}

	if claims.Email != "user@example.com" {
		t.Fatalf("expected email, got %q", claims.Email)
	}
	if claims.Auth.PlanType != "plus" {
		t.Fatalf("expected plan type, got %q", claims.Auth.PlanType)
	}
	if claims.Auth.AccountID != "workspace_456" {
		t.Fatalf("expected account id, got %q", claims.Auth.AccountID)
	}
}

func TestConvertUsageWindow(t *testing.T) {
	window := convertUsageWindow(&rateLimitStatus{
		PrimaryWindow: &usageWindowDTO{
			UsedPercent:        32,
			LimitWindowSeconds: 18000,
			ResetAfterSeconds:  14504,
			ResetAt:            1775780517,
		},
	}, true)

	if window == nil {
		t.Fatal("expected usage window")
	}
	if window.UsedPercent != 32 {
		t.Fatalf("expected used percent 32, got %d", window.UsedPercent)
	}
	if window.WindowDurationMins != 300 {
		t.Fatalf("expected 300 minutes, got %d", window.WindowDurationMins)
	}
	if want := time.Unix(1775780517, 0); !window.ResetsAt.Equal(want) {
		t.Fatalf("expected reset time %v, got %v", want, window.ResetsAt)
	}
}

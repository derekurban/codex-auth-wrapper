package broker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/derekurban/codex-auth-wrapper/internal/codex"
	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
	"github.com/derekurban/codex-auth-wrapper/internal/model"
	"github.com/derekurban/codex-auth-wrapper/internal/store"
)

const warningThreshold = 90

type Service struct {
	paths store.Paths
	store *store.Store

	mu             sync.Mutex
	server         *ipc.Server
	appServerCmd   *exec.Cmd
	appServerURL   string
	appServerToken string
	control        *codex.AppServerClient
	degradedReason string
}

func New(paths store.Paths) *Service {
	return &Service{
		paths: paths,
		store: store.New(paths),
	}
}

func (s *Service) Run(ctx context.Context) error {
	now := time.Now()
	if err := s.store.EnsureLayout(now); err != nil {
		return err
	}
	if err := os.WriteFile(s.paths.RuntimeConfigToml, []byte("cli_auth_credentials_store = \"file\"\n"), 0o644); err != nil {
		return err
	}
	if err := s.reconcileStartup(ctx); err != nil {
		s.setDegraded(err)
	}
	srv, err := ipc.Listen(s.handleIPC)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.server = srv
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		s.shutdownAppServer()
	}()
	return srv.Serve(ctx)
}

func (s *Service) reconcileStartup(ctx context.Context) error {
	state, err := s.store.LoadState()
	if err != nil {
		return err
	}
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return err
	}
	if state.SelectedProfileID == nil {
		brokerState.BrokerState = model.BrokerStateHomeReady
		brokerState.ActiveProfileID = nil
		brokerState.Server.State = model.ServerStateStopped
		brokerState.UpdatedAt = time.Now()
		return s.store.SaveBroker(brokerState)
	}
	return s.activateProfile(ctx, *state.SelectedProfileID, "startup")
}

func (s *Service) handleIPC(ctx context.Context, connID string, method string, payload json.RawMessage) (any, error) {
	switch method {
	case "session.register":
		var req ipc.RegisterSessionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.registerSession(req)
	case "home.snapshot":
		var req ipc.HomeSnapshotRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return s.homeSnapshot(ctx, req.SessionID)
	case "profile.add":
		var req ipc.AddProfileRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.addProfile(req)
	case "profile.select":
		var req ipc.SelectProfileRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.selectProfile(ctx, req)
	case "launch.prepare":
		var req ipc.PrepareLaunchRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return s.prepareLaunch(ctx, req)
	case "session.return_home":
		var req ipc.ReturnHomeRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.returnHome(req.SessionID)
	case "session.update_state":
		var req ipc.UpdateSessionStateRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.updateSessionState(req)
	case "status.snapshot":
		return s.statusSnapshot()
	case "broker.stop":
		go func() {
			time.Sleep(50 * time.Millisecond)
			s.shutdownAppServer()
			s.mu.Lock()
			server := s.server
			s.mu.Unlock()
			if server != nil {
				_ = server.Close()
			}
		}()
		return ipc.Empty{}, nil
	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

func (s *Service) registerSession(req ipc.RegisterSessionRequest) error {
	sessions, err := s.store.LoadSessions()
	if err != nil {
		return err
	}
	now := time.Now()
	record, ok := sessions.Sessions[req.SessionID]
	if !ok {
		record = model.SessionRecord{
			SessionID:     req.SessionID,
			State:         model.SessionStateHome,
			Cwd:           req.Cwd,
			ResumeAllowed: true,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
	} else {
		record.Cwd = req.Cwd
		record.UpdatedAt = now
	}
	sessions.Sessions[req.SessionID] = record
	sessions.UpdatedAt = now
	return s.store.SaveSessions(sessions)
}

func (s *Service) homeSnapshot(ctx context.Context, sessionID string) (ipc.HomeSnapshotResponse, error) {
	if err := s.refreshSelectedProfileStatus(ctx); err != nil {
		s.setDegraded(err)
	}
	state, err := s.store.LoadState()
	if err != nil {
		return ipc.HomeSnapshotResponse{}, err
	}
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.HomeSnapshotResponse{}, err
	}
	sessions, err := s.store.LoadSessions()
	if err != nil {
		return ipc.HomeSnapshotResponse{}, err
	}
	profiles, err := s.store.ListProfiles()
	if err != nil {
		return ipc.HomeSnapshotResponse{}, err
	}
	summaries := make([]ipc.ProfileSummary, 0, len(profiles))
	for _, profile := range profiles {
		selected := state.SelectedProfileID != nil && *state.SelectedProfileID == profile.ID
		summaries = append(summaries, ipc.ProfileSummary{
			ID:                   profile.ID,
			Name:                 profile.Name,
			Enabled:              profile.Enabled,
			Health:               profile.Status.Health,
			WarningState:         profile.Status.WarningState,
			FiveHourUsagePercent: profile.Status.FiveHourUsagePercent,
			WeeklyUsagePercent:   profile.Status.WeeklyUsagePercent,
			Selected:             selected,
		})
	}
	resp := ipc.HomeSnapshotResponse{
		SelectedProfileID: state.SelectedProfileID,
		Profiles:          summaries,
		BrokerState:       brokerState.BrokerState,
		ActiveAuthEpochID: brokerState.ActiveAuthEpochID,
	}
	if session, ok := sessions.Sessions[sessionID]; ok {
		resp.Session = &session
	}
	if s.degradedReason != "" {
		reason := s.degradedReason
		resp.DegradedReason = &reason
	}
	return resp, nil
}

func (s *Service) addProfile(req ipc.AddProfileRequest) error {
	if req.ID == "" || req.Name == "" || req.AuthPath == "" {
		return errors.New("profile id, name, and auth path are required")
	}
	if s.store.ProfileExists(req.ID) {
		return fmt.Errorf("profile %q already exists", req.ID)
	}
	if err := s.store.SaveProfileAuthFrom(req.ID, req.AuthPath); err != nil {
		return err
	}
	now := time.Now()
	profile := model.ProfileFile{
		SchemaVersion: model.SchemaVersion,
		ID:            req.ID,
		Name:          req.Name,
		Enabled:       true,
		AuthFile:      s.paths.ProfileAuthFile(req.ID),
		CreatedAt:     now,
		UpdatedAt:     now,
		Status: model.ProfileStatus{
			Health:       model.ProfileHealthUnknown,
			WarningState: model.ProfileWarningNone,
		},
	}
	if err := s.store.SaveProfile(profile); err != nil {
		return err
	}
	state, err := s.store.LoadState()
	if err != nil {
		return err
	}
	state.ProfileOrder = append(state.ProfileOrder, req.ID)
	if state.SelectedProfileID == nil {
		state.SelectedProfileID = &profile.ID
	}
	state.UpdatedAt = now
	if err := s.store.SaveState(state); err != nil {
		return err
	}
	if state.SelectedProfileID != nil && *state.SelectedProfileID == req.ID {
		return s.activateProfile(context.Background(), req.ID, "first_profile_added")
	}
	return nil
}

func (s *Service) selectProfile(ctx context.Context, req ipc.SelectProfileRequest) error {
	state, err := s.store.LoadState()
	if err != nil {
		return err
	}
	if state.SelectedProfileID != nil && *state.SelectedProfileID == req.ProfileID {
		return nil
	}
	if !s.store.ProfileExists(req.ProfileID) {
		return fmt.Errorf("profile %q does not exist", req.ProfileID)
	}
	now := time.Now()
	state.SelectedProfileID = &req.ProfileID
	state.CurrentAuthEpochID = nextEpochID(state.NextAuthEpochCounter)
	state.NextAuthEpochCounter++
	state.UpdatedAt = now
	if err := s.store.SaveState(state); err != nil {
		return err
	}
	if err := s.activateProfile(ctx, req.ProfileID, "profile_switch"); err != nil {
		return err
	}
	s.broadcastReload(state.CurrentAuthEpochID, state.SelectedProfileID, "profile_switched")
	return nil
}

func (s *Service) prepareLaunch(ctx context.Context, req ipc.PrepareLaunchRequest) (ipc.LaunchSpec, error) {
	state, err := s.store.LoadState()
	if err != nil {
		return ipc.LaunchSpec{}, err
	}
	if state.SelectedProfileID == nil {
		return ipc.LaunchSpec{}, errors.New("no selected profile")
	}
	if err := s.activateProfile(ctx, *state.SelectedProfileID, "prepare_launch"); err != nil {
		return ipc.LaunchSpec{}, err
	}
	sessions, err := s.store.LoadSessions()
	if err != nil {
		return ipc.LaunchSpec{}, err
	}
	record, ok := sessions.Sessions[req.SessionID]
	if !ok {
		return ipc.LaunchSpec{}, fmt.Errorf("session %q not registered", req.SessionID)
	}
	now := time.Now()
	record.State = model.SessionStateLaunchingCodex
	record.Cwd = req.Cwd
	record.LastKnownProfileID = state.SelectedProfileID
	record.LastSeenAuthEpochID = &state.CurrentAuthEpochID
	record.UpdatedAt = now
	sessions.Sessions[req.SessionID] = record
	sessions.UpdatedAt = now
	if err := s.store.SaveSessions(sessions); err != nil {
		return ipc.LaunchSpec{}, err
	}
	mode := ipc.LaunchModeFresh
	if record.ActiveThreadID != nil && record.ResumeAllowed {
		mode = ipc.LaunchModeResume
	}
	s.mu.Lock()
	spec := ipc.LaunchSpec{
		SessionID:    req.SessionID,
		ProfileID:    *state.SelectedProfileID,
		AuthEpochID:  state.CurrentAuthEpochID,
		GatewayURL:   s.appServerURL,
		TokenEnvName: codex.RemoteAuthTokenEnv,
		Token:        s.appServerToken,
		ThreadID:     record.ActiveThreadID,
		Mode:         mode,
		SelectedCwd:  req.Cwd,
	}
	s.mu.Unlock()
	return spec, nil
}

func (s *Service) returnHome(sessionID string) error {
	sessions, err := s.store.LoadSessions()
	if err != nil {
		return err
	}
	record, ok := sessions.Sessions[sessionID]
	if !ok {
		return nil
	}
	now := time.Now()
	record.State = model.SessionStateHome
	record.LastReturnedHomeAt = &now
	record.CodexChildPID = nil
	record.UpdatedAt = now
	sessions.Sessions[sessionID] = record
	sessions.UpdatedAt = now
	return s.store.SaveSessions(sessions)
}

func (s *Service) updateSessionState(req ipc.UpdateSessionStateRequest) error {
	sessions, err := s.store.LoadSessions()
	if err != nil {
		return err
	}
	record, ok := sessions.Sessions[req.SessionID]
	if !ok {
		return nil
	}
	now := time.Now()
	record.State = req.State
	record.CodexChildPID = req.CodexChildPID
	if req.State == model.SessionStateInCodex {
		record.LastEnteredCodexAt = &now
	}
	record.UpdatedAt = now
	sessions.Sessions[req.SessionID] = record
	sessions.UpdatedAt = now
	return s.store.SaveSessions(sessions)
}

func (s *Service) statusSnapshot() (ipc.StatusSnapshot, error) {
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.StatusSnapshot{}, err
	}
	sessions, err := s.store.LoadSessions()
	if err != nil {
		return ipc.StatusSnapshot{}, err
	}
	return ipc.StatusSnapshot{
		BrokerState:       brokerState.BrokerState,
		ActiveProfileID:   brokerState.ActiveProfileID,
		ActiveAuthEpochID: brokerState.ActiveAuthEpochID,
		SessionCount:      len(sessions.Sessions),
		ServerState:       brokerState.Server.State,
		ServerURL:         brokerState.Server.ListenURL,
		UpdatedAt:         brokerState.UpdatedAt,
	}, nil
}

func (s *Service) activateProfile(ctx context.Context, profileID string, reason string) error {
	if err := s.store.CopyProfileAuthToRuntime(profileID); err != nil {
		return err
	}
	if err := os.WriteFile(s.paths.RuntimeConfigToml, []byte("cli_auth_credentials_store = \"file\"\n"), 0o644); err != nil {
		return err
	}
	s.shutdownAppServer()
	if err := s.startAppServer(ctx, profileID, reason); err != nil {
		return err
	}
	state, err := s.store.LoadState()
	if err != nil {
		return err
	}
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return err
	}
	now := time.Now()
	brokerState.BrokerState = model.BrokerStateActive
	brokerState.ActiveProfileID = &profileID
	brokerState.ActiveAuthEpochID = state.CurrentAuthEpochID
	brokerState.Server.State = model.ServerStateHealthy
	brokerState.Server.ListenURL = &s.appServerURL
	authMode := "capability-token"
	brokerState.Server.AuthMode = &authMode
	brokerState.Server.StartedAt = &now
	brokerState.Server.LastRestartReason = &reason
	brokerState.SwitchContext = model.SwitchContext{}
	brokerState.UpdatedAt = now
	if err := s.store.SaveBroker(brokerState); err != nil {
		return err
	}
	profile, err := s.store.LoadProfile(profileID)
	if err == nil {
		profile.LastSelectedAt = &now
		profile.UpdatedAt = now
		_ = s.store.SaveProfile(profile)
	}
	s.clearDegraded()
	return nil
}

func (s *Service) startAppServer(ctx context.Context, profileID, reason string) error {
	port, err := allocateLoopbackPort()
	if err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.paths.AppServerTokenFile, []byte(token), 0o600); err != nil {
		return err
	}
	listenURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	cmd := exec.Command("codex", "app-server",
		"--listen", listenURL,
		"--ws-auth", "capability-token",
		"--ws-token-file", s.paths.AppServerTokenFile,
		"-c", "cli_auth_credentials_store=file",
	)
	cmd.Env = append(os.Environ(),
		"CODEX_HOME="+s.paths.RuntimeCodexHome,
		"LOG_FORMAT=json",
	)
	logFile := filepath.Join(s.paths.LogsDir, "broker-app-server.log")
	logHandle, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd.Stdout = logHandle
	cmd.Stderr = logHandle
	if err := cmd.Start(); err != nil {
		_ = logHandle.Close()
		return err
	}
	s.mu.Lock()
	s.appServerCmd = cmd
	s.appServerURL = listenURL
	s.appServerToken = token
	s.mu.Unlock()
	go func(cmd *exec.Cmd, f *os.File) {
		_ = cmd.Wait()
		_ = f.Close()
		s.mu.Lock()
		if s.appServerCmd == cmd {
			s.appServerCmd = nil
		}
		s.mu.Unlock()
	}(cmd, logHandle)
	if err := waitForReady(ctx, listenURL, 8*time.Second); err != nil {
		s.shutdownAppServer()
		return err
	}
	control, err := codex.DialAppServer(ctx, listenURL, token)
	if err != nil {
		s.shutdownAppServer()
		return err
	}
	s.mu.Lock()
	if s.control != nil {
		_ = s.control.Close()
	}
	s.control = control
	s.mu.Unlock()
	return nil
}

func (s *Service) shutdownAppServer() {
	s.mu.Lock()
	cmd := s.appServerCmd
	control := s.control
	s.appServerCmd = nil
	s.control = nil
	s.appServerURL = ""
	s.appServerToken = ""
	s.mu.Unlock()
	if control != nil {
		_ = control.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	brokerState, err := s.store.LoadBroker()
	if err == nil {
		brokerState.Server.State = model.ServerStateStopped
		brokerState.Server.ListenURL = nil
		brokerState.Server.AuthMode = nil
		now := time.Now()
		brokerState.UpdatedAt = now
		_ = s.store.SaveBroker(brokerState)
	}
}

func (s *Service) refreshSelectedProfileStatus(ctx context.Context) error {
	state, err := s.store.LoadState()
	if err != nil {
		return err
	}
	if state.SelectedProfileID == nil {
		return nil
	}
	s.mu.Lock()
	control := s.control
	s.mu.Unlock()
	if control == nil {
		return nil
	}
	profile, err := s.store.LoadProfile(*state.SelectedProfileID)
	if err != nil {
		return err
	}
	account, err := control.AccountRead(ctx)
	if err != nil {
		return err
	}
	rateLimits, err := control.RateLimitsRead(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	profile.UpdatedAt = now
	profile.Status.LastCheckedAt = &now
	profile.Status.FiveHourUsagePercent = nil
	profile.Status.WeeklyUsagePercent = nil
	profile.Status.FiveHourWindowLabel = ""
	profile.Status.WeeklyWindowLabel = ""
	if rateLimits.Primary != nil {
		val := rateLimits.Primary.UsedPercent
		profile.Status.FiveHourUsagePercent = &val
		profile.Status.FiveHourWindowLabel = "Primary resets " + rateLimits.Primary.ResetsAt.Local().Format(time.Kitchen)
	}
	if rateLimits.Secondary != nil {
		val := rateLimits.Secondary.UsedPercent
		profile.Status.WeeklyUsagePercent = &val
		profile.Status.WeeklyWindowLabel = "Secondary resets " + rateLimits.Secondary.ResetsAt.Local().Format(time.Kitchen)
	}
	profile.Status.WarningState = warningState(profile.Status.FiveHourUsagePercent, profile.Status.WeeklyUsagePercent)
	profile.Status.Health = healthState(account, profile.Status.WarningState, profile.Status.FiveHourUsagePercent, profile.Status.WeeklyUsagePercent)
	return s.store.SaveProfile(profile)
}

func healthState(account codex.AccountInfo, warning model.ProfileWarningState, five *int, weekly *int) model.ProfileHealth {
	if account.RequiresOpenAIAuth && account.Type == "" {
		return model.ProfileHealthAuthFailed
	}
	if overOrEqual(five, 100) || overOrEqual(weekly, 100) {
		return model.ProfileHealthExhausted
	}
	if warning != model.ProfileWarningNone {
		return model.ProfileHealthWarning
	}
	if account.Type == "" {
		return model.ProfileHealthUnknown
	}
	return model.ProfileHealthHealthy
}

func overOrEqual(v *int, threshold int) bool {
	return v != nil && *v >= threshold
}

func warningState(five *int, weekly *int) model.ProfileWarningState {
	fiveWarn := overOrEqual(five, warningThreshold)
	weeklyWarn := overOrEqual(weekly, warningThreshold)
	switch {
	case fiveWarn && weeklyWarn:
		return model.ProfileWarningBoth
	case fiveWarn:
		return model.ProfileWarningFiveHour
	case weeklyWarn:
		return model.ProfileWarningWeekly
	default:
		return model.ProfileWarningNone
	}
}

func (s *Service) broadcastReload(authEpochID string, profileID *string, reason string) {
	s.mu.Lock()
	server := s.server
	s.mu.Unlock()
	if server != nil {
		server.Broadcast("reload.notice", ipc.ReloadNotice{
			AuthEpochID: authEpochID,
			ProfileID:   profileID,
			Reason:      reason,
		})
	}
}

func (s *Service) setDegraded(err error) {
	s.mu.Lock()
	s.degradedReason = err.Error()
	s.mu.Unlock()
	brokerState, loadErr := s.store.LoadBroker()
	if loadErr == nil {
		brokerState.BrokerState = model.BrokerStateDegraded
		brokerState.UpdatedAt = time.Now()
		_ = s.store.SaveBroker(brokerState)
	}
}

func (s *Service) clearDegraded() {
	s.mu.Lock()
	s.degradedReason = ""
	s.mu.Unlock()
}

func nextEpochID(counter int) string {
	return fmt.Sprintf("epoch-%07d", counter)
}

func allocateLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func waitForReady(ctx context.Context, rawWSURL string, timeout time.Duration) error {
	u, err := url.Parse(rawWSURL)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	default:
		return fmt.Errorf("unsupported websocket scheme %q", u.Scheme)
	}
	u.Path = "/readyz"
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(timeout)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return nil
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return errors.New("timed out waiting for app-server readiness")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

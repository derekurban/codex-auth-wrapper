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

const (
	warningThreshold       = 90
	profileRefreshInterval = 2 * time.Minute
	warningRefreshInterval = 30 * time.Second
	errorRefreshInterval   = 45 * time.Second
	idleShutdownDelay      = 10 * time.Second
	backgroundRefreshTTL   = 20 * time.Second
	maxProfileRefreshJobs  = 3
)

type Service struct {
	paths store.Paths
	store *store.Store

	mu             sync.Mutex
	switchMu       sync.Mutex
	refreshRunMu   sync.Mutex
	server         *ipc.Server
	gateway        *gatewayServer
	appServerCmd   *exec.Cmd
	appServerURL   string
	appServerToken string
	gatewayURL     string
	sessionTokens  map[string]string
	connSessions   map[string]string
	sessionRuntime map[string]*sessionRuntime
	control        *codex.AppServerClient
	degradedReason string
	refreshing     bool
}

func New(paths store.Paths) *Service {
	return &Service{
		paths:          paths,
		store:          store.New(paths),
		sessionTokens:  map[string]string{},
		connSessions:   map[string]string{},
		sessionRuntime: map[string]*sessionRuntime{},
	}
}

func (s *Service) Run(ctx context.Context) error {
	now := time.Now()
	if err := s.store.EnsureLayout(now); err != nil {
		return err
	}
	if err := s.reconcileStartup(ctx); err != nil {
		s.setDegraded(err)
	}
	if err := s.startGateway(ctx); err != nil {
		return err
	}
	srv, err := ipc.Listen(s.handleIPC)
	if err != nil {
		s.shutdownGateway()
		return err
	}
	s.mu.Lock()
	s.server = srv
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		s.shutdownGateway()
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
	if brokerState.SwitchContext.InProgress && brokerState.SwitchContext.ToProfileID != nil {
		s.switchMu.Lock()
		defer s.switchMu.Unlock()
		return s.commitProfileSwitch(ctx, *brokerState.SwitchContext.ToProfileID, false, "startup_pending_switch")
	}
	return s.activateProfile(ctx, *state.SelectedProfileID, "startup")
}

func (s *Service) handleIPC(ctx context.Context, connID string, method string, payload json.RawMessage) (any, error) {
	switch method {
	case "$connection.closed":
		return ipc.Empty{}, s.handleConnectionClosed(ctx, connID)
	case "session.register":
		var req ipc.RegisterSessionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.registerSession(connID, req)
	case "home.snapshot":
		var req ipc.HomeSnapshotRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return s.homeSnapshot(ctx, req)
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
		return s.selectProfile(ctx, connID, req)
	case "profile.switch.force":
		var req ipc.ForcePendingSwitchRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return s.forcePendingSwitch(ctx, req)
	case "profile.switch.cancel":
		var req ipc.CancelPendingSwitchRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return s.cancelPendingSwitch(req)
	case "launch.prepare":
		var req ipc.PrepareLaunchRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return s.prepareLaunch(ctx, req)
	case "settings.update":
		var req ipc.UpdateSettingsRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.updateSettings(req)
	case "session.return_home":
		var req ipc.ReturnHomeRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.returnHome(req.SessionID)
	case "session.unregister":
		var req ipc.UnregisterSessionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.unregisterSession(req.SessionID)
	case "session.update_state":
		var req ipc.UpdateSessionStateRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.updateSessionState(req)
	case "status.snapshot":
		return s.statusSnapshot()
	case "profiles.refresh":
		var req ipc.HomeSnapshotRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return ipc.Empty{}, s.runProfileRefresh(ctx, req.ForceRefresh, false)
	case "broker.stop":
		go func() {
			time.Sleep(50 * time.Millisecond)
			s.shutdownGateway()
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

func (s *Service) registerSession(connID string, req ipc.RegisterSessionRequest) error {
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
	if err := s.store.SaveSessions(sessions); err != nil {
		return err
	}
	// Host IPC connections are the broker's source of truth for whether a CAW
	// window is still alive. Pending global switches should not wait on windows
	// that have already disconnected.
	s.bindHostConnection(req.SessionID, connID)
	return nil
}

func (s *Service) homeSnapshot(ctx context.Context, req ipc.HomeSnapshotRequest) (ipc.HomeSnapshotResponse, error) {
	if req.ForceRefresh {
		if err := s.runProfileRefresh(ctx, true, false); err != nil {
			s.setDegraded(err)
		}
	} else if s.shouldStartBackgroundRefresh() {
		s.startBackgroundRefresh()
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
	pendingTargetID := ""
	if brokerState.SwitchContext.InProgress && brokerState.SwitchContext.ToProfileID != nil {
		pendingTargetID = *brokerState.SwitchContext.ToProfileID
	}
	for _, profile := range profiles {
		selected := state.SelectedProfileID != nil && *state.SelectedProfileID == profile.ID
		summaries = append(summaries, ipc.ProfileSummary{
			ID:                   profile.ID,
			Name:                 profile.Name,
			Enabled:              profile.Enabled,
			Health:               profile.Status.Health,
			WarningState:         profile.Status.WarningState,
			Email:                profile.Status.Email,
			PlanType:             profile.Status.PlanType,
			LinkedAccountID:      profile.Status.LinkedAccountID,
			LinkedUserID:         profile.Status.LinkedUserID,
			FiveHourUsagePercent: profile.Status.FiveHourUsagePercent,
			WeeklyUsagePercent:   profile.Status.WeeklyUsagePercent,
			FiveHourResetsAt:     profile.Status.FiveHourResetsAt,
			WeeklyResetsAt:       profile.Status.WeeklyResetsAt,
			LastCheckedAt:        profile.Status.LastCheckedAt,
			LastError:            profile.Status.LastError,
			Selected:             selected,
			PendingTarget:        pendingTargetID != "" && pendingTargetID == profile.ID,
		})
	}
	resp := ipc.HomeSnapshotResponse{
		SelectedProfileID: state.SelectedProfileID,
		Profiles:          summaries,
		Settings: ipc.WrapperSettings{
			ClearTerminalBeforeLaunch: state.Settings.ClearTerminalEnabled(),
		},
		BrokerState:       brokerState.BrokerState,
		ActiveAuthEpochID: brokerState.ActiveAuthEpochID,
		PendingSwitch:     s.pendingSwitchSummary(&brokerState, req.SessionID),
		RefreshInProgress: s.refreshInProgress(),
	}
	if session, ok := sessions.Sessions[req.SessionID]; ok {
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
		if err := s.activateProfile(context.Background(), req.ID, "first_profile_added"); err != nil {
			return err
		}
	}
	refreshCtx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := s.refreshProfileStatus(refreshCtx, req.ID); err != nil {
		s.setDegraded(err)
		return err
	}
	return nil
}

func (s *Service) prepareLaunch(ctx context.Context, req ipc.PrepareLaunchRequest) (ipc.LaunchSpec, error) {
	state, err := s.store.LoadState()
	if err != nil {
		return ipc.LaunchSpec{}, err
	}
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.LaunchSpec{}, err
	}
	if state.SelectedProfileID == nil {
		return ipc.LaunchSpec{}, errors.New("no selected profile")
	}
	if brokerState.SwitchContext.InProgress {
		return ipc.LaunchSpec{}, errors.New("profile switch is pending; wait for live Codex sessions to become idle or force/cancel the switch from Home")
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
	selectedCwd := req.Cwd
	if record.ActiveThreadID != nil && record.ResumeAllowed && record.ActiveThreadCwd != nil && *record.ActiveThreadCwd != "" {
		selectedCwd = *record.ActiveThreadCwd
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
	sessionToken, err := s.issueSessionToken(req.SessionID)
	if err != nil {
		return ipc.LaunchSpec{}, err
	}
	s.mu.Lock()
	spec := ipc.LaunchSpec{
		SessionID:    req.SessionID,
		ProfileID:    *state.SelectedProfileID,
		AuthEpochID:  state.CurrentAuthEpochID,
		GatewayURL:   s.gatewayURL,
		TokenEnvName: codex.RemoteAuthTokenEnv,
		Token:        sessionToken,
		ThreadID:     record.ActiveThreadID,
		Mode:         mode,
		SelectedCwd:  selectedCwd,
		Settings: ipc.WrapperSettings{
			ClearTerminalBeforeLaunch: state.Settings.ClearTerminalEnabled(),
		},
	}
	s.mu.Unlock()
	return spec, nil
}

func (s *Service) updateSettings(req ipc.UpdateSettingsRequest) error {
	state, err := s.store.LoadState()
	if err != nil {
		return err
	}
	now := time.Now()
	value := req.ClearTerminalBeforeLaunch
	state.Settings.ClearTerminalBeforeLaunch = &value
	state.UpdatedAt = now
	return s.store.SaveState(state)
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
	if err := s.store.SaveSessions(sessions); err != nil {
		return err
	}
	s.maybeScheduleIdleShutdown()
	return s.reconcilePendingSwitch(context.Background(), "session_returned_home")
}

func (s *Service) unregisterSession(sessionID string) error {
	sessions, err := s.store.LoadSessions()
	if err != nil {
		return err
	}
	if _, ok := sessions.Sessions[sessionID]; !ok {
		s.revokeSessionTokens(sessionID)
		s.unregisterRuntimeSession(sessionID)
		s.maybeScheduleIdleShutdown()
		return s.reconcilePendingSwitch(context.Background(), "session_unregistered")
	}
	delete(sessions.Sessions, sessionID)
	sessions.UpdatedAt = time.Now()
	if err := s.store.SaveSessions(sessions); err != nil {
		return err
	}
	s.revokeSessionTokens(sessionID)
	s.unregisterRuntimeSession(sessionID)
	s.maybeScheduleIdleShutdown()
	return s.reconcilePendingSwitch(context.Background(), "session_unregistered")
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
	if err := s.store.SaveSessions(sessions); err != nil {
		return err
	}
	s.maybeScheduleIdleShutdown()
	return s.reconcilePendingSwitch(context.Background(), "session_state_updated")
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
	if s.canReuseActiveProfile(profileID) {
		return s.markActiveProfile(profileID, "")
	}
	if err := s.store.CopyProfileAuthToRuntime(profileID); err != nil {
		return err
	}
	s.shutdownAppServer()
	if err := s.startAppServer(ctx, profileID, reason); err != nil {
		return err
	}
	return s.markActiveProfile(profileID, reason)
}

func (s *Service) markActiveProfile(profileID string, reason string) error {
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
	if s.appServerURL != "" {
		brokerState.Server.ListenURL = &s.appServerURL
	}
	authMode := "capability-token"
	brokerState.Server.AuthMode = &authMode
	if reason != "" {
		brokerState.Server.StartedAt = &now
		brokerState.Server.LastRestartReason = &reason
	}
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

func (s *Service) canReuseActiveProfile(profileID string) bool {
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return false
	}
	if brokerState.ActiveProfileID == nil || *brokerState.ActiveProfileID != profileID || brokerState.Server.State != model.ServerStateHealthy {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appServerCmd != nil && s.control != nil && s.appServerURL != ""
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
		"CODEX_HOME="+s.paths.CodexHome,
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

func (s *Service) refreshProfileStatuses(ctx context.Context, force bool) error {
	profiles, err := s.store.ListProfiles()
	if err != nil {
		return err
	}
	targets := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		if !force && !shouldRefreshProfile(profile) {
			continue
		}
		targets = append(targets, profile.ID)
	}
	if len(targets) == 0 {
		return nil
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(targets))
	sem := make(chan struct{}, maxProfileRefreshJobs)
	for _, profileID := range targets {
		profileID := profileID
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			if err := s.refreshProfileStatus(ctx, profileID); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) shouldStartBackgroundRefresh() bool {
	if s.refreshInProgress() {
		return false
	}
	profiles, err := s.store.ListProfiles()
	if err != nil {
		return false
	}
	for _, profile := range profiles {
		if shouldRefreshProfile(profile) {
			return true
		}
	}
	return false
}

func (s *Service) startBackgroundRefresh() {
	s.mu.Lock()
	if s.refreshing {
		s.mu.Unlock()
		return
	}
	s.refreshing = true
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), backgroundRefreshTTL)
		defer cancel()
		if err := s.runProfileRefresh(ctx, false, true); err != nil {
			s.setDegraded(err)
		}
	}()
}

func (s *Service) runProfileRefresh(ctx context.Context, force bool, alreadyMarked bool) error {
	s.refreshRunMu.Lock()
	defer s.refreshRunMu.Unlock()
	if !alreadyMarked {
		s.setRefreshInProgress(true)
	}
	defer s.setRefreshInProgress(false)
	return s.refreshProfileStatuses(ctx, force)
}

func (s *Service) setRefreshInProgress(v bool) {
	s.mu.Lock()
	s.refreshing = v
	s.mu.Unlock()
}

func (s *Service) refreshInProgress() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshing
}

func (s *Service) refreshProfileStatus(ctx context.Context, profileID string) error {
	profile, err := s.store.LoadProfile(profileID)
	if err != nil {
		return err
	}
	now := time.Now()
	if !profile.Enabled {
		profile.Status.Health = model.ProfileHealthDisabled
		profile.UpdatedAt = now
		return s.store.SaveProfile(profile)
	}

	snapshot, err := codex.RefreshProfileUsage(ctx, profile.AuthFile)
	if err != nil {
		profile.Status.LastCheckedAt = &now
		profile.Status.LastError = err.Error()
		profile.Status.Health = healthState(profile.Enabled, profile.Status.Email, profile.Status.PlanType, profile.Status.WarningState, profile.Status.FiveHourUsagePercent, profile.Status.WeeklyUsagePercent, profile.Status.LastError)
		profile.UpdatedAt = now
		return s.store.SaveProfile(profile)
	}

	profile.Status.Email = snapshot.Identity.Email
	profile.Status.PlanType = snapshot.Identity.PlanType
	profile.Status.LinkedAccountID = snapshot.Identity.LinkedAccountID
	profile.Status.LinkedUserID = snapshot.Identity.LinkedUserID
	profile.Status.FiveHourUsagePercent = nil
	profile.Status.WeeklyUsagePercent = nil
	profile.Status.FiveHourWindowLabel = ""
	profile.Status.WeeklyWindowLabel = ""
	profile.Status.FiveHourResetsAt = nil
	profile.Status.WeeklyResetsAt = nil
	if snapshot.FiveHour != nil {
		val := snapshot.FiveHour.UsedPercent
		resetAt := snapshot.FiveHour.ResetsAt
		profile.Status.FiveHourUsagePercent = &val
		profile.Status.FiveHourResetsAt = &resetAt
		profile.Status.FiveHourWindowLabel = formatWindowLabel("5-hour", snapshot.FiveHour)
	}
	if snapshot.Weekly != nil {
		val := snapshot.Weekly.UsedPercent
		resetAt := snapshot.Weekly.ResetsAt
		profile.Status.WeeklyUsagePercent = &val
		profile.Status.WeeklyResetsAt = &resetAt
		profile.Status.WeeklyWindowLabel = formatWindowLabel("weekly", snapshot.Weekly)
	}
	profile.Status.LastCheckedAt = &snapshot.FetchedAt
	profile.Status.LastError = ""
	profile.Status.WarningState = warningState(profile.Status.FiveHourUsagePercent, profile.Status.WeeklyUsagePercent)
	profile.Status.Health = healthState(profile.Enabled, profile.Status.Email, profile.Status.PlanType, profile.Status.WarningState, profile.Status.FiveHourUsagePercent, profile.Status.WeeklyUsagePercent, "")
	profile.UpdatedAt = now
	return s.store.SaveProfile(profile)
}

func healthState(enabled bool, email string, planType string, warning model.ProfileWarningState, five *int, weekly *int, lastErr string) model.ProfileHealth {
	if !enabled {
		return model.ProfileHealthDisabled
	}
	if lastErr != "" {
		return model.ProfileHealthAuthFailed
	}
	if overOrEqual(five, 100) || overOrEqual(weekly, 100) {
		return model.ProfileHealthExhausted
	}
	if warning != model.ProfileWarningNone {
		return model.ProfileHealthWarning
	}
	if email == "" && planType == "" {
		return model.ProfileHealthUnknown
	}
	return model.ProfileHealthHealthy
}

func shouldRefreshProfile(profile model.ProfileFile) bool {
	if !profile.Enabled {
		return false
	}
	if profile.Status.LastCheckedAt == nil {
		return true
	}
	age := time.Since(*profile.Status.LastCheckedAt)
	switch {
	case profile.Status.LastError != "":
		return age >= errorRefreshInterval
	case profile.Status.WarningState != model.ProfileWarningNone:
		return age >= warningRefreshInterval
	default:
		return age >= profileRefreshInterval
	}
}

func formatWindowLabel(name string, window *codex.UsageWindow) string {
	if window == nil {
		return ""
	}
	return fmt.Sprintf("%s resets %s", name, window.ResetsAt.Local().Format("Jan 2 3:04 PM"))
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

func (s *Service) maybeScheduleIdleShutdown() {
	go func() {
		time.Sleep(idleShutdownDelay)
		sessions, err := s.store.LoadSessions()
		if err != nil {
			return
		}
		if len(sessions.Sessions) > 0 {
			return
		}
		s.shutdownGateway()
		s.shutdownAppServer()
		s.mu.Lock()
		server := s.server
		s.mu.Unlock()
		if server != nil {
			_ = server.Close()
		}
	}()
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

func (s *Service) issueSessionToken(sessionID string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for existingToken, existingSessionID := range s.sessionTokens {
		if existingSessionID == sessionID {
			delete(s.sessionTokens, existingToken)
		}
	}
	s.sessionTokens[token] = sessionID
	return token, nil
}

func (s *Service) revokeSessionTokens(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, existingSessionID := range s.sessionTokens {
		if existingSessionID == sessionID {
			delete(s.sessionTokens, token)
		}
	}
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

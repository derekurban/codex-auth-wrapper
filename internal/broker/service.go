package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	gatewaypkg "github.com/derekurban/codex-auth-wrapper/internal/broker/gateway"
	runtimepkg "github.com/derekurban/codex-auth-wrapper/internal/broker/runtime"
	sessionspkg "github.com/derekurban/codex-auth-wrapper/internal/broker/sessions"
	switchflowpkg "github.com/derekurban/codex-auth-wrapper/internal/broker/switchflow"
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

// Service is the broker composition root. It exposes the existing IPC surface
// while delegating session ownership, runtime control, and profile-switch
// decisions to dedicated bounded-context components.
type Service struct {
	paths store.Paths
	store *store.Store

	server   *ipc.Server
	gateway  *gatewaypkg.Server
	runtime  *runtimepkg.Controller
	sessions *sessionspkg.Manager
	switches *switchflowpkg.Coordinator

	switchMu     chan struct{}
	refreshRunMu chan struct{}

	degradedReason string
	refreshing     bool
}

func New(paths store.Paths) *Service {
	st := store.New(paths)
	return &Service{
		paths:        paths,
		store:        st,
		runtime:      runtimepkg.New(paths, st),
		sessions:     sessionspkg.New(st),
		switches:     switchflowpkg.New(),
		switchMu:     make(chan struct{}, 1),
		refreshRunMu: make(chan struct{}, 1),
	}
}

func (s *Service) Run(ctx context.Context) error {
	now := time.Now()
	if err := s.store.EnsureLayout(now); err != nil {
		return err
	}
	if err := s.reconcileStartup(ctx, now); err != nil {
		s.setDegraded(err)
	}
	if err := s.startGateway(ctx); err != nil {
		return err
	}
	srv, err := ipc.Listen(s.handleIPC)
	if err != nil {
		s.gateway.Close()
		s.runtime.Shutdown()
		return err
	}
	s.server = srv
	go func() {
		<-ctx.Done()
		_ = srv.Close()
		s.gateway.Close()
		s.runtime.Shutdown()
	}()
	return srv.Serve(ctx)
}

func (s *Service) reconcileStartup(ctx context.Context, now time.Time) error {
	if err := s.sessions.Reset(now); err != nil {
		return err
	}
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return err
	}
	cleared := s.switches.ClearStaleOnStartup(now, brokerState)
	if cleared != brokerState {
		if err := s.store.SaveBroker(cleared); err != nil {
			return err
		}
	}
	return s.runtime.ReconcileStartup(ctx)
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
		return ipc.Empty{}, s.sessions.RegisterHost(req.SessionID, connID, req.Cwd, time.Now())
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
		return s.forcePendingSwitch(ctx, connID, req)
	case "profile.switch.cancel":
		var req ipc.CancelPendingSwitchRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, err
		}
		return s.cancelPendingSwitch(connID, req)
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
			s.gateway.Close()
			s.runtime.Shutdown()
			if s.server != nil {
				_ = s.server.Close()
			}
		}()
		return ipc.Empty{}, nil
	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

func (s *Service) startGateway(ctx context.Context) error {
	if s.gateway != nil {
		return nil
	}
	s.gateway = gatewaypkg.New(s.gatewayBackend, s.sessions.SessionThreadFilterCwd, s)
	return s.gateway.Start(ctx)
}

func (s *Service) gatewayBackend(sessionID string) (string, string, bool) {
	backendURL, backendToken, ok := s.runtime.Backend()
	if !ok {
		return "", "", false
	}
	if sessionID == "" {
		return "", "", false
	}
	return backendURL, backendToken, true
}

func (s *Service) handleConnectionClosed(ctx context.Context, connID string) error {
	sessionID := s.sessions.HandleHostDisconnect(connID)
	if sessionID == "" {
		return nil
	}
	return s.reconcilePendingSwitch(ctx, "", "host_disconnected")
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
	session, err := s.sessions.Session(req.SessionID)
	if err != nil {
		return ipc.HomeSnapshotResponse{}, err
	}
	resp := ipc.HomeSnapshotResponse{
		SelectedProfileID: state.SelectedProfileID,
		Profiles:          summaries,
		Session:           session,
		Settings: ipc.WrapperSettings{
			ClearTerminalBeforeLaunch: state.Settings.ClearTerminalEnabled(),
		},
		BrokerState:       brokerState.BrokerState,
		ActiveAuthEpochID: brokerState.ActiveAuthEpochID,
		PendingSwitch:     s.pendingSwitchSummary(&brokerState, req.SessionID),
		RefreshInProgress: s.refreshing,
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
		if err := s.runtime.EnsureActiveProfile(context.Background(), req.ID, "first_profile_added"); err != nil {
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

func (s *Service) selectProfile(ctx context.Context, connID string, req ipc.SelectProfileRequest) (ipc.SelectProfileResponse, error) {
	s.lockSwitch()
	defer s.unlockSwitch()
	state, err := s.store.LoadState()
	if err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	readiness, err := s.sessions.Readiness()
	if err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	decision, err := s.switches.RequestSwitch(time.Now(), state, brokerState, req.SessionID, req.ProfileID, switchflowpkg.Readiness(readiness), s.store.ProfileExists(req.ProfileID))
	if err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	if decision.CommitProfileID != nil {
		if _, err := s.runtime.CommitProfileSwitch(ctx, *decision.CommitProfileID, false, "profile_switched"); err != nil {
			return ipc.SelectProfileResponse{}, err
		}
		state, _ = s.store.LoadState()
		s.broadcastSwitchNotice(connID, "committed", "Account switched. Live Codex sessions are reloading onto the new account.", nil)
		s.broadcastReload(state.CurrentAuthEpochID, state.SelectedProfileID, "profile_switched", false, "Account switched. Live Codex sessions are reloading onto the new account.")
		return ipc.SelectProfileResponse{
			Outcome:         decision.Outcome,
			ActiveProfileID: state.SelectedProfileID,
		}, nil
	}
	if err := s.store.SaveBroker(decision.Broker); err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	summary := s.pendingSwitchSummary(&decision.Broker, req.SessionID)
	switch decision.Outcome {
	case ipc.ProfileSelectOutcomePending:
		s.broadcastSwitchNotice(connID, "pending", switchPendingMessage(&decision.Broker.SwitchContext), summary)
	case ipc.ProfileSelectOutcomeUpdatedPending:
		s.broadcastSwitchNotice(connID, "updated", switchPendingMessage(&decision.Broker.SwitchContext), summary)
	}
	return ipc.SelectProfileResponse{
		Outcome:         decision.Outcome,
		ActiveProfileID: decision.ActiveProfileID,
		PendingSwitch:   summary,
	}, nil
}

func (s *Service) forcePendingSwitch(ctx context.Context, connID string, req ipc.ForcePendingSwitchRequest) (ipc.PendingSwitchResponse, error) {
	s.lockSwitch()
	defer s.unlockSwitch()
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.PendingSwitchResponse{}, err
	}
	decision, err := s.switches.Force(brokerState, req.SessionID)
	if err != nil {
		return ipc.PendingSwitchResponse{}, err
	}
	if decision.CommitProfileID == nil {
		return ipc.PendingSwitchResponse{}, nil
	}
	result, err := s.runtime.CommitProfileSwitch(ctx, *decision.CommitProfileID, true, "profile_switch_forced")
	if err != nil {
		return ipc.PendingSwitchResponse{}, err
	}
	state, _ := s.store.LoadState()
	s.broadcastSwitchNotice(connID, "committed", "Account switch was forced. Live Codex sessions are reloading onto the new account.", nil)
	s.broadcastReload(result.AuthEpochID, state.SelectedProfileID, "profile_switch_forced", true, "Account switch was forced. Live Codex sessions are reloading onto the new account.")
	return ipc.PendingSwitchResponse{Committed: true}, nil
}

func (s *Service) cancelPendingSwitch(connID string, req ipc.CancelPendingSwitchRequest) (ipc.PendingSwitchResponse, error) {
	s.lockSwitch()
	defer s.unlockSwitch()
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.PendingSwitchResponse{}, err
	}
	decision, err := s.switches.Cancel(time.Now(), brokerState, req.SessionID)
	if err != nil {
		return ipc.PendingSwitchResponse{}, err
	}
	if err := s.store.SaveBroker(decision.Broker); err != nil {
		return ipc.PendingSwitchResponse{}, err
	}
	s.broadcastSwitchNotice(connID, "cancelled", "Pending account switch cancelled.", nil)
	return ipc.PendingSwitchResponse{Cancelled: true}, nil
}

func (s *Service) reconcilePendingSwitch(ctx context.Context, excludeConnID string, reason string) error {
	s.lockSwitch()
	defer s.unlockSwitch()
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return err
	}
	readiness, err := s.sessions.Readiness()
	if err != nil {
		return err
	}
	decision := s.switches.Reconcile(time.Now(), brokerState, switchflowpkg.Readiness(readiness))
	if decision.CommitProfileID != nil {
		result, err := s.runtime.CommitProfileSwitch(ctx, *decision.CommitProfileID, false, reason)
		if err != nil {
			return err
		}
		state, _ := s.store.LoadState()
		s.broadcastSwitchNotice(excludeConnID, "committed", "Account switched. Live Codex sessions are reloading onto the new account.", nil)
		s.broadcastReload(result.AuthEpochID, state.SelectedProfileID, reason, false, "Account switched. Live Codex sessions are reloading onto the new account.")
		return nil
	}
	if decision.Broker != brokerState {
		if err := s.store.SaveBroker(decision.Broker); err != nil {
			return err
		}
		if decision.Broker.SwitchContext.InProgress {
			s.broadcastSwitchNotice(excludeConnID, "updated", switchPendingMessage(&decision.Broker.SwitchContext), s.pendingSwitchSummary(&decision.Broker, ""))
		}
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
	if err := s.runtime.EnsureActiveProfile(ctx, *state.SelectedProfileID, "prepare_launch"); err != nil {
		return ipc.LaunchSpec{}, err
	}
	record, selectedCwd, err := s.sessions.PrepareLaunch(req.SessionID, req.Cwd, state.SelectedProfileID, state.CurrentAuthEpochID, time.Now())
	if err != nil {
		return ipc.LaunchSpec{}, err
	}
	if record.SessionID == "" {
		return ipc.LaunchSpec{}, fmt.Errorf("session %q not registered", req.SessionID)
	}
	mode := ipc.LaunchModeFresh
	if record.ActiveThreadID != nil && record.ResumeAllowed {
		mode = ipc.LaunchModeResume
	}
	sessionToken, err := s.gateway.IssueSessionToken(req.SessionID)
	if err != nil {
		return ipc.LaunchSpec{}, err
	}
	return ipc.LaunchSpec{
		SessionID:    req.SessionID,
		ProfileID:    *state.SelectedProfileID,
		AuthEpochID:  state.CurrentAuthEpochID,
		GatewayURL:   s.gateway.URL(),
		TokenEnvName: codex.RemoteAuthTokenEnv,
		Token:        sessionToken,
		ThreadID:     record.ActiveThreadID,
		Mode:         mode,
		SelectedCwd:  selectedCwd,
		Settings: ipc.WrapperSettings{
			ClearTerminalBeforeLaunch: state.Settings.ClearTerminalEnabled(),
		},
	}, nil
}

func (s *Service) updateSettings(req ipc.UpdateSettingsRequest) error {
	state, err := s.store.LoadState()
	if err != nil {
		return err
	}
	value := req.ClearTerminalBeforeLaunch
	state.Settings.ClearTerminalBeforeLaunch = &value
	state.UpdatedAt = time.Now()
	return s.store.SaveState(state)
}

func (s *Service) returnHome(sessionID string) error {
	if err := s.sessions.ReturnHome(sessionID, time.Now()); err != nil {
		return err
	}
	s.maybeScheduleIdleShutdown()
	return s.reconcilePendingSwitch(context.Background(), "", "session_returned_home")
}

func (s *Service) unregisterSession(sessionID string) error {
	if err := s.sessions.Unregister(sessionID, time.Now()); err != nil {
		return err
	}
	s.gateway.RevokeSessionTokens(sessionID)
	s.maybeScheduleIdleShutdown()
	return s.reconcilePendingSwitch(context.Background(), "", "session_unregistered")
}

func (s *Service) updateSessionState(req ipc.UpdateSessionStateRequest) error {
	if err := s.sessions.UpdateState(req.SessionID, req.State, req.CodexChildPID, time.Now()); err != nil {
		return err
	}
	s.maybeScheduleIdleShutdown()
	return s.reconcilePendingSwitch(context.Background(), "", "session_state_updated")
}

func (s *Service) statusSnapshot() (ipc.StatusSnapshot, error) {
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.StatusSnapshot{}, err
	}
	count, err := s.sessions.SessionCount()
	if err != nil {
		return ipc.StatusSnapshot{}, err
	}
	return ipc.StatusSnapshot{
		BrokerState:       brokerState.BrokerState,
		ActiveProfileID:   brokerState.ActiveProfileID,
		ActiveAuthEpochID: brokerState.ActiveAuthEpochID,
		SessionCount:      count,
		ServerState:       brokerState.Server.State,
		ServerURL:         brokerState.Server.ListenURL,
		UpdatedAt:         brokerState.UpdatedAt,
	}, nil
}

func (s *Service) pendingSwitchSummary(brokerState *model.BrokerFile, sessionID string) *ipc.PendingSwitch {
	if brokerState == nil || !brokerState.SwitchContext.InProgress || brokerState.SwitchContext.ToProfileID == nil {
		return nil
	}
	toProfileName := ""
	if profile, err := s.store.LoadProfile(*brokerState.SwitchContext.ToProfileID); err == nil {
		toProfileName = profile.Name
	}
	readiness, _ := s.sessions.Readiness()
	return &ipc.PendingSwitch{
		FromProfileID:             brokerState.SwitchContext.FromProfileID,
		ToProfileID:               brokerState.SwitchContext.ToProfileID,
		ToProfileName:             toProfileName,
		InitiatedByCurrentSession: brokerState.SwitchContext.InitiatedBySessionID != nil && sessionID != "" && *brokerState.SwitchContext.InitiatedBySessionID == sessionID,
		InitiatedAt:               brokerState.SwitchContext.InitiatedAt,
		BlockingBusySessionCount:  len(readiness.BusySessionIDs),
		LiveCodexSessionCount:     readiness.LiveCodexSessions,
		CanForce:                  brokerState.SwitchContext.InitiatedBySessionID != nil && sessionID != "" && *brokerState.SwitchContext.InitiatedBySessionID == sessionID,
		CanCancel:                 brokerState.SwitchContext.InitiatedBySessionID != nil && sessionID != "" && *brokerState.SwitchContext.InitiatedBySessionID == sessionID,
	}
}

func (s *Service) broadcastSwitchNotice(excludeConnID string, phase, message string, pending *ipc.PendingSwitch) {
	if s.server != nil {
		s.server.BroadcastExcept(excludeConnID, "switch.notice", ipc.SwitchNotice{
			Phase:         phase,
			Message:       message,
			PendingSwitch: pending,
		})
	}
}

func (s *Service) broadcastReload(authEpochID string, profileID *string, reason string, forced bool, message string) {
	if s.server != nil {
		s.server.Broadcast("reload.notice", ipc.ReloadNotice{
			AuthEpochID: authEpochID,
			ProfileID:   profileID,
			Reason:      reason,
			Forced:      forced,
			Message:     message,
		})
	}
}

func switchPendingMessage(ctx *model.SwitchContext) string {
	if ctx == nil || ctx.ToProfileID == nil {
		return ""
	}
	switch ctx.BlockingBusySessionCount {
	case 0:
		return "Account switch is pending and will commit as soon as CAW rechecks live sessions."
	case 1:
		return "Waiting for 1 active Codex session to become idle before switching accounts."
	default:
		return fmt.Sprintf("Waiting for %d active Codex sessions to become idle before switching accounts.", ctx.BlockingBusySessionCount)
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
	errCh := make(chan error, len(targets))
	sem := make(chan struct{}, maxProfileRefreshJobs)
	done := make(chan struct{}, len(targets))
	for _, profileID := range targets {
		go func(profileID string) {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				done <- struct{}{}
				return
			}
			defer func() {
				<-sem
				done <- struct{}{}
			}()
			if err := s.refreshProfileStatus(ctx, profileID); err != nil {
				errCh <- err
			}
		}(profileID)
	}
	for range targets {
		<-done
	}
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) shouldStartBackgroundRefresh() bool {
	if s.refreshing {
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
	if s.refreshing {
		return
	}
	s.refreshing = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), backgroundRefreshTTL)
		defer cancel()
		if err := s.runProfileRefresh(ctx, false, true); err != nil {
			s.setDegraded(err)
		}
	}()
}

func (s *Service) runProfileRefresh(ctx context.Context, force bool, alreadyMarked bool) error {
	s.lockRefresh()
	defer s.unlockRefresh()
	if !alreadyMarked {
		s.refreshing = true
	}
	defer func() { s.refreshing = false }()
	return s.refreshProfileStatuses(ctx, force)
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
	s.degradedReason = err.Error()
	brokerState, loadErr := s.store.LoadBroker()
	if loadErr == nil {
		brokerState.BrokerState = model.BrokerStateDegraded
		brokerState.UpdatedAt = time.Now()
		_ = s.store.SaveBroker(brokerState)
	}
}

func (s *Service) maybeScheduleIdleShutdown() {
	go func() {
		time.Sleep(idleShutdownDelay)
		count, err := s.sessions.SessionCount()
		if err != nil || count > 0 {
			return
		}
		s.gateway.Close()
		s.runtime.Shutdown()
		if s.server != nil {
			_ = s.server.Close()
		}
	}()
}

func (s *Service) lockSwitch()   { s.switchMu <- struct{}{} }
func (s *Service) unlockSwitch() { <-s.switchMu }

func (s *Service) lockRefresh()   { s.refreshRunMu <- struct{}{} }
func (s *Service) unlockRefresh() { <-s.refreshRunMu }

func (s *Service) OnGatewayConnected(ctx context.Context, sessionID string, connected bool) error {
	s.sessions.SetGatewayConnected(sessionID, connected)
	return s.reconcilePendingSwitch(ctx, "", "gateway_connection_changed")
}

func (s *Service) OnThreadObserved(sessionID, threadID, cwd string) error {
	return s.sessions.RecordThread(sessionID, threadID, cwd, time.Now())
}

func (s *Service) OnTurnStarted(ctx context.Context, sessionID string) error {
	s.sessions.NoteTurnStarted(sessionID)
	return s.reconcilePendingSwitch(ctx, "", "turn_started")
}

func (s *Service) OnTurnCompleted(ctx context.Context, sessionID string) error {
	s.sessions.NoteTurnCompleted(sessionID)
	return s.reconcilePendingSwitch(ctx, "", "turn_completed")
}

func (s *Service) OnThreadStatus(ctx context.Context, sessionID string, status string) error {
	s.sessions.NoteThreadStatus(sessionID, status)
	return s.reconcilePendingSwitch(ctx, "", "thread_status_changed")
}

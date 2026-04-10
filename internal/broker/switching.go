package broker

import (
	"context"
	"fmt"
	"time"

	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
	"github.com/derekurban/codex-auth-wrapper/internal/model"
)

func (s *Service) selectProfile(ctx context.Context, connID string, req ipc.SelectProfileRequest) (ipc.SelectProfileResponse, error) {
	_ = connID
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	state, err := s.store.LoadState()
	if err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	if !s.store.ProfileExists(req.ProfileID) {
		return ipc.SelectProfileResponse{}, fmt.Errorf("profile %q does not exist", req.ProfileID)
	}

	if brokerState.SwitchContext.InProgress {
		if brokerState.SwitchContext.InitiatedBySessionID != nil && *brokerState.SwitchContext.InitiatedBySessionID != req.SessionID {
			return ipc.SelectProfileResponse{}, fmt.Errorf("profile switch is already pending in another CAW window")
		}
		if brokerState.SwitchContext.ToProfileID != nil && *brokerState.SwitchContext.ToProfileID == req.ProfileID {
			return ipc.SelectProfileResponse{
				Outcome:         ipc.ProfileSelectOutcomeNoop,
				ActiveProfileID: state.SelectedProfileID,
				PendingSwitch:   s.pendingSwitchSummary(&brokerState, req.SessionID),
			}, nil
		}
		return s.startPendingSwitch(req.SessionID, state.SelectedProfileID, req.ProfileID, true)
	}

	if state.SelectedProfileID != nil && *state.SelectedProfileID == req.ProfileID {
		return ipc.SelectProfileResponse{
			Outcome:         ipc.ProfileSelectOutcomeNoop,
			ActiveProfileID: state.SelectedProfileID,
		}, nil
	}

	readiness, err := s.pendingSwitchReadiness()
	if err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	if len(readiness.busySessionIDs) > 0 {
		return s.startPendingSwitch(req.SessionID, state.SelectedProfileID, req.ProfileID, false)
	}
	if err := s.commitProfileSwitch(ctx, req.ProfileID, false, "profile_switched"); err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	state, _ = s.store.LoadState()
	return ipc.SelectProfileResponse{
		Outcome:         ipc.ProfileSelectOutcomeSwitched,
		ActiveProfileID: state.SelectedProfileID,
	}, nil
}

func (s *Service) forcePendingSwitch(ctx context.Context, req ipc.ForcePendingSwitchRequest) (ipc.PendingSwitchResponse, error) {
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.PendingSwitchResponse{}, err
	}
	if !brokerState.SwitchContext.InProgress || brokerState.SwitchContext.ToProfileID == nil {
		return ipc.PendingSwitchResponse{}, fmt.Errorf("no pending switch to force")
	}
	if brokerState.SwitchContext.InitiatedBySessionID != nil && *brokerState.SwitchContext.InitiatedBySessionID != req.SessionID {
		return ipc.PendingSwitchResponse{}, fmt.Errorf("only the initiating CAW window can force the pending switch")
	}
	if err := s.commitProfileSwitch(ctx, *brokerState.SwitchContext.ToProfileID, true, "profile_switch_forced"); err != nil {
		return ipc.PendingSwitchResponse{}, err
	}
	return ipc.PendingSwitchResponse{Committed: true}, nil
}

func (s *Service) cancelPendingSwitch(req ipc.CancelPendingSwitchRequest) (ipc.PendingSwitchResponse, error) {
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.PendingSwitchResponse{}, err
	}
	if !brokerState.SwitchContext.InProgress {
		return ipc.PendingSwitchResponse{Cancelled: true}, nil
	}
	if brokerState.SwitchContext.InitiatedBySessionID != nil && *brokerState.SwitchContext.InitiatedBySessionID != req.SessionID {
		return ipc.PendingSwitchResponse{}, fmt.Errorf("only the initiating CAW window can cancel the pending switch")
	}
	brokerState.SwitchContext = model.SwitchContext{}
	brokerState.BrokerState = model.BrokerStateActive
	brokerState.UpdatedAt = time.Now()
	if err := s.store.SaveBroker(brokerState); err != nil {
		return ipc.PendingSwitchResponse{}, err
	}
	s.broadcastSwitchNotice("cancelled", "Pending account switch cancelled.", nil)
	return ipc.PendingSwitchResponse{Cancelled: true}, nil
}

func (s *Service) reconcilePendingSwitch(ctx context.Context, reason string) error {
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return err
	}
	if !brokerState.SwitchContext.InProgress || brokerState.SwitchContext.ToProfileID == nil {
		return nil
	}

	readiness, err := s.pendingSwitchReadiness()
	if err != nil {
		return err
	}
	now := time.Now()
	brokerState.SwitchContext.BlockingBusySessionCount = len(readiness.busySessionIDs)
	brokerState.SwitchContext.LastUpdatedAt = timePtr(now)

	if len(readiness.busySessionIDs) > 0 {
		brokerState.BrokerState = model.BrokerStateSwitchingProfile
		brokerState.UpdatedAt = now
		if err := s.store.SaveBroker(brokerState); err != nil {
			return err
		}
		s.broadcastSwitchNotice("updated", switchPendingMessage(&brokerState.SwitchContext), s.pendingSwitchSummary(&brokerState, ""))
		return nil
	}

	return s.commitProfileSwitch(ctx, *brokerState.SwitchContext.ToProfileID, false, reason)
}

func (s *Service) startPendingSwitch(sessionID string, fromProfileID *string, toProfileID string, updating bool) (ipc.SelectProfileResponse, error) {
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	readiness, err := s.pendingSwitchReadiness()
	if err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	now := time.Now()
	// The selected profile remains the currently active auth context until the
	// switch commits. Home renders the target separately so Enter never launches
	// stale Codex sessions against an auth change that has not happened yet.
	brokerState.BrokerState = model.BrokerStateSwitchingProfile
	brokerState.SwitchContext = model.SwitchContext{
		InProgress:               true,
		FromProfileID:            fromProfileID,
		ToProfileID:              &toProfileID,
		InitiatedBySessionID:     &sessionID,
		InitiatedAt:              &now,
		BlockingBusySessionCount: len(readiness.busySessionIDs),
		LastUpdatedAt:            &now,
	}
	brokerState.UpdatedAt = now
	if err := s.store.SaveBroker(brokerState); err != nil {
		return ipc.SelectProfileResponse{}, err
	}
	outcome := ipc.ProfileSelectOutcomePending
	phase := "pending"
	if updating {
		outcome = ipc.ProfileSelectOutcomeUpdatedPending
		phase = "updated"
	}
	summary := s.pendingSwitchSummary(&brokerState, sessionID)
	s.broadcastSwitchNotice(phase, switchPendingMessage(&brokerState.SwitchContext), summary)
	return ipc.SelectProfileResponse{
		Outcome:         outcome,
		ActiveProfileID: fromProfileID,
		PendingSwitch:   summary,
	}, nil
}

func (s *Service) commitProfileSwitch(ctx context.Context, profileID string, forced bool, reason string) error {
	state, err := s.store.LoadState()
	if err != nil {
		return err
	}
	brokerState, err := s.store.LoadBroker()
	if err != nil {
		return err
	}
	previousProfileID := ""
	if state.SelectedProfileID != nil {
		previousProfileID = *state.SelectedProfileID
	}
	if previousProfileID != "" && previousProfileID != profileID {
		// The stock Codex home is the live auth runtime. Before switching away,
		// persist whatever Codex refreshed there back into the profile vault.
		if err := s.store.CopyRuntimeAuthToProfile(previousProfileID); err != nil {
			return err
		}
	}

	now := time.Now()
	state.SelectedProfileID = &profileID
	state.CurrentAuthEpochID = nextEpochID(state.NextAuthEpochCounter)
	state.NextAuthEpochCounter++
	state.UpdatedAt = now
	if err := s.store.SaveState(state); err != nil {
		return err
	}

	brokerState.BrokerState = model.BrokerStateReloading
	brokerState.SwitchContext = model.SwitchContext{}
	brokerState.UpdatedAt = now
	if err := s.store.SaveBroker(brokerState); err != nil {
		return err
	}
	if err := s.activateProfile(ctx, profileID, reason); err != nil {
		return err
	}

	message := "Account switched. Live Codex sessions are reloading onto the new account."
	if forced {
		message = "Account switch was forced. Live Codex sessions are reloading onto the new account."
	}
	s.broadcastSwitchNotice("committed", message, nil)
	s.broadcastReload(state.CurrentAuthEpochID, state.SelectedProfileID, reason, forced, message)
	return nil
}

func (s *Service) pendingSwitchSummary(brokerState *model.BrokerFile, sessionID string) *ipc.PendingSwitch {
	if brokerState == nil || !brokerState.SwitchContext.InProgress || brokerState.SwitchContext.ToProfileID == nil {
		return nil
	}
	toProfileName := ""
	if profile, err := s.store.LoadProfile(*brokerState.SwitchContext.ToProfileID); err == nil {
		toProfileName = profile.Name
	}
	readiness, _ := s.pendingSwitchReadiness()
	return &ipc.PendingSwitch{
		FromProfileID:             brokerState.SwitchContext.FromProfileID,
		ToProfileID:               brokerState.SwitchContext.ToProfileID,
		ToProfileName:             toProfileName,
		InitiatedByCurrentSession: brokerState.SwitchContext.InitiatedBySessionID != nil && sessionID != "" && *brokerState.SwitchContext.InitiatedBySessionID == sessionID,
		InitiatedAt:               brokerState.SwitchContext.InitiatedAt,
		BlockingBusySessionCount:  len(readiness.busySessionIDs),
		LiveCodexSessionCount:     readiness.liveCodexSessions,
		CanForce:                  brokerState.SwitchContext.InitiatedBySessionID != nil && sessionID != "" && *brokerState.SwitchContext.InitiatedBySessionID == sessionID,
		CanCancel:                 brokerState.SwitchContext.InitiatedBySessionID != nil && sessionID != "" && *brokerState.SwitchContext.InitiatedBySessionID == sessionID,
	}
}

func (s *Service) broadcastSwitchNotice(phase, message string, pending *ipc.PendingSwitch) {
	s.mu.Lock()
	server := s.server
	s.mu.Unlock()
	if server != nil {
		server.Broadcast("switch.notice", ipc.SwitchNotice{
			Phase:         phase,
			Message:       message,
			PendingSwitch: pending,
		})
	}
}

func (s *Service) broadcastReload(authEpochID string, profileID *string, reason string, forced bool, message string) {
	s.mu.Lock()
	server := s.server
	s.mu.Unlock()
	if server != nil {
		server.Broadcast("reload.notice", ipc.ReloadNotice{
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

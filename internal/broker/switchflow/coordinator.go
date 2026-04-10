package switchflow

import (
	"fmt"
	"time"

	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
	"github.com/derekurban/codex-auth-wrapper/internal/model"
)

type Readiness struct {
	BusySessionIDs    []string
	LiveCodexSessions int
}

type SelectDecision struct {
	Outcome         ipc.ProfileSelectOutcome
	ActiveProfileID *string
	Broker          model.BrokerFile
	CommitProfileID *string
}

type PendingDecision struct {
	Broker          model.BrokerFile
	CommitProfileID *string
}

type Coordinator struct{}

func New() *Coordinator {
	return &Coordinator{}
}

func (c *Coordinator) ClearStaleOnStartup(now time.Time, broker model.BrokerFile) model.BrokerFile {
	if !broker.SwitchContext.InProgress {
		return broker
	}
	broker.SwitchContext = model.SwitchContext{}
	if broker.BrokerState == model.BrokerStateSwitchingProfile || broker.BrokerState == model.BrokerStateReloading {
		broker.BrokerState = model.BrokerStateStarting
	}
	broker.UpdatedAt = now
	return broker
}

func (c *Coordinator) RequestSwitch(now time.Time, state model.StateFile, broker model.BrokerFile, sessionID, profileID string, readiness Readiness, profileExists bool) (SelectDecision, error) {
	if !profileExists {
		return SelectDecision{}, fmt.Errorf("profile %q does not exist", profileID)
	}
	if broker.SwitchContext.InProgress {
		if broker.SwitchContext.InitiatedBySessionID != nil && *broker.SwitchContext.InitiatedBySessionID != sessionID {
			return SelectDecision{}, fmt.Errorf("profile switch is already pending in another CAW window")
		}
		if broker.SwitchContext.ToProfileID != nil && *broker.SwitchContext.ToProfileID == profileID {
			return SelectDecision{
				Outcome:         ipc.ProfileSelectOutcomeNoop,
				ActiveProfileID: state.SelectedProfileID,
				Broker:          broker,
			}, nil
		}
		return c.pendingDecision(now, broker, sessionID, state.SelectedProfileID, profileID, readiness, ipc.ProfileSelectOutcomeUpdatedPending), nil
	}
	if state.SelectedProfileID != nil && *state.SelectedProfileID == profileID {
		return SelectDecision{
			Outcome:         ipc.ProfileSelectOutcomeNoop,
			ActiveProfileID: state.SelectedProfileID,
			Broker:          broker,
		}, nil
	}
	if len(readiness.BusySessionIDs) > 0 {
		return c.pendingDecision(now, broker, sessionID, state.SelectedProfileID, profileID, readiness, ipc.ProfileSelectOutcomePending), nil
	}
	return SelectDecision{
		Outcome:         ipc.ProfileSelectOutcomeSwitched,
		ActiveProfileID: state.SelectedProfileID,
		Broker:          broker,
		CommitProfileID: &profileID,
	}, nil
}

func (c *Coordinator) Reconcile(now time.Time, broker model.BrokerFile, readiness Readiness) PendingDecision {
	if !broker.SwitchContext.InProgress || broker.SwitchContext.ToProfileID == nil {
		return PendingDecision{Broker: broker}
	}
	broker.SwitchContext.BlockingBusySessionCount = len(readiness.BusySessionIDs)
	broker.SwitchContext.LastUpdatedAt = &now
	broker.UpdatedAt = now
	if len(readiness.BusySessionIDs) > 0 {
		broker.BrokerState = model.BrokerStateSwitchingProfile
		return PendingDecision{Broker: broker}
	}
	return PendingDecision{
		Broker:          broker,
		CommitProfileID: broker.SwitchContext.ToProfileID,
	}
}

func (c *Coordinator) Force(broker model.BrokerFile, sessionID string) (PendingDecision, error) {
	if !broker.SwitchContext.InProgress || broker.SwitchContext.ToProfileID == nil {
		return PendingDecision{}, fmt.Errorf("no pending switch to force")
	}
	if broker.SwitchContext.InitiatedBySessionID != nil && *broker.SwitchContext.InitiatedBySessionID != sessionID {
		return PendingDecision{}, fmt.Errorf("only the initiating CAW window can force the pending switch")
	}
	return PendingDecision{
		Broker:          broker,
		CommitProfileID: broker.SwitchContext.ToProfileID,
	}, nil
}

func (c *Coordinator) Cancel(now time.Time, broker model.BrokerFile, sessionID string) (PendingDecision, error) {
	if !broker.SwitchContext.InProgress {
		return PendingDecision{Broker: broker}, nil
	}
	if broker.SwitchContext.InitiatedBySessionID != nil && *broker.SwitchContext.InitiatedBySessionID != sessionID {
		return PendingDecision{}, fmt.Errorf("only the initiating CAW window can cancel the pending switch")
	}
	broker.SwitchContext = model.SwitchContext{}
	broker.BrokerState = model.BrokerStateActive
	broker.UpdatedAt = now
	return PendingDecision{Broker: broker}, nil
}

func (c *Coordinator) pendingDecision(now time.Time, broker model.BrokerFile, sessionID string, fromProfileID *string, toProfileID string, readiness Readiness, outcome ipc.ProfileSelectOutcome) SelectDecision {
	broker.BrokerState = model.BrokerStateSwitchingProfile
	broker.SwitchContext = model.SwitchContext{
		InProgress:               true,
		FromProfileID:            fromProfileID,
		ToProfileID:              &toProfileID,
		InitiatedBySessionID:     &sessionID,
		InitiatedAt:              &now,
		BlockingBusySessionCount: len(readiness.BusySessionIDs),
		LastUpdatedAt:            &now,
	}
	broker.UpdatedAt = now
	return SelectDecision{
		Outcome:         outcome,
		ActiveProfileID: fromProfileID,
		Broker:          broker,
	}
}

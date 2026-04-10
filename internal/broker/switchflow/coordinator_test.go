package switchflow

import (
	"testing"
	"time"

	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
	"github.com/derekurban/codex-auth-wrapper/internal/model"
)

func TestRequestSwitchCreatesPendingWhenBusySessionsExist(t *testing.T) {
	now := time.Now()
	selected := "personal"
	state := model.NewInitialState(now)
	state.SelectedProfileID = &selected
	broker := model.NewInitialBroker(now)

	decision, err := New().RequestSwitch(now, state, broker, "sess-1", "work", Readiness{
		BusySessionIDs:    []string{"sess-2"},
		LiveCodexSessions: 1,
	}, true)
	if err != nil {
		t.Fatalf("request switch: %v", err)
	}
	if decision.Outcome != ipc.ProfileSelectOutcomePending {
		t.Fatalf("expected pending outcome, got %s", decision.Outcome)
	}
	if !decision.Broker.SwitchContext.InProgress {
		t.Fatal("expected pending switch context")
	}
	if decision.CommitProfileID != nil {
		t.Fatal("did not expect immediate commit target")
	}
}

func TestRequestSwitchCommitsImmediatelyWhenIdle(t *testing.T) {
	now := time.Now()
	selected := "personal"
	state := model.NewInitialState(now)
	state.SelectedProfileID = &selected
	broker := model.NewInitialBroker(now)

	decision, err := New().RequestSwitch(now, state, broker, "sess-1", "work", Readiness{}, true)
	if err != nil {
		t.Fatalf("request switch: %v", err)
	}
	if decision.Outcome != ipc.ProfileSelectOutcomeSwitched {
		t.Fatalf("expected switched outcome, got %s", decision.Outcome)
	}
	if decision.CommitProfileID == nil || *decision.CommitProfileID != "work" {
		t.Fatalf("unexpected commit target: %#v", decision.CommitProfileID)
	}
}

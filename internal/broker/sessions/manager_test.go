package sessions

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/derekurban/codex-auth-wrapper/internal/model"
	"github.com/derekurban/codex-auth-wrapper/internal/store"
)

func TestManagerResetClearsPersistedSessions(t *testing.T) {
	paths := testPaths(t)
	st := store.New(paths)
	now := time.Now()
	if err := st.EnsureLayout(now); err != nil {
		t.Fatalf("ensure layout: %v", err)
	}
	sessionsFile := model.NewInitialSessions(now)
	sessionsFile.Sessions["sess-1"] = model.SessionRecord{SessionID: "sess-1", State: model.SessionStateInCodex, CreatedAt: now, UpdatedAt: now}
	if err := st.SaveSessions(sessionsFile); err != nil {
		t.Fatalf("save sessions: %v", err)
	}

	manager := New(st)
	if err := manager.Reset(now.Add(time.Second)); err != nil {
		t.Fatalf("reset: %v", err)
	}

	saved, err := st.LoadSessions()
	if err != nil {
		t.Fatalf("load sessions: %v", err)
	}
	if len(saved.Sessions) != 0 {
		t.Fatalf("expected reset sessions to be empty, got %d", len(saved.Sessions))
	}
}

func TestManagerReadinessCountsOnlyLiveBusyCodexSessions(t *testing.T) {
	paths := testPaths(t)
	st := store.New(paths)
	now := time.Now()
	if err := st.EnsureLayout(now); err != nil {
		t.Fatalf("ensure layout: %v", err)
	}
	manager := New(st)
	if err := manager.RegisterHost("sess-a", "conn-a", "D:\\Working\\A", now); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := manager.RegisterHost("sess-b", "conn-b", "D:\\Working\\B", now); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := manager.UpdateState("sess-a", model.SessionStateInCodex, nil, now); err != nil {
		t.Fatalf("update a: %v", err)
	}
	if err := manager.UpdateState("sess-b", model.SessionStateHome, nil, now); err != nil {
		t.Fatalf("update b: %v", err)
	}
	manager.SetGatewayConnected("sess-a", true)
	manager.NoteTurnStarted("sess-a")

	readiness, err := manager.Readiness()
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if readiness.LiveCodexSessions != 1 {
		t.Fatalf("expected 1 live codex session, got %d", readiness.LiveCodexSessions)
	}
	if len(readiness.BusySessionIDs) != 1 || readiness.BusySessionIDs[0] != "sess-a" {
		t.Fatalf("unexpected busy sessions: %#v", readiness.BusySessionIDs)
	}
}

func testPaths(t *testing.T) store.Paths {
	t.Helper()
	root := t.TempDir()
	codexHome := filepath.Join(root, ".codex")
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatalf("mkdir codex home: %v", err)
	}
	return store.NewPaths(filepath.Join(root, ".codex-auth-wrapper"), codexHome)
}

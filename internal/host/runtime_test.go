package host

import (
	"testing"

	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
)

func TestReturnHomeMessagePrefersResumeCopy(t *testing.T) {
	threadID := "thread_123"
	msg := returnHomeMessage(ipc.LaunchSpec{
		Mode:     ipc.LaunchModeResume,
		ThreadID: &threadID,
	})
	if msg == "" || msg == "Returned from Codex." {
		t.Fatalf("expected resume-oriented return message, got %q", msg)
	}
}

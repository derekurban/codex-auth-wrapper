package gateway

import (
	"encoding/json"
	"testing"
)

func TestRewriteClientMessageInjectsSessionScopedCWD(t *testing.T) {
	server := New(
		func(sessionID string) (string, string, bool) { return "", "", false },
		func(sessionID string) (string, bool) { return `D:\Working\proj-b`, true },
		nil,
	)
	input := map[string]any{
		"method": "thread/list",
		"params": map[string]any{
			"cwd": `D:\Working\proj-a`,
		},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	out := server.rewriteClientMessage("sess-1", raw)
	var msg map[string]any
	if err := json.Unmarshal(out, &msg); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	params := msg["params"].(map[string]any)
	if got := params["cwd"]; got != `D:\Working\proj-b` {
		t.Fatalf("expected rewritten cwd, got %#v", got)
	}
}

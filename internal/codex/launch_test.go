package codex

import (
	"strings"
	"testing"

	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
)

func TestBuildRemoteCommandResumeIncludesCwdAndRemoteFlags(t *testing.T) {
	threadID := "thread-123"
	spec := BuildRemoteCommand(ipc.LaunchSpec{
		Mode:         ipc.LaunchModeResume,
		ThreadID:     &threadID,
		SelectedCwd:  `D:\Working\repo`,
		GatewayURL:   "ws://127.0.0.1:1234",
		TokenEnvName: RemoteAuthTokenEnv,
		Token:        "token-abc",
	}, `C:\Users\derek\.codex`)

	if spec.Path != "codex" {
		t.Fatalf("expected codex path, got %q", spec.Path)
	}
	args := strings.Join(spec.Args, " ")
	for _, fragment := range []string{"resume thread-123", "-C D:\\Working\\repo", "--remote ws://127.0.0.1:1234", "--remote-auth-token-env " + RemoteAuthTokenEnv} {
		if !strings.Contains(args, fragment) {
			t.Fatalf("expected args to contain %q, got %q", fragment, args)
		}
	}
	if spec.Dir != `D:\Working\repo` {
		t.Fatalf("expected launch dir to be selected cwd, got %q", spec.Dir)
	}
	if !containsEnv(spec.Env, "CODEX_HOME=C:\\Users\\derek\\.codex") {
		t.Fatalf("expected CODEX_HOME env to be present")
	}
	if !containsEnv(spec.Env, RemoteAuthTokenEnv+"=token-abc") {
		t.Fatalf("expected remote auth env to be present")
	}
}

func containsEnv(env []string, expected string) bool {
	for _, entry := range env {
		if entry == expected {
			return true
		}
	}
	return false
}

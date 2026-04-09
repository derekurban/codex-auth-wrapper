package codex

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
)

const RemoteAuthTokenEnv = "CAW_REMOTE_AUTH_TOKEN"

func Slugify(input string) string {
	trimmed := strings.TrimSpace(strings.ToLower(input))
	if trimmed == "" {
		return ""
	}
	re := regexp.MustCompile(`[^a-z0-9]+`)
	slug := re.ReplaceAllString(trimmed, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "profile"
	}
	return slug
}

func RunLogin(tempCodexHome string) error {
	if err := os.MkdirAll(tempCodexHome, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tempCodexHome, "config.toml"), []byte("cli_auth_credentials_store = \"file\"\n"), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("codex", "login", "-c", "cli_auth_credentials_store=file")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+tempCodexHome)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func LaunchRemote(spec ipc.LaunchSpec, codexHome string) error {
	cmd, err := StartRemote(spec, codexHome)
	if err != nil {
		return err
	}
	return cmd.Wait()
}

func StartRemote(spec ipc.LaunchSpec, codexHome string) (*exec.Cmd, error) {
	args := []string{}
	if spec.Mode == ipc.LaunchModeResume && spec.ThreadID != nil && *spec.ThreadID != "" {
		args = append(args, "resume", *spec.ThreadID)
	}
	if spec.SelectedCwd != "" {
		args = append(args, "-C", spec.SelectedCwd)
	}
	args = append(args,
		"--remote", spec.GatewayURL,
		"--remote-auth-token-env", spec.TokenEnvName,
	)
	cmd := exec.Command("codex", args...)
	cmd.Env = append(os.Environ(),
		"CODEX_HOME="+codexHome,
		fmt.Sprintf("%s=%s", spec.TokenEnvName, spec.Token),
	)
	if spec.SelectedCwd != "" {
		cmd.Dir = spec.SelectedCwd
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

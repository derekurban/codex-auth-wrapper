package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/derekurban/codex-auth-wrapper/internal/model"
	"github.com/derekurban/codex-auth-wrapper/internal/store"
)

type Controller struct {
	paths store.Paths
	store *store.Store

	mu             sync.Mutex
	appServerCmd   *exec.Cmd
	appServerURL   string
	appServerToken string
	control        *codex.AppServerClient
}

type CommitResult struct {
	ProfileID   string
	AuthEpochID string
	Forced      bool
}

func New(paths store.Paths, st *store.Store) *Controller {
	return &Controller{paths: paths, store: st}
}

// ReconcileStartup resets runtime-facing broker state to the persisted selected
// profile. Stale pending switches do not survive a new broker lifetime.
func (c *Controller) ReconcileStartup(ctx context.Context) error {
	state, err := c.store.LoadState()
	if err != nil {
		return err
	}
	brokerState, err := c.store.LoadBroker()
	if err != nil {
		return err
	}
	if state.SelectedProfileID == nil {
		brokerState.BrokerState = model.BrokerStateHomeReady
		brokerState.ActiveProfileID = nil
		brokerState.Server.State = model.ServerStateStopped
		brokerState.Server.ListenURL = nil
		brokerState.Server.AuthMode = nil
		brokerState.SwitchContext = model.SwitchContext{}
		brokerState.UpdatedAt = time.Now()
		return c.store.SaveBroker(brokerState)
	}
	return c.EnsureActiveProfile(ctx, *state.SelectedProfileID, "startup")
}

func (c *Controller) EnsureActiveProfile(ctx context.Context, profileID string, reason string) error {
	if c.canReuseActiveProfile(profileID) {
		return c.markActiveProfile(profileID, "")
	}
	if err := c.store.CopyProfileAuthToRuntime(profileID); err != nil {
		return err
	}
	c.Shutdown()
	if err := c.startAppServer(ctx, reason); err != nil {
		return err
	}
	return c.markActiveProfile(profileID, reason)
}

func (c *Controller) CommitProfileSwitch(ctx context.Context, profileID string, forced bool, reason string) (CommitResult, error) {
	state, err := c.store.LoadState()
	if err != nil {
		return CommitResult{}, err
	}
	brokerState, err := c.store.LoadBroker()
	if err != nil {
		return CommitResult{}, err
	}
	previousProfileID := ""
	if state.SelectedProfileID != nil {
		previousProfileID = *state.SelectedProfileID
	}
	if previousProfileID != "" && previousProfileID != profileID {
		if err := c.store.CopyRuntimeAuthToProfile(previousProfileID); err != nil {
			return CommitResult{}, err
		}
	}
	now := time.Now()
	state.SelectedProfileID = &profileID
	state.CurrentAuthEpochID = nextEpochID(state.NextAuthEpochCounter)
	state.NextAuthEpochCounter++
	state.UpdatedAt = now
	if err := c.store.SaveState(state); err != nil {
		return CommitResult{}, err
	}

	brokerState.BrokerState = model.BrokerStateReloading
	brokerState.SwitchContext = model.SwitchContext{}
	brokerState.UpdatedAt = now
	if err := c.store.SaveBroker(brokerState); err != nil {
		return CommitResult{}, err
	}
	if err := c.EnsureActiveProfile(ctx, profileID, reason); err != nil {
		return CommitResult{}, err
	}
	return CommitResult{
		ProfileID:   profileID,
		AuthEpochID: state.CurrentAuthEpochID,
		Forced:      forced,
	}, nil
}

func (c *Controller) Backend() (string, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.appServerURL == "" || c.appServerToken == "" {
		return "", "", false
	}
	return c.appServerURL, c.appServerToken, true
}

func (c *Controller) Shutdown() {
	c.mu.Lock()
	cmd := c.appServerCmd
	control := c.control
	c.appServerCmd = nil
	c.control = nil
	c.appServerURL = ""
	c.appServerToken = ""
	c.mu.Unlock()
	if control != nil {
		_ = control.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	if brokerState, err := c.store.LoadBroker(); err == nil {
		brokerState.Server.State = model.ServerStateStopped
		brokerState.Server.ListenURL = nil
		brokerState.Server.AuthMode = nil
		brokerState.UpdatedAt = time.Now()
		_ = c.store.SaveBroker(brokerState)
	}
}

func (c *Controller) canReuseActiveProfile(profileID string) bool {
	brokerState, err := c.store.LoadBroker()
	if err != nil {
		return false
	}
	if brokerState.ActiveProfileID == nil || *brokerState.ActiveProfileID != profileID || brokerState.Server.State != model.ServerStateHealthy {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.appServerCmd != nil && c.control != nil && c.appServerURL != ""
}

func (c *Controller) markActiveProfile(profileID string, reason string) error {
	state, err := c.store.LoadState()
	if err != nil {
		return err
	}
	brokerState, err := c.store.LoadBroker()
	if err != nil {
		return err
	}
	now := time.Now()
	brokerState.BrokerState = model.BrokerStateActive
	brokerState.ActiveProfileID = &profileID
	brokerState.ActiveAuthEpochID = state.CurrentAuthEpochID
	brokerState.Server.State = model.ServerStateHealthy
	c.mu.Lock()
	if c.appServerURL != "" {
		brokerState.Server.ListenURL = &c.appServerURL
	}
	c.mu.Unlock()
	authMode := "capability-token"
	brokerState.Server.AuthMode = &authMode
	if reason != "" {
		brokerState.Server.StartedAt = &now
		brokerState.Server.LastRestartReason = &reason
	}
	brokerState.SwitchContext = model.SwitchContext{}
	brokerState.UpdatedAt = now
	if err := c.store.SaveBroker(brokerState); err != nil {
		return err
	}
	profile, err := c.store.LoadProfile(profileID)
	if err == nil {
		profile.LastSelectedAt = &now
		profile.UpdatedAt = now
		_ = c.store.SaveProfile(profile)
	}
	return nil
}

func (c *Controller) startAppServer(ctx context.Context, reason string) error {
	port, err := allocateLoopbackPort()
	if err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	if err := os.WriteFile(c.paths.AppServerTokenFile, []byte(token), 0o600); err != nil {
		return err
	}
	listenURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	cmd := exec.Command("codex", "app-server",
		"--listen", listenURL,
		"--ws-auth", "capability-token",
		"--ws-token-file", c.paths.AppServerTokenFile,
		"-c", "cli_auth_credentials_store=file",
	)
	cmd.Env = append(os.Environ(),
		"CODEX_HOME="+c.paths.CodexHome,
		"LOG_FORMAT=json",
	)
	logFile := filepath.Join(c.paths.LogsDir, "broker-app-server.log")
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
	c.mu.Lock()
	c.appServerCmd = cmd
	c.appServerURL = listenURL
	c.appServerToken = token
	c.mu.Unlock()
	go func(cmd *exec.Cmd, f *os.File) {
		_ = cmd.Wait()
		_ = f.Close()
		c.mu.Lock()
		if c.appServerCmd == cmd {
			c.appServerCmd = nil
		}
		c.mu.Unlock()
	}(cmd, logHandle)
	if err := waitForReady(ctx, listenURL, 8*time.Second); err != nil {
		c.Shutdown()
		return err
	}
	control, err := codex.DialAppServer(ctx, listenURL, token)
	if err != nil {
		c.Shutdown()
		return err
	}
	c.mu.Lock()
	if c.control != nil {
		_ = c.control.Close()
	}
	c.control = control
	c.mu.Unlock()
	if brokerState, err := c.store.LoadBroker(); err == nil {
		brokerState.Server.State = model.ServerStateHealthy
		brokerState.UpdatedAt = time.Now()
		reasonCopy := reason
		brokerState.Server.LastRestartReason = &reasonCopy
		listenCopy := listenURL
		brokerState.Server.ListenURL = &listenCopy
		_ = c.store.SaveBroker(brokerState)
	}
	return nil
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

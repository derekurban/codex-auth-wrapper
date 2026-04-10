package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/derekurban/codex-auth-wrapper/internal/codex"
	"github.com/derekurban/codex-auth-wrapper/internal/homeui"
	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
	"github.com/derekurban/codex-auth-wrapper/internal/model"
	"github.com/derekurban/codex-auth-wrapper/internal/store"
)

type SignalBuffer struct {
	mu           sync.Mutex
	reload       *ipc.ReloadNotice
	switchNotice *ipc.SwitchNotice
}

func (s *SignalBuffer) HandleEvent(name string, payload json.RawMessage) {
	switch name {
	case "reload.notice":
		var notice ipc.ReloadNotice
		if json.Unmarshal(payload, &notice) == nil {
			s.setReload(notice)
		}
	case "switch.notice":
		var notice ipc.SwitchNotice
		if json.Unmarshal(payload, &notice) == nil {
			s.setSwitchNotice(notice)
		}
	}
}

func (s *SignalBuffer) TakeHomeEvent() *homeui.ExternalEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.switchNotice != nil {
		event := &homeui.ExternalEvent{Switch: s.switchNotice}
		s.switchNotice = nil
		return event
	}
	if s.reload == nil {
		return nil
	}
	event := &homeui.ExternalEvent{Reload: s.reload}
	s.reload = nil
	return event
}

func (s *SignalBuffer) TakeReload() *ipc.ReloadNotice {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reload == nil {
		return nil
	}
	notice := s.reload
	s.reload = nil
	return notice
}

func (s *SignalBuffer) setReload(notice ipc.ReloadNotice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := notice
	s.reload = &copy
}

func (s *SignalBuffer) setSwitchNotice(notice ipc.SwitchNotice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := notice
	s.switchNotice = &copy
}

// SessionRuntime owns one visible terminal's Codex child lifecycle. It is the
// only place that decides whether a child exit returns Home or relaunches under
// a newer auth epoch.
type SessionRuntime struct {
	client   *ipc.Client
	paths    store.Paths
	signals  *SignalBuffer
	session  string
	cwd      string
	clearTTY func()
}

func NewSessionRuntime(client *ipc.Client, paths store.Paths, signals *SignalBuffer, sessionID string, cwd string, clearTTY func()) *SessionRuntime {
	return &SessionRuntime{
		client:   client,
		paths:    paths,
		signals:  signals,
		session:  sessionID,
		cwd:      cwd,
		clearTTY: clearTTY,
	}
}

func (r *SessionRuntime) EnterCodex() (string, error) {
	var reloadNotice *ipc.ReloadNotice
launchLoop:
	for {
		reloadMessage := ""
		if reloadNotice != nil {
			reloadMessage = reloadNotice.Message
			reloadNotice = nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		var spec ipc.LaunchSpec
		err := r.client.Request(ctx, "launch.prepare", ipc.PrepareLaunchRequest{
			SessionID: r.session,
			Cwd:       r.cwd,
		}, &spec)
		cancel()
		if err != nil {
			return "", err
		}
		if err := r.client.Request(context.Background(), "session.update_state", ipc.UpdateSessionStateRequest{
			SessionID: r.session,
			State:     model.SessionStateInCodex,
		}, nil); err != nil {
			return "", err
		}
		if spec.Settings.ClearTerminalBeforeLaunch && r.clearTTY != nil {
			r.clearTTY()
		}
		fmt.Println()
		if reloadMessage != "" {
			fmt.Println(reloadMessage)
			fmt.Println()
		}
		if spec.Mode == ipc.LaunchModeResume && spec.ThreadID != nil && *spec.ThreadID != "" {
			fmt.Printf("Resuming stock Codex thread %s.\n", *spec.ThreadID)
		} else {
			fmt.Println("Launching stock Codex connected to the shared wrapper-managed app-server.")
		}
		fmt.Println("Exit Codex normally to return to the wrapper home. F12 interception is not wired yet in this build.")
		fmt.Println()

		cmd, err := codex.StartRemote(spec, r.paths.CodexHome)
		if err != nil {
			return "", err
		}
		waitCh := make(chan error, 1)
		go func() {
			waitCh <- cmd.Wait()
		}()
		for {
			select {
			case err := <-waitCh:
				if nextReload, reload := r.reloadAfterExit(spec, reloadNotice); reload {
					reloadNotice = nextReload
					continue launchLoop
				}
				homeCtx, homeCancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = r.client.Request(homeCtx, "session.return_home", ipc.ReturnHomeRequest{SessionID: r.session}, nil)
				homeCancel()
				if err != nil {
					var exitErr *exec.ExitError
					if errors.As(err, &exitErr) {
						return returnHomeMessage(spec), nil
					}
					return "", err
				}
				return returnHomeMessage(spec), nil
			default:
				if notice := r.signals.TakeReload(); notice != nil && isNewerAuthEpoch(notice.AuthEpochID, spec.AuthEpochID) {
					reloadNotice = notice
					_ = r.client.Request(context.Background(), "session.update_state", ipc.UpdateSessionStateRequest{
						SessionID: r.session,
						State:     model.SessionStateReloading,
					}, nil)
				}
				time.Sleep(150 * time.Millisecond)
			}
		}
	}
}

func (r *SessionRuntime) reloadAfterExit(spec ipc.LaunchSpec, current *ipc.ReloadNotice) (*ipc.ReloadNotice, bool) {
	if current != nil && isNewerAuthEpoch(current.AuthEpochID, spec.AuthEpochID) {
		return current, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var snapshot ipc.StatusSnapshot
	if err := r.client.Request(ctx, "status.snapshot", ipc.Empty{}, &snapshot); err != nil {
		return nil, false
	}
	if isNewerAuthEpoch(snapshot.ActiveAuthEpochID, spec.AuthEpochID) {
		return &ipc.ReloadNotice{
			AuthEpochID: snapshot.ActiveAuthEpochID,
			ProfileID:   snapshot.ActiveProfileID,
			Reason:      "profile_switched",
			Message:     "Account switched. Reloading this Codex session onto the new account.",
		}, true
	}
	return nil, false
}

func returnHomeMessage(spec ipc.LaunchSpec) string {
	if spec.Mode == ipc.LaunchModeResume && spec.ThreadID != nil && *spec.ThreadID != "" {
		return fmt.Sprintf("Returned from Codex. Enter resumes thread %s.", *spec.ThreadID)
	}
	return "Returned from Codex."
}

func isNewerAuthEpoch(candidate string, current string) bool {
	return candidate > current
}

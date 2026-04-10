package broker

import (
	"context"
	"time"

	"github.com/derekurban/codex-auth-wrapper/internal/model"
)

// sessionRuntime tracks live host/gateway state that should not survive process
// restarts. Persistent session records remain the source of truth for resumable
// thread metadata; this runtime layer only answers "is this live session busy?"
// so the broker can defer global auth switches safely.
type sessionRuntime struct {
	HostConnID       string
	HostConnected    bool
	GatewayConnected bool
	ActiveTurns      int
	LastThreadStatus string
}

func (s *Service) bindHostConnection(sessionID, connID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.connSessions == nil {
		s.connSessions = map[string]string{}
	}
	if s.sessionRuntime == nil {
		s.sessionRuntime = map[string]*sessionRuntime{}
	}
	runtime := s.ensureSessionRuntimeLocked(sessionID)
	runtime.HostConnID = connID
	runtime.HostConnected = true
	s.connSessions[connID] = sessionID
}

func (s *Service) unregisterRuntimeSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteRuntimeSessionLocked(sessionID)
}

func (s *Service) handleConnectionClosed(ctx context.Context, connID string) error {
	s.mu.Lock()
	sessionID, ok := s.connSessions[connID]
	if ok {
		delete(s.connSessions, connID)
	}
	if ok {
		if runtime := s.sessionRuntime[sessionID]; runtime != nil && runtime.HostConnID == connID {
			runtime.HostConnected = false
			runtime.HostConnID = ""
			if !runtime.GatewayConnected {
				delete(s.sessionRuntime, sessionID)
			}
		}
	}
	s.mu.Unlock()
	if ok {
		return s.reconcilePendingSwitch(ctx, "host_disconnected")
	}
	return nil
}

func (s *Service) setGatewayConnected(ctx context.Context, sessionID string, connected bool) error {
	s.mu.Lock()
	if s.sessionRuntime == nil {
		s.sessionRuntime = map[string]*sessionRuntime{}
	}
	runtime := s.ensureSessionRuntimeLocked(sessionID)
	runtime.GatewayConnected = connected
	if !connected {
		runtime.ActiveTurns = 0
		runtime.LastThreadStatus = ""
		if !runtime.HostConnected {
			delete(s.sessionRuntime, sessionID)
		}
	}
	s.mu.Unlock()
	return s.reconcilePendingSwitch(ctx, "gateway_connection_changed")
}

func (s *Service) noteTurnStarted(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	runtime := s.ensureSessionRuntimeLocked(sessionID)
	runtime.ActiveTurns++
	runtime.LastThreadStatus = "active"
	s.mu.Unlock()
	return s.reconcilePendingSwitch(ctx, "turn_started")
}

func (s *Service) noteTurnCompleted(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	runtime := s.ensureSessionRuntimeLocked(sessionID)
	if runtime.ActiveTurns > 0 {
		runtime.ActiveTurns--
	}
	if runtime.ActiveTurns == 0 {
		runtime.LastThreadStatus = "idle"
	}
	s.mu.Unlock()
	return s.reconcilePendingSwitch(ctx, "turn_completed")
}

func (s *Service) noteThreadStatus(ctx context.Context, sessionID string, status string) error {
	s.mu.Lock()
	runtime := s.ensureSessionRuntimeLocked(sessionID)
	runtime.LastThreadStatus = status
	if status == "active" && runtime.ActiveTurns == 0 {
		runtime.ActiveTurns = 1
	}
	if status != "active" && runtime.ActiveTurns == 0 {
		runtime.LastThreadStatus = status
	}
	s.mu.Unlock()
	return s.reconcilePendingSwitch(ctx, "thread_status_changed")
}

func (s *Service) ensureSessionRuntimeLocked(sessionID string) *sessionRuntime {
	if s.sessionRuntime == nil {
		s.sessionRuntime = map[string]*sessionRuntime{}
	}
	runtime := s.sessionRuntime[sessionID]
	if runtime == nil {
		runtime = &sessionRuntime{}
		s.sessionRuntime[sessionID] = runtime
	}
	return runtime
}

func (s *Service) deleteRuntimeSessionLocked(sessionID string) {
	runtime := s.sessionRuntime[sessionID]
	if runtime == nil {
		return
	}
	if runtime.HostConnID != "" {
		delete(s.connSessions, runtime.HostConnID)
	}
	delete(s.sessionRuntime, sessionID)
}

type switchReadiness struct {
	busySessionIDs    []string
	liveCodexSessions int
}

func (s *Service) pendingSwitchReadiness() (switchReadiness, error) {
	sessions, err := s.store.LoadSessions()
	if err != nil {
		return switchReadiness{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	readiness := switchReadiness{}
	for sessionID, record := range sessions.Sessions {
		runtime := s.sessionRuntime[sessionID]
		if runtime == nil || !runtime.HostConnected || record.State != model.SessionStateInCodex {
			continue
		}
		readiness.liveCodexSessions++
		if runtime.GatewayConnected && runtime.ActiveTurns > 0 {
			readiness.busySessionIDs = append(readiness.busySessionIDs, sessionID)
		}
	}
	return readiness, nil
}

func timePtr(t time.Time) *time.Time {
	return &t
}

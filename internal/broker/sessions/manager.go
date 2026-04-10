package sessions

import (
	"sync"
	"time"

	"github.com/derekurban/codex-auth-wrapper/internal/model"
	"github.com/derekurban/codex-auth-wrapper/internal/store"
)

// RuntimeSession is the broker's in-memory truth about one currently live CAW
// window. It complements, but does not replace, the persisted session mirror.
type RuntimeSession struct {
	HostConnID       string
	HostConnected    bool
	GatewayConnected bool
	ActiveTurns      int
	LastThreadStatus string
}

// Readiness answers whether a pending global auth switch may commit safely.
type Readiness struct {
	BusySessionIDs    []string
	LiveCodexSessions int
}

// Manager owns live wrapper-session state and the mirrored session records on
// disk. New broker lifetimes start with an empty live-session set; persisted
// sessions are only a mirror of currently connected windows.
type Manager struct {
	store        *store.Store
	mu           sync.Mutex
	connSessions map[string]string
	runtime      map[string]*RuntimeSession
}

func New(st *store.Store) *Manager {
	return &Manager{
		store:        st,
		connSessions: map[string]string{},
		runtime:      map[string]*RuntimeSession{},
	}
}

func (m *Manager) Reset(now time.Time) error {
	m.mu.Lock()
	m.connSessions = map[string]string{}
	m.runtime = map[string]*RuntimeSession{}
	m.mu.Unlock()
	return m.store.SaveSessions(model.NewInitialSessions(now))
}

func (m *Manager) RegisterHost(sessionID, connID, cwd string, now time.Time) error {
	sessionsFile, err := m.store.LoadSessions()
	if err != nil {
		return err
	}
	record, ok := sessionsFile.Sessions[sessionID]
	if !ok {
		record = model.SessionRecord{
			SessionID:     sessionID,
			State:         model.SessionStateHome,
			Cwd:           cwd,
			ResumeAllowed: true,
			CreatedAt:     now,
			UpdatedAt:     now,
		}
	} else {
		record.Cwd = cwd
		record.UpdatedAt = now
	}
	sessionsFile.Sessions[sessionID] = record
	sessionsFile.UpdatedAt = now
	if err := m.store.SaveSessions(sessionsFile); err != nil {
		return err
	}
	m.mu.Lock()
	runtime := m.ensureRuntime(sessionID)
	runtime.HostConnID = connID
	runtime.HostConnected = true
	m.connSessions[connID] = sessionID
	m.mu.Unlock()
	return nil
}

func (m *Manager) HandleHostDisconnect(connID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	sessionID, ok := m.connSessions[connID]
	if !ok {
		return ""
	}
	delete(m.connSessions, connID)
	runtime := m.runtime[sessionID]
	if runtime != nil && runtime.HostConnID == connID {
		runtime.HostConnected = false
		runtime.HostConnID = ""
		if !runtime.GatewayConnected {
			delete(m.runtime, sessionID)
		}
	}
	return sessionID
}

func (m *Manager) Unregister(sessionID string, now time.Time) error {
	sessionsFile, err := m.store.LoadSessions()
	if err != nil {
		return err
	}
	if _, ok := sessionsFile.Sessions[sessionID]; ok {
		delete(sessionsFile.Sessions, sessionID)
		sessionsFile.UpdatedAt = now
		if err := m.store.SaveSessions(sessionsFile); err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.deleteRuntime(sessionID)
	m.mu.Unlock()
	return nil
}

func (m *Manager) UpdateState(sessionID string, state model.SessionState, pid *int, now time.Time) error {
	sessionsFile, err := m.store.LoadSessions()
	if err != nil {
		return err
	}
	record, ok := sessionsFile.Sessions[sessionID]
	if !ok {
		return nil
	}
	record.State = state
	record.CodexChildPID = pid
	if state == model.SessionStateInCodex {
		record.LastEnteredCodexAt = &now
	}
	record.UpdatedAt = now
	sessionsFile.Sessions[sessionID] = record
	sessionsFile.UpdatedAt = now
	return m.store.SaveSessions(sessionsFile)
}

func (m *Manager) ReturnHome(sessionID string, now time.Time) error {
	sessionsFile, err := m.store.LoadSessions()
	if err != nil {
		return err
	}
	record, ok := sessionsFile.Sessions[sessionID]
	if !ok {
		return nil
	}
	record.State = model.SessionStateHome
	record.LastReturnedHomeAt = &now
	record.CodexChildPID = nil
	record.UpdatedAt = now
	sessionsFile.Sessions[sessionID] = record
	sessionsFile.UpdatedAt = now
	return m.store.SaveSessions(sessionsFile)
}

func (m *Manager) PrepareLaunch(sessionID, requestedCwd string, selectedProfileID *string, authEpochID string, now time.Time) (model.SessionRecord, string, error) {
	sessionsFile, err := m.store.LoadSessions()
	if err != nil {
		return model.SessionRecord{}, "", err
	}
	record, ok := sessionsFile.Sessions[sessionID]
	if !ok {
		return model.SessionRecord{}, "", nil
	}
	selectedCwd := requestedCwd
	if record.ActiveThreadID != nil && record.ResumeAllowed && record.ActiveThreadCwd != nil && *record.ActiveThreadCwd != "" {
		selectedCwd = *record.ActiveThreadCwd
	}
	record.State = model.SessionStateLaunchingCodex
	record.Cwd = requestedCwd
	record.LastKnownProfileID = selectedProfileID
	record.LastSeenAuthEpochID = &authEpochID
	record.UpdatedAt = now
	sessionsFile.Sessions[sessionID] = record
	sessionsFile.UpdatedAt = now
	if err := m.store.SaveSessions(sessionsFile); err != nil {
		return model.SessionRecord{}, "", err
	}
	return record, selectedCwd, nil
}

func (m *Manager) RecordThread(sessionID, threadID, cwd string, now time.Time) error {
	if threadID == "" {
		return nil
	}
	sessionsFile, err := m.store.LoadSessions()
	if err != nil {
		return err
	}
	record, ok := sessionsFile.Sessions[sessionID]
	if !ok {
		return nil
	}
	previousThreadID := ""
	if record.ActiveThreadID != nil {
		previousThreadID = *record.ActiveThreadID
	}
	record.ActiveThreadID = &threadID
	if cwd != "" {
		record.ActiveThreadCwd = &cwd
	} else if previousThreadID != threadID {
		record.ActiveThreadCwd = nil
	}
	record.ResumeAllowed = true
	record.UpdatedAt = now
	sessionsFile.Sessions[sessionID] = record
	sessionsFile.UpdatedAt = now
	return m.store.SaveSessions(sessionsFile)
}

func (m *Manager) SetGatewayConnected(sessionID string, connected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.ensureRuntime(sessionID)
	runtime.GatewayConnected = connected
	if !connected {
		runtime.ActiveTurns = 0
		runtime.LastThreadStatus = ""
		if !runtime.HostConnected {
			delete(m.runtime, sessionID)
		}
	}
}

func (m *Manager) NoteTurnStarted(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.ensureRuntime(sessionID)
	runtime.ActiveTurns++
	runtime.LastThreadStatus = "active"
}

func (m *Manager) NoteTurnCompleted(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.ensureRuntime(sessionID)
	if runtime.ActiveTurns > 0 {
		runtime.ActiveTurns--
	}
	if runtime.ActiveTurns == 0 {
		runtime.LastThreadStatus = "idle"
	}
}

func (m *Manager) NoteThreadStatus(sessionID, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.ensureRuntime(sessionID)
	runtime.LastThreadStatus = status
	if status == "active" && runtime.ActiveTurns == 0 {
		runtime.ActiveTurns = 1
	}
	if status != "active" && runtime.ActiveTurns == 0 {
		runtime.LastThreadStatus = status
	}
}

func (m *Manager) Readiness() (Readiness, error) {
	sessionsFile, err := m.store.LoadSessions()
	if err != nil {
		return Readiness{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	readiness := Readiness{}
	for sessionID, record := range sessionsFile.Sessions {
		runtime := m.runtime[sessionID]
		if runtime == nil || !runtime.HostConnected || record.State != model.SessionStateInCodex {
			continue
		}
		readiness.LiveCodexSessions++
		if runtime.GatewayConnected && runtime.ActiveTurns > 0 {
			readiness.BusySessionIDs = append(readiness.BusySessionIDs, sessionID)
		}
	}
	return readiness, nil
}

func (m *Manager) Session(sessionID string) (*model.SessionRecord, error) {
	sessionsFile, err := m.store.LoadSessions()
	if err != nil {
		return nil, err
	}
	record, ok := sessionsFile.Sessions[sessionID]
	if !ok {
		return nil, nil
	}
	copy := record
	return &copy, nil
}

func (m *Manager) SessionCount() (int, error) {
	sessionsFile, err := m.store.LoadSessions()
	if err != nil {
		return 0, err
	}
	return len(sessionsFile.Sessions), nil
}

func (m *Manager) SessionThreadFilterCwd(sessionID string) (string, bool) {
	record, err := m.Session(sessionID)
	if err != nil || record == nil {
		return "", false
	}
	if record.ActiveThreadCwd != nil && *record.ActiveThreadCwd != "" {
		return *record.ActiveThreadCwd, true
	}
	if record.Cwd != "" {
		return record.Cwd, true
	}
	return "", false
}

func (m *Manager) ensureRuntime(sessionID string) *RuntimeSession {
	runtime := m.runtime[sessionID]
	if runtime == nil {
		runtime = &RuntimeSession{}
		m.runtime[sessionID] = runtime
	}
	return runtime
}

func (m *Manager) deleteRuntime(sessionID string) {
	runtime := m.runtime[sessionID]
	if runtime != nil && runtime.HostConnID != "" {
		delete(m.connSessions, runtime.HostConnID)
	}
	delete(m.runtime, sessionID)
}

package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type BackendResolver func(sessionID string) (backendURL string, backendToken string, ok bool)
type CwdResolver func(sessionID string) (string, bool)

type Observer interface {
	OnGatewayConnected(ctx context.Context, sessionID string, connected bool) error
	OnThreadObserved(sessionID, threadID, cwd string) error
	OnTurnStarted(ctx context.Context, sessionID string) error
	OnTurnCompleted(ctx context.Context, sessionID string) error
	OnThreadStatus(ctx context.Context, sessionID string, status string) error
}

// Server owns the thin websocket proxy in front of the shared Codex app-server.
// It is intentionally limited to auth, request rewriting, and observation.
type Server struct {
	backendResolver BackendResolver
	cwdResolver     CwdResolver
	observer        Observer

	mu            sync.Mutex
	httpServer    *http.Server
	listener      net.Listener
	listenURL     string
	sessionTokens map[string]string
}

type connState struct {
	mu      sync.Mutex
	pending map[string]pendingThreadRequest
}

type pendingThreadRequest struct {
	Method   string
	ThreadID string
	Cwd      string
}

type trackedThread struct {
	ID  string
	Cwd string
}

type observation struct {
	Thread       *trackedThread
	TurnStarted  bool
	TurnComplete bool
	ThreadStatus string
}

func New(backendResolver BackendResolver, cwdResolver CwdResolver, observer Observer) *Server {
	return &Server{
		backendResolver: backendResolver,
		cwdResolver:     cwdResolver,
		observer:        observer,
		sessionTokens:   map[string]string{},
	}
}

func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.httpServer != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	listenURL := fmt.Sprintf("ws://%s", listener.Addr().String())
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleWebSocket)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.mu.Lock()
	s.listener = listener
	s.httpServer = server
	s.listenURL = listenURL
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.Close()
	}()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = err
		}
	}()
	return nil
}

func (s *Server) Close() {
	s.mu.Lock()
	server := s.httpServer
	listener := s.listener
	s.httpServer = nil
	s.listener = nil
	s.listenURL = ""
	s.sessionTokens = map[string]string{}
	s.mu.Unlock()
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}
	if listener != nil {
		_ = listener.Close()
	}
}

func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listenURL
}

func (s *Server) IssueSessionToken(sessionID string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for existingToken, existingSessionID := range s.sessionTokens {
		if existingSessionID == sessionID {
			delete(s.sessionTokens, existingToken)
		}
	}
	s.sessionTokens[token] = sessionID
	return token, nil
}

func (s *Server) RevokeSessionTokens(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for token, existingSessionID := range s.sessionTokens {
		if existingSessionID == sessionID {
			delete(s.sessionTokens, token)
		}
	}
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	sessionID, backendURL, backendToken, ok := s.authContext(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer clientConn.Close()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+backendToken)
	backendConn, _, err := websocket.DefaultDialer.Dial(backendURL, header)
	if err != nil {
		_ = clientConn.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "backend unavailable"),
			time.Now().Add(time.Second),
		)
		return
	}
	defer backendConn.Close()

	state := &connState{pending: map[string]pendingThreadRequest{}}
	if s.observer != nil {
		_ = s.observer.OnGatewayConnected(context.Background(), sessionID, true)
		defer func() {
			_ = s.observer.OnGatewayConnected(context.Background(), sessionID, false)
		}()
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- proxyFrames(clientConn, backendConn, func(data []byte) []byte {
			return s.rewriteClientMessage(sessionID, data)
		}, func(data []byte) {
			state.trackClientMessage(data)
		})
	}()
	go func() {
		errCh <- proxyFrames(backendConn, clientConn, nil, func(data []byte) {
			observation := state.trackServerMessage(data)
			if s.observer == nil {
				return
			}
			if observation.Thread != nil {
				_ = s.observer.OnThreadObserved(sessionID, observation.Thread.ID, observation.Thread.Cwd)
			}
			if observation.TurnStarted {
				_ = s.observer.OnTurnStarted(context.Background(), sessionID)
			}
			if observation.TurnComplete {
				_ = s.observer.OnTurnCompleted(context.Background(), sessionID)
			}
			if observation.ThreadStatus != "" {
				_ = s.observer.OnThreadStatus(context.Background(), sessionID, observation.ThreadStatus)
			}
		})
	}()
	<-errCh
}

func (s *Server) authContext(r *http.Request) (string, string, string, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", "", "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	s.mu.Lock()
	sessionID, ok := s.sessionTokens[token]
	s.mu.Unlock()
	if !ok {
		return "", "", "", false
	}
	backendURL, backendToken, ok := s.backendResolver(sessionID)
	if !ok {
		return "", "", "", false
	}
	return sessionID, backendURL, backendToken, true
}

func (s *Server) rewriteClientMessage(sessionID string, data []byte) []byte {
	cwd, ok := s.cwdResolver(sessionID)
	if !ok {
		return data
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return data
	}
	method, _ := msg["method"].(string)
	switch method {
	case "initialize":
		params, _ := msg["params"].(map[string]any)
		if params == nil {
			return data
		}
		clientInfo, _ := params["clientInfo"].(map[string]any)
		if clientInfo == nil {
			return data
		}
		clientInfo["cwd"] = cwd
		params["clientInfo"] = clientInfo
		msg["params"] = params
	case "thread/list", "thread/start":
		params, _ := msg["params"].(map[string]any)
		if params == nil {
			params = map[string]any{}
		}
		params["cwd"] = cwd
		msg["params"] = params
	default:
		return data
	}
	rewritten, err := json.Marshal(msg)
	if err != nil {
		return data
	}
	return rewritten
}

func proxyFrames(src *websocket.Conn, dst *websocket.Conn, rewrite func([]byte) []byte, inspect func([]byte)) error {
	for {
		messageType, data, err := src.ReadMessage()
		if err != nil {
			return err
		}
		if messageType == websocket.TextMessage {
			if rewrite != nil {
				data = rewrite(data)
			}
			if inspect != nil {
				inspect(data)
			}
		}
		if err := dst.WriteMessage(messageType, data); err != nil {
			return err
		}
	}
}

func (s *connState) trackClientMessage(data []byte) {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ThreadID string `json:"threadId"`
			Cwd      string `json:"cwd"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	if len(msg.ID) == 0 {
		return
	}
	if msg.Method != "thread/start" && msg.Method != "thread/resume" && msg.Method != "thread/fork" {
		return
	}
	key := responseIDKey(msg.ID)
	if key == "" {
		return
	}
	s.mu.Lock()
	s.pending[key] = pendingThreadRequest{Method: msg.Method, ThreadID: msg.Params.ThreadID, Cwd: msg.Params.Cwd}
	s.mu.Unlock()
}

func (s *connState) trackServerMessage(data []byte) observation {
	observation := observation{}
	if thread, ok := trackThreadStartedNotification(data); ok {
		observation.Thread = &thread
	}
	if thread, ok := s.trackResponse(data); ok {
		observation.Thread = &thread
	}
	if trackTurnStartedNotification(data) {
		observation.TurnStarted = true
	}
	if trackTurnCompletedNotification(data) {
		observation.TurnComplete = true
	}
	if status, ok := trackThreadStatusChangedNotification(data); ok {
		observation.ThreadStatus = status
	}
	return observation
}

func (s *connState) trackResponse(data []byte) (trackedThread, bool) {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Result struct {
			Thread struct {
				ID  string `json:"id"`
				Cwd string `json:"cwd"`
			} `json:"thread"`
		} `json:"result"`
		Error *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return trackedThread{}, false
	}
	if len(msg.ID) == 0 {
		return trackedThread{}, false
	}
	key := responseIDKey(msg.ID)
	if key == "" {
		return trackedThread{}, false
	}
	s.mu.Lock()
	req, ok := s.pending[key]
	if ok {
		delete(s.pending, key)
	}
	s.mu.Unlock()
	if !ok || req.Method == "" || msg.Error != nil {
		return trackedThread{}, false
	}
	thread := trackedThread{ID: msg.Result.Thread.ID, Cwd: msg.Result.Thread.Cwd}
	if thread.ID == "" {
		thread.ID = req.ThreadID
	}
	if thread.Cwd == "" && req.Method == "thread/start" {
		thread.Cwd = req.Cwd
	}
	if thread.ID == "" {
		return trackedThread{}, false
	}
	return thread, true
}

func trackThreadStartedNotification(data []byte) (trackedThread, bool) {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Thread struct {
				ID  string `json:"id"`
				Cwd string `json:"cwd"`
			} `json:"thread"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return trackedThread{}, false
	}
	if msg.Method != "thread/started" || msg.Params.Thread.ID == "" {
		return trackedThread{}, false
	}
	return trackedThread{ID: msg.Params.Thread.ID, Cwd: msg.Params.Thread.Cwd}, true
}

func trackTurnStartedNotification(data []byte) bool {
	var msg struct {
		Method string `json:"method"`
	}
	return json.Unmarshal(data, &msg) == nil && msg.Method == "turn/started"
}

func trackTurnCompletedNotification(data []byte) bool {
	var msg struct {
		Method string `json:"method"`
	}
	return json.Unmarshal(data, &msg) == nil && msg.Method == "turn/completed"
}

func trackThreadStatusChangedNotification(data []byte) (string, bool) {
	var msg struct {
		Method string `json:"method"`
		Params struct {
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"params"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", false
	}
	if msg.Method != "thread/status/changed" || msg.Params.Status.Type == "" {
		return "", false
	}
	return msg.Params.Status.Type, true
}

func responseIDKey(raw json.RawMessage) string {
	var anyID any
	if err := json.Unmarshal(raw, &anyID); err != nil {
		return ""
	}
	return fmt.Sprint(anyID)
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type gatewayServer struct {
	httpServer *http.Server
	listener   net.Listener
	listenURL  string
}

type gatewayConnState struct {
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

func (s *Service) startGateway(ctx context.Context) error {
	s.mu.Lock()
	if s.gateway != nil {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	gatewayURL := fmt.Sprintf("ws://%s", listener.Addr().String())

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleGatewayWebSocket)
	httpServer := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	s.mu.Lock()
	s.gatewayURL = gatewayURL
	s.gateway = &gatewayServer{
		httpServer: httpServer,
		listener:   listener,
		listenURL:  gatewayURL,
	}
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.shutdownGateway()
	}()

	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.setDegraded(fmt.Errorf("gateway serve failed: %w", err))
		}
	}()

	return nil
}

func (s *Service) shutdownGateway() {
	s.mu.Lock()
	gateway := s.gateway
	s.gateway = nil
	s.gatewayURL = ""
	s.sessionTokens = map[string]string{}
	s.mu.Unlock()
	if gateway != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = gateway.httpServer.Shutdown(ctx)
		_ = gateway.listener.Close()
	}
}

func (s *Service) handleGatewayWebSocket(w http.ResponseWriter, r *http.Request) {
	sessionID, backendURL, backendToken, ok := s.gatewayAuthContext(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
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

	state := &gatewayConnState{pending: map[string]pendingThreadRequest{}}
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
			if thread, ok := state.trackServerMessage(data); ok {
				if err := s.updateSessionActiveThread(sessionID, thread); err != nil {
					s.setDegraded(err)
				}
			}
		})
	}()

	<-errCh
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

func (s *Service) gatewayAuthContext(r *http.Request) (string, string, string, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", "", "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID, ok := s.sessionTokens[token]
	if !ok || s.appServerURL == "" || s.appServerToken == "" {
		return "", "", "", false
	}
	return sessionID, s.appServerURL, s.appServerToken, true
}

func (s *Service) rewriteClientMessage(sessionID string, data []byte) []byte {
	cwd, ok := s.threadListFilterCwd(sessionID)
	if !ok {
		return data
	}

	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return data
	}
	method, _ := msg["method"].(string)
	if method != "thread/list" {
		return data
	}

	params, _ := msg["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	if rawCwd, exists := params["cwd"]; exists && rawCwd != nil && fmt.Sprint(rawCwd) != "" {
		return data
	}

	params["cwd"] = cwd
	msg["params"] = params
	rewritten, err := json.Marshal(msg)
	if err != nil {
		return data
	}
	return rewritten
}

func (s *Service) threadListFilterCwd(sessionID string) (string, bool) {
	sessions, err := s.store.LoadSessions()
	if err != nil {
		return "", false
	}
	record, ok := sessions.Sessions[sessionID]
	if !ok {
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

func (s *Service) updateSessionActiveThread(sessionID string, thread trackedThread) error {
	if thread.ID == "" {
		return nil
	}
	sessions, err := s.store.LoadSessions()
	if err != nil {
		return err
	}
	record, ok := sessions.Sessions[sessionID]
	if !ok {
		return nil
	}
	previousThreadID := ""
	if record.ActiveThreadID != nil {
		previousThreadID = *record.ActiveThreadID
	}
	record.ActiveThreadID = &thread.ID
	if thread.Cwd != "" {
		record.ActiveThreadCwd = &thread.Cwd
	} else if previousThreadID != thread.ID {
		record.ActiveThreadCwd = nil
	}
	record.ResumeAllowed = true
	record.UpdatedAt = time.Now()
	sessions.Sessions[sessionID] = record
	sessions.UpdatedAt = record.UpdatedAt
	return s.store.SaveSessions(sessions)
}

func (s *gatewayConnState) trackClientMessage(data []byte) {
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
	s.pending[key] = pendingThreadRequest{
		Method:   msg.Method,
		ThreadID: msg.Params.ThreadID,
		Cwd:      msg.Params.Cwd,
	}
	s.mu.Unlock()
}

func (s *gatewayConnState) trackServerMessage(data []byte) (trackedThread, bool) {
	if thread, ok := trackThreadStartedNotification(data); ok {
		return thread, true
	}
	return s.trackResponse(data)
}

func (s *gatewayConnState) trackResponse(data []byte) (trackedThread, bool) {
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
	thread := trackedThread{
		ID:  msg.Result.Thread.ID,
		Cwd: msg.Result.Thread.Cwd,
	}
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

func responseIDKey(raw json.RawMessage) string {
	var anyID any
	if err := json.Unmarshal(raw, &anyID); err != nil {
		return ""
	}
	return fmt.Sprint(anyID)
}

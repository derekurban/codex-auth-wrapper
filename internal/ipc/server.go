package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
)

type RequestHandler func(ctx context.Context, connID string, method string, payload json.RawMessage) (any, error)

type Server struct {
	listener net.Listener
	handler  RequestHandler
	connsMu  sync.Mutex
	conns    map[string]*serverConn
}

type serverConn struct {
	id   string
	conn net.Conn
	enc  *json.Encoder
	mu   sync.Mutex
}

func Listen(handler RequestHandler) (*Server, error) {
	l, err := winio.ListenPipe(PipeName, &winio.PipeConfig{
		MessageMode:      false,
		InputBufferSize:  65536,
		OutputBufferSize: 65536,
	})
	if err != nil {
		return nil, err
	}
	return &Server{
		listener: l,
		handler:  handler,
		conns:    map[string]*serverConn{},
	}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		c := &serverConn{
			id:   uuidLike(),
			conn: conn,
			enc:  json.NewEncoder(conn),
		}
		s.connsMu.Lock()
		s.conns[c.id] = c
		s.connsMu.Unlock()
		go s.handleConn(ctx, c)
	}
}

func (s *Server) Close() error {
	return s.listener.Close()
}

func (s *Server) SendEvent(connID, name string, payload any) error {
	s.connsMu.Lock()
	conn := s.conns[connID]
	s.connsMu.Unlock()
	if conn == nil {
		return errors.New("connection not found")
	}
	body, err := json.Marshal(struct {
		Name string `json:"name"`
		Data any    `json:"data"`
	}{
		Name: name,
		Data: payload,
	})
	if err != nil {
		return err
	}
	return conn.send(Envelope{Type: TypeEvent, Payload: body})
}

func (s *Server) Broadcast(name string, payload any) {
	s.connsMu.Lock()
	ids := make([]string, 0, len(s.conns))
	for id := range s.conns {
		ids = append(ids, id)
	}
	s.connsMu.Unlock()
	for _, id := range ids {
		_ = s.SendEvent(id, name, payload)
	}
}

func (s *Server) handleConn(ctx context.Context, conn *serverConn) {
	defer func() {
		_ = conn.conn.Close()
		s.connsMu.Lock()
		delete(s.conns, conn.id)
		s.connsMu.Unlock()
	}()
	dec := json.NewDecoder(conn.conn)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		var env Envelope
		if err := dec.Decode(&env); err != nil {
			return
		}
		if env.Type != TypeRequest {
			continue
		}
		var req struct {
			Method string          `json:"method"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(env.Payload, &req); err != nil {
			_ = conn.send(Envelope{Type: TypeResponse, RequestID: env.RequestID, Error: err.Error()})
			continue
		}
		resp, err := s.handler(ctx, conn.id, req.Method, req.Data)
		if err != nil {
			_ = conn.send(Envelope{Type: TypeResponse, RequestID: env.RequestID, Error: err.Error()})
			continue
		}
		var payload []byte
		if resp != nil {
			payload, err = json.Marshal(resp)
			if err != nil {
				_ = conn.send(Envelope{Type: TypeResponse, RequestID: env.RequestID, Error: err.Error()})
				continue
			}
		}
		_ = conn.send(Envelope{Type: TypeResponse, RequestID: env.RequestID, Payload: payload})
	}
}

func (c *serverConn) send(env Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(env)
}

func uuidLike() string {
	return "conn-" + time.Now().Format("20060102150405.000000000")
}

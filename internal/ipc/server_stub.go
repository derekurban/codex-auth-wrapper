//go:build !windows

package ipc

import (
	"context"
	"encoding/json"
	"errors"
)

type RequestHandler func(ctx context.Context, connID string, method string, payload json.RawMessage) (any, error)

type Server struct{}

func Listen(handler RequestHandler) (*Server, error) {
	_ = handler
	return nil, errors.New("caw broker IPC server is only supported on Windows")
}

func (s *Server) Serve(ctx context.Context) error {
	_ = s
	_ = ctx
	return errors.New("caw broker IPC server is only supported on Windows")
}

func (s *Server) Close() error {
	_ = s
	return nil
}

func (s *Server) SendEvent(connID, name string, payload any) error {
	_ = s
	_ = connID
	_ = name
	_ = payload
	return errors.New("caw broker IPC server is only supported on Windows")
}

func (s *Server) Broadcast(name string, payload any) {
	_ = s
	_ = name
	_ = payload
}

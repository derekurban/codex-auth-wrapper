//go:build !windows

package conpty

import (
	"errors"

	"github.com/derekurban/codex-auth-wrapper/internal/codex"
)

type Session struct{}

func Start(_ codex.CommandSpec, _ func()) (*Session, error) {
	return nil, errors.New("conpty session hosting is only supported on Windows")
}

func (s *Session) PID() int {
	return 0
}

func (s *Session) Wait() (int, error) {
	return 0, errors.New("conpty session hosting is only supported on Windows")
}

func (s *Session) Kill() error {
	return errors.New("conpty session hosting is only supported on Windows")
}

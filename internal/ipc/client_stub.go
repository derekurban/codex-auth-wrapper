//go:build !windows

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type EventHandler func(name string, payload json.RawMessage)

type Client struct{}

func Dial(ctx context.Context, timeout time.Duration, handler EventHandler) (*Client, error) {
	_ = ctx
	_ = timeout
	_ = handler
	return nil, errors.New("caw broker IPC is only supported on Windows")
}

func (c *Client) Close() error {
	_ = c
	return nil
}

func (c *Client) Request(ctx context.Context, method string, payload any, out any) error {
	_ = c
	_ = ctx
	_ = method
	_ = payload
	_ = out
	return errors.New("caw broker IPC is only supported on Windows")
}

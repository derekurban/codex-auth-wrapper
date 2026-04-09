//go:build windows

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/google/uuid"
)

type EventHandler func(name string, payload json.RawMessage)

type Client struct {
	conn    net.Conn
	enc     *json.Encoder
	dec     *json.Decoder
	mu      sync.Mutex
	pending map[string]chan Envelope
	handler EventHandler
	closed  chan struct{}
}

func Dial(ctx context.Context, timeout time.Duration, handler EventHandler) (*Client, error) {
	conn, err := winio.DialPipeContext(ctx, PipeName)
	if err != nil {
		return nil, err
	}
	c := &Client{
		conn:    conn,
		enc:     json.NewEncoder(conn),
		dec:     json.NewDecoder(conn),
		pending: map[string]chan Envelope{},
		handler: handler,
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

func (c *Client) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return c.conn.Close()
}

func (c *Client) Request(ctx context.Context, method string, payload any, out any) error {
	requestID := uuid.NewString()
	reqPayload, err := json.Marshal(struct {
		Method string `json:"method"`
		Data   any    `json:"data,omitempty"`
	}{
		Method: method,
		Data:   payload,
	})
	if err != nil {
		return err
	}
	respCh := make(chan Envelope, 1)
	c.mu.Lock()
	c.pending[requestID] = respCh
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, requestID)
		c.mu.Unlock()
	}()
	if err := c.send(Envelope{Type: TypeRequest, RequestID: requestID, Payload: reqPayload}); err != nil {
		return err
	}
	select {
	case resp := <-respCh:
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		if out == nil || len(resp.Payload) == 0 {
			return nil
		}
		return json.Unmarshal(resp.Payload, out)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return fmt.Errorf("broker connection closed")
	}
}

func (c *Client) send(env Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(env)
}

func (c *Client) readLoop() {
	for {
		var env Envelope
		if err := c.dec.Decode(&env); err != nil {
			_ = c.Close()
			return
		}
		switch env.Type {
		case TypeResponse:
			c.mu.Lock()
			ch := c.pending[env.RequestID]
			c.mu.Unlock()
			if ch != nil {
				ch <- env
			}
		case TypeEvent:
			if c.handler == nil {
				continue
			}
			var evt Event
			if err := json.Unmarshal(env.Payload, &evt); err != nil {
				continue
			}
			var body struct {
				Name string          `json:"name"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(env.Payload, &body); err != nil {
				continue
			}
			c.handler(evt.Name, body.Data)
		}
	}
}

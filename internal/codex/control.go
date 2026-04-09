package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type AppServerClient struct {
	conn    *websocket.Conn
	pending map[string]chan responseEnvelope
	mu      sync.Mutex
	writeMu sync.Mutex
	closed  chan struct{}
}

type responseEnvelope struct {
	ID     any             `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type AccountInfo struct {
	RequiresOpenAIAuth bool
	Type               string
	Email              string
	PlanType           string
}

type RateLimit struct {
	UsedPercent        int
	WindowDurationMins int
	ResetsAt           time.Time
}

type RateLimits struct {
	Primary   *RateLimit
	Secondary *RateLimit
}

func DialAppServer(ctx context.Context, rawURL, bearerToken string) (*AppServerClient, error) {
	header := http.Header{}
	if bearerToken != "" {
		header.Set("Authorization", "Bearer "+bearerToken)
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, rawURL, header)
	if err != nil {
		return nil, err
	}
	c := &AppServerClient{
		conn:    conn,
		pending: map[string]chan responseEnvelope{},
		closed:  make(chan struct{}),
	}
	go c.readLoop()
	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *AppServerClient) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return c.conn.Close()
}

func (c *AppServerClient) AccountRead(ctx context.Context) (AccountInfo, error) {
	var resp struct {
		Account *struct {
			Type     string `json:"type"`
			Email    string `json:"email"`
			PlanType string `json:"planType"`
		} `json:"account"`
		RequiresOpenaiAuth bool `json:"requiresOpenaiAuth"`
	}
	if err := c.request(ctx, "account/read", map[string]any{"refreshToken": false}, &resp); err != nil {
		return AccountInfo{}, err
	}
	info := AccountInfo{RequiresOpenAIAuth: resp.RequiresOpenaiAuth}
	if resp.Account != nil {
		info.Type = resp.Account.Type
		info.Email = resp.Account.Email
		info.PlanType = resp.Account.PlanType
	}
	return info, nil
}

func (c *AppServerClient) RateLimitsRead(ctx context.Context) (RateLimits, error) {
	var resp struct {
		RateLimits struct {
			Primary   *limitResponse `json:"primary"`
			Secondary *limitResponse `json:"secondary"`
		} `json:"rateLimits"`
	}
	if err := c.request(ctx, "account/rateLimits/read", map[string]any{}, &resp); err != nil {
		return RateLimits{}, err
	}
	return RateLimits{
		Primary:   convertLimit(resp.RateLimits.Primary),
		Secondary: convertLimit(resp.RateLimits.Secondary),
	}, nil
}

type limitResponse struct {
	UsedPercent        int   `json:"usedPercent"`
	WindowDurationMins int   `json:"windowDurationMins"`
	ResetsAt           int64 `json:"resetsAt"`
}

func convertLimit(in *limitResponse) *RateLimit {
	if in == nil {
		return nil
	}
	return &RateLimit{
		UsedPercent:        in.UsedPercent,
		WindowDurationMins: in.WindowDurationMins,
		ResetsAt:           time.Unix(in.ResetsAt, 0),
	}
}

func (c *AppServerClient) initialize(ctx context.Context) error {
	var resp map[string]any
	if err := c.request(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "caw_broker",
			"title":   "Codex Auth Wrapper",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{
			"experimentalApi": false,
		},
	}, &resp); err != nil {
		return err
	}
	return c.notify("initialized", map[string]any{})
}

func (c *AppServerClient) notify(method string, params any) error {
	body, err := json.Marshal(map[string]any{
		"method": method,
		"params": params,
	})
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, body)
}

func (c *AppServerClient) request(ctx context.Context, method string, params any, out any) error {
	requestID := uuid.NewString()
	body, err := json.Marshal(map[string]any{
		"id":     requestID,
		"method": method,
		"params": params,
	})
	if err != nil {
		return err
	}
	respCh := make(chan responseEnvelope, 1)
	c.mu.Lock()
	c.pending[requestID] = respCh
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, requestID)
		c.mu.Unlock()
	}()
	c.writeMu.Lock()
	err = c.conn.WriteMessage(websocket.TextMessage, body)
	c.writeMu.Unlock()
	if err != nil {
		return err
	}
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return fmt.Errorf("%s: %s", method, resp.Error.Message)
		}
		if out == nil || len(resp.Result) == 0 {
			return nil
		}
		return json.Unmarshal(resp.Result, out)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return fmt.Errorf("app-server connection closed")
	}
}

func (c *AppServerClient) readLoop() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			_ = c.Close()
			return
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(data, &probe); err != nil {
			continue
		}
		rawID, ok := probe["id"]
		if !ok {
			continue
		}
		var id any
		if err := json.Unmarshal(rawID, &id); err != nil {
			continue
		}
		key := fmt.Sprint(id)
		var env responseEnvelope
		if err := json.Unmarshal(data, &env); err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[key]
		c.mu.Unlock()
		if ch != nil {
			ch <- env
		}
	}
}

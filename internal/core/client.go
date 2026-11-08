package core

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const MaxFrame = 16 << 20

type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return "core RPC error: " + e.Code }

type Frame struct {
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
	Type    string          `json:"type,omitempty"`
	State   *int            `json:"state,omitempty"`
	Message string          `json:"message,omitempty"`
	Line    string          `json:"line,omitempty"`
}

type Status struct {
	State          int    `json:"state"`
	ConnectedSince int64  `json:"connected_since"`
	Error          string `json:"error"`
	KillSwitch     struct {
		Supported bool `json:"supported"`
		Armed     bool `json:"armed"`
	} `json:"kill_switch"`
	ProxyPort uint16 `json:"proxy_port,omitempty"`
}

type Hello struct {
	CoreVersion     string `json:"coreVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
}

type Client struct {
	conn  net.Conn
	scan  *bufio.Scanner
	mu    sync.Mutex
	id    int64
	Hello Hello
}

func Dial(ctx context.Context, endpoint string) (*Client, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
	if err != nil {
		return nil, fmt.Errorf("connect to core: %w", err)
	}
	c := &Client{conn: conn, scan: bufio.NewScanner(conn)}
	c.scan.Buffer(make([]byte, 65536), MaxFrame)
	if err = c.Call(ctx, "hello", map[string]any{"protocolVersion": 1}, &c.Hello); err != nil {
		conn.Close()
		return nil, err
	}
	if c.Hello.ProtocolVersion != 1 {
		conn.Close()
		return nil, errors.New("unsupported core protocol version")
	}
	return c, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) deadline(ctx context.Context) func() {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(15 * time.Second)
	}
	c.conn.SetDeadline(deadline)
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { c.conn.SetDeadline(time.Now()); close(done) })
	return func() {
		if !stop() {
			<-done
		}
		c.conn.SetDeadline(time.Time{})
	}
}

func (c *Client) next() (Frame, error) {
	var frame Frame
	if !c.scan.Scan() {
		if err := c.scan.Err(); err != nil {
			return frame, fmt.Errorf("read core frame: %w", err)
		}
		return frame, errors.New("core closed the connection")
	}
	if err := json.Unmarshal(c.scan.Bytes(), &frame); err != nil {
		return frame, errors.New("invalid core frame")
	}
	return frame, nil
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear := c.deadline(ctx)
	defer clear()
	c.id++
	request := struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{c.id, method, params}
	if err := json.NewEncoder(c.conn).Encode(request); err != nil {
		return fmt.Errorf("write core request: %w", err)
	}
	for {
		frame, err := c.next()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if frame.ID == nil {
			continue
		}
		if *frame.ID != c.id {
			return errors.New("unexpected core response ID")
		}
		if frame.Error != nil {
			return frame.Error
		}
		if result != nil {
			if err := json.Unmarshal(frame.Result, result); err != nil {
				return errors.New("invalid core response payload")
			}
		}
		return nil
	}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	err := c.Call(ctx, "status", nil, &status)
	return status, err
}

func (c *Client) Watch(ctx context.Context, consume func(Frame)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { c.conn.Close() })
	defer stop()
	for {
		frame, err := c.next()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if frame.ID == nil {
			consume(frame)
		}
	}
}

func StateName(state int) string {
	switch state {
	case 0:
		return "disconnected"
	case 1:
		return "connecting"
	case 2:
		return "connected"
	case 3:
		return "disconnecting"
	case 4:
		return "error"
	default:
		return "unknown"
	}
}

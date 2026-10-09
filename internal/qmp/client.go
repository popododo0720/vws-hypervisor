// Package qmp is a minimal QEMU Machine Protocol client.
//
// Callers see typed methods. Greeting, capability negotiation, command ids,
// and asynchronous events stay in this package.
package qmp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// State is the run state QEMU reports.
type State int

const (
	// StateOther is any status this client does not act on.
	StateOther State = iota
	// StateRunning means guest vCPUs are running.
	StateRunning
	// StatePaused means vCPUs are stopped after the guest has run.
	StatePaused
	// StatePrelaunch means the process was started with vCPUs stopped.
	StatePrelaunch
	// StateShutdown means the guest has powered off.
	StateShutdown
)

// Error is a QMP error response.
type Error struct {
	Class string
	Desc  string
}

func (e *Error) Error() string { return "qmp: " + e.Class + ": " + e.Desc }

// ErrClosed reports that the peer closed the connection.
var ErrClosed = errors.New("qmp: connection closed")

// Client is one QMP session. It runs one command at a time.
type Client struct {
	conn net.Conn
	dec  *json.Decoder
	mu   sync.Mutex
	next uint64
}

// Dial connects to a QMP unix socket, reads the greeting, and enables commands.
func Dial(ctx context.Context, path string) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("qmp: dial: %w", err)
	}
	c := &Client{conn: conn, dec: json.NewDecoder(conn)}
	if err := c.handshake(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *Client) handshake(ctx context.Context) error {
	var greeting map[string]json.RawMessage
	if err := c.decode(ctx, &greeting); err != nil {
		return fmt.Errorf("qmp: greeting: %w", err)
	}
	if _, ok := greeting["QMP"]; !ok {
		return errors.New("qmp: greeting missing QMP")
	}
	if _, err := c.execute(ctx, "qmp_capabilities", nil); err != nil {
		return err
	}
	return nil
}

// Close closes the socket. It does not stop the guest.
func (c *Client) Close() error { return c.conn.Close() }

// Status runs query-status.
func (c *Client) Status(ctx context.Context) (State, error) {
	raw, err := c.execute(ctx, "query-status", nil)
	if err != nil {
		return StateOther, err
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return StateOther, fmt.Errorf("qmp: query-status: %w", err)
	}
	switch body.Status {
	case "running":
		return StateRunning, nil
	case "paused":
		return StatePaused, nil
	case "prelaunch":
		return StatePrelaunch, nil
	case "shutdown":
		return StateShutdown, nil
	default:
		return StateOther, nil
	}
}

// UUID runs query-uuid.
func (c *Client) UUID(ctx context.Context) (string, error) {
	raw, err := c.execute(ctx, "query-uuid", nil)
	if err != nil {
		return "", err
	}
	var body struct {
		UUID string `json:"UUID"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", fmt.Errorf("qmp: query-uuid: %w", err)
	}
	if body.UUID == "" {
		return "", errors.New("qmp: query-uuid returned an empty uuid")
	}
	return body.UUID, nil
}

// Cont runs cont.
func (c *Client) Cont(ctx context.Context) error {
	_, err := c.execute(ctx, "cont", nil)
	return err
}

// Powerdown runs system_powerdown.
func (c *Client) Powerdown(ctx context.Context) error {
	_, err := c.execute(ctx, "system_powerdown", nil)
	return err
}

// Quit runs quit. QEMU may close the socket before the return value arrives.
func (c *Client) Quit(ctx context.Context) error {
	_, err := c.execute(ctx, "quit", nil)
	if errors.Is(err, ErrClosed) {
		return nil
	}
	return err
}

func (c *Client) execute(ctx context.Context, cmd string, args any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	id := c.next
	msg := map[string]any{"execute": cmd, "id": id}
	if args != nil {
		msg["arguments"] = args
	}
	if err := c.write(ctx, msg); err != nil {
		return nil, err
	}
	for {
		var env struct {
			Event  string          `json:"event"`
			ID     uint64          `json:"id"`
			Return json.RawMessage `json:"return"`
			Error  *struct {
				Class string `json:"class"`
				Desc  string `json:"desc"`
			} `json:"error"`
		}
		if err := c.decode(ctx, &env); err != nil {
			return nil, err
		}
		if env.Event != "" && env.Return == nil && env.Error == nil {
			continue
		}
		if env.ID != id {
			continue
		}
		if env.Error != nil {
			return nil, &Error{Class: env.Error.Class, Desc: env.Error.Desc}
		}
		if env.Return == nil {
			env.Return = json.RawMessage("{}")
		}
		return env.Return, nil
	}
}

func (c *Client) write(ctx context.Context, v any) error {
	if err := c.deadline(ctx); err != nil {
		return err
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = c.conn.Write(raw)
	return err
}

func (c *Client) decode(ctx context.Context, dest any) error {
	if err := c.deadline(ctx); err != nil {
		return err
	}
	if err := c.dec.Decode(dest); err != nil {
		if errors.Is(err, net.ErrClosed) {
			return ErrClosed
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() && ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, io.EOF) {
			return ErrClosed
		}
		return err
	}
	return nil
}

func (c *Client) deadline(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dl, ok := ctx.Deadline()
	if !ok {
		dl = time.Now().Add(30 * time.Second)
	}
	return c.conn.SetDeadline(dl)
}

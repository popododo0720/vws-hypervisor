// Package qmptest is an in-process QMP peer.
// It does not import the client, so a mistake shared with the client fails a test.
package qmptest

import (
	"encoding/json"
	"net"
	"sync"
)

// Machine is one fake QEMU monitor.
type Machine struct {
	mu       sync.Mutex
	uuid     string
	status   string
	received []string
	onDown   func()
	ln       net.Listener
	conns    []net.Conn
}

// Listen serves QMP on a unix socket.
func Listen(path, uuid string) (*Machine, error) {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	m := &Machine{uuid: uuid, status: "running", ln: ln}
	go m.accept()
	return m, nil
}

func (m *Machine) accept() {
	for {
		m.mu.Lock()
		ln := m.ln
		m.mu.Unlock()
		if ln == nil {
			return
		}
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		m.mu.Lock()
		if m.ln == nil {
			m.mu.Unlock()
			_ = conn.Close()
			return
		}
		m.conns = append(m.conns, conn)
		m.mu.Unlock()
		go m.serve(conn)
	}
}

func (m *Machine) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	greeting := map[string]any{
		"QMP": map[string]any{
			"version":      map[string]any{"qemu": map[string]any{"major": 8}, "package": "qmptest"},
			"capabilities": []any{},
		},
	}
	if err := enc.Encode(greeting); err != nil {
		return
	}
	for {
		var req map[string]any
		if err := dec.Decode(&req); err != nil {
			return
		}
		exec, _ := req["execute"].(string)
		m.mu.Lock()
		m.received = append(m.received, exec)
		status := m.status
		uuid := m.uuid
		onDown := m.onDown
		m.mu.Unlock()
		id := req["id"]
		switch exec {
		case "qmp_capabilities", "cont":
			if exec == "cont" {
				m.mu.Lock()
				m.status = "running"
				m.mu.Unlock()
			}
			_ = enc.Encode(map[string]any{"return": map[string]any{}, "id": id})
		case "query-status":
			// An event before the return is normal QMP. The client must skip it.
			_ = enc.Encode(map[string]any{"event": "STOP"})
			_ = enc.Encode(map[string]any{"return": map[string]any{"status": status, "running": status == "running"}, "id": id})
		case "query-uuid":
			_ = enc.Encode(map[string]any{"return": map[string]any{"UUID": uuid}, "id": id})
		case "system_powerdown":
			m.mu.Lock()
			m.status = "shutdown"
			m.mu.Unlock()
			_ = enc.Encode(map[string]any{"return": map[string]any{}, "id": id})
			if onDown != nil {
				onDown()
			}
		case "quit":
			_ = enc.Encode(map[string]any{"return": map[string]any{}, "id": id})
			return
		default:
			_ = enc.Encode(map[string]any{"error": map[string]any{"class": "CommandNotFound", "desc": exec}, "id": id})
		}
	}
}

// Received returns execute names in order.
func (m *Machine) Received() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.received...)
}

// SetUUID changes the uuid returned by query-uuid.
func (m *Machine) SetUUID(uuid string) {
	m.mu.Lock()
	m.uuid = uuid
	m.mu.Unlock()
}

// SetStatus changes the status returned by query-status.
func (m *Machine) SetStatus(status string) {
	m.mu.Lock()
	m.status = status
	m.mu.Unlock()
}

// OnPowerdown runs after system_powerdown has been acknowledged.
func (m *Machine) OnPowerdown(fn func()) {
	m.mu.Lock()
	m.onDown = fn
	m.mu.Unlock()
}

// StopListening closes the listener and leaves accepted connections alone.
// New dials fail. The command that triggered this can still finish.
func (m *Machine) StopListening() {
	m.mu.Lock()
	ln := m.ln
	m.ln = nil
	m.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
}

// Close stops the listener and accepted connections.
func (m *Machine) Close() error {
	m.StopListening()
	m.mu.Lock()
	conns := m.conns
	m.conns = nil
	m.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	return nil
}

// Package systest is a host.Sys whose processes are in-process QMP peers.
// The peers outlive the agent that spawned them, which is what a restarted
// agent has to reattach to.
package systest

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/popododo0720/vws-hypervisor/internal/qmp/qmptest"
)

// Sys implements the host privileged seam with fake processes and links.
type Sys struct {
	mu       sync.Mutex
	procs    map[string]*proc
	links    map[string]struct{}
	spawns   map[string]int
	argvs    map[string][][]string
	onBridge func(string)
}

type proc struct {
	m      *qmptest.Machine
	socket string
	exited bool
}

// New returns an empty fake host.
func New() *Sys {
	return &Sys{
		procs:  map[string]*proc{},
		links:  map[string]struct{}{},
		spawns: map[string]int{},
		argvs:  map[string][][]string{},
	}
}

// ShortDir returns a directory short enough for a QMP socket path.
func ShortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "vws")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// SetBridgeHook runs when a bridge is ensured. It is called without the
// systest lock held.
func (s *Sys) SetBridgeHook(fn func(string)) {
	s.mu.Lock()
	s.onBridge = fn
	s.mu.Unlock()
}

// Spawn starts a QMP peer. An already-live name is left as it is.
func (s *Sys) Spawn(_ context.Context, name string, argv []string) error {
	socket, uuid, err := parseQEMU(argv)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if p := s.procs[name]; p != nil && !p.exited {
		s.mu.Unlock()
		return nil
	}
	if p := s.procs[name]; p != nil && p.m != nil {
		_ = p.m.Close()
	}
	s.mu.Unlock()
	m, err := qmptest.Listen(socket, uuid)
	if err != nil {
		return err
	}
	p := &proc{m: m, socket: socket}
	m.OnPowerdown(func() {
		s.mu.Lock()
		if cur := s.procs[name]; cur == p {
			cur.exited = true
		}
		s.mu.Unlock()
		m.StopListening()
		_ = os.Remove(socket)
	})
	s.mu.Lock()
	s.procs[name] = p
	s.spawns[name]++
	s.argvs[name] = append(s.argvs[name], append([]string(nil), argv...))
	s.mu.Unlock()
	return nil
}

// Kill removes the process. A missing process is success.
func (s *Sys) Kill(_ context.Context, name string) error {
	s.mu.Lock()
	p := s.procs[name]
	delete(s.procs, name)
	s.mu.Unlock()
	if p == nil {
		return nil
	}
	if p.m != nil {
		_ = p.m.Close()
	}
	_ = os.Remove(p.socket)
	return nil
}

// Processes lists live process names.
func (s *Sys) Processes(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for name, p := range s.procs {
		if !p.exited {
			out = append(out, name)
		}
	}
	return out, nil
}

// EnsureBridge records a bridge and runs the bridge hook.
func (s *Sys) EnsureBridge(_ context.Context, name string) error {
	s.remember(name, true)
	return nil
}

func (s *Sys) remember(name string, hook bool) {
	s.mu.Lock()
	s.links[name] = struct{}{}
	var fn func(string)
	if hook {
		fn = s.onBridge
	}
	s.mu.Unlock()
	if fn != nil {
		fn(name)
	}
}

// EnsureTap records a tap. The guest MAC is not applied to the tap.
func (s *Sys) EnsureTap(_ context.Context, name, _ string) error {
	s.remember(name, false)
	return nil
}

// DeleteLink removes a bridge or tap. A missing link is success.
func (s *Sys) DeleteLink(_ context.Context, name string) error {
	s.mu.Lock()
	delete(s.links, name)
	s.mu.Unlock()
	return nil
}

// Links lists known links.
func (s *Sys) Links(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for name := range s.links {
		out = append(out, name)
	}
	return out, nil
}

// Reboot drops processes and links. Disk files are left for the caller.
func (s *Sys) Reboot() {
	s.mu.Lock()
	procs := s.procs
	s.procs = map[string]*proc{}
	s.links = map[string]struct{}{}
	s.mu.Unlock()
	for _, p := range procs {
		if p.m != nil {
			_ = p.m.Close()
		}
		_ = os.Remove(p.socket)
	}
}

// SpawnCount returns how many times name was actually started.
func (s *Sys) SpawnCount(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spawns[name]
}

// Argvs returns every argv that started name, oldest first.
func (s *Sys) Argvs(name string) [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]string
	for _, argv := range s.argvs[name] {
		out = append(out, append([]string(nil), argv...))
	}
	return out
}

// Received returns QMP execute names for the live or last machine of name.
func (s *Sys) Received(name string) []string {
	s.mu.Lock()
	p := s.procs[name]
	s.mu.Unlock()
	if p == nil || p.m == nil {
		return nil
	}
	return p.m.Received()
}

// SetUUID changes the uuid the live machine reports.
func (s *Sys) SetUUID(name, uuid string) {
	s.mu.Lock()
	p := s.procs[name]
	s.mu.Unlock()
	if p != nil && p.m != nil {
		p.m.SetUUID(uuid)
	}
}

func parseQEMU(argv []string) (socket, uuid string, err error) {
	for i := 0; i < len(argv); i++ {
		switch argv[i] {
		case "-uuid":
			if i+1 < len(argv) {
				uuid = argv[i+1]
			}
		case "-qmp":
			if i+1 < len(argv) {
				socket = strings.TrimPrefix(argv[i+1], "unix:")
				socket = strings.TrimSuffix(socket, ",server,nowait")
			}
		}
	}
	if socket == "" || uuid == "" {
		return "", "", errors.New("systest: argv missing -qmp or -uuid")
	}
	return socket, uuid, nil
}

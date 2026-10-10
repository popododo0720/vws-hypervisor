// Package agent converges one host to the last accepted desired state.
//
// The agent never pulls from a control plane. Documents arrive through
// SetDesired or a drop file, and the last accepted document is stored on
// disk. With no accepted document the agent only observes. Closing the
// agent releases its lock and does not signal guests.
package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/popododo0720/vws-hypervisor/internal/atomicfile"
	"github.com/popododo0720/vws-hypervisor/internal/host"
	"github.com/popododo0720/vws-hypervisor/internal/reconcile"
	"github.com/popododo0720/vws-hypervisor/spec"
)

var (
	// ErrLocked means another agent holds the state directory.
	ErrLocked = errors.New("agent: state dir locked")
	// ErrStaleRevision means the document is older than one already accepted.
	ErrStaleRevision = errors.New("agent: stale revision")
	// ErrRevisionConflict means the same revision was reused with different content.
	ErrRevisionConflict = errors.New("agent: same revision, different content")
	// ErrSuperseded means the accepted document changed while a pass was applying.
	ErrSuperseded = errors.New("agent: desired state changed during reconcile")
)

// Config configures an Agent.
type Config struct {
	StateDir    string
	RunDir      string
	DesiredFile string
	Interval    time.Duration
	QEMU        string
	Sys         host.Sys
	Logger      *slog.Logger
	Now         func() time.Time
}

// Status is the outcome of the latest Reconcile.
type Status struct {
	Revision    uint64
	HaveDesired bool
	Converged   bool
	Observed    reconcile.Observed
	Waits       []reconcile.Wait
	Applied     []string
	Errors      []error
	Err         error
	IntakeErr   error
}

// Agent converges one host.
type Agent struct {
	cfg    Config
	host   *host.Host
	lock   *os.File
	pass   sync.Mutex
	accept sync.Mutex
	cur    atomic.Pointer[spec.Desired]
	floor  uint64
	intake error
	status atomic.Pointer[Status]
}

// Open locks the state directory, loads the cached document, and opens the host.
// A missing or unreadable cache does not delete guests. The revision floor is
// kept so an older drop file cannot be admitted after a bad cache.
func Open(cfg Config) (*Agent, error) {
	if cfg.Sys == nil {
		return nil, errors.New("agent: sys is required")
	}
	if cfg.Interval == 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		return nil, err
	}
	h, err := host.Open(host.Config{
		VolumeDir: filepath.Join(cfg.StateDir, "volumes"),
		RunDir:    cfg.RunDir,
		QEMU:      cfg.QEMU,
		Sys:       cfg.Sys,
		Now:       cfg.Now,
	})
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(cfg.StateDir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, ErrLocked
	}
	a := &Agent{cfg: cfg, host: h, lock: f}
	a.load()
	return a, nil
}

// Close releases the lock. It does not stop guests.
func (a *Agent) Close() error {
	if a.lock == nil {
		return nil
	}
	err := syscall.Flock(int(a.lock.Fd()), syscall.LOCK_UN)
	err2 := a.lock.Close()
	a.lock = nil
	return errors.Join(err, err2)
}

// SetDesired validates doc, writes it, and makes it the accepted state.
// The in-memory value is the parse of the bytes that were written.
func (a *Agent) SetDesired(_ context.Context, doc spec.Document) error {
	d, err := spec.Parse(doc)
	if err != nil {
		return err
	}
	raw, err := d.MarshalCanonical()
	if err != nil {
		return err
	}
	back, err := spec.Decode(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	stored, err := spec.Parse(back)
	if err != nil {
		return err
	}
	if stored.Digest() != d.Digest() {
		return errors.New("agent: canonical document did not round-trip")
	}
	a.accept.Lock()
	defer a.accept.Unlock()
	floor := a.floor
	if cur := a.cur.Load(); cur != nil && !cur.IsZero() && cur.Revision() > floor {
		floor = cur.Revision()
	}
	if stored.Revision() < floor {
		return ErrStaleRevision
	}
	if cur := a.cur.Load(); cur != nil && !cur.IsZero() && stored.Revision() == cur.Revision() {
		if stored.Digest() != cur.Digest() {
			return ErrRevisionConflict
		}
		return nil
	}
	if err := atomicfile.Write(a.desiredPath(), raw); err != nil {
		return err
	}
	if err := a.writeFloor(stored.Revision()); err != nil {
		return err
	}
	a.floor = stored.Revision()
	a.cur.Store(&stored)
	return nil
}

// Reconcile converges once. It is safe to call while SetDesired runs.
// Two Reconcile calls do not run at the same time.
func (a *Agent) Reconcile(ctx context.Context) Status {
	a.pass.Lock()
	defer a.pass.Unlock()
	a.poll(ctx)
	st := Status{IntakeErr: a.intake}
	var plan reconcile.Plan
	for pass := 0; pass < 8; pass++ {
		cur := a.cur.Load()
		obs, err := a.host.Observe(ctx)
		st.Observed = obs
		if err != nil {
			st.Err = err
			break
		}
		if cur == nil || cur.IsZero() {
			st.HaveDesired = false
			plan = reconcile.Plan{}
			break
		}
		st.HaveDesired = true
		st.Revision = cur.Revision()
		plan = reconcile.Next(*cur, obs, a.cfg.Now())
		if len(plan.Steps()) == 0 {
			break
		}
		digest := cur.Digest()
		err = a.host.Apply(ctx, plan, func() error {
			now := a.cur.Load()
			if now == nil || now.Digest() != digest {
				return ErrSuperseded
			}
			return nil
		})
		if err != nil {
			st.Errors = append(st.Errors, err)
			break
		}
		for _, step := range plan.Steps() {
			st.Applied = append(st.Applied, reconcile.Op(step)+" "+step.Target().ID)
		}
	}
	st.Waits = plan.Waits()
	st.Converged = st.HaveDesired && st.Err == nil && len(st.Errors) == 0 && plan.Converged()
	a.status.Store(&st)
	return st
}

// Run reconciles until ctx is canceled. Cancellation does not stop guests.
func (a *Agent) Run(ctx context.Context) error {
	a.Reconcile(ctx)
	ticker := time.NewTicker(a.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			a.Reconcile(ctx)
		}
	}
}

// Status returns the latest reconcile result.
func (a *Agent) Status() Status {
	if st := a.status.Load(); st != nil {
		return *st
	}
	return Status{}
}

func (a *Agent) poll(ctx context.Context) {
	if a.cfg.DesiredFile == "" {
		return
	}
	raw, err := os.ReadFile(a.cfg.DesiredFile)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		a.intake = err
		a.cfg.Logger.Warn("desired file", "err", err)
		return
	}
	doc, err := spec.Decode(bytes.NewReader(raw))
	if err != nil {
		a.intake = err
		a.cfg.Logger.Warn("desired file", "err", err)
		return
	}
	if err := a.SetDesired(ctx, doc); err != nil {
		a.intake = err
		return
	}
	a.intake = nil
}

func (a *Agent) load() {
	a.floor = a.readFloor()
	raw, err := os.ReadFile(a.desiredPath())
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		a.intake = err
		return
	}
	doc, err := spec.Decode(bytes.NewReader(raw))
	if err != nil {
		a.intake = err
		return
	}
	d, err := spec.Parse(doc)
	if err != nil {
		a.intake = err
		return
	}
	if d.Revision() < a.floor {
		a.intake = fmt.Errorf("%w: cache revision %d is below floor %d", ErrStaleRevision, d.Revision(), a.floor)
		return
	}
	if d.Revision() > a.floor {
		if err := a.writeFloor(d.Revision()); err != nil {
			a.intake = err
			return
		}
		a.floor = d.Revision()
	}
	a.cur.Store(&d)
}

func (a *Agent) desiredPath() string { return filepath.Join(a.cfg.StateDir, "desired.json") }

func (a *Agent) floorPath() string { return filepath.Join(a.cfg.StateDir, "revision") }

func (a *Agent) readFloor() uint64 {
	raw, err := os.ReadFile(a.floorPath())
	if err != nil {
		return 0
	}
	var n uint64
	_, err = fmt.Sscan(string(raw), &n)
	if err != nil {
		return 0
	}
	return n
}

func (a *Agent) writeFloor(n uint64) error {
	return atomicfile.Write(a.floorPath(), []byte(fmt.Sprintf("%d\n", n)))
}

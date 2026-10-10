// Package host observes and changes volumes, networks, and QEMU processes
// on one machine.
//
// Records are written before the resource they describe and removed after
// that resource is gone. An existing guest is reached only through the
// process name, socket, and NIC names stored in its launch record.
package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/popododo0720/vws-hypervisor/internal/reconcile"
	"github.com/popododo0720/vws-hypervisor/spec"
)

const maxSocketPath = 107

// ErrSizeMismatch means a backing file exists at a different size.
// The file is not resized.
var ErrSizeMismatch = errors.New("host: volume size does not match")

// ErrInUse means a volume or network is still referenced by a guest record
// or by a live process whose attachments are unknown.
var ErrInUse = errors.New("host: resource in use")

// Config configures a Host.
type Config struct {
	VolumeDir    string
	RunDir       string
	QEMU         string
	Sys          Sys
	Now          func() time.Time
	ProbeTimeout time.Duration
}

// Host mutates one machine. It stores no probe results between calls.
type Host struct {
	cfg Config
}

// Open creates the directory skeleton and checks that a QMP socket path for
// the longest id fits in a unix address.
func Open(cfg Config) (*Host, error) {
	if cfg.Sys == nil {
		return nil, errors.New("host: sys is required")
	}
	if !filepath.IsAbs(cfg.VolumeDir) || !filepath.IsAbs(cfg.RunDir) {
		return nil, errors.New("host: volume dir and run dir must be absolute")
	}
	if cfg.QEMU == "" {
		cfg.QEMU = "qemu-system-x86_64"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = 2 * time.Second
	}
	if socketBudget(cfg.RunDir) > maxSocketPath {
		return nil, fmt.Errorf("host: run dir %q is too long for a qmp socket", cfg.RunDir)
	}
	h := &Host{cfg: cfg}
	for _, dir := range []string{cfg.VolumeDir, h.vmRoot(), h.networkDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return h, nil
}

func socketBudget(runDir string) int {
	return len(runDir) + len("/vms/") + spec.MaxIDLen + len("/qmp-") + 8 + len(".sock")
}

func (h *Host) now() time.Time { return h.cfg.Now() }

func (h *Host) volumePath(id spec.VolumeID) string {
	return filepath.Join(h.cfg.VolumeDir, id.String()+".raw")
}

func (h *Host) vmRoot() string { return filepath.Join(h.cfg.RunDir, "vms") }

func (h *Host) vmDir(id spec.VMID) string {
	return filepath.Join(h.vmRoot(), id.String())
}

func (h *Host) launchPath(id spec.VMID) string {
	return filepath.Join(h.vmDir(id), "launch.json")
}

func (h *Host) networkDir() string { return filepath.Join(h.cfg.RunDir, "networks") }

func (h *Host) networkPath(id spec.NetworkID) string {
	return filepath.Join(h.networkDir(), id.String()+".json")
}

// Apply runs every step in the plan. cont is called before each step.
// A non-nil error from cont aborts the rest of the plan. A step error is
// collected and later steps still run, because one volume must not block
// another guest.
func (h *Host) Apply(ctx context.Context, p reconcile.Plan, cont func() error) error {
	var errs []error
	for _, step := range p.Steps() {
		if cont != nil {
			if err := cont(); err != nil {
				return err
			}
		}
		if err := h.applyOne(ctx, step); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h *Host) applyOne(ctx context.Context, step reconcile.Step) error {
	switch s := step.(type) {
	case reconcile.EnsureVolume:
		return h.ensureVolume(s.Volume)
	case reconcile.DeleteVolume:
		return h.deleteVolume(s.ID)
	case reconcile.EnsureNetwork:
		return h.ensureNetwork(ctx, s.Network)
	case reconcile.DeleteNetwork:
		return h.deleteNetwork(ctx, s.ID)
	case reconcile.StartVM:
		return h.startVM(ctx, s.VM)
	case reconcile.StopVM:
		return h.stopVM(ctx, s.ID, s.Force, s.Gen)
	case reconcile.DestroyVM:
		return h.destroyVM(ctx, s.ID)
	}
	return fmt.Errorf("host: unknown step %T", step)
}

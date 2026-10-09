// Package linuxsys is the production host.Sys. It asks systemd to own QEMU
// so the agent exiting does not signal guests, and it asks ip for links.
package linuxsys

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type runner interface {
	Start(ctx context.Context, argv []string) error
	Output(ctx context.Context, argv []string) ([]byte, error)
}

// Sys runs systemd-run and ip.
type Sys struct {
	run runner
}

// New returns a Sys that executes on this host.
func New() *Sys { return &Sys{run: execRunner{}} }

// Spawn starts argv in a transient unit named after the process.
// An already-active unit is left running.
func (s *Sys) Spawn(ctx context.Context, name string, argv []string) error {
	unit := name + ".service"
	if s.active(ctx, unit) {
		return nil
	}
	_ = s.run.Start(ctx, []string{"systemctl", "reset-failed", unit})
	cmd := append([]string{"systemd-run", "--unit=" + unit, "--collect", "--quiet", "--"}, argv...)
	return s.run.Start(ctx, cmd)
}

func (s *Sys) active(ctx context.Context, unit string) bool {
	out, err := s.run.Output(ctx, []string{"systemctl", "is-active", unit})
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(out))
	return state == "active" || state == "activating" || state == "deactivating"
}

// Kill sends SIGKILL to the unit. A missing unit is success.
func (s *Sys) Kill(ctx context.Context, name string) error {
	err := s.run.Start(ctx, []string{"systemctl", "kill", "--signal=SIGKILL", name + ".service"})
	if err == nil || missingUnit(err) {
		return nil
	}
	return err
}

// Processes lists vm-* units that still have a main process.
func (s *Sys) Processes(ctx context.Context) ([]string, error) {
	out, err := s.run.Output(ctx, []string{
		"systemctl", "list-units", "--plain", "--no-legend", "--all",
		"--state=active,activating,deactivating", "vm-*.service",
	})
	if err != nil {
		return nil, err
	}
	return parseUnits(string(out)), nil
}

// EnsureBridge creates a bridge and brings it up.
func (s *Sys) EnsureBridge(ctx context.Context, name string) error {
	_ = s.run.Start(ctx, []string{"ip", "link", "add", name, "type", "bridge"})
	return s.run.Start(ctx, []string{"ip", "link", "set", name, "up"})
}

// EnsureTap creates a tap and enslaves it. It does not set a MAC.
func (s *Sys) EnsureTap(ctx context.Context, name, bridge string) error {
	_ = s.run.Start(ctx, []string{"ip", "tuntap", "add", "dev", name, "mode", "tap"})
	if err := s.run.Start(ctx, []string{"ip", "link", "set", name, "master", bridge}); err != nil {
		return err
	}
	return s.run.Start(ctx, []string{"ip", "link", "set", name, "up"})
}

// DeleteLink deletes a link. A missing link is success.
func (s *Sys) DeleteLink(ctx context.Context, name string) error {
	err := s.run.Start(ctx, []string{"ip", "link", "del", name})
	if err == nil || missingLink(err) {
		return nil
	}
	return err
}

// Links lists link names.
func (s *Sys) Links(ctx context.Context) ([]string, error) {
	out, err := s.run.Output(ctx, []string{"ip", "-o", "link", "show"})
	if err != nil {
		return nil, err
	}
	return parseLinks(string(out)), nil
}

func parseUnits(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		unit := f[0]
		if !strings.HasPrefix(unit, "vm-") || !strings.HasSuffix(unit, ".service") {
			continue
		}
		names = append(names, strings.TrimSuffix(unit, ".service"))
	}
	return names
}

func parseLinks(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		name := strings.TrimSuffix(f[1], ":")
		if name != "" && name != "lo" {
			names = append(names, name)
		}
	}
	return names
}

func missingUnit(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not found") || strings.Contains(msg, "not loaded") || strings.Contains(msg, "not-found")
}

func missingLink(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Cannot find device") || strings.Contains(msg, "does not exist")
}

type execRunner struct{}

func (execRunner) Start(ctx context.Context, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", argv[0], err, out)
	}
	return nil
}

func (execRunner) Output(ctx context.Context, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", argv[0], err, out)
	}
	return out, nil
}

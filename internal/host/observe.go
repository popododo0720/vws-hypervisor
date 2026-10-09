package host

import (
	"context"
	"errors"
	"os"

	"github.com/popododo0720/vws-hypervisor/internal/qmp"
	"github.com/popododo0720/vws-hypervisor/internal/reconcile"
	"github.com/popododo0720/vws-hypervisor/spec"
)

// Observe probes the host. A listing error fails the whole call.
// A guest whose QMP identity check fails is Unresponsive, not absent.
func (h *Host) Observe(ctx context.Context) (reconcile.Observed, error) {
	procs, err := h.cfg.Sys.Processes(ctx)
	if err != nil {
		return reconcile.Observed{}, err
	}
	links, err := h.cfg.Sys.Links(ctx)
	if err != nil {
		return reconcile.Observed{}, err
	}
	linkSet := map[string]struct{}{}
	for _, name := range links {
		linkSet[name] = struct{}{}
	}
	vols, err := h.readVolumes()
	if err != nil {
		return reconcile.Observed{}, err
	}
	nets, err := h.readNetworks(linkSet)
	if err != nil {
		return reconcile.Observed{}, err
	}
	vms, err := h.readVMs(ctx, procs)
	if err != nil {
		return reconcile.Observed{}, err
	}
	return reconcile.Observed{VMs: vms, Volumes: vols, Networks: nets}, nil
}

func (h *Host) readVMs(ctx context.Context, procs []string) (map[spec.VMID]reconcile.VM, error) {
	ids := map[spec.VMID]string{}
	entries, err := os.ReadDir(h.vmRoot())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, err := spec.ParseVMID(e.Name())
		if err != nil {
			continue
		}
		ids[id] = ""
	}
	for _, name := range procs {
		id, ok := matchProcess(name)
		if !ok {
			continue
		}
		if _, seen := ids[id]; !seen {
			ids[id] = name
		} else if ids[id] == "" {
			ids[id] = name
		}
	}
	out := map[spec.VMID]reconcile.VM{}
	for id, listed := range ids {
		vm, err := h.probeVM(ctx, id, listed, procs)
		if err != nil {
			return nil, err
		}
		out[id] = vm
	}
	return out, nil
}

func (h *Host) probeVM(ctx context.Context, id spec.VMID, listed string, procs []string) (reconcile.VM, error) {
	rec, has, err := h.readLaunch(id)
	if err != nil {
		return reconcile.VM{}, err
	}
	if listed == "" {
		if has && contains(procs, rec.Process) {
			listed = rec.Process
		} else if name, ok := findProcess(procs, id); ok {
			listed = name
		}
	}
	var obs reconcile.VM
	obs.Process = listed
	if !has || rec.Schema != recordSchema {
		obs.State = reconcile.Unresponsive
		if listed == "" {
			up := false
			if has && rec.Socket != "" {
				up, _, _, _ = h.inspect(ctx, rec.Socket)
			}
			if !up {
				obs.State = reconcile.Off
			}
		}
		return obs, nil
	}
	launch, ok := rec.launch()
	if !ok {
		obs.State = reconcile.Unresponsive
		return obs, nil
	}
	obs.SpecKnown = true
	obs.Spec = launch
	obs.AttachmentsKnown = true
	obs.Volumes = append([]spec.VolumeID{launch.Boot}, launch.Extra...)
	obs.Networks = append([]spec.NetworkID(nil), launch.Networks...)
	obs.PowerdownAt = rec.PowerdownAt
	obs.StopGen = rec.StopGen

	up, st, uuid, qerr := h.inspect(ctx, rec.Socket)
	alive := listed != "" || up || qerr != nil
	if !alive {
		obs.State = reconcile.Off
		return obs, nil
	}
	if qerr != nil || uuid != rec.UUID {
		obs.State = reconcile.Unresponsive
		return obs, nil
	}
	switch st {
	case qmp.StateRunning:
		obs.State = reconcile.Running
	case qmp.StatePrelaunch:
		obs.State = reconcile.Prelaunch
	case qmp.StatePaused:
		obs.State = reconcile.Paused
	case qmp.StateShutdown:
		obs.State = reconcile.Shutdown
	default:
		obs.State = reconcile.Unresponsive
	}
	return obs, nil
}

func (h *Host) inspect(ctx context.Context, socket string) (bool, qmp.State, string, error) {
	if socket == "" {
		return false, qmp.StateOther, "", nil
	}
	ctx, cancel := context.WithTimeout(ctx, h.cfg.ProbeTimeout)
	defer cancel()
	c, err := qmp.Dial(ctx, socket)
	if err != nil {
		return false, qmp.StateOther, "", nil
	}
	defer func() { _ = c.Close() }()
	uuid, err := c.UUID(ctx)
	if err != nil {
		return true, qmp.StateOther, "", err
	}
	st, err := c.Status(ctx)
	if err != nil {
		return true, qmp.StateOther, uuid, err
	}
	return true, st, uuid, nil
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func findProcess(procs []string, id spec.VMID) (string, bool) {
	for _, name := range procs {
		got, ok := matchProcess(name)
		if ok && got == id {
			return name, true
		}
	}
	return "", false
}

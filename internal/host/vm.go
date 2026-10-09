package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/popododo0720/vws-hypervisor/internal/qmp"
	"github.com/popododo0720/vws-hypervisor/spec"
)

func (h *Host) startVM(ctx context.Context, vm spec.VM) error {
	rec, has, err := h.readLaunch(vm.ID)
	if err != nil {
		return err
	}
	alive, err := h.alive(ctx, vm.ID, rec, has)
	if err != nil {
		return err
	}
	if alive {
		return h.adopt(ctx, vm, rec, has)
	}
	if has {
		// The pinned process is gone and the pinned socket did not answer.
		// Only then is it safe to drop the old taps and socket path.
		_ = os.Remove(rec.Socket)
		if err := h.deleteTaps(ctx, rec); err != nil {
			return err
		}
	}
	bridges := map[spec.NetworkID]string{}
	for _, id := range vm.Networks {
		nrec, ok, err := h.readNet(id)
		if err != nil {
			return err
		}
		if !ok || nrec.Schema != recordSchema || nrec.Bridge == "" {
			return fmt.Errorf("host: network %s is not ready", id)
		}
		bridges[id] = nrec.Bridge
	}
	rec, err = mintRecord(h.cfg.RunDir, vm, bridges)
	if err != nil {
		return err
	}
	if err := h.writeLaunch(vm.ID, rec); err != nil {
		return err
	}
	if err := h.ensureNICs(ctx, rec); err != nil {
		return err
	}
	return h.cfg.Sys.Spawn(ctx, rec.Process, qemuArgs(h.cfg.QEMU, rec, h.disks(vm)))
}

func (h *Host) adopt(ctx context.Context, vm spec.VM, rec launchRecord, has bool) error {
	if !has || rec.Schema != recordSchema {
		return errors.New("host: live guest has no launch record")
	}
	launch, ok := rec.launch()
	if !ok || !launch.Equal(vm.Launch()) {
		return errors.New("host: live guest launch spec differs")
	}
	up, st, uuid, err := h.inspect(ctx, rec.Socket)
	if err != nil || !up {
		return errors.New("host: live guest did not answer qmp")
	}
	if uuid != rec.UUID {
		return errors.New("host: qmp uuid does not match launch record")
	}
	if st == qmp.StatePrelaunch {
		return h.cont(ctx, rec)
	}
	return nil
}

func (h *Host) alive(ctx context.Context, id spec.VMID, rec launchRecord, has bool) (bool, error) {
	procs, err := h.cfg.Sys.Processes(ctx)
	if err != nil {
		return false, err
	}
	if has && contains(procs, rec.Process) {
		return true, nil
	}
	if _, ok := findProcess(procs, id); ok {
		return true, nil
	}
	if has && rec.Socket != "" {
		up, _, _, qerr := h.inspect(ctx, rec.Socket)
		if qerr != nil || up {
			return true, nil
		}
	}
	return false, nil
}

func (h *Host) cont(ctx context.Context, rec launchRecord) error {
	ctx, cancel := context.WithTimeout(ctx, h.cfg.ProbeTimeout)
	defer cancel()
	c, err := qmp.Dial(ctx, rec.Socket)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return c.Cont(ctx)
}

func (h *Host) stopVM(ctx context.Context, id spec.VMID, force bool, gen string) error {
	rec, has, err := h.readLaunch(id)
	if err != nil {
		return err
	}
	procs, err := h.cfg.Sys.Processes(ctx)
	if err != nil {
		return err
	}
	if !has || rec.Schema != recordSchema {
		if name, ok := findProcess(procs, id); ok {
			return h.cfg.Sys.Kill(ctx, name)
		}
		return nil
	}
	alive := contains(procs, rec.Process)
	if !alive {
		up, _, _, qerr := h.inspect(ctx, rec.Socket)
		alive = up || qerr != nil
	}
	if !alive {
		return nil
	}
	if force {
		return h.cfg.Sys.Kill(ctx, rec.Process)
	}
	if rec.StopGen != gen {
		rec.StopGen = gen
		rec.PowerdownAt = h.now()
		if err := h.writeLaunch(id, rec); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, h.cfg.ProbeTimeout)
	defer cancel()
	c, err := qmp.Dial(ctx, rec.Socket)
	if err != nil {
		return nil
	}
	defer func() { _ = c.Close() }()
	_ = c.Powerdown(ctx)
	return nil
}

func (h *Host) destroyVM(ctx context.Context, id spec.VMID) error {
	rec, has, err := h.readLaunch(id)
	if err != nil {
		return err
	}
	procs, err := h.cfg.Sys.Processes(ctx)
	if err != nil {
		return err
	}
	if has && rec.Process != "" && contains(procs, rec.Process) {
		if err := h.cfg.Sys.Kill(ctx, rec.Process); err != nil {
			return err
		}
	} else if name, ok := findProcess(procs, id); ok {
		if err := h.cfg.Sys.Kill(ctx, name); err != nil {
			return err
		}
	}
	if has {
		if err := h.deleteTaps(ctx, rec); err != nil {
			return err
		}
		_ = os.Remove(rec.Socket)
	}
	if err := os.RemoveAll(h.vmDir(id)); err != nil {
		return err
	}
	return nil
}

func (h *Host) deleteTaps(ctx context.Context, rec launchRecord) error {
	for _, nic := range rec.NICs {
		if nic.Tap == "" {
			continue
		}
		if err := h.cfg.Sys.DeleteLink(ctx, nic.Tap); err != nil {
			return err
		}
	}
	return nil
}

func (h *Host) ensureNICs(ctx context.Context, rec launchRecord) error {
	seen := map[string]struct{}{}
	for _, nic := range rec.NICs {
		if _, ok := seen[nic.Bridge]; !ok {
			if err := h.cfg.Sys.EnsureBridge(ctx, nic.Bridge); err != nil {
				return err
			}
			seen[nic.Bridge] = struct{}{}
		}
		if err := h.cfg.Sys.EnsureTap(ctx, nic.Tap, nic.Bridge); err != nil {
			return err
		}
	}
	return nil
}

func (h *Host) disks(vm spec.VM) []string {
	paths := []string{h.volumePath(vm.BootVolume)}
	for _, id := range vm.Volumes {
		paths = append(paths, h.volumePath(id))
	}
	return paths
}

func qemuArgs(bin string, rec launchRecord, disks []string) []string {
	args := []string{
		bin,
		"-name", rec.Process,
		"-uuid", rec.UUID,
		"-smp", strconv.Itoa(rec.VCPUs),
		"-m", strconv.Itoa(rec.MemoryMiB),
		"-qmp", "unix:" + rec.Socket + ",server,nowait",
		"-display", "none",
		"-vga", "none",
	}
	for i, disk := range disks {
		id := "drv" + strconv.Itoa(i)
		args = append(args,
			"-drive", "file="+disk+",format=raw,if=none,id="+id,
			"-device", "virtio-blk-pci,drive="+id,
		)
	}
	for i, nic := range rec.NICs {
		id := "n" + strconv.Itoa(i)
		args = append(args,
			"-netdev", "tap,id="+id+",ifname="+nic.Tap+",script=no,downscript=no",
			"-device", "virtio-net-pci,netdev="+id+",mac="+nic.MAC,
		)
	}
	return args
}

package host

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/popododo0720/vws-hypervisor/internal/reconcile"
	"github.com/popododo0720/vws-hypervisor/spec"
)

func (h *Host) readNetworks(links map[string]struct{}) (map[spec.NetworkID]reconcile.Network, error) {
	entries, err := os.ReadDir(h.networkDir())
	if errors.Is(err, os.ErrNotExist) {
		return map[spec.NetworkID]reconcile.Network{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[spec.NetworkID]reconcile.Network{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		id, err := spec.ParseNetworkID(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		rec, has, err := h.readNet(id)
		if err != nil || !has {
			return nil, err
		}
		ready := rec.Schema == recordSchema && rec.Bridge != ""
		if ready {
			_, ready = links[rec.Bridge]
		}
		out[id] = reconcile.Network{Ready: ready}
	}
	return out, nil
}

func (h *Host) ensureNetwork(ctx context.Context, n spec.Network) error {
	rec, has, err := h.readNet(n.ID)
	if err != nil {
		return err
	}
	if !has || rec.Schema != recordSchema || rec.Bridge == "" {
		rec = newNetworkRecord(n.ID)
		if err := h.writeNet(n.ID, rec); err != nil {
			return err
		}
	}
	if err := h.cfg.Sys.EnsureBridge(ctx, rec.Bridge); err != nil {
		return err
	}
	return h.reattachTaps(ctx, n.ID, rec.Bridge)
}

func (h *Host) reattachTaps(ctx context.Context, id spec.NetworkID, bridge string) error {
	entries, err := os.ReadDir(h.vmRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		vmID, err := spec.ParseVMID(e.Name())
		if err != nil {
			continue
		}
		rec, has, err := h.readLaunch(vmID)
		if err != nil || !has || rec.Schema != recordSchema {
			continue
		}
		for _, nic := range rec.NICs {
			if nic.Network != id.String() {
				continue
			}
			if err := h.cfg.Sys.EnsureTap(ctx, nic.Tap, bridge); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *Host) deleteNetwork(ctx context.Context, id spec.NetworkID) error {
	inUse, err := h.networkInUse(id)
	if err != nil {
		return err
	}
	if inUse {
		return ErrInUse
	}
	rec, has, err := h.readNet(id)
	if err != nil {
		return err
	}
	if has && rec.Bridge != "" {
		if err := h.cfg.Sys.DeleteLink(ctx, rec.Bridge); err != nil {
			return err
		}
	}
	err = os.Remove(h.networkPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (h *Host) networkInUse(id spec.NetworkID) (bool, error) {
	entries, err := os.ReadDir(h.vmRoot())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		vmID, err := spec.ParseVMID(e.Name())
		if err != nil {
			return true, nil
		}
		rec, has, err := h.readLaunch(vmID)
		if err != nil {
			return false, err
		}
		if !has || rec.Schema != recordSchema {
			return true, nil
		}
		for _, nic := range rec.NICs {
			if nic.Network == id.String() {
				return true, nil
			}
		}
	}
	return false, nil
}

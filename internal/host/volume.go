package host

import (
	"errors"
	"os"
	"strings"

	"github.com/popododo0720/vws-hypervisor/internal/reconcile"
	"github.com/popododo0720/vws-hypervisor/spec"
)

func (h *Host) readVolumes() (map[spec.VolumeID]reconcile.Volume, error) {
	entries, err := os.ReadDir(h.cfg.VolumeDir)
	if err != nil {
		return nil, err
	}
	out := map[spec.VolumeID]reconcile.Volume{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".raw") {
			continue
		}
		id, err := spec.ParseVolumeID(strings.TrimSuffix(name, ".raw"))
		if err != nil {
			continue
		}
		fi, err := os.Stat(h.volumePath(id))
		if err != nil {
			return nil, err
		}
		out[id] = reconcile.Volume{SizeBytes: fi.Size()}
	}
	return out, nil
}

func (h *Host) ensureVolume(v spec.Volume) error {
	path := h.volumePath(v.ID)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		fi, statErr := os.Stat(path)
		if statErr != nil {
			return statErr
		}
		if fi.Size() != v.SizeBytes {
			return ErrSizeMismatch
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Truncate(v.SizeBytes)
}

func (h *Host) deleteVolume(id spec.VolumeID) error {
	inUse, err := h.volumeInUse(id)
	if err != nil {
		return err
	}
	if inUse {
		return ErrInUse
	}
	err = os.Remove(h.volumePath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (h *Host) volumeInUse(id spec.VolumeID) (bool, error) {
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
		for _, vol := range rec.volumeIDs() {
			if vol == id.String() {
				return true, nil
			}
		}
	}
	return false, nil
}

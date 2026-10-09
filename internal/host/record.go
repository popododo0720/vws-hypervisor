package host

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/popododo0720/vws-hypervisor/internal/atomicfile"
	"github.com/popododo0720/vws-hypervisor/spec"
)

const recordSchema = 1

type nicRecord struct {
	Network string `json:"network"`
	Tap     string `json:"tap"`
	MAC     string `json:"mac"`
	Bridge  string `json:"bridge"`
}

// launchRecord is the pin for one QEMU process. Observe and Apply use these
// fields for an existing guest. They are filled in only when the record is minted.
type launchRecord struct {
	Schema      int         `json:"schema"`
	Process     string      `json:"process"`
	Socket      string      `json:"socket"`
	UUID        string      `json:"uuid"`
	NICs        []nicRecord `json:"nics"`
	VCPUs       int         `json:"vcpus"`
	MemoryMiB   int         `json:"memory_mib"`
	Boot        string      `json:"boot"`
	Extra       []string    `json:"extra"`
	Networks    []string    `json:"networks"`
	PowerdownAt time.Time   `json:"powerdown_at,omitempty"`
	StopGen     string      `json:"stop_gen,omitempty"`
}

type netRecord struct {
	Schema int    `json:"schema"`
	Bridge string `json:"bridge"`
}

func (r launchRecord) launch() (spec.Launch, bool) {
	if r.Schema != recordSchema {
		return spec.Launch{}, false
	}
	boot, err := spec.ParseVolumeID(r.Boot)
	if err != nil {
		return spec.Launch{}, false
	}
	extra := make([]spec.VolumeID, 0, len(r.Extra))
	for _, s := range r.Extra {
		id, err := spec.ParseVolumeID(s)
		if err != nil {
			return spec.Launch{}, false
		}
		extra = append(extra, id)
	}
	nets := make([]spec.NetworkID, 0, len(r.Networks))
	for _, s := range r.Networks {
		id, err := spec.ParseNetworkID(s)
		if err != nil {
			return spec.Launch{}, false
		}
		nets = append(nets, id)
	}
	return spec.Launch{VCPUs: r.VCPUs, MemoryMiB: r.MemoryMiB, Boot: boot, Extra: extra, Networks: nets}, true
}

func (r launchRecord) volumeIDs() []string {
	out := []string{}
	if r.Boot != "" {
		out = append(out, r.Boot)
	}
	return append(out, r.Extra...)
}

func (h *Host) readLaunch(id spec.VMID) (launchRecord, bool, error) {
	raw, err := os.ReadFile(h.launchPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return launchRecord{}, false, nil
	}
	if err != nil {
		return launchRecord{}, false, err
	}
	var rec launchRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return launchRecord{}, true, err
	}
	return rec, true, nil
}

func (h *Host) writeLaunch(id spec.VMID, rec launchRecord) error {
	if err := os.MkdirAll(h.vmDir(id), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return atomicfile.Write(h.launchPath(id), raw)
}

func (h *Host) readNet(id spec.NetworkID) (netRecord, bool, error) {
	raw, err := os.ReadFile(h.networkPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return netRecord{}, false, nil
	}
	if err != nil {
		return netRecord{}, false, err
	}
	var rec netRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return netRecord{}, true, err
	}
	return rec, true, nil
}

func (h *Host) writeNet(id spec.NetworkID, rec netRecord) error {
	if err := os.MkdirAll(h.networkDir(), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return atomicfile.Write(h.networkPath(id), raw)
}

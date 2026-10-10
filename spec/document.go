package spec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Power is the desired power state of a VM.
type Power string

const (
	// PowerRunning means the guest should be running.
	PowerRunning Power = "running"
	// PowerStopped means no QEMU process should exist. Volumes stay.
	PowerStopped Power = "stopped"
)

// Document is a desired-state document as a caller produces it.
// Decode validates ids and power. Parse validates references.
type Document struct {
	Revision uint64    `json:"revision"`
	Volumes  []Volume  `json:"volumes,omitempty"`
	Networks []Network `json:"networks,omitempty"`
	VMs      []VM      `json:"vms,omitempty"`
}

// Volume is a raw backing file. An existing file is never resized.
type Volume struct {
	ID        VolumeID `json:"id"`
	SizeBytes int64    `json:"size_bytes"`
}

// Network is an L2 segment. The host bridge name is pinned when the network
// is first created and is not part of this document.
type Network struct {
	ID NetworkID `json:"id"`
}

// VM is one guest. Volumes and Networks keep guest device order.
// Nil and empty attachment lists are the same desired state.
type VM struct {
	ID         VMID        `json:"id"`
	VCPUs      int         `json:"vcpus"`
	MemoryMiB  int         `json:"memory_mib"`
	BootVolume VolumeID    `json:"boot_volume"`
	Volumes    []VolumeID  `json:"volumes,omitempty"`
	Networks   []NetworkID `json:"networks,omitempty"`
	Power      Power       `json:"power"`
}

// Launch is the part of a VM that requires a new QEMU process.
// Comparison is structural. A hash is not used, because a hash changes when
// the encoding changes and would restart guests after an agent upgrade.
type Launch struct {
	VCPUs     int
	MemoryMiB int
	Boot      VolumeID
	Extra     []VolumeID
	Networks  []NetworkID
}

// Launch returns the launch identity of vm. Nil and empty slices compare equal.
func (vm VM) Launch() Launch {
	return Launch{
		VCPUs:     vm.VCPUs,
		MemoryMiB: vm.MemoryMiB,
		Boot:      vm.BootVolume,
		Extra:     cloneVol(vm.Volumes),
		Networks:  cloneNet(vm.Networks),
	}
}

// Equal reports whether l and o would produce the same QEMU invocation,
// including attachment order.
func (l Launch) Equal(o Launch) bool {
	if l.VCPUs != o.VCPUs || l.MemoryMiB != o.MemoryMiB || l.Boot != o.Boot {
		return false
	}
	if len(l.Extra) != len(o.Extra) || len(l.Networks) != len(o.Networks) {
		return false
	}
	for i := range l.Extra {
		if l.Extra[i] != o.Extra[i] {
			return false
		}
	}
	for i := range l.Networks {
		if l.Networks[i] != o.Networks[i] {
			return false
		}
	}
	return true
}

// Decode reads one JSON document. Unknown fields and trailing data are errors.
func Decode(r io.Reader) (Document, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var doc Document
	if err := dec.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("spec: decode document: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Document{}, errors.New("spec: trailing data after document")
		}
		return Document{}, fmt.Errorf("spec: decode document: %w", err)
	}
	return doc, nil
}

func cloneVol(in []VolumeID) []VolumeID {
	if len(in) == 0 {
		return []VolumeID{}
	}
	return append([]VolumeID(nil), in...)
}

func cloneNet(in []NetworkID) []NetworkID {
	if len(in) == 0 {
		return []NetworkID{}
	}
	return append([]NetworkID(nil), in...)
}

// canonical is the bytes Parse hashes and the agent writes to disk.
// Top-level lists are sorted by id. Per-VM attachment order is preserved.
func canonical(d Desired) ([]byte, error) {
	body := canonDoc{
		Revision: d.Revision(),
		Volumes:  []canonVol{},
		Networks: []canonNet{},
		VMs:      []canonVM{},
	}
	for _, v := range d.Volumes() {
		body.Volumes = append(body.Volumes, canonVol{ID: v.ID.String(), SizeBytes: v.SizeBytes})
	}
	for _, n := range d.Networks() {
		body.Networks = append(body.Networks, canonNet{ID: n.ID.String()})
	}
	for _, vm := range d.VMs() {
		extra := make([]string, len(vm.Volumes))
		for i, id := range vm.Volumes {
			extra[i] = id.String()
		}
		nets := make([]string, len(vm.Networks))
		for i, id := range vm.Networks {
			nets[i] = id.String()
		}
		body.VMs = append(body.VMs, canonVM{
			ID:         vm.ID.String(),
			VCPUs:      vm.VCPUs,
			MemoryMiB:  vm.MemoryMiB,
			BootVolume: vm.BootVolume.String(),
			Volumes:    extra,
			Networks:   nets,
			Power:      string(vm.Power),
		})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

type canonDoc struct {
	Revision uint64     `json:"revision"`
	Volumes  []canonVol `json:"volumes"`
	Networks []canonNet `json:"networks"`
	VMs      []canonVM  `json:"vms"`
}

type canonVol struct {
	ID        string `json:"id"`
	SizeBytes int64  `json:"size_bytes"`
}

type canonNet struct {
	ID string `json:"id"`
}

type canonVM struct {
	ID         string   `json:"id"`
	VCPUs      int      `json:"vcpus"`
	MemoryMiB  int      `json:"memory_mib"`
	BootVolume string   `json:"boot_volume"`
	Volumes    []string `json:"volumes"`
	Networks   []string `json:"networks"`
	Power      string   `json:"power"`
}

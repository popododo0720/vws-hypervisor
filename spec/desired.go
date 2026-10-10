package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	minMemoryMiB = 128
	maxMemoryMiB = 4 << 20
	maxVCPU      = 256
	mib          = 1 << 20
)

// Desired is a validated document. The zero value means nothing has been
// accepted. It is not a request to remove workloads. Parse is the only
// constructor, so a non-zero Desired has already passed validation.
type Desired struct {
	d *desired
}

type desired struct {
	revision uint64
	raw      []byte
	digest   string

	vms      map[VMID]VM
	volumes  map[VolumeID]Volume
	networks map[NetworkID]Network

	vmOrder      []VMID
	volumeOrder  []VolumeID
	networkOrder []NetworkID
}

// Parse validates doc. The error joins every problem it finds.
// An empty resource list with a positive revision is a real document.
func Parse(doc Document) (Desired, error) {
	var probs []error
	if doc.Revision == 0 {
		probs = append(probs, errors.New("spec: revision must be positive"))
	}
	d := &desired{
		revision: doc.Revision,
		vms:      map[VMID]VM{},
		volumes:  map[VolumeID]Volume{},
		networks: map[NetworkID]Network{},
	}
	seenVol := map[VolumeID]struct{}{}
	for _, v := range doc.Volumes {
		if v.ID.IsZero() {
			probs = append(probs, errors.New("spec: volume missing id"))
			continue
		}
		if _, ok := d.volumes[v.ID]; ok {
			probs = append(probs, fmt.Errorf("spec: duplicate volume %s", v.ID))
			continue
		}
		if v.SizeBytes <= 0 || v.SizeBytes%mib != 0 {
			probs = append(probs, fmt.Errorf("spec: volume %s size must be a positive multiple of 1 MiB", v.ID))
		}
		d.volumes[v.ID] = v
		d.volumeOrder = append(d.volumeOrder, v.ID)
	}
	for _, n := range doc.Networks {
		if n.ID.IsZero() {
			probs = append(probs, errors.New("spec: network missing id"))
			continue
		}
		if _, ok := d.networks[n.ID]; ok {
			probs = append(probs, fmt.Errorf("spec: duplicate network %s", n.ID))
			continue
		}
		d.networks[n.ID] = n
		d.networkOrder = append(d.networkOrder, n.ID)
	}
	for _, vm := range doc.VMs {
		if vm.ID.IsZero() {
			probs = append(probs, errors.New("spec: vm missing id"))
			continue
		}
		if _, ok := d.vms[vm.ID]; ok {
			probs = append(probs, fmt.Errorf("spec: duplicate vm %s", vm.ID))
			continue
		}
		if vm.VCPUs < 1 || vm.VCPUs > maxVCPU {
			probs = append(probs, fmt.Errorf("spec: vm %s vcpus out of range", vm.ID))
		}
		if vm.MemoryMiB < minMemoryMiB || vm.MemoryMiB > maxMemoryMiB {
			probs = append(probs, fmt.Errorf("spec: vm %s memory out of range", vm.ID))
		}
		if vm.Power != PowerRunning && vm.Power != PowerStopped {
			probs = append(probs, fmt.Errorf("spec: vm %s power %q", vm.ID, vm.Power))
		}
		if vm.BootVolume.IsZero() {
			probs = append(probs, fmt.Errorf("spec: vm %s missing boot volume", vm.ID))
		} else if _, ok := d.volumes[vm.BootVolume]; !ok {
			probs = append(probs, fmt.Errorf("spec: vm %s boot volume %s not declared", vm.ID, vm.BootVolume))
		} else {
			if _, ok := seenVol[vm.BootVolume]; ok {
				probs = append(probs, fmt.Errorf("spec: volume %s attached more than once", vm.BootVolume))
			}
			seenVol[vm.BootVolume] = struct{}{}
		}
		vm.Volumes = cloneVol(vm.Volumes)
		vm.Networks = cloneNet(vm.Networks)
		for _, id := range vm.Volumes {
			if id == vm.BootVolume || id.IsZero() {
				probs = append(probs, fmt.Errorf("spec: vm %s extra volume %s", vm.ID, id))
				continue
			}
			if _, ok := d.volumes[id]; !ok {
				probs = append(probs, fmt.Errorf("spec: vm %s volume %s not declared", vm.ID, id))
				continue
			}
			if _, ok := seenVol[id]; ok {
				probs = append(probs, fmt.Errorf("spec: volume %s attached more than once", id))
			}
			seenVol[id] = struct{}{}
		}
		seenNet := map[NetworkID]struct{}{}
		for _, id := range vm.Networks {
			if id.IsZero() {
				probs = append(probs, fmt.Errorf("spec: vm %s network missing id", vm.ID))
				continue
			}
			if _, ok := d.networks[id]; !ok {
				probs = append(probs, fmt.Errorf("spec: vm %s network %s not declared", vm.ID, id))
				continue
			}
			if _, ok := seenNet[id]; ok {
				probs = append(probs, fmt.Errorf("spec: vm %s network %s repeated", vm.ID, id))
			}
			seenNet[id] = struct{}{}
		}
		d.vms[vm.ID] = vm
		d.vmOrder = append(d.vmOrder, vm.ID)
	}
	if err := errors.Join(probs...); err != nil {
		return Desired{}, err
	}
	slices.SortFunc(d.volumeOrder, func(a, b VolumeID) int { return strings.Compare(a.String(), b.String()) })
	slices.SortFunc(d.networkOrder, func(a, b NetworkID) int { return strings.Compare(a.String(), b.String()) })
	slices.SortFunc(d.vmOrder, func(a, b VMID) int { return strings.Compare(a.String(), b.String()) })
	out := Desired{d: d}
	raw, err := canonical(out)
	if err != nil {
		return Desired{}, err
	}
	sum := sha256.Sum256(raw)
	d.raw = raw
	d.digest = hex.EncodeToString(sum[:])
	return out, nil
}

// IsZero reports whether d is the zero Desired.
func (d Desired) IsZero() bool { return d.d == nil }

// Revision returns the document revision. It is zero when d is zero.
func (d Desired) Revision() uint64 {
	if d.d == nil {
		return 0
	}
	return d.d.revision
}

// Digest identifies the canonical document. It is empty when d is zero.
func (d Desired) Digest() string {
	if d.d == nil {
		return ""
	}
	return d.d.digest
}

// MarshalCanonical returns the canonical JSON document.
func (d Desired) MarshalCanonical() ([]byte, error) {
	if d.d == nil {
		return nil, errors.New("spec: zero desired has no canonical form")
	}
	return append([]byte(nil), d.d.raw...), nil
}

// VMs returns VMs sorted by id.
func (d Desired) VMs() []VM {
	if d.d == nil {
		return nil
	}
	out := make([]VM, len(d.d.vmOrder))
	for i, id := range d.d.vmOrder {
		out[i] = d.d.vms[id]
	}
	return out
}

// VM returns the VM id.
func (d Desired) VM(id VMID) (VM, bool) {
	if d.d == nil {
		return VM{}, false
	}
	vm, ok := d.d.vms[id]
	return vm, ok
}

// Volumes returns volumes sorted by id.
func (d Desired) Volumes() []Volume {
	if d.d == nil {
		return nil
	}
	out := make([]Volume, len(d.d.volumeOrder))
	for i, id := range d.d.volumeOrder {
		out[i] = d.d.volumes[id]
	}
	return out
}

// Volume returns the volume id.
func (d Desired) Volume(id VolumeID) (Volume, bool) {
	if d.d == nil {
		return Volume{}, false
	}
	v, ok := d.d.volumes[id]
	return v, ok
}

// Networks returns networks sorted by id.
func (d Desired) Networks() []Network {
	if d.d == nil {
		return nil
	}
	out := make([]Network, len(d.d.networkOrder))
	for i, id := range d.d.networkOrder {
		out[i] = d.d.networks[id]
	}
	return out
}

// Network returns the network id.
func (d Desired) Network(id NetworkID) (Network, bool) {
	if d.d == nil {
		return Network{}, false
	}
	n, ok := d.d.networks[id]
	return n, ok
}

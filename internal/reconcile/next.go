package reconcile

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/popododo0720/vws-hypervisor/spec"
)

const (
	genStop   = "stop"
	genAbsent = "absent"
)

// Next returns the mutations that move o toward d at time now.
// The zero Desired yields an empty plan. A non-zero Desired with no resources
// removes whatever o still has.
//
// A VM that is desired running is stopped only when its launch spec is known
// and differs. An unknown spec is left running. Start is emitted only for a
// guest that is absent or Off, or for a prelaunch guest whose spec matches.
func Next(d spec.Desired, o Observed, now time.Time) Plan {
	if d.IsZero() {
		return Plan{}
	}
	if o.VMs == nil {
		o.VMs = map[spec.VMID]VM{}
	}
	if o.Volumes == nil {
		o.Volumes = map[spec.VolumeID]Volume{}
	}
	if o.Networks == nil {
		o.Networks = map[spec.NetworkID]Network{}
	}
	var b planBuilder
	unknown := false
	held := map[spec.VolumeID][]spec.VMID{}
	for id, vm := range o.VMs {
		if !vm.AttachmentsKnown {
			unknown = true
			continue
		}
		for _, vol := range vm.Volumes {
			held[vol] = append(held[vol], id)
		}
	}
	for _, v := range d.Volumes() {
		ov, ok := o.Volumes[v.ID]
		if !ok {
			b.step(EnsureVolume{Volume: v})
			continue
		}
		if ov.SizeBytes != v.SizeBytes {
			b.wait(Ref{KindVolume, v.ID.String()}, WaitSizeMismatch)
		}
	}
	for id := range o.Volumes {
		if _, ok := d.Volume(id); ok {
			continue
		}
		if unknown || len(held[id]) > 0 {
			b.wait(Ref{KindVolume, id.String()}, WaitInUse)
			continue
		}
		b.step(DeleteVolume{ID: id})
	}
	for _, n := range d.Networks() {
		on, ok := o.Networks[n.ID]
		if !ok || !on.Ready {
			b.step(EnsureNetwork{Network: n})
		}
	}
	for id := range o.Networks {
		if _, ok := d.Network(id); ok {
			continue
		}
		if networkInUse(o, id) || unknown {
			b.wait(Ref{KindNetwork, id.String()}, WaitInUse)
			continue
		}
		b.step(DeleteNetwork{ID: id})
	}
	for _, vm := range d.VMs() {
		ovm, ok := o.VMs[vm.ID]
		if vm.Power == spec.PowerStopped {
			b.wantStopped(vm, ovm, ok, now)
			continue
		}
		b.wantRunning(o, vm, ovm, ok, now, held, unknown)
	}
	for id, ovm := range o.VMs {
		if _, ok := d.VM(id); ok {
			continue
		}
		if ovm.State == Off && ovm.AttachmentsKnown {
			b.step(DestroyVM{ID: id})
			continue
		}
		b.stop(id, ovm, now, genAbsent)
	}
	return b.plan()
}

func networkInUse(o Observed, id spec.NetworkID) bool {
	for _, vm := range o.VMs {
		if !vm.AttachmentsKnown || vm.State == Off {
			continue
		}
		for _, n := range vm.Networks {
			if n == id {
				return true
			}
		}
	}
	return false
}

type planBuilder struct {
	steps []Step
	waits []Wait
}

func (b *planBuilder) step(s Step) { b.steps = append(b.steps, s) }

func (b *planBuilder) wait(ref Ref, reason WaitReason) {
	b.waits = append(b.waits, Wait{Target: ref, Reason: reason})
}

func (b *planBuilder) plan() Plan {
	slices.SortFunc(b.steps, func(a, c Step) int {
		if ra, rc := rank(a), rank(c); ra != rc {
			return ra - rc
		}
		return strings.Compare(a.Target().ID, c.Target().ID)
	})
	slices.SortFunc(b.waits, func(a, c Wait) int {
		if a.Target.Kind != c.Target.Kind {
			return int(a.Target.Kind) - int(c.Target.Kind)
		}
		if a.Target.ID != c.Target.ID {
			return strings.Compare(a.Target.ID, c.Target.ID)
		}
		return int(a.Reason) - int(c.Reason)
	})
	return Plan{steps: b.steps, waits: b.waits}
}

func rank(s Step) int {
	switch s.(type) {
	case EnsureVolume:
		return 1
	case EnsureNetwork:
		return 2
	case StopVM:
		return 3
	case StartVM:
		return 4
	case DestroyVM:
		return 5
	case DeleteVolume:
		return 6
	case DeleteNetwork:
		return 7
	}
	return 9
}

func (b *planBuilder) wantStopped(vm spec.VM, ovm VM, ok bool, now time.Time) {
	if !ok || ovm.State == Off {
		return
	}
	b.stop(vm.ID, ovm, now, genStop)
}

func (b *planBuilder) wantRunning(o Observed, vm spec.VM, ovm VM, ok bool, now time.Time, held map[spec.VolumeID][]spec.VMID, unknown bool) {
	ref := Ref{KindVM, vm.ID.String()}
	if !ok || ovm.State == Off {
		if !depsReady(o, vm) {
			b.wait(ref, WaitDependencies)
			return
		}
		if unknown || volumeHeldByOther(vm, held) {
			b.wait(ref, WaitInUse)
			return
		}
		b.step(StartVM{VM: vm})
		return
	}
	switch specCmp(vm, ovm) {
	case specUnknown:
		b.wait(ref, WaitUnknownSpec)
	case specEqual:
		switch ovm.State {
		case Running:
		case Prelaunch:
			b.step(StartVM{VM: vm})
		case Paused:
			b.wait(ref, WaitPaused)
		case Shutdown:
			b.step(StopVM{ID: vm.ID, Force: true, Gen: genReap})
		case Unresponsive:
			b.wait(ref, WaitUnresponsive)
		default:
			b.wait(ref, WaitUnresponsive)
		}
	case specDiffer:
		if ovm.State == Shutdown {
			b.step(StopVM{ID: vm.ID, Force: true, Gen: genSpec(vm)})
			return
		}
		b.stop(vm.ID, ovm, now, genSpec(vm))
	}
}

const genReap = "reap"

func genSpec(vm spec.VM) string {
	l := vm.Launch()
	return "spec:" + l.Boot.String() + ":" + itoa(l.VCPUs) + ":" + itoa(l.MemoryMiB) + ":" + joinVol(l.Extra) + ":" + joinNet(l.Networks)
}

func itoa(n int) string { return strconv.Itoa(n) }

func joinVol(ids []spec.VolumeID) string {
	s := ""
	for i, id := range ids {
		if i > 0 {
			s += ","
		}
		s += id.String()
	}
	return s
}

func joinNet(ids []spec.NetworkID) string {
	s := ""
	for i, id := range ids {
		if i > 0 {
			s += ","
		}
		s += id.String()
	}
	return s
}

type specRelation int

const (
	specUnknown specRelation = iota
	specEqual
	specDiffer
)

func specCmp(vm spec.VM, ovm VM) specRelation {
	if !ovm.SpecKnown {
		return specUnknown
	}
	if ovm.Spec.Equal(vm.Launch()) {
		return specEqual
	}
	return specDiffer
}

func (b *planBuilder) stop(id spec.VMID, ovm VM, now time.Time, gen string) {
	if ovm.State == Off {
		return
	}
	force := false
	if ovm.State == Shutdown {
		force = true
	} else if ovm.StopGen == gen && !ovm.PowerdownAt.IsZero() && !now.Before(ovm.PowerdownAt.Add(StopGrace)) {
		force = true
	}
	b.step(StopVM{ID: id, Force: force, Gen: gen})
}

func depsReady(o Observed, vm spec.VM) bool {
	if _, ok := o.Volumes[vm.BootVolume]; !ok {
		return false
	}
	for _, id := range vm.Volumes {
		if _, ok := o.Volumes[id]; !ok {
			return false
		}
	}
	for _, id := range vm.Networks {
		n, ok := o.Networks[id]
		if !ok || !n.Ready {
			return false
		}
	}
	return true
}

func volumeHeldByOther(vm spec.VM, held map[spec.VolumeID][]spec.VMID) bool {
	check := append([]spec.VolumeID{vm.BootVolume}, vm.Volumes...)
	for _, id := range check {
		for _, owner := range held[id] {
			if owner != vm.ID {
				return true
			}
		}
	}
	return false
}

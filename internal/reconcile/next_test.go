package reconcile_test

import (
	"testing"
	"time"

	"github.com/popododo0720/vws-hypervisor/internal/reconcile"
	"github.com/popododo0720/vws-hypervisor/spec"
)

func idVM(s string) spec.VMID       { return spec.Must(spec.ParseVMID(s)) }
func idVol(s string) spec.VolumeID  { return spec.Must(spec.ParseVolumeID(s)) }
func idNet(s string) spec.NetworkID { return spec.Must(spec.ParseNetworkID(s)) }

func webDoc(power spec.Power, vcpus int) spec.Desired {
	d, err := spec.Parse(spec.Document{
		Revision: 1,
		Volumes:  []spec.Volume{{ID: idVol("root"), SizeBytes: 1 << 20}},
		Networks: []spec.Network{{ID: idNet("lan")}},
		VMs: []spec.VM{{
			ID: idVM("web"), VCPUs: vcpus, MemoryMiB: 512,
			BootVolume: idVol("root"),
			Networks:   []spec.NetworkID{idNet("lan")},
			Power:      power,
		}},
	})
	if err != nil {
		panic(err)
	}
	return d
}

func runningObs(vcpus int) reconcile.Observed {
	vm := webDoc(spec.PowerRunning, vcpus).VMs()[0]
	return reconcile.Observed{
		VMs: map[spec.VMID]reconcile.VM{
			idVM("web"): {
				State: reconcile.Running, SpecKnown: true, Spec: vm.Launch(),
				AttachmentsKnown: true,
				Volumes:          []spec.VolumeID{idVol("root")},
				Networks:         []spec.NetworkID{idNet("lan")},
			},
		},
		Volumes:  map[spec.VolumeID]reconcile.Volume{idVol("root"): {SizeBytes: 1 << 20}},
		Networks: map[spec.NetworkID]reconcile.Network{idNet("lan"): {Ready: true}},
	}
}

func ops(p reconcile.Plan) []string {
	var out []string
	for _, s := range p.Steps() {
		out = append(out, reconcile.Op(s))
	}
	return out
}

func TestZeroDesiredDoesNothing(t *testing.T) {
	p := reconcile.Next(spec.Desired{}, runningObs(2), time.Unix(0, 0))
	if len(p.Steps()) != 0 || len(p.Waits()) != 0 {
		t.Fatalf("plan %+v %+v", ops(p), p.Waits())
	}
}

func TestSteadyRunningIsEmpty(t *testing.T) {
	p := reconcile.Next(webDoc(spec.PowerRunning, 2), runningObs(2), time.Unix(0, 0))
	if !p.Converged() {
		t.Fatalf("ops %v waits %+v", ops(p), p.Waits())
	}
}

func TestUnknownSpecDoesNotStop(t *testing.T) {
	obs := runningObs(2)
	vm := obs.VMs[idVM("web")]
	vm.SpecKnown = false
	obs.VMs[idVM("web")] = vm
	p := reconcile.Next(webDoc(spec.PowerRunning, 2), obs, time.Unix(0, 0))
	if len(p.Steps()) != 0 {
		t.Fatalf("stopped an unknown spec: %v", ops(p))
	}
	if len(p.Waits()) != 1 || p.Waits()[0].Reason != reconcile.WaitUnknownSpec {
		t.Fatalf("waits %+v", p.Waits())
	}
}

func TestSpecChangeStopsThenStartsOnlyWhenOff(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	p := reconcile.Next(webDoc(spec.PowerRunning, 4), runningObs(2), now)
	steps := p.Steps()
	if len(steps) != 1 {
		t.Fatalf("ops %v", ops(p))
	}
	stop, ok := steps[0].(reconcile.StopVM)
	if !ok || stop.Force {
		t.Fatalf("step %#v", steps[0])
	}
	obs := runningObs(2)
	vm := obs.VMs[idVM("web")]
	vm.State = reconcile.Off
	obs.VMs[idVM("web")] = vm
	p = reconcile.Next(webDoc(spec.PowerRunning, 4), obs, now)
	if _, ok := p.Steps()[0].(reconcile.StartVM); !ok || len(p.Steps()) != 1 {
		t.Fatalf("ops %v", ops(p))
	}
}

func TestGraceThenForce(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	obs := runningObs(2)
	vm := obs.VMs[idVM("web")]
	vm.StopGen = "spec:root:4:512::lan"
	vm.PowerdownAt = now.Add(-reconcile.StopGrace)
	obs.VMs[idVM("web")] = vm
	p := reconcile.Next(webDoc(spec.PowerRunning, 4), obs, now)
	stop, ok := p.Steps()[0].(reconcile.StopVM)
	if !ok || !stop.Force {
		t.Fatalf("step %#v", p.Steps())
	}
}

func TestStalePowerdownDoesNotForceMatchingSpec(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	obs := runningObs(2)
	vm := obs.VMs[idVM("web")]
	vm.StopGen = "spec:root:4:512::lan"
	vm.PowerdownAt = now.Add(-2 * reconcile.StopGrace)
	obs.VMs[idVM("web")] = vm
	p := reconcile.Next(webDoc(spec.PowerRunning, 2), obs, now)
	if !p.Converged() {
		t.Fatalf("ops %v waits %+v", ops(p), p.Waits())
	}
}

func TestEmptyDocumentStopsThenDestroys(t *testing.T) {
	empty, err := spec.Parse(spec.Document{Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	p := reconcile.Next(empty, runningObs(2), time.Unix(0, 0))
	if _, ok := p.Steps()[0].(reconcile.StopVM); !ok {
		t.Fatalf("ops %v", ops(p))
	}
	obs := runningObs(2)
	vm := obs.VMs[idVM("web")]
	vm.State = reconcile.Off
	obs.VMs[idVM("web")] = vm
	p = reconcile.Next(empty, obs, time.Unix(0, 0))
	var destroy, del bool
	for _, s := range p.Steps() {
		switch s.(type) {
		case reconcile.DestroyVM:
			destroy = true
		case reconcile.DeleteVolume:
			del = true
		}
	}
	if !destroy || del {
		t.Fatalf("ops %v", ops(p))
	}
}

func TestStartWaitsForForeignVolume(t *testing.T) {
	d, err := spec.Parse(spec.Document{
		Revision: 3,
		Volumes:  []spec.Volume{{ID: idVol("root"), SizeBytes: 1 << 20}},
		VMs: []spec.VM{
			{ID: idVM("old"), VCPUs: 1, MemoryMiB: 128, BootVolume: idVol("root"), Power: spec.PowerStopped},
			{ID: idVM("new"), VCPUs: 1, MemoryMiB: 128, BootVolume: idVol("root"), Power: spec.PowerRunning},
		},
	})
	if err == nil {
		t.Fatal("shared volume should not parse")
	}
	_ = d
	obs := reconcile.Observed{
		VMs: map[spec.VMID]reconcile.VM{
			idVM("old"): {
				State: reconcile.Running, SpecKnown: true, AttachmentsKnown: true,
				Volumes: []spec.VolumeID{idVol("root")},
			},
		},
		Volumes: map[spec.VolumeID]reconcile.Volume{idVol("root"): {SizeBytes: 1 << 20}},
	}
	want, err := spec.Parse(spec.Document{
		Revision: 3,
		Volumes:  []spec.Volume{{ID: idVol("root"), SizeBytes: 1 << 20}},
		VMs: []spec.VM{
			{ID: idVM("new"), VCPUs: 1, MemoryMiB: 128, BootVolume: idVol("root"), Power: spec.PowerRunning},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := reconcile.Next(want, obs, time.Unix(0, 0))
	for _, s := range p.Steps() {
		if _, ok := s.(reconcile.StartVM); ok {
			t.Fatal("started while old guest still holds the volume")
		}
		if _, ok := s.(reconcile.DeleteVolume); ok {
			t.Fatal("deleted a volume an observed guest still holds")
		}
	}
}

func TestEveryStepHasAnOp(t *testing.T) {
	samples := []reconcile.Step{
		reconcile.EnsureVolume{},
		reconcile.DeleteVolume{},
		reconcile.EnsureNetwork{},
		reconcile.DeleteNetwork{},
		reconcile.StartVM{},
		reconcile.StopVM{},
		reconcile.DestroyVM{},
	}
	for _, s := range samples {
		if reconcile.Op(s) == "" {
			t.Fatalf("unnamed %T", s)
		}
	}
}

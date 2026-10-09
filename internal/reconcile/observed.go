// Package reconcile decides the next host mutations.
// It performs no I/O and reads no clock except the time the caller passes in.
package reconcile

import (
	"time"

	"github.com/popododo0720/vws-hypervisor/spec"
)

// RunState is what a probe just learned. The zero value is Unresponsive so a
// forgotten field makes the planner do less, not start a second QEMU.
type RunState int

const (
	// Unresponsive means a process or socket may exist but the probe could
	// not prove identity.
	Unresponsive RunState = iota
	// Off means the pinned process is gone and the pinned socket does not answer.
	Off
	// Running means QMP reports running vCPUs.
	Running
	// Prelaunch means QEMU was started with vCPUs stopped and the launch spec matches.
	Prelaunch
	// Paused means vCPUs are stopped for a reason other than first boot.
	Paused
	// Shutdown means the guest has powered off and the QEMU process is still alive.
	Shutdown
)

// VM is one observed guest. SpecKnown is false when the launch record is
// missing or from an older schema. AttachmentsKnown is false when this
// observation cannot say which volumes the process still has open.
type VM struct {
	State            RunState
	SpecKnown        bool
	Spec             spec.Launch
	AttachmentsKnown bool
	Volumes          []spec.VolumeID
	Networks         []spec.NetworkID
	PowerdownAt      time.Time
	StopGen          string
	Process          string
}

// Volume is a backing file that exists under its final name.
type Volume struct {
	SizeBytes int64
}

// Network is a network record. Ready means the pinned bridge exists.
type Network struct {
	Ready bool
}

// Observed is one complete probe. A missing map entry means the resource
// was not found. A failed listing fails Observe and never produces a partial
// Observed.
type Observed struct {
	VMs      map[spec.VMID]VM
	Volumes  map[spec.VolumeID]Volume
	Networks map[spec.NetworkID]Network
}

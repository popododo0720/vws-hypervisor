package reconcile

import "time"

// StopGrace is how long a graceful powerdown may take before the planner
// asks for a forced stop.
const StopGrace = 2 * time.Minute

// WaitReason explains a difference this plan does not mutate.
type WaitReason int

const (
	// WaitDependencies means a start is waiting for a volume or network.
	WaitDependencies WaitReason = iota + 1
	// WaitInUse means a delete or start is waiting for another guest to release a volume.
	WaitInUse
	// WaitSizeMismatch means an existing file is a different size.
	WaitSizeMismatch
	// WaitUnknownSpec means the launch record cannot be compared, so the guest is left alone.
	WaitUnknownSpec
	// WaitUnresponsive means the probe could not prove identity.
	WaitUnresponsive
	// WaitPaused means vCPUs are paused and the launch spec already matches.
	WaitPaused
)

// Wait is one unexplained difference.
type Wait struct {
	Target Ref
	Reason WaitReason
}

// Plan is the mutations for one pass. steps is unexported so only Next can
// build a plan.
type Plan struct {
	steps []Step
	waits []Wait
}

// Steps returns a copy of the plan's mutations.
func (p Plan) Steps() []Step {
	return append([]Step(nil), p.steps...)
}

// Waits returns a copy of the plan's waits.
func (p Plan) Waits() []Wait {
	return append([]Wait(nil), p.waits...)
}

// Converged reports whether the observation already matches the desired state.
func (p Plan) Converged() bool { return len(p.steps) == 0 && len(p.waits) == 0 }

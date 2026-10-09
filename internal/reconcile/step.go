package reconcile

import "github.com/popododo0720/vws-hypervisor/spec"

// Step is one idempotent host mutation. The set is closed. Only Next can put
// steps into a Plan. Callers outside this package cannot construct a Plan.
type Step interface {
	Target() Ref
	isStep()
}

// Kind is a resource kind.
type Kind int

const (
	// KindVolume is a backing file.
	KindVolume Kind = iota + 1
	// KindNetwork is a bridge.
	KindNetwork
	// KindVM is a guest.
	KindVM
)

// Ref names one resource.
type Ref struct {
	Kind Kind
	ID   string
}

// EnsureVolume creates the backing file when it is absent.
// The same size is success. A different size is left untouched.
type EnsureVolume struct{ Volume spec.Volume }

// DeleteVolume removes a backing file. A missing file is success.
type DeleteVolume struct{ ID spec.VolumeID }

// EnsureNetwork creates the bridge pinned for the network and reattaches
// recorded taps.
type EnsureNetwork struct{ Network spec.Network }

// DeleteNetwork removes the bridge and the network record.
type DeleteNetwork struct{ ID spec.NetworkID }

// StartVM starts a guest that is Off, or continues a prelaunch guest whose
// launch spec matches. It does not replace a live process.
type StartVM struct{ VM spec.VM }

// StopVM stops the pinned process. Force kills it. Otherwise the host sends
// an ACPI powerdown and records gen so a later pass can escalate.
type StopVM struct {
	ID    spec.VMID
	Force bool
	Gen   string
}

// DestroyVM removes a guest runtime that is already Off.
type DestroyVM struct{ ID spec.VMID }

// Target implements Step.
func (s EnsureVolume) Target() Ref { return Ref{KindVolume, s.Volume.ID.String()} }

// Target implements Step.
func (s DeleteVolume) Target() Ref { return Ref{KindVolume, s.ID.String()} }

// Target implements Step.
func (s EnsureNetwork) Target() Ref { return Ref{KindNetwork, s.Network.ID.String()} }

// Target implements Step.
func (s DeleteNetwork) Target() Ref { return Ref{KindNetwork, s.ID.String()} }

// Target implements Step.
func (s StartVM) Target() Ref { return Ref{KindVM, s.VM.ID.String()} }

// Target implements Step.
func (s StopVM) Target() Ref { return Ref{KindVM, s.ID.String()} }

// Target implements Step.
func (s DestroyVM) Target() Ref { return Ref{KindVM, s.ID.String()} }

func (EnsureVolume) isStep()  {}
func (DeleteVolume) isStep()  {}
func (EnsureNetwork) isStep() {}
func (DeleteNetwork) isStep() {}
func (StartVM) isStep()       {}
func (StopVM) isStep()        {}
func (DestroyVM) isStep()     {}

// Op names a step for status. The default is empty so a new kind fails tests
// that require a name.
func Op(s Step) string {
	switch s.(type) {
	case EnsureVolume:
		return "ensure-volume"
	case DeleteVolume:
		return "delete-volume"
	case EnsureNetwork:
		return "ensure-network"
	case DeleteNetwork:
		return "delete-network"
	case StartVM:
		return "start-vm"
	case StopVM:
		return "stop-vm"
	case DestroyVM:
		return "destroy-vm"
	default:
		return ""
	}
}

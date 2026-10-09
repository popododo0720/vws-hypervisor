package host

import "context"

// Sys is every operation that needs root, KVM, or an external binary.
// File layout, QMP, and policy stay in this package and run in tests.
// Methods are idempotent in the way their comments state.
type Sys interface {
	// Spawn starts argv as a process named name that outlives the agent.
	// If that name is already alive, Spawn returns nil and starts nothing.
	Spawn(ctx context.Context, name string, argv []string) error
	// Kill terminates the process named name and waits until it is gone.
	// No such process is success.
	Kill(ctx context.Context, name string) error
	// Processes lists names of live processes started by Spawn.
	// A name whose main process still exists is included while it is stopping.
	Processes(ctx context.Context) ([]string, error)
	// EnsureBridge creates the bridge and brings it up.
	EnsureBridge(ctx context.Context, name string) error
	// EnsureTap creates a tap, enslaves it to bridge, and brings it up.
	// It does not set the guest MAC. The guest MAC belongs to the virtio device.
	EnsureTap(ctx context.Context, name, bridge string) error
	// DeleteLink deletes a bridge or tap. No such link is success.
	DeleteLink(ctx context.Context, name string) error
	// Links lists bridge and tap names.
	Links(ctx context.Context) ([]string, error)
}

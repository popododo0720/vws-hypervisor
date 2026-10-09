// Package spec is the desired-state document for one VWS host.
//
// A Document is untrusted input. Parse is the only constructor of a Desired,
// and every other package trusts a Desired. Identifiers are typed so a volume
// id cannot be used where a VM id is required.
package spec

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// MaxIDLen is the longest identifier. Host paths and interface names are
// derived from ids, and this bound keeps them inside kernel limits.
const MaxIDLen = 40

// ErrInvalidID reports an identifier that cannot be one path segment.
var ErrInvalidID = errors.New("spec: invalid id")

var idPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

type (
	vmKind      struct{}
	volumeKind  struct{}
	networkKind struct{}
)

type kind interface {
	vmKind | volumeKind | networkKind
}

// ID is an identifier that has already passed Parse. The type parameter keeps
// VM, volume, and network ids distinct. The zero ID is not valid.
type ID[K kind] struct{ s string }

// VMID identifies a virtual machine.
type VMID = ID[vmKind]

// VolumeID identifies a volume.
type VolumeID = ID[volumeKind]

// NetworkID identifies a network.
type NetworkID = ID[networkKind]

// ParseVMID validates s as a VM id.
func ParseVMID(s string) (VMID, error) { return parseID[vmKind](s) }

// ParseVolumeID validates s as a volume id.
func ParseVolumeID(s string) (VolumeID, error) { return parseID[volumeKind](s) }

// ParseNetworkID validates s as a network id.
func ParseNetworkID(s string) (NetworkID, error) { return parseID[networkKind](s) }

// Must returns v or panics. Tests use it for literal ids.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func parseID[K kind](s string) (ID[K], error) {
	if !idPattern.MatchString(s) {
		return ID[K]{}, fmt.Errorf("%w %q", ErrInvalidID, s)
	}
	return ID[K]{s: s}, nil
}

// String returns the identifier text.
func (id ID[K]) String() string { return id.s }

// IsZero reports whether id is the zero value.
func (id ID[K]) IsZero() bool { return id.s == "" }

// MarshalJSON encodes the identifier as a JSON string.
func (id ID[K]) MarshalJSON() ([]byte, error) { return json.Marshal(id.s) }

// UnmarshalJSON rejects any string that is not a valid id.
func (id *ID[K]) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := parseID[K](s)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// Less reports whether id sorts before o. Both must be the same kind.
func (id ID[K]) Less(o ID[K]) bool { return id.s < o.s }

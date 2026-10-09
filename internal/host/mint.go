package host

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"

	"github.com/popododo0720/vws-hypervisor/spec"
)

// Name derivation lives in this file. Existing guests are reached through the
// fields stored in their launch record, not by calling these functions again.

func processName(id spec.VMID) string { return "vm-" + id.String() }

func matchProcess(name string) (spec.VMID, bool) {
	const prefix = "vm-"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return spec.VMID{}, false
	}
	id, err := spec.ParseVMID(name[len(prefix):])
	if err != nil || processName(id) != name {
		return spec.VMID{}, false
	}
	return id, true
}

func bridgeName(id spec.NetworkID) string {
	return "vb" + hash12(id.String())
}

func tapName(vm spec.VMID, net spec.NetworkID) string {
	return "vt" + hash12(vm.String()+"\n"+net.String())
}

func guestMAC(vm spec.VMID, net spec.NetworkID) string {
	sum := sha256.Sum256([]byte(vm.String() + "\n" + net.String()))
	b := sum[:6]
	b[0] = (b[0] | 0x02) & 0xfe
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

func hash12(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

func newNonce() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

func newNetworkRecord(id spec.NetworkID) netRecord {
	return netRecord{Schema: recordSchema, Bridge: bridgeName(id)}
}

func mintRecord(runDir string, vm spec.VM, bridges map[spec.NetworkID]string) (launchRecord, error) {
	nonce, err := newNonce()
	if err != nil {
		return launchRecord{}, err
	}
	uuid, err := newUUID()
	if err != nil {
		return launchRecord{}, err
	}
	extra := make([]string, len(vm.Volumes))
	for i, id := range vm.Volumes {
		extra[i] = id.String()
	}
	nets := make([]string, len(vm.Networks))
	nics := make([]nicRecord, len(vm.Networks))
	for i, id := range vm.Networks {
		nets[i] = id.String()
		bridge, ok := bridges[id]
		if !ok || bridge == "" {
			return launchRecord{}, fmt.Errorf("host: network %s has no bridge", id)
		}
		nics[i] = nicRecord{
			Network: id.String(),
			Tap:     tapName(vm.ID, id),
			MAC:     guestMAC(vm.ID, id),
			Bridge:  bridge,
		}
	}
	return launchRecord{
		Schema:    recordSchema,
		Process:   processName(vm.ID),
		Socket:    filepath.Join(runDir, "vms", vm.ID.String(), "qmp-"+nonce+".sock"),
		UUID:      uuid,
		NICs:      nics,
		VCPUs:     vm.VCPUs,
		MemoryMiB: vm.MemoryMiB,
		Boot:      vm.BootVolume.String(),
		Extra:     extra,
		Networks:  nets,
	}, nil
}

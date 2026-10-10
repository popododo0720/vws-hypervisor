package spec_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/popododo0720/vws-hypervisor/spec"
)

func TestParseCanonicalIgnoresNilSlice(t *testing.T) {
	withNil := spec.Document{
		Revision: 1,
		Volumes:  []spec.Volume{{ID: spec.Must(spec.ParseVolumeID("root")), SizeBytes: 1 << 20}},
		Networks: []spec.Network{{ID: spec.Must(spec.ParseNetworkID("lan"))}},
		VMs: []spec.VM{{
			ID: spec.Must(spec.ParseVMID("web")), VCPUs: 2, MemoryMiB: 512,
			BootVolume: spec.Must(spec.ParseVolumeID("root")),
			Networks:   []spec.NetworkID{spec.Must(spec.ParseNetworkID("lan"))},
			Power:      spec.PowerRunning,
		}},
	}
	withEmpty := withNil
	withEmpty.VMs = append([]spec.VM(nil), withNil.VMs...)
	withEmpty.VMs[0].Volumes = []spec.VolumeID{}

	a, err := spec.Parse(withNil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := spec.Parse(withEmpty)
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest() != b.Digest() {
		t.Fatalf("digest nil %s empty %s", a.Digest(), b.Digest())
	}
	if !a.VMs()[0].Launch().Equal(b.VMs()[0].Launch()) {
		t.Fatal("launch spec differs for nil and empty volumes")
	}
	raw, err := a.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"revision":1,"volumes":[{"id":"root","size_bytes":1048576}],"networks":[{"id":"lan"}],"vms":[{"id":"web","vcpus":2,"memory_mib":512,"boot_volume":"root","volumes":[],"networks":["lan"],"power":"running"}]}`
	if string(raw) != want {
		t.Fatalf("canonical\n got %s\nwant %s", raw, want)
	}
	back, err := spec.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	again, err := spec.Parse(back)
	if err != nil {
		t.Fatal(err)
	}
	if again.Digest() != a.Digest() {
		t.Fatalf("round trip digest %s want %s", again.Digest(), a.Digest())
	}
}

func TestParseRejectsSharedVolumeAndUnknownField(t *testing.T) {
	doc := spec.Document{
		Revision: 1,
		Volumes:  []spec.Volume{{ID: spec.Must(spec.ParseVolumeID("root")), SizeBytes: 1 << 20}},
		VMs: []spec.VM{
			{ID: spec.Must(spec.ParseVMID("a")), VCPUs: 1, MemoryMiB: 128, BootVolume: spec.Must(spec.ParseVolumeID("root")), Power: spec.PowerRunning},
			{ID: spec.Must(spec.ParseVMID("b")), VCPUs: 1, MemoryMiB: 128, BootVolume: spec.Must(spec.ParseVolumeID("root")), Power: spec.PowerRunning},
		},
	}
	if _, err := spec.Parse(doc); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("err %v", err)
	}
	if _, err := spec.Decode(strings.NewReader(`{"revision":1,"typo":true}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestAttachmentOrderChangesLaunch(t *testing.T) {
	base := func(nets ...string) spec.Document {
		var ids []spec.NetworkID
		var netsDoc []spec.Network
		for _, n := range []string{"a", "b"} {
			id := spec.Must(spec.ParseNetworkID(n))
			netsDoc = append(netsDoc, spec.Network{ID: id})
		}
		for _, n := range nets {
			ids = append(ids, spec.Must(spec.ParseNetworkID(n)))
		}
		return spec.Document{
			Revision: 1,
			Volumes:  []spec.Volume{{ID: spec.Must(spec.ParseVolumeID("root")), SizeBytes: 1 << 20}},
			Networks: netsDoc,
			VMs: []spec.VM{{
				ID: spec.Must(spec.ParseVMID("web")), VCPUs: 1, MemoryMiB: 128,
				BootVolume: spec.Must(spec.ParseVolumeID("root")),
				Networks:   ids, Power: spec.PowerRunning,
			}},
		}
	}
	ab, err := spec.Parse(base("a", "b"))
	if err != nil {
		t.Fatal(err)
	}
	ba, err := spec.Parse(base("b", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if ab.VMs()[0].Launch().Equal(ba.VMs()[0].Launch()) {
		t.Fatal("nic order was ignored")
	}
	if ab.Digest() == ba.Digest() {
		t.Fatal("document digest ignored nic order")
	}
}

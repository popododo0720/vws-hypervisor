package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/popododo0720/vws-hypervisor/internal/agent"
	"github.com/popododo0720/vws-hypervisor/internal/host/systest"
	"github.com/popododo0720/vws-hypervisor/internal/qmp/qmptest"
	"github.com/popododo0720/vws-hypervisor/internal/reconcile"
	"github.com/popododo0720/vws-hypervisor/spec"
)

func TestRestartReattachesWithoutRelaunch(t *testing.T) {
	sys, cfg := env(t)
	ctx := context.Background()
	a := open(t, cfg)
	if err := a.SetDesired(ctx, web(1, 2)); err != nil {
		t.Fatal(err)
	}
	st := a.Reconcile(ctx)
	if !st.Converged {
		t.Fatalf("first %+v errs %v intake %v", st.Applied, st.Errors, st.IntakeErr)
	}
	before := len(sys.Received("vm-web"))
	if sys.SpawnCount("vm-web") != 1 {
		t.Fatalf("spawns %d", sys.SpawnCount("vm-web"))
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	b := open(t, cfg)
	st = b.Reconcile(ctx)
	if !st.Converged || !st.HaveDesired || st.Revision != 1 {
		t.Fatalf("restart status converged %v have %v rev %d applied %v err %v", st.Converged, st.HaveDesired, st.Revision, st.Applied, st.Err)
	}
	if len(st.Applied) != 0 {
		t.Fatalf("restart applied %v", st.Applied)
	}
	if sys.SpawnCount("vm-web") != 1 {
		t.Fatalf("relaunched, spawns %d", sys.SpawnCount("vm-web"))
	}
	got := sys.Received("vm-web")
	if len(got) <= before || !contains(got[before:], "query-uuid") {
		t.Fatalf("second probe did not query-uuid: before %d received %v", before, got)
	}
}

func TestPinnedSocketIsProbed(t *testing.T) {
	sys, cfg := env(t)
	ctx := context.Background()
	a := open(t, cfg)
	if err := a.SetDesired(ctx, web(1, 2)); err != nil {
		t.Fatal(err)
	}
	if st := a.Reconcile(ctx); !st.Converged {
		t.Fatal(st.Errors)
	}
	socket, uuid := readPin(t, cfg.RunDir)
	before := len(sys.Received("vm-web"))
	other := filepath.Join(filepath.Dir(socket), "other.sock")
	m, err := qmptest.Listen(other, uuid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	rewriteSocket(t, cfg.RunDir, other)
	st := a.Reconcile(ctx)
	if !st.Converged || sys.SpawnCount("vm-web") != 1 {
		t.Fatalf("converged %v spawns %d applied %v waits %+v err %v", st.Converged, sys.SpawnCount("vm-web"), st.Applied, st.Waits, st.Err)
	}
	if len(sys.Received("vm-web")) != before {
		t.Fatalf("original socket was dialed again: %v", sys.Received("vm-web"))
	}
	if !contains(m.Received(), "query-uuid") {
		t.Fatalf("pinned socket was not queried: %v", m.Received())
	}
}

func TestUUIDMismatchDoesNotSpawn(t *testing.T) {
	sys, cfg := env(t)
	ctx := context.Background()
	a := open(t, cfg)
	if err := a.SetDesired(ctx, web(1, 2)); err != nil {
		t.Fatal(err)
	}
	if st := a.Reconcile(ctx); !st.Converged {
		t.Fatal(st.Errors)
	}
	sys.SetUUID("vm-web", "99999999-9999-4999-8999-999999999999")
	st := a.Reconcile(ctx)
	if st.Converged || sys.SpawnCount("vm-web") != 1 {
		t.Fatalf("converged %v spawns %d", st.Converged, sys.SpawnCount("vm-web"))
	}
}

func TestSpecChangeRestartsAfterStop(t *testing.T) {
	sys, cfg := env(t)
	ctx := context.Background()
	a := open(t, cfg)
	if err := a.SetDesired(ctx, web(1, 2)); err != nil {
		t.Fatal(err)
	}
	if st := a.Reconcile(ctx); !st.Converged {
		t.Fatal(st.Errors)
	}
	if err := a.SetDesired(ctx, web(2, 4)); err != nil {
		t.Fatal(err)
	}
	st := a.Reconcile(ctx)
	if !st.Converged {
		t.Fatalf("applied %v waits %+v errs %v", st.Applied, st.Waits, st.Errors)
	}
	if sys.SpawnCount("vm-web") != 2 {
		t.Fatalf("spawns %d", sys.SpawnCount("vm-web"))
	}
	argv := sys.Argvs("vm-web")
	if len(argv) != 2 || !contains(argv[1], "-smp") || !contains(argv[1], "4") {
		t.Fatalf("argv %#v", argv)
	}
}

func TestControlPlaneLossKeepsGuest(t *testing.T) {
	sys, cfg := env(t)
	ctx := context.Background()
	a := open(t, cfg)
	if err := a.SetDesired(ctx, web(1, 2)); err != nil {
		t.Fatal(err)
	}
	if st := a.Reconcile(ctx); !st.Converged {
		t.Fatal(st.Errors)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.DesiredFile, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cfg.DesiredFile); err != nil {
		t.Fatal(err)
	}
	b := open(t, cfg)
	st := b.Reconcile(ctx)
	if !st.HaveDesired || len(st.Applied) != 0 || sys.SpawnCount("vm-web") != 1 {
		t.Fatalf("have %v applied %v spawns %d", st.HaveDesired, st.Applied, sys.SpawnCount("vm-web"))
	}
	vm := st.Observed.VMs[spec.Must(spec.ParseVMID("web"))]
	if vm.State != reconcile.Running {
		t.Fatalf("state %v", vm.State)
	}
}

func TestInvalidDropFileKeepsCommitment(t *testing.T) {
	sys, cfg := env(t)
	ctx := context.Background()
	a := open(t, cfg)
	if err := a.SetDesired(ctx, web(1, 2)); err != nil {
		t.Fatal(err)
	}
	if st := a.Reconcile(ctx); !st.Converged {
		t.Fatal(st.Errors)
	}
	if err := os.WriteFile(cfg.DesiredFile, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := a.Reconcile(ctx)
	if !st.Converged || st.Revision != 1 || sys.SpawnCount("vm-web") != 1 || st.IntakeErr == nil {
		t.Fatalf("rev %d spawns %d intake %v converged %v", st.Revision, sys.SpawnCount("vm-web"), st.IntakeErr, st.Converged)
	}
}

func TestEmptyDocumentRemovesGuest(t *testing.T) {
	sys, cfg := env(t)
	ctx := context.Background()
	a := open(t, cfg)
	if err := a.SetDesired(ctx, web(1, 2)); err != nil {
		t.Fatal(err)
	}
	if st := a.Reconcile(ctx); !st.Converged {
		t.Fatal(st.Errors)
	}
	empty := spec.Document{Revision: 2}
	if err := a.SetDesired(ctx, empty); err != nil {
		t.Fatal(err)
	}
	st := a.Reconcile(ctx)
	if !st.Converged {
		t.Fatalf("applied %v waits %+v errs %v", st.Applied, st.Waits, st.Errors)
	}
	if _, ok := st.Observed.VMs[spec.Must(spec.ParseVMID("web"))]; ok {
		t.Fatal("guest still observed")
	}
	if _, err := os.Stat(filepath.Join(cfg.StateDir, "volumes", "root.raw")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("volume err %v", err)
	}
	if sys.SpawnCount("vm-web") != 1 {
		t.Fatalf("spawns %d", sys.SpawnCount("vm-web"))
	}
}

func TestCorruptCacheRejectsOlderDrop(t *testing.T) {
	sys, cfg := env(t)
	ctx := context.Background()
	a := open(t, cfg)
	if err := a.SetDesired(ctx, web(2, 2)); err != nil {
		t.Fatal(err)
	}
	if st := a.Reconcile(ctx); !st.Converged {
		t.Fatal(st.Errors)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "desired.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(web(1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.DesiredFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	b := open(t, cfg)
	st := b.Reconcile(ctx)
	if st.HaveDesired || sys.SpawnCount("vm-web") != 1 {
		t.Fatalf("have %v spawns %d applied %v intake %v", st.HaveDesired, sys.SpawnCount("vm-web"), st.Applied, st.IntakeErr)
	}
	if _, ok := st.Observed.VMs[spec.Must(spec.ParseVMID("web"))]; !ok {
		t.Fatal("guest disappeared without a document")
	}
}

func TestSupersededPlanDoesNotDelete(t *testing.T) {
	sys, cfg := env(t)
	ctx := context.Background()
	a := open(t, cfg)
	fired := false
	sys.SetBridgeHook(func(name string) {
		if fired || len(name) < 2 {
			return
		}
		fired = true
		doc := web(2, 2)
		doc.Networks = []spec.Network{{ID: spec.Must(spec.ParseNetworkID("lan"))}}
		if err := a.SetDesired(ctx, doc); err != nil {
			t.Errorf("set during apply: %v", err)
		}
	})
	both := web(1, 2)
	both.Networks = []spec.Network{
		{ID: spec.Must(spec.ParseNetworkID("dmz"))},
		{ID: spec.Must(spec.ParseNetworkID("lan"))},
	}
	if err := a.SetDesired(ctx, both); err != nil {
		t.Fatal(err)
	}
	st := a.Reconcile(ctx)
	if len(st.Errors) == 0 || !errors.Is(st.Errors[0], agent.ErrSuperseded) {
		t.Fatalf("errors %v", st.Errors)
	}
	st = a.Reconcile(ctx)
	if !st.Converged || st.Revision != 2 {
		t.Fatalf("rev %d converged %v applied %v errs %v waits %+v", st.Revision, st.Converged, st.Applied, st.Errors, st.Waits)
	}
	if _, err := os.Stat(filepath.Join(cfg.RunDir, "networks", "dmz.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dmz record %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.RunDir, "networks", "lan.json")); err != nil {
		t.Fatal(err)
	}
}

func TestRebootMintsSameNICNames(t *testing.T) {
	sys, cfg := env(t)
	ctx := context.Background()
	a := open(t, cfg)
	if err := a.SetDesired(ctx, web(1, 2)); err != nil {
		t.Fatal(err)
	}
	if st := a.Reconcile(ctx); !st.Converged {
		t.Fatal(st.Errors)
	}
	first := nicArgs(sys.Argvs("vm-web")[0])
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	sys.Reboot()
	if err := os.RemoveAll(cfg.RunDir); err != nil {
		t.Fatal(err)
	}
	b := open(t, cfg)
	st := b.Reconcile(ctx)
	if !st.Converged || sys.SpawnCount("vm-web") != 2 {
		t.Fatalf("converged %v spawns %d errs %v", st.Converged, sys.SpawnCount("vm-web"), st.Errors)
	}
	second := nicArgs(sys.Argvs("vm-web")[1])
	if first != second {
		t.Fatalf("nic args %s then %s", first, second)
	}
}

func env(t *testing.T) (*systest.Sys, agent.Config) {
	t.Helper()
	sys := systest.New()
	root := systest.ShortDir(t)
	state := filepath.Join(root, "state")
	run := filepath.Join(root, "run")
	drop := filepath.Join(root, "desired.json")
	return sys, agent.Config{StateDir: state, RunDir: run, DesiredFile: drop, Sys: sys}
}

func open(t *testing.T, cfg agent.Config) *agent.Agent {
	t.Helper()
	a, err := agent.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func web(rev uint64, vcpus int) spec.Document {
	return spec.Document{
		Revision: rev,
		Volumes:  []spec.Volume{{ID: spec.Must(spec.ParseVolumeID("root")), SizeBytes: 1 << 20}},
		Networks: []spec.Network{{ID: spec.Must(spec.ParseNetworkID("lan"))}},
		VMs: []spec.VM{{
			ID: spec.Must(spec.ParseVMID("web")), VCPUs: vcpus, MemoryMiB: 512,
			BootVolume: spec.Must(spec.ParseVolumeID("root")),
			Networks:   []spec.NetworkID{spec.Must(spec.ParseNetworkID("lan"))},
			Power:      spec.PowerRunning,
		}},
	}
}

func readPin(t *testing.T, runDir string) (string, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(runDir, "vms", "web", "launch.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rec struct {
		Socket string `json:"socket"`
		UUID   string `json:"uuid"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	return rec.Socket, rec.UUID
}

func rewriteSocket(t *testing.T, runDir, socket string) {
	t.Helper()
	path := filepath.Join(runDir, "vms", "web", "launch.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatal(err)
	}
	rec["socket"] = socket
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func nicArgs(argv []string) string {
	var parts []string
	for _, a := range argv {
		if len(a) >= 4 && (a[:4] == "tap," || containsMAC(a)) {
			parts = append(parts, a)
		}
	}
	return join(parts)
}

func containsMAC(s string) bool {
	return len(s) > 4 && s[:4] == "mac=" || (len(s) > 10 && containsSub(s, "mac="))
}

func containsSub(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > len(sub) && (s[:len(sub)] == sub || containsSub(s[1:], sub)))
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

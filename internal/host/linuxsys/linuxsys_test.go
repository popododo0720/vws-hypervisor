package linuxsys

import (
	"context"
	"strings"
	"testing"
)

type fakeRun struct {
	starts [][]string
	out    []byte
	err    error
}

func (f *fakeRun) Start(_ context.Context, argv []string) error {
	f.starts = append(f.starts, append([]string(nil), argv...))
	return f.err
}

func (f *fakeRun) Output(context.Context, []string) ([]byte, error) {
	return f.out, nil
}

func TestSpawnUsesTransientUnit(t *testing.T) {
	f := &fakeRun{out: []byte("inactive\n")}
	s := &Sys{run: f}
	err := s.Spawn(context.Background(), "vm-web", []string{"qemu-system-x86_64", "-name", "vm-web"})
	if err != nil {
		t.Fatal(err)
	}
	var run []string
	for _, argv := range f.starts {
		if argv[0] == "systemd-run" {
			run = argv
		}
	}
	if len(run) == 0 {
		t.Fatalf("starts %v", f.starts)
	}
	joined := strings.Join(run, " ")
	if !strings.Contains(joined, "--unit=vm-web.service") || !strings.Contains(joined, "-- qemu-system-x86_64 -name vm-web") {
		t.Fatalf("argv %s", joined)
	}
}

func TestSpawnSkipsActiveUnit(t *testing.T) {
	f := &fakeRun{out: []byte("active\n")}
	s := &Sys{run: f}
	if err := s.Spawn(context.Background(), "vm-web", []string{"qemu"}); err != nil {
		t.Fatal(err)
	}
	for _, argv := range f.starts {
		if argv[0] == "systemd-run" {
			t.Fatalf("spawned an active unit: %v", f.starts)
		}
	}
}

func TestParseUnits(t *testing.T) {
	got := parseUnits("vm-web.service loaded active running QEMU\nvm-old.service loaded deactivating stop QEMU\nfoo.service loaded active running\n")
	if len(got) != 2 || got[0] != "vm-web" || got[1] != "vm-old" {
		t.Fatalf("units %v", got)
	}
}

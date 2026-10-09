package host_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/popododo0720/vws-hypervisor/internal/host"
	"github.com/popododo0720/vws-hypervisor/internal/host/systest"
	"github.com/popododo0720/vws-hypervisor/internal/reconcile"
	"github.com/popododo0720/vws-hypervisor/spec"
)

func TestVolumeCreateIsIdempotentAndDoesNotTruncate(t *testing.T) {
	sys := systest.New()
	dir := systest.ShortDir(t)
	h, err := host.Open(host.Config{VolumeDir: filepath.Join(dir, "vol"), RunDir: filepath.Join(dir, "run"), Sys: sys})
	if err != nil {
		t.Fatal(err)
	}
	vol := spec.Volume{ID: spec.Must(spec.ParseVolumeID("root")), SizeBytes: 1 << 20}
	d, err := spec.Parse(spec.Document{Revision: 1, Volumes: []spec.Volume{vol}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Unix(0, 0)
	obs, err := h.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(ctx, reconcile.Next(d, obs, now), nil); err != nil {
		t.Fatal(err)
	}
	obs, err = h.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(ctx, reconcile.Next(d, obs, now), nil); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "vol", "root.raw"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 1<<20 {
		t.Fatalf("size %d", fi.Size())
	}
	bigger := vol
	bigger.SizeBytes = 2 << 20
	bad, err := spec.Parse(spec.Document{Revision: 2, Volumes: []spec.Volume{bigger}})
	if err != nil {
		t.Fatal(err)
	}
	obs, err = h.Observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plan := reconcile.Next(bad, obs, now)
	if len(plan.Steps()) != 0 {
		t.Fatalf("resize became a step: %v", plan.Steps())
	}
	if len(plan.Waits()) != 1 || plan.Waits()[0].Reason != reconcile.WaitSizeMismatch {
		t.Fatalf("waits %+v", plan.Waits())
	}
	fi, err = os.Stat(filepath.Join(dir, "vol", "root.raw"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != 1<<20 {
		t.Fatalf("truncated to %d", fi.Size())
	}
}

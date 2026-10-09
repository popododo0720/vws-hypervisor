package qmp_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/popododo0720/vws-hypervisor/internal/qmp"
	"github.com/popododo0720/vws-hypervisor/internal/qmp/qmptest"
)

func TestHandshakeStatusAndUUID(t *testing.T) {
	path := sock(t)
	const uuid = "11111111-1111-4111-8111-111111111111"
	m, err := qmptest.Listen(path, uuid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := qmp.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st != qmp.StateRunning {
		t.Fatalf("status %v", st)
	}
	got, err := c.UUID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != uuid {
		t.Fatalf("uuid %s", got)
	}
	recv := m.Received()
	if len(recv) != 3 || recv[0] != "qmp_capabilities" || recv[1] != "query-status" || recv[2] != "query-uuid" {
		t.Fatalf("received %v", recv)
	}
}

func TestPowerdownChangesStatus(t *testing.T) {
	path := sock(t)
	m, err := qmptest.Listen(path, "22222222-2222-4222-8222-222222222222")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := qmp.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Powerdown(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st != qmp.StateShutdown {
		t.Fatalf("status %v", st)
	}
	if err := c.Quit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestContFromPrelaunch(t *testing.T) {
	path := sock(t)
	m, err := qmptest.Listen(path, "33333333-3333-4333-8333-333333333333")
	if err != nil {
		t.Fatal(err)
	}
	m.SetStatus("prelaunch")
	t.Cleanup(func() { _ = m.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := qmp.Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st != qmp.StatePrelaunch {
		t.Fatalf("status %v", st)
	}
	if err := c.Cont(ctx); err != nil {
		t.Fatal(err)
	}
	st, err = c.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st != qmp.StateRunning {
		t.Fatalf("status after cont %v", st)
	}
}

func sock(t *testing.T) string {
	t.Helper()
	path := filepath.Join("/tmp", fmt.Sprintf("vws-qmp-%d.sock", time.Now().UnixNano()))
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

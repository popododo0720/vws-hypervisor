// Command vws-agent converges one host to a desired-state document.
// It does not install itself. A supervisor such as systemd starts the binary.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/popododo0720/vws-hypervisor/internal/agent"
	"github.com/popododo0720/vws-hypervisor/internal/host/linuxsys"
)

func main() {
	state := flag.String("state-dir", "/var/lib/vws", "durable directory for the desired-state cache and volumes")
	run := flag.String("run-dir", "/run/vws", "runtime directory cleared when the node reboots")
	desired := flag.String("desired-file", "", "desired-state JSON file to poll; a missing or invalid file keeps the cache")
	qemu := flag.String("qemu", "qemu-system-x86_64", "QEMU binary passed to the host")
	interval := flag.Duration("interval", 5*time.Second, "time between reconcile passes")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	node, err := agent.Open(agent.Config{
		StateDir:    *state,
		RunDir:      *run,
		DesiredFile: *desired,
		Interval:    *interval,
		QEMU:        *qemu,
		Sys:         linuxsys.New(),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = node.Close() }()
	if err := node.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

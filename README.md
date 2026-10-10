# vws-hypervisor

`vws-agent` is the host node agent for VWS. It runs as an ordinary process, reads a desired-state document, and keeps the volumes, networks, and QEMU guests on this machine matched to that document.

The agent does not install itself, build images, or ship a unit file. Start the binary under whatever supervisor you already use. Guests are started through `systemd-run`, so stopping the agent does not stop them.

## Run

```
vws-agent -state-dir /var/lib/vws -run-dir /run/vws -desired-file /etc/vws/desired.json
```

`-state-dir` holds the accepted document, the revision floor, and volume files. It must survive a reboot. `-run-dir` holds guest records and should be a tmpfs such as `/run/vws`, so a reboot drops the records of guests the reboot already killed. `-desired-file` is optional. A missing or invalid file leaves the accepted document in place.

A document is one JSON object. Revision must be a positive integer and must increase to replace the accepted document. The same revision with the same content is a no-op. The same revision with different content is rejected.

```json
{
  "revision": 1,
  "volumes": [{"id": "root", "size_bytes": 1048576}],
  "networks": [{"id": "lan"}],
  "vms": [{
    "id": "web",
    "vcpus": 2,
    "memory_mib": 512,
    "boot_volume": "root",
    "networks": ["lan"],
    "power": "running"
  }]
}
```

Ids are one to forty characters from `a-z`, `0-9`, and `-`, and they start and end with a letter or digit. Volume size is a positive multiple of 1 MiB. An existing volume is not resized. A volume can be attached to one VM. Networks are attached in the listed order.

`power` is `running` or `stopped`. Removing a VM from an accepted document destroys that guest. Removing a volume deletes the file once no guest record still references it. An empty document with a new revision removes workloads. No accepted document does not.

## Restart

Every pass reads the host again. After the agent process exits, the next process loads `desired.json` and probes the QMP socket stored in each guest record. A live guest with the same launch spec is left running. A record whose schema the agent does not understand is left running too.

A reboot clears `-run-dir` and the guests. The next process starts them again from the cached document. Tap names and guest MACs are derived from the ids, so the new guest gets the same NIC identity.

If the file named by `-desired-file` disappears or does not parse, the cached document stays and guests keep running.

## Test

```
go test -race ./...
golangci-lint run
```

Tests use an in-process QMP peer. They do not need KVM, a QEMU binary, or root.

# Agent rules

Work on a branch. Do not push to `main`. Each commit does one thing and leaves `go test -race ./...` passing. Open a pull request when the branch is ready.

For a non-trivial change, use pstack in this order:

1. **architect**. Sketch types, signatures, and module boundaries before implementation. Compare more than one shape.
2. **interrogate**. Have more than one model challenge the sketch or the diff. Do not treat that review as a patch to apply blindly.
3. **correct**. When a mistake shows up twice, make it fail a build, a type, or a test. Add a row here only after that check exists.

Do not add a logo, a trademark notice, or a file-level license banner. The Apache-2.0 text in `LICENSE` is the license.

## Rules

| Rule | Enforcement |
| --- | --- |
| Google Go style | `gofmt` and `golangci-lint` in `.github/workflows/ci.yml` |
| Branch work, no direct pushes to `main` | Review policy. CI runs on the branch. |
| Atomic commits | Review policy. A commit that needs a second commit to build is not done. |
| pstack: architect, then interrogate, then correct | This file. Use it for non-trivial changes. |
| No logo or trademark notice | This file. Do not add one. |
| A missing desired document does not delete guests | `spec.Desired` zero value. `TestZeroDesiredDoesNothing`. `TestControlPlaneLossKeepsGuest`. |
| An unknown launch record does not restart a live guest | `TestUnknownSpecDoesNotStop`. Records with `schema` other than 1 are unknown. |
| A known launch-spec change stops the guest, then starts once it is off | `TestSpecChangeStopsThenStartsOnlyWhenOff`. `TestSpecChangeRestartsAfterStop`. |
| Restart reattaches instead of launching a second QEMU | `TestRestartReattachesWithoutRelaunch` asserts one spawn and a later `query-uuid`. |
| Probe the socket stored in the launch record | `TestPinnedSocketIsProbed`. |
| Derive process, tap, MAC, and bridge names only in `internal/host/mint.go` | `TestDerivationsStayInMint`. |
| Only `reconcile.Next` builds a plan | `Plan.steps` is unexported. `gochecksumtype` checks switches on `reconcile.Step`. |
| Do not keep applying a plan after a newer document is accepted | `TestSupersededPlanDoesNotDelete`. |
| QMP and privileged commands stay behind `host.Sys` | `depguard` rules in `.golangci.yml`. |
| `reconcile` does not read the clock or the disk | `depguard` and `forbidigo` on `time.Now`. |
| No deploy, packaging, or unit installation | `.github/workflows/ci.yml` runs lint and test only. |

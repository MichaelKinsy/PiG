# Packed Provider terminal-event ordering

## Finding and source fix

The layout rehearsal's unmoved production candidate is `e04490990bc7248e314aa83873dd895362dc4bbe`. Its `extensions/sdk/provider_proxy.go` marks creation failed before waiting for previously received notifications. Commit `7bc810bf77ed9fb2cc8a420314585070e2ff6632` moves that decision after the notification barrier. The assigned base `fbcc3a7d5` and this lane's starting commit `7121b8dc7` already contain that fix. The rehearsal candidate does not. This change strengthens the regression and records qualification; it does not duplicate or claim a new production fix.

The Go SDK socket reader routes `call_result` directly to the waiting worker. It queues `model_stream_event` notifications for the extension dispatcher. A fast Provider can send `started`, its terminal event, and the call result before the dispatcher runs. The old worker then calls `markStarted(error)` first. Its `sync.Once` rejects the later valid creation acknowledgement, so the caller receives `Provider stream ended without a terminal event` even though a terminal event is queued.

The fixed worker waits for the notification prefix before inferring missing creation or termination. This is a scheduling race, not an unsynchronized memory access. Packing exposes the schedule but does not change the protocol. No retry, timeout increase, synthetic success, SDK API change, or host workaround is needed.

## Pi 0.87.1 contract

- `packages/ai/src/utils/event-stream.ts:44-57` resolves the result on a terminal push, retains the event for consumers, and ignores later pushes.
- `packages/ai/src/utils/event-stream.ts:73-86` drains queued events before ending iteration.
- `packages/ai/src/api/lazy.ts:31-38` forwards source events before ending the target stream.
- `packages/coding-agent/src/core/model-runtime.ts:638-660` uses that stream contract for simple and deferred calls.

A direct probe of the pinned upstream implementation checks success, error, and aborted terminals. It requests the result before iterating, attempts a late fallback error, and compares the complete result and event sequence. All three cases retain the original terminal. The existing `extensions-runtime/53-node-provider-object-carrier` scenario also passes all three declared Pi/PiG pairs. That scenario proves Node Provider identity/callback behavior, not the native Go dispatcher race.

## Regression and mutation evidence

`TestProviderStreamResultDoesNotOvertakeNotifications` now covers the Cartesian product of three methods (`stream`, `streamSimple`, `fetchDeferred`), three terminal reasons (`stop`, `error`, `aborted`), and two delivery states (creation still queued, creation already delivered).

The test uses `net.Pipe` and `testing/synctest`. It lets the socket reader deliver the correlated response while the dispatcher is held. It never uses sleeps or probabilistic scheduling to establish the required ordering. It compares the complete terminal message and event sequence instead of only the stop reason and event type. It also checks that creation returns before termination, joins the completion worker, and checks that the stream and callback registries are empty.

The exact old two-line ordering was temporarily restored in `provider_proxy.go`. That compiling mutation fails every buffered-creation case in every one of 20 race-enabled repetitions. The real `TestProviderObjectsAcrossSDKs/go-reader-packed` path reproduces the reported error once in 50 ordinary repetitions with that mutation. Twenty race-enabled host repetitions of the mutated caller do not reproduce it, which confirms why the deterministic dispatcher guard is necessary. The mutation is removed before final verification.

## Verification

Commands run from the repository root with Go 1.27.1. Production-path checks use Node 24.19.0, Python 3.12.3, Rust/Cargo 1.97.1, physical Go/Node executables, and temporary home/agent directories. No tool is installed or upgraded.

| Command or check | Result |
| --- | --- |
| `go test -race -count=100 ./extensions/sdk -run '^TestProviderStreamResultDoesNotOvertakeNotifications$'` | Pass after restoring the inherited source fix |
| `go test -race -count=1 ./extensions/sdk/...` | Pass |
| `go test -race -count=100 ./test/extension-conformance -run '^TestProviderObjectsAcrossSDKs/go-reader-packed$'` | Pass |
| Same packed caller with `-count=100` and without `-race` | Pass |
| `go test -race -count=3 ./test/extension-conformance -run '^(TestProviderObjectsAcrossSDKs\|TestNode(Remote)?ProviderObjectCarrier)$'` | Pass for Go/Python/Rust owner and reader, isolated and packed, fused Go, and Node same-cell/remote carriers |
| Direct Pi terminal-event probe | Pass for all three terminal reasons |
| `make parity-bin` and scenario `53-node-provider-object-carrier` | Pass, three declared pairs |
| `go tool golangci-lint config verify` | Pass |
| `make lint-changed LINT_BASE=fbcc3a7d5` and `make lint` | Pass, no suppressions added |
| `make docs-drift` and `make source-hygiene` | Pass |
| `go test -count=1 ./test/gomodule` | Pass after updating the SDK archive hash printed by `TestRootGoSumPinsNestedModuleHashes` |
| `git diff --check` | Pass |

The root `go test -json -count=1 ./...` run is not green. It reports the three inherited `test/parity/closure` failures (`TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit`). The conformance package reaches its default ten-minute package timeout while compiling a Rust packed tool-schema fixture. Its Provider-object and Provider-producer tests have already passed; the stack is in `BuildRustPackedCell`/`exec.Cmd.Wait`, not event delivery. No timeout is increased and no rerun is used to claim that full gate passed. The run also detects the expected nested SDK hash change from this test edit; the owning hash check passes after updating `go.sum`.

`go fix -diff ./extensions/sdk/...` reports pre-existing modernization suggestions in unchanged `context.go` and `model_registry_test.go`. It reports none in the changed test. Those unrelated files remain unchanged.

An attempted `GOFLAGS=-race` subprocess build fails because generated native runners explicitly disable CGO. Host race tests do not instrument those child binaries. The deterministic Go SDK test directly instruments the faulty SDK path under the race detector; fused Go also executes it in the race-enabled host. An initial temporary-HOME probe through the Node mise shim fails before loading the Provider. Subsequent qualified runs use the physical Node executable. Neither setup failure counts as behavior evidence.

## Resource disposition and retained evidence

The production code and its resource policy are unchanged. The test joins the stream worker and reader, closes both pipe endpoints, drains the event consumer, and verifies removal of stream/callback registrations. Real Host conformance shuts down each loaded topology. CPU and allocation profiles of 100 deterministic test repetitions are retained as diagnostic evidence, not a production performance claim.

Raw logs, the exact old-order mutation patch, the upstream probe, environment driver, full-suite JSONL, parity results, and profiles are retained in the `fix-packed-provider-event/` evidence directory located by the maintainer handoff. Failed runs remain present. The lane handoff names the delivered commit and evidence archive digest.

# p1-experimental: Pi 1.0.0 experimental-* tests (phase 1)

Status: READY at `ad7b52800` (code `e94fbec5d`, ledgers `ad7b52800`). Open: the owner QUESTION below; nothing else remains in this area.

Area: the seven coding-agent `experimental-*` test files split off `c1-login-fixes`: `experimental-agent-controller`, `-client-tui`, `-plugin-reload`, `-remote-runtime`, `-session-worker-lifecycle`, `-session-worker-manager` and `-transcript-provider`.

Upstream: `.upstream/v1.0.0` against `.upstream/v0.99.2`. Base: `porter/pi-0.99.1` at `48fff2a29`.

## Note for c1-login-fixes

This lane owns the seven files above. `c1-login-fixes` and `p1-login-fixes` never touched them (checked: `git diff HEAD...{p1,c1}-login-fixes` has no `experimental` path). `c1-login-fixes` should not port or map them.

## Finding

Pi 1.0.0 removed `pi-agent-core`'s harness (`packages/agent/src/harness`, including `AgentLane`, `JsonlSessionRepo` and Pico3) and rebuilt the experimental server on `pi-durable`. The session worker opens a pi-durable `Harness` on `<session>/session.sqlite` (`session-worker.ts:527`, `durable/harness-setup.ts`). The server lists a `meta.json` catalog (`session-catalog.ts`, `server.ts:383-397`). The `AgentController` contract and provider, the `Transcript` state (`ConversationView`) and the client TUI's transcript render the durable conversation. The tests use `experimental-durable-support.ts` `openFauxConversation`, a faux pi-durable Harness.

pi-durable is outside PiG's package denominator (owner decision D-H, `docs/plan/upgrade-0.99.1.md:58` and `:74`; `docs/parity/PORT_MAP.md` package scope). The 38 pi-durable test files are designed-out (`docs/plan/progress/port-992-durable.md`). PiG's experimental server still runs the Go Harness (`agent/harness`), the port of the removed 0.99.2 harness.

## Per-file decision

| upstream test (1.0.0) | disposition | reason |
|---|---|---|
| `experimental-session-worker-lifecycle.test.ts` | ported | `WorkerLifecycle` has no pi-durable dependency. `setHarnessActive` replaces `operationStarted`/`operationStopped` (`session-worker.ts:278-304`). The Go worker reports live Harness work from its run, compaction and navigation events, where Pi reports a non-empty durable task graph (`:591-592`). |
| `experimental-session-worker-manager.test.ts` | ported | The delta is the `SessionCatalogMetadata` type and the strict four-member worker metadata schema (`session-worker.ts:69-74`). Neither needs pi-durable. |
| `experimental-plugin-reload.test.ts` | partial | The reload assertions are unchanged and pass through production `CreateSessionWorkerServices`. The 1.0.0 input is a faux pi-durable conversation; the Go services take the Go Harness lane. |
| `experimental-agent-controller.test.ts` | pending | The 1.0.0 contract (`abort`, `waitForPrompt`, `cancelQueued` outcomes, `busy`) and its provider are built on pi-durable submissions, inbox and tasks. |
| `experimental-transcript-provider.test.ts` | pending | The Transcript service replicates the pi-durable `ConversationView`. |
| `experimental-client-tui.test.ts` | pending | The client TUI renders the replicated `ConversationView`, and the fixture is a faux pi-durable Harness. |
| `experimental-remote-runtime.test.ts` | pending | All 26 cases run on catalog Sessions whose only storage is the worker's pi-durable `session.sqlite`. |

None is designed out. The pending rows need an owner decision, not more lane work (see QUESTION).

## Changes

- `internal/experimental/worker_lifecycle.go`: `SetHarnessActive` replaces `OperationStarted`/`OperationStopped`; only an inactive update reconciles.
- `internal/experimental/session_worker_process.go`: `installWorkerLifecycle` keeps the live operation set and reports `SetHarnessActive(len > 0)` under one lock.
- `internal/experimental/session_catalog.go` (new): `SessionCatalogMetadata` {ID, CreatedAt (float64), Cwd, Path}, plus the projection from and to the JSONL repository metadata that the server and worker still use.
- `internal/experimental/session_worker_manager.go`, `session_worker_events.go`: the manager API, launch options and `worker_ready` use `SessionCatalogMetadata`. The decoder accepts exactly `{id (non-empty), createdAt (number), cwd, path}`.
- `internal/experimental/server_runtime.go`: the JSONL listing is projected onto the catalog metadata at the manager calls.
- Ledgers: `test-mapping-v1.0.0.json` (7 rows), `upstream-sync/v1.0.0.toml` (`session-worker-manager.ts` ported; `session-worker.ts` and `session-catalog.ts` pending with a rationale), `PORT_MAP.md` (`session-catalog.ts` 🟡, two row descriptions).

No user-visible change: the experimental server ships only in the `pig_experimental` build. No changelog entry or user doc is needed.

## Red -> green

- `TestPortWave08WorkerLifecycle` (12 cases, 1.0.0 lines): red at compile (`SetHarnessActive` undefined), green after the port. Mutations: dropping `harnessActive` from reconciliation fails `:22` and `:48`; not reconciling on an inactive update fails `:22` and `:48`.
- `TestInstallWorkerLifecycleReportsLiveHarnessWork` (new, caller boundary): reporting only the latest event fails the nested-compaction case.
- `TestPortWave08WorkerManager` (9 cases, 1.0.0 metadata): with the 0.99.2 decoder that required `storageVersion`, the adoption case `:99` fails and the run then hangs in `OpenSession`. Green after the port.
- `TestSessionWorkerMetadataWireSchema` (replaces the 0.99.2 number tests): rows confirmed with a TypeBox 1.3.27 `Check` under Node 24. A mutation accepting additional members fails the `storageVersion`, 0.99.2 metadata and `parentSessionId` rows.
- `TestPortWave08WorkerLaunchMetadataProjection`, `TestSessionWorkerAdoptsWorkerWithFractionalCreatedAt`: rewritten for the four-member contract.
- The 0.99.2-ported remote-runtime, client-TUI, transcript and controller tests still pass and keep covering PiG's current JSONL server.

## Gates

- `go vet ./internal/experimental/... ./cmd/...` (plain, `GOOS=windows` and `-tags pig_experimental`): clean.
- `go tool golangci-lint run ./internal/experimental/...`: 0 issues.
- `go test ./internal/experimental/ ./cmd/pig-experimental/`, and `-race` on the lifecycle, manager, worker and remote-runtime tests: pass.
- `make ci-contracts`: `test-inventory` OK (490 ported, 2 partial, 51 pending). Failures that exist on the base and name no file of this lane: `correspondence-check` (quiet-startup and tui-mode settings), `test-porting-release` and `known-gaps-drift` (other lanes' hot-path pending rows), `sdk-surface-drift` (`generateImages`).
- `make ci-drift`: `port-map-drift`, `source-hygiene` and `docs-drift` clean. `coverage-drift` clean after a local `make coverage` (generated files restored). `divergence-guard` fails on the base only, on `agent/harness` and `internal/codingagent/tools` literals whose upstream harness files 1.0.0 removed.
- Base failures in the area, outside this diff: `internal/experimental/services` `TestControllerMatchesPinnedProvider` and `TestModelsProviderMatchesPinnedUpstream` run the 1.0.0 providers, which import `@earendil-works/pi-durable`. `internal/experimental/mini/shared` oracles import `experimental/mini`, which 1.0.0 removed.

## QUESTION (owner)

Pi 1.0.0's experimental server is a pi-durable application. D-H keeps pi-durable out of scope. That leaves four test files pending and one partial, and the experimental server rows (`server.ts`, `session-worker.ts`, `session-catalog.ts`, `services/*`) cannot follow 1.0.0. Choose one:

- A: port pi-durable (about 17,700 source lines at 1.0.0) and move the experimental stack onto it.
- B: design out the experimental server's pi-durable parts with a numbered divergence that keeps PiG's Go-Harness JSONL stack, and mark these test files designed-out.
- C: leave them pending until the experimental server is scheduled.

This lane continues on nothing else in the area until you answer.

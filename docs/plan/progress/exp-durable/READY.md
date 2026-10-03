# READY: exp-durable (partial scope, remainder in xd-* lanes)

Branch `team/smc1/exp-durable`. Merged: d-foundation, d-storage, d-session, chord-100, d-harness-a, d-harness-b, d-env-tools. Not merged: d-harness-c (conflicts with d-harness-b in `durable/harness/{stubs,task_graph,tool}.go`).

## Done (red -> green)

| Change | Red before | Green after |
|---|---|---|
| Delete `internal/experimental/mini` (Pi removed mini/micro) | `TestRpcUpstreamChildPeer`, `TestRpcInvocationOrderUpstreamOracle`, `TestJSONConnectionUpstreamOracle` read removed upstream files | package gone; upstream-sync rows `designed-out` |
| `meta.json` Session catalog (`session_catalog.go`) and server wiring | `Session demo-1 already exists` not enforced; JSONL repository used | `TestCreateSessionRejectsDuplicateAndInvalidIDs`, `TestListSessionsSkipsEntriesWithoutValidMetadata`, `TestRunningServerRoutesServicesAndJoinsClose` (explicit `""` id rejected, UUIDv4 default) |
| `routing.SessionMetadata` interface (packages/server 1.0.0) | routing tests needed `agent/harness/session` | routing tests on an in-memory map |
| AgentController: `abort`, `waitForPrompt`; no `requestAbort`/`nextRun`/`resume`/`navigate` | contract test of 9 members | `TestAgentControllerContract`, `TestAgentControllerInitiation*`, `TestUpstreamAgentController` (3 upstream cases on the real Harness) |
| Models service follows the `pi.agent` document | lane reads and writes | `TestModelsSelectConfiguresClampsAndPersists`, `TestModelsSyncConfigurationPublishesOnlyChanges`, `TestModelsFacetOnConcreteChordHost` |
| Transcript serves `harness.ConversationView` | lane snapshot and reducer | `TestPortWave08ExperimentalTranscriptProvider` |
| Worker over `durableadapter.Session`: task-graph activity, `Resume`, no fault hook | `installWorkerLifecycle` over Go harness events | `TestWorkerStaysOpenWhileTheHarnessHasLiveTasks` (mutation: removing the subscription turns it red), `TestWorkerRetiresAfterTheInitialGraceWithoutLiveTasks`, cleanup-failure tests |
| `client_run.go` through `waitForPrompt`; chat view and client TUI on the durable view | streaming and lane-snapshot tests | `TestPromptClientSessionWaitsForTheAnswer`, `TestExperimentalClientTuiUpstream` (3 rows), `TestClientChatViewReusesStreamingComponentAndRebasesDivergentPrefix`, remote-runtime prompt cases |

`go test ./internal/experimental/...` green; `-race` on the worker, client and remote-runtime tests clean; `golangci-lint` clean for `internal/experimental` and `cmd/pig`; `go vet -tags pig_experimental ./cmd/...` clean.

Test mapping: agent-controller, transcript-provider, plugin-reload, client-tui, cli-command, cli-resolution `ported`; remote-runtime `partial`. Upstream-sync: no experimental row pending; `session-worker.ts`, `durable/*`, `vacation/*` `deferred`.

## Not done (moved to lanes)

xd-models-iface, xd-coding-harness (production `openCodingHarness`, demo presentations), xd-transcript-bridge (exact-frame bridge), xd-harness-retire (`agent/harness/**`, harness tools, divergence-guard hits; needs `pico3` relocation decision).

## Gate notes

`make test-porting-release` fails only on chord-100 and 2860 owner-approval rows. `make upstream-delta` needs `.upstream/v0.99.2`.

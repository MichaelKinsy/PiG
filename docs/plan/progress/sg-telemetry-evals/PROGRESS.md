# sg-telemetry-evals: generator-fed port of Pi 1.0.0 telemetry and evals

Lane: sg-telemetry-evals. Branch `sg-telemetry-evals` from `agg-100`, with `stubgen-100`, `d-foundation`, `d-harness-a`, `d-harness-b`, `d-session`, `d-storage` and `chord-100` merged. Upstream `.upstream/v1.0.0`.

## Status

READY at `bbf358472` (pushed to staging; this READY commit adds the changelog fragment). All 8 test files are ported and green (telemetry 2, evals 6). No hand lane had started either package: `telemetry/` held the hand port of `index.ts` carriers, `noop.ts` and `memory.ts` from earlier work, and no Go package ported `packages/evals`.

## Placement

| Upstream | Go | Notes |
|---|---|---|
| `packages/telemetry/src/index.ts` | `telemetry/context.go` (carriers, existing), `telemetry/schema.go` (schema data, `DefineTelemetrySchema`, `TypedSpanStarter`, `CreateTypedSpanStarter`) | The conditional and mapped types have no Go form; `stubgen:omit` records each. |
| `packages/telemetry/src/memory.ts`, `noop.ts` | `telemetry/memory.go`, `telemetry/context.go` (existing) | The zero value is the constructor (`stubgen:omit NewInMemoryTelemetryContext`). |
| `packages/telemetry/src/testing/*` | `telemetry/telemetrytest` | Go keeps test support in its own package, as `durable/durabletest` does. |
| `packages/evals/src/{plan,report,harness}.ts` | `internal/evals/{plan,report,harness}.go` | The `@vitest-evals/core` 0.15.0 report reader is `internal/evals/vitest_report.go`. |
| `packages/evals/evals/{acme-server,configured-runtime}.ts` | `internal/evals/{acme_server,configured_runtime}.go` | Fixtures the tests import. |
| `packages/evals/src/{cli,docker}.ts`, `createPiCodingAgentHarness` | not ported | They run Vitest eval files in Docker and drive an agent Session through vitest-evals; there is no Go host (`stubgen:omit` in `internal/evals/plan.go`). |

## Timings

| Step | Clock | Elapsed |
|---|---|---|
| Lane start (merges) | 22:06:37 | |
| Generator: telemetry stubs and 2 skeleton files | 22:08:00 | 2.0 s run |
| Generator: evals stubs (`SCOPE=module`) and 6 skeleton files | 22:10:20 | 3.2 s run |
| Telemetry green by hand: schema API, `telemetrytest`, 7 cases; one source bug fixed | 22:24 | 16 min after the generator |
| Evals green by hand: 5 files, 6 test files, 43 cases | 22:36 | 26 min after the generator |
| Evals evidence: Node oracle, Acme differential (one bug fixed), mutations | 22:43 | 7 min |
| Ledger rows flipped by the generator (`TEST_MAPPING=1`), rationales by hand | 22:44 | |
| Server `--check` | 23:03 | 1.8 s run |

Generator against hand, per package:

| Package | Generator run | Stubs proposed | Kept as generated | Hand time to green |
|---|---:|---:|---:|---:|
| telemetry | 2.0 s | 29 of 41 declarations (12 were hand-ported already; 4 stub files) | 1 enum (`TelemetryAttributeType`); the schema structs were rewritten (the generator emitted `any` for the attribute-definition unions and `float64` for the version) | 16 min |
| evals | 3.2 s | 39 of 39 declarations (4 stub files) | names of the plan and report types; every function signature changed (int counts, `CaseID`, `(T, error)` returns, no ctx for file IO) | 26 min, plus 7 min of differential evidence |

The skeleton test files were kept: every subtest name and `upstream:` line marker the generator wrote survived, except that `it.each` names now substitute their argument. The generator's value was the inventory (every case and declaration listed, the ledger rows kept current) rather than the signatures.

## Bugs found

1. `telemetry` recorder stored nil (Pi's `undefined`) attribute values and let them overwrite recorded values. `copyAttributes` now omits them, as `memory.ts` does. Red: `TestConformanceUpstream › recording › merges attributes and records ordered events`.
2. `internal/jsstring.ToFixed` dropped the sign of a negative value that rounds to zero: `(-0.04).toFixed(1)` is `-0.0` in Node. Red rows added to `TestToFixedMatchesNode`. Callers outside evals pass non-negative values.
3. The new Acme fixture wrote its NDJSON `text_delta` lines from a Go map, so the keys came out sorted. A differential run against upstream's server found it; `TestAcmeServerStreamsUpstreamBytes` pins both bodies and fails before the fix.

## Evidence

- `internal/evals/testdata/oracle.json` records upstream's `summarizeEvalObservations`, `formatEvalComparisonReport` and `readTaskObservation` on Node v24 over 101 observation sets and 36 report variants; `internal/evals/testdata/oracle.ts` regenerates it. `TestSummarizeEvalObservationsMatchesUpstream` and `TestReadTaskObservationMatchesUpstream` compare JSON structurally and the formatted text and persisted files byte for byte.
- Compiling mutations: 9 telemetry (8 killed; the survivor replaces the noop span with an equal value) and 36 evals (all killed; four first-round survivors led to new oracle rows and a sandbox case).
- `go test -race -count=10 ./internal/evals ./telemetry` passes.

## Server `--check`

`gen-go-stubs --package server --out internal/experimental/routing --check` proposes 63 declarations with no same-named Go declaration (25 are present):

| Upstream file | Missing Go names | Count |
|---|---|---:|
| `server.ts` | `Server.ServerId` | 1 |
| `testing/client.ts` | `WireChannel`, `ProtocolTestClient`, `NewProtocolTestClient`, `ProtocolTestClient.Messages`, `ProtocolTestClient.Closed`, `ProtocolTestClient.Hello`, `ProtocolTestClient.RequestService`, `ProtocolTestClient.Attach`, `ProtocolTestClient.RequestSessionService`, `ProtocolTestClient.SendMessage`, `ProtocolTestClient.SendBytes`, `ProtocolTestClient.SendFragmentedMessage`, `ProtocolTestClient.Next`, `ProtocolTestClient.NextFrom`, `ProtocolTestClient.WaitForClose`, `ProtocolTestClient.Close`, `ProtocolTestClient.Receive`, `ProtocolTestClient.MarkClosed`, `ProtocolTestClient.Fail`, `ConnectUnixTestClient` | 20 |
| `testing/host.ts` | `Deferred`, `NewDeferred`, `Deferred.Promise`, `Deferred.Resolve`, `TestHarness`, `NewTestHarness`, `TestHarness.Metadata`, `TestHarness.Closed`, `TestHarness.Terminated`, `TestHarness.AttachedClients`, `TestHarness.AttachmentReleaseCount`, `TestHarness.CloseCount`, `TestHarness.ServiceCalls`, `TestHarness.FailAttachmentRelease`, `TestHarness.FailClose`, `TestHarness.NextServiceError`, `TestHarness.NextServiceResult`, `TestHarness.AttachClient`, `TestHarness.InvokeService`, `TestHarness.Close`, `TestHarness.Terminate`, `TestHarness.GateNextClose`, `TestHarness.GateNextServiceCall`, `CreateTestServerServices`, `TestServerHost`, `NewTestServerHost`, `TestServerHost.ServerServices`, `TestServerHost.Sessions`, `TestServerHost.Harnesses`, `TestServerHost.OpenSessionCount`, `TestServerHost.NextOpenSessionError`, `TestServerHost.NextHarnessCloseError`, `TestServerHost.ResolveSession`, `TestServerHost.OpenSession`, `TestServerHost.Seed`, `TestServerHost.GateNextOpenSession`, `TestServerHost.LatestHarness` | 37 |
| `testing/server.ts` | `TestServerOptions`, `TestServer`, `CreateTestServer` | 3 |
| `types.ts` | `MaybePromise`, `SessionMetadata` | 2 |
| total | | 63 |

Disposition: 60 of the 63 are upstream's public test kit (`@earendil-works/pi-server/testing`). The Go port implements them as unexported test helpers in `internal/experimental/routing/host_test.go` and `support_test.go` (`testHarness`, `testServerHost`, `testServerServices`, `unixTestClient`), so they are not importable by other packages. `Server.ServerId` is upstream's getter; Go exposes the field `Server.ServerId`, which the generator does not match as a method. `MaybePromise` has no Go form, and `SessionMetadata` is `session.SessionMetadata` in Go. No stub was written.

## Gates

- `go test -race ./telemetry/... ./internal/evals ./internal/jsstring`, `go vet`, `golangci-lint` on the changed packages: pass.
- `make test-inventory`, `test-inventory-drift`, `correspondence-check`, `porter-check`, `interface-go-drift`, `port-map-drift`, `coverage-drift`, `divergence-consistency`: pass.
- Generator `--check` on telemetry and evals is clean, and `TEST_MAPPING=1` is idempotent.
- Inherited from the merged lanes, not from this lane: `make test-porting-release` and `make known-gaps` fail on `packages/chord/test/delta-tracker/tracker.test.ts` (designedOutCases without owner approval, from chord-100); `check-scratch-paths` flags the d-harness-b, d-session and d-storage PROGRESS files; `make divergence-guard` reports `internal/codingagent/tools/harness_bash.go:107`; `lint-changed` reports durable/harness findings. Tests in `ai`, `agent/harness/env` and `agent/harness/pico3` that need `extensions/sdk-ts/node_modules` fail in this worktree because `make parity-deps` was not run.

# sg-server-check: the 63 server declarations without a same-named Go declaration

Lane: sg-server-check. Branch `sg-server-check` from `agg-100` (`ca0490f96`). Upstream `.upstream/v1.0.0`.

## Status

READY. `gen-go-stubs --check` reports 0 missing declarations for `packages/server`, against 63 of 88 at the start. The six server test rows stay `ported`; the client `unix.test.ts` row stays `ported`.

## To p100-rest

p100-rest owns the server rows. This lane changed no disposition. It appended one sentence to the rationale of the five server rows whose tests import `packages/server/src/testing` (conformance, listener, protocol, server, unix) and of `packages/client/test/unix.test.ts`, naming the shared `routingtest` fixtures. The server tests now use those fixtures instead of private copies; their case names, `upstream:` markers and evidence anchors are unchanged. Do not run `TEST_MAPPING=1` over these rows: the generator replaces a reviewed rationale whenever the evidence list changes shape (here it would split the file-level anchors into `#Test` anchors and drop the reviewed text).

## Decision per missing declaration

| Upstream | Count | Decision | Go |
|---|---:|---|---|
| `server.ts` `Server.serverId` (readonly property) | 1 | (a) present: the field `Server.ServerId` | The generator saw only methods, and its stub `func (s *Server) ServerId()` would not compile beside the field. Fixed in the generator: a hand-written struct field now ports a class property or getter. |
| `types.ts` `SessionMetadata` | 1 | (a) present under another package | `routing.SessionMetadata = session.SessionMetadata`. Upstream's server is generic over metadata that extends `{ id }`; PiG's server routes the durable repository's metadata (`ID` is that id), which production (`server_runtime.go`) passes. The alias names it where upstream names it; `ServerHost` now uses it. |
| `types.ts` `MaybePromise<T>` | 1 | (c) no Go form | `T \| Promise<T>` collapses to a blocking `(T, error)` call. Recorded with `stubgen:omit MaybePromise` in `types.go`. |
| `testing/host.ts` `Deferred`, `TestHarness`, `createTestServerServices`, `TestServerHost` (with members) | 38 | (b) missing public API: Pi publishes `@earendil-works/pi-server/testing`, and its own server and client tests import it | Ported to `internal/experimental/routing/routingtest`. Before this lane, the routing tests held three partial private copies (`host_test.go`, `support_test.go`, `protocol_test.go`), one of which (`testServerHost`) was backed by a durable `MemorySessionRepo` that upstream's fixture does not have, and the client tests had a fourth stand-in (`unusedServerServices`). |
| `testing/client.ts` `WireChannel`, `ProtocolTestClient` (with members), `connectUnixTestClient` | 20 | (b) | `routingtest.WireChannel`, `ProtocolTestClient`, `ConnectUnixTestClient`. One client over any channel, as upstream; the routing tests had two separate clients (in-memory and Unix). |
| `testing/server.ts` `TestServerOptions`, `TestServer`, `createTestServer` | 3 | (b) | `routingtest.TestServerOptions`, `TestServer`, `CreateTestServer`. As upstream, `OnConnectionCountChanged` is accepted and not forwarded. |
| total | 63 | 3 present or no Go form, 60 ported | |

The `./testing` subpath is a separate Go package, as `telemetry/telemetrytest` is for telemetry's. `routing/types.go` delegates it with `stubgen:subpath ./testing`, so the root run no longer stubs it.

### Go mechanics in `routingtest`

- Mutable public fields (`attachedClients`, `failClose`, `nextServiceResult`, ...) are a getter and a setter under the harness lock: the server calls the harness from its own goroutines.
- `Deferred<T>.promise` is `Promise() <-chan struct{}` plus `Value() (T, bool)`; the first `Resolve` wins.
- Upstream's unexported `OpenGate` is exported because exported methods return it.
- Upstream's `TestServerHost` implements `ServerHost`; Go's `ServerHost` is a struct of capabilities, so `TestServerHost.ServerHost()` builds it.
- Optional parameters are pointers (`Hello(ctx, version *float64)`, `RequestService(..., id *string)`, `Seed(id *string)`), so an explicit empty value stays distinct from an omitted one.
- Waits (`Next`, `NextFrom`, `Hello`, `RequestService`, `WaitForClose`) take a `context.Context` for the test's bound; a wait that ends by context is unregistered. A failed send unregisters its response waiter.
- `NextFrom` and `SendFragmented` apply `Array.prototype.slice`/`subarray` index semantics (negative counts from the end, clamped).
- The Unix channel reports a socket error to `Fail` before the close reaches `MarkClosed`, and never reports the error its own `Close` causes, as a destroyed Node socket emits only `close`.

## Generator changes (`test/parity/interface-extractor`)

- A hand-written struct field (including an embedded type) ports a class property or getter (`scanGoPackage` records `Type.Field`). Without it, every hand port that keeps a readonly property as a field gets a non-compiling stub.
- `--subpath ./testing` (`make go-stubs SUBPATH=./testing`) stubs only that public subpath into its own Go package and resolves the package's other public types through the package's own `--import` mapping (`routing.Server`, `routing.SessionMetadata`). A subpath run writes no test skeletons or ledger rows; those belong to the run that includes `.`.
- `stubgen:subpath <subpath>` in a hand-written file of the root Go package leaves that subpath out of the root run, and the run deletes stale generated stubs for it.
- New cases in `test/go-stubs.test.mjs`: the field case (red before: the stub declared `func (c *Codec) State()` beside the field), the delegated-subpath case (red before: the root run still generated `testing_fake_stub.go`, and `--subpath` was ignored), and the unknown-subpath error. `npm test`: 68 pass.

## Tests

Pi has no tests of its own for `src/testing`; its server tests are the consumers. Both are covered:

- The routing tests (`TestSessionProtocol`, `TestRoutedSessionAcquisitionFailures`, the protocol, listener, server and Unix cases) now run on `routingtest`, as upstream's do on `src/testing`. The fragmented and coalesced protocol cases now send through `SendFragmentedMessage` and `SendBytes`, as `protocol.test.ts:75-145` does, instead of calling the server handler directly. `client/unix_upstream_test.go` uses `routingtest.CreateTestServerServices` and Session hosts that reject with `unused`, as `client/test/unix.test.ts:34-41`.
- `routingtest/routingtest_test.go` and `routingtest/unix_test.go` pin the fixture behavior the server cases do not reach, each citing its `src/testing` lines.

Compiling mutations, each restored afterwards:

| Mutation | Fails |
|---|---|
| attachment release not idempotent | `TestTestHarnessAttachmentRelease` |
| scripted service error ignored | routing `TestSessionProtocol`, `TestTestHarnessScriptsServiceCalls` |
| scripted result not reset to `{"ok":true}` | `TestTestHarnessScriptsServiceCalls` |
| unseeded Session opens | `TestTestServerHostOpensSeededSessions` |
| attach accepts a non-string argument | `TestCreateTestServerServicesRoutesOnlyAttachAndDetach` |
| successful close does not terminate | `TestTestHarnessCloseAndTermination` (bounded wait, 30 s) |
| null attachment envelope keeps the old attachment | `TestProtocolTestClientRequests` |
| `MarkClosed` does not reject pending waits | routing `TestSessionProtocol`, `TestRoutedSessionAcquisitionFailures`; `TestProtocolTestClientWaitsForMessages`, both Unix cases |
| a closed client accepts a new wait | `TestProtocolTestClientWaitsForMessages` |
| request IDs do not advance | routing `TestSessionProtocol`, `TestRoutedSessionAcquisitionFailures`; `TestProtocolTestClientRequests` |
| the client's own close is reported as a socket error | `TestConnectUnixTestClientCloseWaitsForTheSocket` |

## Timings

| Step | Clock (approximate) |
|---|---|
| Lane start, `--check` report (63 of 88) | 22:40 |
| Generator fixes red, then green | 22:55 |
| Root run clean (0 generated), `routingtest` stubs generated in 1.9 s | 23:00 |
| `routingtest` written, generated stubs gone, routing and client tests green on it | 23:15 |
| Fixture tests and mutations | 23:25 |

The generated `routingtest` stubs did not compile as generated: they assumed upstream's generic `Server<TMetadata>` and Chord's `Context` parameter, where the hand port uses a non-generic `Server` and `context.Context`. They served as the checklist of names; all 56 names were then written by hand, and a re-run reports them present.

## Gates

- `go vet` (linux, windows, darwin) and `go tool golangci-lint run` on `./internal/experimental/routing/...` and `./internal/experimental/client/...`: clean.
- `go test -race -count=3 ./internal/experimental/routing/... ./internal/experimental/client/`: pass. `routingtest`: `-race -count=5` pass.
- `go test ./internal/experimental/...`: pass except `services` (`TestModelsProviderMatchesPinnedUpstream`, `TestControllerMatchesPinnedProvider`) and `mini/shared` (three oracle tests). Those packages are untouched; their Node oracles import `packages/coding-agent/src/experimental/...` paths that `.upstream/current` (1.0.0) does not have.
- `make ci-drift`: every target passes except `divergence-guard`, whose 23 findings are all in untouched base code (`agent/harness/...`, `internal/codingagent/tools/harness_bash.go`).
- `make ci-contracts`: every target passes except `test-porting-release` and `known-gaps-drift` (the chord `delta-tracker/tracker.test.ts` policy row) and `format-version-inventory` (`durable/types.go`), all outside this lane.
- `gen-go-stubs --check` for `server` (root and `./testing`), `protocol` and `client`: exit 0, nothing to write.

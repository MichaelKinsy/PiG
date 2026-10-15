# gap-remote-stack: Pi 1.0.4 remote Session stack

Scope: `packages/protocol`, `packages/client`, `packages/server`, `packages/session-backends` at Pi 1.0.4 (`.upstream/v1.0.4`). Go ports live under `internal/experimental/{protocol,client,routing,routing/routingtest}`. This file is the file map for the integrator, who adds the PORT_MAP rows. PORT_MAP.md is not edited here.

## `packages/session-backends` does not exist in Pi 1.0.x

The package was last present in Pi 0.99.2 and is absent from 1.0.0 through 1.0.4 (`ls .upstream/v1.0.4/packages`). PORT_MAP's sentence naming it as part of the stack is stale at 1.0.4. It contributes no source file and no test file to the denominator.

## Source file map

Status: `ported` = a Go file reproduces the behavior and the differential proof in the last column passes. `designed out` = a TypeScript module system or Promise helper with no Go counterpart.

| upstream file | Go file | status | evidence |
|---|---|---|---|
| protocol/src/framing.ts | internal/experimental/protocol/framing.go | ported | framing_test.go (11 cases), interop `TestProtocolDifferential` (frame cases) |
| protocol/src/cbor/options.ts | protocol/cbor.go | ported | cbor_test.go, differential `cbor` |
| protocol/src/cbor/encoder.ts | protocol/cbor.go | ported | cbor_test.go, differential `cbor` + every encode case |
| protocol/src/cbor/decoder.ts | protocol/cbor.go | ported | cbor_test.go, 165 raw CBOR vectors + ~12k mutated frames |
| protocol/src/protocol.ts | protocol/messages.go | ported | messages_test.go (20 cases), differential encode/decode of 96 schema probes |
| protocol/src/codec.ts | protocol/messages.go, protocol/json.go | ported | same; error names and messages compared byte for byte |
| protocol/src/cbor/index.ts, protocol/src/index.ts | package `protocol` exports | designed out | barrel re-exports |
| client/src/client.ts | client/client.go, client/invocation.go, client/callbacks.go | ported | client_upstream_test.go (15 cases), interop client matrix |
| client/src/connection.ts, client/src/types.ts | client/connection.go | ported | client_upstream_test.go, interop matrix |
| client/src/transport.ts, client/src/errors.ts | client/transport.go | ported | client_upstream_test.go |
| client/src/unix.ts | client/unix.go | ported | unix_upstream_test.go (8), unix_transport_*_upstream_test.go (4), interop matrix |
| client/src/promise.ts | none | designed out | `createPromiseResolvers` is a Promise helper; Go uses channels |
| client/src/index.ts | package `client` exports | designed out | barrel |
| server/src/server.ts | routing/server.go | ported | conformance + server + protocol tests, interop matrix, wire differential, ordering test |
| server/src/session-router.ts | routing/session_router.go | ported | conformance_test.go (20), interop |
| server/src/errors.ts | routing/errors.go | ported | conformance, interop (error codes and messages equal) |
| server/src/types.ts, connection.ts, listener.ts | routing/types.go | ported | compile-time contract; used by every routing test |
| server/src/transports/unix/{listener,preset,types,address}.ts | routing/unix.go (+ unix_errors_*.go) | ported | unix_test.go (6), unix_connection test, listener_test.go |
| server/src/testing/{host,server,client,index}.ts | routing/routingtest/{host,server,client}.go | ported | used by the ported server and client tests |
| server/src/index.ts, transports/unix/index.ts | package `routing` exports | designed out | barrels |

Source-file totals (portable = not designed out): protocol 6/6, client 6/6, server 14/14. All portable files were already ported before this lane; PORT_MAP did not count them (0 of 36 rows existed). The new evidence is the wire differential below.

## Test file map

Every upstream test file is ported at case level. A script comparing `test(`/`it(`/`.each` expansions with the `upstream: packages/<file>:<line>` citations in `*_test.go` finds equal counts for all 12 files (protocol 3, client 3, server 6). `test/parity/interfaces/test-mapping-v1.0.4.json` already carries a `ported` disposition for each. Fixtures `server/test/fixtures/stale-socket-server.mjs` and `client/test/fixtures/stale-socket-server.mjs` are replaced by a re-executed test binary child (`TestStaleSocketOwnerProcess`); `client/test/support.ts` is `client/support_upstream_test.go`.

## Wire proof against the pinned Node packages

`internal/experimental/interop` runs the Pi 1.0.4 TypeScript sources of `protocol`, `client`, `server` and `chord` unmodified. `testdata/loader.mjs` maps the workspace package names to `.upstream/current/packages/*/src`; external dependencies (typebox) resolve from `extensions/sdk-ts/node_modules`. Node 24 strips the types. Run `go test ./internal/experimental/interop/`.

| test | what it proves | result |
|---|---|---|
| `TestProtocolDifferential` | The Go and Node protocol packages accept and reject the same bytes with the same errors, and re-encode accepted messages identically. Corpus: 96 valid and invalid client and server messages, every one-byte mutation and every truncation of each, every split point, concatenation, 165 raw CBOR vectors (all major types, widths, indefinite lengths, tags, floats, depth and container limits, key order, duplicates, invalid UTF-8), frame header edge cases (about 13,000 cases). | 0 differences. Mutations of `DefaultMaxCborDepth` and `IsSupportedProtocolVersion` fail it. |
| `TestClientServerInterop` | The Go client against the Go server, Go client against Node server, Node client against Go server all produce the transcript of the Node client against the Node server (28 steps: hello, catalogue, attach, detach, echo of mixed JSON values, undefined and null results, error codes and messages, wrong server, stale attachment, 1 MiB result, 2 MiB argument, 50 pipelined requests, cancel, reattach, disconnect, reconnect, dispose) and the servers observe the same host events. | passes. A changed error message fails it. |
| `TestServerWireDifferential` | The Go and Node servers answer 712 raw byte streams (hello variants, version mismatch, request before hello, garbage, oversized and zero length frames, truncation, one-byte mutations of hello and request frames) with the same bytes and close the same connections. | passes |
| `TestServerRequestOrdering` | 300 pipelined requests on one connection reach the host in the order sent, as on the Node server. | failed before this lane; see below |
| `TestServerServicesMutationOrderOverTheWire` | The real server-wide services receive 300 pipelined `create` calls in the order sent. | failed before this lane; see below |

Not compared byte for byte: Node re-emits an input object's own key order, while a Go message is a typed value and always emits schema order. Both are the same CBOR map to every decoder (`canonicalSteps` in `interop/protocol_test.go`). The completion order of concurrent requests is unspecified in both implementations; clients correlate by id.

## Bugs found and fixed

Pi's `handleRequest` runs synchronously up to the service call, and a chord provider applies a member's synchronous prefix during invoke, so one connection's calls reach their service in the order sent (`server.ts:317-345`, `session-router.ts:54,207`, chord `provider.ts:234`). The Go stack started a goroutine per call and reordered them:

1. **Server routing.** 300 pipelined session requests arrived shuffled on most runs. `requestTurn` in `routing/server.go` makes each request wait until the previous one on the connection has entered the host; `SessionRouter.QueueServiceCall` fixes the admission order synchronously; `routing.ServiceInitiator` is the endpoint admission boundary (`TestServerRequestOrdering`).
2. **Server-wide services** (`pi.session-management` create/remove/attach/detach, `pi.presentation-plugins`). The mutation tail took its position at method entry, so pipelined Create then Remove could invert. `RoutedServerServiceAttachment.BeginInvokeService` now exists; `chord.ServiceMemberAdmitter` lets the implementation reserve its tail position during admission (`TestServerServicesAdmitMutationsInCallOrder`, `TestServerServicesMutationOrderOverTheWire`).
3. **Worker agent controller** (prompt, steer, followUp, cancelQueued, abort, compact). A steer sent after a prompt could reach the durable conversation first. The controller reserves its conversation-queue position at admission (`TestAgentControllerOperationsReachTheConversationInCallOrder`). `SessionWorkerServices.BeginInvoke`, the worker control loop and the worker manager's `RoutedSessionAttachment.BeginInvokeService` now begin calls in message order, so the order holds from the server socket to the worker process.

New chord API: `ServiceMemberAdmitter`, `InitiatingServiceEndpoint`, `BeginEndpointInvoke`. A call to an implementation that exposes neither an initiator nor an admitter starts on a goroutine that has begun running before the next admission; only those implementations lack an exact first-instruction guarantee, and none of the stack's own services is among them.

Audit of the other goroutine-per-message sites in `internal/experimental` (client callbacks, rebinding transitions, Radius reconnect, session worker manager demand and shutdown writes, coordinator forwarding): each is either ordered by an explicit tail or slot (`enqueueSendLocked`, `source.transition`) or is not a request path. The three sites above were the request paths.

## Live streaming and discovery

`TestClientServerInterop` includes the `stream` step (subscribe to a replicated-state Chord service, four ordered mutations, unsubscribe, no update after dispose), `stream-unknown-service` and `catalogue-session`, against a Chord provider hosted by the Go server and by the pinned Node server. `TestDiscoverUnixServersAgainstLiveServers` runs the Go and the pinned Node `discoverUnixServers` over one directory of Node servers, Go servers, a mismatching identity, a silent socket, a stale file, a directory and non-canonical names; both find the same routes.

## Not covered

- Windows and macOS: the interop tests are `!windows`, as the upstream Unix transport is. `GOOS=darwin` and `GOOS=windows` vet only.
- Pi 1.0.4 ships no WebSocket transport in these packages; the browser/mobile route is a custom `ServerListener` and `ByteTransportFactory` over the same envelopes.

## Notes for pig-pocket-arch

- Protocol version 8, CBOR subset, 4-byte big-endian length frames, 16 MiB default limit: proven wire-compatible in both directions with Pi's Node client and server.
- The Go server hosts any `routing.ServerHost`; a Session host supplies `ResolveSession`, `OpenSession`, and server services. The Node client reaches it unchanged over a Unix socket.
- A Pocket transport implements `routing.ServerListener` (server) or `client.ByteTransportFactory` (client); framing, handshake, routing and cancellation come from this stack.
- A host that needs strict call ordering implements `routing.ServiceInitiator`, or `chord.ServiceMemberAdmitter` when it hosts a Chord provider.

## Review corrections (rev-gap-remote-stack)

The review found and fixed these defects in the ordering work above:

- `routing/server.go` `requestTurn`: a request that ended before it reached the host (wrong server, duplicate subscription, no server services) released its successor at once, so the successor could overtake its predecessor. The turn now waits for its predecessor before it releases its successor. Guard: `TestServerRequestOrderingAcrossRejectedRequests`.
- Agent controller: Pi's controller members call `conversation.submit/abort/compact` and `harness.abortSubmission` directly (`agent-controller-provider.ts:18-60`); their order is fixed when each commit joins the Session line (`session.ts` `commitWith`), and an abort's Promise then waits for idle. The ordering above held each operation's position until it finished, so every operation sent after an abort waited for idle and ignored its own cancellation. `durable/session` `CommitWith` now reports line admission through `internal/lineadmission`, and the controller releases the next operation at that moment. Guards: `TestAgentControllerOperationAfterAbortDoesNotWaitForIdle`, `TestCommitReportsLineAdmissionBeforeEarlierCommitsFinish`, `TestDurableConversationOperationsReportLineAdmission`.
- Mutation tickets used one context key for every tail, so work on another tail consumed a reservation. A ticket is now bound to its tail. Guard: `TestMutationTicketIsIgnoredByAnotherTail`.
- The worker control loop and `RoutedSessionAttachment.BeginInvokeService` changes had no failing test. Guards: `TestWorkerControlBeginsOperationsInMessageOrder`, `TestRoutedSessionAttachmentQueuesWorkerOperationsInAdmissionOrder`.

The source map counts 32 upstream source files (protocol 8, client 8, server 16), not 36. `server/src/testing/index.ts` is a barrel.

`SessionRouter` calls `BeginInvokeService` while it holds its lock. A `ServiceInitiator` must not block or call back into the router before it returns.

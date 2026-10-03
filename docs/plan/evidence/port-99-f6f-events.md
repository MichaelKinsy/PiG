# Lane port-99-f6f-events: evidence, integrator rows and the D86 draft

Upstream reference (identical in 0.87.1 and 0.99.1): `.upstream/v0.99.1/packages/coding-agent/src/core/event-bus.ts` (33 lines): `emit` is `EventEmitter.emit` (15-17), `on` wraps the handler in `safeHandler` that awaits it and prints `Event handler error (<channel>):` for a throw (19-25), and returns `emitter.off` (27).

## What landed

| commit | content |
|---|---|
| 42ba15468 | red, Go: `extensions/sdk/event_bus.go` stub, 5 SDK wire tests, 15 conformance scenarios in `test/extension-conformance/events_bridge_*_test.go`. Node control passes, Go fails on the stub (`docs/plan/evidence/port-99-f6f-events-go.red.txt`) |
| 86150f13a | green, Go: SDK, Host (`protocol_events.go`, `event_bus.go`), xref op `json`, regenerated `runtime-node.zip` |
| 348f6ad48 | mutation-driven cases (settle, nested emit lane, top-level BigInt), stress and hung-listener cases |
| c7b15a3d1 | red, Python and Rust: stubs, unit tests, the same conformance rows in both languages (`...-py-rs.red.txt`) |
| 97326d8dc | green, Python and Rust |
| fefb2896c | fused Go extension case, citation fix |
| f77d88e21 | refactor of the native call fast path |

Mutation checks: `docs/plan/evidence/port-99-f6f-events-go.mutations.txt` (13 mutations; 3 first survived and got their own cases).

## Wire (native realms)

- `events.on {channel, handlerId, value: true}` and `events.emit {channel, value: true, json}`; `events.off {handlerId}`.
- Host to native: request `events.dispatch {handlerId, channel, json}`; notify `events.release {handlerId}` after `events.off` once no dispatch snapshot can still call the listener.
- The Host assigns the realm (one per OS process); native connections send no `xref.hello`. Handler IDs are process-unique in each SDK.
- Node to native: the emitter realm computes `JSON.stringify` (xref op `json`, `runtime-node/xref.mjs`); `undefined`, symbols and functions become `null`, `-0` becomes `0`, NaN and the infinities become `null`, and a throw (BigInt, cycle, throwing getter) fails only that listener's delivery with `Event handler error (<channel>): <message>` on the Host's stderr.
- Native to Node: the Host wraps the JSON as `{"$x":"json","v":...}`; each Node listener decodes its own copy.
- A native emitter's emission is settled by the Host before the call returns (`busEmission.settle`), because a native emitter has no microtask queue.
- The first native `events.on` or `events.emit` switches the Host registry on and moves the in-heap node listeners over (`migrateEventBus`), so a Node-only session keeps upstream's in-heap `EventEmitter` and pays nothing.

## Results

Go SDK `go test -race ./extensions/sdk/...` passes. `go test ./coding/extension/host/subprocess/` passes except `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` (needs Xvfb, absent on this host; unrelated to this lane). `cargo test --offline` (50 tests) and `PYTHONPATH=extensions/sdk-py:<cached pytest> python3 -m pytest extensions/sdk-py/tests` pass. `go test -race -count=24` of the conformance rows (no stress) under 8 CPU burners on 4 cores with `GOMAXPROCS=4`: see the load section of the report.

## Rows the integrator applies (I did not edit these files)

`docs/extension-sdk-surface.md`, section `pi.events (EventBus)`: `pi.events.emit` and `pi.events.on` become implemented for Go (`Extension.Events()`/`Context.Events()` then `EventBus.Emit`/`EventBus.On`), Rust (`Extension::events()`/`Context::events()` then `EventBus::emit`/`EventBus::on`) and Python (`Extension.events`/`Context.events` then `EventBus.emit`/`EventBus.on`). Remove the `pi.events.*` line of the exceptions list (currently "Go, Rust, Python | No native SDK event-bus bridge exists...").

`docs/extension-api-parity.md`, row `pi.events`: append "Native realms (Go, Rust, Python) join the same Host registry by value: `emit` and `on` mean the same as for a Node realm, listeners run in registration order across every realm, and a listener's error is reported without reaching the emitter or later listeners. A node emitter's payload reaches a native listener as the `JSON.stringify` view of the emitter's object when that listener runs, so it sees what earlier listeners wrote; a payload with no JSON form fails only that listener's delivery. D86 records the by-value boundary." Add the test names `TestNativeEventBus*` and the SDK wire tests to the evidence column.

`docs/extension-api-parity.md`, async contract paragraph for `pi.events`, native realm: "`Emit` is a blocking host call. The Host snapshots the channel's listeners and sends each one `events.dispatch` in order; a native listener answers when its handler returns, and the emitter waits for each answer, so a handler can call `Emit` (nested dispatch) and two crossing emitters do not deadlock, because an SDK serves requests while its own call is in flight. Errors and panics come back as the dispatch's error and are reported on the Host's stderr. There is no timeout: a listener that never returns blocks its emitter. A native emitter's emission is settled before `Emit` returns, so a node listener's first continuation has run. Nested emits use the dispatching request as parent so they are ordered and cancelled with it. Unsubscribe removes the listener and it stays callable for dispatch snapshots taken before that until the Host releases it. A closed connection drops that runtime's listeners."

`test/parity/async-contracts.toml`, entry `packages/coding-agent/src/core/event-bus.ts` (currently `deferred`): a contract for the native realm with evidence `test:test/extension-conformance/events_bridge_test.go`, `test:test/extension-conformance/events_bridge_stress_test.go`, `test:extensions/sdk/event_bus_test.go`, `test:extensions/sdk-py/tests/test_event_bus.py`, `test:extensions/sdk-rs/src/event_bus_tests.rs`.

`docs/parity/DIVERGENCES.md`: D83 boundary 5 ("Native realms") and the D77 sentence about native SDKs become D86's text below; D83 keeps boundaries 1 to 4 and its "Scope: only the five boundaries" becomes four.

SDK READMEs (`extensions/sdk`, `extensions/sdk-py`, `extensions/sdk-rs`): add a short `pi.events` section (`On`/`Emit` names, by-value JSON, call through the context inside a handler).

`docs/parity/PORT_MAP.md`: `packages/coding-agent/src/core/event-bus.ts` maps to `coding/extension/eventbus.go` (in-process) and `coding/extension/host/subprocess/event_bus.go`, `protocol_events.go` (Host registry), `extensions/sdk/event_bus.go`, `extensions/sdk-py/pig_sdk/event_bus.py`, `extensions/sdk-rs/src/event_bus.rs` (native SDKs). `test/parity/interfaces/*` needs `make interface-go` (new exported Go API: `sdk.EventBus`, `sdk.EventBusHandler`, `Extension.Events`, `Context.Events`).

## D86 draft (flagged for the owner; not merged)

**D86 Native pi.events payloads cross by value.**

What: a Go, Rust or Python extension receives a `pi.events` payload as JSON. Node listeners in different processes share the emitter's original object through cross-process references (D83), but a native runtime has no JavaScript objects to reference. So a native listener cannot observe object identity, the emitter's later writes, or another listener's mutations (Pi passes one object to every listener, `event-bus.ts:15-17`), and a native emitter's mutations after `Emit` are not seen. A node emitter's payload reaches a native listener as `JSON.stringify` of the emitter's object: values with no JSON form (`undefined`, functions, symbols) are `null`, Maps, Sets and Dates take their JSON forms, and a BigInt or a cycle fails the delivery to that listener. A payload from a native emitter reaches each node listener as a fresh object.

Why: the wire between a native SDK and the Host carries JSON. Extending cross-process references to native languages would need a foreign object model in Go, Rust and Python and has no upstream counterpart.

Effect: code that relies on Pi's shared-object contract works between Node extensions (D83) and does not work across the native boundary; code that treats events as messages works everywhere.

Remove when: not applicable while native SDKs have no JavaScript heap.

Call-site markers, when the record is approved: `coding/extension/host/subprocess/protocol_events.go` (`newEmitPayload`, `nodeJSONView`), `runtime-node/xref.mjs` (`jsonOutcome`).

Evidence: `TestNativeEventBusNodePayloadIsTheEmittersJSONView`, `TestNativeEventBusUnserializableNodePayloadReachesNoNativeListener`, `TestNativeEventBusNativeEmitterReachesNodeAndOtherCellListeners`.

## Open points and residuals

- A Node realm spawned between `busRoutedForSpawn` and `noteNodeRealm` (two adjacent calls in `host.go` and `packed_go.go`, outside this lane) while a native realm makes its first bus call would keep the in-heap `EventEmitter`. Closing it needs those two calls to be one locked step; the window is microseconds. Handed to 6D as a one-line stub request.
- `host_calls.go` (6D-owned) gains the one-word change that passes `me` to `handleEventBusCall`. `runtime-node.zip` and `runtime_node_digest_generated.go` are regenerated (`go generate ./coding/extension/host/subprocess`); a merge conflict there is resolved by regenerating.
- The Rust SDK runs a dispatch that arrives before `ready` once ready has arrived (its handler `Context` needs the ready payload); Go and Python run it on arrival. Implementation variance; the emitter waits either way.
- `unsubscribe` uses an untied host call (its function has no context), so it waits behind other untied calls of the same extension. A handler that must unsubscribe promptly while another goroutine holds an untied blocked `Emit` waits for it. The docs tell authors to emit through the context.
- A Go number in a payload decodes as `float64` (`any`), as `OnEvent` payloads do.

## Generated ledgers the integrator regenerates

- `go run ./test/parity/cmd/sdksurface` currently fails with `test/parity/sdk-surface-exceptions.toml: exception for "pi.events.*" is stale: none of its cells is missing`. Delete that exception, then regenerate `docs/extension-sdk-surface.md`. The generator already recognizes the new API: `pi.events.emit` is implemented as `EventBus.Emit` (Go), `EventBus::emit` (Rust) and `EventBus.emit` (Python), `pi.events.on` as `EventBus.On`, `EventBus::on` and `EventBus.on`. The totals move to Go 404 implemented and 24 missing, Rust and Python 404 and 25.
- `make interface-go interface-recommendations-generate` for the new exported Go API.

## Stress under load

`TestNativeEventBusStressKeepsEveryDeliveryAndBoundedState` (100 subscribers over four realms, 10,000 emits from all four, one slow native listener; exact per-realm delivery counts, heap growth under 64 MiB, goroutine growth under 17), unloaded: Go 80 s, Python 105 s, Rust 115 s. Under `-race`, `GOMAXPROCS=4`, 4 cores shared with 8 CPU burners: Go and Rust pass; Python did not finish inside the first 10 minute hang guard (an interpreter running 250,000 dispatches per realm, one thread each, on a starved core: about 6 times its unloaded time). Nothing was lost or wrong; the run was slow. The guard is a hang detector, so it is now 40 minutes, and the Python run then passed with exact counts. (A hang still fails: the hung-listener case and the mutation M8 show that.)

# gap-xproc: one cross-process reference mechanism for D83 and D73

Status: PLAN, owner-decided. Base: `release/0.3.0-base2` (`606595120`). Upstream: Pi 0.87.1 (`.upstream/v0.87.1`). Owner decisions (2026-09-29): Q1 = B (strict isolation and exact standalones join the shared bus through xref; D77 is removed once evidence is in), Q2 = A (narrowed D83 naming only the section 8 residuals 1-5; everything else matches Pi). Lane gap-d78 builds on the API in section 4.

## 1. Pi's observable contract

### 1.1 Event bus (D83)

- `packages/coding-agent/src/core/event-bus.ts:12-33`: one `node:events` `EventEmitter`. `emit` (`:15-17`) calls `emitter.emit(channel, data)` and returns `undefined`. `on` (`:18-28`) wraps the handler in `safeHandler = async (data) => { try { await handler(data) } catch (err) { console.error(...) } }` and returns `() => emitter.off(channel, safeHandler)`. `clear` (`:29-31`) removes every listener.
- Consequences, all in one JavaScript heap and one microtask queue:
  1. Every listener receives the same `data` reference. Identity holds (`a === b`, `data.self === data`), and functions, cycles, class instances, getters, symbols, `toJSON`, Maps, Promises and prototypes are the author's own objects.
  2. Listeners run in registration order. Each listener's synchronous prefix (up to its first `await`) runs before the next listener and before `emit` returns. The emitter sees prefix mutations immediately after `emit`.
  3. Post-`await` mutations run as microtasks/macrotasks after `emit` returns. They are not visible to the emitter's synchronous code after `emit`; they are visible to later tasks. Microtask continuations interleave in one FIFO queue shared by the emitter and all listeners.
  4. A listener that retains `data` sees the emitter's and other listeners' later mutations, and its own later writes are visible to them.
  5. Nested `emit` inside a listener dispatches synchronously, reentrantly, with a listener snapshot taken per `emit` (EventEmitter clones its listener array per emit).
  6. `emit("error", x)` with no listener throws `x` from `emit` (EventEmitter rule; `safeHandler` never throws synchronously because it is `async`).
  7. `newListener`/`removeListener` events fire from `on`/`off`.
- `packages/coding-agent/src/core/extensions/loader.ts:153-205` (`createExtensionRuntime`): `invalidate` unsubscribes every tracked bus subscription; `trackEventBusSubscription` wraps the unsubscribe. `loader.ts:436-447`: `pi.events.emit` and `pi.events.on` call `assertActive()` first; `on` tracks the unsubscribe and records it in `loadingUnsubscribers` while the factory loads (failed factories discard their subscriptions).
- `packages/coding-agent/src/core/resource-loader.ts:163,258,559,579,602,962`: one bus per resource loader, supplied to every extension (including inline factories); an SDK caller can supply its own `eventBus`.

### 1.2 Live main-process objects (D73)

- `packages/tui/src/tui.ts:253-257,280`: `OverlayHandle.unfocus({ target: Component | null })`. `tui.ts:745-775`: `unfocus` with options resumes to or focuses exactly `unfocusOptions.target`, which may be `null`, the extension's own component, or any main-process component the extension holds.
- Extensions hold live objects of the Pi process: the `tui` passed to `ctx.ui.custom` factories, editor components, theme instances, and the components they reach from those. Prototype patches on imported classes affect the main process because it is one heap.

## 2. Current PiG state and reusable checkpoints

- Base bus: `coding/extension/host/subprocess/runtime-node/runtime.mjs:24-29` creates one process-local `createEventBus()` (Pi's own module). `runtime.mjs:1234-1245` wires `pi.events` with `assertActive`, tracking and loading rollback. Same-process members share Pi's bus exactly (D77).
- Foreign delivery on the base does not exist: every Node process has its own bus. This applies to explicit isolation and standalones (D77, approved), and also to ordinary factories that `planCells` splits by `nodeRecoveryGroup` or `quarantine:` (`node_recovery.go:60-78`, `cell_plan.go:84-94`). The second case silently breaks D77's own claim that ordinary factories share Pi's bus. It is in scope here.
- Native SDKs expose no bus. `extension.EventBusController` is the in-process Go bus (`coding/extension/eventbus.go`).
- Synchronous path: `runtime.mjs:568-583` `Connection.callSync` blocks the Node main thread with `Atomics.wait` in `ProviderSocket.waitUntil` (`provider-socket.mjs:126-135`), which pumps every socket of the process. `Connection.onEnvelope` (`runtime.mjs:497-503`) services restricted synchronous requests (`provider_sync`, `provider_object_callback_sync`, `autocomplete.sync`) immediately, whether the process is idle or blocked. Host side: `host_calls.go:98-200` runs calls on per-parent-request lanes; a lane never waits for another lane. `provider_object.go:30-110` already routes a synchronous call from one extension to another's owner.
- Checkpoint `ad56349de` (merged by `8ac67ca8e`, on the extensions h4 lane branch, based on `f30eefde6`, 734 commits behind this base):
  - `coding/extension/eventbus.go`: `Undefined`, `PublishProjected`, `SubscribeOwned` with snapshot-release accounting, `Clear` release ordering. Reusable as is.
  - `host/subprocess/event_bus.go`: `hostEventBus` (Host-owned shared controller, `SetEventBus`, `DrainEventBus`, per-connection `remoteEventBusSubscriptions`, liveness holds, `events.on/off/emit`, reverse `event_bus` request with acknowledgment after the synchronous prefix). Reusable; its `event_bus_snapshot` JSON projection is replaced.
  - `runtime-node/event-bus.mjs` `RemoteEventBus` and the `runtime.mjs` early-socket/`registrationPending`/`dispatchEventBusRequest`/`reportFactoryFailure` changes. Reusable; payload transfer changes.
  - Tests in `source_event_bus_test.go`: shared controller, same-cell identity, retained original across await, `TestSourceEventBusCrossProcessMutationPendingOwnerDecision` (red: emitter `{n:0}`, Pi `{n:1}`), snapshot parent scoping, unhandled `error`, pre-register handlers, failed-factory rollback, isolated/packed ordering, nested emission order, drain cancellation. All are kept; none is loosened.
  - Session-scope invalidation (`session_scope.go`, `c8a0a6596`) is required so a captured factory API is invalidated on its original connection scope.
  - The checkpoint puts `isolation: isolated` members on the shared Host bus. That changes D77 and is Q1.

## 3. Design overview

One mechanism, **xref** (cross-process references): each address space is a **realm**. A realm exports its objects by reference and imports foreign references as proxies. Every operation on a proxy is executed by the owning realm on the original object, synchronously, through the existing synchronous host call path. The Host routes operations between realms and is the lease authority. The event bus and the D73 surfaces are clients of xref; neither invents its own transfer.

Properties:
- Identity: a realm resolves its own reference to the original object (no copy, no proxy). A foreign realm keeps one canonical proxy per remote object while it is reachable, so `a === b` holds across deliveries and listeners.
- Mutation visibility: there is exactly one copy of state, in the owner. Reads and writes from anywhere, before or after an `await`, and through retained aliases, observe it in the owner's serial order.
- Non-JSON contents: functions (call and construct execute in the owner with unwrapped receivers), cycles, class instances, accessors, private fields, Maps, Dates, Promises and `toJSON` work because the owner runs the real operation.
- Reentrancy: owner operations and bus listener prefixes are restricted synchronous requests, serviced while the process is idle or blocked in `waitUntil`, at any nesting depth.

### 3.1 Realms and routing

- Realm IDs are Host-assigned and opaque: one per OS process (`n<k>` for Node processes, including every member socket of a packed cell), `h` for the Go Host. Native SDK processes get one when gap-d78 implements them. The Host passes the ID at spawn (`PIG_XREF_REALM`); a packed cell's members share it because they share a heap.
- The Host maps each connection to its realm and stamps the source realm on every forwarded operation; it never trusts a realm named by the payload as the caller's identity.
- An operation on reference `(realm R, id)` is sent as a restricted synchronous request to any live connection of R (preferring the exporting connection). If R has exited, the operation fails with `Error("cross-process reference owner exited")` in the caller.

### 3.2 Value encoding (Pig-owned wire, one current unversioned shape)

A transferred value (`XValue`) is exactly one of:
- JSON `null`, boolean, string, or finite number other than `-0`;
- `{"$x":"undefined"}`;
- `{"$x":"number","v":"NaN"|"Infinity"|"-Infinity"|"-0"}`, `{"$x":"bigint","v":"<decimal>"}`;
- `{"$x":"symbol","wk":"<name>"}` (well-known), `{"$x":"symbol","for":"<key>"}` (registry), `{"$x":"symbol","realm":"<R>","id":"<n>","desc":<string|null>}` (unique, owned);
- `{"$x":"intrinsic","name":"%Object.prototype%"}` and the other ECMAScript intrinsic prototypes/constructors, so `getPrototypeOf` and `instanceof` resolve to the reader's intrinsics;
- `{"$x":"ref","realm":"<R>","id":"<n>","kind":"object"|"array"|"function"}`;
- `{"$x":"json","v":<JSON>}`: a by-value copy, used only where the source has no reference identity to preserve (Go Host or native SDK data). It decodes to fresh plain objects.

Objects are never inlined, so `$x` tags cannot collide with author data. Arrays and functions keep their `kind` so the reader's proxy target is an array or function and `Array.isArray`/`typeof` match.

### 3.3 Operations

Extension→Host synchronous call `xref.op`; Host→owner restricted synchronous request `xref.op`:

```json
{"ref":{"realm":"n1","id":"42"},"op":"get","args":[XValue...]}
```

`op` is one of the ECMAScript Proxy traps: `get`, `set`, `has`, `deleteProperty`, `ownKeys`, `getOwnPropertyDescriptor`, `defineProperty`, `getPrototypeOf`, `setPrototypeOf`, `isExtensible`, `preventExtensions`, `apply`, `construct`. Keys and receivers are `XValue`s. The result is `{"value":XValue}` or `{"threw":XValue}`; a thrown owner error is rethrown in the caller as the owner's error object (a proxy), with its message and stack readable.

Proxy invariants: the reader keeps a shadow target per proxy. When the owner reports a non-configurable property or non-extensibility, the reader defines it on the shadow before returning, so V8's invariant checks agree with the owner (standard membrane shadow-target synchronization). Array `length` uses an array shadow.

### 3.4 Lifetime

- Owner export table: `id → { value, sent }`, plus `WeakMap value → id` so one object keeps one ID while exported. `sent` counts transmissions.
- Host lease ledger: `(owner, id) → { received, importers: realm → delivered }`. The Host increments `received` when it forwards an owner message carrying the ref and increments `delivered` per importer delivery (bus fan-out delivers one owner transmission to many realms).
- Importer: `(realm, id) → { WeakRef(proxy), delivered }` and a `FinalizationRegistry`. When a proxy is collected, the importer sends `xref.release {realm, id, count: delivered}` (batched notify). A re-delivery after collection creates a fresh entry with its own count; the old finalizer releases only its own count.
- When every importer count is zero, the Host sends `xref.unpin {id, count: received}` to the owner. The owner deletes the entry only if `sent === count`; a transmission in flight keeps it alive and will be accounted.
- Delivery to the owner realm itself decodes to the original and holds no lease.
- Realm exit: the Host drops the dead importer's counts (possibly unpinning) and marks a dead owner's references failed. Connection retirement, reload and Host shutdown close the ledger; the exporting process's table is discarded with it.
- Cross-realm cycles (A's object holds B's proxy whose object holds A's proxy) are not collected until one realm exits or the owning scope is retired. Pi collects them. This is a residual (section 8) unless distributed cycle collection lands; it is shared with D78's obligation.

### 3.5 Scheduling, reentrancy and deadlock freedom

- `xref.op` and bus listener invocations are restricted synchronous requests. `Connection.onEnvelope` services them immediately in both the idle event loop and `waitUntil`, exactly like `provider_object_callback_sync` today.
- Deadlock freedom: a realm thread is always either running JavaScript (which terminates or blocks in `waitUntil`), idle, or in `waitUntil`, and the last two service every restricted request. Host lanes are keyed by parent request and never wait for another lane, and the Host holds no lock across a forwarded request. So a cycle A→B→A→B of nested synchronous waits always progresses. Evidence: a four-level nested emit/read test and a two-emitter crossing test with `-race -count=3`.
- No author timeout is added: an owner that never returns blocks its caller, as a hung Pi listener blocks Pi. Process exit or Host shutdown fails pending operations.
- Atomicity boundary: each owner operation is atomic. A synchronous run in one realm is not atomic against an independent run in another realm when it blocks in a synchronous host call, because a foreign request can execute at that wait point. Pi serializes independent tasks. This affects only truly concurrent independent triggers; it is a residual (section 8).
- Microtask ordering: after `emit` returns, the emitter queues one barrier microtask that waits in `waitUntil` until every foreign listener realm of that emission reports that its microtask queue drained after the prefix (the listener realm sends the mark from a `setImmediate` scheduled at prefix end). This gives Pi's order for a single post-`await` generation (emitter microtasks queued before `emit`, then the listener's first continuation, then emitter microtasks queued after `emit`). Deeper microtask-only chains of a foreign listener finish before the emitter's later microtasks, where Pi interleaves them FIFO. This is a residual (section 8). Macrotask-separated continuations have no residual.

### 3.6 Event bus on xref (D83)

- Port the checkpoint's Host-authoritative ordered listener registry (`hostEventBus`, `SubscribeOwned`, `PublishProjected`, liveness holds, drain, rollback, session-scope invalidation) onto this base.
- Payloads cross as one `XValue`. Same-realm listeners receive the original (the checkpoint's direct predicate, now "same realm"); foreign listeners receive the canonical proxy. The checkpoint's `event_bus_snapshot` JSON projection is deleted for Node realms.
- Go Host listeners (SDK embedders using `SetEventBus`) are not Pi callers. They keep a by-value JSON snapshot (`$x:"json"` in the other direction), recorded as additive D19, not as D83. Go Host publications reach Node listeners as `$x:"json"` values.
- Fast path: when one realm holds every listener and is the only emitting realm, emit and on stay in-heap with Pi's own `createEventBus` (no host round trip). The Host switches the bus to routed mode synchronously, before the joining realm's first `on`/`emit` returns, by fetching the owner realm's ordered listener list. Benchmark gate: single-cell emit/on cost within noise of the base.
- `emit("error")` with no listener, `newListener`/`removeListener`, `clear`, nested snapshots and failed-factory rollback follow section 1.1 through the Host registry, as in the checkpoint tests.
- Membership (Q1 = B): every Node realm joins the one shared bus: packed cells, recovery groups, quarantined members, strict isolation and exact standalones. Process, crash and memory isolation remain; a hung foreign listener blocks its emitter, as in Pi.

### 3.7 Main-process objects on xref (D73)

- The Host realm `h` exports Go-owned objects as opaque references (`kind:"object"`, no enumerable properties, operations other than identity throw `TypeError`). Node realms export their components normally.
- `OverlayHandle.unfocus({ target })` accepts `null` (Pi's `setFocus(null)`), a component of the caller's realm that the Host has mounted (own overlay, custom component, or installed editor component: the Host resolves the reference to its mounted key), and an opaque Host reference to a main-process component handed to the extension (the Main Screen editor and mounted components). The Host applies Pi's `tui.ts:745-775` resume/focus rules on its UI loop.
- Unchanged residuals: prototype patches to Go-owned UI objects, arbitrary Go component methods and fields, and `initTheme` affecting the host theme. D73 is narrowed, not removed.

### 3.8 Platforms

- Linux, macOS, Windows use the same socket/named-pipe transport and `Atomics.wait` worker; xref adds no transport. `FinalizationRegistry` timing is nondeterministic on every platform, so lifetime tests force collection (`--expose-gc` under the test hook) and poll the ledger with a bounded wait. Windows fixtures stay portable (`e071a5658` pattern).

## 4. Public API and wire impact (for gap-d78)

Wire, in `coding/extension/host/subprocess/protocol.go` (shared SDK contract, no version or negotiation):
- Call `xref.op` (sync lane), request `xref.op` (restricted synchronous), notify `xref.release {realm,id,count}` (extension→Host), notify `xref.unpin {id,count}` and `xref.drop {realm}` (Host→owner).
- `XValue` as in section 3.2; `XRef {realm,id,kind}`.

Go, new package `coding/extension/host/xref` (Host-internal routing, stable for gap-d78):
- `type Realm string`; `type Ref struct{ Realm Realm; ID string; Kind string }`; `type Value = json.RawMessage` holding an `XValue`.
- `type Ledger` with `Received(owner Realm, refs []Ref)`, `Delivered(importer Realm, refs []Ref)`, `Release(importer Realm, ref Ref, count uint64) (unpin *Unpin)`, `DropRealm(r Realm) []Unpin`, `Len() int` (cleanup evidence).
- `func Scan(values []Value) ([]Ref, error)`: finds refs in a flat `XValue` list (objects are never inlined, so this is not recursive).
- `type Router` on the Host: `Op(ctx, from Realm, ref Ref, op string, args []Value) (Value, thrown bool, err error)`.
- Host realm export: `func (h *Host) ExportOpaque(obj any) Ref` and `func (h *Host) ResolveOpaque(Ref) (any, bool)`.

Node, new `runtime-node/xref.mjs` (runtime-internal):
- `class Realm { constructor(id, transport); encode(value) → XValue; decode(xvalue) → value; serve(args) → result; onUnpin(args); onDrop(args); stats() }`.
- `transport` is the process's `Runtime`: `callSync("xref.op", …)`, `notify("xref.release", …)`.

Native SDK readers/owners (Go/Rust/Python) implement the same wire as gap-d78 needs; a native reader uses an explicit handle (`Get/Set/Call/Keys`) rather than a language proxy. Until then native realms remain by-value (`$x:"json"`) and state so explicitly.

Public Go API: additive only. `extension.Undefined`, `EventBusController.PublishProjected`, `SubscribeOwned` (from the checkpoint) and `Host.SetEventBus`/`DrainEventBus`. No existing signature changes, so no deprecated aliases are needed. `extensions/sdk*` are not touched by this lane; the SDK wire types gain the new methods only when an SDK implements them (gap-d78).

No silently stale traps: a proxy never returns cached property values; every read goes to the owner. A dead owner fails loudly. By-value `$x:"json"` data is documented at each Host-listener API.

## 5. Work breakdown

- W0 Plan and Q1/Q2 (this commit and a `QUESTION gap-xproc:` commit).
- W1 Port the checkpoint's shared Host bus onto this base (eventbus.go, event_bus.go, event-bus.mjs, runtime/cell/early-socket changes, session-scope invalidation). Keep `TestSourceEventBusCrossProcessMutationPendingOwnerDecision` red and unchanged. Membership predicate: ordinary factories in all realms; strict per Q1 (default until answered: D77 unchanged).
- W2 `xref` wire + Go ledger/router + Node realm (encode/decode, proxy handler with shadow targets, serve, intrinsics, symbols), with unit tests: identity, each trap, errors, invariants, arrays, functions/constructors, Map/Date/Promise, symbols, accessors, private fields.
- W3 Lifetime: export counts, importer finalization, unpin/drop, realm exit; ledger `Len()`/`stats()` cleanup tests with forced GC; process-exit and reload retirement.
- W4 Bus payloads over xref: replace the foreign snapshot for Node realms; same-realm original; nested/crossing reentrancy; fast path + switch; benchmark.
- W5 Microtask barrier (section 3.5) and its ordering tests.
- W6 D73 narrowing: `unfocus({target})` for null/own/Host-opaque targets; Host realm opaque exports.
- W7 Records: update D83/D77/D73 per results and owner answers; `docs/extension-api-parity.md` async contract; `test/parity/async-contracts.toml` if needed; `make generate`.

## 6. Test and evidence plan

Pi-derived differential tests (each asserts Pi's value, derived from `event-bus.ts`/`tui.ts` and run against real Pi 0.87.1 modules where a harness exists; the same script runs isolated, packed and cross-realm):
- Synchronous prefix: foreign `data.n++` → emitter sees `{n:1}` (the kept checkpoint test turns green without edits).
- Post-await: foreign listener writes after `await Promise.resolve()` and after `setImmediate`; emitter's synchronous code after `emit` sees the old value; its later task sees the new one; the single-generation microtask order equals Pi's.
- Retained: listener retains `data`; emitter mutates later; listener's next task reads it; listener writes later; emitter reads it.
- Identity: two listeners in one foreign realm get `===` payloads; the same object emitted twice is `===`; a proxy emitted back to its owner is the original; `data.self === data`.
- Non-JSON: function call with receiver, class instance method with private field, getter, Symbol keys, Map/Date methods, Promise `await`, `toJSON` not invoked on transfer, cycles.
- Reentrancy: listener emits back to the emitter's realm while the emitter is blocked (A→B→A→B, depth 4); two realms emitting into each other concurrently; no deadlock, correct order.
- Errors: listener throw reported as Pi's `Event handler error (<channel>):`; unhandled `error` channel throws the original error object in the emitter; owner exit fails pending operations.
- Lifetime: after listeners drop references and GC, both realms' tables and the Host ledger return to zero; after realm exit the ledger has no entries for it; reload leaves nothing.
- Same-realm regression: `TestNodeRuntimeEventBusMatchesPi`, `TestNodeCellInterleavedGoFactoryKeepsOrderAndBus`, scenarios `extensions-runtime/31-extension-runtime-surface` and `52-interleaved-node-admission` stay strict and pass all declared runs.
- Recovery split: a quarantined member and a healthy cell share the bus after bisection.
- D73: `unfocus({target:null})` and own-component target versus Pi's `tui.ts` behavior on the Main Screen through the full host path.
- Performance: benchmark of emit/on (single realm and routed) and per-operation latency with allocations; profile the routed path.
- Gates: build, vet, `-race -count=3` on the subprocess and extension packages, `make lint`, `make test-porting-release`, the strict scenarios above (tmux), and the ledger gates after `make generate`.

## 7. Removal conditions

- D83: narrowed (Q2 = A) to exactly section 8 residuals 1-5 once every other section 1.1 behavior has a distinguishing Pi/isolated/packed test and cleanup evidence. Remove when those residuals close.
- D77: removed (Q1 = B) with the strict/standalone membership tests, using the divergence tooling.
- D73: narrowed to the remaining main-process surfaces (section 3.7); not removed.

## 8. Residuals expected after this lane

1. Cross-realm cycles are collected only at realm/scope retirement (shared with D78).
2. Atomicity of independent concurrent runs across realms at synchronous wait points.
3. Microtask interleaving beyond the first post-`await` generation of a foreign listener.
4. Brand checks on proxies of exotic objects (`util.types.isMap(proxy)`, `Map.prototype.get.call(proxy)`), inherent to any Proxy membrane; method calls through the proxy work. Because V8 offers no hook, `util.inspect` with `customInspect: false` prints the proxy's empty shadow, `showProxy: true` shows the Proxy wrapper, `util.types.isProxy()` is true, and a non-extensible foreign payload prints inside `Proxy(...)`; Node 26.0.0 introduced the `Proxy(...)` wrapper (nodejs/node#61029), so on Node 25 and earlier the shadow prints unwrapped and the non-extensible payload prints as in Pi (Q3).
5. Native SDK realms by value until gap-d78.

## 9. Owner questions

Q1 (D77). The xref bus can share `pi.events` with strictly isolated extensions and exact standalones without colocating them.
- A: keep D77. Strict/standalone keep their own bus. xref serves ordinary factories split by crash recovery/quarantine and Host listeners.
- B: strict/standalone join the shared bus through xref. Crash and memory isolation remain; a foreign listener that hangs blocks the emitter, as in Pi. D77 is removed.
- C: B, but only for extensions that opt in with a flag. This adds configuration surface.
Owner decision: B.

Q2 (D83 end state). Residuals 2 and 3 are inherent to multiple event loops without global serialization. Accept a narrowed D83 naming only section 8 residuals, or require global serialization of extension JavaScript (every realm acquires one Host turn per task, which removes multi-process parallelism)?
Owner decision: A. D83 is narrowed to exactly section 8 residuals 1-5.

Q3 (QUESTION xproc-r, 4487f24d5). A non-extensible foreign payload and `util.inspect` with `customInspect: false` or `showProxy: true` cannot print as in Pi on Node 26. Approve the boundary-4 wording, or require another design?
Owner decision (2026-09-29, Michael Kinsy): approve exactly two effects under D83 boundary 4: `util.inspect` of a foreign non-extensible payload, or with `customInspect: false`, prints `Proxy(...)` because V8 offers no hook. Every other inspect, clone and write case matches Pi. Nothing else is widened: the nesting level of owner-formatted kinds and separate copies of an object shared by two owners stay open defects.
Owner decision (2026-09-29, Michael Kinsy, addendum): boundary 4 also approves Opus R3 a (`util.inspect` with `showProxy: true` shows the Proxy wrapper for a foreign payload) and R3 d (`util.types.isProxy()` returns true). R3 b (Promise nesting level) and R3 c (cross-owner shared clone) stay open defects under D83 Scope, targeted at 0.3.1.

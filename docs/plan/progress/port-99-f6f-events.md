# Lane port-99-f6f-events: the `pi.events` bridge for native SDKs

Slice `port-99-f6f-events` (family 6, sub-lane 6F-events; split doc `docs/plan/progress/family-6-split.md` section 4). Branch `port-99-f6f-events` from staging `porter/pi-0.99.1`.

## Lead answers (binding)

1. Native API uses upstream's names and semantics (`Emit`/`On`), on the extension SDK surface only. The in-process `extension.EventBus` (`Publish`/`Subscribe`) is untouched, so nothing breaks and there is no shim.
2. The bridge is parity with upstream `pi.events`; no divergence ID. A behavior that provably cannot match upstream gets a drafted D86 record in the evidence file and is flagged, not merged.

## Design decisions

- Wire: reuse `events.on`, `events.off`, `events.emit`, `events.dispatch`. Native calls add `"value": true` on `events.on` and `events.emit`; `events.emit` from a native realm carries the payload under `json` (not `data`), so the Host's xref reference scan never reads user data. Host-to-native `events.dispatch` carries `{handlerId, channel, json}`.
- Realm: the Host assigns a native realm per OS process (`processIdentity`), no `xref.hello`.
- Routing: the first native `events.on`/`events.emit` switches the Host registry on and migrates in-heap Node realms (`migrateEventBus`), so Node-only sessions keep upstream's in-heap `EventEmitter`.
- Payload to a native listener from a Node emitter: `JSON.stringify` computed in the emitter realm by a new xref op `json` (one call per native listener, when that listener runs, so it sees earlier listeners' writes as in upstream's single shared object).
- Native emitter: the Host settles the emission itself (a Go/Rust/Python emitter has no microtask), then the call returns.

## Status

Green for Go, Rust and Python (commits and results: `docs/plan/evidence/port-99-f6f-events.md`). Red evidence: `port-99-f6f-events-go.red.txt`, `port-99-f6f-events-py-rs.red.txt`. Mutation checks: `port-99-f6f-events-go.mutations.txt`. Integrator rows, D86 draft and residuals are in the evidence file.

Commit order: 42ba15468 red (Go), 86150f13a green (Go), 348f6ad48 mutation-driven and stress cases, c7b15a3d1 red (Python, Rust), 97326d8dc green (Python, Rust), fefb2896c fused case and citations, f77d88e21 refactor, then test additions for primitive payloads, newListener and dispatch during load.

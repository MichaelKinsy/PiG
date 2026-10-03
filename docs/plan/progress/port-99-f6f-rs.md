# Lane port-99-f6f-rs: the Rust SDK for the upstream 0.99.1 extension API (family 6, sub-lane 6F-rs)

Branch `port-99-f6f-rs` from the 6D contract commit 906c8fe28, merged with staging `porter/pi-0.99.1` (family 4). Scope: `extensions/sdk-rs`, conformance rows in new files (`test/extension-conformance/extension_api_rust_test.go`, fixture `testdata/extapi-rust`). Upstream: `.upstream/v0.99.1/packages/coding-agent/src/core/extensions/{types,runner,loader}.ts`, `core/virtual-models.ts`, `core/agent-session.ts` (loadout).

## Commits
- red: ported wire tests (`extensions/sdk-rs/tests/extension_api_wire.rs`, 13 cases), conformance rows (7 tests x isolated/packed), fixture, SDK data declarations and `unimplemented!` stubs. Evidence: `docs/plan/evidence/port-99-f6f-rs-red.txt`.
- merge of staging porter/pi-0.99.1 (family 4) a7734de2b, before the red commit, for `agent.AgentToolResult.StructuredContent`.
- green: Rust SDK implementation (`extension_api.rs`, `context.rs`, `extension.rs`, `protocol.rs`, `events.rs`, `lib.rs`); tests strengthened after mutation (2 rows); README and `changelog.d/port-99-f6f-rs.md`; post-load `registerTool` and provider model-type rows. Evidence: `docs/plan/evidence/port-99-f6f-rs-green.txt`.

## Counts
Rust wire tests 14 (11 red-then-green in the first pass, 2 green in red because the declaration is the implementation, 1 added with the post-load registerTool mutation). Conformance 8 tests x isolated and packed (all red on the stubs, green on the integration tree). Mutations: 20 Rust wire, 7 conformance, all killed after two tests were strengthened.

## Deferred, and stubs for other lanes
- Rows need lane 6D's host to go green: this branch alone has the contract stubs (`handleMcpServerCall`, `handleVirtualModelCall`, `handleExecuteToolCall`, `StatePayload` fill). Green run on base + staging port-99-f6d.
- `Extension.replaceable` has no wire field; provider `images` and `classifiers` implementations have no wire form. Not done, no code.
- The `pi.events` bridge in Rust (`Emit`/`On`) waits for lane 6F-events's wire.
- Integrator: `docs/extension-api-parity.md`, `docs/extension-sdk-surface.md`, `test/parity/sdk-surface.toml` rows and the async contracts (text in the green evidence), then `make generate`.

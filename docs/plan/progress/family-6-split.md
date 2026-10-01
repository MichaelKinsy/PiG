# Family 6 split: coding-agent core

Planning document for the family 6 lanes of the 0.87.1 to upstream 0.99.1 port. It splits `coding-agent core` (plan section 10, item 6) into six lanes that run in parallel with minimal file overlap, and plans the `pi.events` bridge for the native SDKs. Sources: `docs/plan/upgrade-0.99.1.md` (section 4.4, Appendix A/B/C, section 13), the `.upstream/v0.87.1` and `.upstream/v0.99.1` mirrors, and the Go tree at the time of writing. Test counts are cases in the upstream file (`old → new` for changed files).

## 1. Ground rules for every lane

Each lane follows the family workflow already used for families 1 and 2: red commit (ported tests plus field/signature stubs), red evidence under `docs/plan/evidence/`, green commit, refactor, mutation checks, load test, Opus review branch. In addition:

1. **One owner per production file.** A lane edits only the files listed as owned by it. A change to a file owned by another lane goes through that lane, or through a one-line stub agreed in advance (section 5).
2. **Generated and shared ledgers are the integrator's.** Lanes do not commit `test/parity/interfaces/*`, `docs/parity/PORT_MAP.md`, `docs/parity/pending-tests-batches.md`, `test/parity/coverage.md`, the known-gap files, or `docs/parity/DIVERGENCES.md` ID allocations. They record what must change in their evidence file; the integrator regenerates once per joint run.
3. **Divergence IDs** come from the lead (next free ID is D85 at the time of writing). A lane asks before it writes `D<N>`.
4. **Cite `.upstream/v0.99.1/...:line`** for every ported behavior and every per-case designed-out reason (L7, L1). The old v0.87.1 citations stay until the pin move.
5. **Public API breaks** carry a `CHANGELOG` migration note and no shim (L14).
6. **Oracles.** Tests that replay a recorded Pi run (`coding/testdata/rpc33-observation/**`, `ai/testdata/**`) fail on their version guard until the pin moves. A lane regenerates an oracle it changes behavior for from the real upstream 0.99.1 packages (recipe in the family 2 detail of the progress file) and never from Go output.
7. **Load test** any goroutine, process, stream or session change (`-race -count=24` under CPU burners, `GOMAXPROCS=4`, L4). No blocking extension IPC may run on the TUI input/render loop.

## 2. Lanes at a glance

| lane | scope | owned Go areas | depends on | wave |
|---|---|---|---|---|
| 6A | settings, session persistence, session lifecycle messages | `internal/codingagent/settings*.go`, `session_manager*.go`, `coding/settings.go`, `coding/session_manager.go`, `coding/runtime*.go`, `cmd/pig/startup_*.go` | none (family 1-5 not required) | 1 |
| 6B | model runtime, registry, catalog protocol, resolver, llama classifier | `coding/model_*.go`, `coding/native_provider.go`, `internal/codingagent/model_*.go`, `internal/codingagent/llama/`, `cmd/pig/model.go`, `cmd/pig/startup_model.go` | family 1 (done), family 3 (`Classify`), family 2 (`thinkingLevel`) | 1 |
| 6C | resource loader, package manager, source info, `builtin:` naming | `coding/resource_loader.go`, `coding/packagecontent/`, `internal/codingagent/resource*.go`, `cmd/pig/package_*.go`, `cmd/pig/extensions.go` | family 5/9 (theme resource shape, one test) | 1 |
| 6D | extension API host side: types, runner, wrapper, loader API, `sdk.ts` and the wire contract | `coding/extension/*.go` (not `eventbus.go`), `coding/extension/host/**` (not `event_bus.go`), `coding/extension_bridge.go`, `coding/sdk*.go`, `coding/services.go` | family 4 (`ToolResult` fields), contract commit gates 6E and 6F | 1 (contract commit first) |
| 6E | AgentSession: nested tool calls, tool loadout/orchestration, virtual models, prompt dispositions, compaction over nested calls | `coding/session*.go` (new `coding/nested_tool_calls.go`, `coding/virtual_models.go`), `internal/codingagent/compaction/` | 6D contract, 6B (routing through the model runtime), family 4 (`runToolCall`, `parentToolCallId`) | 2 |
| 6F | the four SDKs (D-C) and the `pi.events` bridge | `extensions/sdk/`, `extensions/sdk-rs/`, `extensions/sdk-py/`, `extensions/sdk-ts/`, `test/extension-conformance/`, `coding/extension/host/subprocess/event_bus.go` | 6D contract; the bridge (6F-events) has no dependency and can start now | 2 (6F-events: 1) |

Wave 1 lanes need no other lane's production code. Wave 2 lanes write their red tests against stubs on day one and turn them green after the 6D contract commit lands. The interactive UI (family 9), RPC/print/CLI surfaces (family 7) and MCP/codemode/tool-search (family 8) consume these lanes and are not part of family 6.

## 3. Lane details

### 6A. Settings and session persistence

**Upstream tests (Appendix C unless noted).**
- `test/settings-manager.test.ts` (46 → 51)
- `test/default-tools-setting.test.ts` (5 → 6)
- `test/session-manager/file-operations.test.ts` (28 → 31)
- `test/session-manager/tree-traversal.test.ts` (31 → 31, +2 −2 case ids)
- `test/session-file-invalid.test.ts`, `test/session-id-readonly.test.ts`, `test/startup-session-name.test.ts`, `test/stdout-cleanliness.test.ts` (the upstream change is the `--import` URL in the harness; port at the Go caller boundary and record the substitution per case)
- `test/suite/agent-session-runtime.test.ts` (12 → 12, message text: "Send a message before cloning or forking it.")

**Upstream sources.** `core/settings-manager.ts` (104 changed lines), `core/session-manager.ts` (47), `core/agent-session-runtime.ts` (4), `config.ts`, `main.ts` (session part).

**Behavior.**
- New settings: `fullscreenWheelScrollLines`, `getOrCreateDeviceId()` (`settings-manager.ts:1172`, consumed by family 3's ChatGPT sign-in and by interactive mode), `getSettings`, the `defaultTools` `+name`/`-name` merge with project on top of user, and `-builtin:<name>` entries in `extensions`.
- Session file is created on the first user message, not on start (`#10000`). Covers the "not saved yet" errors, `--session-id`/`--name` startup paths, crash-before-first-reply persistence and `--no-session`. Poisoned-history cases: empty session, header-only file left by 0.87.1, resume of such a file.

**Go targets.** `internal/codingagent/settings.go` and siblings, `internal/codingagent/session_manager.go`, `coding/runtime.go`, `coding/session_boundaries.go` for the error texts.

**Dependencies.** None. The `/settings` row and the wheel callback belong to family 9, which reads the accessor this lane adds. Agree the accessor names in the red commit.

**Hot spots.** `internal/codingagent/settings.go` (the struct is also read by 6B for `types=` catalog options and by 6C for `-builtin:`): 6A adds every new field in its red commit, even those it only stores, so other lanes read fields and never add them. `coding/session.go` is not owned by 6A (see 6E): 6A only edits the persistence call site through `session_manager.go`.

### 6B. Model runtime, registry and catalog

**Upstream tests.**
- New: `test/model-runtime-images.test.ts` (7), `test/model-runtime-classifiers.test.ts` (1), `test/model-catalog-protocol.test.ts` (1)
- Changed: `test/model-resolver.test.ts` (51, +1 −1), `test/model-runtime-cloudflare-compat.test.ts` (2), `test/remote-catalog-provider.test.ts` (7 → 8, currently designed-out: re-record the reason for the new case), `test/llama-extension.test.ts` (11 → 13)

**Upstream sources.** `core/model-runtime.ts` (277 changed lines), `core/provider-composer.ts` (236), `core/model-registry.ts` (57), `core/model-resolver.ts` (12), `core/remote-catalog-provider.ts`, `extensions/llama/provider.ts`, `extensions/index.ts` (llama), `defaults.ts`.

**Behavior.** `generateImages`, `classify`, typed accessors and the `types=chat,image,classifier` catalog request, extension model lists with chat/image/classifier entries, `models.json` providers keep built-in image generation, the classifier model per chat model for llama.cpp plus the autoload preset context-window fix (`#10077`), default-model resolution for a provider with no default chat model. Shared-path proof uses the four provider shapes of plan section 6 rule 5.

**Go targets.** `coding/model_runtime_create.go`, `coding/model_registry_facade.go`, `coding/model_catalog_backend.go`, `coding/model_availability.go`, `internal/codingagent/model_registry.go`, `internal/codingagent/model_catalog_*.go`, `internal/codingagent/llama/`, `cmd/pig/model.go`, `cmd/pig/startup_model.go`.

**Dependencies.** Family 1 is done (typed models). `classify` waits for family 3's `Models.Classify`; write the red tests now against a stub. `thinkingLevel` comes from family 2 (done).

**Hot spots.** `coding/extension/provider.go` and `coding/extension/model_info.go` carry the discriminated model config on the wire: 6D owns the declaration in its contract commit, 6B consumes it. `internal/codingagent/model_registry.go` is 6B-only. `cmd/pig/startup_model.go` is shared with family 7 (CLI flags); 6B lands first.

### 6C. Resource loader, package manager and `builtin:` naming

**Upstream tests.**
- Changed: `test/resource-loader.test.ts` (42 → 50), `test/package-manager.test.ts` (123 → 127, +8 −4 case ids), `test/package-command-paths.test.ts` (31 → 33), `test/extensions-discovery.test.ts` (30 → 31), `test/agent-session-dynamic-tools.test.ts` (`builtin:read` source path), `test/system-prompt.test.ts` (docs list gains `docs/mcp.md`, 1 case; needs the docs mirror from the plan phase)
- New: `test/resource-loader-theme.test.ts` (2; needs the family 5 capability overrides and a family 9 theme JSON, write the red now and mark the two cases pending on that dependency in the evidence file, not skipped)

**Upstream sources.** `core/resource-loader.ts` (262), `core/package-manager.ts` (85), `core/source-info.ts` (17: `BUILTIN_PATH_PREFIX`), `package-manager-cli.ts`, `core/system-prompt.ts`, the naming half of `core/extensions/loader.ts`.

**Behavior.** `builtin:<name>` in errors, diagnostics and RPC source info (replaces `<inline:name>`/`<builtin:name>`), `--extension builtin:<name>`, `--no-extensions` also disables built-ins including the llama.cpp provider, warning when an extension replaces a built-in tool/command/flag, managed git packages stop auto-installing peer dependencies plus the host-provided modules warning, pinned git `-e` refresh after the ref changes, theme resource loading by color mode.

**Go targets.** `coding/resource_loader.go`, `coding/packagecontent/`, `internal/codingagent/resource_source_info.go`, `internal/codingagent/resources.go`, `cmd/pig/package_*.go`, `cmd/pig/extensions.go`.

**Dependencies.** Loader naming and 6D share `core/extensions/loader.ts`: 6C owns discovery, path naming and source info; 6D owns extension registration and API binding. Family 8 registers the built-in extension code (`builtin:mcp`, `builtin:codemode`, `builtin:tool-search`) through the registry this lane defines: define it in the red commit as `builtin:<name>` to factory lookup, empty at first.

**Hot spots.** `cmd/pig/extensions.go` (family 8 adds built-ins to it) and `cmd/pig/args.go` (family 7 adds `--extension builtin:`). 6C edits `extensions.go`; families 7 and 8 rebase on it. `docs/parity/PORT_MAP.md` rows for `resource-loader.ts` are integrator-owned.

### 6D. Extension API host side and the wire contract

**Upstream tests.** `test/extensions-runner.test.ts` (50 → 50; helper change `getSettings: () => ({})`, hash changed so the whole file is re-ported and re-hashed), `test/image-resize-callers.test.ts` (`ExtensionToolContext`), `test/sdk-stream-options.test.ts` (9 → 10: `createAgentSession` with a resource loader and an extension factory). The runner behaviors that no upstream test covers (D-C items) are proved by the conformance rows of 6F.

**Upstream sources.** `core/extensions/types.ts` (353 changed lines), `core/extensions/runner.ts` (101), `core/extensions/loader.ts` (71, registration half), `core/extensions/wrapper.ts` (8), `core/tools/tool-definition-wrapper.ts` (21), `core/sdk.ts` (38), `core/agent-session-services.ts` (12).

**Behavior (D-C).**
- API: `registerMcpServer`, `unregisterMcpServer`, `getMcpServers`, `registerVirtualModel`, `unregisterVirtualModel`, `getSettings`, `Extension.replaceable`.
- Events: `provider_stream_event` (family 2 supplies the provider side), `mcp_servers_change`.
- Tool definition fields: `exposure` (`direct`, `model-only`, `codemode`, `deferred`, `hidden`), `namespace`, `annotations`, `outputSchema`, `prepareLoadout`, `defaultActive`.
- Tool results: `structuredContent`, `isError`.
- Context: `ExtensionToolContext`, `ctx.executeTool()`, `getCallableTools()`, `parentToolCallId` on tool events, `ExecuteToolOptions`.
- Provider config: chat/image/classifier entries.

**Contract commit (first deliverable, no behavior).** Add every new type, field and event name to `coding/extension/*.go`, `coding/extension/host/subprocess/protocol.go` and the in-process API, with `not implemented` bodies. 6E and 6F cannot compile their red tests without it, so it lands first and quickly. The shape follows `.upstream/v0.99.1/packages/coding-agent/src/core/extensions/types.ts`; the wire names follow the existing conventions in `protocol.go` (no version field, no negotiation).

**Go targets.** `coding/extension/api.go`, `events.go`, `tool.go`, `tool_registry.go`, `provider.go`, `context.go`, `coding/extension/host/subprocess/protocol.go`, `host_calls.go`, `coding/extension_bridge.go`, `coding/session_tool_registry.go` (tool exposure only), `coding/sdk*.go`.

**Hot spots.** `coding/extension/host/subprocess/protocol.go` (936 lines) and `coding/extension/events.go` (935 lines) are shared with 6F and 6E. Rules: 6D is the only lane that edits them in wave 1; 6F-events puts its wire code in `event_bus.go` and a new `protocol_events.go`; later edits by 6E/6F are additive and land after the 6D contract commit.

### 6E. AgentSession: nested tool calls, virtual models, orchestration

**Upstream tests.**
- New: `test/nested-tool-calls.test.ts` (6), `test/suite/agent-session-tool-orchestration.test.ts` (3), `test/compaction-nested-calls.test.ts` (1), `test/virtual-models.test.ts` (14), `test/suite/virtual-models.test.ts` (13), `test/jev-router-example.test.ts` (2, with `examples/extensions/jev-router.ts` run in the Node runtime)
- Changed: `test/agent-session-concurrent.test.ts` (7; `steer` and `followUp` resolve `"queued"`), `test/suite/agent-session-compaction.test.ts` (28; seeds a compactable session before the auth check), `test/suite/regressions/5943-session-start-notify.test.ts` (7; helper rewrite, theme part waits on family 9's theme API), `test/suite/regressions/7150-rpc-prompt-during-compaction.test.ts` (1; `PromptDisposition`)

**Upstream sources.** `core/agent-session.ts` (924 changed lines, the largest single delta), `core/nested-tool-calls.ts`, `core/virtual-models.ts`, `core/agent-session-runtime.ts`, `core/compaction/*` (already tracked under `compaction`).

**Behavior.**
- Nested calls: `parentToolCallId`, bounded `nestedCalls` on results, nested cost rolled into the caller, cancellation of children with the parent, compaction over nested calls, tool loadout (`prepareLoadout`, exposure, callable tools).
- Virtual models: registration, routing per request, `routedModel`, `VIRTUAL_MODEL_STATE_ENTRY` (`"pi.virtual-model-state"`) custom entry in the session file, `/session` cost per physical model, the footer reads `routedModel`.
- `prompt`/`steer`/`followUp` return `PromptDisposition`; family 7 consumes it for RPC.

**Go targets.** New files `coding/nested_tool_calls.go`, `coding/virtual_models.go`, `coding/session_loadout.go`; touched in place: `coding/session_tool_registry.go`, `coding/session_prompt.go`, `coding/session_queue_events.go`, `coding/session_compaction_extensions.go`, `internal/codingagent/compaction/`.

**Dependencies.** 6D contract commit (types, `executeTool`, registration calls); family 4's `runToolCall` and `parentToolCallId` in agent events; 6B (`ModelRuntime` routes a virtual model to a physical one); the poisoned-history matrix of plan section 6 rule 6 applies (nested calls in errored/aborted turns, orphaned nested calls, model switch mid-run).

**Hot spots.** `coding/session.go` (3,002 lines) is the reason this lane keeps to new files and small call-site edits. No other family-6 lane edits it; families 7, 8 and 9 touch it only through the `Session` API this lane defines. A `Session` field added here lands in the red commit's stub so consumers compile.

### 6F. The four SDKs and the `pi.events` bridge

**Upstream tests.** None in TypeScript targets the SDKs; the oracle is the in-process Go reference plus Pi's own behavior through the Node runtime. Deliverable per capability: a conformance row in `test/extension-conformance/` that fails when any one SDK is broken, bound to a value distinguishable from that SDK's fallback (`AGENTS.md`, SDK rules).

**Scope.**
1. Each D-C capability listed under 6D, in Go (`extensions/sdk`), Rust (`extensions/sdk-rs`), Python (`extensions/sdk-py`) and the TypeScript declaration adapter (`extensions/sdk-ts`, pin bump verified with `make test-sdk-ts`).
2. The `pi.events` bridge (section 4).
3. `docs/extension-api-parity.md` and `docs/extension-sdk-surface.md` rows, one row per capability, edited by the integrator from the lane's evidence file to avoid merge conflicts in those large tables.

**Sub-lanes.** `6F-go`, `6F-rs`, `6F-py` are file-disjoint and run in parallel after the 6D contract commit; `6F-ts` is small. `6F-events` starts immediately (it touches only `event_bus.go`, the new protocol file and the SDK event files).

**Hot spots.** `extensions/sdk-py/pig_sdk/__init__.py` (2,810 lines, single file) belongs to `6F-py` alone. `extensions/sdk-rs/src/context.rs` (3,171 lines) and `extension.rs` (2,948 lines) belong to `6F-rs` alone. `test/extension-conformance/conformance_test.go` and its fixtures are shared: add rows in new `*_test.go` files per capability, never in the existing file.

## 4. `pi.events` bridge for the native SDKs

**Why.** Node realms share one bus (D77, D83). Go, Rust and Python extensions have none (`docs/extension-sdk-surface.md` row `pi.events.*`, D83 boundary 5). Pigpen's Go port of the herdr integration must receive `herdr:blocked`, which Node extensions emit, and must emit its own events to Node listeners. The owner recommended the bridge.

**Classification (`AGENTS.md` product-extension boundary).** Inert capability: a generic, product-neutral host mechanism that extensions select. It has no Pi counterpart for native SDKs, so it is additive: record it in `docs/additive-features.md` (ID from the lead) and retire D83 boundary 5 in the same change. Confirm with the lead that the additive record is wanted before code.

**Existing pieces.** `coding/extension/host/subprocess/event_bus.go` already holds the Host's ordered listener registry keyed by connection and handler ID, with wire calls `events.on`, `events.off`, `events.emit`, `events.settle` and Host-to-realm `events.dispatch`, and shutdown cleanup through `conn.onClosed`. `coding/extension/eventbus.go` is the in-process Go bus (`Publish`/`Subscribe`).

**Design.**
- **Wire.** Reuse `events.on/off/emit` and `events.dispatch`; add nothing to the wire except a listener kind flag on `events.on` meaning "payload by value" (a native connection sends it; Node connections omit it). The Host stays the single ordered registry, so a native listener sits in registration order between Node listeners.
- **Payload.** By value as JSON. A native emit sends its JSON to Node listeners, who receive a fresh decoded value. A Node emit reaches a native listener as the emitter's `JSON.stringify` view, computed in the emitter's realm (one xref operation that reads each getter once); a payload that has no JSON form (BigInt, a cycle) fails only that listener's delivery, reported like Pi's `safeHandler` (`Event handler error (<channel>):` on stderr), and never fails the emitter or the other listeners.
- **Ordering and reentrancy.** The emitter waits for each listener's synchronous prefix in order, as today. A native handler is one unit of work, so its ack is sent when it returns. The SDK connection must keep servicing host calls while a handler runs, so a handler can call `Emit` (nested dispatch) and two emitters that cross do not deadlock; `TestXrefEventBusCrossingEmittersDoNotDeadlock` gives the Node baseline.
- **Lifecycle.** `On` returns an idempotent unsubscribe; a factory that fails after subscribing has its subscriptions dropped; runtime shutdown or connection loss removes only that runtime's listeners (`h.eventBus.closed(conn)`).
- **API shape.** The SDK mirrors the in-process `extension.EventBus` (`Publish`/`Subscribe`) so both Go surfaces read the same. Pi names them `emit`/`on`. Renaming the existing in-process API is a public break outside this lane: ask the lead which spelling wins before the red commit. Rust and Python follow the naming convention of their SDKs.
- **Async contract.** Add a row to `docs/extension-api-parity.md` (async contract for a native realm) and `test/parity/async-contracts.toml`.

**Red tests (first).**
1. Node emitter to Go listener: the Go extension echoes a transformed payload to a second channel that a Node extension observes; the value cannot be produced by a fallback.
2. Go emitter to a Node listener, and to a Go listener in another cell.
3. Registration order across three realms with interleaved subscribe and unsubscribe.
4. Reentrant emit from a native handler, and crossing emitters.
5. Handler panic or error does not reach the emitter or later listeners.
6. Unserializable Node payload reaches no native listener and is reported once.
7. Unsubscribe during dispatch (snapshot semantics, as `EventEmitter` clones its listener array).
8. Runtime crash mid-dispatch releases the emitter with the same outcome as a crashed Node realm.
9. The same conformance row in Rust and Python (a capability is unfinished until all SDKs have it).

**Stress.** 100 subscribers over four realms, 10,000 emits, one slow listener, `-race -count=24` under burners; assert bounded memory and that a hung native listener blocks its emitter and nothing else (Pi behavior, D83).

**Docs to change (integrator, one commit).** `docs/extension-api-parity.md` row `pi.events`, `docs/extension-sdk-surface.md` row `pi.events.*`, D83 boundary 5 and the D77 text, the additive-features record, the SDK READMEs.

## 5. Order, contracts and shared files

**Order.**
1. Start 6A, 6B, 6C, 6D and 6F-events together. 6D's first commit is the contract commit (stubs only, no behavior); it merges to the family branch before any other 6D work.
2. 6E and 6F-go/rs/py/ts branch from the contract commit. Their red tests do not wait for 6D's behavior.
3. Joint run after wave 1 (6A-6D green) and again after wave 2, per plan Phase 3.

**Shared-file map (who may edit).**

| file | editor | others |
|---|---|---|
| `coding/session.go` | 6E | read only |
| `coding/extension/host/subprocess/protocol.go` | 6D | 6F after the contract commit, additive |
| `coding/extension/events.go`, `api.go` | 6D | 6E, 6F additive after the contract commit |
| `coding/extension/host/subprocess/event_bus.go` | 6F-events | none |
| `internal/codingagent/settings.go` | 6A (all new fields in its red commit) | 6B, 6C, family 9 read |
| `internal/codingagent/model_registry.go` | 6B | none |
| `cmd/pig/extensions.go` | 6C | families 7, 8 rebase |
| `cmd/pig/args.go`, `startup_model.go` | 6B for model flags first, then family 7 | |
| `coding/extension/provider.go`, `model_info.go` | 6D declares, 6B consumes | |
| `extensions/sdk-py/pig_sdk/__init__.py` | 6F-py | none |
| `test/extension-conformance/*` | new files per capability | existing `conformance_test.go` untouched |
| `docs/parity/PORT_MAP.md`, `test/parity/interfaces/*`, `coverage.md`, known-gap and batch files | integrator | none |
| `docs/extension-api-parity.md`, `extension-sdk-surface.md`, `DIVERGENCES.md` | integrator | lanes record row text in their evidence file |

**Test files from the appendices that are not family 6.** They belong to other lanes, and a family 6 lane must not port them:
- Family 5 or 9: `clipboard-paste-file-paths`, `clipboard-image*`, `syntax-highlight`, `system-theme`, `theme-style`, `themed-text`, `theme-controller`, `theme-detection`, `theme-export`, `footer-width`, `settings-selector`, `interactive-tui`, `tool-execution-component`, `codemode-renderer`.
- Family 7: `rpc-prompt-response-semantics` (consumes 6E's `PromptDisposition`).
- Family 8: `mcp-*`, `tool-search`, `suite/agent-session-mcp*`, `suite/agent-session-codemode`.
- No lane owns these yet; the lead assigns one: `tools.test.ts` (84 → 85, the bash/powershell structured results), `edit-tool-legacy-input`, `experimental-cli-entry`. Plan section 10 has no lane for the tools work.

## 6. Questions for the lead

1. Spelling of the native event API: keep `Publish`/`Subscribe` (matches the existing Go in-process bus) or move to Pi's `emit`/`on` (breaks the in-process API)?
2. Is an additive record for the `pi.events` bridge approved as written in section 4, and which D-ID?
3. Which lane owns the bash/powershell structured result and `tools.test.ts` work (plan section 4.4 "Tools")?
4. Should 6A's session-file change wait for a decision on files that 0.87.1 already wrote with only a header (resume of a header-only file)? The plan assumes resume keeps working.

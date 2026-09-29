# Independent Node SDK Session evidence

Reference: Pi 0.87.1. Baseline: `fdcf667708`. This is a functional checkpoint, not release approval. The unchanged cold-start budget remains a blocker. The coordinated `bbe9095d8` carrier checkpoint is merged; overlay controls now use its acknowledged synchronous `runtime.callSync` contract instead of local prediction.

## Source corrections

| Symptom | Source correction | Pi contract and regression |
|---|---|---|
| `ModelRuntime.create` is missing and `createAgentSession` throws | Vendor the complete published coding-agent graph and delegate independent creation to Pi; retain each child until its connection drains or the caller disposes it | `packages/coding-agent/src/core/sdk.ts:175-439`; `TestNodeCreateAgentSessionMatchesPi`, `TestNodeSDKSessionDefaultsAndSkillsMatchPi`, `TestNodeIndependentSessionLifetimeIsConnectionScoped` |
| A child-only model becomes a parent-catalog request | Distinguish an already-resolved API leaf from a registry request; preserve its model, endpoint, credentials and options | `core/model-runtime.ts:573-647`; `TestSubprocessAPIStreamKeepsIndependentModel`, scenario 53 |
| Child provider hooks disappear during JSON serialization | Keep callback handles in Node and await reverse calls at the provider boundary | `core/sdk.ts:309-347`; scenario 53's exact hook trace; a compiling mutation disabling `onPayload` fails with all three payload entries missing |
| Cancellation while awaiting response headers becomes `error` | Classify cancelled API-leaf failures as `aborted` | `packages/ai/src/api/openai-completions.ts:710-716`; `TestIndependentAPIRequestAbortDuringResponse` covers completions and responses |
| Real pi-btw hangs in `ModelRuntime.create()` | Replace Go regular-file sidecars with the same directory-lock protocol Pi's stores use; share it with settings and trust | `core/auth-storage.ts:52-189`, `core/models-store.ts`; `TestGoStoresReleasePiDirectoryLocks`, `TestSharedAuthDirectoryLockWaitIsCancelled`, `TestConcurrentStoreWritesWithPiBackend` |
| pi-btw opens a second overlay on follow-up and blocks provider admission | Publish the mounted `onHandle` value; route focus and visibility controls to the UI owner | `modes/interactive/interactive-mode.ts:2918-2920`, `packages/tui/src/tui.ts:715-769`; `TestNodeCustomOverlayPublishesMountedHandle`, `TestRemoteOverlayHandleControlsOwnInputRoute`, scenario 54 |
| pi-btw uses its 30-row fallback and draws a shorter overlay | Publish measured host stdout rows/columns and resize events | pi-btw `extensions/btw.ts:1421-1423`; `TestNodeStdoutGeometryFollowsHostResize` |
| A bare extension host has no process theme | Initialize Pi's theme before factories; use the real capability cache | Pi `modes/interactive/theme/theme.ts:774`; existing `TestNodeTypeScriptSourceResolutionAndShims` is red then green |
| Go queries tmux options serially | Join the two queries concurrently, as Pi's Promise.all does | `modes/interactive/interactive-mode.ts:1214-1255`; `TestTmuxKeyboardQueriesStartConcurrently` |

The complete stand-in inventory is [node-shim-audit.md](node-shim-audit.md). Coding-agent exports now use actual Pi modules. Type-only fake classes and the stale Pi-AI session-cleanup values are removed. The designated ScrollView lane owns the remaining pi-tui stand-ins; this checkpoint supplies the `getCapabilities` dependency required by Pi's Theme initialization. D73's live-main-object boundary and D74's existing provider limits are not retired.

## Red/green evidence

Raw logs and captures are retained under `tmp/fix-node-createagentsession/` in this worktree.

- `red-unit.log`: the Pi control passes; PiG fails because `ModelRuntime.create` is not a function.
- `red-model.log`: a private model fails with `not in parent catalog` before API-leaf routing.
- `red-abort-unit.log`: both OpenAI API paths return `error` instead of `aborted` during response-callback cancellation.
- `red-store-lock.log`: both stores leave regular `.lock` files, and a contended Node-style directory lock produces `is a directory` instead of waiting with cancellation.
- `red-onhandle.log`: `onHandle was dropped`.
- `red-geometry.log`: stdout rows are undefined instead of 55.
- `mutation-payload.log`: the compiling mutant loses all three `payload` hook entries; Pi retains the complete trace. The mutation is restored.
- `focused-final.log`, `vendor-final-tests.log`, `bundle-tests.log`, `options-green.log`, and `theme-green-2.log`: focused green results.
- Scenario 53 passes three pairs with independent tools, progress, hooks, persistence, cancellation and unchanged parent state. Scenario 54 passes three escaped-output pairs after its crop is corrected to include the full owning line rather than only the start marker.
- Scenario 25 retains escaped-output equality. Its readiness condition now waits for both hosts' intentionally asynchronous tmux warning before sampling the tool-only crop; no timeout or comparator is weakened.

## Locked published package

Replay command:

```sh
python3 test/parity/unit-evidence/replay-node-btw.py \
  --npm <corpus>/homes/pi-btw/pi/.pi/agent/npm \
  --pi extensions/sdk-ts/node_modules/.bin/pi \
  --pig bin/pig \
  --output tmp/fix-node-createagentsession/corpus-final
```

The copied lock is unchanged in every run:

- Package: `pi-btw@0.6.1`.
- Lock SHA-256: `43faa182eff083cffa2650270f60906ef81872ae1a405b85fa0caace376eb566`.
- npm integrity: `sha512-DFRoz+DyA6Wbyj3lQ5bAyPe80l6EGU8/zc9KcY7v1j6oXJd7D9hcWyFWImUoXKQKzGfxuX3dBW4Ef8uEj5NVlw==`.

Three Pi-first/PiG-second pairs produce `SIDE ANSWER`, cancel the follow-up, close the overlay, and produce `MAIN ANSWER`. The provider recordings prove that the main request does not contain the side question. No published package file is changed. A separate last-loaded fixture supplies the session-start readiness marker. Raw escaped panes and requests remain visible; the underlying D2 identity text and the existing 33k/32k footer label difference are not normalized away. After the stdout fix, the overlay's height and content agree.

Earlier probes are retained as failures: the initial dismissal sequence was not a valid Pi interaction; the next run exposed the store lock; the next exposed missing `onHandle`. Those are not counted as successful replays.

## Build closure and resource evidence

`automation/gen/vendor-pi-dist.sh` copies the exact locked graph, assets and dependencies. Its compiler walk handles multiline re-exports. `bundle-pi-sdk.mjs` uses the Pi installation's locked esbuild to reduce module-file startup overhead while preserving original asset URLs and shared external namespaces. `TestVendoredPiDistMatchesThePinnedPackage` checks raw source seams; `TestNodeSDKBundleRegeneratesExactly` checks compiler outputs. `vendor-manifest.json` records package versions/integrities, source hashes, output hashes and rewrites. Regeneration has produced an identical file set and bytes.

Profiles are under `tmp/fix-node-createagentsession/profiles/`:

- Independent Go API request: 461,472 ns/op, 114,534 B/op, 316 allocations on this Linux amd64 Xeon 6746E run. This measures the local HTTP/provider path, not Node IPC throughput.
- 100 independent Node Session constructions/disposals: p50 0.118 ms, p95 0.489 ms; RSS 122,945,536 bytes before and after collection; zero retained model streams/callbacks. CPU and heap profiles are retained. This measures warm composition, not cold startup.
- Embedded runtime materialization: approximately 95–102 ms before bounded, joined file writes and 37–43 ms afterward. The final files remain identical; directories are established before disjoint writes and the launcher is published only after joining workers.

The full SDK still raises cold startup cost. The unchanged EventBus limit is 2.0× Pi. Recorded results include 1.564 s versus 0.737 s (2.12×), despite passing all four behavioral pairs. Other probes fall just below the ceiling. This is not durable performance closure, and no lucky rerun or limit relaxation is accepted.

## Carrier union

The `bbe9095d8` merge retains the worker-owned Connection and all Provider carriers, the SDK Session `apiRequest` and stream-callback fields, child disposal, process-theme initialization, and measured terminal geometry. The only Runtime conflict is its constructor: retain both `nativeProviderObjects` and theme initialization. Merge release/API notes by union and regenerate coverage and inventories.

`corpus-union/` records another three Pi/PiG pairs with exact locked pi-btw inputs, matching complete panel content/geometry, side cancellation and independent main turns. `parity-union.log` records three passing pairs each for scenarios 53 and 54 using acknowledged overlay controls. D78's separate builtin/composed-object and final-qualification obligations remain with its owner.

## Post-union integration guards

The joined full Go run exposed the new Rust `src/provider/proxy.rs` missing from the SDK source bundle, the IO-worker socket missing its `destroy` lifecycle operation, stale native-getter/cancel-stream exceptions, and the nested Go SDK checksum. The fixes make Rust bundling and its denominator recursive, close the worker-owned socket through its control channel, remove implemented capability gaps, and regenerate the exact SDK hash. Existing fresh-home/scaffold tests are red then green; `TestNodeRuntimeSDKSurfaceAdditions`, capability gates and the module-hash guard pass. The socket guard also passes under the race detector. Full lint and both vet commands pass after these corrections. The full joined run is not represented as green: its initial failures and the targeted rechecks are retained.

The final joined `extensions-runtime` family completes every behavioral comparison successfully, including both new scenarios and the carrier scenario. Its only failed assertion is the unchanged EventBus runtime ratio: 1.760 s / 0.863 s = 2.04, above 2.0 (`family-union.log`). Performance closure remains blocked.

## Gate status at this checkpoint

- Go vet and Windows vet for touched packages pass.
- Touched-package golangci-lint passes without new suppressions.
- Focused race tests for API cancellation, shared locks and overlay state pass.
- The final qualified full Go run passes all touched production packages and extension conformance. Only the three inherited approval/accounting assertions in `test/parity/closure` fail: `TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit`. Raw output is `go-test-last.log`. Full and changed-file lint both report zero issues.
- The family run exposes the cold ratio, an asynchronous tmux warning inside scenario 25's crop, and scenario 54's too-short crop. The latter two are corrected and re-probed successfully; the cold ratio remains blocked.
- `ci-contracts` now fails only its inherited open hot-path test obligations. `ci-drift` fails only the unapproved D77/D78 records. Generated interfaces, recommendations, SDK surface, coverage, scenario lint, source hygiene, and divergence-guard checks pass. Neither record is marked approved to satisfy a gate.
- `8008a8bd7` preserves published CRLF and intentional vendor whitespace through Git. Every manifest-owned file in the committed tree matches its recorded hash.
- Native Go SDK test closure is not inferred from Node-only evidence. The SDK session-manager and skills test mappings remain partial, with the new Node cases named explicitly.

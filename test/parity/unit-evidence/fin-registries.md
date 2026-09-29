# fin-registries evidence

## Native Provider host implementation (current)

The lead directs D78 to implementation, not approval. The Node-to-host native Provider path is implemented. The older checkpoint sections below retain the original before evidence; their local-only host limitation is superseded by this section. Remaining SDK carrier work is listed explicitly below.

### Root fixes and source contracts

- Native registration now installs metadata and connection-owned callbacks in the host ModelRegistry, not just a Node-local map. Both registration handshakes and post-bind `registerProvider(Provider)` use this path. Pi reference: `core/model-runtime.ts:744-797`.
- The CLI has a separate model constructor from the embedding API. Initially it still built an OpenAI HTTP provider for the native model and attempted port 9. `coding.BuildNativeModel` now binds both constructors to the native callback path. A real `--model parity-native/native-model -p hello` turn returns `native answer` without HTTP.
- `provider_call` carries reverse auth, refresh and filter operations. `provider_stream` carries ordered events through the existing request/update machinery. Callback errors terminate the stream. Cancellation sends the native AbortSignal and then forwards the provider's own terminal event, including `native cancelled`; it does not replace it with a setup error. Pi reference: `packages/ai/src/api/lazy.ts:31-63` and `core/model-runtime.ts:611-653`. The existing setup-cancellation tests remain unchanged and pass: setup rejection stays an error, as Pi's lazy setup does.
- Native refresh restores the offline phase first, resolves the refresh credential, then performs the optional network phase with `force`. Publications persist or delete store data before the update callback runs. Accepted model snapshots survive a later callback failure. Generation checks reject superseded publication. Availability uses the native check and `filterModels`. Pi reference: `packages/ai/src/models.ts:344-530`.
- Native OAuth login, refresh and request auth reach the owning process. The shared credential structs previously discarded arbitrary provider fields such as `account`. The JSON boundary now retains and clones them across storage and rotation. OAuth equality tests use structural equality because the open credential record contains a map. Pi reference: `packages/ai/src/auth/types.ts:21-34,51-97,185-241` and `auth/resolve.ts`.
- Native auth results preserve omitted versus explicitly empty `source`. `TestNativeAuthResultOmitsAbsentSource` is mutation-red-proven against the former fabricated empty source field, and scenario 38 now exercises an auth callback that omits source.
- Native stream `onPayload` callbacks return replacements through the active reverse request. Stream/streamSimple remain distinct in Node, the host, and all native SDK consumers. Node's own Provider object identity remains unchanged.

### Automated red/green proof

`38-native-provider-callbacks` was red before the implementation: `found`, `catalog` and `refreshed` were false, no native answer or text delta appeared, and the artifacts differed. It now compares exact deterministic values for owner identity, catalog/availability, refresh, forced publication, resolved auth, both stream methods, ordered events, a thrown stream error, and cooperative cancellation against Pi. `38b-native-provider-main-turn` checks the actual CLI/agent path with exact output. Both scenarios pass three pairs, as do cross-family scenarios 36/37.

A compiling `-overlay` mutation replaces the resolved native API key with `mutation-key`. Both native scenarios fail because the native callback rejects the missing credential; this proves the primary paths actually invoke the extension. `/tmp/fin-native-mutation-final.log` retains the failures. A second compiling overlay suppresses accepted model publication; `TestNativeProviderPublicationSurvivesLaterRefreshError` fails with `accepted publication was rolled back on callback error` (`/tmp/fin-native-publication-red.log`). No production source is left mutated. `TestCredentialStoreRetainsNativeProviderFields` is mutation-red-proven by dropping the extra fields during stored-credential normalization: it reports the lost account object before the source fix.

Unit and integration guards:

- `TestNativeProviderHostLifecycle`: isolated and packed Node; catalog lookup, checked auth, streamed result, thrown stream/auth/refresh errors, in-flight auth and refresh cancellation, exact native stream cancellation message, onPayload round trip, persist-before-update acknowledgement, shutdown removal, and stale-handle failure.
- `TestNativeProviderOAuthRefreshAndLogin`: expired credential rotation, provider-owned fields, resolved headers, host login registry and reverse login prompt.
- `TestNativeProviderPublicationSurvivesLaterRefreshError`: accepted publication remains after callback failure; force and pre-cancelled refresh behavior.
- `TestCredentialStoreRetainsNativeProviderFields`: read/modify/write retains provider-defined OAuth data.
- Native lifecycle and OAuth tests pass under `go test -race`.

### Performance and lifetime

`BenchmarkNativeProviderStream` uses a real Node extension and the production ModelRuntime. It includes request auth, partial/terminal stream transport and event consumption. The final recorded sample is 581,762 ns/op, 34,455 B/op and 356 allocs/op. This is a baseline, not a speedup claim. CPU/allocation profiles are `/tmp/fin-native-{cpu,mem}-final.pprof`. Each request owns its callback map entries and removes them before returning. Teardown removes the connection's native registrations. Stream lifetime ends at the provider terminal response or connection failure; shutdown still cancels the runtime's owned tasks. A native callback that ignores cancellation can stall its own stream, as Pi permits; no retry or arbitrary timeout masks that behavior.

### Verification of the native host change

`go vet ./...`, Windows vet for all touched production packages, full `make lint` and `make lint-changed LINT_BASE=0c5e9fce8e` pass. Go/Rust/Python SDK tests and model-streaming/registry/session conformance pass. The new native lifecycle/OAuth tests pass three repetitions and under the race detector. Scenarios 36/37 and both native 38 scenarios pass their three declared pairs. Generated Go interfaces, recommendations and coverage are regenerated.

A full `go test ./...` run with the maintained fixture exports passes the production packages, including ai (15.6 s), cmd/pig (250.5 s), coding (9.9 s), subprocess (228.6 s) and codingagent (17.6 s). Its remaining failures are the known pending-D78 closure accounting and predecessor SDK/Markdown surface inventories, not a native callback execution failure. `make ci-contracts ci-drift` passes the contract/inventory/scenario/coverage checks and stops at unapproved D78, as intended. Direct divergence-guard still reports the inherited `file_lock.go` proper-lockfile duration marker. No check is weakened and no new suppression is added.

The completed runtime-surface (`952a5af40a`) and Pi-vim (`89f8ef8b2a`) predecessors are merged after the native implementation. The entire `extensions-runtime` parity family passes on that merged tree (164.2 s); the earlier Markdown/editor failures below are closed by their owning lanes. Post-merge native/registry/model-streaming conformance, vet, Windows vet, changed-file lint and divergence-guard pass. The post-merge `go test ./...` run passes all production packages, including cmd/pig (244.4 s), subprocess (271.3 s), coding (10.3 s), ai (15.6 s) and codingagent (18.6 s). Only pending-D77/D78 closure accounting and the source-scanner SDK capability inventory remain red; `test/upstream-parity` is now green. D77 remains pending as required by its owner, and D78 remains the SDK carrier implementation gap. `ci-drift` stops on those scrutiny statuses rather than on native Provider execution.

### Remaining SDK carrier scope and estimate

D78 now covers SDK Provider object carriers only. Go, Rust and Python can find, authenticate and stream a Node-registered native model, but cannot author or retrieve a native Provider callback object. A Node extension can retain its own native object but cannot retrieve a different extension's object; that missing carrier now throws explicitly instead of returning a fabricated object or falsely reporting no native registration. The host executes the owner's callbacks for all normal registry/model operations.

Estimate for the remaining carrier work: **24 engineer-hours**, excluding review and shared-machine gate queues: Go authoring/retrieval 4 h; Rust 5 h; Python 3 h; foreign-Node object callbacks 4 h; cross-language/isolated/packed/fused conformance and negative/lifetime tests 8 h. The wire and host implementation in this change are reusable. This is not a request to approve a divergence, and no SDK stub is presented as complete.

The independent `createAgentSession` composition gap in pi-btw is unchanged and separate from native Provider registration.

## Scope and oracle

Pi is the reference implementation. All source citations below refer to `.upstream/v0.87.1/packages/coding-agent/src/`. The comparator is `extensions/sdk-ts/node_modules/.bin/pi` (0.87.1). Tests use Go 1.27.1 and Node 24.19.0 on Linux amd64.

This is partial closure, not a claim that every Provider callback or writable SessionManager member is implemented. The native SDK facades cover the fifteen ReadonlySessionManager reads and the data/auth/refresh/configuration portion of ModelRegistry. Native SDK `getProvider`, `getRegisteredNativeProvider`, the native Provider registration overload, remote extension-supplied Provider callbacks (including OAuth/stream functions), and writable main-session manager methods remain open. The imported Node SessionManager and ModelRegistry classes are Pi's implementations, not empty classes. D78 is recorded as a proposed, unapproved boundary for the handoff's native Provider limitation. The strict divergence gate must reject it until the owner decides or the callback bridge is implemented.

## Source contracts

- `core/model-registry.ts:34-171`: facade delegation, independent array containers, configured auth, provider lookup, auth error conversion, refresh, registration queries and registration overloads.
- `core/model-runtime.ts:562-571,744-797`: auth-source priority and provider registration merge/order. The host's available set still uses configured-auth filtering. Checked-auth snapshot/filterModels closure is not proven by these tests.
- `core/provider-composer.ts:340-444,446-565`: callable composed API-key/OAuth methods and synchronous structural validation. Node now runs this exact code instead of returning `auth: {}`.
- `core/session-manager.ts:245-263,439-581,1467-1587,1801-1807`: readonly member denominator, context edits/compaction, branch/tree/label reads, reset-to-root semantics, and independently constructed in-memory sessions.
- `utils/paths.ts:75-97`: `normalizePath` preserves `./sessions` rather than resolving it to an absolute path.
- `core/extensions/loader.ts:153-221`: an imported `createExtensionRuntime` owns pre-bind queues, subscription disposal and invalidation. This pure function is now vendored unchanged.
- `core/sdk.ts:175-439`: independent AgentSession composition. This is not implemented by an empty SessionManager or a registry proxy.

## Red and green evidence

| Regression | Red result | Fix and green proof |
|---|---|---|
| `TestExtensionSessionTimestampUsesMilliseconds` | `123.456789` instead of `123`; `-0.000001` instead of `-1`; year 2500 overflowed UnixNano | Use `UnixMilli`; Node Date probe agrees on all three inputs |
| `TestImportedRegistryAndSessionClassesMatchPi` | `SessionManager.inMemory is not a function` after successful compilation | Export exact Pi classes; local append, label, context edit, tree and registry delegation match Pi |
| `TestNodeComposedProviderAuthMatchesPi` | Missing `auth.apiKey` on the composed provider | Use exact Pi composer; auth method inventory, check, resolve, configured headers and model definitions match Pi |
| `TestImportedExtensionRuntimeMatchesPi` | Throwing `createExtensionRuntime` stand-in | Export the exact pure Pi function; pending queues, unsubscription, invalidation and unbound errors match Pi |
| `TestRegistrySessionFacadesAcrossSDKs/subprocess-node-packed` | Replacing an empty in-memory session with a persisted session returned an empty log | Reset the per-session mirror/subscription; all seven realizations pass. Session replacement integration is coordinated with fin-sdk-surface, which owns scenarios 39-42 |
| `TestNodeSessionClearedLeafMatchesPi` | Empty host leaf selected the last stored entry instead of returning null/empty branch | Serialize an empty leaf and treat it as a root reset; differential test against Pi `resetLeaf` passes |
| `TestExtensionSessionReadRejectsCorruptRetainedEntry` | Invalid retained JSON was silently discarded | Surface the decoding error on the read operation instead of returning fabricated incomplete history; no suppression is needed |
| Fresh-home SDK/scaffold tests | Rust could not find `session_manager`; Python could not import `pig_sdk.session_manager` | Derive the embedded source inventory from `src/*.rs` and `pig_sdk/*.py`; `TestFreshHomeLoadsScaffoldedExtensionsWithoutCheckout` and `TestExtensionInitScaffoldsBuildAndRegister` pass |

`TestRegistrySessionFacadesAcrossSDKs` exercises in-process Go, subprocess Go, subprocess Node, packed Node, Rust, Python, and fused Go. It uses a named label, nonempty model catalog, configured credential source, explicit registry error, aborted refresh with an error, and a missing-auth error. Its reference uses the production host projection. The independent Pi scenario checks that projection's branch/compaction/context-edit contract. `TestRegistryRegistrationMergeAndOrder` checks defined empty/null replacement and reinsertion order. `TestExtensionProviderAuthStatusSources` checks runtime, extension literal, models.json literal, command, resolved template and unresolved template sources.

### Paired scenarios

- `36-model-registry-session-manager`: three Pi/PiG pairs pass. It compares artifacts containing models, auth, refresh, late registration, session labels/tree/branches and compaction/context-edit projections.
- `37-imported-registry-relative-session-dir`: three pairs pass. It checks imported class behavior, registry auth error conversion, and exact `getSessionDir() === "./sessions"` in a writable temporary cwd.
- Both scenarios retain `artifact_normalized_equal`; no ANSI, whitespace or behavior difference is normalized away by a new rule.
- A compiling mutation that suppresses the production late-provider callback fails scenario 36: `parity-late/late-a` disappears and artifacts differ.
- A compiling mutation that restores the empty imported SessionManager class fails scenario 37: `SessionManager.inMemory is not a function`, no artifact, and artifact mismatch.
- Mutation binaries deliberately run with `-pig-parity.allow-stale` after restoring the worktree. Acceptance runs build the current source without that override.

## Corpus before/after

The before column is the retained corpus handoff observation on `da3b0ff`, not a new baseline run. The after column is a fresh-home replay on this machine against Pi 0.87.1. Both binaries install the exact existing package directories from the earlier npm sweep, preserving the versions below. Each run performs install, print `hello`, and a 170x55 tmux startup/command/turn path against a local OpenAI-compatible fixture. It does not use real provider credentials.

All eight packages install and finish the print prompt with exit 0 under both binaries. Interactive results are narrower than a complete-package compatibility claim.

| Package | Version | Before | After versus Pi |
|---|---|---|---|
| `pi-btw` | 0.6.1 | `/btw` fails at `getRegisteredNativeProvider` | Registry call is present. A separate command-selection problem chooses the package's skill when typing `/btw`; reported to fin-loader. Direct `/btw:ask hello` progresses through the imported SessionManager and ExtensionRuntime, then fails at the separate `createAgentSession` stand-in. Not closed |
| `@narumitw/pi-btw` | 0.61.1 | `/btw` fails at `getAll` | Both show the Pi BTW menu and `Same as main thread (e2e-model [e2e])` |
| `pi-advisor-flow` | 0.8.2 | `/advisor` says `No matching models found` | Both show `Select Executor Model` and `e2e/e2e-model` |
| `billion-context-pi` | 0.1.81 | ACP disabled because `buildContextEntries` is missing | Neither startup shows the unsupported-host warning |
| `@gotgenes/pi-permission-system` | 34.0.1 | Loader failure; `getSessionDir` also missing | After predecessor loader fixes and this session facade, both show the permission-system settings menu |
| `pi-web-access` | 0.31.0 | Control; registry methods were static risks | Both show the curator-started/manual-browser-open message; dynamic ports/session IDs differ |
| `@narumitw/pi-usage` | 0.61.1 | Control | Both report that usage is unsupported for the scripted e2e provider |
| `pi-hermes-memory` | 0.9.9 | Control | Both install, run print and reach the memory-insights command/turn path without a registry/session error |

Raw replay artifacts and exact package-source paths are in `/tmp/fin-registries-corpus-mr_g2m2_/manifest.json` and its per-package directories. The first attempted replay used mise shims under a temporary HOME; Node could not start. That run is invalid environment evidence, not a package failure. The replay uses the installed Node binary directory explicitly. PiG still logs stdin EIO when tmux closes some sessions; this is the pre-existing corpus shutdown diagnostic, not fixed here.

### pi-btw remaining composition blocker

The package calls `createExtensionRuntime` at `extensions/btw.ts:303`, then `createAgentSession` at `:1996` or `:2675`. A model override or runtime key additionally calls `ModelRuntime.create` at `:335`. The imported SessionManager and ExtensionRuntime now work. The direct side-question probe reports `Extension error (command:btw:ask): createAgentSession is not yet supported in the pig TS subprocess shim`; Pi completes the command without that error.

Vendoring `core/sdk.js` is not just a SessionManager change. Its relative import graph reaches AgentSession, resource discovery, tool renderers, settings/auth stores and extension loading. A conservative traversal including static dynamic imports reaches 205 module paths and external dependencies including `undici`, `cross-spawn`, `proper-lockfile`, `chalk`, `jiti`, and the Pi package barrels. Some paths are lazy. The traversal is a feasibility inventory, not proof that all are eagerly required. Loading the entire graph without its dependency closure would only replace one stand-in with module-load failures. This lane does not claim the independent AgentSession product path is implemented.

## Native Provider probe (D78 proposal)

`test/parity/scenarios/testdata/extension-native-provider-probe.mjs` registers a minimal native Provider with an API-key auth method and one model. A print command checks owner identity, `find` and `getAll`. Run with `-ne -e test/parity/testdata/test-faux-provider.ts -e test/parity/scenarios/testdata/extension-native-provider-probe.mjs --model test-faux/faux-1 --no-session -p /native-probe` under Pi and with `PIG_TEST_FAUX=1 -ne -e test/parity/scenarios/testdata/extension-native-provider-probe.mjs` under PiG, in separate temporary agent directories. Pi returns `{"ownerObject":true,"found":true,"catalog":true}`; PiG returns `{"ownerObject":true,"found":false,"catalog":false}`. Raw outputs are `/tmp/fin-native-pi.log` and `/tmp/fin-native-pig.log`. This is direct evidence of the open callback-registration gap, not accepted parity or a reason to weaken a scenario.

## Performance and lifetime

`BenchmarkExtensionSessionProjection` uses a 1,000-entry session and measures the actual host projection. On this host it recorded 8.82 ms/op, 2,331,154 B/op and 51,385 allocs/op. CPU and allocation profiles are retained as `/tmp/fin-session-{cpu,mem}.pprof`, with text summaries in `/tmp/fin-session-{cpu,alloc}.txt`. This is a baseline, not a speedup claim. JSON decoding dominates allocation. Native facade reads allocate their returned projection per call; no new background workers or retained host caches are introduced. Node keeps one session mirror plus its index and pending entries, invalidated on replacement. The host runs these calls away from the TUI loop.

## Final verification update after predecessor merges

The completed loader and the current `fin-pivim` branch are merged. The Pi-vim `TestHost_Integration_TSFileShim` regression now passes. The completed loader removes four of the earlier family failures; the later full family run fails only `33-markdown-transformer` and `30-custom-editor-component`. The owning lanes have the exact logs.

At `e0fb8162fb`, a clean serial `go vet ./...`, Windows vet for touched packages, and `make ci-contracts` pass. `make lint-changed LINT_BASE=0c5e9fce8e` reports zero issues. Focused Node differential tests and registry/session conformance pass after both merges.

The complete `go test ./...` run uses `eval "$(./automation/ci/test-fixtures.sh --exports)"` and the unchanged ten-minute package budget. The production packages pass, including `cmd/pig` (254 s), `coding/extension/host/subprocess` (231 s), `internal/codingagent`, `tui`, and `coding`. The overall run remains red: closure tests correctly notice that D78 is pending rather than approved; the wire-capability source scanner misses the new facade files/generic helper and Node replicated-state implementations (reported to fin-sdk-surface); and the inherited runtime-surface runner inventory lacks its two Markdown accessor dispositions. The D78 count and bundle listing are updated without pretending it is approved. No accepted tests are weakened to hide these failures.

The task is not release-complete. D78 needs an owner decision or implementation closure, `createAgentSession` remains a precise pi-btw blocker, and the two remaining behavioral family failures require their owning slices. The branch is an integration checkpoint, not a green release claim.

## Gates at the integration checkpoint

- Passed: `go vet ./...`; Windows vet for touched host/mode/conformance and SDK bundle packages; `go tool golangci-lint config verify`; full `make lint` (0 issues); native SDK Go tests and Rust tests; new differential/unit/conformance tests; fresh-home/scaffold tests; `go test ./cmd/pig ./coding ./internal/codingagent`; `go test ./test/parity/...`; scenarios 36/37 (three pairs each).
- `make lint-changed LINT_BASE=0c5e9fce8e` passes with zero issues. Its default base has no merge base with this worktree's `main`; the explicit base is the handoff parent. Full `make lint` also passes.
- Full `go test ./coding/extension/...` exposes an inherited Pi-vim test calling shim-only `editor.lockBorderColor`; this is sent to fin-pivim. A second inherited loader expectation still listed pi-agent-core as unshimmed; the test is tightened to require every current Pi virtual module to have a shim.
- The initial broad test run overlapped SDK development/vendor regeneration and saw transient compile failures; it is not counted as acceptance. The later broad run uses the repository's documented `CARGO_TARGET_DIR`, and completes without a timeout.
- `make ci-contracts ci-drift` exposed generated Go-interface and recommendation drift; both inventories are regenerated. A clean serial run passes correspondence and the interface gates. Coverage is regenerated with `RESULTS=` so inherited transient run results are not presented as current evidence. `ci-drift` then reports the predecessor's unaccounted proper-lockfile stale-duration literal (`internal/codingagent/file_lock.go:40`); sent to fin-pivim. The registry slice's skipped-decode finding is fixed at the source.
- Full `make parity-family FAMILY=extensions-runtime` completes but fails six cross-family/predecessor scenarios: 21 tool/command info, 34 empty footer, 33 Markdown transformer, 27 argument-completion footer, and 24/25 tool-renderer final text padding. Failures are retained in `test/parity/artifacts` and `/tmp/fin-family.log` and reported to the owning lanes. Comparators are not weakened. This is not a green family gate.
- Native Python tests pass through the already installed cached pytest executable (`PYTHONPATH=extensions/sdk-py <cached-pytest> extensions/sdk-py/tests -q`). The system Python has no pytest; unittest discovery finds no pytest-style functions and is not acceptance evidence.
- No new lint suppressions. D78 is proposed and unapproved; it deliberately blocks the strict divergence gate. No PORT_MAP entry is newly promoted to complete.

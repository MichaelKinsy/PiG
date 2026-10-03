# Session runtime helper evidence

This is the port of the five `pt-sessions-runtime-b` test files. `agent-session-runtime.test.ts`, `agent-session-branching.test.ts` and `2860-replaced-session-context.test.ts` are `ported`. D30 and D61 remain for library callers of the Session-level replacement methods; the CLI modes no longer use them.

## Reference and case inventory

The reference is published Pi 0.87.1. `test/parity/testdata/session-runtime-pi.mjs` transpiles the original test bodies, replaces their production imports with published Pi modules, and provides the assertion and spy operations those bodies use. It does not copy production behavior. A run without a site argument executes every original case in the selected file.

All test paths below are under `packages/coding-agent/test/`.

| File | Original sites | Current evidence |
| --- | --- | --- |
| `suite/agent-session-runtime.test.ts` | 125, 166, 216, 249, 294, 329, 378, 391, 431, 543, 548, 620 | All 12 native cases pass through real Runtime/Session construction. Session25 passes three exact-output pairs. Print, JSON, RPC and interactive mode replace through the factory-owned Runtime (Production modes below). |
| `suite/regressions/2860-replaced-session-context.test.ts` | 147, 211, 243 | All three native cases pass through real new/fork/switch replacement. Session27 pairs the original assertions. `TestReplacedSession2860AcrossSDKs` runs the same bodies through the pig binary for Node, Go, Python and Rust extensions. |
| `suite/regressions/5943-session-start-notify.test.ts` | 256, 276, 313, 368, 418, 451, 475 | All seven native cases pass. Session24 pairs resource ordering, Session28 pairs reload, and Session29 pairs replacement notification/message ordering. Production factory wiring is closed. |
| `suite/regressions/startup-session-rebind-duplicate-subscription.test.ts` | 23 | The native overlapping-bind case passes through real Runtime replacement and the production startup/rebind owner. Session29 pairs the original assertions; The CLI now builds its startup Session through the same factory. |
| `agent-session-branching.test.ts` | 90, 110, 131 | All three native prompt/selection cases pass through actual Runtime replacement. Session25 compares the implementation-based roles and file presence. The CLI modes replace through the factory-owned Runtime. |

Published Pi passes all 23 original bodies in the first four files. The first runtime adapter draft stopped at site431 because its assertion bridge lacked `toBeUndefined`. That was an adapter error, not a Pi failure. The corrected adapter passes all 12 runtime bodies.

## Factory-owned Runtime

`coding/runtime_replacement.go` adds the factory-owned Go embedding path. It retains one factory and publishes each constructed Session with its owning Services. It calls that factory for new, resume, fork, and import. It does not move a leaf inside a reused Session.

Pi `src/core/agent-session-runtime.ts:164-193` settles the outgoing response, emits shutdown, invalidates the old Session, constructs the replacement, and awaits rebinding. The Go path preserves that sequence. The tool-settlement case asserts persisted roles `[system,user,assistant,toolResult,assistant]`. A supplemental shutdown observer verifies those entries already exist when shutdown starts, rather than only after the caller subsequently joins the prompt.

The original lifecycle cases retain startup/new/resume/fork reasons, previous and destination paths, cancellation before file validation, invalid entry diagnostics, the unflushed-fork diagnostic, import collision preservation, and destination CWD/model/thinking state. The factory builds real Services and Sessions. Its provider boundary is scripted; model restoration, prompt execution, tool execution, persistence, and disposal are production paths.

`Session.BindExtensions` applies the mode bindings and dispatches the configured `session_start` event. The caller consumes Session events before binding. `Runtime.SetRebindSession` is awaited. Supplemental tests verify that a blocked rebind delays completion, a rebind error propagates without rolling back the installed Session, and factory failure occurs after disposal without installing a fabricated success value.

No detached production task is added. Replacement calls block on their owned operation. The factory and event consumer are explicit host responsibilities. CLI and extension session-switch handlers still use the old host-scoped path; they are not silently redirected to an incomplete factory.

## Native replacement callbacks and outgoing capture

The three #2860 cases first fail because no `withSession` callback is invoked: new-session events omit `with:1`, and fork/switch histories omit the callback's user and assistant messages. Native Runtime replacement now awaits `Setup`, refreshes initialized Session state, awaits host rebinding, and invokes `WithSession` with a newly created command context. The replacement context's message methods call the direct awaited Session operations. Its user-message method defaults expansion to false and retains source `extension`. Pi `agent-session-runtime.ts:187-193,253-259,330-350` and `agent-session.ts:4000-4007` define these operations.

The Go `CommandContext` now passes its request context to context-aware actions. `Runner.BindCommandActions` replaces both invocation forms together so an older contextual callback cannot override a newly bound plain callback. Native context values and cancellation are covered independently. Callback completion, failure, cancellation, setup ordering, and direct message completion have deterministic guards; the message-await guard uses `testing/synctest` rather than a timing delay.

Session27 executes the three original Pi bodies and their native Go equivalents. Four compiling mutation groups fail both unit tests and the pair: omit the callback, invoke it before rebinding, retain the old runner, and detach the replacement's message turn. A fifth compiling unit mutation drops request-context forwarding. This is native evidence only; no cross-SDK closure is claimed.

`settleActiveRun` and `waitForIdle` capture the outgoing Session before waiting for the UI turn to finish. `TestIdleWaitRetainsOutgoingSessionAcrossUIRebind` uses real Sessions and select-boundary observations to replace the mode's Session handle during that wait. A compiling old-method overlay returns nil against the new idle Session and fails the guard. The captured-owner path passes five race repetitions. The shared waiter logic is one function, not two independent implementations.

A separate inspection of parent commit3321 identifies a merge regression: `settleActiveRun` returns early when only the Session owns a custom run and waits only for the mode's turn channel. The earlier helper base already waits for both owners. A compiling overlay containing only the regressed method reproduces `TestInteractiveReplacementWaitsForSessionOwnedCustomRun`; focused restoration and outgoing-capture patches, including the deterministic test, were supplied to the parent without merging a moving tip. Shared Abort/Escape changes remain parent-owned.

## Factory resource retirement

`CreateAgentSessionRuntimeResult.Dispose func(reason string)` requests retirement of resources owned by that particular factory result. The Runtime captures the outgoing result before awaiting its shutdown, invalidates the runner, closes the Session, requests retirement, and only then constructs the replacement. A private per-result once guard prevents double retirement after a construction failure followed by final Runtime close. It never disposes a shared Model Runtime.

The disposer is not a physical-shutdown barrier. A leased outgoing extension must survive through `withSession` and the originating call result/command response. Waiting for those inside the disposer would deadlock because the callback has not run yet. Generic Host lease, scoped callback routing and final HostOwner cancellation/drain belong to the Extension Host implementation. Modes must retain a mode-lifetime Host owner and use its final shutdown barrier for current and deferred predecessor Hosts. The native disposer tests explicitly prove request ordering/idempotence only, not that final physical-drain contract.

## Reload UI ordering and completion

The original #5943 reload cases run through a real Session, the interactive owner loop, and the production Reload action. Site475 first fails because the editor retains focus while a `session_start` handler is independently suspended. The reload action now installs a focus-owning box and restores the editor only after the awaited action completes. Pi `interactive-mode.ts:6194-6206,6210-6248` defines that ownership and the pre-start chat rebuild. The native handler inspects the rebuilt chat before notifying, so queued notification delivery cannot mask a late rebuild.

The existing display-setting order already matches Pi. Site451 additionally inspects a rebuilt assistant block with hidden thinking to distinguish applying `hideThinkingBlock` before rendering from merely setting the flag afterward. Three compiling mutations fail both native assertions and Session28: leave the editor focused, rebuild after `session_start`, and apply display settings after rendering. Session28 compares case records only after all original assertions pass; it does not claim a terminal-frame byte comparison.

`awaitReloadStep` keeps extension shutdown/start/discovery, SDK staging, and Host reload off the UI owner. The owner continues servicing posted mutations and Session events, while the modal input route discards keys for the actionless reload box. Every step joins before returning. Cancellation preserves its cause, rejects cancelled admission, and waits for the worker to finish before releasing input ownership. Separate compiling mutations prove UI-task servicing and the cancellation join. Resource-file discovery, settings, theme loading, and transcript rebuilding remain synchronous; this work does not claim arbitrary-tree or large-history input latency bounds.

The cross-family reload probes exposed an initial implementation error: cancelling a helper-created context after successful reload also cancelled the newly loaded extension processes. `TestReloadStepDoesNotCancelSuccessfulOperationContext` first fails on that implementation. The step now retains the caller context without creating and cancelling a shorter lifetime. Extension-runtime scenarios10/15/16 then pass with their unchanged comparators and declared durability. This does not implement the separately owned HostOwner or callback-scope retirement contract.

The subprocess reload action also used to run mode mutations on the extension worker and replace the request context with `Background`. `TestExtensionReloadUsesUIOwnerAndRequestContext` proves both failures with a suspended owner and a distinguishable context value. The action now posts to the real UI owner, forwards the original context and awaits the result. Native request routing does not claim complete subprocess replacement transport.

The clean-base replay preserves user-bash settlement, remote-editor detachment and autocomplete-wrapper reset before reload. It releases the current input ticket before awaiting callbacks so a reload handler can still open a dialog. Error-returning Reload is propagated through the slash/extension callers instead of printing success after failure. The terminal-free fixture now uses its real discard output for theme writes and selects the same explicit dark theme as the upstream reload fixture; no ANSI normalization is applied to scenario output.

`BenchmarkInteractiveSessionReload` uses the same real Session and owner. Three ten-iteration samples measure approximately 179–220us, 69KB and 610 allocations for empty history; 1000 retained assistant entries take approximately 3.12–3.65ms, 1.93–1.94MB and 16.45K allocations. The allocation profile attributes about 58% cumulatively to `renderSessionEntries`, including message cloning and component construction. Each callback worker and mode owner is joined; no historical worker is retained. No optimization or speed-improvement claim is made. This benchmark excludes subprocess startup and large resource trees.

## Startup and replacement UI rebinding

`interactive_rebind.go` supplies the shared owner used by interactive startup and native Runtime replacement tests. Pi `interactive-mode.ts:2020-2043` captures the current Session before awaiting binding. Replacement applies runtime state, rebuilds chat, and attaches the event consumer before `session_start` can notify or send custom/user messages. Startup attaches its consumer after binding. A late startup completion must not attach another consumer or update the replacement's title.

The four native original cases use actual `CreateAgentSessionRuntime` factories and `Runtime.NewSession`, not an in-place Session leaf move. The overlap case suspends startup and replacement handlers independently, observes the replacement's live event channel before either finishes, releases startup, checks zero title updates, releases replacement, and checks exactly one title update. Go uses one selected Session channel rather than registering callback subscribers; it cannot create duplicate consumers by assigning that channel again. The captured-Session guard still prevents stale title effects and stale replacement state changes.

These four cases were added with the production implementation and initially passed. They are mutation-proven, not pre-fix red-proven. Omitting runtime apply, rendering, pre-bind subscription, or the stale guard each compiles and fails both native tests and the strict Session29 pair. A deliberately missing subscription initially made the failing test block in its subsequent `FlushEvents`; the guard now fails immediately after its failed pre-bind assertion instead of waiting for a consumer it has already proved absent. This changes failure cleanup, not successful assertions or production timing.

The shared owner-call helper is extracted from the already guarded extension reload path. The clean-base replay preserves the current input owner and `initScopedModels`; it does not restore the older input pump or model-scope representation. The complete Session family passes at declared durability. Startup09's existing package-collision diagnostic compares different randomly named Pi/PiG agent roots; a compiling pre-rebind Run overlay reproduces that same path-only failure. Its comparator is unchanged, and the resource/scenario owner must resolve the environment/provenance boundary. Other startup scenarios pass. Full coding/internal-codingagent tests and focused race tests pass; no full repository pass is claimed.

`BenchmarkRuntimeInteractiveRebind` measures fresh destination Services/Session construction, optional setup history, projection refresh, UI rendering/binding, and outgoing Session shutdown. It supplies the actual factory ModelRegistry/settings and retains only the current factory metadata. Empty replacements take approximately 9.8–10.7ms, 19.8MB, and 19.5–19.9K allocations; replacements with 1000 restored assistant entries take approximately 45.2–52.7ms, 29.4MB, and 95.8K allocations. About 78% of allocation space is in `ModelRegistry.RuntimeModels`, mostly `ai.ListModels`; this work makes no optimization or bounded-latency claim. Subprocess startup and physical retirement are not measured.

All 26 assigned native original cases now have evidence, but the five file mappings remain partial. Actual CLI factory construction and mode replacement, cross-SDK callbacks/scopes, and HostOwner retirement are separate incomplete obligations. The production startup caller alone does not close the CLI replacement path.

## Persistence fixes

The first valid runtime red is `TestRuntimeOriginalDuplicateCurrentBranch/memory`: `SessionManager.Clone` unconditionally created a disk file. The shared clone path now constructs the retained log in memory, selects a path only for persisted sources, and writes only when the retained branch contains a user or assistant message (upstream 0.99.1 `session-manager.ts:1717-1725`). The root-user fallback in `ForkToNewSession` also preserves memory-only storage.

The pure guard `TestClonePreservesPersistenceModeAndDefersConversationFreeBranches` covers memory/disk and setup-only/user/assistant-retaining branches. `TestForkBeforeRootUserKeepsMemoryOnlyStorage` covers the separate root fallback. The actual Runtime cases cover the caller boundary. The first-message branching cases assert that the selected disk path does not yet exist.

Pi `src/core/agent-session-runtime.ts:335-352` retains the in-memory SessionManager object while replacing its owning Session. `session_branch_runtime.go` preserves that identity and clears the old log caches when it installs the new branch. `TestRuntimeOriginalDuplicateCurrentBranch/memory` checks both Session replacement and manager identity.

The immediate-write context-edit fixture receives the exact assistant-row adjustment already reviewed in persistence commit `77e85a5ad65af0345c926a65e9c966901a3c3c21`. Its original raw-entry and projected-role assertions remain intact. The new persistence guards test deferred writes separately.

The missing-CWD helper is copied from `40845f8ae` without importing that commit's broader persistence stack. It follows Pi `src/core/session-cwd.ts:14-31`: only a persisted manager with a nonempty missing CWD produces an issue. It does not reject an in-memory manager or impose an extra directory-type check. The factory preflight test verifies rejection before the factory runs. Explicit Open/CWD override and migration integration remain separately owned.

Import uses the existing default path normalizer, including file URLs. Its missing-file error retains Pi's message and file-path property. Exclusive copying preserves source permissions and removes a newly created destination after a failed copy. A directory-input regression first failed because an empty destination remained; the matching Node `copyFileSync(..., COPYFILE_EXCL)` probe returned `EISDIR` without retaining a destination. The original collision test and supplemental import tests exercise these paths.

## Paired evidence

Session24 uses `output_equal` with three runs. It compares actual assistant roles/costs from original runtime site125 and the original resource/chat predicates from #5943 site256. It does not normalize ANSI or whitespace. Its coverage is limited to `agent-session.ts` and `interactive-mode.ts`.

The cost case preserves `usage.cost.total = 0.123` in both live state and the persisted entry. Pi `src/core/agent-session.ts:917-940` awaits extension dispatch before persistence and applies the replacement at `:1098-1110`.

The resource case preserves `/repo/AGENTS.md`, `stale resources`, `restored message`, and width80. It asserts that restored chat survives, resource entries stay out of chat, stale resource entries disappear, and resources precede chat. It also requires the context heading to exist: the original `indexOf` ordering assertion alone can pass when the heading is absent because `-1` sorts before the restored-message index. The Pi adapter records that additional observed predicate without changing the original assertion body. Pi `src/modes/interactive/interactive-mode.ts:1695-1701` clears the resource container independently.

Session25 executes every original runtime assertion in both implementations. Its case markers are emitted only after the full case passes; they are not readiness or registration markers. It also compares actual branching roles, selected text, and file presence. A failing Go assertion fails the process before output comparison.

Twelve compiling mutation groups have unit and paired failures:

- Session24: drop assistant replacement; retain stale resource entries; remove the context heading.
- Session25: retain the old installed Session; omit awaited settlement; reuse a colliding import path; ignore switch cancellation; ignore fork cancellation; construct destination Services with the old CWD; write assistant-free clones eagerly; persist memory-only clones; replace the in-memory manager object.

Two additional compiling unit mutations cover the root-memory fork fallback and failed-copy destination cleanup. Each mutation compiles before its behavioral failure is counted. The source changes exist only in subprocess-local Go overlays. No production mutation remains in the checkout.

## Branching oracle ruling

The original tests expect zero messages at `agent-session-branching.test.ts:105` and `:127`, and two messages at `:152-154`. The pinned implementation retains native system messages. `src/core/agent-session-runtime.ts:291-349` branches at the selected user's parent. `src/core/session-manager.ts:1625-1750` retains the selected branch and defers persistence when it contains no assistant.

The mapping records: **upstream test expectation disagrees with the pinned implementation; PiG matches the implementation**.

`test/parity/testdata/session-branching-oracle-pi.mjs` preserves the original prompt texts and selection positions. It substitutes deterministic faux responses for the credential-gated Anthropic responses. It verifies actual Runtime identity replacement and observes:

```text
Say hello -> selected Say hello -> [system], selected file does not yet exist
Say hi -> selected Say hi -> [system], sessionFile is undefined
Say one / Say two / Say three -> selected Say two -> [system,user,assistant], file exists
```

The Go cases retain those native system entries. The explicit system filter in #2860 belongs only to that file. This is not a live Anthropic comparison.

## Resource measurement

`BenchmarkRuntimeNewSession` includes fresh cwd-bound Services construction and joins the outgoing event consumer before constructing the next Session. It retains only the current consumer, rather than registering a cleanup for every benchmark iteration.

Three Go1.27.1 runs on the lane host measure approximately 240–261 microseconds, 86KB, and 708 allocations per memory-only replacement. The allocation profile attributes about 31% of cumulative allocation space to `ai.publishedRadiusModels`; Session construction and prompt/tool schemas are other material costs. The CPU profile includes substantial GC and scheduler work. No optimization or speed-improvement claim is made.

This measures native memory-only replacement without extension subprocesses or large history. It does not establish subprocess restart latency, UI responsiveness, or large-history bounds. Those measurements remain part of the uncompleted production-mode lifecycle work.

### Replacement latency with extension subprocesses

`BenchmarkSessionReplacementCycle` in `cmd/pig/session_replacement_bench_test.go` drives print mode's production path: `Runtime.NewSession`, then `cliSessionFactory.create` with `cliRuntimeBuilder.rebuild`, then the Session's `Dispose` and retirement. `BenchmarkSessionReplacementPhases` times `buildResources`, `buildSession` and the host release of one build. `PIG_STARTUP_TRACE=1` prints the extension spawn and handshake marks. The fixture is `cmd/pig/testdata/session-replace.mjs`, copied once per extension.

On the lane host (i9-9900K, load 4-12 from other workers, Go 1.27.1, Node 24.19.0), one replacement measures 205-400 ms with one Node extension (median about 300 ms; 4 MB and 34k allocations) and 0.6-1.0 s with four. On the scenario harness, five replacements took 765 ms on rc2 before this branch and 2191 ms with it. Pi took 695 ms.

The time is one Node process start per replacement (D70):

| Phase | Time |
|---|---|
| Services creation, discovery, `buildSession`, Session construction | about 20 ms in total |
| Extension process from spawn to the register handshake (`spawn-done` to `handshake-start` in the startup trace) | about 250 ms |
| Handshake and load reply | about 4 ms |
| Outgoing host shutdown and `Services.Close` (`release`) | 15-30 ms |

Of the 250 ms, `node -e ""` takes 45 ms and `import('./runtime.mjs')` takes 190 ms, with the compile cache already enabled by the runtime; the rest is the extension module. The Go side of a replacement (about 4 MB and 34k allocations) is a small share. Extra extensions add roughly 50-150 ms each.

Disposition: no needless serial wait or double rebuild exists in this path. A replacement runs the pre-trust extension load only for a destination that needs a trust decision and has no cached answer. Starting the new process while the outgoing Session shuts down would reorder extension factory side effects against `session_shutdown`, which Pi's `teardown` then `createRuntime` order does not allow. The cost is the D70 decision to start fresh processes, and the change that removes it is re-invoking factories in the retained process (`team/smc1/d70-close`). Re-measure with these benchmarks when that lands. No scenario declares `runtime_ratio_max` for scenarios 39-42 because Pi's single-process replacement is faster by design.

## Remaining implementation blockers

The CLI modes create and replace their Session through the factory-owned Runtime; see Production modes below. D30 (Part 2) and D61 remain for library callers of `Session.NewSession`, `SwitchSession`, `CloneInPlace`, `ForkToNewSession`, `ForkToNewSessionWithText`, the headless `Session.DispatchSlash` `/fork` path, `ImportFromJsonl` and the default `Session.ExtensionCommandActions` replacement actions. D30 (Part 1) remains for Go, Rust and Python extensions in the CLI modes: their host rejects a replaced Session's later calls with Pi's stale message, but SDK-local getters return the outgoing Session's values and some fire-and-forget calls discard the error. The Node runtime receives the host's `invalidate` notification and throws Pi's stale message from a captured `pi` or `ctx`. Every SDK passes a `withSession` callback to `ctx.newSession`, `ctx.fork` and `ctx.switchSession`; the host runs it with a `with_session` request whose calls the replacement Session serves (`docs/extension-api-parity.md`, Replacement callback async contract). `setup` remains native-only.

Pi `src/modes/interactive/interactive-mode.ts:2020-2043` captures Session identity across an awaited bind. Replacement subscribes before bind. Stale startup completion neither subscribes nor updates the title. Two independent bind completions are required to prove that case. The native #5943 and stale-rebind UI cases now have original-case guards and compiling paired mutations. Their production startup caller is wired; the CLI's actual replacement factory and mode wiring remain incomplete.

The merge train is paused by the lead while the candidate syncs public/main. Do not merge a moving target. Resume integration only after the lead names a stable post-sync tip. The prior integration delta changes Session admission, persistence signatures, tool registries, raw prompt inputs, and event delivery. The helper supplied semantic resolutions for `coding/runtime.go` and `coding/session_ops.go` to the parent lane. The shared merge is not yet validated here. In particular, its `branchedSessionEntries` label/compaction rewriting must survive when combined with the lazy-write fix.

## Production modes

`cmd/pig/cli_runtime_build.go` holds Pi's `createRuntime` closure for the process: `cliRuntimeBuilder` builds the cwd-bound half of one Session runtime (project trust, services, resources, extension host, model, tools and system prompt), and `cmd/pig/cli_session_factory.go` wraps it as the `CreateAgentSessionRuntimeFactory` every mode passes to `coding.CreateAgentSessionRuntime`. The first Session takes the build startup already made. Every replacement rebuilds for the destination cwd. Print, JSON, RPC and interactive mode differ only in how they bind a Session: `printHost`, `rpcHost.bind` and `InteractiveMode.rebindFromRuntime` re-create the extension bindings, event subscription and UI wiring for each Session, as Pi's `rebindSession` does.

The outgoing extension host stays alive while a command handler of that Session is still running, because a handler that requested the replacement still awaits its result. `Runner.WaitForCommands` reports the running handlers, and `cliRetirement` waits for them from a goroutine it owns before it shuts the host down. A Session's in-process extension runner is invalidated, so contexts it captured fail as stale; a subprocess extension's host rejects its later calls with the stale message, but its SDK-local getters still answer and its host calls do not throw at the call site (D30 Part 1). Extension processes are new, so process-wide state of the replaced process does not carry over; D70 covers this difference for `/reload` and for session replacement.

RPC commands rebind twice, as Pi 0.87.1's rpc-mode does: `runtimeHost.newSession` rebinds through its rebind callback, and the command handler then calls `rebindSession()` again. The replacement's extensions therefore receive `session_start` twice after `new_session`, `switch_session`, `fork` and `clone`; an extension-initiated replacement rebinds once. Pi 0.87.1 was probed with a logging extension: `session_start startup`, `session_shutdown new`, `session_start new`, `session_start new`, `session_shutdown quit`.

## Reproduction

Run every command with disposable `HOME`, `TMPDIR`, and effective Pi/PiG agent roots. Resolve installed toolchain paths before changing `HOME`. Use `go test -p 2`, not `go test -p2`: the latter invocation on this toolchain did not run the requested packages and is not evidence of a package pass.

```sh
node --experimental-import-meta-resolve test/parity/testdata/session-runtime-pi.mjs runtime
node --experimental-import-meta-resolve test/parity/testdata/session-runtime-pi.mjs replaced
node --experimental-import-meta-resolve test/parity/testdata/session-runtime-pi.mjs notify
node --experimental-import-meta-resolve test/parity/testdata/session-runtime-pi.mjs rebind
node --experimental-import-meta-resolve test/parity/testdata/session-branching-oracle-pi.mjs

go test -p 2 ./coding ./internal/codingagent
go test -race ./coding -run '^(TestRuntimeOriginal|TestRuntimeImportFileURLAndMissingInput|TestRuntimeMissingCWDPrecedesFactory)' -count=3

PIG_PARITY_PIG_BIN="$PWD/bin/pig-parity" PIG_PARITY_PI_BIN="$PWD/extensions/sdk-ts/node_modules/.bin/pi" go test -tags=parity ./test/parity/runner -count=1 -run '^TestParity/(24-runtime-cost-and-resource-order|25-runtime-replacements)$' -pig-parity.dir="$PWD/test/parity/scenarios/session" -pig-parity.allow-stale
```

The lane handoff identifies the retained evidence directory. Mutation scripts record compilation, unit, and paired exits separately. A merged-tip release-gate pass is not claimed.

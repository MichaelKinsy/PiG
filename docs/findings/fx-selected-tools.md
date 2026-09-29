# fx-selected-tools review handoff

## Disposition

This lane is ready for independent review, not for a claim that PR-4-1 is complete. Two requested part-1 findings remain unresolved: live prompt getters during a handler and Rust whole-options reassignment. No new divergence or waiver is approved here.

The lane imports `0728db573`, `4d6d6f980`, and `ff88e7b79` individually from `staging/team/win/gaps-selected-tools`. The imported commits are `3068a9edc`, `e909d29ce`, and `8e993a9be`. The source branch contains no `native-proof/` files. The coordinator's `PS-HANDOFF-0.3.0.md` supplies the reviewer findings. The follow-up source commit `cede3cf07` corrects the rejected behavior instead of treating the imported tests as acceptance.

| Requested item | Disposition and evidence |
|---|---|
| `_pigPromptSelectedTools` in all four SDKs | Imported. `TestPromptSelectedToolsMutationsAcrossSDKs` exercises native Go, fused Go, and isolated/packed Go, Node, Python, and Rust. It checks replacement, append, empty lists, ordered chaining, awaited completion, handler-error retention, mixed arrays, and null rejection. |
| Non-breaking native Go shape | Fixed. `BeforeAgentStartEvent.SystemPromptOptions` remains a value. `extension.BeforeAgentStartOptions(ctx)` returns the shared per-run pointer. Existing collection edits still work; collection replacements use the accessor. `TestBeforeAgentStartSelectionCanBeRepaired` includes value-typed event construction. |
| Edited tools in the idle prompt/getters | Fixed for the tested Session and interactive paths. Selecting the run loadout no longer rebuilds the base prompt. Session getters distinguish an explicit Agent override from replayed transcript state. The live loadout remains active, but the idle prompt returns to the base options. |
| Duplicate tools | Fixed at admission, not at `setActiveTools`. Pi's direct setter preserves duplicates; `_preparePromptAndToolLoadout` removes them in first-seen order. Session and interactive regression inputs include a duplicate and an unknown name. |
| Null/non-string array | Fixed for null and arrays containing non-string elements. The wire preserves the untyped selection between handlers. Null rejects admission after the chain rather than becoming a swallowed handler error. Non-string array entries are registry misses. Native repair is tested. Arbitrary non-array values and deleted properties remain outside this closure; see the self-review. |
| Section-validation rejection emits `agent_settled` | Fixed. Rejected preflight emits no `agent_start`, Provider request, or `agent_settled`. |
| Custom-message runs emit `before_agent_start` | Fixed. Only prompt-seeded runs dispatch preflight. `TestCustomSeedDoesNotEmitBeforeAgentStart` drives the shared custom-run entry path. |
| `endRunPrompt` and rebind ordering | Fixed at idle publication. The outgoing run clears its prompt before releasing `turnSettled` or publishing idle. Settlement captures its Session and runner rather than consulting a replacement. The stalled-owner regression is mutation-proven. Existing replacement regressions pass with race detection. |
| `ff88e7b79` model/auth ordering | Reviewed and imported. The model/auth check precedes compaction and `before_agent_start`. OAuth failure uses Pi's re-login text. The ordinary replacement fixture now supplies faux auth because its success case must pass the new preflight check; its assertions are unchanged. The inherited unregistered-Provider exemption is not claimed Pi-exact. |
| Rust whole-options reassignment | Unresolved. The Rust dispatcher reads `data["systemPromptOptions"]` after the callback. Pi retains its original options object when a callback replaces the event field. A snapshot of Rust's original `Value` does not fix mutation-before-reassignment because replacing the value destroys the edited original. This needs a shared mutable event-options representation, not a post-hoc value comparison or unsafe borrowed pointer. No such redesign lands in this lane. |
| Live getters inside `before_agent_start` | Unresolved. Session getters during the Provider run and between runs are covered, but the runner still renders handler-facing prompt text from the pre-edit string. Node's event field is also a snapshot. The corresponding SDK context getters do not render each handler's local edits. Fix these together with a shared renderer and an explicit Rust options ownership design; do not close the matrix row based on the new Session getter guard. |

`extensions/sdk` is changed. The lead must recompute its module hash. The lane does not change the SDK checksum lines or the coordinator-owned generated inventories. `go generate ./coding/extension/host/subprocess` regenerates the imported runtime source against this base, and the archive/source consistency test passes.

## Upstream rules

The reference is Pi 0.87.1.

- `packages/coding-agent/src/core/extensions/runner.ts:1312-1364` normalizes one options object, awaits handlers serially, retains mutations after rejection, and renders the event/context getters from that object.
- `packages/coding-agent/src/core/agent-session.ts:1702-1714` compares the final selected-tools list with the base list before choosing between an explicit edit and the live loadout.
- `packages/coding-agent/src/core/agent-session.ts:1409-1419` applies `new Set`, filters registry misses, and changes executable tools without rebuilding the base options.
- `packages/coding-agent/src/core/agent-session.ts:1239-1241,1485` uses the base options after the run ends.
- `packages/coding-agent/src/core/agent-session.ts:1673-1691,1747-1760` validates model/auth and rejects invalid prompt construction before `_runAgentPrompt` and its settlement event.
- `packages/coding-agent/src/core/agent-session.ts:1934-1960` seeds custom-message runs directly through `_runAgentPrompt`.

`TestSelectedToolsPiContract` executes the installed pinned Pi Runner and `AgentSession.prompt` methods. It replaces only the Provider-run sink. The exact observations include first-seen dedupe, mixed-array filtering, null rejection without a run, later-handler repair, retained-object reassignment semantics, and both live getters. The latter two observations expose the unresolved Pig cases rather than proving Pig parity.

## Red and green evidence

The following failures are observed before their corresponding fixes:

- `TestBeforeAgentStartSelectedToolsControlLoadout/replaced_options`: executable active tools remain `[second second]`.
- `TestSubprocessBeforeAgentStartSelectedToolsControlLoadout`: both fused Go and Node leave the idle prompt listing `second` instead of base tool `first`.
- `TestInteractiveBeforeAgentStartControlsRunPromptAndLoadout/edited_options`: the settled getter lists the edited duplicate tools.
- `TestInteractivePromptValidationPrecedesBeforeAgentStart/invalid_section`: events are `[before_agent_start agent_settled]` even though no Provider request occurs.
- `TestCustomSeedDoesNotEmitBeforeAgentStart`: the custom seed emits the hook once.
- `TestPromptSelectedToolsMutationsAcrossSDKs`: null does not reject native admission; mixed arrays produce swallowed JSON-decoding handler errors in fused Go and Node.

`TestRunPromptEndsBeforeIdlePublication` is added after the lifecycle fix. Removing its production `endRunPrompt` callback compiles and fails with `idle published before ending prompt: "run"`. Restoring the callback passes with `-race -count=3`. The additional Agent override accessor, Pi oracle, and native repair tests are added after implementation; they are not independently red-proven.

Final required commands pass:

```text
go build ./...
go vet ./agent ./coding ./coding/extension/... ./internal/codingagent/... ./test/extension-conformance
make lint
make test-porting-release
go test ./test/extension-conformance/
```

The targeted Session, interactive, in-process runner, all-SDK selected-tools, Pi oracle, and replacement tests pass with `-race -count=3`. The Go SDK module tests also pass with `go test -race -count=3 ./extensions/sdk/...`. Complete `go test ./coding ./internal/codingagent` passes. `go tool golangci-lint config verify` and `make lint-changed LINT_BASE=606595120` pass. Plain `make lint-changed` cannot find a merge base with this checkout's `main`.

The full lint gate initially finds an existing import-group error in `coding/extension/host/subprocess/owner_upgrade_test.go`. The only unrelated source change is that file's import grouping. No lint suppressions are added.

Two additional gates are not passing evidence:

- Full subprocess-package tests stop at `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` because Xvfb is absent. The targeted transport tests, all-SDK conformance, and runtime-archive test pass. No skip or replacement display server is added.
- `make generate` runs in a disposable worktree containing the lane edits to respect the task's generated-file restriction. It stops in `parity-deps`, which refuses installation through the shared `extensions/sdk-ts/node_modules` symlink. The lead must regenerate public API inventories in the dependency-owning checkout. No generated disposition or coverage promotion is hand-edited.

## Resource and performance check

The change retains serial awaited handlers and the existing request cancellation/lifetime. The per-run pointer and raw selection belong to that emission; they do not reference base-option collections. No new goroutine, timer, process, retry, or global options cache is introduced. Space is proportional to the options and the existing bounded wire frame. Tool selection runs off the terminal input path except for the existing owner publication of an explicitly changed interactive loadout.

`BenchmarkBeforeAgentStartSelectedTools` measures 0, 8, 128, and 1,024 names with an actual runner dispatch. On Linux amd64, Go 1.27.1, Xeon 6746E, the native typed path measures about 0.85–0.91 µs/992 B/15 allocations at zero names, 1.1–1.4 µs/1,488 B/17 allocations at eight, 3.0–3.3 µs/5,584 B/16 allocations at 128, and 16–18 µs/37,840 B/16 allocations at 1,024. CPU and allocation profiles identify cloning and dispatch allocations. An initial implementation unnecessarily round-trips native selections through JSON; profiles expose that cost, and retaining the typed path removes it. The foreign-value path still preserves JSON semantics. These are construction benchmarks, not claims about end-to-end Provider latency.

Repeat with:

```bash
go test ./coding/extension/host/inproc -run '^$' -bench '^BenchmarkBeforeAgentStartSelectedTools$' -benchmem -count=3 -cpuprofile=/tmp/selected.cpu -memprofile=/tmp/selected.mem
go tool pprof -top /tmp/selected.cpu
go tool pprof -top -alloc_space /tmp/selected.mem
```

## Adversarial self-review

The full diff is re-read against the cited upstream source. Keep these review obligations open:

1. Do not approve PR-4-1 closure while Rust reassignment or live handler getters differ. The Pi oracle proves the required result; Pig does not yet meet it.
2. Untyped values beyond null and mixed arrays are not closed. Pi treats a deleted property as `undefined`, while the current transport turns a missing selected-tools field into null. A string can reach JavaScript iterable semantics; Pig currently rejects non-arrays with a generic array error. These are follow-up requirements, not approved divergences.
3. The inherited `ValidatePromptModelAuth` guard exempts an injected Provider not known to the registry or `modelRuntimeRequiresAuth`. Pi checks configured/checkAuth for every selected model. The imported commit preserves that existing Go embedding path; the registered-provider tests do not close the exemption.
4. Interactive still projects the rendered per-run prompt rather than recording its custom section deltas. This is the pre-existing partial matrix item, not a completed persistence port.
5. The generation gate and the Xvfb-dependent test require coordinator-owned dependency work. Do not report those commands green.

No `native-proof/` file, SDK checksum line, coverage roll-up, or generated interface inventory is included. No tmux server is started. No command writes to `~/.pig`.

# Extension discovery and Markdown callback evidence

This lane starts at `18a0319bd` and completes all 30 original cases in `packages/coding-agent/test/extensions-discovery.test.ts`, together with the normal-path Markdown ordering, cache, and disposal fixes below. The lead assigns exceptional callback completion to `fix-drift-ext` and parser scaling to `fix-markdown-autolink`. Those separate obligations keep the broader Markdown API partial; no original discovery case remains excluded. Runner708 belongs to the separate `pt-extensions-c` checkpoint `1a3c30e9c`; this change does not modify its getter, tests, or mapping row.

## Oracle and contract

The oracle is installed Pi 0.87.1. `core/extensions/loader.ts:327-340` stores message, Markdown, and entry callbacks. Discovery test374-397 registers all three in one `with-renderer.ts` factory and asserts zero errors, one extension, the Markdown callback, and both map entries. The new Go table row retains that combined fixture. It passed before production changes because the registration/proxy APIs already existed on the base. The prior mapping rationale claiming an absent API was stale. A compiling registration-erasure mutation supplies the negative proof; this is not a newly implemented API.

`modes/interactive/components/markdown-transform.ts:18-29` invokes each transformer synchronously in chain order. It accepts string results, including empty strings, and retains the current input after a throw, Promise, or other non-string result. Direct installed-Pi probes record the complete `B first → A first entered → A first returned → C first → B second → A second entered → C second` trace. `assistant-message.ts:91-116` clears and reconstructs content components on each update. `packages/tui/src/components/markdown.ts:266-285` invalidates its render cache and reruns the transform, including for unchanged text.

PiG keeps logical generation cancellation separate from the lifetime of an admitted synchronous chain. The generation loses publication authority on replacement. The chain retains the captured Mode context until completion or Mode cancellation. A single Mode queue serializes individual generations across components. Replacing a queued generation removes its old queue position and appends the new admission. No generic Conn, request-parent, SDK dispatch, Provider, or session-replacement code changes.

## Original discovery case table

Each row is in `TestUpstreamExtensionsDiscovery`. The unchanged 29 rows retain their original test inputs and assertions; the full subprocess package executes all 30.

The scope-closure recheck matches all 30 ordered Go case names to the independent upstream `it` sites and verifies the recorded upstream SHA-256. `go test -race ./coding/extension/host/subprocess -run '^TestUpstreamExtensionsDiscovery$' -count=3 -timeout 10m` passes in 53.178s. Scenarios33/62/63 again pass three installed-Pi pairs each. Full native vet, touched Windows vet/lint, and docs-drift pass. This closure update changes only the ledger and ownership documentation; production code and the earlier mutation/conformance evidence are unchanged.

| Pi test line | Original case | Status |
|---:|---|---|
| 43 | direct TypeScript files | retained/pass |
| 54 | coding-agent entrypoint import | retained/pass |
| 72 | OAuth compatibility barrel | retained/pass |
| 90 | direct JavaScript file | retained/pass |
| 100 | subdirectory index.ts | retained/pass |
| 113 | subdirectory index.js | retained/pass |
| 125 | index.ts preference | retained/pass |
| 138 | package pi field | retained/pass |
| 162 | package-relative tilde entries | retained/pass |
| 187 | multiple package extensions | retained/pass |
| 208 | pi field precedence over index | retained/pass |
| 233 | missing pi field fallback | retained/pass |
| 252 | ignore directory without entrypoint | retained/pass |
| 264 | no recursion beyond one level | retained/pass |
| 278 | mixed direct and directory entries | retained/pass |
| 299 | nonexistent package entries | retained/pass |
| 319 | command registration | retained/pass |
| 329 | tool registration | retained/pass |
| 339 | invalid source error | retained/pass |
| 349 | explicit configured path | retained/pass |
| 361 | extension-local dependency | retained/pass |
| 374 | combined Markdown/message/entry renderers | added/mutation-proven |
| 399 | initialization failure | retained/pass |
| 414 | missing default export | retained/pass |
| 429 | distinct tools across extensions | retained/pass |
| 448 | event handlers | retained/pass |
| 467 | shortcuts | retained/pass |
| 485 | flags | retained/pass |
| 503 | explicit-only loading | retained/pass |
| 521 | empty explicit loading | retained/pass |

## Red and mutation evidence

Raw logs and profiles are retained in the lane evidence directory. No source mutation remains active; mutations use external Go overlays.

| Guard | Observed red result | Source correction |
|---|---|---|
| `TestMarkdownModeReplacementWaitsForCallbackBody` | next B and A entered before the independently released first remote A returned | Mode-owned invocation context and FIFO chain execution |
| `TestMarkdownModeCrossComponentAdmissionOrder` | B1 entered while A1 remained blocked | one shared queue, not separate component workers |
| replacement chain with trailing C | the initial draft skipped C(first) after logical cancellation | finish the admitted chain using the existing shared `applyMarkdownTransformers` function |
| `TestAssistantEqualContentRerunsMarkdownTransformer` | second update displayed version1 | component revision changes on retained equal-text updates |
| `TestAsyncMarkdownOptionsReplacementRepaintsEqualText` | second transformer kept the first painted result | options identity and transformed-content cache key |
| `TestDroppedAssistantMarkdownCancelsGeneration` | removed segment retained a live generation | dispose removed/replaced segments |
| `TestMarkdownModeClearRemovesQueuedBodies` | mutation executed a removed queued callback | Mode clear/rebuild/shutdown disposal |
| discovery374 | registration-erasure mutation removed the Markdown declaration | existing host registration binding remains exercised |

The local-queue, early-request-cancel, omitted-equal-content-invalidation, stale-transformed-cache, omitted-segment-disposal, omitted-Mode-disposal, removed-registration, and reversed-queue-position mutations each compile and fail their owning runtime assertion. The registration mutation also fails real-Pi scenario62; both Mode-order mutations fail scenario63. Restored scenarios33/62/63 pass three pairs each. Queue tests additionally cover both admission orders, replacement at the tail, A→B→A, and empty retained queue state. Owner-shutdown tests prove cancellation remains connected to the real Mode context. The original logical-cancellation and first-paint tests are retained.

## Paired and cross-SDK evidence

- Scenario62 compares the complete original discovery registration record with installed Pi for three pairs. It is deliberately `registration-only` and claims only `loader.ts`, not callback rendering.
- Scenario63 compares complete callback-order traces from the actual Go Mode and fused SDK with Pi's synchronous chain. The Go adapter runs the private package tests and fails if either test fails or a trace is absent. It does not implement a second transform pipeline. The real terminal path remains scenario33.
- Scenario33 retains its complete escaped-output comparator and three pairs. It asserts user and assistant display rewrites, not cross-component timing.
- `TestMarkdownContextsAcrossSDKs` uses the existing common fixture through all seven reference/isolated/fused cases plus packed Go, Python, and Rust. It compares immediate results for empty, ordinary, Unicode/multiline, and64KiB input across user/assistant/visible-thinking contexts, widths1/17/4096, and streaming state. Every expected value differs from a transport fallback. It passes under race in57.461s. No SDK fixture or callback implementation changes.

## Resource disposition

Admission stores one active owner job and at most one queued replacement per component. Completion and disposal remove queue references. The worker holds no queue or component locks during extension callbacks. Its protected result and the parent's existing atomic dirty flag feed the race-safe render scheduler. The Mode owns the worker through its existing background-task join. Removed messages retain no queued work; an admitted synchronous callback can finish without publishing obsolete output. Stored Session messages and provider input are unchanged.

`BenchmarkMarkdownTransformQueue` measures admission and joined worker completion separately from parsing. Three100-iteration samples span roughly1.48–2.66µs,325–331B, and7allocations at0/1KiB/64KiB. CPU and allocation profiles are retained. These are local queue measurements under concurrent gate load, not IPC, terminal, or before/after speedup claims.

`BenchmarkMarkdownTransformGeneration` retains the full parse, including an unbroken64KiB paragraph. Its single64KiB iteration completes in134.796s with2,377,677,152 allocated bytes and136,554 allocations. `parseAutoLink` accounts for96.52% cumulative CPU and98.07% cumulative allocation space. It rescans, converts, and email-matches the remaining nonspace suffix for every character. The bounded URL-prefix probe does not bound that email path. This is a parser-owner blocker, not a passing performance qualification. The original three-minute budget was not extended and the stress input was not reduced. Parser/style code is unchanged. The lead assigns the finding and retained profiles to `fix-markdown-autolink`.

## Separately owned work and gates

Exceptional inactivity, disconnect, and shutdown can settle the proxy before an uncooperative SDK body returns. The ordinary-replacement fix does not prove global body order after those exceptional exits, does not add SDK callback gates, and does not expand D56 approval. The lead assigns that remaining behavior to `fix-drift-ext`, which coordinates any shared transport changes with KEEP. `fix-markdown-autolink` owns the parser correction. Both remain open outside this lane's completed discovery and normal-path scope.

Initial full touched-package race run: TUI passes30.200s and subprocess passes365.027s. Internal/codingagent fails116.829s on a new test's missing explicit PIG_HOME declaration and the separate shell truncation test. The new test now explicitly pins PIG_HOME; the hygiene guard passes. Final full TUI passes29.987s. Final internal/codingagent fails113.853s only on `TestShellResultRendersExecutedTruncation`. Its expected single-line truncation suffix wraps under this lane's long isolated TMPDIR. A compiling overlay of all eight original18a production files reproduces the same unchanged shell assertion; the new tests are absent from that parent-only probe. A separate disabled-Markdown-pipeline probe also fails. No shell assertion, timeout, path, or comparator is changed to hide it.

| Gate | Result |
|---|---|
| full original `go test -race ./test/extension-conformance -count=1 -timeout 30m` | PASS842.997s; all original tests run |
| new Markdown context matrix across ten realizations | PASS57.461s under race |
| focused Mode/queue/cache/disposal/first-paint tests | PASS three race repetitions |
| full TUI/subprocess packages | PASS29.987s /365.027s under race |
| full internal/codingagent package | FAIL only the independently parent-red shell suffix assertion |
| full extensions-runtime family | completes231.001s; only scenario29 fails at `/10/assistantMessageEvent/contentIndex`, Pig1/Pi0; all other scenarios retain their declared runs and comparators |
| new62/63 plus unchanged33 after mutations | PASS three actual-Pi pairs each |
| full native vet; touched Windows vet | PASS |
| touched lint, parity-driver lint, `make lint-changed LINT_BASE=18a0319bd`, full `make lint` | PASS, no new suppressions |
| `make -k ci-contracts ci-drift` after local generation | production checkpoint retains217 release findings; discovery closure retains216. Both retain unapprovedD78 and inherited scratch-path findings in `docs/parity/extension-factory-cache.md:27` and `docs/parity/failed-factory-api.md:22`; owners notified |
| docs-drift and scenario lint | PASS |
| `go fix -diff ./...` | only two out-of-scope baseline suggestions: frontmatter reverse iteration and the skills-test string split; no source changes applied |

Scenario29 retains its exact JSON comparator and reports a missing first `toolcall_end` before the next toolcall starts. No Markdown ordering claim relies on that failed Provider/Event Stream path. The initial parity invocation without an explicit installed-Pi override skipped; it is not counted as evidence. All reported paired passes use real Pi0.87.1.

Generated coverage and interface outputs are regenerated for gates but are not part of the lane commit. Only the assigned discovery test mapping is promoted to ported. The broader Markdown API and source mapping remain partial for the separately owned obligations; no other upstream test mapping row changes and no new divergence is claimed. The merge train remains paused, so this checkpoint retains its assigned18a base instead of merging a moving integration tip.

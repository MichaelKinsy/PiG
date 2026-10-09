# Review: designout-audit (READY d52b0cf872)

Verdict: ACCEPT-WITH-FIXES. The fixes are on staging `team/imladris/rev-designout-audit`, based on d52b0cf872.

Scope: the lane is docs-only. Commit d52b0cf872 adds `handoffs/designout-audit.md` and changes no code, ledger data or generated file. The reviewed upstream is the pinned mirror: `internal/coding/pigversion/pigversion.go` pins Pi 1.1.0 (`.upstream/current -> v1.1.0`). The review brief names Pi 1.0.4, but this branch targets 1.1.0, and every claim below was checked against 1.1.0. `docs/parity/gap-closure/designout-audit.md` does not exist; the lane's only output is the handoff.

## What holds

- Completeness: the table has 383 rows. They are exactly the 383 `designed-out` rows of `test/parity/interfaces/mapping-v1.1.0.json`: none is missing, none is extra and none is duplicated. The lane's totals matched its own table.
- The 46 `name` rebinds: every Go `Name()` value equals Pi's `this.name`. That includes `Error` for env `HostKeyChangedError`, `HostKeyUnknownError` and `SshError`, which set no name in `env/src/ssh.ts:40-46`, and `AbortError` for `McpAbortError` (`mcp/src/protocol/jsonrpc.ts:74`). The two chord/delta rows are the exception (finding 2).
- The 34 `cause` rows: every listed Pi class has an explicit constructor that calls `super(message)` with no ErrorOptions, and no Pi code assigns `.cause` to them. `DisconnectedError` forwards a cause (`client/src/errors.ts:14-15`), and its row is correctly not designed out. The Pi claim behind KEEP is therefore true. Finding 1 changes the class for nine of the rows.
- The 47 `stack` rows rest on D103, which records an owner decision with a date and `SCRUTINIZED:approved` (`docs/parity/DIVERGENCES.md:1403-1421`). None of the designed-out error types has a Go `Stack` member.
- The `telemetryContext` rows: Pi declares the field (`ai/src/types.ts:141`), copies it once (`simple-options.ts:48`) and never reads it. Go declares it on `StreamOptions`, `ClassifierOptions` and `ImagesOptions` and also never reads it. The rebind is correct.
- OWNER Q1 and Q2 cite the right Pi lines (`agent-session.ts:252-296`, `sdk.ts:461`, `anthropic-messages.ts:286-290,607-609`). Go can match each option that the lane lists, so the questions are product choices, not impossibilities.

## Claim probes (riskiest first)

The lane claims no test, so the mutation check was applied to the claims that decide each PORT verdict. A probe fails when the claimed gap exists.

1. `ClassifierHttpError::property:name` (PORT code). A temporary test wrapped `ai.PostClassifierRequest` with `TimeoutMs: 50` against a stalled server inside `telemetry.NewInMemoryTelemetryContext().StartSpan`. The span recorded `{Name:Error Message:Request timed out after 50ms}`. Pi records `TimeoutError` (`classifier-shared.ts:23`, `telemetry/src/memory.ts:81`). The gap is confirmed.
2. `LatestConversationSemantics` and `RewindableConversationSemantics` (PORT code). A temporary test called `durable.DefineDoc` with `{conversation, latest, asOf}` and with `{session, rewindable, asOf}`. Both were accepted without a panic. The gap is confirmed.
3. `is*ToolResult` (PORT code). No production code builds a `BashToolResultEvent` or any other built-in variant. The Session emits `CustomToolResultEvent` (`coding/session_extension_hooks.go:109`). `extension.UnmarshalToolResultEvent` (`coding/extension/marshalling.go:152`) has no production caller. The Go SDK passes `map[string]any` to tool_result handlers (`extensions/sdk/extension.go:41,886`). The documented type-switch alternative never matches. The gap is confirmed.
4. `discoverAndLoadExtensions` and `LoadExtensionsResult.runtime` (PORT code). No production code sets `DefaultResourceLoaderOptions.LoadExtensions`, and nil loads no extensions (`coding/resource_loader_extend.go:141-145`). `ApplyExtensionFlagValues` is called only from `cmd/pig/cli_runtime_build.go:435`. `coding/extension/host/subprocess` does not import `coding`, so a public loader in `coding` creates no import cycle. The gap is confirmed and the port is feasible.
5. `cloudflareStreams` and `resolveCloudflareModel` (PORT code). `ai/cloudflare.go:14-15` gates on two provider-ID literals, and `ai/system_one.go:354` passes the literal `"cloudflare-workers-ai"`. Pi wraps the providers (`cloudflare-workers-ai.ts:20`, `cloudflare-ai-gateway.ts:22-24`). The gap is confirmed. Finding 3 corrects the proposed work.

Temporary probe files were deleted. No probe is committed, because this branch is docs-only like the lane.

## Findings

1. Fixed (row correction, 9 rows). The `cause` rows of ClientDisposedError, McpAbortError, McpAuthRequiredError, McpConnectionClosedError, McpError, McpHttpError, McpSessionExpiredError, McpTimeoutError and OAuthRegistrationError were KEEP a. Go already declares `Cause()` returning nil on each of them: `internal/experimental/client/transport.go:93`, `mcp/jsonrpc.go:333,358,382,406`, `mcp/streamable_http.go:82` (promoted to the two embedding types) and `mcp/oauth/errors.go:135`. Five ledger rows already target `X.Cause`. The report's own rule ("Go already has the member, so the row is mislabelled") makes these mapping-only rebinds, as it did for `name`. Now PORT rebind.
2. Fixed (row correction, 2 rows). `pkg:chord/delta#PathError::property:name` and `UnsafePathError::property:name` were PORT rebind to `internal/chord/service.go`. The public package `chord/delta` defines its own PathError and UnsafePathError without `Name()` (`chord/delta/ops.go:55-66`). The public durable packages import `chord/delta`, so a Go caller receives the types that lack a name, and cannot import the internal target. The lane noted the duplicate as a side issue but kept the rebind. Now PORT code, lane port-chord-delta-error-name.
3. Fixed (lane text). The port-ai-cloudflare-streams lane told the porter to port `cloudflareClassifier`. It is already ported as `ai.CloudflareClassifier` (`ai/system_one.go:351`, ledger `ported`). It passes the literal `"cloudflare-workers-ai"` to get past the provider-ID gate (`ai/system_one.go:354`), which is a second hard-coded provider site. The lane now reworks the existing wrapper.
4. Fixed (lane text). port-durable-semantics-validate proposed a define-time panic. Pi has no runtime check: `validateDefinition` checks only the version (`durable/src/documents.ts:95-99`), and the overloads reject the combinations at compile time. A panic adds a Go-only runtime rejection. The closer translation is a compile-time Go shape, with distinct semantics types and fork enums. The panic remains the fallback. The verdict stays PORT.
5. Fixed (row correction, 2 rows). `pkg:codemode/.#loadQuickJSWasm` and its `call:0` were KEEP c with performance `n/a`. The function is a runtime entry point, not a TypeScript-only artifact. Pi compiles a module once per path for the whole process and retries a failed load (`codemode/src/wasm.ts:14-35`). Go caches the default embedded module process-wide (`codemode/sandbox.go:190-202`), but compiles a custom `SandboxOptions.Wasm` module once per Sandbox (`codemode/sandbox.go:204-219`), and a compile takes hundreds of milliseconds without `CacheDir` (`codemode/types.go:129-130`). The owner addendum makes a design-out that is slower than Pi's shape a PORT. Now PORT code, lane port-codemode-wasm-cache.
6. Fixed (evidence). The BeforeToolCallContext rows listed `Result` and `IsError` members. Go's `BeforeToolCallContext` has neither (`agent/tool_call_hook_funcs.go:11-20`). The env ssh `name` rows said "(no this.name, so 'Error') sets this.name". They now state that Pi sets no name and Go's `Name()` returns `Error`.
7. Fixed (evidence). The ClassifierHttpError PORT cited only telemetry. Pi's own classify paths catch the error (`openai-decisions.ts:191-194`, `system-one-shared.ts:130-132`). The observable route is the exported `postClassifierRequest` (`classifier-shared.ts:60-91`), which Go ports as `ai.PostClassifierRequest`. The lane row now cites that route and probe 1.
8. Open, evidence quality. 69 rows carry evidence truncated with "..." from the old rationale, and 12 of them cite no Pi file:line: `pkg:ai/compat#setBedrockProviderModule::call:0`, `pkg:durable/.#HooksOf` and ten `pkg:telemetry/.#*` type aliases. The brief asked for file:line evidence on every row. The verdicts are correct, because each is a type-level alias, but the evidence column should be completed in the ledger-rebind lane when it rewrites rationales.
9. Open, consistency. The rule "Go has the member, so rebind" is applied unevenly. `ContextKey` is rebound to `internal/chord/chordctx.Key`, but `Context::property:value` stays KEEP c although `chordctx.Value` exists. `createExpectAssertions` stays KEEP c although `durabletest.CreateTestingAssertions` exists with a `testing.TB` parameter. `Context::property:toString` carries class-a evidence ("no Pi reader (grep)") under class c. The verdicts are defensible. The class column is not.
10. Open, outside the designed-out rows. Pi's public `@earendil-works/chord/context` API (`createContextKey`, `withContextValue`, `withAbortSignal`, `withCancel` and `awaitWithContext`) is marked `ported` to `internal/chord/chordctx`, which a Go library caller cannot import. The lane's ContextKey rebind points to the same internal type. chord is in the shipped surface, so the chord owner should decide whether a public Go package exports these.
11. Open, process. Commit d52b0cf872 has no cryptographic signature (no `gpgsig` header), against AGENTS.md "Commit hygiene". It has a `Signed-off-by` line and no AI attribution. Re-signing needs a history rewrite, which the lane rules forbid. The maintainer must re-sign it at landing.

## DIVERGENCE-PROPOSED and OWNER items

The lane proposed no new D-number. It raised three OWNER questions.

- Q1 (AgentSessionConfig.agent, cacheWarmer, extensionRunnerRef). Go can match: `SessionOptions` can take an optional prebuilt agent and warmer, as option B says. The lane's recommendation B is sound. `extensionRunnerRef` is a mutable cell that Go replaces with a closure. Keeping it designed out needs a D-number, as the lane says.
- Q2 (AnthropicOptions.client). Go can match with a consumer-owned client interface (option B). Option A needs a D-number, because Pi's documented AnthropicVertex injection has no Go equivalent without user-written URL, auth and body rewriting.
- Q3 (StdinBuffer get/setMaxListeners). Go can match with Node's default of 10 and the `MaxListenersExceededWarning` text (option B). The lead ruling against a magic 10 is not an owner ratification. Option A needs a D-number. The divergence guard flags a cap literal unless it cites the upstream source. Node's `events.defaultMaxListeners` is a dependency, not a Pi file, so option B would need a dependency-source proof entry (`docs/parity/dependency-source-proof.md`).

## Revised totals

KEEP 196 (a 27, b 47, c 122) · PORT 177 (rebind 147, code 30) · OWNER 10 · total 383. The lane reported KEEP 207, PORT 166 (rebind 140, code 26) and OWNER 10.

## Gates

Only Markdown changed, in both the lane and this review, so no Go package was touched.

- `go vet ./...` passes natively, with `GOOS=windows GOARCH=amd64` and with `GOOS=darwin GOARCH=arm64`.
- `make lint-changed LINT_BASE=593d9e16c8` reports no changed Go files.
- `go test -race` (with `LANE_RACE=1`, because the lane shim drops `-race` otherwise) passes on the probed packages durable, telemetry, chord and chord/delta.
- `make divergence-guard` reports 11 checks and 50 baselined hits, with no new hit.
- `make source-hygiene` is clean.

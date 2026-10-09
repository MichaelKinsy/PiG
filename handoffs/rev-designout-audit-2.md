# Second review: designout-audit (READY d52b0cf872, then READY 9de9f3f6f4)

Verdict: ACCEPT-WITH-FIXES on 9de9f3f6f4. The fixes are on staging `team/imladris/rev-designout-audit-2`. The branch starts at d52b0cf872, fast-forwards to the lane's tip 9de9f3f6f4 (which contains the first review's tip 1b89c50507 and fixes ab03f52c89), and adds this review's commit.

The lane posted a new READY at 9de9f3f6f4 while this review was in progress. That commit closes the first review's F8 and F9. This review covers d52b0cf872 and 9de9f3f6f4, and its row corrections are applied on top of 9de9f3f6f4.

The first reviewer, rev-designout-audit, wrote an ACCEPT-WITH-FIXES verdict in `handoffs/rev-designout-audit.md` (findings F1 to F11). It did not append a REVIEWED line to `logs/ledger-batches.log`, so the router had no verdict. This review does not repeat the first review's checks. It verifies the first review's fixes, reviews the row classes that the first review did not probe (the KEEP a and KEEP c rows and the rebind rows outside the name, cause, telemetryContext and StdinBuffer families), and mutation-checks the tests that the report cites as evidence. Findings continue the first review's numbering at F12.

## Preflight

`bin/ledger-preflight.sh --base 593d9e16c8` on 9de9f3f6f4 reports 4 commits, 0 FAIL and 0 WARN. The default base (the lane's recorded base d52b0cf872) reports 3 commits, 0 FAIL and 0 WARN. The lane changes only `handoffs/designout-audit.md`: no code, no ledger data and no generated file. There is no history rewrite, and FORCE-PUSH passes.

## First review: verified

- F1: `Cause()` exists at every cited line: `internal/experimental/client/transport.go:93`, `mcp/jsonrpc.go:333,358,382,406`, `mcp/streamable_http.go:82` and `mcp/oauth/errors.go:135`. None of the 18 cause rows that stay KEEP a has a `Cause()` method. Five of them have an `Unwrap()` method (F16).
- F2: `chord/delta/ops.go:55-66` defines PathError and UnsafePathError without `Name()`. `internal/chord/delta/delta.go:119-150` has both types with `Name()`, and `internal/chord/service.go:27-28` aliases those internal types. F17 shows that the duplication is wider than the two types.
- Totals: after the first review, the table had 383 rows with KEEP 196, PORT 177 and OWNER 10, and they matched the 383 designed-out rows of `mapping-v1.1.0.json`. Only D103 and D19 are cited by designed-out rationales, and the report covers both.

## Lane follow-up 9de9f3f6f4

- F8 (truncated evidence): a script checked every changed row against the cited Pi 1.1.0 file:line. All 74 changed rows cite a file that exists, and the cited lines contain the symbol, except one row. `pkg:durable/testing#createExpectAssertions` cites `assertions.ts` without a line, and the line is 17. The ApiOptionsMap rows now cite 1.1.0 lines instead of 1.0.4 lines.
- F9: the lane rebinds `Context::property:value` and its `call:0` to `chordctx.Value`, and `createExpectAssertions` to `durabletest.CreateTestingAssertions`. This review had reached the same three corrections independently, and they are accepted.
- The lane moved `Context::property:toString` and its `call:0` to KEEP a with the maintainability claim "chordctx.go maps 1:1 to context/index.ts". That claim is false (F12).
- 9de9f3f6f4 is unsigned, like d52b0cf872 (F18).

## Mutation checks of cited evidence

The lane cites existing tests as proof for some rebind and KEEP rows. Three were mutated, run and restored. The tree was clean afterwards.

1. `TestListToolsResultKeepsEveryToolMember`, which the three `mcp ListToolsResult` rebind rows cite: the `_meta` JSON tag in `mcp/types.go` was renamed to `meta`. The test fails.
2. `TestTypedSpanStarterRecordsStartAttributesAndNestsByCallbackSpan`, which the `TypedSpanStarter` rebind row cites: `bindTypedSpanStarter` in `telemetry/schema.go` was changed to bind the child starter to the root context instead of the callback span. The test fails.
3. `TestBaseToolsOverrideReplacesTheBuiltInToolsAndIsActive`, which the `baseToolsOverride` rebind row cites: `coding/session_tool_registry.go` was changed to drop the override keys from the active names. The test fails with `active tools = [], want the override keys in record order [zeta alpha]`.

All three tests kill their mutant, so the cited evidence holds. The test cited for `BACKGROUND_CONTEXT` and `TODO_CONTEXT` (`chordctx_upstream_test.go:14-26`) asserts only Go standard-library roots and cannot fail on any Pig change. The verdict on those rows is unaffected, but the test does not prove a Pig behaviour (F12).

## Findings

12. Fixed (row correction, 2 rows). `pkg:chord/.#Context::property:toString` and its `call:0` were KEEP c at d52b0cf872 and KEEP a at 9de9f3f6f4, both with "no Pi reader". chordctx has no toString counterpart, so the 9de9f3f6f4 maintainability claim "chordctx.go maps 1:1 to context/index.ts" is false. Pi's own test asserts the text (`chord/test/context.test.ts:20,38`: `[Context BACKGROUND_CONTEXT]` and `...WithValue(first).WithValue(second).WithValue(first)`). Go's port of that test substitutes Go's standard-library text (`internal/chord/chordctx/chordctx_upstream_test.go:10,23-28`). A probe printed `context.Background.WithValue(*chordctx.keyToken, one)` for the layer that Pi prints as `[Context BACKGROUND_CONTEXT].WithValue(first)`. The owner addendum names "a divergence a ported test has to work around" as a maintainability failure. The rows are now OWNER, and the report adds Q4, which also takes up the first review's open F10 (the chord context API is internal in Go).
13. Agreed (row correction, 3 rows, made by the lane in 9de9f3f6f4). This closes the first review's open F9. `pkg:chord/.#Context::property:value` and its `call:0` become PORT rebind to `chordctx.Value` (`internal/chord/chordctx/chordctx.go:27`). `pkg:durable/testing#createExpectAssertions` becomes PORT rebind to `durabletest.CreateTestingAssertions` (`durable/durabletest/durabletest.go:53`), which returns the same `StorageConformanceAssertions`. `createExpectAssertions::call:0` stays KEEP c, because its parameter is the designed-out `ExpectLike` adapter.
14. Fixed (row correction, 1 row). `pkg:coding-agent/.#CreateAgentSessionRuntimeResult::property:extensionsResult` was KEEP a, with the maintainability column "Go runtime result maps the read members", which is a parity reason. Pi's interface extends `CreateAgentSessionResult` (`agent-session-runtime.ts:23`), and Pi's own factory returns `{ ...created, services, diagnostics }` (`main.ts:839-860`). Go's `CreateAgentSessionRuntimeResult` (`coding/runtime_replacement.go:32-41`) redeclares two of the three inherited fields instead of embedding `CreateAgentSessionResult` (`coding/create_agent_session.go:41`). The type therefore does not map 1:1, and that fails the maintainability test. The row is now PORT code, lane `port-runtime-result-embed`. This is not a caller-free addition: `ExtensionsResult` already exists on the embedded type.
15. Fixed (evidence). The `CodemodeSandboxOptions::property:workerUrl` row claimed that the Go loader's retry and caching map to `wasm.ts:20-37`. The first review's F5 proved that claim false, but the row was not updated. The maintainability column now cites the owner approval 2026-10-01 A2/B4 (`owner/approval-2026-10-01.txt:3,5`). That approval has no D-number, so the ledger-rebind lane should record one when it rewrites the rationale. The verdict stays KEEP.
16. Fixed (evidence, 5 rows). The `cause` rows of WrongServerError, SessionNotFoundError, SessionAmbiguousError, SessionNotAttachedError and ServerDrainingError said that Go has no cause. Each type has an `Unwrap()` that returns its embedded `*ServerError` (`internal/experimental/routing/errors.go:41-86`). Under D103's reading that "cause is Unwrap()", Go reports a cause where Pi has none. The Unwrap models `extends ServerError` for `errors.As`. No Go code reads it as a cause, and a caller that walks the chain for a code finds the same code on both links, as Pi's `isErrorCode` (`client/src/unix.ts:289-297`) would. The KEEP holds, and the parity column now states this.
17. Open, outside the designed-out rows (lane note added). F2 understates the duplication. `chord/delta` (about 2,640 lines) and `internal/chord/delta` (about 2,300 lines) are two Go implementations of Pi's `chord/src/delta`. Both export ApplyImmutable, ApplyImmutableBatches, AssertValidOp, AssertValidWireOp, IsBase, IsReplace, NewDecoder, NewEncoder, Overlap and Track. The `port-chord-delta-error-name` lane note now says so and prefers the single-owner option. The chord owner should schedule the consolidation, because every Pi delta change must be ported twice until then.
18. Open, process. The first review's F11 still applies, and it now covers two commits: lane commits d52b0cf872 and 9de9f3f6f4 have no `gpgsig` header. ab03f52c89, 1b89c50507 and this review's commit are signed. The maintainer must re-sign both lane commits at landing, because a rewrite is forbidden here.

The lane closed the first review's F8 and F9 in 9de9f3f6f4, apart from the missing line on `createExpectAssertions` noted above. The ledger-rebind lane can add that line when it rewrites the rationale.

## Rows checked without change

- KEEP a `AgentTool::property:replay`: Pi 1.1.0 declares `replay?: "never" | "safe"` at `agent/src/types.ts:490`, and no Pi code reads it. Durable reads `ToolRegistration.replay` (`durable/src/harness/tool.ts:88,98`), which is a different type with a different domain (`"safe" | "unsafe"`). Go `agent.AgentTool` is an interface, and a dead field would be a caller-free member. The KEEP holds.
- KEEP a for the remaining 17 `cause` rows: the Pi constructors at the cited lines pass no ErrorOptions, and Pi assigns `.cause` only in `openai-codex-responses.ts:691-710`. Pi's generic cause walkers (`client/src/unix.ts:296`, `experimental/server.ts:238` and `utils/tools-manager.ts:390`) never see a defined cause on these classes.
- KEEP c `CopyJsonOptions` and `copyJson::call:0`: `omitUndefinedProperties` acts only on a property whose value is `undefined` (`chord/src/json.ts:55`). Go has no such value, and `chord/json.go:12-13` documents this.
- KEEP c for `tui Marked`, `Token` and `Tokens`: no public Pi tui signature takes a marked `Token`. Only private methods of `components/markdown.ts` use it.
- PORT rebind `DeferredFetchOptions::property:wait`: Pi passes the options through to a provider's `fetchDeferred` (`ai/src/models.ts:929,1138`), so an extension provider can read `wait`. Go carries `Wait` (`ai/models_runtime.go:38`). The rebind is correct.
- PORT rebind for the Before/AfterToolCallContext, AnyTask, CommonDocDefinition, Services, UsesDefaultTools and TypedSpanStarter rows: each cited Go member exists at the cited file.

## Revised totals

KEEP 190 (a 26, b 47, c 117) · PORT 181 (rebind 150, code 31) · OWNER 12 · total 383. At 9de9f3f6f4 the lane's totals were KEEP 193 (a 29, b 47, c 117), PORT 180 (rebind 150, code 30) and OWNER 10. After the first review they were KEEP 196, PORT 177 and OWNER 10.

## Gates

Only Markdown changed. All mutation probes were restored, and `git status` was clean before the commit.

- `bin/ledger-preflight.sh` reports 0 FAIL and 0 WARN.
- `go test` killed the mutant in `./mcp`, `./telemetry` and `./coding` for each of the three named tests. Each run used scratch `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR`, `PIG_HOME` and `PI_HOME` directories.

REVIEWED designout-audit 9de9f3f6f4 -> ACCEPT-WITH-FIXES (staging team/imladris/rev-designout-audit-2; signed, Signed-off-by, based on 9de9f3f6f4)

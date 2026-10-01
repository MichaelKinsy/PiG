# Lane port-99-f6g: tools (family 6, sub-lane 6G)

Scope: plan section 4.4 "Tools". Owned code: `internal/codingagent/tools/**` (there is no `coding/tools*`). Upstream: `.upstream/v0.99.1/packages/coding-agent/src/core/tools/{bash,output-accumulator,truncate,tool-definition-wrapper,render-utils}.ts`, `renderers/{bash,read}.ts`, `src/utils/shell.ts`. Tests: `test/tools.test.ts` (84 to 85), `edit-tool-legacy-input.test.ts`, `experimental-cli-entry.test.ts`.

## Upstream delta and disposition

| upstream change | Go disposition |
|---|---|
| `bash.ts:23-66,262,389-411` `outputSchema`, `structuredContent` `{output, truncated, full_output_path?, exit_code, wall_time_seconds}`, non-zero exit is a returned `isError` result (was a throw), details kept | `executeShellTool`, `BashTool.OutputSchema`, `PowerShellTool.OutputSchema` (shared by both shells) |
| `output-accumulator.ts:151-183` `readFullOutput` | `OutputAccumulator.ReadFullOutput` |
| `truncate.ts:278-314` `truncateMiddle` | `TruncateMiddle` (consumer: family 8 MCP) |
| `shell.ts:153-161` `sanitizeBinaryOutput` rewrite | none: the Go filter already removes exactly the same code points; exhaustive test added |
| `renderers/read.ts:29-33` null offset/limit | none: JSON null decodes to a nil pointer in `tui.FormatReadHeader` and `readLineRange`; guard test added |
| `renderers/bash.ts` caches the complete rendered lines | none: performance only, output order unchanged (blank line, hint, preview); Go renderer `makeShellBodyRenderer` has no per-width cache and is not on a hot path measured here |
| `render-utils.ts` `formatToolCallWithArgs` | not mine: generic tool-call rendering is family 9 (`tool-execution-component.test.ts`) |
| `tool-definition-wrapper.ts` `ToolContextFactory(toolCallId, signal)`, `outputSchema` passthrough | `ExtensionToolContext` and the wrapper are lane 6D; the tool `OutputSchema()` method is the tool side |
| `tools.test.ts` `ExtensionContext` to `ExtensionToolContext` (fakeCtx) | type rename only; Go tools take a `context.Context` |
| `edit-tool-legacy-input.test.ts` same rename | per-case: the eight cases keep their inputs; existing `TestEditPrepareArgumentsUpstreamCases` passes unchanged |
| `experimental-cli-entry.test.ts` `--import` now takes a file URL | per-case designed-out: the Go tests build and run the `cmd/pig` entries, so no Node `--import` specifier exists (`cmd/pig-experimental/entry_upstream_test.go`, `cmd/pig/experimental_entry_test.go` pass unchanged) |

## Stubs and dependencies

- `agent.AgentToolResult.StructuredContent json.RawMessage`: family 4 (READY on `port-99-f4`) owns it. This lane adds the identical field and comment line so the two merge without a conflict; the integrator should keep one copy.
- `agent.OutputSchemaProvider` (family 4, `agent/run_tool_call.go`) is satisfied structurally by `OutputSchema() json.RawMessage`; this lane does not import it.
- Consumers of `structuredContent` on the wire (RPC, extension host, session JSON) belong to 6D/6E and family 7.

## Commits

| step | commit |
|---|---|
| red: ported tests with stubs | afb5e66df |
| green: implementation | de4c7fe2b |
| follow-up: builtin tool wire probe compares structuredContent | see `git log` (subject `test(parity): compare structuredContent in the builtin tool wire probe`) |

Counts: red run 23 failing subtests and 43 passing (evidence `docs/plan/evidence/port-99-f6g.red.txt`); green run passes (`port-99-f6g.green.txt`). Ported/new tests: `tools.test.ts` v0.99.1 cases at :493, :513, :554; `TestShellTool*` (both shells), `TestReadFullOutput`, `TestTruncateMiddle` (includes `mcp-extension.test.ts:289`), `TestBashStructuredResultMatchesRecordedUpstream` (oracle recorded from real upstream 0.99.1), `TestSanitizeBinaryOutputRemovesExactlyTheUpstreamClass`, `TestRoundedWallTimeSeconds`, `TestReadHeadersTreatNullRangeAsOmitted`.

## Integrator notes (generated ledgers this lane does not commit)
- `test/parity/interfaces/test-mapping-v0.87.1.json` (and its 0.99.1 successor) rows: `tools.test.ts` hash changes, 84 to 85 cases, evidence adds the three v0.99.1 cases above and `bash_structured_oracle_test.go`; `edit-tool-legacy-input.test.ts` hash changes (type rename only); `experimental-cli-entry.test.ts` hash changes (`--import` URL, designed out per case: Go builds and runs the entry).
- PORT_MAP rows: `core/tools/bash.ts`, `output-accumulator.ts`, `truncate.ts` (`truncateMiddle`, consumer family 8 MCP), `utils/shell.ts` (sanitize unchanged in Go by construction), `renderers/read.ts` (null range already faithful), `renderers/bash.ts` (cache only), `render-utils.ts` (family 9).
- `make interface-go interface-recommendations-generate`: new exported `FullOutput`, `OutputAccumulator.ReadFullOutput`, `MiddleTruncationResult`, `TruncateMiddle`, `BashTool.OutputSchema`, `PowerShellTool.OutputSchema`, `agent.AgentToolResult.StructuredContent` (identical line to family 4's).
- Public API: `agent.AgentToolResult` gains `StructuredContent`; a non-zero shell exit is now a returned error result with no `details` (was `details: {}`). CHANGELOG migration text belongs in the family 4 / 6D fragment that introduces `StructuredContent`; this lane adds no separate fragment.

## Deferred
- Renderer cache of `renderers/bash.ts` (perf only). `formatToolCallWithArgs` (family 9).
- The `ReadFullOutput` error branch inside `executeShellTool` (temp file removed between `Snapshot` and read) is covered at the accumulator level only.
- `cmd/pig` has five failing tests on the unchanged base as well (TestRPCBedrockConverseStreamObservation, TestRPCMistralMatchesPi, TestRPCPiMessagesMatchesPi, TestRPCStdoutBackpressureLetsProviderFinishBufferedBody, TestJSONModeStdoutBackpressureMatchesPi); `./coding` has the four known oracle-version-guard failures.

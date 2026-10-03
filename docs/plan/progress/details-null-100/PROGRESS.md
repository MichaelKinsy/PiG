# details-null-100: explicit null tool-result details

Lane: details-null-100. Branch `details-null-100`, from staging `agg-100`. Upstream Pi 1.0.0.

## Status

READY for review. `packages/durable/test/harness-tools.test.ts` is `ported`; it was the last partial durable row (open item of rev-d-harness-c).

## Change

Pi keeps a tool result's `details: null` apart from absent details: `agent-loop.ts` createToolResultMessage sets `details: finalized.result.details`, durable `tool.ts` keeps an explicit null over the last reported value, and JSON.stringify writes null but omits undefined.

- `ai.ToolResultMessage` and `agent.ToolResultMessage` carry `DetailsNull`. JSON writes it as `"details":null` and decodes it from that member. `ai.ToolResultMessage` decodes itself; `durable.DecodeMessage` uses it.
- Producers: durable `AppendToolResult`, and the agent loop through `AgentToolResult.DetailsNull()` (a subprocess tool that wrote the details member with no value).
- Writers and readers: the session file, RPC messages, JSON events, `get_messages`/`get_entries`, provider conversion, compaction conversion, extension model requests, the subprocess render payload, and resumed transcripts (`ToolResultMessage.Result()`).
- The `tool_result` event gives handlers `details: null`. A hook result with null details is nullish, as `afterResult.details ?? result.details`.
- `durable.ToolControl` writes an empty `addTools` list as `[]`, as Pi stores it.

## harness-tools cases closed

1. "uses explicit null details instead of the last reported value" asserts Pi's stored message byte for byte over the memory, SQLite and JSONL storages (after a reopen), beside a tool without details.
2. "drops control keys set to undefined instead of faulting" asserts the stored control `{ addTools: ["extra"] }`, no fault, and the edited `{ remove }` filter.

## Evidence

| Test | Red before the fix |
|---|---|
| `durable/harness` TestToolProgressAndLifetime "uses explicit null details" | yes; also by mutation of `tool.go` and of the decoder |
| `durable` TestDecodeMessageKeepsToolResultDetailsAsPi | yes |
| `agent` TestAgentMessageToolResultDetailsRoundTripAsPi, TestCreateToolResultMessageKeepsExplicitNullDetails | yes |
| `cmd/pig` TestRPCToolResultMessageKeepsDetailsAsPi | yes |
| `internal/codingagent` TestSessionToolResultDetailsRoundTripAsPi | yes, by mutation of the decoder |
| `internal/codingagent` TestSubprocessRequestMessagesKeepToolResultDetailsAsPi | yes, by mutation |
| `coding/extension/host/subprocess` TestToolResponseKeepsExplicitNullDetails | yes, by mutation of the render payload |
| `durable` TestToolControlJSONKeepsPresenceAsPi, `internal/codingagent` TestToolResultEventOverrideTreatsNullDetailsAsNullish | written after the fix; not red-proven |

Parity scenarios against Pi 1.0.0, red with the base binary and green 3/3 with the fix:

- `json/10-json-extension-tool-null-details` (`output_equal`, `artifact_normalized_equal`): events, the `tool_result` handler's view, and the session file.
- `rpc/43-rpc-extension-tool-null-details` (`json_output_equal`): events, `get_messages`, `get_entries`.

Commands: `go test` over ai, agent, durable, cmd/pig, coding, internal; `-race` on the harness tool tests; `make lint-changed` (0 issues), `lint-scenarios`, `test-inventory`, `interface-go-drift`, `divergence-guard`, `source-hygiene`; Windows vet.

## Not caused by this lane

- `make generate` and `test-porting-release` stop at the release policy of `packages/chord/test/delta-tracker/tracker.test.ts` (needs `SCRUTINIZED:approved`); identical on the base. The remaining generate steps were run individually.
- `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` needs Xvfb, which this host lacks.

## Review fixes (rev-details-null-100)

- `RenderCustomTools` (HTML export) dropped a stored `details: null` before a tool's `renderResult`; Pi passes `msg.details` (export-html/tool-renderer.ts). Regression: `internal/codingagent/export` TestRenderCustomToolsKeepsStoredNullDetails.
- The `turn_end` event's `toolResults` were `agent.ToolResultMessage` values, which JSON writes without `details: null`; Pi hands the messages on (agent-session.ts `_dispatchTurnEndBoundary`). Regressions: `coding` TestTurnEndToolResultsKeepDetailsAsPi and a `turn_end` row in the `json/10-json-extension-tool-null-details` artifact.

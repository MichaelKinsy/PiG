# fix-992-argument-order: tool arguments keep the model's member order (audit-992-codemode-mcp CM3)

Base: `staging/porter/pi-0.99.1` at `299ad70c3`. Oracle: upstream 0.99.2 (`.upstream/current`), cited as `.upstream/v0.99.1/...` where the file is unchanged.

## Cause

Pi stores `toolCall.arguments` as the JavaScript object `JSON.parse` built, so every consumer sees insertion order (integer-like keys first). `validateToolArguments` returns `structuredClone` of it (`packages/ai/src/utils/validation.ts:317-339`). PiG sorted the members in two layers: `ai.ToolCall.Arguments` is a `map[string]any` and `agent/validate.go` re-marshals a map.

## Red run (before any fix)

Stubs: `ai/tool_call_arguments.go` (`ToolCall.ArgumentsJSON`, `ToolCall.SetStreamingArguments`, `MarshalJSONInSourceOrder`) return the sorted result of `encoding/json`, so every test below fails on member order and nothing else.

| test | result before the fix |
|---|---|
| `ai` `TestToolCallJSONKeepsArgumentMemberOrder` (5 cases) | 3 fail on order (nested, integer-like keys, repeated key); sorted and empty pass |
| `ai` `TestAssistantMessageToolCallKeepsArgumentOrderThroughCloneAndJSON` | fail |
| `ai` `TestSetStreamingArgumentsKeepsMemberOrder` (6 cases) | 4 fail (complete, 3 unfinished forms); empty and non-object pass |
| `ai` `TestToolCallArgumentsJSONAfterMutation`, `TestMarshalJSONInSourceOrder`, `TestStreamBuilderToolCallKeepsArgumentOrder`, `TestSetArgumentsJSONKeepsMemberOrderAndRejectsANonObject` | fail |
| `ai` `TestProviderRequestsReplayToolCallArgumentsInModelOrder` (openai completions, openai responses, mistral, anthropic, google) | all 5 fail |
| `agent` `TestValidateToolArgsKeepsMemberOrder`, `TestPendingToolCallKeepsArgumentOrder`, `TestAgentMessageToolCallArgumentsKeepWrittenMemberOrder`, `TestAgentLoopDeliversToolArgumentsInModelOrder` | all fail |
| `coding` `TestNestedToolCallKeepsTheScriptsArgumentOrder` | fail (start event, host call, nestedCalls record) |
| `cmd/pig` `TestAuditNodeExtensionToolCallsReachTheToolLikePi/arguments_keep_the_models_member_order` (cherry-picked from `audit-992-codemode-mcp` e6a5e8ed2) | fail: `{"alpha":"a","zeta":"1"}`, want `{"zeta":"1","alpha":"a"}` |

The cherry-picked file also holds `prepareArguments_runs_before_validation` and `parallel_batch_starts_in_source_order` (audit findings CM1, CM2). They belong to `fix-992-ext-tool-dispatch`; the file is kept byte-identical to the audit branch so the two lanes merge without a conflict, and they stay red here.

## Green: the fix

Option B (approved by the lead in `questions/fix-992-argument-order.md`): `ai.ToolCall.Arguments` stays a `JsonObject`; the model's member order rides beside it in an unexported `argumentOrder schemaObjectOrder`, the type `ToolSchema.parameterOrder` already uses (`ai/schema_object_order.go`). That helper is reused, not copied: `readSchemaObjectOrder` (strict text), `marshalSchemaWithOrder` (encode), `javascriptObjectKeyOrder` (integer-like keys first). Only an object whose order differs from sorted order is recorded, so a call whose objects are all sorted has no sidecar and compares equal to a literal.

| layer | change |
|---|---|
| `ai/streaming_json.go` | `parseStreamingJsonArguments` returns the order; the partial parser records it as it builds (nested objects are now `map[string]any`, as the strict path decodes them) |
| `ai/tool_call_arguments.go` | `ToolCall.ArgumentsJSON`, `SetStreamingArguments`, `SetArgumentsJSON`, `MarshalJSONInSourceOrder` (new exported surface, no exported field changed) |
| `ai/types.go` | `ToolCall.MarshalJSON` writes the order; new `ToolCall.UnmarshalJSON` reads it (decode of the fields still goes through `extensions/sdk/json`, so a lone surrogate survives as before) |
| `ai/messages.go` | `NestedToolCallRecord` carries the same sidecar (`SetArgumentsJSON`, `MarshalJSON`, `UnmarshalJSON`): the session's `nestedCalls[].arguments` keeps the script's order |
| `ai/stream_builder.go`, `anthropic.go`, `pi_messages_events.go`, `assistant_message_frame.go`, `agent/proxy.go` | every place that rebuilt `Arguments` from streamed JSON now calls `SetStreamingArguments` |
| `ai/openai.go`, `openai_responses.go`, `mistral.go`, `anthropic.go`, `google.go` | a replayed tool call writes `ArgumentsJSON` (Pi: `JSON.stringify(toolCall.arguments)` / `input: toolCall.arguments`). Google decodes `functionCall.args` as raw JSON and builds its delta as `JSON.stringify(args ?? {})` (`google-generative-ai.ts:206-217`), so a missing `args` is `{}` as in Pi |
| `agent/validate.go` | the three `json.Marshal(instance)` returns became `ai.MarshalJSONInSourceOrder(instance, args)`: coercion changes a value, never a member's place (`validation.ts:317-339`) |
| `agent/agent.go`, `agent/message_json.go` | `pendingToolCall.args` is `ArgumentsJSON` (a nil `Arguments` is `{}`, as in Pi, where it is always an object); the session reload decodes a tool call through `ToolCall.UnmarshalJSON` |
| `coding/nested_tool_calls.go` | a codemode/extension nested call decodes with `SetArgumentsJSON`; the start event, the host call and the record all carry the script's order |

The agent loop, hooks, `tool_execution_*` events and every SDK tool already moved arguments as `json.RawMessage`, so they needed no change once `pendingToolCall.args` and validation kept the order. MCP `tools/call` forwards that raw text (`coding/mcpext/tools.go`, `mcp/client.go:CallTool`); `TestClientCallToolSendsRawArgumentsInTheirMemberOrder` pins it on the wire.

New tests in the green commit (added after the red commit, so not red-run in that commit): `TestClientCallToolSendsRawArgumentsInTheirMemberOrder` (guard, green before and after), `TestNestedToolCallRecordKeepsArgumentMemberOrderThroughJSONAndClone` (red on the base: it needs `SetArgumentsJSON`), and `cmd/pig` `TestPersistedSessionKeepsTheModelsToolArgumentOrder` (real binary, session JSONL; red on the base worktree: `{"alpha":"a","zeta":"1"}`).

## Evidence

- Red tests now green: all of the red table above, plus `TestAuditNodeExtensionToolCallsReachTheToolLikePi/arguments_keep_the_models_member_order` (real binary, Node extension).
- Mutation checks (each fails the named test, then restored): `validate.go` back to `json.Marshal` (`TestValidateToolArgsKeepsMemberOrder`, `TestAgentLoopDeliversToolArgumentsInModelOrder`); `ToolCall.UnmarshalJSON` dropping the order (`TestToolCallJSONKeepsArgumentMemberOrder`); `newPendingToolCall` back to `json.Marshal` (`TestPendingToolCallKeepsArgumentOrder`, loop test); partial parser returning no order (`TestSetStreamingArgumentsKeepsMemberOrder`); OpenAI replay back to `json.Marshal` (`TestProviderRequestsReplayToolCallArgumentsInModelOrder`); nested call back to `json.Marshal` (`TestNestedToolCallKeepsTheScriptsArgumentOrder`).
- `GOMAXPROCS=4 go test -race -count=24` on the new tests in `ai`, `agent`, `coding`, `mcp`: ok. The same under `taskset -c 40-43` with four CPU burners (PIDs 1659896, 1659898, 1659899, 1659902, stopped with `kill` on those PIDs): ok.
- Performance (`go test -bench`, same machine, base `299ad70c3` vs this tip):
  - a 1.3 KB `edit` argument text parsed at five prefixes plus the full text: 140 µs, 1226 allocs → 190 µs, 1660 allocs. A first version cost 2625 allocs; `strconv.ParseUint` allocated an error for every non-index key, so `javascriptObjectKeyOrder` now tests for digits first, and `unsortedKeyOrder` skips the clone and sort when no key can be an index.
  - `BenchmarkToolArgumentValidation` (`agent`): read 167 µs → 173 µs, edits-1 223 µs → 223 µs, edits-1000 8.1 ms → 10.2 ms (the token walk that reads the order). The cost is linear in the text, like the validation it follows.
- `go build ./...`, `go vet ./...`, `GOOS=windows go vet` and `go fix -diff` on the touched packages, gofmt, and `golangci-lint` (`ai`, `agent`, `coding`, `mcp`, `cmd/pig`, `internal/codingagent`) are clean.
- Test comparison with the base worktree: every failure in `ai`, `agent`, `coding`, `internal/...`, `extensions/...`, `mcp`, `codemode`, `tui` and `cmd/pig` also fails on the base (a upstream 0.99.2 oracle test needs `extensions/sdk-ts/node_modules`, absent while the npm package is in cooldown, or a Node lockfile oracle), except `TestNodeRetainedRequestContextNormalAndCancelled`, which fails only inside the 437 s `subprocess` package run and passes alone (`-count=3`).

## Not covered (deferred, by owner family)

- `agent/harness` and `pico3`: the frame types (`ai.ToolCallEndFrame.Arguments`, the reducer's `endToolCall`, `agent/harness/execution/tools.go` `ValidateToolArguments` returning a map, `LaneSnapshotTool.Args`, `ToolStartPayload.Args`) carry a map, so the order is lost there. `cloneFrameToolCall` and the checkpoint/finish paths of the reducer keep it. A frame field with order needs the harness owner.
- Bedrock: the AWS SDK decodes `toolUse.input` into a document map and `sanitizeBedrockDocument` rebuilds a map on replay.
- The interactive TUI header of a generic tool (`tui.FormatToolArgs`, `internal/codingagent/interactive_transcript.go`) prints sorted keys by design of that renderer; it belongs to the interactive-rendering family.
- A lone UTF-16 surrogate in a tool argument of a call whose order is not sorted is written as U+FFFD by `marshalSchemaWithOrder` (`json.Marshal` leaves, shared with `ToolSchema`). A sorted call still goes through the unchanged path. Changing the shared helper's leaf encoder collides with `fix-992-ai-js-semantics`, which edits the same lines (`marshalJSONUnescaped`).
- A member added to the `Arguments` map after decoding, when the model's order was already sorted, sorts into place instead of following the others (no sidecar exists then). Nothing in the production path adds members.
- `make parity-family`: needs `extensions/sdk-ts/node_modules/.bin/pi` (npm cooldown), as the other 0.99.2 lanes report.
- `prepareArguments_runs_before_validation` and `parallel_batch_starts_in_source_order` in the cherry-picked `cmd/pig` test stay red: CM1 and CM2 belong to `fix-992-ext-tool-dispatch`.

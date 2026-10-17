# JSON event stream mode

JSON mode runs one prompt and writes its Session header and model-loop events as JSON Lines.

```bash
pig --mode json --model <provider/model> "Your prompt"
```

Use this mode when one process invocation handles one prompt. Use [RPC mode](/docs/latest/rpc) when a client must send several commands to a long-running PiG process. The record types and fields are those of Pi 1.3.0 ([Pi's JSON event stream reference](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/json.md)).

## Framing

Each output record is one JSON object followed by LF (`\n`). PiG writes diagnostics to standard error. JSON mode uses the same field ordering and string escaping as the [RPC event encoder](/docs/latest/rpc#framing).

The first output line is the Session header. PiG writes it with `--no-session` too:

```json
{"type":"session","version":3,"id":"session-uuid","timestamp":"2026-01-15T14:00:00Z","cwd":"/work/project"}
```

Model-loop events follow as they occur. PiG converts and serializes each event during the Session notification, before message persistence. The output reader writes those owned bytes in order; it does not observe a retained partial again later.

## Events

JSON mode converts Session events with the same code as RPC mode. The model loop emits:

- `agent_start`;
- `agent_end` with `messages` and `willRetry`;
- `agent_settled` with `aborted`, which is `true` when the run ended because it was aborted;
- `turn_start`;
- `turn_end` with the final assistant message and tool results;
- `message_start`;
- `message_update`;
- `message_end`;
- `tool_execution_start`;
- `tool_execution_update`;
- `tool_execution_end` with `durationMs`, how long the tool's `execute()` took in milliseconds, measured with a monotonic clock and excluding hooks; it is absent when the tool did not run.

The Session can also emit these events during a JSON-mode run:

- `queue_update` with `steering` and `followUp` when a queued message changes;
- `entry_appended` with `entry` when an extension appends a custom session entry;
- `session_info_changed` with `name` when the session display name changes; an absent `name` means it was cleared;
- `thinking_level_changed` with `level`;
- `compaction_start` and `compaction_end` around automatic compaction;
- `auto_retry_start` and `auto_retry_end` around an automatic retry of a failed provider request;
- `summarization_retry_scheduled`, `summarization_retry_attempt_start`, and `summarization_retry_finished` around a retried summary.

A retried request produces more than one `agent_start` and `agent_end` pair. For example, a request that fails once and then retries emits `agent_end`, `auto_retry_start`, a second `agent_start` through `agent_end`, `auto_retry_end`, and then `agent_settled`.

JSON mode does not emit extension UI requests. Use [RPC mode](/docs/latest/rpc-extension-ui) for extension UI.

## Streaming message updates

A `message_update` record contains one `assistantMessageEvent`. It does not contain a cumulative partial message.

Text streaming uses:

```json
{"type":"message_update","assistantMessageEvent":{"type":"text_start","contentIndex":0}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hello"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_end","contentIndex":0,"content":"Hello"}}
```

Thinking streaming uses `thinking_start`, `thinking_delta`, and `thinking_end` with the same fields. Tool-call streaming uses `toolcall_start` (with `id` and `toolName`), `toolcall_delta` (with the serialized argument text in `delta`), and `toolcall_end` (with the complete `toolCall`). A stream also has `start` at the beginning, without the cumulative `partial`, and ends with `done` (with `reason` and `message`) or `error` (with `reason` and `error`).

The final `message_end` record is authoritative:

```json
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}],"stopReason":"stop"}}
```

Do not concatenate `message_end` text with accumulated deltas. Use one or the other.

## Tool events

A tool call emits lifecycle records:

```json
{"type":"tool_execution_start","toolCallId":"call-1","toolName":"bash","args":{"command":"echo out; exit 3"}}
{"type":"tool_execution_update","toolCallId":"call-1","toolName":"bash","args":{"command":"echo out; exit 3"},"partialResult":{"content":[{"type":"text","text":"out\n"}],"details":{}}}
{"type":"tool_execution_end","toolCallId":"call-1","toolName":"bash","result":{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"structuredContent":{"output":"out\n","truncated":false,"exit_code":3,"wall_time_seconds":0},"isError":true},"isError":true,"durationMs":12}
```

The top-level `isError` reports whether the call failed. `result` is the tool's finalized result object. It carries its own `isError` only when the tool returned a failure, as `bash` does for a non-zero exit. A thrown tool error, an unknown tool, invalid arguments, a blocked call, and a failing `tool_result` handler produce a result of `content` and an empty `details` object without `isError`.

## Completion and errors

PiG drains serialized event output after the terminal `agent_settled` event before it closes the stream. It does not print the final assistant text a second time.

A conversion or serialization failure fails the run without abandoning the event drain. Already serialized records remain ordered, and the first failure remains the reported cause. Termination signals retain their signal exit status rather than reporting an internal cancellation error.

The process returns the prompt error as its exit status. A consumer must read standard error and inspect terminal message events.

## Worked example

This example runs one prompt that makes the model call the `bash` tool, then reads the stream three ways. The provider is a scripted test server that answers with one `bash` call and then a short sentence, so the model's wording is illustrative. The record types, their order and the tool fields are what PiG printed. Pi 1.1.0 printed the same record types and field names for the same prompt, and Pi 1.3.0 did not change the event types.

```bash
pig --mode json --no-session --model mock/mock-1 "Run: echo out; exit 3" \
  2>pig-json.stderr > run.jsonl
echo "exit status: $?"
```

```text
exit status: 0
```

The exit status is 0 although the command inside failed. The tool's `exit 3` is data in the stream, not the exit status of `pig` (the exit status is the prompt error, see [Completion and errors](#completion-and-errors)). `pig-json.stderr` is empty.

**1. The shape of the run.** This `jq` filter prints one line per record: the type, the role of the message, and the kind of streaming update.

```bash
jq -c '[.type, .message.role, .assistantMessageEvent.type]' run.jsonl
```

```text
["session",null,null]
["agent_start",null,null]
["turn_start",null,null]
["message_start","system",null]
["message_end","system",null]
["message_start","user",null]
["message_end","user",null]
["message_start","assistant",null]
["message_update",null,"toolcall_start"]
["message_update",null,"toolcall_delta"]
["message_update",null,"toolcall_end"]
["message_end","assistant",null]
["tool_execution_start",null,null]
["tool_execution_update",null,null]
["tool_execution_update",null,null]
["tool_execution_end",null,null]
["message_start","toolResult",null]
["message_end","toolResult",null]
["turn_end","assistant",null]
["turn_start",null,null]
["message_start","assistant",null]
["message_update",null,"text_start"]
["message_update",null,"text_delta"]
["message_update",null,"text_end"]
["message_end","assistant",null]
["turn_end","assistant",null]
["agent_end",null,null]
["agent_settled",null,null]
```

Read it from the top:

- Line 1 is the Session header. Lines 2 and 3 open the run and its first turn.
- Lines 4 and 5 are a message with `"role":"system"` and empty `content`. It carries the system prompt in `sections` (`preamble`, `tools`, `rules`, `docs`, `cwd`) and the tool declarations in `toolsAdded`. It appears once, at the start of the run, so a consumer sees exactly what the model was given. Skip it if you only want the conversation. Pi emits the same record (`packages/coding-agent/src/core/agent-session.ts:1753`).
- A turn that ends in a tool call has `tool_execution_*` records and a `toolResult` message between the assistant's `message_end` and the `turn_end`. The model then starts a second turn, here with the final text.
- Every `message_update` also has a `usage` field with the token counts so far. The `assistantMessageEvent` holds the new piece (`rpcMessageUpdate` in `coding/cli/rpc_events.go`).

**2. The tool call.** This filter prints the record for the finished call:

```bash
jq -c 'select(.type == "tool_execution_end")' run.jsonl
```

```json
{"type":"tool_execution_end","toolCallId":"call_0","toolName":"bash","result":{"content":[{"type":"text","text":"out\n\n\nCommand exited with code 3"}],"structuredContent":{"output":"out\n","truncated":false,"exit_code":3,"wall_time_seconds":0},"isError":true},"isError":true,"durationMs":6}
```

`"isError":true` is how a consumer sees that the command failed. `structuredContent.exit_code` is `3`. `durationMs` is the time `bash` took to run (6 ms here; yours differs).

**3. The final answer.** Use the last assistant `message_end` and do not join the deltas (see [Streaming message updates](#streaming-message-updates)):

```bash
jq -r 'select(.type == "message_end" and .message.role == "assistant")
       | .message.content[] | select(.type == "text") | .text' run.jsonl
```

```text
The command printed out and exited with code 3.
```

The first assistant message has no text block, only the `toolCall`, so only one line prints. Its `stopReason` is `toolUse`. The last one's is `stop`.

**An unknown model name.** `pig --mode json --model mock/nope "hi"` prints `Warning: Model "nope" not found for provider "mock". Using custom model id.` on standard error. PiG then sends the request with the model ID `nope`, and the stream and exit status 0 follow as above. A typo in the model name therefore does not stop the run when the provider accepts any ID.

## Message shapes

Read [Session file format](/docs/latest/session-format) for message, content-block, usage, and Session entry shapes.

## Source references

PiG implementation:

- `coding/cli/print_mode.go` owns one-shot text and JSON mode.
- `coding/cli/rpc_events.go` converts internal events to wire records.

Pi reference (1.2.0):

- [Pi print/JSON mode](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/src/modes/print-mode.ts)
- [Pi RPC event types](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/src/modes/rpc/rpc-types.ts)

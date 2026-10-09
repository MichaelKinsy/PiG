# JSON event stream mode

JSON mode runs one prompt and writes its Session header and model-loop events as JSON Lines.

```bash
pig --mode json --model <provider/model> "Your prompt"
```

Use this mode when one process invocation handles one prompt. Use [RPC mode](/docs/latest/rpc) when a client must send several commands to a long-running PiG process.

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

Tool-call streaming uses `toolcall_start`, `toolcall_delta`, and `toolcall_end`. PiG assembles the tool ID, name, and arguments in the end record.

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

## Example

```bash
pig --mode json --model openai/gpt-5 "List files" \
  2>pig-json.stderr \
  | jq -c 'select(.type == "message_end")'
```

## Message shapes

Read [Session file format](/docs/latest/session-format) for message, content-block, usage, and Session entry shapes.

## Source references

PiG implementation:

- `coding/cli/print_mode.go` owns one-shot text and JSON mode.
- `coding/cli/rpc_events.go` converts internal events to wire records.

Upstream Pi reference:

- [Pi print/JSON mode](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/modes/print-mode.ts)
- [Pi RPC event types](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/modes/rpc/rpc-types.ts)

# JSON event stream mode

JSON mode runs one prompt and writes its Session header and model-loop events as JSON Lines.

```bash
pig --mode json --model <provider/model> "Your prompt"
```

Use this mode when one process invocation handles one prompt. Use [RPC mode](/docs/latest/rpc) when a client must send several commands to a long-running PiG process.

## Framing

Each output record is one JSON object followed by LF (`\n`). PiG writes diagnostics to standard error.

The first output line is the Session header. PiG writes it with `--no-session` too:

```json
{"type":"session","version":3,"id":"session-uuid","timestamp":"2026-01-15T14:00:00Z","cwd":"/work/project"}
```

Model-loop events follow as they occur.

## Events

JSON mode converts Session events with the same code as RPC mode. The model loop emits:

- `agent_start`;
- `agent_end` with `messages` and `willRetry`;
- `agent_settled`;
- `turn_start`;
- `turn_end` with the final assistant message and tool results;
- `message_start`;
- `message_update`;
- `message_end`;
- `tool_execution_start`;
- `tool_execution_update`;
- `tool_execution_end`.

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
{"type":"tool_execution_start","toolCallId":"call-1","toolName":"read","args":{"path":"README.md"}}
{"type":"tool_execution_update","toolCallId":"call-1","toolName":"read","args":{"path":"README.md"},"partialResult":{"content":[{"type":"text","text":"..."}],"isError":false}}
{"type":"tool_execution_end","toolCallId":"call-1","toolName":"read","result":{"content":[{"type":"text","text":"..."}],"isError":false},"isError":false}
```

## Completion and errors

PiG waits briefly for the terminal `agent_settled` event before it closes the stream. It does not print the final assistant text a second time.

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

- `cmd/pig/print_mode.go` owns one-shot text and JSON mode.
- `cmd/pig/rpc_events.go` converts internal events to wire records.

Upstream Pi reference:

- [Pi print/JSON mode](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/modes/print-mode.ts)
- [Pi RPC event types](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/modes/rpc/rpc-types.ts)

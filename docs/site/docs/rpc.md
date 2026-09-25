# RPC mode

RPC mode runs PiG as a headless JSON Lines process. A client writes commands to standard input and reads responses and events from standard output.

Use the [PiG Go SDK](/docs/latest/sdk) when your Go application does not need a process boundary. Use RPC mode for another language, an IDE, or a separate process.

## Start RPC mode

```bash
pig --mode rpc [--model <provider/model>] [options]
```

Common options:

- `--provider <name>` selects a provider when the model argument does not include one.
- `--model <provider/model>` selects the model.
- `--name <name>` or `-n <name>` sets the initial Session name.
- `--no-session` disables Session persistence.
- `--session-dir <path>` selects a Session directory.
- `--piglet <name-or-path>` selects one agent application.

PiG writes operational diagnostics to standard error. Treat standard output as protocol data only.

If you omit `--model` and no default model exists, RPC mode starts with Pi's
`unknown` model record. Use `get_state`, `get_available_models`, and `set_model`
to select a model. A prompt fails preflight until the selected provider has
configured authentication.

## Framing

Each input or output record is one JSON object followed by LF (`\n`).

- Split records on LF only.
- Remove a trailing CR when you send CRLF.
- Do not split JSON strings on Unicode line separators.
- Keep each input command within the 16 MiB command-reader limit.

Every command can contain an `id`. The corresponding response repeats it:

```json
{"id":"req-1","type":"get_state"}
```

```json
{"id":"req-1","type":"response","command":"get_state","success":true,"data":{}}
```

A response confirms command acceptance or reports a command error. Later model or tool failures arrive as events.

## Pi compatibility boundary

PiG implements the command union in the pinned Pi `rpc-types.ts`. The generated
test inventory fails when a pinned Pi command has no PiG dispatch branch.

Close standard input to shut down RPC mode. PiG cancels active prompts, Bash
commands, Session transitions, model-selection handlers, retries, and extension
UI requests before it closes the process.

Go programs can use `github.com/MichaelKinsy/PiG/coding/rpcclient`, the port of
Pi's TypeScript `RpcClient`. It starts `pig --mode rpc`, correlates responses,
exposes typed command methods, and delivers events to listeners. Other
languages use the JSONL protocol directly.

PiG reuses its host-scoped extension runner when it replaces a Session. Pi
invalidates the old per-Session runner. This difference is D30. Session lifecycle
events still run before and after each replacement.

## Events

PiG currently emits these model-loop events:

- `agent_start`;
- `agent_end` with `messages` and `willRetry`;
- `agent_settled`;
- `turn_start`;
- `turn_end` with the assistant message and tool results;
- `message_start`;
- `message_update`;
- `message_end`;
- `tool_execution_start`;
- `tool_execution_update`;
- `tool_execution_end`;
- `bash_execution_update`;
- `queue_update`;
- `thinking_level_changed`;
- `compaction_start` and `compaction_end`;
- automatic-retry and summarization-retry events;
- `entry_appended` after an extension persists a custom Session entry;
- `session_info_changed`;
- `extension_ui_request`;
- `extension_error`;
- `error`.

`message_update` contains an `assistantMessageEvent`. Text and tool-call streams use matching start, delta, and end records. Deltas do not contain cumulative partial messages.

Example text stream:

```json
{"type":"message_start","message":{"role":"assistant","content":[]}}
{"type":"message_update","assistantMessageEvent":{"type":"text_start","contentIndex":0}}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hello"}}
{"type":"message_update","assistantMessageEvent":{"type":"text_end","contentIndex":0,"content":"Hello"}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Hello"}]}}
```

Read message and Session entry shapes in [Session file format](/docs/latest/session-format).

## Minimal client

```python
import json
import subprocess

process = subprocess.Popen(
    ["pig", "--mode", "rpc", "--model", "openai/gpt-5", "--no-session"],
    stdin=subprocess.PIPE,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True,
    bufsize=1,
)

process.stdin.write(json.dumps({
    "id": "prompt-1",
    "type": "prompt",
    "message": "List the packages in this repository.",
}) + "\n")
process.stdin.flush()

for line in process.stdout:
    event = json.loads(line)
    print(event)
    if event.get("type") == "agent_settled":
        break

process.stdin.close()
```

Keep reading standard error separately. A full error pipe can block a child process.

## Reference

- [RPC commands](/docs/latest/rpc-commands) lists every command and its response.
- [RPC extension UI](/docs/latest/rpc-extension-ui) describes extension dialog requests and the responses a client returns.
- [JSON event stream](/docs/latest/json) describes the event records PiG also writes in JSON mode.
- [Session file format](/docs/latest/session-format) describes the entries that Session commands return.

## Source references

PiG implementation:

- `cmd/pig/rpc_mode.go` defines the command loop.
- `cmd/pig/rpc_types.go` defines command and response types.
- `cmd/pig/rpc_ui.go` defines the extension UI request and response transport.
- `cmd/pig/rpc_events.go` defines event conversion.
- `coding/rpcclient` defines the Go subprocess client.

Upstream Pi reference:

- [RPC mode](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/modes/rpc/rpc-mode.ts)
- [RPC types](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/src/modes/rpc/rpc-types.ts)

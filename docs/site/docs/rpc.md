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

The initial active tool list and system-message tool declarations retain extension load and registration order. When several extensions register the same tool name, the first registration wins.

## Framing

Each input or output record is one JSON object followed by LF (`\n`).

JSON and RPC mode share the event encoder. Event fields follow Pi's construction order rather than alphabetical order. The encoder writes `<`, `>`, `&`, and Unicode line separators as literal characters in JSON strings, including nested records. Parse records as JSON rather than relying on member positions.

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

A `compact` request aborts and joins the active turn before summarizing. It waits off the input loop, so the client can continue to send state queries and replies to extension dialogs.

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

`new_session`, `switch_session`, `fork` and `clone` replace the Session through the
same runtime factory that creates the first Session, as Pi's rpc-mode does with its
runtime host. The replacement gets a new extension runner, new extension processes
and services for the destination Session's working directory. The old runner is
invalidated. As in Pi 1.0.2, the replacement's extensions receive `session_start`
twice after these four commands, because rpc-mode rebinds once more after the
command returns. An extension process does not inherit process-wide state such as
environment variables from the replaced one; This is D70, which also covers `/reload`.

## Events

Partial-message observation across the process boundary is a documented 0.3.x known gap (D82, owner decision 2026-09-28). PiG sends snapshots rather than live producer references. An initial assistant message can therefore contain less content or an earlier stop state than Pi's shallow/live observation. Intermediate Completions snapshots omit `partialArgs` and `streamIndex`; Responses snapshots omit `partialJson`. Final messages must omit these parser properties in both hosts. Event order, deltas, terminal results and persisted messages are not part of this allowance. The strict RPC33 comparison retains the complete raw difference; it is not passing parity.

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

An assistant `message_start` can arrive after successful provider response headers, before any response body data arrives. OpenAI Completions and Responses return their Event Stream before HTTP setup settles and admit start before reading body data. A later cancellation or stream failure still terminates the assistant message; start does not guarantee a successful response.

Partial messages are observed when the event is encoded, not when the provider originally pushes it. A `message_start` can already contain buffered text or a tool call. Intermediate blocks can include the provider's parser fields, as Pi's do: OpenAI Completions tool calls carry `partialArgs`, `customInput` or `streamIndex`; OpenAI Responses and Codex tool calls carry `partialJson`; Anthropic Messages and Bedrock Converse Stream open blocks carry `index`, and their tool calls `partialJson`; Mistral tool calls carry `partialArgs`. Final messages remove those fields. Persist the finalized message or Session entry rather than an intermediate partial.

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

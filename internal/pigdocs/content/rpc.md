# RPC mode

RPC mode runs PiG as a headless JSON Lines process. A client writes commands to standard input and reads responses and events from standard output. The protocol is the RPC protocol of Pi 1.4.0 ([Pi's RPC protocol reference](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/docs/rpc.md)), apart from the differences named on this page.

Use the [PiG Go SDK](sdk.md) when your Go application does not need a process boundary. Use RPC mode for another language, an IDE, or a separate process.

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

If you omit `--model` and no default model exists, RPC mode starts with Pi's `unknown` model record. Use `get_state`, `get_available_models`, and `set_model` to select a model. A prompt fails preflight until the selected provider has configured authentication. A Session resumed without credentials for its saved model also starts on the `unknown` model, as in Pi.

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

A `compact` request aborts and joins the active turn before summarizing. It waits off the input loop, so the client can continue to send state queries and replies to extension dialogs. Several `compact` commands of one input batch run one after another. Pi 1.4.0 starts them at the same time (D147). A Session replacement in the same batch aborts a running compaction, which then answers one error (D148).

## Pi compatibility boundary

PiG implements the command union in the pinned Pi `rpc-types.ts`. The generated test inventory fails when a pinned Pi command has no PiG dispatch branch. PiG adds one command, `get_extensions` (D101). See [RPC commands](rpc-commands.md#loaded-extensions).

Close standard input to shut down RPC mode. PiG cancels active prompts, Bash commands, Session transitions, model-selection handlers, retries, and extension UI requests before it closes the process. When standard input ends while `new_session`, `fork`, `clone` or `switch_session` is still replacing the Session, PiG finishes the replacement and writes its response before it exits. Pi 1.4.0 exits without answering that command (D146). A suspended extension command's continuation can run before its runtime stops (D85), and a settle-tail handler of a runtime that cannot report its wait holds standard input open until it finishes (D86).

Go programs can use `github.com/MichaelKinsy/PiG/coding/rpcclient`, the port of Pi's TypeScript `RpcClient`. It starts `pig --mode rpc`, correlates responses, exposes typed command methods, and delivers events to listeners. Other languages use the JSONL protocol directly.

`new_session`, `switch_session`, `fork` and `clone` replace the Session through the same runtime factory that creates the first Session, as Pi's rpc-mode does with its runtime host. The replacement gets a new extension runner, new extension processes and services for the destination Session's working directory. The old runner is invalidated. As in Pi 1.4.0, the replacement's extensions receive `session_start` twice after these four commands, because rpc-mode rebinds once more after the command returns. An extension process does not inherit process-wide state such as environment variables from the replaced one. This is D70, which also covers `/reload`.

## Events

Partial-message observation across the process boundary is a known gap (D82, owner decision 2026-09-28). PiG sends snapshots rather than live producer references. An initial assistant message can therefore contain less content or an earlier stop state than Pi's shallow/live observation. Intermediate Completions snapshots omit `partialArgs` and `streamIndex`; Responses snapshots omit `partialJson`. Final messages must omit these parser properties in both hosts. Event order, deltas, terminal results and persisted messages are not part of this allowance. The strict RPC33 comparison retains the complete raw difference; it is not passing parity.

PiG emits these events:

- `agent_start`;
- `agent_end` with `messages` and `willRetry`;
- `agent_settled` with `aborted`, which is `true` when the run ended because it was aborted;
- `turn_start`;
- `turn_end` with the assistant message and tool results;
- `message_start`;
- `message_update`;
- `message_end`;
- `tool_execution_start`;
- `tool_execution_update`;
- `tool_execution_end` with `durationMs` when the tool ran;
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

Read message and Session entry shapes in [Session file format](session-format.md).

## Worked example

This example drives `pig --mode rpc` from a shell with no client library. It sends two commands, waits for the run to finish, then sends three more, one of them invalid, and ends with a line that is not JSON. The provider is a scripted test server that always answers `Hello from the test model.`, so the reply wording is illustrative. The records, their order and the error text are what PiG printed. Pi 1.4.0 printed the same record types, the same order and the same parse error for the same input.

```bash
(
  printf '%s\n' '{"id":"1","type":"get_state"}' \
                '{"id":"2","type":"prompt","message":"Say hello"}'
  sleep 3                       # let the run finish
  printf '%s\n' '{"id":"3","type":"get_last_assistant_text"}' \
                '{"id":"4","type":"get_session_stats"}' \
                '{"id":"5","type":"bogus"}' \
                'not json'
  sleep 1
) | pig --mode rpc --no-session --model mock/mock-1 2>rpc.stderr > rpc.jsonl
echo "exit status: $?"
```

```text
exit status: 0
```

When standard input closes, PiG ends the process with status 0. `rpc.stderr` is empty. The `sleep 3` is only for this shell script. A real client waits for the `agent_settled` event instead.

**The order of records.** This filter prints the type, `id`, command and outcome of each line:

```bash
jq -c '[.type, .id, .command, .success]' rpc.jsonl
```

```text
["response","1","get_state",true]
["response","2","prompt",true]
["agent_start",null,null,null]
["turn_start",null,null,null]
["message_start",null,null,null]
["message_end",null,null,null]
["message_start",null,null,null]
["message_end",null,null,null]
["message_start",null,null,null]
["message_update",null,null,null]
["message_update",null,null,null]
["message_update",null,null,null]
["message_end",null,null,null]
["turn_end",null,null,null]
["agent_end",null,null,null]
["agent_settled",null,null,null]
["response",null,"parse",false]
["response","3","get_last_assistant_text",true]
["response","4","get_session_stats",true]
["response","5","bogus",false]
```

Three things to notice:

- The response to `prompt` arrives **before** the model has answered. `"data":{"disposition":"started"}` means PiG accepted the prompt. The answer arrives as events, and `agent_settled` is the last one. A failure of the model call is an event, not a failed response (see [Framing](#framing)).
- Events carry no `id`. Match responses to commands by `id`, and treat every record without `"type":"response"` as an event.
- The response to the line that was not JSON (`"command":"parse"`, no `id`, because there was none to repeat) arrived **before** the responses to commands 3 to 5, which were sent earlier in the same burst. Do not rely on responses arriving in send order when you send several commands at once. Match them by `id`.

**The state.** `get_state` before any prompt:

```json
{"id":"1","type":"response","command":"get_state","success":true,"data":{"model":{"id":"mock-1","name":"Mock","api":"openai-completions","provider":"mock","baseUrl":"http://127.0.0.1:18931/v1","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096},"thinkingLevel":"off","isStreaming":false,"isCompacting":false,"steeringMode":"one-at-a-time","followUpMode":"one-at-a-time","sessionId":"01a12c60-0fd7-71bc-8a36-b557333c8aeb","autoCompactionEnabled":true,"messageCount":0,"pendingMessageCount":0}}
```

**The answer and the totals.**

```json
{"id":"3","type":"response","command":"get_last_assistant_text","success":true,"data":{"text":"Hello from the test model."}}
{"id":"4","type":"response","command":"get_session_stats","success":true,"data":{"sessionId":"01a12c60-0fd7-71bc-8a36-b557333c8aeb","userMessages":1,"assistantMessages":1,"toolCalls":0,"toolResults":0,"totalMessages":3,"tokens":{"input":100,"output":20,"cacheRead":0,"cacheWrite":0,"total":120},"cost":0,"contextUsage":{"tokens":120,"contextWindow":128000,"percent":0.09375}}}
```

`totalMessages` is 3: the system message, your prompt and the reply. The token counts are the test server's.

**Errors.** An unknown command and a line that is not JSON each get a `success:false` response and the process keeps running:

```json
{"id":"5","type":"response","command":"bogus","success":false,"error":"Unknown command: bogus"}
{"type":"response","command":"parse","success":false,"error":"Failed to parse command: Unexpected token 'o', \"not json\" is not valid JSON"}
```

The parse message is the text a JavaScript JSON parser gives, as in Pi. Parse `error` as free text, not as a stable code.

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

- [RPC commands](rpc-commands.md) lists every command and its response.
- [RPC extension UI](rpc-extension-ui.md) describes extension dialog requests and the responses a client returns.
- [JSON event stream](json.md) describes the event records PiG also writes in JSON mode.
- [Session file format](session-format.md) describes the entries that Session commands return.

## Source references

PiG implementation:

- `coding/cli/rpc_mode.go` defines the command loop.
- `coding/cli/rpc_types.go` defines command and response types.
- `coding/cli/rpc_ui.go` defines the extension UI request and response transport.
- `coding/cli/rpc_events.go` defines event conversion.
- `coding/rpcclient` defines the Go subprocess client.

Pi reference (1.2.0):

- [RPC mode](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/src/modes/rpc/rpc-mode.ts)
- [RPC types](https://github.com/earendil-works/pi/blob/v1.2.0/packages/coding-agent/src/modes/rpc/rpc-types.ts)

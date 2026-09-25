# RPC commands

This page lists the commands an RPC client writes to PiG on standard input, with example records and their responses. [RPC mode](/docs/latest/rpc) describes how to start the process and frame records.

## Supported commands

| Command | Required fields | Purpose |
|---|---|---|
| `prompt` | `message` | Accept a prompt, queue it with `streamingBehavior`, or run a registered extension command. |
| `steer` | `message` | Queue a steering message in the active turn. |
| `follow_up` | `message` | Queue a message after the active turn settles. |
| `abort` | none | Cancel the current prompt and wait for it to settle. |
| `clear_queue` | none | Remove queued steering and follow-up messages and return their text. |
| `new_session` | optional `parentSession` | Replace the active Session with a fresh Session. |
| `get_commands` | none | List extension, prompt-template, and Skill commands. |
| `get_state` | none | Read current Session, queue, and model state. |
| `set_model` | `provider`, `modelId` | Select an available model. |
| `cycle_model` | none | Select the next available or scoped model. |
| `get_available_models` | none | List available full model records. |
| `set_thinking_level` | `level` | Set the effective thinking level. |
| `cycle_thinking_level` | none | Select the next supported level. |
| `get_available_thinking_levels` | none | List levels supported by the current model. |
| `set_steering_mode` | `mode` | Set and persist `all` or `one-at-a-time`. |
| `set_follow_up_mode` | `mode` | Set and persist `all` or `one-at-a-time`. |
| `compact` | none | Run manual compaction and return its result. |
| `set_auto_compaction` | `enabled` | Persist automatic-compaction state. |
| `set_auto_retry` | `enabled` | Set automatic retry for this Session. |
| `abort_retry` | none | Cancel the active retry delay. |
| `bash` | `command` | Run a shell command and stream output records. |
| `abort_bash` | none | Cancel active RPC Bash commands. |
| `get_session_stats` | none | Read Session statistics. |
| `export_html` | none | Export the Session to HTML. |
| `switch_session` | `sessionPath` | Replace the active Session from a file. |
| `fork` | `entryId` | Fork before a user message and return its text. |
| `clone` | none | Clone at the active leaf and switch to the clone. |
| `get_fork_messages` | none | List user messages that can identify a fork point. |
| `get_entries` | optional `since` | Read Session entries after an entry ID. |
| `get_tree` | none | Read the Session entry tree and active leaf. |
| `get_last_assistant_text` | none | Read the last assistant text, when present. |
| `set_session_name` | `name` | Set the current Session name. |
| `get_messages` | none | Read current Agent messages. |

Unknown command types return `success:false`.

## Prompt

```json
{"id":"prompt-1","type":"prompt","message":"Find the test entry points"}
```

PiG acknowledges the prompt after extension input handling and model authentication
preflight succeed. The response arrives before model events finish:

```json
{"id":"prompt-1","type":"response","command":"prompt","success":true}
```

`metadata` is an optional JSON object:

```json
{"id":"prompt-2","type":"prompt","message":"Review this change","metadata":{"task_id":"task-42"}}
```

RPC prompt input accepts text and optional images. If the Agent is already
running, set `streamingBehavior` to `steer` or `followUp`. PiG rejects a second
prompt without this field.

A registered extension command such as `/review strict` runs immediately through the `prompt` command. Prompt templates and `/skill:<name>` expand before model submission.

## Abort

```json
{"id":"abort-1","type":"abort"}
```

PiG cancels the current prompt context and acknowledges the command:

```json
{"id":"abort-1","type":"response","command":"abort","success":true}
```

## State

```json
{"id":"state-1","type":"get_state"}
```

The response data contains:

```json
{
  "sessionId":"...",
  "sessionFile":"...",
  "sessionName":"...",
  "model":{"id":"gpt-5","provider":"openai"},
  "thinkingLevel":"medium",
  "steeringMode":"all",
  "followUpMode":"one-at-a-time",
  "isStreaming":false,
  "isCompacting":false,
  "autoCompactionEnabled":true,
  "messageCount":5,
  "pendingMessageCount":0
}
```

`sessionFile` and `sessionName` can be absent.

## Select a model

```json
{"id":"model-1","type":"set_model","provider":"openai","modelId":"gpt-5"}
```

A successful response contains the full selected model record.

List available models:

```json
{"id":"models-1","type":"get_available_models"}
```

The result is a `models` array of full Pi model records.

## Thinking level

```json
{"type":"set_thinking_level","level":"high"}
```

Accepted values are `off`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`. Model capabilities can clamp the effective level.

Cycle the configured level:

```json
{"type":"cycle_thinking_level"}
```

The response contains the next `level`.

## Compaction and retry

Start compaction:

```json
{"id":"compact-1","type":"compact","customInstructions":"Keep file names and unresolved errors."}
```

The response arrives after compaction finishes. It contains `summary`,
`firstKeptEntryId`, `tokensBefore`, and `estimatedTokensAfter`. It can also
contain `usage` and `details`. A compaction failure returns `success:false`.

Set automatic compaction:

```json
{"type":"set_auto_compaction","enabled":true}
```

Set automatic retry for the current Session:

```json
{"type":"set_auto_retry","enabled":true}
```

## Steer and follow up

```json
{"type":"steer","message":"Stop and inspect the parser first."}
```

PiG queues this message after the current tool batch and before the next model
call.

```json
{"type":"follow_up","message":"Then update the documentation."}
```

PiG queues this message after the active turn settles. `queue_update` events
report both queues when a message enters or leaves them.

## Clear the queue

```json
{"id":"clear-1","type":"clear_queue"}
```

PiG removes every queued steering and follow-up message. It writes a `queue_update` event with both queues empty, and then the response. The response data contains the text of the removed messages:

```json
{"id":"clear-1","type":"response","command":"clear_queue","success":true,"data":{"steering":["Stop and inspect the parser first."],"followUp":["Then update the documentation."]}}
```

## Bash

```json
{"id":"bash-1","type":"bash","command":"go test ./...","excludeFromContext":false}
```

The final response data contains `output`, `exitCode`, `cancelled`, `truncated`,
and optional `fullOutputPath`.

Cancel active Bash work:

```json
{"type":"abort_bash"}
```

## Session data

Get current Agent messages:

```json
{"type":"get_messages"}
```

Get entries after a known entry:

```json
{"type":"get_entries","since":"a1b2c3d4"}
```

The response contains `entries` and `leafId`. An unknown `since` ID returns an error.

Get the complete tree:

```json
{"type":"get_tree"}
```

Get the last assistant text:

```json
{"type":"get_last_assistant_text"}
```

The response data is empty when no assistant text exists.

Set a Session name:

```json
{"id":"name-1","type":"set_session_name","name":"Review authentication"}
```

PiG writes `session_info_changed` before the success response.

An extension can also change Session metadata. `appendEntry` persists a custom
entry and then writes `entry_appended`. `setSessionName` writes
`session_info_changed`. An empty extension-defined name clears the Session name,
and the event omits `name`.

## Session replacement and export

Start a new Session:

```json
{"type":"new_session","parentSession":"/optional/parent.jsonl"}
```

Switch to an existing Session:

```json
{"type":"switch_session","sessionPath":"/path/to/session.jsonl"}
```

PiG keeps the startup project Resources when the target Session uses a different
working directory. Direct RPC Bash uses the target working directory. Built-in
tools, project settings, context files, prompts, skills, themes, and the system
prompt still use the startup project. See [D61](https://github.com/MichaelKinsy/PiG/blob/main/DIVERGENCES.md#d61-session-replacement-keeps-startup-project-services-and-resources).

Fork before a user message:

```json
{"type":"fork","entryId":"a1b2c3d4"}
```

The fork response contains the selected user text. `clone` forks at the active
leaf instead:

```json
{"type":"clone"}
```

Each replacement can return `data.cancelled:true` when an extension cancels its
`session_before_switch` or `session_before_fork` event.

Export the current persisted Session:

```json
{"type":"export_html","outputPath":"/tmp/session.html"}
```

Omit `outputPath` to use the default export path.

## Command catalogue

```json
{"type":"get_commands"}
```

Each result contains:

- `name`;
- optional `description`;
- `source`, such as `extension`, `prompt`, or `skill`;
- `sourceInfo` with `path`, `source`, `scope`, `origin`, and optional `baseDir`.

Built-in interactive slash commands are not RPC commands.


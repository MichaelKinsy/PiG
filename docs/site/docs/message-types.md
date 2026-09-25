# Message types

PiG uses one message format in session files, in JSON and RPC events, and in the Go SDK. This page defines the messages and the content blocks inside them. The JSON field names match Pi 0.87.1.

Every message has a `role` field that names its type. A message `timestamp` is a Unix time in milliseconds. Session entries use ISO 8601 timestamps instead. See [session file format](/docs/latest/session-format).

The Go definitions live in two packages:

- `github.com/MichaelKinsy/PiG/ai` defines the content blocks, `Usage`, and the provider-facing messages.
- `github.com/MichaelKinsy/PiG/agent` defines `AgentMessage`, the message type that sessions and events carry.

## Content blocks

A content block has a `type` field. PiG knows four block types.

### Text

```json
{"type":"text","text":"The tests pass."}
```

| Field | Type | Meaning |
|---|---|---|
| `text` | string | The text. |
| `textSignature` | string, optional | Provider metadata. Pass it through unchanged. |

Go type: `ai.TextContent`.

### Image

```json
{"type":"image","data":"iVBORw0KGgo...","mimeType":"image/png"}
```

| Field | Type | Meaning |
|---|---|---|
| `data` | string | The image bytes, base64-encoded. |
| `mimeType` | string | The media type, such as `image/png` or `image/jpeg`. |

Go type: `ai.ImageContent`.

### Thinking

```json
{"type":"thinking","thinking":"The failing test reads a stale fixture.","thinkingSignature":"..."}
```

| Field | Type | Meaning |
|---|---|---|
| `thinking` | string | The model's visible reasoning. |
| `thinkingSignature` | string, optional | Provider data that lets PiG send the block back later. Pass it through unchanged. |
| `redacted` | boolean, optional | `true` when the provider hid the reasoning. The text can then be empty while the signature holds encrypted data. |

Go type: `ai.ThinkingContent`.

### Tool call

```json
{"type":"toolCall","id":"call_1","name":"read","arguments":{"path":"README.md"}}
```

| Field | Type | Meaning |
|---|---|---|
| `id` | string | The call ID. The matching tool result repeats it. |
| `name` | string | The tool name. |
| `arguments` | object | The tool arguments. |
| `thoughtSignature` | string, optional | Provider data. Pass it through unchanged. |
| `namespace` | string, optional | The OpenAI Responses namespace of a namespaced tool. |

Go type: `ai.ToolCall`.

## Usage

Assistant messages always carry `usage`. A tool result carries `usage` only when the tool made its own model calls.

```json
{"input":1200,"output":340,"cacheRead":800,"cacheWrite":0,"totalTokens":2340,"cost":{"input":0.0012,"output":0.0034,"cacheRead":0.0002,"cacheWrite":0,"total":0.0048}}
```

| Field | Type | Meaning |
|---|---|---|
| `input` | number | Input tokens. |
| `output` | number | Output tokens. |
| `cacheRead` | number | Tokens read from the prompt cache. |
| `cacheWrite` | number | Tokens written to the prompt cache. |
| `cacheWrite1h` | number, optional | The part of `cacheWrite` stored with one-hour retention. |
| `reasoning` | number, optional | Reasoning tokens. They are already part of `output`, so do not add them again. |
| `totalTokens` | number | Total tokens. |
| `cost` | object | Cost of `input`, `output`, `cacheRead` and `cacheWrite`, and the `total`. |

Tool-result usage counts toward session statistics. It is not part of the usage of the main model call.

Go type: `ai.Usage`.

## Model messages

These four roles are the messages a provider understands.

### System message

A system message sets the instructions and the tool set from its position in the transcript onward.

```json
{"role":"system","content":"","sections":{"preamble":"You are an expert coding assistant..."},"timestamp":1768485600000}
```

| Field | Type | Meaning |
|---|---|---|
| `content` | string or text blocks | Instruction text. |
| `sections` | object, optional | Named prompt sections in order. A later message replaces a section by name. A `null` value removes the section. |
| `toolsAdded` | array, optional | Full definitions of tools that become available here. |
| `toolsRemoved` | array, optional | `{"name": ...}` references to tools that stop being available here. |

The first system message declares the starting prompt and tools. Replay the later ones in order to get the current state.

Pi's `message-types.md` also lists a `replace` field. The Pi 0.87.1 source does not define that field, and PiG does not read or write it.

Go type: `ai.SystemMessage`.

### User message

```json
{"role":"user","content":[{"type":"text","text":"Fix the failing test."}],"timestamp":1768485601000}
```

`content` holds text and image blocks. PiG also accepts a plain string when it reads a message, and it turns the string into one text block. It always writes an array.

Go type: `agent.UserMessage`, or `ai.UserMessage` for a provider request.

### Assistant message

```json
{"role":"assistant","content":[{"type":"text","text":"Done."}],"api":"openai-responses","provider":"openai","model":"gpt-5","usage":{"input":1200,"output":340,"cacheRead":0,"cacheWrite":0,"totalTokens":1540,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1768485605000}
```

| Field | Type | Meaning |
|---|---|---|
| `content` | array | Text, thinking and tool-call blocks. |
| `api` | string | The provider API that produced the message. |
| `provider` | string | The provider name. |
| `model` | string | The requested model ID. |
| `responseModel` | string, optional | The model the provider reports, when it differs from `model`. |
| `responseId` | string, optional | The provider's response ID. |
| `providerThinkingLevel` | string, optional | The thinking level the provider applied. |
| `diagnostics` | array, optional | Runtime diagnostics, each with `type`, `timestamp`, and an optional `error` and `details`. |
| `usage` | object | See [Usage](#usage). |
| `stopReason` | string | Why the response ended. See below. |
| `deferred` | object, optional | A handle for a response that the provider finishes later. |
| `errorMessage` | string, optional | The error text when `stopReason` is `error` or `aborted`. |
| `rawStopReason` | string, optional | The provider's own stop reason. |
| `endTurn` | boolean, optional | Whether the provider marked the end of its turn. |

`stopReason` is one of these values:

| Value | Meaning |
|---|---|
| `pending` | The message is still streaming. |
| `stop` | The model finished. |
| `length` | The model reached its output limit. |
| `toolUse` | The model called one or more tools. |
| `error` | The request failed. |
| `aborted` | The run was stopped. |
| `deferred` | The provider will finish the response later. |

A `deferred` handle has `provider`, `modelId`, `api` and `id`, and optionally `expiresAt`, `pollAfterMs` and `data`.

Go type: `agent.AssistantMessage`, or `ai.AssistantMessage` for a provider request.

### Tool result

```json
{"role":"toolResult","toolCallId":"call_1","toolName":"read","content":[{"type":"text","text":"# Project"}],"isError":false,"timestamp":1768485603000}
```

| Field | Type | Meaning |
|---|---|---|
| `toolCallId` | string | The `id` of the tool call. |
| `toolName` | string | The tool name. |
| `content` | array | Text and image blocks that the model sees. |
| `details` | any, optional | Tool-specific data for renderers and extensions. The model does not see it. |
| `usage` | object, optional | Model usage of the tool itself. |
| `isError` | boolean | Whether the tool failed. |

Go type: `agent.ToolResultMessage`.

## Coding agent messages

PiG adds four roles for the coding agent. Before a model request, PiG turns each of them into user-role text, with the exceptions that this section names.

### Shell execution

PiG records a shell command that you run with `!` or `!!`, or through the RPC `bash` command. It is not a tool result.

```json
{"role":"bashExecution","command":"go test ./...","output":"ok ...","exitCode":0,"cancelled":false,"truncated":false,"timestamp":1768485610000}
```

| Field | Type | Meaning |
|---|---|---|
| `command` | string | The command as typed. |
| `output` | string | The combined output. |
| `exitCode` | number | The exit status. |
| `cancelled` | boolean | Whether the command was stopped. |
| `truncated` | boolean | Whether PiG shortened the output. |
| `fullOutputPath` | string, optional | The file that holds the full output when PiG shortened it. |
| `excludeFromContext` | boolean, optional | `true` for `!!` commands. PiG does not send them to the model. |

### Custom message

An extension creates a custom message when it sends a message into the session.

```json
{"role":"custom","customType":"review-note","content":"Check the error path.","display":true,"timestamp":1768485620000}
```

| Field | Type | Meaning |
|---|---|---|
| `customType` | string | A name that the extension chooses. |
| `content` | string or array | The text, or text and image blocks. |
| `display` | boolean | Whether the terminal UI shows the message. |
| `details` | any, optional | Extension data. The model does not see it. |

PiG sends a custom message to the model when its `content` is a string. Pi also sends a custom message whose `content` is an array of blocks. PiG currently leaves such a message out of the model request.

### Branch summary

```json
{"role":"branchSummary","summary":"Tried a cache fix; it did not help.","fromId":"e5f6a7b8","timestamp":1768485630000}
```

PiG builds this message from a `branch_summary` session entry. `fromId` is the entry the branch started from, or `null`.

### Compaction summary

```json
{"role":"compactionSummary","summary":"## Goal\n...","tokensBefore":50000,"timestamp":1768485640000}
```

PiG builds this message from a `compaction` session entry. `tokensBefore` is the context size before compaction. See [compaction](/docs/latest/compaction).

## AgentMessage in Go

`agent.AgentMessage` holds exactly one message. It has one pointer field per model role and a map for every other role:

| Field | Role |
|---|---|
| `System *ai.SystemMessage` | `system` |
| `User *agent.UserMessage` | `user` |
| `Assistant *agent.AssistantMessage` | `assistant` |
| `ToolResult *agent.ToolResultMessage` | `toolResult` |
| `Custom map[string]any` | `bashExecution`, `custom`, `branchSummary`, `compactionSummary`, and any other role |

`Role()` returns the role string. `MarshalJSON` and `UnmarshalJSON` read and write the flat JSON shapes on this page. A message with an unknown role decodes into `Custom`, so code that reads messages from an extended host keeps working. The constants `agent.RoleBashExecution`, `agent.RoleCustom`, `agent.RoleBranchSummary` and `agent.RoleCompactionSummary` name the coding agent roles.

## Related

- [Session file format](/docs/latest/session-format) shows how messages sit inside session entries.
- [JSON event stream mode](/docs/latest/json) and [RPC mode](/docs/latest/rpc) carry these messages in events.
- [Go SDK](/docs/latest/sdk) reads and sends these types.

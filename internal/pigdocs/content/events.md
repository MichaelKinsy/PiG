# Events and hooks

Pig-native extensions react to **events**. A separately selected extension can
translate another harness's hook format into those events. Stock Pig does not
load a hook bridge.

## Register Pig-native events in an extension

A Go factory extension registers event handlers on the SDK extension object:

```go
func Extension() *sdk.Extension {
    ext := sdk.New("demo")

    ext.OnSessionStart(func(ctx sdk.Context, data map[string]any) (any, error) {
        ctx.SetStatus("demo", "demo extension loaded")
        return nil, nil
    })

    ext.OnToolResult(func(ctx sdk.Context, data map[string]any) (any, error) {
        ctx.SetStatus("demo", "a tool finished")
        return nil, nil
    })

    return ext
}
```

Validate the extension after changing event code:

```bash
pig install ./my-ext --validate-only --json
```

## Event names are the extension API surface

The Go SDK exposes helpers for common events:

| SDK helper | Event name |
| --- | --- |
| `OnSessionStart` | `session_start` |
| `OnSessionShutdown` | `session_shutdown` |
| `OnToolResult` | `tool_result` |
| `OnEvent("session_info_changed", handler)` | `session_info_changed` |
| `OnEvent("session_before_compact", handler)` | `session_before_compact` |
| `OnEvent("session_compact", handler)` | `session_compact` |
| `OnEvent(name, handler)` | any named event |

The lower-level event bridge also emits agent, turn, message, tool-execution,
input, model-selection, and session lifecycle events. The
`session_info_changed` event contains the effective Session name. Its `name`
field is absent when an extension clears the name. The
`session_before_compact` event contains the preparation, branch entries,
reason, retry state, custom instructions, and cancellation context. A handler
can cancel compaction or provide the compaction result. PiG persists a provided
result before it emits `session_compact`. The `session_compact` event contains
the persisted entry and identifies an extension-provided result. Use the SDK
helper when one exists. Use `OnEvent` only when you need a named event without
a helper.

## Hooks are external Resources

Some harnesses use declarative command triggers called hooks. A Package can
distribute hook metadata, and a Piglet can select an extension that translates
that metadata into Pig events. Stock Pig does not interpret cross-harness hook
files by itself.

Use SDK events when you write a Pig extension and need direct access to the SDK
context, host calls, and cancellation. Use a selected bridge extension when a
composition requires another harness's hook format.

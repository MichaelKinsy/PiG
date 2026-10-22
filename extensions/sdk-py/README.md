# pig-sdk (Python)

Python SDK for building pig subprocess extensions.

This SDK is a **language bridge** into the same extension API that upstream
pi exposes to TypeScript extensions: the wire protocol, register payload,
tool/command/event semantics, request/response and host-call shapes are
identical to the Go SDK (`extensions/sdk`) and the Rust SDK (`extensions/sdk-rs`).
See [`docs/extension-api-parity.md`](../../docs/extension-api-parity.md) for the host API parity matrix and
[`docs/extension-runtime-cells.md`](../../docs/extension-runtime-cells.md) for how factory extensions are packed
into generated runner subprocesses.

## Breaking changes in 0.3.0

Host-backed `Context` getters raise `HostCallError` on a host failure or a reply that lacks its field, and never return an empty value. `get_session_name`, `get_session_file` and `get_leaf_id` return `None` when absent, and `get_branch` and `get_entries` raise a failed session-log subscription. `timeout` options accept a float. See the migration tables in `docs/site/docs/extensions.md`.

## Status

```text
maturity: production bridge
runtime:  subprocess (no in-process, no WASM)
wire:      current Pig subprocess contract (no independent version)
host APIs implemented:
  full host-call bridge matching the Go SDK method set
declaration kinds implemented:
  tools
  commands
  event handlers
  shortcuts
  flags
  providers
  message renderers
  widgets / widget_push
```

The Python SDK is a first-class bridge into the same subprocess protocol as
Go and Rust. When a new host API is added, update all SDKs and the parity
matrix in the same change.

## Pi helper exports

The helpers Pi exports to extensions keep their names in snake case and behave like Pi's, which the conformance suite checks against Pi's own code:

```python
import os
import pathlib

import pig_sdk


def summarize(text: str) -> str:
    # Pi's truncateTail keeps the end of command output; the limits default to Pi's 2000 lines and 50KB.
    output = pig_sdk.truncate_tail(text)
    if not output.truncated:
        return output.content
    return f"{output.content}\n[showing {pig_sdk.format_size(output.output_bytes)} of {pig_sdk.format_size(output.total_bytes)}]"


def save_note(text: str) -> None:
    # Pi's withFileMutationQueue: parallel tool calls on one file run one at a time, in call order.
    notes = os.path.join(pig_sdk.get_agent_dir(), "notes.txt")
    pig_sdk.with_file_mutation_queue(notes, lambda: pathlib.Path(notes).write_text(text))
```

`is_tool_call_event_type(name, event)` is Pi's `isToolCallEventType` (the tool name must match exactly). `get_package_dir()` and `get_shell_config(custom_shell_path=None)` are Pi's `getPackageDir()` and `getShellConfig()`. Pi's pure pi-ai helpers are in `pig_sdk` too (`string_enum`, `clamp_thinking_level`, `get_supported_thinking_levels`, `calculate_cost`, `content_text`, `normalize_context`, `get_current_system_prompt`, `is_context_overflow`, `is_retryable_assistant_error`, `env_api_key_auth`, `create_assistant_message_event_stream` and the rest of `pi_ai_helpers.py`), over the dicts the wire carries. `ctx.set_working_message("")` shows an empty working label and `ctx.set_working_message(None)` restores the default one, as in Pi; `set_hidden_thinking_label` follows the same rule. `truncate_head`, `truncate_tail`, `truncate_line` and `format_size` are Pi's `truncateHead`, `truncateTail`, `truncateLine` and `formatSize`; byte limits count UTF-8 bytes and `truncate_line` counts UTF-16 units like a JavaScript string. `get_agent_dir`, `get_config_dir_name` and `get_version` are Pi's `getAgentDir()`, `CONFIG_DIR_NAME` and `VERSION`; they raise `HostInfoUnavailable`, never return a made-up path, when the process was not started by a PiG host. `ctx.get_editor_component()` returns the factory this extension installed with `ctx.set_editor_component`, or `None`. Pi's `getEditorComponent` returns the factory installed by any extension, so one extension can wrap another's editor; PiG returns only the calling extension's own factory.

`ctx.set_status(key, "")` sets an empty status text, as Pi's `setStatus(key, "")` does (RPC mode writes it); `ctx.clear_status(key)` is Pi's `setStatus(key, undefined)`. `ctx.input(title, placeholder=None)` and `ctx.editor(title, prefill=None)` leave the placeholder or prefill unset (RPC mode writes no such member); an empty string is written as `""`.

## Authoring a factory extension

```python
import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("my-py-ext")

    def hello(ctx: pig_sdk.Context, args: dict) -> dict:
        ctx.notify(f"hello, {args.get('name', 'world')}")
        return {"content": "ok"}

    ext.tool("hello", "Say hello", {"type": "object"}, hello)
    return ext


if __name__ == "__main__":
    new_extension().run()
```

Optional packing override:

```yaml
kind: Extension
metadata:
  name: my-py-ext
spec:
  runtime:
    language: python
    isolation: shared-ok
    entrypoint:
      mode: factory
      package: my_py_ext
      factory: new_extension
```

Two or more factory-style Python extensions with `isolation: shared-ok`
are automatically packed into a single generated runner subprocess by
`PlanCells` (see [`docs/extension-runtime-cells.md`](../../docs/extension-runtime-cells.md)).

## The upstream 0.99.1 extension API

`Extension` and `Context` carry the extension API that upstream 0.99.1 added. Names follow the SDK convention (`registerMcpServer` is `register_mcp_server`); dicts keep upstream's key names (`extensionPath`, `thinkingLevel`, `structuredContent` of an outcome).

| upstream | Python |
|---|---|
| `registerMcpServer`, `unregisterMcpServer` | `register_mcp_server(name, config)`, `unregister_mcp_server(name)` on `Extension` and `Context`. The host validates the config and the name's owner and raises `HostCallError`. |
| `getMcpServers` | `ctx.get_mcp_servers()`, answered from the state the host replicates and from the replies of registration calls, applied in the order the host sent them. |
| `registerVirtualModel`, `unregisterVirtualModel` | `register_virtual_model(VirtualModel(provider=..., id=..., name=..., route=route))`, `unregister_virtual_model(provider, id)`. `route(ctx, request)` returns `{"model": {"provider", "id"}, "thinkingLevel", "state"?}`; a `state` that is absent or `None` keeps the current state, as upstream's `undefined` does. `request["signal"]` is its cancellation. |
| `getSettings` | `ctx.get_settings()`. It raises `RuntimeError` until the host has sent the settings. |
| `ToolDefinition.outputSchema`, `exposure`, `namespace`, `annotations`, `defaultActive`, `prepareLoadout` | `ToolDefinition(output_schema=..., exposure=..., namespace=..., annotations=..., default_active=..., prepare_loadout=hook)`. `hook(loadout)` gets a `ToolLoadout` and returns `{"descriptions": ..., "hiddenDeclarations": ...}` or `None`. |
| `structuredContent`, `isError` of a tool result | keys `structured_content` and `is_error` of the dict a tool returns. |
| `ctx.tools`, `ctx.executeTool(name, args, options)` | `ctx.tools` and `ctx.execute_tool(name, args, ExecuteToolOptions(signal=..., on_update=...))`, available while a tool runs. The outcome is upstream's `AgentToolCallOutcome` as a dict; tool failures come back with `isError` true. `signal` defaults to the calling tool's cancellation; a given signal replaces it, so cancelling the calling tool leaves the nested call running (upstream `options.signal ?? signal`). `on_update` runs on the calling thread, in order, before the call returns. |
| `provider_stream_event`, `mcp_servers_change` | `EVENT_PROVIDER_STREAM_EVENT`, `EVENT_MCP_SERVERS_CHANGE` with `on_event`. Tool events carry `parentToolCallId`. |
| `pi.events` | `ext.events` and `ctx.events`: `on(channel, handler) -> unsubscribe` and `emit(channel, data)`. Payloads cross as JSON. A handler is `handler(ctx, data)` and may call `emit`. The unsubscribe function never raises; a host refusal is printed to stderr. |

A registration made while the factory runs is queued and applied when the host accepts the extension; a failing factory leaves none behind. `Extension.replaceable` is not an extension API: only the resource loader sets it for an inline extension.

## Threading and cancellation

The SDK uses one reader thread plus one worker thread per in-flight
request. Host calls from a handler block on a single shared write lock
and use the reader thread to deliver `call_result` envelopes.

Each handler receives a `Context` exposing cooperative cancellation:

```python
def slow_tool(ctx, args):
    for chunk in stream():
        if ctx.is_cancelled():
            return {"content": "", "is_error": True}
```

Cancellation is cooperative; a handler that never checks it will run until
the process exits or shuts down.

`is_cancelled()` reports the handler's own request. Pi's `ctx.signal` is the
signal of the run in progress, so it is `ctx.signal`: one `ProviderSignal` for
the whole run that is set when the run aborts, even while a handler is still in
flight, and `None` while no run is active (a command or `session_start` while
idle). Use it for work owned by the current turn:

```python
def on_turn_start(event, ctx):
    signal = ctx.signal
    if signal is not None:
        signal.subscribe(stop_background_work)
```

## Terminal strings

`on_terminal_input` receives strings with the same UTF-16 units as Pi. A high or low surrogate can arrive without its partner. Python `str` retains that unit, and the SDK's JSON encoder sends it as a standard `\u` escape. Return it directly in `TerminalInputResult(data=...)`. Do not encode it to UTF-8 yourself or replace it with U+FFFD. Callback order and cancellation are unchanged.

## Development

```bash
cd extensions/sdk-py
uv run python -m ast pig_sdk/__init__.py >/dev/null
uv run pytest
```

From the repository root, `make test-sdk-py` runs the same suite in a cached venv with the hash-locked dev dependencies in `requirements-dev.txt`; `make check` and CI's `ci-sdk` shard include it. After changing the `dev` dependency group, regenerate the lock with the command at the top of `requirements-dev.txt`.

## License

MIT: see `LICENSE`.

## Child agent sessions

`ctx.create_agent_session(CreateAgentSessionOptions(...))` is Pi's `createAgentSession`: a child agent session over the host's model runtime that the extension prompts (`prompt`, `steer`, `follow_up`), observes (`subscribe`), aborts (`abort`) and disposes (`dispose`), with `custom_tools` served by this extension. Calls block as Pi's Promises resolve; the session ends with the connection. See [Child agent sessions](../../docs/extension-authoring.md#child-agent-sessions-createagentsession).

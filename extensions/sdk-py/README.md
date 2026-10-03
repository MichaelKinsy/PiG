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

## License

MIT: see `LICENSE`.

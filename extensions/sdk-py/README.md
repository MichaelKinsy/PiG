# pig-sdk (Python)

Python SDK for building pig subprocess extensions.

This SDK is a **language bridge** into the same extension API that upstream
pi exposes to TypeScript extensions: the wire protocol, register payload,
tool/command/event semantics, request/response and host-call shapes are
identical to the Go SDK (`extensions/sdk`) and the Rust SDK (`extensions/sdk-rs`).
See [`docs/extension-api-parity.md`](../../docs/extension-api-parity.md) for the host API parity matrix and
[`docs/extension-runtime-cells.md`](../../docs/extension-runtime-cells.md) for how factory extensions are packed
into generated runner subprocesses.

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

## Development

```bash
cd extensions/sdk-py
uv run python -m ast pig_sdk/__init__.py >/dev/null
uv run pytest
```

## License

MIT: see `LICENSE`.

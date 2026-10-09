# Rust extension SDK strings

The Rust SDK targets Pi's extension API through PiG's subprocess host. See [Extension authoring](../../docs/extension-authoring.md) and [API parity](../../docs/extension-api-parity.md).

## Constrained sampling

`ToolDefinition.constrained_sampling` is `Option<ToolConstrainedSampling>`. Use `None` for omission, `Some(ToolConstrainedSampling::Disabled)` for explicit false, or `Some(config.into())` for a `ConstrainedSampling` configuration. `tool_with_constrained_sampling` accepts either the configuration or the disabled variant. This retains Pi's false/config union; true is not a valid value.

## Breaking changes in 0.3.0

Host-backed `Context` getters return `io::Result` and never an empty value or default; Pi's `undefined` is `Option::None` (`get_session_name`, `get_session_file`, `get_leaf_id`, `get_flag`, `get_context_usage`, `get_model_info`). `get_branch` and `get_entries` return the session-log subscription failure. `ExecOptions.timeout` and `DialogOptions.timeout` are `Option<f64>`, and `ToolDefinition.constrained_sampling` is `Option<ToolConstrainedSampling>`. See the migration tables in `docs/site/docs/extensions.md`.

## Upstream 0.99.1 extension API

- Tool fields: `ToolDefinition` carries `output_schema`, `exposure` (`ToolExposure`), `namespace`, `annotations`, `default_active` and `prepare_loadout`. A tool result gains `with_structured_content`, `with_details` and `with_is_error`; `is_error` reports a failure without throwing and keeps the details.
- MCP servers: `Extension::register_mcp_server` while loading, `Context::register_mcp_server` and `unregister_mcp_server` afterwards. The host validates the config; its message is the error. `Context::get_mcp_servers` is answered from the replicated state: the host refreshes it before each tool call, command and event, and each registration reply replaces it.
- Virtual models: `VirtualModel::new(provider, id, name, route)` with `Extension::register_virtual_model` or `Context::register_virtual_model`. `route` runs in the extension for each request; the `Context` it receives is cancelled with the request.
- `Context::get_settings` returns a copy of the effective settings and fails until the host has sent them. `Context::tools` lists the tools `execute_tool` can call and, like `execute_tool`, fails outside a tool call.
- `Context::signal()` is Pi's `ctx.signal`: the `ProviderSignal` of the run in progress, one for the whole run and cancelled when the run aborts even while a handler is in flight, or `None` while no run is active. `Context::is_cancelled()` reports the handler's own request.
- `Context::execute_tool(name, args, ExecuteToolOptions)` runs another tool for the calling tool call and returns its outcome. A tool failure is an outcome with `is_error`, never an `Err`. `on_update` receives partial results in order before the call returns; `signal` cancels the nested call, which by default is cancelled with the calling request.
- `EVENT_PROVIDER_STREAM_EVENT` and `EVENT_MCP_SERVERS_CHANGE` subscribe to `provider_stream_event` and `mcp_servers_change`. The extension that handles `mcp_servers_change` is the one that connects registered servers.

## Terminal input

JavaScript strings contain UTF-16 units, not only Unicode scalar values. Pi can deliver a non-BMP character as two separate terminal-input chunks. Either chunk can be consumed or rewritten before the next arrives.

`Context::on_terminal_input` takes a callback over `&JsString`. `RemoteComponent::handle_input` receives the same lossless type when a custom overlay owns focus. `TerminalInputResult::data` is `Option<JsString>`. `Context::get_editor_text` also returns `JsString`; `set_editor_text` and `paste_to_editor` accept ordinary strings or `JsString`.

```rust
use pig_sdk::{JsString, TerminalInputResult};

let subscription = ctx.on_terminal_input(|data| {
    if data.as_units() == [0xd83d] {
        return TerminalInputResult {
            consume: false,
            data: Some(JsString::from_units(vec![0xd83d])),
        };
    }
    TerminalInputResult::default()
})?;
```

Keep the subscription alive until you want to unsubscribe. Return promptly. The host awaits the verdict in input order and preserves the existing cancellation and connection lifetime.

`JsString::from_units` and `as_units` preserve every unit. `From<&str>` and `From<String>` accept ordinary Rust strings. `to_string` returns an error for unmatched units. `to_string_lossy` explicitly replaces unmatched units for display.

Serialize `JsString` directly with `serde_json::to_string` or `serde_json::to_vec`. The result is valid JSON with ordinary `\u` escapes for lone surrogates. Do not first convert it through `serde_json::Value` or `json!`: `Value::String` cannot hold a lone surrogate. The SDK's typed terminal and editor host calls avoid that conversion internally.

## Mouse input

A `RemoteComponent` or `ViewComponent` that returns `true` from `handles_mouse` receives Pi's fullscreen mouse events in `handle_mouse(&MouseEvent)` (snake_case fields such as `screen_x`, `wheel_delta`, `click_count`; `x`/`y` local to the component), on the same serial queue as `handle_input` and with the same result meaning. Regular `tuiMode` delivers none, as in Pi. See "Mouse in custom components" in `docs/extension-authoring.md`.


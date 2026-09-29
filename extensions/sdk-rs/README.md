# Rust extension SDK strings

The Rust SDK targets Pi's extension API through PiG's subprocess host. See [Extension authoring](../../docs/extension-authoring.md) and [API parity](../../docs/extension-api-parity.md).

## Constrained sampling

`ToolDefinition.constrained_sampling` is `Option<ToolConstrainedSampling>`. Use `None` for omission, `Some(ToolConstrainedSampling::Disabled)` for explicit false, or `Some(config.into())` for a `ConstrainedSampling` configuration. `tool_with_constrained_sampling` accepts either the configuration or the disabled variant. This retains Pi's false/config union; true is not a valid value.

## Breaking changes in 0.3.0

Host-backed `Context` getters return `io::Result` and never an empty value or default; Pi's `undefined` is `Option::None` (`get_session_name`, `get_session_file`, `get_leaf_id`, `get_flag`, `get_context_usage`, `get_model_info`). `get_branch` and `get_entries` return the session-log subscription failure. `ExecOptions.timeout` and `DialogOptions.timeout` are `Option<f64>`, and `ToolDefinition.constrained_sampling` is `Option<ToolConstrainedSampling>`. See the migration tables in `docs/site/docs/extensions.md`.

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

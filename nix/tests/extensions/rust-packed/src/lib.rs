//! The Rust factory (packed cell) of the Nix acceptance check.

use pig_sdk::{CommandResult, Extension};
use serde_json::{Value, json};

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("rust-packed");
    ext.on_event("session_start", |ctx, _data| {
        ctx.set_status("rust-packed", "rust-packed: ready");
        None
    });
    ext.command("rust-packed-probe", "Append an entry and count it", |ctx, _args| {
        if let Err(err) = ctx.append_entry("nix-probe", json!({"by": "rust-packed"})) {
            return CommandResult::Error(err.to_string());
        }
        let entries = match ctx.get_entries() {
            Ok(entries) => entries,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let n = entries
            .iter()
            .filter(|entry| entry.get("customType").and_then(Value::as_str) == Some("nix-probe") && entry.pointer("/data/by").and_then(Value::as_str) == Some("rust-packed"))
            .count();
        ctx.notify(&format!("rust-packed: entries={n}"), "info");
        CommandResult::Ok
    });
    ext
}

//! The Rust standalone (isolated cell) of the Nix acceptance check.

use pig_sdk::{CommandResult, Extension};
use serde_json::{Value, json};

fn main() {
    let mut ext = Extension::new("rust-isolated");
    ext.on_event("session_start", |ctx, _data| {
        ctx.set_status("rust-isolated", "rust-isolated: ready");
        None
    });
    ext.command("rust-isolated-probe", "Append an entry and count it", |ctx, _args| {
        if let Err(err) = ctx.append_entry("nix-probe", json!({"by": "rust-isolated"})) {
            return CommandResult::Error(err.to_string());
        }
        let entries = match ctx.get_entries() {
            Ok(entries) => entries,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let n = entries
            .iter()
            .filter(|entry| entry.get("customType").and_then(Value::as_str) == Some("nix-probe") && entry.pointer("/data/by").and_then(Value::as_str) == Some("rust-isolated"))
            .count();
        ctx.notify(&format!("rust-isolated: entries={n}"), "info");
        CommandResult::Ok
    });
    ext.run().unwrap();
}

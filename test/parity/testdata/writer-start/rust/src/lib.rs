//! The Rust SDK version of the writer-start fixture (../node/index.mjs).

use pig_sdk::{CommandResult, Extension};
use serde_json::json;

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("rust");
    ext.on_event("session_start", |ctx, _data| {
        let _ = ctx.append_entry("writer-start", json!({"text": "from-start"}));
        let _ = ctx.set_session_name("start-name");
        let _ = ctx.send_message("writer-start", "start-message", false, Some(false), None);
        ctx.set_status("writer", "writer: ready");
        None
    });
    ext.command("writer-after", "Append, name and send from a command", |ctx, _args| {
        if let Err(err) = ctx.append_entry("writer-after", json!({"text": "from-command"})) {
            return CommandResult::Error(err.to_string());
        }
        if let Err(err) = ctx.set_session_name("after-name") {
            return CommandResult::Error(err.to_string());
        }
        if let Err(err) = ctx.send_message("writer-after", "after-message", false, Some(false), None) {
            return CommandResult::Error(err.to_string());
        }
        CommandResult::Ok
    });
    ext
}

//! The Rust SDK version of rpc/testdata/command-send-wait-idle.mjs: a command that sends a user message and then waits for the Session to be idle.

use pig_sdk::{CommandResult, Extension};

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("rust");
    ext.command("ask", "Send a user message and wait for idle", |ctx, _args| {
        if let Err(err) = ctx.send_user_message("Say hello", "") {
            return CommandResult::Error(err.to_string());
        }
        match ctx.wait_for_idle() {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext
}

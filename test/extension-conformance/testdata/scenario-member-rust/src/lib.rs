//! The Rust scenario member: a second extension of the SDK under test, loaded beside
//! the scenario driver so that, when the scenario runs shared, both are members of one
//! cell (one process). It registers one command and reads no Spec.
use pig_sdk::*;

pub fn new_extension() -> Extension {
    let mut extension = Extension::new("scenario-member-rust");
    extension.command("member-ping", "The cell member's command", |_ctx, _path| {
        CommandResult::Ok
    });
    extension
}

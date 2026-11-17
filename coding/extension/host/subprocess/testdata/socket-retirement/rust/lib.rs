// The Rust fixture of the socket retirement rows (socket_retirement_runtime_test.go). The test names the gate directory in PIG_RETIREMENT_GATES.
use pig_sdk::{CommandResult, Extension};
use std::{fs, path::Path, thread, time::Duration};

fn gate(name: &str) -> std::path::PathBuf {
    Path::new(&std::env::var("PIG_RETIREMENT_GATES").unwrap()).join(name)
}

fn touch(name: &str) {
    fs::write(gate(name), b"").unwrap();
}

fn exists(name: &str) -> bool {
    gate(name).exists()
}

pub fn new_extension() -> Extension {
    // The factory holds while the test keeps the factory gate, before the SDK connects to the host.
    if exists("factory-hold") {
        touch("factory-holding");
        while exists("factory-hold") {
            thread::sleep(Duration::from_millis(5));
        }
    }
    let mut ext = Extension::new("retire-probe");
    ext.command("retire-hold", "reload, then hold the command", |ctx, _| {
        if let Err(err) = ctx.reload() {
            return CommandResult::Error(err.to_string());
        }
        touch("reloaded");
        while !exists("release-command") {
            thread::sleep(Duration::from_millis(5));
        }
        CommandResult::Ok
    });
    ext.command("retire-ping", "answer from this generation", |_ctx, _| {
        touch("pinged");
        CommandResult::Ok
    });
    ext
}

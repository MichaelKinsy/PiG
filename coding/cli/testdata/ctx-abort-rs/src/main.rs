//! ctx-abort-rs: calls ctx.abort() once, from a message_update handler the run awaits, and logs "abort" before the call, "returned" after it and each
//! agent_end with the stop reason of its last assistant message.
use pig_sdk::Extension;
use std::io::Write;
use std::sync::atomic::{AtomicBool, Ordering};

static FIRED: AtomicBool = AtomicBool::new(false);

fn log_line(line: &str) {
    let path = std::env::var("ABORT_PROBE_LOG").unwrap_or_default();
    if let Ok(mut file) = std::fs::OpenOptions::new()
        .append(true)
        .create(true)
        .open(path)
    {
        let _ = writeln!(file, "{line}");
    }
}

fn main() {
    let mut ext = Extension::new("ctx-abort-rs");
    ext.on_event("message_update", |ctx, _event| {
        if !FIRED.swap(true, Ordering::SeqCst) {
            log_line("abort");
            ctx.abort();
            log_line("returned");
        }
        None
    });
    ext.on_event("agent_end", |_ctx, event| {
        let reason = event["messages"]
            .as_array()
            .and_then(|messages| {
                messages
                    .iter()
                    .rev()
                    .find(|message| message["role"] == "assistant")
            })
            .and_then(|message| message["stopReason"].as_str())
            .unwrap_or("")
            .to_string();
        log_line(&format!("agent_end:{reason}"));
        None
    });
    if let Err(error) = ext.run() {
        eprintln!("ctx-abort-rs: {error}");
        std::process::exit(1);
    }
}

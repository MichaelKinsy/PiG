//! rpc-settle-wait-rs: the agent_settled handler and the hold command wait on a plain TCP read.
//! They dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer
//! writes a line. The input handler dials RPC_SETTLE_INPUT_ADDR instead, the before_agent_start handler RPC_SETTLE_START_ADDR. Without the address they return at once. With RPC_SETTLE_DONE the hold command writes that file once its wait returned. The wait is no SDK call, so nothing but the read itself tells the extension
//! runtime from a handler that computes.
use pig_sdk::{CommandResult, Extension};
use std::io::{BufRead, BufReader, Write};
use std::net::TcpStream;

fn wait(env: &str) -> std::io::Result<()> {
    let Ok(addr) = std::env::var(env) else {
        return Ok(());
    };
    let mut peer = TcpStream::connect(addr)?;
    peer.write_all(b"waiting\n")?;
    BufReader::new(peer).read_line(&mut String::new())?;
    Ok(())
}

fn main() {
    let mut ext = Extension::new("rpc-settle-wait-rs");
    ext.on_event("agent_settled", |_, _| {
        let _ = wait("RPC_SETTLE_ADDR");
        None
    });
    ext.on_event("input", |_, _| {
        let _ = wait("RPC_SETTLE_INPUT_ADDR");
        None
    });
    // The SDK registers before_agent_start handlers with on_before_agent_start; on_event refuses that event.
    ext.on_before_agent_start(|_, _| {
        let _ = wait("RPC_SETTLE_START_ADDR");
        Ok(None)
    });
    // Handlers of the two events Pi emits without awaiting them: with RPC_NAME_ADDR they wait on the peer, with RPC_NAME_BUSY_MS they compute for that long without waiting, and then they notify.
    for event in ["session_info_changed", "thinking_level_select"] {
        ext.on_event(event, move |ctx, _| {
            let busy = std::env::var("RPC_NAME_BUSY_MS")
                .ok()
                .and_then(|v| v.parse::<u64>().ok())
                .unwrap_or(0);
            if std::env::var("RPC_NAME_ADDR").is_ok() || busy > 0 {
                let end = std::time::Instant::now() + std::time::Duration::from_millis(busy);
                while std::time::Instant::now() < end {
                    std::hint::spin_loop(); // computing: nothing waits
                }
                let _ = wait("RPC_NAME_ADDR");
                ctx.notify(&format!("name-handler-done:{event}"), "info");
            }
            None
        });
    }
    ext.command("hold", "wait on a socket read", |_, _| {
        match wait("RPC_SETTLE_ADDR") {
            Ok(()) => {
                if let Ok(done) = std::env::var("RPC_SETTLE_DONE") {
                    let _ = std::fs::write(done, "done\n");
                }
                CommandResult::Ok
            }
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    if let Err(error) = ext.run() {
        eprintln!("rpc-settle-wait-rs: {error}");
        std::process::exit(1);
    }
}

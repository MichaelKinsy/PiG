//! rpc-settle-wait-rs: the agent_settled handler and the hold command wait on a plain TCP read.
//! They dial RPC_SETTLE_ADDR, announce themselves with a "waiting" line and return when the peer
//! writes a line. The input handler dials RPC_SETTLE_INPUT_ADDR instead. Without the address they return at once. The wait is no SDK call, so nothing but the read itself tells the extension
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
    ext.command("hold", "wait on a socket read", |_, _| match wait("RPC_SETTLE_ADDR") {
        Ok(()) => CommandResult::Ok,
        Err(error) => CommandResult::Error(error.to_string()),
    });
    if let Err(error) = ext.run() {
        eprintln!("rpc-settle-wait-rs: {error}");
        std::process::exit(1);
    }
}

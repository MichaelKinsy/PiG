//! A handler that makes a blocking host call completes, whatever the host sends meanwhile.
//!
//! Pi runs every extension handler in one process, and a handler can always await a host call
//! (packages/coding-agent/src/core/extensions/types.ts: the `ctx.ui` dialogs `select`, `confirm`
//! and `input`, `exec` and `executeTool` return promises; `notify` and `setFooter` are synchronous
//! in-process calls). A pig handler waits on a socket reply, so no handler may run on the
//! thread that reads those replies.

use super::{CommandResult, Extension};
use crate::protocol::{CallResultMsg, Envelope, NotifyMsg, PingMsg, ReadyMsg, RequestMsg};
use serde_json::{json, Value};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{mpsc, Arc, Mutex};
use std::thread::JoinHandle;
use std::time::Duration;

const WAIT: Duration = Duration::from_secs(5);

pub(super) struct Host {
    stream: UnixStream,
    socket: std::path::PathBuf,
    extension: Option<JoinHandle<std::io::Result<()>>>,
}

fn next_socket() -> std::path::PathBuf {
    static NEXT: AtomicU64 = AtomicU64::new(0);
    std::env::temp_dir().join(format!(
        "pig-rs-dispatch-{}-{}.sock",
        std::process::id(),
        NEXT.fetch_add(1, Ordering::Relaxed)
    ))
}

impl Host {
    pub(super) fn connect(ext: Extension) -> Host {
        let socket = next_socket();
        let _ = std::fs::remove_file(&socket);
        let listener = UnixListener::bind(&socket).unwrap();
        let path = socket.clone();
        let extension = std::thread::spawn(move || ext.run_with_socket(path.to_str().unwrap()));
        let (stream, _) = listener.accept().unwrap();
        stream.set_read_timeout(Some(WAIT)).unwrap();
        let mut host = Host {
            stream,
            socket,
            extension: Some(extension),
        };
        assert_eq!(host.read_until("register").msg_type, "register");
        host.send(Envelope {
            msg_type: "ready".into(),
            ready: Some(ReadyMsg {
                session_name: String::new(),
                cwd: "/tmp".into(),
                mode: "print".into(),
                width: 80,
                height: 24,
                model: String::new(),
                state: None,
            }),
            ..Default::default()
        });
        host
    }

    pub(super) fn send(&mut self, env: Envelope) {
        let data = serde_json::to_vec(&env).unwrap();
        self.stream.write_all(&(data.len() as u32).to_be_bytes()).unwrap();
        self.stream.write_all(&data).unwrap();
    }

    pub(super) fn read_until(&mut self, kind: &str) -> Envelope {
        loop {
            let mut header = [0u8; 4];
            self.stream
                .read_exact(&mut header)
                .unwrap_or_else(|err| panic!("the extension sent no {kind} frame: {err}"));
            let mut data = vec![0u8; u32::from_be_bytes(header) as usize];
            self.stream.read_exact(&mut data).unwrap();
            let env: Envelope = serde_json::from_slice(&data).unwrap();
            if env.msg_type == kind {
                return env;
            }
        }
    }

    fn request(&mut self, id: &str, method: &str, tool: &str, event: Option<&str>, handler_id: u32) {
        self.send(Envelope {
            msg_type: "request".into(),
            id: Some(id.into()),
            request: Some(RequestMsg {
                terminal_input: None,
                method: method.into(),
                tool: (!tool.is_empty()).then(|| tool.into()),
                event: event.map(Into::into),
                handler_id,
                tool_call_id: Some(format!("call-{id}")),
                args: Some(json!({})),
            }),
            ..Default::default()
        });
    }

    fn command(&mut self, name: &str) {
        self.request(&format!("req-{name}"), "command", name, None, 0);
    }

    fn width(&mut self, width: u32) {
        self.send(Envelope {
            msg_type: "notify".into(),
            notify: Some(NotifyMsg {
                custom_input: None,
                method: "width_change".into(),
                args: Some(json!({"width": width})),
            }),
            ..Default::default()
        });
    }

    fn answer(&mut self, call: &Envelope) {
        self.send(Envelope {
            msg_type: "call_result".into(),
            id: call.id.clone(),
            call_result: Some(CallResultMsg {
                result: Some(Value::Null),
                error: None,
            }),
            ..Default::default()
        });
    }

    pub(super) fn stop(mut self) {
        self.send(Envelope {
            msg_type: "shutdown".into(),
            ..Default::default()
        });
        let extension = self.extension.take().unwrap();
        let deadline = std::time::Instant::now() + WAIT;
        while !extension.is_finished() {
            assert!(std::time::Instant::now() < deadline, "the extension did not stop after shutdown");
            std::thread::sleep(Duration::from_millis(5));
        }
        extension.join().unwrap().unwrap();
        let _ = std::fs::remove_file(&self.socket);
    }
}

type WidthHandler = Box<dyn Fn(&crate::Context, u32) + Send + Sync>;

/// Registers command `arm`, which subscribes each handler to width changes with a retained command context, the way an extension keeps one.
fn arm_widths(ext: &mut Extension, handlers: Vec<WidthHandler>) {
    let handlers: Vec<Arc<WidthHandler>> = handlers.into_iter().map(Arc::new).collect();
    let subscriptions = Mutex::new(Vec::new());
    ext.command("arm", "arm", move |ctx, _| {
        for handler in &handlers {
            let (retained, handler) = (ctx.clone(), handler.clone());
            subscriptions
                .lock()
                .unwrap()
                .push(ctx.on_width_change(move |width| handler(&retained, width)));
        }
        CommandResult::Ok
    });
}

fn armed(ext: Extension) -> Host {
    let mut host = Host::connect(ext);
    host.command("arm");
    host.read_until("response");
    host
}

#[test]
fn width_handler_blocking_host_call_completes() {
    let mut ext = Extension::new("width-call");
    let (done_tx, done_rx) = mpsc::channel();
    let done_tx = Mutex::new(done_tx);
    arm_widths(
        &mut ext,
        vec![Box::new(move |ctx, _| {
            ctx.set_footer(vec!["footer".into()]).unwrap();
            done_tx.lock().unwrap().send(()).unwrap();
        })],
    );
    let mut host = armed(ext);
    host.width(100);
    let call = host.read_until("call");
    assert_eq!(call.call.as_ref().unwrap().method, "ui.setFooter");
    host.answer(&call);
    done_rx
        .recv_timeout(WAIT)
        .expect("the width handler's host call never returned: the extension stopped reading replies");
    host.stop();
}

#[test]
fn blocked_width_handler_does_not_stop_the_reader() {
    let mut ext = Extension::new("width-reader");
    arm_widths(&mut ext, vec![Box::new(|ctx, _| ctx.notify("from the width handler", "info"))]);
    ext.command("ping", "ping", |_, _| CommandResult::Ok);
    let mut host = armed(ext);
    host.width(100);
    let held = host.read_until("call");
    // The handler's call has no reply. Pings, requests and the reply itself are still read.
    host.send(Envelope {
        msg_type: "ping".into(),
        ping: Some(PingMsg { nonce: "n1".into() }),
        ..Default::default()
    });
    assert_eq!(host.read_until("pong").pong.unwrap().nonce, "n1");
    host.command("ping");
    assert_eq!(host.read_until("response").id.as_deref(), Some("req-ping"));
    host.answer(&held);
    host.stop();
}

#[test]
fn width_handlers_run_in_order_while_one_blocks() {
    let mut ext = Extension::new("width-order");
    let (seen_tx, seen_rx) = mpsc::channel();
    let seen_tx = Mutex::new(seen_tx);
    arm_widths(
        &mut ext,
        vec![Box::new(move |ctx, width| {
            ctx.notify(&format!("w{width}"), "info");
            seen_tx.lock().unwrap().send(width).unwrap();
        })],
    );
    let mut host = armed(ext);
    for width in [100, 90, 80] {
        host.width(width);
    }
    for _ in 0..3 {
        let call = host.read_until("call");
        host.answer(&call);
    }
    let seen: Vec<u32> = (0..3).map(|_| seen_rx.recv_timeout(WAIT).unwrap()).collect();
    assert_eq!(seen, [100, 90, 80]);
    host.stop();
}

#[test]
fn panicking_width_handler_does_not_stop_the_extension() {
    let mut ext = Extension::new("width-panic");
    let (seen_tx, seen_rx) = mpsc::channel();
    let seen_tx = Mutex::new(seen_tx);
    arm_widths(
        &mut ext,
        vec![
            Box::new(|_, _| panic!("handler failed")),
            Box::new(move |_, width| seen_tx.lock().unwrap().send(width).unwrap()),
        ],
    );
    ext.command("ping", "ping", |_, _| CommandResult::Ok);
    let mut host = armed(ext);
    host.width(100);
    assert_eq!(
        seen_rx.recv_timeout(WAIT).expect("a panicking handler stopped the other handlers"),
        100
    );
    host.command("ping");
    assert_eq!(host.read_until("response").id.as_deref(), Some("req-ping"));
    host.stop();
}

#[test]
fn shutdown_ends_a_width_handler_blocked_on_a_host_call() {
    let mut ext = Extension::new("width-shutdown");
    let (ended_tx, ended_rx) = mpsc::channel();
    let ended_tx = Mutex::new(ended_tx);
    arm_widths(
        &mut ext,
        vec![Box::new(move |ctx, _| {
            ctx.notify("never answered", "info");
            ended_tx.lock().unwrap().send(()).unwrap();
        })],
    );
    let mut host = armed(ext);
    host.width(100);
    host.read_until("call");
    // The extension drains its handlers before it returns, so stop() also proves the wait ended.
    host.stop();
    ended_rx.recv_timeout(WAIT).expect("shutdown left the width handler waiting");
}

#[test]
fn request_handlers_complete_blocking_host_calls() {
    for kind in ["event", "command", "tool"] {
        let mut ext = Extension::new(format!("call-{kind}"));
        let (done_tx, done_rx) = mpsc::channel();
        let done_tx = Mutex::new(done_tx);
        let finish = move |ctx: &crate::Context| {
            ctx.set_footer(vec!["footer".into()]).unwrap();
            done_tx.lock().unwrap().send(()).unwrap();
        };
        match kind {
            "event" => drop(ext.on_event("agent_start", false, move |ctx, _| {
                finish(ctx);
                None
            })),
            "command" => ext.command("go", "go", move |ctx, _| {
                finish(ctx);
                CommandResult::Ok
            }),
            _ => ext.tool("go", "go", json!({"type": "object"}), move |ctx, _| {
                finish(ctx);
                crate::ToolResult::text("ok")
            }),
        }
        let mut host = Host::connect(ext);
        match kind {
            "event" => host.request("req-1", "event", "", Some("agent_start"), 1),
            "command" => host.command("go"),
            _ => host.request("req-1", "tool_call", "go", None, 0),
        }
        let call = host.read_until("call");
        assert_eq!(call.call.as_ref().unwrap().method, "ui.setFooter", "{kind}");
        host.answer(&call);
        done_rx.recv_timeout(WAIT).unwrap_or_else(|_| panic!("the {kind} handler never finished"));
        host.read_until("response");
        host.stop();
    }
}

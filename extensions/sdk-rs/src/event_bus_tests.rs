//! The wire the Rust SDK speaks for upstream's pi.events (packages/coding-agent/src/core/event-bus.ts:12-33): events.on and events.emit calls flagged by value, events.off, and the Host's events.dispatch request. The Go SDK's tests state the same rows.

use crate::protocol::{CallResultMsg, Connection, Envelope, RequestMsg};
use crate::{CommandResult, Extension};
use serde_json::{Value, json};
use std::os::unix::net::UnixListener;
use std::sync::{Arc, Mutex, mpsc};
use std::thread;
use std::time::Duration;

struct Host {
    conn: Connection,
    thread: Option<thread::JoinHandle<std::io::Result<()>>>,
    _dir: tempfile_dir::Dir,
}

mod tempfile_dir {
    pub struct Dir(pub std::path::PathBuf);
    impl Dir {
        pub fn new(tag: &str) -> Self {
            let path = std::env::temp_dir().join(format!("pig-sdk-rs-{tag}-{}", std::process::id()));
            let _ = std::fs::remove_dir_all(&path);
            std::fs::create_dir_all(&path).unwrap();
            Self(path)
        }
    }
    impl Drop for Dir {
        fn drop(&mut self) {
            let _ = std::fs::remove_dir_all(&self.0);
        }
    }
}

impl Host {
    fn start(tag: &str, ext: Extension) -> Self {
        let dir = tempfile_dir::Dir::new(tag);
        let path = dir.0.join("ext.sock");
        let listener = UnixListener::bind(&path).unwrap();
        let path = path.to_str().unwrap().to_string();
        let thread = thread::spawn(move || ext.run_with_socket(&path));
        let (stream, _) = listener.accept().unwrap();
        stream.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
        Self { conn: Connection::new(stream), thread: Some(thread), _dir: dir }
    }

    fn next(&self) -> Envelope<Box<serde_json::value::RawValue>> {
        loop {
            let env = self.conn.read_envelope().unwrap();
            if env.msg_type != "request_state" {
                return env;
            }
        }
    }

    /// Reads the extension's next host call and answers it.
    fn call(&self, method: &str, result: Option<Value>) -> (Value, Option<String>) {
        loop {
            let env = self.next();
            if env.msg_type != "call" {
                continue;
            }
            let call = env.call.unwrap();
            assert_eq!(call.method, method);
            self.conn
                .write_envelope(&Envelope {
                    msg_type: "call_result".into(),
                    id: env.id,
                    call_result: Some(CallResultMsg { result, error: None }),
                    ..Default::default()
                })
                .unwrap();
            return (call.args.unwrap_or(Value::Null), call.parent_request_id);
        }
    }

    fn ready(&self) {
        while self.next().msg_type != "register" {}
        self.conn
            .write_envelope(&Envelope { msg_type: "ready".into(), ready: Some(serde_json::from_value(json!({"session_name": "", "cwd": "/tmp", "mode": "print", "width": 80, "model": ""})).unwrap()), ..Default::default() })
            .unwrap();
    }

    fn request(&self, id: &str, method: &str, tool: Option<&str>, args: Option<Value>) {
        self.conn
            .write_envelope(&Envelope {
                msg_type: "request".into(),
                id: Some(id.into()),
                request: Some(RequestMsg { method: method.into(), tool: tool.map(str::to_string), event: None, handler_id: 0, tool_call_id: None, args, terminal_input: None }),
                ..Default::default()
            })
            .unwrap();
    }

    fn response(&self) -> (String, Option<String>) {
        loop {
            let env = self.next();
            if env.msg_type == "response" {
                return (env.id.unwrap(), env.response.unwrap().error.map(|error| error.message));
            }
        }
    }

    fn stop(mut self) {
        self.conn.write_envelope(&Envelope { msg_type: "shutdown".into(), shutdown: Some(serde_json::from_value(json!({"reason": "test"})).unwrap()), ..Default::default() }).unwrap();
        self.thread.take().unwrap().join().unwrap().unwrap();
    }
}

// A listener declared while the factory runs is registered before the register message, as a node factory's pi.events.on is (loader.ts runs the factory before the runtime registers), and carries the by-value flag.
#[test]
fn factory_listener_is_registered_before_register() {
    let ext = Extension::new("bus");
    ext.events().on("ch", |_, _| Ok(())).unwrap();
    let host = Host::start("factory", ext);
    let (on, parent) = host.call("events.on", None);
    assert_eq!(on["channel"], "ch");
    assert!(on["handlerId"].as_str().is_some_and(|id| !id.is_empty()));
    assert_eq!(on["value"], true);
    assert_eq!(parent, None);
    host.ready();
    host.stop();
}

// Dispatch runs the handler with the payload decoded from the JSON the Host sends and answers after the handler returns (event-bus.ts:19-25).
#[test]
fn dispatch_runs_handler_then_answers() {
    let seen = Arc::new(Mutex::new(Vec::new()));
    let ext = Extension::new("bus");
    let sink = seen.clone();
    ext.events().on("ch", move |_, data| { sink.lock().unwrap().push(data.clone()); Ok(()) }).unwrap();
    let host = Host::start("dispatch", ext);
    let (on, _) = host.call("events.on", None);
    host.ready();
    host.request("d1", "events.dispatch", None, Some(json!({"handlerId": on["handlerId"], "channel": "ch", "json": {"a": [1, 2]}})));
    let (id, error) = host.response();
    assert_eq!((id.as_str(), error), ("d1", None));
    assert_eq!(*seen.lock().unwrap(), vec![json!({"a": [1, 2]})], "the response arrived before the handler ran");
    host.stop();
}

// A handler's error or panic is the dispatch's failure and nothing else: the extension keeps serving (event-bus.ts:19-25 catches and prints, and never rethrows).
#[test]
fn handler_error_and_panic_fail_only_their_dispatch() {
    let ext = Extension::new("bus");
    ext.events().on("fail", |_, _| Err("listener-failed".to_string())).unwrap();
    ext.events().on("panic", |_, _| panic!("listener-panicked")).unwrap();
    ext.events().on("ok", |_, _| Ok(())).unwrap();
    let host = Host::start("failure", ext);
    let mut ids = std::collections::HashMap::new();
    for _ in 0..3 {
        let (on, _) = host.call("events.on", None);
        ids.insert(on["channel"].as_str().unwrap().to_string(), on["handlerId"].clone());
    }
    host.ready();
    for (i, (channel, want)) in [("fail", "listener-failed"), ("panic", "listener-panicked"), ("ok", "")].into_iter().enumerate() {
        host.request(&format!("d{i}"), "events.dispatch", None, Some(json!({"handlerId": ids[channel], "channel": channel, "json": null})));
        let (_, error) = host.response();
        let message = error.unwrap_or_default();
        assert_eq!(want.is_empty(), message.is_empty(), "{channel}: {message}");
        assert!(message.contains(want), "{channel}: {message}");
    }
    host.stop();
}

// Emit sends the payload as JSON under the by-value flag and takes the calling request as the call's parent, so a handler's nested emit is ordered in its own lane and cancelled with it. The unhandled "error" rule surfaces as the emit's error (event-bus.ts:15-17, EventEmitter throws).
#[test]
fn emit_carries_payload_and_parent() {
    let mut ext = Extension::new("bus");
    let (outcomes, results) = mpsc::channel::<String>();
    let outcomes = Mutex::new(outcomes);
    ext.command("go", "", move |ctx, _| {
        ctx.events().emit("ch", json!({"a": 1})).unwrap();
        let error = ctx.events().emit("error", json!("boom")).err().map(|error| error.to_string()).unwrap_or_default();
        outcomes.lock().unwrap().send(error).unwrap();
        CommandResult::Ok
    });
    let host = Host::start("emit", ext);
    host.ready();
    host.request("cmd1", "command", Some("go"), None);
    let (args, parent) = host.call("events.emit", None);
    assert_eq!(args, json!({"channel": "ch", "value": true, "json": {"a": 1}}));
    assert_eq!(parent.as_deref(), Some("cmd1"));
    host.call("events.emit", Some(json!({"unhandledError": true})));
    assert_eq!(host.response().0, "cmd1");
    assert!(results.recv_timeout(Duration::from_secs(5)).unwrap().contains("unhandled error"));
    host.stop();
}

// Unsubscribe removes the listener with one events.off call and is idempotent (event-bus.ts:27, EventEmitter.off).
#[test]
fn unsubscribe_sends_one_off() {
    let mut ext = Extension::new("bus");
    let (done, finished) = mpsc::channel::<()>();
    let done = Mutex::new(done);
    ext.command("off", "", move |ctx, _| {
        let subscription = ctx.events().on("ch", |_, _| Ok(())).unwrap();
        subscription.unsubscribe();
        subscription.unsubscribe();
        done.lock().unwrap().send(()).unwrap();
        CommandResult::Ok
    });
    let host = Host::start("off", ext);
    host.ready();
    host.request("cmd1", "command", Some("off"), None);
    let (on, parent) = host.call("events.on", None);
    assert_eq!(parent.as_deref(), Some("cmd1"));
    let (off, _) = host.call("events.off", None);
    assert_eq!(off["handlerId"], on["handlerId"]);
    finished.recv_timeout(Duration::from_secs(5)).expect("the second unsubscribe waited for a host call it should not send");
    host.stop();
}


// A listener the Host dispatches to while the extension is still loading (after register, before ready) is answered once the extension is ready, in dispatch order; the emitter waits either way.
#[test]
fn dispatch_during_load_is_served_after_ready() {
    let seen = Arc::new(Mutex::new(Vec::new()));
    let ext = Extension::new("bus");
    let sink = seen.clone();
    ext.events().on("ch", move |_, data| { sink.lock().unwrap().push(data.clone()); Ok(()) }).unwrap();
    let host = Host::start("early", ext);
    let (on, _) = host.call("events.on", None);
    while host.next().msg_type != "register" {}
    host.request("d1", "events.dispatch", None, Some(json!({"handlerId": on["handlerId"], "channel": "ch", "json": "early"})));
    host.conn
        .write_envelope(&Envelope { msg_type: "ready".into(), ready: Some(serde_json::from_value(json!({"session_name": "", "cwd": "/tmp", "mode": "print", "width": 80, "model": ""})).unwrap()), ..Default::default() })
        .unwrap();
    let (id, error) = host.response();
    assert_eq!((id.as_str(), error), ("d1", None));
    assert_eq!(*seen.lock().unwrap(), vec![json!("early")]);
    host.stop();
}

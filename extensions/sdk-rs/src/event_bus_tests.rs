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
                request: Some(RequestMsg { method: method.into(), tool: tool.map(str::to_string), event: None, handler_id: 0, tool_call_id: None, args, terminal_input: None, stream: None, stream_error: None, tool_call_index: None, args_raw: None }),
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

// A factory's pi.events.emit is Pi's synchronous EventEmitter emit (event-bus.ts:13-34): the listeners of the extensions loaded before hear it at once, then the
// factory's own, each before emit returns, and an emit on "error" that nobody hears fails. The SDK has no connection while the factory runs, so the first
// factory-time bus call dials the host (as parse_frontmatter does) and run takes that connection. The Go SDK's tests state the same rows
// (extensions/sdk/event_bus_factory_test.go).
mod factory_emit {
    use super::*;

    /// A host that accepts the one connection the factory dials and answers each call it reads with the next scripted result. PIG_EXT_SOCKET names its socket for the test, under the lock every test that sets the environment shares.
    struct FactoryHost {
        _guard: std::sync::MutexGuard<'static, ()>,
        _dir: tempfile_dir::Dir,
        path: String,
        listener: UnixListener,
    }

    impl FactoryHost {
        fn new(tag: &str) -> Self {
            let guard = crate::extension::registration_tests::env_lock().lock().unwrap_or_else(|poisoned| poisoned.into_inner());
            let dir = tempfile_dir::Dir::new(tag);
            let path = dir.0.join("ext.sock");
            let listener = UnixListener::bind(&path).unwrap();
            let path = path.to_str().unwrap().to_string();
            unsafe { std::env::set_var("PIG_EXT_SOCKET", &path) };
            Self { _guard: guard, _dir: dir, path, listener }
        }

        /// Accepts the connection and serves `results.len()` calls on a thread; the thread returns the calls it saw and the connection.
        fn serve(&self, results: Vec<Option<Value>>) -> thread::JoinHandle<(Vec<(String, Value, Option<String>)>, Connection)> {
            let listener = self.listener.try_clone().unwrap();
            thread::spawn(move || {
                let (stream, _) = listener.accept().unwrap();
                stream.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
                let conn = Connection::new(stream);
                let mut seen = Vec::new();
                for result in results {
                    let env = loop {
                        let env = conn.read_envelope().unwrap();
                        if env.msg_type == "call" {
                            break env;
                        }
                    };
                    let call = env.call.unwrap();
                    seen.push((call.method, call.args.unwrap_or(Value::Null), call.parent_request_id));
                    conn.write_envelope(&Envelope { msg_type: "call_result".into(), id: env.id, call_result: Some(CallResultMsg { result, error: None }), ..Default::default() }).unwrap();
                }
                (seen, conn)
            })
        }
    }

    impl Drop for FactoryHost {
        fn drop(&mut self) {
            unsafe { std::env::remove_var("PIG_EXT_SOCKET") };
            let _ = crate::host_helpers::take_dialled(&self.path);
        }
    }

    #[test]
    fn emit_reaches_the_host_and_own_listeners_before_it_returns() {
        let host = FactoryHost::new("fe-own");
        let heard = Arc::new(Mutex::new(Vec::new()));
        let ext = Extension::new("bus");
        let sink = heard.clone();
        ext.events().on("ch", move |_, data| { sink.lock().unwrap().push(data.clone()); Ok(()) }).unwrap();
        let served = host.serve(vec![None]);
        ext.events().emit("ch", json!({"n": 1})).unwrap();
        let (seen, _conn) = served.join().unwrap();
        assert_eq!(seen, vec![("events.emit".to_string(), json!({"channel": "ch", "value": true, "json": {"n": 1}}), None)]);
        // The Host does not hold this extension's listener yet: it ran inline, and has finished when emit returned.
        assert_eq!(*heard.lock().unwrap(), vec![json!({"n": 1})]);
    }

    #[test]
    fn listeners_run_in_order_and_a_failure_reaches_neither_the_emitter_nor_the_rest() {
        let host = FactoryHost::new("fe-order");
        let order = Arc::new(Mutex::new(Vec::new()));
        let ext = Extension::new("bus");
        let bus = ext.events();
        let record = |name: &'static str| {
            let sink = order.clone();
            move |_: &crate::Context, _: &Value| { sink.lock().unwrap().push(name); Ok(()) }
        };
        bus.on("ch", record("first")).unwrap();
        bus.on("ch", |_, _| Err("boom".to_string())).unwrap();
        bus.on("ch", |_, _| panic!("kaboom")).unwrap();
        bus.on("ch", record("unsubscribed")).unwrap().unsubscribe();
        bus.on("ch", record("last")).unwrap();
        let served = host.serve(vec![None]);
        bus.emit("ch", Value::Null).unwrap();
        bus.on("ch", record("late")).unwrap();
        served.join().unwrap();
        assert_eq!(*order.lock().unwrap(), vec!["first", "last"]);
    }

    #[test]
    fn an_error_emit_nobody_hears_fails_and_an_own_listener_hears_it() {
        let host = FactoryHost::new("fe-error");
        let ext = Extension::new("bus");
        let served = host.serve(vec![Some(json!({"unhandledError": true})), Some(json!({"unhandledError": true}))]);
        let err = ext.events().emit("error", json!({"x": 1})).unwrap_err();
        assert!(err.to_string().contains("unhandled error event"), "{err}");
        let heard = Arc::new(Mutex::new(Vec::new()));
        let sink = heard.clone();
        let own = Extension::new("own");
        own.events().on("error", move |_, data| { sink.lock().unwrap().push(data.clone()); Ok(()) }).unwrap();
        own.events().emit("error", json!("bad")).unwrap();
        served.join().unwrap();
        assert_eq!(*heard.lock().unwrap(), vec![json!("bad")]);
    }

    #[test]
    fn without_a_host_a_factory_emit_fails() {
        let _guard = crate::extension::registration_tests::env_lock().lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        unsafe { std::env::remove_var("PIG_EXT_SOCKET") };
        unsafe { std::env::remove_var("GOPI_EXT_SOCKET") };
        let err = Extension::new("bus").events().emit("ch", Value::Null).unwrap_err();
        assert!(err.to_string().contains("PIG_EXT_SOCKET"), "{err}");
    }

    #[test]
    fn a_factory_listener_context_serves_only_the_bus() {
        let host = FactoryHost::new("fe-ctx");
        let heard = Arc::new(Mutex::new(Vec::new()));
        let failure = Arc::new(Mutex::new(None));
        let ext = Extension::new("bus");
        let sink = heard.clone();
        ext.events().on("inner", move |_, _| { sink.lock().unwrap().push("inner"); Ok(()) }).unwrap();
        let slot = failure.clone();
        ext.events()
            .on("outer", move |ctx, _| {
                ctx.events().emit("inner", Value::Null).map_err(|err| err.to_string())?;
                *slot.lock().unwrap() = ctx.get_tools_expanded().err().map(|err| err.to_string());
                Ok(())
            })
            .unwrap();
        let served = host.serve(vec![None, None]);
        ext.events().emit("outer", Value::Null).unwrap();
        let (seen, _conn) = served.join().unwrap();
        assert_eq!(seen.iter().map(|(_, args, _)| args["channel"].clone()).collect::<Vec<_>>(), vec![json!("outer"), json!("inner")]);
        assert_eq!(*heard.lock().unwrap(), vec!["inner"]);
        assert!(failure.lock().unwrap().as_deref().is_some_and(|message| message.contains("not registered")), "{:?}", failure.lock().unwrap());
    }

    #[test]
    fn run_takes_the_connection_the_factory_dialled_and_registers_the_listener() {
        let host = FactoryHost::new("fe-run");
        let heard = Arc::new(Mutex::new(Vec::new()));
        let ext = Extension::new("bus");
        let sink = heard.clone();
        ext.events().on("ch", move |_, data| { sink.lock().unwrap().push(data.clone()); Ok(()) }).unwrap();
        let listener = host.listener.try_clone().unwrap();
        let accepted = Arc::new(Mutex::new(0));
        let counter = accepted.clone();
        let served = thread::spawn(move || {
            let (stream, _) = listener.accept().unwrap();
            *counter.lock().unwrap() += 1;
            stream.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
            let conn = Connection::new(stream);
            let answer = |conn: &Connection, method: &str| {
                let env = loop {
                    let env = conn.read_envelope().unwrap();
                    if env.msg_type == "call" {
                        break env;
                    }
                };
                assert_eq!(env.call.unwrap().method, method);
                conn.write_envelope(&Envelope { msg_type: "call_result".into(), id: env.id, call_result: Some(CallResultMsg { result: None, error: None }), ..Default::default() }).unwrap();
            };
            answer(&conn, "events.emit");
            answer(&conn, "events.on");
            while conn.read_envelope().unwrap().msg_type != "register" {}
            conn.write_envelope(&Envelope { msg_type: "ready".into(), ready: Some(serde_json::from_value(json!({"session_name": "", "cwd": "/tmp", "mode": "print", "width": 80, "model": ""})).unwrap()), ..Default::default() }).unwrap();
            conn.write_envelope(&Envelope { msg_type: "shutdown".into(), shutdown: Some(serde_json::from_value(json!({"reason": "test"})).unwrap()), ..Default::default() }).unwrap();
        });
        ext.events().emit("ch", json!(1)).unwrap();
        let path = host.path.clone();
        ext.run_with_socket(&path).unwrap();
        served.join().unwrap();
        // One connection served the emit and the run, and the emit was not delivered a second time.
        assert_eq!(*accepted.lock().unwrap(), 1);
        assert_eq!(*heard.lock().unwrap(), vec![json!(1)]);
    }
}

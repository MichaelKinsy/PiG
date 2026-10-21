//! The wire the Rust SDK speaks for upstream's pi.events (packages/coding-agent/src/core/event-bus.ts:12-33): events.on and events.emit calls flagged by value, events.off, and the Host's events.dispatch request. The Go SDK's tests state the same rows.

use crate::protocol::{CallResultMsg, Connection, Envelope, RequestMsg};
use crate::{CommandResult, Extension};
use serde_json::{Value, json};
use std::os::unix::net::UnixListener;
use std::sync::{Arc, Mutex, mpsc};
use std::thread;
use std::time::Duration;

/// Holds the environment lock with no host named: a listener subscribed before `run` connects to the host `PIG_EXT_SOCKET` names, so a test that subscribes without one must not run while another test sets it.
fn without_host() -> std::sync::MutexGuard<'static, ()> {
    let guard = crate::extension::registration_tests::env_lock().lock().unwrap_or_else(|poisoned| poisoned.into_inner());
    unsafe { std::env::remove_var("PIG_EXT_SOCKET") };
    unsafe { std::env::remove_var("GOPI_EXT_SOCKET") };
    guard
}

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
            .write_envelope(&Envelope { msg_type: "ready".into(), ready: Some(serde_json::from_value(json!({"cwd": "/tmp", "mode": "print", "width": 80})).unwrap()), ..Default::default() })
            .unwrap();
    }

    fn request(&self, id: &str, method: &str, tool: Option<&str>, args: Option<Value>) {
        self.conn
            .write_envelope(&Envelope {
                msg_type: "request".into(),
                id: Some(id.into()),
                request: Some(RequestMsg { method: method.into(), tool: tool.map(str::to_string), event: None, handler_id: 0, tool_call_id: None, batch: None, args, child_session: None, terminal_input: None, stream: None, stream_error: None, tool_call_index: None, args_raw: None }),
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
    let _no_host = without_host();
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
    let _no_host = without_host();
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
    let _no_host = without_host();
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
    let _no_host = without_host();
    let seen = Arc::new(Mutex::new(Vec::new()));
    let ext = Extension::new("bus");
    let sink = seen.clone();
    ext.events().on("ch", move |_, data| { sink.lock().unwrap().push(data.clone()); Ok(()) }).unwrap();
    let host = Host::start("early", ext);
    let (on, _) = host.call("events.on", None);
    while host.next().msg_type != "register" {}
    host.request("d1", "events.dispatch", None, Some(json!({"handlerId": on["handlerId"], "channel": "ch", "json": "early"})));
    host.conn
        .write_envelope(&Envelope { msg_type: "ready".into(), ready: Some(serde_json::from_value(json!({"cwd": "/tmp", "mode": "print", "width": 80})).unwrap()), ..Default::default() })
        .unwrap();
    let (id, error) = host.response();
    assert_eq!((id.as_str(), error), ("d1", None));
    assert_eq!(*seen.lock().unwrap(), vec![json!("early")]);
    host.stop();
}

// A listener a factory subscribes joins Pi's shared emitter at once (event-bus.ts:13-34), so it hears an emit another extension makes while the factory runs,
// and a factory's emit is delivered by the Host to every listener in order. The SDK has no connection while the factory runs, so the first factory-time bus call
// dials the host (as parse_frontmatter does), run takes that connection, and a factory-time call answers the host's events.dispatch requests on it while it
// waits. The Go SDK's tests state the same rows (extensions/sdk/event_bus_factory_test.go).
mod factory_emit {
    use super::*;

    /// A host that accepts the one connection the factory dials. PIG_EXT_SOCKET names its socket for the test, under the lock every test that sets the environment shares.
    struct FactoryHost {
        _guard: std::sync::MutexGuard<'static, ()>,
        _dir: tempfile_dir::Dir,
        path: String,
        listener: UnixListener,
    }

    /// The host's end of the factory's connection, with what it saw.
    struct Peer {
        conn: Connection,
        calls: Vec<(String, Value)>,
    }

    impl Peer {
        /// Reads until the next call and returns its envelope, unanswered.
        fn next_call(&mut self) -> Envelope<Box<serde_json::value::RawValue>> {
            loop {
                let env = self.conn.read_envelope().unwrap();
                if env.msg_type == "call" {
                    let call = env.call.as_ref().unwrap();
                    let args = call.args.clone().unwrap_or(Value::Null);
                    self.calls.push((call.method.clone(), args));
                    return env;
                }
            }
        }

        fn reply(&self, call: &Envelope<Box<serde_json::value::RawValue>>, result: Option<Value>) {
            self.conn.write_envelope(&Envelope { msg_type: "call_result".into(), id: call.id.clone(), call_result: Some(CallResultMsg { result, error: None }), ..Default::default() }).unwrap();
        }

        /// Answers the next call with `result`.
        fn answer(&mut self, result: Option<Value>) -> (String, Value) {
            let call = self.next_call();
            self.reply(&call, result);
            self.calls.last().unwrap().clone()
        }

        /// Sends the host's events.dispatch request and returns the error message the extension answers with, serving any call the listener makes meanwhile.
        fn dispatch(&mut self, id: &str, handler_id: &str, channel: &str, payload: Value) -> Option<String> {
            self.conn
                .write_envelope(&Envelope {
                    msg_type: "request".into(),
                    id: Some(id.into()),
                    request: Some(serde_json::from_value(json!({"method": "events.dispatch", "args": {"handlerId": handler_id, "channel": channel, "json": payload}})).unwrap()),
                    ..Default::default()
                })
                .unwrap();
            loop {
                let env = self.conn.read_envelope().unwrap();
                match env.msg_type.as_str() {
                    "call" => {
                        let call = env.call.as_ref().unwrap();
                        self.calls.push((call.method.clone(), call.args.clone().unwrap_or(Value::Null)));
                        self.reply(&env, None);
                    }
                    "response" if env.id.as_deref() == Some(id) => return env.response.unwrap().error.map(|error| error.message),
                    _ => {}
                }
            }
        }
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

        /// Accepts the connection the factory dials and runs `script` on it on a thread; the thread returns what the script saw and the peer.
        fn serve<F: FnOnce(&mut Peer) + Send + 'static>(&self, script: F) -> thread::JoinHandle<Peer> {
            let listener = self.listener.try_clone().unwrap();
            thread::spawn(move || {
                let (stream, _) = listener.accept().unwrap();
                stream.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
                let mut peer = Peer { conn: Connection::new(stream), calls: Vec::new() };
                script(&mut peer);
                peer
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
    fn on_registers_with_the_host_at_once() {
        let host = FactoryHost::new("fe-on");
        let ext = Extension::new("bus");
        let served = host.serve(|peer| {
            peer.answer(None);
        });
        ext.events().on("ch", |_, _| Ok(())).unwrap();
        let peer = served.join().unwrap();
        let (method, args) = &peer.calls[0];
        assert_eq!(method, "events.on");
        assert_eq!((args["channel"].as_str(), args["value"].as_bool()), (Some("ch"), Some(true)));
        assert!(args["handlerId"].as_str().is_some_and(|id| !id.is_empty()), "{args}");
    }

    /// The reply to a factory's own emit: the host dispatches the second emit's listener, the factory's, on the factory's connection while the first emit's call is in flight, and the thread that is blocked in emit serves it.
    #[test]
    fn the_host_dispatches_to_a_factory_listener_while_its_emit_waits() {
        let host = FactoryHost::new("fe-nested");
        let heard = Arc::new(Mutex::new(Vec::new()));
        let ext = Extension::new("bus");
        let sink = heard.clone();
        let served = host.serve(|peer| {
            let (_, on) = peer.answer(None);
            let handler_id = on["handlerId"].as_str().unwrap().to_string();
            let cross = peer.next_call();
            assert_eq!(peer.dispatch("d1", &handler_id, "ack", json!("pong")), None);
            peer.reply(&cross, None);
        });
        ext.events().on("ack", move |_, data| { sink.lock().unwrap().push(data.clone()); Ok(()) }).unwrap();
        ext.events().emit("cross", json!("ping")).unwrap();
        let peer = served.join().unwrap();
        assert_eq!(peer.calls.iter().map(|(method, _)| method.as_str()).collect::<Vec<_>>(), vec!["events.on", "events.emit"]);
        assert_eq!(*heard.lock().unwrap(), vec![json!("pong")]);
    }

    /// A listener's error or panic is the dispatch's failure and nothing else: the connection keeps serving the next dispatch.
    #[test]
    fn a_listener_failure_fails_only_its_dispatch() {
        let host = FactoryHost::new("fe-fail");
        let ext = Extension::new("bus");
        let failures = Arc::new(Mutex::new(Vec::new()));
        let sink = failures.clone();
        let served = host.serve(move |peer| {
            let ids: Vec<String> = (0..3).map(|_| peer.answer(None).1["handlerId"].as_str().unwrap().to_string()).collect();
            let emit = peer.next_call();
            let outcomes = vec![peer.dispatch("d1", &ids[0], "fail", Value::Null), peer.dispatch("d2", &ids[1], "panic", Value::Null), peer.dispatch("d3", &ids[2], "ok", Value::Null)];
            *sink.lock().unwrap() = outcomes;
            peer.reply(&emit, None);
        });
        let bus = ext.events();
        bus.on("fail", |_, _| Err("boom".to_string())).unwrap();
        bus.on("panic", |_, _| panic!("kaboom")).unwrap();
        bus.on("ok", |_, _| Ok(())).unwrap();
        bus.emit("x", Value::Null).unwrap();
        served.join().unwrap();
        assert_eq!(*failures.lock().unwrap(), vec![Some("boom".to_string()), Some("kaboom".to_string()), None]);
    }

    /// An unsubscribe before run reaches the host over the factory's connection.
    #[test]
    fn unsubscribe_reaches_the_host_over_the_factory_connection() {
        let host = FactoryHost::new("fe-off");
        let ext = Extension::new("bus");
        let served = host.serve(|peer| {
            peer.answer(None);
            peer.answer(None);
        });
        ext.events().on("ch", |_, _| Ok(())).unwrap().unsubscribe();
        let peer = served.join().unwrap();
        assert_eq!(peer.calls.iter().map(|(method, _)| method.as_str()).collect::<Vec<_>>(), vec!["events.on", "events.off"]);
        assert_eq!(peer.calls[0].1["handlerId"], peer.calls[1].1["handlerId"]);
    }

    /// An emit on "error" that the host reports nobody heard fails, as EventEmitter throws it. The host holds the factory's own listeners too, so its flag is the only verdict.
    #[test]
    fn an_error_emit_fails_when_the_host_reports_nobody_heard() {
        let host = FactoryHost::new("fe-error");
        let ext = Extension::new("bus");
        let served = host.serve(|peer| {
            peer.answer(Some(json!({"unhandledError": true})));
        });
        let err = ext.events().emit("error", json!({"x": 1})).unwrap_err();
        assert!(err.to_string().contains("unhandled error event"), "{err}");
        served.join().unwrap();
    }

    #[test]
    fn without_a_host_a_factory_emit_fails_and_a_listener_waits_for_run() {
        let _guard = crate::extension::registration_tests::env_lock().lock().unwrap_or_else(|poisoned| poisoned.into_inner());
        unsafe { std::env::remove_var("PIG_EXT_SOCKET") };
        unsafe { std::env::remove_var("GOPI_EXT_SOCKET") };
        let ext = Extension::new("bus");
        let err = ext.events().emit("ch", Value::Null).unwrap_err();
        assert!(err.to_string().contains("PIG_EXT_SOCKET"), "{err}");
        ext.events().on("ch", |_, _| Ok(())).unwrap();
    }

    /// A listener the host dispatches before the extension registers gets a Context whose host calls are the bus's: a nested emit reaches the host, and any other call fails at once instead of waiting for a host that serves only the bus until the extension registers.
    #[test]
    fn a_factory_listener_context_serves_only_the_bus() {
        let host = FactoryHost::new("fe-ctx");
        let failure = Arc::new(Mutex::new(None));
        let ext = Extension::new("bus");
        let slot = failure.clone();
        let served = host.serve(|peer| {
            let (_, on) = peer.answer(None);
            let handler_id = on["handlerId"].as_str().unwrap().to_string();
            let emit = peer.next_call();
            assert_eq!(peer.dispatch("d1", &handler_id, "outer", Value::Null), None);
            peer.reply(&emit, None);
        });
        ext.events()
            .on("outer", move |ctx, _| {
                ctx.events().emit("inner", Value::Null).map_err(|err| err.to_string())?;
                *slot.lock().unwrap() = ctx.get_tools_expanded().err().map(|err| err.to_string());
                Ok(())
            })
            .unwrap();
        ext.events().emit("x", Value::Null).unwrap();
        let peer = served.join().unwrap();
        assert_eq!(peer.calls.iter().map(|(_, args)| args["channel"].clone()).collect::<Vec<_>>()[1..], [json!("x"), json!("inner")]);
        assert!(failure.lock().unwrap().as_deref().is_some_and(|message| message.contains("not registered")), "{:?}", failure.lock().unwrap());
    }

    /// run takes the connection the factory's first call dialled, and the listener the factory registered is not registered a second time: the first frame run sends is the register frame.
    #[test]
    fn run_takes_the_connection_the_factory_dialled_and_does_not_register_the_listener_again() {
        let host = FactoryHost::new("fe-run");
        let ext = Extension::new("bus");
        let served = host.serve(|peer| {
            peer.answer(None);
            let first = peer.conn.read_envelope().unwrap();
            assert_eq!(first.msg_type, "register", "run must not register the factory's listener again");
            peer.conn.write_envelope(&Envelope { msg_type: "ready".into(), ready: Some(serde_json::from_value(json!({"session_name": "", "cwd": "/tmp", "mode": "print", "width": 80, "model": ""})).unwrap()), ..Default::default() }).unwrap();
            peer.conn.write_envelope(&Envelope { msg_type: "shutdown".into(), shutdown: Some(serde_json::from_value(json!({"reason": "test"})).unwrap()), ..Default::default() }).unwrap();
        });
        ext.events().on("ch", |_, _| Ok(())).unwrap();
        let path = host.path.clone();
        ext.run_with_socket(&path).unwrap();
        served.join().unwrap();
        // One connection served the subscription and the run.
        host.listener.set_nonblocking(true).unwrap();
        assert!(host.listener.accept().is_err(), "run dialled a second connection");
    }
}

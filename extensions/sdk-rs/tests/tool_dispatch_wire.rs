//! How a model-issued tool call reaches a tool (`.upstream/v0.99.2/packages/agent/src/agent-loop.ts`), against a fake host:
//! `prepareArguments` runs before the host validates, and a parallel batch starts in source order.
#![cfg(unix)]

use pig_sdk::{Extension, ToolDefinition, ToolResult, empty_schema};
use serde_json::{Value, json};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

static NEXT: AtomicUsize = AtomicUsize::new(0);

fn read(stream: &mut UnixStream) -> Value {
    let mut prefix = [0; 4];
    stream.read_exact(&mut prefix).expect("frame prefix");
    let mut data = vec![0; u32::from_be_bytes(prefix) as usize];
    stream.read_exact(&mut data).expect("frame body");
    serde_json::from_slice(&data).unwrap()
}

fn send(stream: &mut UnixStream, frame: &Value) {
    let raw = frame.to_string();
    stream.write_all(&(raw.len() as u32).to_be_bytes()).unwrap();
    stream.write_all(raw.as_bytes()).unwrap();
}

struct Host {
    stream: UnixStream,
    thread: Option<std::thread::JoinHandle<std::io::Result<()>>>,
    root: std::path::PathBuf,
    register: Value,
}

impl Host {
    fn start(ext: Extension) -> Host {
        let root = std::env::temp_dir().join(format!("pig-dispatch-{}-{}", std::process::id(), NEXT.fetch_add(1, Ordering::Relaxed)));
        std::fs::create_dir(&root).unwrap();
        let socket = root.join("sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let thread = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        stream.set_read_timeout(Some(Duration::from_secs(10))).unwrap();
        let register = read(&mut stream);
        assert_eq!(register["type"], "register");
        send(&mut stream, &json!({"type":"ready","ready":{"state":{},"width":80,"mode":"tui"}}));
        Host { stream, thread: Some(thread), root, register }
    }

    fn next(&mut self) -> Value {
        loop {
            let frame = read(&mut self.stream);
            if frame["type"] != "request_state" {
                return frame;
            }
        }
    }

    fn request(&mut self, id: &str, method: &str, tool: &str, args: Value) {
        send(
            &mut self.stream,
            &json!({"type":"request","id":id,"request":{"method":method,"tool":tool,"tool_call_id":"c","args":args}}),
        );
    }

    fn response(&mut self, id: &str) -> Value {
        let frame = self.next();
        assert_eq!(frame["type"], "response", "frame: {frame}");
        assert_eq!(frame["id"], id);
        frame["response"].clone()
    }

    fn finish(mut self) {
        send(&mut self.stream, &json!({"type":"shutdown","shutdown":{"reason":"done"}}));
        self.thread.take().unwrap().join().unwrap().unwrap();
    }
}

impl Drop for Host {
    fn drop(&mut self) {
        let _ = self.stream.shutdown(std::net::Shutdown::Both);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
        let _ = std::fs::remove_dir_all(&self.root);
    }
}

// agent-loop.ts:707-716 (prepareToolCall) calls tool.prepareArguments before validateToolArguments. The host validates, so it asks the
// extension for the prepared arguments, and tool_call then carries them: the runtime must not prepare them a second time.
#[test]
fn prepare_arguments_runs_for_the_host_and_tool_call_does_not_repeat_it() {
    let mut ext = Extension::new("dispatch");
    let handled: Arc<Mutex<Vec<Value>>> = Arc::new(Mutex::new(Vec::new()));
    let recorded = handled.clone();
    let mut legacy = ToolDefinition::new("legacy", "Legacy", "Echo", json!({"type":"object","required":["text"]}), move |_, params| {
        recorded.lock().unwrap().push(params);
        ToolResult::text("ok")
    });
    legacy.prepare_arguments = Some(Box::new(|params| {
        if params["fail"] == true {
            return Err("prepare exploded".to_string());
        }
        Ok(json!({"text": params["legacy"]}))
    }));
    ext.register_tool(legacy);
    ext.tool("plain", "Plain", empty_schema(), |_, _| ToolResult::text("ok"));
    let mut host = Host::start(ext);
    let tools = host.register["register"]["tools"].as_array().unwrap().clone();
    assert_eq!(tools[0]["prepares_arguments"], true, "{tools:?}");
    assert!(tools[1].get("prepares_arguments").is_none(), "{tools:?}");

    host.request("p1", "tool_prepare_arguments", "legacy", json!({"legacy":"hello"}));
    assert_eq!(host.response("p1")["result"], json!({"text":"hello"}));
    host.request("p2", "tool_prepare_arguments", "legacy", json!({"fail":true}));
    assert_eq!(host.response("p2")["error"]["message"], "prepare exploded");
    host.request("p3", "tool_prepare_arguments", "missing", json!({}));
    assert!(host.response("p3").get("error").is_some_and(|error| !error.is_null()));
    host.request("p4", "tool_prepare_arguments", "plain", json!({}));
    assert!(host.response("p4").get("error").is_some_and(|error| !error.is_null()));

    host.request("c1", "tool_call", "legacy", json!({"text":"prepared","legacy":"raw"}));
    assert!(host.response("c1").get("error").is_none_or(Value::is_null));
    assert_eq!(*handled.lock().unwrap(), vec![json!({"text":"prepared","legacy":"raw"})]);
    host.finish();
}

// agent-loop.ts:619-647 and 820-837: the calls of a parallel batch reach tool.execute in source order. A request that ends before its
// handler (an unknown tool, a panicking hook) hands its place in the order on, so the requests behind it still reach their handlers.
#[test]
fn a_tool_call_that_ends_before_its_handler_hands_on_its_place() {
    let mut ext = Extension::new("dispatch");
    ext.tool("probe", "Probe", empty_schema(), |_, _| ToolResult::text("ok"));
    let mut host = Host::start(ext);
    for round in 0..8 {
        host.request(&format!("m{round}"), "tool_call", "missing", json!({}));
        assert!(host.response(&format!("m{round}")).get("error").is_some_and(|error| !error.is_null()));
        host.request(&format!("p{round}"), "tool_call", "probe", json!({}));
        assert!(host.response(&format!("p{round}")).get("error").is_none_or(Value::is_null));
    }
    host.finish();
}

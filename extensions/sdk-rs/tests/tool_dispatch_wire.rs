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

    /// A `tool_call` of the parallel batch `batch`: the host names the batch in every call of one.
    fn batch_request(&mut self, batch: &str, id: &str, tool: &str, args: Value) {
        send(
            &mut self.stream,
            &json!({"type":"request","id":id,"request":{"method":"tool_call","tool":tool,"tool_call_id":"c","batch":batch,"args":args}}),
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

fn dialog_order_extension() -> Extension {
    let mut ext = Extension::new("dialog-order");
    ext.tool(
        "dialog_order",
        "Dialog order",
        json!({"type": "object", "properties": {"title": {"type": "string"}, "then": {"type": "string"}}}),
        |ctx, params| {
            let title = params["title"].as_str().unwrap_or("").to_string();
            let answer = match ctx.select(&title, &["a", "b"]) {
                Ok((selected, _)) => selected.as_str().unwrap_or("").to_string(),
                Err(error) => return ToolResult::Error(error.to_string()),
            };
            if params["then"] == "host_call" {
                // The continuation goes on to another host call, which is the next await it reaches.
                ctx.set_title(&title);
            } else {
                // The continuation does some work before it returns, so a handler that wakes alongside it would respond first.
                std::thread::sleep(Duration::from_millis(20));
            }
            ToolResult::text(format!("dialog:{title}={answer}"))
        },
    );
    ext
}

/// Reads the `ui.select` calls of a batch: their call ids, in the order they were written, and the requests that made them.
fn select_calls(host: &mut Host, count: usize) -> Vec<(String, String)> {
    (0..count)
        .map(|_| {
            let frame = host.next();
            assert_eq!(frame["type"], "call", "frame: {frame}");
            assert_eq!(frame["call"]["method"], "ui.select", "frame: {frame}");
            (frame["id"].as_str().unwrap().to_string(), frame["call"]["parent_request_id"].as_str().unwrap().to_string())
        })
        .collect()
}

fn answer(host: &mut Host, call_id: &str) {
    send(&mut host.stream, &json!({"type":"call_result","id":call_id,"call_result":{"result":{"selected":"a","ok":true}}}));
}

// agent-loop.ts:586-647 and rpc-mode.ts:91-130, 771-777: the calls of one parallel batch start in source order and each reaches its first await, here a select
// dialog, before the next starts, so the dialogs open in call order. Two answers read back to back resume their tools in answer order, each running to its
// return before the next one resumes, so the responses follow the answers. One thread gives Pi both orders; every request runs on a thread of its own here, so
// the SDK keeps them itself, whatever order the answers come in. The host names the batch in each call.
#[test]
fn a_batch_opens_its_dialogs_in_call_order_and_responds_in_answer_order() {
    const BATCH: usize = 4;
    let mut host = Host::start(dialog_order_extension());
    for round in 0..9 {
        let ids: Vec<String> = (0..BATCH).map(|i| format!("d{round}-{i}")).collect();
        for (i, id) in ids.iter().enumerate() {
            host.batch_request(&format!("batch-{round}"), id, "dialog_order", json!({"title": format!("T{i}")}));
        }
        let calls = select_calls(&mut host, BATCH);
        let parents: Vec<&str> = calls.iter().map(|(_, parent)| parent.as_str()).collect();
        assert_eq!(parents, ids, "round {round}: the dialogs did not open in call order");
        // Forward, reversed and rotated answers, written back to back.
        let answers: Vec<usize> = (0..BATCH)
            .map(|i| match round % 3 {
                0 => BATCH - 1 - i,
                1 => i,
                _ => (i + 1) % BATCH,
            })
            .collect();
        for &call in &answers {
            answer(&mut host, &calls[call].0);
        }
        let responded: Vec<String> = (0..BATCH)
            .map(|_| {
                let frame = host.next();
                assert_eq!(frame["type"], "response", "frame: {frame}");
                frame["id"].as_str().unwrap().to_string()
            })
            .collect();
        let want: Vec<&String> = answers.iter().map(|&call| &ids[call]).collect();
        assert_eq!(responded.iter().collect::<Vec<_>>(), want, "round {round}: the responses did not follow the answers");
    }
    host.finish();
}

// A handler whose answer was read first holds the responses of the later ones only until its continuation reaches its next await: its next host call.
// The host answers that call only after the other handler's response arrived, so a handler that kept its place would wait for a response that waits for it.
#[test]
fn a_handler_that_reached_its_next_host_call_lets_the_ones_behind_it_respond() {
    let mut host = Host::start(dialog_order_extension());
    host.batch_request("batch", "first", "dialog_order", json!({"title": "T0"}));
    host.batch_request("batch", "second", "dialog_order", json!({"title": "T1", "then": "host_call"}));
    let calls = select_calls(&mut host, 2);
    assert_eq!(calls[0].1, "first");
    assert_eq!(calls[1].1, "second");
    // The second dialog is answered first, so the second handler holds the place the first one waits for.
    answer(&mut host, &calls[1].0);
    answer(&mut host, &calls[0].0);
    let (mut title_call, mut first_responded) = (None, false);
    while title_call.is_none() || !first_responded {
        let frame = host.next();
        match frame["type"].as_str() {
            Some("call") => {
                assert_eq!(frame["call"]["method"], "ui.setTitle", "frame: {frame}");
                title_call = Some(frame["id"].as_str().unwrap().to_string());
            }
            Some("response") => {
                assert_eq!(frame["id"], "first", "frame: {frame}");
                first_responded = true;
            }
            other => panic!("unexpected frame {other:?}: {frame}"),
        }
    }
    send(&mut host.stream, &json!({"type":"call_result","id":title_call.unwrap(),"call_result":{}}));
    assert_eq!(host.response("second")["result"]["content"], "dialog:T1=a");
    host.finish();
}

// The orders are one batch's: a long call of one batch that opens no dialog holds no dialog of another batch, nor of a call that names no batch (a subagent's
// tool calls reach the same connection while the tool that runs it has not returned), and such a call is in no order itself. Within its own batch a call still
// answers in the order of the answers.
#[test]
fn a_long_call_of_one_batch_does_not_hold_the_dialogs_of_another_batch() {
    let mut ext = dialog_order_extension();
    let (release, released) = std::sync::mpsc::channel::<()>();
    let released = Mutex::new(released);
    ext.tool("long", "Long", empty_schema(), move |_, _| {
        // It opens no dialog and returns when the test lets it.
        let _ = released.lock().unwrap().recv();
        ToolResult::text("long done")
    });
    let mut host = Host::start(ext);
    host.batch_request("A", "long", "long", json!({}));
    host.batch_request("B", "b0", "dialog_order", json!({"title": "T0"}));
    host.batch_request("B", "b1", "dialog_order", json!({"title": "T1"}));
    host.request("none", "tool_call", "dialog_order", json!({"title": "N"}));
    let calls = select_calls(&mut host, 3);
    let mut parents: Vec<&str> = calls.iter().map(|(_, parent)| parent.as_str()).collect();
    let position = |parents: &[&str], id: &str| parents.iter().position(|parent| *parent == id).unwrap_or_else(|| panic!("no dialog of {id}: {parents:?}"));
    assert!(position(&parents, "b0") < position(&parents, "b1"), "the dialogs of batch B did not open in call order: {parents:?}");
    parents.sort_unstable();
    assert_eq!(parents, ["b0", "b1", "none"]);
    // The answers of batch B come in reverse order, the call outside a batch last.
    for id in ["b1", "b0", "none"] {
        let call = &calls[calls.iter().position(|(_, parent)| parent == id).unwrap()].0;
        answer(&mut host, call);
    }
    let responded: Vec<String> = (0..3)
        .map(|_| {
            let frame = host.next();
            assert_eq!(frame["type"], "response", "frame: {frame}");
            frame["id"].as_str().unwrap().to_string()
        })
        .collect();
    let at = |id: &str| responded.iter().position(|responded| responded == id).unwrap_or_else(|| panic!("no response of {id}: {responded:?}"));
    assert!(at("b1") < at("b0"), "the responses of batch B did not follow the answers: {responded:?}");
    assert!(responded.contains(&"none".to_string()), "{responded:?}");
    release.send(()).unwrap();
    assert_eq!(host.response("long")["result"]["content"], "long done");
    host.finish();
}

// runner.ts:1096-1125: concurrent emits start their handlers in call order, so an event or command request takes a place like a tool_call. A
// request that ends before its handler (an unknown handler id or command) hands its place on, so the requests behind it still reach their
// handlers. The order itself is pinned by `request_start_order::tests`, which hold the places.
#[test]
fn an_event_or_command_that_ends_before_its_handler_hands_on_its_place() {
    let mut ext = Extension::new("dispatch");
    ext.on_event("tool_result", |_, _| None);
    ext.command("probe", "Probe", |_, _| pig_sdk::CommandResult::Ok);
    let mut host = Host::start(ext);
    for round in 0..8 {
        send(
            &mut host.stream,
            &json!({"type":"request","id":format!("e{round}"),"request":{"method":"event","event":"tool_result","handler_id":99,"args":{}}}),
        );
        assert!(host.response(&format!("e{round}")).get("error").is_some_and(|error| !error.is_null()));
        host.request(&format!("m{round}"), "command", "missing", json!(""));
        assert!(host.response(&format!("m{round}")).get("error").is_some_and(|error| !error.is_null()));
        send(
            &mut host.stream,
            &json!({"type":"request","id":format!("ok{round}"),"request":{"method":"event","event":"tool_result","handler_id":1,"args":{}}}),
        );
        assert!(host.response(&format!("ok{round}")).get("error").is_none_or(Value::is_null));
    }
    host.finish();
}

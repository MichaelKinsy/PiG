//! Pi 1.2.0 runner.ts:1426-1450 renderCurrentSystemPrompt against a fake host: in a before_agent_start handler,
//! `event.system_prompt()` (Pi's `event.systemPrompt`) and `ctx.get_system_prompt()` read the prompt the shared options render,
//! so the handler sees its own edit.
#![cfg(unix)]

use pig_sdk::{BeforeAgentStartEvent, Extension};
use serde_json::{Value, json};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::atomic::{AtomicUsize, Ordering};
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

/// A fake host: the accepted socket of one running extension.
struct Host {
    stream: UnixStream,
    thread: Option<std::thread::JoinHandle<std::io::Result<()>>>,
    root: std::path::PathBuf,
}

impl Host {
    fn start(ext: Extension, state: Value) -> Host {
        let root = std::env::temp_dir().join(format!(
            "pig-render-prompt-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        std::fs::create_dir(&root).unwrap();
        let socket = root.join("sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let thread = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        stream.set_read_timeout(Some(Duration::from_secs(10))).unwrap();
        let register = read(&mut stream);
        assert_eq!(register["type"], "register");
        send(&mut stream, &json!({"type":"ready","ready":{"state":state,"width":80,"mode":"tui"}}));
        Host { stream, thread: Some(thread), root }
    }

    /// The next frame that is not a request_state.
    fn next(&mut self) -> Value {
        loop {
            let frame = read(&mut self.stream);
            if frame["type"] != "request_state" {
                return frame;
            }
        }
    }

    fn expect_call(&mut self, method: &str) -> Value {
        let frame = self.next();
        assert_eq!(frame["type"], "call", "frame: {frame}");
        assert_eq!(frame["call"]["method"], method, "frame: {frame}");
        frame
    }

    fn reply(&mut self, call: &Value, result: Value) {
        send(&mut self.stream, &json!({"type":"call_result","id":call["id"],"call_result":{"result":result}}));
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

fn event(host: &mut Host, args: Value) {
    send(
        &mut host.stream,
        &json!({"type":"request","id":"r1","request":{"method":"event","event":"before_agent_start","handler_id":1,"args":args}}),
    );
}

fn response(host: &mut Host) -> Value {
    let frame = host.next();
    assert_eq!(frame["type"], "response", "frame: {frame}");
    frame
}

#[test]
fn before_agent_start_reads_the_prompt_its_current_options_render() {
    let mut ext = Extension::new("rs-render-prompt");
    let (record, seen) = std::sync::mpsc::channel::<String>();
    ext.on_before_agent_start(move |ctx, event| {
        let read = |event: &BeforeAgentStartEvent| -> Result<String, String> {
            Ok(format!("{} {}", event.system_prompt().map_err(|e| e.to_string())?, ctx.get_system_prompt().map_err(|e| e.to_string())?))
        };
        record.send(read(event)?).unwrap();
        event.edit_system_prompt_options(|options| options["appendSystemPrompt"] = json!("EDITED"));
        record.send(read(event)?).unwrap();
        record.send(read(event)?).unwrap();
        Ok(None)
    });
    let mut host = Host::start(ext, json!({}));
    event(&mut host, json!({"prompt":"hi","systemPrompt":"RECEIVED","systemPromptOptions":{"customPrompt":"BASE","sections":{}}}));
    let call = host.expect_call("renderSystemPrompt");
    assert_eq!(call["call"]["args"]["options"]["customPrompt"], "BASE");
    assert_eq!(call["call"]["args"]["options"]["appendSystemPrompt"], "EDITED");
    host.reply(&call, json!({"prompt":"RENDERED"}));
    let reply = response(&mut host);
    assert!(reply["response"]["error"].is_null(), "reply: {reply}");
    assert_eq!(reply["response"]["result"]["_pigPromptOptions"]["appendSystemPrompt"], "EDITED");
    // The unedited options read the prompt the host sent without a call; the edit renders once and is then reused.
    assert_eq!(seen.try_iter().collect::<Vec<_>>(), vec!["RECEIVED RECEIVED", "RENDERED RENDERED", "RENDERED RENDERED"]);
    host.finish();
}

/// The read Go, Python and Node make after an edit: only the context's getter, with no event read in between.
#[test]
fn ctx_get_system_prompt_sees_the_handlers_own_edit() {
    let mut ext = Extension::new("rs-ctx-read");
    let (record, seen) = std::sync::mpsc::channel::<String>();
    ext.on_before_agent_start(move |ctx, event| {
        event.set_system_prompt_options(json!({"customPrompt":"BASE","appendSystemPrompt":"EDITED","sections":{}}));
        record.send(ctx.get_system_prompt().map_err(|e| e.to_string())?).unwrap();
        Ok(None)
    });
    let mut host = Host::start(ext, json!({}));
    event(&mut host, json!({"prompt":"hi","systemPrompt":"RECEIVED","systemPromptOptions":{"customPrompt":"BASE","sections":{}}}));
    let call = host.expect_call("renderSystemPrompt");
    assert_eq!(call["call"]["args"]["options"]["appendSystemPrompt"], "EDITED");
    host.reply(&call, json!({"prompt":"RENDERED"}));
    let reply = response(&mut host);
    assert!(reply["response"]["error"].is_null(), "reply: {reply}");
    assert_eq!(seen.try_iter().collect::<Vec<_>>(), vec!["RENDERED"]);
    host.finish();
}

#[test]
fn the_event_hides_the_options_that_change_and_keeps_the_rest() {
    let mut ext = Extension::new("rs-payload");
    let (record, seen) = std::sync::mpsc::channel::<Value>();
    ext.on_before_agent_start(move |_, event| {
        record.send(event.payload().clone()).unwrap();
        record.send(event.system_prompt_options()).unwrap();
        Ok(None)
    });
    let mut host = Host::start(ext, json!({}));
    event(&mut host, json!({"prompt":"hi","images":[],"systemPrompt":"RECEIVED","systemPromptOptions":{"customPrompt":"BASE"}}));
    let reply = response(&mut host);
    assert!(reply["response"]["error"].is_null(), "reply: {reply}");
    let seen = seen.try_iter().collect::<Vec<_>>();
    assert_eq!(seen[0], json!({"prompt":"hi","images":[]}));
    assert_eq!(seen[1]["customPrompt"], "BASE");
    host.finish();
}

#[test]
fn before_agent_start_registered_through_on_event_fails_instead_of_reading_stale_state() {
    let mut ext = Extension::new("rs-render-prompt-old");
    ext.on_event("before_agent_start", |_, _| None);
    let mut host = Host::start(ext, json!({}));
    event(&mut host, json!({"prompt":"hi","systemPrompt":"RECEIVED","systemPromptOptions":{"sections":{}}}));
    let reply = response(&mut host);
    assert!(reply["response"]["error"]["message"].as_str().unwrap().contains("on_before_agent_start"), "reply: {reply}");
    host.finish();
}

#[test]
fn before_agent_start_render_error_is_the_readers_error() {
    let mut ext = Extension::new("rs-render-prompt-error");
    ext.on_before_agent_start(|ctx, event| {
        event.edit_system_prompt_options(|options| options["sections"]["preamble"] = json!("x"));
        ctx.get_system_prompt().map_err(|e| e.to_string())?;
        Ok(None)
    });
    let mut host = Host::start(ext, json!({}));
    event(&mut host, json!({"prompt":"hi","systemPrompt":"RECEIVED","systemPromptOptions":{"sections":{}}}));
    let call = host.expect_call("renderSystemPrompt");
    send(&mut host.stream, &json!({"type":"call_result","id":call["id"],"call_result":{"error":{"message":"invalid section preamble"}}}));
    let reply = response(&mut host);
    assert!(reply["response"]["error"]["message"].as_str().unwrap().contains("invalid section preamble"), "reply: {reply}");
    host.finish();
}

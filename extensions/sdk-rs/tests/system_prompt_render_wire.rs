//! Pi 1.2.0 runner.ts:1426-1450 renderCurrentSystemPrompt against a fake host: in a before_agent_start handler,
//! `ctx.current_system_prompt(data)` (Pi's `event.systemPrompt`) and `ctx.get_system_prompt()` read the prompt the shared
//! options render, so the handler sees its own edit.
#![cfg(unix)]

use pig_sdk::Extension;
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
    ext.on_event_result("before_agent_start", move |ctx, data| {
        let read = |data: &Value| -> Result<String, String> {
            Ok(format!("{} {}", ctx.current_system_prompt(data).map_err(|e| e.to_string())?, ctx.get_system_prompt().map_err(|e| e.to_string())?))
        };
        record.send(read(data)?).unwrap();
        data["systemPromptOptions"]["appendSystemPrompt"] = json!("EDITED");
        record.send(read(data)?).unwrap();
        record.send(read(data)?).unwrap();
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

#[test]
fn before_agent_start_render_error_is_the_readers_error() {
    let mut ext = Extension::new("rs-render-prompt-error");
    ext.on_event_result("before_agent_start", |ctx, data| {
        data["systemPromptOptions"]["sections"]["preamble"] = json!("x");
        ctx.current_system_prompt(data).map_err(|e| e.to_string())?;
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

#[test]
fn current_system_prompt_outside_before_agent_start_is_an_error() {
    let mut ext = Extension::new("rs-render-prompt-outside");
    ext.on_event_result("turn_start", |ctx, data| Err(ctx.current_system_prompt(data).unwrap_err().to_string()));
    let mut host = Host::start(ext, json!({}));
    send(&mut host.stream, &json!({"type":"request","id":"r1","request":{"method":"event","event":"turn_start","handler_id":1,"args":{}}}));
    let reply = response(&mut host);
    assert_eq!(reply["response"]["error"]["message"], "current_system_prompt is only available in a before_agent_start handler");
    host.finish();
}

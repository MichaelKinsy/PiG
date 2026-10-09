//! Event subscription handles of the Rust SDK against a fake host (`pi.on` returns `() => void`, types.ts:1558).
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
    /// The `register` frame the extension sent.
    register: Value,
}

impl Host {
    fn start(ext: Extension, state: Value) -> Host {
        let root = std::env::temp_dir().join(format!(
            "pig-extapi-{}-{}",
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
        Host { stream, thread: Some(thread), root, register }
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

// `pi.on` returns `() => void` (packages/coding-agent/src/core/extensions/types.ts:1558). The Rust SDK's handle removes a
// declaration before the extension connects and, once connected, tells the host with `event.subscribe` /
// `event.unsubscribe`, as the Go SDK's `Extension.OnEvent` does (extensions/sdk/extension.go).

#[test]
fn unsubscribe_before_connect_removes_the_declaration() {
    let mut ext = Extension::new("rs-unsub-pre");
    let _keep = ext.on_event("turn_start", false, |_, _| None);
    let drop = ext.on_event("turn_end", false, |_, _| None);
    drop.unsubscribe();
    drop.unsubscribe();
    let host = Host::start(ext, json!({}));
    let handlers: Vec<(String, u64)> = host.register["register"]["handlers"]
        .as_array()
        .unwrap()
        .iter()
        .map(|h| (h["event"].as_str().unwrap().to_string(), h["handler_id"].as_u64().unwrap()))
        .collect();
    assert_eq!(handlers, vec![("turn_start".to_string(), 1)]);
    host.finish();
}

#[test]
fn project_trust_registration_returns_its_unsubscribe() {
    let mut ext = Extension::new("rs-unsub-trust");
    let subscription = ext.on_project_trust(|_, _| Err("unused".to_string()));
    subscription.unsubscribe();
    let host = Host::start(ext, json!({}));
    assert_eq!(host.register["register"]["handlers"].as_array().map_or(0, |h| h.len()), 0);
    host.finish();
}

#[test]
fn subscribe_and_unsubscribe_after_connect_are_host_calls() {
    let ext = Extension::new("rs-unsub-live");
    let subscriber = ext.event_subscriber();
    let mut host = Host::start(ext, json!({}));
    let worker = std::thread::spawn(move || {
        let subscription = subscriber.on_event("turn_start", false, |_, _| Ok(None));
        subscription.unsubscribe();
        subscription.unsubscribe();
    });
    let subscribed = host.expect_call("event.subscribe");
    assert_eq!(subscribed["call"]["args"], json!({"event":"turn_start","handlerId":1}));
    host.reply(&subscribed, json!({}));
    let unsubscribed = host.expect_call("event.unsubscribe");
    assert_eq!(unsubscribed["call"]["args"], json!({"event":"turn_start","handlerId":1}));
    host.reply(&unsubscribed, json!({}));
    // The second unsubscribe sent nothing: the worker returns without another call to answer.
    worker.join().unwrap();
    host.finish();
}

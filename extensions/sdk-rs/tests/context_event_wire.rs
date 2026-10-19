//! The reply of the Rust SDK to a `context` event against a fake host.
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

    fn event(&mut self, id: &str, event: &str, handler_id: u64, args: Value) -> Value {
        send(&mut self.stream, &json!({"type":"request","id":id,"request":{"method":"event","event":event,"handler_id":handler_id,"args":args}}));
        let frame = self.next();
        assert_eq!(frame["type"], "response", "frame: {frame}");
        assert_eq!(frame["id"], id);
        frame["response"]["result"].clone()
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

// runner.ts emitContext takes `handlerResult?.messages`, or event.messages when the handler edited that list in place, and
// sameMessages compares the lists by message identity. A Rust handler holds values, not Pi's objects, so it states identity:
// a conversation left as received is unchanged and a list it returns is a replacement, even an equal one; `context_unchanged()`
// says the messages are the ones it received. The reply is the report the other SDKs send, without a list the host holds.

fn messages() -> Value {
    json!([{"role":"user","content":"one","timestamp":0},{"role":"user","content":"two","timestamp":0}])
}

#[test]
fn a_conversation_left_as_received_is_reported_unchanged_without_the_list() {
    let mut ext = Extension::new("rs-context-same");
    ext.on_event("context", |_, _| None);
    ext.on_event("context", |_, _| pig_sdk::context_unchanged());
    ext.on_event("context", |_, data| {
        let list = data["messages"].clone();
        Some(json!({"messages": list, "_pigContextUnchanged": true}))
    });
    ext.on_event("context_with_system", |_, _| None);
    let mut host = Host::start(ext, json!({}));
    let handlers = host.register["register"]["handlers"].as_array().unwrap().clone();
    let id = |n: usize| handlers[n]["handler_id"].as_u64().unwrap();
    for (n, event) in [(0, "context"), (1, "context"), (2, "context"), (3, "context_with_system")] {
        let reply = host.event(&format!("same-{n}"), event, id(n), json!({"type":event,"messages":messages()}));
        assert_eq!(reply, json!({"_pigContextUnchanged": true}), "handler {n}");
    }
    host.finish();
}

#[test]
fn a_returned_list_is_a_replacement_even_when_equal() {
    let mut ext = Extension::new("rs-context-replace");
    ext.on_event("context", |_, data| Some(json!({"messages": data["messages"].clone()})));
    ext.on_event("context", |_, data| {
        let rest = data["messages"].as_array().unwrap()[1..].to_vec();
        Some(json!({"messages": rest}))
    });
    let mut host = Host::start(ext, json!({}));
    let handlers = host.register["register"]["handlers"].as_array().unwrap().clone();
    let equal = host.event("equal", "context", handlers[0]["handler_id"].as_u64().unwrap(), json!({"type":"context","messages":messages()}));
    assert_eq!(equal, json!({"messages": messages(), "_pigContextUnchanged": false}));
    let shorter = host.event("shorter", "context", handlers[1]["handler_id"].as_u64().unwrap(), json!({"type":"context","messages":messages()}));
    assert_eq!(shorter, json!({"messages":[{"role":"user","content":"two","timestamp":0}], "_pigContextUnchanged": false}));
    host.finish();
}

#[test]
fn an_edit_in_place_is_a_replacement_unless_the_handler_says_the_messages_are_its_own() {
    let mut ext = Extension::new("rs-context-edit");
    ext.on_event("context", |_, data| {
        data["messages"][0]["content"] = json!("edited");
        None
    });
    ext.on_event("context", |_, data| {
        data["messages"][0]["content"] = json!("edited");
        pig_sdk::context_unchanged()
    });
    ext.on_event("context", |_, data| {
        data["messages"].as_array_mut().unwrap().reverse();
        pig_sdk::context_unchanged()
    });
    let mut host = Host::start(ext, json!({}));
    let handlers = host.register["register"]["handlers"].as_array().unwrap().clone();
    let edited = json!([{"role":"user","content":"edited","timestamp":0},{"role":"user","content":"two","timestamp":0}]);
    let plain = host.event("plain", "context", handlers[0]["handler_id"].as_u64().unwrap(), json!({"type":"context","messages":messages()}));
    assert_eq!(plain, json!({"messages": edited, "_pigContextUnchanged": false}));
    let own = host.event("own", "context", handlers[1]["handler_id"].as_u64().unwrap(), json!({"type":"context","messages":messages()}));
    assert_eq!(own, json!({"messages": edited, "_pigContextUnchanged": true}));
    let reversed = host.event("reversed", "context", handlers[2]["handler_id"].as_u64().unwrap(), json!({"type":"context","messages":messages()}));
    assert_eq!(reversed["messages"][0]["content"], "two");
    host.finish();
}

//! `Context::set_model` on the wire, against a fake host: Pi's `pi.setModel(model)` (`packages/coding-agent/src/core/extensions/types.ts`
//! `ExtensionAPI.setModel`) takes the model object, and the string form asks the host for the object of the name (`resolveModel`) and sets it.
//! The frame shapes are `SetModelArgs`, `SetModelResult`, `ResolveModelArgs` and `ResolveModelResult` in the host's `protocol_extension_api.go`.
#![cfg(unix)]

use pig_sdk::{CommandResult, Extension, SetModelArg};
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
    fn start(ext: Extension) -> Host {
        let root = std::env::temp_dir().join(format!(
            "pig-setmodel-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        std::fs::create_dir(&root).unwrap();
        let socket = root.join("sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let thread = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(10)))
            .unwrap();
        let register = read(&mut stream);
        assert_eq!(register["type"], "register");
        send(
            &mut stream,
            &json!({"type":"ready","ready":{"state":{},"width":80,"mode":"tui"}}),
        );
        Host {
            stream,
            thread: Some(thread),
            root,
        }
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
        send(
            &mut self.stream,
            &json!({"type":"call_result","id":call["id"],"call_result":{"result":result}}),
        );
    }

    fn reply_error(&mut self, call: &Value, message: &str) {
        send(
            &mut self.stream,
            &json!({"type":"call_result","id":call["id"],"call_result":{"error":{"message":message}}}),
        );
    }

    fn command(&mut self, id: &str, name: &str, args: &str) {
        send(
            &mut self.stream,
            &json!({"type":"request","id":id,"request":{"method":"command","tool":name,"args":args}}),
        );
    }

    /// The answer the command's handler reported: the message of its response's error.
    fn answer(&mut self, id: &str) -> String {
        let frame = self.next();
        assert_eq!(frame["type"], "response", "frame: {frame}");
        assert_eq!(frame["id"], id);
        frame["response"]["error"]["message"]
            .as_str()
            .unwrap_or_else(|| panic!("frame: {frame}"))
            .to_string()
    }

    fn finish(mut self) {
        send(
            &mut self.stream,
            &json!({"type":"shutdown","shutdown":{"reason":"done"}}),
        );
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

fn extension() -> Extension {
    let mut ext = Extension::new("set-model");
    ext.command("object", "set a model object", |ctx, args| {
        let model: Value = serde_json::from_str(args).unwrap();
        let (ok, error) = ctx.set_model(&model);
        // The answer travels as the command's error so the test reads it from the response frame.
        CommandResult::Error(format!("{ok}|{error}"))
    });
    ext.command("named", "set a model by name", |ctx, args| {
        let (ok, error) = ctx.set_model(args);
        // The answer travels as the command's error so the test reads it from the response frame.
        CommandResult::Error(format!("{ok}|{error}"))
    });
    ext
}

#[test]
fn every_argument_form_converts() {
    let object = json!({"provider": "acme", "id": "chat-1"});
    assert_eq!(
        SetModelArg::from(&object),
        SetModelArg::Object(object.clone())
    );
    assert_eq!(
        SetModelArg::from(object.clone()),
        SetModelArg::Object(object)
    );
    let named = SetModelArg::Named("acme/chat-1".to_string());
    assert_eq!(SetModelArg::from("acme/chat-1"), named);
    assert_eq!(SetModelArg::from("acme/chat-1".to_string()), named);
    assert_eq!(SetModelArg::from(&"acme/chat-1".to_string()), named);
}

#[test]
fn object_is_sent_as_given_and_the_answer_returned() {
    let mut host = Host::start(extension());
    let object = r#"{"id":"chat-1","provider":"acme","api":"openai-completions","baseUrl":"http://gateway/v1","contextWindow":4321,"extra":{"kept":true}}"#;
    host.command("c1", "object", object);
    let call = host.expect_call("setModel");
    assert_eq!(
        call["call"]["args"],
        json!({"model": serde_json::from_str::<Value>(object).unwrap()})
    );
    // The members keep the order the extension gave them.
    assert_eq!(call["call"]["args"]["model"].to_string(), object);
    host.reply(&call, json!({"success": true}));
    assert_eq!(host.answer("c1"), "true|");

    host.command("c2", "object", object);
    let call = host.expect_call("setModel");
    host.reply(&call, json!({"success": false}));
    assert_eq!(host.answer("c2"), "false|");

    // The host's rejection of a value that is not a chat model object is the error.
    host.command(
        "c3",
        "object",
        r#"{"type":"image","id":"i","provider":"acme"}"#,
    );
    let call = host.expect_call("setModel");
    host.reply_error(&call, "setModel: model must be a chat model object");
    assert_eq!(
        host.answer("c3"),
        "false|setModel: model must be a chat model object"
    );
    host.finish();
}

#[test]
fn name_is_resolved_and_the_resolved_object_is_set() {
    let mut host = Host::start(extension());
    host.command("c1", "named", "acme/chat-1");
    let call = host.expect_call("resolveModel");
    assert_eq!(call["call"]["args"], json!({"spec": "acme/chat-1"}));
    host.reply(
        &call,
        json!({"model": {"id": "chat-1", "provider": "acme", "contextWindow": 4321}}),
    );
    let call = host.expect_call("setModel");
    assert_eq!(
        call["call"]["args"],
        json!({"model": {"id": "chat-1", "provider": "acme", "contextWindow": 4321}})
    );
    host.reply(&call, json!({"success": true}));
    assert_eq!(host.answer("c1"), "true|");
    host.finish();
}

#[test]
fn resolve_error_is_the_answer_and_no_set_is_sent() {
    let mut host = Host::start(extension());
    host.command("c1", "named", "acme/unknown");
    let call = host.expect_call("resolveModel");
    host.reply(&call, json!({"error": "model switching not configured"}));
    // The next frame is the command's response: no setModel was sent.
    assert_eq!(host.answer("c1"), "false|model switching not configured");

    host.command("c2", "named", "acme/unknown");
    let call = host.expect_call("resolveModel");
    host.reply_error(&call, "resolveModel refused");
    assert_eq!(host.answer("c2"), "false|resolveModel refused");
    host.finish();
}

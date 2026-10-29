#![cfg(unix)]

use pig_sdk::{
    Extension, JsString, RemoteComponent, RemoteComponentResult, ToolResult, empty_schema,
};
use serde_json::{Value, json};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::{
    Arc,
    atomic::{AtomicBool, Ordering},
};

struct Echo {
    units: Vec<u16>,
    disposed: Arc<AtomicBool>,
}
impl RemoteComponent for Echo {
    fn render(&self, _: u32) -> Vec<String> {
        vec!["input".into()]
    }
    fn handle_input(&mut self, data: &JsString) -> Result<RemoteComponentResult, String> {
        self.units.extend_from_slice(data.as_units());
        if self.units.len() == 4 {
            Ok(RemoteComponentResult::done(Some(json!(self.units))))
        } else {
            Ok(RemoteComponentResult::pending())
        }
    }
    fn dispose(&mut self) {
        self.disposed.store(true, Ordering::Release);
    }
}
fn read(stream: &mut UnixStream) -> Value {
    let mut prefix = [0; 4];
    stream.read_exact(&mut prefix).unwrap();
    let size = u32::from_be_bytes(prefix) as usize;
    assert!(size <= 65536);
    let mut data = vec![0; size];
    stream.read_exact(&mut data).unwrap();
    serde_json::from_slice(&data).unwrap()
}
fn send(stream: &mut UnixStream, raw: &str) {
    stream.write_all(&(raw.len() as u32).to_be_bytes()).unwrap();
    stream.write_all(raw.as_bytes()).unwrap();
}
struct Runtime {
    stream: UnixStream,
    thread: Option<std::thread::JoinHandle<std::io::Result<()>>>,
    root: std::path::PathBuf,
}
impl Drop for Runtime {
    fn drop(&mut self) {
        let _ = self.stream.shutdown(std::net::Shutdown::Both);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
        let _ = std::fs::remove_dir_all(&self.root);
    }
}

// Pi stdin-buffer.ts:251 emits individual halves. tui.ts:1080-1082 sends the
// listener result to the focused component without converting its JS string.
#[test]
fn custom_component_receives_lone_units_without_disconnect() {
    let root = std::env::temp_dir().join(format!("pig-custom-surrogate-{}", std::process::id()));
    std::fs::create_dir(&root).unwrap();
    let socket = root.join("sock");
    let listener = UnixListener::bind(&socket).unwrap();
    let disposed = Arc::new(AtomicBool::new(false));
    let mut ext = Extension::new("custom-surrogate");
    let observer = disposed.clone();
    ext.tool(
        "custom",
        "Read units",
        empty_schema(),
        move |ctx, _| match ctx.custom_component(
            Echo {
                units: vec![],
                disposed: observer.clone(),
            },
            json!({}),
        ) {
            Ok(value) => ToolResult::json(value.unwrap_or(Value::Null)),
            Err(err) => ToolResult::Error(err.to_string()),
        },
    );
    let thread = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
    let (stream, _) = listener.accept().unwrap();
    // A missing frame fails the test after 10 s instead of hanging it.
    stream.set_read_timeout(Some(std::time::Duration::from_secs(10))).unwrap();
    let mut runtime = Runtime {
        stream,
        thread: Some(thread),
        root,
    };
    assert_eq!(read(&mut runtime.stream)["type"], "register");
    send(
        &mut runtime.stream,
        r#"{"type":"ready","ready":{"state":{"hasUI":true},"width":80}}"#,
    );
    send(
        &mut runtime.stream,
        r#"{"type":"request","id":"input","request":{"method":"tool_call","tool":"custom","args":{}}}"#,
    );
    let call = loop {
        let frame = read(&mut runtime.stream);
        if frame["type"] == "call" {
            break frame;
        }
        assert_eq!(frame["type"], "request_state");
    };
    assert_eq!(call["call"]["method"], "ui.custom");
    let key = call["call"]["args"]["key"].as_str().unwrap();
    for text in [r#""\ud83d""#, r#""\ude00""#, r#""😀""#] {
        send(
            &mut runtime.stream,
            &format!(
                r#"{{"type":"notify","notify":{{"method":"ui.custom.input","args":{{"key":{},"data":{text}}}}}}}"#,
                serde_json::to_string(key).unwrap()
            ),
        );
    }
    loop {
        let frame = read(&mut runtime.stream);
        assert_eq!(frame["type"], "notify");
        if frame["notify"]["method"] == "ui.custom.close" {
            assert_eq!(
                frame["notify"]["args"]["result"],
                json!([0xd83d, 0xde00, 0xd83d, 0xde00])
            );
            break;
        }
        assert_eq!(frame["notify"]["method"], "ui.custom.render");
    }
    send(&mut runtime.stream, &json!({"type":"call_result","id":call["id"],"call_result":{"result":{"ok":true,"result":[0xd83d,0xde00,0xd83d,0xde00]}}}).to_string());
    loop {
        let frame = read(&mut runtime.stream);
        if frame["type"] == "response" {
            assert_eq!(
                frame["response"]["result"],
                json!([0xd83d, 0xde00, 0xd83d, 0xde00])
            );
            break;
        }
        assert_eq!(frame["type"], "request_state");
    }
    assert!(disposed.load(Ordering::Acquire));
    send(
        &mut runtime.stream,
        r#"{"type":"shutdown","shutdown":{"reason":"done"}}"#,
    );
    runtime.thread.take().unwrap().join().unwrap().unwrap();
}

struct Viewport {
    captures: bool,
}
impl RemoteComponent for Viewport {
    fn render(&self, _: u32) -> Vec<String> {
        vec!["viewport probe".into()]
    }
    fn captures_viewport_input(&self) -> bool {
        self.captures
    }
    fn handle_input(&mut self, data: &JsString) -> Result<RemoteComponentResult, String> {
        match data.to_string().as_deref() {
            Some("t") => {
                self.captures = false;
                Ok(RemoteComponentResult::pending())
            }
            Some("q") => Ok(RemoteComponentResult::done(Some(json!("viewport=done")))),
            _ => Ok(RemoteComponentResult::pending()),
        }
    }
}

// Component.capturesViewportInput rides the ui.custom.render frame as `capturesViewportInput: true` and is omitted when false; a frame
// whose lines did not change still goes out when only the flag changed.
#[test]
fn custom_frame_carries_captures_viewport_input_and_resends_when_only_it_changed() {
    let root = std::env::temp_dir().join(format!("pig-custom-viewport-{}", std::process::id()));
    std::fs::create_dir(&root).unwrap();
    let socket = root.join("sock");
    let listener = UnixListener::bind(&socket).unwrap();
    let mut ext = Extension::new("custom-viewport");
    ext.tool("custom", "Viewport", empty_schema(), |ctx, _| match ctx.custom_component(Viewport { captures: true }, json!({})) {
        Ok(value) => ToolResult::json(value.unwrap_or(Value::Null)),
        Err(err) => ToolResult::Error(err.to_string()),
    });
    let thread = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
    let (stream, _) = listener.accept().unwrap();
    stream.set_read_timeout(Some(std::time::Duration::from_secs(10))).unwrap();
    let mut runtime = Runtime { stream, thread: Some(thread), root };
    assert_eq!(read(&mut runtime.stream)["type"], "register");
    send(&mut runtime.stream, r#"{"type":"ready","ready":{"state":{"hasUI":true},"width":80}}"#);
    send(&mut runtime.stream, r#"{"type":"request","id":"v","request":{"method":"tool_call","tool":"custom","args":{}}}"#);
    let call = loop {
        let frame = read(&mut runtime.stream);
        if frame["type"] == "call" {
            break frame;
        }
        assert_eq!(frame["type"], "request_state");
    };
    let key = call["call"]["args"]["key"].as_str().unwrap().to_string();
    let next_render = |stream: &mut UnixStream| loop {
        let frame = read(stream);
        if frame["type"] == "notify" && frame["notify"]["method"] == "ui.custom.render" {
            return frame["notify"]["args"].clone();
        }
    };
    let input = |stream: &mut UnixStream, data: &str| {
        send(stream, &json!({"type":"notify","notify":{"method":"ui.custom.input","args":{"key":key,"data":data}}}).to_string());
    };
    let first = next_render(&mut runtime.stream);
    assert_eq!(first["lines"], json!(["viewport probe"]));
    assert_eq!(first["capturesViewportInput"], json!(true));
    input(&mut runtime.stream, "t");
    let second = next_render(&mut runtime.stream);
    assert_eq!(second["lines"], json!(["viewport probe"]));
    assert!(second.get("capturesViewportInput").is_none(), "false is omitted: {second}");
    input(&mut runtime.stream, "q");
    loop {
        let frame = read(&mut runtime.stream);
        if frame["type"] == "notify" && frame["notify"]["method"] == "ui.custom.close" {
            assert_eq!(frame["notify"]["args"]["result"], json!("viewport=done"));
            break;
        }
    }
    send(&mut runtime.stream, &json!({"type":"call_result","id":call["id"],"call_result":{"result":{"ok":true,"result":"viewport=done"}}}).to_string());
    send(&mut runtime.stream, r#"{"type":"shutdown","shutdown":{"reason":"done"}}"#);
    runtime.thread.take().unwrap().join().unwrap().unwrap();
}

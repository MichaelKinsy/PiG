#![cfg(unix)]

//! The wire of `TuiAltScreen` (D204): `tui.alt` calls carry the terminal block and the component caps, and the host's `tui.alt.cb`
//! requests are answered on their own request threads while the extension's thread is blocked in a `tui.alt` call.

use pig_sdk::{
    Component, ComponentRef, CopySelectionOutcome, Extension, ProgramStatus, Terminal, TerminalInputFn, TerminalResizeFn, ToolResult, TuiAltScreen,
    TuiAltScreenOptions, WheelScrollLines, empty_schema,
};
use serde_json::{Value, json};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::{Arc, Mutex};

fn read(stream: &mut UnixStream) -> Value {
    let mut prefix = [0; 4];
    stream.read_exact(&mut prefix).unwrap();
    let mut data = vec![0; u32::from_be_bytes(prefix) as usize];
    stream.read_exact(&mut data).unwrap();
    serde_json::from_slice(&data).unwrap()
}

fn send(stream: &mut UnixStream, frame: Value) {
    let raw = frame.to_string();
    stream.write_all(&(raw.len() as u32).to_be_bytes()).unwrap();
    stream.write_all(raw.as_bytes()).unwrap();
}

/// The next frame that is not a request_state report.
fn next(stream: &mut UnixStream) -> Value {
    loop {
        let frame = read(stream);
        if frame["type"] != "request_state" {
            return frame;
        }
    }
}

fn request(stream: &mut UnixStream, id: &str, method: &str, tool: &str, args: Value) {
    send(stream, json!({"type": "request", "id": id, "request": {"method": method, "tool": tool, "args": args}}));
}

fn answer(stream: &mut UnixStream, call: &Value, result: Value) {
    send(stream, json!({"type": "call_result", "id": call["id"], "call_result": {"result": result}}));
}

struct Runtime {
    stream: UnixStream,
    thread: Option<std::thread::JoinHandle<std::io::Result<()>>>,
    root: std::path::PathBuf,
}

impl Runtime {
    fn start(name: &str, ext: Extension) -> Self {
        let root = std::env::temp_dir().join(format!("pig-tui-alt-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir(&root).unwrap();
        let socket = root.join("sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let thread = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        // A missing frame fails the test after 10 s instead of hanging it.
        stream.set_read_timeout(Some(std::time::Duration::from_secs(10))).unwrap();
        assert_eq!(read(&mut stream)["type"], "register");
        send(&mut stream, json!({"type": "ready", "ready": {"state": {"hasUI": true}, "width": 80}}));
        Self { stream, thread: Some(thread), root }
    }

    fn shutdown(mut self) {
        send(&mut self.stream, json!({"type": "shutdown", "shutdown": {"reason": "done"}}));
        self.thread.take().unwrap().join().unwrap().unwrap();
    }
}

impl Drop for Runtime {
    fn drop(&mut self) {
        let _ = self.stream.shutdown(std::net::Shutdown::Both);
        if let Some(thread) = self.thread.take().filter(|_| !std::thread::panicking()) {
            let _ = thread.join();
        }
        let _ = std::fs::remove_dir_all(&self.root);
    }
}

#[derive(Default)]
struct Recording {
    writes: Mutex<Vec<String>>,
    statuses: Mutex<Vec<ProgramStatus>>,
}

impl Terminal for Recording {
    fn start(&self, _on_input: TerminalInputFn, _on_resize: TerminalResizeFn) {}
    fn stop(&self) {}
    fn drain_input(&self, _max_ms: u64, _idle_ms: u64) {}
    fn write(&self, data: &str) {
        self.writes.lock().unwrap().push(data.to_string());
    }
    fn columns(&self) -> u32 {
        20
    }
    fn rows(&self) -> u32 {
        6
    }
    fn kitty_protocol_active(&self) -> bool {
        false
    }
    fn move_by(&self, _lines: i32) {}
    fn hide_cursor(&self) {}
    fn show_cursor(&self) {}
    fn clear_line(&self) {}
    fn clear_from_cursor(&self) {}
    fn clear_screen(&self) {}
    fn set_title(&self, _title: &str) {}
    fn set_progress(&self, _active: bool) {}
    fn set_program_status(&self, status: &ProgramStatus) {
        self.statuses.lock().unwrap().push(status.clone());
    }
}

#[derive(Default)]
struct Doc {
    inputs: Mutex<Vec<String>>,
    focused: Mutex<Vec<bool>>,
}

impl Component for Doc {
    fn render(&self, width: u32) -> Vec<String> {
        vec![format!("w={width}")]
    }
    fn handles_input(&self) -> bool {
        true
    }
    fn handle_input(&self, data: &str) {
        self.inputs.lock().unwrap().push(data.to_string());
    }
    fn is_focusable(&self) -> bool {
        true
    }
    fn set_focused(&self, focused: bool) {
        self.focused.lock().unwrap().push(focused);
    }
}

#[test]
fn calls_carry_the_terminal_block_and_callbacks_run_while_a_call_is_blocked() {
    let terminal = Arc::new(Recording::default());
    let doc = Arc::new(Doc::default());
    let mut ext = Extension::new("tui-alt-wire");
    let (handler_terminal, handler_doc) = (terminal.clone(), doc.clone());
    ext.tool("alt", "Drive a TuiAltScreen", empty_schema(), move |ctx, _| {
        let run = || -> std::io::Result<String> {
            let screen = TuiAltScreen::new(
                ctx,
                handler_terminal.clone(),
                TuiAltScreenOptions {
                    mouse: Some(false),
                    wheel_scroll_lines: Some(WheelScrollLines::Lines(3.0)),
                    copy_selection: Some(Arc::new(|text: &str| {
                        if text == "ok" { CopySelectionOutcome::Copied } else { CopySelectionOutcome::Message(format!("no: {text}")) }
                    })),
                    ..Default::default()
                },
            )?;
            let component: ComponentRef = handler_doc.clone();
            screen.add_child(&component)?;
            screen.set_focus(Some(&component))?;
            screen.scroll_by(-2)?;
            Ok(screen.viewport_top()?.to_string())
        };
        match run() {
            Ok(top) => ToolResult::text(top),
            Err(err) => ToolResult::Error(err.to_string()),
        }
    });
    let mut rt = Runtime::start("wire", ext);
    let s = &mut rt.stream;
    request(s, "alt", "tool_call", "alt", json!({}));

    let create = next(s);
    assert_eq!(create["call"]["method"], "tui.alt");
    assert_eq!(
        create["call"]["args"],
        json!({
            "id": "t1", "op": "create",
            "options": {"wheelScrollLines": 3, "mouse": false, "callbacks": ["copySelection"]},
            "terminal": {"columns": 20, "rows": 6, "kitty": false},
        })
    );
    answer(s, &create, json!({}));

    // The extension's thread is now blocked in addChild; the host's callbacks still run.
    let add = next(s);
    assert_eq!(
        add["call"]["args"],
        json!({
            "id": "t1", "op": "addChild", "key": "c1",
            "caps": {"input": true, "mouse": false, "focusable": true, "wantsKeyRelease": false},
            "terminal": {"columns": 20, "rows": 6, "kitty": false},
        })
    );
    let callback = |s: &mut UnixStream, id: &str, args: Value| {
        request(s, id, "tui.alt.cb", "", args);
        let response = next(s);
        assert_eq!((response["type"].as_str(), response["id"].as_str()), (Some("response"), Some(id)), "{response}");
        response["response"].clone()
    };
    assert_eq!(callback(s, "cb1", json!({"id": "t1", "kind": "render", "key": "c1", "width": 7}))["result"], json!({"lines": ["w=7"]}));
    assert_eq!(callback(s, "cb2", json!({"id": "t1", "kind": "input", "key": "c1", "data": "x"}))["result"], json!({}));
    assert_eq!(callback(s, "cb3", json!({"id": "t1", "kind": "focus", "key": "c1", "focused": true}))["result"], json!({}));
    assert_eq!(callback(s, "cb4", json!({"id": "t1", "kind": "terminal", "method": "write", "data": "\u{1b}[?1049h"}))["result"], json!({}));
    assert_eq!(
        callback(s, "cb5", json!({"id": "t1", "kind": "terminal", "method": "setProgramStatus", "status": {"state": "blocked", "kind": "auth"}}))["result"],
        json!({})
    );
    assert_eq!(callback(s, "cb6", json!({"id": "t1", "kind": "option", "name": "copySelection", "args": ["ok"]}))["result"], json!({"value": true}));
    assert_eq!(callback(s, "cb7", json!({"id": "t1", "kind": "option", "name": "copySelection", "args": ["bad"]}))["result"], json!({"value": "no: bad"}));
    assert_eq!(callback(s, "cb8", json!({"id": "t1", "kind": "listener", "listenerId": "l9", "data": "x"}))["result"], json!({}));
    let unknown = callback(s, "cb9", json!({"id": "t9", "kind": "debug"}));
    assert!(unknown["error"]["message"].as_str().unwrap().contains("unknown TuiAltScreen"), "{unknown}");
    answer(s, &add, json!({}));

    let focus = next(s);
    assert_eq!(focus["call"]["args"]["op"], "setFocus");
    assert_eq!(focus["call"]["args"]["key"], "c1", "the component keeps its key");
    assert_eq!(focus["call"]["args"]["caps"]["focusable"], true);
    answer(s, &focus, json!({}));

    let scroll = next(s);
    assert_eq!((scroll["call"]["args"]["op"].as_str(), scroll["call"]["args"]["lines"].as_i64()), (Some("scrollBy"), Some(-2)));
    answer(s, &scroll, json!({}));

    let state = next(s);
    assert_eq!(state["call"]["args"]["op"], "state");
    answer(s, &state, json!({"viewportTop": 4}));

    // Dropping the last handle disposes the host's screen; dispose carries no terminal block.
    let dispose = next(s);
    assert_eq!(dispose["call"]["args"], json!({"id": "t1", "op": "dispose"}));
    answer(s, &dispose, json!({}));
    assert_eq!(next(s)["type"], "response");
    rt.shutdown();

    assert_eq!(*doc.inputs.lock().unwrap(), vec!["x".to_string()]);
    assert_eq!(*doc.focused.lock().unwrap(), vec![true]);
    assert_eq!(*terminal.writes.lock().unwrap(), vec!["\u{1b}[?1049h".to_string()]);
    let statuses = terminal.statuses.lock().unwrap();
    assert_eq!(statuses.len(), 1);
    assert_eq!((statuses[0].state.as_str(), statuses[0].kind.as_deref(), statuses[0].app.as_deref()), ("blocked", Some("auth"), None));
}

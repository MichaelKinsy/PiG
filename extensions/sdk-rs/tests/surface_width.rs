// SPDX-License-Identifier: MIT

//! Header and footer rows carry the width they were laid out for (issue #104).
//!
//! upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:2418-2480
//! installs a footer or header as a component factory the TUI renders at the
//! current width every frame. A subprocess cannot be called every frame, so the
//! SDK sends the width each set of rows was laid out for and the host never
//! paints rows for another width.
//! upstream: interactive-mode.ts:2321-2336 lays a string list widget out with
//! Text(line, 1, 0) at the current width, so the SDK sends it as a widget_push
//! with no width, which the host lays out instead of painting it as a
//! pre-rendered frame.

#![cfg(unix)]

use pig_sdk::{CommandResult, Extension};
use serde_json::{Value, json};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::PathBuf;
use std::sync::mpsc::{Receiver, RecvTimeoutError, channel};
use std::sync::{Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::Duration;

struct Host {
    write: Arc<Mutex<UnixStream>>,
    calls: Receiver<(String, Value)>,
    runner: Option<JoinHandle<std::io::Result<()>>>,
    dir: PathBuf,
}

fn read_frame(stream: &mut UnixStream) -> Option<Value> {
    let mut prefix = [0; 4];
    stream.read_exact(&mut prefix).ok()?;
    let mut bytes = vec![0; u32::from_be_bytes(prefix) as usize];
    stream.read_exact(&mut bytes).ok()?;
    serde_json::from_slice(&bytes).ok()
}

fn write_frame(stream: &Mutex<UnixStream>, frame: &Value) {
    let bytes = serde_json::to_vec(frame).unwrap();
    let mut framed = (bytes.len() as u32).to_be_bytes().to_vec();
    framed.extend(bytes);
    let _ = stream.lock().unwrap().write_all(&framed);
}

impl Host {
    /// Runs `ext` against a host that answers every call and records the calls in order.
    fn start(ext: Extension) -> Self {
        Self::start_in(ext, "tui", true)
    }

    /// `start` with the mode and hasUI the host reports: Pi runs a footer or header factory only in the TUI.
    fn start_in(ext: Extension, mode: &str, has_ui: bool) -> Self {
        let dir = std::env::temp_dir().join(format!("pig-surface-width-{}-{:?}", std::process::id(), thread::current().id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir(&dir).unwrap();
        let socket = dir.join("host.sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let runner = thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        let write = Arc::new(Mutex::new(stream.try_clone().unwrap()));
        assert_eq!(read_frame(&mut stream).unwrap()["type"], "register");
        write_frame(&write, &json!({"type": "ready", "ready": {"cwd": "/tmp", "width": 80, "mode": mode, "state": {"hasUI": has_ui}}}));
        let (tx, calls) = channel();
        let reply = write.clone();
        thread::spawn(move || {
            while let Some(frame) = read_frame(&mut stream) {
                if frame["type"] == "call" {
                    let method = frame["call"]["method"].as_str().unwrap().to_string();
                    let _ = tx.send((method, frame["call"]["args"].clone()));
                    write_frame(&reply, &json!({"type": "call_result", "id": frame["id"], "call_result": {}}));
                }
                // A widget_push has no reply; it is recorded as a call named
                // widget_push whose arguments are the payload.
                if frame["type"] == "widget_push" {
                    let _ = tx.send(("widget_push".to_string(), frame["widget_push"].clone()));
                }
            }
        });
        Self { write, calls, runner: Some(runner), dir }
    }

    fn command(&self, name: &str) {
        write_frame(&self.write, &json!({"type": "request", "id": format!("req-{name}"), "request": {"method": "command", "tool": name, "args": {}}}));
    }

    fn width(&self, width: u32) {
        write_frame(&self.write, &json!({"type": "notify", "notify": {"method": "width_change", "args": {"width": width}}}));
    }

    fn next(&self, method: &str) -> Value {
        loop {
            let (got, args) = self.calls.recv_timeout(Duration::from_secs(5)).unwrap_or_else(|_| panic!("no {method} call within the deadline"));
            if got == method {
                return args;
            }
        }
    }

    fn quiet(&self, method: &str) {
        match self.calls.recv_timeout(Duration::from_millis(300)) {
            Err(RecvTimeoutError::Timeout) => {}
            Ok((got, args)) => assert_ne!(got, method, "unexpected {method} call: {args}"),
            Err(error) => panic!("host stopped: {error}"),
        }
    }
}

impl Drop for Host {
    fn drop(&mut self) {
        write_frame(&self.write, &json!({"type": "shutdown", "shutdown": {"reason": "test"}}));
        // A failed assertion may leave the extension blocked; report the
        // failure instead of waiting for it forever.
        if thread::panicking() {
            return;
        }
        if let Some(runner) = self.runner.take() {
            let _ = runner.join();
        }
        let _ = std::fs::remove_dir_all(&self.dir);
    }
}

#[test]
fn static_rows_are_tagged_with_the_sdk_width() {
    for (surface, method) in [("footer", "ui.setFooter"), ("header", "ui.setHeader")] {
        let mut ext = Extension::new("rs-surface");
        ext.command("go", "push rows", move |ctx, _| {
            let rows = vec![format!("rows@{}", ctx.width())];
            let result = if surface == "footer" { ctx.set_footer(rows) } else { ctx.set_header(rows) };
            result.map_or_else(|e| CommandResult::Error(e.to_string()), |()| CommandResult::Ok)
        });
        let host = Host::start(ext);
        host.command("go");
        assert_eq!(host.next(method), json!({"lines": ["rows@80"], "width": 80}), "{surface}");
        host.width(60);
        host.command("go");
        assert_eq!(host.next(method), json!({"lines": ["rows@60"], "width": 60}), "{surface}");
    }
}

#[test]
fn renderers_render_at_host_width_and_follow_resize() {
    for (surface, method) in [("footer", "ui.setFooter"), ("header", "ui.setHeader")] {
        let mut ext = Extension::new("rs-surface");
        ext.command("go", "install renderer", move |ctx, _| {
            let render = |width: u32| vec![format!("row@{width}")];
            let result = if surface == "footer" { ctx.set_footer_renderer(Some(render)) } else { ctx.set_header_renderer(Some(render)) };
            result.map_or_else(|e| CommandResult::Error(e.to_string()), |()| CommandResult::Ok)
        });
        ext.command("clear", "clear renderer", move |ctx, _| {
            let none: Option<fn(u32) -> Vec<String>> = None;
            let result = if surface == "footer" { ctx.set_footer_renderer(none) } else { ctx.set_header_renderer(none) };
            result.map_or_else(|e| CommandResult::Error(e.to_string()), |()| CommandResult::Ok)
        });
        let host = Host::start(ext);
        host.command("go");
        assert_eq!(host.next(method), json!({"lines": ["row@80"], "width": 80}), "{surface}");
        // No extension action: the SDK re-renders at the host's new width.
        host.width(60);
        assert_eq!(host.next(method), json!({"lines": ["row@60"], "width": 60}), "{surface}");
        host.width(114);
        assert_eq!(host.next(method), json!({"lines": ["row@114"], "width": 114}), "{surface}");
        host.command("clear");
        assert_eq!(host.next(method), json!({"clear": true}), "{surface}");
        host.width(90);
        host.quiet(method);
    }
}

#[test]
fn static_footer_replaces_renderer() {
    let mut ext = Extension::new("rs-surface");
    ext.command("go", "install renderer", |ctx, _| {
        ctx.set_footer_renderer(Some(|width: u32| vec![format!("dyn@{width}")])).map_or_else(|e| CommandResult::Error(e.to_string()), |()| CommandResult::Ok)
    });
    ext.command("static", "static rows", |ctx, _| {
        ctx.set_footer(vec!["static".to_string()]).map_or_else(|e| CommandResult::Error(e.to_string()), |()| CommandResult::Ok)
    });
    let host = Host::start(ext);
    host.command("go");
    host.next("ui.setFooter");
    host.command("static");
    assert_eq!(host.next("ui.setFooter"), json!({"lines": ["static"], "width": 80}));
    host.width(60);
    host.quiet("ui.setFooter");
}

#[test]
fn renderer_panic_is_reported_not_fatal() {
    let mut ext = Extension::new("rs-surface");
    ext.command("go", "install renderer", |ctx, _| {
        let render = |width: u32| {
            assert_eq!(width, 80, "boom");
            vec!["ok".to_string()]
        };
        ctx.set_footer_renderer(Some(render)).map_or_else(|e| CommandResult::Error(e.to_string()), |()| CommandResult::Ok)
    });
    let host = Host::start(ext);
    host.command("go");
    host.next("ui.setFooter");
    host.width(60);
    let notice = host.next("ui.notify");
    assert_eq!(notice["level"], "error");
    assert!(notice["message"].as_str().unwrap().starts_with("footer render failed: "), "{notice}");
}

#[test]
fn set_widget_string_list_is_content_without_width() {
    let mut ext = Extension::new("rs-surface");
    ext.command("go", "set widget", |ctx, _| {
        ctx.set_widget("status", vec!["● 3 agents running".to_string()]).map_or_else(|e| CommandResult::Error(e.to_string()), |()| CommandResult::Ok)
    });
    let host = Host::start(ext);
    host.command("go");
    // No width: the host treats a width-less widget_push as string list content.
    assert_eq!(host.next("widget_push"), json!({"key": "status", "lines": ["● 3 agents running"]}));
}

// Pi's RPC mode writes one request per setWidget call, in call order and before the handler's response (rpc-mode.ts), so in RPC mode a string
// list is a ui.setWidget host call like every other shape: an unanswered widget_push frame has no place in that order.
#[test]
fn set_widget_string_list_in_rpc_mode_is_a_host_call() {
    let mut ext = Extension::new("rs-surface");
    ext.command("go", "set widget", |ctx, _| {
        ctx.set_widget("status", vec!["w-one".to_string()]).map_or_else(|e| CommandResult::Error(e.to_string()), |()| CommandResult::Ok)
    });
    let host = Host::start_in(ext, "rpc", true);
    host.command("go");
    let call = host.next("ui.setWidget");
    assert_eq!(call["key"], "status");
    assert_eq!(call["content"], json!(["w-one"]));
}

// Pi's ctx.ui.setWidget returns without a host round trip
// (interactive-mode.ts:2300-2340). Width handlers run on the loop that reads
// host replies, so a set_widget that waited for a reply there would never
// return and the extension would stop reading the socket.
#[test]
fn set_widget_from_a_width_handler_does_not_block_the_message_loop() {
    let mut ext = Extension::new("rs-surface");
    ext.command("go", "subscribe", |ctx, _| {
        let handler_ctx = ctx.clone();
        std::mem::forget(ctx.on_width_change(move |width| {
            let _ = handler_ctx.set_widget("fit", vec![format!("w@{width}")]);
        }));
        ctx.set_widget("fit", vec![format!("w@{}", ctx.width())]).map_or_else(|e| CommandResult::Error(e.to_string()), |()| CommandResult::Ok)
    });
    let host = Host::start(ext);
    host.command("go");
    assert_eq!(host.next("widget_push")["lines"], json!(["w@80"]));
    host.width(100);
    assert_eq!(host.next("widget_push")["lines"], json!(["w@100"]));
    // The second resize is read only if the first handler returned.
    host.width(90);
    assert_eq!(host.next("widget_push")["lines"], json!(["w@90"]));
}

// Pi calls a footer or header factory only in the TUI: rpc-mode.ts setFooter/setHeader and runner.ts noOpUIContext ignore it, so the renderer is never
// called, no frame is sent for it, and a resize does not render it.
#[test]
fn renderers_are_not_run_outside_the_tui() {
    for (mode, has_ui) in [("rpc", true), ("print", false), ("json", false)] {
        for (surface, method) in [("footer", "ui.setFooter"), ("header", "ui.setHeader")] {
            let rendered: Arc<Mutex<Vec<u32>>> = Arc::default();
            let mut ext = Extension::new("rs-surface");
            let seen = rendered.clone();
            ext.command("go", "install", move |ctx, _| {
                let seen = seen.clone();
                let render = move |width: u32| {
                    seen.lock().unwrap().push(width);
                    vec!["must-not-render".to_string()]
                };
                let result = if surface == "footer" { ctx.set_footer_renderer(Some(render)) } else { ctx.set_header_renderer(Some(render)) };
                result.map_or_else(|e| CommandResult::Error(e.to_string()), |()| CommandResult::Ok)
            });
            let host = Host::start_in(ext, mode, has_ui);
            host.command("go");
            host.width(60);
            host.quiet(method);
            host.command("go");
            host.quiet(method);
            assert_eq!(*rendered.lock().unwrap(), Vec::<u32>::new(), "{mode} {surface}");
        }
    }
}

// rpc-mode.ts custom() resolves undefined without running the component; the host call keeps the dialog scope, as the Node runtime's does.
#[test]
fn custom_resolves_none_in_rpc_without_running_the_component() {
    use pig_sdk::{RemoteComponent, RemoteComponentResult};
    struct Component(Arc<Mutex<Vec<&'static str>>>);
    impl RemoteComponent for Component {
        fn render(&self, _width: u32) -> Vec<String> {
            self.0.lock().unwrap().push("render");
            Vec::new()
        }
        fn handle_input(&mut self, _data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
            self.0.lock().unwrap().push("input");
            Ok(RemoteComponentResult::pending())
        }
    }
    let ran: Arc<Mutex<Vec<&'static str>>> = Arc::default();
    let (results_tx, results) = channel::<String>();
    let (seen_ran, seen_results) = (ran.clone(), Mutex::new(results_tx));
    let mut ext = Extension::new("rs-surface");
    ext.command("go", "custom", move |ctx, _| {
        let component = ctx.custom_component(Component(seen_ran.clone()), json!({}));
        let options = ctx.custom(json!({"overlay": true}));
        seen_results.lock().unwrap().send(format!("{component:?} {options:?}")).unwrap();
        CommandResult::Ok
    });
    let host = Host::start_in(ext, "rpc", true);
    host.command("go");
    for _ in 0..2 {
        let args = host.next("ui.custom");
        let keys: Vec<&String> = args.as_object().unwrap().keys().collect();
        assert_eq!(keys, ["key"], "ui.custom args {args}");
    }
    assert_eq!(results.recv_timeout(Duration::from_secs(5)).unwrap(), "Ok(None) Ok(None)");
    assert!(ran.lock().unwrap().is_empty(), "component callbacks ran in RPC mode: {:?}", ran.lock().unwrap());
}

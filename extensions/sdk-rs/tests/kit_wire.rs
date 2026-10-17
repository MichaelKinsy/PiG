#![cfg(unix)]

//! The component kit's wire (D107): authoritative views on every carrier,
//! view events on the overlay's input queue, image refs sent once per
//! connection and again after an eviction, and frontend-only annotations
//! only while a frontend draws.

use pig_sdk::kit::{
    Container, Event, EventKind, Image, Lines, SelectItem, SelectList, Spacer, Text, View,
};
use pig_sdk::{
    Extension, JsString, RemoteComponentResult, ToolDefinition, ToolRenderResult,
    ToolRendererSet, ToolResult, ViewComponent, empty_schema,
};
use serde_json::{Value, json};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::Arc;

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

fn notify(stream: &mut UnixStream, method: &str, args: Value) {
    send(stream, json!({"type": "notify", "notify": {"method": method, "args": args}}));
}

fn request(stream: &mut UnixStream, id: &str, method: &str, tool: &str, args: Value) {
    send(stream, json!({"type": "request", "id": id, "request": {"method": method, "tool": tool, "args": args}}));
}

fn answer(stream: &mut UnixStream, call: &Value, result: Value) {
    send(stream, json!({"type": "call_result", "id": call["id"], "call_result": {"result": result}}));
}

fn sha256_hex(data: &[u8]) -> String {
    // The ref is the image node's own: the kit computes it.
    Image::new(data.to_vec(), "image/png").reference().to_string()
}

struct Runtime {
    stream: UnixStream,
    thread: Option<std::thread::JoinHandle<std::io::Result<()>>>,
    root: std::path::PathBuf,
    register: Value,
}

impl Runtime {
    fn start(name: &str, ext: Extension, state: Value) -> Self {
        let root = std::env::temp_dir().join(format!("pig-kit-{name}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&root);
        std::fs::create_dir(&root).unwrap();
        let socket = root.join("sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let thread = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        // A missing frame fails the test after 10 s instead of hanging it.
        stream.set_read_timeout(Some(std::time::Duration::from_secs(10))).unwrap();
        let register = read(&mut stream);
        assert_eq!(register["type"], "register");
        send(&mut stream, json!({"type": "ready", "ready": {"state": state, "width": 80}}));
        Self { stream, thread: Some(thread), root, register }
    }

    fn shutdown(mut self) {
        send(&mut self.stream, json!({"type": "shutdown", "shutdown": {"reason": "done"}}));
        self.thread.take().unwrap().join().unwrap().unwrap();
    }
}

impl Drop for Runtime {
    fn drop(&mut self) {
        let _ = self.stream.shutdown(std::net::Shutdown::Both);
        // A failed assertion may leave the extension blocked in a handler;
        // report the failure instead of waiting for it.
        if let Some(thread) = self.thread.take().filter(|_| !std::thread::panicking()) {
            let _ = thread.join();
        }
        let _ = std::fs::remove_dir_all(&self.root);
    }
}

fn tracks() -> Vec<SelectItem> {
    (0..5).map(|i| SelectItem::new(format!("k{i}"), format!("Track {i}")).description(format!("d{i}"))).collect()
}

/// The conformance fixture's picker: logs every event and input it acts on,
/// and closes on select with the log.
struct Picker {
    log: Vec<String>,
}

impl ViewComponent for Picker {
    fn view(&self, _width: u32) -> View {
        View::new(
            Container::new()
                .child(Text::new(format!("{} logged", self.log.len()), 1, 0))
                .child(SelectList::new("kit-tracks", tracks(), 3).selected_index(2)),
        )
        .focus("kit-tracks")
        .theme("accent", "#d75f00")
    }

    fn handle_input(&mut self, data: &JsString) -> Result<RemoteComponentResult, String> {
        let data = data.to_string_lossy();
        if data != "noop" {
            self.log.push(format!("input:{data}"));
        }
        Ok(RemoteComponentResult::pending())
    }

    fn handle_view_event(&mut self, event: Event) -> Result<RemoteComponentResult, String> {
        let value = event.item.as_ref().map(|item| item.value.clone()).unwrap_or_default();
        match event.kind {
            EventKind::SelectionChange => self.log.push(format!("selectionChange:{}:{value}", event.index)),
            EventKind::Select => {
                self.log.push(format!("select:{}:{value}", event.index));
                return Ok(RemoteComponentResult::done(Some(Value::from(self.log.join(",")))));
            }
            EventKind::Cancel => return Err("cancelled".into()),
            EventKind::Change => self.log.push(format!("change:{}:{}", event.id, event.value)),
        }
        Ok(RemoteComponentResult::pending())
    }
}

#[test]
fn custom_view_sends_authoritative_frames_and_receives_events_in_order() {
    let mut ext = Extension::new("kit-custom");
    ext.tool("pick", "Pick a track", empty_schema(), |ctx, _| {
        match ctx.custom_view(Picker { log: Vec::new() }, json!({})) {
            Ok(value) => ToolResult::json(value.unwrap_or(Value::Null)),
            Err(err) => ToolResult::Error(err.to_string()),
        }
    });
    let mut rt = Runtime::start("custom", ext, json!({"hasUI": true}));
    let s = &mut rt.stream;
    request(s, "pick", "tool_call", "pick", json!({}));
    let call = next(s);
    assert_eq!(call["call"]["method"], "ui.custom");
    let key = call["call"]["args"]["key"].as_str().unwrap().to_string();

    let first = next(s);
    assert_eq!(first["notify"]["method"], "ui.custom.render");
    let frame = &first["notify"]["args"];
    assert!(frame.get("lines").is_none(), "an authoritative view frame has no lines: {frame}");
    assert_eq!((frame["key"].as_str(), frame["width"].as_u64(), frame["seq"].as_u64()), (Some(key.as_str()), Some(80), Some(1)));
    assert_eq!(frame["view"]["focus"], "kit-tracks");
    assert_eq!(frame["view"]["theme"], json!({"accent": "#d75f00"}));
    assert_eq!(frame["view"]["root"]["children"][1]["selectedIndex"], 2);
    assert_eq!(frame["view"]["root"]["children"][1]["items"][3], json!({"value": "k3", "label": "Track 3", "description": "d3"}));

    let event = |kind: &str, index: u64, value: &str| {
        json!({"key": key, "node": "kit-tracks", "type": kind, "index": index,
               "item": {"value": value, "label": "x", "description": "y"}})
    };
    // An unchanged view after input is not sent again.
    notify(s, "ui.custom.input", json!({"key": key, "data": "noop"}));
    notify(s, "ui.view.event", event("selectionChange", 3, "k3"));
    // An event of a surface that is not open is dropped.
    notify(s, "ui.view.event", json!({"key": "custom-999", "node": "kit-tracks", "type": "select", "index": 0}));
    notify(s, "ui.view.event", event("selectionChange", 4, "k4"));
    notify(s, "ui.custom.input", json!({"key": key, "data": "x"}));
    notify(s, "ui.view.event", event("select", 0, "k0"));

    let mut seqs = Vec::new();
    let close = loop {
        let frame = next(s);
        assert_eq!(frame["type"], "notify", "{frame}");
        if frame["notify"]["method"] == "ui.custom.close" {
            break frame;
        }
        assert_eq!(frame["notify"]["method"], "ui.custom.render");
        assert!(frame["notify"]["args"].get("lines").is_none());
        seqs.push(frame["notify"]["args"]["seq"].as_u64().unwrap());
    };
    assert_eq!(seqs, vec![2, 3, 4], "one frame per changed view, none for the no-op input");
    let result = "selectionChange:3:k3,selectionChange:4:k4,input:x,select:0:k0";
    assert_eq!(close["notify"]["args"]["result"], result);
    answer(s, &call, json!({"ok": true, "result": result}));
    let response = next(s);
    assert_eq!(response["type"], "response");
    rt.shutdown();
}

#[test]
fn view_event_error_closes_the_overlay_with_it() {
    let mut ext = Extension::new("kit-cancel");
    ext.tool("pick", "Pick", empty_schema(), |ctx, _| match ctx.custom_view(Picker { log: Vec::new() }, json!({})) {
        Ok(value) => ToolResult::json(value.unwrap_or(Value::Null)),
        Err(err) => ToolResult::Error(err.to_string()),
    });
    let mut rt = Runtime::start("cancel", ext, json!({"hasUI": true}));
    let s = &mut rt.stream;
    request(s, "pick", "tool_call", "pick", json!({}));
    let call = next(s);
    let key = call["call"]["args"]["key"].as_str().unwrap().to_string();
    assert_eq!(next(s)["notify"]["method"], "ui.custom.render");
    notify(s, "ui.view.event", json!({"key": key, "node": "kit-tracks", "type": "cancel", "index": 0}));
    let close = next(s);
    assert_eq!(close["notify"]["method"], "ui.custom.close");
    assert_eq!(close["notify"]["args"]["error"], "cancelled");
    answer(s, &call, json!({"ok": false}));
    assert_eq!(next(s)["type"], "response");
    rt.shutdown();
}

struct Cover;

impl ViewComponent for Cover {
    fn view(&self, _width: u32) -> View {
        View::new(Image::new(b"cover".to_vec(), "image/png"))
    }

    fn handle_input(&mut self, _data: &JsString) -> Result<RemoteComponentResult, String> {
        Ok(RemoteComponentResult::done(None))
    }
}

#[test]
fn custom_view_sends_an_evicted_image_again_in_an_unchanged_frame() {
    let mut ext = Extension::new("kit-cover");
    ext.tool("cover", "Cover", empty_schema(), |ctx, _| match ctx.custom_view(Cover, json!({})) {
        Ok(_) => ToolResult::text("ok"),
        Err(err) => ToolResult::Error(err.to_string()),
    });
    let mut rt = Runtime::start("cover", ext, json!({"hasUI": true}));
    let s = &mut rt.stream;
    let cover = sha256_hex(b"cover");
    request(s, "c", "tool_call", "cover", json!({}));
    let call = next(s);
    let key = call["call"]["args"]["key"].as_str().unwrap().to_string();
    let first = next(s);
    assert_eq!(first["notify"]["args"]["view"]["images"][0]["ref"], cover);
    // A new width sends the unchanged view again, naming the ref only.
    notify(s, "width_change", json!({"width": 90}));
    let resized = next(s);
    assert_eq!((resized["notify"]["args"]["seq"].as_u64(), resized["notify"]["args"]["width"].as_u64()), (Some(2), Some(90)));
    assert!(resized["notify"]["args"]["view"].get("images").is_none());
    // An eviction of another ref leaves the overlay alone; its own ref sends
    // the unchanged frame again with the data.
    notify(s, "ui.view.evicted", json!({"refs": ["0000"]}));
    notify(s, "ui.view.evicted", json!({"refs": [cover]}));
    let again = next(s);
    assert_eq!(again["notify"]["method"], "ui.custom.render", "{again}");
    assert_eq!(again["notify"]["args"]["seq"], 3);
    assert_eq!(again["notify"]["args"]["view"]["images"][0]["data"], "Y292ZXI=");
    notify(s, "ui.custom.input", json!({"key": key, "data": "q"}));
    assert_eq!(next(s)["notify"]["method"], "ui.custom.close");
    answer(s, &call, json!({"ok": true}));
    assert_eq!(next(s)["type"], "response");
    rt.shutdown();
}

fn cover_view() -> View {
    View::new(
        Container::new()
            .child(Image::new(b"cover".to_vec(), "image/png"))
            .child(Lines::new(vec!["▀▀".into()]).progress(1.0, 4.0)),
    )
}

#[test]
fn widget_header_and_footer_views_send_images_once_and_again_after_eviction() {
    let mut ext = Extension::new("kit-surfaces");
    ext.tool("show", "Show", empty_schema(), |ctx, _| {
        let steps = || -> std::io::Result<()> {
            ctx.set_widget_view("w", cover_view(), None)?;
            ctx.set_widget_view("w", cover_view(), None)?;
            ctx.set_header_view(cover_view())?;
            ctx.set_footer_view(View::new(Spacer::new(1)))?;
            ctx.set_widget_view("w2", View::new(Spacer::new(2)), Some(json!({"placement": "belowEditor"})))?;
            Ok(())
        };
        match steps() {
            Ok(()) => ToolResult::text("ok"),
            Err(err) => ToolResult::Error(err.to_string()),
        }
    });
    ext.tool("plain", "Plain", empty_schema(), |ctx, _| {
        let _ = ctx.set_widget_view("w", cover_view(), None);
        ToolResult::text("ok")
    });
    let mut rt = Runtime::start("surfaces", ext, json!({"hasUI": true, "frontend": true}));
    let s = &mut rt.stream;
    let cover = sha256_hex(b"cover");
    request(s, "show", "tool_call", "show", json!({}));

    let push = next(s);
    assert_eq!(push["type"], "widget_push");
    assert!(push["widget_push"].get("lines").is_none());
    let view = &push["widget_push"]["view"];
    assert_eq!(view["images"], json!([{"ref": cover, "mimeType": "image/png", "data": "Y292ZXI="}]));
    assert_eq!(view["root"]["children"][1]["progress"], json!({"value": 1.0, "max": 4.0}), "a frontend draws");

    let again = next(s);
    assert_eq!(again["type"], "widget_push");
    assert!(again["widget_push"]["view"].get("images").is_none(), "the ref is sent once");

    let header = next(s);
    assert_eq!(header["call"]["method"], "ui.setHeader");
    assert_eq!(header["call"]["args"]["view"]["root"]["children"][0]["ref"], cover);
    assert!(header["call"]["args"]["view"].get("images").is_none());
    assert!(header["call"]["args"].get("lines").is_none() && header["call"]["args"].get("width").is_none());
    answer(s, &header, json!({}));
    let footer = next(s);
    assert_eq!(footer["call"]["method"], "ui.setFooter");
    assert_eq!(footer["call"]["args"], json!({"view": {"root": {"kind": "spacer", "lines": 1}}}));
    answer(s, &footer, json!({}));
    let widget = next(s);
    assert_eq!(widget["call"]["method"], "ui.setWidget");
    assert_eq!(
        widget["call"]["args"],
        json!({"key": "w2", "view": {"root": {"kind": "spacer", "lines": 2}}, "options": {"placement": "belowEditor"}})
    );
    answer(s, &widget, json!({}));
    assert_eq!(next(s)["type"], "response");
    // The host dropped the cover: the widget and the header that name it go
    // out again; the footer and w2 do not. The data travels once, with the
    // first frame that names the ref again.
    notify(s, "ui.view.evicted", json!({"refs": [cover]}));
    let resent = next(s);
    assert_eq!(resent["type"], "widget_push");
    assert_eq!(resent["widget_push"]["key"], "w");
    assert_eq!(resent["widget_push"]["view"]["images"], json!([{"ref": cover, "mimeType": "image/png", "data": "Y292ZXI="}]));
    let header = next(s);
    assert_eq!(header["call"]["method"], "ui.setHeader");
    assert_eq!(header["call"]["args"]["view"]["root"]["children"][0]["ref"], cover);
    assert!(header["call"]["args"]["view"].get("images").is_none());
    answer(s, &header, json!({}));

    // Without a frontend the progress annotation is not sent.
    notify(s, "state_update", json!({"state": {"hasUI": true}}));
    request(s, "plain", "tool_call", "plain", json!({}));
    let plain = next(s);
    assert_eq!(plain["type"], "widget_push");
    assert_eq!(plain["widget_push"]["view"]["root"]["children"][1], json!({"kind": "lines", "content": ["▀▀"]}));
    assert_eq!(next(s)["type"], "response");
    rt.shutdown();
}

#[test]
fn view_renderers_answer_with_a_view_and_win_over_lines() {
    let mut ext = Extension::new("kit-renderers");
    let mut tool = ToolDefinition::new("t", "T", "tool", empty_schema(), |_, _| ToolResult::text("ok"));
    tool.render_result = Some(Box::new(|_, _, _, _, _| Ok(vec!["lines".into()])));
    tool.render_result_view = Some(Box::new(|_, result: ToolRenderResult, _, render, width| {
        render.state.insert("seen".into(), Value::from(width));
        Ok(View::new(Text::new(format!("{} blocks at {width}", result.content.len()), 0, 0)))
    }));
    ext.register_tool(tool);
    ext.tool("u", "U", empty_schema(), |_, _| ToolResult::text("ok"));
    ext.render_tool_call_view("u", |_, args, _, _| Ok(View::new(Text::new(args["q"].to_string(), 0, 0))));
    ext.tool_renderer(|tool, _| {
        (tool == "r").then(|| ToolRendererSet::with_call_view(|_, _, _, width| Ok(View::new(Spacer::new(width / 10)))))
    });
    ext.message_view_renderer("note", |_, message, _, _| Ok(View::new(Text::new(message["content"].as_str().unwrap_or(""), 1, 1))));
    ext.entry_view_renderer("mark", |_, entry, _, _| Ok(View::new(Text::new(entry["data"].as_str().unwrap_or(""), 1, 0))));
    let mut rt = Runtime::start("renderers", ext, json!({"hasUI": true}));
    let tools = rt.register["register"]["tools"].as_array().unwrap().clone();
    let declared = |name: &str| tools.iter().find(|tool| tool["name"] == name).unwrap().clone();
    assert_eq!(declared("t")["renders_result"], true);
    assert_eq!(declared("u")["renders_call"], true, "a view form declares the phase rendered");
    let s = &mut rt.stream;

    let text = |result: &Value| result["response"]["result"]["view"]["root"]["text"].clone();
    request(s, "1", "render_tool", "t", json!({"card": "c", "phase": "result", "result": {"content": [{"type": "text", "text": "x"}]}, "width": 40}));
    let result = next(s);
    assert!(result["response"]["result"].get("lines").is_none(), "{result}");
    assert_eq!(text(&result), "1 blocks at 40");

    request(s, "2", "render_tool", "u", json!({"card": "d", "phase": "call", "args": {"q": "hi"}, "width": 40}));
    assert_eq!(text(&next(s)), "\"hi\"");

    request(s, "3", "resolve_tool_renderers", "", json!({"tool": "r"}));
    let resolved = next(s);
    assert_eq!(resolved["response"]["result"]["renders_call"], true);
    let id = resolved["response"]["result"]["renderers"].clone();
    request(s, "4", "render_tool", "r", json!({"card": "e", "phase": "call", "renderers": id, "width": 30}));
    assert_eq!(next(s)["response"]["result"]["view"]["root"], json!({"kind": "spacer", "lines": 3}));

    request(s, "5", "render_message", "note", json!({"message": {"content": "hello"}, "options": {}, "width": 50}));
    assert_eq!(text(&next(s)), "hello");
    request(s, "6", "render_entry", "mark", json!({"entry": {"data": "entry"}, "options": {}, "width": 50}));
    assert_eq!(text(&next(s)), "entry");
    rt.shutdown();
}

#[test]
fn views_are_shared_across_threads() {
    // A view is plain data an author may build on any thread.
    fn assert_send_sync<T: Send + Sync>() {}
    assert_send_sync::<View>();
    let view = Arc::new(View::new(Spacer::new(1)));
    std::thread::spawn(move || assert_eq!(*view, View::new(Spacer::new(1)))).join().unwrap();
}

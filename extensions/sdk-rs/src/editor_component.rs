//! `ctx.ui.setEditorComponent` for Rust extensions.
//!
//! Pi's factory returns a CustomEditor subclass: the host's keys reach its
//! `handleInput`, its `render` output is the editor on screen and its `super`
//! calls reach the default editor. Rust has no subclassing, so the component
//! implements [`EditorComponent`], whose methods default to the host's default
//! editor reached through [`EditorBase`]; override the ones that differ and
//! call `self.base()` for `super`.
//!
//! Every method runs on one worker thread per installed editor, in the order
//! the host produced the events. After each event the SDK renders the
//! component and sends the frame when it changed.

use crate::js_string::JsString;
use crate::protocol::Connection;
use serde::Serialize;
use serde_json::{Value, json};
use std::io;
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::atomic::{AtomicU32, Ordering};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::sync::{Arc, Mutex};
use std::thread;

/// pi-tui's TuiMouseEvent with the row relative to the editor's first row.
#[derive(Debug, Clone, Default, Serialize, serde::Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct EditorMouseEvent {
    #[serde(rename = "type", default)]
    pub kind: String,
    #[serde(default)]
    pub button: String,
    #[serde(default)]
    pub x: i64,
    #[serde(default)]
    pub y: i64,
    #[serde(default)]
    pub screen_x: i64,
    #[serde(default)]
    pub screen_y: i64,
    #[serde(default)]
    pub width: i64,
    #[serde(default)]
    pub height: i64,
    #[serde(default)]
    pub shift: bool,
    #[serde(default)]
    pub alt: bool,
    #[serde(default)]
    pub ctrl: bool,
    #[serde(default)]
    pub click_count: i64,
}

/// The cursor of the host's default editor: a logical line and a UTF-16 column.
#[derive(Debug, Clone, Copy, PartialEq, Eq, serde::Deserialize)]
pub struct EditorCursor {
    pub line: i64,
    pub col: i64,
}

/// The host's default editor, the `super` of an [`EditorComponent`]. Every
/// method is a call to the host's editor, valid once the factory has returned.
#[derive(Clone)]
pub struct EditorBase {
    conn: Arc<Connection>,
    key: String,
}

/// Arguments of one `ui.editor.base` operation. JavaScript strings travel as [`JsString`] through the typed call, which keeps lone UTF-16 units that a `serde_json::Value` cannot hold.
#[derive(Default, Serialize)]
struct BaseArgs<'a> {
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<&'a JsString>,
    #[serde(skip_serializing_if = "Option::is_none")]
    text: Option<&'a JsString>,
    #[serde(skip_serializing_if = "Option::is_none")]
    width: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    n: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    event: Option<&'a EditorMouseEvent>,
}

#[derive(Serialize)]
struct BaseCall<'a> {
    key: &'a str,
    op: &'a str,
    args: BaseArgs<'a>,
}

#[derive(serde::Deserialize)]
struct LinesReply {
    #[serde(default)]
    lines: Vec<JsString>,
}

#[derive(serde::Deserialize)]
struct TextReply {
    #[serde(default)]
    text: JsString,
}

impl EditorBase {
    fn op_typed<R: serde::de::DeserializeOwned>(&self, op: &str, args: BaseArgs) -> io::Result<Option<R>> {
        let reply = self.conn.call_typed::<_, R>(None, None, "ui.editor.base", Some(BaseCall { key: &self.key, op, args }))?;
        if let Some(error) = reply.error {
            return Err(io::Error::other(crate::context::host_error_message(error)));
        }
        Ok(reply.result)
    }

    fn op(&self, op: &str, args: BaseArgs) -> io::Result<Value> {
        Ok(self.op_typed::<Value>(op, args)?.unwrap_or(Value::Null))
    }

    /// The default editor's handleInput: extension shortcuts, then the app actions, then editing.
    pub fn handle_input(&self, data: &JsString) -> io::Result<()> {
        self.op("handleInput", BaseArgs { data: Some(data), ..Default::default() }).map(drop)
    }
    /// The default editor's handleMouse.
    pub fn handle_mouse(&self, event: &EditorMouseEvent) -> io::Result<()> {
        self.op("handleMouse", BaseArgs { event: Some(event), ..Default::default() }).map(drop)
    }
    /// The default editor's render.
    pub fn render(&self, width: u32) -> io::Result<Vec<JsString>> {
        Ok(self.op_typed::<LinesReply>("render", BaseArgs { width: Some(width), ..Default::default() })?.map(|reply| reply.lines).unwrap_or_default())
    }
    /// The default editor's setText.
    pub fn set_text(&self, text: &JsString) -> io::Result<()> {
        self.op("setText", BaseArgs { text: Some(text), ..Default::default() }).map(drop)
    }
    /// The default editor's insertTextAtCursor.
    pub fn insert_text_at_cursor(&self, text: &JsString) -> io::Result<()> {
        self.op("insertTextAtCursor", BaseArgs { text: Some(text), ..Default::default() }).map(drop)
    }
    /// The default editor's addToHistory.
    pub fn add_to_history(&self, text: &JsString) -> io::Result<()> {
        self.op("addToHistory", BaseArgs { text: Some(text), ..Default::default() }).map(drop)
    }
    /// The default editor's getText.
    pub fn get_text(&self) -> io::Result<JsString> {
        Ok(self.op_typed::<TextReply>("getText", BaseArgs::default())?.map(|reply| reply.text).unwrap_or_default())
    }
    /// The default editor's getExpandedText: the text with paste markers expanded.
    pub fn get_expanded_text(&self) -> io::Result<JsString> {
        Ok(self.op_typed::<TextReply>("getExpandedText", BaseArgs::default())?.map(|reply| reply.text).unwrap_or_default())
    }
    /// The default editor's getLines.
    pub fn get_lines(&self) -> io::Result<Vec<JsString>> {
        Ok(self.op_typed::<LinesReply>("getLines", BaseArgs::default())?.map(|reply| reply.lines).unwrap_or_default())
    }
    /// The default editor's getCursor.
    pub fn get_cursor(&self) -> io::Result<EditorCursor> {
        serde_json::from_value(self.op("getCursor", BaseArgs::default())?).map_err(io::Error::other)
    }
    /// The default editor's isShowingAutocomplete.
    pub fn is_showing_autocomplete(&self) -> io::Result<bool> {
        Ok(self.op("isShowingAutocomplete", BaseArgs::default())?.get("value").and_then(Value::as_bool).unwrap_or(false))
    }
    /// The default editor's setPaddingX.
    pub fn set_padding_x(&self, padding: u32) -> io::Result<()> {
        self.op("setPaddingX", BaseArgs { n: Some(padding), ..Default::default() }).map(drop)
    }
    /// The default editor's setAutocompleteMaxVisible.
    pub fn set_autocomplete_max_visible(&self, max_visible: u32) -> io::Result<()> {
        self.op("setAutocompleteMaxVisible", BaseArgs { n: Some(max_visible), ..Default::default() }).map(drop)
    }
}

/// The editor an extension installs with `Context::set_editor_component`.
/// Every method defaults to the host's default editor; an error is reported to
/// the user as an editor failure.
pub trait EditorComponent: Send {
    /// The host's default editor, this component's `super`.
    fn base(&self) -> &EditorBase;

    /// Whether the component draws the working, compaction, summarization and retry status in its top border,
    /// as Pi's CustomEditor does with `embedWorkingStatus: true`. The host then renders the status into the rows
    /// that [`EditorBase::render`] returns instead of showing it above the editor. Read once, after the factory returns.
    fn embed_working_status(&self) -> bool {
        false
    }

    /// One key from the host.
    fn handle_input(&mut self, data: &JsString) -> Result<(), String> {
        self.base().handle_input(data).map_err(|e| e.to_string())
    }
    /// A left click on the editor rows.
    fn handle_mouse(&mut self, event: &EditorMouseEvent) -> Result<(), String> {
        self.base().handle_mouse(event).map_err(|e| e.to_string())
    }
    /// The editor rows for `width`.
    fn render(&mut self, width: u32) -> Result<Vec<JsString>, String> {
        self.base().render(width).map_err(|e| e.to_string())
    }
    /// The calls the host makes on its editor.
    fn set_text(&mut self, text: &JsString) -> Result<(), String> {
        self.base().set_text(text).map_err(|e| e.to_string())
    }
    fn insert_text_at_cursor(&mut self, text: &JsString) -> Result<(), String> {
        self.base().insert_text_at_cursor(text).map_err(|e| e.to_string())
    }
    fn add_to_history(&mut self, text: &JsString) -> Result<(), String> {
        self.base().add_to_history(text).map_err(|e| e.to_string())
    }
}

/// Builds the editor component around the host's default editor.
pub type EditorFactory = Box<dyn FnOnce(EditorBase) -> Box<dyn EditorComponent> + Send>;

/// One rendered frame; its rows are [`JsString`] so lone UTF-16 units reach the host.
#[derive(Serialize)]
struct Frame<'a> {
    key: &'a str,
    lines: &'a [JsString],
    width: u32,
    seq: u64,
}

enum EditorEvent {
    Input(JsString),
    Mouse(EditorMouseEvent),
    SetText(JsString),
    InsertText(JsString),
    AddToHistory(JsString),
    Render,
    Stop,
}

/// One installed editor component and the worker that runs it.
pub(crate) struct EditorSession {
    key: String,
    events: Sender<EditorEvent>,
}

impl EditorSession {
    fn send(&self, event: EditorEvent) {
        let _ = self.events.send(event);
    }
    pub(crate) fn close(&self) {
        self.send(EditorEvent::Stop);
    }
    pub(crate) fn request_render(&self) {
        self.send(EditorEvent::Render);
    }
}

pub(crate) type EditorSlot = Arc<Mutex<Option<Arc<EditorSession>>>>;

struct EditorWorker {
    conn: Arc<Connection>,
    key: String,
    width: Arc<AtomicU32>,
    component: Box<dyn EditorComponent>,
    seq: u64,
    last: Option<(u32, Vec<JsString>)>,
}

impl EditorWorker {
    fn fail(&self, what: &str, message: String) {
        let _ = self.conn.notify("ui.notify", Some(json!({"message": format!("editor {what} failed: {message}"), "level": "error"})));
    }

    fn guarded<T>(&mut self, what: &str, call: impl FnOnce(&mut dyn EditorComponent) -> Result<T, String>) -> Option<T> {
        let component = &mut self.component;
        match catch_unwind(AssertUnwindSafe(|| call(component.as_mut()))) {
            Ok(Ok(value)) => Some(value),
            Ok(Err(message)) => {
                self.fail(what, message);
                None
            }
            Err(_) => {
                self.fail(what, "the component panicked".to_string());
                None
            }
        }
    }

    fn render_now(&mut self) {
        let width = match self.width.load(Ordering::Relaxed) {
            0 => 80,
            width => width,
        };
        let Some(lines) = self.guarded("render", |c| c.render(width)) else {
            return;
        };
        if self.last.as_ref().is_some_and(|(w, l)| *w == width && *l == lines) {
            return;
        }
        self.seq += 1;
        let frame = Frame { key: &self.key, lines: &lines, width, seq: self.seq };
        let _ = self.conn.notify_typed("ui.editor.render", Some(frame));
        self.last = Some((width, lines));
    }

    fn run(mut self, events: Receiver<EditorEvent>) {
        while let Ok(event) = events.recv() {
            match event {
                EditorEvent::Stop => return,
                EditorEvent::Input(data) => {
                    self.guarded("handleInput", |c| c.handle_input(&data));
                    self.render_now();
                    let _ = self.conn.notify("ui.editor.inputDone", Some(json!({"key": self.key})));
                    continue;
                }
                EditorEvent::Mouse(event) => {
                    self.guarded("handleMouse", |c| c.handle_mouse(&event));
                }
                EditorEvent::SetText(text) => {
                    self.guarded("setText", |c| c.set_text(&text));
                }
                EditorEvent::InsertText(text) => {
                    self.guarded("insertTextAtCursor", |c| c.insert_text_at_cursor(&text));
                }
                EditorEvent::AddToHistory(text) => {
                    self.guarded("addToHistory", |c| c.add_to_history(&text));
                }
                EditorEvent::Render => {}
            }
            self.render_now();
        }
    }
}

/// Install the component the factory builds and announce it to the host. The host learns of the editor before the
/// factory runs, so the factory may already call the [`EditorBase`] it receives: such a call waits until the host's
/// editor is ready.
pub(crate) fn install_editor(
    conn: &Arc<Connection>,
    slot: &EditorSlot,
    width: &Arc<AtomicU32>,
    seq: u64,
    factory: EditorFactory,
) -> io::Result<()> {
    let key = format!("editor-{seq}");
    let (events, receiver) = channel();
    let session = Arc::new(EditorSession { key: key.clone(), events });
    if let Some(previous) = slot.lock().unwrap().replace(session.clone()) {
        previous.close();
    }
    let remove = |slot: &EditorSlot| {
        let mut current = slot.lock().unwrap();
        if current.as_ref().is_some_and(|s| Arc::ptr_eq(s, &session)) {
            *current = None;
        }
        let _ = conn.notify("ui.editor.clear", Some(json!({"key": key})));
    };
    if let Err(err) = conn.notify("ui.editor.install", Some(json!({"key": key, "delegated": true}))) {
        remove(slot);
        return Err(err);
    }
    let base = EditorBase { conn: conn.clone(), key: key.clone() };
    let component = match catch_unwind(AssertUnwindSafe(|| factory(base))) {
        Ok(component) => component,
        Err(panic) => {
            remove(slot);
            std::panic::resume_unwind(panic);
        }
    };
    let embed = component.embed_working_status();
    let worker = EditorWorker { conn: conn.clone(), key: key.clone(), width: width.clone(), component, seq: 0, last: None };
    thread::Builder::new().name(format!("pig-{key}")).spawn(move || worker.run(receiver))?;
    if embed {
        conn.notify("ui.editor.options", Some(json!({"key": key, "embedWorkingStatus": true})))?;
    }
    Ok(())
}

/// Route one of the host's editor notifies to the installed component.
pub(crate) fn editor_notify(slot: &EditorSlot, method: &str, key: &str, text: Option<JsString>, args: Option<&Value>) {
    let session = slot.lock().unwrap().clone();
    let Some(session) = session.filter(|s| s.key == key) else {
        return;
    };
    match method {
        "ui.editor.input" => session.send(EditorEvent::Input(text.unwrap_or_default())),
        "ui.editor.setText" => session.send(EditorEvent::SetText(text.unwrap_or_default())),
        "ui.editor.insertText" => session.send(EditorEvent::InsertText(text.unwrap_or_default())),
        "ui.editor.addToHistory" => session.send(EditorEvent::AddToHistory(text.unwrap_or_default())),
        "ui.editor.mouse" => {
            let event = args.and_then(|a| a.get("event")).cloned().and_then(|e| serde_json::from_value(e).ok()).unwrap_or_default();
            session.send(EditorEvent::Mouse(event));
        }
        "ui.editor.configure" => session.request_render(),
        "ui.editor.closed" => {
            let mut current = slot.lock().unwrap();
            if current.as_ref().is_some_and(|s| Arc::ptr_eq(s, &session)) {
                *current = None;
            }
            session.close();
        }
        _ => {}
    }
}

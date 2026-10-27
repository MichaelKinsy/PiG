//! pi-tui's `TuiAltScreen`: the alternate-screen renderer of Pi's fullscreen mode, for an extension that owns a terminal.
//!
//! The engine runs in the host (`tui.TuiAltScreen`, the port of `tui-alt-screen.ts`); [`TuiAltScreen`] is the extension's handle to
//! one screen there. The extension supplies the [`Terminal`] the screen draws on and the [`Component`]s it lays out; the host runs
//! layout, scroll, selection, overlays, focus and rendering, and calls back into the extension for everything the extension owns:
//! terminal output, component render, input, mouse and focus, input listeners and the screen's callbacks. A component or terminal
//! callback runs on its own request thread, so the thread that called a screen method may be blocked in that call meanwhile.
//!
//! Every member of Pi's class is a method of the same name in snake case. Pi's synchronous members are synchronous here (a host
//! round trip); the Promise ones, [`Terminal::drain_input`], [`TuiAltScreen::copy_active_selection_to_clipboard`],
//! [`TuiAltScreen::query_terminal_colors`] and the `copy_selection` option, block the calling thread and return their value. Members
//! that read the screen's state ask the host, so they return `io::Result`.
//!
//! Components and terminals are shared as `Arc<dyn ...>` and identified by that `Arc`: the component [`TuiAltScreen::get_focused_component`]
//! returns is the very object given to [`TuiAltScreen::set_focus`], and [`TuiAltScreen::terminal`] is the terminal given to
//! [`TuiAltScreen::new`]. Their methods take `&self` and a component keeps its own state behind its own locks, so a component may call
//! the screen from its `handle_input` or `render` without holding a lock the host's next callback needs. A screen keeps every component it
//! has been given until it is dropped; dropping the last handle (the screen and its [`AltScreenOverlayHandle`]s) disposes the host's screen.

use crate::context::{Context, MouseEvent, OverlayBounds, OverlayState};
use crate::tui::RgbColor;
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use std::collections::HashMap;
use std::io;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, Weak};

/// What a terminal calls with the data of one input event; it returns after the host handled the event.
pub type TerminalInputFn = Arc<dyn Fn(&str) -> io::Result<()> + Send + Sync>;
/// What a terminal calls when its dimensions changed; it returns after the host handled the resize.
pub type TerminalResizeFn = Arc<dyn Fn() -> io::Result<()> + Send + Sync>;

/// pi-tui's `ProgramStatus` (OSC 7501): `state` is `idle`, `working`, `blocked`, `done`, `error` or `clear`; `kind` is `permission`,
/// `question` or `auth` for a blocked program.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ProgramStatus {
    pub state: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub app: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub kind: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub message: Option<String>,
}

/// pi-tui's `Terminal`: what a [`TuiAltScreen`] draws on and reads input from. The screen reads `columns`, `rows` and
/// `kitty_protocol_active` before every host call, and calls the rest as the host's engine does.
pub trait Terminal: Send + Sync {
    /// Starts delivering input and resizes: `on_input` carries each input event and `on_resize` each change of dimensions.
    fn start(&self, on_input: TerminalInputFn, on_resize: TerminalResizeFn);
    /// Stops the terminal and restores its state.
    fn stop(&self);
    /// Drains input before exit; returns when drained. Pi's defaults are 1000 ms and 50 ms.
    fn drain_input(&self, max_ms: u64, idle_ms: u64);
    fn write(&self, data: &str);
    fn columns(&self) -> u32;
    fn rows(&self) -> u32;
    fn kitty_protocol_active(&self) -> bool;
    /// Moves the cursor up (negative) or down (positive) by `lines`.
    fn move_by(&self, lines: i32);
    fn hide_cursor(&self);
    fn show_cursor(&self);
    fn clear_line(&self);
    fn clear_from_cursor(&self);
    fn clear_screen(&self);
    fn set_title(&self, title: &str);
    fn set_progress(&self, active: bool);
    fn set_program_status(&self, status: &ProgramStatus);
    /// Writes `before`, the terminal's own mode resets, then `after` when the process exits without `stop` having run. The default
    /// writes nothing, as a terminal that owns no process modes.
    fn restore_on_exit(&self, _before: &str, _after: &str) {}
}

/// pi-tui's `Component` with its optional members: `render` draws; the rest default to a component that lacks them. A component that
/// takes keys returns true from [`Self::handles_input`], one that takes the mouse from [`Self::handles_mouse`], and a `Focusable` one
/// from [`Self::is_focusable`] and keeps its `focused` flag in [`Self::focused`] / [`Self::set_focused`].
pub trait Component: Send + Sync {
    fn render(&self, width: u32) -> Vec<String>;
    fn invalidate(&self) {}
    fn handles_input(&self) -> bool {
        false
    }
    fn handle_input(&self, _data: &str) {}
    fn handles_mouse(&self) -> bool {
        false
    }
    fn handle_mouse(&self, _event: &MouseEvent) -> Option<MouseEventResult> {
        None
    }
    fn is_focusable(&self) -> bool {
        false
    }
    fn focused(&self) -> bool {
        false
    }
    fn set_focused(&self, _focused: bool) {}
    fn wants_key_release(&self) -> bool {
        false
    }
}

/// A component as a screen holds it and identifies it.
pub type ComponentRef = Arc<dyn Component>;

/// pi-tui's `TuiMouseEventResult`.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct MouseEventResult {
    /// Stops propagation and suppresses renderer-level fallback behavior.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub handled: Option<bool>,
    /// Routes later drag and release events to the component.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub capture: Option<bool>,
    /// Gives keyboard focus to the component.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub focus: Option<bool>,
    /// Requests (true) or suppresses (false) a render; `None` takes the default.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub render: Option<bool>,
}

/// pi-tui's `TuiInputListener` result: `consume` swallows the input, `data` replaces it.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct InputListenerResult {
    pub consume: bool,
    pub data: Option<String>,
}

/// pi-tui's `TuiInputListener`: sees every input before the components do. It is identified by its `Arc`, as Pi identifies the function.
pub type InputListener = Arc<dyn Fn(&str) -> Option<InputListenerResult> + Send + Sync>;

/// Ends an input listener or a color scheme subscription.
pub type Unsubscribe = Box<dyn Fn() -> io::Result<()> + Send + Sync>;

/// pi-tui's `TerminalColorScheme`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TerminalColorScheme {
    Dark,
    Light,
}

impl TerminalColorScheme {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Dark => "dark",
            Self::Light => "light",
        }
    }
}

/// pi-tui's `TerminalColors`: what the terminal reports for its theme.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct TerminalColors {
    /// Default foreground (OSC 10).
    pub foreground: Option<RgbColor>,
    /// Default background (OSC 11).
    pub background: Option<RgbColor>,
    /// ANSI colors 0-15 (OSC 4); only set when the terminal reported all 16.
    pub palette: Option<Vec<RgbColor>>,
}

fn rgb_to_value(color: &RgbColor) -> Value {
    json!({"r": number(color.r), "g": number(color.g), "b": number(color.b)})
}

fn rgb_from_value(value: &Value) -> Option<RgbColor> {
    Some(RgbColor { r: value.get("r")?.as_f64()?, g: value.get("g")?.as_f64()?, b: value.get("b")?.as_f64()? })
}

impl TerminalColors {
    /// The wire and `JSON.stringify` form: an absent member is omitted.
    pub fn to_value(&self) -> Value {
        let mut object = Map::new();
        if let Some(color) = &self.foreground {
            object.insert("foreground".into(), rgb_to_value(color));
        }
        if let Some(color) = &self.background {
            object.insert("background".into(), rgb_to_value(color));
        }
        if let Some(palette) = &self.palette {
            object.insert("palette".into(), Value::Array(palette.iter().map(rgb_to_value).collect()));
        }
        Value::Object(object)
    }

    fn from_value(value: &Value) -> Self {
        Self {
            foreground: value.get("foreground").and_then(rgb_from_value),
            background: value.get("background").and_then(rgb_from_value),
            palette: value.get("palette").and_then(Value::as_array).map(|palette| palette.iter().filter_map(rgb_from_value).collect()),
        }
    }
}

impl Serialize for TerminalColors {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        self.to_value().serialize(serializer)
    }
}

/// The replies of a color query that completed after its timeout.
pub type LateColorsFn = Arc<dyn Fn(TerminalColors) + Send + Sync>;

/// pi-tui's `queryTerminalColors` options.
#[derive(Clone)]
pub struct QueryTerminalColorsOptions {
    /// Query timeout in milliseconds, for terminals that do not answer DA1 either.
    pub timeout_ms: f64,
    /// Receives the replies of the query when it completes after the timeout.
    pub on_late_reply: Option<LateColorsFn>,
}

/// Pi's `visible(termWidth, termHeight)` overlay callback: the host asks it whenever it decides whether the overlay shows.
pub type OverlayVisibleFn = Arc<dyn Fn(u32, u32) -> bool + Send + Sync>;

/// pi-tui's `OverlayOptions` for [`TuiAltScreen::show_overlay`]: `layout` is the serialisable part as JSON (`width`, `minWidth`, `maxHeight`,
/// `anchor`, `offsetX`, `offsetY`, `row`, `col`, `margin`, `nonCapturing`), `visible` Pi's callback (`None` is always visible).
#[derive(Clone, Default)]
pub struct TuiOverlayOptions {
    pub layout: Option<Value>,
    pub visible: Option<OverlayVisibleFn>,
}

/// pi-tui's `WheelScrollLines`: logical lines per wheel event, or `Auto` to accelerate fast spins.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum WheelScrollLines {
    Lines(f64),
    Auto,
}

impl WheelScrollLines {
    fn to_value(self) -> Value {
        match self {
            Self::Lines(lines) => number(lines),
            Self::Auto => Value::from("auto"),
        }
    }
}

/// The outcome of the `copy_selection` option, Pi's `boolean | string`: copied, failed with the generic message, or failed with this message.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum CopySelectionOutcome {
    Copied,
    Failed,
    Message(String),
}

/// pi-tui's `TuiStopOptions`.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct TuiStopOptions {
    /// Leaves the last frame on the screen instead of clearing it.
    pub preserve_screen: bool,
}

/// Where `unfocus` puts keyboard focus other than Pi's default release.
#[derive(Clone)]
pub enum OverlayUnfocusTarget {
    /// Focus nothing (Pi's `target: null`).
    Nothing,
    /// A component the screen holds.
    Component(ComponentRef),
}

/// The arguments of Pi's `TuiAltScreen` constructor besides the terminal: `show_hardware_cursor` and `log_directory` are its second and third
/// parameters, the rest its `options`. A field left `None` takes Pi's default.
#[derive(Clone, Default)]
pub struct TuiAltScreenOptions {
    pub show_hardware_cursor: Option<bool>,
    /// Where the differential-render overflow crash log goes; the OS temp directory when absent.
    pub log_directory: Option<String>,
    pub wheel_scroll_lines: Option<WheelScrollLines>,
    /// Captures mouse events for viewport scrolling and selection.
    pub mouse: Option<bool>,
    /// Copies a completed text selection; the default is true.
    pub copy_on_select: Option<bool>,
    /// Writes selected text to the clipboard; the host's OSC 52 copy runs when absent.
    pub copy_selection: Option<Arc<dyn Fn(&str) -> CopySelectionOutcome + Send + Sync>>,
    /// Activates an OSC 8 hyperlink on primary-button click.
    pub open_url: Option<Arc<dyn Fn(&str) + Send + Sync>>,
    pub search_match_style: Option<Arc<dyn Fn(&str) -> String + Send + Sync>>,
    pub search_current_match_style: Option<Arc<dyn Fn(&str) -> String + Send + Sync>>,
    pub search_navigation_button_style: Option<Arc<dyn Fn(&str, bool) -> String + Send + Sync>>,
    pub scroll_to_end_indicator: Option<Arc<dyn Fn() -> String + Send + Sync>>,
    pub on_right_click_paste: Option<Arc<dyn Fn() + Send + Sync>>,
}

impl TuiAltScreenOptions {
    fn wire(&self) -> Option<Value> {
        let mut options = Map::new();
        if let Some(lines) = self.wheel_scroll_lines {
            options.insert("wheelScrollLines".into(), lines.to_value());
        }
        if let Some(mouse) = self.mouse {
            options.insert("mouse".into(), Value::Bool(mouse));
        }
        if let Some(copy) = self.copy_on_select {
            options.insert("copyOnSelect".into(), Value::Bool(copy));
        }
        let callbacks: Vec<&str> = [
            ("copySelection", self.copy_selection.is_some()),
            ("openUrl", self.open_url.is_some()),
            ("searchMatchStyle", self.search_match_style.is_some()),
            ("searchCurrentMatchStyle", self.search_current_match_style.is_some()),
            ("searchNavigationButtonStyle", self.search_navigation_button_style.is_some()),
            ("scrollToEndIndicator", self.scroll_to_end_indicator.is_some()),
            ("onRightClickPaste", self.on_right_click_paste.is_some()),
        ]
        .into_iter()
        .filter_map(|(name, present)| present.then_some(name))
        .collect();
        if !callbacks.is_empty() {
            options.insert("callbacks".into(), json!(callbacks));
        }
        (!options.is_empty()).then_some(Value::Object(options))
    }
}

/// A JSON number as JavaScript prints it: an integral value is an integer.
fn number(value: f64) -> Value {
    if value.is_finite() && value.fract() == 0.0 && value.abs() < 9_007_199_254_740_992.0 {
        Value::from(value as i64)
    } else {
        serde_json::Number::from_f64(value).map_or(Value::Null, Value::Number)
    }
}

fn invalid(message: impl Into<String>) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, message.into())
}

fn thin<T: ?Sized>(pointer: *const T) -> usize {
    pointer as *const () as usize
}

#[derive(Default)]
struct Components {
    next: u64,
    by_ptr: HashMap<usize, String>,
    by_key: HashMap<String, ComponentRef>,
}

#[derive(Default)]
struct Listeners {
    next: u64,
    by_id: HashMap<String, InputListener>,
    by_ptr: HashMap<usize, String>,
}

thread_local! {
    /// How many frame callbacks (component `render`, overlay `visible`, the style and indicator options) this thread is running.
    static FRAME_DEPTH: std::cell::Cell<u32> = const { std::cell::Cell::new(0) };
}

/// Marks the thread as running a frame callback until dropped: its `tui.alt` calls carry `inFrame`, which tells the host the call comes from
/// inside the frame being drawn.
struct InFrame;

impl InFrame {
    fn enter() -> Self {
        FRAME_DEPTH.with(|depth| depth.set(depth.get() + 1));
        Self
    }
}

impl Drop for InFrame {
    fn drop(&mut self) {
        FRAME_DEPTH.with(|depth| depth.set(depth.get() - 1));
    }
}

type SchemeFn = Arc<dyn Fn(TerminalColorScheme) + Send + Sync>;

struct Inner {
    id: String,
    ctx: Context,
    terminal: Arc<dyn Terminal>,
    options: TuiAltScreenOptions,
    created: AtomicBool,
    components: Mutex<Components>,
    children: Mutex<Vec<ComponentRef>>,
    listeners: Mutex<Listeners>,
    debug: Mutex<Option<Arc<dyn Fn() + Send + Sync>>>,
    schemes: Mutex<(u64, Vec<(u64, SchemeFn)>)>,
    late_colors: Mutex<HashMap<String, LateColorsFn>>,
    /// The `visible` callback of the overlay each component was last shown with, by component key.
    visible: Mutex<HashMap<String, OverlayVisibleFn>>,
    query_seq: AtomicU64,
}

/// The screens of one connection, by the id the host knows them by; the host's `tui.alt.cb` requests find their screen here.
#[derive(Default)]
pub(crate) struct TuiAltRegistry {
    next: AtomicU64,
    screens: Mutex<HashMap<String, Weak<Inner>>>,
}

impl TuiAltRegistry {
    fn get(&self, id: &str) -> Option<Arc<Inner>> {
        self.screens.lock().unwrap().get(id).and_then(Weak::upgrade)
    }
}

impl Drop for Inner {
    fn drop(&mut self) {
        self.ctx.conn.tui_alt.screens.lock().unwrap().remove(&self.id);
        if self.created.load(Ordering::Acquire) {
            // Nothing can call back into a screen that no longer exists: the host forgets it. A closed connection has already.
            let _ = self.call("dispose", Value::Null);
        }
    }
}

impl Inner {
    fn terminal_block(&self) -> Value {
        json!({"columns": self.terminal.columns(), "rows": self.terminal.rows(), "kitty": self.terminal.kitty_protocol_active()})
    }

    /// One `tui.alt` call. It carries the terminal's current size and kitty state, except `dispose`.
    fn call(&self, op: &str, fields: Value) -> io::Result<Value> {
        let mut args = Map::new();
        args.insert("id".into(), Value::from(self.id.clone()));
        args.insert("op".into(), Value::from(op));
        if let Value::Object(fields) = fields {
            args.extend(fields);
        }
        if op != "dispose" {
            args.insert("terminal".into(), self.terminal_block());
            if FRAME_DEPTH.with(std::cell::Cell::get) > 0 {
                args.insert("inFrame".into(), Value::Bool(true));
            }
        }
        Ok(self.ctx.call_host("tui.alt", Some(Value::Object(args)))?.unwrap_or(Value::Null))
    }

    fn state(&self) -> io::Result<Value> {
        self.call("state", Value::Null)
    }

    fn state_field<T>(&self, name: &str, read: impl FnOnce(&Value) -> Option<T>) -> io::Result<T> {
        let state = self.state()?;
        state.get(name).and_then(read).ok_or_else(|| invalid(format!("host reply to tui.alt state has no {name:?}")))
    }

    fn component_key(&self, component: &ComponentRef) -> String {
        let mut components = self.components.lock().unwrap();
        let pointer = thin(Arc::as_ptr(component));
        if let Some(key) = components.by_ptr.get(&pointer) {
            return key.clone();
        }
        components.next += 1;
        let key = format!("c{}", components.next);
        components.by_ptr.insert(pointer, key.clone());
        components.by_key.insert(key.clone(), component.clone());
        key
    }

    fn held_component_key(&self, component: &ComponentRef) -> Option<String> {
        self.components.lock().unwrap().by_ptr.get(&thin(Arc::as_ptr(component))).cloned()
    }

    fn component(&self, key: &str) -> Result<ComponentRef, String> {
        self.components.lock().unwrap().by_key.get(key).cloned().ok_or_else(|| format!("unknown component {key:?}"))
    }

    /// The `key` and `caps` fields that name a component.
    fn component_fields(&self, component: &ComponentRef) -> (String, Value) {
        let caps = json!({
            "input": component.handles_input(),
            "mouse": component.handles_mouse(),
            "focusable": component.is_focusable(),
            "wantsKeyRelease": component.wants_key_release(),
        });
        (self.component_key(component), caps)
    }

    fn named_call(&self, op: &str, component: Option<&ComponentRef>, extra: Value) -> io::Result<Value> {
        let mut fields = match extra {
            Value::Object(fields) => fields,
            _ => Map::new(),
        };
        match component {
            Some(component) => {
                let (key, caps) = self.component_fields(component);
                fields.insert("key".into(), Value::from(key));
                fields.insert("caps".into(), caps);
            }
            None => {
                fields.insert("key".into(), Value::Null);
            }
        }
        self.call(op, Value::Object(fields))
    }

    fn lines(reply: &Value) -> io::Result<Vec<String>> {
        reply
            .get("lines")
            .and_then(Value::as_array)
            .and_then(|lines| lines.iter().map(|line| line.as_str().map(str::to_string)).collect::<Option<Vec<_>>>())
            .ok_or_else(|| invalid("host reply to tui.alt has no \"lines\" of strings"))
    }

    fn remove_input_listener(&self, listener: &InputListener) -> io::Result<()> {
        let id = {
            let mut listeners = self.listeners.lock().unwrap();
            let Some(id) = listeners.by_ptr.remove(&thin(Arc::as_ptr(listener))) else {
                return Ok(());
            };
            listeners.by_id.remove(&id);
            id
        };
        self.call("removeInputListener", json!({"listenerId": id})).map(|_| ())
    }

    fn overlay(&self, key: &str, action: &str, extra: Value) -> io::Result<OverlayState> {
        let mut fields = match extra {
            Value::Object(fields) => fields,
            _ => Map::new(),
        };
        fields.insert("key".into(), Value::from(key));
        fields.insert("action".into(), Value::from(action));
        Ok(OverlayState::from_wire(&self.call("overlay", Value::Object(fields))?))
    }

    // ─── host -> extension: tui.alt.cb ───────────────────────────────────

    fn terminal_callback(self: &Arc<Self>, args: &Value) -> Result<Value, String> {
        let method = args.get("method").and_then(Value::as_str).unwrap_or("");
        let text = |name: &str| args.get(name).and_then(Value::as_str).unwrap_or("");
        let count = |name: &str, default: u64| args.get(name).and_then(Value::as_f64).map_or(default, |value| value.max(0.0) as u64);
        let terminal = &self.terminal;
        match method {
            "start" => {
                let weak = Arc::downgrade(self);
                let on_input: TerminalInputFn = {
                    let weak = weak.clone();
                    Arc::new(move |data: &str| {
                        let screen = weak.upgrade().ok_or_else(screen_gone)?;
                        screen.call("input", json!({"data": data})).map(|_| ())
                    })
                };
                let on_resize: TerminalResizeFn = Arc::new(move || {
                    let screen = weak.upgrade().ok_or_else(screen_gone)?;
                    screen.call("resize", Value::Null).map(|_| ())
                });
                terminal.start(on_input, on_resize);
            }
            "stop" => terminal.stop(),
            "write" => terminal.write(text("data")),
            "moveBy" => terminal.move_by(args.get("lines").and_then(Value::as_i64).unwrap_or(0) as i32),
            "hideCursor" => terminal.hide_cursor(),
            "showCursor" => terminal.show_cursor(),
            "clearLine" => terminal.clear_line(),
            "clearFromCursor" => terminal.clear_from_cursor(),
            "clearScreen" => terminal.clear_screen(),
            "setTitle" => terminal.set_title(text("title")),
            "setProgress" => terminal.set_progress(args.get("active").and_then(Value::as_bool).unwrap_or(false)),
            "setProgramStatus" => {
                let status = args.get("status").cloned().unwrap_or(Value::Null);
                let status: ProgramStatus = serde_json::from_value(status).map_err(|err| format!("setProgramStatus: {err}"))?;
                terminal.set_program_status(&status);
            }
            "restoreOnExit" => terminal.restore_on_exit(text("before"), text("after")),
            "drainInput" => terminal.drain_input(count("maxMs", 1000), count("idleMs", 50)),
            other => return Err(format!("unknown terminal method {other:?}")),
        }
        Ok(json!({}))
    }

    fn option_callback(&self, args: &Value) -> Result<Value, String> {
        let name = args.get("name").and_then(Value::as_str).unwrap_or("");
        let positional = args.get("args").and_then(Value::as_array).map(Vec::as_slice).unwrap_or_default();
        let text = |index: usize| positional.get(index).and_then(Value::as_str).unwrap_or("");
        let options = &self.options;
        let missing = || format!("option callback {name:?} is not set");
        let _frame = matches!(name, "searchMatchStyle" | "searchCurrentMatchStyle" | "searchNavigationButtonStyle" | "scrollToEndIndicator").then(InFrame::enter);
        let value = match name {
            "copySelection" => match (options.copy_selection.as_ref().ok_or_else(missing)?)(text(0)) {
                CopySelectionOutcome::Copied => Value::Bool(true),
                CopySelectionOutcome::Failed => Value::Bool(false),
                CopySelectionOutcome::Message(message) => Value::from(message),
            },
            "openUrl" => {
                (options.open_url.as_ref().ok_or_else(missing)?)(text(0));
                Value::Null
            }
            "searchMatchStyle" => Value::from((options.search_match_style.as_ref().ok_or_else(missing)?)(text(0))),
            "searchCurrentMatchStyle" => Value::from((options.search_current_match_style.as_ref().ok_or_else(missing)?)(text(0))),
            "searchNavigationButtonStyle" => {
                let hovered = positional.get(1).and_then(Value::as_bool).unwrap_or(false);
                Value::from((options.search_navigation_button_style.as_ref().ok_or_else(missing)?)(text(0), hovered))
            }
            "scrollToEndIndicator" => Value::from((options.scroll_to_end_indicator.as_ref().ok_or_else(missing)?)()),
            "onRightClickPaste" => {
                (options.on_right_click_paste.as_ref().ok_or_else(missing)?)();
                Value::Null
            }
            other => return Err(format!("unknown option callback {other:?}")),
        };
        Ok(json!({"value": value}))
    }

    fn callback(self: &Arc<Self>, args: &Value) -> Result<Value, String> {
        let kind = args.get("kind").and_then(Value::as_str).unwrap_or("");
        let key = args.get("key").and_then(Value::as_str).unwrap_or("");
        match kind {
            "terminal" => self.terminal_callback(args),
            "render" => {
                let width = args.get("width").and_then(Value::as_u64).unwrap_or(0) as u32;
                let component = self.component(key)?;
                let _frame = InFrame::enter();
                Ok(json!({"lines": component.render(width)}))
            }
            "input" => {
                self.component(key)?.handle_input(args.get("data").and_then(Value::as_str).unwrap_or(""));
                Ok(json!({}))
            }
            "mouse" => {
                let event: MouseEvent = serde_json::from_value(args.get("event").cloned().unwrap_or(Value::Null)).map_err(|err| format!("mouse event: {err}"))?;
                let result = self.component(key)?.handle_mouse(&event);
                Ok(json!({"result": result.map(|result| serde_json::to_value(result).unwrap_or(Value::Null))}))
            }
            "focus" => {
                self.component(key)?.set_focused(args.get("focused").and_then(Value::as_bool).unwrap_or(false));
                Ok(json!({}))
            }
            "invalidate" => {
                self.component(key)?.invalidate();
                Ok(json!({}))
            }
            "listener" => {
                let id = args.get("listenerId").and_then(Value::as_str).unwrap_or("");
                // A listener removed while the host's callback was on its way sees nothing, as one Pi removed.
                let listener = self.listeners.lock().unwrap().by_id.get(id).cloned();
                let result = listener.and_then(|listener| listener(args.get("data").and_then(Value::as_str).unwrap_or("")));
                let mut response = Map::new();
                if let Some(result) = result {
                    if result.consume {
                        response.insert("consume".into(), Value::Bool(true));
                    }
                    if let Some(data) = result.data {
                        response.insert("data".into(), Value::from(data));
                    }
                }
                Ok(Value::Object(response))
            }
            "debug" => {
                let debug = self.debug.lock().unwrap().clone();
                if let Some(debug) = debug {
                    debug();
                }
                Ok(json!({}))
            }
            "colorScheme" => {
                let scheme = match args.get("scheme").and_then(Value::as_str) {
                    Some("dark") => TerminalColorScheme::Dark,
                    Some("light") => TerminalColorScheme::Light,
                    other => return Err(format!("unknown color scheme {other:?}")),
                };
                let listeners: Vec<SchemeFn> = self.schemes.lock().unwrap().1.iter().map(|(_, listener)| listener.clone()).collect();
                for listener in listeners {
                    listener(scheme);
                }
                Ok(json!({}))
            }
            "lateColors" => {
                let query = args.get("queryId").and_then(Value::as_str).unwrap_or("");
                let callback = self.late_colors.lock().unwrap().get(query).cloned();
                if let Some(callback) = callback {
                    callback(TerminalColors::from_value(args.get("colors").unwrap_or(&Value::Null)));
                }
                Ok(json!({}))
            }
            "visible" => {
                let size = |name: &str| args.get(name).and_then(Value::as_u64).unwrap_or(0) as u32;
                let visible = self.visible.lock().unwrap().get(key).cloned();
                let _frame = InFrame::enter();
                Ok(json!({"value": visible.is_none_or(|visible| visible(size("termWidth"), size("termHeight")))}))
            }
            "option" => self.option_callback(args),
            other => Err(format!("unknown tui.alt callback kind {other:?}")),
        }
    }
}

fn screen_gone() -> io::Error {
    io::Error::new(io::ErrorKind::NotConnected, "the TuiAltScreen was dropped")
}

/// The host's `tui.alt.cb` request: runs a terminal, component, listener or option callback of the screen it names.
pub(crate) fn dispatch_callback(registry: &TuiAltRegistry, args: &Value) -> Result<Value, String> {
    let id = args.get("id").and_then(Value::as_str).unwrap_or("");
    let screen = registry.get(id).ok_or_else(|| format!("unknown TuiAltScreen {id:?}"))?;
    screen.callback(args)
}

/// pi-tui's `TuiAltScreen`.
pub struct TuiAltScreen {
    inner: Arc<Inner>,
}

impl TuiAltScreen {
    /// Creates a screen in the host that draws on `terminal`. `ctx` carries the connection and is kept for the screen's lifetime.
    pub fn new(ctx: &Context, terminal: Arc<dyn Terminal>, options: TuiAltScreenOptions) -> io::Result<Self> {
        let registry = &ctx.conn.tui_alt;
        let id = format!("t{}", registry.next.fetch_add(1, Ordering::Relaxed) + 1);
        let inner = Arc::new(Inner {
            id: id.clone(),
            ctx: ctx.clone(),
            terminal,
            options,
            created: AtomicBool::new(false),
            components: Mutex::default(),
            children: Mutex::default(),
            listeners: Mutex::default(),
            debug: Mutex::default(),
            schemes: Mutex::default(),
            late_colors: Mutex::default(),
            visible: Mutex::default(),
            query_seq: AtomicU64::new(0),
        });
        registry.screens.lock().unwrap().insert(id, Arc::downgrade(&inner));
        let mut fields = Map::new();
        if let Some(show) = inner.options.show_hardware_cursor {
            fields.insert("showHardwareCursor".into(), Value::Bool(show));
        }
        if let Some(directory) = &inner.options.log_directory {
            fields.insert("logDirectory".into(), Value::from(directory.clone()));
        }
        if let Some(options) = inner.options.wire() {
            fields.insert("options".into(), options);
        }
        inner.call("create", Value::Object(fields))?;
        inner.created.store(true, Ordering::Release);
        Ok(Self { inner })
    }

    /// Pi's `mode`: always `"fullscreen"`.
    pub fn mode(&self) -> &'static str {
        "fullscreen"
    }

    /// Pi's `[Symbol.VIEWPORT_TUI]`: true, as every `TuiAltScreen` is a viewport TUI.
    pub fn viewport_tui(&self) -> bool {
        true
    }

    /// The root's children, in order.
    pub fn children(&self) -> Vec<ComponentRef> {
        self.inner.children.lock().unwrap().clone()
    }

    /// The terminal the screen was created with.
    pub fn terminal(&self) -> Arc<dyn Terminal> {
        self.inner.terminal.clone()
    }

    pub fn viewport_top(&self) -> io::Result<i64> {
        self.inner.state_field("viewportTop", Value::as_i64)
    }

    pub fn is_following_output(&self) -> io::Result<bool> {
        self.inner.state_field("isFollowingOutput", Value::as_bool)
    }

    /// The number of full repaints of the screen.
    pub fn full_redraws(&self) -> io::Result<u64> {
        self.inner.state_field("fullRedraws", Value::as_u64)
    }

    pub fn has_overlay_entries(&self) -> io::Result<bool> {
        self.inner.state_field("hasOverlayEntries", Value::as_bool)
    }

    pub fn has_overlay(&self) -> io::Result<bool> {
        self.inner.state_field("hasOverlay", Value::as_bool)
    }

    pub fn has_active_selection(&self) -> io::Result<bool> {
        self.inner.state_field("hasActiveSelection", Value::as_bool)
    }

    pub fn get_copy_on_select(&self) -> io::Result<bool> {
        self.inner.state_field("copyOnSelect", Value::as_bool)
    }

    pub fn get_show_hardware_cursor(&self) -> io::Result<bool> {
        self.inner.state_field("showHardwareCursor", Value::as_bool)
    }

    pub fn get_clear_on_shrink(&self) -> io::Result<bool> {
        self.inner.state_field("clearOnShrink", Value::as_bool)
    }

    /// The component that has keyboard focus, when it is one this screen was given: a component of the host's own (the search box) is `None`.
    pub fn get_focused_component(&self) -> io::Result<Option<ComponentRef>> {
        let state = self.inner.state()?;
        Ok(state.get("focusedKey").and_then(Value::as_str).and_then(|key| self.inner.component(key).ok()))
    }

    /// The lines of the last frame drawn on the screen.
    pub fn get_screen_lines(&self) -> io::Result<Vec<String>> {
        Inner::lines(&self.inner.call("getScreenLines", Value::Null)?)
    }

    /// Renders the root (the layout root, else the children) at `width`.
    pub fn render(&self, width: u32) -> io::Result<Vec<String>> {
        Inner::lines(&self.inner.call("render", json!({"width": width}))?)
    }

    /// Enters the alternate screen and starts the terminal; the first render is requested.
    pub fn start(&self) -> io::Result<()> {
        self.inner.call("start", Value::Null).map(|_| ())
    }

    /// Stops the terminal and leaves the alternate screen.
    pub fn stop(&self, options: TuiStopOptions) -> io::Result<()> {
        let fields = if options.preserve_screen { json!({"preserveScreen": true}) } else { Value::Null };
        self.inner.call("stop", fields).map(|_| ())
    }

    pub fn render_now(&self, force: bool) -> io::Result<()> {
        self.inner.call("renderNow", if force { json!({"force": true}) } else { Value::Null }).map(|_| ())
    }

    pub fn request_render(&self, force: bool) -> io::Result<()> {
        self.inner.call("requestRender", if force { json!({"force": true}) } else { Value::Null }).map(|_| ())
    }

    pub fn invalidate(&self) -> io::Result<()> {
        self.inner.call("invalidate", Value::Null).map(|_| ())
    }

    pub fn scroll_by(&self, lines: i64) -> io::Result<()> {
        self.inner.call("scrollBy", json!({"lines": lines})).map(|_| ())
    }

    pub fn scroll_to_top(&self) -> io::Result<()> {
        self.inner.call("scrollToTop", Value::Null).map(|_| ())
    }

    pub fn scroll_to_bottom(&self) -> io::Result<()> {
        self.inner.call("scrollToBottom", Value::Null).map(|_| ())
    }

    pub fn reset_text_selection(&self) -> io::Result<()> {
        self.inner.call("resetTextSelection", Value::Null).map(|_| ())
    }

    /// Shows `message` on the screen for `duration_ms` (Pi's default when `None`).
    pub fn flash(&self, message: &str, duration_ms: Option<u64>) -> io::Result<()> {
        let mut fields = json!({"message": message});
        if let Some(duration) = duration_ms {
            fields["durationMs"] = Value::from(duration);
        }
        self.inner.call("flash", fields).map(|_| ())
    }

    pub fn set_wheel_scroll_lines(&self, lines: WheelScrollLines) -> io::Result<()> {
        self.inner.call("setWheelScrollLines", json!({"lines": lines.to_value()})).map(|_| ())
    }

    pub fn set_copy_on_select(&self, enabled: bool) -> io::Result<()> {
        self.inner.call("setCopyOnSelect", json!({"enabled": enabled})).map(|_| ())
    }

    pub fn set_show_hardware_cursor(&self, enabled: bool) -> io::Result<()> {
        self.inner.call("setShowHardwareCursor", json!({"enabled": enabled})).map(|_| ())
    }

    pub fn set_clear_on_shrink(&self, enabled: bool) -> io::Result<()> {
        self.inner.call("setClearOnShrink", json!({"enabled": enabled})).map(|_| ())
    }

    /// Copies the active text selection; false when there is none or the copy failed. Blocks until the copy settled.
    pub fn copy_active_selection_to_clipboard(&self) -> io::Result<bool> {
        let reply = self.inner.call("copyActiveSelectionToClipboard", Value::Null)?;
        reply.get("value").and_then(Value::as_bool).ok_or_else(|| invalid("host reply to tui.alt copyActiveSelectionToClipboard has no \"value\""))
    }

    pub fn add_child(&self, component: &ComponentRef) -> io::Result<()> {
        self.inner.named_call("addChild", Some(component), Value::Null)?;
        self.inner.children.lock().unwrap().push(component.clone());
        Ok(())
    }

    /// Removes the first occurrence of `component` from the children, as Pi's `Container.removeChild` does.
    pub fn remove_child(&self, component: &ComponentRef) -> io::Result<()> {
        let Some(key) = self.inner.held_component_key(component) else {
            return Ok(());
        };
        self.inner.call("removeChild", json!({"key": key}))?;
        let mut children = self.inner.children.lock().unwrap();
        if let Some(index) = children.iter().position(|child| thin(Arc::as_ptr(child)) == thin(Arc::as_ptr(component))) {
            children.remove(index);
        }
        Ok(())
    }

    /// Removes every child.
    pub fn clear(&self) -> io::Result<()> {
        self.inner.call("clear", Value::Null)?;
        self.inner.children.lock().unwrap().clear();
        Ok(())
    }

    /// Lays the screen out with `component` as the document instead of the children; `None` returns to the children.
    pub fn set_layout_root(&self, component: Option<&ComponentRef>) -> io::Result<()> {
        self.inner.named_call("setLayoutRoot", component, Value::Null).map(|_| ())
    }

    /// Gives keyboard focus to `component`, or to nothing.
    pub fn set_focus(&self, component: Option<&ComponentRef>) -> io::Result<()> {
        self.inner.named_call("setFocus", component, Value::Null).map(|_| ())
    }

    /// Shows `component` as an overlay; `None` options are Pi's defaults.
    pub fn show_overlay(&self, component: &ComponentRef, options: Option<TuiOverlayOptions>) -> io::Result<AltScreenOverlayHandle> {
        let inner = &self.inner;
        let options = options.unwrap_or_default();
        let (key, caps) = inner.component_fields(component);
        let mut fields = Map::new();
        fields.insert("key".into(), Value::from(key.clone()));
        fields.insert("caps".into(), caps);
        if let Some(layout) = options.layout {
            fields.insert("options".into(), layout);
        }
        // The host may ask `visible` as soon as it mounts the overlay, before this call returns.
        match options.visible {
            Some(visible) => {
                fields.insert("visible".into(), Value::Bool(true));
                inner.visible.lock().unwrap().insert(key.clone(), visible);
            }
            None => {
                inner.visible.lock().unwrap().remove(&key);
            }
        }
        inner.call("showOverlay", Value::Object(fields))?;
        Ok(AltScreenOverlayHandle { inner: self.inner.clone(), key })
    }

    /// Hides the topmost overlay.
    pub fn hide_overlay(&self) -> io::Result<()> {
        self.inner.call("hideOverlay", Value::Null).map(|_| ())
    }

    /// Adds a listener that sees every input before the components. Adding a listener already added does nothing; the returned function
    /// removes it.
    pub fn add_input_listener(&self, listener: InputListener) -> io::Result<Unsubscribe> {
        let inner = &self.inner;
        let pointer = thin(Arc::as_ptr(&listener));
        let added = {
            let mut listeners = inner.listeners.lock().unwrap();
            if listeners.by_ptr.contains_key(&pointer) {
                None
            } else {
                listeners.next += 1;
                let id = format!("l{}", listeners.next);
                listeners.by_id.insert(id.clone(), listener.clone());
                listeners.by_ptr.insert(pointer, id.clone());
                Some(id)
            }
        };
        if let Some(id) = added {
            if let Err(err) = inner.call("addInputListener", json!({"listenerId": id})) {
                let mut listeners = inner.listeners.lock().unwrap();
                listeners.by_id.remove(&id);
                listeners.by_ptr.remove(&pointer);
                return Err(err);
            }
        }
        let weak = Arc::downgrade(inner);
        Ok(Box::new(move || match weak.upgrade() {
            Some(inner) => inner.remove_input_listener(&listener),
            None => Ok(()),
        }))
    }

    pub fn remove_input_listener(&self, listener: &InputListener) -> io::Result<()> {
        self.inner.remove_input_listener(listener)
    }

    /// Sets (or with `None` clears) Pi's `onDebug`, called on Shift+Ctrl+D.
    pub fn set_on_debug(&self, callback: Option<Arc<dyn Fn() + Send + Sync>>) -> io::Result<()> {
        let enabled = callback.is_some();
        *self.inner.debug.lock().unwrap() = callback;
        self.inner.call("setOnDebug", json!({"enabled": enabled})).map(|_| ())
    }

    /// Subscribes to the terminal's color scheme changes, delivered while [`Self::set_terminal_color_scheme_notifications`] is on.
    pub fn on_terminal_color_scheme_change(&self, listener: impl Fn(TerminalColorScheme) + Send + Sync + 'static) -> Unsubscribe {
        let id = {
            let mut schemes = self.inner.schemes.lock().unwrap();
            schemes.0 += 1;
            let id = schemes.0;
            schemes.1.push((id, Arc::new(listener)));
            id
        };
        let weak = Arc::downgrade(&self.inner);
        Box::new(move || {
            if let Some(inner) = weak.upgrade() {
                inner.schemes.lock().unwrap().1.retain(|(entry, _)| *entry != id);
            }
            Ok(())
        })
    }

    pub fn set_terminal_color_scheme_notifications(&self, enabled: bool) -> io::Result<()> {
        self.inner.call("setTerminalColorSchemeNotifications", json!({"enabled": enabled})).map(|_| ())
    }

    /// Asks the terminal for its colors; blocks until its device attributes reply or the timeout.
    pub fn query_terminal_colors(&self, options: QueryTerminalColorsOptions) -> io::Result<TerminalColors> {
        let inner = &self.inner;
        let mut fields = json!({"timeoutMs": number(options.timeout_ms), "onLateReply": options.on_late_reply.is_some()});
        let mut query = None;
        if let Some(late) = options.on_late_reply {
            let id = format!("q{}", inner.query_seq.fetch_add(1, Ordering::Relaxed) + 1);
            inner.late_colors.lock().unwrap().insert(id.clone(), late);
            fields["queryId"] = Value::from(id.clone());
            query = Some(id);
        }
        match inner.call("queryTerminalColors", fields) {
            Ok(reply) => Ok(TerminalColors::from_value(reply.get("colors").unwrap_or(&Value::Null))),
            Err(err) => {
                if let Some(id) = query {
                    inner.late_colors.lock().unwrap().remove(&id);
                }
                Err(err)
            }
        }
    }

    /// Delivers a mouse event to the screen as the terminal would. `None` when nothing handled it.
    pub fn handle_mouse(&self, event: &MouseEvent) -> io::Result<Option<MouseEventResult>> {
        let event = serde_json::to_value(event).map_err(io::Error::other)?;
        let reply = self.inner.call("handleMouse", json!({"event": event}))?;
        match reply.get("result") {
            None | Some(Value::Null) => Ok(None),
            Some(result) => serde_json::from_value(result.clone()).map(Some).map_err(|err| invalid(format!("host reply to tui.alt handleMouse: {err}"))),
        }
    }
}

/// pi-tui's `OverlayHandle` of an overlay [`TuiAltScreen::show_overlay`] mounted. Its reads ask the host, which owns the overlay's state.
#[derive(Clone)]
pub struct AltScreenOverlayHandle {
    inner: Arc<Inner>,
    key: String,
}

impl AltScreenOverlayHandle {
    /// Removes the overlay.
    pub fn hide(&self) -> io::Result<()> {
        self.inner.overlay(&self.key, "hide", Value::Null).map(|_| ())
    }

    pub fn set_hidden(&self, hidden: bool) -> io::Result<()> {
        self.inner.overlay(&self.key, "setHidden", json!({"hidden": hidden})).map(|_| ())
    }

    pub fn is_hidden(&self) -> io::Result<bool> {
        Ok(self.inner.overlay(&self.key, "state", Value::Null)?.hidden)
    }

    pub fn focus(&self) -> io::Result<()> {
        self.inner.overlay(&self.key, "focus", Value::Null).map(|_| ())
    }

    /// Takes keyboard focus from the overlay: with `None` to where Pi's default release puts it, else exactly `target`.
    pub fn unfocus(&self, target: Option<OverlayUnfocusTarget>) -> io::Result<()> {
        let extra = match target {
            None => Value::Null,
            Some(OverlayUnfocusTarget::Nothing) => json!({"target": null}),
            Some(OverlayUnfocusTarget::Component(component)) => {
                let key = self
                    .inner
                    .held_component_key(&component)
                    .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidInput, "unfocus target is not a component this screen holds"))?;
                json!({"target": {"key": key}})
            }
        };
        self.inner.overlay(&self.key, "unfocus", extra).map(|_| ())
    }

    pub fn is_focused(&self) -> io::Result<bool> {
        Ok(self.inner.overlay(&self.key, "state", Value::Null)?.focused)
    }

    /// The overlay's last rendered rectangle, `None` while it has none.
    pub fn bounds(&self) -> io::Result<Option<OverlayBounds>> {
        Ok(self.inner.overlay(&self.key, "state", Value::Null)?.bounds)
    }
}

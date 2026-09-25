//! Context passed to tool/command/event handlers.
//!
//! Provides access to the host's UI and session state. Every method maps 1:1
//! to a Go SDK `Context` method and a wire-protocol `call` message.

use crate::login::LoginDefinition;
use crate::protocol::{CallResultMsg, Connection, MAX_FRAME_SIZE};
use std::collections::{HashMap, VecDeque};
use std::fs::File;
use std::io::{self, BufRead, BufReader};
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::atomic::{AtomicBool, AtomicU32, AtomicU64, Ordering};
use std::sync::mpsc::{Receiver, SyncSender, TrySendError, sync_channel};
use std::sync::{Arc, Condvar, Mutex};
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

/// Result of one focused component input event.
pub struct RemoteComponentResult {
    pub done: bool,
    pub value: Option<serde_json::Value>,
}

impl RemoteComponentResult {
    pub fn pending() -> Self {
        Self {
            done: false,
            value: None,
        }
    }

    pub fn done(value: Option<serde_json::Value>) -> Self {
        Self { done: true, value }
    }
}

pub type RemoteComponentInvalidate = Arc<dyn Fn() + Send + Sync>;

/// A subprocess component rendered locally while the host overlay owns focus.
pub trait RemoteComponent: Send {
    fn render(&self, width: u32) -> Vec<String>;
    fn handle_input(&mut self, data: &str) -> Result<RemoteComponentResult, String>;
    fn set_invalidate(&mut self, _invalidate: Option<RemoteComponentInvalidate>) {}
    fn dispose(&mut self) {}
}

pub(crate) struct RemoteComponentState {
    pub(crate) component: Box<dyn RemoteComponent>,
    pub(crate) last_lines: Vec<String>,
    pub(crate) seq: u64,
    pub(crate) last_render: Option<Instant>,
}

enum RemoteComponentEvent {
    Render,
    Input(String),
    Stop,
}

pub(crate) struct RemoteOverlay {
    state: Mutex<RemoteComponentState>,
    events: SyncSender<RemoteComponentEvent>,
    pub(crate) active: AtomicBool,
    render_pending: AtomicBool,
}

impl RemoteOverlay {
    pub(crate) fn request_render(&self) {
        if !self.active.load(Ordering::Acquire) || self.render_pending.swap(true, Ordering::AcqRel)
        {
            return;
        }
        match self.events.try_send(RemoteComponentEvent::Render) {
            Ok(()) => {}
            Err(TrySendError::Full(RemoteComponentEvent::Render)) => {
                self.render_pending.store(false, Ordering::Release);
            }
            Err(TrySendError::Disconnected(_)) => {
                self.render_pending.store(false, Ordering::Release);
                self.active.store(false, Ordering::Release);
            }
            Err(TrySendError::Full(_)) => unreachable!(),
        }
    }

    pub(crate) fn send_input(&self, data: String) -> Result<(), String> {
        if !self.active.load(Ordering::Acquire) {
            return Ok(());
        }
        self.events
            .try_send(RemoteComponentEvent::Input(data))
            .map_err(|err| match err {
                TrySendError::Full(_) => "focused input queue is full".to_string(),
                TrySendError::Disconnected(_) => "focused component is closed".to_string(),
            })
    }

    fn stop(&self) {
        self.active.store(false, Ordering::Release);
        let _ = self.events.try_send(RemoteComponentEvent::Stop);
    }
}

pub(crate) type RemoteComponentRef = Arc<RemoteOverlay>;
pub(crate) type RemoteComponents = Arc<Mutex<HashMap<String, RemoteComponentRef>>>;

fn render_remote_component_frame(
    conn: &Connection,
    key: &str,
    overlay: &RemoteOverlay,
    width: u32,
) -> io::Result<()> {
    let mut state = overlay.state.lock().unwrap();
    let lines = catch_unwind(AssertUnwindSafe(|| state.component.render(width)))
        .map_err(|_| io::Error::other("focused render panicked"))?;
    state.last_render = Some(Instant::now());
    if lines == state.last_lines {
        return Ok(());
    }
    state.last_lines.clone_from(&lines);
    state.seq += 1;
    let seq = state.seq;
    drop(state);
    conn.notify(
        "ui.custom.render",
        Some(serde_json::json!({"key": key, "lines": lines, "width": width, "seq": seq})),
    )
}

fn run_remote_component_worker(
    conn: Arc<Connection>,
    key: String,
    overlay: RemoteComponentRef,
    width: Arc<AtomicU32>,
    events: Receiver<RemoteComponentEvent>,
    done: std::sync::mpsc::Sender<()>,
) {
    while overlay.active.load(Ordering::Acquire) {
        let Ok(event) = events.recv() else {
            break;
        };
        if !overlay.active.load(Ordering::Acquire) || matches!(event, RemoteComponentEvent::Stop) {
            break;
        }
        let result = match event {
            RemoteComponentEvent::Render => {
                overlay.render_pending.store(false, Ordering::Release);
                let delay = {
                    let state = overlay.state.lock().unwrap();
                    state
                        .last_render
                        .map(|last| Duration::from_millis(16).saturating_sub(last.elapsed()))
                        .unwrap_or_default()
                };
                if !delay.is_zero() {
                    thread::sleep(delay);
                }
                if !overlay.active.load(Ordering::Acquire) {
                    break;
                }
                render_remote_component_frame(&conn, &key, &overlay, width.load(Ordering::Relaxed))
            }
            RemoteComponentEvent::Input(data) => {
                let input = {
                    let mut state = overlay.state.lock().unwrap();
                    catch_unwind(AssertUnwindSafe(|| state.component.handle_input(&data)))
                        .map_err(|_| "focused input panicked".to_string())
                };
                match input {
                    Ok(Ok(result)) if result.done => {
                        overlay.active.store(false, Ordering::Release);
                        conn.notify(
                            "ui.custom.close",
                            Some(serde_json::json!({"key": &key, "result": result.value})),
                        )
                    }
                    Ok(Ok(_)) => render_remote_component_frame(
                        &conn,
                        &key,
                        &overlay,
                        width.load(Ordering::Relaxed),
                    ),
                    Ok(Err(err)) | Err(err) => Err(io::Error::other(err)),
                }
            }
            RemoteComponentEvent::Stop => break,
        };
        if let Err(err) = result {
            overlay.active.store(false, Ordering::Release);
            let _ = conn.notify(
                "ui.custom.close",
                Some(serde_json::json!({"key": &key, "error": err.to_string()})),
            );
            break;
        }
    }
    let _ = done.send(());
}

fn dispose_remote_component(overlay: &RemoteOverlay) {
    let mut state = overlay
        .state
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner);
    let _ = catch_unwind(AssertUnwindSafe(|| state.component.set_invalidate(None)));
    let _ = catch_unwind(AssertUnwindSafe(|| state.component.dispose()));
}

/// A raw terminal-input handler's verdict on one chunk, mirroring upstream's
/// `{ consume?: boolean; data?: string }`.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct TerminalInputResult {
    /// Suppresses normal handling of the chunk, so the editor and keybindings
    /// never see it.
    pub consume: bool,
    /// When set, replaces the chunk for later handlers and for normal
    /// handling. An empty replacement drops the chunk.
    pub data: Option<String>,
}

/// A handler receiving every raw input chunk before the editor does.
///
/// Like upstream's synchronous listener, the input waits for the verdict, so a
/// handler should return promptly.
pub type TerminalInputHandler = Box<dyn Fn(&str) -> TerminalInputResult + Send + Sync>;

pub(crate) type TerminalInputSubs = Arc<Mutex<Vec<(u64, Arc<TerminalInputHandler>)>>>;

/// Width handlers, invoked after the shared width is stored so a handler that
/// calls [`Context::width`] observes the new value.
pub(crate) type WidthChangeHandler = Box<dyn Fn(u32) + Send + Sync>;
pub(crate) type WidthChangeSubs = Arc<Mutex<Vec<(u64, Arc<WidthChangeHandler>)>>>;

fn call_result_to_io(result: CallResultMsg) -> io::Result<()> {
    if let Some(err) = result.error {
        let code = err.code.unwrap_or_else(|| "call_failed".to_string());
        return Err(io::Error::new(
            io::ErrorKind::Other,
            format!("{}: {}", code, err.message),
        ));
    }
    Ok(())
}

fn call_result_value(result: CallResultMsg) -> io::Result<serde_json::Value> {
    if let Some(err) = result.error {
        let code = err.code.unwrap_or_else(|| "call_failed".to_string());
        return Err(io::Error::new(
            io::ErrorKind::Other,
            format!("{}: {}", code, err.message),
        ));
    }
    Ok(result.result.unwrap_or_default())
}

pub(crate) type ModelStreams = Arc<Mutex<HashMap<String, Arc<ModelEventStream>>>>;

#[derive(Default)]
struct ModelStreamState {
    events: VecDeque<serde_json::Value>,
    terminal: bool,
    result: Option<serde_json::Value>,
}

pub struct ModelEventStream {
    state: Mutex<ModelStreamState>,
    changed: Condvar,
}

impl ModelEventStream {
    fn new() -> Self {
        Self {
            state: Mutex::new(ModelStreamState::default()),
            changed: Condvar::new(),
        }
    }
    pub(crate) fn push(&self, event: serde_json::Value) {
        let mut state = self.state.lock().unwrap();
        if state.terminal {
            return;
        }
        if matches!(
            event.get("type").and_then(|v| v.as_str()),
            Some("done" | "error")
        ) {
            state.terminal = true;
            state.result = if event.get("type").and_then(|v| v.as_str()) == Some("done") {
                event.get("message").cloned()
            } else {
                event.get("error").cloned()
            };
        }
        state.events.push_back(event);
        self.changed.notify_all();
    }
    pub fn next(&self) -> Option<serde_json::Value> {
        let mut state = self.state.lock().unwrap();
        loop {
            if let Some(event) = state.events.pop_front() {
                return Some(event);
            }
            if state.terminal {
                return None;
            }
            state = self.changed.wait(state).unwrap();
        }
    }
    pub fn result(&self) -> Option<serde_json::Value> {
        let mut state = self.state.lock().unwrap();
        while !state.terminal {
            state = self.changed.wait(state).unwrap();
        }
        state.result.clone()
    }
}

fn model_stream_error_event(message: &str, model: &serde_json::Value) -> serde_json::Value {
    let provider = model
        .get("provider")
        .and_then(|value| value.as_str())
        .or_else(|| {
            model
                .get("provider")
                .and_then(|value| value.get("id"))
                .and_then(|value| value.as_str())
        })
        .unwrap_or_default();
    let model_id = model
        .get("modelId")
        .and_then(|value| value.as_str())
        .or_else(|| model.get("id").and_then(|value| value.as_str()))
        .unwrap_or_default();
    let api = model
        .get("api")
        .and_then(|value| value.as_str())
        .unwrap_or_default();
    let timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64;
    serde_json::json!({
        "type":"error", "reason":"error",
        "error":{
            "role":"assistant", "content":[], "api":api, "provider":provider, "model":model_id,
            "usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},
            "stopReason":"error", "errorMessage":message, "timestamp":timestamp
        }
    })
}

/// ModelRegistry exposes Session model discovery, request authentication, and
/// model operations through the host-owned runtime.
pub struct ModelRegistry {
    conn: Arc<Connection>,
    request_id: String,
    streams: ModelStreams,
    sequence: Arc<AtomicU64>,
}

impl ModelRegistry {
    pub fn find(&self, provider_id: &str, model_id: &str) -> Option<serde_json::Value> {
        let parent = (!self.request_id.is_empty()).then_some(self.request_id.as_str());
        let result = self
            .conn
            .call_for(
                parent,
                "getModel",
                Some(serde_json::json!({"provider":provider_id,"modelId":model_id})),
            )
            .ok()?;
        let model = call_result_value(result).ok()?;
        (!model.is_null()).then_some(model)
    }

    pub fn get_api_key_and_headers(
        &self,
        model: &serde_json::Value,
    ) -> io::Result<serde_json::Value> {
        let provider = model
            .get("provider")
            .and_then(|value| value.as_str())
            .or_else(|| {
                model
                    .get("provider")
                    .and_then(|value| value.get("id"))
                    .and_then(|value| value.as_str())
            })
            .unwrap_or_default();
        let model_id = model
            .get("modelId")
            .and_then(|value| value.as_str())
            .or_else(|| model.get("id").and_then(|value| value.as_str()))
            .unwrap_or_default();
        let parent = (!self.request_id.is_empty()).then_some(self.request_id.as_str());
        self.conn
            .call_for(
                parent,
                "getModelAuth",
                Some(serde_json::json!({"provider":provider,"modelId":model_id})),
            )
            .and_then(call_result_value)
    }

    pub fn stream(
        &self,
        model: serde_json::Value,
        request: serde_json::Value,
        options: serde_json::Value,
    ) -> Arc<ModelEventStream> {
        let id = format!(
            "model-stream-{}",
            self.sequence.fetch_add(1, Ordering::Relaxed) + 1
        );
        let stream = Arc::new(ModelEventStream::new());
        self.streams
            .lock()
            .unwrap()
            .insert(id.clone(), stream.clone());
        let conn = self.conn.clone();
        let parent = self.request_id.clone();
        let streams = self.streams.clone();
        let output = stream.clone();
        thread::spawn(move || {
            let mut merged = request.as_object().cloned().unwrap_or_default();
            if let Some(values) = options.as_object() {
                for (key, value) in values {
                    merged.insert(key.clone(), value.clone());
                }
            }
            let error_model = model.clone();
            let result = conn.call_for(
                (!parent.is_empty()).then_some(parent.as_str()),
                "modelStream",
                Some(serde_json::json!({"streamId": id, "model": model, "request": merged})),
            );
            let error = match result {
                Err(error) => Some(error.to_string()),
                Ok(result) => result.error.map(|error| match error.code.as_deref() {
                    Some(code) if !code.is_empty() => format!("{code}: {}", error.message),
                    _ => error.message,
                }),
            };
            if let Some(error) = error {
                output.push(model_stream_error_event(&error, &error_model));
            }
            streams.lock().unwrap().remove(&id);
        });
        stream
    }
    pub fn stream_simple(
        &self,
        model: serde_json::Value,
        request: serde_json::Value,
        options: serde_json::Value,
    ) -> Arc<ModelEventStream> {
        self.stream(model, request, options)
    }
    pub fn complete(
        &self,
        model: serde_json::Value,
        request: serde_json::Value,
        options: serde_json::Value,
    ) -> Option<serde_json::Value> {
        self.stream(model, request, options).result()
    }
}

/// Context provides access to the host's UI and session state.
/// Passed to every handler function.
pub struct Context {
    pub(crate) conn: Arc<Connection>,
    pub(crate) tool_call_id: Option<String>,
    pub(crate) request_id: String,
    pub(crate) session_name: String,
    pub(crate) cwd: String,
    pub(crate) mode: String,
    pub(crate) shared_width: Arc<AtomicU32>,
    pub(crate) shared_height: Arc<AtomicU32>,
    pub(crate) shared_model: Arc<Mutex<String>>,
    pub(crate) shared_session: Arc<Mutex<SessionMirror>>,
    /// Serializes the one-time session-log subscribe so concurrent first
    /// readers make a single host call.
    pub(crate) session_sub_lock: Arc<Mutex<()>>,
    pub(crate) cancel_flag: Arc<AtomicBool>,
    pub(crate) cancel_reason: Arc<Mutex<Option<String>>>,
    pub(crate) overlay_seq: Arc<AtomicU64>,
    pub(crate) overlays: RemoteComponents,
    pub(crate) terminal_input: TerminalInputSubs,
    pub(crate) terminal_input_seq: Arc<AtomicU64>,
    pub(crate) width_change: WidthChangeSubs,
    pub(crate) width_change_seq: Arc<AtomicU64>,
    pub(crate) model_streams: ModelStreams,
    pub(crate) model_stream_seq: Arc<AtomicU64>,
}

/// Local session mirror kept in sync by incremental appends from state_update.
/// Eliminates the need to fetch the full session log over IPC on every
/// GetBranch/GetEntries call.
#[derive(Default)]
pub struct SessionMirror {
    /// Whether this extension has asked the host for the session log. The host
    /// sends none until it does, so that the majority of extensions, which
    /// never inspect the session, do not each hold a full copy of it resident.
    /// Atomic so the reader thread can test it while a subscribe is in flight
    /// on another thread.
    pub(crate) subscribed: std::sync::Arc<std::sync::atomic::AtomicBool>,
    entries: Vec<std::sync::Arc<serde_json::Value>>,
    leaf_id: String,
    index: std::collections::HashMap<String, EntryMeta>,
    branch_cache: Option<Vec<std::sync::Arc<serde_json::Value>>>,
    branch_cache_for: String,
    branch_decoded: Option<Vec<std::sync::Arc<BranchEntry>>>,
    branch_decoded_for: String,
}

struct EntryMeta {
    pos: usize,
    parent_id: String,
}

impl SessionMirror {
    pub fn apply_update(&mut self, session: &serde_json::Value) -> bool {
        let leaf_id = session.get("leafId").and_then(|v| v.as_str()).unwrap_or("");
        let appended = session.get("entriesAppended").and_then(|v| v.as_array());
        let entry_count = session
            .get("entryCount")
            .and_then(|v| v.as_u64())
            .unwrap_or(0) as usize;

        let append_len = appended.map(|a| a.len()).unwrap_or(0);
        let expected_base = entry_count.saturating_sub(append_len);
        let mut changed = false;

        // The leaf is small and always tracked. The log itself is applied only
        // once subscribed, and never from a push carrying no entries and a zero
        // count: that is the shape sent to an unsubscribed extension, and
        // reading it as an empty session would discard a mirror a concurrent
        // subscribe had just filled.
        let subscribed = self.subscribed.load(std::sync::atomic::Ordering::Acquire);
        if !subscribed || (entry_count == 0 && append_len == 0) {
            if !leaf_id.is_empty() && leaf_id != self.leaf_id {
                self.leaf_id = leaf_id.to_string();
                self.branch_cache = None;
                self.branch_cache_for.clear();
                self.branch_decoded = None;
                self.branch_decoded_for.clear();
                return true;
            }
            return false;
        }

        if expected_base != self.entries.len() {
            self.entries.clear();
            self.index.clear();
            self.entries.reserve(entry_count);
            changed = true;
        }

        if let Some(arr) = appended {
            for entry in arr {
                let pos = self.entries.len();
                let id = entry
                    .get("id")
                    .and_then(|v| v.as_str())
                    .unwrap_or("")
                    .to_string();
                let parent_id = entry
                    .get("parentId")
                    .and_then(|v| v.as_str())
                    .unwrap_or("")
                    .to_string();
                self.entries.push(std::sync::Arc::new(entry.clone()));
                if !id.is_empty() {
                    self.index.insert(id, EntryMeta { pos, parent_id });
                }
                changed = true;
            }
        }

        if !leaf_id.is_empty() && leaf_id != self.leaf_id {
            self.leaf_id = leaf_id.to_string();
            changed = true;
        }

        if changed {
            self.branch_cache = None;
            self.branch_cache_for.clear();
            self.branch_decoded = None;
            self.branch_decoded_for.clear();
        }
        changed
    }

    /// Installs the log returned by the host at subscribe time so the first
    /// read need not wait for a push. A no-op once the push stream has
    /// delivered anything, which keeps the two paths from fighting.
    pub fn seed(&mut self, entries: Vec<serde_json::Value>, leaf_id: &str) {
        if !leaf_id.is_empty() {
            self.leaf_id = leaf_id.to_string();
        }
        if !self.entries.is_empty() {
            return;
        }
        self.index.clear();
        for (pos, entry) in entries.iter().enumerate() {
            if let Some(id) = entry.get("id").and_then(|v| v.as_str()) {
                let parent_id = entry
                    .get("parentId")
                    .and_then(|v| v.as_str())
                    .unwrap_or("")
                    .to_string();
                self.index
                    .insert(id.to_string(), EntryMeta { pos, parent_id });
            }
        }
        self.entries = entries.into_iter().map(std::sync::Arc::new).collect();
        self.branch_cache = None;
        self.branch_cache_for.clear();
        self.branch_decoded = None;
        self.branch_decoded_for.clear();
    }

    pub fn get_entries(&self) -> Vec<std::sync::Arc<serde_json::Value>> {
        self.entries.clone()
    }

    pub fn get_branch(&mut self) -> Vec<std::sync::Arc<serde_json::Value>> {
        if let Some(ref cache) = self.branch_cache {
            if self.branch_cache_for == self.leaf_id {
                return cache.clone();
            }
        }

        let branch = if self.leaf_id.is_empty() || self.index.is_empty() {
            self.entries.clone()
        } else {
            let mut path = Vec::new();
            let mut seen = std::collections::HashSet::new();
            let mut current = self.leaf_id.clone();
            while !current.is_empty() && seen.insert(current.clone()) {
                if let Some(meta) = self.index.get(&current) {
                    path.push(self.entries[meta.pos].clone());
                    current = meta.parent_id.clone();
                } else {
                    break;
                }
            }
            path.reverse();
            path
        };

        self.branch_cache = Some(branch.clone());
        self.branch_cache_for = self.leaf_id.clone();
        branch
    }

    pub fn get_branch_entries(&mut self) -> Vec<std::sync::Arc<BranchEntry>> {
        if let Some(ref cache) = self.branch_decoded {
            if self.branch_decoded_for == self.leaf_id {
                return cache.clone();
            }
        }
        let decoded = self
            .get_branch()
            .iter()
            .filter_map(|entry| serde_json::from_value((**entry).clone()).ok())
            .map(std::sync::Arc::new)
            .collect::<Vec<_>>();
        self.branch_decoded = Some(decoded.clone());
        self.branch_decoded_for = self.leaf_id.clone();
        decoded
    }
}

impl Context {
    pub fn model_registry(&self) -> ModelRegistry {
        ModelRegistry {
            conn: self.conn.clone(),
            request_id: self.request_id.clone(),
            streams: self.model_streams.clone(),
            sequence: self.model_stream_seq.clone(),
        }
    }

    // ─── Read-only session state ─────────────────────────────────────────

    /// Returns the working directory.
    pub fn cwd(&self) -> &str {
        &self.cwd
    }

    /// Returns the run mode pi is operating in: "tui", "rpc", "json", or
    /// "print". Guard terminal-only UI on "tui". Defaults to "print".
    pub fn mode(&self) -> &str {
        if self.mode.is_empty() {
            "print"
        } else {
            &self.mode
        }
    }

    /// Returns the terminal width.
    pub fn width(&self) -> u32 {
        self.shared_width.load(Ordering::Relaxed)
    }

    /// Returns the terminal height in rows, or 0 when the host has not
    /// reported one. Updated by `height_change` notifications.
    pub fn height(&self) -> u32 {
        self.shared_height.load(Ordering::Relaxed)
    }

    /// Returns the current model name.
    pub fn model(&self) -> String {
        self.shared_model.lock().unwrap().clone()
    }

    /// Returns the session name.
    pub fn session_name(&self) -> &str {
        &self.session_name
    }

    /// Streams a partial result of the running tool, as upstream's `onUpdate`
    /// does. The host shows updates in order, before the tool's final result.
    pub fn on_update(&self, partial: crate::ToolResult) -> io::Result<()> {
        if self.tool_call_id.is_none() || self.request_id.is_empty() {
            return Err(io::Error::other(
                "on_update is only available while a tool runs",
            ));
        }
        let result = match partial {
            crate::ToolResult::Text(text) => serde_json::json!({"content": text}),
            crate::ToolResult::Json(value) => value,
            crate::ToolResult::Error(text) => serde_json::json!({"content": text, "is_error": true}),
        };
        self.conn.notify(
            "tool_update",
            Some(serde_json::json!({"request_id": self.request_id, "result": result})),
        )
    }

    /// Returns true when the host has cancelled this request.
    pub fn is_cancelled(&self) -> bool {
        self.cancel_flag.load(Ordering::Relaxed)
    }

    /// Returns the host-provided cancellation reason, if any.
    pub fn cancellation_reason(&self) -> Option<String> {
        self.cancel_reason.lock().unwrap().clone()
    }

    /// Returns the tool call ID (only valid inside tool handlers).
    pub fn tool_call_id(&self) -> Option<&str> {
        self.tool_call_id.as_deref()
    }

    /// Returns the pig config home directory.
    pub fn config_home(&self) -> String {
        std::env::var("PIG_HOME")
            .or_else(|_| std::env::var("GOPI_HOME"))
            .unwrap_or_else(|_| {
                let home = std::env::var("HOME").unwrap_or_default();
                format!("{}/.pig", home)
            })
    }

    fn call_wire(
        &self,
        method: &str,
        args: Option<serde_json::Value>,
    ) -> io::Result<crate::protocol::CallResultMsg> {
        if !matches!(
            method,
            "ui.select" | "ui.confirm" | "ui.input" | "ui.editor" | "ui.custom"
        ) && !self.request_id.is_empty()
        {
            let _ = self
                .conn
                .request_state(&self.request_id, "blocked", Some("host_call"));
        }
        let parent_request_id = (!self.request_id.is_empty()).then_some(self.request_id.as_str());
        let result = self.conn.call_for(parent_request_id, method, args);
        if !self.request_id.is_empty() {
            let _ = self.conn.request_state(&self.request_id, "progress", None);
        }
        result
    }

    fn block_for_user(&self) {
        if !self.request_id.is_empty() {
            let _ = self
                .conn
                .request_state(&self.request_id, "blocked", Some("user"));
        }
    }

    /// Low-level host call escape hatch. Prefer typed methods when available.
    pub fn call_host(
        &self,
        method: &str,
        args: Option<serde_json::Value>,
    ) -> io::Result<Option<serde_json::Value>> {
        let result = self.call_wire(method, args)?;
        if let Some(err) = result.error {
            let code = err.code.unwrap_or_else(|| "call_failed".to_string());
            return Err(io::Error::new(
                io::ErrorKind::Other,
                format!("{}: {}", code, err.message),
            ));
        }
        Ok(result.result)
    }

    // ─── Notifications & Status ──────────────────────────────────────────

    /// Show a notification to the user.
    pub fn notify(&self, message: &str, level: &str) {
        let _ = self.call_wire(
            "ui.notify",
            Some(serde_json::json!({"message": message, "level": level})),
        );
    }

    /// Set status text in the footer.
    pub fn set_status(&self, key: &str, text: &str) {
        let _ = self.call_wire(
            "ui.setStatus",
            Some(serde_json::json!({"key": key, "text": text})),
        );
    }

    /// Set the working/loading message shown during tool execution.
    pub fn set_working_message(&self, message: &str) {
        let _ = self.call_wire(
            "ui.setWorkingMessage",
            Some(serde_json::json!({"message": message})),
        );
    }

    /// Toggle whether the working/loading indicator is visible.
    pub fn set_working_visible(&self, visible: bool) {
        let _ = self.call_wire(
            "ui.setWorkingVisible",
            Some(serde_json::json!({"visible": visible})),
        );
    }

    /// Configure the working/loading indicator with an opaque option object.
    pub fn set_working_indicator(&self, options: serde_json::Value) -> io::Result<()> {
        call_result_to_io(self.call_wire("ui.setWorkingIndicator", Some(options))?)
    }

    /// Set the label shown for hidden thinking blocks.
    pub fn set_hidden_thinking_label(&self, label: &str) -> io::Result<()> {
        call_result_to_io(self.call_wire(
            "ui.setHiddenThinkingLabel",
            Some(serde_json::json!({"label": label})),
        )?)
    }

    /// Set the terminal title.
    pub fn set_title(&self, title: &str) {
        let _ = self.call_wire("ui.setTitle", Some(serde_json::json!({"title": title})));
    }

    // ─── User Interaction ────────────────────────────────────────────────

    /// Show a selection list. Returns the chosen option and whether the
    /// user confirmed (false = cancelled).
    pub fn select(&self, title: &str, options: &[&str]) -> io::Result<(String, bool)> {
        self.block_for_user();
        let v = call_result_value(self.call_wire(
            "ui.select",
            Some(serde_json::json!({"title": title, "options": options})),
        )?)?;
        let selected = v
            .get("selected")
            .and_then(|s| s.as_str())
            .unwrap_or("")
            .to_string();
        let ok = v.get("ok").and_then(|b| b.as_bool()).unwrap_or(false);
        Ok((selected, ok))
    }

    /// Show a yes/no confirmation dialog.
    pub fn confirm(&self, title: &str, message: &str) -> io::Result<bool> {
        self.block_for_user();
        let v = call_result_value(self.call_wire(
            "ui.confirm",
            Some(serde_json::json!({"title": title, "message": message})),
        )?)?;
        Ok(v.get("confirmed")
            .and_then(|c| c.as_bool())
            .unwrap_or(false))
    }

    /// Show a text input prompt. Returns the entered text and whether the
    /// user confirmed.
    pub fn input(&self, title: &str, placeholder: &str) -> io::Result<(String, bool)> {
        self.block_for_user();
        let v = call_result_value(self.call_wire(
            "ui.input",
            Some(serde_json::json!({"title": title, "placeholder": placeholder})),
        )?)?;
        let text = v
            .get("text")
            .and_then(|s| s.as_str())
            .unwrap_or("")
            .to_string();
        let ok = v.get("ok").and_then(|b| b.as_bool()).unwrap_or(false);
        Ok((text, ok))
    }

    /// Show a multi-line editor. Returns the edited text and whether the
    /// user confirmed.
    pub fn editor(&self, title: &str, prefill: &str) -> io::Result<(String, bool)> {
        self.block_for_user();
        let v = call_result_value(self.call_wire(
            "ui.editor",
            Some(serde_json::json!({"title": title, "prefill": prefill})),
        )?)?;
        let text = v
            .get("text")
            .and_then(|s| s.as_str())
            .unwrap_or("")
            .to_string();
        let ok = v.get("ok").and_then(|b| b.as_bool()).unwrap_or(false);
        Ok((text, ok))
    }

    // ─── Message injection ───────────────────────────────────────────────

    /// Send a custom message into the conversation.
    pub fn send_message(
        &self,
        custom_type: &str,
        content: &str,
        display: bool,
        trigger_turn: Option<bool>,
        deliver_as: Option<&str>,
    ) -> io::Result<()> {
        // Both options are optional upstream: None is sent as unset and the
        // host applies upstream's default for the session's state.
        let mut options = serde_json::Map::new();
        if let Some(trigger_turn) = trigger_turn {
            options.insert("triggerTurn".into(), serde_json::Value::Bool(trigger_turn));
        }
        if let Some(deliver_as) = deliver_as.filter(|value| !value.is_empty()) {
            options.insert("deliverAs".into(), serde_json::Value::String(deliver_as.to_string()));
        }
        call_result_to_io(self.call_wire(
            "sendMessage",
            Some(serde_json::json!({
                "message": {
                    "customType": custom_type,
                    "content": content,
                    "display": display,
                },
                "options": options,
            })),
        )?)
    }

    /// Send a user message containing a string or text/image content blocks.
    pub fn send_user_message(
        &self,
        content: impl serde::Serialize,
        deliver_as: &str,
    ) -> io::Result<()> {
        call_result_to_io(self.call_wire(
            "sendUserMessage",
            Some(serde_json::json!({
                "content": content,
                "options": {"deliverAs": deliver_as},
            })),
        )?)
    }

    /// Append a custom persistent entry to the session.
    pub fn append_entry(&self, custom_type: &str, data: serde_json::Value) -> io::Result<()> {
        call_result_to_io(self.call_wire(
            "appendEntry",
            Some(serde_json::json!({"customType": custom_type, "data": data})),
        )?)
    }

    // ─── Editor access ───────────────────────────────────────────────────

    /// Get the current text in the editor input.
    pub fn get_editor_text(&self) -> String {
        match self.call_wire("ui.getEditorText", None) {
            Ok(result) => result
                .result
                .and_then(|v| {
                    v.get("text")
                        .and_then(|s| s.as_str())
                        .map(|s| s.to_string())
                })
                .unwrap_or_default(),
            Err(_) => String::new(),
        }
    }

    /// Set the editor input text.
    pub fn set_editor_text(&self, text: &str) {
        let _ = self.call_wire("ui.setEditorText", Some(serde_json::json!({"text": text})));
    }

    /// Paste text into the editor at cursor position.
    pub fn paste_to_editor(&self, text: &str) {
        let _ = self.call_wire("ui.pasteToEditor", Some(serde_json::json!({"text": text})));
    }

    // ─── Session state ───────────────────────────────────────────────────

    /// Get the session name (same as `session_name()` but fetched live from host).
    pub fn get_session_name(&self) -> String {
        match self.call_wire("getSessionName", None) {
            Ok(result) => result
                .result
                .and_then(|v| {
                    v.get("name")
                        .and_then(|s| s.as_str())
                        .map(|s| s.to_string())
                })
                .unwrap_or_else(|| self.session_name.clone()),
            Err(_) => self.session_name.clone(),
        }
    }

    /// Set the session name.
    pub fn set_session_name(&self, name: &str) -> io::Result<()> {
        call_result_to_io(
            self.call_wire("setSessionName", Some(serde_json::json!({"name": name})))?,
        )
    }

    /// Set or clear a label for a session entry.
    /// Sets or clears an entry label. The host's failure is returned, as
    /// upstream's setLabel throws when the session cannot record the label.
    pub fn set_label(&self, entry_id: &str, label: &str) -> io::Result<()> {
        call_result_to_io(self.call_wire(
            "setLabel",
            Some(serde_json::json!({"entryId": entry_id, "label": label})),
        )?)
    }

    /// Return a registered CLI flag value as raw JSON.
    pub fn get_flag(&self, name: &str) -> Option<serde_json::Value> {
        self.call_wire("getFlag", Some(serde_json::json!({"name": name})))
            .ok()
            .and_then(|result| result.result)
            .and_then(|v| v.get("value").cloned())
    }

    /// Get the current thinking level.
    pub fn get_thinking_level(&self) -> String {
        match self.call_wire("getThinkingLevel", None) {
            Ok(result) => result
                .result
                .and_then(|v| {
                    v.get("level")
                        .and_then(|s| s.as_str())
                        .map(|s| s.to_string())
                })
                .unwrap_or_default(),
            Err(_) => String::new(),
        }
    }

    /// Set the thinking level ("off", "brief", "verbose").
    pub fn set_thinking_level(&self, level: &str) {
        let _ = self.call_wire(
            "setThinkingLevel",
            Some(serde_json::json!({"level": level})),
        );
    }

    /// Set the model. Returns (success, error_message).
    pub fn set_model(&self, model: &str) -> (bool, String) {
        match self.call_wire("setModel", Some(serde_json::json!({"model": model}))) {
            Ok(result) => {
                let v = result.result.unwrap_or_default();
                let ok = v
                    .get("success")
                    .or_else(|| v.get("ok"))
                    .and_then(|b| b.as_bool())
                    .unwrap_or(false);
                let err = v
                    .get("error")
                    .and_then(|s| s.as_str())
                    .unwrap_or("")
                    .to_string();
                (ok, err)
            }
            Err(e) => (false, e.to_string()),
        }
    }

    // ─── Tool state ─────────────────────────────────────────────────────

    /// Get the list of active (enabled) tools.
    pub fn get_active_tools(&self) -> Vec<String> {
        match self.call_wire("getActiveTools", None) {
            Ok(result) => result
                .result
                .and_then(|v| {
                    v.get("tools").and_then(|a| {
                        a.as_array().map(|arr| {
                            arr.iter()
                                .filter_map(|s| s.as_str().map(|s| s.to_string()))
                                .collect()
                        })
                    })
                })
                .unwrap_or_default(),
            Err(_) => vec![],
        }
    }

    /// Set the list of active (enabled) tools.
    pub fn set_active_tools(&self, tools: &[&str]) {
        let _ = self.call_wire("setActiveTools", Some(serde_json::json!({"tools": tools})));
    }

    /// Refresh tool definitions from the host.
    pub fn refresh_tools(&self) {
        let _ = self.call_wire("refreshTools", None);
    }

    /// Get whether tool outputs are expanded.
    pub fn get_tools_expanded(&self) -> bool {
        match self.call_wire("ui.getToolsExpanded", None) {
            Ok(result) => result
                .result
                .and_then(|v| v.get("expanded").and_then(|b| b.as_bool()))
                .unwrap_or(false),
            Err(_) => false,
        }
    }

    /// Set whether tool outputs are expanded.
    pub fn set_tools_expanded(&self, expanded: bool) {
        let _ = self.call_wire(
            "ui.setToolsExpanded",
            Some(serde_json::json!({"expanded": expanded})),
        );
    }

    // ─── Theme ───────────────────────────────────────────────────────────

    /// Get all available themes.
    pub fn get_all_themes(&self) -> Vec<serde_json::Value> {
        self.call_wire("ui.getAllThemes", None)
            .ok()
            .and_then(|result| result.result)
            .and_then(|v| v.get("themes").and_then(|a| a.as_array()).cloned())
            .unwrap_or_default()
    }

    /// Load a theme by name without switching to it.
    pub fn get_theme(&self, name: &str) -> io::Result<Option<serde_json::Value>> {
        self.call_host("ui.getTheme", Some(serde_json::json!({"name": name})))
            .map(|v| v.and_then(|raw| raw.get("theme").cloned()))
    }

    /// Set the theme. Returns (success, error_message).
    pub fn set_theme(&self, name: &str) -> (bool, String) {
        match self.call_wire("ui.setTheme", Some(serde_json::json!({"theme": name}))) {
            Ok(result) => {
                let v = result.result.unwrap_or_default();
                let ok = v
                    .get("success")
                    .or_else(|| v.get("ok"))
                    .and_then(|b| b.as_bool())
                    .unwrap_or(false);
                let err = v
                    .get("error")
                    .and_then(|s| s.as_str())
                    .unwrap_or("")
                    .to_string();
                (ok, err)
            }
            Err(e) => (false, e.to_string()),
        }
    }

    // ─── Widget ──────────────────────────────────────────────────────────

    /// Push rendered lines to the host for display.
    pub fn set_widget(&self, key: &str, lines: Vec<String>) -> io::Result<()> {
        self.conn.push_widget(key, lines)
    }

    /// Set or clear a widget using the full host-call shape.
    pub fn set_widget_value(
        &self,
        key: &str,
        content: serde_json::Value,
        options: Option<serde_json::Value>,
    ) -> io::Result<()> {
        call_result_to_io(self.call_wire("ui.setWidget", Some(serde_json::json!({"key": key, "content": content, "options": options.unwrap_or_default()})))?)
    }

    /// Clear a custom footer. Passing live component factories is intentionally unsupported by the subprocess bridge.
    pub fn clear_footer(&self) -> io::Result<()> {
        call_result_to_io(self.call_wire("ui.setFooter", Some(serde_json::json!({"clear": true})))?)
    }

    /// Set the semantic login rendered by Pig's fixed native template.
    /// Validation is performed by the host.
    pub fn set_login(&self, definition: &LoginDefinition) -> io::Result<()> {
        let args = serde_json::to_value(definition)
            .map_err(|err| io::Error::new(io::ErrorKind::InvalidInput, err))?;
        call_result_to_io(self.conn.call("ui.setLogin", Some(args))?)
    }

    /// Clear a custom header. Passing live component factories is intentionally unsupported by the subprocess bridge.
    pub fn clear_header(&self) -> io::Result<()> {
        call_result_to_io(self.call_wire("ui.setHeader", Some(serde_json::json!({"clear": true})))?)
    }

    /// Clear a custom editor component. Passing live component factories is intentionally unsupported by the subprocess bridge.
    pub fn clear_editor_component(&self) -> io::Result<()> {
        call_result_to_io(self.call_wire(
            "ui.setEditorComponent",
            Some(serde_json::json!({"clear": true})),
        )?)
    }

    /// Invoke the host custom UI bridge with raw options.
    pub fn custom(&self, options: serde_json::Value) -> io::Result<Option<serde_json::Value>> {
        self.block_for_user();
        self.call_host("ui.custom", Some(options))
    }

    /// Open a focused subprocess component. Input reaches the component only
    /// while the host overlay owns focus. Render requests are coalesced on one
    /// component worker, and cleanup detaches invalidation before disposal.
    pub fn custom_component(
        &self,
        component: impl RemoteComponent + 'static,
        options: serde_json::Value,
    ) -> io::Result<Option<serde_json::Value>> {
        self.block_for_user();
        let mut args = options.as_object().cloned().ok_or_else(|| {
            io::Error::new(
                io::ErrorKind::InvalidInput,
                "custom overlay options must be an object",
            )
        })?;
        let key = format!(
            "custom-{}",
            self.overlay_seq.fetch_add(1, Ordering::Relaxed) + 1
        );
        args.insert("key".to_string(), serde_json::Value::String(key.clone()));

        let (events_tx, events_rx) = sync_channel(64);
        let overlay: RemoteComponentRef = Arc::new(RemoteOverlay {
            state: Mutex::new(RemoteComponentState {
                component: Box::new(component),
                last_lines: Vec::new(),
                seq: 0,
                last_render: None,
            }),
            events: events_tx,
            active: AtomicBool::new(true),
            render_pending: AtomicBool::new(false),
        });
        let weak_overlay = Arc::downgrade(&overlay);
        let attach_result = catch_unwind(AssertUnwindSafe(|| {
            overlay
                .state
                .lock()
                .unwrap()
                .component
                .set_invalidate(Some(Arc::new(move || {
                    if let Some(overlay) = weak_overlay.upgrade() {
                        overlay.request_render();
                    }
                })));
        }));
        if attach_result.is_err() {
            overlay.active.store(false, Ordering::Release);
            dispose_remote_component(&overlay);
            return Err(io::Error::other("attach focused invalidation panicked"));
        }
        self.overlays
            .lock()
            .unwrap()
            .insert(key.clone(), overlay.clone());

        let pending = match self.conn.begin_call_for(
            Some(&self.request_id),
            "ui.custom",
            Some(serde_json::Value::Object(args)),
        ) {
            Ok(pending) => pending,
            Err(err) => {
                self.overlays.lock().unwrap().remove(&key);
                overlay.stop();
                dispose_remote_component(&overlay);
                return Err(err);
            }
        };

        let (worker_done_tx, worker_done_rx) = std::sync::mpsc::channel();
        let worker = {
            let conn = self.conn.clone();
            let worker_key = key.clone();
            let worker_overlay = overlay.clone();
            let width = self.shared_width.clone();
            thread::Builder::new()
                .name(format!("pig-overlay-{key}"))
                .spawn(move || {
                    run_remote_component_worker(
                        conn,
                        worker_key,
                        worker_overlay,
                        width,
                        events_rx,
                        worker_done_tx,
                    )
                })
        };
        let worker = match worker {
            Ok(worker) => worker,
            Err(err) => {
                let message = format!("start focused component worker: {err}");
                let _ = self.conn.notify(
                    "ui.custom.close",
                    Some(serde_json::json!({"key": key, "error": &message})),
                );
                let _ = self.conn.wait_call(pending);
                self.overlays.lock().unwrap().remove(&key);
                overlay.stop();
                dispose_remote_component(&overlay);
                return Err(io::Error::other(message));
            }
        };
        overlay.request_render();

        let call_result = self.conn.wait_call(pending);
        let _ = self.conn.request_state(&self.request_id, "progress", None);
        self.overlays.lock().unwrap().remove(&key);
        overlay.stop();
        if worker_done_rx.recv_timeout(Duration::from_secs(1)).is_err() {
            return Err(io::Error::new(
                io::ErrorKind::TimedOut,
                "focused component did not stop before the cleanup deadline",
            ));
        }
        let _ = worker.join();
        dispose_remote_component(&overlay);

        let result = call_result?;
        let value = call_result_value(result)?;
        if !value.get("ok").and_then(|ok| ok.as_bool()).unwrap_or(false) {
            return Ok(None);
        }
        Ok(value.get("result").cloned())
    }

    /// Attempt to register an autocomplete provider; host may return a typed unsupported error.
    pub fn add_autocomplete_provider(&self) -> io::Result<()> {
        call_result_to_io(
            self.call_wire("ui.addAutocompleteProvider", Some(serde_json::json!({})))?,
        )
    }

    /// Subscribes to raw terminal input, receiving every chunk before the
    /// editor does.
    ///
    /// The host is told to start forwarding only on the first subscription and
    /// to stop on the last, so an extension that never subscribes costs the
    /// input loop nothing. The returned guard unsubscribes when dropped.
    pub fn on_terminal_input<F>(&self, handler: F) -> io::Result<TerminalInputSubscription>
    where
        F: Fn(&str) -> TerminalInputResult + Send + Sync + 'static,
    {
        let id = self.terminal_input_seq.fetch_add(1, Ordering::SeqCst);
        let boxed: TerminalInputHandler = Box::new(handler);
        let first = {
            let mut subs = self.terminal_input.lock().unwrap();
            subs.push((id, Arc::new(boxed)));
            subs.len() == 1
        };

        let subscription = TerminalInputSubscription {
            id,
            subs: self.terminal_input.clone(),
            conn: self.conn.clone(),
            released: false,
        };

        if !first {
            return Ok(subscription);
        }
        match call_result_to_io(self.call_wire("ui.onTerminalInput", Some(serde_json::json!({})))?)
        {
            Ok(()) => Ok(subscription),
            Err(err) => Err(err),
        }
    }

    // ─── Context & Model Info ────────────────────────────────────────────

    /// Get all registered tools with metadata.
    pub fn get_all_tools(&self) -> Vec<ToolInfo> {
        match self.call_wire("getAllTools", None) {
            Ok(result) => {
                let v = result.result.unwrap_or_default();
                v.get("tools")
                    .and_then(|a| a.as_array())
                    .map(|arr| {
                        arr.iter()
                            .filter_map(|t| {
                                let name = t.get("name")?.as_str()?.to_string();
                                let description = t
                                    .get("description")
                                    .and_then(|d| d.as_str())
                                    .unwrap_or("")
                                    .to_string();
                                Some(ToolInfo { name, description })
                            })
                            .collect()
                    })
                    .unwrap_or_default()
            }
            Err(_) => vec![],
        }
    }

    /// Get all registered slash commands.
    pub fn get_commands(&self) -> Vec<CommandInfo> {
        match self.call_wire("getCommands", None) {
            Ok(result) => {
                let v = result.result.unwrap_or_default();
                v.get("commands")
                    .and_then(|a| a.as_array())
                    .map(|arr| {
                        arr.iter()
                            .filter_map(|c| {
                                let name = c.get("name")?.as_str()?.to_string();
                                let description = c
                                    .get("description")
                                    .and_then(|d| d.as_str())
                                    .unwrap_or("")
                                    .to_string();
                                Some(CommandInfo { name, description })
                            })
                            .collect()
                    })
                    .unwrap_or_default()
            }
            Err(_) => vec![],
        }
    }

    /// Get current context usage (token counts and context window percentage).
    pub fn get_context_usage(&self) -> Option<ContextUsage> {
        match self.call_wire("getContextUsage", None) {
            Ok(result) => {
                let v = result.result?;
                let tokens = v.get("tokens").and_then(|t| t.as_i64()).unwrap_or(0) as i32;
                let context_window =
                    v.get("contextWindow").and_then(|c| c.as_i64()).unwrap_or(0) as i32;
                let percent = v.get("percent").and_then(|p| p.as_f64()).unwrap_or(0.0);
                if tokens == 0 && context_window == 0 {
                    return None;
                }
                Some(ContextUsage {
                    tokens,
                    context_window,
                    percent,
                })
            }
            Err(_) => None,
        }
    }

    /// Get the current system prompt text.
    pub fn get_system_prompt(&self) -> String {
        match self.call_wire("getSystemPrompt", None) {
            Ok(result) => result
                .result
                .and_then(|v| {
                    v.get("prompt")
                        .and_then(|s| s.as_str())
                        .map(|s| s.to_string())
                })
                .unwrap_or_default(),
            Err(_) => String::new(),
        }
    }

    /// Get the base inputs pi currently uses to build the system prompt
    /// (customPrompt, selectedTools, toolSnippets, promptGuidelines,
    /// appendSystemPrompt, cwd, contextFiles, skills). Reports current
    /// base inputs only, not per-turn before_agent_start changes. May
    /// include full context-file contents; treat as sensitive.
    pub fn get_system_prompt_options(&self) -> serde_json::Value {
        match self.call_wire("getSystemPromptOptions", None) {
            Ok(result) => result.result.unwrap_or_else(|| serde_json::json!({})),
            Err(_) => serde_json::json!({}),
        }
    }

    /// Get structured metadata about the active model.
    pub fn get_model_info(&self) -> Option<ModelInfo> {
        match self.call_wire("getModelInfo", None) {
            Ok(result) => {
                let v = result.result?;
                let id = v.get("id").and_then(|s| s.as_str())?.to_string();
                if id.is_empty() {
                    return None;
                }
                Some(ModelInfo {
                    input_limits: v.get("inputLimits").filter(|value| !value.is_null()).cloned(),
                    id,
                    name: v
                        .get("name")
                        .and_then(|s| s.as_str())
                        .unwrap_or("")
                        .to_string(),
                    provider: v
                        .get("provider")
                        .and_then(|s| s.as_str())
                        .unwrap_or("")
                        .to_string(),
                    context_window: v.get("contextWindow").and_then(|c| c.as_i64()).unwrap_or(0)
                        as i32,
                    max_output_tokens: v
                        .get("maxOutputTokens")
                        .and_then(|c| c.as_i64())
                        .unwrap_or(0) as i32,
                    reasoning: v
                        .get("reasoning")
                        .and_then(|b| b.as_bool())
                        .unwrap_or(false),
                    input_cost_per_1m: v
                        .get("inputCostPer1M")
                        .and_then(|c| c.as_f64())
                        .unwrap_or(0.0),
                    output_cost_per_1m: v
                        .get("outputCostPer1M")
                        .and_then(|c| c.as_f64())
                        .unwrap_or(0.0),
                    cache_read_cost_per_1m: v
                        .get("cacheReadCostPer1M")
                        .and_then(|c| c.as_f64())
                        .unwrap_or(0.0),
                    cache_write_cost_per_1m: v
                        .get("cacheWriteCostPer1M")
                        .and_then(|c| c.as_f64())
                        .unwrap_or(0.0),
                })
            }
            Err(_) => None,
        }
    }

    /// Get all persisted session entries as shared raw JSON values.
    pub fn get_entries(&self) -> Vec<std::sync::Arc<serde_json::Value>> {
        self.ensure_session_log();
        self.shared_session.lock().unwrap().get_entries()
    }

    fn read_session_entries(path: &str) -> Vec<serde_json::Value> {
        if path.is_empty() {
            return Vec::new();
        }
        let Ok(file) = File::open(path) else {
            return Vec::new();
        };
        let mut entries = Vec::new();
        for line in BufReader::new(file).lines() {
            let Ok(line) = line else { return Vec::new() };
            if line.len() > MAX_FRAME_SIZE as usize {
                return Vec::new();
            }
            let Ok(entry) = serde_json::from_str::<serde_json::Value>(&line) else {
                return Vec::new();
            };
            if entry.get("type").and_then(|value| value.as_str()) != Some("session") {
                entries.push(entry);
            }
        }
        entries
    }

    /// Enrol this extension in session-log replication on its first read, and
    /// install the log before returning so readers stay synchronous.
    ///
    /// The host withholds the log until asked, because replicating a large
    /// session into every loaded extension costs each of them the whole log in
    /// resident memory for data most never inspect.
    fn ensure_session_log(&self) {
        use std::sync::atomic::Ordering;
        let _guard = self.session_sub_lock.lock().unwrap();
        let subscribed = {
            let mirror = self.shared_session.lock().unwrap();
            mirror.subscribed.clone()
        };
        if subscribed.load(Ordering::Acquire) {
            return;
        }
        // Set before the call: the host starts sending the log as soon as it
        // registers the subscription, and those pushes must be applied. The
        // mirror lock is not held across the call, which the reader thread
        // needs in order to deliver the response.
        subscribed.store(true, Ordering::Release);
        let mut entries = Self::read_session_entries(&self.get_session_file());
        let mut cursor = entries.len();
        let mut leaf = String::new();
        loop {
            let requested_cursor = cursor;
            let Ok(result) = self.call_wire(
                "watchSessionLog",
                Some(serde_json::json!({"cursor": cursor})),
            ) else {
                return;
            };
            let Some(value) = result.result else { return };
            let page = value
                .get("entries")
                .and_then(|v| v.as_array())
                .cloned()
                .unwrap_or_default();
            let next_cursor = value
                .get("entryCount")
                .and_then(|v| v.as_u64())
                .unwrap_or(cursor as u64) as usize;
            if next_cursor.saturating_sub(page.len()) != requested_cursor {
                entries.clear();
            }
            entries.extend(page);
            cursor = next_cursor;
            if let Some(next_leaf) = value.get("leafId").and_then(|v| v.as_str()) {
                leaf = next_leaf.to_string();
            }
            if !value
                .get("hasMore")
                .and_then(|v| v.as_bool())
                .unwrap_or(false)
            {
                break;
            }
        }
        self.shared_session.lock().unwrap().seed(entries, &leaf);

        loop {
            let Ok(result) = self.call_wire(
                "watchSessionLog",
                Some(serde_json::json!({"cursor": cursor, "complete": true})),
            ) else {
                return;
            };
            let Some(value) = result.result else { return };
            let page = value
                .get("entries")
                .and_then(|v| v.as_array())
                .cloned()
                .unwrap_or_default();
            cursor = value
                .get("entryCount")
                .and_then(|v| v.as_u64())
                .unwrap_or(cursor as u64) as usize;
            if let Some(next_leaf) = value.get("leafId").and_then(|v| v.as_str()) {
                leaf = next_leaf.to_string();
            }
            let page_empty = page.is_empty();
            self.shared_session
                .lock()
                .unwrap()
                .apply_update(&serde_json::json!({
                    "entriesAppended": page,
                    "entryCount": cursor,
                    "leafId": leaf,
                }));
            let has_more = value
                .get("hasMore")
                .and_then(|v| v.as_bool())
                .unwrap_or(false);
            if !has_more && page_empty {
                return;
            }
        }
    }

    /// Get model auth metadata from the host.
    pub fn get_model_auth(&self, provider_id: &str, model_id: &str) -> Option<serde_json::Value> {
        self.call_wire(
            "getModelAuth",
            Some(serde_json::json!({"provider": provider_id, "modelId": model_id})),
        )
        .ok()
        .and_then(|result| result.result)
    }

    /// Perform a one-shot LLM completion through the host.
    pub fn complete(
        &self,
        model: serde_json::Value,
        request: serde_json::Value,
        auth: serde_json::Value,
    ) -> io::Result<Option<serde_json::Value>> {
        self.call_host(
            "complete",
            Some(serde_json::json!({"model": model, "request": request, "auth": auth})),
        )
    }

    /// Get a shallow vector copy of the current branch. Entry values are shared
    /// and must be treated as read-only.
    pub fn get_branch(&self) -> Vec<std::sync::Arc<BranchEntry>> {
        self.ensure_session_log();
        self.shared_session.lock().unwrap().get_branch_entries()
    }

    // ─── Session identity ────────────────────────────────────────────────

    /// The current session's id, or an empty string when the host does not
    /// answer.
    pub fn get_session_id(&self) -> String {
        self.string_field("getSessionID", "id")
    }

    /// Path to the current session's file, or an empty string when
    /// unavailable.
    pub fn get_session_file(&self) -> String {
        self.string_field("getSessionFile", "path")
    }

    /// Id of the current branch leaf entry, or an empty string when
    /// unavailable.
    pub fn get_leaf_id(&self) -> String {
        self.string_field("getLeafID", "id")
    }

    fn string_field(&self, method: &str, field: &str) -> String {
        self.call_wire(method, None)
            .ok()
            .and_then(|result| result.result)
            .and_then(|v| v.get(field).and_then(|s| s.as_str().map(str::to_owned)))
            .unwrap_or_default()
    }

    // ─── Shell ───────────────────────────────────────────────────────────

    /// Run a command through the host's executor.
    ///
    /// Returns `Err` when the host reports a failure, so a caller sees the
    /// reason rather than an exit code of zero it never produced.
    pub fn exec(&self, command: &str, args: &[&str]) -> Result<ExecResult, String> {
        let result = self
            .call_wire(
                "exec",
                Some(serde_json::json!({ "command": command, "args": args })),
            )
            .map_err(|e| e.to_string())?;
        if let Some(error) = result.error {
            let code = error.code.unwrap_or_else(|| "call_failed".to_string());
            return Err(format!("{}: {}", code, error.message));
        }
        let value = result.result.unwrap_or(serde_json::Value::Null);
        Ok(ExecResult {
            stdout: value
                .get("stdout")
                .and_then(|v| v.as_str())
                .unwrap_or_default()
                .to_owned(),
            stderr: value
                .get("stderr")
                .and_then(|v| v.as_str())
                .unwrap_or_default()
                .to_owned(),
            exit_code: value.get("code").and_then(|v| v.as_i64()).unwrap_or(0) as i32,
        })
    }

    // ─── Agent/session control ───────────────────────────────────────────

    /// Whether the current project is trusted. Untrusted projects have
    /// project-scoped settings and hooks disabled. Defaults to trusted when the
    /// host does not answer, matching the upstream runner.
    pub fn is_project_trusted(&self) -> bool {
        self.call_wire("isProjectTrusted", None)
            .ok()
            .and_then(|result| result.result)
            .and_then(|v| v.get("trusted").and_then(|b| b.as_bool()))
            .unwrap_or(true)
    }

    pub fn is_idle(&self) -> bool {
        self.call_wire("isIdle", None)
            .ok()
            .and_then(|result| result.result)
            .and_then(|v| v.get("idle").and_then(|b| b.as_bool()))
            .unwrap_or(true)
    }

    pub fn abort(&self) {
        let _ = self.call_wire("abort", None);
    }

    pub fn has_pending_messages(&self) -> bool {
        self.call_wire("hasPendingMessages", None)
            .ok()
            .and_then(|result| result.result)
            .and_then(|v| v.get("pending").and_then(|b| b.as_bool()))
            .unwrap_or(false)
    }

    pub fn shutdown(&self) {
        let _ = self.call_wire("shutdown", None);
    }

    pub fn compact(&self, opts: serde_json::Value) {
        let _ = self.call_wire("compact", Some(opts));
    }

    pub fn wait_for_idle(&self) -> io::Result<()> {
        call_result_to_io(self.call_wire("waitForIdle", None)?)
    }

    pub fn new_session(&self, opts: serde_json::Value) -> io::Result<Option<serde_json::Value>> {
        self.call_host("newSession", Some(opts))
    }

    pub fn fork(
        &self,
        entry_id: &str,
        mut opts: serde_json::Value,
    ) -> io::Result<Option<serde_json::Value>> {
        if !opts.is_object() {
            opts = serde_json::json!({});
        }
        if let Some(obj) = opts.as_object_mut() {
            obj.insert(
                "entryId".to_string(),
                serde_json::Value::String(entry_id.to_string()),
            );
        }
        self.call_host("fork", Some(opts))
    }

    pub fn navigate_tree(
        &self,
        target_id: &str,
        mut opts: serde_json::Value,
    ) -> io::Result<Option<serde_json::Value>> {
        if !opts.is_object() {
            opts = serde_json::json!({});
        }
        if let Some(obj) = opts.as_object_mut() {
            obj.insert(
                "targetId".to_string(),
                serde_json::Value::String(target_id.to_string()),
            );
        }
        self.call_host("navigateTree", Some(opts))
    }

    pub fn switch_session(
        &self,
        session_path: &str,
        mut opts: serde_json::Value,
    ) -> io::Result<Option<serde_json::Value>> {
        if !opts.is_object() {
            opts = serde_json::json!({});
        }
        if let Some(obj) = opts.as_object_mut() {
            obj.insert(
                "sessionPath".to_string(),
                serde_json::Value::String(session_path.to_string()),
            );
        }
        self.call_host("switchSession", Some(opts))
    }

    pub fn reload(&self) -> io::Result<()> {
        call_result_to_io(self.call_wire("reload", None)?)
    }
}

// ─── Event payload helpers ───────────────────────────────────────────────

/// Role of an event's message payload, or `None` when the event carries no
/// message.
///
/// Message-shaped events carry upstream's flat role-discriminated union:
/// `{"type": "message_end", "message": {"role": "assistant", "content": [...]}}`
/// where content is a block array, never a bare string.
pub fn message_role(data: &serde_json::Value) -> Option<String> {
    data.get("message")?
        .get("role")?
        .as_str()
        .map(str::to_owned)
}

/// Concatenated text blocks of an event's message payload. Non-text blocks
/// (tool calls, images, thinking) are skipped. Returns an empty string when the
/// event carries no message or the message has no text.
pub fn message_text(data: &serde_json::Value) -> String {
    let Some(content) = data.get("message").and_then(|m| m.get("content")) else {
        return String::new();
    };
    if let Some(text) = content.as_str() {
        return text.to_owned();
    }
    let Some(blocks) = content.as_array() else {
        return String::new();
    };
    let mut out = String::new();
    for block in blocks {
        if block.get("type").and_then(|t| t.as_str()) != Some("text") {
            continue;
        }
        if let Some(text) = block.get("text").and_then(|t| t.as_str()) {
            out.push_str(text);
        }
    }
    out
}

// ─── Data Types ──────────────────────────────────────────────────────────

/// Outcome of a host-executed command, returned by `exec()`.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ExecResult {
    pub stdout: String,
    pub stderr: String,
    pub exit_code: i32,
}

/// Tool metadata returned by `get_all_tools()`.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ToolInfo {
    pub name: String,
    pub description: String,
}

/// Command metadata returned by `get_commands()`.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct CommandInfo {
    pub name: String,
    pub description: String,
}

/// Context usage data returned by `get_context_usage()`.
#[derive(Debug, Clone)]
pub struct ContextUsage {
    pub tokens: i32,
    pub context_window: i32,
    pub percent: f64,
}

/// Structured model metadata returned by `get_model_info()`.
#[derive(Debug, Clone)]
pub struct ModelInfo {
    /// Provider input limits and cache-safe image preprocessing metadata.
    pub input_limits: Option<serde_json::Value>,
    pub id: String,
    pub name: String,
    pub provider: String,
    pub context_window: i32,
    pub max_output_tokens: i32,
    pub reasoning: bool,
    pub input_cost_per_1m: f64,
    pub output_cost_per_1m: f64,
    pub cache_read_cost_per_1m: f64,
    pub cache_write_cost_per_1m: f64,
}

/// A single entry in the session conversation history.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct BranchEntry {
    #[serde(rename = "type")]
    pub entry_type: String,
    #[serde(default)]
    pub role: String,
    #[serde(default)]
    pub content: String,
    #[serde(default)]
    pub thinking: String,
    #[serde(default)]
    pub provider: String,
    #[serde(default)]
    pub model: String,
    #[serde(default, rename = "toolName")]
    pub tool_name: String,
    #[serde(default, rename = "toolCallId")]
    pub tool_call_id: String,
    #[serde(default, rename = "isError")]
    pub is_error: bool,
    #[serde(default)]
    pub usage: Option<UsageInfo>,
    #[serde(default, rename = "toolCalls")]
    pub tool_calls: Vec<ToolCallInfo>,
}

/// Token usage data for an assistant message.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct UsageInfo {
    pub input: i32,
    pub output: i32,
    #[serde(default, rename = "cacheRead")]
    pub cache_read: i32,
    #[serde(default, rename = "cacheWrite")]
    pub cache_write: i32,
}

/// A tool invocation within an assistant message.
#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ToolCallInfo {
    pub name: String,
    pub id: String,
    pub args: String,
}

/// Guard returned by [`Context::on_terminal_input`]. Dropping it, or calling
/// [`TerminalInputSubscription::unsubscribe`], releases the subscription and
/// tells the host to stop forwarding once no handlers remain.
pub struct TerminalInputSubscription {
    id: u64,
    subs: TerminalInputSubs,
    conn: Arc<Connection>,
    released: bool,
}

impl TerminalInputSubscription {
    /// Releases the subscription. Idempotent.
    pub fn unsubscribe(mut self) {
        self.release();
    }

    fn release(&mut self) {
        if self.released {
            return;
        }
        self.released = true;
        let last = {
            let mut subs = self.subs.lock().unwrap();
            subs.retain(|(id, _)| *id != self.id);
            subs.is_empty()
        };
        if last {
            // A subscription guard can outlive the request that created it.
            // Drop has no request context, so this is the one context API path
            // that intentionally uses an unparented connection call.
            let _ = self
                .conn
                .call("ui.offTerminalInput", Some(serde_json::json!({})));
        }
    }
}

impl Drop for TerminalInputSubscription {
    fn drop(&mut self) {
        self.release();
    }
}

/// Unsubscribes a width handler when dropped.
pub struct WidthChangeSubscription {
    id: u64,
    subs: WidthChangeSubs,
    released: bool,
}

impl WidthChangeSubscription {
    /// Unsubscribe now. Idempotent; dropping the guard afterwards does nothing.
    pub fn unsubscribe(mut self) {
        self.release();
        self.released = true;
    }

    fn release(&mut self) {
        if self.released {
            return;
        }
        if let Ok(mut subs) = self.subs.lock() {
            subs.retain(|(id, _)| *id != self.id);
        }
    }
}

impl Drop for WidthChangeSubscription {
    fn drop(&mut self) {
        self.release();
    }
}

impl Context {
    /// Subscribe to terminal resizes, receiving the new width after
    /// [`Context::width`] has been updated.
    ///
    /// Upstream Pi installs headers and footers as component factories whose
    /// `render(width)` runs every frame, so they follow a resize with no work
    /// from the extension. A pig extension is a subprocess and sends static
    /// lines instead, so a footer keeps the width it was built for until
    /// something re-pushes it. This is that trigger.
    ///
    /// Handlers run on the message loop and must not block: re-push the lines
    /// and return. The returned guard unsubscribes when dropped.
    pub fn on_width_change<F>(&self, handler: F) -> WidthChangeSubscription
    where
        F: Fn(u32) + Send + Sync + 'static,
    {
        let id = self.width_change_seq.fetch_add(1, Ordering::SeqCst);
        let boxed: WidthChangeHandler = Box::new(handler);
        if let Ok(mut subs) = self.width_change.lock() {
            subs.push((id, Arc::new(boxed)));
        }
        WidthChangeSubscription {
            id,
            subs: self.width_change.clone(),
            released: false,
        }
    }
}

#[cfg(test)]
mod width_change_tests {
    use super::*;
    use std::os::unix::net::UnixStream;
    use std::sync::atomic::AtomicU32;

    // Exercises the subscription bookkeeping directly. The notify path that
    // drives these handlers is covered by the conformance fixture, which is what
    // proves this SDK agrees with the Go reference.
    fn subs() -> (WidthChangeSubs, Arc<AtomicU64>) {
        (
            Arc::new(Mutex::new(Vec::new())),
            Arc::new(AtomicU64::new(0)),
        )
    }

    fn ctx_with(subs: WidthChangeSubs, seq: Arc<AtomicU64>, width: Arc<AtomicU32>) -> Context {
        Context {
            // A connected socketpair: these tests never write to it, but
            // Context owns a real Connection rather than a test-only shim.
            conn: Arc::new(Connection::new(UnixStream::pair().unwrap().0)),
            tool_call_id: None,
            request_id: String::new(),
            session_name: String::new(),
            cwd: String::new(),
            mode: String::new(),
            shared_width: width,
            shared_height: Arc::new(AtomicU32::new(0)),
            shared_model: Arc::new(Mutex::new(String::new())),
            shared_session: Arc::new(Mutex::new(SessionMirror::default())),
            session_sub_lock: Arc::new(Mutex::new(())),
            cancel_flag: Arc::new(AtomicBool::new(false)),
            cancel_reason: Arc::new(Mutex::new(None)),
            overlay_seq: Arc::new(AtomicU64::new(0)),
            overlays: Arc::new(Mutex::new(Default::default())),
            terminal_input: Arc::new(Mutex::new(Vec::new())),
            terminal_input_seq: Arc::new(AtomicU64::new(0)),
            width_change: subs,
            width_change_seq: seq,
            model_streams: Arc::new(Mutex::new(HashMap::new())),
            model_stream_seq: Arc::new(AtomicU64::new(0)),
        }
    }

    #[test]
    fn registers_a_handler() {
        let (s, q) = subs();
        let ctx = ctx_with(s.clone(), q, Arc::new(AtomicU32::new(0)));
        let _guard = ctx.on_width_change(|_| {});
        assert_eq!(s.lock().unwrap().len(), 1, "handler was not registered");
    }

    #[test]
    fn dropping_the_guard_unsubscribes() {
        let (s, q) = subs();
        let ctx = ctx_with(s.clone(), q, Arc::new(AtomicU32::new(0)));
        {
            let _guard = ctx.on_width_change(|_| {});
            assert_eq!(s.lock().unwrap().len(), 1);
        }
        assert!(
            s.lock().unwrap().is_empty(),
            "handler outlived its guard, so delivery would continue after unsubscribe"
        );
    }

    #[test]
    fn explicit_unsubscribe_is_not_double_removed() {
        let (s, q) = subs();
        let ctx = ctx_with(s.clone(), q, Arc::new(AtomicU32::new(0)));
        let keep = ctx.on_width_change(|_| {});
        let drop_me = ctx.on_width_change(|_| {});
        assert_eq!(s.lock().unwrap().len(), 2);
        drop_me.unsubscribe();
        assert_eq!(
            s.lock().unwrap().len(),
            1,
            "unsubscribe removed the wrong handler or removed more than one"
        );
        drop(keep);
        assert!(s.lock().unwrap().is_empty());
    }

    #[test]
    fn each_subscription_gets_a_distinct_id() {
        let (s, q) = subs();
        let ctx = ctx_with(s.clone(), q, Arc::new(AtomicU32::new(0)));
        let _a = ctx.on_width_change(|_| {});
        let _b = ctx.on_width_change(|_| {});
        let ids: Vec<u64> = s.lock().unwrap().iter().map(|(id, _)| *id).collect();
        assert_ne!(
            ids[0], ids[1],
            "shared ids would make one unsubscribe drop both handlers"
        );
    }
}

#[cfg(test)]
mod login_call_tests {
    use super::*;
    use crate::protocol::{CallResultMsg, Envelope};
    use std::os::unix::net::UnixStream;

    fn context(stream: UnixStream) -> Arc<Context> {
        Arc::new(Context {
            conn: Arc::new(Connection::new(stream)),
            request_id: String::new(),
            tool_call_id: None,
            session_name: String::new(),
            cwd: String::new(),
            mode: String::new(),
            shared_width: Arc::new(AtomicU32::new(0)),
            shared_height: Arc::new(AtomicU32::new(0)),
            shared_model: Arc::new(Mutex::new(String::new())),
            shared_session: Arc::new(Mutex::new(SessionMirror::default())),
            session_sub_lock: Arc::new(Mutex::new(())),
            cancel_flag: Arc::new(AtomicBool::new(false)),
            cancel_reason: Arc::new(Mutex::new(None)),
            overlay_seq: Arc::new(AtomicU64::new(0)),
            overlays: Arc::new(Mutex::new(Default::default())),
            terminal_input: Arc::new(Mutex::new(Vec::new())),
            terminal_input_seq: Arc::new(AtomicU64::new(0)),
            width_change: Arc::new(Mutex::new(Vec::new())),
            width_change_seq: Arc::new(AtomicU64::new(0)),
            model_streams: Arc::new(Mutex::new(HashMap::new())),
            model_stream_seq: Arc::new(AtomicU64::new(0)),
        })
    }

    #[test]
    fn set_login_sends_the_definition_directly_as_call_args() {
        let (extension_stream, host_stream) = UnixStream::pair().unwrap();
        let ctx = context(extension_stream);
        let definition = LoginDefinition {
            brand: vec!["brand".to_string()],
            hero: vec!["hero".to_string()],
            mascot: vec!["mascot".to_string()],
            palette: HashMap::from([("A".to_string(), "#112233".to_string())]),
            name: "name".to_string(),
            description: "description".to_string(),
            tagline: "tagline".to_string(),
        };

        let caller = {
            let ctx = ctx.clone();
            let definition = definition.clone();
            std::thread::spawn(move || ctx.set_login(&definition))
        };
        let host = Connection::new(host_stream);
        let envelope = host.read_envelope().unwrap();
        let call = envelope.call.unwrap();
        assert_eq!(call.method, "ui.setLogin");
        assert_eq!(call.args, Some(serde_json::to_value(&definition).unwrap()));
        assert_eq!(
            call.args
                .unwrap()
                .get("name")
                .and_then(|value| value.as_str()),
            Some("name"),
            "the definition must be Args itself, not nested under another key"
        );

        assert!(ctx.conn.complete_call(&Envelope {
            msg_type: "call_result".to_string(),
            id: envelope.id,
            call_result: Some(CallResultMsg {
                result: None,
                error: None
            }),
            ..Default::default()
        }));
        caller.join().unwrap().unwrap();
    }
}

#[cfg(test)]
mod model_stream_tests {
    use super::*;

    #[test]
    fn preserves_order_and_terminal_result() {
        let stream = ModelEventStream::new();
        stream.push(serde_json::json!({"type":"start"}));
        stream.push(serde_json::json!({"type":"text_delta","delta":"ok"}));
        stream.push(serde_json::json!({"type":"done","message":{"stopReason":"stop"}}));
        assert_eq!(stream.next().unwrap()["type"], "start");
        assert_eq!(stream.next().unwrap()["type"], "text_delta");
        assert_eq!(stream.next().unwrap()["type"], "done");
        assert!(stream.next().is_none());
        assert_eq!(stream.result().unwrap()["stopReason"], "stop");
    }

    #[test]
    fn transport_error_shape_is_complete() {
        let event = model_stream_error_event(
            "transport boom",
            &serde_json::json!({"api":"openai-responses","provider":"conformance","modelId":"transport-error"}),
        );
        let error = &event["error"];
        assert_eq!(error["role"], "assistant");
        assert_eq!(error["api"], "openai-responses");
        assert_eq!(error["provider"], "conformance");
        assert_eq!(error["model"], "transport-error");
        assert_eq!(error["stopReason"], "error");
        assert_eq!(error["errorMessage"], "transport boom");
        assert!(error["timestamp"].as_u64().unwrap_or_default() > 0);
        assert_eq!(error["usage"]["totalTokens"], 0);
        assert_eq!(error["usage"]["cost"]["total"], 0);
    }

    #[test]
    fn competing_consumers_drain_one_fifo_without_retention() {
        let stream = Arc::new(ModelEventStream::new());
        for sequence in 0..1000 {
            stream.push(serde_json::json!({"type":"text_delta","sequence":sequence}));
        }
        stream.push(serde_json::json!({"type":"done","message":{"stopReason":"stop"}}));
        stream.push(serde_json::json!({"type":"text_delta","sequence":"ignored"}));
        let seen = Arc::new(Mutex::new(Vec::new()));
        let mut workers = Vec::new();
        for _ in 0..2 {
            let stream = stream.clone();
            let seen = seen.clone();
            workers.push(thread::spawn(move || {
                while let Some(event) = stream.next() {
                    if let Some(sequence) = event.get("sequence").and_then(|value| value.as_u64()) {
                        seen.lock().unwrap().push(sequence);
                    }
                }
            }));
        }
        for worker in workers {
            worker.join().unwrap();
        }
        let mut got = seen.lock().unwrap().clone();
        got.sort_unstable();
        assert_eq!(got, (0..1000).collect::<Vec<_>>());
        let state = stream.state.lock().unwrap();
        assert!(
            state.events.is_empty(),
            "drained stream retained {} events",
            state.events.len()
        );
    }
}

//! Context passed to tool/command/event handlers.
//!
//! Provides access to the host's UI and session state. Every method maps 1:1
//! to a Go SDK `Context` method and a wire-protocol `call` message.

use crate::login::{LoginDefinition, SpriteDefinition};
use crate::protocol::{CallResultMsg, Connection, MAX_FRAME_SIZE};
use crate::theme::{Theme, UiState};
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
    /// Raw input preserves lone UTF-16 units just like terminal listeners.
    fn handle_input(&mut self, data: &crate::JsString) -> Result<RemoteComponentResult, String>;
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
    Input(crate::JsString),
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

    pub(crate) fn send_input(&self, data: crate::JsString) -> Result<(), String> {
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
#[derive(Debug, Clone, Default, PartialEq, Eq, serde::Serialize)]
pub struct TerminalInputResult {
    /// Suppresses normal handling of the chunk, so the editor and keybindings
    /// never see it.
    pub consume: bool,
    /// When set, replaces the chunk for later handlers and for normal
    /// handling. An empty replacement drops the chunk.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub data: Option<crate::JsString>,
}

/// A handler receiving every raw input chunk before the editor does.
///
/// Like upstream's synchronous listener, the input waits for the verdict, so a
/// handler should return promptly.
pub type TerminalInputHandler = Box<dyn Fn(&crate::JsString) -> TerminalInputResult + Send + Sync>;

pub(crate) type TerminalInputSubs = Arc<Mutex<Vec<(u64, Arc<TerminalInputHandler>)>>>;

/// Width handlers, invoked after the shared width is stored so a handler that
/// calls [`Context::width`] observes the new value.
pub(crate) type WidthChangeHandler = Box<dyn Fn(u32) + Send + Sync>;
pub(crate) type WidthChangeSubs = Arc<Mutex<Vec<(u64, Arc<WidthChangeHandler>)>>>;

/// Returns opts, an object, with key set to value. A non-object opts is replaced by an empty object.
fn with_field(mut opts: serde_json::Value, key: &str, value: &str) -> serde_json::Value {
    if !opts.is_object() {
        opts = serde_json::json!({});
    }
    if let Some(object) = opts.as_object_mut() {
        object.insert(key.to_string(), serde_json::Value::String(value.to_string()));
    }
    opts
}

/// The message of a failed host call as the Node runtime rejects with it: the
/// host's message, prefixed by "code: " only when the host names a code.
pub(crate) fn host_error_message(error: crate::protocol::ErrorInfo) -> String {
    match error.code.as_deref() {
        Some(code) if !code.is_empty() => format!("{code}: {}", error.message),
        _ => error.message,
    }
}

fn call_result_to_io(result: CallResultMsg) -> io::Result<()> {
    if let Some(err) = result.error {
        return Err(io::Error::new(io::ErrorKind::Other, host_error_message(err)));
    }
    Ok(())
}

/// The message a panic carries, as upstream's `error.message` of a throw.
fn panic_message(payload: &(dyn std::any::Any + Send)) -> String {
    if let Some(message) = payload.downcast_ref::<&str>() {
        (*message).to_string()
    } else if let Some(message) = payload.downcast_ref::<String>() {
        message.clone()
    } else {
        "extension callback panicked".to_string()
    }
}

fn invalid_reply(method: &str, field: &str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, format!("host reply to {method} has no {field:?}"))
}

fn call_result_value(result: CallResultMsg) -> io::Result<serde_json::Value> {
    if let Some(err) = result.error {
        return Err(io::Error::new(io::ErrorKind::Other, host_error_message(err)));
    }
    Ok(result.result.unwrap_or_default())
}

/// Installs the route of a virtual model, then registers it with the host, and restores the previous route when the host refuses. The route is installed first: the host may route a request as soon as it has registered the model.
fn install_virtual_model(conn: &Connection, model: crate::VirtualModel, register: impl FnOnce() -> io::Result<()>) -> io::Result<()> {
    let key = (model.provider.clone(), model.id.clone());
    let previous = conn.virtual_models.lock().unwrap().insert(key.clone(), Arc::new(model));
    if let Err(error) = register() {
        let mut models = conn.virtual_models.lock().unwrap();
        match previous {
            Some(previous) => models.insert(key, previous),
            None => models.remove(&key),
        };
        return Err(error);
    }
    Ok(())
}

pub(crate) type ModelStreams = Arc<Mutex<HashMap<String, Arc<ModelEventStream>>>>;

#[derive(Default)]
struct ModelStreamState {
    events: VecDeque<serde_json::Value>,
    terminal: bool,
    result: Option<serde_json::Value>,
    started: Option<Result<(),String>>,
}

pub struct ModelEventStream {
    state: Mutex<ModelStreamState>,
    changed: Condvar,
}

impl Default for ModelEventStream {
    fn default() -> Self {
        Self::new()
    }
}

impl ModelEventStream {
    pub fn new() -> Self {
        Self {
            state: Mutex::new(ModelStreamState::default()),
            changed: Condvar::new(),
        }
    }
    pub(crate) fn mark_started(&self,error:Option<String>){let mut state=self.state.lock().unwrap();if state.started.is_none(){state.started=Some(error.map_or(Ok(()),Err));self.changed.notify_all();}}
    pub(crate) fn wait_started(&self)->Result<(),String>{let mut state=self.state.lock().unwrap();loop{if let Some(result)=&state.started{return result.clone()}state=self.changed.wait(state).unwrap();}}
    pub fn push(&self, event: serde_json::Value) {
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
    pub fn end(&self, result: serde_json::Value) {
        let mut state = self.state.lock().unwrap();
        if state.terminal {
            return;
        };
        state.terminal = true;
        state.result = Some(result);
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
    pub(crate) fn forward(
        self: &Arc<Self>,
        stopped: &crate::provider::ProviderSignal,
        mut emit: impl FnMut(serde_json::Value) -> Result<(), String>,
    ) -> Result<(), String> {
        // pig additive (D19): teardown releases transport waits without settling the caller-owned stream.
        let stream = Arc::downgrade(self);
        let _wake = stopped.subscribe(Arc::new(move || {
            if let Some(stream) = stream.upgrade() {
                let _state = stream.state.lock().unwrap();
                stream.changed.notify_all();
            }
        }));
        loop {
            let event = {
                let mut state = self.state.lock().unwrap();
                while state.events.is_empty() && !state.terminal && !stopped.is_cancelled() {
                    state = self.changed.wait(state).unwrap();
                }
                if stopped.is_cancelled() {
                    return Err("Provider connection closed".into());
                }
                state.events.pop_front()
            };
            match event {
                Some(event) => emit(event)?,
                None => return Ok(()),
            }
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

pub(crate) fn model_stream_error_event(message: &str, model: &serde_json::Value) -> serde_json::Value {
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
    request_parent: Option<Arc<crate::protocol::RequestParent>>,
    conn: Arc<Connection>,
    request_id: String,
    streams: ModelStreams,
    sequence: Arc<AtomicU64>,
}

impl ModelRegistry {
    pub fn get_registered_native_provider(&self,id:&str)->io::Result<Option<Arc<crate::Provider>>>{
        let snapshot=self.state()?;
        let Some(declaration)=snapshot["registered"].as_array().and_then(|entries|entries.iter().find(|entry|entry["name"]==id&&entry["native"].is_object())).map(|entry|entry["native"].clone())else{return Ok(None)};
        let state=self.conn.provider_objects.get().ok_or_else(||io::Error::other("Provider runtime unavailable"))?;
        state.get(self.conn.clone(),declaration).map(Some).map_err(io::Error::other)
    }
    // pig divergence (D78): builtin/composed Provider methods still need a native SDK carrier.
    pub fn get_provider(&self,id:&str)->io::Result<Option<Arc<crate::Provider>>>{
        if let Some(provider)=self.get_registered_native_provider(id)?{return Ok(Some(provider))}
        if self.state()?["providers"].get(id).is_some(){return Err(io::Error::other("builtin/composed Provider object carrier is unavailable (D78)"))}
        Ok(None)
    }
    pub fn register_native_provider(&self,provider:Arc<crate::Provider>)->io::Result<()>{
        let state=self.conn.provider_objects.get().ok_or_else(||io::Error::other("Provider runtime unavailable"))?;
        let declaration=state.register(provider).map_err(io::Error::other)?;
        self.call("registerProvider",serde_json::to_value(declaration).map_err(io::Error::other)?)?;
        Ok(())
    }
    fn call(&self, method: &str, args: serde_json::Value) -> io::Result<serde_json::Value> {
        let parent = (!self.request_id.is_empty()).then_some(self.request_id.as_str());
        self.conn.call_for_scope(self.request_parent.as_deref(), parent, method, Some(args)).and_then(call_result_value)
    }
    fn state(&self) -> io::Result<serde_json::Value> {
        self.call("getModelRegistryState", serde_json::Value::Null)
    }
    pub fn get_all(&self) -> io::Result<serde_json::Value> { Ok(self.state()?["models"].clone()) }
    /// Every known model of a type (`chat`, `image` or `classifier`), optionally for one provider. Chat models come from the registry state's `models` and the others from its `typedModels`, in the host's order (`model-registry.ts` `getModelsOfType`).
    pub fn get_models_of_type(&self, model_type: &str, provider: Option<&str>) -> io::Result<serde_json::Value> {
        let state = self.state()?;
        let mut models = Vec::new();
        for list in [&state["models"], &state["typedModels"]] {
            for model in list.as_array().into_iter().flatten() {
                let kind = model["type"].as_str().filter(|kind| !kind.is_empty()).unwrap_or("chat");
                if kind == model_type && provider.is_none_or(|provider| model["provider"] == provider) {
                    models.push(model.clone());
                }
            }
        }
        Ok(serde_json::Value::Array(models))
    }
    /// One model of a type by provider and id, or `None` (`getModelOfType`).
    pub fn get_model_of_type(&self, model_type: &str, provider: &str, model_id: &str) -> io::Result<Option<serde_json::Value>> {
        Ok(self.get_models_of_type(model_type, Some(provider))?.as_array().and_then(|models| models.iter().find(|model| model["id"] == model_id)).cloned())
    }
    /// One model of a type by provider and id, or `None` (`findOfType`).
    pub fn find_of_type(&self, model_type: &str, provider: &str, model_id: &str) -> io::Result<Option<serde_json::Value>> {
        self.get_model_of_type(model_type, provider, model_id)
    }
    /// The models of a type whose provider has working credentials. It awaits the host (`getAvailableOfType`).
    pub fn get_available_of_type(&self, model_type: &str, provider: Option<&str>) -> io::Result<serde_json::Value> {
        let mut args = serde_json::json!({"type": model_type});
        if let Some(provider) = provider {
            args["provider"] = serde_json::json!(provider);
        }
        self.call("getAvailableOfType", args)
    }
    /// Classifies structured state with request-time authentication. It never fails: a failure, and a cancelled request, are a result with `stopReason` `error` or `aborted` that names the model (`classify`).
    pub fn classify(&self, model: &serde_json::Value, context: serde_json::Value, options: Option<serde_json::Value>) -> serde_json::Value {
        let mut args = serde_json::json!({"model": model, "context": context});
        if let Some(options) = options {
            args["options"] = options;
        }
        match self.call("classify", args) {
            Ok(result) => result,
            Err(error) => {
                let aborted = self.request_parent.as_ref().is_some_and(|parent| parent.cancelled());
                let timestamp = SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or_default().as_millis() as u64;
                serde_json::json!({
                    "api": model["api"], "provider": model["provider"], "model": model["id"], "answers": {},
                    "stopReason": if aborted { "aborted" } else { "error" }, "errorMessage": error.to_string(), "timestamp": timestamp
                })
            }
        }
    }
    /// Generates images with request-time authentication. `context` is `{"input": [...]}` with text and image blocks and the result is the `AssistantImages` object. It never fails: a failure, and a cancelled request, are a result with `stopReason` `error` or `aborted` that names the model (`generateImages`).
    pub fn generate_images(&self, model: &serde_json::Value, context: serde_json::Value, options: Option<serde_json::Value>) -> serde_json::Value {
        let mut args = serde_json::json!({"model": model, "context": context});
        if let Some(options) = options {
            args["options"] = options;
        }
        match self.call("generateImages", args) {
            Ok(result) => result,
            Err(error) => {
                let aborted = self.request_parent.as_ref().is_some_and(|parent| parent.cancelled());
                let timestamp = SystemTime::now().duration_since(UNIX_EPOCH).unwrap_or_default().as_millis() as u64;
                serde_json::json!({
                    "api": model["api"], "provider": model["provider"], "model": model["id"], "output": [],
                    "stopReason": if aborted { "aborted" } else { "error" }, "errorMessage": error.to_string(), "timestamp": timestamp
                })
            }
        }
    }
    /// Registers a virtual model, as [`Context::register_virtual_model`] does (`registerVirtualModel`).
    pub fn register_virtual_model(&self, model: crate::VirtualModel) -> io::Result<()> {
        let declaration = serde_json::to_value(model.declaration()).map_err(io::Error::other)?;
        install_virtual_model(&self.conn, model, || self.call("registerVirtualModel", declaration).map(|_| ()))
    }
    /// Removes a virtual model (`unregisterVirtualModel`).
    pub fn unregister_virtual_model(&self, provider: &str, id: &str) -> io::Result<()> {
        self.call("unregisterVirtualModel", serde_json::json!({"provider": provider, "id": id}))?;
        self.conn.virtual_models.lock().unwrap().remove(&(provider.to_string(), id.to_string()));
        Ok(())
    }
    pub fn get_available(&self) -> io::Result<serde_json::Value> {
        let state = self.state()?;
        let models = state["models"].as_array().ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "registry models must be an array"))?;
        Ok(serde_json::Value::Array(models.iter().filter(|m| {
            let provider = &state["providers"][m["provider"].as_str().unwrap_or_default()];
            provider["configured"] == true && provider["availableModelIds"].as_array().is_none_or(|ids| ids.contains(&m["id"]))
        }).cloned().collect()))
    }
    pub fn get_error(&self) -> io::Result<serde_json::Value> { Ok(self.state()?["error"].clone()) }
    pub fn has_configured_auth(&self, model: &serde_json::Value) -> io::Result<bool> {
        Ok(self.state()?["providers"][model["provider"].as_str().unwrap_or_default()]["configured"] == true)
    }
    pub fn is_using_oauth(&self, model: &serde_json::Value) -> io::Result<bool> {
        Ok(self.state()?["providers"][model["provider"].as_str().unwrap_or_default()]["usingOAuth"] == true)
    }
    pub fn get_provider_auth_status(&self, provider: &str) -> io::Result<serde_json::Value> {
        let state = self.state()?;
        Ok(state["providers"].get(provider).map(|p| p["authStatus"].clone()).unwrap_or_else(|| serde_json::json!({"configured":false})))
    }
    pub fn get_provider_display_name(&self, provider: &str) -> io::Result<String> {
        Ok(self.state()?["providers"][provider]["name"].as_str().unwrap_or(provider).to_owned())
    }
    pub fn get_provider_auth(&self, provider: &str) -> io::Result<serde_json::Value> {
        self.call("getProviderAuth", serde_json::json!({"provider":provider}))
    }
    pub fn get_api_key_for_provider(&self, provider: &str) -> Option<String> {
        self.get_provider_auth(provider).ok()?["auth"]["apiKey"].as_str().map(str::to_owned)
    }
    pub fn get_registered_provider_ids(&self) -> io::Result<Vec<String>> {
        let state = self.state()?;
        let registered = state["registered"].as_array().ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "registry registrations must be an array"))?;
        registered.iter().map(|r| r["name"].as_str().map(str::to_owned).ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "registry name must be a string"))).collect()
    }
    pub fn get_registered_provider_config(&self, provider: &str) -> io::Result<serde_json::Value> {
        let state = self.state()?;
        let registered = state["registered"].as_array().ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "registry registrations must be an array"))?;
        Ok(registered.iter().find(|r| r["name"] == provider).map(|r| r["config"].clone()).unwrap_or_default())
    }
    pub fn register_provider(&self, name: &str, config: serde_json::Value) -> io::Result<()> {
        self.register_provider_operations(name, config, crate::ProviderOperations::new())
    }
    /// Applies a provider config at once, as Pi's `pi.registerProvider` does after the factory finished (types.ts:1766-1803, runner.ts:517-523). The config's `images`, `classifiers` and `streamSimple` run in this extension as a factory registration's do: they stay here and the call names them. A registration the host refuses leaves the implementations the provider had.
    pub fn register_provider_operations(&self, name: &str, config: serde_json::Value, operations: crate::ProviderOperations) -> io::Result<()> {
        let callbacks = self.conn.provider_callbacks.get().ok_or_else(|| io::Error::other("Provider runtime unavailable"))?;
        let kept = callbacks.held(name);
        let declaration = callbacks.declare(name.to_string(), config, operations);
        let outcome = serde_json::to_value(declaration).map_err(io::Error::other).and_then(|args| self.call("registerProvider", args));
        if outcome.is_err() {
            callbacks.restore(name, kept);
        }
        outcome.map(|_| ())
    }
    /// Removes a provider and, once the host removed it, drops the implementations the extension held for it.
    pub fn unregister_provider(&self, name: &str) -> io::Result<()> {
        self.call("unregisterProvider", serde_json::json!({"name":name}))?;
        if let Some(callbacks) = self.conn.provider_callbacks.get() {
            callbacks.restore(name, None);
        }
        Ok(())
    }
    pub fn refresh(&self, options: serde_json::Value) -> io::Result<serde_json::Value> {
        let result = self.call("refreshModelRegistry", options)?;
        Ok(serde_json::json!({"aborted":result["aborted"],"errors":result["errors"]}))
    }

    pub fn find(&self, provider_id: &str, model_id: &str) -> Option<serde_json::Value> {
        let parent = (!self.request_id.is_empty()).then_some(self.request_id.as_str());
        let result = self
            .conn
            .call_for_scope(
                self.request_parent.as_deref(),
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
            .call_for_scope(
                self.request_parent.as_deref(),
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
        self.stream_with_method(model,request,options,false)
    }
    fn stream_with_method(&self,model:serde_json::Value,request:serde_json::Value,options:serde_json::Value,simple:bool)->Arc<ModelEventStream> {
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
        let request_parent = self.request_parent.clone();
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
            let result = conn.call_for_scope(
                request_parent.as_deref(),
                (!parent.is_empty()).then_some(parent.as_str()),
                "modelStream",
                Some(serde_json::json!({"streamId": id, "model": model, "request": merged, "simple":simple})),
            );
            let error = match result {
                Err(error) => Some(error.to_string()),
                Ok(result) => result.error.map(host_error_message),
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
        self.stream_with_method(model, request, options,true)
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
/// Passed to every handler function. Clones share the connection, cancellation and live session state, so retained callbacks can own a context without copying that state.
#[derive(Clone)]
pub struct Context {
    pub(crate) request_parent: Option<Arc<crate::protocol::RequestParent>>,
    pub(crate) conn: Arc<Connection>,
    pub(crate) tool_call_id: Option<String>,
    pub(crate) request_id: String,
    pub(crate) session_name: String,
    pub(crate) cwd: String,
    pub(crate) mode: String,
    pub(crate) shared_width: Arc<AtomicU32>,
    pub(crate) shared_height: Arc<AtomicU32>,
    pub(crate) shared_model: Arc<Mutex<String>>,
    pub(crate) flag_defaults: Arc<HashMap<String, serde_json::Value>>,
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
    /// Replicated `hasUI` and theme palette.
    pub(crate) shared_ui: Arc<Mutex<UiState>>,
    pub(crate) bus: Arc<crate::event_bus::BusRegistry>,
    /// Footer and header renderers, keyed by the host method that installs them.
    pub(crate) surfaces: Surfaces,
    /// Whether this is a withSession context, whose session log is the replacement Session's.
    pub(crate) replacement: bool,
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
    /// The outcome of the one subscription attempt. A failed attempt is not retried, so every read reports it.
    subscribe_error: Option<(io::ErrorKind, String)>,
    session_id: String,
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
        if let Some(id) = session.get("sessionId").and_then(|v| v.as_str()) {
            if !id.is_empty() && id != self.session_id {
                self.session_id = id.to_string();
                self.entries.clear();
                self.index.clear();
                self.leaf_id.clear();
                self.branch_cache = None;
                self.branch_cache_for.clear();
                self.branch_decoded = None;
                self.branch_decoded_for.clear();
                changed = true;
            }
        }

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
            return changed;
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
            request_parent: self.request_parent.clone(),
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
        if self.replacement {
            // This process replicates only the requesting Session's model.
            if let Ok(info) = self.get_model_info() {
                return info.map(|info| info.id).unwrap_or_default();
            }
        }
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
            crate::ToolResult::Error(text) => {
                serde_json::json!({"content": text, "is_error": true})
            }
        };
        self.conn.notify(
            "tool_update",
            Some(serde_json::json!({"request_id": self.request_id, "result": result})),
        )
    }

    /// Upstream's `ctx.signal`: the cancellation of the run in progress, or `None` while no run is active. Every read during one run returns the same signal, and aborting the run cancels it, including for a handler still in flight. It is the run's, not the request's: [`Context::is_cancelled`] reports the request.
    pub fn signal(&self) -> Option<crate::provider::ProviderSignal> {
        self.conn.run_signal.lock().unwrap().as_ref().map(|(_, signal)| signal.clone())
    }

    /// Returns true when the host has cancelled this request.
    pub fn is_cancelled(&self) -> bool {
        if let Some(parent) = self.request_parent.as_ref() {
            if parent.completed() { return self.conn.is_closed(); }
            if parent.cancelled() { return true; }
        }
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

    pub(crate) fn call_wire(
        &self,
        method: &str,
        args: Option<serde_json::Value>,
    ) -> io::Result<crate::protocol::CallResultMsg> {
        self.call_wire_typed(method, args)
    }

    fn call_wire_typed<A: serde::Serialize, R: serde::de::DeserializeOwned>(&self, method: &str, args: Option<A>) -> io::Result<crate::protocol::CallResultMsg<R>> {
        if !matches!(
            method,
            "ui.select" | "ui.confirm" | "ui.input" | "ui.editor" | "ui.custom"
        ) && !self.request_id.is_empty()
        {
            let _ = self
                .conn
                .request_state_for(self.request_parent.as_deref(), &self.request_id, "blocked", Some("host_call"));
        }
        let parent_request_id = (!self.request_id.is_empty()).then_some(self.request_id.as_str());
        let result = self.conn.call_typed(self.request_parent.as_deref(), parent_request_id, method, args);
        if !self.request_id.is_empty() {
            let _ = self.conn.request_state_for(self.request_parent.as_deref(), &self.request_id, "progress", None);
        }
        result
    }

    fn block_for_user(&self) {
        if !self.request_id.is_empty() {
            let _ = self
                .conn
                .request_state_for(self.request_parent.as_deref(), &self.request_id, "blocked", Some("user"));
        }
    }

    /// Register or replace a tool in the running Session. Validation precedes replacement; the host refresh completes before returning.
    pub fn register_tool(&self, definition: crate::ToolDefinition) -> io::Result<()> {
        if !definition.parameters.is_object() {
            return Err(io::Error::new(io::ErrorKind::InvalidInput, "tool parameters must be an object"));
        }
        let mut declaration = serde_json::json!({
            "name": definition.name, "label": definition.label, "description": definition.description,
            "parameters": definition.parameters, "prompt_snippet": definition.prompt_snippet,
            "prompt_guidelines": definition.prompt_guidelines, "constrained_sampling": definition.constrained_sampling,
            "execution_mode": definition.execution_mode,
            "render_shell": if definition.render_shell == crate::ToolRenderShell::SelfShell { "self" } else { "default" },
            "renders_call": definition.render_call.is_some(), "renders_result": definition.render_result.is_some(),
        });
        let optional = [
            ("output_schema", definition.output_schema.clone()),
            ("exposure", definition.exposure.map(|exposure| serde_json::Value::from(exposure.as_str()))),
            ("namespace", definition.namespace.as_ref().and_then(|namespace| serde_json::to_value(namespace).ok())),
            ("annotations", definition.annotations.as_ref().and_then(|annotations| serde_json::to_value(annotations).ok())),
            ("default_active", definition.default_active.map(serde_json::Value::from)),
            ("prepares_loadout", definition.prepare_loadout.is_some().then_some(serde_json::Value::Bool(true))),
            ("prepares_arguments", definition.prepare_arguments.is_some().then_some(serde_json::Value::Bool(true))),
        ];
        for (key, value) in optional {
            if let Some(value) = value {
                declaration[key] = value;
            }
        }
        let _registration = self.conn.tool_registration_lock.lock().unwrap();
        self.conn.registered_tools.lock().unwrap().insert(definition.name.clone(), Arc::new(definition));
        self.call_host("registerTool", Some(declaration))?;
        Ok(())
    }

    /// The host's reply object for a getter. A missing result is a protocol error, not an empty value.
    fn host_reply(&self, method: &str, args: Option<serde_json::Value>) -> io::Result<serde_json::Value> {
        self.call_host(method, args)?
            .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, format!("host returned no result for {method}")))
    }

    /// One field of a getter's reply; a missing or null field is `None`.
    fn reply_field<T: serde::de::DeserializeOwned>(&self, method: &str, args: Option<serde_json::Value>, field: &str) -> io::Result<Option<T>> {
        let mut reply = self.host_reply(method, args)?;
        match reply.get_mut(field).map(serde_json::Value::take) {
            None | Some(serde_json::Value::Null) => Ok(None),
            Some(value) => serde_json::from_value(value)
                .map(Some)
                .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, format!("host reply to {method} field {field:?}: {error}"))),
        }
    }

    /// A field every reply of the method carries; its absence is a protocol error, not an empty value.
    fn required_field<T: serde::de::DeserializeOwned>(&self, method: &str, args: Option<serde_json::Value>, field: &str) -> io::Result<T> {
        self.reply_field(method, args, field)?.ok_or_else(|| invalid_reply(method, field))
    }

    /// Pi's `string | undefined` getters carry an empty string for absent state on the wire.
    fn optional_string_field(&self, method: &str, field: &str) -> io::Result<Option<String>> {
        Ok(self.reply_field::<String>(method, None, field)?.filter(|value| !value.is_empty()))
    }

    /// Upstream's `pi.events` for calls made from this request.
    pub fn events(&self) -> crate::EventBus {
        crate::EventBus::new(self.bus.clone(), Some(self.clone()))
    }

    /// Low-level host call escape hatch. Prefer typed methods when available.
    pub fn call_host(
        &self,
        method: &str,
        args: Option<serde_json::Value>,
    ) -> io::Result<Option<serde_json::Value>> {
        let result = self.call_wire(method, args)?;
        if let Some(err) = result.error {
            return Err(io::Error::new(io::ErrorKind::Other, host_error_message(err)));
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
            options.insert(
                "deliverAs".into(),
                serde_json::Value::String(deliver_as.to_string()),
            );
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

    /// Return the resolved session scope in selection order.
    pub fn scoped_models(&self) -> io::Result<Vec<serde_json::Value>> {
        let result = self.call_wire("getScopedModels", None).and_then(call_result_value)?;
        serde_json::from_value(result)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))
    }

    /// Append a custom persistent entry to the session.
    pub fn append_entry(&self, custom_type: &str, data: serde_json::Value) -> io::Result<()> {
        call_result_to_io(self.call_wire(
            "appendEntry",
            Some(serde_json::json!({"customType": custom_type, "data": data})),
        )?)
    }

    // ─── Editor access ───────────────────────────────────────────────────

    /// Get the current editor text without replacing lone UTF-16 units. A host failure is an error, never empty text.
    pub fn get_editor_text(&self) -> io::Result<crate::JsString> {
        #[derive(serde::Deserialize)]
        struct Text { text: crate::JsString }
        let reply = self.call_wire_typed::<serde_json::Value, Text>("ui.getEditorText", None)?;
        if let Some(err) = reply.error {
            return Err(io::Error::new(io::ErrorKind::Other, host_error_message(err)));
        }
        reply.result.map(|text| text.text).ok_or_else(|| invalid_reply("ui.getEditorText", "text"))
    }

    /// Set the editor input text, preserving JavaScript UTF-16 units.
    pub fn set_editor_text(&self, text: impl Into<crate::JsString>) {
        self.editor_text_call("ui.setEditorText", text.into());
    }

    /// Paste text into the editor at cursor position.
    pub fn paste_to_editor(&self, text: impl Into<crate::JsString>) {
        self.editor_text_call("ui.pasteToEditor", text.into());
    }

    fn editor_text_call(&self, method: &str, text: crate::JsString) {
        #[derive(serde::Serialize)]
        struct Text { text: crate::JsString }
        let _: io::Result<crate::protocol::CallResultMsg> = self.call_wire_typed(method, Some(Text { text }));
    }

    // ─── Session state ───────────────────────────────────────────────────

    /// Get the session name, fetched live from the host. Pi's `getSessionName` is `string | undefined`: `None` means the session has no name.
    pub fn get_session_name(&self) -> io::Result<Option<String>> {
        self.optional_string_field("getSessionName", "name")
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

    /// Return the host flag value or its first registered default; false and empty strings remain values. `None` is Pi's undefined; a host failure is an error, not the default.
    pub fn get_flag(&self, name: &str) -> io::Result<Option<serde_json::Value>> {
        let value = self.reply_field("getFlag", Some(serde_json::json!({"name": name})), "value")?;
        Ok(value.or_else(|| self.flag_defaults.get(name).cloned()))
    }

    /// Get the current thinking level.
    pub fn get_thinking_level(&self) -> io::Result<String> {
        self.required_field("getThinkingLevel", None, "level")
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
    pub fn get_active_tools(&self) -> io::Result<Vec<String>> {
        self.required_field("getActiveTools", None, "tools")
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
    pub fn get_tools_expanded(&self) -> io::Result<bool> {
        self.required_field("ui.getToolsExpanded", None, "expanded")
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
    pub fn get_all_themes(&self) -> io::Result<Vec<serde_json::Value>> {
        self.required_field("ui.getAllThemes", None, "themes")
    }

    /// Load a theme by name without switching to it.
    pub fn get_theme(&self, name: &str) -> io::Result<Option<serde_json::Value>> {
        self.call_host("ui.getTheme", Some(serde_json::json!({"name": name})))
            .map(|v| v.and_then(|raw| raw.get("theme").filter(|theme| !theme.is_null()).cloned()))
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

    /// Sets a widget from a list of strings, as Pi's `ctx.ui.setWidget(key,
    /// string[])`. The host lays the list out at its own width: each entry
    /// becomes `Text(line, 1, 0)`, the first ten entries are shown, and a muted
    /// "... (widget truncated)" row follows a longer list
    /// (interactive-mode.ts:2321-2336). A row wider than the pane wraps.
    ///
    /// The list goes out as a `widget_push` frame without a width, which the
    /// host applies in arrival order and never answers: Pi's `setWidget`
    /// returns nothing, so it may be called from an
    /// [`Self::on_width_change`] handler, and the error reports only a failure
    /// to send. [`Self::set_widget_value`] waits for the host.
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
        self.set_surface("ui.setFooter", None)
    }

    /// Set the semantic login rendered by Pig's fixed native template.
    /// Validation is performed by the host.
    pub fn set_login(&self, definition: &LoginDefinition) -> io::Result<()> {
        let args = serde_json::to_value(definition)
            .map_err(|err| io::Error::new(io::ErrorKind::InvalidInput, err))?;
        call_result_to_io(self.conn.call("ui.setLogin", Some(args))?)
    }

    /// Add a sprite to /sprite, where the user can choose and save it. Validation is performed
    /// by the host; registering the same ID again replaces this extension's sprite, and the
    /// sprite leaves /sprite when the extension unloads.
    pub fn register_sprite(&self, definition: &SpriteDefinition) -> io::Result<()> {
        let args = serde_json::to_value(definition)
            .map_err(|err| io::Error::new(io::ErrorKind::InvalidInput, err))?;
        call_result_to_io(self.conn.call("ui.registerSprite", Some(args))?)
    }

    /// Clear a custom header. Passing live component factories is intentionally unsupported by the subprocess bridge.
    pub fn clear_header(&self) -> io::Result<()> {
        self.set_surface("ui.setHeader", None)
    }

    /// Clear a custom editor component. Passing live component factories is intentionally unsupported by the subprocess bridge.
    pub fn clear_editor_component(&self) -> io::Result<()> {
        call_result_to_io(self.call_wire(
            "ui.setEditorComponent",
            Some(serde_json::json!({"clear": true})),
        )?)
    }

    /// Invoke the host custom UI bridge with raw options, or return None when no UI is bound.
    pub fn custom(&self, options: serde_json::Value) -> io::Result<Option<serde_json::Value>> {
        if !self.has_ui() {
            return Ok(None);
        }
        self.block_for_user();
        self.call_host("ui.custom", Some(options))
    }

    /// Open a focused subprocess component. Input reaches the component only
    /// while the host overlay owns focus. Render requests are coalesced on one
    /// component worker, and cleanup detaches invalidation before disposal. With no UI, returns None without invoking component callbacks.
    pub fn custom_component(
        &self,
        component: impl RemoteComponent + 'static,
        options: serde_json::Value,
    ) -> io::Result<Option<serde_json::Value>> {
        if !self.has_ui() {
            return Ok(None);
        }
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

        let pending = match self.conn.begin_call_with_scope(
            self.request_parent.as_deref(),
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
        let _ = self.conn.request_state_for(self.request_parent.as_deref(), &self.request_id, "progress", None);
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

    /// Subscribes to raw terminal input, receiving every chunk before the
    /// editor does.
    ///
    /// The host is told to start forwarding only on the first subscription and
    /// to stop on the last, so an extension that never subscribes costs the
    /// input loop nothing. With no UI, no subscription is retained. The returned guard unsubscribes when dropped.
    pub fn on_terminal_input<F>(&self, handler: F) -> io::Result<TerminalInputSubscription>
    where
        F: Fn(&crate::JsString) -> TerminalInputResult + Send + Sync + 'static,
    {
        if !self.has_ui() {
            return Ok(TerminalInputSubscription {
                id: 0,
                subs: self.terminal_input.clone(),
                conn: self.conn.clone(),
                released: true,
            });
        }
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

    /// Get every tool in the session's registry, active or not: built-in
    /// tools, then extension tools. Mirrors upstream `pi.getAllTools()`.
    pub fn get_all_tools(&self) -> io::Result<Vec<ToolInfo>> {
        self.required_field("getAllTools", None, "tools")
    }

    /// Upstream `pi.getSettings()` (`types.ts:1711`): a copy of the effective settings, global and project settings merged with overrides. Answered from the state the host replicates, as upstream's call is synchronous.
    ///
    /// Fails until the host has sent settings, as upstream's getter throws before the runtime is bound (`loader.ts:157-177`).
    pub fn get_settings(&self) -> io::Result<serde_json::Value> {
        self.conn
            .api_state
            .lock()
            .unwrap()
            .settings
            .clone()
            .ok_or_else(|| io::Error::other("the host sent no settings"))
    }

    /// Upstream `pi.getMcpServers()` (`types.ts:1839`): every MCP server registered by extensions, in registration order, from the replicated state.
    pub fn get_mcp_servers(&self) -> Vec<crate::RegisteredMcpServer> {
        self.conn.api_state.lock().unwrap().mcp_servers.clone()
    }

    /// Upstream `pi.registerMcpServer(name, config)` after load (`types.ts:1833`). The server connects right away. Registering a name again replaces this extension's earlier registration.
    ///
    /// Fails, as upstream throws, for an invalid config and for a name another extension registered; the error carries the host's message. The registration is not saved; register again on every load. During load, use [`crate::Extension::register_mcp_server`].
    pub fn register_mcp_server(&self, name: &str, config: serde_json::Value) -> io::Result<()> {
        let servers = self.mcp_servers_call("registerMcpServer", serde_json::json!({"name": name, "config": config}))?;
        self.conn.api_state.lock().unwrap().mcp_servers = servers;
        Ok(())
    }

    /// Upstream `pi.registerToolRenderer(resolver)` after load: the resolver runs after the extension's earlier
    /// resolvers, and the host asks the extension's resolvers again for every tool. During load, use
    /// [`crate::Extension::tool_renderer`].
    pub fn register_tool_renderer(
        &self,
        resolver: impl Fn(&str, &dyn Fn() -> Option<crate::ToolRendererSet>) -> Option<crate::ToolRendererSet> + Send + Sync + 'static,
    ) -> io::Result<()> {
        let late = &self.conn.late_tool_renderers;
        let mut resolvers = late.resolvers.lock().unwrap();
        resolvers.push(Arc::new(resolver));
        let count = late.loaded.load(std::sync::atomic::Ordering::SeqCst) + resolvers.len();
        // The count reaches the host in registration order.
        self.conn.notify("tool_renderers", Some(serde_json::json!({ "count": count })))
    }

    /// Upstream `pi.unregisterMcpServer(name)` (`types.ts:1836`): removes an MCP server this extension registered and closes its connection.
    pub fn unregister_mcp_server(&self, name: &str) -> io::Result<()> {
        let servers = self.mcp_servers_call("unregisterMcpServer", serde_json::json!({"name": name}))?;
        self.conn.api_state.lock().unwrap().mcp_servers = servers;
        Ok(())
    }

    /// The reply of an MCP registration call: every registered server after it.
    fn mcp_servers_call(&self, method: &str, args: serde_json::Value) -> io::Result<Vec<crate::RegisteredMcpServer>> {
        let reply = self.host_reply(method, Some(args))?;
        serde_json::from_value::<crate::extension_api::McpServersResult>(reply)
            .map(|result| result.servers)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, format!("host reply to {method}: {error}")))
    }

    /// Upstream `pi.registerVirtualModel(model)` after load (`types.ts:1852`). Registering the same provider and id again replaces the virtual model. During load, use [`crate::Extension::register_virtual_model`].
    pub fn register_virtual_model(&self, model: crate::VirtualModel) -> io::Result<()> {
        let declaration = serde_json::to_value(model.declaration()).map_err(io::Error::other)?;
        install_virtual_model(&self.conn, model, || self.call_host("registerVirtualModel", Some(declaration)).map(|_| ()))
    }

    /// Upstream `pi.unregisterVirtualModel(provider, id)` (`types.ts:1855`).
    pub fn unregister_virtual_model(&self, provider: &str, id: &str) -> io::Result<()> {
        self.call_host("unregisterVirtualModel", Some(serde_json::json!({"provider": provider, "id": id})))?;
        self.conn.virtual_models.lock().unwrap().remove(&(provider.to_string(), id.to_string()));
        Ok(())
    }

    /// Upstream `ExtensionToolContext.tools` (`types.ts:385`): the tools [`Context::execute_tool`] can call, as the host lists them when this is called (`runner.ts:958-961`): a read after [`Context::set_active_tools`] in the same handler sees the change.
    ///
    /// Fails outside a tool call: upstream defines `tools` only on the context of a tool's `execute` (`runner.ts:952-965`).
    pub fn tools(&self) -> io::Result<Vec<crate::AgentTool>> {
        if self.tool_call_id.is_none() || self.request_id.is_empty() {
            return Err(io::Error::other("tools is only available while a tool runs"));
        }
        self.required_field("getCallableTools", None, "tools")
    }

    /// Upstream `ExtensionToolContext.executeTool(name, args, options)` (`types.ts:394`): runs another tool through the same validation, hooks and permission checks as a model-issued call. The call gets the id `<calling id>/<n>` and its events carry `parentToolCallId`.
    ///
    /// Tool failures (unknown tools, validation errors, blocked calls, thrown errors) come back as an outcome with `is_error` set, as upstream never rejects for them; an `Err` is a lost connection, a cancelled request or a call made outside a tool. The call is cancelled with `options.signal`, which defaults to this tool call's own cancellation (the host cancels the nested call with the calling request). Partial results reach `options.on_update` in order, before this returns.
    pub fn execute_tool(
        &self,
        name: &str,
        args: serde_json::Value,
        options: crate::ExecuteToolOptions,
    ) -> io::Result<crate::AgentToolCallOutcome> {
        let Some(caller_id) = self.tool_call_id.clone().filter(|_| !self.request_id.is_empty()) else {
            return Err(io::Error::other("execute_tool is only available while a tool runs"));
        };
        let execute_id = format!("x{}", self.conn.execute_seq.fetch_add(1, Ordering::Relaxed));
        // Updates and the outcome are read from one ordered socket; the dispatcher runs the callback off the reader and is joined
        // after the outcome, so every update precedes the return.
        // The host sends each update as a request and waits for the answer, so the callback's panic is that answer: upstream's callback throws and the call rejects with the first error once the tool returned (`nested-tool-calls.ts:219-248`).
        let dispatcher = options.on_update.as_ref().map(|callback| {
            let (sender, receiver) = std::sync::mpsc::channel::<crate::protocol::ExecuteUpdate>();
            self.conn.execute_updates.lock().unwrap().insert(execute_id.clone(), sender);
            let callback = callback.clone();
            std::thread::spawn(move || {
                for (update, answer) in receiver {
                    let failure = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| callback(update))).err().map(|payload| panic_message(payload.as_ref()));
                    let _ = answer.send(failure);
                }
            })
        });
        let outcome = self.run_execute_tool(&execute_id, crate::extension_api::ExecuteToolArgs {
            caller_id,
            name: name.to_string(),
            args,
            execute_id: execute_id.clone(),
            wants_updates: dispatcher.is_some(),
            own_signal: options.signal.is_some(),
        }, options.signal.as_ref());
        self.conn.execute_updates.lock().unwrap().remove(&execute_id);
        if let Some(dispatcher) = dispatcher {
            let _ = dispatcher.join();
        }
        outcome
    }

    fn run_execute_tool(
        &self,
        execute_id: &str,
        args: crate::extension_api::ExecuteToolArgs,
        signal: Option<&crate::provider::ProviderSignal>,
    ) -> io::Result<crate::AgentToolCallOutcome> {
        let _ = self.conn.request_state_for(self.request_parent.as_deref(), &self.request_id, "blocked", Some("host_call"));
        let pending = self.conn.begin_call_with_scope(self.request_parent.as_deref(), Some(&self.request_id), "executeTool", Some(args))?;
        // Subscribed after the call frame is written: the host applies the cancel after it started the call (runner.ts:979).
        let _cancel = signal.map(|signal| {
            let conn = self.conn.clone();
            let execute_id = execute_id.to_string();
            signal.subscribe(Arc::new(move || {
                let _ = conn.begin_call_for(None, "executeTool.cancel", Some(serde_json::json!({"executeId": execute_id})));
            }))
        });
        let reply = self.conn.wait_call_typed::<crate::AgentToolCallOutcome>(pending);
        let _ = self.conn.request_state_for(self.request_parent.as_deref(), &self.request_id, "progress", None);
        let reply = reply?;
        if let Some(error) = reply.error {
            return Err(io::Error::other(host_error_message(error)));
        }
        reply.result.ok_or_else(|| invalid_reply("executeTool", "result"))
    }

    /// Get the session's extension commands, prompt templates and skills.
    /// Mirrors upstream `pi.getCommands()`.
    pub fn get_commands(&self) -> io::Result<Vec<CommandInfo>> {
        self.required_field("getCommands", None, "commands")
    }

    /// Get current context usage (token counts and context window percentage). Pi's `getContextUsage` is `ContextUsage | undefined`: `None` means no usable context window.
    pub fn get_context_usage(&self) -> io::Result<Option<ContextUsage>> {
        let Some(v) = self.call_host("getContextUsage", None)? else {
            return Ok(None);
        };
        if v.is_null() {
            return Ok(None);
        }
        let context_window = v
            .get("contextWindow")
            .and_then(serde_json::Value::as_i64)
            .ok_or_else(|| invalid_reply("getContextUsage", "contextWindow"))?;
        Ok(Some(ContextUsage {
            tokens: v.get("tokens").and_then(serde_json::Value::as_i64).map(|n| n as i32),
            context_window: context_window as i32,
            percent: v.get("percent").and_then(serde_json::Value::as_f64),
        }))
    }

    /// Get the current system prompt text.
    pub fn get_system_prompt(&self) -> io::Result<String> {
        self.required_field("getSystemPrompt", None, "prompt")
    }

    /// Get the base inputs pi currently uses to build the system prompt
    /// (customPrompt, selectedTools, toolSnippets, promptGuidelines,
    /// appendSystemPrompt, cwd, contextFiles, skills). Reports current
    /// base inputs only, not per-turn before_agent_start changes. May
    /// include full context-file contents; treat as sensitive.
    pub fn get_system_prompt_options(&self) -> io::Result<serde_json::Value> {
        self.host_reply("getSystemPromptOptions", None)
    }

    /// Get structured metadata about the active model. Pi's model is `Model | undefined`: `None` means no model is set.
    pub fn get_model_info(&self) -> io::Result<Option<ModelInfo>> {
        {
            {
                let Some(v) = self.call_host("getModelInfo", None)? else {
                    return Ok(None);
                };
                let Some(id) = v.get("id").and_then(|s| s.as_str()).filter(|id| !id.is_empty()).map(str::to_owned) else {
                    return Ok(None);
                };
                Ok(Some(ModelInfo {
                    input_limits: v
                        .get("inputLimits")
                        .filter(|value| !value.is_null())
                        .cloned(),
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
                }))
            }
        }
    }

    /// Get all persisted session entries as shared raw JSON values.
    pub fn get_entries(&self) -> io::Result<Vec<std::sync::Arc<serde_json::Value>>> {
        if self.replacement {
            // The local mirror replicates the Session that requested the replacement.
            let entries = self.session_manager().get_entries()?;
            return Ok(entries.as_array().into_iter().flatten().cloned().map(std::sync::Arc::new).collect());
        }
        self.ensure_session_log()?;
        Ok(self.shared_session.lock().unwrap().get_entries())
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
    fn ensure_session_log(&self) -> io::Result<()> {
        use std::sync::atomic::Ordering;
        let _guard = self.session_sub_lock.lock().unwrap();
        let subscribed = {
            let mirror = self.shared_session.lock().unwrap();
            mirror.subscribed.clone()
        };
        if subscribed.load(Ordering::Acquire) {
            return match &self.shared_session.lock().unwrap().subscribe_error {
                Some((kind, message)) => Err(io::Error::new(*kind, message.clone())),
                None => Ok(()),
            };
        }
        // Set before the call: the host starts sending the log as soon as it
        // registers the subscription, and those pushes must be applied. The
        // mirror lock is not held across the call, which the reader thread
        // needs in order to deliver the response.
        subscribed.store(true, Ordering::Release);
        let outcome = self.subscribe_session_log();
        if let Err(error) = &outcome {
            self.shared_session.lock().unwrap().subscribe_error = Some((error.kind(), error.to_string()));
        }
        outcome
    }

    fn subscribe_session_log(&self) -> io::Result<()> {
        let mut entries = Self::read_session_entries(&self.get_session_file()?.unwrap_or_default());
        let mut cursor = entries.len();
        let mut leaf = String::new();
        loop {
            let requested_cursor = cursor;
            let value = self.watch_session_log(serde_json::json!({"cursor": cursor}))?;
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
            let value = self.watch_session_log(serde_json::json!({"cursor": cursor, "complete": true}))?;
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
                return Ok(());
            }
        }
    }

    fn watch_session_log(&self, args: serde_json::Value) -> io::Result<serde_json::Value> {
        self.host_reply("watchSessionLog", Some(args))
    }

    /// Get model auth metadata from the host. A host failure is an error; `None` is an empty reply.
    pub fn get_model_auth(&self, provider_id: &str, model_id: &str) -> io::Result<Option<serde_json::Value>> {
        self.call_host(
            "getModelAuth",
            Some(serde_json::json!({"provider": provider_id, "modelId": model_id})),
        )
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
    pub fn get_branch(&self) -> io::Result<Vec<std::sync::Arc<BranchEntry>>> {
        if self.replacement {
            let branch = self.session_manager().get_branch(None)?;
            return Ok(branch
                .as_array()
                .into_iter()
                .flatten()
                .filter_map(|entry| serde_json::from_value(entry.clone()).ok())
                .map(std::sync::Arc::new)
                .collect());
        }
        self.ensure_session_log()?;
        Ok(self.shared_session.lock().unwrap().get_branch_entries())
    }

    // ─── Session identity ────────────────────────────────────────────────

    /// The current session's id, including for in-memory sessions.
    pub fn get_session_id(&self) -> io::Result<String> {
        self.required_field("getSessionID", None, "sessionId")
    }

    /// The current session file path. Pi's `getSessionFile` is `string | undefined`: `None` means an in-memory session.
    pub fn get_session_file(&self) -> io::Result<Option<String>> {
        self.optional_string_field("getSessionFile", "sessionFile")
    }

    /// The current leaf entry id. Pi's `getLeafId` is `string | null`: `None` means an empty session.
    pub fn get_leaf_id(&self) -> io::Result<Option<String>> {
        self.optional_string_field("getLeafID", "leafId")
    }

    // ─── Shell ───────────────────────────────────────────────────────────

    /// Run a command through the host's executor.
    ///
    /// Returns `Err` when the host reports a failure, so a caller sees the
    /// reason rather than an exit code of zero it never produced.
    pub fn exec(&self, command: &str, args: &[&str]) -> Result<ExecResult, String> {
        self.exec_call(serde_json::json!({ "command": command, "args": args }))
    }

    /// Run a command through the host's executor with upstream `ExecOptions`
    /// (timeout in milliseconds, working directory). Errors as [`Self::exec`].
    pub fn exec_with_options(
        &self,
        command: &str,
        args: &[&str],
        options: &ExecOptions,
    ) -> Result<ExecResult, String> {
        let mut opts = serde_json::Map::new();
        if let Some(timeout) = options.timeout {
            opts.insert("timeout".into(), serde_json::Value::from(timeout));
        }
        if let Some(cwd) = options.cwd.as_deref() {
            opts.insert("cwd".into(), serde_json::Value::String(cwd.to_string()));
        }
        self.exec_call(serde_json::json!({ "command": command, "args": args, "options": opts }))
    }

    fn exec_call(&self, args: serde_json::Value) -> Result<ExecResult, String> {
        let result = self
            .call_wire("exec", Some(args))
            .map_err(|e| e.to_string())?;
        if let Some(error) = result.error {
            return Err(host_error_message(error));
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
            killed: value
                .get("killed")
                .and_then(|v| v.as_bool())
                .unwrap_or(false),
        })
    }

    // ─── Agent/session control ───────────────────────────────────────────

    /// Whether the current project is trusted. Untrusted projects have
    /// project-scoped settings and hooks disabled. A host failure is an error, not an assumed trust.
    pub fn is_project_trusted(&self) -> io::Result<bool> {
        self.required_field("isProjectTrusted", None, "trusted")
    }

    /// Whether the agent is idle. A host failure is an error, not an assumed idle state.
    pub fn is_idle(&self) -> io::Result<bool> {
        self.required_field("isIdle", None, "idle")
    }

    pub fn abort(&self) {
        let _ = self.call_wire("abort", None);
    }

    /// Whether messages are queued. A host failure is an error.
    pub fn has_pending_messages(&self) -> io::Result<bool> {
        self.required_field("hasPendingMessages", None, "pending")
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
        self.call_replacement("newSession", opts, None)
    }

    /// `new_session` with Pi's `withSession` callback, which runs with the replacement Session's context before the
    /// call returns.
    pub fn new_session_with(
        &self,
        opts: serde_json::Value,
        with_session: impl FnOnce(&crate::ReplacedSessionContext) -> io::Result<()> + Send + 'static,
    ) -> io::Result<Option<serde_json::Value>> {
        self.call_replacement("newSession", opts, Some(Box::new(with_session)))
    }

    pub fn fork(
        &self,
        entry_id: &str,
        opts: serde_json::Value,
    ) -> io::Result<Option<serde_json::Value>> {
        self.call_replacement("fork", with_field(opts, "entryId", entry_id), None)
    }

    /// `fork` with Pi's `withSession` callback, which runs with the replacement Session's context before the call
    /// returns.
    pub fn fork_with(
        &self,
        entry_id: &str,
        opts: serde_json::Value,
        with_session: impl FnOnce(&crate::ReplacedSessionContext) -> io::Result<()> + Send + 'static,
    ) -> io::Result<Option<serde_json::Value>> {
        self.call_replacement("fork", with_field(opts, "entryId", entry_id), Some(Box::new(with_session)))
    }

    pub fn navigate_tree(
        &self,
        target_id: &str,
        opts: serde_json::Value,
    ) -> io::Result<Option<serde_json::Value>> {
        self.call_host("navigateTree", Some(with_field(opts, "targetId", target_id)))
    }

    pub fn switch_session(
        &self,
        session_path: &str,
        opts: serde_json::Value,
    ) -> io::Result<Option<serde_json::Value>> {
        self.call_replacement("switchSession", with_field(opts, "sessionPath", session_path), None)
    }

    /// `switch_session` with Pi's `withSession` callback, which runs with the replacement Session's context before
    /// the call returns.
    pub fn switch_session_with(
        &self,
        session_path: &str,
        opts: serde_json::Value,
        with_session: impl FnOnce(&crate::ReplacedSessionContext) -> io::Result<()> + Send + 'static,
    ) -> io::Result<Option<serde_json::Value>> {
        self.call_replacement(
            "switchSession",
            with_field(opts, "sessionPath", session_path),
            Some(Box::new(with_session)),
        )
    }

    pub fn reload(&self) -> io::Result<()> {
        call_result_to_io(self.call_wire("reload", None)?)
    }
}

impl Context {
    // ─── Upstream-shaped variants of existing calls ─────────────────────

    /// Upstream `pi.sendMessage(message, options)`: injects a custom message
    /// whose content may be a string or content blocks, with optional
    /// details.
    pub fn send_custom_message(
        &self,
        message: &CustomMessage,
        options: &SendMessageOptions,
    ) -> io::Result<()> {
        let mut msg = serde_json::Map::new();
        msg.insert(
            "customType".into(),
            serde_json::Value::String(message.custom_type.clone()),
        );
        msg.insert("content".into(), message.content.clone());
        msg.insert("display".into(), serde_json::Value::Bool(message.display));
        if let Some(details) = &message.details {
            msg.insert("details".into(), details.clone());
        }
        let mut opts = serde_json::Map::new();
        if let Some(trigger_turn) = options.trigger_turn {
            opts.insert("triggerTurn".into(), serde_json::Value::Bool(trigger_turn));
        }
        if let Some(deliver_as) = options.deliver_as.as_deref().filter(|v| !v.is_empty()) {
            opts.insert(
                "deliverAs".into(),
                serde_json::Value::String(deliver_as.to_string()),
            );
        }
        call_result_to_io(self.call_wire(
            "sendMessage",
            Some(serde_json::json!({"message": msg, "options": opts})),
        )?)
    }

    fn dialog_opts(options: &DialogOptions) -> serde_json::Value {
        let mut opts = serde_json::Map::new();
        if let Some(timeout) = options.timeout {
            opts.insert("timeout".into(), serde_json::Value::from(timeout));
        }
        serde_json::Value::Object(opts)
    }

    /// [`Self::select`] with upstream dialog options.
    pub fn select_with_options(
        &self,
        title: &str,
        options: &[&str],
        opts: &DialogOptions,
    ) -> io::Result<(String, bool)> {
        self.block_for_user();
        let v = call_result_value(self.call_wire(
            "ui.select",
            Some(serde_json::json!({
                "title": title,
                "options": options,
                "opts": Self::dialog_opts(opts),
            })),
        )?)?;
        let selected = v
            .get("selected")
            .and_then(|s| s.as_str())
            .unwrap_or("")
            .to_string();
        let ok = v.get("ok").and_then(|b| b.as_bool()).unwrap_or(false);
        Ok((selected, ok))
    }

    /// [`Self::confirm`] with upstream dialog options.
    pub fn confirm_with_options(
        &self,
        title: &str,
        message: &str,
        opts: &DialogOptions,
    ) -> io::Result<bool> {
        self.block_for_user();
        let v = call_result_value(self.call_wire(
            "ui.confirm",
            Some(serde_json::json!({
                "title": title,
                "message": message,
                "opts": Self::dialog_opts(opts),
            })),
        )?)?;
        Ok(v.get("confirmed")
            .and_then(|c| c.as_bool())
            .unwrap_or(false))
    }

    /// [`Self::input`] with upstream dialog options.
    pub fn input_with_options(
        &self,
        title: &str,
        placeholder: &str,
        opts: &DialogOptions,
    ) -> io::Result<(String, bool)> {
        self.block_for_user();
        let v = call_result_value(self.call_wire(
            "ui.input",
            Some(serde_json::json!({
                "title": title,
                "placeholder": placeholder,
                "opts": Self::dialog_opts(opts),
            })),
        )?)?;
        let text = v
            .get("text")
            .and_then(|s| s.as_str())
            .unwrap_or("")
            .to_string();
        let ok = v.get("ok").and_then(|b| b.as_bool()).unwrap_or(false);
        Ok((text, ok))
    }

    /// Replaces the footer with pre-rendered lines (upstream
    /// `ctx.ui.setFooter`; a component factory cannot cross the process
    /// boundary). [`Self::clear_footer`] restores the default.
    ///
    /// Pi renders a footer component at the current width every frame, so it
    /// never paints rows laid out for another width. The rows sent here carry
    /// the width the SDK holds when they are sent, and the host paints them
    /// only at that width: after a resize they stay hidden until the extension
    /// sends rows for the new width (see [`Self::on_width_change`] for where to
    /// send them from). A footer
    /// that must never be wrong renders through [`Self::set_footer_renderer`].
    /// Setting rows replaces any footer renderer.
    pub fn set_footer(&self, lines: Vec<String>) -> io::Result<()> {
        self.set_surface("ui.setFooter", Some(lines))
    }

    /// Replaces the header with pre-rendered lines (upstream
    /// `ctx.ui.setHeader`). [`Self::clear_header`] restores the default. The
    /// rows carry the width the SDK holds when they are sent, as described at
    /// [`Self::set_footer`].
    pub fn set_header(&self, lines: Vec<String>) -> io::Result<()> {
        self.set_surface("ui.setHeader", Some(lines))
    }

    /// Installs a footer that `render` lays out at the host's terminal width,
    /// the subprocess form of the component factory Pi's `ctx.ui.setFooter`
    /// takes. The SDK renders at the width the host reports and again after
    /// every width change, and sends each set of rows with the width it was
    /// rendered for, so the host never paints rows laid out for another width.
    /// `None` restores the built-in footer. A panic in `render` is reported to
    /// the user as "footer render failed: ..." and leaves the previous rows.
    pub fn set_footer_renderer<F>(&self, render: Option<F>) -> io::Result<()>
    where
        F: Fn(u32) -> Vec<String> + Send + Sync + 'static,
    {
        self.set_surface_renderer("ui.setFooter", render)
    }

    /// Installs a header that `render` lays out at the host's terminal width;
    /// it follows the contract of [`Self::set_footer_renderer`].
    pub fn set_header_renderer<F>(&self, render: Option<F>) -> io::Result<()>
    where
        F: Fn(u32) -> Vec<String> + Send + Sync + 'static,
    {
        self.set_surface_renderer("ui.setHeader", render)
    }

    /// Upstream `ctx.hasUI`, from the host's replicated state.
    pub fn has_ui(&self) -> bool {
        self.shared_ui.lock().unwrap().has_ui
    }

    /// Upstream `ctx.ui.theme`: a snapshot of the host's active theme, kept
    /// current by the host's state and `theme_change` notifies.
    pub fn theme(&self) -> Theme {
        self.shared_ui.lock().unwrap().theme.clone()
    }

    /// Upstream `ctx.compact(options)`. Returns immediately. Without
    /// callbacks it asks the host to compact, as [`Self::compact`] does. With
    /// either callback, a background thread waits for compaction to finish
    /// and runs `on_complete` with upstream's `CompactionResult` or
    /// `on_error` with the failure text. That wait is not tied to the
    /// current request, so it outlives the handler that started it.
    pub fn compact_with_options(&self, options: CompactOptions) {
        let CompactOptions {
            custom_instructions,
            on_complete,
            on_error,
        } = options;
        let mut args = serde_json::Map::new();
        if let Some(instructions) = custom_instructions {
            args.insert(
                "customInstructions".into(),
                serde_json::Value::String(instructions),
            );
        }
        if on_complete.is_none() && on_error.is_none() {
            self.compact(serde_json::Value::Object(args));
            return;
        }
        args.insert("awaitCompletion".into(), serde_json::Value::Bool(true));
        let conn = self.conn.clone();
        thread::spawn(move || {
            let outcome = match conn.call("compact", Some(serde_json::Value::Object(args))) {
                Err(err) => Err(err.to_string()),
                Ok(result) => match result.error {
                    Some(error) => Err(error.message),
                    None => Ok(result.result.unwrap_or(serde_json::Value::Null)),
                },
            };
            match outcome {
                Ok(result) => {
                    if let Some(on_complete) = on_complete {
                        on_complete(result);
                    }
                }
                Err(message) => {
                    if let Some(on_error) = on_error {
                        on_error(message);
                    }
                }
            }
        });
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
    /// The host's `code`.
    pub exit_code: i32,
    /// Whether the command was killed (timeout or cancellation).
    #[serde(default)]
    pub killed: bool,
}

/// Upstream `ExecOptions` for [`Context::exec_with_options`]. Cancellation
/// (upstream's `signal`) is the request's own.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct ExecOptions {
    /// Timeout in milliseconds. Pi's `timeout` is a JavaScript number: fractional and very large values are meaningful, and only a positive one starts a timer.
    pub timeout: Option<f64>,
    /// Working directory; the session's when unset.
    pub cwd: Option<String>,
}

/// Upstream `ExtensionUIDialogOptions` for the `*_with_options` dialogs.
/// Cancellation (upstream's `signal`) is the request's own.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct DialogOptions {
    /// Dismisses the dialog after this many milliseconds. Pi's `timeout` is a JavaScript number: fractional and very large values are meaningful, and only a positive one starts a countdown.
    pub timeout: Option<f64>,
}

/// The message of upstream `pi.sendMessage`: `CustomMessage`'s `customType`,
/// `content` (a string or text/image content blocks), `display` and
/// `details`.
#[derive(Debug, Clone, PartialEq)]
pub struct CustomMessage {
    pub custom_type: String,
    pub content: serde_json::Value,
    pub display: bool,
    pub details: Option<serde_json::Value>,
}

/// Upstream `pi.sendMessage` options. `None` leaves an option unset so the
/// host applies upstream's default for the session's state.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SendMessageOptions {
    pub trigger_turn: Option<bool>,
    /// `"steer"`, `"followUp"` or `"nextTurn"`.
    pub deliver_as: Option<String>,
}

/// Called with upstream's `CompactionResult` when compaction finishes.
pub type CompactCompleteHandler = Box<dyn FnOnce(serde_json::Value) + Send>;
/// Called with the failure text when compaction fails.
pub type CompactErrorHandler = Box<dyn FnOnce(String) + Send>;

/// Upstream `CompactOptions` for [`Context::compact_with_options`].
#[derive(Default)]
pub struct CompactOptions {
    pub custom_instructions: Option<String>,
    pub on_complete: Option<CompactCompleteHandler>,
    pub on_error: Option<CompactErrorHandler>,
}

/// Upstream `SourceInfo`: where a tool, command, prompt template or skill
/// came from.
#[derive(Debug, Clone, Default, PartialEq, serde::Serialize, serde::Deserialize)]
pub struct SourceInfo {
    pub path: String,
    pub source: String,
    pub scope: String,
    pub origin: String,
    #[serde(default, rename = "baseDir", skip_serializing_if = "Option::is_none")]
    pub base_dir: Option<String>,
}

/// Upstream `ToolInfo`, one entry of `get_all_tools()`: a tool definition's
/// name, description, parameter schema and prompt guidelines, and the
/// `SourceInfo` of what registered it (`builtin` for built-in tools).
#[derive(Debug, Clone, PartialEq, serde::Serialize, serde::Deserialize)]
pub struct ToolInfo {
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub parameters: serde_json::Value,
    /// `None` when the definition has no prompt guidelines.
    #[serde(
        default,
        rename = "promptGuidelines",
        skip_serializing_if = "Option::is_none"
    )]
    pub prompt_guidelines: Option<Vec<String>>,
    #[serde(default, rename = "sourceInfo")]
    pub source_info: SourceInfo,
    /// How the model reaches the tool (`types.ts:2063`); `direct` when the host omits it.
    #[serde(default)]
    pub exposure: crate::ToolExposure,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub namespace: Option<crate::ToolNamespace>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub annotations: Option<crate::ToolAnnotations>,
}

/// Upstream `SlashCommandInfo`, one entry of `get_commands()`.
#[derive(Debug, Clone, PartialEq, serde::Serialize, serde::Deserialize)]
pub struct CommandInfo {
    pub name: String,
    /// Empty when the command has no description.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub description: String,
    /// `extension`, `prompt` or `skill`.
    #[serde(default)]
    pub source: String,
    #[serde(default, rename = "sourceInfo")]
    pub source_info: SourceInfo,
}

/// Context usage data returned by `get_context_usage()`. Tokens and percent are None while usage is unknown after compaction.
#[derive(Debug, Clone)]
pub struct ContextUsage {
    pub tokens: Option<i32>,
    pub context_window: i32,
    pub percent: Option<f64>,
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

/// A footer or header renderer, the subprocess form of Pi's component factory.
///
/// pig additive (D19): Pi's TUI renders the in-process component every frame; a
/// subprocess SDK renders at the host width itself and sends the width with the rows.
///
/// The TUI calls `render(width)` for every frame (interactive-mode.ts:2418-2480).
/// The SDK renders at the width the host reported, sends the rows with that
/// width so the host never paints them at another, and renders again after each
/// `width_change`. Only the newest width is rendered when several arrive together.
pub(crate) struct SurfaceRenderer {
    method: &'static str,
    render: Box<dyn Fn(u32) -> Vec<String> + Send + Sync>,
    /// True once superseded. Held while a push is in flight, so a superseded
    /// renderer never overwrites the rows that replaced it.
    stopped: Mutex<bool>,
    /// (a refresh is queued, a refresh thread is running)
    refresh: Mutex<(bool, bool)>,
}

pub(crate) type Surfaces = Arc<Mutex<HashMap<&'static str, Arc<SurfaceRenderer>>>>;

fn surface_args(lines: Vec<String>, width: u32) -> serde_json::Value {
    if width > 0 {
        serde_json::json!({"lines": lines, "width": width})
    } else {
        serde_json::json!({"lines": lines})
    }
}

impl SurfaceRenderer {
    fn kind(&self) -> &'static str {
        if self.method == "ui.setHeader" { "header" } else { "footer" }
    }

    /// Renders at the current host width and sends the rows tagged with it. A
    /// panicking renderer is reported as the Node runtime reports a failing
    /// component (runtime.mjs renderSpecialSurface) and leaves the previous rows.
    fn push(&self, ctx: &Context) -> io::Result<()> {
        let stopped = self.stopped.lock().unwrap();
        if *stopped {
            return Ok(());
        }
        let width = ctx.width();
        let lines = match catch_unwind(AssertUnwindSafe(|| (self.render)(width))) {
            Ok(lines) => lines,
            Err(payload) => {
                ctx.notify(&format!("{} render failed: {}", self.kind(), panic_message(payload.as_ref())), "error");
                return Ok(());
            }
        };
        call_result_to_io(ctx.call_wire(self.method, Some(surface_args(lines, width)))?)
    }

    fn stop(&self) {
        *self.stopped.lock().unwrap() = true;
    }

    /// Renders again off the message loop, coalescing calls that arrive while a
    /// push is in flight into one more push at the newest width.
    fn refresh(self: &Arc<Self>, ctx: Context) {
        {
            let mut state = self.refresh.lock().unwrap();
            state.0 = true;
            if state.1 {
                return;
            }
            state.1 = true;
        }
        let renderer = self.clone();
        std::thread::spawn(move || {
            loop {
                {
                    let mut state = renderer.refresh.lock().unwrap();
                    if !state.0 {
                        state.1 = false;
                        return;
                    }
                    state.0 = false;
                }
                let _ = renderer.push(&ctx);
            }
        });
    }
}

impl Context {
    /// Retires the renderer installed for `method` and installs `next`.
    fn replace_surface(&self, method: &'static str, next: Option<Arc<SurfaceRenderer>>) {
        let previous = {
            let mut surfaces = self.surfaces.lock().unwrap();
            let previous = match &next {
                Some(renderer) => surfaces.insert(method, renderer.clone()),
                None => surfaces.remove(method),
            };
            drop(surfaces);
            previous
        };
        if let Some(previous) = previous {
            previous.stop();
        }
    }

    fn set_surface(&self, method: &'static str, lines: Option<Vec<String>>) -> io::Result<()> {
        self.replace_surface(method, None);
        let args = match lines {
            Some(lines) => surface_args(lines, self.width()),
            None => serde_json::json!({"clear": true}),
        };
        call_result_to_io(self.call_wire(method, Some(args))?)
    }

    fn set_surface_renderer<F>(&self, method: &'static str, render: Option<F>) -> io::Result<()>
    where
        F: Fn(u32) -> Vec<String> + Send + Sync + 'static,
    {
        let Some(render) = render else {
            return self.set_surface(method, None);
        };
        let renderer = Arc::new(SurfaceRenderer {
            method,
            render: Box::new(render),
            stopped: Mutex::new(false),
            refresh: Mutex::new((false, false)),
        });
        self.replace_surface(method, Some(renderer.clone()));
        renderer.push(self)
    }

    /// Renders every installed footer and header renderer again at the host's width.
    pub(crate) fn refresh_surfaces(&self) {
        let renderers: Vec<_> = self.surfaces.lock().unwrap().values().cloned().collect();
        for renderer in renderers {
            renderer.refresh(self.clone());
        }
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
    /// [`Context::width`] has been updated (a handler that reads it sees that
    /// width or a newer one).
    ///
    /// Upstream Pi installs headers and footers as component factories whose
    /// `render(width)` runs every frame, so they follow a resize with no work
    /// from the extension. Rows sent with [`Self::set_footer`] or
    /// [`Self::set_header`] are painted only at the width they carry, so after
    /// a resize they stay hidden until the extension sends rows for the new
    /// width; this is that trigger. [`Self::set_footer_renderer`] and
    /// [`Self::set_header_renderer`] follow a resize with no handler.
    ///
    /// Handlers run on a worker thread, one at a time in the order the host
    /// sent the widths, never on the thread that reads the host's replies, so
    /// a handler can make a blocking host call such as [`Context::set_footer`].
    /// A slow handler delays the later deliveries, not the extension's other
    /// handlers. A handler that panics does not stop the others. The returned
    /// guard unsubscribes when dropped; a delivery already queued still
    /// reaches the handlers that were subscribed when the width arrived.
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
            request_parent: None,
            tool_call_id: None,
            request_id: String::new(),
            session_name: String::new(),
            cwd: String::new(),
            mode: String::new(),
            shared_width: width,
            flag_defaults: Arc::new(HashMap::new()),
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
            shared_ui: Arc::new(Mutex::new(UiState::default())),
            bus: Arc::new(crate::event_bus::BusRegistry::default()),
            surfaces: Arc::new(Mutex::new(HashMap::new())),
            replacement: false,
        }
    }

    #[test]
    fn retained_context_shares_state_and_releases_with_its_subscription() {
        let (s, q) = subs();
        let ctx = ctx_with(s, q, Arc::new(AtomicU32::new(0)));
        let retained = ctx.clone();
        assert!(Arc::ptr_eq(&ctx.conn, &retained.conn));
        assert!(Arc::ptr_eq(&ctx.shared_session, &retained.shared_session));
        let connection = Arc::downgrade(&ctx.conn);
        ctx.cancel_flag.store(true, Ordering::SeqCst);
        assert!(retained.is_cancelled());
        let guard = ctx.on_width_change(move |_| {
            assert!(retained.is_cancelled());
        });
        drop(ctx);
        assert!(connection.upgrade().is_some());
        drop(guard);
        assert!(connection.upgrade().is_none());
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
            request_parent: None,
            conn: Arc::new(Connection::new(stream)),
            request_id: String::new(),
            tool_call_id: None,
            session_name: String::new(),
            cwd: String::new(),
            mode: String::new(),
            shared_width: Arc::new(AtomicU32::new(0)),
            flag_defaults: Arc::new(HashMap::new()),
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
            shared_ui: Arc::new(Mutex::new(UiState::default())),
            bus: Arc::new(crate::event_bus::BusRegistry::default()),
            surfaces: Arc::new(Mutex::new(HashMap::new())),
            replacement: false,
        })
    }

    // Pi's loader appends a resolver registered after loading; the host learns the extension's new resolver count
    // and asks the late resolver after the loaded ones.
    #[test]
    fn register_tool_renderer_after_loading_reports_the_count_and_runs_after_the_loaded_resolvers() {
        let (extension_stream, host_stream) = UnixStream::pair().unwrap();
        let ctx = context(extension_stream);
        ctx.conn.late_tool_renderers.loaded.store(2, Ordering::SeqCst);
        ctx.register_tool_renderer(|tool, next| if tool == "late" { Some(crate::ToolRendererSet::default()) } else { next() })
            .unwrap();
        let host = Connection::new(host_stream);
        let notify = host.read_envelope().unwrap().notify.unwrap();
        assert_eq!(notify.method, "tool_renderers");
        assert_eq!(notify.args, Some(serde_json::json!({ "count": 3 })));

        let renderers = crate::tool_render::ToolRenderers::default();
        let next = serde_json::json!({ "render_shell": "self" });
        let late = renderers.resolve(&ctx.conn, Some(&serde_json::json!({ "tool": "late", "next": next })));
        assert_eq!(late["use"], "own");
        let other = renderers.resolve(&ctx.conn, Some(&serde_json::json!({ "tool": "other", "next": next })));
        assert_eq!(other, serde_json::json!({ "use": "next" }));
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

        assert!(ctx.conn.complete_call(&Envelope::<serde_json::Value> {
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

    #[test]
    fn register_sprite_sends_the_definition_directly_as_call_args() {
        let (extension_stream, host_stream) = UnixStream::pair().unwrap();
        let ctx = context(extension_stream);
        let definition = SpriteDefinition {
            id: "blue-pig".to_string(),
            name: "Blue PiG".to_string(),
            tagline: "From an extension.".to_string(),
            mascot: vec!["mascot".to_string()],
            palette: HashMap::from([("P".to_string(), "#5B8DEF".to_string())]),
        };
        let caller = {
            let ctx = ctx.clone();
            let definition = definition.clone();
            std::thread::spawn(move || ctx.register_sprite(&definition))
        };
        let host = Connection::new(host_stream);
        let envelope = host.read_envelope().unwrap();
        let call = envelope.call.unwrap();
        assert_eq!(call.method, "ui.registerSprite");
        assert_eq!(call.args, Some(serde_json::to_value(&definition).unwrap()));
        assert!(ctx.conn.complete_call(&Envelope::<serde_json::Value> {
            msg_type: "call_result".to_string(),
            id: envelope.id,
            call_result: Some(CallResultMsg {
                result: None,
                error: Some(crate::protocol::ErrorInfo {
                    code: Some("invalid_sprite".to_string()),
                    message: "invalid sprite definition mascot: must contain exactly 14 rows".to_string(),
                }),
            }),
            ..Default::default()
        }));
        let err = caller.join().unwrap().unwrap_err();
        assert!(err.to_string().contains("invalid sprite definition mascot"), "{err}");
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

#[cfg(test)]
mod tool_and_command_info_tests {
    use super::*;

    // The host answers getAllTools and getCommands with upstream's ToolInfo
    // and SlashCommandInfo objects.
    #[test]
    fn decodes_upstream_tool_and_command_info() {
        let result = serde_json::json!({"tools": [
            {"name": "read", "description": "Read a file", "parameters": {"type": "object"},
             "promptGuidelines": ["Use read."],
             "sourceInfo": {"path": "<builtin:read>", "source": "builtin", "scope": "temporary", "origin": "top-level"}},
            {"name": "probe", "description": "Probe", "parameters": {"type": "object", "properties": {}},
             "sourceInfo": {"path": "/x/probe.ts", "source": "cli", "scope": "temporary", "origin": "top-level"}}
        ]});
        let tools: Vec<ToolInfo> = serde_json::from_value(result["tools"].clone()).unwrap();
        assert_eq!(tools.len(), 2);
        assert_eq!(
            tools[0].prompt_guidelines.as_deref(),
            Some(&["Use read.".to_string()][..])
        );
        assert_eq!(tools[0].source_info.source, "builtin");
        assert_eq!(tools[1].prompt_guidelines, None);
        assert_eq!(
            tools[1].parameters,
            serde_json::json!({"type": "object", "properties": {}})
        );
        assert_eq!(tools[1].source_info.path, "/x/probe.ts");

        let result = serde_json::json!({"commands": [
            {"name": "probe", "description": "Probe command", "source": "extension",
             "sourceInfo": {"path": "/x/probe.ts", "source": "cli", "scope": "temporary", "origin": "top-level"}},
            {"name": "skill:review", "source": "skill",
             "sourceInfo": {"path": "/s/SKILL.md", "source": "local", "scope": "user", "origin": "top-level", "baseDir": "/s"}}
        ]});
        let commands: Vec<CommandInfo> = serde_json::from_value(result["commands"].clone()).unwrap();
        assert_eq!(commands.len(), 2);
        assert_eq!(commands[0].source, "extension");
        assert_eq!(commands[1].description, "");
        assert_eq!(commands[1].source_info.base_dir.as_deref(), Some("/s"));
    }
}

#[cfg(test)]
mod sdk_surface_call_tests {
    use super::*;
    use crate::protocol::{CallMsg, CallResultMsg, Envelope, ErrorInfo};
    use serde_json::json;
    use std::os::unix::net::UnixStream;
    use std::sync::mpsc;

    fn context(stream: UnixStream, request_id: &str) -> Arc<Context> {
        Arc::new(Context {
            conn: Arc::new(Connection::new(stream)),
            request_id: request_id.to_string(),
            request_parent: None,
            flag_defaults: Arc::new(HashMap::new()),
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
            shared_ui: Arc::new(Mutex::new(UiState::default())),
            bus: Arc::new(crate::event_bus::BusRegistry::default()),
            surfaces: Arc::new(Mutex::new(HashMap::new())),
            replacement: false,
        })
    }

    /// Reads the next call frame the extension wrote, skipping request_state.
    fn next_call(host: &Connection) -> (Option<String>, CallMsg) {
        loop {
            let env = host.read_envelope().unwrap();
            if env.msg_type == "call" {
                return (env.id, env.call.unwrap());
            }
            assert_eq!(env.msg_type, "request_state", "unexpected frame");
        }
    }

    fn reply(ctx: &Context, id: Option<String>, result: CallResultMsg) {
        assert!(ctx.conn.complete_call(&Envelope {
            msg_type: "call_result".to_string(),
            id,
            call_result: Some(result),
            ..Default::default()
        }));
    }

    fn ok(result: Option<serde_json::Value>) -> CallResultMsg {
        CallResultMsg {
            result,
            error: None,
        }
    }

    /// Runs `f` against a context whose host answers its one call with
    /// `answer`, returning the call the host saw and `f`'s result.
    fn roundtrip<T: Send + 'static>(
        f: impl FnOnce(&Context) -> T + Send + 'static,
        answer: CallResultMsg,
    ) -> (CallMsg, T) {
        let (ext_stream, host_stream) = UnixStream::pair().unwrap();
        let ctx = context(ext_stream, "req-1");
        let caller = {
            let ctx = ctx.clone();
            std::thread::spawn(move || f(&ctx))
        };
        let host = Connection::new(host_stream);
        let (id, call) = next_call(&host);
        reply(&ctx, id, answer);
        (call, caller.join().unwrap())
    }

    #[test]
    fn send_custom_message_sends_content_blocks_details_and_options() {
        let (call, result) = roundtrip(
            |ctx| {
                ctx.send_custom_message(
                    &CustomMessage {
                        custom_type: "note".to_string(),
                        content: json!([{"type": "text", "text": "hi"}]),
                        display: true,
                        details: Some(json!({"k": 1})),
                    },
                    &SendMessageOptions {
                        trigger_turn: Some(false),
                        deliver_as: Some("nextTurn".to_string()),
                    },
                )
            },
            ok(None),
        );
        result.unwrap();
        assert_eq!(call.method, "sendMessage");
        assert_eq!(
            call.args.unwrap(),
            json!({
                "message": {"customType": "note", "content": [{"type": "text", "text": "hi"}],
                            "display": true, "details": {"k": 1}},
                "options": {"triggerTurn": false, "deliverAs": "nextTurn"}
            })
        );
    }

    #[test]
    fn exec_with_options_sends_options_and_decodes_code_and_killed() {
        let (call, result) = roundtrip(
            |ctx| {
                ctx.exec_with_options(
                    "sleep",
                    &["10"],
                    &ExecOptions {
                        timeout: Some(250.0),
                        cwd: Some("/work".to_string()),
                    },
                )
            },
            ok(Some(
                json!({"stdout": "out", "stderr": "err", "code": 137, "killed": true}),
            )),
        );
        assert_eq!(call.method, "exec");
        assert_eq!(
            call.args.unwrap(),
            json!({"command": "sleep", "args": ["10"], "options": {"timeout": 250.0, "cwd": "/work"}})
        );
        let result = result.unwrap();
        assert_eq!(result.stdout, "out");
        assert_eq!(result.stderr, "err");
        assert_eq!(result.exit_code, 137);
        assert!(result.killed);
    }

    #[test]
    fn dialogs_with_options_send_the_timeout() {
        let opts = DialogOptions {
            timeout: Some(1500.0),
        };
        let (call, result) = roundtrip(
            move |ctx| ctx.select_with_options("Pick", &["a", "b"], &opts),
            ok(Some(json!({"selected": "b", "ok": true}))),
        );
        assert_eq!(call.method, "ui.select");
        assert_eq!(
            call.args.unwrap(),
            json!({"title": "Pick", "options": ["a", "b"], "opts": {"timeout": 1500.0}})
        );
        assert_eq!(result.unwrap(), ("b".to_string(), true));

        let (call, result) = roundtrip(
            move |ctx| ctx.confirm_with_options("Sure?", "Really", &opts),
            ok(Some(json!({"confirmed": true}))),
        );
        assert_eq!(call.method, "ui.confirm");
        assert_eq!(
            call.args.unwrap(),
            json!({"title": "Sure?", "message": "Really", "opts": {"timeout": 1500.0}})
        );
        assert!(result.unwrap());

        let (call, result) = roundtrip(
            move |ctx| ctx.input_with_options("Name", "type", &opts),
            ok(Some(json!({"text": "", "ok": false}))),
        );
        assert_eq!(call.method, "ui.input");
        assert_eq!(
            call.args.unwrap(),
            json!({"title": "Name", "placeholder": "type", "opts": {"timeout": 1500.0}})
        );
        assert_eq!(result.unwrap(), (String::new(), false));
    }

    #[test]
    fn set_footer_and_header_send_lines() {
        let (call, result) = roundtrip(
            |ctx| ctx.set_footer(vec!["f1".to_string(), "f2".to_string()]),
            ok(None),
        );
        result.unwrap();
        assert_eq!(call.method, "ui.setFooter");
        assert_eq!(call.args.unwrap(), json!({"lines": ["f1", "f2"]}));

        let (call, result) = roundtrip(|ctx| ctx.set_header(vec!["h".to_string()]), ok(None));
        result.unwrap();
        assert_eq!(call.method, "ui.setHeader");
        assert_eq!(call.args.unwrap(), json!({"lines": ["h"]}));
    }

    fn compact_roundtrip(answer: CallResultMsg) -> (CallMsg, Result<serde_json::Value, String>) {
        let (ext_stream, host_stream) = UnixStream::pair().unwrap();
        let ctx = context(ext_stream, "req-1");
        let (tx, rx) = mpsc::channel();
        let err_tx = tx.clone();
        ctx.compact_with_options(CompactOptions {
            custom_instructions: Some("focus".to_string()),
            on_complete: Some(Box::new(move |result| {
                let _ = tx.send(Ok(result));
            })),
            on_error: Some(Box::new(move |message| {
                let _ = err_tx.send(Err(message));
            })),
        });
        let host = Connection::new(host_stream);
        let (id, call) = next_call(&host);
        reply(&ctx, id, answer);
        let outcome = rx.recv_timeout(Duration::from_secs(5)).unwrap();
        (call, outcome)
    }

    #[test]
    fn compact_with_options_awaits_completion_outside_the_request() {
        let (call, outcome) = compact_roundtrip(ok(Some(
            json!({"summary": "s", "firstKeptEntryId": "e1", "tokensBefore": 100}),
        )));
        assert_eq!(call.method, "compact");
        assert_eq!(call.parent_request_id, None);
        assert_eq!(
            call.args.unwrap(),
            json!({"customInstructions": "focus", "awaitCompletion": true})
        );
        assert_eq!(
            outcome.unwrap(),
            json!({"summary": "s", "firstKeptEntryId": "e1", "tokensBefore": 100})
        );

        let (call, outcome) = compact_roundtrip(CallResultMsg {
            result: None,
            error: Some(ErrorInfo {
                code: None,
                message: "Nothing to compact".to_string(),
            }),
        });
        assert_eq!(call.parent_request_id, None);
        assert_eq!(outcome.unwrap_err(), "Nothing to compact");
    }

    // The Node runtime rejects a failed host call with the host's message and
    // prefixes "code: " only when the host names a code, so pi.exec rejects
    // with "spawn EINVAL", not "call_failed: spawn EINVAL".
    #[test]
    fn host_errors_keep_the_message_without_a_code() {
        fn bare() -> CallResultMsg {
            CallResultMsg {
                result: None,
                error: Some(ErrorInfo { code: None, message: "spawn EINVAL".to_string() }),
            }
        }
        let Err(message) = roundtrip(|c| c.exec("tool.cmd", &[]), bare()).1 else { panic!("exec resolved") };
        assert_eq!(message, "spawn EINVAL");
        let Err(message) = roundtrip(|c| c.exec("tool.cmd", &[]), failed()).1 else { panic!("exec resolved") };
        assert_eq!(message, "host_failed: boom");
        let Err(error) = roundtrip(|c| c.call_host("x", None), bare()).1 else { panic!("call resolved") };
        assert_eq!(error.to_string(), "spawn EINVAL");
        let Err(error) = roundtrip(|c| c.get_editor_text(), bare()).1 else { panic!("call resolved") };
        assert_eq!(error.to_string(), "spawn EINVAL");
    }
    fn failed() -> CallResultMsg {
        CallResultMsg {
            result: None,
            error: Some(ErrorInfo { code: Some("host_failed".to_string()), message: "boom".to_string() }),
        }
    }

    // Pi's getters return undefined for absent state and throw for a failed
    // host call. The Rust getters return `None` for the first and `Err` for the
    // second, never an empty value, a cached name or a default.
    #[test]
    fn getters_distinguish_absent_empty_and_failure() {
        let (call, name) = roundtrip(|c| c.get_session_name(), ok(Some(json!({"name": "named"}))));
        assert_eq!(call.method, "getSessionName");
        assert_eq!(name.unwrap().as_deref(), Some("named"));
        assert_eq!(roundtrip(|c| c.get_session_name(), ok(Some(json!({"name": ""})))).1.unwrap(), None);
        assert_eq!(roundtrip(|c| c.get_session_file(), ok(Some(json!({"sessionFile": ""})))).1.unwrap(), None);
        assert_eq!(roundtrip(|c| c.get_leaf_id(), ok(Some(json!({"leafId": null})))).1.unwrap(), None);
        assert!(roundtrip(|c| c.get_context_usage(), ok(Some(json!(null)))).1.unwrap().is_none());
        assert!(roundtrip(|c| c.get_model_info(), ok(Some(json!({})))).1.unwrap().is_none());
        assert_eq!(roundtrip(|c| c.get_editor_text(), ok(Some(json!({"text": ""})))).1.unwrap().to_string().unwrap(), "");
        assert_eq!(roundtrip(|c| c.get_flag("f"), ok(Some(json!({"value": false})))).1.unwrap(), Some(json!(false)));
        assert_eq!(roundtrip(|c| c.get_flag("f"), ok(Some(json!({})))).1.unwrap(), None);
        assert_eq!(roundtrip(|c| c.is_idle(), ok(Some(json!({"idle": false})))).1.unwrap(), false);

        assert!(roundtrip(|c| c.get_session_name(), failed()).1.unwrap_err().to_string().contains("boom"));
        assert!(roundtrip(|c| c.get_editor_text(), failed()).1.is_err());
        assert!(roundtrip(|c| c.get_flag("f"), failed()).1.is_err());
        assert!(roundtrip(|c| c.get_active_tools(), failed()).1.is_err());
        assert!(roundtrip(|c| c.get_context_usage(), failed()).1.is_err());
        assert!(roundtrip(|c| c.is_idle(), failed()).1.is_err());
        assert!(roundtrip(|c| c.is_project_trusted(), failed()).1.is_err());
        assert!(roundtrip(|c| c.get_thinking_level(), ok(Some(json!({"unrelated": 1})))).1.is_err());
    }
    // A failed session-log subscription is returned by every mirror read, not hidden as an empty or partial mirror.
    #[test]
    fn session_log_getters_report_subscription_failure() {
        let (ext_stream, host_stream) = UnixStream::pair().unwrap();
        let ctx = context(ext_stream, "req-1");
        let caller = {
            let ctx = ctx.clone();
            std::thread::spawn(move || (ctx.get_entries().map(|entries| entries.len()), ctx.get_branch().map(|branch| branch.len())))
        };
        let host = Connection::new(host_stream);
        let (id, call) = next_call(&host);
        assert_eq!(call.method, "getSessionFile", "the seed read reports its own failure instead of an empty path");
        reply(&ctx, id, failed());
        let (entries, branch) = caller.join().unwrap();
        assert!(entries.unwrap_err().to_string().contains("boom"));
        assert!(branch.unwrap_err().to_string().contains("boom"), "the second read must report the same failure without a second call");
    }
}

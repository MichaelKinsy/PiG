//! Child agent sessions: Pi's `createAgentSession` (core/sdk.ts) for a Rust extension.
//!
//! The host owns each session for this extension's connection. The extension prompts it, listens to its events, aborts it and disposes it; the host runs
//! the agent loop and calls back into the extension for the tools it serves (`custom_tools`). The Node runtime serves Pi's own `createAgentSession` in the
//! extension process; this is the same call for the native SDKs, with the same observable behavior (`TestChildAgentSessionsAcrossSDKs`).
//!
//! Async contract (Pi's promises, kept as blocking calls):
//!
//! * [`Context::create_agent_session`](crate::Context::create_agent_session) is awaited: resources are loaded and the model resolved before it returns.
//! * [`AgentSession::prompt`] returns when the run has settled, after every listener has run through the run's last event. A rejected prompt is its error.
//!   After [`AgentSession::abort`] the run settles and `prompt` returns `Ok`.
//! * [`AgentSession::abort`] returns once the session is idle. [`AgentSession::dispose`] is synchronous and idempotent.
//! * Listeners run in event order on one thread of the session, never on the connection's reader, so a listener may call the host or the session.
//! * The session ends with the connection: its run is aborted and no listener runs afterwards.

use std::collections::HashMap;
use std::io;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Condvar, Mutex};

use serde_json::{json, Value};

use crate::extension::ToolDefinition;
use crate::protocol::Connection;

/// The DefaultResourceLoader options of Pi (resource-loader.ts) that select resources.
#[derive(Default, Clone)]
pub struct ResourceOptions {
    pub no_extensions: bool,
    pub no_skills: bool,
    pub no_prompt_templates: bool,
    pub no_themes: bool,
    pub no_context_files: bool,
    /// Loaded after the resolved resources.
    pub additional_extension_paths: Option<Vec<String>>,
    pub additional_skill_paths: Option<Vec<String>>,
    pub additional_prompt_template_paths: Option<Vec<String>>,
    /// Keep only the extensions whose path contains one of the names, compared without case (Pi's `extensionsOverride`). `None` keeps all, `Some([])` none.
    pub extension_names: Option<Vec<String>>,
    /// Keep only the skills named exactly by one of the names (Pi's `skillsOverride`). `None` keeps all, `Some([])` none.
    pub skill_names: Option<Vec<String>>,
    /// Replace SYSTEM.md / APPEND_SYSTEM.md discovery; each entry is text or the path of a file holding it. `None` discovers.
    pub system_prompt: Option<String>,
    pub append_system_prompt: Option<Vec<String>>,
}

/// The members of Pi's `CreateAgentSessionOptions` (core/sdk.ts) that cross the process boundary. `None` is Pi's `undefined`.
///
/// The child shares the model runtime of the host's Session (Pi's `modelRuntime: ctx.modelRegistry.runtime`), so every provider the extensions registered
/// is available to it.
#[derive(Default)]
pub struct CreateAgentSessionOptions {
    /// Working directory for project-local discovery. `None` is the host's.
    pub cwd: Option<String>,
    /// Global config directory. `None` is the host's.
    pub agent_dir: Option<String>,
    /// A model object such as `ctx.model_registry().find(..)` returns; resolve a name or pattern through the registry first, as Pi's option takes a Model. `None` is Pi's default: the settings' default, else the first available model.
    pub model: Option<Value>,
    /// The requested level, clamped to the model.
    pub thinking_level: Option<String>,
    /// Allowlist of tool names or `*` patterns, or a list of only `+name` and `-name` entries. `None` is Pi's undefined; `Some([])` allows nothing.
    pub tools: Option<Vec<String>>,
    /// Denylist applied after `tools`.
    pub exclude_tools: Option<Vec<String>>,
    /// `"all"` (no tools) or `"builtin"` (no default built-in tools; extension and custom tools stay enabled).
    pub no_tools: Option<String>,
    /// Tools this extension serves to the session. `execute`, `prepare_arguments`, `execution_mode` and the model-facing metadata apply; renderers,
    /// `prepare_loadout` and `reaches_tools` have no meaning in a child.
    pub custom_tools: Vec<ToolDefinition>,
    /// In-memory settings, Pi's `SettingsManager.inMemory(settings)`: the session reads no settings file, so `{"compaction": {"enabled": false}}` turns
    /// compaction off. `None` is the settings files of `cwd` and `agent_dir`.
    pub settings: Option<Value>,
    /// `SessionManager.inMemory(cwd)`: the session log is never written. `false` is `SessionManager.create(cwd)`.
    pub in_memory: bool,
    /// What the session's resource loader loads. `None` is Pi's default loader.
    pub resources: Option<ResourceOptions>,
    /// Pi's `sessionManager`, `settingsManager`, `resourceLoader` and `modelRuntime` options, as the `handle()` of an object this extension built (`SessionManager`, `SettingsManager`, `DefaultResourceLoader`, `ModelRuntime`). The session uses the very objects. Each replaces the option that builds its own (`in_memory`, `settings`, `resources`; the host's model runtime), and naming both is an error. Keep the object alive until the session is created.
    pub session_manager: Option<String>,
    pub settings_manager: Option<String>,
    pub resource_loader: Option<String>,
    pub model_runtime: Option<String>,
    /// Pi's `scopedModels`: a JSON list of `{"model": <model object>, "thinkingLevel"?: ...}` the session cycles through.
    pub scoped_models: Option<Value>,
}

fn resources_wire(resources: &ResourceOptions) -> Value {
    let mut wire = serde_json::Map::new();
    for (flag, key) in [
        (resources.no_extensions, "noExtensions"), (resources.no_skills, "noSkills"), (resources.no_prompt_templates, "noPromptTemplates"),
        (resources.no_themes, "noThemes"), (resources.no_context_files, "noContextFiles"),
    ] {
        if flag {
            wire.insert(key.into(), Value::Bool(true));
        }
    }
    for (value, key) in [
        (&resources.additional_extension_paths, "additionalExtensionPaths"), (&resources.additional_skill_paths, "additionalSkillPaths"),
        (&resources.additional_prompt_template_paths, "additionalPromptTemplatePaths"), (&resources.extension_names, "extensionNames"),
        (&resources.skill_names, "skillNames"), (&resources.append_system_prompt, "appendSystemPrompt"),
    ] {
        if let Some(list) = value {
            wire.insert(key.into(), json!(list));
        }
    }
    if let Some(prompt) = &resources.system_prompt {
        wire.insert("systemPrompt".into(), json!(prompt));
    }
    Value::Object(wire)
}

type Listener = Arc<dyn Fn(&Value) + Send + Sync>;

/// The connection's live child sessions.
#[derive(Default)]
pub(crate) struct ChildSessions {
    state: Mutex<ChildTable>,
    next: AtomicU64,
}

#[derive(Default)]
struct ChildTable {
    by_id: HashMap<String, Arc<SessionShared>>,
    closed: bool,
}

impl ChildSessions {
    fn add(&self, session: &Arc<SessionShared>) -> bool {
        let mut table = self.state.lock().unwrap();
        if table.closed {
            return false;
        }
        table.by_id.insert(session.id.clone(), session.clone());
        true
    }

    fn remove(&self, id: &str) {
        self.state.lock().unwrap().by_id.remove(id);
    }

    pub(crate) fn lookup(&self, id: &str) -> Option<Arc<SessionShared>> {
        self.state.lock().unwrap().by_id.get(id).cloned()
    }

    /// Ends every session without telling the host: it disposes them as the connection closes.
    pub(crate) fn close_all(&self) {
        let sessions: Vec<_> = {
            let mut table = self.state.lock().unwrap();
            table.closed = true;
            table.by_id.drain().map(|(_, session)| session).collect()
        };
        for session in sessions {
            session.stop();
        }
    }

    /// Queues one `childSession.event` notification on its session. The reader calls it, so it only queues.
    pub(crate) fn deliver(&self, args: &Value) {
        let Some(id) = args["id"].as_str() else { return };
        if let Some(session) = self.lookup(id) {
            let seq = args["seq"].as_u64().unwrap_or(0);
            let event = match args.get("stream") {
                None | Some(Value::Null) => args["event"].clone(),
                Some(stream) => session.apply_update(stream, args.get("event")),
            };
            session.enqueue(seq, event);
        }
    }
}

/// What the connection's reader and the session's methods share.
pub(crate) struct SessionShared {
    id: String,
    /// The custom tools by name; the host's `tool_call` and `tool_prepare_arguments` requests name the session.
    pub(crate) tools: HashMap<String, Arc<ToolDefinition>>,
    queue: Mutex<Delivery>,
    /// The `message_update` payload the host last sent, with its number; only the connection's reader touches it.
    replica: Mutex<Option<(u64, Value)>>,
    changed: Condvar,
    disposed: AtomicBool,
}

#[derive(Default)]
struct Delivery {
    listeners: Vec<(u64, Listener)>,
    next_listener: u64,
    pending: std::collections::VecDeque<(u64, Value)>,
    delivered: u64,
    stopped: bool,
    running: bool,
}

impl SessionShared {
    fn stop(&self) {
        self.disposed.store(true, Ordering::SeqCst);
        let mut delivery = self.queue.lock().unwrap();
        delivery.stopped = true;
        delivery.listeners.clear();
        delivery.pending.clear();
        self.changed.notify_all();
    }

    /// The full `message_update` event the stream member produces (`EventStream` in the host's protocol.go), or `Null` when it cannot be: no listener sees the event, but its number still counts.
    fn apply_update(&self, stream: &Value, keyframe: Option<&Value>) -> Value {
        let mut replica = self.replica.lock().unwrap();
        let produced = match keyframe {
            Some(keyframe) if !keyframe.is_null() => Ok(keyframe.clone()),
            _ => {
                let base = stream["base"].as_u64().unwrap_or(0);
                match replica.take() {
                    Some((seq, mut root)) if seq == base => crate::event_stream::apply_ops(&mut root, &stream["ops"]).map(|()| root),
                    _ => Err(format!("message_update delta applies to payload {base}, the replica holds a different one")),
                }
            }
        };
        match produced {
            Ok(root) => {
                *replica = Some((stream["seq"].as_u64().unwrap_or(0), root.clone()));
                root
            }
            Err(message) => {
                eprintln!("pig: child session {}: {message}", self.id);
                Value::Null
            }
        }
    }

    fn enqueue(self: &Arc<Self>, seq: u64, event: Value) {
        let start = {
            let mut delivery = self.queue.lock().unwrap();
            if delivery.stopped {
                return;
            }
            delivery.pending.push_back((seq, event));
            !std::mem::replace(&mut delivery.running, true)
        };
        if start {
            let session = self.clone();
            std::thread::spawn(move || session.deliver_loop());
        }
    }

    fn deliver_loop(&self) {
        loop {
            let (seq, event, listeners) = {
                let mut delivery = self.queue.lock().unwrap();
                match delivery.pending.pop_front() {
                    Some((seq, event)) if !delivery.stopped => (seq, event, delivery.listeners.iter().map(|(_, l)| l.clone()).collect::<Vec<_>>()),
                    _ => {
                        delivery.running = false;
                        return;
                    }
                }
            };
            for listener in listeners.into_iter().filter(|_| !event.is_null()) {
                // A panicking listener does not stop Pi's emission.
                let _ = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| listener(&event)));
            }
            let mut delivery = self.queue.lock().unwrap();
            delivery.delivered = seq;
            self.changed.notify_all();
        }
    }

    fn wait_delivered(&self, seq: u64) {
        let mut delivery = self.queue.lock().unwrap();
        while delivery.delivered < seq && !delivery.stopped {
            delivery = self.changed.wait(delivery).unwrap();
        }
    }
}

/// A child agent session: Pi's `AgentSession` as an extension holds it. Create it with
/// [`Context::create_agent_session`](crate::Context::create_agent_session).
pub struct AgentSession {
    conn: Arc<Connection>,
    shared: Arc<SessionShared>,
    /// `session.sessionId`.
    pub session_id: String,
    /// `session.sessionFile`; `None` for an in-memory session.
    pub session_file: Option<String>,
    /// What the session held when it was created. Read the current values with [`AgentSession::state`].
    pub model: Value,
    pub thinking_level: String,
    pub active_tools: Vec<String>,
    pub system_prompt: String,
    /// `CreateAgentSessionResult.modelFallbackMessage`.
    pub model_fallback_message: Option<String>,
    /// The errors of `CreateAgentSessionResult.extensionsResult.errors`, as `"path: error"`.
    pub extension_errors: Vec<String>,
}

/// Options of [`AgentSession::prompt_with`] (Pi's `PromptOptions`).
#[derive(Default)]
pub struct PromptOptions {
    /// `None` is true.
    pub expand_prompt_templates: Option<bool>,
    /// Images as `{"data", "mimeType"}` objects.
    pub images: Vec<Value>,
    /// `"steer"` or `"followUp"`; a prompt to a running session needs it.
    pub streaming_behavior: Option<String>,
    /// `"interactive"` (default), `"rpc"` or `"extension"`.
    pub source: Option<String>,
}

fn call(conn: &Connection, method: &str, args: Value) -> io::Result<Option<Value>> {
    let result = conn.call(method, Some(args))?;
    if let Some(err) = result.error {
        return Err(io::Error::new(io::ErrorKind::Other, crate::context::host_error_message(err)));
    }
    Ok(result.result)
}

pub(crate) fn create(conn: &Arc<Connection>, call_host: impl Fn(&str, Option<Value>) -> io::Result<Option<Value>>, options: CreateAgentSessionOptions) -> io::Result<AgentSession> {
    let id = format!("c{}", conn.children.next.fetch_add(1, Ordering::SeqCst) + 1);
    let mut args = serde_json::Map::new();
    args.insert("id".into(), json!(id));
    for (value, key) in [(&options.cwd, "cwd"), (&options.agent_dir, "agentDir"), (&options.thinking_level, "thinkingLevel"), (&options.no_tools, "noTools")] {
        if let Some(value) = value {
            args.insert(key.into(), json!(value));
        }
    }
    if let Some(model) = &options.model {
        args.insert("model".into(), model.clone());
    }
    for (value, key) in [(&options.tools, "tools"), (&options.exclude_tools, "excludeTools")] {
        if let Some(list) = value {
            args.insert(key.into(), json!(list));
        }
    }
    if let Some(settings) = &options.settings {
        args.insert("settings".into(), settings.clone());
    }
    if options.in_memory {
        args.insert("inMemory".into(), Value::Bool(true));
    }
    for (value, key) in [(&options.session_manager, "sessionManager"), (&options.settings_manager, "settingsManager"), (&options.resource_loader, "resourceLoader"), (&options.model_runtime, "modelRuntime")] {
        if let Some(handle) = value {
            args.insert(key.into(), json!(handle));
        }
    }
    if let Some(scoped) = &options.scoped_models {
        args.insert("scopedModels".into(), scoped.clone());
    }
    if let Some(resources) = &options.resources {
        let wire = resources_wire(resources);
        if wire.as_object().is_some_and(|object| !object.is_empty()) {
            args.insert("resources".into(), wire);
        }
    }
    let mut declarations = Vec::new();
    let mut tools = HashMap::new();
    for definition in options.custom_tools {
        declarations.push(crate::context::tool_declaration(&definition)?);
        tools.insert(definition.name.clone(), Arc::new(definition));
    }
    if !declarations.is_empty() {
        args.insert("customTools".into(), Value::Array(declarations));
    }
    let shared = Arc::new(SessionShared { id: id.clone(), tools, queue: Mutex::new(Delivery::default()), replica: Mutex::new(None), changed: Condvar::new(), disposed: AtomicBool::new(false) });
    // The tools are served from the moment the host knows the session, which can be before the call returns.
    if !conn.children.add(&shared) {
        return Err(io::Error::new(io::ErrorKind::BrokenPipe, "Extension connection is closed"));
    }
    let info = match call_host("childSession.create", Some(Value::Object(args))) {
        Ok(info) => info.unwrap_or(Value::Null),
        Err(error) => {
            conn.children.remove(&id);
            return Err(error);
        }
    };
    let text = |key: &str| info[key].as_str().unwrap_or("").to_string();
    Ok(AgentSession {
        conn: conn.clone(),
        shared,
        session_id: text("sessionId"),
        session_file: info["sessionFile"].as_str().filter(|file| !file.is_empty()).map(str::to_string),
        model: info["model"].clone(),
        thinking_level: text("thinkingLevel"),
        active_tools: info["activeTools"].as_array().map(|tools| tools.iter().filter_map(|t| t.as_str().map(str::to_string)).collect()).unwrap_or_default(),
        system_prompt: text("systemPrompt"),
        model_fallback_message: info["modelFallbackMessage"].as_str().filter(|m| !m.is_empty()).map(str::to_string),
        extension_errors: info["extensionErrors"].as_array().map(|errors| errors.iter().filter_map(|e| e.as_str().map(str::to_string)).collect()).unwrap_or_default(),
    })
}

impl AgentSession {
    /// A host call that belongs to the session, not to the request that happens to use it: the session outlives the command that created it.
    fn call(&self, method: &str, mut args: Value) -> io::Result<Option<Value>> {
        args["id"] = json!(self.shared.id);
        call(&self.conn, method, args)
    }

    /// `AgentSession.prompt(text)`: run the agent and return when the run has settled, after the listeners have seen its last event.
    pub fn prompt(&self, text: &str) -> io::Result<()> {
        self.prompt_with(text, PromptOptions::default())
    }

    /// `AgentSession.prompt(text, options)`.
    pub fn prompt_with(&self, text: &str, options: PromptOptions) -> io::Result<()> {
        let mut args = json!({"text": text});
        if !options.images.is_empty() {
            args["images"] = Value::Array(options.images);
        }
        if let Some(expand) = options.expand_prompt_templates {
            args["expandPromptTemplates"] = json!(expand);
        }
        if let Some(behavior) = options.streaming_behavior {
            args["streamingBehavior"] = json!(behavior);
        }
        if let Some(source) = options.source {
            args["source"] = json!(source);
        }
        let result = self.call("childSession.prompt", args)?.unwrap_or(Value::Null);
        self.shared.wait_delivered(result["events"].as_u64().unwrap_or(0));
        Ok(())
    }

    /// `AgentSession.steer`: queue `text` for the running session after the current turn's tool calls, before its next model call.
    pub fn steer(&self, text: &str) -> io::Result<()> {
        self.call("childSession.queue", json!({"behavior": "steer", "text": text})).map(|_| ())
    }

    /// `AgentSession.followUp`: queue `text` for after the run's current work finishes.
    pub fn follow_up(&self, text: &str) -> io::Result<()> {
        self.call("childSession.queue", json!({"behavior": "followUp", "text": text})).map(|_| ())
    }

    /// `AgentSession.abort`: abort the run and return once the session is idle. An aborted run settles normally, so its `prompt` returns `Ok`.
    pub fn abort(&self) -> io::Result<()> {
        self.call("childSession.abort", json!({})).map(|_| ())
    }

    /// `AgentSession.bindExtensions({})`: the extensions the session's resources loaded start, headless, and receive `session_start`.
    pub fn bind_extensions(&self) -> io::Result<()> {
        self.call("childSession.bindExtensions", json!({})).map(|_| ())
    }

    /// The session's current state: `sessionId`, `sessionFile`, `model`, `isStreaming`, `isIdle`, `thinkingLevel`, `activeTools`, `systemPrompt`,
    /// `pendingMessageCount` and `lastAssistantText` (absent when there is none).
    pub fn state(&self) -> io::Result<Value> {
        Ok(self.call("childSession.state", json!({}))?.unwrap_or(Value::Null))
    }

    /// `session.messages`: the transcript the session holds, in Pi's message shape.
    pub fn messages(&self) -> io::Result<Vec<Value>> {
        Ok(self.call("childSession.messages", json!({}))?.and_then(|messages| messages.as_array().cloned()).unwrap_or_default())
    }

    /// `AgentSession.subscribe`: `listener` receives the session's events (Pi's `AgentSessionEvent` as JSON) in order until the returned
    /// [`SessionSubscription`] unsubscribes it, the session is disposed or the connection ends.
    pub fn subscribe(&self, listener: impl Fn(&Value) + Send + Sync + 'static) -> SessionSubscription {
        let mut delivery = self.shared.queue.lock().unwrap();
        delivery.next_listener += 1;
        let key = delivery.next_listener;
        delivery.listeners.push((key, Arc::new(listener)));
        SessionSubscription { shared: self.shared.clone(), key }
    }

    /// `AgentSession.dispose`: abort the run, release the session in the host and remove its listeners. Idempotent.
    pub fn dispose(&self) {
        if self.shared.disposed.swap(true, Ordering::SeqCst) {
            return;
        }
        self.shared.stop();
        if let Err(error) = self.call("childSession.dispose", json!({})) {
            eprintln!("pig: dispose of child session {} failed: {error}", self.shared.id);
        }
    }
}

/// The handle [`AgentSession::subscribe`] returns.
pub struct SessionSubscription {
    shared: Arc<SessionShared>,
    key: u64,
}

impl SessionSubscription {
    /// Removes the listener; it takes effect before the next event.
    pub fn unsubscribe(&self) {
        self.shared.queue.lock().unwrap().listeners.retain(|(key, _)| *key != self.key);
    }
}

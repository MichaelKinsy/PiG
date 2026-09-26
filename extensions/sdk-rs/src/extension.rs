//! Extension builder and runtime.

use crate::context::{Context, ModelStreams, RemoteComponents, TerminalInputSubs};
use crate::oauth::{OAuthLoginCallbacks, OAuthProvider, ProviderOAuthConfig};
use crate::protocol::*;
use crate::tool_render::{
    ToolRenderContext, ToolRenderResult, ToolRenderResultOptions, ToolRenderShell, ToolRenderers,
};
use crate::transport::UnixStream;
use serde_json::Value;
use std::collections::HashMap;
use std::io;
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::atomic::{AtomicBool, AtomicU32, AtomicU64, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::thread;
use std::time::Duration;

/// Result from a tool handler.
pub enum ToolResult {
    /// Successful text result.
    Text(String),
    /// Successful JSON result.
    Json(Value),
    /// Error result (visible to LLM as tool error).
    Error(String),
}

impl ToolResult {
    pub fn text(s: impl Into<String>) -> Self {
        Self::Text(s.into())
    }
    pub fn json(v: Value) -> Self {
        Self::Json(v)
    }
    pub fn error(s: impl Into<String>) -> Self {
        Self::Error(s.into())
    }
}

/// Result from a command handler.
pub enum CommandResult {
    Ok,
    Error(String),
}

/// Type alias for tool argument preparation functions.
pub type ToolPrepareArguments = Box<dyn Fn(Value) -> Result<Value, String> + Send + Sync>;

/// Type alias for tool handler functions.
pub type ToolHandler = Box<dyn Fn(&Context, Value) -> ToolResult + Send + Sync>;

/// Type alias for command handler functions.
pub type CommandHandler = Box<dyn Fn(&Context, &str) -> CommandResult + Send + Sync>;

/// Type alias for event handler functions.
/// Errors are returned to the host as request failures.
pub type EventHandler = Box<dyn Fn(&Context, &mut Value) -> Result<Option<Value>, String> + Send + Sync>;

#[derive(Debug, Clone, Copy, serde::Serialize)]
#[serde(rename_all = "lowercase")]
pub enum ProjectTrustDecision {
    Yes,
    No,
    Undecided,
}

#[derive(Debug, Clone, serde::Serialize)]
pub struct ProjectTrustResult {
    pub trusted: ProjectTrustDecision,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub remember: Option<bool>,
}

/// Type alias for keyboard shortcut handlers.
pub type ShortcutHandler = Box<dyn Fn(&Context) -> CommandResult + Send + Sync>;

/// Options supplied to custom-message renderers.
#[derive(Debug, Clone, Default, serde::Serialize, serde::Deserialize)]
pub struct MessageRenderOptions {
    #[serde(default)]
    pub expanded: bool,
    /// Horizontal padding configured by the outputPad setting.
    #[serde(rename = "outputPad")]
    pub output_pad: u32,
}

/// Type alias for custom message renderers.
pub type RendererHandler = Box<
    dyn Fn(&Context, Value, MessageRenderOptions, u32) -> Result<Vec<String>, String> + Send + Sync,
>;

/// Options supplied to custom session-entry renderers.
#[derive(Debug, Clone, Default, serde::Serialize, serde::Deserialize)]
pub struct EntryRenderOptions {
    #[serde(default)]
    pub expanded: bool,
}

/// Type alias for custom session-entry renderers.
pub type EntryRendererHandler = Box<
    dyn Fn(&Context, Value, EntryRenderOptions, u32) -> Result<Vec<String>, String> + Send + Sync,
>;

/// Flag type for extension CLI flags.
#[derive(Debug, Clone, Copy)]
pub enum FlagType {
    Boolean,
    String,
}

impl FlagType {
    fn as_str(self) -> &'static str {
        match self {
            FlagType::Boolean => "boolean",
            FlagType::String => "string",
        }
    }
}

/// Options for an extension CLI flag.
#[derive(Debug, Clone)]
pub struct FlagOptions {
    pub description: String,
    pub flag_type: FlagType,
    pub default: Option<Value>,
}

impl FlagOptions {
    pub fn boolean(description: impl Into<String>, default: bool) -> Self {
        Self {
            description: description.into(),
            flag_type: FlagType::Boolean,
            default: Some(Value::Bool(default)),
        }
    }

    pub fn string(description: impl Into<String>, default: impl Into<String>) -> Self {
        Self {
            description: description.into(),
            flag_type: FlagType::String,
            default: Some(Value::String(default.into())),
        }
    }
}

/// Factory constructs an extension instance for generated standalone or packed
/// runners. Factory-style crates should expose a function such as:
///
/// ```ignore
/// pub fn new_extension() -> pig_sdk::Extension
/// ```
pub type Factory = fn() -> Extension;

#[derive(Clone)]
struct RequestCancel {
    flag: Arc<AtomicBool>,
    reason: Arc<Mutex<Option<String>>>,
}

impl RequestCancel {
    fn new() -> Self {
        Self {
            flag: Arc::new(AtomicBool::new(false)),
            reason: Arc::new(Mutex::new(None)),
        }
    }

    fn cancel(&self, reason: impl Into<String>) {
        self.flag.store(true, Ordering::Relaxed);
        *self.reason.lock().unwrap() = Some(reason.into());
    }
}

#[derive(Default)]
struct RequestThreads {
    count: Mutex<usize>,
    idle: Condvar,
}

impl RequestThreads {
    fn start(&self) {
        *self.count.lock().unwrap() += 1;
    }

    fn finish(&self) {
        let mut count = self.count.lock().unwrap();
        *count -= 1;
        if *count == 0 {
            self.idle.notify_all();
        }
    }

    fn wait(&self, timeout: Duration) -> bool {
        let count = self.count.lock().unwrap();
        let (count, _) = self
            .idle
            .wait_timeout_while(count, timeout, |count| *count > 0)
            .unwrap();
        *count == 0
    }
}

impl Context {
    fn clone_for_request(
        &self,
        cancel: RequestCancel,
        tool_call_id: Option<String>,
        request_id: String,
    ) -> Self {
        Self {
            conn: self.conn.clone(),
            tool_call_id,
            request_id,
            session_name: self.session_name.clone(),
            cwd: self.cwd.clone(),
            mode: self.mode.clone(),
            shared_width: self.shared_width.clone(),
            shared_height: self.shared_height.clone(),
            shared_model: self.shared_model.clone(),
            shared_session: self.shared_session.clone(),
            session_sub_lock: self.session_sub_lock.clone(),
            cancel_flag: cancel.flag,
            cancel_reason: cancel.reason,
            overlay_seq: self.overlay_seq.clone(),
            overlays: self.overlays.clone(),
            terminal_input: self.terminal_input.clone(),
            terminal_input_seq: self.terminal_input_seq.clone(),
            width_change: self.width_change.clone(),
            width_change_seq: self.width_change_seq.clone(),
            model_streams: self.model_streams.clone(),
            model_stream_seq: self.model_stream_seq.clone(),
        }
    }
}

/// Extension is the main builder for a pig subprocess extension.
pub struct Extension {
    name: String,
    tools: Vec<ToolDef>,
    commands: Vec<CmdDef>,
    shortcuts: Vec<ShortcutDef>,
    handlers: Vec<HandlerDef>,
    flags: Vec<FlagDef>,
    providers: Vec<ProviderDef>,
    renderers: Vec<RendererDef>,
    entry_renderers: Vec<RendererDef>,

    tool_fns: HashMap<String, ToolHandler>,
    tool_prepare_fns: HashMap<String, ToolPrepareArguments>,
    command_fns: HashMap<String, CommandHandler>,
    event_fns: HashMap<u32, EventHandler>,
    shortcut_fns: HashMap<String, ShortcutHandler>,
    renderer_fns: HashMap<String, RendererHandler>,
    entry_renderer_fns: HashMap<String, EntryRendererHandler>,
    // oauth_fns holds OAuth closures for providers registered with an OAuth
    // capability, keyed by provider name for oauth_* dispatch.
    oauth_fns: HashMap<String, OAuthProvider>,
    tool_renderers: ToolRenderers,
}

impl Extension {
    /// Create a new extension with the given name.
    pub fn new(name: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            tools: Vec::new(),
            commands: Vec::new(),
            shortcuts: Vec::new(),
            handlers: Vec::new(),
            flags: Vec::new(),
            providers: Vec::new(),
            renderers: Vec::new(),
            entry_renderers: Vec::new(),
            tool_fns: HashMap::new(),
            tool_prepare_fns: HashMap::new(),
            command_fns: HashMap::new(),
            event_fns: HashMap::new(),
            shortcut_fns: HashMap::new(),
            renderer_fns: HashMap::new(),
            entry_renderer_fns: HashMap::new(),
            oauth_fns: HashMap::new(),
            tool_renderers: ToolRenderers::default(),
        }
    }

    /// Set the render shell of the registered tool `name` (upstream
    /// ToolDefinition.renderShell).
    pub fn tool_render_shell(&mut self, name: &str, shell: ToolRenderShell) {
        for tool in self.tools.iter_mut().filter(|tool| tool.name == name) {
            tool.render_shell = (shell == ToolRenderShell::SelfShell).then(|| "self".to_string());
        }
    }

    /// Set the call renderer of the registered tool `name` (upstream
    /// ToolDefinition.renderCall). An error draws upstream's fallback.
    pub fn render_tool_call(
        &mut self,
        name: &str,
        handler: impl Fn(&Context, Value, &mut ToolRenderContext, u32) -> Result<Vec<String>, String>
        + Send
        + Sync
        + 'static,
    ) {
        for tool in self.tools.iter_mut().filter(|tool| tool.name == name) {
            tool.renders_call = true;
        }
        self.tool_renderers.call.insert(name.to_string(), Box::new(handler));
    }

    /// Set the result renderer of the registered tool `name` (upstream
    /// ToolDefinition.renderResult). An error draws upstream's fallback.
    pub fn render_tool_result(
        &mut self,
        name: &str,
        handler: impl Fn(
            &Context,
            ToolRenderResult,
            ToolRenderResultOptions,
            &mut ToolRenderContext,
            u32,
        ) -> Result<Vec<String>, String>
        + Send
        + Sync
        + 'static,
    ) {
        for tool in self.tools.iter_mut().filter(|tool| tool.name == name) {
            tool.renders_result = true;
        }
        self.tool_renderers.result.insert(name.to_string(), Box::new(handler));
    }

    /// Returns the extension's registered name.
    pub fn name(&self) -> &str {
        &self.name
    }

    /// Register a tool that the LLM can invoke.
    pub fn tool(
        &mut self,
        name: impl Into<String>,
        description: impl Into<String>,
        schema: Value,
        handler: impl Fn(&Context, Value) -> ToolResult + Send + Sync + 'static,
    ) {
        let n = name.into();
        self.tools.push(ToolDef {
            name: n.clone(),
            description: description.into(),
            parameters: schema,
            constrained_sampling: None,
            prompt_guidelines: Vec::new(),
            source: None,
            render_shell: None,
            renders_call: false,
            renders_result: false,
        });
        self.tool_fns.insert(n, Box::new(handler));
    }

    /// Register a tool with a local pre-validation argument transform.
    pub fn tool_with_prepare_arguments(
        &mut self,
        name: impl Into<String>,
        description: impl Into<String>,
        schema: Value,
        prepare: impl Fn(Value) -> Result<Value, String> + Send + Sync + 'static,
        handler: impl Fn(&Context, Value) -> ToolResult + Send + Sync + 'static,
    ) {
        let name = name.into();
        self.tool(name.clone(), description, schema, handler);
        self.tool_prepare_fns.insert(name, Box::new(prepare));
    }

    /// Register a tool with system prompt guidelines.
    /// Guidelines are bullets injected into the system prompt's Guidelines section
    /// when this tool is active. Each guideline must name the tool it refers to.
    pub fn tool_with_guidelines(
        &mut self,
        name: impl Into<String>,
        description: impl Into<String>,
        schema: Value,
        guidelines: Vec<String>,
        handler: impl Fn(&Context, Value) -> ToolResult + Send + Sync + 'static,
    ) {
        let n = name.into();
        self.tools.push(ToolDef {
            name: n.clone(),
            description: description.into(),
            parameters: schema,
            constrained_sampling: None,
            prompt_guidelines: guidelines,
            source: None,
            render_shell: None,
            renders_call: false,
            renders_result: false,
        });
        self.tool_fns.insert(n, Box::new(handler));
    }

    /// Register a tool with an explicit source identifier.
    /// Source overrides the default extension-name attribution Piglet tool
    /// scoping reads, allowing extensions that wrap external tool sources
    /// (e.g. MCP servers) to provide per-tool provenance. `get_all_tools()`
    /// reports upstream's `sourceInfo` for the tool, not this source.
    pub fn tool_with_source(
        &mut self,
        name: impl Into<String>,
        description: impl Into<String>,
        schema: Value,
        source: impl Into<String>,
        guidelines: Vec<String>,
        handler: impl Fn(&Context, Value) -> ToolResult + Send + Sync + 'static,
    ) {
        let n = name.into();
        self.tools.push(ToolDef {
            name: n.clone(),
            description: description.into(),
            parameters: schema,
            constrained_sampling: None,
            prompt_guidelines: guidelines,
            source: Some(source.into()),
            render_shell: None,
            renders_call: false,
            renders_result: false,
        });
        self.tool_fns.insert(n, Box::new(handler));
    }

    /// Register a tool that requests provider-side constrained sampling.
    /// The host forwards the request to the provider, which (for OpenAI-compatible
    /// providers) turns a grammar request into a custom grammar tool.
    /// Mirrors upstream ToolDefinition.constrainedSampling.
    pub fn tool_with_constrained_sampling(
        &mut self,
        name: impl Into<String>,
        description: impl Into<String>,
        schema: Value,
        sampling: ConstrainedSampling,
        handler: impl Fn(&Context, Value) -> ToolResult + Send + Sync + 'static,
    ) {
        let n = name.into();
        self.tools.push(ToolDef {
            name: n.clone(),
            description: description.into(),
            parameters: schema,
            constrained_sampling: Some(sampling),
            prompt_guidelines: Vec::new(),
            source: None,
            render_shell: None,
            renders_call: false,
            renders_result: false,
        });
        self.tool_fns.insert(n, Box::new(handler));
    }

    /// Register a slash command.
    pub fn command(
        &mut self,
        name: impl Into<String>,
        description: impl Into<String>,
        handler: impl Fn(&Context, &str) -> CommandResult + Send + Sync + 'static,
    ) {
        let n = name.into();
        self.commands.push(CmdDef {
            name: n.clone(),
            description: description.into(),
        });
        self.command_fns.insert(n, Box::new(handler));
    }

    /// Register a keyboard shortcut handler.
    pub fn shortcut(
        &mut self,
        key: impl Into<String>,
        description: impl Into<String>,
        handler: impl Fn(&Context) -> CommandResult + Send + Sync + 'static,
    ) {
        let k = key.into();
        self.shortcuts.push(ShortcutDef {
            key: k.clone(),
            description: description.into(),
        });
        self.shortcut_fns.insert(k, Box::new(handler));
    }

    /// Register a CLI flag declaration for this extension.
    pub fn flag(&mut self, name: impl Into<String>, options: FlagOptions) {
        self.flags.push(FlagDef {
            name: name.into(),
            description: options.description,
            flag_type: options.flag_type.as_str().to_string(),
            default: options.default,
        });
    }

    /// Register or override a model provider.
    pub fn register_provider(&mut self, name: impl Into<String>, config: Value) {
        self.providers.push(ProviderDef {
            name: name.into(),
            config,
        });
    }

    /// Remove a previously queued provider registration.
    pub fn unregister_provider(&mut self, name: &str) {
        self.providers.retain(|provider| provider.name != name);
    }

    /// Register a model provider that also contributes an OAuth capability.
    /// The provider's closures are stored for oauth_* dispatch and the config
    /// gains a serializable "oauth" capability descriptor (never closures).
    pub fn register_oauth_provider(
        &mut self,
        name: impl Into<String>,
        config: Value,
        provider: OAuthProvider,
    ) {
        let name = name.into();
        let mut config = match config {
            Value::Object(_) => config,
            _ => Value::Object(Default::default()),
        };
        let flags = ProviderOAuthConfig::from_provider(&name, &provider);
        config["oauth"] = serde_json::to_value(flags).unwrap_or_default();
        self.providers.push(ProviderDef {
            name: name.clone(),
            config,
        });
        self.oauth_fns.insert(name, provider);
    }

    /// Register a custom message renderer.
    pub fn message_renderer(
        &mut self,
        custom_type: impl Into<String>,
        handler: impl Fn(&Context, Value, MessageRenderOptions, u32) -> Result<Vec<String>, String>
        + Send
        + Sync
        + 'static,
    ) {
        let ty = custom_type.into();
        self.renderers.push(RendererDef {
            custom_type: ty.clone(),
        });
        self.renderer_fns.insert(ty, Box::new(handler));
    }

    /// Register a custom session-entry renderer.
    pub fn entry_renderer(
        &mut self,
        custom_type: impl Into<String>,
        handler: impl Fn(&Context, Value, EntryRenderOptions, u32) -> Result<Vec<String>, String>
        + Send
        + Sync
        + 'static,
    ) {
        let ty = custom_type.into();
        self.entry_renderers.push(RendererDef {
            custom_type: ty.clone(),
        });
        self.entry_renderer_fns.insert(ty, Box::new(handler));
    }

    /// Register an event handler. Mutations to boundary entries are retained.
    /// Return `Some(value)` to pass data back to the host, `None` to ack.
    pub fn on_event(
        &mut self,
        event: impl Into<String>,
        can_block: bool,
        handler: impl Fn(&Context, &mut Value) -> Option<Value> + Send + Sync + 'static,
    ) {
        let e = event.into();
        let handler_id = self.handlers.len() as u32 + 1;
        self.handlers.push(HandlerDef {
            event: e,
            can_block,
            handler_id,
        });
        self.event_fns.insert(
            handler_id,
            Box::new(move |ctx, data| Ok(handler(ctx, data))),
        );
    }

    /// Register an awaited project_trust handler with surfaced errors.
    pub fn on_project_trust(
        &mut self,
        handler: impl Fn(&Context, Value) -> Result<ProjectTrustResult, String> + Send + Sync + 'static,
    ) {
        let handler_id = self.handlers.len() as u32 + 1;
        self.handlers.push(HandlerDef {
            event: "project_trust".to_string(),
            can_block: true,
            handler_id,
        });
        self.event_fns.insert(
            handler_id,
            Box::new(move |ctx, data| {
                handler(ctx, data.clone()).and_then(|result| {
                    serde_json::to_value(result)
                        .map(Some)
                        .map_err(|err| err.to_string())
                })
            }),
        );
    }

    /// Connect to the host and run until shutdown.
    /// Reads PIG_EXT_SOCKET from environment. GOPI_EXT_SOCKET is accepted as a
    /// legacy fallback for older local launchers built before the rename.
    pub fn run(self) -> io::Result<()> {
        let sock_path = resolve_socket_path_from_env()?;
        self.run_with_socket(&sock_path)
    }

    /// Connect to the host at the given socket path.
    pub fn run_with_socket(self, sock_path: &str) -> io::Result<()> {
        let stream = UnixStream::connect(sock_path)?;
        let conn = Arc::new(Connection::new(stream));

        // Send register.
        conn.write_envelope(&Envelope {
            msg_type: "register".to_string(),
            register: Some(RegisterMsg {
                name: self.name.clone(),
                tools: self.tools.clone(),
                commands: self.commands.clone(),
                shortcuts: self.shortcuts.clone(),
                handlers: self.handlers.clone(),
                flags: self.flags.clone(),
                providers: self.providers.clone(),
                renderers: self.renderers.clone(),
                entry_renderers: self.entry_renderers.clone(),
            }),
            ..Default::default()
        })?;

        // Wait for ready.
        let env = conn.read_envelope()?;
        if env.msg_type != "ready" {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                format!("expected ready, got {}", env.msg_type),
            ));
        }
        let ready = env.ready.ok_or_else(|| {
            io::Error::new(io::ErrorKind::InvalidData, "ready message has no payload")
        })?;

        // Build base context.
        let shared_width = Arc::new(AtomicU32::new(ready.width));
        let shared_height = Arc::new(AtomicU32::new(ready.height));
        let shared_model = Arc::new(Mutex::new(ready.model.clone()));
        let shared_session = Arc::new(Mutex::new(crate::context::SessionMirror::default()));
        // Initialize session mirror from the ready payload.
        if let Some(ref state) = ready.state {
            if let Some(session) = state.get("session") {
                let _ = shared_session.lock().unwrap().apply_update(session);
            }
        }
        let overlay_seq = Arc::new(AtomicU64::new(0));
        let overlays: RemoteComponents = Arc::new(Mutex::new(HashMap::new()));
        let terminal_input: TerminalInputSubs = Arc::new(Mutex::new(Vec::new()));
        let terminal_input_seq = Arc::new(AtomicU64::new(0));
        let width_change: crate::context::WidthChangeSubs = Arc::new(Mutex::new(Vec::new()));
        let width_change_seq = Arc::new(AtomicU64::new(0));
        let model_streams: ModelStreams = Arc::new(Mutex::new(HashMap::new()));
        let model_stream_seq = Arc::new(AtomicU64::new(0));
        let base_ctx = Context {
            conn: conn.clone(),
            tool_call_id: None,
            request_id: String::new(),
            session_name: ready.session_name,
            cwd: ready.cwd,
            mode: ready.mode,
            shared_width,
            shared_height,
            shared_model,
            shared_session: shared_session.clone(),
            session_sub_lock: Arc::new(Mutex::new(())),
            cancel_flag: Arc::new(AtomicBool::new(false)),
            cancel_reason: Arc::new(Mutex::new(None)),
            overlay_seq,
            terminal_input,
            terminal_input_seq,
            width_change,
            width_change_seq,
            model_streams,
            model_stream_seq,
            overlays,
        };
        let ext = Arc::new(self);
        let active_requests: Arc<Mutex<HashMap<String, RequestCancel>>> =
            Arc::new(Mutex::new(HashMap::new()));
        let request_threads = Arc::new(RequestThreads::default());
        let stop_requests = |reason: &str| -> io::Result<()> {
            for cancel in active_requests.lock().unwrap().values() {
                cancel.cancel(reason);
            }
            conn.cancel_pending_calls();
            if request_threads.wait(Duration::from_secs(2)) {
                Ok(())
            } else {
                Err(io::Error::new(
                    io::ErrorKind::TimedOut,
                    "extension handlers did not stop before the shutdown deadline",
                ))
            }
        };

        // Main loop.
        loop {
            let env = match conn.read_envelope() {
                Ok(e) => e,
                Err(_) => return stop_requests("connection closed"),
            };
            if conn.complete_call(&env) {
                continue;
            }
            match env.msg_type.as_str() {
                "ping" => {
                    if let Some(ping) = env.ping {
                        conn.write_envelope(&crate::protocol::Envelope {
                            msg_type: "pong".to_string(),
                            pong: Some(crate::protocol::PongMsg { nonce: ping.nonce }),
                            ..Default::default()
                        })?;
                    }
                }
                "request" => {
                    let id = env.id.unwrap_or_default();
                    if let Some(req) = env.request {
                        conn.request_state(&id, "started", None)?;
                        let cancel = RequestCancel::new();
                        active_requests
                            .lock()
                            .unwrap()
                            .insert(id.clone(), cancel.clone());
                        let active = active_requests.clone();
                        let base = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                        let conn = conn.clone();
                        let ext = ext.clone();
                        request_threads.start();
                        let threads = request_threads.clone();
                        let failed_id = id.clone();
                        let failed_conn = conn.clone();
                        let failed_active = active.clone();
                        let failed_threads = threads.clone();
                        if let Err(err) = thread::Builder::new()
                            .name(format!("pig-request-{id}"))
                            .spawn(move || {
                                let handled = catch_unwind(AssertUnwindSafe(|| {
                                    ext.handle_request(&base, &conn, &id, &req, cancel)
                                }));
                                if handled.is_err() {
                                    let _ = conn.respond(
                                        &id,
                                        None,
                                        Some(ErrorInfo {
                                            code: Some("handler_panic".to_string()),
                                            message: "extension handler panicked".to_string(),
                                        }),
                                    );
                                }
                                active.lock().unwrap().remove(&id);
                                threads.finish();
                            })
                        {
                            failed_active.lock().unwrap().remove(&failed_id);
                            failed_threads.finish();
                            let _ = failed_conn.respond(
                                &failed_id,
                                None,
                                Some(ErrorInfo {
                                    code: Some("handler_start".to_string()),
                                    message: format!("start extension handler: {err}"),
                                }),
                            );
                        }
                    }
                }
                "cancel" => {
                    let request_id = env
                        .cancel
                        .as_ref()
                        .map(|c| c.request_id.as_str())
                        .filter(|id| !id.is_empty())
                        .or(env.id.as_deref())
                        .unwrap_or("");
                    if let Some(cancel) = active_requests.lock().unwrap().get(request_id) {
                        let reason = env
                            .cancel
                            .as_ref()
                            .map(|c| c.reason.clone())
                            .unwrap_or_default();
                        cancel.cancel(reason);
                    }
                    conn.cancel_pending_calls_for(request_id);
                }
                "notify" => {
                    if let Some(notify) = env.notify {
                        match notify.method.as_str() {
                            "tool_render_release" => {
                                ext.tool_renderers.release(notify.args.as_ref());
                            }
                            "model_stream_event" => {
                                if let Some(args) = &notify.args {
                                    let stream_id = args.get("streamId").and_then(|value| value.as_str()).unwrap_or("");
                                    let event = args.get("event").cloned().unwrap_or(Value::Null);
                                    if let Some(stream) = base_ctx.model_streams.lock().unwrap().get(stream_id).cloned() {
                                        stream.push(event);
                                    }
                                }
                            }
                            "state_update" => {
                                if let Some(args) = &notify.args {
                                    if let Some(state) =
                                        args.get("state").and_then(|s| s.as_object())
                                    {
                                        // Session replication: apply incremental entries.
                                        if let Some(session) = state.get("session") {
                                            let _ = base_ctx
                                                .shared_session
                                                .lock()
                                                .unwrap()
                                                .apply_update(session);
                                        }
                                        if let Some(model) =
                                            state.get("model").and_then(|m| m.as_object())
                                        {
                                            let name =
                                                model.get("name").and_then(|n| n.as_str()).or_else(
                                                    || model.get("id").and_then(|i| i.as_str()),
                                                );
                                            if let Some(name) = name {
                                                *base_ctx.shared_model.lock().unwrap() =
                                                    name.to_string();
                                            }
                                        }
                                    }
                                }
                            }
                            "width_change" => {
                                if let Some(args) = &notify.args {
                                    if let Some(w) = args.get("width").and_then(|w| w.as_u64()) {
                                        if w > 0 {
                                            base_ctx
                                                .shared_width
                                                .store(w as u32, Ordering::Relaxed);
                                            // After the store, so a handler that
                                            // reads Context::width sees the new value.
                                            let width_subs: Vec<_> = base_ctx
                                                .width_change
                                                .lock()
                                                .map(|subs| {
                                                    subs.iter().map(|(_, h)| h.clone()).collect()
                                                })
                                                .unwrap_or_default();
                                            for handler in width_subs {
                                                handler(w as u32);
                                            }
                                            let overlays: Vec<_> = base_ctx
                                                .overlays
                                                .lock()
                                                .unwrap()
                                                .values()
                                                .cloned()
                                                .collect();
                                            for overlay in overlays {
                                                overlay.request_render();
                                            }
                                        }
                                    }
                                }
                            }
                            "height_change" => {
                                if let Some(args) = &notify.args {
                                    if let Some(h) = args.get("height").and_then(|h| h.as_u64()) {
                                        if h > 0 {
                                            base_ctx
                                                .shared_height
                                                .store(h as u32, Ordering::Relaxed);
                                        }
                                    }
                                }
                            }
                            "ui.custom.input" => {
                                let Some(args) = notify.args.as_ref() else {
                                    continue;
                                };
                                let key = args.get("key").and_then(|v| v.as_str()).unwrap_or("");
                                let data = args.get("data").and_then(|v| v.as_str()).unwrap_or("");
                                let overlay = base_ctx.overlays.lock().unwrap().get(key).cloned();
                                let Some(overlay) = overlay else {
                                    continue;
                                };
                                if let Err(err) = overlay.send_input(data.to_string()) {
                                    overlay.active.store(false, Ordering::Release);
                                    let _ = conn.notify(
                                        "ui.custom.close",
                                        Some(serde_json::json!({"key": key, "error": err})),
                                    );
                                }
                            }
                            _ => {}
                        }
                    }
                }
                "shutdown" => return stop_requests("shutdown"),
                _ => {}
            }
        }
    }

    fn dispatch_oauth(&self, conn: &Arc<Connection>, id: &str, req: &RequestMsg) {
        let name = req.tool.as_deref().unwrap_or("");
        let Some(provider) = self.oauth_fns.get(name) else {
            let _ = conn.respond(
                id,
                None,
                Some(ErrorInfo {
                    code: None,
                    message: format!("unknown oauth provider: {}", name),
                }),
            );
            return;
        };
        let err = |msg: String| ErrorInfo {
            code: None,
            message: msg,
        };
        match req.method.as_str() {
            "oauth_login" => {
                let cb = OAuthLoginCallbacks {
                    conn: conn.clone(),
                    request_id: id.to_string(),
                };
                match (provider.login)(&cb) {
                    Ok(creds) => {
                        let _ = conn.respond(id, serde_json::to_value(creds).ok(), None);
                    }
                    Err(e) => {
                        let _ = conn.respond(id, None, Some(err(e)));
                    }
                }
            }
            "oauth_refresh" => {
                let Some(refresh) = provider.refresh_token.as_ref() else {
                    let _ = conn.respond(
                        id,
                        None,
                        Some(err("provider does not support refresh".into())),
                    );
                    return;
                };
                match refresh(decode_creds(&req.args)) {
                    Ok(creds) => {
                        let _ = conn.respond(id, serde_json::to_value(creds).ok(), None);
                    }
                    Err(e) => {
                        let _ = conn.respond(id, None, Some(err(e)));
                    }
                }
            }
            "oauth_get_api_key" => {
                let key = provider
                    .get_api_key
                    .as_ref()
                    .map(|f| f(decode_creds(&req.args)))
                    .unwrap_or_default();
                let _ = conn.respond(id, Some(serde_json::json!({ "apiKey": key })), None);
            }
            "oauth_credential_status" => {
                let Some(store) = provider.credential_store.as_ref() else {
                    let _ = conn.respond(
                        id,
                        None,
                        Some(err("provider has no credential store".into())),
                    );
                    return;
                };
                let st = store.credential_status();
                let _ = conn.respond(
                    id,
                    Some(serde_json::json!({
                        "present": st.present,
                        "authType": st.auth_type,
                        "source": st.source,
                    })),
                    None,
                );
            }
            "oauth_store_credentials" => {
                let Some(store) = provider.credential_store.as_ref() else {
                    let _ = conn.respond(
                        id,
                        None,
                        Some(err("provider has no credential store".into())),
                    );
                    return;
                };
                match store.store_credentials(decode_creds(&req.args)) {
                    Ok(path) => {
                        let _ = conn.respond(id, Some(serde_json::json!({ "path": path })), None);
                    }
                    Err(e) => {
                        let _ = conn.respond(id, None, Some(err(e)));
                    }
                }
            }
            "oauth_delete_credentials" => {
                let Some(store) = provider.credential_store.as_ref() else {
                    let _ = conn.respond(
                        id,
                        None,
                        Some(err("provider has no credential store".into())),
                    );
                    return;
                };
                match store.delete_credentials() {
                    Ok(deleted) => {
                        let _ =
                            conn.respond(id, Some(serde_json::json!({ "deleted": deleted })), None);
                    }
                    Err(e) => {
                        let _ = conn.respond(id, None, Some(err(e)));
                    }
                }
            }
            other => {
                let _ = conn.respond(
                    id,
                    None,
                    Some(err(format!("unknown oauth method: {}", other))),
                );
            }
        }
    }

    fn handle_request(
        &self,
        base_ctx: &Context,
        conn: &Arc<Connection>,
        id: &str,
        req: &RequestMsg,
        cancel: RequestCancel,
    ) {
        match req.method.as_str() {
            "oauth_login"
            | "oauth_refresh"
            | "oauth_get_api_key"
            | "oauth_credential_status"
            | "oauth_store_credentials"
            | "oauth_delete_credentials" => {
                self.dispatch_oauth(conn, id, req);
            }
            "terminal_input" => {
                // The host waits on this reply and upstream's handler is
                // synchronous, so handlers run inline in registration order. A
                // handler's data replaces the chunk for the handlers after it;
                // a panicking handler yields no verdict.
                let original = req
                    .args
                    .as_ref()
                    .and_then(|a| a.get("data"))
                    .and_then(|d| d.as_str())
                    .unwrap_or("")
                    .to_string();
                let handlers: Vec<_> = base_ctx
                    .terminal_input
                    .lock()
                    .map(|subs| subs.iter().map(|(_, h)| h.clone()).collect())
                    .unwrap_or_default();
                let mut current = original.clone();
                let mut consume = false;
                for handler in handlers {
                    let verdict = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                        handler(&current)
                    }))
                    .unwrap_or_default();
                    if verdict.consume {
                        consume = true;
                        break;
                    }
                    if let Some(data) = verdict.data {
                        current = data;
                    }
                }
                let verdict = if !consume && current != original {
                    serde_json::json!({"consume": false, "data": current})
                } else {
                    serde_json::json!({"consume": consume})
                };
                let _ = conn.respond(id, Some(verdict), None);
            }
            "tool_call" => {
                let tool_name = req.tool.as_deref().unwrap_or("");
                if let Some(handler) = self.tool_fns.get(tool_name) {
                    let ctx = base_ctx.clone_for_request(
                        cancel.clone(),
                        req.tool_call_id.clone(),
                        id.to_string(),
                    );
                    let args = req
                        .args
                        .clone()
                        .unwrap_or(Value::Object(Default::default()));
                    let args = if let Some(prepare) = self.tool_prepare_fns.get(tool_name) {
                        match prepare(args) {
                            Ok(prepared) => prepared,
                            Err(message) => {
                                let _ = conn.respond(
                                    id,
                                    None,
                                    Some(ErrorInfo {
                                        code: None,
                                        message,
                                    }),
                                );
                                return;
                            }
                        }
                    } else {
                        args
                    };
                    let result = handler(&ctx, args);
                    match result {
                        ToolResult::Text(s) => {
                            let _ = conn.respond(id, Some(serde_json::json!({"content": s})), None);
                        }
                        ToolResult::Json(v) => {
                            let _ = conn.respond(id, Some(v), None);
                        }
                        ToolResult::Error(s) => {
                            let _ = conn.respond(
                                id,
                                None,
                                Some(ErrorInfo {
                                    code: None,
                                    message: s,
                                }),
                            );
                        }
                    }
                } else {
                    let _ = conn.respond(
                        id,
                        None,
                        Some(ErrorInfo {
                            code: None,
                            message: format!("unknown tool: {}", tool_name),
                        }),
                    );
                }
            }
            "command" => {
                let cmd_name = req.tool.as_deref().unwrap_or("");
                if let Some(handler) = self.command_fns.get(cmd_name) {
                    let args_str = req.args.as_ref().and_then(|v| v.as_str()).unwrap_or("");
                    let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                    match handler(&ctx, args_str) {
                        CommandResult::Ok => {
                            let _ = conn.respond(id, None, None);
                        }
                        CommandResult::Error(s) => {
                            let _ = conn.respond(
                                id,
                                None,
                                Some(ErrorInfo {
                                    code: None,
                                    message: s,
                                }),
                            );
                        }
                    }
                } else {
                    let _ = conn.respond(
                        id,
                        None,
                        Some(ErrorInfo {
                            code: None,
                            message: format!("unknown command: {}", cmd_name),
                        }),
                    );
                }
            }
            "event" => {
                let handler_id = req.handler_id;
                let mut data = req.args.clone().unwrap_or(Value::Object(Default::default()));
                let result = if let Some(handler) = self.event_fns.get(&handler_id) {
                    let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                    match catch_unwind(AssertUnwindSafe(|| handler(&ctx, &mut data))) {
                        Ok(result) => result,
                        Err(_) => Err("extension handler panicked".to_string()),
                    }
                } else {
                    Err(format!(
                        "unknown event handler {} for {}",
                        handler_id,
                        req.event.as_deref().unwrap_or("")
                    ))
                };
                let (mut value, error) = match result {
                    Ok(value) => (value, None),
                    Err(message) => (None, Some(ErrorInfo { code: None, message })),
                };
                if req.event.as_deref() == Some("agent_before_settle") {
                    value = Some(serde_json::json!({
                        "_pigBoundaryEntries": data.get("entries"),
                        "_pigBoundaryResult": value,
                    }));
                }
                let _ = conn.respond(id, value, error);
            }
            "shortcut" => {
                let shortcut_key = req.tool.as_deref().unwrap_or("");
                if let Some(handler) = self.shortcut_fns.get(shortcut_key) {
                    let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                    match handler(&ctx) {
                        CommandResult::Ok => {
                            let _ = conn.respond(id, None, None);
                        }
                        CommandResult::Error(s) => {
                            let _ = conn.respond(
                                id,
                                None,
                                Some(ErrorInfo {
                                    code: None,
                                    message: s,
                                }),
                            );
                        }
                    }
                } else {
                    let _ = conn.respond(
                        id,
                        None,
                        Some(ErrorInfo {
                            code: None,
                            message: format!("unknown shortcut: {}", shortcut_key),
                        }),
                    );
                }
            }
            "render_message" => {
                let custom_type = req.tool.as_deref().unwrap_or("");
                if let Some(handler) = self.renderer_fns.get(custom_type) {
                    let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                    let args = req
                        .args
                        .clone()
                        .unwrap_or(Value::Object(Default::default()));
                    let message = args
                        .get("message")
                        .cloned()
                        .unwrap_or(Value::Object(Default::default()));
                    let options = args
                        .get("options")
                        .cloned()
                        .and_then(|v| serde_json::from_value(v).ok())
                        .unwrap_or_default();
                    let width = args.get("width").and_then(|v| v.as_u64()).unwrap_or(0) as u32;
                    match handler(&ctx, message, options, width) {
                        Ok(lines) => {
                            let _ =
                                conn.respond(id, Some(serde_json::json!({"lines": lines})), None);
                        }
                        Err(message) => {
                            let _ = conn.respond(
                                id,
                                None,
                                Some(ErrorInfo {
                                    code: None,
                                    message,
                                }),
                            );
                        }
                    }
                } else {
                    let _ = conn.respond(
                        id,
                        None,
                        Some(ErrorInfo {
                            code: None,
                            message: format!("unknown renderer: {}", custom_type),
                        }),
                    );
                }
            }
            "render_tool" => {
                let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                let tool = req.tool.as_deref().unwrap_or("");
                match self.tool_renderers.render(&ctx, conn, tool, req.args.as_ref()) {
                    Ok(lines) => {
                        let _ = conn.respond(id, Some(serde_json::json!({"lines": lines})), None);
                    }
                    Err(message) => {
                        let _ = conn.respond(id, None, Some(ErrorInfo { code: None, message }));
                    }
                }
            }
            "render_entry" => {
                let custom_type = req.tool.as_deref().unwrap_or("");
                if let Some(handler) = self.entry_renderer_fns.get(custom_type) {
                    let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                    let args = req
                        .args
                        .clone()
                        .unwrap_or(Value::Object(Default::default()));
                    let entry = args
                        .get("entry")
                        .cloned()
                        .unwrap_or(Value::Object(Default::default()));
                    let options = args
                        .get("options")
                        .cloned()
                        .and_then(|v| serde_json::from_value(v).ok())
                        .unwrap_or_default();
                    let width = args.get("width").and_then(|v| v.as_u64()).unwrap_or(0) as u32;
                    match handler(&ctx, entry, options, width) {
                        Ok(lines) => {
                            let _ =
                                conn.respond(id, Some(serde_json::json!({"lines": lines})), None);
                        }
                        Err(message) => {
                            let _ = conn.respond(
                                id,
                                None,
                                Some(ErrorInfo {
                                    code: None,
                                    message,
                                }),
                            );
                        }
                    }
                } else {
                    let _ = conn.respond(
                        id,
                        None,
                        Some(ErrorInfo {
                            code: None,
                            message: format!("unknown entry renderer: {}", custom_type),
                        }),
                    );
                }
            }
            _ => {
                let _ = conn.respond(
                    id,
                    None,
                    Some(ErrorInfo {
                        code: None,
                        message: format!("unknown method: {}", req.method),
                    }),
                );
            }
        }
    }
}

/// Connect and close a failed packed member's socket so the host stops waiting for registration.
/// Uses the same transport as `Extension::run_with_socket` and sends no registration frame.
// pig additive (D20): generated packed runners report factory failures independently of live siblings.
pub fn report_load_failure(sock_path: &str) -> io::Result<()> {
    UnixStream::connect(sock_path).map(drop)
}

fn resolve_socket_path_from_env() -> io::Result<String> {
    std::env::var("PIG_EXT_SOCKET")
        .or_else(|_| std::env::var("GOPI_EXT_SOCKET"))
        .map_err(|_| {
            io::Error::new(
                io::ErrorKind::NotFound,
                "PIG_EXT_SOCKET not set: extension must be launched by pig",
            )
        })
}

/// Decode OAuth credentials from an oauth_* request's args, defaulting to empty.
fn decode_creds(args: &Option<Value>) -> crate::oauth::OAuthCredentials {
    args.clone()
        .and_then(|v| serde_json::from_value(v).ok())
        .unwrap_or_default()
}

#[cfg(test)]
mod tests {
    use super::{CommandResult, Extension, resolve_socket_path_from_env};
    use crate::protocol::{Envelope, ReadyMsg, RequestMsg};
    use std::io::{Read, Write};
    use std::os::unix::net::{UnixListener, UnixStream};
    use std::sync::mpsc;
    use std::sync::{Mutex, OnceLock};
    use std::time::{Duration, SystemTime, UNIX_EPOCH};

    fn env_lock() -> &'static Mutex<()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(()))
    }

    fn write_env(stream: &mut UnixStream, env: &Envelope) {
        let data = serde_json::to_vec(env).unwrap();
        stream
            .write_all(&(data.len() as u32).to_be_bytes())
            .unwrap();
        stream.write_all(&data).unwrap();
        stream.flush().unwrap();
    }

    fn read_env(stream: &mut UnixStream) -> Envelope {
        loop {
            let env = read_env_raw(stream);
            if env.msg_type != "request_state" {
                return env;
            }
        }
    }

    fn read_env_raw(stream: &mut UnixStream) -> Envelope {
        let mut hdr = [0u8; 4];
        stream.read_exact(&mut hdr).unwrap();
        let mut data = vec![0u8; u32::from_be_bytes(hdr) as usize];
        stream.read_exact(&mut data).unwrap();
        serde_json::from_slice(&data).unwrap()
    }

    #[test]
    fn registers_full_declaration_surface() {
        let dir = std::env::temp_dir();
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let sock = dir.join(format!("pig-sdk-rs-decls-{unique}.sock"));
        let _ = std::fs::remove_file(&sock);
        let listener = UnixListener::bind(&sock).unwrap();

        let mut ext = Extension::new("decl-test");
        ext.shortcut("ctrl+x", "shortcut", |_ctx| CommandResult::Ok);
        ext.flag("dry-run", super::FlagOptions::boolean("dry run", false));
        ext.register_provider("fake", serde_json::json!({"kind": "test"}));
        ext.message_renderer("custom", |_ctx, message, _options, width| {
            Ok(vec![format!(
                "{}:{}",
                message.get("text").and_then(|v| v.as_str()).unwrap_or(""),
                width
            )])
        });

        let sock_for_ext = sock.clone();
        let handle =
            std::thread::spawn(move || ext.run_with_socket(sock_for_ext.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        let register = read_env(&mut stream);
        let reg = register.register.unwrap();
        assert_eq!(reg.shortcuts[0].key, "ctrl+x");
        assert_eq!(reg.flags[0].name, "dry-run");
        assert_eq!(reg.providers[0].name, "fake");
        assert_eq!(reg.renderers[0].custom_type, "custom");
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "ready".to_string(),
                ready: Some(ReadyMsg {
                    session_name: "test".to_string(),
                    cwd: "/tmp".to_string(),
                    mode: "tui".to_string(),
                    width: 80,
                    height: 24,
                    model: "test-model".to_string(),
                    state: None,
                }),
                ..Default::default()
            },
        );
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("r1".to_string()),
                request: Some(RequestMsg {
                    method: "render_message".to_string(),
                    tool: Some("custom".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: Some(
                        serde_json::json!({"message":{"text":"hi"},"options":{},"width":42}),
                    ),
                }),
                ..Default::default()
            },
        );
        let response = read_env(&mut stream);
        assert_eq!(
            response.response.unwrap().result.unwrap(),
            serde_json::json!({"lines":["hi:42"]})
        );
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "shutdown".to_string(),
                ..Default::default()
            },
        );
        handle.join().unwrap().unwrap();
        let _ = std::fs::remove_file(sock);
    }

    #[test]
    fn cancels_in_flight_command_context() {
        let dir = std::env::temp_dir();
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let sock = dir.join(format!("pig-sdk-rs-cancel-{unique}.sock"));
        let _ = std::fs::remove_file(&sock);
        let listener = UnixListener::bind(&sock).unwrap();
        let (started_tx, started_rx) = mpsc::channel();
        let (cancelled_tx, cancelled_rx) = mpsc::channel();

        let mut ext = Extension::new("cancel-test");
        ext.command("wait", "wait", move |ctx, _args| {
            let _ = started_tx.send(());
            while !ctx.is_cancelled() {
                std::thread::sleep(Duration::from_millis(5));
            }
            let _ = cancelled_tx.send(ctx.cancellation_reason());
            CommandResult::Error("cancelled".to_string())
        });

        let sock_for_ext = sock.clone();
        let handle =
            std::thread::spawn(move || ext.run_with_socket(sock_for_ext.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        let register = read_env(&mut stream);
        assert_eq!(register.msg_type, "register");
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "ready".to_string(),
                ready: Some(ReadyMsg {
                    session_name: "test".to_string(),
                    cwd: "/tmp".to_string(),
                    mode: "tui".to_string(),
                    width: 80,
                    height: 24,
                    model: "test-model".to_string(),
                    state: None,
                }),
                ..Default::default()
            },
        );
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("req-1".to_string()),
                request: Some(RequestMsg {
                    method: "command".to_string(),
                    tool: Some("wait".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: None,
                }),
                ..Default::default()
            },
        );
        started_rx.recv_timeout(Duration::from_secs(2)).unwrap();
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "cancel".to_string(),
                id: Some("req-1".to_string()),
                cancel: Some(crate::protocol::CancelMsg {
                    request_id: "req-1".to_string(),
                    reason: "test cancel".to_string(),
                }),
                ..Default::default()
            },
        );
        assert_eq!(
            cancelled_rx.recv_timeout(Duration::from_secs(2)).unwrap(),
            Some("test cancel".to_string())
        );
        let response = read_env(&mut stream);
        assert_eq!(response.msg_type, "response");
        assert_eq!(response.id.as_deref(), Some("req-1"));
        assert!(response.response.unwrap().error.is_some());
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "shutdown".to_string(),
                ..Default::default()
            },
        );
        handle.join().unwrap().unwrap();
        let _ = std::fs::remove_file(sock);
    }

    #[test]
    fn resolves_pig_ext_socket_first() {
        let _guard = env_lock().lock().unwrap();
        unsafe {
            std::env::set_var("PIG_EXT_SOCKET", "/tmp/pig.sock");
            std::env::set_var("GOPI_EXT_SOCKET", "/tmp/legacy.sock");
        }
        let got = resolve_socket_path_from_env().unwrap();
        unsafe {
            std::env::remove_var("PIG_EXT_SOCKET");
            std::env::remove_var("GOPI_EXT_SOCKET");
        }
        assert_eq!(got, "/tmp/pig.sock");
    }

    #[test]
    fn falls_back_to_legacy_gopi_ext_socket() {
        let _guard = env_lock().lock().unwrap();
        unsafe {
            std::env::remove_var("PIG_EXT_SOCKET");
            std::env::set_var("GOPI_EXT_SOCKET", "/tmp/legacy.sock");
        }
        let got = resolve_socket_path_from_env().unwrap();
        unsafe {
            std::env::remove_var("GOPI_EXT_SOCKET");
        }
        assert_eq!(got, "/tmp/legacy.sock");
    }

    #[test]
    fn errors_when_socket_env_missing() {
        let _guard = env_lock().lock().unwrap();
        unsafe {
            std::env::remove_var("PIG_EXT_SOCKET");
            std::env::remove_var("GOPI_EXT_SOCKET");
        }
        let err = resolve_socket_path_from_env().unwrap_err();
        assert_eq!(err.kind(), std::io::ErrorKind::NotFound);
        assert!(err.to_string().contains("PIG_EXT_SOCKET not set"));
    }
    #[test]
    fn oauth_provider_bridge() {
        use crate::oauth::{
            OAuthCredentialStatus, OAuthCredentialStore, OAuthCredentials, OAuthDeviceCodeInfo,
            OAuthLoginCallbacks, OAuthPrompt, OAuthProvider,
        };
        use crate::protocol::CallResultMsg;
        use std::sync::Mutex as StdMutex;

        struct FakeStore {
            stored: StdMutex<Option<OAuthCredentials>>,
        }
        impl OAuthCredentialStore for FakeStore {
            fn credential_status(&self) -> OAuthCredentialStatus {
                let present = self.stored.lock().unwrap().is_some();
                OAuthCredentialStatus {
                    present,
                    auth_type: "oauth".to_string(),
                    source: "fake".to_string(),
                }
            }
            fn store_credentials(&self, creds: OAuthCredentials) -> Result<String, String> {
                *self.stored.lock().unwrap() = Some(creds);
                Ok("/fake/path".to_string())
            }
            fn delete_credentials(&self) -> Result<bool, String> {
                *self.stored.lock().unwrap() = None;
                Ok(true)
            }
        }

        let dir = std::env::temp_dir();
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let sock = dir.join(format!("pig-sdk-rs-oauth-{unique}.sock"));
        let _ = std::fs::remove_file(&sock);
        let listener = UnixListener::bind(&sock).unwrap();

        let mut ext = Extension::new("oauth-test");
        let provider = OAuthProvider {
            name: "Example Provider".to_string(),
            is_subscription: true,
            login: Box::new(|cb: &OAuthLoginCallbacks| {
                cb.on_device_code(OAuthDeviceCodeInfo {
                    user_code: "WXYZ".to_string(),
                    verification_uri: "https://verify".to_string(),
                    ..Default::default()
                });
                let value = cb.on_prompt(OAuthPrompt {
                    message: "URL".to_string(),
                    ..Default::default()
                })?;
                Ok(OAuthCredentials {
                    access: format!("tok:{value}"),
                    expires: 999,
                    ..Default::default()
                })
            }),
            refresh_token: Some(Box::new(|_creds| {
                Ok(OAuthCredentials {
                    access: "fresh".to_string(),
                    refresh: "r2".to_string(),
                    expires: 42,
                    ..Default::default()
                })
            })),
            get_api_key: Some(Box::new(|creds: OAuthCredentials| {
                format!("key-for-{}", creds.access)
            })),
            credential_store: Some(Box::new(FakeStore {
                stored: StdMutex::new(None),
            })),
        };
        ext.register_oauth_provider(
            "example-provider",
            serde_json::json!({"name": "Example Provider"}),
            provider,
        );

        let sock_for_ext = sock.clone();
        let handle =
            std::thread::spawn(move || ext.run_with_socket(sock_for_ext.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();

        // Register advertises capability flags, no closures.
        let register = read_env(&mut stream).register.unwrap();
        let oauth = register.providers[0].config.get("oauth").unwrap();
        assert_eq!(oauth.get("name").unwrap(), "Example Provider");
        assert_eq!(oauth.get("isSubscription").unwrap(), true);
        assert_eq!(oauth.get("has_login").unwrap(), true);
        assert_eq!(oauth.get("has_refresh").unwrap(), true);
        assert_eq!(oauth.get("has_get_api_key").unwrap(), true);
        assert_eq!(oauth.get("has_credential_store").unwrap(), true);

        write_env(
            &mut stream,
            &Envelope {
                msg_type: "ready".to_string(),
                ready: Some(ReadyMsg {
                    session_name: "t".to_string(),
                    cwd: "/tmp".to_string(),
                    mode: "tui".to_string(),
                    width: 80,
                    height: 24,
                    model: "m".to_string(),
                    state: None,
                }),
                ..Default::default()
            },
        );

        // oauth_login: extension drives callbacks, then returns creds.
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("login-1".to_string()),
                request: Some(RequestMsg {
                    method: "oauth_login".to_string(),
                    tool: Some("example-provider".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: None,
                }),
                ..Default::default()
            },
        );

        // onDeviceCode call: ack it.
        let dc = read_env(&mut stream);
        assert_eq!(dc.msg_type, "call");
        let dc_call = dc.call.unwrap();
        assert_eq!(dc_call.method, "oauth.cb.onDeviceCode");
        assert_eq!(dc_call.parent_request_id.as_deref(), Some("login-1"));
        assert_eq!(dc_call.args.unwrap().get("userCode").unwrap(), "WXYZ");
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "call_result".to_string(),
                id: dc.id.clone(),
                call_result: Some(CallResultMsg {
                    result: None,
                    error: None,
                }),
                ..Default::default()
            },
        );

        // onPrompt call: return a value.
        let pr = read_env(&mut stream);
        assert_eq!(pr.call.unwrap().method, "oauth.cb.onPrompt");
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "call_result".to_string(),
                id: pr.id.clone(),
                call_result: Some(CallResultMsg {
                    result: Some(serde_json::json!({"value": "typed-value"})),
                    error: None,
                }),
                ..Default::default()
            },
        );

        // login response: creds echo the prompt value.
        let login_resp = read_env(&mut stream);
        assert_eq!(login_resp.id.as_deref(), Some("login-1"));
        let creds = login_resp.response.unwrap().result.unwrap();
        assert_eq!(creds.get("access").unwrap(), "tok:typed-value");
        assert_eq!(creds.get("expires").unwrap(), 999);

        // oauth_get_api_key.
        let cred_args = serde_json::json!({"access": "abc"});
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("key-1".to_string()),
                request: Some(RequestMsg {
                    method: "oauth_get_api_key".to_string(),
                    tool: Some("example-provider".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: Some(cred_args.clone()),
                }),
                ..Default::default()
            },
        );
        let key_resp = read_env(&mut stream).response.unwrap().result.unwrap();
        assert_eq!(key_resp.get("apiKey").unwrap(), "key-for-abc");

        // oauth_refresh.
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("ref-1".to_string()),
                request: Some(RequestMsg {
                    method: "oauth_refresh".to_string(),
                    tool: Some("example-provider".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: Some(cred_args.clone()),
                }),
                ..Default::default()
            },
        );
        let ref_resp = read_env(&mut stream).response.unwrap().result.unwrap();
        assert_eq!(ref_resp.get("access").unwrap(), "fresh");
        assert_eq!(ref_resp.get("refresh").unwrap(), "r2");

        // oauth_store_credentials then oauth_credential_status reflect the store.
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("store-1".to_string()),
                request: Some(RequestMsg {
                    method: "oauth_store_credentials".to_string(),
                    tool: Some("example-provider".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: Some(cred_args),
                }),
                ..Default::default()
            },
        );
        let store_resp = read_env(&mut stream).response.unwrap().result.unwrap();
        assert_eq!(store_resp.get("path").unwrap(), "/fake/path");
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("status-1".to_string()),
                request: Some(RequestMsg {
                    method: "oauth_credential_status".to_string(),
                    tool: Some("example-provider".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: None,
                }),
                ..Default::default()
            },
        );
        let status_resp = read_env(&mut stream).response.unwrap().result.unwrap();
        assert_eq!(status_resp.get("present").unwrap(), true);
        assert_eq!(status_resp.get("source").unwrap(), "fake");

        write_env(
            &mut stream,
            &Envelope {
                msg_type: "shutdown".to_string(),
                ..Default::default()
            },
        );
        handle.join().unwrap().unwrap();
        let _ = std::fs::remove_file(sock);
    }

    #[test]
    fn heartbeat_and_request_states_bypass_handlers() {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let sock = std::env::temp_dir().join(format!("pig-sdk-rs-liveness-{unique}.sock"));
        let _ = std::fs::remove_file(&sock);
        let listener = UnixListener::bind(&sock).unwrap();
        let mut ext = Extension::new("rust-liveness");
        ext.command("done", "complete immediately", |_ctx, _args| {
            CommandResult::Ok
        });
        ext.command("ask", "wait for input", |ctx, _args| {
            match ctx.input("Question", "Answer") {
                Ok(_) => CommandResult::Ok,
                Err(err) => CommandResult::Error(err.to_string()),
            }
        });
        ext.command("title", "set title", |ctx, _args| {
            ctx.set_title("Rust title");
            CommandResult::Ok
        });
        ext.command("panic", "panic", |_ctx, _args| panic!("handler boom"));
        let sock_for_ext = sock.clone();
        let handle =
            std::thread::spawn(move || ext.run_with_socket(sock_for_ext.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        assert_eq!(read_env(&mut stream).msg_type, "register");
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "ready".to_string(),
                ready: Some(ReadyMsg {
                    session_name: String::new(),
                    cwd: "/tmp".to_string(),
                    mode: "tui".to_string(),
                    width: 80,
                    height: 24,
                    model: String::new(),
                    state: None,
                }),
                ..Default::default()
            },
        );
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "ping".to_string(),
                ping: Some(crate::protocol::PingMsg {
                    nonce: "heartbeat-1".to_string(),
                }),
                ..Default::default()
            },
        );
        let pong = read_env_raw(&mut stream);
        assert_eq!(pong.msg_type, "pong");
        assert_eq!(pong.pong.unwrap().nonce, "heartbeat-1");
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("req-state".to_string()),
                request: Some(RequestMsg {
                    method: "command".to_string(),
                    tool: Some("done".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: None,
                }),
                ..Default::default()
            },
        );
        let started = read_env_raw(&mut stream).request_state.unwrap();
        let completed = read_env_raw(&mut stream).request_state.unwrap();
        assert_eq!(
            (started.request_id.as_str(), started.state.as_str()),
            ("req-state", "started")
        );
        assert_eq!(
            (completed.request_id.as_str(), completed.state.as_str()),
            ("req-state", "completed")
        );
        assert_eq!(read_env_raw(&mut stream).msg_type, "response");

        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("req-user".to_string()),
                request: Some(RequestMsg {
                    method: "command".to_string(),
                    tool: Some("ask".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: None,
                }),
                ..Default::default()
            },
        );
        assert_eq!(
            read_env_raw(&mut stream).request_state.unwrap().state,
            "started"
        );
        let blocked = read_env_raw(&mut stream).request_state.unwrap();
        assert_eq!(
            (blocked.state.as_str(), blocked.reason.as_deref()),
            ("blocked", Some("user"))
        );
        let call = read_env_raw(&mut stream);
        assert_eq!(
            call.call.as_ref().unwrap().parent_request_id.as_deref(),
            Some("req-user")
        );
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "ping".to_string(),
                ping: Some(crate::protocol::PingMsg {
                    nonce: "while-blocked".to_string(),
                }),
                ..Default::default()
            },
        );
        let pong = read_env_raw(&mut stream);
        assert_eq!(pong.pong.unwrap().nonce, "while-blocked");
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "call_result".to_string(),
                id: call.id,
                call_result: Some(crate::protocol::CallResultMsg {
                    result: Some(serde_json::json!({"text":"ok","ok":true})),
                    error: None,
                }),
                ..Default::default()
            },
        );
        assert_eq!(
            read_env_raw(&mut stream).request_state.unwrap().state,
            "progress"
        );
        assert_eq!(
            read_env_raw(&mut stream).request_state.unwrap().state,
            "completed"
        );
        assert_eq!(read_env_raw(&mut stream).msg_type, "response");

        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("req-title".to_string()),
                request: Some(RequestMsg {
                    method: "command".to_string(),
                    tool: Some("title".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: None,
                }),
                ..Default::default()
            },
        );
        assert_eq!(
            read_env_raw(&mut stream).request_state.unwrap().state,
            "started"
        );
        let blocked = read_env_raw(&mut stream).request_state.unwrap();
        assert_eq!(
            (blocked.state.as_str(), blocked.reason.as_deref()),
            ("blocked", Some("host_call"))
        );
        let call = read_env_raw(&mut stream);
        assert_eq!(call.call.as_ref().unwrap().method, "ui.setTitle");
        assert_eq!(
            call.call.as_ref().unwrap().parent_request_id.as_deref(),
            Some("req-title")
        );
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "call_result".to_string(),
                id: call.id,
                call_result: Some(crate::protocol::CallResultMsg {
                    result: None,
                    error: None,
                }),
                ..Default::default()
            },
        );
        assert_eq!(
            read_env_raw(&mut stream).request_state.unwrap().state,
            "progress"
        );
        assert_eq!(
            read_env_raw(&mut stream).request_state.unwrap().state,
            "completed"
        );
        assert_eq!(read_env_raw(&mut stream).msg_type, "response");

        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("req-panic".to_string()),
                request: Some(RequestMsg {
                    method: "command".to_string(),
                    tool: Some("panic".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: None,
                }),
                ..Default::default()
            },
        );
        assert_eq!(
            read_env_raw(&mut stream).request_state.unwrap().state,
            "started"
        );
        assert_eq!(
            read_env_raw(&mut stream).request_state.unwrap().state,
            "completed"
        );
        let panic_response = read_env_raw(&mut stream).response.unwrap();
        assert_eq!(
            panic_response.error.unwrap().code.as_deref(),
            Some("handler_panic")
        );
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "shutdown".to_string(),
                ..Default::default()
            },
        );
        handle.join().unwrap().unwrap();
        let _ = std::fs::remove_file(sock);
    }
    #[test]
    fn set_label_returns_host_error() {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let sock = std::env::temp_dir().join(format!("pig-sdk-rs-label-{unique}.sock"));
        let _ = std::fs::remove_file(&sock);
        let listener = UnixListener::bind(&sock).unwrap();
        let mut ext = Extension::new("rust-label");
        ext.command("label", "label an entry", |ctx, _args| {
            match ctx.set_label("missing-entry", "tag") {
                Ok(()) => CommandResult::Ok,
                Err(err) => CommandResult::Error(err.to_string()),
            }
        });
        let sock_for_ext = sock.clone();
        let handle =
            std::thread::spawn(move || ext.run_with_socket(sock_for_ext.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        assert_eq!(read_env(&mut stream).msg_type, "register");
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "ready".to_string(),
                ready: Some(ReadyMsg {
                    session_name: String::new(),
                    cwd: "/tmp".to_string(),
                    mode: "tui".to_string(),
                    width: 80,
                    height: 24,
                    model: String::new(),
                    state: None,
                }),
                ..Default::default()
            },
        );
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "request".to_string(),
                id: Some("req-label".to_string()),
                request: Some(RequestMsg {
                    method: "command".to_string(),
                    tool: Some("label".to_string()),
                    event: None,
                    handler_id: 0,
                    tool_call_id: None,
                    args: None,
                }),
                ..Default::default()
            },
        );
        let response = loop {
            let env = read_env_raw(&mut stream);
            if env.msg_type == "call" {
                write_env(
                    &mut stream,
                    &Envelope {
                        msg_type: "call_result".to_string(),
                        id: env.id,
                        call_result: Some(crate::protocol::CallResultMsg {
                            result: None,
                            error: Some(crate::protocol::ErrorInfo {
                                code: None,
                                message: "Entry missing-entry not found".to_string(),
                            }),
                        }),
                        ..Default::default()
                    },
                );
                continue;
            }
            if env.msg_type == "response" {
                break env.response.unwrap();
            }
        };
        let message = response.error.expect("setLabel error was discarded").message;
        assert!(message.contains("Entry missing-entry not found"), "{message}");
        write_env(
            &mut stream,
            &Envelope {
                msg_type: "shutdown".to_string(),
                ..Default::default()
            },
        );
        handle.join().unwrap().unwrap();
        let _ = std::fs::remove_file(sock);
    }

}

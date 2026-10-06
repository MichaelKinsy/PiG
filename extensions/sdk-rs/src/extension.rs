//! Extension builder and runtime.

use crate::context::{
    Context, ModelEventStream, ModelStreams, RemoteComponents, TerminalInputSubs,
};
use crate::oauth::{OAuthLoginCallbacks, OAuthProvider, ProviderOAuthConfig};
use crate::protocol::*;
use crate::theme::UiState;
use crate::tool_render::{ToolRendererSet, 
    ToolRenderCallHandler, ToolRenderContext, ToolRenderResult, ToolRenderResultHandler,
    ToolRenderResultOptions, ToolRenderShell, ToolRenderers,
};
use crate::transport::UnixStream;
use serde_json::Value;
use std::collections::{HashMap, VecDeque};
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

    /// Sets upstream `AgentToolResult.structuredContent` (`agent/src/types.ts:433`): the machine-readable result of a tool that declares an `output_schema`. Not sent to the model; the content remains the model-facing result.
    pub fn with_structured_content(self, structured_content: Value) -> Self {
        self.with_field("structured_content", structured_content)
    }

    /// Sets upstream `AgentToolResult.details` (`agent/src/types.ts:428`).
    pub fn with_details(self, details: Value) -> Self {
        self.with_field("details", details)
    }

    /// Sets upstream `AgentToolResult.isError` (`agent/src/types.ts:440`): the failure is reported without throwing, so the model sees the content as an error result while the details and structured content stay for the UI and programmatic callers.
    pub fn with_is_error(self, is_error: bool) -> Self {
        self.with_field("is_error", Value::Bool(is_error))
    }

    /// The wire result object with one more field. `Text` is its content, `Error` its content with `is_error`, and a JSON value that is not an object is the content.
    fn with_field(self, key: &str, value: Value) -> Self {
        let mut object = match self {
            Self::Text(content) => serde_json::Map::from_iter([("content".to_string(), Value::String(content))]),
            Self::Error(content) => serde_json::Map::from_iter([
                ("content".to_string(), Value::String(content)),
                ("is_error".to_string(), Value::Bool(true)),
            ]),
            Self::Json(Value::Object(object)) => object,
            Self::Json(content) => serde_json::Map::from_iter([("content".to_string(), content)]),
        };
        object.insert(key.to_string(), value);
        Self::Json(Value::Object(object))
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

/// Upstream `ToolDefinition`, registered with [`Extension::register_tool`].
///
/// `execute` receives the JSON parameters; upstream's `onUpdate` is
/// [`Context::on_update`] and its `AbortSignal` is the request's cancellation
/// ([`Context::is_cancelled`]). The renderers return terminal lines at a
/// width, since a Pi Component cannot cross the process boundary.
pub struct ToolDefinition {
    pub name: String,
    /// Display name.
    pub label: String,
    pub description: String,
    /// One-line entry for the default system prompt's Available tools
    /// section; `None` leaves the tool out of that section.
    pub prompt_snippet: Option<String>,
    /// Bullets appended to the system prompt's Guidelines section while the
    /// tool is active.
    pub prompt_guidelines: Vec<String>,
    /// JSON Schema of the parameters.
    pub parameters: Value,
    pub constrained_sampling: Option<crate::ToolConstrainedSampling>,
    pub render_shell: ToolRenderShell,
    /// Transforms raw arguments before schema validation.
    pub prepare_arguments: Option<ToolPrepareArguments>,
    /// `"sequential"` or `"parallel"`; `None` uses the session default.
    pub execution_mode: Option<String>,
    pub execute: ToolHandler,
    pub render_call: Option<ToolRenderCallHandler>,
    pub render_result: Option<ToolRenderResultHandler>,
    /// JSON Schema of `structured_content` in successful results (upstream `outputSchema`, `types.ts:582`).
    pub output_schema: Option<Value>,
    /// How the model reaches the tool; `None` is `direct` (upstream `exposure`, `types.ts:588`).
    pub exposure: Option<crate::ToolExposure>,
    /// Group the tool belongs to (upstream `namespace`, `types.ts:591`).
    pub namespace: Option<crate::ToolNamespace>,
    /// Hints about what the tool does (upstream `annotations`, `types.ts:594`).
    pub annotations: Option<crate::ToolAnnotations>,
    /// Whether registering the tool activates it; `None` is the exposure's default (upstream `defaultActive`, `types.ts:600`).
    pub default_active: Option<bool>,
    /// Adjusts how the loadout is presented to the model while this tool is active (upstream `prepareLoadout`, `types.ts:607`).
    pub prepare_loadout: Option<crate::ToolPrepareLoadout>,
}

impl ToolDefinition {
    /// A definition with upstream's required fields; the optional ones are
    /// unset and can be assigned before registering.
    pub fn new(
        name: impl Into<String>,
        label: impl Into<String>,
        description: impl Into<String>,
        parameters: Value,
        execute: impl Fn(&Context, Value) -> ToolResult + Send + Sync + 'static,
    ) -> Self {
        Self {
            name: name.into(),
            label: label.into(),
            description: description.into(),
            prompt_snippet: None,
            prompt_guidelines: Vec::new(),
            parameters,
            constrained_sampling: None,
            render_shell: ToolRenderShell::Default,
            prepare_arguments: None,
            execution_mode: None,
            execute: Box::new(execute),
            render_call: None,
            render_result: None,
            output_schema: None,
            exposure: None,
            namespace: None,
            annotations: None,
            default_active: None,
            prepare_loadout: None,
        }
    }
}

/// Type alias for command handler functions.
pub type CommandHandler = Box<dyn Fn(&Context, &str) -> CommandResult + Send + Sync>;
/// Command argument completions (upstream getArgumentCompletions).
pub type CommandCompletionHandler =
    Box<dyn Fn(&str) -> Option<Vec<AutocompleteItem>> + Send + Sync>;

/// Type alias for event handler functions.
/// Errors are returned to the host as request failures.
pub type EventHandler =
    Box<dyn Fn(&Context, &mut Value) -> Result<Option<Value>, String> + Send + Sync>;

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

/// Pi's MarkdownTransformContext: the transcript message being rendered
/// ("user", "assistant" or "assistant-thinking"), whether it is still
/// streaming, and the width it is rendered at.
#[derive(Debug, Clone, Default, serde::Serialize, serde::Deserialize)]
pub struct MarkdownTransformContext {
    #[serde(default, rename = "messageType")]
    pub message_type: String,
    #[serde(default, rename = "isStreaming")]
    pub is_streaming: bool,
    #[serde(default, rename = "availableWidth")]
    pub available_width: u32,
}

/// Type alias for a display-only Markdown transformer (Pi's
/// MarkdownTransformer). Returning `None` keeps the input.
pub type MarkdownTransformerHandler =
    Box<dyn Fn(&str, &MarkdownTransformContext) -> Option<String> + Send + Sync>;

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
    signal: crate::ProviderSignal,
    flag: Arc<AtomicBool>,
    reason: Arc<Mutex<Option<String>>>,
    provider_stream: Arc<Mutex<Option<Arc<ModelEventStream>>>>,
}

impl RequestCancel {
    fn new() -> Self {
        Self {
            flag: Arc::new(AtomicBool::new(false)),
            reason: Arc::new(Mutex::new(None)),
            signal: crate::ProviderSignal::new(),
            provider_stream: Arc::new(Mutex::new(None)),
        }
    }

    fn cancel(&self, reason: impl Into<String>) {
        self.flag.store(true, Ordering::Relaxed);
        *self.reason.lock().unwrap() = Some(reason.into());
        self.signal.cancel();
        if let Some(stream) = self.provider_stream.lock().unwrap().as_ref() {
            stream.end(Value::Null);
        }
    }
}

#[derive(Default)]
pub(crate) struct RequestThreads {
    count: Mutex<usize>,
    idle: Condvar,
}

impl RequestThreads {
    pub(crate) fn start(&self) {
        *self.count.lock().unwrap() += 1;
    }

    pub(crate) fn finish(&self) {
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

/// Width deliveries wait here for their worker thread. A width handler may block on a host call, whose reply only the main loop reads, so handlers never run on the main loop. One worker runs the queued deliveries in the order the host sent the widths and exits when the queue is empty.
#[derive(Default)]
struct WidthDeliveries {
    state: Mutex<WidthDeliveryState>,
}

#[derive(Default)]
struct WidthDeliveryState {
    queue: VecDeque<(u32, Vec<Arc<crate::context::WidthChangeHandler>>)>,
    running: bool,
    stopped: bool,
}

impl WidthDeliveries {
    /// Queue one delivery for the handlers subscribed now; an unsubscribe after this call changes only the next delivery.
    fn submit(
        self: &Arc<Self>,
        width: u32,
        handlers: Vec<Arc<crate::context::WidthChangeHandler>>,
        threads: &Arc<RequestThreads>,
    ) {
        {
            let mut state = self.state.lock().unwrap();
            if state.stopped {
                return;
            }
            state.queue.push_back((width, handlers));
            if state.running {
                return;
            }
            state.running = true;
        }
        threads.start();
        let (deliveries, finished) = (self.clone(), threads.clone());
        let spawned = thread::Builder::new()
            .name("pig-width-change".to_string())
            .spawn(move || {
                deliveries.run();
                finished.finish();
            });
        if spawned.is_err() {
            let mut state = self.state.lock().unwrap();
            state.running = false;
            state.queue.clear();
            drop(state);
            threads.finish();
        }
    }

    fn run(&self) {
        loop {
            let (width, handlers) = {
                let mut state = self.state.lock().unwrap();
                match state.queue.pop_front() {
                    Some(delivery) if !state.stopped => delivery,
                    _ => {
                        state.queue.clear();
                        state.running = false;
                        return;
                    }
                }
            };
            for handler in handlers {
                // One handler's panic must not stop the others, as a thrown handler error rejects one call upstream.
                let _ = catch_unwind(AssertUnwindSafe(|| handler(width)));
            }
        }
    }

    fn stop(&self) {
        let mut state = self.state.lock().unwrap();
        state.stopped = true;
        state.queue.clear();
    }
}

impl Context {
    fn clone_for_request(
        &self,
        cancel: RequestCancel,
        tool_call_id: Option<String>,
        request_id: String,
    ) -> Self {
        let parent = if self.request_id == request_id { self.request_parent.clone() } else { None }
            .unwrap_or_else(|| self.conn.arm_parent(&request_id));
        Self {
            conn: self.conn.clone(),
            request_parent: Some(parent),
            tool_call_id,
            request_id,
            session_name: self.session_name.clone(),
            cwd: self.cwd.clone(),
            mode: self.mode.clone(),
            shared_width: self.shared_width.clone(),
            shared_height: self.shared_height.clone(),
            shared_model: self.shared_model.clone(),
            flag_defaults: self.flag_defaults.clone(),
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
            shared_ui: self.shared_ui.clone(),
            bus: self.bus.clone(),
            surfaces: self.surfaces.clone(),
            replacement: self.replacement,
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
    provider_objects: Arc<crate::provider::ProviderObjects>,
    provider_callbacks: Arc<crate::provider::ProviderCallbacks>,
    renderers: Vec<RendererDef>,
    entry_renderers: Vec<RendererDef>,

    tool_fns: HashMap<String, ToolHandler>,
    tool_prepare_fns: HashMap<String, ToolPrepareArguments>,
    command_fns: HashMap<String, CommandHandler>,
    event_fns: HashMap<u32, EventHandler>,
    shortcut_fns: HashMap<String, ShortcutHandler>,
    renderer_fns: HashMap<String, RendererHandler>,
    entry_renderer_fns: HashMap<String, EntryRendererHandler>,
    markdown_transformer: Option<MarkdownTransformerHandler>,
    // oauth_fns holds OAuth closures for providers registered with an OAuth
    // capability, keyed by provider name for oauth_* dispatch.
    oauth_fns: HashMap<String, OAuthProvider>,
    tool_renderers: ToolRenderers,
    command_completion_fns: HashMap<String, CommandCompletionHandler>,
    tool_prepare_loadout_fns: HashMap<String, crate::ToolPrepareLoadout>,
    mcp_servers: Vec<McpServerDecl>,
    virtual_models: Vec<Arc<crate::VirtualModel>>,
    virtual_model_unregistrations: Vec<crate::protocol::VirtualModelRef>,
    bus: Arc<crate::event_bus::BusRegistry>,
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
            provider_objects: Arc::new(crate::provider::ProviderObjects::default()),
            provider_callbacks: Arc::new(crate::provider::ProviderCallbacks::default()),
            renderers: Vec::new(),
            entry_renderers: Vec::new(),
            tool_fns: HashMap::new(),
            tool_prepare_fns: HashMap::new(),
            command_fns: HashMap::new(),
            event_fns: HashMap::new(),
            shortcut_fns: HashMap::new(),
            renderer_fns: HashMap::new(),
            entry_renderer_fns: HashMap::new(),
            markdown_transformer: None,
            oauth_fns: HashMap::new(),
            tool_renderers: ToolRenderers::default(),
            command_completion_fns: HashMap::new(),
            tool_prepare_loadout_fns: HashMap::new(),
            mcp_servers: Vec::new(),
            virtual_models: Vec::new(),
            virtual_model_unregistrations: Vec::new(),
            bus: Arc::new(crate::event_bus::BusRegistry::default()),
        }
    }

    /// Upstream's `pi.events`, shared with every other realm of the session. A listener subscribed before the extension runs is registered with the Host before the extension registers.
    pub fn events(&self) -> crate::EventBus {
        crate::EventBus::new(self.bus.clone(), None)
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
        self.tool_renderers
            .call
            .insert(name.to_string(), Box::new(handler));
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
        self.tool_renderers
            .result
            .insert(name.to_string(), Box::new(handler));
    }

    /// Returns the extension's registered name.
    pub fn name(&self) -> &str {
        &self.name
    }

    // upstream: packages/coding-agent/src/core/extensions/loader.ts:registerTool
    fn validate_tool_schema(&self, name: &str, schema: &Value) {
        assert!(schema.is_object(), "Tool \"{name}\" registered by extension \"{}\" must define an object parameter schema.", self.name);
    }

    /// Register a tool that the LLM can invoke. Panics before registration if the schema is not an object.
    pub fn tool(
        &mut self,
        name: impl Into<String>,
        description: impl Into<String>,
        schema: Value,
        handler: impl Fn(&Context, Value) -> ToolResult + Send + Sync + 'static,
    ) {
        let n = name.into();
        self.validate_tool_schema(&n, &schema);
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
            ..Default::default()
        });
        self.tool_fns.insert(n, Box::new(handler));
    }

    /// Set the registered tool's optional system-prompt summary before running the extension.
    pub fn tool_prompt_snippet(&mut self, name: &str, snippet: impl Into<String>) {
        let snippet = snippet.into();
        for tool in self.tools.iter_mut().filter(|tool| tool.name == name) {
            tool.prompt_snippet = snippet.clone();
        }
    }

    /// Upstream `pi.registerTool(definition)`. Registering a name again
    /// replaces the earlier definition in place, as upstream's tool map does.
    pub fn register_tool(&mut self, definition: ToolDefinition) {
        self.validate_tool_schema(&definition.name, &definition.parameters);
        let ToolDefinition {
            name,
            label,
            description,
            prompt_snippet,
            prompt_guidelines,
            parameters,
            constrained_sampling,
            render_shell,
            prepare_arguments,
            execution_mode,
            execute,
            render_call,
            render_result,
            output_schema,
            exposure,
            namespace,
            annotations,
            default_active,
            prepare_loadout,
        } = definition;
        let decl = ToolDef {
            name: name.clone(),
            label,
            description,
            parameters,
            constrained_sampling,
            prompt_guidelines,
            prompt_snippet: prompt_snippet.unwrap_or_default(),
            execution_mode: execution_mode.unwrap_or_default(),
            source: None,
            render_shell: (render_shell == ToolRenderShell::SelfShell).then(|| "self".to_string()),
            renders_call: render_call.is_some(),
            renders_result: render_result.is_some(),
            output_schema,
            exposure,
            namespace,
            annotations,
            default_active,
            prepares_loadout: prepare_loadout.is_some(),
            prepares_arguments: prepare_arguments.is_some(),
        };
        match self.tools.iter_mut().find(|tool| tool.name == name) {
            Some(existing) => *existing = decl,
            None => self.tools.push(decl),
        }
        self.tool_fns.insert(name.clone(), execute);
        match prepare_arguments {
            Some(prepare) => self.tool_prepare_fns.insert(name.clone(), prepare),
            None => self.tool_prepare_fns.remove(&name),
        };
        match render_call {
            Some(render) => self.tool_renderers.call.insert(name.clone(), render),
            None => self.tool_renderers.call.remove(&name),
        };
        match render_result {
            Some(render) => self.tool_renderers.result.insert(name.clone(), render),
            None => self.tool_renderers.result.remove(&name),
        };
        match prepare_loadout {
            Some(prepare) => self.tool_prepare_loadout_fns.insert(name, prepare),
            None => self.tool_prepare_loadout_fns.remove(&name),
        };
    }

    /// Register a tool with a local pre-validation argument transform. Panics before registration if the schema is not an object.
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
        if let Some(declaration) = self.tools.iter_mut().rev().find(|tool| tool.name == name) {
            declaration.prepares_arguments = true;
        }
        self.tool_prepare_fns.insert(name, Box::new(prepare));
    }

    /// Register a tool with system prompt guidelines. Panics before registration if the schema is not an object.
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
        self.validate_tool_schema(&n, &schema);
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
            ..Default::default()
        });
        self.tool_fns.insert(n, Box::new(handler));
    }

    /// Register a tool with an explicit source identifier. Panics before registration if the schema is not an object.
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
        self.validate_tool_schema(&n, &schema);
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
            ..Default::default()
        });
        self.tool_fns.insert(n, Box::new(handler));
    }

    /// Register a tool that requests provider-side constrained sampling. Panics before registration if the schema is not an object.
    /// The host forwards the request to the provider, which (for OpenAI-compatible
    /// providers) turns a grammar request into a custom grammar tool.
    /// Mirrors upstream ToolDefinition.constrainedSampling.
    pub fn tool_with_constrained_sampling(
        &mut self,
        name: impl Into<String>,
        description: impl Into<String>,
        schema: Value,
        sampling: impl Into<crate::ToolConstrainedSampling>,
        handler: impl Fn(&Context, Value) -> ToolResult + Send + Sync + 'static,
    ) {
        let n = name.into();
        self.validate_tool_schema(&n, &schema);
        self.tools.push(ToolDef {
            name: n.clone(),
            description: description.into(),
            parameters: schema,
            constrained_sampling: Some(sampling.into()),
            prompt_guidelines: Vec::new(),
            source: None,
            render_shell: None,
            renders_call: false,
            renders_result: false,
            ..Default::default()
        });
        self.tool_fns.insert(n, Box::new(handler));
    }

    /// Upstream `pi.registerMcpServer(name, config)` during load (`types.ts:1833`): registers an MCP server for the session, with the same config as an `mcpServers` entry in `mcp.json`.
    ///
    /// The host validates the config when the extension registers, as upstream validates in the call (`loader.ts:456`); an invalid config or a name another extension registered fails the load, as the factory's throw does. Registering a name again replaces this extension's earlier registration. After load, use [`Context::register_mcp_server`].
    pub fn register_mcp_server(&mut self, name: impl Into<String>, config: Value) {
        let name = name.into();
        match self.mcp_servers.iter_mut().find(|server| server.name == name) {
            Some(existing) => existing.config = config,
            None => self.mcp_servers.push(McpServerDecl { name, config }),
        }
    }

    /// Upstream `pi.unregisterMcpServer(name)` during load (`loader.ts:468-471`). Removes the server this extension queued with [`Extension::register_mcp_server`]. After load, use [`Context::unregister_mcp_server`].
    pub fn unregister_mcp_server(&mut self, name: &str) {
        self.mcp_servers.retain(|server| server.name != name);
    }

    /// Upstream `pi.registerVirtualModel(model)` during load (`types.ts:1852`). Registering the same provider and id again replaces the virtual model. After load, use [`Context::register_virtual_model`].
    pub fn register_virtual_model(&mut self, model: crate::VirtualModel) {
        let model = Arc::new(model);
        match self
            .virtual_models
            .iter_mut()
            .find(|existing| existing.provider == model.provider && existing.id == model.id)
        {
            Some(existing) => *existing = model,
            None => self.virtual_models.push(model),
        }
    }

    /// Upstream `pi.unregisterVirtualModel(provider, id)` during load (`loader.ts:492-495`). After load, use [`Context::unregister_virtual_model`].
    pub fn unregister_virtual_model(&mut self, provider: &str, id: &str) {
        self.virtual_models.retain(|model| model.provider != provider || model.id != id);
        // Upstream filters the runtime-wide queue (`loader.ts:228-232`), so the host removes another extension's queued model too.
        self.virtual_model_unregistrations.push(crate::protocol::VirtualModelRef { provider: provider.to_string(), id: id.to_string() });
    }

    /// Register a slash command.
    pub fn command(
        &mut self,
        name: impl Into<String>,
        description: impl Into<String>,
        handler: impl Fn(&Context, &str) -> CommandResult + Send + Sync + 'static,
    ) {
        let n = name.into();
        // upstream: packages/coding-agent/src/core/extensions/loader.ts:302-311. The handler is a required closure,
        // so only the name can be invalid.
        assert!(!n.is_empty(), "Command registered by extension \"{}\" must have a non-empty string name. Use pi.registerCommand(\"name\", {{ description, handler }}).", self.name);
        self.commands.push(CmdDef {
            name: n.clone(),
            description: description.into(),
            argument_completions: false,
        });
        self.command_fns.insert(n, Box::new(handler));
    }

    /// Set the argument completions of the registered command `name`
    /// (upstream RegisteredCommand.getArgumentCompletions): the items for the
    /// text after "/<command> ", or `None` for none.
    pub fn command_argument_completions(
        &mut self,
        name: &str,
        handler: impl Fn(&str) -> Option<Vec<AutocompleteItem>> + Send + Sync + 'static,
    ) {
        for command in self
            .commands
            .iter_mut()
            .filter(|command| command.name == name)
        {
            command.argument_completions = true;
        }
        self.command_completion_fns
            .insert(name.to_string(), Box::new(handler));
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

    pub fn register_native_provider(&mut self,provider:Arc<crate::Provider>)->Result<(),String>{
        let declaration=self.provider_objects.register(provider)?;
        self.unregister_provider(&declaration.name);
        self.providers.push(declaration);
        Ok(())
    }

    /// Queue a provider registration for the register payload, as Pi queues `pi.registerProvider` while the factory runs (loader.ts:213-218).
    ///
    /// The queue methods (`register_provider`, `register_provider_operations`, `register_provider_stream`, `register_oauth_provider`, `register_native_provider` and `unregister_provider`) take `&mut self`, and [`Extension::run`] consumes the extension, so no handler can reach the queue once the extension is loaded: a registration after load is a compile error, never a silent no-op. Pi's `pi.registerProvider` and `pi.unregisterProvider` after load take effect at once (types.ts:1766-1819, runner.ts:517-523); in this SDK a handler makes them through [`crate::Context::model_registry`] with `register_provider_operations`, `register_provider` and `unregister_provider`, which keep the same callbacks.
    ///
    /// ```no_run
    /// let mut ext = pig_sdk::Extension::new("late");
    /// ext.register_provider("queued", serde_json::json!({"baseUrl": "https://queued.test"}));
    /// ext.command("late", "register a provider after load", |ctx, _args| {
    ///     match ctx.model_registry().register_provider("late", serde_json::json!({"baseUrl": "https://late.test"})) {
    ///         Ok(()) => pig_sdk::CommandResult::Ok,
    ///         Err(error) => pig_sdk::CommandResult::Error(error.to_string()),
    ///     }
    /// });
    /// ext.run().unwrap();
    /// ```
    ///
    /// The same queue call after `run` does not compile:
    ///
    /// ```compile_fail
    /// let mut ext = pig_sdk::Extension::new("late");
    /// ext.run().unwrap();
    /// ext.register_provider("late", serde_json::json!({"baseUrl": "https://late.test"}));
    /// ```
    pub fn register_provider(&mut self, name: impl Into<String>, config: Value) {
        self.register_provider_operations(name, config, crate::ProviderOperations::new());
    }

    /// Registers a provider whose image and classifier implementations and `streamSimple` (upstream `ProviderConfig.images`, `classifiers` and `streamSimple`) run in this extension. The config names the models; `operations` implements their APIs.
    pub fn register_provider_operations(&mut self, name: impl Into<String>, config: Value, operations: crate::ProviderOperations) {
        let declaration = self.provider_callbacks.declare(name.into(), config, operations);
        self.providers.push(declaration);
    }

    /// Register an extension-owned provider stream with request cancellation and ordered events.
    pub fn register_provider_stream(
        &mut self,
        name: impl Into<String>,
        config: Value,
        handler: impl Fn(&Context, Value, Value, Value) -> Result<Arc<ModelEventStream>, String>
        + Send
        + Sync
        + 'static,
    ) {
        self.register_provider_operations(name, config, crate::ProviderOperations::new().stream_simple(handler));
    }

    /// Remove a previously queued provider registration. After load a handler removes a provider with [`crate::ModelRegistry::unregister_provider`]; see [`Extension::register_provider`].
    pub fn unregister_provider(&mut self, name: &str) {
        self.providers.retain(|provider| provider.name != name);
        self.provider_callbacks.restore(name, None);
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
            native: None,
            stream_simple: false,
            image_apis: Vec::new(),
            classifier_apis: Vec::new(),
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

    /// Register the extension's display-only Markdown transform (Pi's
    /// `pi.registerMarkdownTransformer`). The host applies it to user and
    /// assistant Markdown in the interactive transcript, after its own
    /// transformers; a later registration replaces an earlier one.
    pub fn markdown_transformer(
        &mut self,
        transformer: impl Fn(&str, &MarkdownTransformContext) -> Option<String> + Send + Sync + 'static,
    ) {
        self.markdown_transformer = Some(Box::new(transformer));
    }

    /// Register a tool renderer resolver (Pi's `pi.registerToolRenderer`). Resolvers run in extension load order, and
    /// an extension's resolvers in registration order. After loading, use [`crate::Context::register_tool_renderer`].
    pub fn tool_renderer(
        &mut self,
        resolver: impl Fn(&str, &dyn Fn() -> Option<ToolRendererSet>) -> Option<ToolRendererSet> + Send + Sync + 'static,
    ) {
        self.tool_renderers.resolvers.push(Box::new(resolver));
    }

    /// Register an event handler. Mutations to boundary entries are retained.
    /// Return `Some(value)` to pass data back to the host, `None` to ack.
    pub fn on_event(
        &mut self,
        event: impl Into<String>,
        can_block: bool,
        handler: impl Fn(&Context, &mut Value) -> Option<Value> + Send + Sync + 'static,
    ) {
        self.on_event_result(event, can_block, move |ctx, data| Ok(handler(ctx, data)));
    }

    /// Register an awaited event handler with surfaced errors. In-place mutations survive an error when Pi retains that event's mutable inputs.
    pub fn on_event_result(
        &mut self,
        event: impl Into<String>,
        can_block: bool,
        handler: impl Fn(&Context, &mut Value) -> Result<Option<Value>, String> + Send + Sync + 'static,
    ) {
        let e = event.into();
        let handler_id = self.handlers.len() as u32 + 1;
        self.handlers.push(HandlerDef {
            event: e,
            can_block,
            handler_id,
        });
        self.event_fns.insert(handler_id, Box::new(handler));
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

    fn pong(conn: &Connection, env: crate::protocol::Envelope<Box<serde_json::value::RawValue>>) -> io::Result<()> {
        if let Some(ping) = env.ping {
            conn.write_envelope(&crate::protocol::Envelope {
                msg_type: "pong".to_string(),
                pong: Some(crate::protocol::PongMsg { nonce: ping.nonce }),
                ..Default::default()
            })?;
        }
        Ok(())
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
        let _=conn.provider_objects.set(self.provider_objects.clone());
        let _=conn.provider_callbacks.set(self.provider_callbacks.clone());
        let bus = self.bus.clone();
        // A node factory's pi.events.on runs before its runtime registers; a native listener subscribed before run does the same.
        let mut early_dispatches: std::collections::VecDeque<crate::protocol::Envelope<Box<serde_json::value::RawValue>>> = std::collections::VecDeque::new();
        for (handler_id, channel) in bus.go_live(&conn) {
            let call = conn.begin_call_for(None, "events.on", Some(serde_json::json!({"channel": channel, "handlerId": handler_id, "value": true})))?;
            loop {
                let env = conn.read_envelope()?;
                if conn.complete_call(&env) {
                    break;
                }
                match env.msg_type.as_str() {
                    "ping" => Self::pong(&conn, env)?,
                    "request" if env.request.as_ref().is_some_and(|req| req.method == "events.dispatch") => early_dispatches.push_back(env),
                    other => return Err(io::Error::new(io::ErrorKind::InvalidData, format!("expected an events.on result, got {other}"))),
                }
            }
            let result = conn.wait_call(call)?;
            if let Some(err) = result.error {
                return Err(io::Error::other(format!("pi.events.on {channel:?}: {}", crate::context::host_error_message(err))));
            }
        }

        conn.late_tool_renderers.loaded.store(self.tool_renderers.resolvers.len(), std::sync::atomic::Ordering::SeqCst);
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
                markdown_transformer: self.markdown_transformer.is_some(),
                tool_renderers: self.tool_renderers.resolvers.len(),
                mcp_servers: self.mcp_servers.clone(),
                virtual_models: self.virtual_models.iter().map(|model| model.declaration()).collect(),
                unregister_virtual_models: self.virtual_model_unregistrations.clone(),
            }),
            ..Default::default()
        })?;

        // Wait for ready. A listener the Host dispatches to while the extension loads runs once the extension is ready, in dispatch order.
        let mut env = conn.read_envelope()?;
        loop {
            match env.msg_type.as_str() {
                "ping" => Self::pong(&conn, env)?,
                "request" if env.request.as_ref().is_some_and(|req| req.method == "events.dispatch") => early_dispatches.push_back(env),
                _ => break,
            }
            env = conn.read_envelope()?;
        }
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
        let shared_ui = Arc::new(Mutex::new(UiState::default()));
        if let Some(state) = ready.state.as_ref().and_then(|state| state.as_object()) {
            shared_ui.lock().unwrap().apply_state(state);
            conn.api_state.lock().unwrap().apply_state(state);
        }
        {
            let mut models = conn.virtual_models.lock().unwrap();
            for model in &self.virtual_models {
                models.insert((model.provider.clone(), model.id.clone()), model.clone());
            }
        }
        let overlay_seq = Arc::new(AtomicU64::new(0));
        let overlays: RemoteComponents = Arc::new(Mutex::new(HashMap::new()));
        let terminal_input: TerminalInputSubs = Arc::new(Mutex::new(Vec::new()));
        let terminal_input_seq = Arc::new(AtomicU64::new(0));
        let width_change: crate::context::WidthChangeSubs = Arc::new(Mutex::new(Vec::new()));
        let width_change_seq = Arc::new(AtomicU64::new(0));
        let model_streams: ModelStreams = self.provider_objects.streams.clone();
        let model_stream_seq = Arc::new(AtomicU64::new(0));
        let mut flag_defaults = HashMap::new();
        for flag in &self.flags {
            if let Some(value) = &flag.default {
                flag_defaults.entry(flag.name.clone()).or_insert_with(|| value.clone());
            }
        }
        let base_ctx = Context {
            request_parent: None,
            conn: conn.clone(),
            tool_call_id: None,
            request_id: String::new(),
            session_name: ready.session_name,
            cwd: ready.cwd,
            mode: ready.mode,
            shared_width,
            shared_height,
            shared_model,
            flag_defaults: Arc::new(flag_defaults),
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
            shared_ui,
            bus: bus.clone(),
            surfaces: Arc::new(Mutex::new(HashMap::new())),
            replacement: false,
        };
        let ext = Arc::new(self);
        let active_requests: Arc<Mutex<HashMap<String, RequestCancel>>> =
            Arc::new(Mutex::new(HashMap::new()));
        let request_threads = ext.provider_objects.workers.clone();
        let width_deliveries = Arc::new(WidthDeliveries::default());
        let tool_starts = crate::tool_start_order::ToolStartOrder::default();
        let stop_requests = |reason: &str| -> io::Result<()> {
            width_deliveries.stop();
            for cancel in active_requests.lock().unwrap().values() {
                cancel.cancel(reason);
            }
            conn.cancel_pending_calls();
            let drained=request_threads.wait(Duration::from_secs(2));
            conn.registered_tools.lock().unwrap().clear();
            ext.bus.reset();
            ext.provider_objects.clear();
            conn.autocomplete.clear();
            conn.close_transport();
            if drained {
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
            let env = match early_dispatches.pop_front() {
                Some(env) => env,
                None => match conn.read_envelope() {
                    Ok(e) => e,
                    Err(_) => return stop_requests("connection closed"),
                },
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
                        // agent-loop.ts:619-647: the handlers of a batch start in source order.
                        let start = (req.method == "tool_call").then(|| tool_starts.reserve());
                        let failed_start = start.clone();
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
                                    ext.handle_request(&base, &conn, &id, &req, cancel, start.as_deref())
                                }));
                                if let Some(start) = &start {
                                    start.end();
                                }
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
                            if let Some(start) = &failed_start {
                                start.release();
                            }
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
                    conn.cancel_pending_calls_for(request_id);
                    if let Some(cancel) = active_requests.lock().unwrap().get(request_id) {
                        let reason = env
                            .cancel
                            .as_ref()
                            .map(|c| c.reason.clone())
                            .unwrap_or_default();
                        cancel.cancel(reason);
                    }
                }
                "notify" => {
                    if let Some(notify) = env.notify {
                        match notify.method.as_str() {
                            "events.release" => ext.bus.release(notify.args.as_ref()),
                            "tool_render_release" => {
                                ext.tool_renderers.release(notify.args.as_ref());
                            }
                            "autocomplete.release" => {
                                if let Some(id) = notify.args.as_ref().and_then(|args| args["id"].as_str()) { conn.autocomplete.release(id); }
                            }
                            "provider_release" => {
                                if let Some(key)=notify.args.as_ref().and_then(|args|args["key"].as_str()){ext.provider_objects.release(key)}
                            }
                            "model_stream_event" => {
                                if let Some(args) = &notify.args {
                                    let stream_id = args
                                        .get("streamId")
                                        .and_then(|value| value.as_str())
                                        .unwrap_or("");
                                    let event = args.get("event").cloned().unwrap_or(Value::Null);
                                    if let Some(stream) = base_ctx
                                        .model_streams
                                        .lock()
                                        .unwrap()
                                        .get(stream_id)
                                        .cloned()
                                    {
                                        if args.get("started")==Some(&Value::Bool(true)){stream.mark_started(None)}else{stream.push(event)}
                                    }
                                }
                            }
                            "run_signal" => {
                                if let Some(args) = &notify.args {
                                    conn.apply_run_signal(args);
                                }
                            }
                            "state_update" => {
                                if let Some(args) = &notify.args {
                                    if let Some(state) =
                                        args.get("state").and_then(|s| s.as_object())
                                    {
                                        base_ctx.shared_ui.lock().unwrap().apply_state(state);
                                        conn.api_state.lock().unwrap().apply_state(state);
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
                            "theme_change" => {
                                base_ctx
                                    .shared_ui
                                    .lock()
                                    .unwrap()
                                    .apply_theme_change(notify.args.as_ref());
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
                                            if !width_subs.is_empty() {
                                                width_deliveries.submit(w as u32, width_subs, &request_threads);
                                            }
                                            base_ctx.refresh_surfaces();
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
                                let Some(args) = notify.custom_input else {
                                    continue;
                                };
                                let key = args.key.as_str();
                                let overlay = base_ctx.overlays.lock().unwrap().get(key).cloned();
                                let Some(overlay) = overlay else {
                                    continue;
                                };
                                if let Err(err) = overlay.send_input(args.data) {
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
        start: Option<&crate::tool_start_order::ToolStart>,
    ) {
        match req.method.as_str() {
            "autocomplete.sync" | "autocomplete.suggest" => {
                let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                let result = conn.autocomplete.dispatch(&ctx, req.args.clone().unwrap_or_default());
                let (value, error) = match result { Ok(value) => (Some(value), None), Err(err) => (None, Some(ErrorInfo { code: None, message: err.to_string() })) };
                let _ = conn.respond(id, value, error);
            }
            "provider_call"|"provider_stream"|"provider_sync"|"provider_object_callback"|"provider_object_callback_sync"=>{
                let result=if req.method.starts_with("provider_object_callback"){self.provider_objects.dispatch_callback(req)}else{self.provider_objects.dispatch(conn,id,req,cancel.signal)};
                let(value,error)=match result{Ok(value)=>(Some(value),None),Err(message)=>(None,Some(ErrorInfo{code:None,message}))};
                let _=conn.respond(id,value,error);
            }
            "provider_operation" => {
                // upstream: types.ts:1896-1898, an error the callback returns is the request's error.
                let result = (|| -> Result<Value, String> {
                    let args = req.args.clone().unwrap_or(Value::Null);
                    let provider = req.tool.as_deref().unwrap_or("");
                    let api = args["api"].as_str().unwrap_or("");
                    let kind = args["kind"].as_str().unwrap_or("");
                    let (images, label) = match kind {
                        "images" => (true, "image"),
                        "classifiers" => (false, "classifier"),
                        other => return Err(format!("Unknown provider operation {other:?}")),
                    };
                    let callback = self
                        .provider_callbacks
                        .operation(provider, images, api)
                        .ok_or_else(|| format!("Provider {provider} has no {label} implementation for {api:?}"))?;
                    let options = crate::ProviderOperationOptions { signal: cancel.signal.clone(), values: args["options"].clone() };
                    catch_unwind(AssertUnwindSafe(|| callback(Arc::new(args["model"].clone()), args["context"].clone(), options)))
                        .map_err(|_| format!("provider {provider} {label} implementation panicked"))?
                })();
                match result {
                    Ok(value) => {
                        let _ = conn.respond(id, Some(value), None);
                    }
                    Err(message) => {
                        let _ = conn.respond(id, None, Some(ErrorInfo { code: None, message }));
                    }
                }
            }
            "execute_tool_update" => {
                // The host waits for this answer before it sends the next partial result or the call's outcome. A panic in on_update is the callback's throw and is the answer (`nested-tool-calls.ts:219-231`).
                let args = req.args.clone().unwrap_or(Value::Null);
                let id_of_call = args.get("executeId").and_then(Value::as_str).unwrap_or("").to_string();
                let update = args.get("result").and_then(|result| serde_json::from_value::<crate::AgentToolResult>(result.clone()).ok());
                let dispatcher = conn.execute_updates.lock().unwrap().get(&id_of_call).cloned();
                let failure = match (update, dispatcher) {
                    (Some(update), Some(dispatcher)) => {
                        let (answer, answered) = std::sync::mpsc::channel();
                        // A dispatcher that ended with its call has no one left to answer.
                        if dispatcher.send((update, answer)).is_ok() { answered.recv().ok().flatten() } else { None }
                    }
                    _ => None,
                };
                let error = failure.map(|message| ErrorInfo { code: None, message });
                let _ = conn.respond(id, None, error);
            }
            "virtual_model_route" => {
                // upstream: loader.ts:485-487, the router runs with a context created per request; its signal is the request's cancellation.
                let outcome = (|| -> Result<Value, String> {
                    let args: crate::extension_api::VirtualModelRouteArgs =
                        serde_json::from_value(req.args.clone().unwrap_or(Value::Null)).map_err(|err| format!("decode virtual_model_route: {err}"))?;
                    let model = conn
                        .virtual_models
                        .lock()
                        .unwrap()
                        .get(&(args.provider.clone(), args.id.clone()))
                        .cloned()
                        .ok_or_else(|| format!("unknown virtual model {}/{}", args.provider, args.id))?;
                    let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                    let route = catch_unwind(AssertUnwindSafe(|| (model.route)(&ctx, args.request)))
                        .map_err(|_| "virtual model router panicked".to_string())??;
                    serde_json::to_value(route).map_err(|err| err.to_string())
                })();
                match outcome {
                    Ok(route) => {
                        let _ = conn.respond(id, Some(route), None);
                    }
                    Err(message) => {
                        let _ = conn.respond(id, None, Some(ErrorInfo { code: None, message }));
                    }
                }
            }
            "tool_prepare_arguments" => {
                // upstream: agent-loop.ts:707-716, prepareToolCall runs the hook on the model's arguments and validates what it returns.
                let name = req.tool.as_deref().unwrap_or("");
                let registered = conn.registered_tools.lock().unwrap().get(name).cloned();
                let hook = match &registered {
                    Some(tool) => tool.prepare_arguments.as_ref(),
                    None => self.tool_prepare_fns.get(name),
                };
                let args = req.args.clone().unwrap_or(Value::Object(Default::default()));
                let outcome = match hook {
                    None => Err(format!("tool {name} has no prepareArguments")),
                    Some(hook) => catch_unwind(AssertUnwindSafe(|| hook(args)))
                        .map_err(|_| format!("prepareArguments of {name} panicked"))
                        .and_then(|prepared| prepared),
                };
                match outcome {
                    Ok(prepared) => {
                        let _ = conn.respond(id, Some(prepared), None);
                    }
                    Err(message) => {
                        let _ = conn.respond(id, None, Some(ErrorInfo { code: None, message }));
                    }
                }
            }
            "tool_prepare_loadout" => {
                // upstream: agent-session.ts:1523, a hook that throws is reported and changes nothing.
                let name = req.tool.as_deref().unwrap_or("");
                let registered = conn.registered_tools.lock().unwrap().get(name).cloned();
                let hook = match &registered {
                    Some(tool) => tool.prepare_loadout.as_ref(),
                    None => self.tool_prepare_loadout_fns.get(name),
                };
                let outcome = match hook {
                    None => Err(format!("tool {name} has no prepareLoadout")),
                    Some(hook) => serde_json::from_value::<crate::extension_api::ToolLoadoutPayload>(req.args.clone().unwrap_or(Value::Null))
                        .map_err(|err| format!("decode tool_prepare_loadout: {err}"))
                        .and_then(|payload| {
                            let loadout = crate::ToolLoadout::from(payload);
                            catch_unwind(AssertUnwindSafe(|| hook(&loadout))).map_err(|_| format!("prepareLoadout of {name} panicked"))
                        })
                        .and_then(|changes| serde_json::to_value(changes).map_err(|err| err.to_string())),
                };
                match outcome {
                    Ok(changes) => {
                        let _ = conn.respond(id, Some(changes), None);
                    }
                    Err(message) => {
                        let _ = conn.respond(id, None, Some(ErrorInfo { code: None, message }));
                    }
                }
            }
            "events.dispatch" => {
                let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                let error = self.bus.dispatch(&ctx, req.args.as_ref().unwrap_or(&Value::Null)).err().map(|message| ErrorInfo { code: None, message });
                let _ = conn.respond(id, None, error);
            }
            "provider_stream_simple" => {
                let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                let result = (|| -> Result<Value, String> {
                    let handler = self
                        .provider_callbacks
                        .stream_simple(req.tool.as_deref().unwrap_or(""))
                        .ok_or("unknown provider stream")?;
                    let args = req.args.clone().unwrap_or(Value::Null);
                    let stream = handler(
                        &ctx,
                        args["model"].clone(),
                        args["context"].clone(),
                        args["options"].clone(),
                    )?;
                    {
                        let mut active = cancel.provider_stream.lock().unwrap();
                        *active = Some(stream.clone());
                        if ctx.is_cancelled() {
                            stream.end(Value::Null);
                        }
                    }
                    while let Some(event) = stream.next() {
                        if ctx.is_cancelled() {
                            return Err("provider stream aborted".into());
                        };
                        conn.notify(
                            "provider_stream_event",
                            Some(serde_json::json!({"request_id":id,"result":event})),
                        )
                        .map_err(|error| error.to_string())?;
                    }
                    if ctx.is_cancelled() {
                        return Err("provider stream aborted".into());
                    };
                    Ok(stream.result().unwrap_or(Value::Null))
                })();
                *cancel.provider_stream.lock().unwrap() = None;
                match result {
                    Ok(value) => {
                        let _ = conn.respond(id, Some(value), None);
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
            }
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
                let original = req.terminal_input.as_ref().map(|a| a.data.clone()).unwrap_or_default();
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
                let verdict = crate::TerminalInputResult {
                    consume,
                    data: if !consume && current != original { Some(current) } else { None },
                };
                let _ = conn.respond_serialized(id, Some(verdict), None);
            }
            "tool_call" => {
                let tool_name = req.tool.as_deref().unwrap_or("");
                let registered = conn.registered_tools.lock().unwrap().get(tool_name).cloned();
                let handler = registered.as_ref().map(|tool| &tool.execute).or_else(|| self.tool_fns.get(tool_name));
                if let Some(handler) = handler {
                    let ctx = base_ctx.clone_for_request(
                        cancel.clone(),
                        req.tool_call_id.clone(),
                        id.to_string(),
                    );
                    let args = req
                        .args
                        .clone()
                        .unwrap_or(Value::Object(Default::default()));
                    if let Some(start) = start {
                        start.begin();
                    }
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
            "with_session" => {
                let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                let error = crate::replaced_session::dispatch(&ctx, conn, req.args.as_ref()).err();
                let _ = conn.respond(id, None, error);
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
                let mut data = req
                    .args
                    .clone()
                    .unwrap_or(Value::Object(Default::default()));
                if req.event.as_deref() == Some("before_agent_start") {
                    // Pi's normalized options always carry every collection; the host omits an empty one.
                    if let Some(options) = data
                        .get_mut("systemPromptOptions")
                        .and_then(Value::as_object_mut)
                    {
                        for name in ["selectedTools", "promptGuidelines", "contextFiles", "skills"] {
                            options.entry(name).or_insert_with(|| Value::Array(Vec::new()));
                        }
                        for name in ["toolSnippets", "toolGuidelines"] {
                            options.entry(name).or_insert_with(|| Value::Object(Default::default()));
                        }
                    }
                }
                // Pi's handler edits event.input in place and the runner reads it back (runner.ts emitToolCall). The host cannot share the value, so the reply carries the input the handler left when it differs from the one it received.
                let tool_call_input_before = (req.event.as_deref() == Some("tool_call")).then(|| data.get("input").cloned());
                let result = if let Some(handler) = self.event_fns.get(&handler_id) {
                    let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                    match catch_unwind(AssertUnwindSafe(|| handler(&ctx, &mut data))) {
                        Ok(result) => result,
                        Err(payload) => Err(payload.downcast_ref::<String>().cloned()
                            .or_else(|| payload.downcast_ref::<&str>().map(|message| (*message).to_string()))
                            .unwrap_or_else(|| "extension handler panicked".to_string())),
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
                    Err(message) => (
                        None,
                        Some(ErrorInfo {
                            code: None,
                            message,
                        }),
                    ),
                };
                if req.event.as_deref() == Some("user_bash") {
                    value = crate::user_bash::user_bash_event_result(value);
                }
                // Upstream runner.ts emitContext uses `handlerResult?.messages`, or
                // event.messages when the handler edited that list in place. The
                // host cannot see an in-place edit, so send the list the handler
                // left; the host compares it by value.
                if matches!(req.event.as_deref(), Some("context" | "context_with_system")) && error.is_none() {
                    let returned = value
                        .as_ref()
                        .and_then(|result| result.get("messages"))
                        .filter(|messages| !messages.is_null())
                        .or_else(|| data.get("messages"))
                        .cloned();
                    if let Some(messages) = returned {
                        value = Some(serde_json::json!({ "messages": messages }));
                    }
                }
                if req.event.as_deref() == Some("before_agent_start") {
                    value = Some(serde_json::json!({
                        "_pigPromptSections": data.get("systemPromptOptions").and_then(|options| options.get("sections")),
                        "_pigPromptSelectedTools": data.get("systemPromptOptions").and_then(|options| options.get("selectedTools")),
                        "_pigPromptOptions": data.get("systemPromptOptions"),
                        "_pigPromptResult": value,
                    }));
                }
                if let Some(before) = tool_call_input_before {
                    let after = data.get("input").cloned();
                    let mut reply = serde_json::json!({ "_pigToolCallResult": value });
                    if after != before {
                        reply["_pigToolCallInput"] = after.unwrap_or(Value::Null);
                    }
                    value = Some(reply);
                }
                // pig additive (D19): preserve boundary mutations alongside handler errors.
                if matches!(req.event.as_deref(), Some("agent_before_settle" | "turn_end")) {
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
            "command_argument_completions" => {
                let cmd_name = req.tool.as_deref().unwrap_or("");
                if let Some(handler) = self.command_completion_fns.get(cmd_name) {
                    let prefix = req.args.as_ref().and_then(|v| v.as_str()).unwrap_or("");
                    let items = handler(prefix).filter(|items| !items.is_empty());
                    let result = items.and_then(|items| serde_json::to_value(items).ok());
                    let _ = conn.respond(id, result, None);
                } else {
                    let _ = conn.respond(
                        id,
                        None,
                        Some(ErrorInfo {
                            code: None,
                            message: format!("command {} has no getArgumentCompletions", cmd_name),
                        }),
                    );
                }
            }
            "resolve_tool_renderers" => {
                // A resolver that panics answers an error, as a resolver that throws upstream.
                match std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                    self.tool_renderers.resolve(conn, req.args.as_ref())
                })) {
                    Ok(answer) => {
                        let _ = conn.respond(id, Some(answer), None);
                    }
                    Err(_) => {
                        let _ = conn.respond(
                            id,
                            None,
                            Some(ErrorInfo { code: None, message: "tool renderer resolver panicked".to_string() }),
                        );
                    }
                }
            }
            "render_tool" => {
                let ctx = base_ctx.clone_for_request(cancel.clone(), None, id.to_string());
                let tool = req.tool.as_deref().unwrap_or("");
                match self
                    .tool_renderers
                    .render(&ctx, conn, tool, req.args.as_ref())
                {
                    Ok(lines) => {
                        let _ = conn.respond(id, Some(serde_json::json!({"lines": lines})), None);
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
            }
            "markdown_transform" => {
                // A transformer that panics keeps its input, as Pi keeps the
                // Markdown when a transformer throws.
                let args = req.args.clone().unwrap_or(Value::Null);
                let markdown = args
                    .get("markdown")
                    .and_then(|v| v.as_str())
                    .unwrap_or("")
                    .to_string();
                let context: MarkdownTransformContext = args
                    .get("context")
                    .cloned()
                    .and_then(|v| serde_json::from_value(v).ok())
                    .unwrap_or_default();
                let transformed = self.markdown_transformer.as_ref().and_then(|transformer| {
                    std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                        transformer(&markdown, &context)
                    }))
                    .ok()
                    .flatten()
                });
                let _ = conn.respond(id, transformed.map(Value::String), None);
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
    #[test]
    fn registration_preserves_sampling_false_separately_from_omission() {
        let mut ext = super::Extension::new("sampling");
        for (name, sampling) in [("unset", None), ("disabled", Some(crate::ToolConstrainedSampling::Disabled))] {
            let mut tool = super::ToolDefinition::new(name, name, name, serde_json::json!({"type":"object"}), |_, _| super::ToolResult::text("ok"));
            tool.constrained_sampling = sampling;
            ext.register_tool(tool);
        }
        let tools = serde_json::to_value(&ext.tools).unwrap();
        assert!(tools[0].get("constrained_sampling").is_none());
        assert_eq!(tools[1].get("constrained_sampling"), Some(&serde_json::json!(false)));
    }
    use super::{CommandResult, Extension, resolve_socket_path_from_env};
    use crate::protocol::{Envelope, ReadyMsg, RequestMsg};
    use std::io::{Read, Write};
    use std::os::unix::net::{UnixListener, UnixStream};
    use std::sync::mpsc;
    use std::sync::{Mutex, OnceLock};
    use std::time::{Duration, SystemTime, UNIX_EPOCH};

    #[test]
    fn tool_schema_validation_precedes_registration() {
        use serde_json::{json, Value};
        for variant in ["tool", "prepare", "guidelines", "source", "sampling"] {
            let register = |ext: &mut Extension, schema: Value| {
                let handler = |_: &crate::Context, _: Value| crate::ToolResult::text("ok");
                match variant {
                    "tool" => ext.tool("noop", "No-op", schema, handler),
                    "prepare" => ext.tool_with_prepare_arguments("noop", "No-op", schema, Ok, handler),
                    "guidelines" => ext.tool_with_guidelines("noop", "No-op", schema, vec![], handler),
                    "source" => ext.tool_with_source("noop", "No-op", schema, "source", vec![], handler),
                    "sampling" => ext.tool_with_constrained_sampling("noop", "No-op", schema, crate::ConstrainedSampling { kind: "json_schema".into(), strict: None, variants: None }, handler),
                    _ => unreachable!(),
                }
            };
            for schema in [Value::Null, json!([]), json!("object"), json!(1), json!(false)] {
                let mut ext = Extension::new("schema");
                let failed = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| register(&mut ext, schema)));
                let failure = failed.expect_err("non-object schema must fail synchronously");
                let message = failure.downcast_ref::<String>().map(String::as_str).or_else(|| failure.downcast_ref::<&str>().copied());
                assert_eq!(message, Some("Tool \"noop\" registered by extension \"schema\" must define an object parameter schema."));
                assert!(ext.tools.is_empty());
                assert!(ext.tool_fns.is_empty());
                assert!(ext.tool_prepare_fns.is_empty());
                register(&mut ext, json!({}));
                assert_eq!(ext.tools.len(), 1);
                assert_eq!(ext.tool_fns.len(), 1);
            }
        }
    }

    // .upstream/v0.99.2/packages/coding-agent/src/core/extensions/loader.ts:302-311 (#10054): a command without a
    // non-empty name fails before it registers, so the extension fails to load instead of crashing pi when `/` lists
    // its commands. A Rust handler is a required closure, so the handler check is enforced by the type.
    #[test]
    fn command_name_validation_precedes_registration() {
        let mut ext = Extension::new("commands");
        let failed = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            ext.command("", "d", |_, _| CommandResult::Ok);
        }));
        let failure = failed.expect_err("an empty command name must fail synchronously");
        let message = failure.downcast_ref::<String>().map(String::as_str).or_else(|| failure.downcast_ref::<&str>().copied());
        assert_eq!(message, Some("Command registered by extension \"commands\" must have a non-empty string name. Use pi.registerCommand(\"name\", { description, handler })."));
        assert!(ext.commands.is_empty());
        assert!(ext.command_fns.is_empty());
        ext.command("noop", "d", |_, _| CommandResult::Ok);
        assert_eq!(ext.commands.len(), 1);
        assert_eq!(ext.command_fns.len(), 1);
    }

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
                    terminal_input: None,
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
                    terminal_input: None,
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
                    terminal_input: None,
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
                    terminal_input: None,
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
                    terminal_input: None,
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
                    terminal_input: None,
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
                    terminal_input: None,
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
                    terminal_input: None,
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
                    terminal_input: None,
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
                    terminal_input: None,
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
                    terminal_input: None,
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
                    terminal_input: None,
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
        let message = response
            .error
            .expect("setLabel error was discarded")
            .message;
        assert!(
            message.contains("Entry missing-entry not found"),
            "{message}"
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

    fn request(id: &str, method: &str, tool: &str, args: Option<serde_json::Value>) -> Envelope {
        Envelope {
            msg_type: "request".to_string(),
            id: Some(id.to_string()),
            request: Some(RequestMsg {
                terminal_input: None,
                method: method.to_string(),
                tool: Some(tool.to_string()),
                event: None,
                handler_id: 0,
                tool_call_id: Some(format!("call-{id}")),
                args,
            }),
            ..Default::default()
        }
    }

    fn notify(method: &str, args: serde_json::Value) -> Envelope {
        Envelope {
            msg_type: "notify".to_string(),
            notify: Some(crate::protocol::NotifyMsg {
                custom_input: None,
                method: method.to_string(),
                args: Some(args),
            }),
            ..Default::default()
        }
    }

    #[test]
    fn register_tool_declares_the_definition_and_replicates_ui_state() {
        use super::{ToolDefinition, ToolResult};
        use crate::tool_render::ToolRenderShell;
        use serde_json::json;

        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let sock = std::env::temp_dir().join(format!("pig-sdk-rs-regtool-{unique}.sock"));
        let _ = std::fs::remove_file(&sock);
        let listener = UnixListener::bind(&sock).unwrap();

        let mut ext = Extension::new("rust-register-tool");
        let mut definition = ToolDefinition::new(
            "greet",
            "Greeter",
            "Greets someone",
            json!({"type": "object", "properties": {"name": {"type": "string"}}}),
            |_ctx, params| {
                ToolResult::text(format!(
                    "hello {}",
                    params.get("name").and_then(|v| v.as_str()).unwrap_or("")
                ))
            },
        );
        definition.prompt_snippet = Some("greet: say hello".to_string());
        definition.prompt_guidelines = vec!["Use greet to say hello.".to_string()];
        definition.execution_mode = Some("sequential".to_string());
        definition.render_shell = ToolRenderShell::SelfShell;
        definition.prepare_arguments = Some(Box::new(|mut args| {
            args["name"] = json!("prepared");
            Ok(args)
        }));
        definition.render_call = Some(Box::new(|_ctx, _args, _render, _width| Ok(vec![])));
        ext.register_tool(definition);

        let (seen_tx, seen_rx) = mpsc::channel();
        ext.command("ui", "report ui state", move |ctx, _args| {
            let theme = ctx.theme();
            let _ = seen_tx.send((
                ctx.has_ui(),
                theme.name.clone(),
                theme.source_path.clone(),
                theme.fg("accent", "x"),
                theme.get_color_mode(),
            ));
            CommandResult::Ok
        });

        let sock_for_ext = sock.clone();
        let handle =
            std::thread::spawn(move || ext.run_with_socket(sock_for_ext.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        let register = read_env(&mut stream).register.unwrap();
        let wire = serde_json::to_value(&register.tools[0]).unwrap();
        assert_eq!(
            wire,
            json!({
                "name": "greet",
                "label": "Greeter",
                "description": "Greets someone",
                "parameters": {"type": "object", "properties": {"name": {"type": "string"}}},
                "prompt_guidelines": ["Use greet to say hello."],
                "prompt_snippet": "greet: say hello",
                "execution_mode": "sequential",
                "render_shell": "self",
                "renders_call": true,
                "prepares_arguments": true
            })
        );

        write_env(
            &mut stream,
            &Envelope {
                msg_type: "ready".to_string(),
                ready: Some(ReadyMsg {
                    session_name: String::new(),
                    cwd: "/tmp".to_string(),
                    mode: "print".to_string(),
                    width: 80,
                    height: 24,
                    model: String::new(),
                    state: Some(json!({
                        "hasUI": false,
                        "theme": {"name": "light", "sourcePath": "/t/light.json",
                                  "foregrounds": {"accent": "<a>"}, "backgrounds": {},
                                  "modifiers": true, "mode": "256color"}
                    })),
                }),
                ..Default::default()
            },
        );

        write_env(
            &mut stream,
            &request("r1", "tool_call", "greet", Some(json!({"name": "raw"}))),
        );
        let response = read_env(&mut stream);
        assert_eq!(
            response.response.unwrap().result.unwrap(),
            // agent-loop.ts:707-716: the host ran prepareArguments (tool_prepare_arguments) before it validated, so tool_call does not run it again.
            json!({"content": "hello raw"})
        );

        write_env(&mut stream, &request("r2", "command", "ui", None));
        assert_eq!(read_env(&mut stream).msg_type, "response");
        assert_eq!(
            seen_rx.recv_timeout(Duration::from_secs(2)).unwrap(),
            (
                false,
                "light".to_string(),
                Some("/t/light.json".to_string()),
                "<a>x\x1b[39m".to_string(),
                "256color".to_string()
            )
        );

        write_env(
            &mut stream,
            &notify(
                "theme_change",
                json!({"name": "dark", "foregrounds": {"accent": "<d>"}, "backgrounds": {},
                       "modifiers": true, "mode": "truecolor"}),
            ),
        );
        write_env(
            &mut stream,
            &notify("state_update", json!({"state": {"hasUI": true}})),
        );
        write_env(&mut stream, &request("r3", "command", "ui", None));
        assert_eq!(read_env(&mut stream).msg_type, "response");
        assert_eq!(
            seen_rx.recv_timeout(Duration::from_secs(2)).unwrap(),
            (
                true,
                "dark".to_string(),
                None,
                "<d>x\x1b[39m".to_string(),
                "truecolor".to_string()
            )
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
}

#[cfg(test)]
#[path = "handler_dispatch_tests.rs"]
mod handler_dispatch_tests;

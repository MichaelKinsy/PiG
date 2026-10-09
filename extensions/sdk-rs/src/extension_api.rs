//! Upstream 0.99.1 extension API additions: tool exposure and loadout, nested
//! tool calls, MCP server and virtual model registration, and the state the
//! host replicates for `pi.getSettings()`, `pi.getMcpServers()` and `ctx.tools`.
//!
//! Sources: `.upstream/v0.99.1/packages/coding-agent/src/core/extensions/types.ts`,
//! `runner.ts`, `loader.ts` and `core/virtual-models.ts`. The wire shapes are
//! `protocol_extension_api.go` and `protocol.go` in the host's subprocess package.

use crate::context::Context;
use crate::provider::ProviderSignal;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashMap;
use std::sync::Arc;

/// Upstream `ToolExposure` (`types.ts:509`): how the model reaches a tool.
///
/// "Callable" means callable from other tools through [`Context::execute_tool`].
///
/// * `Direct`: declared to the model while active, and callable while active.
/// * `ModelOnly`: declared to the model while active, never callable.
/// * `Codemode`: callable whenever registered; not declared to the model unless explicitly activated.
/// * `Deferred`: like `Codemode`, but codemode tools do not list it; tool search can find it.
/// * `Hidden`: registered but unreachable.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum ToolExposure {
    #[default]
    #[serde(rename = "direct")]
    Direct,
    #[serde(rename = "model-only")]
    ModelOnly,
    #[serde(rename = "codemode")]
    Codemode,
    #[serde(rename = "deferred")]
    Deferred,
    #[serde(rename = "hidden")]
    Hidden,
}

impl ToolExposure {
    /// The wire and upstream spelling.
    pub fn as_str(self) -> &'static str {
        match self {
            ToolExposure::Direct => "direct",
            ToolExposure::ModelOnly => "model-only",
            ToolExposure::Codemode => "codemode",
            ToolExposure::Deferred => "deferred",
            ToolExposure::Hidden => "hidden",
        }
    }
}

/// Upstream `ToolAnnotations` (`types.ts:515`): unverified hints about what a tool does, with the meaning of MCP tool annotations.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct ToolAnnotations {
    #[serde(default, rename = "readOnlyHint", skip_serializing_if = "Option::is_none")]
    pub read_only_hint: Option<bool>,
    #[serde(default, rename = "destructiveHint", skip_serializing_if = "Option::is_none")]
    pub destructive_hint: Option<bool>,
    #[serde(default, rename = "idempotentHint", skip_serializing_if = "Option::is_none")]
    pub idempotent_hint: Option<bool>,
    #[serde(default, rename = "openWorldHint", skip_serializing_if = "Option::is_none")]
    pub open_world_hint: Option<bool>,
}

/// Upstream `ToolNamespace` (`types.ts:527`): a group of related tools, such as the tools of one MCP server.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct ToolNamespace {
    /// For example `mcp__docs`.
    pub name: String,
    /// A short summary shown once with the group in model-facing tool listings.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
    /// Longer usage guidance, such as MCP server instructions. Not part of tool listings; tools that describe the
    /// namespace on request (codemode's `describeNamespace()`) return it.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub instructions: Option<String>,
}

/// The read-only view of a tool that [`Context::tools`] and [`ToolLoadout`] list: upstream `AgentTool` without `execute`.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct AgentTool {
    pub name: String,
    #[serde(default)]
    pub label: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub parameters: Value,
    #[serde(default, rename = "outputSchema", skip_serializing_if = "Option::is_none")]
    pub output_schema: Option<Value>,
    #[serde(default, rename = "constrainedSampling", skip_serializing_if = "Option::is_none")]
    pub constrained_sampling: Option<Value>,
    #[serde(default, rename = "executionMode", skip_serializing_if = "Option::is_none")]
    pub execution_mode: Option<String>,
}

/// Upstream `ToolLoadout` (`types.ts:541`): the tools of a session as a tool's `prepare_loadout` sees them.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct ToolLoadout {
    /// Tools declared to the model (the active tools), in order, with their original descriptions.
    pub declared: Vec<AgentTool>,
    /// Tools callable through [`Context::execute_tool`].
    pub callable: Vec<AgentTool>,
    /// Every registered tool.
    pub registered: Vec<AgentTool>,
    exposures: HashMap<String, ToolExposure>,
    namespaces: HashMap<String, ToolNamespace>,
    prompt_guidelines: HashMap<String, Vec<String>>,
}

impl ToolLoadout {
    /// A loadout with the exposure, namespace and prompt guideline tables the host sent.
    pub fn new(
        declared: Vec<AgentTool>,
        callable: Vec<AgentTool>,
        registered: Vec<AgentTool>,
        exposures: HashMap<String, ToolExposure>,
        namespaces: HashMap<String, ToolNamespace>,
        prompt_guidelines: HashMap<String, Vec<String>>,
    ) -> Self {
        Self { declared, callable, registered, exposures, namespaces, prompt_guidelines }
    }

    /// Upstream `getExposure`: a tool the host does not list is `direct`, as `_getToolExposure` defaults (`agent-session.ts:1481`).
    pub fn get_exposure(&self, name: &str) -> ToolExposure {
        self.exposures.get(name).copied().unwrap_or_default()
    }

    /// Upstream `getNamespace`: `None` for a tool without a namespace.
    pub fn get_namespace(&self, name: &str) -> Option<&ToolNamespace> {
        self.namespaces.get(name)
    }

    /// Upstream `getPromptGuidelines`: a tool's `promptGuidelines` as the system prompt has them (trimmed, without
    /// duplicates); empty for a tool without any. Hidden declarations leave them out of the system prompt.
    pub fn get_prompt_guidelines(&self, name: &str) -> &[String] {
        self.prompt_guidelines.get(name).map(Vec::as_slice).unwrap_or_default()
    }
}

/// Upstream `ToolLoadoutChanges` (`types.ts:554`): what `prepare_loadout` changes about what the model sees.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct ToolLoadoutChanges {
    /// Model-facing descriptions of declared tools, by tool name.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub descriptions: Option<HashMap<String, String>>,
    /// Declared tools whose declarations requests leave out. They stay active and callable.
    #[serde(default, rename = "hiddenDeclarations", skip_serializing_if = "Option::is_none")]
    pub hidden_declarations: Option<Vec<String>>,
}

/// Upstream `ToolDefinition.prepareLoadout` (`types.ts:607`). `None` leaves the loadout as it is (upstream returns `undefined`).
pub type ToolPrepareLoadout = Box<dyn Fn(&ToolLoadout) -> Option<ToolLoadoutChanges> + Send + Sync>;

/// Upstream `AgentToolCall`: the tool call block a nested call ran for. `id` is `<calling id>/<n>`.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct AgentToolCall {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub arguments: Value,
}

/// Upstream `AgentToolResult` in Pi's JSON shape.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct AgentToolResult {
    /// Text and image blocks returned to the model.
    #[serde(default)]
    pub content: Vec<Value>,
    #[serde(default)]
    pub details: Value,
    /// Machine-readable result matching the tool's `outputSchema`.
    #[serde(default, rename = "structuredContent", skip_serializing_if = "Option::is_none")]
    pub structured_content: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub usage: Option<Value>,
    /// The failure was reported without throwing.
    #[serde(default, rename = "isError")]
    pub is_error: bool,
    #[serde(default)]
    pub terminate: bool,
}

/// Upstream `AgentToolCallOutcome`: the final outcome of a nested call after the hooks ran.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct AgentToolCallOutcome {
    #[serde(default, rename = "toolCall")]
    pub tool_call: AgentToolCall,
    #[serde(default)]
    pub result: AgentToolResult,
    #[serde(default, rename = "isError")]
    pub is_error: bool,
    /// Milliseconds `execute()` took, measured with a monotonic clock; absent when the tool did not run (`types.ts:454`).
    #[serde(default, rename = "durationMs")]
    pub duration_ms: Option<i64>,
}

/// Partial results of a nested tool, in order.
pub type ExecuteToolUpdate = Arc<dyn Fn(AgentToolResult) + Send + Sync>;

/// Upstream `ExecuteToolOptions` (`types.ts:367`).
#[derive(Clone, Default)]
pub struct ExecuteToolOptions {
    /// Cancels the nested call. Defaults to the calling tool's own cancellation.
    pub signal: Option<ProviderSignal>,
    /// Receives partial results of the nested tool, in addition to `tool_execution_update` events.
    pub on_update: Option<ExecuteToolUpdate>,
}

/// A server an extension registered with `pi.registerMcpServer()`. Upstream `RegisteredMcpServer`.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct RegisteredMcpServer {
    pub name: String,
    /// The validated `mcpServers` entry.
    #[serde(default)]
    pub config: Value,
    /// The path of the extension that registered the server.
    #[serde(default, rename = "extensionPath")]
    pub extension_path: String,
}

/// Upstream `ModelRouteRequest` (`virtual-models.ts:55`). The router's `signal` is [`Context::is_cancelled`].
#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
pub struct ModelRouteRequest {
    /// The selected virtual model.
    #[serde(default)]
    pub model: Value,
    #[serde(default, rename = "thinkingLevel")]
    pub thinking_level: String,
    /// `user`, `continuation`, `retry` or `direct`.
    #[serde(default)]
    pub reason: String,
    /// Physical model and thinking level of the latest successful response in `messages`.
    #[serde(default)]
    pub previous: Option<Value>,
    /// The failed request of a `retry`.
    #[serde(default)]
    pub failed: Option<Value>,
    /// Router state last returned on this session branch; `None` before the first state and for `direct` requests.
    #[serde(default)]
    pub state: Option<Value>,
    #[serde(default)]
    pub messages: Vec<Value>,
}

/// Upstream `ModelRoute` (`virtual-models.ts:75`): the physical model and thinking level for one request.
#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub struct ModelRoute {
    /// The physical model, as a Model object with at least `provider` and `id`.
    pub model: Value,
    #[serde(rename = "thinkingLevel")]
    pub thinking_level: String,
    /// New router state; `None` keeps the current state.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state: Option<Value>,
}

/// Upstream `ExtensionVirtualModel.route` (`types.ts:1866`).
pub type ModelRouteFn = Box<dyn Fn(&Context, ModelRouteRequest) -> Result<ModelRoute, String> + Send + Sync>;

/// Upstream `ExtensionVirtualModel`: a selectable catalog entry that routes each request to a physical model.
pub struct VirtualModel {
    /// Provider the virtual model is listed under. May be a provider with physical models.
    pub provider: String,
    /// Model id. Must not be the id of a physical model of `provider`.
    pub id: String,
    pub name: String,
    /// Thinking levels offered for selection. Defaults to `["off"]`.
    pub thinking_levels: Option<Vec<String>>,
    /// Limits shown before the first response; unset limits are unknown (0).
    pub context_window: Option<u64>,
    pub max_tokens: Option<u64>,
    /// Input types accepted for selection (`text`, `image`).
    pub input: Option<Vec<String>>,
    pub route: ModelRouteFn,
}

impl VirtualModel {
    /// A virtual model with upstream's required fields.
    pub fn new(
        provider: impl Into<String>,
        id: impl Into<String>,
        name: impl Into<String>,
        route: impl Fn(&Context, ModelRouteRequest) -> Result<ModelRoute, String> + Send + Sync + 'static,
    ) -> Self {
        Self {
            provider: provider.into(),
            id: id.into(),
            name: name.into(),
            thinking_levels: None,
            context_window: None,
            max_tokens: None,
            input: None,
            route: Box::new(route),
        }
    }

    /// The wire declaration: every field but `route`, which stays in the extension.
    pub(crate) fn declaration(&self) -> crate::protocol::VirtualModelDecl {
        crate::protocol::VirtualModelDecl {
            provider: self.provider.clone(),
            id: self.id.clone(),
            name: self.name.clone(),
            thinking_levels: self.thinking_levels.clone().unwrap_or_default(),
            context_window: self.context_window.unwrap_or(0),
            max_tokens: self.max_tokens.unwrap_or(0),
            input: self.input.clone().unwrap_or_default(),
        }
    }
}

/// Registered virtual models by `(provider, id)`, shared by the load-time and post-load registrations.
pub(crate) type VirtualModels = std::sync::Mutex<HashMap<(String, String), Arc<VirtualModel>>>;

/// State the host replicates for synchronous getters: `pi.getSettings()` and `pi.getMcpServers()` (`types.ts:1708`, `1839`). `ctx.tools` asks the host when it is read (`runner.ts:958-961`).
#[derive(Debug, Clone, Default)]
pub(crate) struct ApiState {
    pub(crate) settings: Option<Value>,
    pub(crate) mcp_servers: Vec<RegisteredMcpServer>,
}

impl ApiState {
    /// Applies a state snapshot (the ready state or a `state_update`): each field present replaces the replica. The host encodes an empty Go slice as `null`, which is an empty list; a field that does not decode leaves the replica as it was.
    pub(crate) fn apply_state(&mut self, state: &serde_json::Map<String, Value>) {
        if let Some(settings) = state.get("settings").filter(|settings| settings.is_object()) {
            self.settings = Some(settings.clone());
        }
        if let Some(servers) = decode_list(state.get("mcpServers")) {
            self.mcp_servers = servers;
        }
    }
}

fn decode_list<T: serde::de::DeserializeOwned>(value: Option<&Value>) -> Option<Vec<T>> {
    match value? {
        Value::Null => Some(Vec::new()),
        list => serde_json::from_value(list.clone()).ok(),
    }
}

/// The host's reply to `registerMcpServer` and `unregisterMcpServer`: every registered server after the call, in registration order.
#[derive(Debug, Deserialize)]
pub(crate) struct McpServersResult {
    #[serde(default)]
    pub(crate) servers: Vec<RegisteredMcpServer>,
}

/// The `executeTool` call argument (`protocol_extension_api.go` `ExecuteToolArgs`).
#[derive(Serialize)]
pub(crate) struct ExecuteToolArgs {
    #[serde(rename = "callerId")]
    pub(crate) caller_id: String,
    pub(crate) name: String,
    pub(crate) args: Value,
    #[serde(rename = "executeId")]
    pub(crate) execute_id: String,
    #[serde(rename = "wantsUpdates", skip_serializing_if = "std::ops::Not::not")]
    pub(crate) wants_updates: bool,
    /// The caller passed `options.signal`: the nested tool runs with it instead of the calling tool's (runner.ts:979-981).
    #[serde(rename = "ownSignal", skip_serializing_if = "std::ops::Not::not")]
    pub(crate) own_signal: bool,
}

/// The `tool_prepare_loadout` request argument (`ToolLoadoutPayload`).
#[derive(Deserialize)]
pub(crate) struct ToolLoadoutPayload {
    #[serde(default)]
    declared: Vec<AgentTool>,
    #[serde(default)]
    callable: Vec<AgentTool>,
    #[serde(default)]
    registered: Vec<AgentTool>,
    #[serde(default)]
    exposures: HashMap<String, ToolExposure>,
    #[serde(default)]
    namespaces: HashMap<String, ToolNamespace>,
    #[serde(default, rename = "promptGuidelines")]
    prompt_guidelines: HashMap<String, Vec<String>>,
}

impl From<ToolLoadoutPayload> for ToolLoadout {
    fn from(payload: ToolLoadoutPayload) -> Self {
        ToolLoadout::new(payload.declared, payload.callable, payload.registered, payload.exposures, payload.namespaces, payload.prompt_guidelines)
    }
}

/// The `virtual_model_route` request argument (`VirtualModelRouteArgs`).
#[derive(Deserialize)]
pub(crate) struct VirtualModelRouteArgs {
    #[serde(default)]
    pub(crate) provider: String,
    #[serde(default)]
    pub(crate) id: String,
    #[serde(default)]
    pub(crate) request: ModelRouteRequest,
}

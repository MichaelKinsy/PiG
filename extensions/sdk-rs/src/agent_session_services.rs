//! Pi's `createAgentSessionServices` and `createAgentSessionFromServices` (core/agent-session-services.ts).
//!
//! The function builds the objects of one session cwd as Pi's does (`ModelRuntime.create`, `SettingsManager.create`, `new DefaultResourceLoader` and its `reload`), then asks the host for the two steps that
//! need its state: the resolved paths, and the registrations and extension flag values of the extensions the loader loaded (agent-session-services.ts:138-139 and :157-193).
//!
//! Async contract (Pi's promises, kept as blocking calls): both functions return once the host has finished; a failure is the `Err`. Pi's `modelRuntimeSignal` is the call itself: a cancelled request ends the
//! creation, as it does `ModelRuntime::create`.

use crate::context::Context;
use crate::graph::call_static;
use crate::{
    AgentSession, CreateAgentSessionOptions, DefaultResourceLoader, DefaultResourceLoaderOptions, ModelRuntime, ModelRuntimeCreateOptions, SessionManager, SessionStartEvent, SettingsManager,
    ToolDefinition,
};
use serde_json::{json, Value};
use std::io;
use std::path::Path;
use std::sync::Arc;

/// Pi's ResourceLoaderReloadOptions: `resolve_project_trust` takes the bootstrap extension set and answers whether the project is trusted.
#[derive(Default)]
pub struct ResourceLoaderReloadOptions {
    pub resolve_project_trust: Option<Arc<dyn Fn(Value) -> bool + Send + Sync>>,
}

/// Pi's `CreateAgentSessionServicesOptions`. `None` and an empty list are Pi's undefined. The `cwd`, `agent_dir` and `settings_manager` of `resource_loader_options` are the services' own, as in Pi.
#[derive(Default)]
pub struct CreateAgentSessionServicesOptions {
    pub cwd: String,
    pub agent_dir: Option<String>,
    pub settings_manager: Option<Arc<SettingsManager>>,
    pub model_runtime: Option<Arc<ModelRuntime>>,
    /// Pi's `extensionFlagValues` Map<string, boolean | string> as `(name, value)` pairs in command-line order (`true` for a boolean flag, a string for a string flag), which is the order of the diagnostics.
    pub extension_flag_values: Vec<(String, Value)>,
    pub resource_loader_options: Option<DefaultResourceLoaderOptions<'static>>,
    pub resource_loader_reload_options: Option<ResourceLoaderReloadOptions>,
}

/// Pi's `AgentSessionServices`: the coherent cwd-bound services of one session cwd. The session itself is created separately ([`create_agent_session_from_services`]). `diagnostics` are Pi's
/// AgentSessionRuntimeDiagnostic list: objects with `type` ("info", "warning" or "error") and `message`.
pub struct AgentSessionServices {
    pub cwd: String,
    pub agent_dir: String,
    pub model_runtime: Arc<ModelRuntime>,
    pub settings_manager: Arc<SettingsManager>,
    pub resource_loader: DefaultResourceLoader,
    pub diagnostics: Vec<Value>,
}

fn join(dir: &str, name: &str) -> String {
    Path::new(dir).join(name).to_string_lossy().into_owned()
}

/// `createAgentSessionServices(options)`: cwd-bound services plus the diagnostics found while creating them. It does not create an AgentSession.
pub fn create_agent_session_services(context: &Context, options: CreateAgentSessionServicesOptions) -> io::Result<AgentSessionServices> {
    let paths = call_static(context, "AgentSessionServices", "resolve", vec![json!(options.cwd), json!(options.agent_dir)])?;
    let text = |key: &str| paths[key].as_str().unwrap_or("").to_string();
    let (cwd, agent_dir) = (text("cwd"), text("agentDir"));
    let model_runtime = match options.model_runtime {
        Some(runtime) => runtime,
        None => Arc::new(ModelRuntime::create(
            context,
            ModelRuntimeCreateOptions { auth_path: Some(join(&agent_dir, "auth.json")), models_path: Some(join(&agent_dir, "models.json")), ..Default::default() },
        )?),
    };
    let settings_manager = match options.settings_manager {
        Some(settings) => settings,
        None => Arc::new(SettingsManager::create(context, &cwd, Some(&agent_dir), None)?),
    };
    let mut loader_options = options.resource_loader_options.unwrap_or_default();
    loader_options.cwd = cwd.clone();
    loader_options.agent_dir = agent_dir.clone();
    loader_options.settings_manager = Some(&settings_manager);
    let resource_loader = DefaultResourceLoader::new(context, loader_options)?;
    resource_loader.reload(options.resource_loader_reload_options.and_then(|reload| reload.resolve_project_trust))?;
    let flags: Vec<Value> = options.extension_flag_values.into_iter().map(|(name, value)| json!([name, value])).collect();
    let diagnostics = call_static(
        context,
        "AgentSessionServices",
        "apply",
        vec![json!(model_runtime.handle()), json!(settings_manager.handle()), json!(resource_loader.handle()), json!(cwd), json!(agent_dir), Value::Array(flags)],
    )?;
    Ok(AgentSessionServices {
        cwd,
        agent_dir,
        model_runtime,
        settings_manager,
        resource_loader,
        diagnostics: diagnostics.as_array().cloned().unwrap_or_default(),
    })
}

/// Pi's `CreateAgentSessionFromServicesOptions`. `None` and an empty list are Pi's undefined.
pub struct CreateAgentSessionFromServicesOptions<'a> {
    pub services: &'a AgentSessionServices,
    pub session_manager: &'a SessionManager,
    pub session_start_event: Option<SessionStartEvent>,
    pub model: Option<Value>,
    pub thinking_level: Option<String>,
    pub scoped_models: Option<Value>,
    pub tools: Option<Vec<String>>,
    pub exclude_tools: Option<Vec<String>>,
    pub no_tools: Option<String>,
    pub custom_tools: Vec<ToolDefinition>,
}

/// `createAgentSessionFromServices(options)`: an AgentSession over previously created services, which keeps session creation separate so the caller can resolve the model, thinking level and tools against the
/// target cwd first.
pub fn create_agent_session_from_services(context: &Context, options: CreateAgentSessionFromServicesOptions) -> io::Result<AgentSession> {
    let services = options.services;
    context.create_agent_session(CreateAgentSessionOptions {
        cwd: Some(services.cwd.clone()),
        agent_dir: Some(services.agent_dir.clone()),
        model_runtime: Some(services.model_runtime.handle().to_string()),
        settings_manager: Some(services.settings_manager.handle().to_string()),
        resource_loader: Some(services.resource_loader.handle().to_string()),
        session_manager: Some(options.session_manager.handle().to_string()),
        model: options.model,
        thinking_level: options.thinking_level,
        scoped_models: options.scoped_models,
        tools: options.tools,
        exclude_tools: options.exclude_tools,
        no_tools: options.no_tools,
        custom_tools: options.custom_tools,
        session_start_event: options.session_start_event,
        ..Default::default()
    })
}

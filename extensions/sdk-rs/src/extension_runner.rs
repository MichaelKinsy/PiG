//! Pi's `ExtensionRunner` class (core/extensions/runner.ts): the dispatcher and registry over the extensions a `DefaultResourceLoader` loaded. The host owns the runner and the extensions stay in their own processes, as a loader's do; the runner dispatches events to them in extension order with Pi's chaining and error isolation, and answers the registries (tools, commands, flags, shortcuts, handlers) from them.
//!
//! Pi's runner is built over `Extension` objects that hold the extensions' functions. Here an extension is named by a `LoadedExtension`, the data `DefaultResourceLoader::get_extensions` returns, and an extension built in this process (an inline factory) cannot take part: it would run here while the runner runs in the host. Load it through `DefaultResourceLoaderOptions::additional_extension_paths`.
//!
//! Events and their results cross as Pi's JSON values (`serde_json::Value`). The functions of `bind_core`, `bind_command_context` and `set_ui_context` run in this process, called by the host when an extension asks for the action; they may call the runner. Each call blocks until Pi's would settle; the reads are calls on the host's state, so a read after a write sees the write.

use crate::context::{CompactOptions, Context};
use crate::extension_api::ExecuteToolOptions;
use crate::extension_runtime::ExtensionRuntime;
use crate::graph::{callback_id, GraphCallback, GraphObject};
use crate::model_runtime::ModelRuntime;
use crate::provider::ProviderSignal;
use crate::resource_loader::LoadedExtension;
use crate::session_manager_class::SessionManager;
use serde_json::{json, Map, Value};
use std::io;
use std::sync::{Arc, Mutex};

/// A bound function that takes no argument.
pub type Fn0<R> = Arc<dyn Fn() -> Result<R, String> + Send + Sync>;
/// A bound function that takes one argument.
pub type Fn1<A, R> = Arc<dyn Fn(A) -> Result<R, String> + Send + Sync>;
/// A bound function that takes two arguments.
pub type Fn2<A, B, R> = Arc<dyn Fn(A, B) -> Result<R, String> + Send + Sync>;

/// `ExtensionActions` of Pi's `bindCore`: the functions the extensions' `pi.sendMessage` and the rest call. `None` is Pi's undefined: the function is not bound, and the runtime's own default applies. A function that returns `Err` is Pi's thrown error. Messages, options, models and tool lists are Pi's JSON values.
#[derive(Default, Clone)]
pub struct ExtensionActions {
    pub send_message: Option<Fn2<Value, Option<Value>, ()>>,
    pub send_user_message: Option<Fn2<Value, Option<Value>, ()>>,
    pub append_entry: Option<Fn2<String, Value, ()>>,
    pub set_session_name: Option<Fn1<String, ()>>,
    pub get_session_name: Option<Fn0<String>>,
    pub set_label: Option<Fn2<String, Option<String>, ()>>,
    pub get_active_tools: Option<Fn0<Vec<String>>>,
    pub get_all_tools: Option<Fn0<Value>>,
    pub get_settings: Option<Fn0<Value>>,
    pub set_active_tools: Option<Fn1<Vec<String>, ()>>,
    pub refresh_tools: Option<Fn0<()>>,
    pub get_commands: Option<Fn0<Value>>,
    pub set_model: Option<Fn1<Value, bool>>,
    pub get_thinking_level: Option<Fn0<String>>,
    pub set_thinking_level: Option<Fn1<String, ()>>,
}

/// `ExtensionContextActions` of Pi's `bindCore`: the functions the contexts read. `None` is Pi's undefined. `get_signal` answers the active run's signal, `None` for undefined.
#[derive(Default, Clone)]
pub struct ExtensionContextActions {
    pub get_model: Option<Fn0<Value>>,
    pub get_scoped_models: Option<Fn0<Value>>,
    pub is_idle: Option<Fn0<bool>>,
    pub is_project_trusted: Option<Fn0<bool>>,
    pub get_signal: Option<Fn0<Option<ProviderSignal>>>,
    pub abort: Option<Fn0<()>>,
    pub has_pending_messages: Option<Fn0<bool>>,
    pub shutdown: Option<Fn0<()>>,
    pub get_context_usage: Option<Fn0<Value>>,
    /// Receives the options of the extension that asked (its callbacks when this process asked through a context).
    pub compact: Option<Arc<dyn Fn(CompactOptions) -> Result<(), String> + Send + Sync>>,
    pub get_system_prompt: Option<Fn0<String>>,
    pub get_system_prompt_options: Option<Fn0<Value>>,
    /// Runs the nested call: the arguments are the calling tool call's id, the tool name, its arguments and the options; the result is the outcome `{toolCall, result, isError}`. A call an extension of the runner makes passes no signal or update function (they run in that extension's process).
    pub execute_tool: Option<Arc<dyn Fn(String, String, Value, ExecuteToolOptions) -> Result<Value, String> + Send + Sync>>,
    pub get_callable_tools: Option<Fn0<Value>>,
}

/// The `providerActions` argument of Pi's `bindCore`: how a provider an extension registers reaches the model registry. `None` leaves the registry's own.
#[derive(Default, Clone)]
pub struct ProviderActions {
    pub register_provider: Option<Fn2<String, Value, ()>>,
    pub unregister_provider: Option<Fn1<String, ()>>,
}

/// The result of a session replacement command: `{cancelled}`.
#[derive(Debug, Clone, Copy, Default, PartialEq, serde::Serialize, serde::Deserialize)]
pub struct CancelledResult {
    pub cancelled: bool,
}

/// A function of this process that sets up the new session's log, or runs with the new session's context.
pub type WithSessionCallback = Arc<dyn Fn(&ExtensionCommandContext) -> io::Result<()> + Send + Sync>;
/// A function of this process that prepares the new session's log before it is used.
pub type SetupCallback = Arc<dyn Fn(&SessionManager) -> io::Result<()> + Send + Sync>;

/// The options of Pi's `newSession`. `setup` and `with_session` are functions of the process that asked for the replacement: a runner extension's request cannot carry them, and fails.
#[derive(Default, Clone)]
pub struct CommandNewSessionOptions {
    pub parent_session: Option<String>,
    pub setup: Option<SetupCallback>,
    pub with_session: Option<WithSessionCallback>,
}

/// The options of Pi's `fork`: `position` is "before" or "at".
#[derive(Default, Clone)]
pub struct ForkOptions {
    pub position: Option<String>,
    pub with_session: Option<WithSessionCallback>,
}

/// The options of Pi's `navigateTree`: `{summarize?, customInstructions?, replaceInstructions?, label?}`.
pub type NavigateTreeOptions = Value;

/// The options of Pi's `switchSession`.
#[derive(Default, Clone)]
pub struct SwitchSessionOptions {
    pub with_session: Option<WithSessionCallback>,
}

/// `ExtensionCommandContextActions` of Pi's `bindCommandContext`. `None` is not bound, and Pi's default applies (resolve at once, or not cancelled).
#[derive(Default, Clone)]
pub struct ExtensionCommandContextActions {
    pub wait_for_idle: Option<Fn0<()>>,
    pub new_session: Option<Fn1<CommandNewSessionOptions, CancelledResult>>,
    pub fork: Option<Fn2<String, ForkOptions, CancelledResult>>,
    pub navigate_tree: Option<Fn2<String, NavigateTreeOptions, CancelledResult>>,
    pub switch_session: Option<Fn2<String, SwitchSessionOptions, CancelledResult>>,
    pub reload: Option<Fn0<()>>,
}

/// Pi's `ExtensionUIContext` as the functions of this process that `set_ui_context` installs. `None` is Pi's no-op one. The members that take or return TUI components (setFooter, setHeader, custom, setEditorComponent, addAutocompleteProvider, theme and the theme functions, onTerminalInput) hold objects of the process that draws them and are not served.
#[derive(Default, Clone)]
pub struct ExtensionUIContext {
    pub select: Option<Fn2<(String, Vec<String>), Option<Value>, Option<String>>>,
    pub confirm: Option<Fn2<(String, String), Option<Value>, bool>>,
    pub input: Option<Fn2<(String, Option<String>), Option<Value>, Option<String>>>,
    pub notify: Option<Fn2<String, Option<String>, ()>>,
    pub set_status: Option<Fn2<String, Option<String>, ()>>,
    pub set_working_message: Option<Fn1<Option<String>, ()>>,
    pub set_working_visible: Option<Fn1<bool, ()>>,
    pub set_hidden_thinking_label: Option<Fn1<Option<String>, ()>>,
    pub set_widget: Option<Fn2<(String, Vec<String>), Option<Value>, ()>>,
    pub set_title: Option<Fn1<String, ()>>,
    pub paste_to_editor: Option<Fn1<String, ()>>,
    pub set_editor_text: Option<Fn1<String, ()>>,
    pub get_editor_text: Option<Fn0<String>>,
    pub editor: Option<Fn2<String, Option<String>, Option<String>>>,
    pub get_tools_expanded: Option<Fn0<bool>>,
    pub set_tools_expanded: Option<Fn1<bool, ()>>,
}

fn arg<T: serde::de::DeserializeOwned>(args: &[Value], index: usize) -> Result<T, String> {
    let value = args.get(index).cloned().unwrap_or(Value::Null);
    serde_json::from_value(value).map_err(|error| format!("argument {}: {error}", index + 1))
}

fn named(table: &mut Map<String, Value>, context: &Context, name: &str, callback: GraphCallback) {
    table.insert(name.to_string(), callback_id(context, Some(callback)));
}

fn to_value<T: serde::Serialize>(value: T) -> Result<Value, String> {
    serde_json::to_value(value).map_err(|error| error.to_string())
}

struct Bound {
    context_actions: ExtensionContextActions,
    command_actions: Option<ExtensionCommandContextActions>,
}

struct Inner {
    object: GraphObject,
    session_manager: Option<Arc<SessionManager>>,
    model_registry: Option<Arc<ModelRuntime>>,
    bound: Mutex<Bound>,
}

/// Pi's `ExtensionRunner`. Dropping it releases the host's reference; the loaders it dispatches through stay alive while it does.
pub struct ExtensionRunner {
    inner: Arc<Inner>,
}

impl ExtensionRunner {
    /// `new ExtensionRunner(extensions, runtime, cwd, sessionManager, modelRegistry)`. `extensions` are the `LoadedExtension` values of a `get_extensions` result, `runtime` its `runtime`. The `session_manager` is the log `ctx.sessionManager` exposes and the `model_registry` the registry `ctx.modelRegistry` reads and provider registration applies to; Pi's ModelRegistry class is served as `ModelRuntime`. A `None` runtime is the runtime the extensions share; a `None` session manager or model registry is not bound.
    pub fn new(
        context: &Context,
        extensions: &[LoadedExtension],
        runtime: Option<&ExtensionRuntime>,
        cwd: &str,
        session_manager: Option<Arc<SessionManager>>,
        model_registry: Option<Arc<ModelRuntime>>,
    ) -> io::Result<ExtensionRunner> {
        let mut refs = Vec::with_capacity(extensions.len());
        for extension in extensions {
            if extension.loader().is_empty() {
                return Err(io::Error::other(format!("extension {} was not read from a DefaultResourceLoader: an extension built in this process cannot join the runner, load it with additional_extension_paths", extension["path"])));
            }
            refs.push(json!({"loader": extension.loader(), "resolvedPath": extension["resolvedPath"]}));
        }
        let mut wire = json!({"extensions": refs, "cwd": cwd});
        if let Some(runtime) = runtime {
            wire["runtime"] = json!(runtime.handle());
        }
        if let Some(manager) = &session_manager {
            wire["sessionManager"] = json!(manager.handle());
        }
        if let Some(registry) = &model_registry {
            wire["modelRegistry"] = json!(registry.handle());
        }
        let object = GraphObject::construct(context, "ExtensionRunner", "new", vec![wire])?;
        Ok(ExtensionRunner {
            inner: Arc::new(Inner { object, session_manager, model_registry, bound: Mutex::new(Bound { context_actions: ExtensionContextActions::default(), command_actions: None }) }),
        })
    }

    /// The host's name for the runner.
    pub fn handle(&self) -> &str {
        self.inner.object.handle()
    }

    fn call(&self, method: &str, args: Vec<Value>) -> io::Result<Value> {
        self.inner.object.call(method, args)
    }

    fn call_async(&self, method: &str, args: Vec<Value>) -> io::Result<Value> {
        self.inner.object.call_async(method, args)
    }

    /// `bindCore(actions, contextActions, providerActions?)`: it installs the functions the extensions' actions and contexts call, and applies the provider registrations the extensions queued while loading (to `provider_actions`, or to the model registry the runner was built with).
    pub fn bind_core(&self, actions: ExtensionActions, context_actions: ExtensionContextActions, provider_actions: Option<ProviderActions>) -> io::Result<()> {
        let context = self.inner.object.context();
        let (mut wire_actions, mut wire_context, mut wire_providers) = (Map::new(), Map::new(), Map::new());
        macro_rules! bind0 {
            ($table:expr, $field:expr, $name:expr, |$f:ident| $body:expr) => {
                if let Some($f) = $field.clone() {
                    named(&mut $table, context, $name, Arc::new(move |_args: Vec<Value>| $body));
                }
            };
        }
        macro_rules! bind {
            ($table:expr, $field:expr, $name:expr, |$f:ident, $args:ident| $body:expr) => {
                if let Some($f) = $field.clone() {
                    named(&mut $table, context, $name, Arc::new(move |$args: Vec<Value>| $body));
                }
            };
        }
        bind!(wire_actions, actions.send_message, "sendMessage", |f, a| f(arg(&a, 0)?, arg(&a, 1)?).map(|()| Value::Null));
        bind!(wire_actions, actions.send_user_message, "sendUserMessage", |f, a| f(arg(&a, 0)?, arg(&a, 1)?).map(|()| Value::Null));
        bind!(wire_actions, actions.append_entry, "appendEntry", |f, a| f(arg(&a, 0)?, arg(&a, 1)?).map(|()| Value::Null));
        bind!(wire_actions, actions.set_session_name, "setSessionName", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        bind0!(wire_actions, actions.get_session_name, "getSessionName", |f| f().map(Value::from));
        bind!(wire_actions, actions.set_label, "setLabel", |f, a| f(arg(&a, 0)?, arg(&a, 1)?).map(|()| Value::Null));
        bind0!(wire_actions, actions.get_active_tools, "getActiveTools", |f| f().map(Value::from));
        bind0!(wire_actions, actions.get_all_tools, "getAllTools", |f| f());
        bind0!(wire_actions, actions.get_settings, "getSettings", |f| f());
        bind!(wire_actions, actions.set_active_tools, "setActiveTools", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        bind0!(wire_actions, actions.refresh_tools, "refreshTools", |f| f().map(|()| Value::Null));
        bind0!(wire_actions, actions.get_commands, "getCommands", |f| f());
        bind!(wire_actions, actions.set_model, "setModel", |f, a| f(arg(&a, 0)?).map(Value::from));
        bind0!(wire_actions, actions.get_thinking_level, "getThinkingLevel", |f| f().map(Value::from));
        bind!(wire_actions, actions.set_thinking_level, "setThinkingLevel", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        bind0!(wire_context, context_actions.get_model, "getModel", |f| f());
        bind0!(wire_context, context_actions.get_scoped_models, "getScopedModels", |f| f());
        bind0!(wire_context, context_actions.is_idle, "isIdle", |f| f().map(Value::from));
        bind0!(wire_context, context_actions.is_project_trusted, "isProjectTrusted", |f| f().map(Value::from));
        bind0!(wire_context, context_actions.get_signal, "getSignal", |f| f().map(|signal| match signal {
            Some(signal) => json!({"aborted": signal.is_cancelled()}),
            None => Value::Null,
        }));
        bind0!(wire_context, context_actions.abort, "abort", |f| f().map(|()| Value::Null));
        bind0!(wire_context, context_actions.has_pending_messages, "hasPendingMessages", |f| f().map(Value::from));
        bind0!(wire_context, context_actions.shutdown, "shutdown", |f| f().map(|()| Value::Null));
        bind0!(wire_context, context_actions.get_context_usage, "getContextUsage", |f| f());
        bind!(wire_context, context_actions.compact, "compact", |f, a| {
            let options: Value = arg(&a, 0)?;
            f(CompactOptions { custom_instructions: options["customInstructions"].as_str().map(str::to_string), on_complete: None, on_error: None }).map(|()| Value::Null)
        });
        bind0!(wire_context, context_actions.get_system_prompt, "getSystemPrompt", |f| f().map(Value::from));
        bind0!(wire_context, context_actions.get_system_prompt_options, "getSystemPromptOptions", |f| f());
        bind!(wire_context, context_actions.execute_tool, "executeTool", |f, a| f(arg(&a, 0)?, arg(&a, 1)?, arg(&a, 2)?, ExecuteToolOptions::default()));
        bind0!(wire_context, context_actions.get_callable_tools, "getCallableTools", |f| f());
        if let Some(providers) = &provider_actions {
            bind!(wire_providers, providers.register_provider, "registerProvider", |f, a| f(arg(&a, 0)?, arg(&a, 1)?).map(|()| Value::Null));
            bind!(wire_providers, providers.unregister_provider, "unregisterProvider", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        }
        let mut args = vec![Value::Object(wire_actions), Value::Object(wire_context)];
        if provider_actions.is_some() {
            args.push(Value::Object(wire_providers));
        }
        self.call("bindCore", args)?;
        self.inner.bound.lock().unwrap().context_actions = context_actions;
        Ok(())
    }

    /// `bindCommandContext(actions?)`. `None` is Pi's undefined: it restores the defaults (waitForIdle and reload resolve, a session replacement is not cancelled).
    pub fn bind_command_context(&self, actions: Option<ExtensionCommandContextActions>) -> io::Result<()> {
        let context = self.inner.object.context();
        let mut wire = Map::new();
        if let Some(actions) = &actions {
            if let Some(f) = actions.wait_for_idle.clone() {
                named(&mut wire, context, "waitForIdle", Arc::new(move |_| f().map(|()| Value::Null)));
            }
            if let Some(f) = actions.new_session.clone() {
                named(&mut wire, context, "newSession", Arc::new(move |a| {
                    let options: Value = arg(&a, 0)?;
                    let result = f(CommandNewSessionOptions { parent_session: options["parentSession"].as_str().map(str::to_string), setup: None, with_session: None })?;
                    to_value(result)
                }));
            }
            if let Some(f) = actions.fork.clone() {
                named(&mut wire, context, "fork", Arc::new(move |a| {
                    let options: Value = arg(&a, 1)?;
                    to_value(f(arg(&a, 0)?, ForkOptions { position: options["position"].as_str().map(str::to_string), with_session: None })?)
                }));
            }
            if let Some(f) = actions.navigate_tree.clone() {
                named(&mut wire, context, "navigateTree", Arc::new(move |a| to_value(f(arg(&a, 0)?, arg(&a, 1)?)?)));
            }
            if let Some(f) = actions.switch_session.clone() {
                named(&mut wire, context, "switchSession", Arc::new(move |a| to_value(f(arg(&a, 0)?, SwitchSessionOptions::default())?)));
            }
            if let Some(f) = actions.reload.clone() {
                named(&mut wire, context, "reload", Arc::new(move |_| f().map(|()| Value::Null)));
            }
        }
        let args = if actions.is_some() { vec![Value::Object(wire)] } else { vec![] };
        self.call("bindCommandContext", args)?;
        self.inner.bound.lock().unwrap().command_actions = actions;
        Ok(())
    }

    /// `onError(listener)`: the listener runs, in this process, for each error the runner reports (`{extensionPath, event, error, stack?}`). The returned function removes it.
    pub fn on_error(&self, listener: Arc<dyn Fn(Value) + Send + Sync>) -> io::Result<Box<dyn FnOnce() + Send>> {
        let context = self.inner.object.context();
        let id = callback_id(
            context,
            Some(Arc::new(move |args: Vec<Value>| {
                listener(args.into_iter().next().unwrap_or(Value::Null));
                Ok(Value::Null)
            })),
        );
        let handle = self.call("onError", vec![id])?;
        let inner = self.inner.clone();
        Ok(Box::new(move || {
            let _ = inner.object.call("offError", vec![handle]);
        }))
    }

    /// `emitError(error)`: it reports `error` (`{extensionPath, event, error, stack?}`) to the runner's listeners.
    pub fn emit_error(&self, error: Value) -> io::Result<()> {
        self.call("emitError", vec![error]).map(|_| ())
    }

    /// `getExtensionPaths()`: the path of each extension, in load order.
    pub fn get_extension_paths(&self) -> io::Result<Vec<String>> {
        Ok(self.call("getExtensionPaths", vec![])?.as_array().map(|list| list.iter().filter_map(|path| path.as_str().map(str::to_string)).collect()).unwrap_or_default())
    }

    /// `getAllRegisteredTools()`: each extension's tools in load order, the first registration of a name winning, as `[{definition, sourceInfo}]`.
    pub fn get_all_registered_tools(&self) -> io::Result<Value> {
        self.call("getAllRegisteredTools", vec![])
    }

    /// `getToolDefinition(toolName)`: the definition's data members, null for Pi's undefined.
    pub fn get_tool_definition(&self, tool_name: &str) -> io::Result<Value> {
        self.call("getToolDefinition", vec![json!(tool_name)])
    }

    /// `getFlags()`: the flags the extensions registered as an object by name, the first registration of a name winning.
    pub fn get_flags(&self) -> io::Result<Value> {
        self.call("getFlags", vec![])
    }

    /// `setFlagValue(name, value)`: `value` is a boolean or a string.
    pub fn set_flag_value(&self, name: &str, value: Value) -> io::Result<()> {
        self.call("setFlagValue", vec![json!(name), value]).map(|_| ())
    }

    /// `getFlagValues()`: a copy of the runtime's flag values as an object, listed by name.
    pub fn get_flag_values(&self) -> io::Result<Value> {
        self.call("getFlagValues", vec![])
    }

    /// `getShortcuts(resolvedKeybindings)`: the extensions' shortcuts under their normalized keys, as `[[key, {shortcut, description?, extensionPath}]]` in Pi's Map order. `resolved_keybindings` maps each action to a key or a list of keys. A conflict with a built-in or another extension's shortcut is reported by `get_shortcut_diagnostics`.
    pub fn get_shortcuts(&self, resolved_keybindings: Value) -> io::Result<Value> {
        self.call("getShortcuts", vec![resolved_keybindings])
    }

    /// `getShortcutDiagnostics()`: the conflicts the last `get_shortcuts` found.
    pub fn get_shortcut_diagnostics(&self) -> io::Result<Value> {
        self.call("getShortcutDiagnostics", vec![])
    }

    /// `hasHandlers(eventType)`.
    pub fn has_handlers(&self, event_type: &str) -> io::Result<bool> {
        Ok(self.call("hasHandlers", vec![json!(event_type)])?.as_bool().unwrap_or(false))
    }

    /// `getMessageRenderer(customType)`: whether an extension registered one. The function runs in the extension; Pi's lookup returns a function or undefined.
    pub fn get_message_renderer(&self, custom_type: &str) -> io::Result<bool> {
        Ok(self.call("getMessageRenderer", vec![json!(custom_type)])?.as_bool().unwrap_or(false))
    }

    /// `getEntryRenderer(customType)`: whether an extension registered one.
    pub fn get_entry_renderer(&self, custom_type: &str) -> io::Result<bool> {
        Ok(self.call("getEntryRenderer", vec![json!(custom_type)])?.as_bool().unwrap_or(false))
    }

    /// `getMarkdownTransformers()`: how many Markdown transformers extensions registered, one per extension, in load order.
    pub fn get_markdown_transformers(&self) -> io::Result<usize> {
        Ok(self.call("getMarkdownTransformers", vec![])?.as_u64().unwrap_or(0) as usize)
    }

    /// `resolveToolRenderers(toolName, base)`: the renderers of calls to `tool_name`, as `{renderCall, renderResult, renderShell?}` (which renderers exist), the extensions' resolvers in load order and then `base`, a function of this process that answers the renderers to use when no extension chose. The result is null when no renderers apply.
    pub fn resolve_tool_renderers(&self, tool_name: &str, base: Option<Arc<dyn Fn() -> Value + Send + Sync>>) -> io::Result<Value> {
        let id = callback_id(self.inner.object.context(), base.map(|base| Arc::new(move |_: Vec<Value>| Ok(base())) as GraphCallback));
        self.call("resolveToolRenderers", vec![json!(tool_name), id])
    }

    /// `getModelRegistry()`: the registry the runner was built with.
    pub fn get_model_registry(&self) -> Option<Arc<ModelRuntime>> {
        self.inner.model_registry.clone()
    }

    /// `getRegisteredCommands()`: each extension's commands in load order, a name two extensions registered invoked as `name:1` and `name:2`.
    pub fn get_registered_commands(&self) -> io::Result<Value> {
        self.call("getRegisteredCommands", vec![])
    }

    /// `getCommandDiagnostics()`.
    pub fn get_command_diagnostics(&self) -> io::Result<Value> {
        self.call("getCommandDiagnostics", vec![])
    }

    /// `getCommand(name)`: the command invoked under `name`, null for Pi's undefined.
    pub fn get_command(&self, name: &str) -> io::Result<Value> {
        self.call("getCommand", vec![json!(name)])
    }

    /// `shutdown()`: it calls the shutdown function bound by `bind_core`.
    pub fn shutdown(&self) -> io::Result<()> {
        self.call("shutdown", vec![]).map(|_| ())
    }

    /// `getActiveTools()`: the active tool names the bound action answers. It fails before the runner is bound, and after it is invalidated.
    pub fn get_active_tools(&self) -> io::Result<Vec<String>> {
        Ok(self.call("getActiveTools", vec![])?.as_array().map(|list| list.iter().filter_map(|name| name.as_str().map(str::to_string)).collect()).unwrap_or_default())
    }

    /// `invalidate(message?)`: later uses of the runner's contexts fail with `message`, or Pi's default one when it is `None`. A second call keeps the first message.
    pub fn invalidate(&self, message: Option<&str>) -> io::Result<()> {
        self.call("invalidate", vec![json!(message)]).map(|_| ())
    }

    /// `reportUnhandledMcpServers()`: it reports through the error listeners each registered MCP server that no loaded extension handles.
    pub fn report_unhandled_mcp_servers(&self) -> io::Result<()> {
        self.call("reportUnhandledMcpServers", vec![]).map(|_| ())
    }

    /// `hasUI()`: whether a UI context is set.
    pub fn has_ui(&self) -> io::Result<bool> {
        Ok(self.call("hasUI", vec![])?.as_bool().unwrap_or(false))
    }

    /// `setUIContext(uiContext?, mode = "print")`. `None` is Pi's undefined: no UI. `mode` is "tui", "rpc", "json" or "print"; empty is "print". The runner wraps the dialogs of the UI so each reports ui_prompt_start and ui_prompt_end to the extensions.
    pub fn set_ui_context(&self, ui: Option<ExtensionUIContext>, mode: &str) -> io::Result<()> {
        let wire = match &ui {
            Some(ui) => Value::Object(self.ui_wire(ui)),
            None => Value::Null,
        };
        self.call("setUIContext", vec![wire, json!(mode)]).map(|_| ())
    }

    fn ui_wire(&self, ui: &ExtensionUIContext) -> Map<String, Value> {
        let context = self.inner.object.context();
        let mut wire = Map::new();
        macro_rules! bind {
            ($field:expr, $name:expr, |$f:ident, $a:ident| $body:expr) => {
                if let Some($f) = $field.clone() {
                    named(&mut wire, context, $name, Arc::new(move |$a: Vec<Value>| $body));
                }
            };
        }
        bind!(ui.select, "select", |f, a| f((arg(&a, 0)?, arg(&a, 1)?), arg(&a, 2)?).map(Value::from));
        bind!(ui.confirm, "confirm", |f, a| f((arg(&a, 0)?, arg(&a, 1)?), arg(&a, 2)?).map(Value::from));
        bind!(ui.input, "input", |f, a| f((arg(&a, 0)?, arg(&a, 1)?), arg(&a, 2)?).map(Value::from));
        bind!(ui.notify, "notify", |f, a| f(arg(&a, 0)?, arg(&a, 1)?).map(|()| Value::Null));
        bind!(ui.set_status, "setStatus", |f, a| f(arg(&a, 0)?, arg(&a, 1)?).map(|()| Value::Null));
        bind!(ui.set_working_message, "setWorkingMessage", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        bind!(ui.set_working_visible, "setWorkingVisible", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        bind!(ui.set_hidden_thinking_label, "setHiddenThinkingLabel", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        bind!(ui.set_widget, "setWidget", |f, a| f((arg(&a, 0)?, arg(&a, 1)?), arg(&a, 2)?).map(|()| Value::Null));
        bind!(ui.set_title, "setTitle", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        bind!(ui.paste_to_editor, "pasteToEditor", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        bind!(ui.set_editor_text, "setEditorText", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        bind!(ui.get_editor_text, "getEditorText", |f, _a| f().map(Value::from));
        bind!(ui.editor, "editor", |f, a| f(arg(&a, 0)?, arg(&a, 1)?).map(Value::from));
        bind!(ui.get_tools_expanded, "getToolsExpanded", |f, _a| f().map(Value::from));
        bind!(ui.set_tools_expanded, "setToolsExpanded", |f, a| f(arg(&a, 0)?).map(|()| Value::Null));
        wire
    }

    /// `getUIContext()`: the runner's UI context. The calls go through the runner, which wraps the dialogs of the context `set_ui_context` installed (or Pi's no-op context) so they report ui_prompt_start and ui_prompt_end.
    pub fn get_ui_context(&self) -> RunnerUIContext {
        RunnerUIContext { inner: self.inner.clone() }
    }

    /// `createContext()`.
    pub fn create_context(&self) -> ExtensionContext {
        ExtensionContext { inner: self.inner.clone(), spec: json!({"kind": "context"}) }
    }

    /// `createToolContext(toolCallId, signal)`: `signal` is the default cancellation of nested calls; `None` is Pi's undefined.
    pub fn create_tool_context(&self, tool_call_id: &str, signal: Option<ProviderSignal>) -> ExtensionToolContext {
        ExtensionToolContext { context: ExtensionContext { inner: self.inner.clone(), spec: json!({"kind": "tool", "toolCallId": tool_call_id}) }, tool_call_id: tool_call_id.to_string(), signal }
    }

    /// `createCommandContext()`.
    pub fn create_command_context(&self) -> ExtensionCommandContext {
        ExtensionCommandContext { context: ExtensionContext { inner: self.inner.clone(), spec: json!({"kind": "command"}) } }
    }

    /// `emit(event)`: `event` is Pi's event object (a RunnerEmitEvent: session_start, agent_start, message_update and the other events that have no emit method of their own). The result is what the chain answered, which is non-null for the session_before_* events only.
    pub fn emit(&self, event: Value) -> io::Result<Value> {
        self.call_async("emit", vec![event])
    }

    /// `emitToolCall(event)`: a tool_call event, chained so each handler sees the input the earlier ones edited; the result is `{block, reason}` when a handler blocked, null otherwise.
    pub fn emit_tool_call(&self, event: Value) -> io::Result<Value> {
        self.call_async("emitToolCall", vec![event])
    }

    /// `emitToolResult(event)`: a tool_result event whose handlers' edits chain; the result is the edits (`{content, details, isError, ...}`), null when none.
    pub fn emit_tool_result(&self, event: Value) -> io::Result<Value> {
        self.call_async("emitToolResult", vec![event])
    }

    /// `emitInput(text, images, source, streamingBehavior?)`: the result is Pi's InputEventResult, `{action: "continue"}`, `{action: "transform", text, images?}` or `{action: "handled"}`.
    pub fn emit_input(&self, text: &str, images: Option<Value>, source: &str, streaming_behavior: Option<&str>) -> io::Result<Value> {
        self.call_async("emitInput", vec![json!(text), images.unwrap_or(Value::Null), json!(source), json!(streaming_behavior)])
    }

    /// `emitContext(messages)`: the messages the `context` handlers left.
    pub fn emit_context(&self, messages: Value) -> io::Result<Value> {
        self.call_async("emitContext", vec![messages])
    }

    /// `emitBeforeAgentStart(trigger, systemPromptOptions)`: `trigger` is what starts the run, `{messageType: "user", prompt, images?}` or `{messageType: "custom", message}`. The result is `{messages, systemPromptOptions}`: the handlers' custom messages and the options their system prompt edits left, with the prompt a handler set as forceSystemPrompt.
    pub fn emit_before_agent_start(&self, trigger: Value, system_prompt_options: Value) -> io::Result<Value> {
        self.call_async("emitBeforeAgentStart", vec![trigger, system_prompt_options])
    }

    /// `emitBeforeProviderRequest(payload)`: the payload the handlers left.
    pub fn emit_before_provider_request(&self, payload: Value) -> io::Result<Value> {
        self.call_async("emitBeforeProviderRequest", vec![payload])
    }

    /// `emitBeforeProviderHeaders(headers)`: the headers the handlers left; a null value deletes a header.
    pub fn emit_before_provider_headers(&self, headers: Value) -> io::Result<Value> {
        self.call_async("emitBeforeProviderHeaders", vec![headers])
    }

    /// `emitUserBash(event)`: the result is a handler's `{result}`, null when none.
    pub fn emit_user_bash(&self, event: Value) -> io::Result<Value> {
        self.call_async("emitUserBash", vec![event])
    }

    /// `emitMessageEnd(event)`: the message a handler replaced the finalized one with, null when none did.
    pub fn emit_message_end(&self, event: Value) -> io::Result<Value> {
        self.call_async("emitMessageEnd", vec![event])
    }

    /// `emitResourcesDiscover(cwd, reason)`: the result is `{skillPaths, promptPaths, themePaths}`, each path with the extension that gave it.
    pub fn emit_resources_discover(&self, cwd: &str, reason: &str) -> io::Result<Value> {
        self.call_async("emitResourcesDiscover", vec![json!(cwd), json!(reason)])
    }

    /// `emitCacheWarmingDecision(event)`: the event's own action, "warm" or "stop", unless a handler overrides it; the last override wins.
    pub fn emit_cache_warming_decision(&self, event: Value) -> io::Result<String> {
        Ok(self.call_async("emitCacheWarmingDecision", vec![event])?.as_str().unwrap_or_default().to_string())
    }

    /// `emitBoundary(baseEvent, buildContext)`: `base_event` is a turn_end or agent_before_settle event without entries, continue and context; `build_context` is a function of this process that answers the context preview for a list of drafted entries. The result is `{entries, continue, context, valid}`.
    pub fn emit_boundary(&self, base_event: Value, build_context: Arc<dyn Fn(Value) -> Result<Value, String> + Send + Sync>) -> io::Result<Value> {
        let id = callback_id(self.inner.object.context(), Some(Arc::new(move |args: Vec<Value>| build_context(args.into_iter().next().unwrap_or(Value::Null)))));
        self.call_async("emitBoundary", vec![base_event, id])
    }
}

/// The runner's UI context, Pi's `runner.getUIContext()`: the calls go through the runner.
pub struct RunnerUIContext {
    inner: Arc<Inner>,
}

impl RunnerUIContext {
    fn member_call(&self, member: &str, mut args: Vec<Value>) -> io::Result<Value> {
        args.insert(0, json!(member));
        self.inner.object.call_async("ui", args)
    }

    fn text(value: Value) -> Option<String> {
        value.as_str().map(str::to_string)
    }

    /// `ui.select(title, options, opts?)`; `None` is the dialog's undefined (cancelled).
    pub fn select(&self, title: &str, options: &[String], opts: Option<Value>) -> io::Result<Option<String>> {
        self.member_call("select", vec![json!(title), json!(options), opts.unwrap_or(Value::Null)]).map(Self::text)
    }
    /// `ui.confirm(title, message, opts?)`.
    pub fn confirm(&self, title: &str, message: &str, opts: Option<Value>) -> io::Result<bool> {
        Ok(self.member_call("confirm", vec![json!(title), json!(message), opts.unwrap_or(Value::Null)])?.as_bool().unwrap_or(false))
    }
    /// `ui.input(title, placeholder?, opts?)`; `None` is the dialog's undefined.
    pub fn input(&self, title: &str, placeholder: Option<&str>, opts: Option<Value>) -> io::Result<Option<String>> {
        self.member_call("input", vec![json!(title), json!(placeholder), opts.unwrap_or(Value::Null)]).map(Self::text)
    }
    /// `ui.editor(title, prefill?)`; `None` is the dialog's undefined.
    pub fn editor(&self, title: &str, prefill: Option<&str>) -> io::Result<Option<String>> {
        self.member_call("editor", vec![json!(title), json!(prefill)]).map(Self::text)
    }
    /// `ui.notify(message, type?)`.
    pub fn notify(&self, message: &str, kind: Option<&str>) -> io::Result<()> {
        self.member_call("notify", vec![json!(message), json!(kind)]).map(|_| ())
    }
    /// `ui.setStatus(key, text)`.
    pub fn set_status(&self, key: &str, text: Option<&str>) -> io::Result<()> {
        self.member_call("setStatus", vec![json!(key), json!(text)]).map(|_| ())
    }
    /// `ui.setWorkingMessage(message?)`.
    pub fn set_working_message(&self, message: Option<&str>) -> io::Result<()> {
        self.member_call("setWorkingMessage", vec![json!(message)]).map(|_| ())
    }
    /// `ui.setWorkingVisible(visible)`.
    pub fn set_working_visible(&self, visible: bool) -> io::Result<()> {
        self.member_call("setWorkingVisible", vec![json!(visible)]).map(|_| ())
    }
    /// `ui.setHiddenThinkingLabel(label?)`.
    pub fn set_hidden_thinking_label(&self, label: Option<&str>) -> io::Result<()> {
        self.member_call("setHiddenThinkingLabel", vec![json!(label)]).map(|_| ())
    }
    /// `ui.setWidget(key, content, options?)` for lines of text.
    pub fn set_widget(&self, key: &str, content: &[String], options: Option<Value>) -> io::Result<()> {
        self.member_call("setWidget", vec![json!(key), json!(content), options.unwrap_or(Value::Null)]).map(|_| ())
    }
    /// `ui.setTitle(title)`.
    pub fn set_title(&self, title: &str) -> io::Result<()> {
        self.member_call("setTitle", vec![json!(title)]).map(|_| ())
    }
    /// `ui.pasteToEditor(text)`.
    pub fn paste_to_editor(&self, text: &str) -> io::Result<()> {
        self.member_call("pasteToEditor", vec![json!(text)]).map(|_| ())
    }
    /// `ui.setEditorText(text)`.
    pub fn set_editor_text(&self, text: &str) -> io::Result<()> {
        self.member_call("setEditorText", vec![json!(text)]).map(|_| ())
    }
    /// `ui.getEditorText()`.
    pub fn get_editor_text(&self) -> io::Result<String> {
        Ok(self.member_call("getEditorText", vec![])?.as_str().unwrap_or_default().to_string())
    }
    /// `ui.getToolsExpanded()`.
    pub fn get_tools_expanded(&self) -> io::Result<bool> {
        Ok(self.member_call("getToolsExpanded", vec![])?.as_bool().unwrap_or(false))
    }
    /// `ui.setToolsExpanded(expanded)`.
    pub fn set_tools_expanded(&self, expanded: bool) -> io::Result<()> {
        self.member_call("setToolsExpanded", vec![json!(expanded)]).map(|_| ())
    }
}

/// Pi's `ExtensionContext` as the runner's `createContext` returns it: its getters read the runner at call time, so a change bound functions answer is seen, and a runner that was invalidated fails each of them with its message.
pub struct ExtensionContext {
    inner: Arc<Inner>,
    spec: Value,
}

impl ExtensionContext {
    fn get(&self, member: &str, args: Vec<Value>) -> io::Result<Value> {
        let mut wire = vec![self.spec.clone(), json!(member)];
        wire.extend(args);
        self.inner.object.call("context", wire)
    }

    fn assert_active(&self) -> io::Result<()> {
        self.get("assertActive", vec![]).map(|_| ())
    }

    /// `ctx.ui`.
    pub fn ui(&self) -> io::Result<RunnerUIContext> {
        self.assert_active()?;
        Ok(RunnerUIContext { inner: self.inner.clone() })
    }
    /// `ctx.mode`: "tui", "rpc", "json" or "print".
    pub fn mode(&self) -> io::Result<String> {
        Ok(self.get("mode", vec![])?.as_str().unwrap_or_default().to_string())
    }
    /// `ctx.hasUI`.
    pub fn has_ui(&self) -> io::Result<bool> {
        Ok(self.get("hasUI", vec![])?.as_bool().unwrap_or(false))
    }
    /// `ctx.cwd`.
    pub fn cwd(&self) -> io::Result<String> {
        Ok(self.get("cwd", vec![])?.as_str().unwrap_or_default().to_string())
    }
    /// `ctx.sessionManager`: the log the runner was built with.
    pub fn session_manager(&self) -> io::Result<Option<Arc<SessionManager>>> {
        self.assert_active()?;
        Ok(self.inner.session_manager.clone())
    }
    /// `ctx.modelRegistry`: the registry the runner was built with.
    pub fn model_registry(&self) -> io::Result<Option<Arc<ModelRuntime>>> {
        self.assert_active()?;
        Ok(self.inner.model_registry.clone())
    }
    /// `ctx.model`: the model object the bound getModel answers; null is Pi's undefined.
    pub fn model(&self) -> io::Result<Value> {
        self.get("model", vec![])
    }
    /// `ctx.scopedModels`: `[{model, thinkingLevel?}]`.
    pub fn scoped_models(&self) -> io::Result<Value> {
        self.get("scopedModels", vec![])
    }
    /// `ctx.thinkingLevel`: what the bound getThinkingLevel answers.
    pub fn thinking_level(&self) -> io::Result<String> {
        Ok(self.get("thinkingLevel", vec![])?.as_str().unwrap_or_default().to_string())
    }
    /// `ctx.isIdle()`.
    pub fn is_idle(&self) -> io::Result<bool> {
        Ok(self.get("isIdle", vec![])?.as_bool().unwrap_or(false))
    }
    /// `ctx.isProjectTrusted()`.
    pub fn is_project_trusted(&self) -> io::Result<bool> {
        Ok(self.get("isProjectTrusted", vec![])?.as_bool().unwrap_or(false))
    }
    /// `ctx.signal`: `None` is Pi's undefined (no run); otherwise a signal that is cancelled when the host's is aborted.
    pub fn signal(&self) -> io::Result<Option<ProviderSignal>> {
        let wire = self.get("signal", vec![])?;
        if wire.is_null() {
            return Ok(None);
        }
        let signal = ProviderSignal::new();
        if wire["aborted"].as_bool().unwrap_or(false) {
            signal.cancel();
        }
        Ok(Some(signal))
    }
    /// `ctx.abort()`.
    pub fn abort(&self) -> io::Result<()> {
        self.get("abort", vec![]).map(|_| ())
    }
    /// `ctx.hasPendingMessages()`.
    pub fn has_pending_messages(&self) -> io::Result<bool> {
        Ok(self.get("hasPendingMessages", vec![])?.as_bool().unwrap_or(false))
    }
    /// `ctx.shutdown()`.
    pub fn shutdown(&self) -> io::Result<()> {
        self.get("shutdown", vec![]).map(|_| ())
    }
    /// `ctx.getContextUsage()`; null is Pi's undefined.
    pub fn get_context_usage(&self) -> io::Result<Value> {
        self.get("getContextUsage", vec![])
    }
    /// `ctx.compact(options?)`: it calls the bound compact function with `options`, here, so its callbacks are this process's.
    pub fn compact(&self, options: CompactOptions) -> io::Result<()> {
        self.assert_active()?;
        let compact = self.inner.bound.lock().unwrap().context_actions.compact.clone();
        if let Some(compact) = compact {
            compact(options).map_err(io::Error::other)?;
        }
        Ok(())
    }
    /// `ctx.getSystemPrompt()`.
    pub fn get_system_prompt(&self) -> io::Result<String> {
        Ok(self.get("getSystemPrompt", vec![])?.as_str().unwrap_or_default().to_string())
    }
}

/// Pi's `ExtensionToolContext`: the context of one tool call, with the callable tools and nested execution. It reads as the `ExtensionContext` it wraps.
pub struct ExtensionToolContext {
    context: ExtensionContext,
    tool_call_id: String,
    signal: Option<ProviderSignal>,
}

impl std::ops::Deref for ExtensionToolContext {
    type Target = ExtensionContext;
    fn deref(&self) -> &ExtensionContext {
        &self.context
    }
}

impl ExtensionToolContext {
    /// `ctx.tools`: the tools a nested call can run.
    pub fn tools(&self) -> io::Result<Value> {
        self.context.get("tools", vec![])
    }

    /// `ctx.executeTool(name, args, options?)`: it calls the bound executeTool function, here, with `options` whose `signal` defaults to the one the context was created with. Without a bound function the outcome is Pi's error outcome ("Nested tool calls are not available in this context").
    pub fn execute_tool(&self, name: &str, args: Value, mut options: ExecuteToolOptions) -> io::Result<Value> {
        self.context.assert_active()?;
        let execute = self.context.inner.bound.lock().unwrap().context_actions.execute_tool.clone();
        if options.signal.is_none() {
            options.signal = self.signal.clone();
        }
        match execute {
            Some(execute) => execute(self.tool_call_id.clone(), name.to_string(), args, options).map_err(io::Error::other),
            None => Ok(json!({
                "toolCall": {"type": "toolCall", "id": format!("{}/0", self.tool_call_id), "name": name, "arguments": {}},
                "result": {"content": [{"type": "text", "text": "Nested tool calls are not available in this context"}], "details": {}},
                "isError": true,
            })),
        }
    }
}

/// Pi's `ExtensionCommandContext`: the context of a command, which can wait for the agent and replace the session. It reads as the `ExtensionContext` it wraps.
pub struct ExtensionCommandContext {
    context: ExtensionContext,
}

impl std::ops::Deref for ExtensionCommandContext {
    type Target = ExtensionContext;
    fn deref(&self) -> &ExtensionContext {
        &self.context
    }
}

impl ExtensionCommandContext {
    /// `ctx.getSystemPromptOptions()`.
    pub fn get_system_prompt_options(&self) -> io::Result<Value> {
        self.context.get("getSystemPromptOptions", vec![])
    }

    fn actions(&self) -> io::Result<ExtensionCommandContextActions> {
        self.context.assert_active()?;
        Ok(self.context.inner.bound.lock().unwrap().command_actions.clone().unwrap_or_default())
    }

    /// `ctx.waitForIdle()`.
    pub fn wait_for_idle(&self) -> io::Result<()> {
        match self.actions()?.wait_for_idle {
            Some(f) => f().map_err(io::Error::other),
            None => Ok(()),
        }
    }
    /// `ctx.newSession(options?)`: the bound function receives `options` as given, with its `setup` and `with_session`.
    pub fn new_session(&self, options: CommandNewSessionOptions) -> io::Result<CancelledResult> {
        match self.actions()?.new_session {
            Some(f) => f(options).map_err(io::Error::other),
            None => Ok(CancelledResult::default()),
        }
    }
    /// `ctx.fork(entryId, options?)`.
    pub fn fork(&self, entry_id: &str, options: ForkOptions) -> io::Result<CancelledResult> {
        match self.actions()?.fork {
            Some(f) => f(entry_id.to_string(), options).map_err(io::Error::other),
            None => Ok(CancelledResult::default()),
        }
    }
    /// `ctx.navigateTree(targetId, options?)`.
    pub fn navigate_tree(&self, target_id: &str, options: NavigateTreeOptions) -> io::Result<CancelledResult> {
        match self.actions()?.navigate_tree {
            Some(f) => f(target_id.to_string(), options).map_err(io::Error::other),
            None => Ok(CancelledResult::default()),
        }
    }
    /// `ctx.switchSession(sessionPath, options?)`.
    pub fn switch_session(&self, session_path: &str, options: SwitchSessionOptions) -> io::Result<CancelledResult> {
        match self.actions()?.switch_session {
            Some(f) => f(session_path.to_string(), options).map_err(io::Error::other),
            None => Ok(CancelledResult::default()),
        }
    }
    /// `ctx.reload()`.
    pub fn reload(&self) -> io::Result<()> {
        match self.actions()?.reload {
            Some(f) => f().map_err(io::Error::other),
            None => Ok(()),
        }
    }
}

//! The extension_runner script of the session graph rows: createExtensionRuntime and ExtensionRunner over two probe extensions, as testdata/extension-runner.mjs does with Pi's own classes; the report is Pi's own run's.

use pig_sdk::{
    create_extension_runtime, CancelledResult, CommandNewSessionOptions, CompactOptions, Context, DefaultResourceLoader, DefaultResourceLoaderOptions, ExtensionActions, ExtensionCommandContextActions,
    ExtensionContextActions, ExtensionRunner, ExtensionRuntime, ExtensionUIContext, ForkOptions, ModelRuntime, ModelRuntimeCreateOptions, SessionManager, SettingsManager,
};
use serde_json::{json, Map, Value};
use std::io;
use std::sync::{Arc, Mutex};
use std::time::Duration;

type Log = Arc<Mutex<Vec<Value>>>;

fn text(value: &Value) -> &str {
    value.as_str().unwrap_or_default()
}

fn error_of<T>(result: io::Result<T>) -> String {
    match result {
        Ok(_) => "no error".into(),
        Err(error) => format!("error: {error}"),
    }
}

fn record(log: &Log, name: &str, args: Vec<Value>) {
    let mut entry = vec![json!(name)];
    entry.extend(args);
    log.lock().unwrap().push(Value::Array(entry));
}

fn count(log: &Log, name: &str, custom_type: &str) -> usize {
    log.lock().unwrap().iter().filter(|entry| entry[0] == name && entry[1] == custom_type).count()
}

fn runtime_view(runtime: &ExtensionRuntime) -> io::Result<Value> {
    Ok(json!({
        "flagValues": runtime.get_flag_values()?, "providers": runtime.pending_provider_registrations()?.as_array().map_or(0, Vec::len),
        "nativeProviders": runtime.pending_native_provider_registrations()?.as_array().map_or(0, Vec::len),
        "virtualModels": runtime.pending_virtual_model_registrations()?.as_array().map_or(0, Vec::len), "mcpServers": runtime.mcp_servers()?.as_array().map_or(0, Vec::len),
    }))
}

fn type_of(found: bool) -> &'static str {
    if found { "function" } else { "undefined" }
}

fn renderers_view(found: &Value) -> Value {
    if found.is_null() {
        return Value::Null;
    }
    json!({"renderShell": found["renderShell"], "renderCall": type_of(found["renderCall"] == true), "renderResult": type_of(found["renderResult"] == true)})
}

fn set(report: &mut Map<String, Value>, key: &str, value: Value) {
    if !value.is_null() {
        report.insert(key.into(), value);
    }
}

pub fn extension_runner_script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let (cwd, agent_dir) = (text(&args["cwd"]), text(&args["agentDir"]));
    let mut r = Map::new();

    let fresh = create_extension_runtime(ctx)?;
    let mut runtime_report = json!({"initial": runtime_view(&fresh)?, "active": error_of(fresh.assert_active())});
    fresh.set_flag_value("x", json!(true))?;
    fresh.set_flag_value("a", json!("b"))?;
    runtime_report["afterSet"] = runtime_view(&fresh)?;
    fresh.invalidate(Some("runtime is stale"))?;
    fresh.invalidate(Some("a second message is ignored"))?;
    runtime_report["stale"] = json!(error_of(fresh.assert_active()));
    r.insert("runtime".into(), runtime_report);

    let rt = Arc::new(ModelRuntime::create(
        ctx,
        ModelRuntimeCreateOptions { auth_path: Some(text(&args["mrAuth"]).into()), models_path: Some(text(&args["mrModels"]).into()), models_store_path: Some(text(&args["mrStore"]).into()), ..Default::default() },
    )?);
    let settings = SettingsManager::in_memory(
        ctx,
        Some(json!({"compaction": {"enabled": false}, "retry": {"enabled": false}, "defaultProvider": "mr-local", "defaultModel": "m1"})),
        None,
    )?;
    let loader_runtime = create_extension_runtime(ctx)?;
    let loader = DefaultResourceLoader::new(
        ctx,
        DefaultResourceLoaderOptions {
            cwd: cwd.into(),
            agent_dir: agent_dir.into(),
            settings_manager: Some(&settings),
            runtime: Some(&loader_runtime),
            no_extensions: true,
            no_skills: true,
            no_prompt_templates: true,
            no_themes: true,
            no_context_files: true,
            additional_extension_paths: vec![text(&args["runnerProbeA"]).into(), text(&args["runnerProbeB"]).into()],
            ..Default::default()
        },
    )?;
    loader.reload(None)?;
    let loaded = loader.get_extensions()?;
    let paths: Vec<Value> = loaded.extensions.iter().map(|ext| ext["path"].clone()).collect();
    let tools: Vec<Value> = loaded.extensions.iter().map(|ext| ext["tools"].clone()).collect();
    r.insert("loaded".into(), json!({"paths": paths, "tools": tools, "errors": loaded["errors"], "sources": loaded.extensions.iter().map(|ext| ext["sourceInfo"]["source"].clone()).collect::<Vec<Value>>(), "runtime": runtime_view(&loaded.runtime)?}));

    let manager = Arc::new(SessionManager::in_memory(ctx, Some(cwd), None, None)?);
    let runner = ExtensionRunner::new(ctx, &loaded.extensions, Some(&loaded.runtime), cwd, Some(manager.clone()), Some(rt.clone()))?;

    // The registries, before anything is bound.
    let keybindings = json!({"app.interrupt": ["ctrl+c"], "app.other": ["ctrl+k"], "app.exit": ["ctrl+d"]});
    let noop = runner.get_ui_context();
    let tool_views: Vec<Value> = runner
        .get_all_registered_tools()?
        .as_array()
        .cloned()
        .unwrap_or_default()
        .iter()
        .map(|tool| json!({"name": tool["definition"]["name"], "description": tool["definition"]["description"], "scope": tool["sourceInfo"]["scope"], "origin": tool["sourceInfo"]["origin"], "path": tool["sourceInfo"]["path"]}))
        .collect();
    let shared = runner.get_tool_definition("shared")?;
    let shortcuts: Vec<Value> = runner
        .get_shortcuts(keybindings)?
        .as_array()
        .cloned()
        .unwrap_or_default()
        .iter()
        .map(|entry| json!({"key": entry[0], "description": entry[1]["description"], "extensionPath": entry[1]["extensionPath"]}))
        .collect();
    let commands: Vec<Value> = runner
        .get_registered_commands()?
        .as_array()
        .cloned()
        .unwrap_or_default()
        .iter()
        .map(|command| json!({"name": command["name"], "invocationName": command["invocationName"], "description": command["description"], "path": command["sourceInfo"]["path"]}))
        .collect();
    let invocation = |name: &str| -> io::Result<Value> { Ok(runner.get_command(name)?["invocationName"].clone()) };
    // An extension process answers a tool renderer resolution on a later turn (D89): the first call answers what the base does, so ask until the answer is the extension's.
    let mut alpha = Value::Null;
    for _ in 0..100 {
        alpha = runner.resolve_tool_renderers("alpha", Some(Arc::new(|| Value::Null)))?;
        if !alpha.is_null() {
            break;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    let context_of = runner.create_context();
    let registry_same = context_of.model_registry()?.is_some_and(|registry| Arc::ptr_eq(&registry, &rt));
    let manager_same = context_of.session_manager()?.is_some_and(|candidate| Arc::ptr_eq(&candidate, &manager));
    let command_before = runner.create_command_context();
    command_before.wait_for_idle()?;
    let new_before = command_before.new_session(CommandNewSessionOptions::default())?;
    command_before.reload()?;
    let nested_before = runner.create_tool_context("call-0", None).execute_tool("alpha", json!({}), Default::default())?;
    let mut reads = json!({
        "extensionPaths": runner.get_extension_paths()?, "tools": tool_views, "toolDefinition": shared["description"], "missingTool": runner.get_tool_definition("nope")?,
        "flags": runner.get_flags()?, "flagValues": runner.get_flag_values()?, "shortcuts": shortcuts, "shortcutDiagnostics": runner.get_shortcut_diagnostics()?,
        "commands": commands, "commandDiagnostics": runner.get_command_diagnostics()?,
        "commandByName": {"hello": invocation("hello")?, "twin": invocation("twin")?, "twin:2": runner.get_command("twin:2")?["description"], "nope": invocation("nope")?},
        "hasHandlers": {"tool_call": runner.has_handlers("tool_call")?, "session_shutdown": runner.has_handlers("session_shutdown")?},
        "renderers": {
            "message": type_of(runner.get_message_renderer("probe-message")?), "missingMessage": type_of(runner.get_message_renderer("nope")?),
            "entry": type_of(runner.get_entry_renderer("probe-entry")?), "missingEntry": type_of(runner.get_entry_renderer("nope")?), "markdownTransformers": runner.get_markdown_transformers()?,
        },
        "toolRenderers": {
            "alpha": renderers_view(&alpha),
            "other": renderers_view(&runner.resolve_tool_renderers("other", Some(Arc::new(|| json!({"renderResult": true}))))?),
            "none": renderers_view(&runner.resolve_tool_renderers("other", Some(Arc::new(|| Value::Null)))?),
        },
        "hasUI": runner.has_ui()?,
        "noUI": {"select": noop.select("t", &["a".to_string()], None)?, "confirm": noop.confirm("t", "m", None)?, "input": noop.input("t", None, None)?, "editorText": noop.get_editor_text()?},
        "registryIsSame": runner.get_model_registry().is_some_and(|registry| Arc::ptr_eq(&registry, &rt)),
        "activeTools": error_of(runner.get_active_tools()), "mode": context_of.mode()?, "cwd": context_of.cwd()?,
        "sessionManagerIsSame": manager_same, "modelRegistryIsSame": registry_same,
        "commandContextBeforeBind": {"newSession": new_before}, "toolContextBeforeBind": nested_before["result"]["content"][0]["text"],
    });
    runner.set_flag_value("probe-flag", json!("changed"))?;
    reads["flagValuesAfterSet"] = runner.get_flag_values()?;
    runner.set_flag_value("probe-bool", json!(true))?;
    reads["flagValuesAfterBool"] = runner.get_flag_values()?;
    r.insert("reads".into(), reads);

    // Bound actions: every call an extension makes reaches one of these, and is logged.
    let log: Log = Arc::new(Mutex::new(Vec::new()));
    let model = rt.get_model("mr-local", "m1")?;
    let source_info = json!({"path": "p", "source": "local", "scope": "temporary", "origin": "top-level"});
    let actions = ExtensionActions {
        send_message: Some({
            let log = log.clone();
            Arc::new(move |message, options| {
                record(&log, "sendMessage", vec![message, options.unwrap_or(Value::Null)]);
                Ok(())
            })
        }),
        send_user_message: Some({
            let log = log.clone();
            Arc::new(move |content, options| {
                record(&log, "sendUserMessage", vec![content, options.unwrap_or(Value::Null)]);
                Ok(())
            })
        }),
        append_entry: Some({
            let log = log.clone();
            Arc::new(move |custom_type, data| {
                record(&log, "appendEntry", vec![json!(custom_type), data]);
                Ok(())
            })
        }),
        set_session_name: Some({
            let log = log.clone();
            Arc::new(move |name| {
                record(&log, "setSessionName", vec![json!(name)]);
                Ok(())
            })
        }),
        get_session_name: Some(Arc::new(|| Ok("session-name".to_string()))),
        set_label: Some({
            let log = log.clone();
            Arc::new(move |entry_id, label| {
                record(&log, "setLabel", vec![json!(entry_id), json!(label)]);
                Ok(())
            })
        }),
        get_active_tools: Some(Arc::new(|| Ok(vec!["alpha".to_string(), "beta".to_string()]))),
        get_all_tools: Some({
            let source_info = source_info.clone();
            Arc::new(move || Ok(json!([{"name": "alpha", "description": "Alpha tool", "parameters": {"type": "object"}, "sourceInfo": source_info}])))
        }),
        get_settings: Some(Arc::new(|| Ok(json!({"theme": "dark"})))),
        set_active_tools: Some({
            let log = log.clone();
            Arc::new(move |names| {
                record(&log, "setActiveTools", vec![json!(names)]);
                Ok(())
            })
        }),
        refresh_tools: Some({
            let log = log.clone();
            Arc::new(move || {
                record(&log, "refreshTools", vec![]);
                Ok(())
            })
        }),
        get_commands: Some({
            let source_info = source_info.clone();
            Arc::new(move || Ok(json!([{"name": "hello", "description": "d", "source": "extension", "sourceInfo": source_info}])))
        }),
        set_model: Some({
            let log = log.clone();
            Arc::new(move |model| {
                record(&log, "setModel", vec![model["id"].clone()]);
                Ok(true)
            })
        }),
        get_thinking_level: Some(Arc::new(|| Ok("medium".to_string()))),
        set_thinking_level: Some({
            let log = log.clone();
            Arc::new(move |level| {
                record(&log, "setThinkingLevel", vec![json!(level)]);
                Ok(())
            })
        }),
    };
    let context_actions = ExtensionContextActions {
        get_model: Some({
            let model = model.clone();
            Arc::new(move || Ok(model.clone()))
        }),
        get_scoped_models: Some({
            let model = model.clone();
            Arc::new(move || Ok(json!([{"model": model, "thinkingLevel": "low"}])))
        }),
        is_idle: Some(Arc::new(|| Ok(false))),
        is_project_trusted: Some(Arc::new(|| Ok(true))),
        get_signal: Some(Arc::new(|| Ok(None))),
        abort: Some({
            let log = log.clone();
            Arc::new(move || {
                record(&log, "abort", vec![]);
                Ok(())
            })
        }),
        has_pending_messages: Some(Arc::new(|| Ok(true))),
        shutdown: Some({
            let log = log.clone();
            Arc::new(move || {
                record(&log, "shutdown", vec![]);
                Ok(())
            })
        }),
        get_context_usage: Some(Arc::new(|| Ok(json!({"tokens": 10, "contextWindow": 100, "percent": 10.0})))),
        compact: Some({
            let log = log.clone();
            Arc::new(move |options: CompactOptions| {
                record(&log, "compact", vec![json!(options.custom_instructions)]);
                Ok(())
            })
        }),
        get_system_prompt: Some(Arc::new(|| Ok("bound system prompt".to_string()))),
        get_system_prompt_options: Some({
            let cwd = cwd.to_string();
            Arc::new(move || Ok(json!({"cwd": cwd, "customPrompt": "custom"})))
        }),
        execute_tool: Some(Arc::new(|caller_id, name, params, _options| {
            Ok(json!({"toolCall": {"type": "toolCall", "id": format!("{caller_id}/1"), "name": name, "arguments": params}, "result": {"content": [{"type": "text", "text": format!("ran {name}")}], "details": {}}, "isError": false}))
        })),
        get_callable_tools: Some(Arc::new(|| Ok(json!([{"name": "alpha", "label": "Alpha", "description": "Alpha tool", "parameters": {"type": "object"}}])))),
    };
    runner.bind_core(actions, context_actions, None)?;
    r.insert("bound".into(), json!({"activeTools": runner.get_active_tools()?, "errors": []}));
    let reported: Arc<Mutex<Vec<Value>>> = Arc::new(Mutex::new(Vec::new()));
    let off = runner.on_error({
        let reported = reported.clone();
        Arc::new(move |report| reported.lock().unwrap().push(json!({"extensionPath": report["extensionPath"], "event": report["event"], "error": report["error"]})))
    })?;
    runner.report_unhandled_mcp_servers()?;

    // The emit family, each through two handlers that chain.
    let mut emits = Map::new();
    runner.emit(json!({"type": "session_start", "reason": "startup"}))?;
    emits.insert("sessionBeforeSwitch".into(), runner.emit(json!({"type": "session_before_switch", "reason": "new"}))?);
    runner.emit(json!({"type": "agent_start"}))?;
    set(&mut emits, "toolCall", runner.emit_tool_call(json!({"type": "tool_call", "toolCallId": "t1", "toolName": "bash", "input": {"command": "ls"}}))?);
    set(&mut emits, "toolCallBlocked", runner.emit_tool_call(json!({"type": "tool_call", "toolCallId": "t2", "toolName": "bash", "input": {"command": "forbidden"}}))?);
    set(&mut emits, "toolResult", runner.emit_tool_result(json!({"type": "tool_result", "toolCallId": "t1", "toolName": "bash", "input": {"command": "ls"}, "content": [{"type": "text", "text": "out"}], "isError": false}))?);
    emits.insert("inputTransform".into(), runner.emit_input("t:x", None, "interactive", None)?);
    emits.insert("inputHandled".into(), runner.emit_input("handled-me", None, "interactive", None)?);
    emits.insert("inputContinue".into(), runner.emit_input("plain", None, "rpc", None)?);
    emits.insert("context".into(), runner.emit_context(json!([{"role": "user", "content": "keep", "timestamp": 1}, {"role": "user", "content": "drop-me", "timestamp": 2}]))?);
    let started = runner.emit_before_agent_start(json!({"messageType": "user", "prompt": "do it"}), json!({"cwd": cwd, "customPrompt": "base"}))?;
    let forced = text(&started["systemPromptOptions"]["forceSystemPrompt"]).to_string();
    emits.insert(
        "beforeAgentStart".into(),
        json!({"messages": started["messages"], "forced": {"startsWithBase": forced.starts_with("base"), "endsWithA": forced.ends_with("[A]")}, "customPrompt": started["systemPromptOptions"]["customPrompt"]}),
    );
    set(&mut emits, "messageEnd", runner.emit_message_end(json!({"type": "message_end", "message": {"role": "user", "content": "hi", "timestamp": 1}}))?);
    emits.insert("providerRequest".into(), runner.emit_before_provider_request(json!({"model": "m"}))?);
    emits.insert("providerHeaders".into(), runner.emit_before_provider_headers(json!({"x-del": "d", "x-keep": "k"}))?);
    set(&mut emits, "userBash", runner.emit_user_bash(json!({"type": "user_bash", "command": "ls", "excludeFromContext": false, "cwd": cwd}))?);
    emits.insert("resourcesDiscover".into(), runner.emit_resources_discover(cwd, "startup")?);
    emits.insert("cacheWarming".into(), json!(runner.emit_cache_warming_decision(json!({"type": "cache_warming_decision", "warmCost": 1, "missCost": 2, "continuationProbability": 0.5, "action": "warm"}))?));
    let built: Arc<Mutex<Vec<Value>>> = Arc::new(Mutex::new(Vec::new()));
    let boundary = runner.emit_boundary(
        json!({"type": "turn_end", "turnIndex": 0, "message": {"role": "user", "content": "x", "timestamp": 1}, "toolResults": [], "messageEntryId": "m1", "toolResultEntryIds": [], "outcome": "completed"}),
        {
            let built = built.clone();
            Arc::new(move |entries| {
                built.lock().unwrap().push(json!(entries.as_array().map_or(0, Vec::len)));
                Ok(json!({"contextEntries": [], "contextMessages": [], "llmMessages": [], "pendingMessages": [], "canContinue": true}))
            })
        },
    )?;
    emits.insert("boundary".into(), boundary);
    emits.insert("boundaryBuilt".into(), Value::Array(built.lock().unwrap().clone()));
    runner.emit_error(json!({"extensionPath": "/x/ext.mjs", "event": "manual", "error": "reported by hand"}))?;
    r.insert("emits".into(), Value::Object(emits));
    r.insert("errors".into(), Value::Array(reported.lock().unwrap().clone()));

    // The contexts, which read the bound functions at call time.
    let live = runner.create_context();
    let signal = match live.signal()? {
        Some(signal) => json!({"aborted": signal.is_cancelled()}),
        None => Value::Null,
    };
    let scoped: Vec<Value> = live.scoped_models()?.as_array().cloned().unwrap_or_default().iter().map(|entry| json!([entry["model"]["id"], entry["thinkingLevel"]])).collect();
    let mut contexts = json!({
        "mode": live.mode()?, "hasUI": live.has_ui()?, "cwd": live.cwd()?, "model": live.model()?["id"], "scopedModels": scoped, "thinkingLevel": live.thinking_level()?,
        "isIdle": live.is_idle()?, "isProjectTrusted": live.is_project_trusted()?, "signal": signal, "hasPendingMessages": live.has_pending_messages()?,
        "contextUsage": live.get_context_usage()?, "systemPrompt": live.get_system_prompt()?,
    });
    live.abort()?;
    live.shutdown()?;
    live.compact(CompactOptions { custom_instructions: Some("short".into()), ..Default::default() })?;
    runner.shutdown()?;
    let command = runner.create_command_context();
    let cancelled = |value: bool| CancelledResult { cancelled: value };
    runner.bind_command_context(Some(ExtensionCommandContextActions {
        wait_for_idle: Some({
            let log = log.clone();
            Arc::new(move || {
                record(&log, "waitForIdle", vec![]);
                Ok(())
            })
        }),
        new_session: Some({
            let log = log.clone();
            Arc::new(move |options: CommandNewSessionOptions| {
                record(&log, "newSession", vec![json!(options.parent_session)]);
                Ok(cancelled(false))
            })
        }),
        fork: Some({
            let log = log.clone();
            Arc::new(move |entry_id, options: ForkOptions| {
                record(&log, "fork", vec![json!(entry_id), json!(options.position)]);
                Ok(cancelled(true))
            })
        }),
        navigate_tree: Some({
            let log = log.clone();
            Arc::new(move |target, options| {
                record(&log, "navigateTree", vec![json!(target), options["label"].clone()]);
                Ok(cancelled(false))
            })
        }),
        switch_session: Some({
            let log = log.clone();
            Arc::new(move |path, _options| {
                record(&log, "switchSession", vec![json!(path)]);
                Ok(cancelled(false))
            })
        }),
        reload: Some({
            let log = log.clone();
            Arc::new(move || {
                record(&log, "reload", vec![]);
                Ok(())
            })
        }),
    }))?;
    // In Pi's order: the calls log in the order they are made.
    let mut command_report = Map::new();
    command_report.insert("systemPromptOptions".into(), command.get_system_prompt_options()?);
    command.wait_for_idle()?;
    command_report.insert("waitForIdle".into(), Value::Null);
    command_report.insert("newSession".into(), json!(command.new_session(CommandNewSessionOptions { parent_session: Some("parent.jsonl".into()), ..Default::default() })?));
    command_report.insert("fork".into(), json!(command.fork("e1", ForkOptions { position: Some("at".into()), with_session: None })?));
    command_report.insert("navigateTree".into(), json!(command.navigate_tree("e2", json!({"label": "l"}))?));
    command_report.insert("switchSession".into(), json!(command.switch_session("other.jsonl", Default::default())?));
    command.reload()?;
    command_report.insert("reload".into(), Value::Null);
    contexts["command"] = Value::Object(command_report);
    let tool_context = runner.create_tool_context("call-1", None);
    let callable: Vec<Value> = tool_context.tools()?.as_array().cloned().unwrap_or_default().iter().map(|tool| tool["name"].clone()).collect();
    contexts["tool"] = json!({"tools": callable, "executeTool": tool_context.execute_tool("alpha", json!({"text": "x"}), Default::default())?});
    r.insert("contexts".into(), contexts);

    // The UI context: the dialogs report ui_prompt_start and ui_prompt_end to the extensions.
    let ui_log: Arc<Mutex<Vec<Value>>> = Arc::new(Mutex::new(Vec::new()));
    let note = |ui_log: &Arc<Mutex<Vec<Value>>>, call: Vec<Value>| ui_log.lock().unwrap().push(Value::Array(call));
    let ui = ExtensionUIContext {
        select: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |(title, options): (String, Vec<String>), _opts| {
                note(&ui_log, vec![json!("select"), json!(title), json!(options)]);
                Ok(options.get(1).cloned())
            })
        }),
        confirm: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |(title, message): (String, String), _opts| {
                note(&ui_log, vec![json!("confirm"), json!(title), json!(message)]);
                Ok(true)
            })
        }),
        input: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |(title, placeholder): (String, Option<String>), _opts| {
                note(&ui_log, vec![json!("input"), json!(title), json!(placeholder)]);
                Ok(Some("typed".to_string()))
            })
        }),
        editor: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |title, prefill: Option<String>| {
                note(&ui_log, vec![json!("editor"), json!(title), json!(prefill)]);
                Ok(Some(format!("{}!", prefill.unwrap_or_default())))
            })
        }),
        notify: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |message, kind| {
                note(&ui_log, vec![json!("notify"), json!(message), json!(kind)]);
                Ok(())
            })
        }),
        set_status: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |key, text| {
                note(&ui_log, vec![json!("setStatus"), json!(key), json!(text)]);
                Ok(())
            })
        }),
        set_working_message: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |message| {
                note(&ui_log, vec![json!("setWorkingMessage"), json!(message)]);
                Ok(())
            })
        }),
        set_working_visible: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |visible| {
                note(&ui_log, vec![json!("setWorkingVisible"), json!(visible)]);
                Ok(())
            })
        }),
        set_hidden_thinking_label: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |label| {
                note(&ui_log, vec![json!("setHiddenThinkingLabel"), json!(label)]);
                Ok(())
            })
        }),
        set_widget: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |(key, content): (String, Vec<String>), options: Option<Value>| {
                note(&ui_log, vec![json!("setWidget"), json!(key), json!(content), options.map_or(Value::Null, |options| options["placement"].clone())]);
                Ok(())
            })
        }),
        set_title: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |title| {
                note(&ui_log, vec![json!("setTitle"), json!(title)]);
                Ok(())
            })
        }),
        paste_to_editor: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |text| {
                note(&ui_log, vec![json!("pasteToEditor"), json!(text)]);
                Ok(())
            })
        }),
        set_editor_text: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |text| {
                note(&ui_log, vec![json!("setEditorText"), json!(text)]);
                Ok(())
            })
        }),
        get_editor_text: Some(Arc::new(|| Ok("editor text".to_string()))),
        get_tools_expanded: Some(Arc::new(|| Ok(true))),
        set_tools_expanded: Some({
            let ui_log = ui_log.clone();
            Arc::new(move |expanded| {
                note(&ui_log, vec![json!("setToolsExpanded"), json!(expanded)]);
                Ok(())
            })
        }),
    };
    runner.set_ui_context(Some(ui), "tui")?;
    let live_ui = runner.get_ui_context();
    let dialogs = json!({
        "select": live_ui.select("Pick", &["x".to_string(), "y".to_string()], None)?, "confirm": live_ui.confirm("Sure?", "really", None)?,
        "input": live_ui.input("Name", Some("placeholder"), None)?, "editor": live_ui.editor("Edit", Some("pre"))?,
    });
    live_ui.notify("hello", Some("info"))?;
    live_ui.set_status("k", Some("v"))?;
    live_ui.set_working_message(Some("working"))?;
    live_ui.set_working_visible(false)?;
    live_ui.set_hidden_thinking_label(Some("hidden"))?;
    live_ui.set_widget("w", &["line".to_string()], Some(json!({"placement": "belowEditor"})))?;
    live_ui.set_title("title")?;
    live_ui.paste_to_editor("pasted")?;
    live_ui.set_editor_text("set")?;
    live_ui.set_tools_expanded(false)?;
    let reads_ui = json!({"editorText": live_ui.get_editor_text()?, "toolsExpanded": live_ui.get_tools_expanded()?});
    // The prompt events are delivered on a later turn: wait for both of each dialog.
    for _ in 0..100 {
        if count(&log, "appendEntry", "a_ui") >= 8 {
            break;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    let after = runner.create_context();
    let mut ui_report = json!({"mode": after.mode()?, "hasUI": runner.has_ui()?, "contextHasUI": after.has_ui()?, "dialogs": dialogs, "reads": reads_ui, "calls": ui_log.lock().unwrap().clone()});
    runner.set_ui_context(None, "")?;
    let cleared = runner.create_context();
    ui_report["afterClear"] = json!({"hasUI": runner.has_ui()?, "mode": cleared.mode()?});
    r.insert("ui".into(), ui_report);

    // Listeners and a stale runner.
    off();
    runner.emit_error(json!({"extensionPath": "/x/ext.mjs", "event": "after-off", "error": "not delivered"}))?;
    r.insert("errors".into(), Value::Array(reported.lock().unwrap().clone()));
    runner.invalidate(Some("runner is stale"))?;
    runner.invalidate(Some("a second message is ignored"))?;
    let stale_context = runner.create_context();
    r.insert(
        "stale".into(),
        json!({
            "cwd": error_of(stale_context.cwd()), "isIdle": error_of(stale_context.is_idle()), "activeTools": error_of(runner.get_active_tools()),
            "commandContext": error_of(runner.create_command_context().wait_for_idle()), "toolContext": error_of(runner.create_tool_context("c", None).execute_tool("alpha", json!({}), Default::default())),
            "ui": error_of(stale_context.ui()), "emit": error_of(runner.emit(json!({"type": "agent_start"}))),
        }),
    );
    r.insert("log".into(), Value::Array(log.lock().unwrap().clone()));
    Ok(Value::Object(r))
}

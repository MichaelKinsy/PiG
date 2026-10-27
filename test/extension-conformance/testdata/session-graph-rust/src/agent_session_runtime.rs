//! The agent_session_runtime script of the session graph rows: createAgentSessionRuntime and AgentSessionRuntime through every replacement method, as testdata/agent-session-runtime.mjs does with Pi's own classes; the report is Pi's own run's.

use pig_sdk::{
    create_agent_session_from_services, create_agent_session_runtime, create_agent_session_services, CreateAgentSessionFromServicesOptions, CreateAgentSessionRuntimeFactoryOptions, CreateAgentSessionRuntimeOptions,
    CreateAgentSessionRuntimeResult, CreateAgentSessionServicesOptions, ForkResult, ForkRuntimeOptions, ModelRuntime, ModelRuntimeCreateOptions, NewSessionRuntimeOptions, SessionManager, SettingsManager,
    DefaultResourceLoaderOptions, Context, CancelledResult,
};
use serde_json::{json, Map, Value};
use std::io;
use std::path::Path;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

fn text(value: &Value) -> &str {
    value.as_str().unwrap_or_default()
}

fn error_of<T>(result: io::Result<T>) -> String {
    match result {
        Ok(_) => "no error".into(),
        Err(error) => format!("error: {error}"),
    }
}

fn entry_text(message: &Value) -> String {
    match &message["content"] {
        Value::String(content) => content.clone(),
        Value::Array(blocks) => blocks.iter().filter(|block| block["type"] == "text").map(|block| text(&block["text"]).to_string()).collect(),
        _ => String::new(),
    }
}

fn fork_view(result: &ForkResult) -> Value {
    let mut out = json!({"cancelled": result.cancelled});
    if let Some(selected) = &result.selected_text {
        out["selectedText"] = json!(selected);
    }
    out
}

pub fn agent_session_runtime_script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let (cwd, agent_dir, root) = (text(&args["cwd"]), text(&args["agentDir"]), text(&args["root"]));
    let mut r = Map::new();
    let rt = Arc::new(ModelRuntime::create(
        ctx,
        ModelRuntimeCreateOptions { auth_path: Some(text(&args["mrAuth"]).into()), models_path: Some(text(&args["mrModels"]).into()), models_store_path: Some(text(&args["mrStore"]).into()), ..Default::default() },
    )?);
    let settings = Arc::new(SettingsManager::in_memory(ctx, Some(json!({"compaction": {"enabled": false}, "retry": {"enabled": false}, "defaultProvider": "mr-local", "defaultModel": "m1"})), None)?);
    let session_dir = Path::new(root).join("runtime-sessions").to_string_lossy().into_owned();
    let log_file = Path::new(cwd).join("runtime-events.log");
    let cancel_file = Path::new(cwd).join("cancel-next");
    std::fs::create_dir_all(cwd)?;
    let calls: Arc<Mutex<Vec<Value>>> = Arc::new(Mutex::new(Vec::new()));
    let probe = text(&args["runtimeProbe"]).to_string();
    let factory = {
        let (ctx, rt, settings, calls) = (ctx.clone(), rt.clone(), settings.clone(), calls.clone());
        Arc::new(move |options: CreateAgentSessionRuntimeFactoryOptions| -> io::Result<CreateAgentSessionRuntimeResult> {
            let (reason, previous) = match &options.session_start_event {
                Some(event) => (json!(event.reason), event.previous_session_file.as_deref().is_some_and(|file| !file.is_empty())),
                None => (Value::Null, false),
            };
            calls.lock().unwrap().push(json!({"cwd": options.cwd, "reason": reason, "previous": previous, "persisted": options.session_manager.is_persisted()?}));
            let services = create_agent_session_services(
                &ctx,
                CreateAgentSessionServicesOptions {
                    cwd: options.cwd.clone(),
                    agent_dir: Some(options.agent_dir.clone()),
                    settings_manager: Some(settings.clone()),
                    model_runtime: Some(rt.clone()),
                    resource_loader_options: Some(DefaultResourceLoaderOptions {
                        no_extensions: true,
                        no_skills: true,
                        no_prompt_templates: true,
                        no_themes: true,
                        no_context_files: true,
                        additional_extension_paths: vec![probe.clone()],
                        ..Default::default()
                    }),
                    ..Default::default()
                },
            )?;
            let session = create_agent_session_from_services(
                &ctx,
                CreateAgentSessionFromServicesOptions {
                    services: &services,
                    session_manager: &options.session_manager,
                    session_start_event: options.session_start_event.clone(),
                    model: None,
                    thinking_level: None,
                    scoped_models: None,
                    tools: None,
                    exclude_tools: None,
                    no_tools: None,
                    custom_tools: vec![],
                },
            )?;
            let diagnostics = services.diagnostics.clone();
            let fallback = session.model_fallback_message.clone();
            Ok(CreateAgentSessionRuntimeResult { session, services, diagnostics, model_fallback_message: fallback })
        })
    };
    let manager = Arc::new(SessionManager::create(ctx, cwd, Some(&session_dir), None)?);
    let run_file = text(&manager.get_session_file()?).to_string();
    let runtime = create_agent_session_runtime(ctx, factory, CreateAgentSessionRuntimeOptions { cwd: cwd.into(), agent_dir: agent_dir.into(), session_manager: manager.clone(), session_start_event: None })?;
    let rebinds = Arc::new(AtomicUsize::new(0));
    let invalidations = Arc::new(AtomicUsize::new(0));
    {
        let rebinds = rebinds.clone();
        runtime.set_rebind_session(Some(Arc::new(move |session| {
            rebinds.fetch_add(1, Ordering::SeqCst);
            session.bind_extensions()
        })));
        let invalidations = invalidations.clone();
        runtime.set_before_session_invalidate(Some(Arc::new(move || {
            invalidations.fetch_add(1, Ordering::SeqCst);
        })));
    }
    runtime.session().bind_extensions()?;
    let current = || runtime.session().session_manager();
    let mut seen = vec![text(&manager.get_session_id()?).to_string()];
    let mut is_new = |runtime_manager: &SessionManager| -> io::Result<bool> {
        let id = text(&runtime_manager.get_session_id()?).to_string();
        let fresh = !seen.contains(&id);
        if fresh {
            seen.push(id);
        }
        Ok(fresh)
    };
    let kinds = |manager: &SessionManager| -> io::Result<Vec<Value>> { Ok(manager.get_entries()?.as_array().cloned().unwrap_or_default().iter().map(|entry| entry["type"].clone()).collect()) };
    let roles = || -> io::Result<Vec<Value>> { Ok(runtime.session().messages()?.iter().map(|message| message["role"].clone()).collect()) };
    let user_entry = |text_wanted: &str| -> io::Result<Value> {
        Ok(current()?
            .get_entries()?
            .as_array()
            .cloned()
            .unwrap_or_default()
            .into_iter()
            .find(|entry| entry["type"] == "message" && entry["message"]["role"] == "user" && entry_text(&entry["message"]) == text_wanted)
            .unwrap_or(Value::Null))
    };
    let assistant_id = || -> io::Result<String> {
        Ok(current()?
            .get_entries()?
            .as_array()
            .cloned()
            .unwrap_or_default()
            .iter()
            .find(|entry| entry["type"] == "message" && entry["message"]["role"] == "assistant")
            .map(|entry| text(&entry["id"]).to_string())
            .unwrap_or_default())
    };
    let cancelling = |run: &mut dyn FnMut() -> io::Result<()>| -> io::Result<()> {
        std::fs::write(&cancel_file, "")?;
        let result = run();
        std::fs::remove_file(&cancel_file)?;
        result
    };
    let parent_is = |file: &str| -> io::Result<bool> { Ok(current()?.get_header()?["parentSession"] == json!(file)) };
    let initial_manager = current()?;
    r.insert(
        "initial".into(),
        json!({
            "cwd": runtime.cwd(), "diagnostics": runtime.diagnostics(), "fallback": runtime.model_fallback_message(), "sameLog": initial_manager.get_session_id()? == manager.get_session_id()?,
            "servicesCwd": runtime.services().cwd, "servicesAgentDir": runtime.services().agent_dir == agent_dir, "calls": calls.lock().unwrap().len(),
        }),
    );
    runtime.session().prompt("one")?;
    r.insert("prompted".into(), json!({"kinds": kinds(&current()?)?, "persisted": Path::new(&run_file).exists()}));

    // newSession.
    let mut cancelled_new = CancelledResult::default();
    cancelling(&mut || {
        cancelled_new = runtime.new_session(NewSessionRuntimeOptions::default())?;
        Ok(())
    })?;
    r.insert(
        "newCancelled".into(),
        json!({"result": cancelled_new, "sameSession": !is_new(&current()?)?, "calls": calls.lock().unwrap().len(), "rebinds": rebinds.load(Ordering::SeqCst), "invalidations": invalidations.load(Ordering::SeqCst)}),
    );
    let created = runtime.new_session(NewSessionRuntimeOptions {
        parent_session: Some(run_file.clone()),
        setup: Some(Arc::new(|sm: &SessionManager| {
            sm.append_custom_message_entry("setup-note", json!("from setup"), true, None)?;
            Ok(())
        })),
        with_session: None,
    })?;
    let fresh = is_new(&current()?)?;
    r.insert(
        "newSession".into(),
        json!({
            "result": created, "fresh": fresh, "parentIsFirst": parent_is(&run_file)?, "kinds": kinds(&current()?)?, "roles": roles()?, "calls": calls.lock().unwrap()[1..].to_vec(),
            "rebinds": rebinds.load(Ordering::SeqCst), "invalidations": invalidations.load(Ordering::SeqCst),
        }),
    );
    runtime.session().prompt("two")?;

    // fork.
    let second_file = text(&current()?.get_session_file()?).to_string();
    let two_id = text(&user_entry("two")?["id"]).to_string();
    let mut cancelled_fork = ForkResult::default();
    cancelling(&mut || {
        cancelled_fork = runtime.fork(&two_id, ForkRuntimeOptions::default())?;
        Ok(())
    })?;
    r.insert("forkCancelled".into(), json!({"result": fork_view(&cancelled_fork), "sameSession": !is_new(&current()?)?}));
    let forked = runtime.fork(&two_id, ForkRuntimeOptions::default())?;
    let fresh = is_new(&current()?)?;
    r.insert(
        "fork".into(),
        json!({
            "result": fork_view(&forked), "fresh": fresh, "parentIsNotSecond": text(&current()?.get_session_file()?) != second_file, "roles": roles()?, "kinds": kinds(&current()?)?,
            "calls": calls.lock().unwrap().len() as i64 - 2,
        }),
    );
    runtime.session().prompt("three")?;
    let forked_at = runtime.fork(&assistant_id()?, ForkRuntimeOptions { position: Some("at".into()), with_session: None })?;
    let fresh = is_new(&current()?)?;
    r.insert("forkAt".into(), json!({"result": fork_view(&forked_at), "fresh": fresh, "roles": roles()?}));
    r.insert(
        "forkErrors".into(),
        json!({
            "missing": error_of(runtime.fork("no-such-entry", ForkRuntimeOptions::default())),
            "notUser": error_of(runtime.fork(&assistant_id()?, ForkRuntimeOptions::default())),
        }),
    );

    // switchSession.
    let mut cancelled_switch = CancelledResult::default();
    cancelling(&mut || {
        cancelled_switch = runtime.switch_session(&run_file, Default::default())?;
        Ok(())
    })?;
    r.insert("switchCancelled".into(), json!({"result": cancelled_switch, "sameSession": !is_new(&current()?)?}));
    let switched = runtime.switch_session(&run_file, Default::default())?;
    let switched_file = text(&current()?.get_session_file()?).to_string();
    r.insert(
        "switch".into(),
        json!({
            "result": switched, "backOnFirst": current()?.get_session_id()? == manager.get_session_id()?, "file": Path::new(&switched_file).file_name() == Path::new(&run_file).file_name(), "roles": roles()?,
        }),
    );

    // A fork before the first message.
    let one_id = text(&user_entry("one")?["id"]).to_string();
    let emptied = runtime.fork(&one_id, ForkRuntimeOptions::default())?;
    let fresh = is_new(&current()?)?;
    r.insert("forkRoot".into(), json!({"result": fork_view(&emptied), "fresh": fresh, "parentIsFirst": parent_is(&run_file)?, "kinds": kinds(&current()?)?}));

    // importFromJsonl.
    let import_dir = Path::new(root).join("import");
    std::fs::create_dir_all(&import_dir)?;
    let import_path = import_dir.join("imported.jsonl");
    std::fs::copy(&run_file, &import_path)?;
    let imported = runtime.import_from_jsonl(&import_path.to_string_lossy(), None)?;
    let imported_file = text(&current()?.get_session_file()?).to_string();
    let fresh = is_new(&current()?)?;
    r.insert(
        "import".into(),
        json!({
            "result": imported, "fresh": fresh, "inSessionDir": imported_file.starts_with(&session_dir),
            "name": Path::new(&imported_file).file_name().map(|name| name.to_string_lossy().into_owned()), "roles": roles()?, "sourceKept": import_path.exists(),
            "missing": error_of(runtime.import_from_jsonl(&import_dir.join("missing.jsonl").to_string_lossy(), None)),
        }),
    );

    // A direct refresh of the live session (the runtime's own after a setup is the one the report shows); Pi's run reads nothing from it, so it adds nothing to the report.
    runtime.session().refresh_context()?;
    runtime.dispose()?;
    let reasons: Vec<String> = calls.lock().unwrap().iter().map(|call| format!("{}{}", if call["reason"].is_null() { "null".to_string() } else { text(&call["reason"]).to_string() }, if call["previous"] == true { "+previous" } else { "" })).collect();
    r.insert(
        "counts".into(),
        json!({"rebinds": rebinds.load(Ordering::SeqCst), "invalidations": invalidations.load(Ordering::SeqCst), "factoryCalls": calls.lock().unwrap().len(), "reasons": reasons}),
    );
    let logged = std::fs::read_to_string(&log_file)?;
    r.insert("events".into(), json!(logged.trim_end_matches('\n').split('\n').collect::<Vec<_>>()));
    Ok(Value::Object(r))
}

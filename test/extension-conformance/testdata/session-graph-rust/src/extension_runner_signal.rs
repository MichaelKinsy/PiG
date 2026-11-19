//! The extension_runner_signal script of the session graph rows: the two sequences of testdata/extension-runner-signal.mjs through the Rust SDK's ExtensionRunner. `get_signal` answers the signal of the client's current run (Pi: one AbortController per run, agent.ts:518-527).

use pig_sdk::{
    Context, DefaultResourceLoader, DefaultResourceLoaderOptions, ExtensionActions,
    ExtensionContextActions, ExtensionRunner, ModelRuntime, ModelRuntimeCreateOptions,
    ProviderSignal, SessionManager, SettingsManager, create_extension_runtime,
};
use serde_json::{Value, json};
use std::io;
use std::sync::{Arc, Mutex};

fn text(value: &Value) -> &str {
    value.as_str().unwrap_or_default()
}

type Current = Arc<Mutex<Option<ProviderSignal>>>;

/// A runner over the signal probe, with appendEntry logged and getSignal answering `current`.
struct Rig {
    runner: ExtensionRunner,
    entries: Arc<Mutex<Vec<Value>>>,
    errors: Arc<Mutex<Vec<Value>>>,
    load_errors: Value,
}

impl Rig {
    fn new(ctx: &Context, args: &Value, current: &Current) -> io::Result<Rig> {
        let (cwd, agent_dir) = (text(&args["cwd"]), text(&args["agentDir"]));
        let rt = Arc::new(ModelRuntime::create(
            ctx,
            ModelRuntimeCreateOptions {
                auth_path: Some(text(&args["mrAuth"]).into()),
                models_path: Some(text(&args["mrModels"]).into()),
                models_store_path: Some(text(&args["mrStore"]).into()),
                ..Default::default()
            },
        )?);
        let settings = SettingsManager::in_memory(
            ctx,
            Some(
                json!({"compaction": {"enabled": false}, "retry": {"enabled": false}, "defaultProvider": "mr-local", "defaultModel": "m1"}),
            ),
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
                additional_extension_paths: vec![text(&args["signalProbe"]).into()],
                ..Default::default()
            },
        )?;
        loader.reload(None)?;
        let loaded = loader.get_extensions()?;
        // The loader's last use: from here only the loaded extensions name it.
        drop(loader);
        let manager = Arc::new(SessionManager::in_memory(ctx, Some(cwd), None, None)?);
        let runner = ExtensionRunner::new(
            ctx,
            &loaded.extensions,
            Some(&loaded.runtime),
            cwd,
            Some(manager),
            Some(rt),
        )?;
        let entries: Arc<Mutex<Vec<Value>>> = Arc::new(Mutex::new(Vec::new()));
        let errors: Arc<Mutex<Vec<Value>>> = Arc::new(Mutex::new(Vec::new()));
        let actions = ExtensionActions {
            append_entry: Some({
                let entries = entries.clone();
                Arc::new(move |custom_type, data| {
                    entries.lock().unwrap().push(json!([custom_type, data]));
                    Ok(())
                })
            }),
            ..Default::default()
        };
        let context_actions = ExtensionContextActions {
            get_signal: Some({
                let current = current.clone();
                Arc::new(move || Ok(current.lock().unwrap().clone()))
            }),
            ..Default::default()
        };
        runner.bind_core(actions, context_actions, None)?;
        // Held for the life of the runner, as the main script holds its listener (a dropped subscription unsubscribes).
        let _ = runner.on_error({
            let errors = errors.clone();
            Arc::new(move |report| {
                errors
                    .lock()
                    .unwrap()
                    .push(json!({"event": report["event"], "error": report["error"]}))
            })
        })?;
        Ok(Rig {
            runner,
            entries,
            errors,
            load_errors: loaded["errors"].clone(),
        })
    }

    fn report(&self) -> Value {
        json!({"entries": self.entries.lock().unwrap().clone(), "errors": self.errors.lock().unwrap().clone(), "loadErrors": self.load_errors})
    }
}

pub fn extension_runner_signal_script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let current: Current = Arc::new(Mutex::new(None));
    let set = |signal: &ProviderSignal| *current.lock().unwrap() = Some(signal.clone());
    let turn = json!({"type": "turn_start", "turnIndex": 0, "timestamp": 1});
    let message = json!({"type": "message_start", "message": {"role": "user", "content": "hi", "timestamp": 1}});

    // Sequence 1, two live runs.
    let first = ProviderSignal::new();
    set(&first);
    let rig = Rig::new(ctx, args, &current)?;
    rig.runner.emit(json!({"type": "agent_start"}))?;
    let second = ProviderSignal::new();
    set(&second);
    rig.runner.emit(turn.clone())?;
    second.cancel();
    rig.runner.emit(message.clone())?;
    let two_live_runs = rig.report();

    // Sequence 2, the own abort missed.
    let third = ProviderSignal::new();
    set(&third);
    let rig = Rig::new(ctx, args, &current)?;
    rig.runner.emit(json!({"type": "agent_start"}))?;
    third.cancel();
    let fourth = ProviderSignal::new();
    set(&fourth);
    // The client drops its own handle to run 1's aborted signal before run 2 is read: the wrapper must hold the signal itself, or it cannot say run 1 had aborted. Rust has no collector to force: the last handle frees the signal at once.
    drop(third);
    rig.runner.emit(turn)?;
    fourth.cancel();
    rig.runner.emit(message)?;
    let aborted_then_live = rig.report();
    Ok(json!({"twoLiveRuns": two_live_runs, "abortedThenLive": aborted_then_live}))
}

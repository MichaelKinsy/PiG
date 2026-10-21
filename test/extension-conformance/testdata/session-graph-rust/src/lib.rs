//! Fixture for the session graph conformance rows (test/extension-conformance/session_graph_test.go): the script of testfixture/sessiongraph (Go), testdata/session-graph.mjs (Node, Pi's own classes) and testdata/session_graph_python.py.

use pig_sdk::{CreateAgentSessionOptions, ModelRuntime, ModelRuntimeCreateOptions, ModelRoute, VirtualModel, DefaultPackageManager, PackageManagerOptions, DefaultResourceLoader, DefaultResourceLoaderOptions, empty_schema, Context, Extension, NewSessionOptions, SessionInfo, SessionManager, SettingsManager, SettingsManagerCreateOptions, ToolResult, has_trust_requiring_project_resources, ProjectTrustStore, ProjectTrustStoreEntry, ProjectTrustUpdate};
use serde_json::{json, Value};
use std::io;
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::{SystemTime, UNIX_EPOCH};

fn text(value: &Value) -> &str {
    value.as_str().unwrap_or_default()
}

fn shape(entries: &Value) -> Vec<String> {
    let entries = entries.as_array().map(Vec::as_slice).unwrap_or_default();
    entries
        .iter()
        .map(|entry| {
            let parent = entry["parentId"]
                .as_str()
                .and_then(|id| entries.iter().position(|candidate| candidate["id"].as_str() == Some(id)))
                .map_or(-1, |at| at as i64);
            format!("{}<{}", text(&entry["type"]), parent)
        })
        .collect()
}

fn failure<T>(result: io::Result<T>, contains: &str) -> String {
    match result {
        Ok(_) => "no error".into(),
        Err(error) if error.to_string().contains(contains) => format!("error: {contains}"),
        Err(error) => format!("error: {error}"),
    }
}

fn infos(list: Vec<SessionInfo>) -> Value {
    let mut out: Vec<Value> = list
        .into_iter()
        .map(|info| {
            json!({
                "id": info.id, "messageCount": info.message_count, "name": info.name, "firstMessage": info.first_message, "cwd": info.cwd,
                "createdRecent": millis(info.created) > 1767225600000, "modifiedMs": millis(info.modified), "fileInDir": !info.path.is_empty(),
            })
        })
        .collect();
    out.sort_by(|a, b| text(&a["id"]).cmp(text(&b["id"])));
    Value::Array(out)
}

fn millis(time: SystemTime) -> i64 {
    match time.duration_since(UNIX_EPOCH) {
        Ok(after) => after.as_millis() as i64,
        Err(before) => -(before.duration().as_millis() as i64),
    }
}

fn dir_of(path: &str) -> String {
    Path::new(path).parent().map(|parent| parent.to_string_lossy().into_owned()).unwrap_or_default()
}

fn assistant() -> Value {
    json!({
        "role": "assistant", "content": [{"type": "text", "text": "hi"}], "api": "test", "provider": "p", "model": "m",
        "usage": {"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
        "stopReason": "stop", "timestamp": 2,
    })
}

fn id(options: &str) -> NewSessionOptions {
    NewSessionOptions { id: Some(options.into()), parent_session: None }
}

fn script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let (cwd, dir, other_dir, junk) = (text(&args["cwd"]), text(&args["dir"]), text(&args["otherDir"]), text(&args["junk"]));
    let mut r = serde_json::Map::new();
    let fixed = "fixed-session-1";
    let m = SessionManager::create(ctx, cwd, Some(dir), Some(&id(fixed)))?;
    let file = text(&m.get_session_file()?).to_string();
    r.insert("created".into(), json!({
        "id": m.get_session_id()?, "persisted": m.is_persisted()?, "dir": m.get_session_dir()?, "fileSuffix": file.ends_with("_fixed-session-1.jsonl"),
        "fileInDir": dir_of(&file) == dir, "cwd": m.get_cwd()?, "defaultDir": m.uses_default_session_dir()?, "entries": m.get_entry_count()?, "leaf": m.get_leaf_id()?,
    }));
    let first = text(&m.append_message(json!({"role": "user", "content": "hello", "timestamp": 1}))?).to_string();
    m.append_message(assistant())?;
    m.append_session_info("graph")?;
    m.append_label_change(&first, Some("start"))?;
    m.append_custom_entry("note", Some(json!({"a": 1, "b": 2})))?;
    m.append_thinking_level_change("high")?;
    let last = text(&m.append_model_change("p", "m")?).to_string();
    let entries = m.get_entries()?;
    let header = m.get_header()?;
    let custom = entries.as_array().and_then(|list| list.iter().find(|entry| entry["type"] == "custom")).cloned().unwrap_or(json!({}));
    r.insert("log".into(), json!({
        "name": m.get_session_name()?, "label": m.get_label(&first)?, "count": m.get_entry_count()?, "shape": shape(&entries), "leafIsLast": m.get_leaf_id()?.as_str() == Some(&last),
        "entryType": m.get_entry(&first)?["type"], "missing": m.get_entry("nope")?.is_null(),
        "headerType": header["type"], "headerVersion": header["version"], "headerId": header["id"], "headerCwd": header["cwd"],
        "customType": custom["customType"], "customData": custom["data"],
    }));
    let built = m.build_session_context()?;
    r.insert("context".into(), json!({"messages": built["messages"].as_array().map_or(0, Vec::len), "thinkingLevel": built["thinkingLevel"], "model": built["model"]}));
    let tree = m.get_tree()?;
    r.insert("tree".into(), json!({"roots": tree.as_array().map_or(0, Vec::len), "firstChildren": tree[0]["children"].as_array().map_or(0, Vec::len)}));

    let msg_id = text(&m.append_custom_message_entry("note-msg", json!("body"), true, Some(json!({"k": 1})))?).to_string();
    let usage = m.append_usage("test", "p", "m", json!({"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}), None)?;
    let msg_entry = m.get_entry(&msg_id)?;
    r.insert("extra".into(), json!({"msgType": msg_entry["type"], "msgCustomType": msg_entry["customType"], "usageType": usage["type"], "usageKind": usage["kind"]}));

    m.branch(&first)?;
    let alt = text(&m.append_message(json!({"role": "user", "content": "alt", "timestamp": 3}))?).to_string();
    r.insert("branch".into(), json!({"shape": shape(&m.get_branch(None)?), "children": m.get_children(&first)?.as_array().map_or(0, Vec::len), "leafIsAlt": m.get_leaf_id()?.as_str() == Some(&alt)}));
    m.reset_leaf()?;
    r.insert("reset".into(), json!({"leaf": m.get_leaf_id()?}));
    let summary = text(&m.branch_with_summary(Some(&first), "summary text", None, None, None)?).to_string();
    let summary_entry = m.get_entry(&summary)?;
    r.insert("summary".into(), json!({"type": summary_entry["type"], "parentIsFirst": summary_entry["parentId"] == json!(first), "text": summary_entry["summary"], "fromId": summary_entry["fromId"] == json!(first)}));
    let branched = m.create_branched_session(&summary)?;
    let branched = branched.as_str();
    r.insert("branched".into(), json!({
        "isFile": branched.is_some_and(|path| path.ends_with(".jsonl")), "inDir": branched.is_some_and(|path| dir_of(path) == dir), "newFile": branched.is_some_and(|path| path != file),
        "idChanged": text(&m.get_session_id()?) != fixed, "count": m.get_entry_count()?, "shape": shape(&m.get_entries()?),
    }));

    let opened = SessionManager::open(ctx, &file, None, None)?;
    r.insert("opened".into(), json!({
        "id": opened.get_session_id()?, "count": opened.get_entry_count()?, "cwd": opened.get_cwd()?, "headerCwd": opened.get_header()?["cwd"],
        "dirIsFileDir": text(&opened.get_session_dir()?) == dir, "file": text(&opened.get_session_file()?) == file, "name": opened.get_session_name()?,
    }));
    let overridden = SessionManager::open(ctx, &file, None, Some(other_dir))?;
    r.insert("override".into(), json!({"cwd": overridden.get_cwd()?, "id": overridden.get_session_id()?}));
    let recent = SessionManager::continue_recent(ctx, cwd, Some(dir))?;
    let recent_file = recent.get_session_file()?;
    r.insert("recent".into(), json!({"inDir": recent_file.as_str().is_some_and(|path| dir_of(path) == dir), "hasEntries": recent.get_entry_count()?.as_u64().unwrap_or(0) > 0, "cwd": recent.get_cwd()?}));

    let mem = SessionManager::in_memory(ctx, Some(cwd), Some(&id("mem-1")), None)?;
    let before = mem.get_leaf_id()?;
    mem.append_message(json!({"role": "user", "content": "x", "timestamp": 1}))?;
    r.insert("memory".into(), json!({
        "id": mem.get_session_id()?, "persisted": mem.is_persisted()?, "file": mem.get_session_file()?, "dir": mem.get_session_dir()?,
        "defaultDir": mem.uses_default_session_dir()?, "leafBefore": before, "count": mem.get_entry_count()?, "cwd": mem.get_cwd()?,
    }));
    let seeded = SessionManager::in_memory(ctx, Some(cwd), None, Some(vec![json!({"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-01T00:00:00.000Z", "message": {"role": "user", "content": "seed", "timestamp": 1}})]))?;
    r.insert("seeded".into(), json!({"count": seeded.get_entry_count()?, "leaf": seeded.get_leaf_id()?, "hasId": text(&seeded.get_session_id()?) != ""}));

    let fork_dir = Path::new(other_dir).join("sessions").to_string_lossy().into_owned();
    let forked = SessionManager::fork_from(ctx, &file, other_dir, Some(&fork_dir), Some(&id("forked-1")))?;
    r.insert("fork".into(), json!({
        "id": forked.get_session_id()?, "count": forked.get_entry_count()?, "parent": forked.get_header()?["parentSession"] == json!(file), "cwd": text(&forked.get_cwd()?) == other_dir,
        "inDir": dir_of(text(&forked.get_session_file()?)) == fork_dir, "shape": shape(&forked.get_entries()?),
    }));

    r.insert("list".into(), infos(SessionManager::list(ctx, cwd, Some(dir), None)?));
    let list_progress = std::sync::Arc::new(std::sync::Mutex::new(Vec::<(u64, u64)>::new()));
    let list_all_progress = std::sync::Arc::new(std::sync::Mutex::new(Vec::<(u64, u64)>::new()));
    let (list_sink, list_all_sink) = (list_progress.clone(), list_all_progress.clone());
    r.insert("listAll".into(), infos(SessionManager::list_all(ctx, Some(dir), Some(std::sync::Arc::new(move |loaded, total, _| list_all_sink.lock().unwrap().push((loaded, total)))))?));
    infos(SessionManager::list(ctx, cwd, Some(dir), Some(std::sync::Arc::new(move |loaded, total, _| list_sink.lock().unwrap().push((loaded, total)))))?);
    let last = |calls: &std::sync::Mutex<Vec<(u64, u64)>>| {
        let calls = calls.lock().unwrap();
        json!({"calls": !calls.is_empty(), "last": calls.last().map(|(loaded, total)| json!([loaded, total]))})
    };
    r.insert("progress".into(), json!({"list": last(&list_progress), "listAll": last(&list_all_progress)}));
    r.insert("listFork".into(), infos(SessionManager::list(ctx, other_dir, Some(&fork_dir), None)?));
    let found = SessionManager::find_by_id(ctx, cwd, fixed, Some(dir))?;
    r.insert("find".into(), json!({"found": found.as_deref() == Some(file.as_str()), "missing": SessionManager::find_by_id(ctx, cwd, "nope", Some(dir))?.is_none()}));

    r.insert("errors".into(), json!({
        "badId": failure(SessionManager::create(ctx, cwd, Some(dir), Some(&id("bad id!"))), "Session id must be"),
        "notSession": failure(SessionManager::open(ctx, junk, None, None), "not a valid"),
        "noFork": failure(SessionManager::fork_from(ctx, &Path::new(dir).join("missing.jsonl").to_string_lossy(), cwd, Some(&fork_dir), None), "Cannot fork"),
    }));
    let compaction_id = text(&m.append_compaction("sum", &first, 100, Some(json!({"k": 1})), None, None)?).to_string();
    let compaction = m.get_entry(&compaction_id)?;
    let edit_id = text(&m.append_context_edit(&first, json!({"content": "edited"}))?).to_string();
    let edit = m.get_entry(&edit_id)?;
    let clear_id = text(&m.append_context_edit(&first, Value::Null)?).to_string();
    let switched = SessionManager::create(ctx, cwd, Some(dir), Some(&id("reopen-1")))?;
    switched.set_session_file(&file)?;
    r.insert("compaction".into(), json!({
        "type": compaction["type"], "summary": compaction["summary"], "firstKept": compaction["firstKeptEntryId"] == json!(first), "tokensBefore": compaction["tokensBefore"], "details": compaction["details"], "fromHook": compaction["fromHook"],
        "editType": edit["type"], "replacement": edit["replacement"], "target": edit["targetId"] == json!(first), "cleared": m.get_entry(&clear_id)?["replacement"], "leaf": m.get_leaf_id()? == json!(clear_id),
        "badTarget": failure(m.append_context_edit("nope", Value::Null), "not found"), "badReplacement": failure(m.append_context_edit(&first, json!({"content": 5})), "must be null or contain"),
        "switched": {
            "id": switched.get_session_id()? == m.get_session_id()?, "file": switched.get_session_file()? == json!(file),
            "shape": shape(&switched.get_entries()?).join(",") == shape(&m.get_entries()?).join(","), "count": switched.get_entry_count()?,
        },
        "badSwitch": failure(switched.set_session_file(junk), "not a valid"),
    }));

    Ok(Value::Object(r))
}


fn read_json(path: &Path) -> io::Result<Value> {
    let text = std::fs::read_to_string(path)?;
    serde_json::from_str(&text).map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error.to_string()))
}

macro_rules! read_all_getters {
    ($s:ident, $($name:literal => $method:ident),* $(,)?) => {{
        let mut report = serde_json::Map::new();
        $( report.insert($name.into(), json!($s.$method()?)); )*
        Ok(Value::Object(report))
    }};
}

fn read_all(s: &SettingsManager) -> io::Result<Value> {
    read_all_getters!(s,
        "getDefaultProvider" => get_default_provider,
        "getDefaultModel" => get_default_model,
        "getDefaultThinkingLevel" => get_default_thinking_level,
        "getTheme" => get_theme,
        "getThemeSetting" => get_theme_setting,
        "getSteeringMode" => get_steering_mode,
        "getFollowUpMode" => get_follow_up_mode,
        "getTransport" => get_transport,
        "getSessionShareDestination" => get_session_share_destination,
        "getCompactionEnabled" => get_compaction_enabled,
        "getBranchSummarySkipPrompt" => get_branch_summary_skip_prompt,
        "getRetryEnabled" => get_retry_enabled,
        "getHttpIdleTimeoutMs" => get_http_idle_timeout_ms,
        "getCacheWarmingMode" => get_cache_warming_mode,
        "getWebSocketConnectTimeoutMs" => get_web_socket_connect_timeout_ms,
        "getHideThinkingBlock" => get_hide_thinking_block,
        "getShowCacheMissNotices" => get_show_cache_miss_notices,
        "getExternalEditorCommand" => get_external_editor_command,
        "getShellPath" => get_shell_path,
        "getQuietStartup" => get_quiet_startup,
        "getDefaultProjectTrust" => get_default_project_trust,
        "getShellCommandPrefix" => get_shell_command_prefix,
        "getNpmCommand" => get_npm_command,
        "getCollapseChangelog" => get_collapse_changelog,
        "getEnableInstallTelemetry" => get_enable_install_telemetry,
        "getEnableAnalytics" => get_enable_analytics,
        "getPackages" => get_packages,
        "getExtensionPaths" => get_extension_paths,
        "getSkillPaths" => get_skill_paths,
        "getPromptTemplatePaths" => get_prompt_template_paths,
        "getThemePaths" => get_theme_paths,
        "getEnableSkillCommands" => get_enable_skill_commands,
        "getThinkingBudgets" => get_thinking_budgets,
        "getTerminalCapabilityOverrides" => get_terminal_capability_overrides,
        "getShowImages" => get_show_images,
        "getImageWidthCells" => get_image_width_cells,
        "getClearOnShrink" => get_clear_on_shrink,
        "getShowTerminalProgress" => get_show_terminal_progress,
        "getTuiMode" => get_tui_mode,
        "getFullscreenExitOutput" => get_fullscreen_exit_output,
        "getFullscreenScrollbar" => get_fullscreen_scrollbar,
        "getFullscreenCopyOnSelect" => get_fullscreen_copy_on_select,
        "getFullscreenWheelScrollLines" => get_fullscreen_wheel_scroll_lines,
        "getImageAutoResize" => get_image_auto_resize,
        "getBlockImages" => get_block_images,
        "getEnabledModels" => get_enabled_models,
        "getDefaultTools" => get_default_tools,
        "getDoubleEscapeAction" => get_double_escape_action,
        "getTreeFilterMode" => get_tree_filter_mode,
        "getShowHardwareCursor" => get_show_hardware_cursor,
        "getEditorPaddingX" => get_editor_padding_x,
        "getOutputPad" => get_output_pad,
        "getAutocompleteMaxVisible" => get_autocomplete_max_visible,
        "getCodeBlockIndent" => get_code_block_indent,
        "getMermaidRenderingMode" => get_mermaid_rendering_mode,
        "getWarnings" => get_warnings,
        "getAllModelThinkingLevels" => get_all_model_thinking_levels,
        "getBranchSummarySettings" => get_branch_summary_settings,
        "getRetrySettings" => get_retry_settings,
        "getProviderRetrySettings" => get_provider_retry_settings,
        "getLastChangelogVersion" => get_last_changelog_version,
        "getSessionDir" => get_session_dir,
        "getSettings" => get_settings,
        "getGlobalSettings" => get_global_settings,
        "getProjectSettings" => get_project_settings,
        "isProjectTrusted" => is_project_trusted,
    )
}

struct MemoryStorage {
    store: std::sync::Mutex<std::collections::HashMap<String, String>>,
    calls: std::sync::Arc<std::sync::Mutex<Vec<String>>>,
    fail: bool,
}

impl pig_sdk::SettingsStorage for MemoryStorage {
    fn with_lock(&self, scope: &str, f: &mut dyn FnMut(Option<String>) -> Result<Option<String>, String>) -> Result<(), String> {
        if self.fail {
            return Err("disk full".into());
        }
        let current = self.store.lock().unwrap().get(scope).cloned();
        self.calls.lock().unwrap().push(format!("{scope}:{}", if current.is_none() { "none" } else { "text" }));
        if let Some(next) = f(current)? {
            self.calls.lock().unwrap().push(format!("{scope}:write"));
            self.store.lock().unwrap().insert(scope.to_string(), next);
        }
        Ok(())
    }
}

fn parsed_text(text: Option<String>) -> Value {
    text.map_or(Value::Null, |text| serde_json::from_str(&text).unwrap_or(Value::Null))
}

fn storage_report(ctx: &Context) -> io::Result<Value> {
    let calls = std::sync::Arc::new(std::sync::Mutex::new(Vec::<String>::new()));
    let storage = std::sync::Arc::new(MemoryStorage { store: std::sync::Mutex::new([("global".to_string(), r#"{"theme":"dark","quietStartup":true}"#.to_string())].into()), calls: calls.clone(), fail: false });
    let snapshot = || calls.lock().unwrap().clone();
    let read = |scope: &str| storage.store.lock().unwrap().get(scope).cloned();
    let mut out = serde_json::Map::new();
    let manager = SettingsManager::from_storage(ctx, storage.clone(), None)?;
    out.insert("loaded".into(), json!({"theme": manager.get_theme()?, "quiet": manager.get_quiet_startup()?, "calls": snapshot(), "errors": manager.drain_errors()?.len()}));
    calls.lock().unwrap().clear();
    manager.set_theme("light")?;
    manager.flush()?;
    out.insert("wrote".into(), json!({"calls": snapshot(), "global": parsed_text(read("global")), "project": read("project").is_none(), "theme": manager.get_theme()?}));
    storage.store.lock().unwrap().insert("global".into(), r#"{"theme":"solarized"}"#.into());
    calls.lock().unwrap().clear();
    manager.reload()?;
    out.insert("reloaded".into(), json!({"theme": manager.get_theme()?, "calls": snapshot()}));
    calls.lock().unwrap().clear();
    manager.set_project_extension_paths(&["./e"])?;
    manager.flush()?;
    out.insert("projectWrote".into(), json!({"project": parsed_text(read("project")), "calls": snapshot(), "paths": manager.get_project_settings()?.get("extensions").cloned().unwrap_or(Value::Null)}));
    calls.lock().unwrap().clear();
    let untrusted = SettingsManager::from_storage(ctx, storage.clone(), Some(&SettingsManagerCreateOptions { project_trusted: Some(false) }))?;
    out.insert("untrusted".into(), json!({"calls": snapshot(), "theme": untrusted.get_theme()?, "trusted": untrusted.is_project_trusted()?}));
    storage.store.lock().unwrap().insert("global".into(), "{bad".into());
    let broken = SettingsManager::from_storage(ctx, storage.clone(), None)?;
    out.insert("broken".into(), json!(broken.drain_errors()?.iter().map(|entry| json!({"scope": entry.scope, "noPath": entry.path.is_none(), "hasMessage": !entry.error.is_empty()})).collect::<Vec<_>>()));
    let failing = SettingsManager::from_storage(ctx, std::sync::Arc::new(MemoryStorage { store: Default::default(), calls: Default::default(), fail: true }), None)?;
    out.insert("failing".into(), json!(failing.drain_errors()?.iter().map(|entry| json!({"scope": entry.scope, "message": entry.error})).collect::<Vec<_>>()));
    Ok(Value::Object(out))
}

fn settings_script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let (cwd, agent_dir, config_dir, broken_cwd) = (text(&args["cwd"]), text(&args["agentDir"]), text(&args["configDir"]), text(&args["brokenCwd"]));
    let mut r = serde_json::Map::new();
    let model = || Some(json!({"provider": "p", "id": "m"}));
    let s = SettingsManager::create(ctx, cwd, Some(agent_dir), None)?;
    r.insert("initial".into(), read_all(&s)?);
    r.insert("modelBudget".into(), json!({
        "reserve": s.get_compaction_reserve_tokens(model())?, "reserveNone": s.get_compaction_reserve_tokens(None)?, "keep": s.get_compaction_keep_recent_tokens(None)?,
        "settings": s.get_compaction_settings(model())?, "thinking": s.get_model_thinking_level("p", "m")?,
    }));
    s.set_default_provider("openai")?;
    s.set_default_model("gpt")?;
    s.set_default_thinking_level("high")?;
    s.set_theme("light")?;
    s.set_steering_mode("one-at-a-time")?;
    s.set_follow_up_mode("one-at-a-time")?;
    s.set_compaction_enabled(true)?;
    s.set_retry_enabled(false)?;
    s.set_packages(json!(["npm:a", {"source": "npm:b", "extensions": ["x"]}]))?;
    s.set_extension_paths(&["a", "b"])?;
    s.set_skill_paths(&["s1"])?;
    s.set_prompt_template_paths(&["p1"])?;
    s.set_theme_paths(&["t1"])?;
    s.set_enabled_models(Some(&["m1", "m2"]))?;
    s.set_quiet_startup(json!("header"))?;
    s.set_shell_path(Some("/bin/sh"))?;
    s.set_shell_command_prefix(Some("set -e"))?;
    s.set_npm_command(Some(&["pnpm", "dlx"]))?;
    s.set_model_thinking_level("p", "m", "low")?;
    s.set_http_idle_timeout_ms(5000.0)?;
    s.set_image_width_cells(40)?;
    s.set_output_pad(0)?;
    s.set_tui_mode("fullscreen")?;
    s.set_fullscreen_wheel_scroll_lines(json!(3))?;
    s.set_warnings(json!({"anthropicExtraUsage": true}))?;
    s.set_default_project_trust("never")?;
    s.set_last_changelog_version("9.9.9")?;
    s.set_enable_analytics(false)?;
    s.set_transport("sse")?;
    s.set_cache_warming_mode("idle")?;
    s.set_hide_thinking_block(true)?;
    s.set_show_images(false)?;
    s.set_clear_on_shrink(true)?;
    s.set_double_escape_action("tree")?;
    s.set_tree_filter_mode("user-only")?;
    s.set_mermaid_rendering_mode("off")?;
    s.set_editor_padding_x(2)?;
    s.set_autocomplete_max_visible(7)?;
    s.set_collapse_changelog(true)?;
    s.set_block_images(true)?;
    s.set_session_share_destination("gist")?;
    s.set_fullscreen_exit_output("resume-hint")?;
    s.set_fullscreen_scrollbar("hidden")?;
    s.set_fullscreen_copy_on_select(false)?;
    s.set_show_hardware_cursor(true)?;
    s.set_image_auto_resize(false)?;
    s.set_show_terminal_progress(true)?;
    s.set_enable_skill_commands(false)?;
    s.set_enable_install_telemetry(false)?;
    s.set_show_cache_miss_notices(false)?;
    s.set_default_model_and_provider("anthropic", "opus")?;
    s.set_project_packages(json!(["npm:proj"]))?;
    s.set_project_extension_paths(&["pe"])?;
    s.set_project_skill_paths(&["ps"])?;
    s.set_project_prompt_template_paths(&["pp"])?;
    s.set_project_theme_paths(&["pt"])?;
    s.flush()?;
    r.insert("afterSet".into(), read_all(&s)?);
    r.insert("modelThinking".into(), json!(s.get_model_thinking_level("p", "m")?));
    s.remove_model_thinking_level("p", "m")?;
    r.insert("removed".into(), json!({"thinking": s.get_model_thinking_level("p", "m")?, "trackingId": s.get_tracking_id()?, "deviceId": !s.get_or_create_device_id()?.is_empty()}));
    s.flush()?;
    r.insert("files".into(), json!({"global": read_json(&Path::new(agent_dir).join("settings.json"))?, "project": read_json(&Path::new(cwd).join(config_dir).join("settings.json"))?}));

    let persisted = SettingsManager::create(ctx, cwd, Some(agent_dir), None)?;
    r.insert("persisted".into(), read_all(&persisted)?);
    std::fs::write(Path::new(agent_dir).join("settings.json"), r#"{"defaultProvider":"external"}"#)?;
    persisted.reload()?;
    r.insert("reload".into(), json!({"provider": persisted.get_default_provider()?, "model": persisted.get_default_model()?, "merged": persisted.get_settings()?}));
    persisted.set_shell_path(None)?;
    persisted.set_shell_command_prefix(None)?;
    persisted.set_npm_command(None)?;
    persisted.set_enabled_models(None)?;
    persisted.set_default_model("kept")?;
    persisted.flush()?;
    r.insert("cleared".into(), json!({
        "shellPath": persisted.get_shell_path()?, "prefix": persisted.get_shell_command_prefix()?, "npm": persisted.get_npm_command()?, "enabled": persisted.get_enabled_models()?,
        "file": read_json(&Path::new(agent_dir).join("settings.json"))?,
    }));

    let memory = SettingsManager::in_memory(ctx, Some(json!({"defaultProvider": "mem", "compaction": {"enabled": false}, "retry": {"maxRetries": 9}})), None)?;
    r.insert("memory".into(), json!({
        "provider": memory.get_default_provider()?, "compaction": memory.get_compaction_settings(None)?, "retry": memory.get_retry_settings()?, "merged": memory.get_settings()?,
        "trusted": memory.is_project_trusted()?,
    }));
    memory.set_default_model("mm")?;
    memory.apply_overrides(json!({"defaultModel": "ov"}))?;
    r.insert("memoryAfter".into(), json!({"model": memory.get_default_model()?, "global": memory.get_global_settings()?, "merged": memory.get_settings()?}));
    let untrusted = SettingsManager::in_memory(ctx, None, Some(&SettingsManagerCreateOptions { project_trusted: Some(false) }))?;
    r.insert("untrustedMemory".into(), json!({"trusted": untrusted.is_project_trusted()?, "write": failure(untrusted.set_project_packages(json!(["x"])), "refusing to write project settings")}));

    let broken = SettingsManager::create(ctx, broken_cwd, Some(agent_dir), None)?;
    let drained = broken.drain_errors()?;
    let mut entry = serde_json::Map::new();
    entry.insert("count".into(), json!(drained.len()));
    if let Some(first) = drained.first() {
        entry.insert("scope".into(), json!(first.scope));
        entry.insert("pathSuffix".into(), json!(first.path.as_deref().is_some_and(|path| path.ends_with(&*Path::new(config_dir).join("settings.json").to_string_lossy()))));
        entry.insert("hasMessage".into(), json!(!first.error.is_empty()));
    }
    entry.insert("again".into(), json!(broken.drain_errors()?.len()));
    r.insert("errors".into(), Value::Object(entry));

    let distrust = SettingsManager::create(ctx, cwd, Some(agent_dir), Some(&SettingsManagerCreateOptions { project_trusted: Some(false) }))?;
    r.insert("distrusted".into(), json!({
        "trusted": distrust.is_project_trusted()?, "project": distrust.get_project_settings()?, "merged": distrust.get_settings()?,
        "write": failure(distrust.set_project_extension_paths(&["z"]), "refusing to write project settings"),
    }));
    distrust.set_project_trusted(true)?;
    r.insert("retrusted".into(), json!({"trusted": distrust.is_project_trusted()?, "projectModel": distrust.get_project_settings()?["defaultModel"]}));
    r.insert("storage".into(), storage_report(ctx)?);
    Ok(Value::Object(r))
}

fn entry_value(entry: Option<ProjectTrustStoreEntry>) -> Value {
    entry.map_or(Value::Null, |entry| json!({"path": entry.path, "decision": entry.decision}))
}

fn trust_script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let (root, cwd, agent_dir, other) = (text(&args["root"]), text(&args["cwd"]), text(&args["agentDir"]), text(&args["otherDir"]));
    let sub = Path::new(cwd).join("sub").to_string_lossy().into_owned();
    let anc = Path::new(root).join("anc").to_string_lossy().into_owned();
    let anc_project = Path::new(root).join("anc").join("proj").to_string_lossy().into_owned();
    let bare = Path::new(root).join("bare").to_string_lossy().into_owned();
    let trust_file = Path::new(agent_dir).join("trust.json");
    let mut r = serde_json::Map::new();
    let store = ProjectTrustStore::new(ctx, agent_dir)?;
    r.insert("initial".into(), json!({"get": store.get(cwd)?, "entry": entry_value(store.get_entry(cwd)?)}));
    ProjectTrustStore::set(&store, cwd, Some(true))?;
    store.set(other, Some(false))?;
    r.insert("saved".into(), json!({"get": store.get(cwd)?, "entry": entry_value(store.get_entry(&sub)?), "otherGet": store.get(other)?, "file": read_json(&trust_file)?}));
    store.set_many(&[ProjectTrustUpdate { path: cwd.into(), decision: None }, ProjectTrustUpdate { path: anc.clone(), decision: Some(true) }])?;
    r.insert("many".into(), json!({"cwd": store.get(cwd)?, "anc": store.get(&anc_project)?, "entryAnc": entry_value(store.get_entry(&anc_project)?), "file": read_json(&trust_file)?}));
    store.set(&sub, Some(false))?;
    r.insert("nested".into(), json!({"sub": store.get(&sub)?, "parent": store.get(cwd)?, "file": read_json(&trust_file)?}));
    let failure_of_get = |dir: &str, contains: &str| -> io::Result<String> {
        let broken = ProjectTrustStore::new(ctx, &Path::new(root).join(dir).to_string_lossy())?;
        Ok(failure(broken.get(cwd), contains))
    };
    r.insert("errors".into(), json!({
        "notObject": failure_of_get("agent-array", "Invalid trust store")?, "unreadable": failure_of_get("agent-bad-json", "Failed to read trust store")?,
        "badValue": failure_of_get("agent-bad-value", "must be true, false, or null")?,
    }));
    r.insert("resources".into(), json!({
        "config": has_trust_requiring_project_resources(ctx, cwd)?, "bare": has_trust_requiring_project_resources(ctx, &bare)?, "ancestor": has_trust_requiring_project_resources(ctx, &anc_project)?,
    }));
    Ok(Value::Object(r))
}

fn base_name(value: &Value) -> String {
    Path::new(text(value)).file_name().map(|name| name.to_string_lossy().into_owned()).unwrap_or_default()
}

fn diagnostic_texts(list: &Value) -> Vec<String> {
    list.as_array().map(|list| list.iter().map(|d| format!("{}: {}", text(&d["type"]), text(&d["message"]))).collect()).unwrap_or_default()
}

fn skill_report(result: &Value) -> Value {
    let mut skills: Vec<Value> = result["skills"].as_array().map(|list| list.iter().map(|skill| json!({
        "name": skill["name"], "description": skill["description"], "disable": skill["disableModelInvocation"].as_bool().unwrap_or(false), "file": base_name(&skill["filePath"]),
        "baseDir": base_name(&skill["baseDir"]), "scope": skill["sourceInfo"]["scope"], "origin": skill["sourceInfo"]["origin"], "source": skill["sourceInfo"]["source"],
    })).collect()).unwrap_or_default();
    skills.sort_by(|a, b| text(&a["name"]).cmp(text(&b["name"])));
    json!({"skills": skills, "diagnostics": diagnostic_texts(&result["diagnostics"])})
}

fn prompt_report(result: &Value) -> Value {
    let mut prompts: Vec<Value> = result["prompts"].as_array().map(|list| list.iter().map(|prompt| json!({
        "name": prompt["name"], "description": prompt["description"], "argumentHint": prompt["argumentHint"].as_str().unwrap_or(""), "content": prompt["content"],
        "file": base_name(&prompt["filePath"]), "scope": prompt["sourceInfo"]["scope"],
    })).collect()).unwrap_or_default();
    prompts.sort_by(|a, b| text(&a["name"]).cmp(text(&b["name"])));
    json!({"prompts": prompts, "diagnostics": diagnostic_texts(&result["diagnostics"])})
}

fn resource_report(loader: &DefaultResourceLoader) -> io::Result<Value> {
    let extensions = loader.get_extensions()?;
    let source = loader.get_system_prompt_source()?;
    let files: Vec<Value> = loader.get_agents_files()?["agentsFiles"].as_array().map(|list| list.iter().map(|file| json!({"path": file["path"], "content": file["content"]})).collect()).unwrap_or_default();
    let sources: Vec<Value> = loader.get_append_system_prompt_sources()?.as_array().map(|list| list.iter().map(|item| item["path"].clone()).collect()).unwrap_or_default();
    Ok(json!({
        "extensions": extensions["extensions"].as_array().map_or(0, Vec::len), "extensionErrors": extensions["errors"].as_array().map_or(0, Vec::len),
        "themes": loader.get_themes()?["themes"].as_array().map_or(0, Vec::len),
        "skills": skill_report(&loader.get_skills()?), "prompts": prompt_report(&loader.get_prompts()?), "agentsFiles": files,
        "systemPrompt": loader.get_system_prompt()?, "systemPromptSource": if source.is_null() { Value::Null } else { source["path"].clone() },
        "appendSystemPrompt": loader.get_append_system_prompt()?, "appendSources": sources,
    }))
}

fn loader_script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let (root, cwd, agent_dir) = (text(&args["root"]), text(&args["cwd"]), text(&args["loaderAgentDir"]));
    let extra_skills = Path::new(root).join("extra-skills");
    let extra_prompts = Path::new(root).join("extra-prompts");
    let mut r = serde_json::Map::new();
    let options = |cwd: &str, agent_dir: &str| DefaultResourceLoaderOptions { cwd: cwd.into(), agent_dir: agent_dir.into(), ..Default::default() };
    let plain = DefaultResourceLoader::new(ctx, options(cwd, agent_dir))?;
    plain.reload(None)?;
    r.insert("plain".into(), resource_report(&plain)?);

    let overridden = DefaultResourceLoader::new(ctx, DefaultResourceLoaderOptions {
        no_skills: true, additional_skill_paths: vec![extra_skills.to_string_lossy().into_owned()], no_prompt_templates: true,
        additional_prompt_template_paths: vec![extra_prompts.to_string_lossy().into_owned()], no_context_files: true,
        system_prompt: Some("inline prompt".into()), append_system_prompt: Some(vec!["a1".into(), "a2".into()]),
        system_prompt_override: Some(std::sync::Arc::new(|base| match base.as_str() { Some(text) => json!(format!("{text}!")), None => Value::Null })),
        append_system_prompt_override: Some(std::sync::Arc::new(|base| { let mut list = base.as_array().cloned().unwrap_or_default(); list.push(json!("z")); Value::Array(list) })),
        skills_override: Some(std::sync::Arc::new(|base| json!({"skills": base["skills"].as_array().map(|list| list.iter().filter(|skill| skill["name"] != "beta").cloned().collect::<Vec<_>>()).unwrap_or_default(), "diagnostics": base["diagnostics"]}))),
        prompts_override: Some(std::sync::Arc::new(|base| json!({"prompts": base["prompts"].as_array().map(|list| list.iter().map(|prompt| { let mut prompt = prompt.clone(); prompt["description"] = json!(format!("overridden {}", text(&prompt["name"]))); prompt }).collect::<Vec<_>>()).unwrap_or_default(), "diagnostics": base["diagnostics"]}))),
        agents_files_override: Some(std::sync::Arc::new(|_| json!({"agentsFiles": [{"path": "/virtual/AGENTS.md", "content": "virtual"}]}))),
        ..options(cwd, agent_dir)
    })?;
    overridden.reload(None)?;
    r.insert("overridden".into(), resource_report(&overridden)?);

    plain.extend_resources(json!({"skillPaths": [{"path": extra_skills.join("beta").to_string_lossy(), "metadata": {"source": "test", "scope": "project", "origin": "top-level"}}]}))?;
    r.insert("extended".into(), skill_report(&plain.get_skills()?));

    let settings = SettingsManager::in_memory(ctx, Some(json!({"skills": [extra_skills.to_string_lossy()]})), None)?;
    let via_settings = DefaultResourceLoader::new(ctx, DefaultResourceLoaderOptions { settings_manager: Some(&settings), ..options(cwd, agent_dir) })?;
    via_settings.reload(None)?;
    r.insert("viaSettings".into(), skill_report(&via_settings.get_skills()?));

    let calls = std::sync::Arc::new(std::sync::atomic::AtomicI64::new(0));
    let counter = calls.clone();
    let trusting = DefaultResourceLoader::new(ctx, options(cwd, agent_dir))?;
    trusting.reload(Some(std::sync::Arc::new(move |_| { counter.fetch_add(1, std::sync::atomic::Ordering::SeqCst); true })))?;
    r.insert("trust".into(), json!({"calls": calls.load(std::sync::atomic::Ordering::SeqCst), "skills": trusting.get_skills()?["skills"].as_array().map_or(0, Vec::len)}));
    let bootstrap = trusting.load_project_trust_extensions()?;
    r.insert("projectTrust".into(), json!({"extensions": bootstrap["extensions"].as_array().map_or(0, Vec::len), "errors": bootstrap["errors"].as_array().map_or(0, Vec::len)}));
    Ok(Value::Object(r))
}

fn resolved_report(paths: &Value) -> Value {
    let skills: Vec<Value> = paths["skills"].as_array().map(|list| list.iter().map(|skill| json!({
        "file": base_name(&skill["path"]), "enabled": skill["enabled"], "source": skill["metadata"]["source"], "scope": skill["metadata"]["scope"], "origin": skill["metadata"]["origin"],
    })).collect()).unwrap_or_default();
    json!({"extensions": paths["extensions"].as_array().map_or(0, Vec::len), "skills": skills, "prompts": paths["prompts"].as_array().map_or(0, Vec::len), "themes": paths["themes"].as_array().map_or(0, Vec::len)})
}

fn packages_script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let (root, cwd, agent_dir) = (text(&args["root"]), text(&args["pmCwd"]), text(&args["pmAgentDir"]));
    let pkg = Path::new(root).join("pkgs").join("localpkg").to_string_lossy().into_owned();
    let mut r = serde_json::Map::new();
    let settings = SettingsManager::create(ctx, cwd, Some(agent_dir), None)?;
    let pm = DefaultPackageManager::new(ctx, PackageManagerOptions { cwd: cwd.into(), agent_dir: agent_dir.into(), settings_manager: &settings, builtin_extensions: vec![] })?;
    r.insert("initial".into(), json!({"list": pm.list_configured_packages()?.as_array().map_or(0, Vec::len), "installed": pm.get_installed_path(&pkg, "user")?}));
    let added = pm.add_source_to_settings(&pkg, false)?;
    let again = pm.add_source_to_settings(&pkg, false)?;
    r.insert("added".into(), json!({"first": added, "again": again, "list": pm.list_configured_packages()?, "packages": settings.get_packages()?, "installed": pm.get_installed_path(&pkg, "user")?}));
    r.insert("resolved".into(), resolved_report(&pm.resolve(None)?));

    let events = std::sync::Arc::new(std::sync::Mutex::new(Vec::<String>::new()));
    let sink = events.clone();
    pm.set_progress_callback(Some(std::sync::Arc::new(move |event| sink.lock().unwrap().push(format!("{}:{}", text(&event["type"]), text(&event["action"]))))))?;
    pm.install_and_persist(&pkg, true)?;
    pm.remove(&pkg, true)?;
    r.insert("removeOnly".into(), json!({"list": pm.list_configured_packages()?.as_array().map_or(0, Vec::len)}));
    r.insert("installed".into(), json!({"list": pm.list_configured_packages()?, "events": events.lock().unwrap().clone()}));
    r.insert("removed".into(), json!({
        "project": pm.remove_and_persist(&pkg, true)?, "list": pm.list_configured_packages()?.as_array().map_or(0, Vec::len),
        "settings": pm.remove_source_from_settings(&pkg, false)?, "settingsAgain": pm.remove_source_from_settings(&pkg, false)?,
    }));

    pm.set_progress_callback(None)?;
    r.insert("temporary".into(), resolved_report(&pm.resolve_extension_sources(&[&pkg], false, true)?));
    r.insert("updates".into(), json!(pm.check_for_available_updates()?.as_array().map_or(0, Vec::len)));

    let missing = std::sync::Arc::new(std::sync::Mutex::new(Vec::<String>::new()));
    let sink = missing.clone();
    pm.add_source_to_settings("npm:not-installed-pkg", false)?;
    let skipped = pm.resolve(Some(std::sync::Arc::new(move |source| { sink.lock().unwrap().push(source); "skip".to_string() })))?;
    r.insert("missing".into(), json!({"calls": missing.lock().unwrap().clone(), "skills": skipped["skills"].as_array().map_or(0, Vec::len)}));
    Ok(Value::Object(r))
}

fn pick_model(model: &Value) -> Value {
    if model.is_null() {
        return Value::Null;
    }
    json!({"id": model["id"], "provider": model["provider"], "api": model["api"], "name": model["name"], "reasoning": model["reasoning"].as_bool().unwrap_or(false), "contextWindow": model["contextWindow"], "maxTokens": model["maxTokens"]})
}

fn pick_models(models: &Value) -> Vec<Value> {
    models.as_array().map(Vec::as_slice).unwrap_or_default().iter().map(pick_model).collect()
}

fn ids(models: &Value, provider: Option<&str>) -> Vec<Value> {
    models.as_array().map(Vec::as_slice).unwrap_or_default().iter().filter(|model| provider.is_none_or(|provider| model["provider"] == provider)).map(|model| model["id"].clone()).collect()
}

fn error_text<T>(result: io::Result<T>) -> String {
    match result {
        Ok(_) => "no error".into(),
        Err(error) => format!("error: {error}"),
    }
}

fn message_text(message: &Value) -> String {
    message["content"].as_array().map(Vec::as_slice).unwrap_or_default().iter().map(|block| text(&block["text"])).collect()
}

fn child_entry_kinds(manager: &SessionManager) -> io::Result<Vec<String>> {
    Ok(manager.get_entries()?.as_array().map(Vec::as_slice).unwrap_or_default().iter().map(|entry| {
        let kind = text(&entry["type"]).to_string();
        if kind == "message" { format!("{}:{}", kind, text(&entry["message"]["role"])) } else { kind }
    }).collect())
}

fn child_last_text(session: &pig_sdk::AgentSession) -> io::Result<String> {
    let messages = session.messages()?;
    let last = messages.last().cloned().unwrap_or(Value::Null);
    Ok(last["content"].as_array().map(Vec::as_slice).unwrap_or_default().iter().filter(|block| block["type"] == "text").map(|block| text(&block["text"]).to_string()).collect::<String>())
}

/// Creates a session with options over manager and settings, binds extensions when bind is set, prompts it and reports what Pi's createAgentSession run reports.
fn observe_child(ctx: &Context, mut options: CreateAgentSessionOptions, manager: &SessionManager, settings: &SettingsManager, bind: bool) -> io::Result<(pig_sdk::AgentSession, Value)> {
    options.session_manager = Some(manager.handle().to_string());
    options.settings_manager = Some(settings.handle().to_string());
    let session = ctx.create_agent_session(options)?;
    let events: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
    let sink = events.clone();
    let _subscription = session.subscribe(move |event| {
        let mut name = event["type"].as_str().unwrap_or("").to_string();
        if name.starts_with("message_") {
            name = format!("{}:{}", name, event["message"]["role"].as_str().unwrap_or(""));
        }
        sink.lock().unwrap().push(name);
    });
    if bind {
        session.bind_extensions()?;
    }
    let file = manager.get_session_file()?;
    let created = json!({
        "model": {"provider": session.model["provider"], "id": session.model["id"]}, "thinkingLevel": session.thinking_level, "activeTools": session.active_tools,
        "fallback": session.model_fallback_message, "extensionErrors": session.extension_errors.len(), "appendix": session.system_prompt.contains("Handle appendix"),
        "sessionId": json!(session.session_id) == manager.get_session_id()?, "sessionFile": session.session_file.as_ref().map_or(file.is_null(), |path| file.as_str() == Some(path.as_str())),
        "persisted": session.session_file.is_some(),
    });
    session.prompt("hello")?;
    let state = session.state()?;
    let roles: Vec<Value> = session.messages()?.iter().map(|message| message["role"].clone()).collect();
    let report = json!({
        "created": created, "events": *events.lock().unwrap(), "final": state.get("lastAssistantText").cloned().unwrap_or(Value::Null), "roles": roles,
        "entries": child_entry_kinds(manager)?, "text": child_last_text(&session)?,
    });
    Ok((session, report))
}

type ChildrenBuilt = (pig_sdk::AgentSession, pig_sdk::AgentSession, SessionManager, SessionManager);

/// Builds the objects and creates two sessions over them; the runtime, settings and loaders are dropped when it returns.
fn child_handles_build(ctx: &Context, args: &Value, r: &mut serde_json::Map<String, Value>) -> io::Result<ChildrenBuilt> {
    let (cwd, dir, agent_dir) = (text(&args["cwd"]), text(&args["dir"]), text(&args["agentDir"]));
    let rt = ModelRuntime::create(ctx, ModelRuntimeCreateOptions { auth_path: Some(text(&args["mrAuth"]).into()), models_path: Some(text(&args["mrModels"]).into()), models_store_path: Some(text(&args["mrStore"]).into()), ..Default::default() })?;
    let settings = SettingsManager::in_memory(ctx, Some(json!({
        "compaction": {"enabled": false}, "retry": {"enabled": false}, "defaultProvider": "mr-local", "defaultModel": "m1", "defaultThinkingLevel": "high",
    })), None)?;
    let quiet = || DefaultResourceLoaderOptions {
        cwd: cwd.into(), agent_dir: agent_dir.into(), settings_manager: Some(&settings), no_extensions: true, no_skills: true, no_prompt_templates: true, no_themes: true, no_context_files: true, ..Default::default()
    };
    let loader = DefaultResourceLoader::new(ctx, DefaultResourceLoaderOptions {
        additional_extension_paths: vec![text(&args["probeExt"]).into()], append_system_prompt: Some(vec!["Handle appendix".into()]), ..quiet()
    })?;
    loader.reload(None)?;
    let memory = SessionManager::in_memory(ctx, Some(cwd), None, None)?;
    let scoped = json!([{"model": rt.get_model("mr-local", "m1")?, "thinkingLevel": "off"}]);
    let (first, mut first_report) = observe_child(ctx, CreateAgentSessionOptions { resource_loader: Some(loader.handle().to_string()), model_runtime: Some(rt.handle().to_string()), scoped_models: Some(scoped), ..Default::default() }, &memory, &settings, true)?;
    first_report["settings"] = json!({"defaultModel": settings.get_default_model()?, "compaction": settings.get_compaction_enabled()?});
    r.insert("memory".into(), first_report);
    let file = SessionManager::create(ctx, cwd, Some(dir), None)?;
    let plain_loader = DefaultResourceLoader::new(ctx, quiet())?;
    plain_loader.reload(None)?;
    let (second, mut second_report) = observe_child(ctx, CreateAgentSessionOptions { resource_loader: Some(plain_loader.handle().to_string()), model_runtime: Some(rt.handle().to_string()), ..Default::default() }, &file, &settings, false)?;
    let file_path = file.get_session_file()?;
    second_report["onDisk"] = json!(Path::new(text(&file_path)).exists());
    second_report["samePath"] = json!(second.session_file.as_deref() == file_path.as_str());
    r.insert("file".into(), second_report);
    Ok((first, second, memory, file))
}

fn child_handles_script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let mut r = serde_json::Map::new();
    // The extension let go of every object it built (the runtime, settings and loaders are dropped when the build returns); the sessions still run over them.
    let (first, second, memory, file) = child_handles_build(ctx, args, &mut r)?;
    std::thread::sleep(std::time::Duration::from_millis(300));
    first.prompt("again")?;
    r.insert("afterDrop".into(), json!({"text": child_last_text(&first)?, "entries": child_entry_kinds(&memory)?.len(), "fileEntries": child_entry_kinds(&file)?.len()}));
    first.dispose();
    second.dispose();
    Ok(Value::Object(r))
}

fn model_runtime_script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let (auth, models, store, server) = (text(&args["mrAuth"]), text(&args["mrModels"]), text(&args["mrStore"]), text(&args["mrServer"]));
    let mut r = serde_json::Map::new();
    let rt = ModelRuntime::create(ctx, ModelRuntimeCreateOptions { auth_path: Some(auth.into()), models_path: Some(models.into()), models_store_path: Some(store.into()), ..Default::default() })?;
    let local = rt.get_model("mr-local", "m1")?;
    r.insert("catalog".into(), json!({
        "error": rt.get_error()?, "models": pick_models(&rt.get_models(Some("mr-local"))?), "all": rt.get_all_models(Some("mr-local"))?.as_array().map_or(0, Vec::len), "one": pick_model(&local),
        "missing": rt.get_model("mr-local", "nope")?.is_null(), "builtinListed": !ids(&rt.get_models(None)?, Some("anthropic")).is_empty(),
        "ofType": ids(&rt.get_models_of_type("chat", Some("mr-local"))?, None), "oneOfType": pick_model(&rt.get_model_of_type("chat", "mr-local", "m1")?),
        "imagesOfType": rt.get_models_of_type("image", Some("mr-local"))?.as_array().map_or(0, Vec::len), "physical": pick_model(&rt.get_physical_model("mr-local", "m1")?),
        "physicalMissing": rt.get_physical_model("mr-local", "nope")?.is_null(),
    }));
    r.insert("auth".into(), json!({
        "configured": rt.has_configured_auth("mr-local")?, "unconfigured": rt.has_configured_auth("nope-prov")?, "oauth": rt.is_using_oauth("mr-local")?, "subscription": rt.is_using_subscription("mr-local")?,
        "status": rt.get_provider_auth_status("mr-local")?, "storedStatus": rt.get_provider_auth_status("stored-prov")?, "unknownStatus": rt.get_provider_auth_status("nope-prov")?,
        "byProvider": rt.get_auth(json!("mr-local"), None)?, "byModel": rt.get_auth(local.clone(), None)?, "unconfiguredAuth": rt.get_auth(json!("nope-prov"), None)?,
        "overridden": rt.get_auth(json!("mr-local"), Some(json!({"apiKey": "override"})))?,
        "check": rt.check_auth("mr-local")?, "uncheckedProvider": rt.check_auth("nope-prov")?, "compat": rt.get_compatibility_request_config(&local)?,
    }));
    rt.set_runtime_api_key("rt-prov", "rk")?;
    let with_key = json!({"status": rt.get_provider_auth_status("rt-prov")?, "configured": rt.has_configured_auth("rt-prov")?, "auth": rt.get_auth(json!("rt-prov"), None)?});
    rt.remove_runtime_api_key("rt-prov")?;
    r.insert("runtimeKey".into(), json!({"withKey": with_key, "removed": rt.get_provider_auth_status("rt-prov")?, "configuredAfter": rt.has_configured_auth("rt-prov")?}));
    let mut credentials: Vec<Value> = rt.list_credentials()?.as_array().cloned().unwrap_or_default().iter().map(|c| json!({"providerId": c["providerId"], "type": c["type"]})).collect();
    credentials.sort_by(|a, b| text(&a["providerId"]).cmp(text(&b["providerId"])));
    r.insert("credentials".into(), json!(credentials));

    let before = rt.get_registered_provider_ids()?;
    rt.register_provider("reg-prov", json!({
        "baseUrl": server, "apiKey": "regkey", "api": "openai-completions", "headers": {"x-reg": "1"},
        "models": [{"id": "r1", "name": "Reg One", "reasoning": false, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 500}],
    }))?;
    rt.register_provider("reg-prov", json!({"baseUrl": format!("{server}/v2")}))?;
    let config = rt.get_registered_provider_config("reg-prov")?;
    let rejected = error_text(rt.register_provider("bad-prov", json!({"models": [{"id": "x"}]})));
    let mut registered = json!({
        "before": before, "ids": rt.get_registered_provider_ids()?, "models": pick_models(&rt.get_models(Some("reg-prov"))?), "baseUrl": config["baseUrl"], "apiKey": config["apiKey"], "api": config["api"], "headers": config["headers"], "modelIds": ids(&config["models"], None),
        "configured": rt.has_configured_auth("reg-prov")?, "unregisteredConfig": rt.get_registered_provider_config("nope-prov")?.is_null(), "rejected": rejected,
    });
    rt.unregister_provider("reg-prov")?;
    registered["after"] = json!({"ids": rt.get_registered_provider_ids()?, "models": rt.get_models(Some("reg-prov"))?.as_array().map_or(0, Vec::len), "config": rt.get_registered_provider_config("reg-prov")?.is_null()});
    r.insert("registered".into(), registered);

    let requests = std::sync::Arc::new(std::sync::Mutex::new(Vec::<Value>::new()));
    let sink = requests.clone();
    let route_rt = std::sync::Arc::new(ModelRuntime::create(ctx, ModelRuntimeCreateOptions { auth_path: Some(auth.into()), models_path: Some(models.into()), models_store_path: Some(store.into()), ..Default::default() })?);
    let physical_source = route_rt.clone();
    let mut virtual_model = VirtualModel::new("mr-virtual", "v1", "Virtual One", move |_ctx, request| {
        let mut requests = sink.lock().unwrap();
        requests.push(json!({"model": request.model["id"], "thinkingLevel": request.thinking_level, "reason": request.reason, "messages": request.messages.len(), "state": request.state}));
        let model = physical_source.get_physical_model("mr-local", "m1").map_err(|error| error.to_string())?;
        Ok(ModelRoute { model, thinking_level: "off".into(), state: Some(json!({"n": requests.len()})) })
    });
    virtual_model.thinking_levels = Some(vec!["off".into(), "high".into()]);
    virtual_model.context_window = Some(1000);
    virtual_model.max_tokens = Some(100);
    virtual_model.input = Some(vec!["text".into()]);
    rt.register_virtual_model(virtual_model)?;
    let virtual_object = rt.get_model("mr-virtual", "v1")?;
    let messages = json!([{"role": "user", "content": "hi", "timestamp": 1}]);
    let routed = rt.resolve_model(&virtual_object, messages.clone(), json!({"reason": "user", "thinkingLevel": "high"}))?;
    let second = rt.resolve_model(&virtual_object, messages, json!({"reason": "continuation", "thinkingLevel": "off", "state": {"n": 1}}))?;
    let unknown = error_text(rt.resolve_model(&local, json!([]), json!({"reason": "user", "thinkingLevel": "off"})));
    r.insert("virtual".into(), json!({
        "model": pick_model(&virtual_object), "thinkingLevels": virtual_object["thinkingLevels"], "route": {"model": pick_model(&routed["model"]), "thinkingLevel": routed["thinkingLevel"], "state": routed["state"]}, "second": second["state"], "requests": requests.lock().unwrap().clone(),
        "physicalIsNull": rt.get_physical_model("mr-virtual", "v1")?.is_null(), "unknown": unknown,
    }));
    rt.unregister_virtual_model("mr-virtual", "v1")?;
    r.get_mut("virtual").unwrap()["after"] = json!(rt.get_model("mr-virtual", "v1")?.is_null());

    let refreshed = rt.refresh(Some(json!({"allowNetwork": false})))?;
    r.insert("refresh".into(), json!({"aborted": refreshed["aborted"], "errors": refreshed["errors"]}));
    r.insert("available".into(), json!({
        "ofProvider": ids(&rt.get_available(Some("mr-local"))?, None), "all": ids(&rt.get_available(None)?, Some("mr-local")), "snapshot": ids(&rt.get_available_snapshot()?, Some("mr-local")),
        "allOfProvider": ids(&rt.get_all_available(Some("mr-local"))?, None), "ofType": ids(&rt.get_available_of_type("chat", Some("mr-local"))?, None), "unavailableProvider": rt.get_available(Some("nope-prov"))?.as_array().map_or(0, Vec::len),
    }));

    let conversation = json!({"systemPrompt": "Be brief.", "messages": [{"role": "user", "content": "say hello", "timestamp": 1}]});
    let options = json!({"maxTokens": 50});
    let stream = rt.stream_simple(local.clone(), conversation.clone(), options.clone());
    let mut events = Vec::new();
    while let Some(event) = stream.next() {
        events.push(event["type"].clone());
    }
    let message = stream.result().unwrap_or(Value::Null);
    let completed = rt.complete_simple(local.clone(), conversation.clone(), options.clone()).unwrap_or(Value::Null);
    let full = rt.complete(local.clone(), conversation.clone(), options).unwrap_or(Value::Null);
    let mut unknown_model = local.clone();
    unknown_model["provider"] = json!("nope-prov");
    let unknown_stop = rt.complete_simple(unknown_model, conversation, json!({})).unwrap_or(Value::Null);
    r.insert("stream".into(), json!({
        "events": events, "stopReason": message["stopReason"], "text": message_text(&message), "provider": message["provider"], "model": message["model"], "api": message["api"],
        "completed": message_text(&completed), "full": full["stopReason"], "unknownProvider": unknown_stop["stopReason"],
    }));
    let classified = rt.classify(&local, json!({"state": {}, "questions": {}}), None);
    let imaged = rt.generate_images(&local, json!({"input": [{"type": "text", "text": "a cat"}]}), None);
    r.insert("typed".into(), json!({"classifyStop": classified["stopReason"], "classifyError": classified["errorMessage"], "imagesStop": imaged["stopReason"], "imagesError": imaged["errorMessage"]}));

    let seen = std::sync::Arc::new(std::sync::Mutex::new(Vec::<Value>::new()));
    let notified = std::sync::Arc::new(std::sync::Mutex::new(Vec::<Value>::new()));
    let (seen_sink, notified_sink) = (seen.clone(), notified.clone());
    let credential = rt.login(
        "openai",
        "api_key",
        std::sync::Arc::new(move |prompt| { seen_sink.lock().unwrap().push(prompt["type"].clone()); Ok("sk-login".to_string()) }),
        Some(std::sync::Arc::new(move |event| notified_sink.lock().unwrap().push(event["type"].clone()))),
        None,
        None,
    )?;
    let listed = |rt: &ModelRuntime| -> io::Result<Vec<String>> {
        let mut out: Vec<String> = rt.list_credentials()?.as_array().cloned().unwrap_or_default().iter().map(|c| text(&c["providerId"]).to_string()).collect();
        out.sort();
        Ok(out)
    };
    let mut login = json!({"credential": credential, "prompts": seen.lock().unwrap().clone(), "events": notified.lock().unwrap().clone(), "listed": listed(&rt)?, "status": rt.get_provider_auth_status("openai")?});
    rt.logout("openai")?;
    login["afterLogout"] = json!(listed(&rt)?);
    r.insert("login".into(), login);
    r.insert("stored".into(), serde_json::from_str(&std::fs::read_to_string(auth)?).map_err(io::Error::other)?);
    Ok(Value::Object(r))
}

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("session-graph");
    ext.tool("session_manager", "Runs the SessionManager script.", empty_schema(), |ctx, args| match script(ctx, &args) {
        Ok(report) => ToolResult::text(report.to_string()),
        Err(error) => ToolResult::Error(error.to_string()),
    });
    ext.tool("settings_manager", "Runs the SettingsManager script.", empty_schema(), |ctx, args| match settings_script(ctx, &args) {
        Ok(report) => ToolResult::text(report.to_string()),
        Err(error) => ToolResult::Error(error.to_string()),
    });
    ext.tool("project_trust_store", "Runs the ProjectTrustStore script.", empty_schema(), |ctx, args| match trust_script(ctx, &args) {
        Ok(report) => ToolResult::text(report.to_string()),
        Err(error) => ToolResult::Error(error.to_string()),
    });
    ext.tool("resource_loader", "Runs the DefaultResourceLoader script.", empty_schema(), |ctx, args| match loader_script(ctx, &args) {
        Ok(report) => ToolResult::text(report.to_string()),
        Err(error) => ToolResult::Error(error.to_string()),
    });
    ext.tool("model_runtime", "Runs the ModelRuntime script.", empty_schema(), |ctx, args| match model_runtime_script(ctx, &args) {
        Ok(report) => ToolResult::text(report.to_string()),
        Err(error) => ToolResult::Error(error.to_string()),
    });
    ext.tool("package_manager", "Runs the DefaultPackageManager script.", empty_schema(), |ctx, args| match packages_script(ctx, &args) {
        Ok(report) => ToolResult::text(report.to_string()),
        Err(error) => ToolResult::Error(error.to_string()),
    });
    ext.tool("child_handles", "Runs the createAgentSession handle options script.", empty_schema(), |ctx, args| match child_handles_script(ctx, &args) {
        Ok(report) => ToolResult::text(report.to_string()),
        Err(error) => ToolResult::Error(error.to_string()),
    });
    ext
}

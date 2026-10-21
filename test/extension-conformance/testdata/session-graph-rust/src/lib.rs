//! Fixture for the session graph conformance rows (test/extension-conformance/session_graph_test.go): the script of testfixture/sessiongraph (Go), testdata/session-graph.mjs (Node, Pi's own classes) and testdata/session_graph_python.py.

use pig_sdk::{empty_schema, Context, Extension, NewSessionOptions, SessionInfo, SessionManager, SettingsManager, SettingsManagerCreateOptions, ToolResult};
use serde_json::{json, Value};
use std::io;
use std::path::Path;
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

    r.insert("list".into(), infos(SessionManager::list(ctx, cwd, Some(dir))?));
    r.insert("listAll".into(), infos(SessionManager::list_all(ctx, Some(dir))?));
    r.insert("listFork".into(), infos(SessionManager::list(ctx, other_dir, Some(&fork_dir))?));
    let found = SessionManager::find_by_id(ctx, cwd, fixed, Some(dir))?;
    r.insert("find".into(), json!({"found": found.as_deref() == Some(file.as_str()), "missing": SessionManager::find_by_id(ctx, cwd, "nope", Some(dir))?.is_none()}));

    r.insert("errors".into(), json!({
        "badId": failure(SessionManager::create(ctx, cwd, Some(dir), Some(&id("bad id!"))), "Session id must be"),
        "notSession": failure(SessionManager::open(ctx, junk, None, None), "not a valid"),
        "noFork": failure(SessionManager::fork_from(ctx, &Path::new(dir).join("missing.jsonl").to_string_lossy(), cwd, Some(&fork_dir), None), "Cannot fork"),
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
    ext
}

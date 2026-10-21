//! Pi's `SettingsManager` class (core/settings-manager.ts): the global and project settings an extension reads and writes, or in-memory settings it builds for a child session. The host owns the object; a `SettingsManager` passed to `create_agent_session` is the very settings object the child session reads. Every method is Pi's under its snake_case name; `None` is Pi's undefined.

use crate::context::Context;
use crate::graph::{callback_id, GraphCallback, GraphObject};
use serde_json::{json, Value};
use std::io;
use std::sync::mpsc;
use std::sync::{Arc, Mutex};

/// Pi's `SettingsManagerCreateOptions`. `None` is Pi's undefined (true).
#[derive(Debug, Clone, Default)]
pub struct SettingsManagerCreateOptions {
    pub project_trusted: Option<bool>,
}

impl SettingsManagerCreateOptions {
    fn to_value(options: Option<&SettingsManagerCreateOptions>) -> Value {
        match options.and_then(|options| options.project_trusted) {
            Some(trusted) => json!({"projectTrusted": trusted}),
            None => json!({}),
        }
    }
}

/// Pi's `SettingsStorage`: the backend of [`SettingsManager::from_storage`]. `with_lock` runs `f` on the current text of the scope's settings (`"global"` or `"project"`; `None` when there is none) while it holds its lock. `f` returns the text to store, `None` to leave it, or an error (a settings file that does not parse); `with_lock` stores the text and returns the error `f` returned.
pub trait SettingsStorage: Send + Sync + 'static {
    fn with_lock(&self, scope: &str, f: &mut dyn FnMut(Option<String>) -> Result<Option<String>, String>) -> Result<(), String>;
}

/// One `with_lock` of the extension's storage that the host drives in two calls.
struct PendingLock {
    finish: mpsc::Sender<Result<Option<String>, String>>,
    done: mpsc::Receiver<Result<(), String>>,
}

/// The function the host runs for every `with_lock`: `["begin", scope]` starts the storage's `with_lock` and answers its current text, and `["finish", result]` gives its `f` the host's result and waits for `with_lock` to return.
fn storage_callback(storage: Arc<dyn SettingsStorage>) -> GraphCallback {
    let pending: Arc<Mutex<Option<PendingLock>>> = Arc::new(Mutex::new(None));
    Arc::new(move |args: Vec<Value>| {
        match args.first().and_then(Value::as_str).unwrap_or_default() {
            "begin" => {
                let scope = args.get(1).and_then(Value::as_str).unwrap_or_default().to_string();
                let (current_tx, current_rx) = mpsc::channel::<Option<String>>();
                let (finish_tx, finish_rx) = mpsc::channel::<Result<Option<String>, String>>();
                let (done_tx, done_rx) = mpsc::channel::<Result<(), String>>();
                let storage = storage.clone();
                std::thread::spawn(move || {
                    let outcome = storage.with_lock(&scope, &mut |current| {
                        let _ = current_tx.send(current);
                        finish_rx.recv().unwrap_or_else(|_| Err("the host closed the storage lock".into()))
                    });
                    let _ = done_tx.send(outcome);
                });
                loop {
                    match current_rx.recv_timeout(std::time::Duration::from_millis(10)) {
                        Ok(current) => {
                            *pending.lock().unwrap() = Some(PendingLock { finish: finish_tx, done: done_rx });
                            return Ok(json!({"current": current}));
                        }
                        Err(_) => {
                            if let Ok(outcome) = done_rx.try_recv() {
                                return Err(outcome.err().unwrap_or_else(|| "settings storage: with_lock returned without calling f".into()));
                            }
                        }
                    }
                }
            }
            "finish" => {
                let Some(lock) = pending.lock().unwrap().take() else { return Err("settings storage callback: finish without begin".into()) };
                let result = args.get(1).cloned().unwrap_or(Value::Null);
                let message = if let Some(error) = result["error"].as_str().filter(|error| !error.is_empty()) {
                    Err(error.to_string())
                } else if result["write"].as_bool() == Some(true) {
                    Ok(Some(result["text"].as_str().unwrap_or_default().to_string()))
                } else {
                    Ok(None)
                };
                let _ = lock.finish.send(message);
                lock.done.recv().unwrap_or_else(|_| Err("settings storage: with_lock ended".into())).map(|_| Value::Null)
            }
            other => Err(format!("settings storage callback: unknown operation {other:?}")),
        }
    })
}

/// Pi's `SettingsError`: a failure to load or write one scope's settings file.
#[derive(Debug, Clone, PartialEq)]
pub struct SettingsError {
    pub scope: String,
    pub path: Option<String>,
    pub error: String,
}

fn text(value: &Value) -> String {
    value.as_str().unwrap_or_default().to_string()
}

fn optional_text(value: &Value) -> Option<String> {
    value.as_str().map(str::to_string)
}

fn texts(value: &Value) -> Vec<String> {
    value.as_array().map(|list| list.iter().map(text).collect()).unwrap_or_default()
}

fn optional_texts(value: &Value) -> Option<Vec<String>> {
    value.as_array().map(|_| texts(value))
}

fn opt(value: Option<&str>) -> Value {
    value.map_or(Value::Null, |text| json!(text))
}

/// Pi's `SettingsManager`. Dropping it releases the host's reference; a session created from it keeps its own.
pub struct SettingsManager {
    object: GraphObject,
}

impl SettingsManager {
    /// `SettingsManager.create(cwd, agentDir = getAgentDir(), options = {})`: settings loaded from the files of `agent_dir` (global) and `cwd` (project). `None` for `agent_dir` is the host's agent directory.
    pub fn create(context: &Context, cwd: &str, agent_dir: Option<&str>, options: Option<&SettingsManagerCreateOptions>) -> io::Result<SettingsManager> {
        let object = GraphObject::construct(context, "SettingsManager", "create", vec![json!(cwd), opt(agent_dir), SettingsManagerCreateOptions::to_value(options)])?;
        Ok(SettingsManager { object })
    }

    /// `SettingsManager.inMemory(settings = {}, options = {})`: settings that never touch a file. `settings` is Pi's `Partial<Settings>` as a JSON object.
    pub fn in_memory(context: &Context, settings: Option<Value>, options: Option<&SettingsManagerCreateOptions>) -> io::Result<SettingsManager> {
        let object = GraphObject::construct(context, "SettingsManager", "inMemory", vec![settings.unwrap_or_else(|| json!({})), SettingsManagerCreateOptions::to_value(options)])?;
        Ok(SettingsManager { object })
    }

    /// `SettingsManager.fromStorage(storage, options = {})`: settings kept by `storage`, an object of this process the host calls back whenever the manager reads or writes a scope.
    pub fn from_storage(context: &Context, storage: Arc<dyn SettingsStorage>, options: Option<&SettingsManagerCreateOptions>) -> io::Result<SettingsManager> {
        let with_lock = callback_id(context, Some(storage_callback(storage)));
        Ok(SettingsManager { object: GraphObject::construct(context, "SettingsManager", "fromStorage", vec![json!({"withLock": with_lock}), SettingsManagerCreateOptions::to_value(options)])? })
    }

    /// The host's name for the settings, which `create_agent_session` puts on the wire.
    pub fn handle(&self) -> &str {
        self.object.handle()
    }

    /// The errors loading or writing the settings files since the last call; the call forgets them.
    pub fn drain_errors(&self) -> io::Result<Vec<SettingsError>> {
        let value = self.object.call("drainErrors", vec![])?;
        Ok(value
            .as_array()
            .map(|list| {
                list.iter()
                    .map(|entry| SettingsError { scope: text(&entry["scope"]), path: optional_text(&entry["path"]), error: text(&entry["error"]) })
                    .collect()
            })
            .unwrap_or_default())
    }

    pub fn get_settings(&self) -> io::Result<Value> {
        let value = self.object.call("getSettings", vec![])?;
        Ok(value)
    }

    pub fn get_global_settings(&self) -> io::Result<Value> {
        let value = self.object.call("getGlobalSettings", vec![])?;
        Ok(value)
    }

    pub fn get_project_settings(&self) -> io::Result<Value> {
        let value = self.object.call("getProjectSettings", vec![])?;
        Ok(value)
    }

    pub fn is_project_trusted(&self) -> io::Result<bool> {
        let value = self.object.call("isProjectTrusted", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_project_trusted(&self, trusted: bool) -> io::Result<()> {
        self.object.call("setProjectTrusted", vec![json!(trusted)]).map(|_| ())
    }

    pub fn reload(&self) -> io::Result<()> {
        self.object.call_async("reload", vec![]).map(|_| ())
    }

    pub fn apply_overrides(&self, overrides: Value) -> io::Result<()> {
        self.object.call("applyOverrides", vec![overrides]).map(|_| ())
    }

    pub fn flush(&self) -> io::Result<()> {
        self.object.call_async("flush", vec![]).map(|_| ())
    }

    pub fn get_last_changelog_version(&self) -> io::Result<Option<String>> {
        let value = self.object.call("getLastChangelogVersion", vec![])?;
        Ok(optional_text(&value))
    }

    pub fn set_last_changelog_version(&self, version: &str) -> io::Result<()> {
        self.object.call("setLastChangelogVersion", vec![json!(version)]).map(|_| ())
    }

    pub fn get_session_dir(&self) -> io::Result<Option<String>> {
        let value = self.object.call("getSessionDir", vec![])?;
        Ok(optional_text(&value))
    }

    pub fn get_default_provider(&self) -> io::Result<Option<String>> {
        let value = self.object.call("getDefaultProvider", vec![])?;
        Ok(optional_text(&value))
    }

    pub fn get_default_model(&self) -> io::Result<Option<String>> {
        let value = self.object.call("getDefaultModel", vec![])?;
        Ok(optional_text(&value))
    }

    pub fn set_default_provider(&self, provider: &str) -> io::Result<()> {
        self.object.call("setDefaultProvider", vec![json!(provider)]).map(|_| ())
    }

    pub fn set_default_model(&self, model_id: &str) -> io::Result<()> {
        self.object.call("setDefaultModel", vec![json!(model_id)]).map(|_| ())
    }

    pub fn set_default_model_and_provider(&self, provider: &str, model_id: &str) -> io::Result<()> {
        self.object.call("setDefaultModelAndProvider", vec![json!(provider), json!(model_id)]).map(|_| ())
    }

    pub fn get_steering_mode(&self) -> io::Result<String> {
        let value = self.object.call("getSteeringMode", vec![])?;
        Ok(text(&value))
    }

    pub fn set_steering_mode(&self, mode: &str) -> io::Result<()> {
        self.object.call("setSteeringMode", vec![json!(mode)]).map(|_| ())
    }

    pub fn get_follow_up_mode(&self) -> io::Result<String> {
        let value = self.object.call("getFollowUpMode", vec![])?;
        Ok(text(&value))
    }

    pub fn set_follow_up_mode(&self, mode: &str) -> io::Result<()> {
        self.object.call("setFollowUpMode", vec![json!(mode)]).map(|_| ())
    }

    pub fn get_theme_setting(&self) -> io::Result<Option<String>> {
        let value = self.object.call("getThemeSetting", vec![])?;
        Ok(optional_text(&value))
    }

    pub fn get_theme(&self) -> io::Result<Option<String>> {
        let value = self.object.call("getTheme", vec![])?;
        Ok(optional_text(&value))
    }

    pub fn set_theme(&self, theme: &str) -> io::Result<()> {
        self.object.call("setTheme", vec![json!(theme)]).map(|_| ())
    }

    pub fn get_default_thinking_level(&self) -> io::Result<Option<String>> {
        let value = self.object.call("getDefaultThinkingLevel", vec![])?;
        Ok(optional_text(&value))
    }

    pub fn set_default_thinking_level(&self, level: &str) -> io::Result<()> {
        self.object.call("setDefaultThinkingLevel", vec![json!(level)]).map(|_| ())
    }

    pub fn get_model_thinking_level(&self, provider: &str, model_id: &str) -> io::Result<Option<String>> {
        let value = self.object.call("getModelThinkingLevel", vec![json!(provider), json!(model_id)])?;
        Ok(optional_text(&value))
    }

    pub fn get_all_model_thinking_levels(&self) -> io::Result<Value> {
        let value = self.object.call("getAllModelThinkingLevels", vec![])?;
        Ok(value)
    }

    pub fn set_model_thinking_level(&self, provider: &str, model_id: &str, level: &str) -> io::Result<()> {
        self.object.call("setModelThinkingLevel", vec![json!(provider), json!(model_id), json!(level)]).map(|_| ())
    }

    pub fn remove_model_thinking_level(&self, provider: &str, model_id: &str) -> io::Result<()> {
        self.object.call("removeModelThinkingLevel", vec![json!(provider), json!(model_id)]).map(|_| ())
    }

    pub fn get_transport(&self) -> io::Result<String> {
        let value = self.object.call("getTransport", vec![])?;
        Ok(text(&value))
    }

    pub fn set_transport(&self, transport: &str) -> io::Result<()> {
        self.object.call("setTransport", vec![json!(transport)]).map(|_| ())
    }

    pub fn get_session_share_destination(&self) -> io::Result<String> {
        let value = self.object.call("getSessionShareDestination", vec![])?;
        Ok(text(&value))
    }

    pub fn set_session_share_destination(&self, destination: &str) -> io::Result<()> {
        self.object.call("setSessionShareDestination", vec![json!(destination)]).map(|_| ())
    }

    pub fn get_compaction_enabled(&self) -> io::Result<bool> {
        let value = self.object.call("getCompactionEnabled", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_compaction_enabled(&self, enabled: bool) -> io::Result<()> {
        self.object.call("setCompactionEnabled", vec![json!(enabled)]).map(|_| ())
    }

    pub fn get_compaction_reserve_tokens(&self, model: Option<Value>) -> io::Result<i64> {
        let value = self.object.call("getCompactionReserveTokens", vec![model.unwrap_or(Value::Null)])?;
        Ok(value.as_i64().unwrap_or(0))
    }

    pub fn get_compaction_keep_recent_tokens(&self, model: Option<Value>) -> io::Result<i64> {
        let value = self.object.call("getCompactionKeepRecentTokens", vec![model.unwrap_or(Value::Null)])?;
        Ok(value.as_i64().unwrap_or(0))
    }

    pub fn get_compaction_settings(&self, model: Option<Value>) -> io::Result<Value> {
        let value = self.object.call("getCompactionSettings", vec![model.unwrap_or(Value::Null)])?;
        Ok(value)
    }

    pub fn get_branch_summary_settings(&self) -> io::Result<Value> {
        let value = self.object.call("getBranchSummarySettings", vec![])?;
        Ok(value)
    }

    pub fn get_branch_summary_skip_prompt(&self) -> io::Result<bool> {
        let value = self.object.call("getBranchSummarySkipPrompt", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn get_retry_enabled(&self) -> io::Result<bool> {
        let value = self.object.call("getRetryEnabled", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_retry_enabled(&self, enabled: bool) -> io::Result<()> {
        self.object.call("setRetryEnabled", vec![json!(enabled)]).map(|_| ())
    }

    pub fn get_retry_settings(&self) -> io::Result<Value> {
        let value = self.object.call("getRetrySettings", vec![])?;
        Ok(value)
    }

    pub fn get_http_idle_timeout_ms(&self) -> io::Result<f64> {
        let value = self.object.call("getHttpIdleTimeoutMs", vec![])?;
        Ok(value.as_f64().unwrap_or(0.0))
    }

    pub fn set_http_idle_timeout_ms(&self, timeout_ms: f64) -> io::Result<()> {
        self.object.call("setHttpIdleTimeoutMs", vec![json!(timeout_ms)]).map(|_| ())
    }

    pub fn get_cache_warming_mode(&self) -> io::Result<String> {
        let value = self.object.call("getCacheWarmingMode", vec![])?;
        Ok(text(&value))
    }

    pub fn set_cache_warming_mode(&self, mode: &str) -> io::Result<()> {
        self.object.call("setCacheWarmingMode", vec![json!(mode)]).map(|_| ())
    }

    pub fn get_provider_retry_settings(&self) -> io::Result<Value> {
        let value = self.object.call("getProviderRetrySettings", vec![])?;
        Ok(value)
    }

    pub fn get_web_socket_connect_timeout_ms(&self) -> io::Result<Option<f64>> {
        let value = self.object.call("getWebSocketConnectTimeoutMs", vec![])?;
        Ok(value.as_f64())
    }

    pub fn get_hide_thinking_block(&self) -> io::Result<bool> {
        let value = self.object.call("getHideThinkingBlock", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn get_show_cache_miss_notices(&self) -> io::Result<bool> {
        let value = self.object.call("getShowCacheMissNotices", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn get_external_editor_command(&self) -> io::Result<String> {
        let value = self.object.call("getExternalEditorCommand", vec![])?;
        Ok(text(&value))
    }

    pub fn set_hide_thinking_block(&self, hide: bool) -> io::Result<()> {
        self.object.call("setHideThinkingBlock", vec![json!(hide)]).map(|_| ())
    }

    pub fn set_show_cache_miss_notices(&self, show: bool) -> io::Result<()> {
        self.object.call("setShowCacheMissNotices", vec![json!(show)]).map(|_| ())
    }

    pub fn get_shell_path(&self) -> io::Result<Option<String>> {
        let value = self.object.call("getShellPath", vec![])?;
        Ok(optional_text(&value))
    }

    pub fn set_shell_path(&self, path: Option<&str>) -> io::Result<()> {
        self.object.call("setShellPath", vec![opt(path)]).map(|_| ())
    }

    pub fn get_quiet_startup(&self) -> io::Result<Value> {
        let value = self.object.call("getQuietStartup", vec![])?;
        Ok(value)
    }

    pub fn set_quiet_startup(&self, quiet: Value) -> io::Result<()> {
        self.object.call("setQuietStartup", vec![quiet]).map(|_| ())
    }

    pub fn get_default_project_trust(&self) -> io::Result<String> {
        let value = self.object.call("getDefaultProjectTrust", vec![])?;
        Ok(text(&value))
    }

    pub fn set_default_project_trust(&self, default_project_trust: &str) -> io::Result<()> {
        self.object.call("setDefaultProjectTrust", vec![json!(default_project_trust)]).map(|_| ())
    }

    pub fn get_shell_command_prefix(&self) -> io::Result<Option<String>> {
        let value = self.object.call("getShellCommandPrefix", vec![])?;
        Ok(optional_text(&value))
    }

    pub fn set_shell_command_prefix(&self, prefix: Option<&str>) -> io::Result<()> {
        self.object.call("setShellCommandPrefix", vec![opt(prefix)]).map(|_| ())
    }

    pub fn get_npm_command(&self) -> io::Result<Option<Vec<String>>> {
        let value = self.object.call("getNpmCommand", vec![])?;
        Ok(optional_texts(&value))
    }

    pub fn set_npm_command(&self, command: Option<&[&str]>) -> io::Result<()> {
        self.object.call("setNpmCommand", vec![command.map_or(Value::Null, |v| json!(v))]).map(|_| ())
    }

    pub fn get_collapse_changelog(&self) -> io::Result<bool> {
        let value = self.object.call("getCollapseChangelog", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_collapse_changelog(&self, collapse: bool) -> io::Result<()> {
        self.object.call("setCollapseChangelog", vec![json!(collapse)]).map(|_| ())
    }

    pub fn get_enable_install_telemetry(&self) -> io::Result<bool> {
        let value = self.object.call("getEnableInstallTelemetry", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_enable_install_telemetry(&self, enabled: bool) -> io::Result<()> {
        self.object.call("setEnableInstallTelemetry", vec![json!(enabled)]).map(|_| ())
    }

    pub fn get_enable_analytics(&self) -> io::Result<bool> {
        let value = self.object.call("getEnableAnalytics", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn get_tracking_id(&self) -> io::Result<Option<String>> {
        let value = self.object.call("getTrackingId", vec![])?;
        Ok(optional_text(&value))
    }

    pub fn set_enable_analytics(&self, enabled: bool) -> io::Result<()> {
        self.object.call("setEnableAnalytics", vec![json!(enabled)]).map(|_| ())
    }

    pub fn get_or_create_device_id(&self) -> io::Result<String> {
        let value = self.object.call("getOrCreateDeviceId", vec![])?;
        Ok(text(&value))
    }

    pub fn get_packages(&self) -> io::Result<Value> {
        let value = self.object.call("getPackages", vec![])?;
        Ok(value)
    }

    pub fn set_packages(&self, packages: Value) -> io::Result<()> {
        self.object.call("setPackages", vec![packages]).map(|_| ())
    }

    pub fn set_project_packages(&self, packages: Value) -> io::Result<()> {
        self.object.call("setProjectPackages", vec![packages]).map(|_| ())
    }

    pub fn get_extension_paths(&self) -> io::Result<Vec<String>> {
        let value = self.object.call("getExtensionPaths", vec![])?;
        Ok(texts(&value))
    }

    pub fn set_extension_paths(&self, paths: &[&str]) -> io::Result<()> {
        self.object.call("setExtensionPaths", vec![json!(paths)]).map(|_| ())
    }

    pub fn set_project_extension_paths(&self, paths: &[&str]) -> io::Result<()> {
        self.object.call("setProjectExtensionPaths", vec![json!(paths)]).map(|_| ())
    }

    pub fn get_skill_paths(&self) -> io::Result<Vec<String>> {
        let value = self.object.call("getSkillPaths", vec![])?;
        Ok(texts(&value))
    }

    pub fn set_skill_paths(&self, paths: &[&str]) -> io::Result<()> {
        self.object.call("setSkillPaths", vec![json!(paths)]).map(|_| ())
    }

    pub fn set_project_skill_paths(&self, paths: &[&str]) -> io::Result<()> {
        self.object.call("setProjectSkillPaths", vec![json!(paths)]).map(|_| ())
    }

    pub fn get_prompt_template_paths(&self) -> io::Result<Vec<String>> {
        let value = self.object.call("getPromptTemplatePaths", vec![])?;
        Ok(texts(&value))
    }

    pub fn set_prompt_template_paths(&self, paths: &[&str]) -> io::Result<()> {
        self.object.call("setPromptTemplatePaths", vec![json!(paths)]).map(|_| ())
    }

    pub fn set_project_prompt_template_paths(&self, paths: &[&str]) -> io::Result<()> {
        self.object.call("setProjectPromptTemplatePaths", vec![json!(paths)]).map(|_| ())
    }

    pub fn get_theme_paths(&self) -> io::Result<Vec<String>> {
        let value = self.object.call("getThemePaths", vec![])?;
        Ok(texts(&value))
    }

    pub fn set_theme_paths(&self, paths: &[&str]) -> io::Result<()> {
        self.object.call("setThemePaths", vec![json!(paths)]).map(|_| ())
    }

    pub fn set_project_theme_paths(&self, paths: &[&str]) -> io::Result<()> {
        self.object.call("setProjectThemePaths", vec![json!(paths)]).map(|_| ())
    }

    pub fn get_enable_skill_commands(&self) -> io::Result<bool> {
        let value = self.object.call("getEnableSkillCommands", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_enable_skill_commands(&self, enabled: bool) -> io::Result<()> {
        self.object.call("setEnableSkillCommands", vec![json!(enabled)]).map(|_| ())
    }

    pub fn get_thinking_budgets(&self) -> io::Result<Value> {
        let value = self.object.call("getThinkingBudgets", vec![])?;
        Ok(value)
    }

    pub fn get_terminal_capability_overrides(&self) -> io::Result<Value> {
        let value = self.object.call("getTerminalCapabilityOverrides", vec![])?;
        Ok(value)
    }

    pub fn get_show_images(&self) -> io::Result<bool> {
        let value = self.object.call("getShowImages", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_show_images(&self, show: bool) -> io::Result<()> {
        self.object.call("setShowImages", vec![json!(show)]).map(|_| ())
    }

    pub fn get_image_width_cells(&self) -> io::Result<i64> {
        let value = self.object.call("getImageWidthCells", vec![])?;
        Ok(value.as_i64().unwrap_or(0))
    }

    pub fn set_image_width_cells(&self, width: i64) -> io::Result<()> {
        self.object.call("setImageWidthCells", vec![json!(width)]).map(|_| ())
    }

    pub fn get_clear_on_shrink(&self) -> io::Result<bool> {
        let value = self.object.call("getClearOnShrink", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_clear_on_shrink(&self, enabled: bool) -> io::Result<()> {
        self.object.call("setClearOnShrink", vec![json!(enabled)]).map(|_| ())
    }

    pub fn get_show_terminal_progress(&self) -> io::Result<bool> {
        let value = self.object.call("getShowTerminalProgress", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_show_terminal_progress(&self, enabled: bool) -> io::Result<()> {
        self.object.call("setShowTerminalProgress", vec![json!(enabled)]).map(|_| ())
    }

    pub fn get_tui_mode(&self) -> io::Result<String> {
        let value = self.object.call("getTuiMode", vec![])?;
        Ok(text(&value))
    }

    pub fn set_tui_mode(&self, mode: &str) -> io::Result<()> {
        self.object.call("setTuiMode", vec![json!(mode)]).map(|_| ())
    }

    pub fn get_fullscreen_exit_output(&self) -> io::Result<String> {
        let value = self.object.call("getFullscreenExitOutput", vec![])?;
        Ok(text(&value))
    }

    pub fn set_fullscreen_exit_output(&self, output: &str) -> io::Result<()> {
        self.object.call("setFullscreenExitOutput", vec![json!(output)]).map(|_| ())
    }

    pub fn get_fullscreen_scrollbar(&self) -> io::Result<String> {
        let value = self.object.call("getFullscreenScrollbar", vec![])?;
        Ok(text(&value))
    }

    pub fn set_fullscreen_scrollbar(&self, mode: &str) -> io::Result<()> {
        self.object.call("setFullscreenScrollbar", vec![json!(mode)]).map(|_| ())
    }

    pub fn get_fullscreen_copy_on_select(&self) -> io::Result<bool> {
        let value = self.object.call("getFullscreenCopyOnSelect", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_fullscreen_copy_on_select(&self, enabled: bool) -> io::Result<()> {
        self.object.call("setFullscreenCopyOnSelect", vec![json!(enabled)]).map(|_| ())
    }

    pub fn get_fullscreen_wheel_scroll_lines(&self) -> io::Result<Value> {
        let value = self.object.call("getFullscreenWheelScrollLines", vec![])?;
        Ok(value)
    }

    pub fn set_fullscreen_wheel_scroll_lines(&self, lines: Value) -> io::Result<()> {
        self.object.call("setFullscreenWheelScrollLines", vec![lines]).map(|_| ())
    }

    pub fn get_image_auto_resize(&self) -> io::Result<bool> {
        let value = self.object.call("getImageAutoResize", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_image_auto_resize(&self, enabled: bool) -> io::Result<()> {
        self.object.call("setImageAutoResize", vec![json!(enabled)]).map(|_| ())
    }

    pub fn get_block_images(&self) -> io::Result<bool> {
        let value = self.object.call("getBlockImages", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_block_images(&self, blocked: bool) -> io::Result<()> {
        self.object.call("setBlockImages", vec![json!(blocked)]).map(|_| ())
    }

    pub fn get_enabled_models(&self) -> io::Result<Option<Vec<String>>> {
        let value = self.object.call("getEnabledModels", vec![])?;
        Ok(optional_texts(&value))
    }

    pub fn get_default_tools(&self) -> io::Result<Option<Vec<String>>> {
        let value = self.object.call("getDefaultTools", vec![])?;
        Ok(optional_texts(&value))
    }

    pub fn set_enabled_models(&self, patterns: Option<&[&str]>) -> io::Result<()> {
        self.object.call("setEnabledModels", vec![patterns.map_or(Value::Null, |v| json!(v))]).map(|_| ())
    }

    pub fn get_double_escape_action(&self) -> io::Result<String> {
        let value = self.object.call("getDoubleEscapeAction", vec![])?;
        Ok(text(&value))
    }

    pub fn set_double_escape_action(&self, action: &str) -> io::Result<()> {
        self.object.call("setDoubleEscapeAction", vec![json!(action)]).map(|_| ())
    }

    pub fn get_tree_filter_mode(&self) -> io::Result<String> {
        let value = self.object.call("getTreeFilterMode", vec![])?;
        Ok(text(&value))
    }

    pub fn set_tree_filter_mode(&self, mode: &str) -> io::Result<()> {
        self.object.call("setTreeFilterMode", vec![json!(mode)]).map(|_| ())
    }

    pub fn get_show_hardware_cursor(&self) -> io::Result<bool> {
        let value = self.object.call("getShowHardwareCursor", vec![])?;
        Ok(value.as_bool().unwrap_or(false))
    }

    pub fn set_show_hardware_cursor(&self, enabled: bool) -> io::Result<()> {
        self.object.call("setShowHardwareCursor", vec![json!(enabled)]).map(|_| ())
    }

    pub fn get_editor_padding_x(&self) -> io::Result<i64> {
        let value = self.object.call("getEditorPaddingX", vec![])?;
        Ok(value.as_i64().unwrap_or(0))
    }

    pub fn set_editor_padding_x(&self, padding: i64) -> io::Result<()> {
        self.object.call("setEditorPaddingX", vec![json!(padding)]).map(|_| ())
    }

    pub fn get_output_pad(&self) -> io::Result<i64> {
        let value = self.object.call("getOutputPad", vec![])?;
        Ok(value.as_i64().unwrap_or(0))
    }

    pub fn set_output_pad(&self, padding: i64) -> io::Result<()> {
        self.object.call("setOutputPad", vec![json!(padding)]).map(|_| ())
    }

    pub fn get_autocomplete_max_visible(&self) -> io::Result<i64> {
        let value = self.object.call("getAutocompleteMaxVisible", vec![])?;
        Ok(value.as_i64().unwrap_or(0))
    }

    pub fn set_autocomplete_max_visible(&self, max_visible: i64) -> io::Result<()> {
        self.object.call("setAutocompleteMaxVisible", vec![json!(max_visible)]).map(|_| ())
    }

    pub fn get_code_block_indent(&self) -> io::Result<String> {
        let value = self.object.call("getCodeBlockIndent", vec![])?;
        Ok(text(&value))
    }

    pub fn get_mermaid_rendering_mode(&self) -> io::Result<String> {
        let value = self.object.call("getMermaidRenderingMode", vec![])?;
        Ok(text(&value))
    }

    pub fn set_mermaid_rendering_mode(&self, mode: &str) -> io::Result<()> {
        self.object.call("setMermaidRenderingMode", vec![json!(mode)]).map(|_| ())
    }

    pub fn get_warnings(&self) -> io::Result<Value> {
        let value = self.object.call("getWarnings", vec![])?;
        Ok(value)
    }

    pub fn set_warnings(&self, warnings: Value) -> io::Result<()> {
        self.object.call("setWarnings", vec![warnings]).map(|_| ())
    }
}

//! Pi's `DefaultPackageManager` class (core/package-manager.ts): it installs, removes, resolves and lists the packages named in the user and project settings of the `SettingsManager` it was given. The host owns the manager. Results are Pi's JSON shapes.

use crate::context::Context;
use crate::graph::{callback_id, GraphCallback, GraphObject};
use crate::settings_manager::SettingsManager;
use serde_json::{json, Value};
use std::io;
use std::sync::Arc;

/// Pi's `PackageManagerOptions`. `builtin_extensions` are the names of built-in extensions resolved as `builtin:<name>`.
pub struct PackageManagerOptions<'a> {
    pub cwd: String,
    pub agent_dir: String,
    pub settings_manager: &'a SettingsManager,
    pub builtin_extensions: Vec<String>,
}

/// Pi's `DefaultPackageManager`. Dropping it releases the host's reference.
pub struct DefaultPackageManager {
    object: GraphObject,
}

fn local(local: bool) -> Value {
    json!({"local": local})
}

impl DefaultPackageManager {
    /// `new DefaultPackageManager(options)`.
    pub fn new(context: &Context, options: PackageManagerOptions) -> io::Result<DefaultPackageManager> {
        let wire = json!({"cwd": options.cwd, "agentDir": options.agent_dir, "settingsManager": options.settings_manager.handle(), "builtinExtensions": options.builtin_extensions});
        Ok(DefaultPackageManager { object: GraphObject::construct(context, "DefaultPackageManager", "new", vec![wire])? })
    }

    /// The host's name for the manager.
    pub fn handle(&self) -> &str {
        self.object.handle()
    }

    /// Sets the function every later install, remove and update reports `{"type", "action", "source", "message"?}` events to; `None` clears it.
    pub fn set_progress_callback(&self, callback: Option<Arc<dyn Fn(Value) + Send + Sync>>) -> io::Result<()> {
        let wrapped: Option<GraphCallback> = callback.map(|callback| Arc::new(move |args: Vec<Value>| { callback(args.into_iter().next().unwrap_or(Value::Null)); Ok(Value::Null) }) as GraphCallback);
        self.object.call("setProgressCallback", vec![callback_id(self.object.context(), wrapped)]).map(|_| ())
    }

    pub fn add_source_to_settings(&self, source: &str, is_local: bool) -> io::Result<bool> {
        Ok(self.object.call("addSourceToSettings", vec![json!(source), local(is_local)])?.as_bool().unwrap_or(false))
    }

    pub fn remove_source_from_settings(&self, source: &str, is_local: bool) -> io::Result<bool> {
        Ok(self.object.call("removeSourceFromSettings", vec![json!(source), local(is_local)])?.as_bool().unwrap_or(false))
    }

    /// Where the package `source` is installed in `scope` (`"user"` or `"project"`), `None` when it is not.
    pub fn get_installed_path(&self, source: &str, scope: &str) -> io::Result<Option<String>> {
        Ok(self.object.call("getInstalledPath", vec![json!(source), json!(scope)])?.as_str().map(str::to_string))
    }

    /// Resolves every configured package into `{"extensions", "skills", "prompts", "themes"}`. `on_missing` answers `"install"`, `"skip"` or `"error"` for a package that is not installed.
    pub fn resolve(&self, on_missing: Option<Arc<dyn Fn(String) -> String + Send + Sync>>) -> io::Result<Value> {
        let decide: GraphCallback = Arc::new(move |args: Vec<Value>| {
            let source = args.first().and_then(Value::as_str).unwrap_or_default().to_string();
            Ok(json!(on_missing.as_ref().map_or_else(|| "install".to_string(), |decide| decide(source))))
        });
        self.object.call_async("resolve", vec![callback_id(self.object.context(), Some(decide))])
    }

    pub fn resolve_extension_sources(&self, sources: &[&str], is_local: bool, temporary: bool) -> io::Result<Value> {
        self.object.call_async("resolveExtensionSources", vec![json!(sources), json!({"local": is_local, "temporary": temporary})])
    }

    pub fn list_configured_packages(&self) -> io::Result<Value> {
        self.object.call("listConfiguredPackages", vec![])
    }

    pub fn install(&self, source: &str, is_local: bool) -> io::Result<()> {
        self.object.call_async("install", vec![json!(source), local(is_local)]).map(|_| ())
    }

    pub fn install_and_persist(&self, source: &str, is_local: bool) -> io::Result<()> {
        self.object.call_async("installAndPersist", vec![json!(source), local(is_local)]).map(|_| ())
    }

    pub fn remove(&self, source: &str, is_local: bool) -> io::Result<()> {
        self.object.call_async("remove", vec![json!(source), local(is_local)]).map(|_| ())
    }

    pub fn remove_and_persist(&self, source: &str, is_local: bool) -> io::Result<bool> {
        Ok(self.object.call_async("removeAndPersist", vec![json!(source), local(is_local)])?.as_bool().unwrap_or(false))
    }

    /// Updates `source`, or every unpinned configured package when `None`.
    pub fn update(&self, source: Option<&str>) -> io::Result<()> {
        self.object.call_async("update", vec![source.map_or(Value::Null, |source| json!(source))]).map(|_| ())
    }

    pub fn check_for_available_updates(&self) -> io::Result<Value> {
        self.object.call_async("checkForAvailableUpdates", vec![])
    }
}

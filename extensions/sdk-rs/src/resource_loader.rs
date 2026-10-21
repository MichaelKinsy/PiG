//! Pi's `DefaultResourceLoader` class (core/resource-loader.ts): the skills, prompt templates, themes, context files, system prompt and extensions of a project and agent directory. The host owns the loader; a loader passed to `create_agent_session` is the very loader the child session reads. Call `reload` before reading, as Pi requires. Results are Pi's JSON shapes (a theme is its `name`, `sourcePath` and `sourceInfo`, an extension its `path`, `resolvedPath`, `sourceInfo` and `tools`: the objects themselves hold functions that live in the host).

use crate::context::Context;
use crate::graph::{callback_id, GraphCallback, GraphObject};
use crate::settings_manager::SettingsManager;
use serde_json::{json, Value};
use std::io;
use std::sync::Arc;

/// An override function of the options: it takes Pi's JSON argument and answers Pi's JSON result.
pub type LoaderOverride = Arc<dyn Fn(Value) -> Value + Send + Sync>;

/// A factory of an inline extension. Not supported yet: see [`DefaultResourceLoaderOptions::extension_factories`].
pub type InlineExtensionFactory = Box<dyn Fn(&mut crate::Extension) + Send + Sync>;

/// Pi's `DefaultResourceLoaderOptions`. `None`, an empty list and `false` are Pi's undefined.
///
/// `extension_factories` and `extensions_override` exist so code that sets them fails loudly instead of losing behavior: an inline factory runs in this process but the child session's extensions load in the host, and an override returns live extension objects. They are not supported yet (an owner decision is pending, gap `SdkSessionGraph` in test/parity/sdk-exports.toml); [`DefaultResourceLoader::new`] returns an error when either is set. `additional_extension_paths` loads extension sources, and `create_agent_session`'s custom tools serve tools from the parent extension.
#[derive(Default)]
pub struct DefaultResourceLoaderOptions<'a> {
    pub cwd: String,
    pub agent_dir: String,
    pub settings_manager: Option<&'a SettingsManager>,
    pub additional_extension_paths: Vec<String>,
    pub additional_skill_paths: Vec<String>,
    pub additional_prompt_template_paths: Vec<String>,
    pub additional_theme_paths: Vec<String>,
    pub extension_factories: Vec<InlineExtensionFactory>,
    pub no_extensions: bool,
    pub disabled_builtin_extensions: Vec<String>,
    pub no_skills: bool,
    pub no_prompt_templates: bool,
    pub no_themes: bool,
    pub no_context_files: bool,
    pub system_prompt: Option<String>,
    pub append_system_prompt: Option<Vec<String>>,
    pub extensions_override: Option<LoaderOverride>,
    pub skills_override: Option<LoaderOverride>,
    pub prompts_override: Option<LoaderOverride>,
    pub themes_override: Option<LoaderOverride>,
    pub agents_files_override: Option<LoaderOverride>,
    pub system_prompt_override: Option<LoaderOverride>,
    pub append_system_prompt_override: Option<LoaderOverride>,
}

fn callback(context: &Context, override_fn: &Option<LoaderOverride>) -> Value {
    let wrapped: Option<GraphCallback> = override_fn.as_ref().map(|function| {
        let function = function.clone();
        Arc::new(move |args: Vec<Value>| Ok(function(args.into_iter().next().unwrap_or(Value::Null)))) as GraphCallback
    });
    callback_id(context, wrapped)
}

/// Pi's `DefaultResourceLoader`. Dropping it releases the host's reference; a session created from it keeps its own.
pub struct DefaultResourceLoader {
    object: GraphObject,
}

impl DefaultResourceLoader {
    /// `new DefaultResourceLoader(options)`.
    pub fn new(context: &Context, options: DefaultResourceLoaderOptions) -> io::Result<DefaultResourceLoader> {
        if !options.extension_factories.is_empty() {
            return Err(io::Error::other("DefaultResourceLoader option extensionFactories is not supported by the Rust SDK: an inline factory runs in this process but the child session's extensions load in the host (decision pending, gap SdkSessionGraph); use additional_extension_paths, or create_agent_session custom tools"));
        }
        if options.extensions_override.is_some() {
            return Err(io::Error::other("DefaultResourceLoader option extensionsOverride is not supported by the Rust SDK: it returns live extension objects, which live in the host (decision pending, gap SdkSessionGraph)"));
        }
        let mut wire = json!({
            "cwd": options.cwd, "agentDir": options.agent_dir,
            "additionalExtensionPaths": options.additional_extension_paths, "additionalSkillPaths": options.additional_skill_paths,
            "additionalPromptTemplatePaths": options.additional_prompt_template_paths, "additionalThemePaths": options.additional_theme_paths,
            "noExtensions": options.no_extensions, "noSkills": options.no_skills, "noPromptTemplates": options.no_prompt_templates, "noThemes": options.no_themes,
            "noContextFiles": options.no_context_files, "disabledBuiltinExtensions": options.disabled_builtin_extensions,
            "systemPrompt": options.system_prompt, "appendSystemPrompt": options.append_system_prompt,
            "skillsOverride": callback(context, &options.skills_override), "promptsOverride": callback(context, &options.prompts_override),
            "themesOverride": callback(context, &options.themes_override), "agentsFilesOverride": callback(context, &options.agents_files_override),
            "systemPromptOverride": callback(context, &options.system_prompt_override),
            "appendSystemPromptOverride": callback(context, &options.append_system_prompt_override),
        });
        if let Some(settings) = options.settings_manager {
            wire["settingsManager"] = json!(settings.handle());
        }
        Ok(DefaultResourceLoader { object: GraphObject::construct(context, "DefaultResourceLoader", "new", vec![wire])? })
    }

    /// The host's name for the loader, which `create_agent_session` puts on the wire.
    pub fn handle(&self) -> &str {
        self.object.handle()
    }

    /// The extensions of the last `reload`, as data.
    pub fn get_extensions(&self) -> io::Result<Value> { self.object.call("getExtensions", vec![]) }
    /// `{"skills", "diagnostics"}` of the last `reload`.
    pub fn get_skills(&self) -> io::Result<Value> { self.object.call("getSkills", vec![]) }
    /// `{"prompts", "diagnostics"}` of the last `reload`.
    pub fn get_prompts(&self) -> io::Result<Value> { self.object.call("getPrompts", vec![]) }
    /// `{"themes", "diagnostics"}` of the last `reload`.
    pub fn get_themes(&self) -> io::Result<Value> { self.object.call("getThemes", vec![]) }
    /// `{"agentsFiles": [{"path", "content"}]}`: the context files of the last `reload`.
    pub fn get_agents_files(&self) -> io::Result<Value> { self.object.call("getAgentsFiles", vec![]) }
    pub fn get_system_prompt(&self) -> io::Result<Option<String>> {
        Ok(self.object.call("getSystemPrompt", vec![])?.as_str().map(str::to_string))
    }
    /// `{"path"}` of the file the system prompt came from, null when it did not come from one.
    pub fn get_system_prompt_source(&self) -> io::Result<Value> { self.object.call("getSystemPromptSource", vec![]) }
    pub fn get_append_system_prompt(&self) -> io::Result<Vec<String>> {
        Ok(self.object.call("getAppendSystemPrompt", vec![])?.as_array().map(|list| list.iter().filter_map(|text| text.as_str().map(str::to_string)).collect()).unwrap_or_default())
    }
    pub fn get_append_system_prompt_sources(&self) -> io::Result<Value> { self.object.call("getAppendSystemPromptSources", vec![]) }
    /// Adds the skills, prompt templates and themes extensions contributed: `{"skillPaths", "promptPaths", "themePaths"}`, lists of `{"path", "metadata": {"source", "scope", "origin", "baseDir"?}}`.
    pub fn extend_resources(&self, paths: Value) -> io::Result<()> { self.object.call("extendResources", vec![paths]).map(|_| ()) }
    /// The bootstrap extension set, loaded with project settings untrusted, that project trust is decided with.
    pub fn load_project_trust_extensions(&self) -> io::Result<Value> { self.object.call_async("loadProjectTrustExtensions", vec![]) }
    /// Rediscovers every resource. `resolve_project_trust` takes the bootstrap extension set and answers whether the project is trusted.
    pub fn reload(&self, resolve_project_trust: Option<Arc<dyn Fn(Value) -> bool + Send + Sync>>) -> io::Result<()> {
        let mut options = serde_json::Map::new();
        if let Some(resolve) = resolve_project_trust {
            let wrapped: GraphCallback = Arc::new(move |args: Vec<Value>| Ok(json!(resolve(args.into_iter().next().unwrap_or(Value::Null)))));
            options.insert("resolveProjectTrust".into(), callback_id(self.object.context(), Some(wrapped)));
        }
        self.object.call_async("reload", vec![Value::Object(options)]).map(|_| ())
    }
}

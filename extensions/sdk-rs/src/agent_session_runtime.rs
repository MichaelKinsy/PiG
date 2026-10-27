//! Pi's `createAgentSessionRuntime` and `AgentSessionRuntime` (core/agent-session-runtime.ts).
//!
//! The runtime owns the current `AgentSession` plus its cwd-bound services. The factory that builds them stays in this process and is called on every session replacement, as Pi's is. The host answers the calls the runtime makes on the session it replaces: the session_before_switch, session_before_fork and session_shutdown events through the session's extensions, `refresh_context` and the session's log. The replacement methods tear the current runtime down first, then create and apply the next one; a failing factory's error is returned and the caller handles it.
//!
//! Async contract (Pi's promises, kept as blocking calls): every replacement method returns once the new session is applied and rebound; a failure is an `Err`. Pi's `withSession` (it takes the `ReplacedSessionContext` of `AgentSession.createReplacedSessionContext`, a C2Sessions gap) and `projectTrustContextFactory` (a record of the host's UI functions) are not served: a `with_session` function fails the call before anything happens, and the factory is not a member.

use crate::agent_session_services::AgentSessionServices;
use crate::child_session::{AgentSession, SessionStartEvent};
use crate::context::Context;
use crate::extension_runner::{CancelledResult, SetupCallback};
use crate::session_manager_class::{NewSessionOptions, SessionManager};
use serde_json::{json, Value};
use std::fs::OpenOptions;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

const WITH_SESSION: &str = "the with_session option is not supported yet: it takes the ReplacedSessionContext of AgentSession.createReplacedSessionContext, which is a C2Sessions gap in test/parity/sdk-exports.toml; use the setup option, or read AgentSessionRuntime::session once the call returned";

/// Pi's `withSession`, which is not served yet: a value fails the call.
pub type RuntimeWithSession = Arc<dyn Fn() + Send + Sync>;

/// The result of Pi's `CreateAgentSessionRuntimeResult`: the session the factory created, its cwd-bound services and the diagnostics collected while setting up.
pub struct CreateAgentSessionRuntimeResult {
    pub session: AgentSession,
    pub services: AgentSessionServices,
    pub diagnostics: Vec<Value>,
    /// Pi's `modelFallbackMessage`; `None` is Pi's undefined.
    pub model_fallback_message: Option<String>,
}

/// The argument of Pi's `CreateAgentSessionRuntimeFactory`: the target of the session to build. Pi's `projectTrustContext` is not passed.
pub struct CreateAgentSessionRuntimeFactoryOptions {
    pub cwd: String,
    pub agent_dir: String,
    pub session_manager: Arc<SessionManager>,
    pub session_start_event: Option<SessionStartEvent>,
}

/// Pi's `CreateAgentSessionRuntimeFactory`: it builds the services for the effective cwd, resolves the session options against them and creates the `AgentSession` over `options.session_manager`.
pub type CreateAgentSessionRuntimeFactory = Arc<dyn Fn(CreateAgentSessionRuntimeFactoryOptions) -> io::Result<CreateAgentSessionRuntimeResult> + Send + Sync>;

/// The second argument of Pi's `createAgentSessionRuntime`.
pub struct CreateAgentSessionRuntimeOptions {
    pub cwd: String,
    pub agent_dir: String,
    pub session_manager: Arc<SessionManager>,
    pub session_start_event: Option<SessionStartEvent>,
}

/// Pi's `SessionImportFileNotFoundError`: `import_from_jsonl` named a file that does not exist. It is the source of the `io::Error` the call returns.
#[derive(Debug)]
pub struct SessionImportFileNotFoundError {
    pub file_path: String,
}

impl std::fmt::Display for SessionImportFileNotFoundError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "File not found: {}", self.file_path)
    }
}

impl std::error::Error for SessionImportFileNotFoundError {}

/// Pi's `MissingSessionCwdError`: the working directory a session file stored does not exist. It is the source of the `io::Error` the call returns.
#[derive(Debug)]
pub struct MissingSessionCwdError {
    pub session_file: String,
    pub session_cwd: String,
    pub fallback_cwd: String,
}

impl std::fmt::Display for MissingSessionCwdError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let file = if self.session_file.is_empty() { String::new() } else { format!("\nSession file: {}", self.session_file) };
        write!(f, "Stored session working directory does not exist: {}{}\nCurrent working directory: {}", self.session_cwd, file, self.fallback_cwd)
    }
}

impl std::error::Error for MissingSessionCwdError {}

/// The options of Pi's `newSession` on the runtime. `setup` runs with the new session's log before the session reads it (the session's context is refreshed after it).
#[derive(Default, Clone)]
pub struct NewSessionRuntimeOptions {
    pub parent_session: Option<String>,
    pub setup: Option<SetupCallback>,
    /// Not served yet: a value fails the call.
    pub with_session: Option<RuntimeWithSession>,
}

/// The options of Pi's `fork` on the runtime: `position` is "before" (the default) or "at".
#[derive(Default, Clone)]
pub struct ForkRuntimeOptions {
    pub position: Option<String>,
    /// Not served yet: a value fails the call.
    pub with_session: Option<RuntimeWithSession>,
}

/// The result of Pi's `fork` on the runtime. `selected_text` is the text of the user message that was forked before.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct ForkResult {
    pub cancelled: bool,
    pub selected_text: Option<String>,
}

/// The options of Pi's `switchSession` on the runtime. `cwd_override` is the working directory to use when the stored one is gone.
#[derive(Default, Clone)]
pub struct SwitchSessionRuntimeOptions {
    pub cwd_override: Option<String>,
    /// Not served yet: a value fails the call.
    pub with_session: Option<RuntimeWithSession>,
}

type Rebind = Arc<dyn Fn(&AgentSession) -> io::Result<()> + Send + Sync>;

struct State {
    session: Arc<AgentSession>,
    services: Arc<AgentSessionServices>,
    diagnostics: Vec<Value>,
    model_fallback_message: Option<String>,
    rebind_session: Option<Rebind>,
    before_session_invalidate: Option<Arc<dyn Fn() + Send + Sync>>,
}

/// Pi's `AgentSessionRuntime`.
pub struct AgentSessionRuntime {
    context: Context,
    create: CreateAgentSessionRuntimeFactory,
    state: Mutex<State>,
}

fn text(value: &Value) -> Option<String> {
    value.as_str().filter(|text| !text.is_empty()).map(str::to_string)
}

fn session_file(manager: &SessionManager) -> io::Result<Option<String>> {
    Ok(text(&manager.get_session_file()?))
}

fn assert_session_cwd_exists(manager: &SessionManager, fallback_cwd: &str) -> io::Result<()> {
    let Some(file) = session_file(manager)? else { return Ok(()) };
    let cwd = manager.get_cwd()?.as_str().unwrap_or_default().to_string();
    if !cwd.is_empty() && !Path::new(&cwd).exists() {
        return Err(io::Error::other(MissingSessionCwdError { session_file: file, session_cwd: cwd, fallback_cwd: fallback_cwd.to_string() }));
    }
    Ok(())
}

fn user_message_text(content: &Value) -> String {
    match content {
        Value::String(text) => text.clone(),
        Value::Array(parts) => parts.iter().filter(|part| part["type"] == "text").filter_map(|part| part["text"].as_str()).collect(),
        _ => String::new(),
    }
}

fn resolve_path(input: &str) -> io::Result<PathBuf> {
    let trimmed = input.trim();
    let expanded = match trimmed.strip_prefix('~') {
        Some(rest) if rest.is_empty() || rest.starts_with('/') => {
            let home = std::env::var_os("HOME").ok_or_else(|| io::Error::other("the home directory is not set"))?;
            PathBuf::from(home).join(rest.trim_start_matches('/'))
        }
        _ => PathBuf::from(trimmed),
    };
    if expanded.is_absolute() { Ok(expanded) } else { Ok(std::env::current_dir()?.join(expanded)) }
}

fn reject_with_session(with_session: &Option<RuntimeWithSession>) -> io::Result<()> {
    if with_session.is_some() { Err(io::Error::other(WITH_SESSION)) } else { Ok(()) }
}

/// `createAgentSessionRuntime(createRuntime, options)`: it builds the initial runtime from the factory and keeps the factory for the replacements.
pub fn create_agent_session_runtime(context: &Context, create_runtime: CreateAgentSessionRuntimeFactory, options: CreateAgentSessionRuntimeOptions) -> io::Result<AgentSessionRuntime> {
    assert_session_cwd_exists(&options.session_manager, &options.cwd)?;
    let result = create_runtime(CreateAgentSessionRuntimeFactoryOptions {
        cwd: options.cwd,
        agent_dir: options.agent_dir,
        session_manager: options.session_manager,
        session_start_event: options.session_start_event,
    })?;
    Ok(AgentSessionRuntime {
        context: context.clone(),
        create: create_runtime,
        state: Mutex::new(State {
            session: Arc::new(result.session),
            services: Arc::new(result.services),
            diagnostics: result.diagnostics,
            model_fallback_message: result.model_fallback_message,
            rebind_session: None,
            before_session_invalidate: None,
        }),
    })
}

impl AgentSessionRuntime {
    /// `runtime.session`: the current session.
    pub fn session(&self) -> Arc<AgentSession> {
        self.state.lock().unwrap().session.clone()
    }

    /// `runtime.services`: the current cwd-bound services.
    pub fn services(&self) -> Arc<AgentSessionServices> {
        self.state.lock().unwrap().services.clone()
    }

    /// `runtime.cwd`: the working directory of the current services.
    pub fn cwd(&self) -> String {
        self.services().cwd.clone()
    }

    /// `runtime.diagnostics`.
    pub fn diagnostics(&self) -> Vec<Value> {
        self.state.lock().unwrap().diagnostics.clone()
    }

    /// `runtime.modelFallbackMessage`.
    pub fn model_fallback_message(&self) -> Option<String> {
        self.state.lock().unwrap().model_fallback_message.clone()
    }

    /// `setRebindSession(rebindSession?)`: the function runs with the new session after every replacement. `None` removes it.
    pub fn set_rebind_session(&self, rebind_session: Option<Rebind>) {
        self.state.lock().unwrap().rebind_session = rebind_session;
    }

    /// `setBeforeSessionInvalidate(callback?)`: the function runs after the current session's session_shutdown handlers finished and before the session is disposed. `None` removes it.
    pub fn set_before_session_invalidate(&self, callback: Option<Arc<dyn Fn() + Send + Sync>>) {
        self.state.lock().unwrap().before_session_invalidate = callback;
    }

    fn apply(&self, result: CreateAgentSessionRuntimeResult) {
        let mut state = self.state.lock().unwrap();
        state.session = Arc::new(result.session);
        state.services = Arc::new(result.services);
        state.diagnostics = result.diagnostics;
        state.model_fallback_message = result.model_fallback_message;
    }

    /// emitBeforeSwitch and emitBeforeFork: `hasHandlers(type)` then `emit(event)`, and whether a handler cancelled.
    fn emit_before(&self, event: Value) -> io::Result<bool> {
        let (result, emitted) = self.session().emit_extension_event(event)?;
        Ok(emitted && result["cancel"] == true)
    }

    fn emit_before_switch(&self, reason: &str, target_session_file: Option<&str>) -> io::Result<bool> {
        let mut event = json!({"type": "session_before_switch", "reason": reason});
        if let Some(target) = target_session_file.filter(|target| !target.is_empty()) {
            event["targetSessionFile"] = json!(target);
        }
        self.emit_before(event)
    }

    fn teardown_current(&self, reason: &str, target_session_file: Option<&str>) -> io::Result<()> {
        let session = self.session();
        // Settle any active response first so the aborted turn (including tool results) is persisted to the outgoing session before it is replaced.
        session.abort()?;
        let mut event = json!({"type": "session_shutdown", "reason": reason});
        if let Some(target) = target_session_file.filter(|target| !target.is_empty()) {
            event["targetSessionFile"] = json!(target);
        }
        session.emit_extension_event(event)?;
        let callback = self.state.lock().unwrap().before_session_invalidate.clone();
        if let Some(callback) = callback {
            callback();
        }
        session.dispose();
        Ok(())
    }

    fn finish_session_replacement(&self) -> io::Result<()> {
        let (rebind, session) = {
            let state = self.state.lock().unwrap();
            (state.rebind_session.clone(), state.session.clone())
        };
        match rebind {
            Some(rebind) => rebind(&session),
            None => Ok(()),
        }
    }

    fn replace(&self, cwd: String, manager: Arc<SessionManager>, reason: &str, previous_session_file: Option<String>) -> io::Result<()> {
        let result = (self.create)(CreateAgentSessionRuntimeFactoryOptions {
            cwd,
            agent_dir: self.services().agent_dir.clone(),
            session_manager: manager,
            session_start_event: Some(SessionStartEvent { reason: reason.to_string(), previous_session_file }),
        })?;
        self.apply(result);
        Ok(())
    }

    fn current_session_file(&self) -> io::Result<Option<String>> {
        session_file(&self.session().session_manager()?)
    }

    /// `switchSession(sessionPath, options?)`.
    pub fn switch_session(&self, session_path: &str, options: SwitchSessionRuntimeOptions) -> io::Result<CancelledResult> {
        reject_with_session(&options.with_session)?;
        if self.emit_before_switch("resume", Some(session_path))? {
            return Ok(CancelledResult { cancelled: true });
        }
        let previous = self.current_session_file()?;
        let manager = Arc::new(SessionManager::open(&self.context, session_path, None, options.cwd_override.as_deref())?);
        assert_session_cwd_exists(&manager, &self.cwd())?;
        self.teardown_current("resume", session_file(&manager)?.as_deref())?;
        let cwd = manager.get_cwd()?.as_str().unwrap_or_default().to_string();
        self.replace(cwd, manager, "resume", previous)?;
        self.finish_session_replacement()?;
        Ok(CancelledResult::default())
    }

    /// `newSession(options?)`.
    pub fn new_session(&self, options: NewSessionRuntimeOptions) -> io::Result<CancelledResult> {
        reject_with_session(&options.with_session)?;
        if self.emit_before_switch("new", None)? {
            return Ok(CancelledResult { cancelled: true });
        }
        let current = self.session().session_manager()?;
        let previous = session_file(&current)?;
        let session_dir = current.get_session_dir()?.as_str().unwrap_or_default().to_string();
        let cwd = self.cwd();
        let manager = Arc::new(if current.is_persisted()?.as_bool().unwrap_or(false) {
            SessionManager::create(&self.context, &cwd, Some(&session_dir), None)?
        } else {
            SessionManager::in_memory(&self.context, Some(&cwd), None, None)?
        });
        if let Some(parent) = &options.parent_session {
            manager.new_session(Some(&NewSessionOptions { id: None, parent_session: Some(parent.clone()) }))?;
        }
        self.teardown_current("new", session_file(&manager)?.as_deref())?;
        self.replace(cwd, manager, "new", previous)?;
        if let Some(setup) = &options.setup {
            setup(&self.session().session_manager()?)?;
            self.session().refresh_context()?;
        }
        self.finish_session_replacement()?;
        Ok(CancelledResult::default())
    }

    /// `fork(entryId, options?)`.
    pub fn fork(&self, entry_id: &str, options: ForkRuntimeOptions) -> io::Result<ForkResult> {
        reject_with_session(&options.with_session)?;
        let position = options.position.clone().unwrap_or_else(|| "before".to_string());
        if self.emit_before(json!({"type": "session_before_fork", "entryId": entry_id, "position": position}))? {
            return Ok(ForkResult { cancelled: true, selected_text: None });
        }
        let current = Arc::new(self.session().session_manager()?);
        let entry = current.get_entry(entry_id)?;
        if entry.is_null() {
            return Err(io::Error::other("Invalid entry ID for forking"));
        }
        let mut selected_text = None;
        let target_leaf_id = if position == "at" {
            text(&entry["id"])
        } else {
            if entry["type"] != "message" || entry["message"]["role"] != "user" {
                return Err(io::Error::other("Invalid entry ID for forking"));
            }
            selected_text = Some(user_message_text(&entry["message"]["content"]));
            text(&entry["parentId"])
        };
        let previous = session_file(&current)?;
        let finish = |manager: Arc<SessionManager>, cwd: String| -> io::Result<ForkResult> {
            self.replace(cwd, manager, "fork", previous.clone())?;
            self.finish_session_replacement()?;
            Ok(ForkResult { cancelled: false, selected_text: selected_text.clone() })
        };
        let cwd = self.cwd();
        if current.is_persisted()?.as_bool().unwrap_or(false) {
            let Some(current_file) = previous.clone() else { return Err(io::Error::other("Persisted session is missing a session file")) };
            let session_dir = current.get_session_dir()?.as_str().unwrap_or_default().to_string();
            let Some(leaf) = target_leaf_id else {
                let manager = Arc::new(SessionManager::create(&self.context, &cwd, Some(&session_dir), None)?);
                manager.new_session(Some(&NewSessionOptions { id: None, parent_session: Some(current_file) }))?;
                self.teardown_current("fork", session_file(&manager)?.as_deref())?;
                return finish(manager, cwd);
            };
            if !Path::new(&current_file).exists() {
                return Err(io::Error::other("This session has not been saved yet. Send a message before cloning or forking it."));
            }
            let manager = Arc::new(SessionManager::open(&self.context, &current_file, Some(&session_dir), None)?);
            if text(&manager.create_branched_session(&leaf)?).is_none() {
                return Err(io::Error::other("Failed to create forked session"));
            }
            self.teardown_current("fork", session_file(&manager)?.as_deref())?;
            let manager_cwd = manager.get_cwd()?.as_str().unwrap_or_default().to_string();
            return finish(manager, manager_cwd);
        }
        self.teardown_current("fork", session_file(&current)?.as_deref())?;
        match target_leaf_id {
            None => {
                current.new_session(previous.clone().map(|parent| NewSessionOptions { id: None, parent_session: Some(parent) }).as_ref())?;
            }
            Some(leaf) => {
                current.create_branched_session(&leaf)?;
            }
        }
        finish(current, cwd)
    }

    /// `importFromJsonl(inputPath, cwdOverride?)`: it copies the session file into the current session directory (unless it is already there) and switches to it. The file's absence is a `SessionImportFileNotFoundError` (the source of the returned error).
    pub fn import_from_jsonl(&self, input_path: &str, cwd_override: Option<&str>) -> io::Result<CancelledResult> {
        let resolved = resolve_path(input_path)?;
        if !resolved.exists() {
            return Err(io::Error::other(SessionImportFileNotFoundError { file_path: resolved.to_string_lossy().into_owned() }));
        }
        let session_dir = self.session().session_manager()?.get_session_dir()?.as_str().unwrap_or_default().to_string();
        if !Path::new(&session_dir).exists() {
            std::fs::create_dir_all(&session_dir)?;
        }
        let mut destination = Path::new(&session_dir).join(resolved.file_name().unwrap_or_default());
        let already_stored = destination == resolved;
        if !already_stored {
            let stem = destination.file_stem().unwrap_or_default().to_string_lossy().into_owned();
            let ext = destination.extension().map(|ext| format!(".{}", ext.to_string_lossy())).unwrap_or_default();
            let mut suffix = 1;
            while destination.exists() {
                destination = Path::new(&session_dir).join(format!("{stem}-{suffix}{ext}"));
                suffix += 1;
            }
        }
        let destination_text = destination.to_string_lossy().into_owned();
        if self.emit_before_switch("resume", Some(&destination_text))? {
            return Ok(CancelledResult { cancelled: true });
        }
        let previous = self.current_session_file()?;
        if !already_stored {
            let mut source = std::fs::File::open(&resolved)?;
            let mut target = OpenOptions::new().write(true).create_new(true).open(&destination)?;
            io::copy(&mut source, &mut target)?;
        }
        let manager = Arc::new(SessionManager::open(&self.context, &destination_text, Some(&session_dir), cwd_override)?);
        assert_session_cwd_exists(&manager, &self.cwd())?;
        self.teardown_current("resume", session_file(&manager)?.as_deref())?;
        let cwd = manager.get_cwd()?.as_str().unwrap_or_default().to_string();
        self.replace(cwd, manager, "resume", previous)?;
        self.finish_session_replacement()?;
        Ok(CancelledResult::default())
    }

    /// `dispose()`: it emits session_shutdown with the reason "quit" to the current session's extensions, runs the before-invalidate callback and disposes the session.
    pub fn dispose(&self) -> io::Result<()> {
        let session = self.session();
        session.emit_extension_event(json!({"type": "session_shutdown", "reason": "quit"}))?;
        let callback = self.state.lock().unwrap().before_session_invalidate.clone();
        if let Some(callback) = callback {
            callback();
        }
        session.dispose();
        Ok(())
    }
}

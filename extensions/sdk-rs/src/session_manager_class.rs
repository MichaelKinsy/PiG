//! Pi's `SessionManager` class (core/session-manager.ts): an append-only session log an extension constructs, reads and appends to. The host owns the log; a `SessionManager` passed to `create_agent_session` is the very log the child session appends to. `ctx.session_manager()` is Pi's `ReadonlySessionManager` ([`crate::ReadonlySessionManager`]).

use crate::context::Context;
use crate::graph::{call_static, call_static_async, callback_id, GraphCallback, GraphObject};
use serde_json::{json, Value};
use std::io;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

/// Pi's `NewSessionOptions`. `None` is Pi's undefined.
#[derive(Debug, Clone, Default)]
pub struct NewSessionOptions {
    pub id: Option<String>,
    pub parent_session: Option<String>,
}

impl NewSessionOptions {
    fn to_value(&self) -> Value {
        let mut object = serde_json::Map::new();
        if let Some(id) = &self.id {
            object.insert("id".into(), json!(id));
        }
        if let Some(parent) = &self.parent_session {
            object.insert("parentSession".into(), json!(parent));
        }
        Value::Object(object)
    }
}

fn options_value(options: Option<&NewSessionOptions>) -> Value {
    options.map_or(Value::Null, NewSessionOptions::to_value)
}

/// Pi's `SessionInfo`: one session file as [`SessionManager::list`] reports it.
#[derive(Debug, Clone, PartialEq)]
pub struct SessionInfo {
    pub path: String,
    pub id: String,
    pub cwd: String,
    pub name: Option<String>,
    pub parent_session_path: Option<String>,
    pub created: SystemTime,
    pub modified: SystemTime,
    pub message_count: u64,
    pub first_message: String,
    pub all_messages_text: String,
}

/// `Date.toISOString` text (`YYYY-MM-DDTHH:MM:SS.mmmZ`) as a time.
fn parse_iso(text: &str) -> io::Result<SystemTime> {
    let bad = || io::Error::new(io::ErrorKind::InvalidData, format!("invalid date {text:?}"));
    let number = |range: std::ops::Range<usize>| text.get(range).and_then(|part| part.parse::<i64>().ok()).ok_or_else(bad);
    let (year, month, day) = (number(0..4)?, number(5..7)?, number(8..10)?);
    let (hour, minute, second) = (number(11..13)?, number(14..16)?, number(17..19)?);
    let millis = if text.get(19..20) == Some(".") { number(20..23)? } else { 0 };
    // Days from the civil calendar (Howard Hinnant's algorithm).
    let year = if month <= 2 { year - 1 } else { year };
    let era = year.div_euclid(400);
    let year_of_era = year - era * 400;
    let day_of_year = (153 * ((month + 9) % 12) + 2) / 5 + day - 1;
    let day_of_era = year_of_era * 365 + year_of_era / 4 - year_of_era / 100 + day_of_year;
    let days = era * 146097 + day_of_era - 719468;
    let total_millis = ((days * 24 + hour) * 60 + minute) * 60_000 + second * 1000 + millis;
    Ok(if total_millis >= 0 {
        UNIX_EPOCH + Duration::from_millis(total_millis as u64)
    } else {
        UNIX_EPOCH - Duration::from_millis(total_millis.unsigned_abs())
    })
}

fn session_infos(value: Value) -> io::Result<Vec<SessionInfo>> {
    let text = |info: &Value, key: &str| info.get(key).and_then(Value::as_str).unwrap_or_default().to_string();
    let optional = |info: &Value, key: &str| info.get(key).and_then(Value::as_str).map(str::to_string);
    value
        .as_array()
        .map(Vec::as_slice)
        .unwrap_or_default()
        .iter()
        .map(|info| {
            Ok(SessionInfo {
                path: text(info, "path"),
                id: text(info, "id"),
                cwd: text(info, "cwd"),
                name: optional(info, "name"),
                parent_session_path: optional(info, "parentSessionPath"),
                created: parse_iso(&text(info, "created"))?,
                modified: parse_iso(&text(info, "modified"))?,
                message_count: info.get("messageCount").and_then(Value::as_u64).unwrap_or(0),
                first_message: text(info, "firstMessage"),
                all_messages_text: text(info, "allMessagesText"),
            })
        })
        .collect()
}

fn opt(value: Option<&str>) -> Value {
    value.map_or(Value::Null, |text| json!(text))
}

/// Pi's `SessionManager`. Dropping it releases the host's reference; a session created from it keeps its own.
pub struct SessionManager {
    object: GraphObject,
}

const KIND: &str = "SessionManager";

/// Pi's `SessionListProgress`: `(loaded, total, partial)`.
pub type ListProgress = Arc<dyn Fn(u64, u64, Value) + Send + Sync>;

fn progress_id(context: &Context, on_progress: Option<ListProgress>) -> Value {
    let callback: Option<GraphCallback> = on_progress.map(|progress| {
        Arc::new(move |args: Vec<Value>| {
            progress(args.first().and_then(Value::as_u64).unwrap_or(0), args.get(1).and_then(Value::as_u64).unwrap_or(0), args.get(2).cloned().unwrap_or(Value::Null));
            Ok(Value::Null)
        }) as GraphCallback
    });
    callback_id(context, callback)
}

impl SessionManager {
    fn construct(context: &Context, op: &str, args: Vec<Value>) -> io::Result<SessionManager> {
        Ok(SessionManager { object: GraphObject::construct(context, KIND, op, args)? })
    }

    /// `SessionManager.create(cwd, sessionDir?, options?)`: a new persisted session. `None` for `session_dir` is the default session directory of `cwd`.
    pub fn create(context: &Context, cwd: &str, session_dir: Option<&str>, options: Option<&NewSessionOptions>) -> io::Result<SessionManager> {
        Self::construct(context, "create", vec![json!(cwd), opt(session_dir), options_value(options)])
    }

    /// `SessionManager.open(path, sessionDir?, cwdOverride?)`: the session file at `path`.
    pub fn open(context: &Context, path: &str, session_dir: Option<&str>, cwd_override: Option<&str>) -> io::Result<SessionManager> {
        Self::construct(context, "open", vec![json!(path), opt(session_dir), opt(cwd_override)])
    }

    /// `SessionManager.continueRecent(cwd, sessionDir?)`: the most recent session of the directory, or a new one when there is none.
    pub fn continue_recent(context: &Context, cwd: &str, session_dir: Option<&str>) -> io::Result<SessionManager> {
        Self::construct(context, "continueRecent", vec![json!(cwd), opt(session_dir)])
    }

    /// `SessionManager.inMemory(cwd = process.cwd(), options?, entries?)`: a session that is never written. `None` for `cwd` is the process's working directory; `entries` are the records (the header optional) the session starts from.
    pub fn in_memory(context: &Context, cwd: Option<&str>, options: Option<&NewSessionOptions>, entries: Option<Vec<Value>>) -> io::Result<SessionManager> {
        let cwd = match cwd {
            Some(cwd) => cwd.to_string(),
            None => std::env::current_dir()?.to_string_lossy().into_owned(),
        };
        Self::construct(context, "inMemory", vec![json!(cwd), options_value(options), entries.map_or(Value::Null, Value::Array)])
    }

    /// `SessionManager.forkFrom(sourcePath, targetCwd, sessionDir?, options?)`: a new session in `target_cwd` with the whole history of the source session.
    pub fn fork_from(context: &Context, source_path: &str, target_cwd: &str, session_dir: Option<&str>, options: Option<&NewSessionOptions>) -> io::Result<SessionManager> {
        Self::construct(context, "forkFrom", vec![json!(source_path), json!(target_cwd), opt(session_dir), options_value(options)])
    }

    /// `SessionManager.findById(cwd, id, sessionDir?)`: the path of the session file with that exact id.
    pub fn find_by_id(context: &Context, cwd: &str, id: &str, session_dir: Option<&str>) -> io::Result<Option<String>> {
        Ok(call_static(context, KIND, "findById", vec![json!(cwd), json!(id), opt(session_dir)])?.as_str().map(str::to_string))
    }

    /// `SessionManager.list(cwd, sessionDir?, onProgress?)`: the sessions of a directory, most recently modified first. `on_progress(loaded, total, partial)` is called as session files are read; `partial` is the sessions read so far (Pi's SessionInfo as JSON) on the updates that carry them and `Null` on the others.
    pub fn list(context: &Context, cwd: &str, session_dir: Option<&str>, on_progress: Option<ListProgress>) -> io::Result<Vec<SessionInfo>> {
        session_infos(call_static_async(context, KIND, "list", vec![json!(cwd), opt(session_dir), progress_id(context, on_progress)])?)
    }

    /// `SessionManager.listAll(sessionDir?, onProgress?)`: the sessions of every project (or of `session_dir`), most recently modified first.
    pub fn list_all(context: &Context, session_dir: Option<&str>, on_progress: Option<ListProgress>) -> io::Result<Vec<SessionInfo>> {
        session_infos(call_static_async(context, KIND, "listAll", vec![opt(session_dir), progress_id(context, on_progress)])?)
    }

    /// The host's name for the log, which `create_agent_session` puts on the wire.
    pub fn handle(&self) -> &str {
        self.object.handle()
    }

    fn call(&self, method: &str, args: Vec<Value>) -> io::Result<Value> {
        self.object.call(method, args)
    }

    pub fn get_cwd(&self) -> io::Result<Value> { self.call("getCwd", vec![]) }
    pub fn get_session_dir(&self) -> io::Result<Value> { self.call("getSessionDir", vec![]) }
    pub fn get_session_id(&self) -> io::Result<Value> { self.call("getSessionId", vec![]) }
    pub fn get_session_file(&self) -> io::Result<Value> { self.call("getSessionFile", vec![]) }
    pub fn get_session_name(&self) -> io::Result<Value> { self.call("getSessionName", vec![]) }
    pub fn get_leaf_id(&self) -> io::Result<Value> { self.call("getLeafId", vec![]) }
    pub fn get_header(&self) -> io::Result<Value> { self.call("getHeader", vec![]) }
    pub fn get_leaf_entry(&self) -> io::Result<Value> { self.call("getLeafEntry", vec![]) }
    pub fn get_entry(&self, id: &str) -> io::Result<Value> { self.call("getEntry", vec![json!(id)]) }
    pub fn get_label(&self, id: &str) -> io::Result<Value> { self.call("getLabel", vec![json!(id)]) }
    pub fn get_entry_count(&self) -> io::Result<Value> { self.call("getEntryCount", vec![]) }
    pub fn get_entries(&self) -> io::Result<Value> { self.call("getEntries", vec![]) }
    pub fn get_branch(&self, from_id: Option<&str>) -> io::Result<Value> { self.call("getBranch", vec![opt(from_id)]) }
    pub fn get_children(&self, parent_id: &str) -> io::Result<Value> { self.call("getChildren", vec![json!(parent_id)]) }
    pub fn get_tree(&self) -> io::Result<Value> { self.call("getTree", vec![]) }
    pub fn build_context_entries(&self) -> io::Result<Value> { self.call("buildContextEntries", vec![]) }
    pub fn build_session_projection(&self) -> io::Result<Value> { self.call("buildSessionProjection", vec![]) }
    pub fn build_session_context(&self) -> io::Result<Value> { self.call("buildSessionContext", vec![]) }
    pub fn is_persisted(&self) -> io::Result<Value> { self.call("isPersisted", vec![]) }
    pub fn uses_default_session_dir(&self) -> io::Result<Value> { self.call("usesDefaultSessionDir", vec![]) }

    /// Appends a message (the JSON object of a Pi Message, CustomMessage or BashExecutionMessage); returns the entry id.
    pub fn append_message(&self, message: Value) -> io::Result<Value> { self.call("appendMessage", vec![message]) }
    pub fn append_thinking_level_change(&self, thinking_level: &str) -> io::Result<Value> { self.call("appendThinkingLevelChange", vec![json!(thinking_level)]) }
    pub fn append_model_change(&self, provider: &str, model_id: &str) -> io::Result<Value> { self.call("appendModelChange", vec![json!(provider), json!(model_id)]) }
    /// Records the usage of work outside the conversation; returns the entry.
    pub fn append_usage(&self, kind: &str, provider: &str, model: &str, usage: Value, note: Option<&str>) -> io::Result<Value> {
        self.call("appendUsage", vec![json!(kind), json!(provider), json!(model), usage, opt(note)])
    }
    pub fn append_compaction(&self, summary: &str, first_kept_entry_id: &str, tokens_before: u64, details: Option<Value>, from_hook: Option<bool>, usage: Option<Value>) -> io::Result<Value> {
        self.call("appendCompaction", vec![json!(summary), json!(first_kept_entry_id), json!(tokens_before), details.unwrap_or(Value::Null), from_hook.map_or(Value::Null, Value::Bool), usage.unwrap_or(Value::Null)])
    }
    pub fn append_custom_entry(&self, custom_type: &str, data: Option<Value>) -> io::Result<Value> { self.call("appendCustomEntry", vec![json!(custom_type), data.unwrap_or(Value::Null)]) }
    pub fn append_session_info(&self, name: &str) -> io::Result<Value> { self.call("appendSessionInfo", vec![json!(name)]) }
    pub fn append_custom_message_entry(&self, custom_type: &str, content: Value, display: bool, details: Option<Value>) -> io::Result<Value> {
        self.call("appendCustomMessageEntry", vec![json!(custom_type), content, json!(display), details.unwrap_or(Value::Null)])
    }
    pub fn append_context_edit(&self, target_id: &str, replacement: Value) -> io::Result<Value> { self.call("appendContextEdit", vec![json!(target_id), replacement]) }
    /// Sets or clears (`None`) the label of an entry.
    pub fn append_label_change(&self, target_id: &str, label: Option<&str>) -> io::Result<Value> { self.call("appendLabelChange", vec![json!(target_id), opt(label)]) }
    pub fn branch(&self, branch_from_id: &str) -> io::Result<()> { self.call("branch", vec![json!(branch_from_id)]).map(|_| ()) }
    pub fn reset_leaf(&self) -> io::Result<()> { self.call("resetLeaf", vec![]).map(|_| ()) }
    pub fn branch_with_summary(&self, branch_from_id: Option<&str>, summary: &str, details: Option<Value>, from_hook: Option<bool>, usage: Option<Value>) -> io::Result<Value> {
        self.call("branchWithSummary", vec![opt(branch_from_id), json!(summary), details.unwrap_or(Value::Null), from_hook.map_or(Value::Null, Value::Bool), usage.unwrap_or(Value::Null)])
    }
    /// Starts a new session holding the path to `leaf_id`; returns the new session file, null when the session is not persisted.
    pub fn create_branched_session(&self, leaf_id: &str) -> io::Result<Value> { self.call("createBranchedSession", vec![json!(leaf_id)]) }
    /// Resets the manager to an empty session; returns the new session file, null when the session is not persisted.
    pub fn new_session(&self, options: Option<&NewSessionOptions>) -> io::Result<Value> { self.call("newSession", vec![options_value(options)]) }
    pub fn set_session_file(&self, session_file: &str) -> io::Result<()> { self.call("setSessionFile", vec![json!(session_file)]).map(|_| ()) }
}

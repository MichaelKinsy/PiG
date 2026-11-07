use crate::context::Context;
use serde_json::{Value, json};
use std::io;

/// Pi's `ReadonlySessionManager`: reads the active host session and returns transport or host failures directly.
pub struct ReadonlySessionManager<'a> {
    context: &'a Context,
}

impl Context {
    pub fn session_manager(&self) -> ReadonlySessionManager<'_> {
        ReadonlySessionManager { context: self }
    }
}

impl ReadonlySessionManager<'_> {
    fn read(&self, method: &str, args: Value) -> io::Result<Value> {
        self.context
            .call_host("sessionRead", Some(json!({"method": method, "args": args})))
            .map(|value| value.unwrap_or(Value::Null))
    }
    pub fn get_cwd(&self) -> io::Result<Value> {
        self.read("getCwd", Value::Null)
    }
    pub fn get_session_dir(&self) -> io::Result<Value> {
        self.read("getSessionDir", Value::Null)
    }
    /// The replicated session id (see [`Context::get_session_id`]); a replacement context reads it from the host.
    pub fn get_session_id(&self) -> io::Result<Value> {
        if let Some(id) = self.context.replicated_session_id()? {
            return Ok(Value::String(id));
        }
        self.read("getSessionId", Value::Null)
    }
    /// The replicated session file, `null` for an in-memory session; the host's for a replacement context.
    pub fn get_session_file(&self) -> io::Result<Value> {
        let file = self
            .context
            .replicated_session(|identity| &identity.session_file)?;
        file.map_or_else(|| self.read("getSessionFile", Value::Null), none_if_empty)
    }
    /// The replicated session name, `null` for none; the host's for a replacement context.
    pub fn get_session_name(&self) -> io::Result<Value> {
        let name = self
            .context
            .replicated_session(|identity| &identity.session_name)?;
        name.map_or_else(|| self.read("getSessionName", Value::Null), none_if_empty)
    }
    pub fn get_leaf_id(&self) -> io::Result<Value> {
        self.read("getLeafId", Value::Null)
    }
    pub fn get_header(&self) -> io::Result<Value> {
        self.read("getHeader", Value::Null)
    }
    pub fn get_leaf_entry(&self) -> io::Result<Value> {
        self.read("getLeafEntry", Value::Null)
    }
    pub fn get_entry(&self, id: &str) -> io::Result<Value> {
        self.read("getEntry", json!({"id": id}))
    }
    pub fn get_label(&self, id: &str) -> io::Result<Value> {
        self.read("getLabel", json!({"id": id}))
    }
    pub fn get_entries(&self) -> io::Result<Value> {
        self.read("getEntries", Value::Null)
    }
    pub fn get_branch(&self, from_id: Option<&str>) -> io::Result<Value> {
        self.read("getBranch", json!({"fromId": from_id}))
    }
    pub fn get_children(&self, parent_id: Option<&str>) -> io::Result<Value> {
        self.read("getChildren", json!({"parentId": parent_id}))
    }
    pub fn get_tree(&self) -> io::Result<Value> {
        self.read("getTree", Value::Null)
    }
    pub fn build_context_entries(&self) -> io::Result<Value> {
        self.read("buildContextEntries", Value::Null)
    }
    pub fn build_session_projection(&self) -> io::Result<Value> {
        self.read("buildSessionProjection", Value::Null)
    }
    pub fn build_session_context(&self) -> io::Result<Value> {
        self.read("buildSessionContext", Value::Null)
    }
    pub fn is_persisted(&self) -> io::Result<Value> {
        self.read("isPersisted", Value::Null)
    }
    pub fn uses_default_session_dir(&self) -> io::Result<Value> {
        self.read("usesDefaultSessionDir", Value::Null)
    }
}

/// The host reads an empty name or file as none, and answers `null` for it.
fn none_if_empty(text: String) -> io::Result<Value> {
    Ok(if text.is_empty() {
        Value::Null
    } else {
        Value::String(text)
    })
}

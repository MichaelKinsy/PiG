//! Pi's newSession `setup` callback (types.ts:411, agent-session-runtime.ts:254-257) across the process boundary.

use crate::context::Context;
use crate::protocol::{Connection, ErrorInfo};
use crate::session_manager::SessionManager;
use serde_json::{json, Value};
use std::collections::HashMap;
use std::io;
use std::ops::Deref;
use std::sync::{Arc, Mutex};

/// The SessionManager that Pi hands the setup callback of `new_session_setup`: the reads of [`SessionManager`] and the
/// appends that seed the replacement Session. Each call acts on the replacement Session and returns the new entry's id.
pub struct SetupSessionManager<'a> {
    reads: SessionManager<'a>,
    context: &'a Context,
}

impl<'a> Deref for SetupSessionManager<'a> {
    type Target = SessionManager<'a>;

    fn deref(&self) -> &SessionManager<'a> {
        &self.reads
    }
}

impl SetupSessionManager<'_> {
    fn write(&self, method: &str, args: Value) -> io::Result<Value> {
        self.context
            .call_host("sessionWrite", Some(json!({"method": method, "args": args})))
            .map(|value| value.unwrap_or(Value::Null))
    }

    /// session-manager.ts:218; `message` is the JSON object of a Pi Message, CustomMessage or BashExecutionMessage.
    pub fn append_message(&self, message: Value) -> io::Result<Value> {
        self.write("appendMessage", json!({"message": message}))
    }

    /// session-manager.ts:304: an extension entry that does not take part in the model context.
    pub fn append_custom_entry(&self, custom_type: &str, data: Value) -> io::Result<Value> {
        self.write("appendCustomEntry", json!({"customType": custom_type, "data": data}))
    }

    /// session-manager.ts:353: an extension message that takes part in the model context.
    pub fn append_custom_message_entry(
        &self,
        custom_type: &str,
        content: Value,
        display: bool,
        details: Value,
    ) -> io::Result<Value> {
        self.write("appendCustomMessageEntry", custom_message_args(custom_type, content, display, details))
    }

    /// session-manager.ts:318: sets the session name.
    pub fn append_session_info(&self, name: &str) -> io::Result<Value> {
        self.write("appendSessionInfo", json!({"name": name}))
    }

    /// session-manager.ts:244.
    pub fn append_model_change(&self, provider: &str, model_id: &str) -> io::Result<Value> {
        self.write("appendModelChange", model_change_args(provider, model_id))
    }

    /// session-manager.ts:231.
    pub fn append_thinking_level_change(&self, thinking_level: &str) -> io::Result<Value> {
        self.write("appendThinkingLevelChange", json!({"thinkingLevel": thinking_level}))
    }

    /// session-manager.ts:455: sets or clears (None) the label of an entry.
    pub fn append_label_change(&self, target_id: &str, label: Option<&str>) -> io::Result<Value> {
        self.write("appendLabelChange", label_args(target_id, label))
    }
}

/// The sessionWrite arguments of appendCustomMessageEntry; the host reads `customType`, `content`, `display` and `details`.
fn custom_message_args(custom_type: &str, content: Value, display: bool, details: Value) -> Value {
    json!({"customType": custom_type, "content": content, "display": display, "details": details})
}

/// The sessionWrite arguments of appendModelChange; the host reads `provider` and `modelId`.
fn model_change_args(provider: &str, model_id: &str) -> Value {
    json!({"provider": provider, "modelId": model_id})
}

/// The sessionWrite arguments of appendLabelChange; the host reads `targetId` and `label` (null clears).
fn label_args(target_id: &str, label: Option<&str>) -> Value {
    json!({"targetId": target_id, "label": label})
}

/// A setup callback. Its error fails the `new_session_setup` call with the same kind and message.
pub type SetupFn = Box<dyn FnOnce(&SetupSessionManager) -> io::Result<()> + Send>;

struct SetupEntry {
    callback: Mutex<Option<SetupFn>>,
    error: Mutex<Option<(io::ErrorKind, String)>>,
}

/// The setup callbacks of newSession calls in flight, by the handle each call names.
#[derive(Default)]
pub(crate) struct SetupRegistry {
    next: Mutex<u64>,
    entries: Mutex<HashMap<String, Arc<SetupEntry>>>,
}

impl SetupRegistry {
    fn add(&self, callback: SetupFn) -> (String, Arc<SetupEntry>) {
        let handle = {
            let mut next = self.next.lock().unwrap();
            *next += 1;
            format!("setup:{next}")
        };
        let entry = Arc::new(SetupEntry {
            callback: Mutex::new(Some(callback)),
            error: Mutex::new(None),
        });
        self.entries.lock().unwrap().insert(handle.clone(), entry.clone());
        (handle, entry)
    }

    fn remove(&self, handle: &str) {
        self.entries.lock().unwrap().remove(handle);
    }

    fn get(&self, handle: &str) -> Option<Arc<SetupEntry>> {
        self.entries.lock().unwrap().get(handle).cloned()
    }
}

impl Context {
    /// `new_session` with Pi's `setup` callback (types.ts:411): it cannot cross the process boundary, so the call names it
    /// by handle and the host runs it with a setup request after the replacement Session is bound and before `withSession`.
    /// A callback error fails the call with that error, as Pi's awaited callback rejects the call.
    pub fn new_session_setup(
        &self,
        mut opts: Value,
        setup: impl FnOnce(&SetupSessionManager) -> io::Result<()> + Send + 'static,
        with_session: Option<crate::WithSessionFn>,
    ) -> io::Result<Option<Value>> {
        let (handle, entry) = self.conn.setups.add(Box::new(setup));
        if !opts.is_object() {
            opts = Value::Object(Default::default());
        }
        if let Some(object) = opts.as_object_mut() {
            object.insert("setup".to_string(), Value::String(handle.clone()));
        }
        let result = self.call_replacement("newSession", opts, with_session);
        self.conn.setups.remove(&handle);
        match result {
            Err(error) => match entry.error.lock().unwrap().take() {
                Some((kind, message)) => Err(io::Error::new(kind, message)),
                None => Err(error),
            },
            ok => ok,
        }
    }
}

/// Runs the callback a setup request names with the replacement Session's manager. `ctx` is the request's context, so its
/// host calls belong to the request.
pub(crate) fn dispatch(ctx: &Context, conn: &Arc<Connection>, args: Option<&Value>) -> Result<(), ErrorInfo> {
    let failure = |message: String| ErrorInfo { code: None, message };
    let args = args.cloned().unwrap_or(Value::Null);
    let handle = args["handle"].as_str().unwrap_or_default();
    let entry = conn
        .setups
        .get(handle)
        .ok_or_else(|| failure(format!("unknown setup callback {handle}")))?;
    let callback = entry
        .callback
        .lock()
        .unwrap()
        .take()
        .ok_or_else(|| failure(format!("setup callback {handle} already ran")))?;
    let manager = SetupSessionManager { reads: ctx.session_manager(), context: ctx };
    callback(&manager).map_err(|error| {
        let message = error.to_string();
        *entry.error.lock().unwrap() = Some((error.kind(), message.clone()));
        failure(message)
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    // types.ts:411: a setup callback stays in the extension; the call names it by handle, and a handle runs once.
    #[test]
    fn registry_names_callbacks_by_handle_and_releases_them() {
        let registry = SetupRegistry::default();
        let (first, entry) = registry.add(Box::new(|_| Ok(())));
        let (second, _) = registry.add(Box::new(|_| Ok(())));
        assert_ne!(first, second);
        assert!(first.starts_with("setup:"));
        let found = registry.get(&first).expect("registered handle");
        assert!(Arc::ptr_eq(&found, &entry));
        assert!(found.callback.lock().unwrap().take().is_some());
        assert!(found.callback.lock().unwrap().take().is_none(), "a handle's callback runs once");
        registry.remove(&first);
        assert!(registry.get(&first).is_none());
        assert!(registry.get(&second).is_some());
        assert!(registry.get("setup:99").is_none());
    }

    // session-manager.ts:353, 244, 455: the argument names are the host's applySessionWrite contract.
    #[test]
    fn session_write_arguments_use_the_hosts_names() {
        assert_eq!(
            custom_message_args("t", json!([{"type": "text"}]), true, json!({"k": 1})),
            json!({"customType": "t", "content": [{"type": "text"}], "display": true, "details": {"k": 1}})
        );
        assert_eq!(model_change_args("openai", "gpt-x"), json!({"provider": "openai", "modelId": "gpt-x"}));
        assert_eq!(label_args("e1", Some("L")), json!({"targetId": "e1", "label": "L"}));
        assert_eq!(label_args("e1", None), json!({"targetId": "e1", "label": null}));
    }
}

//! Pi's withSession callbacks (types.ts:410-452, agent-session-runtime.ts:187-194) across the process boundary.

use crate::context::Context;
use crate::protocol::{Connection, ErrorInfo};
use serde_json::Value;
use std::collections::HashMap;
use std::io;
use std::ops::Deref;
use std::sync::{Arc, Mutex};

/// Pi's `ReplacedSessionContext` (types.ts:442-452): the command context of the replacement Session that
/// `new_session_with`, `fork_with` and `switch_session_with` pass to their callback. Its host calls act on the
/// replacement Session, and `send_message` and `send_user_message` return after the Session operation, including the
/// turn they trigger. `cwd`, `mode`, `has_ui` and `model` answer for the replacement Session.
pub struct ReplacedSessionContext(Context);

impl Deref for ReplacedSessionContext {
    type Target = Context;

    fn deref(&self) -> &Context {
        &self.0
    }
}

impl ReplacedSessionContext {
    /// The underlying context, for APIs that take a `Context`.
    pub fn context(&self) -> &Context {
        &self.0
    }
}

/// A withSession callback. Its error fails the replacement call with the same kind and message.
pub type WithSessionFn = Box<dyn FnOnce(&ReplacedSessionContext) -> io::Result<()> + Send>;

struct WithSessionEntry {
    callback: Mutex<Option<WithSessionFn>>,
    error: Mutex<Option<(io::ErrorKind, String)>>,
}

/// The withSession callbacks of replacement calls in flight, by the handle each call names.
#[derive(Default)]
pub(crate) struct WithSessionRegistry {
    next: Mutex<u64>,
    entries: Mutex<HashMap<String, Arc<WithSessionEntry>>>,
}

impl WithSessionRegistry {
    fn add(&self, prefix: &str, callback: WithSessionFn) -> (String, Arc<WithSessionEntry>) {
        let handle = {
            let mut next = self.next.lock().unwrap();
            *next += 1;
            format!("{prefix}:{next}")
        };
        let entry = Arc::new(WithSessionEntry {
            callback: Mutex::new(Some(callback)),
            error: Mutex::new(None),
        });
        self.entries
            .lock()
            .unwrap()
            .insert(handle.clone(), entry.clone());
        (handle, entry)
    }

    fn remove(&self, handle: &str) {
        self.entries.lock().unwrap().remove(handle);
    }

    fn get(&self, handle: &str) -> Option<Arc<WithSessionEntry>> {
        self.entries.lock().unwrap().get(handle).cloned()
    }
}

impl Context {
    /// A withSession callback cannot cross the process boundary, so the call names it by handle and the host runs it
    /// with a with_session request before the call returns. A callback error fails the call with that error, as Pi's
    /// awaited callback rejects the call.
    pub(crate) fn call_replacement(
        &self,
        method: &str,
        mut args: Value,
        with_session: Option<WithSessionFn>,
    ) -> io::Result<Option<Value>> {
        let Some(callback) = with_session else {
            return self.call_host(method, Some(args));
        };
        let (handle, entry) = self.conn.with_sessions.add("with-session", callback);
        if !args.is_object() {
            args = Value::Object(Default::default());
        }
        if let Some(object) = args.as_object_mut() {
            object.insert("withSession".to_string(), Value::String(handle.clone()));
        }
        let result = self.call_host(method, Some(args));
        self.conn.with_sessions.remove(&handle);
        match result {
            Err(error) => match entry.error.lock().unwrap().take() {
                Some((kind, message)) => Err(io::Error::new(kind, message)),
                None => Err(error),
            },
            ok => ok,
        }
    }
}

/// Runs the callback a with_session request names with a context of the replacement Session. `ctx` is the request's
/// context, so its host calls belong to the request.
pub(crate) fn dispatch(
    ctx: &Context,
    conn: &Arc<Connection>,
    args: Option<&Value>,
) -> Result<(), ErrorInfo> {
    let failure = |message: String| ErrorInfo {
        code: None,
        message,
    };
    let args = args.cloned().unwrap_or(Value::Null);
    let handle = args["handle"].as_str().unwrap_or_default();
    let entry = conn
        .with_sessions
        .get(handle)
        .ok_or_else(|| failure(format!("unknown withSession callback {handle}")))?;
    let callback = entry
        .callback
        .lock()
        .unwrap()
        .take()
        .ok_or_else(|| failure(format!("withSession callback {handle} already ran")))?;
    let ready = &args["ready"];
    let state = &ready["state"];
    let mut replaced = ctx.clone();
    replaced.replacement = true;
    replaced.cwd = ready["cwd"].as_str().unwrap_or_default().to_string();
    if let Some(mode) = ready["mode"].as_str().filter(|mode| !mode.is_empty()) {
        replaced.mode = mode.to_string();
    }
    let model = &state["model"];
    let model_id = model["id"]
        .as_str()
        .filter(|id| !id.is_empty())
        .or_else(|| model["name"].as_str())
        .unwrap_or_default();
    replaced.shared_model = Arc::new(Mutex::new(model_id.to_string()));
    let mut ui = replaced.shared_ui.lock().unwrap().clone();
    ui.has_ui = state["hasUI"].as_bool().unwrap_or(false);
    replaced.shared_ui = Arc::new(Mutex::new(ui));
    let replaced = ReplacedSessionContext(replaced);
    callback(&replaced).map_err(|error| {
        let message = error.to_string();
        *entry.error.lock().unwrap() = Some((error.kind(), message.clone()));
        failure(message)
    })
}

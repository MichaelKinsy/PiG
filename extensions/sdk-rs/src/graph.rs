//! The session graph: the classes Pi extensions construct around a session (SessionManager, SettingsManager, ModelRuntime, DefaultResourceLoader, DefaultPackageManager, ProjectTrustStore). The host owns each object for the extension's connection; the value an extension holds names it. Every method is Pi's method under its snake_case name with Pi's positional parameters (`Option` is Pi's undefined) and returns Pi's result as the JSON value of Pi's type.

use crate::context::Context;
use serde_json::{json, Value};
use std::collections::HashMap;
use std::io;
use std::sync::{Arc, Mutex};

/// The extension's reference to a host object. The host drops its reference when this value is dropped or the connection closes.
pub(crate) struct GraphObject {
    context: Context,
    handle: String,
}

fn positional(args: Vec<Value>) -> Value {
    Value::Array(args)
}

impl GraphObject {
    /// Constructs a host object of Pi's class `kind` with its static factory `op` ("new" for a constructor).
    pub(crate) fn construct(context: &Context, kind: &str, op: &str, args: Vec<Value>) -> io::Result<GraphObject> {
        let reply = context
            .call_host("sessionGraph.new", Some(json!({"kind": kind, "op": op, "args": positional(args)})))?
            .unwrap_or(Value::Null);
        let handle = reply
            .get("handle")
            .and_then(Value::as_str)
            .filter(|handle| !handle.is_empty())
            .ok_or_else(|| io::Error::new(io::ErrorKind::Other, "host returned no session graph handle"))?
            .to_string();
        Ok(GraphObject { context: context.clone(), handle })
    }

    /// The extension's reference to the host object `handle` names, which the host made without a `sessionGraph.new` call (the log of a child session).
    pub(crate) fn adopt(context: &Context, handle: String) -> GraphObject {
        GraphObject { context: context.clone(), handle }
    }

    /// The connection the object was made on, which names the functions of its later calls.
    pub(crate) fn context(&self) -> &Context {
        &self.context
    }

    /// The host's name for the object, which an option that takes the object (`create_agent_session`) puts on the wire.
    pub(crate) fn handle(&self) -> &str {
        &self.handle
    }

    /// A method Pi returns synchronously.
    pub(crate) fn call(&self, method: &str, args: Vec<Value>) -> io::Result<Value> {
        self.context
            .call_host("sessionGraph.call", Some(json!({"handle": self.handle, "method": method, "args": positional(args)})))
            .map(|value| value.unwrap_or(Value::Null))
    }

    /// A method Pi returns a Promise from.
    pub(crate) fn call_async(&self, method: &str, args: Vec<Value>) -> io::Result<Value> {
        self.context
            .call_host("sessionGraph.async", Some(json!({"handle": self.handle, "method": method, "args": positional(args)})))
            .map(|value| value.unwrap_or(Value::Null))
    }
}

/// A static function of Pi's class `kind` that constructs no object.
pub(crate) fn call_static(context: &Context, kind: &str, method: &str, args: Vec<Value>) -> io::Result<Value> {
    context
        .call_host("sessionGraph.call", Some(json!({"kind": kind, "method": method, "args": positional(args)})))
        .map(|value| value.unwrap_or(Value::Null))
}

/// A static function of Pi's class `kind` that returns a Promise.
pub(crate) fn call_static_async(context: &Context, kind: &str, method: &str, args: Vec<Value>) -> io::Result<Value> {
    context
        .call_host("sessionGraph.async", Some(json!({"kind": kind, "method": method, "args": positional(args)})))
        .map(|value| value.unwrap_or(Value::Null))
}

impl Drop for GraphObject {
    fn drop(&mut self) {
        // Pi frees an object when it is unreachable. The release never blocks the thread that dropped the value, which may be a handler.
        let context = self.context.clone();
        let handle = std::mem::take(&mut self.handle);
        let _ = std::thread::Builder::new().name("pig-graph-release".into()).spawn(move || {
            let _ = context.call_host("sessionGraph.release", Some(json!({"handle": handle})));
        });
    }
}

/// A function an extension passed in an option of a session graph object: it takes the JSON arguments of Pi's function and answers its JSON result or the message of what it threw.
pub type GraphCallback = Arc<dyn Fn(Vec<Value>) -> Result<Value, String> + Send + Sync>;

/// The functions of one connection, by name. A function stays until the connection ends: the host may run it again on a later reload of the object it was passed to, including after the extension dropped the object.
#[derive(Default)]
pub(crate) struct GraphCallbacks {
    callbacks: Mutex<HashMap<String, GraphCallback>>,
    sequence: std::sync::atomic::AtomicU64,
}

/// Names `callback` for the host. `None` is Pi's undefined option and has no name.
pub(crate) fn callback_id(context: &Context, callback: Option<GraphCallback>) -> Value {
    let Some(callback) = callback else { return Value::Null };
    json!(add_callback(context, callback))
}

/// Names `callback` for the host and returns the name.
pub(crate) fn add_callback(context: &Context, callback: GraphCallback) -> String {
    let id = format!("cb{}", context.conn.graph_callbacks.sequence.fetch_add(1, std::sync::atomic::Ordering::Relaxed) + 1);
    context.conn.graph_callbacks.callbacks.lock().unwrap().insert(id.clone(), callback);
    id
}

/// Forgets a function the host will not call again (the callbacks of a closed transport).
pub(crate) fn remove_callback(context: &Context, id: &str) {
    context.conn.graph_callbacks.callbacks.lock().unwrap().remove(id);
}

/// Runs the function a `sessionGraph.callback` request names.
pub(crate) fn dispatch_callback(conn: &crate::protocol::Connection, args: Option<&Value>) -> Result<Value, String> {
    let request = args.cloned().unwrap_or(Value::Null);
    let id = request["id"].as_str().unwrap_or_default();
    let callback = conn.graph_callbacks.callbacks.lock().unwrap().get(id).cloned().ok_or_else(|| format!("unknown session graph callback {id}"))?;
    callback(request["args"].as_array().cloned().unwrap_or_default())
}

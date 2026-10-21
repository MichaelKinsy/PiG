//! The session graph: the classes Pi extensions construct around a session (SessionManager, SettingsManager, ModelRuntime, DefaultResourceLoader, DefaultPackageManager, ProjectTrustStore). The host owns each object for the extension's connection; the value an extension holds names it. Every method is Pi's method under its snake_case name with Pi's positional parameters (`Option` is Pi's undefined) and returns Pi's result as the JSON value of Pi's type.

use crate::context::Context;
use serde_json::{json, Value};
use std::io;

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

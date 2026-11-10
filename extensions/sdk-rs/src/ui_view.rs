//! pig additive (D614): plugin UI mounts.
//!
//! An extension mounts a [`crate::kit::View`] in a slot the attached client draws, updates it, and answers the user's
//! intents. The host replicates what the client draws ([`Context::view_abilities`]); with no client attached it is
//! `None` and a mount fails with code `"unsupported"`. The wire is the one the Go SDK speaks: the `ui.view.mount`,
//! `update`, `hide`, `show`, `finish`, `remove` and `state` calls, and the `ui.view.intent` request.

use std::collections::BTreeMap;
use std::sync::Mutex;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};

use crate::Context;

/// A failed mount operation. `code` is invalid, unsupported, limit, stale, terminal or not_found; `transport` is a
/// failure to reach the host.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ViewError {
    pub code: String,
    pub message: String,
}

impl std::fmt::Display for ViewError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.code, self.message)
    }
}

impl std::error::Error for ViewError {}

/// One revision of one mount incarnation. Pass the ref the last operation returned to the next update.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ViewRef {
    pub key: String,
    #[serde(default)]
    pub epoch: u64,
    #[serde(default)]
    pub rev: u64,
}

/// The reply of every mount operation. `state` is live, hidden, finished, removed or broken; `reason` is set for broken:
/// session_replaced, owner_gone or shutdown.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct ViewStatus {
    pub mount: ViewRef,
    pub state: String,
    #[serde(default)]
    pub reason: String,
}

/// Ties a tool-card, message or entry mount to what it decorates; set exactly one field.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct ViewTarget {
    #[serde(rename = "toolCallId", skip_serializing_if = "String::is_empty")]
    pub tool_call_id: String,
    #[serde(rename = "messageType", skip_serializing_if = "String::is_empty")]
    pub message_type: String,
    #[serde(rename = "entryType", skip_serializing_if = "String::is_empty")]
    pub entry_type: String,
}

/// The closed JSON subset an intent argument or answer is checked against: type boolean, string, integer, number, enum
/// (`enum_values`, at most 32), object (`properties` at most 32, `required`) or array (`items`, `max_items` at most 64),
/// nested to depth 4.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct ViewShape {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(rename = "enum", skip_serializing_if = "Vec::is_empty")]
    pub enum_values: Vec<String>,
    #[serde(skip_serializing_if = "BTreeMap::is_empty")]
    pub properties: BTreeMap<String, ViewShape>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub required: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub items: Option<Box<ViewShape>>,
    #[serde(rename = "maxItems", skip_serializing_if = "is_zero")]
    pub max_items: u32,
}

fn is_zero(n: &u32) -> bool {
    *n == 0
}

impl ViewShape {
    pub fn new(kind: &str) -> Self {
        Self { kind: kind.into(), ..Self::default() }
    }

    pub fn object(properties: &[(&str, ViewShape)], required: &[&str]) -> Self {
        Self {
            kind: "object".into(),
            properties: properties.iter().map(|(k, v)| ((*k).to_string(), v.clone())).collect(),
            required: required.iter().map(|s| (*s).to_string()).collect(),
            ..Self::default()
        }
    }
}

/// One intent the view's nodes may raise. `name` is `"<extension>.<verb>"`; `timeout_ms` defaults to 30000 (0) and is
/// 100..300000.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct ViewIntent {
    pub name: String,
    #[serde(rename = "nodeKey", skip_serializing_if = "String::is_empty")]
    pub node_key: String,
    pub arg: ViewShape,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub answer: Option<ViewShape>,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub cancellable: bool,
    #[serde(rename = "timeoutMs", skip_serializing_if = "is_zero")]
    pub timeout_ms: u32,
}

/// A bounded image a view refers to by id; `delivery` is inline or managed.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct ViewAsset {
    pub id: String,
    pub mime: String,
    pub size: u64,
    pub sha256: String,
    pub delivery: String,
}

/// The argument of [`Context::mount_view`].
pub struct ViewMount {
    pub key: String,
    pub slot: String,
    pub target: Option<ViewTarget>,
    pub view: crate::kit::View,
    pub intents: Vec<ViewIntent>,
    pub assets: Vec<ViewAsset>,
    pub fallback: Option<crate::kit::Node>,
}

/// The argument of [`Context::update_view`]: a full replacement of the content of the revision `mount` names.
pub struct ViewUpdate {
    pub mount: ViewRef,
    pub view: crate::kit::View,
    pub intents: Vec<ViewIntent>,
    pub assets: Vec<ViewAsset>,
    pub fallback: Option<crate::kit::Node>,
}

/// Bounds of one mount on the attached client.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct ViewLimits {
    #[serde(default)]
    pub nodes: u64,
    #[serde(default)]
    pub depth: u64,
    #[serde(default, rename = "snapshotBytes")]
    pub snapshot_bytes: u64,
    #[serde(default, rename = "textBytes")]
    pub text_bytes: u64,
    #[serde(default)]
    pub mounts: u64,
    #[serde(default, rename = "intentArgBytes")]
    pub intent_arg_bytes: u64,
}

/// Which input the client delivers to intents.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct ViewInput {
    #[serde(default)]
    pub keyboard: bool,
    #[serde(default)]
    pub pointer: bool,
    #[serde(default)]
    pub text: bool,
}

/// What the attached client draws.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct ViewAbilities {
    #[serde(default)]
    pub slots: Vec<String>,
    #[serde(default)]
    pub nodes: Vec<String>,
    #[serde(default)]
    pub markdown: bool,
    #[serde(default, rename = "imageFormats")]
    pub image_formats: Vec<String>,
    #[serde(default)]
    pub limits: ViewLimits,
    #[serde(default)]
    pub input: ViewInput,
}

/// One intent the user raised. `context` is the handler's context: `context.is_cancelled()` turns true when the host
/// cancels a cancellable intent (timeout, or the user withdrew it); a cancelled handler's answer is dropped.
pub struct ViewIntentInvocation {
    pub mount: ViewRef,
    pub rev: u64,
    pub node_key: String,
    pub intent: String,
    pub arg: Value,
    pub ticket: String,
    pub context: Context,
}

/// A handler's answer: outcome `"done"` (`answer`), `"refused"` or `"failed"` (`error`).
#[derive(Debug, Clone, PartialEq)]
pub struct ViewIntentResult {
    pub outcome: String,
    pub answer: Option<Value>,
    pub error: String,
}

impl ViewIntentResult {
    pub fn done(answer: Value) -> Self {
        Self { outcome: "done".into(), answer: Some(answer), error: String::new() }
    }

    pub fn refused(error: &str) -> Self {
        Self { outcome: "refused".into(), answer: None, error: error.into() }
    }

    pub fn failed(error: &str) -> Self {
        Self { outcome: "failed".into(), answer: None, error: error.into() }
    }
}

pub(crate) type ViewIntentHandler = Box<dyn Fn(ViewIntentInvocation) -> ViewIntentResult + Send + Sync>;

/// The replicated abilities of one connection.
#[derive(Default)]
pub(crate) struct ViewState {
    abilities: Mutex<Option<ViewAbilities>>,
}

impl ViewState {
    /// Applies a whole state snapshot: an absent `uiAbilities` clears them.
    pub(crate) fn apply_state(&self, state: &Map<String, Value>) {
        let next = state
            .get("uiAbilities")
            .and_then(|v| serde_json::from_value::<ViewAbilities>(v.clone()).ok());
        *self.abilities.lock().unwrap() = next;
    }

    pub(crate) fn abilities(&self) -> Option<ViewAbilities> {
        self.abilities.lock().unwrap().clone()
    }
}

/// Answers a `ui.view.intent` request.
pub(crate) fn handle_intent(handler: Option<&ViewIntentHandler>, context: Context, args: Option<&Value>) -> Value {
    let Some(handler) = handler else {
        return json!({"outcome": "refused", "error": "no intent handler"});
    };
    let args = args.cloned().unwrap_or(Value::Null);
    let mount = args
        .get("mount")
        .and_then(|m| serde_json::from_value::<ViewRef>(m.clone()).ok())
        .unwrap_or(ViewRef { key: String::new(), epoch: 0, rev: 0 });
    let text = |name: &str| args.get(name).and_then(Value::as_str).unwrap_or("").to_string();
    let res = handler(ViewIntentInvocation {
        mount,
        rev: args.get("rev").and_then(Value::as_u64).unwrap_or(0),
        node_key: text("nodeKey"),
        intent: text("intent"),
        arg: args.get("arg").cloned().unwrap_or(Value::Null),
        ticket: text("ticket"),
        context,
    });
    let mut out = Map::new();
    out.insert("outcome".into(), Value::String(if res.outcome.is_empty() { "done".into() } else { res.outcome }));
    if let Some(answer) = res.answer {
        out.insert("answer".into(), answer);
    }
    if !res.error.is_empty() {
        out.insert("error".into(), Value::String(res.error));
    }
    Value::Object(out)
}

fn transport(err: impl std::fmt::Display) -> ViewError {
    ViewError { code: "transport".into(), message: err.to_string() }
}

impl Context {
    /// What the attached client draws, or `None` while none is attached.
    pub fn view_abilities(&self) -> Option<ViewAbilities> {
        self.conn.view_state.abilities()
    }

    fn view_content(
        &self,
        view: &crate::kit::View,
        intents: &[ViewIntent],
        assets: &[ViewAsset],
        fallback: Option<&crate::kit::Node>,
    ) -> Result<Map<String, Value>, ViewError> {
        let encoded = self.conn.views.encode(view).map_err(|e| ViewError { code: "invalid".into(), message: e })?;
        let mut out = Map::new();
        out.insert("view".into(), encoded.inline_wire());
        if !intents.is_empty() {
            out.insert("intents".into(), serde_json::to_value(intents).map_err(transport)?);
        }
        if !assets.is_empty() {
            out.insert("assets".into(), serde_json::to_value(assets).map_err(transport)?);
        }
        if let Some(node) = fallback {
            let enc = self
                .conn
                .views
                .encode(&crate::kit::View::new(node.clone()))
                .map_err(|e| ViewError { code: "invalid".into(), message: e })?;
            if let Some(root) = enc.body.get("root") {
                out.insert("fallback".into(), root.clone());
            }
        }
        Ok(out)
    }

    fn view_call(&self, method: &str, args: Value) -> Result<ViewStatus, ViewError> {
        let reply = self.call_wire(method, Some(args)).map_err(transport)?;
        if let Some(err) = reply.error {
            return Err(ViewError { code: err.code.unwrap_or_default(), message: err.message });
        }
        let result = reply.result.ok_or_else(|| transport("missing reply"))?;
        serde_json::from_value(result).map_err(transport)
    }

    /// Mounts a view in a slot the attached client draws. The key names the mount among this extension's.
    pub fn mount_view(&self, mount: ViewMount) -> Result<ViewStatus, ViewError> {
        let mut args = self.view_content(&mount.view, &mount.intents, &mount.assets, mount.fallback.as_ref())?;
        args.insert("key".into(), Value::String(mount.key));
        args.insert("slot".into(), Value::String(mount.slot));
        if let Some(target) = mount.target {
            args.insert("target".into(), serde_json::to_value(target).map_err(transport)?);
        }
        self.view_call("ui.view.mount", Value::Object(args))
    }

    /// Replaces the content of a mount. Fails with code `"stale"` unless `mount` carries the epoch and rev the last
    /// operation returned.
    pub fn update_view(&self, update: ViewUpdate) -> Result<ViewStatus, ViewError> {
        let mut args = self.view_content(&update.view, &update.intents, &update.assets, update.fallback.as_ref())?;
        args.insert("mount".into(), serde_json::to_value(update.mount).map_err(transport)?);
        self.view_call("ui.view.update", Value::Object(args))
    }

    fn view_transition(&self, op: &str, mount: &ViewRef) -> Result<ViewStatus, ViewError> {
        self.view_call(&format!("ui.view.{op}"), json!({"mount": mount}))
    }

    /// Hides a live mount.
    pub fn hide_view(&self, mount: &ViewRef) -> Result<ViewStatus, ViewError> {
        self.view_transition("hide", mount)
    }

    /// Shows a hidden mount.
    pub fn show_view(&self, mount: &ViewRef) -> Result<ViewStatus, ViewError> {
        self.view_transition("show", mount)
    }

    /// Ends a mount as completed. Terminal: a later operation on the mount fails with code `"terminal"`.
    pub fn finish_view(&self, mount: &ViewRef) -> Result<ViewStatus, ViewError> {
        self.view_transition("finish", mount)
    }

    /// Ends a mount as withdrawn. Terminal.
    pub fn remove_view(&self, mount: &ViewRef) -> Result<ViewStatus, ViewError> {
        self.view_transition("remove", mount)
    }

    /// Reports the mount under `key`.
    pub fn view_state(&self, key: &str) -> Result<ViewStatus, ViewError> {
        self.view_call("ui.view.state", json!({"key": key}))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn intent_wire_names() {
        let int = ViewShape::new("integer");
        let intent = ViewIntent {
            name: "x.go".into(),
            node_key: "btn".into(),
            arg: ViewShape::object(&[("n", int.clone())], &["n"]),
            answer: Some(ViewShape { kind: "array".into(), items: Some(Box::new(ViewShape::new("string"))), max_items: 3, ..ViewShape::default() }),
            cancellable: true,
            timeout_ms: 500,
        };
        assert_eq!(
            serde_json::to_value(&intent).unwrap(),
            json!({
                "name": "x.go",
                "nodeKey": "btn",
                "arg": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]},
                "answer": {"type": "array", "items": {"type": "string"}, "maxItems": 3},
                "cancellable": true,
                "timeoutMs": 500
            })
        );
        assert_eq!(serde_json::to_value(ViewIntent { name: "y.go".into(), arg: int, ..Default::default() }).unwrap(), json!({"name": "y.go", "arg": {"type": "integer"}}));
    }

    #[test]
    fn abilities_decode_and_absence_clears() {
        let state = ViewState::default();
        let snapshot = json!({"uiAbilities": {"slots": ["panel"], "nodes": ["text"], "markdown": true, "imageFormats": ["png"], "limits": {"mounts": 7, "intentArgBytes": 123}, "input": {"keyboard": true}}});
        state.apply_state(snapshot.as_object().unwrap());
        let ab = state.abilities().unwrap();
        assert_eq!((ab.slots, ab.limits.mounts, ab.limits.intent_arg_bytes, ab.input.keyboard, ab.input.pointer, ab.markdown), (vec!["panel".to_string()], 7, 123, true, false, true));
        state.apply_state(&Map::new());
        assert!(state.abilities().is_none());
    }

    #[test]
    fn status_decodes_a_broken_reason() {
        let st: ViewStatus = serde_json::from_value(json!({"mount": {"key": "k", "epoch": 3, "rev": 2}, "state": "broken", "reason": "owner_gone"})).unwrap();
        assert_eq!((st.mount.epoch, st.mount.rev, st.state.as_str(), st.reason.as_str()), (3, 2, "broken", "owner_gone"));
    }
}

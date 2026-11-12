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
    /// The JSON value a finished mount was given by [`Context::finish_view_with`].
    #[serde(default)]
    pub result: Option<Value>,
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
    /// At most 8; a client is sent the first whose `when` holds for its claims.
    pub alternatives: Vec<ViewAlternative>,
    pub drawing: Option<ViewDrawing>,
    /// At most 256 entries naming view nodes by id.
    pub a11y: Vec<ViewA11y>,
    /// At most 256 entries; at most one requests the focus.
    pub focus: Vec<ViewFocus>,
}

/// The argument of [`Context::update_view`]: a full replacement of the content of the revision `mount` names.
pub struct ViewUpdate {
    pub mount: ViewRef,
    pub view: crate::kit::View,
    pub intents: Vec<ViewIntent>,
    pub assets: Vec<ViewAsset>,
    pub fallback: Option<crate::kit::Node>,
    /// At most 8; a client is sent the first whose `when` holds for its claims.
    pub alternatives: Vec<ViewAlternative>,
    pub drawing: Option<ViewDrawing>,
    /// At most 256 entries naming view nodes by id.
    pub a11y: Vec<ViewA11y>,
    /// At most 256 entries; at most one requests the focus.
    pub focus: Vec<ViewFocus>,
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
    /// Further facts the client states; with several clients attached, the claims all of them share.
    #[serde(default)]
    pub claims: Vec<ViewClaim>,
    #[serde(default, rename = "slotClaims")]
    pub slot_claims: Vec<ViewSlotClaims>,
    /// `None` when the client draws no drawing.
    #[serde(default)]
    pub drawing: Option<ViewDrawingAbilities>,
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

/// One fact the attached client states about itself, named `<area>.<fact>`. `value` is a boolean, a non-negative
/// integer, a string or an array of strings.
#[derive(Debug, Clone, PartialEq, Eq, Deserialize)]
pub struct ViewClaim {
    pub name: String,
    pub value: Value,
}

/// The claims that replace the same-named ones for mounts of one slot.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct ViewSlotClaims {
    #[serde(default)]
    pub slot: String,
    #[serde(default)]
    pub claims: Vec<ViewClaim>,
}

/// The drawing profiles (`scene`, `pixels`) the client draws, and how many shapes and pixels.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct ViewDrawingAbilities {
    #[serde(default)]
    pub profiles: Vec<String>,
    #[serde(default)]
    pub shapes: u64,
    #[serde(default)]
    pub pixels: u64,
}

/// Tests one claim: `op` is `present`, `equals` (a boolean, integer or string `value`), `contains` (`value` an array
/// of strings the claimed list must hold) or `atLeast` (an integer `value`).
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct ViewClause {
    pub claim: String,
    pub op: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub value: Option<Value>,
}

/// Holds when all its clauses hold.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct ViewWhenAll {
    pub all: Vec<ViewClause>,
}

/// Holds when any of its groups holds. At most 8 groups of 8 clauses.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct ViewWhen {
    pub any: Vec<ViewWhenAll>,
}

/// A view a client is sent in place of the mount's view when `when` holds for that client's claims. The first
/// alternative that holds wins; a mount carries at most 8.
pub struct ViewAlternative {
    pub when: ViewWhen,
    pub view: crate::kit::View,
    pub drawing: Option<ViewDrawing>,
}

fn zero(n: &f64) -> bool {
    *n == 0.0
}

/// The rectangle of drawing coordinates the client fits (`contain`, `cover`, `stretch`) into its space.
#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub struct ViewViewport {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
    pub fit: String,
}

/// Outlines a shape.
#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub struct ViewStroke {
    pub color: String,
    pub width: f64,
}

/// One vertex of a polyline.
#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub struct ViewPoint {
    pub x: f64,
    pub y: f64,
}

/// One step of a path: `op` is `move`, `line`, `quad` (`x1`, `y1`), `cubic` (`x1`, `y1`, `x2`, `y2`) or `close`.
#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub struct ViewPathCommand {
    pub op: String,
    #[serde(skip_serializing_if = "zero")]
    pub x: f64,
    #[serde(skip_serializing_if = "zero")]
    pub y: f64,
    #[serde(skip_serializing_if = "zero")]
    pub x1: f64,
    #[serde(skip_serializing_if = "zero")]
    pub y1: f64,
    #[serde(skip_serializing_if = "zero")]
    pub x2: f64,
    #[serde(skip_serializing_if = "zero")]
    pub y2: f64,
}

/// One shape of a scene. `kind` is `group` (`children`), `rect` (`x`, `y`, `width`, `height`, `radius`), `ellipse`
/// (`cx`, `cy`, `rx`, `ry`), `line` (`x1`, `y1`, `x2`, `y2`), `polyline` (`points`, `closed`), `path` (`commands`),
/// `text` (`x`, `y`, `text`, `size`, `align`) or `image` (`x`, `y`, `width`, `height`, `asset`). A member of another
/// kind rejects the drawing. A color is `#rrggbb`, `#rrggbbaa` or a theme color token. At most 4096 shapes nested 8
/// deep, 1024 points or commands in one.
#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub struct ViewDrawShape {
    pub kind: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub key: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub fill: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stroke: Option<ViewStroke>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub opacity: Option<f64>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub transform: Vec<f64>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub children: Vec<ViewDrawShape>,
    #[serde(skip_serializing_if = "zero")]
    pub x: f64,
    #[serde(skip_serializing_if = "zero")]
    pub y: f64,
    #[serde(skip_serializing_if = "zero")]
    pub width: f64,
    #[serde(skip_serializing_if = "zero")]
    pub height: f64,
    #[serde(skip_serializing_if = "zero")]
    pub radius: f64,
    #[serde(skip_serializing_if = "zero")]
    pub cx: f64,
    #[serde(skip_serializing_if = "zero")]
    pub cy: f64,
    #[serde(skip_serializing_if = "zero")]
    pub rx: f64,
    #[serde(skip_serializing_if = "zero")]
    pub ry: f64,
    #[serde(skip_serializing_if = "zero")]
    pub x1: f64,
    #[serde(skip_serializing_if = "zero")]
    pub y1: f64,
    #[serde(skip_serializing_if = "zero")]
    pub x2: f64,
    #[serde(skip_serializing_if = "zero")]
    pub y2: f64,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub points: Vec<ViewPoint>,
    #[serde(skip_serializing_if = "std::ops::Not::not")]
    pub closed: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub commands: Vec<ViewPathCommand>,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub text: String,
    #[serde(skip_serializing_if = "zero")]
    pub size: f64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub align: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub asset: String,
}

/// A pixel grid over an image asset; `scaling` is `nearest` or `smooth`.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct ViewPixels {
    pub width: u64,
    pub height: u64,
    pub asset: String,
    pub scaling: String,
}

/// Turns a gesture (`activate`, `press`, `release`, `drag`) inside `shape` (a rect, an ellipse or a closed polyline)
/// into the declared intent `intent`. The client hit-tests. At most 64 regions.
#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub struct ViewRegion {
    pub key: String,
    pub shape: ViewDrawShape,
    pub gestures: Vec<String>,
    pub intent: String,
    pub name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub role: String,
}

/// The drawing of a view: `profile` `scene` with `shapes`, or `pixels` with `pixels`. `node` names the view node the
/// drawing fills. A client that does not draw the profile makes the mount fail with code `unsupported`.
#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub struct ViewDrawing {
    pub profile: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub node: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub viewport: Option<ViewViewport>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub shapes: Vec<ViewDrawShape>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pixels: Option<ViewPixels>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub regions: Vec<ViewRegion>,
}

/// Ties a node to another: `kind` is `labelledBy`, `describedBy`, `controls` or `owns`.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct ViewRelation {
    pub kind: String,
    pub target: String,
}

/// Describes the view node with id `node` to assistive technology. At most 256 entries, 16 relations each.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct ViewA11y {
    pub node: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub description: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub role: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub selected: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub checked: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub expanded: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub busy: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub disabled: Option<bool>,
    #[serde(rename = "readOnly", skip_serializing_if = "Option::is_none")]
    pub read_only: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub required: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub invalid: Option<bool>,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub relations: Vec<ViewRelation>,
}

/// Makes the node with id `node` focusable; the order of the entries is the focus order. `request` `mount` asks for
/// the focus when the mount is first drawn (at most one entry may); `restore` is `previous` or `self`.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize)]
pub struct ViewFocus {
    pub node: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub request: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub restore: String,
}

/// One diagnostic of a mount: an operation the host rejected (`invalid`, `unsupported`, `limit`, `stale`,
/// `terminal`), an intent it ended (`refused`, `failed`, `timeout`, `stale`) or a part a client could not draw
/// (`undrawn`). `count` is how often it happened.
#[derive(Debug, Clone, Default, PartialEq, Eq, Deserialize)]
pub struct ViewNotice {
    pub code: String,
    pub severity: String,
    #[serde(default)]
    pub message: String,
    #[serde(default)]
    pub path: String,
    #[serde(default)]
    pub claim: String,
    #[serde(default)]
    pub intent: String,
    #[serde(default)]
    pub measured: Option<i64>,
    #[serde(default)]
    pub limit: Option<i64>,
    #[serde(default)]
    pub epoch: u64,
    #[serde(default)]
    pub rev: u64,
    #[serde(default)]
    pub count: u64,
}

/// The content of a mount or an update, borrowed from either.
struct ViewParts<'a> {
    view: &'a crate::kit::View,
    intents: &'a [ViewIntent],
    assets: &'a [ViewAsset],
    fallback: Option<&'a crate::kit::Node>,
    alternatives: &'a [ViewAlternative],
    drawing: Option<&'a ViewDrawing>,
    a11y: &'a [ViewA11y],
    focus: &'a [ViewFocus],
}

impl Context {
    /// What the attached client draws, or `None` while none is attached.
    pub fn view_abilities(&self) -> Option<ViewAbilities> {
        self.conn.view_state.abilities()
    }

    fn view_wire(&self, view: &crate::kit::View) -> Result<Value, ViewError> {
        Ok(self.conn.views.encode(view).map_err(|e| ViewError { code: "invalid".into(), message: e })?.inline_wire())
    }

    fn view_content(&self, parts: ViewParts<'_>) -> Result<Map<String, Value>, ViewError> {
        let ViewParts { view, intents, assets, fallback, alternatives, drawing, a11y, focus } = parts;
        let mut out = Map::new();
        out.insert("view".into(), self.view_wire(view)?);
        if !alternatives.is_empty() {
            let mut alts = Vec::with_capacity(alternatives.len());
            for alt in alternatives {
                let mut wire = Map::new();
                wire.insert("when".into(), serde_json::to_value(&alt.when).map_err(transport)?);
                wire.insert("view".into(), self.view_wire(&alt.view)?);
                if let Some(drawing) = &alt.drawing {
                    wire.insert("drawing".into(), serde_json::to_value(drawing).map_err(transport)?);
                }
                alts.push(Value::Object(wire));
            }
            out.insert("alternatives".into(), Value::Array(alts));
        }
        if let Some(drawing) = drawing {
            out.insert("drawing".into(), serde_json::to_value(drawing).map_err(transport)?);
        }
        if !a11y.is_empty() {
            out.insert("a11y".into(), serde_json::to_value(a11y).map_err(transport)?);
        }
        if !focus.is_empty() {
            out.insert("focus".into(), serde_json::to_value(focus).map_err(transport)?);
        }
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
        let mut args = self.view_content(ViewParts {
            view: &mount.view,
            intents: &mount.intents,
            assets: &mount.assets,
            fallback: mount.fallback.as_ref(),
            alternatives: &mount.alternatives,
            drawing: mount.drawing.as_ref(),
            a11y: &mount.a11y,
            focus: &mount.focus,
        })?;
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
        let mut args = self.view_content(ViewParts {
            view: &update.view,
            intents: &update.intents,
            assets: &update.assets,
            fallback: update.fallback.as_ref(),
            alternatives: &update.alternatives,
            drawing: update.drawing.as_ref(),
            a11y: &update.a11y,
            focus: &update.focus,
        })?;
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

    /// Ends a mount as completed and leaves `result`, any JSON value of at most 65536 encoded bytes, as the
    /// extension's last word on it. The clients' snapshots and [`Context::view_state`] carry it.
    pub fn finish_view_with(&self, mount: &ViewRef, result: Value) -> Result<ViewStatus, ViewError> {
        self.view_call("ui.view.finish", json!({"mount": mount, "result": result}))
    }

    /// The notices of the mount under `key`, oldest first: what the host rejected or ended and what a client could
    /// not draw. The host keeps at most 32 and counts repeats.
    pub fn view_diagnostics(&self, key: &str) -> Result<Vec<ViewNotice>, ViewError> {
        let reply = self.call_wire("ui.view.diagnostics", Some(json!({"key": key}))).map_err(transport)?;
        if let Some(err) = reply.error {
            return Err(ViewError { code: err.code.unwrap_or_default(), message: err.message });
        }
        let result = reply.result.ok_or_else(|| transport("missing reply"))?;
        let notices = result.get("notices").cloned().ok_or_else(|| transport("reply has no notices"))?;
        serde_json::from_value(notices).map_err(transport)
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
        assert_eq!(st.result, None);
        let done: ViewStatus = serde_json::from_value(json!({"mount": {"key": "k", "epoch": 3, "rev": 2}, "state": "finished", "result": {"picked": 77}})).unwrap();
        assert_eq!(done.result, Some(json!({"picked": 77})));
    }

    #[test]
    fn amendment_records_use_the_wire_names() {
        let shape = ViewDrawShape { kind: "rect".into(), width: 2.0, height: 1.0, ..Default::default() };
        let drawing = ViewDrawing {
            profile: "scene".into(),
            shapes: vec![shape.clone()],
            regions: vec![ViewRegion { key: "r".into(), shape, gestures: vec!["activate".into()], intent: "x.go".into(), name: "Go".into(), ..Default::default() }],
            ..Default::default()
        };
        assert_eq!(
            serde_json::to_value(&drawing).unwrap(),
            json!({"profile": "scene", "shapes": [{"kind": "rect", "width": 2.0, "height": 1.0}],
                "regions": [{"key": "r", "shape": {"kind": "rect", "width": 2.0, "height": 1.0}, "gestures": ["activate"], "intent": "x.go", "name": "Go"}]})
        );
        let when = ViewWhen { any: vec![ViewWhenAll { all: vec![ViewClause { claim: "display.colors".into(), op: "atLeast".into(), value: Some(json!(256)) }, ViewClause { claim: "display.hover".into(), op: "present".into(), value: None }] }] };
        assert_eq!(serde_json::to_value(&when).unwrap(), json!({"any": [{"all": [{"claim": "display.colors", "op": "atLeast", "value": 256}, {"claim": "display.hover", "op": "present"}]}]}));
        let a11y = ViewA11y { node: "n".into(), read_only: Some(true), relations: vec![ViewRelation { kind: "owns".into(), target: "m".into() }], ..Default::default() };
        assert_eq!(serde_json::to_value(&a11y).unwrap(), json!({"node": "n", "readOnly": true, "relations": [{"kind": "owns", "target": "m"}]}));
        assert_eq!(serde_json::to_value(ViewFocus { node: "n".into(), request: "mount".into(), ..Default::default() }).unwrap(), json!({"node": "n", "request": "mount"}));
        let state = ViewState::default();
        let snapshot = json!({"uiAbilities": {"claims": [{"name": "display.colors", "value": 4242}], "slotClaims": [{"slot": "panel", "claims": [{"name": "display.colors", "value": 77}]}], "drawing": {"profiles": ["scene"], "shapes": 5, "pixels": 16}}});
        state.apply_state(snapshot.as_object().unwrap());
        let ab = state.abilities().unwrap();
        assert_eq!((ab.claims[0].value.as_i64(), ab.slot_claims[0].claims[0].value.as_i64(), ab.drawing.unwrap().shapes), (Some(4242), Some(77), 5));
        let notices: Vec<ViewNotice> = serde_json::from_value(json!([{"code": "limit", "severity": "error", "path": "update", "measured": 6, "limit": 5, "epoch": 2, "rev": 1, "count": 3}])).unwrap();
        assert_eq!((notices[0].code.as_str(), notices[0].measured, notices[0].limit, notices[0].count), ("limit", Some(6), Some(5), 3));
    }
}

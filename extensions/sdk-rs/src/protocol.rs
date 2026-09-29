//! Wire protocol types matching the Go host's `subprocess/protocol.go`.
//! 4-byte big-endian length prefix + JSON payload.

use crate::JsString;
use crate::transport::UnixStream;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashMap;
use std::io::{self, Read, Write};
use std::sync::{Arc, Mutex};
use std::sync::mpsc;
use std::sync::atomic::{AtomicBool, AtomicU8, Ordering};

#[cfg(test)]
#[path = "surrogate_tests.rs"]
mod surrogate_tests;

#[cfg(all(test, unix))]
#[path = "request_lifetime_tests.rs"]
mod request_lifetime_tests;

/// Maximum frame size (128 MB). Bounds a single length-prefixed frame to guard
/// against unbounded allocation while allowing large host responses such as
/// getBranch on a long session. Must match the host and other-language SDK
/// MaxFrameSize constants.
pub const MAX_FRAME_SIZE: u32 = 128 * 1024 * 1024;

fn is_zero(value: &u32) -> bool {
    *value == 0
}

/// JSON Schema type alias.
pub type Schema = Value;

/// Helper for creating empty schemas.
pub fn empty_schema() -> Value {
    serde_json::json!({
        "type": "object",
        "properties": {},
    })
}

// ─── Wire message types ──────────────────────────────────────────────────────

#[derive(Debug, Serialize, Deserialize, Default)]
pub struct Envelope<R = Value> {
    #[serde(rename = "type")]
    pub msg_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub register: Option<RegisterMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ready: Option<ReadyMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub request: Option<RequestMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub response: Option<ResponseMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub notify: Option<NotifyMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cancel: Option<CancelMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub call: Option<CallMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub call_result: Option<CallResultMsg<R>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub widget_push: Option<WidgetPushMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub shutdown: Option<ShutdownMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ping: Option<PingMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pong: Option<PongMsg>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub request_state: Option<RequestStateMsg>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct RegisterMsg {
    pub name: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub tools: Vec<ToolDef>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub commands: Vec<CmdDef>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub shortcuts: Vec<ShortcutDef>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub handlers: Vec<HandlerDef>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub flags: Vec<FlagDef>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub providers: Vec<ProviderDef>,
    #[serde(
        default,
        skip_serializing_if = "Vec::is_empty",
        rename = "message_renderers"
    )]
    pub renderers: Vec<RendererDef>,
    #[serde(
        default,
        skip_serializing_if = "Vec::is_empty",
        rename = "entry_renderers"
    )]
    pub entry_renderers: Vec<RendererDef>,
    /// A registered Markdown transformer; the host runs it with
    /// `markdown_transform` requests.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub markdown_transformer: bool,
}

#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct ToolDef {
    pub name: String,
    /// Upstream ToolDefinition.label: the tool's display name.
    #[serde(skip_serializing_if = "String::is_empty", default)]
    pub label: String,
    pub description: String,
    pub parameters: Value,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub constrained_sampling: Option<crate::ToolConstrainedSampling>,
    #[serde(skip_serializing_if = "Vec::is_empty", default)]
    pub prompt_guidelines: Vec<String>,
    /// Upstream ToolDefinition.promptSnippet.
    #[serde(skip_serializing_if = "String::is_empty", default)]
    pub prompt_snippet: String,
    /// Upstream ToolDefinition.executionMode: "sequential" or "parallel".
    #[serde(skip_serializing_if = "String::is_empty", default)]
    pub execution_mode: String,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub source: Option<String>,
    /// "self" when the tool's renderers draw their own framing.
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub render_shell: Option<String>,
    /// The tool has a call renderer.
    #[serde(skip_serializing_if = "std::ops::Not::not", default)]
    pub renders_call: bool,
    /// The tool has a result renderer.
    #[serde(skip_serializing_if = "std::ops::Not::not", default)]
    pub renders_result: bool,
}

/// Provider-side constrained sampling request for a tool. `type` is "json_schema"
/// or "grammar"; for json_schema `strict` is "prefer" or "require"; for grammar
/// `variants` maps a grammar format ("openai_lark", "openai_regex") to its
/// definition. Mirrors upstream ConstrainedSamplingConfig.
#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ConstrainedSampling {
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub strict: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub variants: Option<std::collections::BTreeMap<String, String>>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct HandlerDef {
    pub event: String,
    pub can_block: bool,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub handler_id: u32,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CmdDef {
    pub name: String,
    pub description: String,
    /// The command defines upstream getArgumentCompletions; the host asks for
    /// it with the command_argument_completions request.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub argument_completions: bool,
}

/// Upstream pi-tui `AutocompleteItem`: `value` is inserted, `label` is shown
/// in its place when set.
#[derive(Debug, Serialize, Deserialize, Clone, PartialEq, Default)]
pub struct AutocompleteItem {
    pub value: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub label: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ShortcutDef {
    pub key: String,
    pub description: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct FlagDef {
    pub name: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub description: String,
    #[serde(rename = "type")]
    pub flag_type: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub default: Option<Value>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ProviderDef {
    pub name: String,
    pub config: Value,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub native: Option<Value>,
    #[serde(default)]
    pub stream_simple: bool,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct RendererDef {
    #[serde(rename = "custom_type")]
    pub custom_type: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ReadyMsg {
    #[serde(default)]
    pub session_name: String,
    #[serde(default)]
    pub cwd: String,
    #[serde(default)]
    pub mode: String,
    #[serde(default)]
    pub width: u32,
    #[serde(default)]
    pub height: u32,
    #[serde(default)]
    pub model: String,
    /// Initial state snapshot, including session entries.
    #[serde(default, deserialize_with = "deserialize_state_replica")]
    pub state: Option<serde_json::Value>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
#[serde(try_from = "RawRequestMsg", into = "RawRequestMsg")]
pub struct RequestMsg {
    pub method: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub event: Option<String>,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub handler_id: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub tool_call_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub args: Option<Value>,
    pub terminal_input: Option<TerminalInputArgs>,
}

/// The ordered chunk and owner snapshot use JS strings, not Unicode-scalar strings.
#[derive(Debug, Serialize, Deserialize, Clone, Default)]
#[serde(rename_all = "camelCase")]
pub struct TerminalInputArgs {
    pub data: JsString,
    #[serde(default)]
    pub editor_text: JsString,
    #[serde(default)]
    pub tools_expanded: bool,
}

#[derive(Serialize, Deserialize)]
struct RawRequestMsg {
    method: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    tool: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    event: Option<String>,
    #[serde(default, skip_serializing_if = "is_zero")]
    handler_id: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    tool_call_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    args: Option<Box<serde_json::value::RawValue>>,
}

impl TryFrom<RawRequestMsg> for RequestMsg {
    type Error = serde_json::Error;
    fn try_from(raw: RawRequestMsg) -> Result<Self, Self::Error> {
        let mut terminal_input = None;
        let args = if raw.method == "terminal_input" {
            terminal_input = raw.args.as_ref().map(|a| serde_json::from_str(a.get())).transpose()?;
            None
        } else {
            raw.args.as_ref().map(|a| serde_json::from_str(a.get())).transpose()?
        };
        Ok(Self { method: raw.method, tool: raw.tool, event: raw.event, handler_id: raw.handler_id, tool_call_id: raw.tool_call_id, args, terminal_input })
    }
}

impl From<RequestMsg> for RawRequestMsg {
    fn from(req: RequestMsg) -> Self {
        let args = match req.terminal_input {
            Some(args) => Some(serde_json::value::to_raw_value(&args).expect("terminal input is JSON")),
            None => req.args.map(|args| serde_json::value::to_raw_value(&args).expect("Value is JSON")),
        };
        Self { method: req.method, tool: req.tool, event: req.event, handler_id: req.handler_id, tool_call_id: req.tool_call_id, args }
    }
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ResponseMsg {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub result: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<ErrorInfo>,
}

#[derive(Debug, Deserialize, Clone)]
#[serde(try_from = "RawNotifyMsg")]
pub struct NotifyMsg {
    #[serde(default)]
    pub method: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub args: Option<Value>,
    #[serde(skip)]
    pub custom_input: Option<CustomInputArgs>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CustomInputArgs {
    pub key: String,
    pub data: JsString,
}

// Rust queries editor text through the typed host call; it does not consume that
// replica field. Decode it as a JS string before projecting the consumed fields.
fn deserialize_state_replica<'de, D: serde::Deserializer<'de>>(deserializer: D) -> Result<Option<Value>, D::Error> {
    #[derive(Deserialize)]
    struct State {
        #[serde(default, rename = "editorText")]
        _editor_text: Option<JsString>,
        #[serde(flatten)]
        fields: serde_json::Map<String, Value>,
    }
    Ok(Option::<State>::deserialize(deserializer)?.map(|s| Value::Object(s.fields)))
}

#[derive(Serialize, Deserialize)]
struct RawNotifyMsg {
    #[serde(default)]
    method: String,
    args: Option<Box<serde_json::value::RawValue>>,
}
impl TryFrom<RawNotifyMsg> for NotifyMsg {
    type Error = serde_json::Error;
    fn try_from(raw: RawNotifyMsg) -> Result<Self, Self::Error> {
        if raw.method == "ui.custom.input" {
            let custom_input = raw.args.as_ref().map(|args| serde_json::from_str(args.get())).transpose()?;
            return Ok(Self { method: raw.method, args: None, custom_input });
        }
        let args = raw.args.map(|args| {
            if raw.method == "state_update" {
                #[derive(Deserialize, Serialize)]
                struct Update {
                    #[serde(default, deserialize_with = "deserialize_state_replica")]
                    state: Option<Value>,
                }
                let update: Update = serde_json::from_str(args.get())?;
                serde_json::to_value(update)
            } else { serde_json::from_str(args.get()) }
        }).transpose()?;
        Ok(Self { method: raw.method, args, custom_input: None })
    }
}

impl Serialize for NotifyMsg {
    fn serialize<S: serde::Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        use serde::ser::SerializeStruct;
        let mut object = serializer.serialize_struct("NotifyMsg", 2)?;
        object.serialize_field("method", &self.method)?;
        if let Some(args) = &self.custom_input {
            object.serialize_field("args", args)?;
        } else if let Some(args) = &self.args {
            object.serialize_field("args", args)?;
        }
        object.end()
    }
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CancelMsg {
    #[serde(default)]
    pub request_id: String,
    #[serde(default)]
    pub reason: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct CallMsg {
    pub method: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub args: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub parent_request_id: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
#[serde(bound(deserialize = "R: Deserialize<'de>"))]
pub struct CallResultMsg<R = Value> {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub result: Option<R>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<ErrorInfo>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct WidgetPushMsg {
    pub key: String,
    pub lines: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct PingMsg {
    pub nonce: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct PongMsg {
    pub nonce: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct RequestStateMsg {
    pub request_id: String,
    pub state: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reason: Option<String>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ShutdownMsg {
    pub reason: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ErrorInfo {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub code: Option<String>,
    pub message: String,
}

// ─── Connection ──────────────────────────────────────────────────────────────

// pig additive (D19): normal response publication retires only the request parent, not a retained Context's connection.
pub(crate) struct RequestParent {
    id: String,
    state: AtomicU8,
    finished: AtomicBool,
}

impl RequestParent {
    pub(crate) fn completed(&self) -> bool { self.state.load(Ordering::Acquire) == 1 }
    pub(crate) fn cancelled(&self) -> bool { self.state.load(Ordering::Acquire) == 2 }
}

/// A thread-safe connection to the host over a Unix socket.
pub struct Connection {
    pub(crate) registered_tools: Mutex<HashMap<String, Arc<crate::extension::ToolDefinition>>>,
    pub(crate) tool_registration_lock: Mutex<()>,
    pub(crate) autocomplete: crate::autocomplete::AutocompleteRegistry,
    pub(crate) provider_objects: std::sync::OnceLock<Arc<crate::provider::ProviderObjects>>,
    reader: Mutex<UnixStream>,
    writer: Mutex<UnixStream>,
    // control ends the socket for every clone without waiting on a reader or writer lock.
    control: UnixStream,
    call_id: std::sync::atomic::AtomicU64,
    pending_calls: Mutex<HashMap<String, PendingCallSender>>,
    request_parents: Mutex<HashMap<String, Arc<RequestParent>>>,
    closed: AtomicBool,
    pub(crate) closed_signal: crate::provider::ProviderSignal,
}

struct PendingCallSender {
    parent_request_id: Option<String>,
    sender: mpsc::Sender<CallResultMsg<Box<serde_json::value::RawValue>>>,
}

pub(crate) struct PendingCall {
    method: String,
    receiver: mpsc::Receiver<CallResultMsg<Box<serde_json::value::RawValue>>>,
}

impl Connection {
    pub fn new(stream: UnixStream) -> Self {
        let writer = stream
            .try_clone()
            .expect("clone UnixStream for extension protocol writer");
        let control = stream
            .try_clone()
            .expect("clone UnixStream for extension protocol control");
        Self {
            reader: Mutex::new(stream),
            writer: Mutex::new(writer),
            control,
            call_id: std::sync::atomic::AtomicU64::new(0),
            pending_calls: Mutex::new(HashMap::new()),
            request_parents: Mutex::new(HashMap::new()),
            closed: AtomicBool::new(false),
            closed_signal: crate::provider::ProviderSignal::new(),
            provider_objects: std::sync::OnceLock::new(),
            registered_tools: Mutex::new(HashMap::new()),
            tool_registration_lock: Mutex::new(()),
            autocomplete: crate::autocomplete::AutocompleteRegistry::default(),
        }
    }

    pub(crate) fn arm_parent(&self, id: &str) -> Arc<RequestParent> {
        let parent = Arc::new(RequestParent { id: id.to_owned(), state: AtomicU8::new(0), finished: AtomicBool::new(false) });
        self.request_parents.lock().unwrap().insert(id.to_owned(), parent.clone());
        parent
    }

    pub(crate) fn is_closed(&self) -> bool { self.closed.load(Ordering::Acquire) }

    /// Ends the socket when the serve loop returns, as Go's RunWithConn closes its connection. A Context the extension keeps past its generation still holds this Connection, and the host's Retire waits for the close while the process lives on for later factory generations.
    pub(crate) fn close_transport(&self) {
        self.closed.store(true, Ordering::Release);
        let _ = self.control.shutdown(std::net::Shutdown::Both);
    }

    /// Read one frame from the socket (4-byte length prefix + JSON).
    pub fn read_envelope(&self) -> io::Result<Envelope<Box<serde_json::value::RawValue>>> {
        let mut stream = self.reader.lock().unwrap();
        let mut hdr = [0u8; 4];
        stream.read_exact(&mut hdr)?;
        let size = u32::from_be_bytes(hdr);
        if size > MAX_FRAME_SIZE {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                format!("frame too large: {} bytes", size),
            ));
        }
        let mut buf = vec![0u8; size as usize];
        stream.read_exact(&mut buf)?;
        serde_json::from_slice(&buf).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
    }

    /// Write one frame to the socket.
    pub fn write_envelope(&self, env: &Envelope) -> io::Result<()> {
        Self::write_envelope_to(&mut self.writer.lock().unwrap(), env)
    }

    fn write_envelope_to(stream: &mut UnixStream, env: &impl Serialize) -> io::Result<()> {
        let data =
            serde_json::to_vec(env).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
        if data.len() > MAX_FRAME_SIZE as usize {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                format!(
                    "frame too large: {} bytes exceeds {}",
                    data.len(),
                    MAX_FRAME_SIZE
                ),
            ));
        }
        let hdr = (data.len() as u32).to_be_bytes();
        stream.write_all(&hdr)?;
        stream.write_all(&data)?;
        stream.flush()
    }

    /// Send a response to a host request.
    pub fn respond(
        &self,
        id: &str,
        result: Option<Value>,
        error: Option<ErrorInfo>,
    ) -> io::Result<()> {
        self.respond_serialized(id, result, error)
    }

    pub(crate) fn respond_serialized<T: Serialize>(&self, id: &str, result: Option<T>, error: Option<ErrorInfo>) -> io::Result<()> {
        let mut writer = self.writer.lock().unwrap();
        if let Some(parent) = self.request_parents.lock().unwrap().remove(id) {
            parent.finished.store(true, Ordering::Release);
            let _ = parent.state.compare_exchange(0, 1, Ordering::AcqRel, Ordering::Acquire);
        }
        self.cancel_pending_calls_for(id);
        Self::write_envelope_to(&mut writer, &Envelope::<Value> {
            msg_type: "request_state".to_string(),
            request_state: Some(RequestStateMsg { request_id: id.to_string(), state: "completed".to_string(), reason: None }),
            ..Default::default()
        })?;
        #[derive(Serialize)]
        struct Response<T> { #[serde(skip_serializing_if = "Option::is_none")] result: Option<T>, #[serde(skip_serializing_if = "Option::is_none")] error: Option<ErrorInfo> }
        #[derive(Serialize)]
        struct Reply<'a, T> { #[serde(rename = "type")] kind: &'static str, id: &'a str, response: Response<T> }
        Self::write_envelope_to(&mut writer, &Reply { kind: "response", id, response: Response { result, error } })
    }

    pub fn request_state(&self, id: &str, state: &str, reason: Option<&str>) -> io::Result<()> {
        self.request_state_for(None, id, state, reason)
    }

    pub(crate) fn request_state_for(&self, scope: Option<&RequestParent>, id: &str, state: &str, reason: Option<&str>) -> io::Result<()> {
        let mut writer = self.writer.lock().unwrap();
        if scope.is_some_and(|parent| parent.finished.load(Ordering::Acquire) || (parent.cancelled() && state != "progress")) || self.is_closed() { return Ok(()); }
        Self::write_envelope_to(&mut writer, &Envelope::<Value> {
            msg_type: "request_state".to_string(),
            request_state: Some(RequestStateMsg {
                request_id: id.to_string(),
                state: state.to_string(),
                reason: reason.map(str::to_string),
            }),
            ..Default::default()
        })
    }

    /// Make a blocking call to the host and wait for call_result.
    /// The main extension loop owns socket reads and routes call_result
    /// envelopes through complete_call, so request handlers can call host APIs
    /// without racing the reader.
    pub fn call(&self, method: &str, args: Option<Value>) -> io::Result<CallResultMsg> {
        self.call_for(None, method, args)
    }

    pub fn call_for(
        &self,
        parent_request_id: Option<&str>,
        method: &str,
        args: Option<Value>,
    ) -> io::Result<CallResultMsg> {
        let pending = self.begin_call_for(parent_request_id, method, args)?;
        self.wait_call(pending)
    }

    pub(crate) fn call_for_scope(&self, scope: Option<&RequestParent>, parent: Option<&str>, method: &str, args: Option<Value>) -> io::Result<CallResultMsg> {
        let pending = self.begin_call_with_scope(scope, parent, method, args)?;
        self.wait_call(pending)
    }

    pub(crate) fn call_typed<A: Serialize, R: serde::de::DeserializeOwned>(&self, scope: Option<&RequestParent>, parent: Option<&str>, method: &str, args: Option<A>) -> io::Result<CallResultMsg<R>> {
        let pending = self.begin_call_with_scope(scope, parent, method, args)?;
        self.wait_call_typed(pending)
    }

    /// Send a host call and return after its frame has been written. Streaming
    /// APIs use this to order notify frames after the host-side consumer call.
    pub(crate) fn begin_call_for(
        &self,
        parent_request_id: Option<&str>,
        method: &str,
        args: Option<Value>,
    ) -> io::Result<PendingCall> {
        self.begin_call_with_scope(None, parent_request_id, method, args)
    }

    pub(crate) fn begin_call_with_scope<A: Serialize>(
        &self,
        scope: Option<&RequestParent>,
        parent_request_id: Option<&str>,
        method: &str,
        args: Option<A>,
    ) -> io::Result<PendingCall> {
        let mut writer = self.writer.lock().unwrap();
        let mut pending_calls = self.pending_calls.lock().unwrap();
        if self.is_closed() || scope.is_some_and(RequestParent::cancelled) {
            return Err(io::Error::new(io::ErrorKind::ConnectionAborted, "host call owner is cancelled or closed"));
        }
        let parent_request_id = match scope {
            Some(parent) if parent.completed() => None,
            Some(parent) => Some(parent.id.as_str()),
            None => parent_request_id,
        };
        let id = format!(
            "c{}",
            self.call_id
                .fetch_add(1, std::sync::atomic::Ordering::Relaxed)
        );
        let (tx, rx) = mpsc::channel();
        pending_calls.insert(
            id.clone(),
            PendingCallSender {
                parent_request_id: parent_request_id.map(str::to_string),
                sender: tx,
            },
        );
        drop(pending_calls);
        #[derive(Serialize)]
        struct Call<'a, A> { method: &'a str, #[serde(skip_serializing_if = "Option::is_none")] args: Option<A>, #[serde(skip_serializing_if = "Option::is_none")] parent_request_id: Option<&'a str> }
        #[derive(Serialize)]
        struct Request<'a, A> { #[serde(rename = "type")] kind: &'static str, id: &'a str, call: Call<'a, A> }
        if let Err(err) = Self::write_envelope_to(&mut writer, &Request { kind: "call", id: &id, call: Call { method, args, parent_request_id } }) {
            self.pending_calls.lock().unwrap().remove(&id);
            return Err(err);
        }
        Ok(PendingCall {
            method: method.to_string(),
            receiver: rx,
        })
    }

    pub(crate) fn wait_call(&self, pending: PendingCall) -> io::Result<CallResultMsg> {
        self.wait_call_typed(pending)
    }

    fn wait_call_typed<R: serde::de::DeserializeOwned>(&self, pending: PendingCall) -> io::Result<CallResultMsg<R>> {
        let reply = pending.receiver.recv().map_err(|_| {
            io::Error::new(
                io::ErrorKind::ConnectionAborted,
                format!("host call {} cancelled", pending.method),
            )
        })?;
        let result = reply.result.map(|raw| serde_json::from_str(raw.get())).transpose().map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
        Ok(CallResultMsg { result, error: reply.error })
    }

    pub(crate) fn notify(&self, method: &str, args: Option<Value>) -> io::Result<()> {
        self.write_envelope(&Envelope {
            msg_type: "notify".to_string(),
            notify: Some(NotifyMsg {
                custom_input: None,
                method: method.to_string(),
                args,
            }),
            ..Default::default()
        })
    }

    /// Complete a pending host call. Returns true when the envelope was routed.
    pub fn complete_call<R: Serialize>(&self, env: &Envelope<R>) -> bool {
        if env.msg_type != "call_result" {
            return false;
        }
        let Some(id) = env.id.as_ref() else {
            return false;
        };
        let Some(pending) = self.pending_calls.lock().unwrap().remove(id) else {
            return false;
        };
        let reply = env.call_result.as_ref();
        let result = reply.and_then(|r| r.result.as_ref()).map(serde_json::value::to_raw_value).transpose();
        if let Ok(result) = result {
            let _ = pending.sender.send(CallResultMsg { result, error: reply.and_then(|r| r.error.clone()) });
        }
        true
    }

    pub(crate) fn cancel_pending_calls_for(&self, parent_request_id: &str) {
        if let Some(parent) = self.request_parents.lock().unwrap().get(parent_request_id) {
            let _ = parent.state.compare_exchange(0, 2, Ordering::AcqRel, Ordering::Acquire);
        }
        self.pending_calls
            .lock()
            .unwrap()
            .retain(|_, pending| pending.parent_request_id.as_deref() != Some(parent_request_id));
    }

    pub(crate) fn cancel_pending_calls(&self) {
        self.closed.store(true, Ordering::Release);
        self.request_parents.lock().unwrap().clear();
        self.pending_calls.lock().unwrap().clear();
        self.closed_signal.cancel();
    }

    /// Push a widget update (fire-and-forget).
    pub fn push_widget(&self, key: &str, lines: Vec<String>) -> io::Result<()> {
        self.write_envelope(&Envelope {
            msg_type: "widget_push".to_string(),
            widget_push: Some(WidgetPushMsg {
                key: key.to_string(),
                lines,
            }),
            ..Default::default()
        })
    }
}

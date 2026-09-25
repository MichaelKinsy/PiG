//! Wire protocol types matching the Go host's `subprocess/protocol.go`.
//! 4-byte big-endian length prefix + JSON payload.

use crate::transport::UnixStream;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::HashMap;
use std::io::{self, Read, Write};
use std::sync::Mutex;
use std::sync::mpsc;

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
pub struct Envelope {
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
    pub call_result: Option<CallResultMsg>,
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
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ToolDef {
    pub name: String,
    pub description: String,
    pub parameters: Value,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub constrained_sampling: Option<ConstrainedSampling>,
    #[serde(skip_serializing_if = "Vec::is_empty", default)]
    pub prompt_guidelines: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none", default)]
    pub source: Option<String>,
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
    #[serde(default)]
    pub state: Option<serde_json::Value>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
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
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct ResponseMsg {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub result: Option<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<ErrorInfo>,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct NotifyMsg {
    #[serde(default)]
    pub method: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub args: Option<Value>,
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
pub struct CallResultMsg {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub result: Option<Value>,
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

/// A thread-safe connection to the host over a Unix socket.
pub struct Connection {
    reader: Mutex<UnixStream>,
    writer: Mutex<UnixStream>,
    call_id: std::sync::atomic::AtomicU64,
    pending_calls: Mutex<HashMap<String, PendingCallSender>>,
}

struct PendingCallSender {
    parent_request_id: Option<String>,
    sender: mpsc::Sender<CallResultMsg>,
}

pub(crate) struct PendingCall {
    method: String,
    receiver: mpsc::Receiver<CallResultMsg>,
}

impl Connection {
    pub fn new(stream: UnixStream) -> Self {
        let writer = stream
            .try_clone()
            .expect("clone UnixStream for extension protocol writer");
        Self {
            reader: Mutex::new(stream),
            writer: Mutex::new(writer),
            call_id: std::sync::atomic::AtomicU64::new(0),
            pending_calls: Mutex::new(HashMap::new()),
        }
    }

    /// Read one frame from the socket (4-byte length prefix + JSON).
    pub fn read_envelope(&self) -> io::Result<Envelope> {
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
        let mut stream = self.writer.lock().unwrap();
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
        self.request_state(id, "completed", None)?;
        self.write_envelope(&Envelope {
            msg_type: "response".to_string(),
            id: Some(id.to_string()),
            response: Some(ResponseMsg { result, error }),
            ..Default::default()
        })
    }

    pub fn request_state(&self, id: &str, state: &str, reason: Option<&str>) -> io::Result<()> {
        self.write_envelope(&Envelope {
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

    /// Send a host call and return after its frame has been written. Streaming
    /// APIs use this to order notify frames after the host-side consumer call.
    pub(crate) fn begin_call_for(
        &self,
        parent_request_id: Option<&str>,
        method: &str,
        args: Option<Value>,
    ) -> io::Result<PendingCall> {
        let id = format!(
            "c{}",
            self.call_id
                .fetch_add(1, std::sync::atomic::Ordering::Relaxed)
        );
        let (tx, rx) = mpsc::channel();
        self.pending_calls.lock().unwrap().insert(
            id.clone(),
            PendingCallSender {
                parent_request_id: parent_request_id.map(str::to_string),
                sender: tx,
            },
        );
        if let Err(err) = self.write_envelope(&Envelope {
            msg_type: "call".to_string(),
            id: Some(id.clone()),
            call: Some(CallMsg {
                method: method.to_string(),
                args,
                parent_request_id: parent_request_id.map(str::to_string),
            }),
            ..Default::default()
        }) {
            self.pending_calls.lock().unwrap().remove(&id);
            return Err(err);
        }
        Ok(PendingCall {
            method: method.to_string(),
            receiver: rx,
        })
    }

    pub(crate) fn wait_call(&self, pending: PendingCall) -> io::Result<CallResultMsg> {
        pending.receiver.recv().map_err(|_| {
            io::Error::new(
                io::ErrorKind::ConnectionAborted,
                format!("host call {} cancelled", pending.method),
            )
        })
    }

    pub(crate) fn notify(&self, method: &str, args: Option<Value>) -> io::Result<()> {
        self.write_envelope(&Envelope {
            msg_type: "notify".to_string(),
            notify: Some(NotifyMsg {
                method: method.to_string(),
                args,
            }),
            ..Default::default()
        })
    }

    /// Complete a pending host call. Returns true when the envelope was routed.
    pub fn complete_call(&self, env: &Envelope) -> bool {
        if env.msg_type != "call_result" {
            return false;
        }
        let Some(id) = env.id.as_ref() else {
            return false;
        };
        let Some(pending) = self.pending_calls.lock().unwrap().remove(id) else {
            return false;
        };
        let _ = pending
            .sender
            .send(env.call_result.clone().unwrap_or(CallResultMsg {
                result: None,
                error: None,
            }));
        true
    }

    pub(crate) fn cancel_pending_calls_for(&self, parent_request_id: &str) {
        self.pending_calls
            .lock()
            .unwrap()
            .retain(|_, pending| pending.parent_request_id.as_deref() != Some(parent_request_id));
    }

    pub(crate) fn cancel_pending_calls(&self) {
        self.pending_calls.lock().unwrap().clear();
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

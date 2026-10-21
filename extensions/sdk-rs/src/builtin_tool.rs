//! Pi's `create<Tool>ToolDefinition`, `create<Tool>Tool`, `createCodingTools`, `createReadOnlyTools` and `createLocalBashOperations` (`core/tools/*.ts`, exported
//! from the package index), for a Rust extension.
//!
//! Pi builds the built-in tool in the extension's own process. Here the host owns the implementation, so the factories reach it through host calls: the
//! definition's metadata (`builtinTool.definition`), its `execute` (`builtinTool.execute`) and the local shell operations (`builtinTool.localExec`). An override
//! that wraps a built-in is written as in Pi:
//!
//! ```ignore
//! let mut built = pig_sdk::create_read_tool_definition(cwd, None)?;
//! let inner = built.executor();
//! built.definition.execute = Box::new(move |ctx, params| match inner.execute(ctx, params) {
//!     Ok(result) => /* post-process */ result.into_tool_result(),
//!     Err(error) => ToolResult::error(error.to_string()),
//! });
//! ext.register_tool(built.definition);
//! ```
//!
//! Async contract (Pi's `execute` returns a Promise): the call is awaited, so it returns when the tool has finished, with the tool's result, or with the error the
//! tool threw. `execute` forwards the calling request's cancellation, as Pi's `execute(...args)` forwards its own signal; `execute_with` takes the signal
//! explicitly, and `None` is Pi's undefined (the request does not cancel the call). `on_update` runs on a thread of the SDK, in order and before the call returns.
//! Pi's `operations` and `spawn_hook` functions stay in this process and run, on other threads, while the call does.

use std::collections::HashMap;
use std::io;
use std::sync::atomic::Ordering;
use std::sync::{Arc, Mutex};

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};

use crate::context::{Context, host_error_message};
use crate::extension::{ToolDefinition, ToolResult};
use crate::extension_api::{AgentToolResult, ExecuteToolUpdate};
use crate::host_helpers;
use crate::protocol::Connection;
use crate::provider::ProviderSignal;
use crate::user_bash::{BashOperations, base64, base64_decode};

const CALL_DEFINITION: &str = "builtinTool.definition";
const CALL_EXECUTE: &str = "builtinTool.execute";
const CALL_LOCAL_EXEC: &str = "builtinTool.localExec";

/// The result of one of Pi's operations: the value, or the message of the error Pi's function throws.
pub type OperationResult<T> = Result<T, String>;
/// An operation of one path.
pub type PathOperation<R> = Arc<dyn Fn(&str) -> OperationResult<R> + Send + Sync>;
/// An operation of a path and the content to write to it.
pub type WriteOperation = Arc<dyn Fn(&str, &str) -> OperationResult<()> + Send + Sync>;

/// Pi's `ModelImageResizeOptions`: the resize profile of an image sent to a model. `None` members are Pi's undefined.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct ModelImageResizeOptions {
    #[serde(rename = "maxWidth", skip_serializing_if = "Option::is_none")]
    pub max_width: Option<u32>,
    #[serde(rename = "maxHeight", skip_serializing_if = "Option::is_none")]
    pub max_height: Option<u32>,
    /// Limits the base64-encoded image payload.
    #[serde(rename = "maxBytes", skip_serializing_if = "Option::is_none")]
    pub max_bytes: Option<u64>,
    #[serde(rename = "jpegQuality", skip_serializing_if = "Option::is_none")]
    pub jpeg_quality: Option<u32>,
}

/// Pi's `ReadOperations`: pluggable file reading. `detect_image_mime_type` returns a MIME type or `None` and is optional (without it every file is read as text).
#[derive(Clone)]
pub struct ReadOperations {
    pub read_file: PathOperation<Vec<u8>>,
    pub access: PathOperation<()>,
    pub detect_image_mime_type: Option<PathOperation<Option<String>>>,
}

/// Pi's `ReadToolOptions`. `auto_resize_images` `None` is Pi's default, true.
#[derive(Clone, Default)]
pub struct ReadToolOptions {
    pub auto_resize_images: Option<bool>,
    pub resize_options: Option<ModelImageResizeOptions>,
    pub operations: Option<ReadOperations>,
}

/// Pi's `WriteOperations`.
#[derive(Clone)]
pub struct WriteOperations {
    pub write_file: WriteOperation,
    pub mkdir: PathOperation<()>,
}

/// Pi's `WriteToolOptions`.
#[derive(Clone, Default)]
pub struct WriteToolOptions {
    pub operations: Option<WriteOperations>,
}

/// Pi's `EditOperations`.
#[derive(Clone)]
pub struct EditOperations {
    pub read_file: PathOperation<Vec<u8>>,
    pub write_file: WriteOperation,
    /// Fails when the file is not readable and writable.
    pub access: PathOperation<()>,
}

/// Pi's `EditToolOptions`.
#[derive(Clone, Default)]
pub struct EditToolOptions {
    pub operations: Option<EditOperations>,
}

/// Pi's `GrepOperations`. `is_directory` fails when the path does not exist; `read_file` is optional and, without it, matched files are streamed from the local
/// file system.
#[derive(Clone)]
pub struct GrepOperations {
    pub is_directory: PathOperation<bool>,
    pub read_file: Option<PathOperation<String>>,
}

/// Pi's `GrepToolOptions`.
#[derive(Clone, Default)]
pub struct GrepToolOptions {
    pub operations: Option<GrepOperations>,
}

/// The options Pi's `FindOperations.glob` receives.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct FindGlobOptions {
    pub ignore: Vec<String>,
    pub limit: f64,
}

/// Pi's `FindOperations`. `glob(pattern, cwd, options)` returns paths relative to `cwd` or absolute.
#[derive(Clone)]
pub struct FindOperations {
    pub exists: PathOperation<bool>,
    pub glob: Arc<dyn Fn(&str, &str, FindGlobOptions) -> OperationResult<Vec<String>> + Send + Sync>,
}

/// Pi's `FindToolOptions`.
#[derive(Clone, Default)]
pub struct FindToolOptions {
    pub operations: Option<FindOperations>,
}

/// The stat Pi's `LsOperations.stat` returns: only `isDirectory()` is read.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct LsStat {
    pub is_directory: bool,
}

/// Pi's `LsOperations`. `stat` fails when the path is not found.
#[derive(Clone)]
pub struct LsOperations {
    pub exists: PathOperation<bool>,
    pub stat: PathOperation<LsStat>,
    pub readdir: PathOperation<Vec<String>>,
}

/// Pi's `LsToolOptions`.
#[derive(Clone, Default)]
pub struct LsToolOptions {
    pub operations: Option<LsOperations>,
}

/// Pi's `BashSpawnContext`: the command, working directory and environment a shell tool is about to run.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct BashSpawnContext {
    pub command: String,
    pub cwd: String,
    pub env: HashMap<String, String>,
}

/// Pi's `BashSpawnHook`: it receives the context the command is about to run in and returns the context to run. An `Err` is the hook's throw: the tool call fails with it.
pub type BashSpawnHook = Arc<dyn Fn(BashSpawnContext) -> OperationResult<BashSpawnContext> + Send + Sync>;

/// Pi's `BashToolOptions`. `operations` `None` is the local shell.
#[derive(Clone, Default)]
pub struct BashToolOptions {
    pub operations: Option<BashOperations>,
    /// Prepended to every command (for example shell setup commands).
    pub command_prefix: Option<String>,
    pub shell_path: Option<String>,
    /// Exposes the session as `PI_*` environment variables. `None` is Pi's default, true.
    pub expose_session_environment: Option<bool>,
    pub spawn_hook: Option<BashSpawnHook>,
}

/// Pi's `PowerShellToolOptions`: `BashToolOptions`' `operations`, `exposeSessionEnvironment` and `spawnHook`.
#[derive(Clone, Default)]
pub struct PowerShellToolOptions {
    pub operations: Option<BashOperations>,
    pub expose_session_environment: Option<bool>,
    pub spawn_hook: Option<BashSpawnHook>,
}

/// Pi's `ToolsOptions`: the options of each built-in tool, for [`create_coding_tools`] and [`create_read_only_tools`].
#[derive(Clone, Default)]
pub struct ToolsOptions {
    pub read: Option<ReadToolOptions>,
    pub bash: Option<BashToolOptions>,
    pub powershell: Option<PowerShellToolOptions>,
    pub write: Option<WriteToolOptions>,
    pub edit: Option<EditToolOptions>,
    pub grep: Option<GrepToolOptions>,
    pub find: Option<FindToolOptions>,
    pub ls: Option<LsToolOptions>,
}

type Callback = Arc<dyn Fn(&Value) -> Result<Value, String> + Send + Sync>;

/// One `create<Tool>ToolDefinition(cwd, options)` call: which tool, where, and with what options. `callbacks` holds the functions that stay in this process.
#[derive(Clone)]
struct Spec {
    name: &'static str,
    cwd: String,
    options: Map<String, Value>,
    callbacks: HashMap<&'static str, Callback>,
    bash: Option<BashOperations>,
}

impl Spec {
    fn new(name: &'static str, cwd: &str) -> Self {
        Self { name, cwd: cwd.to_string(), options: Map::new(), callbacks: HashMap::new(), bash: None }
    }

    fn operations(&mut self, names: &[&str]) {
        self.options.insert("operations".into(), json!(names));
    }

    fn callback(&mut self, member: &'static str, function: impl Fn(&Value) -> Result<Value, String> + Send + Sync + 'static) {
        self.callbacks.insert(member, Arc::new(function));
    }
}

fn text_of<'a>(args: &'a Value, field: &str) -> &'a str {
    args.get(field).and_then(Value::as_str).unwrap_or("")
}

fn encoded(data: Vec<u8>) -> Value {
    Value::String(base64(&data))
}

fn path_callback<R: 'static>(function: PathOperation<R>, wire: impl Fn(R) -> Value + Send + Sync + 'static) -> impl Fn(&Value) -> Result<Value, String> + Send + Sync + 'static {
    move |args| function(text_of(args, "path")).map(&wire)
}

fn write_callback(function: WriteOperation) -> impl Fn(&Value) -> Result<Value, String> + Send + Sync + 'static {
    move |args| function(text_of(args, "path"), text_of(args, "content")).map(|()| Value::Null)
}

fn read_spec(cwd: &str, options: Option<ReadToolOptions>) -> Spec {
    let mut spec = Spec::new("read", cwd);
    let Some(options) = options else { return spec };
    if let Some(auto) = options.auto_resize_images {
        spec.options.insert("autoResizeImages".into(), json!(auto));
    }
    if let Some(resize) = options.resize_options {
        spec.options.insert("resizeOptions".into(), serde_json::to_value(resize).unwrap_or(Value::Null));
    }
    if let Some(ops) = options.operations {
        let mut names = vec!["readFile", "access"];
        spec.callback("operations.readFile", path_callback(ops.read_file.clone(), encoded));
        spec.callback("operations.access", path_callback(ops.access.clone(), |()| Value::Null));
        if let Some(detect) = ops.detect_image_mime_type.clone() {
            names.push("detectImageMimeType");
            spec.callback("operations.detectImageMimeType", path_callback(detect, |mime| mime.map_or(Value::Null, Value::String)));
        }
        spec.operations(&names);
    }
    spec
}

fn write_spec(cwd: &str, options: Option<WriteToolOptions>) -> Spec {
    let mut spec = Spec::new("write", cwd);
    if let Some(ops) = options.and_then(|options| options.operations) {
        spec.callback("operations.writeFile", write_callback(ops.write_file.clone()));
        spec.callback("operations.mkdir", path_callback(ops.mkdir.clone(), |()| Value::Null));
        spec.operations(&["writeFile", "mkdir"]);
    }
    spec
}

fn edit_spec(cwd: &str, options: Option<EditToolOptions>) -> Spec {
    let mut spec = Spec::new("edit", cwd);
    if let Some(ops) = options.and_then(|options| options.operations) {
        spec.callback("operations.readFile", path_callback(ops.read_file.clone(), encoded));
        spec.callback("operations.writeFile", write_callback(ops.write_file.clone()));
        spec.callback("operations.access", path_callback(ops.access.clone(), |()| Value::Null));
        spec.operations(&["readFile", "writeFile", "access"]);
    }
    spec
}

fn grep_spec(cwd: &str, options: Option<GrepToolOptions>) -> Spec {
    let mut spec = Spec::new("grep", cwd);
    if let Some(ops) = options.and_then(|options| options.operations) {
        spec.callback("operations.isDirectory", path_callback(ops.is_directory.clone(), Value::Bool));
        let mut names = vec!["isDirectory"];
        if let Some(read_file) = ops.read_file.clone() {
            names.push("readFile");
            spec.callback("operations.readFile", path_callback(read_file, Value::String));
        }
        spec.operations(&names);
    }
    spec
}

fn find_spec(cwd: &str, options: Option<FindToolOptions>) -> Spec {
    let mut spec = Spec::new("find", cwd);
    if let Some(ops) = options.and_then(|options| options.operations) {
        spec.callback("operations.exists", path_callback(ops.exists.clone(), Value::Bool));
        let glob = ops.glob.clone();
        spec.callback("operations.glob", move |args| {
            let ignore = args
                .get("ignore")
                .and_then(Value::as_array)
                .map(|items| items.iter().filter_map(|item| item.as_str().map(str::to_string)).collect())
                .unwrap_or_default();
            let options = FindGlobOptions { ignore, limit: args.get("limit").and_then(Value::as_f64).unwrap_or(0.0) };
            glob(text_of(args, "pattern"), text_of(args, "cwd"), options).map(|paths| json!(paths))
        });
        spec.operations(&["exists", "glob"]);
    }
    spec
}

fn ls_spec(cwd: &str, options: Option<LsToolOptions>) -> Spec {
    let mut spec = Spec::new("ls", cwd);
    if let Some(ops) = options.and_then(|options| options.operations) {
        spec.callback("operations.exists", path_callback(ops.exists.clone(), Value::Bool));
        spec.callback("operations.stat", path_callback(ops.stat.clone(), |stat| json!({"isDirectory": stat.is_directory})));
        spec.callback("operations.readdir", path_callback(ops.readdir.clone(), |names| json!(names)));
        spec.operations(&["exists", "stat", "readdir"]);
    }
    spec
}

fn shell_spec(name: &'static str, cwd: &str, operations: Option<BashOperations>, hook: Option<BashSpawnHook>, expose: Option<bool>) -> Spec {
    let mut spec = Spec::new(name, cwd);
    if let Some(expose) = expose {
        spec.options.insert("exposeSessionEnvironment".into(), json!(expose));
    }
    if let Some(operations) = operations {
        spec.operations(&["exec"]);
        spec.bash = Some(operations);
    }
    if let Some(hook) = hook {
        spec.options.insert("spawnHook".into(), json!(true));
        spec.callback("spawnHook", move |args| {
            let context: BashSpawnContext = serde_json::from_value(args.clone()).map_err(|error| format!("decode spawnHook context: {error}"))?;
            hook(context).map(|context| serde_json::to_value(context).unwrap_or(Value::Null))
        });
    }
    spec
}

fn bash_spec(cwd: &str, options: Option<BashToolOptions>) -> Spec {
    let Some(options) = options else { return Spec::new("bash", cwd) };
    let mut spec = shell_spec("bash", cwd, options.operations, options.spawn_hook, options.expose_session_environment);
    if let Some(prefix) = options.command_prefix.filter(|prefix| !prefix.is_empty()) {
        spec.options.insert("commandPrefix".into(), json!(prefix));
    }
    if let Some(shell_path) = options.shell_path.filter(|path| !path.is_empty()) {
        spec.options.insert("shellPath".into(), json!(shell_path));
    }
    spec
}

fn powershell_spec(cwd: &str, options: Option<PowerShellToolOptions>) -> Spec {
    let Some(options) = options else { return Spec::new("powershell", cwd) };
    shell_spec("powershell", cwd, options.operations, options.spawn_hook, options.expose_session_environment)
}

type ExecDataSink = Arc<dyn Fn(&[u8]) -> Result<(), String> + Send + Sync>;

/// The functions the host calls back, for the calls in flight: the options' callbacks by handle and the output sinks of local executions by execute id. Ids are
/// unique across the process, so one table serves every extension of a packed cell.
#[derive(Default)]
struct Registry {
    seq: u64,
    callbacks: HashMap<String, HashMap<&'static str, Callback>>,
    exec_data: HashMap<String, ExecDataSink>,
}

static REGISTRY: Mutex<Option<Registry>> = Mutex::new(None);

/// The guard of a lock whose holder panicked: the tables here stay consistent across a callback's panic, which is caught.
fn locked<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(|poisoned| poisoned.into_inner())
}

fn registry<T>(use_registry: impl FnOnce(&mut Registry) -> T) -> T {
    let mut guard = locked(&REGISTRY);
    use_registry(guard.get_or_insert_with(Registry::default))
}

impl AgentToolResult {
    /// The result as a tool handler returns it (the wire object: `content`, `details`, `structured_content`, `usage`, `is_error`, `terminate`), so a result of a
    /// built-in tool can be returned from a handler, or passed to [`Context::on_update`], as it is.
    pub fn into_tool_result(self) -> ToolResult {
        let mut object = Map::new();
        object.insert("content".into(), Value::Array(self.content));
        if !self.details.is_null() {
            object.insert("details".into(), self.details);
        }
        if let Some(structured) = self.structured_content {
            object.insert("structured_content".into(), structured);
        }
        if let Some(usage) = self.usage {
            object.insert("usage".into(), usage);
        }
        if self.is_error {
            object.insert("is_error".into(), Value::Bool(true));
        }
        if self.terminate {
            object.insert("terminate".into(), Value::Bool(true));
        }
        ToolResult::Json(Value::Object(object))
    }

    /// The text blocks of the result, joined with newlines.
    pub fn text(&self) -> String {
        self.content
            .iter()
            .filter(|block| block.get("type").and_then(Value::as_str) == Some("text"))
            .filter_map(|block| block.get("text").and_then(Value::as_str))
            .collect::<Vec<_>>()
            .join("\n")
    }
}

/// The `ToolDefinition` Pi's `create<Tool>ToolDefinition` returns for a built-in tool, and the way to run it. `definition` carries its name, label, description,
/// prompt snippet and guidelines, schemas and execution mode, with an `execute` that runs the built-in tool on the host: register it with
/// [`crate::Extension::register_tool`] to override the built-in, assigning another `execute` first to wrap it ([`BuiltinToolDefinition::executor`] is the
/// handle that runs the built-in).
///
/// The host draws the built-in tool's call and result itself, so a definition that sets no renderers keeps the built-in's, as an override of a built-in does in Pi.
pub struct BuiltinToolDefinition {
    pub definition: ToolDefinition,
    spec: Arc<Spec>,
}

/// A cloneable handle that runs a built-in tool's `execute`.
#[derive(Clone)]
pub struct BuiltinToolExecutor {
    spec: Arc<Spec>,
}

impl BuiltinToolDefinition {
    /// The handle that runs the built-in, for a wrapping `execute` to call.
    pub fn executor(&self) -> BuiltinToolExecutor {
        BuiltinToolExecutor { spec: self.spec.clone() }
    }
}

impl BuiltinToolExecutor {
    /// Pi's `definition.execute(...args)` of a tool handler: the tool call id, the calling request's cancellation as the signal, the request's update sink as
    /// `onUpdate` and the extension's context are the handler's own.
    pub fn execute(&self, ctx: &Context, params: Value) -> io::Result<AgentToolResult> {
        let forward: Option<ExecuteToolUpdate> = if ctx.tool_call_id.is_some() && !ctx.request_id.is_empty() {
            let ctx = ctx.clone();
            Some(Arc::new(move |partial: AgentToolResult| {
                if let Err(error) = ctx.on_update(partial.into_tool_result()) {
                    panic!("{error}");
                }
            }))
        } else {
            None
        };
        execute(&ctx.conn, Some(ctx), &self.spec, ctx.tool_call_id.as_deref().unwrap_or(""), params, None, true, forward)
    }

    /// Pi's `definition.execute(toolCallId, params, signal, onUpdate, ctx)`. A `None` signal is Pi's undefined: only the call itself ends it. `on_update`
    /// receives each partial result, in order and before the call returns; a panic in it is the callback's throw and fails the call with its message after the
    /// tool returned. The tool reads the session's working directory, model and identity from `ctx`, as `ctx?.cwd || cwd` and `ctx?.model` do; the cwd given to
    /// the factory applies only to a tool without a context ([`BuiltinTool`]).
    pub fn execute_with(
        &self,
        ctx: &Context,
        tool_call_id: &str,
        params: Value,
        signal: Option<&ProviderSignal>,
        on_update: Option<ExecuteToolUpdate>,
    ) -> io::Result<AgentToolResult> {
        execute(&ctx.conn, Some(ctx), &self.spec, tool_call_id, params, signal, false, on_update)
    }
}

/// The AgentTool Pi's `create<Tool>Tool` returns: the built-in tool without an extension context, so it works in the working directory given to the factory and
/// without the session's model and identity.
pub struct BuiltinTool {
    pub name: String,
    pub label: String,
    pub description: String,
    pub parameters: Value,
    pub output_schema: Option<Value>,
    pub constrained_sampling: Option<Value>,
    pub execution_mode: Option<String>,
    /// Set for the shell tools, as Pi's `createBashTool` and `createPowerShellTool` do.
    pub prompt_snippet: Option<String>,
    pub prompt_guidelines: Option<Vec<String>>,
    spec: Arc<Spec>,
}

impl BuiltinTool {
    /// Pi's `tool.execute(toolCallId, params, signal, onUpdate)`. It runs over the connection of the running extension, so it needs [`crate::Extension::run`] to
    /// have started: a call before that is an error. A `None` signal is Pi's undefined.
    pub fn execute(&self, tool_call_id: &str, params: Value, signal: Option<&ProviderSignal>, on_update: Option<ExecuteToolUpdate>) -> io::Result<AgentToolResult> {
        let conn = host_helpers::running_connection()?;
        execute(&conn, None, &self.spec, tool_call_id, params, signal, false, on_update)
    }
}

fn definition_reply(spec: &Spec) -> io::Result<Value> {
    let mut args = json!({"name": spec.name, "cwd": spec.cwd});
    if !spec.options.is_empty() {
        args["options"] = Value::Object(spec.options.clone());
    }
    host_helpers::helper_call(CALL_DEFINITION, args)?.ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "host returned no result for builtinTool.definition"))
}

fn text_field(reply: &Value, field: &str) -> String {
    text_of(reply, field).to_string()
}

fn present(reply: &Value, field: &str) -> Option<Value> {
    reply.get(field).filter(|value| !value.is_null()).cloned()
}

fn strings(reply: &Value, field: &str) -> Option<Vec<String>> {
    reply.get(field).and_then(Value::as_array).map(|items| items.iter().filter_map(|item| item.as_str().map(str::to_string)).collect())
}

fn create_definition(spec: Spec) -> io::Result<BuiltinToolDefinition> {
    let reply = definition_reply(&spec)?;
    let spec = Arc::new(spec);
    let executor = BuiltinToolExecutor { spec: spec.clone() };
    let mut definition = ToolDefinition::new(
        text_field(&reply, "name"),
        text_field(&reply, "label"),
        text_field(&reply, "description"),
        reply.get("parameters").cloned().unwrap_or(Value::Null),
        move |ctx, params| match executor.execute(ctx, params) {
            Ok(result) => result.into_tool_result(),
            Err(error) => ToolResult::error(error.to_string()),
        },
    );
    let snippet = text_field(&reply, "promptSnippet");
    definition.prompt_snippet = (!snippet.is_empty()).then_some(snippet);
    definition.prompt_guidelines = strings(&reply, "promptGuidelines").unwrap_or_default();
    definition.output_schema = present(&reply, "outputSchema");
    let mode = text_field(&reply, "executionMode");
    definition.execution_mode = (!mode.is_empty()).then_some(mode);
    if text_field(&reply, "renderShell") == "self" {
        definition.render_shell = crate::ToolRenderShell::SelfShell;
    }
    if let Some(sampling) = present(&reply, "constrainedSampling") {
        definition.constrained_sampling = serde_json::from_value::<crate::ConstrainedSampling>(sampling).ok().map(crate::ToolConstrainedSampling::Config);
    }
    Ok(BuiltinToolDefinition { definition, spec })
}

fn create_tool(spec: Spec) -> io::Result<BuiltinTool> {
    let reply = definition_reply(&spec)?;
    let shell = matches!(spec.name, "bash" | "powershell");
    let snippet = text_field(&reply, "promptSnippet");
    Ok(BuiltinTool {
        name: text_field(&reply, "name"),
        label: text_field(&reply, "label"),
        description: text_field(&reply, "description"),
        parameters: reply.get("parameters").cloned().unwrap_or(Value::Null),
        output_schema: present(&reply, "outputSchema"),
        constrained_sampling: present(&reply, "constrainedSampling"),
        execution_mode: Some(text_field(&reply, "executionMode")).filter(|mode| !mode.is_empty()),
        prompt_snippet: (shell && !snippet.is_empty()).then_some(snippet),
        prompt_guidelines: if shell { strings(&reply, "promptGuidelines") } else { None },
        spec: Arc::new(spec),
    })
}

/// `execute(toolCallId, params, signal, onUpdate, ctx)` of a built-in tool's definition. `ctx` `None` is an undefined context. `forward_signal` makes the calling
/// request's cancellation the signal.
#[allow(clippy::too_many_arguments)]
fn execute(
    conn: &Arc<Connection>,
    ctx: Option<&Context>,
    spec: &Spec,
    tool_call_id: &str,
    params: Value,
    signal: Option<&ProviderSignal>,
    forward_signal: bool,
    on_update: Option<ExecuteToolUpdate>,
) -> io::Result<AgentToolResult> {
    let execute_id = format!("x{}", conn.execute_seq.fetch_add(1, Ordering::Relaxed));
    // Updates and the result are read from one ordered socket; the dispatcher runs the callback off the reader and is joined after the result, so every update
    // precedes the return. The host waits for each answer, so a callback's panic is that answer.
    let dispatcher = on_update.as_ref().map(|callback| {
        let (sender, receiver) = std::sync::mpsc::channel::<crate::protocol::ExecuteUpdate>();
        locked(&conn.execute_updates).insert(execute_id.clone(), sender);
        let callback = callback.clone();
        std::thread::spawn(move || {
            for (update, answer) in receiver {
                let failure = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| callback(update))).err().map(|payload| crate::context::panic_message(payload.as_ref()));
                let _ = answer.send(failure);
            }
        })
    });
    let mut handle = String::new();
    if !spec.callbacks.is_empty() || spec.bash.is_some() {
        handle = registry(|registry| {
            registry.seq += 1;
            let handle = format!("builtin-{}", registry.seq);
            registry.callbacks.insert(handle.clone(), spec.callbacks.clone());
            handle
        });
        if let Some(bash) = &spec.bash {
            // The shell tool's exec rides the user_bash exec request under the same handle.
            conn.bash_operations.register_as(&handle, bash.clone());
        }
    }
    let mut args = json!({"name": spec.name, "cwd": spec.cwd, "toolCallId": tool_call_id, "params": params, "executeId": execute_id});
    if !spec.options.is_empty() {
        args["options"] = Value::Object(spec.options.clone());
    }
    if on_update.is_some() {
        args["wantsUpdates"] = Value::Bool(true);
    }
    if ctx.is_some() {
        args["context"] = Value::Bool(true);
    }
    if !handle.is_empty() {
        args["callbacks"] = Value::String(handle.clone());
    }
    if signal.is_some_and(ProviderSignal::is_cancelled) {
        args["aborted"] = Value::Bool(true);
    }
    let outcome = run_call(conn, ctx, CALL_EXECUTE, args, &execute_id, signal, forward_signal);
    locked(&conn.execute_updates).remove(&execute_id);
    if let Some(dispatcher) = dispatcher {
        let _ = dispatcher.join();
    }
    if !handle.is_empty() {
        registry(|registry| registry.callbacks.remove(&handle));
        conn.bash_operations.release(&handle);
    }
    let result = outcome?;
    serde_json::from_value(result).map_err(|error| io::Error::new(io::ErrorKind::InvalidData, format!("host reply to builtinTool.execute: {error}")))
}

/// Sends one call and waits for its result. A call under `ctx` with `forward_signal` is scoped to the calling request; any other call is detached from it, and an
/// explicit `signal` aborts it through a second call, sent after the call's frame.
fn run_call(conn: &Arc<Connection>, ctx: Option<&Context>, method: &str, args: Value, execute_id: &str, signal: Option<&ProviderSignal>, forward_signal: bool) -> io::Result<Value> {
    if let Some(ctx) = ctx {
        let _ = conn.request_state_for(ctx.request_parent.as_deref(), &ctx.request_id, "blocked", Some("host_call"));
    }
    let pending = match ctx {
        Some(ctx) if forward_signal => conn.begin_call_with_scope(ctx.request_parent.as_deref(), Some(&ctx.request_id), method, Some(args)),
        _ => conn.begin_call_with_scope::<Value>(None, None, method, Some(args)),
    };
    let pending = match pending {
        Ok(pending) => pending,
        Err(error) => {
            if let Some(ctx) = ctx {
                let _ = conn.request_state_for(ctx.request_parent.as_deref(), &ctx.request_id, "progress", None);
            }
            return Err(error);
        }
    };
    let _cancel = signal.map(|signal| {
        let conn = conn.clone();
        let execute_id = execute_id.to_string();
        signal.subscribe(Arc::new(move || {
            let _ = conn.begin_call_for(None, "executeTool.cancel", Some(json!({"executeId": execute_id})));
        }))
    });
    let reply = conn.wait_call_typed::<Value>(pending);
    if let Some(ctx) = ctx {
        let _ = conn.request_state_for(ctx.request_parent.as_deref(), &ctx.request_id, "progress", None);
    }
    let reply = reply?;
    if let Some(error) = reply.error {
        return Err(io::Error::other(host_error_message(error)));
    }
    reply.result.ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, format!("host reply to {method} has no result")))
}

/// Runs one function of a call's options for the host's `builtin_tool_callback`. An `Err` is the callback's throw.
pub(crate) fn dispatch_callback(args: &Value) -> Result<Value, String> {
    let handle = text_of(args, "handle");
    let member = text_of(args, "member");
    let function = registry(|registry| registry.callbacks.get(handle).and_then(|callbacks| callbacks.get(member).cloned()));
    let function = function.ok_or_else(|| format!("no built-in tool callback {member}"))?;
    let arguments = args.get("args").cloned().unwrap_or(Value::Null);
    std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| function(&arguments))).map_err(|payload| crate::context::panic_message(payload.as_ref()))?
}

/// Delivers one output chunk of a local execution to its `on_data`, and returns what the host waits for: `Ok`, or `on_data`'s throw.
pub(crate) fn dispatch_exec_data(args: &Value) -> Result<(), String> {
    let sink = registry(|registry| registry.exec_data.get(text_of(args, "executeId")).cloned());
    let Some(sink) = sink else { return Ok(()) };
    let data = base64_decode(text_of(args, "data")).ok_or("invalid builtin_tool_exec_data")?;
    sink(&data)
}

/// The `{ shellPath }` of Pi's `createLocalBashOperations`.
#[derive(Debug, Clone, Default)]
pub struct LocalBashOperationsOptions {
    /// An explicit shell path; `None` resolves the platform's shell.
    pub shell_path: Option<String>,
}

fn local_exec(shell: &'static str, shell_path: Option<String>) -> BashOperations {
    BashOperations {
        exec: Arc::new(move |command, cwd, options| {
            let conn = host_helpers::running_connection().map_err(|error| error.to_string())?;
            let execute_id = format!("x{}", conn.execute_seq.fetch_add(1, Ordering::Relaxed));
            // A throw in onData (a panic) ends the command with it, as Pi's would reject the exec.
            let failure: Arc<Mutex<Option<String>>> = Arc::new(Mutex::new(None));
            let on_data = options.on_data.clone();
            let sink_failure = failure.clone();
            registry(|registry| {
                registry.exec_data.insert(
                    execute_id.clone(),
                    Arc::new(move |data: &[u8]| {
                        if let Some(message) = locked(&sink_failure).clone() {
                            return Err(message);
                        }
                        std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| on_data(data))).map_err(|payload| {
                            let message = crate::context::panic_message(payload.as_ref());
                            *locked(&sink_failure) = Some(message.clone());
                            message
                        })
                    }),
                );
            });
            let mut args = json!({"shell": shell, "command": command, "cwd": cwd, "executeId": execute_id});
            if let Some(path) = &shell_path {
                args["shellPath"] = json!(path);
            }
            if let Some(timeout) = options.timeout {
                args["timeout"] = json!(timeout);
            }
            if let Some(env) = &options.env {
                args["env"] = json!(env);
            }
            if options.signal.is_cancelled() {
                args["aborted"] = Value::Bool(true);
            }
            let outcome = run_call(&conn, None, CALL_LOCAL_EXEC, args, &execute_id, Some(&options.signal), false);
            registry(|registry| registry.exec_data.remove(&execute_id));
            if let Some(message) = locked(&failure).clone() {
                return Err(message);
            }
            let reply = outcome.map_err(|error| error.to_string())?;
            Ok(reply.get("exitCode").and_then(Value::as_i64))
        }),
    }
}

/// Pi's `createLocalBashOperations(options)`: the `BashOperations` that run a command in the local shell, which an extension wraps to rewrite commands while it
/// keeps Pi's local shell behavior (for example in a `user_bash` handler or a bash tool's `operations`). The command runs on the host; `exec` streams its output
/// to `on_data` in order and returns when the command ends, with Pi's `{ exitCode }`, or with the error Pi's exec rejects with (`"aborted"` when the signal was
/// set, `"timeout:<seconds>"` when the timeout elapsed). It runs over the connection of the running extension, so it needs [`crate::Extension::run`] to have started.
pub fn create_local_bash_operations(options: Option<LocalBashOperationsOptions>) -> BashOperations {
    local_exec("bash", options.and_then(|options| options.shell_path))
}

/// Pi's `createLocalPowerShellOperations()`: [`create_local_bash_operations`] for PowerShell.
pub fn create_local_powershell_operations() -> BashOperations {
    local_exec("powershell", None)
}

/// Pi's `createReadToolDefinition(cwd, options)`. Like every `create_*_tool_definition` it asks the host for the built-in tool's definition, so it may be called
/// while the factory runs, before [`crate::Extension::run`].
pub fn create_read_tool_definition(cwd: &str, options: Option<ReadToolOptions>) -> io::Result<BuiltinToolDefinition> {
    create_definition(read_spec(cwd, options))
}

/// Pi's `createWriteToolDefinition(cwd, options)`.
pub fn create_write_tool_definition(cwd: &str, options: Option<WriteToolOptions>) -> io::Result<BuiltinToolDefinition> {
    create_definition(write_spec(cwd, options))
}

/// Pi's `createEditToolDefinition(cwd, options)`.
pub fn create_edit_tool_definition(cwd: &str, options: Option<EditToolOptions>) -> io::Result<BuiltinToolDefinition> {
    create_definition(edit_spec(cwd, options))
}

/// Pi's `createBashToolDefinition(cwd, options)`.
pub fn create_bash_tool_definition(cwd: &str, options: Option<BashToolOptions>) -> io::Result<BuiltinToolDefinition> {
    create_definition(bash_spec(cwd, options))
}

/// Pi's `createGrepToolDefinition(cwd, options)`.
pub fn create_grep_tool_definition(cwd: &str, options: Option<GrepToolOptions>) -> io::Result<BuiltinToolDefinition> {
    create_definition(grep_spec(cwd, options))
}

/// Pi's `createFindToolDefinition(cwd, options)`.
pub fn create_find_tool_definition(cwd: &str, options: Option<FindToolOptions>) -> io::Result<BuiltinToolDefinition> {
    create_definition(find_spec(cwd, options))
}

/// Pi's `createLsToolDefinition(cwd, options)`.
pub fn create_ls_tool_definition(cwd: &str, options: Option<LsToolOptions>) -> io::Result<BuiltinToolDefinition> {
    create_definition(ls_spec(cwd, options))
}

/// Pi's `createPowerShellToolDefinition(cwd, options)`.
pub fn create_powershell_tool_definition(cwd: &str, options: Option<PowerShellToolOptions>) -> io::Result<BuiltinToolDefinition> {
    create_definition(powershell_spec(cwd, options))
}

/// Pi's `createReadTool(cwd, options)`.
pub fn create_read_tool(cwd: &str, options: Option<ReadToolOptions>) -> io::Result<BuiltinTool> {
    create_tool(read_spec(cwd, options))
}

/// Pi's `createWriteTool(cwd, options)`.
pub fn create_write_tool(cwd: &str, options: Option<WriteToolOptions>) -> io::Result<BuiltinTool> {
    create_tool(write_spec(cwd, options))
}

/// Pi's `createEditTool(cwd, options)`.
pub fn create_edit_tool(cwd: &str, options: Option<EditToolOptions>) -> io::Result<BuiltinTool> {
    create_tool(edit_spec(cwd, options))
}

/// Pi's `createBashTool(cwd, options)`.
pub fn create_bash_tool(cwd: &str, options: Option<BashToolOptions>) -> io::Result<BuiltinTool> {
    create_tool(bash_spec(cwd, options))
}

/// Pi's `createGrepTool(cwd, options)`.
pub fn create_grep_tool(cwd: &str, options: Option<GrepToolOptions>) -> io::Result<BuiltinTool> {
    create_tool(grep_spec(cwd, options))
}

/// Pi's `createFindTool(cwd, options)`.
pub fn create_find_tool(cwd: &str, options: Option<FindToolOptions>) -> io::Result<BuiltinTool> {
    create_tool(find_spec(cwd, options))
}

/// Pi's `createLsTool(cwd, options)`.
pub fn create_ls_tool(cwd: &str, options: Option<LsToolOptions>) -> io::Result<BuiltinTool> {
    create_tool(ls_spec(cwd, options))
}

/// Pi's `createPowerShellTool(cwd, options)`.
pub fn create_powershell_tool(cwd: &str, options: Option<PowerShellToolOptions>) -> io::Result<BuiltinTool> {
    create_tool(powershell_spec(cwd, options))
}

/// Pi's `createCodingTools(cwd, options)`: read, bash, edit and write.
pub fn create_coding_tools(cwd: &str, options: Option<ToolsOptions>) -> io::Result<Vec<BuiltinTool>> {
    let options = options.unwrap_or_default();
    Ok(vec![
        create_read_tool(cwd, options.read)?,
        create_bash_tool(cwd, options.bash)?,
        create_edit_tool(cwd, options.edit)?,
        create_write_tool(cwd, options.write)?,
    ])
}

/// Pi's `createReadOnlyTools(cwd, options)`: read, grep, find and ls.
pub fn create_read_only_tools(cwd: &str, options: Option<ToolsOptions>) -> io::Result<Vec<BuiltinTool>> {
    let options = options.unwrap_or_default();
    Ok(vec![
        create_read_tool(cwd, options.read)?,
        create_grep_tool(cwd, options.grep)?,
        create_find_tool(cwd, options.find)?,
        create_ls_tool(cwd, options.ls)?,
    ])
}

#[cfg(test)]
mod tests {
    use super::*;

    // The behavior of the tools is the shared conformance row TestConformance_BuiltinToolDelegationAcrossSDKs; these cases pin what the SDK does before and after
    // the host call: which options cross the wire, which functions stay, and the tool-result shape of a host result.

    #[test]
    fn serializable_options_cross_and_functions_stay() {
        let spec = read_spec(
            "/c",
            Some(ReadToolOptions {
                auto_resize_images: Some(false),
                resize_options: Some(ModelImageResizeOptions { max_width: Some(5), ..Default::default() }),
                operations: Some(ReadOperations {
                    read_file: Arc::new(|path| Ok(format!("bytes of {path}").into_bytes())),
                    access: Arc::new(|path| Err(format!("denied: {path}"))),
                    detect_image_mime_type: None,
                }),
            }),
        );
        assert_eq!(Value::Object(spec.options.clone()), json!({"autoResizeImages": false, "resizeOptions": {"maxWidth": 5}, "operations": ["readFile", "access"]}));
        let read_file = spec.callbacks["operations.readFile"].clone();
        assert_eq!(read_file(&json!({"path": "/a b"})).unwrap(), Value::String(base64(b"bytes of /a b")));
        let access = spec.callbacks["operations.access"].clone();
        assert_eq!(access(&json!({"path": "/x"})).unwrap_err(), "denied: /x");
        let handle = registry(|registry| {
            registry.callbacks.insert("test-handle".into(), spec.callbacks.clone());
            "test-handle"
        });
        assert_eq!(dispatch_callback(&json!({"handle": handle, "member": "operations.access", "args": {"path": "/y"}})), Err("denied: /y".to_string()));
        registry(|registry| registry.callbacks.remove(handle));
        assert!(dispatch_callback(&json!({"handle": handle, "member": "operations.access", "args": {"path": "/y"}})).is_err());
    }

    #[test]
    fn optional_members_are_announced_only_when_given() {
        let grep = |read_file: Option<PathOperation<String>>| {
            grep_spec("/c", Some(GrepToolOptions { operations: Some(GrepOperations { is_directory: Arc::new(|_| Ok(true)), read_file }) })).options["operations"].clone()
        };
        assert_eq!(grep(None), json!(["isDirectory"]));
        assert_eq!(grep(Some(Arc::new(|_| Ok(String::new())))), json!(["isDirectory", "readFile"]));
    }

    #[test]
    fn a_spawn_hook_and_the_shell_options_are_announced() {
        let hook: BashSpawnHook = Arc::new(Ok);
        let spec = bash_spec(
            "/c",
            Some(BashToolOptions { command_prefix: Some("set -e".into()), expose_session_environment: Some(false), spawn_hook: Some(hook), ..Default::default() }),
        );
        assert_eq!(Value::Object(spec.options.clone()), json!({"exposeSessionEnvironment": false, "spawnHook": true, "commandPrefix": "set -e"}));
        let context = json!({"command": "echo", "cwd": "/w", "env": {"A": "1"}});
        assert_eq!(spec.callbacks["spawnHook"](&context).unwrap(), context);
    }

    #[test]
    fn a_host_result_is_the_wire_object_a_tool_returns() {
        let result: AgentToolResult = serde_json::from_value(json!({
            "content": [{"type": "text", "text": ""}], "details": {"a": 1}, "structuredContent": {"exit_code": 0}, "isError": true
        }))
        .unwrap();
        assert_eq!(result.text(), "");
        let ToolResult::Json(wire) = result.into_tool_result() else { panic!("not a JSON result") };
        assert_eq!(wire, json!({"content": [{"type": "text", "text": ""}], "details": {"a": 1}, "structured_content": {"exit_code": 0}, "is_error": true}));
        assert_eq!(wire.as_object().unwrap().keys().collect::<Vec<_>>(), ["content", "details", "structured_content", "is_error"]);
    }
}

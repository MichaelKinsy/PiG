use serde_json::{Value, json};

// Native null represents the number-or-undefined exit code. Preserve its property presence before JSON encoding.
pub(crate) fn user_bash_event_result(mut value: Option<Value>) -> Option<Value> {
    if let Some(Value::Object(object)) = value.as_mut()
        && let Some(Value::Object(result)) = object.get_mut("result")
    {
        let undefined = result.get("exitCode").is_some_and(Value::is_null);
        if undefined {
            result.remove("exitCode");
        }
        if result.get("fullOutputPath").is_some_and(Value::is_null) {
            result.remove("fullOutputPath");
        }
        object.insert("_pigUserBashExitCodeUndefined".into(), json!(undefined));
    }
    value
}

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use crate::provider::ProviderSignal;

type OnData = Arc<dyn Fn(&[u8]) + Send + Sync>;

/// What `BashOperations::exec` receives (core/tools/bash.ts): `on_data` gets the command's output as it arrives, in order; `signal` is set when the host aborts the command; `timeout` is seconds; `env`, when set, replaces the inherited environment.
#[derive(Clone)]
pub struct BashExecOptions {
    pub on_data: OnData,
    pub signal: ProviderSignal,
    pub timeout: Option<f64>,
    pub env: Option<HashMap<String, String>>,
}

/// The error a [`BashExecFn`] rejects with: Pi's exec rejects with an `Error` whose class the bash tool tells (`ShellTimeoutError`, which carries the shell's own
/// exit code and run time when a command timed out after its shell had exited). Build one from a message with `From<String>` or `From<&str>`; `"aborted"` is the
/// message when the signal was set. The error [`create_local_bash_operations`](crate::create_local_bash_operations) returns for that timeout keeps the shell's exit
/// in a private field of its own, so it survives a move, a clone or a thread hop and cannot be forged: a wrapper that returns the error unchanged (for example with
/// `?`) makes the bash tool say that the shell exited with code C after S seconds, as Pi's `instanceof ShellTimeoutError` does, while one that builds a new
/// error from its message (including `error.message()` or `error.to_string()`) gets the plain `timeout:<seconds>` text.
#[derive(Debug, Clone)]
pub struct BashExecError {
    message: String,
    shell_exit: Option<crate::protocol::ShellExit>,
}

impl BashExecError {
    /// The error's message, as Pi's `error.message`.
    pub fn message(&self) -> &str {
        &self.message
    }

    /// The error of a command that timed out after its shell had exited: only the local operations make one.
    pub(crate) fn shell_timeout(message: String, shell_exit: crate::protocol::ShellExit) -> Self {
        BashExecError {
            message,
            shell_exit: Some(shell_exit),
        }
    }

    /// The error's message and its shell exit, consumed: the `user_bash_exec` error response carries both.
    pub(crate) fn into_parts(self) -> (String, Option<crate::protocol::ShellExit>) {
        (self.message, self.shell_exit)
    }
}

impl From<String> for BashExecError {
    fn from(message: String) -> Self {
        BashExecError {
            message,
            shell_exit: None,
        }
    }
}

impl From<&str> for BashExecError {
    fn from(message: &str) -> Self {
        BashExecError::from(message.to_string())
    }
}

impl std::fmt::Display for BashExecError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for BashExecError {}

/// The exec function of Pi's `BashOperations`: runs `command` in `cwd` and returns the exit code, or `None` for a failed command (report a signal termination as 128 plus the signal number). An `Err` rejects, as Pi's exec rejects; return `"aborted".into()` when the signal was set.
pub type BashExecFn =
    Arc<dyn Fn(&str, &str, BashExecOptions) -> Result<Option<i64>, BashExecError> + Send + Sync>;

/// Pi's `BashOperations`: pluggable command execution, local by default and remote (for example SSH) when a `user_bash` handler returns it from [`crate::Context::bash_operations`].
#[derive(Clone)]
pub struct BashOperations {
    pub exec: BashExecFn,
}

/// The `BashOperations` objects a user_bash reply named by handle, until the host releases them.
#[derive(Default)]
pub(crate) struct BashOperationsTable {
    inner: Mutex<(u64, HashMap<String, BashExecFn>)>,
}

impl BashOperationsTable {
    pub(crate) fn register(&self, operations: BashOperations) -> String {
        let mut inner = self.inner.lock().unwrap();
        inner.0 += 1;
        let handle = format!("bash-{}", inner.0);
        inner.1.insert(handle.clone(), operations.exec);
        handle
    }

    pub(crate) fn register_as(&self, handle: &str, operations: BashOperations) {
        self.inner
            .lock()
            .unwrap()
            .1
            .insert(handle.to_string(), operations.exec);
    }

    pub(crate) fn get(&self, handle: &str) -> Option<BashExecFn> {
        self.inner.lock().unwrap().1.get(handle).cloned()
    }

    pub(crate) fn release(&self, handle: &str) {
        self.inner.lock().unwrap().1.remove(handle);
    }
}

/// Runs the object a `user_bash_exec` request names and returns Pi's `{ exitCode }`. `notify` receives each output chunk as the base64 `tool_update` result the host decodes.
pub(crate) fn run_user_bash_exec(
    table: &BashOperationsTable,
    req: &crate::protocol::RequestMsg,
    signal: ProviderSignal,
    notify: impl Fn(Value) + Send + Sync + 'static,
) -> Result<Value, BashExecError> {
    let handle = req.tool.as_deref().unwrap_or("");
    let exec = table
        .get(handle)
        .ok_or_else(|| BashExecError::from(format!("unknown bash operations: {handle}")))?;
    let args = req.args.clone().unwrap_or(Value::Null);
    let finished = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let live = finished.clone();
    let on_data: OnData = Arc::new(move |data| {
        if !live.load(std::sync::atomic::Ordering::SeqCst) {
            notify(json!({ "data": base64(data) }));
        }
    });
    let env = args.get("env").and_then(Value::as_object).map(|object| {
        object
            .iter()
            .filter_map(|(name, value)| {
                value
                    .as_str()
                    .map(|value| (name.clone(), value.to_string()))
            })
            .collect()
    });
    let options = BashExecOptions {
        on_data,
        signal,
        timeout: args.get("timeout").and_then(Value::as_f64),
        env,
    };
    let result = exec(
        args["command"].as_str().unwrap_or(""),
        args["cwd"].as_str().unwrap_or(""),
        options,
    );
    finished.store(true, std::sync::atomic::Ordering::SeqCst);
    result.map(|exit_code| json!({ "exitCode": exit_code }))
}

pub(crate) fn base64(data: &[u8]) -> String {
    const ALPHABET: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity(data.len().div_ceil(3) * 4);
    for chunk in data.chunks(3) {
        let n = (u32::from(chunk[0]) << 16)
            | (u32::from(*chunk.get(1).unwrap_or(&0)) << 8)
            | u32::from(*chunk.get(2).unwrap_or(&0));
        out.push(ALPHABET[(n >> 18) as usize & 63] as char);
        out.push(ALPHABET[(n >> 12) as usize & 63] as char);
        out.push(if chunk.len() > 1 {
            ALPHABET[(n >> 6) as usize & 63] as char
        } else {
            '='
        });
        out.push(if chunk.len() > 2 {
            ALPHABET[n as usize & 63] as char
        } else {
            '='
        });
    }
    out
}

/// The standard alphabet with padding; None for any other input.
pub(crate) fn base64_decode(text: &str) -> Option<Vec<u8>> {
    let mut out = Vec::with_capacity(text.len() / 4 * 3);
    let bytes = text.as_bytes();
    if !bytes.len().is_multiple_of(4) {
        return None;
    }
    for quad in bytes.chunks(4) {
        let mut n = 0u32;
        let mut padding = 0;
        for &byte in quad {
            let value = match byte {
                b'A'..=b'Z' => byte - b'A',
                b'a'..=b'z' => byte - b'a' + 26,
                b'0'..=b'9' => byte - b'0' + 52,
                b'+' => 62,
                b'/' => 63,
                b'=' => {
                    padding += 1;
                    0
                }
                _ => return None,
            };
            n = (n << 6) | u32::from(value);
        }
        out.push((n >> 16) as u8);
        if padding < 2 {
            out.push((n >> 8) as u8);
        }
        if padding < 1 {
            out.push(n as u8);
        }
    }
    Some(out)
}

#[cfg(test)]
mod tests {
    use super::{base64, base64_decode};

    #[test]
    fn base64_decode_inverts_the_encoder() {
        for data in [&b""[..], b"f", b"fo", b"foo", &[0xff, 0xfe, 0xfd, 0x00]] {
            assert_eq!(base64_decode(&base64(data)).as_deref(), Some(data));
        }
        assert_eq!(base64_decode("Zg="), None);
        assert_eq!(base64_decode("Z*=="), None);
    }

    #[test]
    fn base64_matches_the_standard_alphabet_with_padding() {
        assert_eq!(base64(b""), "");
        assert_eq!(base64(b"f"), "Zg==");
        assert_eq!(base64(b"fo"), "Zm8=");
        assert_eq!(base64(b"foo"), "Zm9v");
        assert_eq!(base64(&[0xff, 0xfe, 0xfd, 0x00]), "//79AA==");
    }
}

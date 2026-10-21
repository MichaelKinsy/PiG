//! Pi's `readStoredCredential` (`core/auth-storage.ts`, exported from `index.ts`).

use serde_json::Value;
use std::io;
use std::path::PathBuf;

/// The part of Pi's `normalizePath` (`utils/paths.ts`) that `read_stored_credential`'s path can use: a leading `~` is the home directory and a `file://` URL is its path.
fn normalize_path(path: &str) -> PathBuf {
    if path == "~" || path.starts_with("~/") {
        if let Some(home) = std::env::var_os(if cfg!(windows) { "USERPROFILE" } else { "HOME" }) {
            return PathBuf::from(home).join(path[1..].trim_start_matches('/'));
        }
    }
    PathBuf::from(path.strip_prefix("file://").unwrap_or(path))
}

/// Pi's `readStoredCredential`: a one-off synchronous read of the credential stored for `provider_id` in an `auth.json` file, without a store and without resolving
/// configured key values. `auth_path` defaults to `auth.json` in the agent directory ([`crate::get_agent_dir`]); a leading `~` in it is the home directory and a `file://`
/// URL is its path. A file that cannot be read or is not a JSON object, and a provider it has no credential for, give `Ok(None)`. The credential keeps the key order of the
/// file. It fails when there is no `auth_path` and the host gave no agent directory, which Pi's default argument throws.
pub fn read_stored_credential(provider_id: &str, auth_path: Option<&str>) -> io::Result<Option<Value>> {
    let path = match auth_path {
        Some(path) => path.to_string(),
        None => PathBuf::from(crate::get_agent_dir()?).join("auth.json").to_string_lossy().into_owned(),
    };
    let Ok(bytes) = std::fs::read(normalize_path(&path)) else { return Ok(None) };
    let Ok(text) = String::from_utf8(bytes) else { return Ok(None) };
    let text = text.strip_prefix('\u{feff}').unwrap_or(&text);
    let Ok(Value::Object(mut data)) = serde_json::from_str::<Value>(text) else { return Ok(None) };
    Ok(data.remove(provider_id))
}

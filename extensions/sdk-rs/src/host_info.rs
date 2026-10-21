//! Pi's `getAgentDir()`, `CONFIG_DIR_NAME`, `VERSION` and `getPackageDir()` (`config.ts`) and the default `getShellConfig()` (`utils/shell.ts`) as the PiG host resolves them.
//!
//! They are callable while an extension's factory runs, before the host's ready message, so the host starts every extension process with
//! them in `PIG_EXT_HOST_INFO`.

use serde_json::Value;
use std::io;

const ENV: &str = "PIG_EXT_HOST_INFO";

fn unavailable(detail: &str) -> io::Error {
    io::Error::new(io::ErrorKind::NotFound, format!("the PiG host did not provide its configuration facts ({detail})"))
}

fn read(key: &str) -> io::Result<String> {
    let raw = std::env::var(ENV).map_err(|_| unavailable("PIG_EXT_HOST_INFO is not set"))?;
    let info: Value = serde_json::from_str(&raw).map_err(|err| unavailable(&format!("PIG_EXT_HOST_INFO is not valid JSON: {err}")))?;
    Ok(info.get(key).and_then(Value::as_str).unwrap_or_default().to_string())
}

/// Pi's `getAgentDir()`: the agent configuration directory the host runs with (for PiG its own tree, not Pi's). It fails when the process
/// was not started by a host that provides it, or when the host could not resolve the directory.
pub fn get_agent_dir() -> io::Result<String> {
    let dir = read("agentDir")?;
    if dir.is_empty() {
        return Err(unavailable("the host could not resolve its agent directory"));
    }
    Ok(dir)
}

/// Pi's `CONFIG_DIR_NAME`: the per-project configuration directory name the host uses (".pig"; ".pi" when it shares Pi's tree).
pub fn get_config_dir_name() -> io::Result<String> {
    read("configDirName")
}

/// Pi's `VERSION`: the Pi version the host implements, which extensions compare against to gate features.
pub fn get_version() -> io::Result<String> {
    read("version")
}

/// Pi's `getPackageDir()`: the install directory the host runs from. For PiG it is `PI_PACKAGE_DIR` when that is set, else the directory of the host executable, which is Pi's own
/// answer for a compiled binary. It fails when the process was not started by a host that provides it, or when the host could not resolve it.
pub fn get_package_dir() -> io::Result<String> {
    let dir = read("packageDir")?;
    if dir.is_empty() {
        return Err(unavailable("the host could not resolve its package directory"));
    }
    Ok(dir)
}

/// Pi's `ShellConfig` (`utils/shell.ts`): the shell binary a command runs in and its leading arguments. The command is appended as the last argument unless `command_transport`
/// is `Some("stdin")`, in which case it is written to the shell's standard input (legacy WSL `bash.exe`).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ShellConfig {
    pub shell: String,
    pub args: Vec<String>,
    pub command_transport: Option<String>,
}

/// `isLegacyWslBashPath`: `/^[a-z]:\\windows\\(?:system32|sysnative)\\bash\.exe$/` on the lowercased path with `/` as `\`. Only an ASCII letter, or the Kelvin sign that lowercases to
/// `k`, can be the drive.
fn is_legacy_wsl_bash_path(shell: &str) -> bool {
    let mut chars = shell.chars();
    let Some(drive) = chars.next() else { return false };
    if !(drive.is_ascii_alphabetic() || drive == '\u{212a}') {
        return false;
    }
    let rest: String = chars.map(|c| if c == '/' { '\\' } else { c.to_ascii_lowercase() }).collect();
    rest == ":\\windows\\system32\\bash.exe" || rest == ":\\windows\\sysnative\\bash.exe"
}

/// Pi's `getShellConfig(customShellPath)`: the shell the bash tool runs commands in. A `Some` non-empty `custom_shell_path` names the shell and must exist (`NotFound`, "Custom
/// shell path not found", otherwise). With none, it is the host's own resolution: `/bin/bash`, then bash on PATH, then `sh` on Unix; Git Bash, then `bash.exe` on PATH on Windows,
/// which is an error when none is found. It fails when the host provides no default.
pub fn get_shell_config(custom_shell_path: Option<&str>) -> io::Result<ShellConfig> {
    if let Some(path) = custom_shell_path.filter(|path| !path.is_empty()) {
        if !std::path::Path::new(path).exists() {
            return Err(io::Error::new(io::ErrorKind::NotFound, format!("Custom shell path not found: {path}")));
        }
        return Ok(if is_legacy_wsl_bash_path(path) {
            ShellConfig { shell: path.to_string(), args: vec!["-s".to_string()], command_transport: Some("stdin".to_string()) }
        } else {
            ShellConfig { shell: path.to_string(), args: vec!["-c".to_string()], command_transport: None }
        });
    }
    let raw = std::env::var(ENV).map_err(|_| unavailable("PIG_EXT_HOST_INFO is not set"))?;
    let info: Value = serde_json::from_str(&raw).map_err(|err| unavailable(&format!("PIG_EXT_HOST_INFO is not valid JSON: {err}")))?;
    let shell = info.get("shell").filter(|shell| shell.is_object()).ok_or_else(|| unavailable("the host did not provide a default shell"))?;
    if let Some(message) = shell.get("error").and_then(Value::as_str).filter(|message| !message.is_empty()) {
        return Err(io::Error::other(message.to_string()));
    }
    Ok(ShellConfig {
        shell: shell.get("shell").and_then(Value::as_str).unwrap_or_default().to_string(),
        args: shell.get("args").and_then(Value::as_array).map(|args| args.iter().filter_map(Value::as_str).map(String::from).collect()).unwrap_or_default(),
        command_transport: shell.get("commandTransport").and_then(Value::as_str).filter(|transport| !transport.is_empty()).map(String::from),
    })
}

#[cfg(test)]
mod tests {
    use super::{ENV, ShellConfig, get_agent_dir, get_config_dir_name, get_package_dir, get_shell_config, get_version, is_legacy_wsl_bash_path};

    // Go's extensions/sdk/host_info_test.go and Python's tests/test_host_info.py: the host's entry answers all three, and a process without a host entry
    // gets an error, never a made-up path.
    #[test]
    fn reads_the_entry_and_fails_without_a_host_that_provides_it() {
        let _guard = crate::extension::registration_tests::env_lock().lock().unwrap();
        let set = |value: &str| unsafe { std::env::set_var(ENV, value) };
        set(r#"{"agentDir":"/home/u/.pig/agent","configDirName":".pig","version":"1.1.0"}"#);
        let answers = (get_agent_dir().ok(), get_config_dir_name().ok(), get_version().ok());
        let mut failures = Vec::new();
        for (name, value) in [("not JSON", "{"), ("no agent directory", r#"{"configDirName":".pig"}"#)] {
            set(value);
            if get_agent_dir().is_ok() {
                failures.push(name);
            }
        }
        unsafe { std::env::remove_var(ENV) };
        let unset = get_agent_dir().is_err();
        assert_eq!(answers, (Some("/home/u/.pig/agent".to_string()), Some(".pig".to_string()), Some("1.1.0".to_string())));
        assert!(failures.is_empty(), "get_agent_dir answered for {failures:?}");
        assert!(unset, "get_agent_dir answered with no PIG_EXT_HOST_INFO");
    }

    #[test]
    fn package_dir_and_the_default_shell_come_from_the_host() {
        let _guard = crate::extension::registration_tests::env_lock().lock().unwrap();
        let set = |value: &str| unsafe { std::env::set_var(ENV, value) };
        set(r#"{"agentDir":"/a","packageDir":"/opt/pig","shell":{"shell":"/bin/bash","args":["-c"]}}"#);
        let answers = (get_package_dir().ok(), get_shell_config(None).ok());
        set(r#"{"agentDir":"/a","shell":{"error":"No bash shell found."}}"#);
        let error = get_shell_config(None).err().map(|e| e.to_string());
        let no_package_dir = get_package_dir().is_err();
        unsafe { std::env::remove_var(ENV) };
        let no_host = get_shell_config(None).is_err();
        assert_eq!(answers, (Some("/opt/pig".to_string()), Some(ShellConfig { shell: "/bin/bash".into(), args: vec!["-c".into()], command_transport: None })));
        assert_eq!(error.as_deref(), Some("No bash shell found."));
        assert!(no_package_dir && no_host);
    }

    // A custom shell path is checked here as Pi does: it must exist, and the legacy WSL bash.exe reads its command from standard input.
    #[test]
    fn a_custom_shell_path_must_exist_and_the_legacy_wsl_bash_reads_stdin() {
        let _guard = crate::extension::registration_tests::env_lock().lock().unwrap();
        unsafe { std::env::remove_var(ENV) };
        let existing = std::env::temp_dir();
        let existing = existing.to_str().unwrap();
        assert_eq!(get_shell_config(Some(existing)).unwrap(), ShellConfig { shell: existing.into(), args: vec!["-c".into()], command_transport: None });
        let missing = format!("{existing}/definitely-missing-shell");
        assert_eq!(get_shell_config(Some(&missing)).unwrap_err().to_string(), format!("Custom shell path not found: {missing}"));
        for (shell, legacy) in [
            ("C:\\Windows\\System32\\bash.exe", true), ("c:/windows/sysnative/bash.exe", true), ("D:\\WINDOWS\\SYSTEM32\\BASH.EXE", true), ("\u{212a}:\\windows\\system32\\bash.exe", true),
            ("C:\\Windows\\System32\\bash.exe ", false), ("C:\\Windows\\bash.exe", false), ("C:\\Program Files\\Git\\bin\\bash.exe", false),
            ("1:\\windows\\system32\\bash.exe", false), ("\u{130}:\\windows\\system32\\bash.exe", false), ("C:\\W\u{130}ndows\\System32\\bash.exe", false),
        ] {
            assert_eq!(is_legacy_wsl_bash_path(shell), legacy, "{shell}");
        }
    }
}

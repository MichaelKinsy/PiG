//! Pi's `getAgentDir()`, `CONFIG_DIR_NAME` and `VERSION` (`config.ts`) as the PiG host resolves them.
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

#[cfg(test)]
mod tests {
    use super::{ENV, get_agent_dir, get_config_dir_name, get_version};

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
}

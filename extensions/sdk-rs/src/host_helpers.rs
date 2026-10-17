//! Host calls for Pi's pure helpers that need the host's parsers: `parseFrontmatter` (the host's YAML parser, the one that reads skills and prompt templates)
//! and `Date.parse` (the host's port of V8's). They read no session state, so the host serves them before an extension registers, and a helper called while
//! the factory runs (before [`crate::Extension::run`]) dials the host itself: the first call opens the connection and `run` later reuses it. A call from a
//! handler uses the handler's own connection ([`crate::Context::parse_frontmatter`], [`crate::Context::parse_date`]).

use std::io;
use std::sync::{Arc, Mutex, Weak};

use serde_json::{Value, json};

use crate::protocol::Connection;
use crate::transport::UnixStream;

const CALL_PARSE_FRONTMATTER: &str = "frontmatter.parse";
const CALL_PARSE_DATE: &str = "date.parse";

/// Pi's `{ frontmatter, body }`. `frontmatter` is `parse(yaml) ?? {}`: an object for the usual mapping header, whose keys follow the order a JavaScript object
/// enumerates them (array-index keys first, ascending, then insertion order), and the array, string, number or boolean of any other YAML document.
#[derive(Debug, Clone, PartialEq)]
pub struct ParsedFrontmatter {
    pub frontmatter: Value,
    pub body: String,
}

/// The connections helpers opened before `run`, by socket path; `run` takes its own.
static DIALLED: Mutex<Vec<(String, Arc<Connection>)>> = Mutex::new(Vec::new());
/// The connections of the extensions that are running (a packed cell runs several), with the socket path each serves: a helper that has no `Context` and no
/// socket of its own uses the first that is still running.
static RUNNING: Mutex<Vec<(String, Weak<Connection>)>> = Mutex::new(Vec::new());

fn locked<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// Pi's `parseFrontmatter` (`utils/frontmatter.ts`, exported from `index.ts`), answered by the host's parser. A header the YAML parser refuses is the error Pi
/// throws. It may be called before `run`: the first call connects to the host, and `run` reuses the connection.
pub fn parse_frontmatter(content: &str) -> io::Result<ParsedFrontmatter> {
    decode_frontmatter(helper_call(CALL_PARSE_FRONTMATTER, json!({"content": content}))?)
}

/// Pi's `stripFrontmatter`: the body of [`parse_frontmatter`].
pub fn strip_frontmatter(content: &str) -> io::Result<String> {
    Ok(parse_frontmatter(content)?.body)
}

/// `Date.parse(text)` (`new Date(text).getTime()` for a string), V8's parser answered by the host: the time value in milliseconds, `None` when it is NaN. It may
/// be called before `run`, like [`parse_frontmatter`].
pub fn parse_date(text: &str) -> io::Result<Option<f64>> {
    decode_date(helper_call(CALL_PARSE_DATE, json!({"text": text}))?)
}

pub(crate) fn frontmatter_args(content: &str) -> (&'static str, Value) {
    (CALL_PARSE_FRONTMATTER, json!({"content": content}))
}

pub(crate) fn date_args(text: &str) -> (&'static str, Value) {
    (CALL_PARSE_DATE, json!({"text": text}))
}

pub(crate) fn decode_frontmatter(result: Option<Value>) -> io::Result<ParsedFrontmatter> {
    let result = result.unwrap_or(Value::Null);
    Ok(ParsedFrontmatter { frontmatter: result.get("frontmatter").cloned().unwrap_or(Value::Null), body: result.get("body").and_then(Value::as_str).unwrap_or("").to_string() })
}

pub(crate) fn decode_date(result: Option<Value>) -> io::Result<Option<f64>> {
    Ok(result.and_then(|result| result.get("ms").and_then(Value::as_f64)))
}

/// The result of a host call made outside a handler: over the running extension's connection, or over the one this call dials, which `run` reuses.
fn helper_call(method: &str, args: Value) -> io::Result<Option<Value>> {
    let running = running_for(&locked(&RUNNING), socket_path().as_deref());
    if let Some(conn) = running {
        let pending = conn.begin_call_for(None, method, Some(args))?;
        return finish(conn.wait_call(pending)?);
    }
    let path = match socket_path() {
        Some(path) => path,
        None => return Err(io::Error::new(io::ErrorKind::NotConnected, "not connected to the host: PIG_EXT_SOCKET is not set, so this extension was not launched by pig")),
    };
    let conn = {
        let mut dialled = locked(&DIALLED);
        match dialled.iter().find(|(dialled_path, _)| *dialled_path == path) {
            Some((_, conn)) => conn.clone(),
            None => {
                let conn = Arc::new(Connection::new(UnixStream::connect(&path)?));
                dialled.push((path, conn.clone()));
                conn
            }
        }
    };
    // No reader thread runs yet: read frames until this call's result, as the extension does for its first events.on.
    let pending = conn.begin_call_for(None, method, Some(args))?;
    loop {
        let env = conn.read_envelope()?;
        if conn.complete_call(&env) {
            break;
        }
    }
    finish(conn.wait_call(pending)?)
}

fn finish(result: crate::protocol::CallResultMsg) -> io::Result<Option<Value>> {
    if let Some(err) = result.error {
        return Err(io::Error::other(crate::context::host_error_message(err)));
    }
    Ok(result.result)
}

/// The socket a helper dials: `PIG_EXT_SOCKET`, which a packed cell's runner sets to the member's socket while the member's factory runs.
fn socket_path() -> Option<String> {
    std::env::var("PIG_EXT_SOCKET").or_else(|_| std::env::var("GOPI_EXT_SOCKET")).ok()
}

/// The connection a helper opened to `path`, for `run` to serve on.
pub(crate) fn take_dialled(path: &str) -> Option<Arc<Connection>> {
    let mut dialled = locked(&DIALLED);
    let at = dialled.iter().position(|(dialled_path, _)| dialled_path == path)?;
    Some(dialled.remove(at).1)
}

/// Makes the connection of a running extension the one helpers use outside a factory, so a helper called from code that has no [`crate::Context`] does not
/// dial a socket the host already accepted.
pub(crate) fn adopt(conn: &Arc<Connection>, path: &str) {
    add_running(&mut locked(&RUNNING), conn, path);
}

/// The connection a helper uses among `running`. A runner that names a socket (`PIG_EXT_SOCKET`, set to the member's own while its factory runs) means the
/// extension that socket belongs to, so a helper called by one member's factory never rides another member's connection, which the host serves only after
/// it has accepted this member's: that is the running extension on `path`, and none while this member has not connected (the helper dials it). With no
/// socket named, the first extension that is still running answers.
fn running_for(running: &[(String, Weak<Connection>)], path: Option<&str>) -> Option<Arc<Connection>> {
    match path.filter(|path| !path.is_empty()) {
        Some(path) => running.iter().filter(|(served, _)| served == path).find_map(|(_, weak)| weak.upgrade()),
        None => running.iter().find_map(|(_, weak)| weak.upgrade()),
    }
}

fn add_running(running: &mut Vec<(String, Weak<Connection>)>, conn: &Arc<Connection>, path: &str) {
    running.retain(|(_, weak)| weak.strong_count() > 0);
    running.push((path.to_string(), Arc::downgrade(conn)));
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::os::unix::net::UnixStream;

    // A packed cell runs several extensions in one process. A helper called with no Context and no socket of its own uses a running extension's connection,
    // and must keep working when the first one to start has stopped.
    #[test]
    fn a_helper_keeps_a_connection_when_the_first_running_extension_stops() {
        let (a, _a_peer) = UnixStream::pair().unwrap();
        let (b, _b_peer) = UnixStream::pair().unwrap();
        let first = Arc::new(Connection::new(a));
        let second = Arc::new(Connection::new(b));
        let mut running = Vec::new();
        add_running(&mut running, &first, "/s/e-0.sock");
        add_running(&mut running, &second, "/s/e-1.sock");
        let live = |running: &[(String, Weak<Connection>)]| running_for(running, None);
        assert!(Arc::ptr_eq(&live(&running).unwrap(), &first));
        drop(first);
        assert!(Arc::ptr_eq(&live(&running).unwrap(), &second));
        drop(second);
        assert!(live(&running).is_none());
    }

    // The runner names a member's socket while that member's factory runs. The host accepts a packed cell's members one at a time, in order, and serves a
    // connection only after the members before it: a helper called by the first member's factory that rode the second member's registered connection waited
    // for an answer the host would not send until the first member had connected.
    #[test]
    fn a_helper_named_for_a_socket_does_not_use_another_members_connection() {
        let (a, _a_peer) = UnixStream::pair().unwrap();
        let (b, _b_peer) = UnixStream::pair().unwrap();
        let first = Arc::new(Connection::new(a));
        let second = Arc::new(Connection::new(b));
        let mut running = Vec::new();
        add_running(&mut running, &second, "/s/e-1.sock");
        assert!(running_for(&running, Some("/s/e-0.sock")).is_none(), "the first member has not connected: its helper dials its own socket");
        assert!(Arc::ptr_eq(&running_for(&running, Some("/s/e-1.sock")).unwrap(), &second));
        add_running(&mut running, &first, "/s/e-0.sock");
        assert!(Arc::ptr_eq(&running_for(&running, Some("/s/e-0.sock")).unwrap(), &first));
        assert!(Arc::ptr_eq(&running_for(&running, Some("")).unwrap(), &second), "an empty name is no name");
    }
}

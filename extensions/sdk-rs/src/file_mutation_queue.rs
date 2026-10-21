//! Pi's `withFileMutationQueue` (`core/tools/file-mutation-queue.ts`) for an extension that runs in a process of its own.
//!
//! Pi has one queue in one process, shared by the built-in edit and write tools and every extension. Here the queue lives in the host: the SDK asks it for
//! the file's turn (`fileMutation.enter`, answered once the file's earlier mutations completed) and gives the turn back (`fileMutation.exit`). The SDK keeps no
//! queue and no path-key code of its own.

use std::io;
use std::path::{Component, Path, PathBuf};

use serde_json::{Value, json};

use crate::host_helpers::{helper_call, helper_call_each_running};

const CALL_FILE_MUTATION_ENTER: &str = "fileMutation.enter";
const CALL_FILE_MUTATION_EXIT: &str = "fileMutation.exit";

/// Node's `path.resolve`: absolute, with `.` and `..` removed lexically.
fn resolve(path: &Path) -> io::Result<PathBuf> {
    let joined = if path.is_absolute() { path.to_path_buf() } else { std::env::current_dir()?.join(path) };
    let mut out = PathBuf::new();
    for component in joined.components() {
        match component {
            Component::ParentDir => {
                out.pop();
            }
            Component::CurDir => {}
            other => out.push(other.as_os_str()),
        }
    }
    Ok(out)
}

/// Run `f` once every mutation registered earlier for the same file has finished, then let the next one run, and
/// return its result. Mutations of different files run in parallel.
///
/// Edit and write extension tools wrap their read-modify-write in it so parallel tool calls on one file cannot
/// interleave. The file is keyed by its real path (a symlink and its target share a queue; a path that does not
/// exist yet is keyed by its absolute path) and callers run in the order they called. A path that cannot be
/// resolved for a reason other than a missing file or directory returns that error without running `f`.
///
/// The queue is the host's: it serializes this extension's mutations with the host's built-in edit and write tools
/// and with every other extension process, as Pi's one queue does. The turn is held by the extension's process: a packed cell's
/// members share it, so one member stopping while another's `f` runs does not release the file, and the host releases it when the
/// process's last connection closes. Like the other host helpers it may be called
/// before `run`; with no connection to the host (`PIG_EXT_SOCKET` unset) it returns an error without running `f`.
pub fn with_file_mutation_queue<T>(file_path: impl AsRef<Path>, f: impl FnOnce() -> T) -> io::Result<T> {
    run_queued(&queue_call, file_path.as_ref(), f)
}

/// A queue call over the running extension's connection. The turn belongs to the host's process registry, not to the connection it was taken on: an exit whose
/// connection is stopping (its extension was replaced or stopped while the task ran) is given to another running extension's connection. With no connection of the
/// process left the host has released the turn already (it does when the last connection closes).
fn queue_call(method: &str, args: Value) -> io::Result<Option<Value>> {
    match helper_call(method, args.clone()) {
        Err(_) if method == CALL_FILE_MUTATION_EXIT => helper_call_each_running(method, args),
        reply => reply,
    }
}

/// The host call a queue turn is made with: the method and its args, answered with the call's result.
type HostCall<'a> = &'a dyn Fn(&str, Value) -> io::Result<Option<Value>>;

fn run_queued<T>(call: HostCall<'_>, file_path: &Path, f: impl FnOnce() -> T) -> io::Result<T> {
    // Pi resolves the path against the process's working directory; the host resolves nothing relative, so the SDK sends it absolute.
    let resolved = resolve(file_path)?;
    let entered = call(CALL_FILE_MUTATION_ENTER, json!({"path": resolved.to_string_lossy()}))?;
    let ticket = entered
        .as_ref()
        .and_then(|result| result.get("ticket"))
        .and_then(Value::as_str)
        .filter(|ticket| !ticket.is_empty())
        .ok_or_else(|| io::Error::new(io::ErrorKind::InvalidData, "fileMutation.enter returned no ticket"))?
        .to_string();
    // The turn is given back when `f` panics too, as Pi's `finally` does. The host also releases it when the connection closes, so a failed exit leaves nothing held.
    struct Exit<'a>(HostCall<'a>, String);
    impl Drop for Exit<'_> {
        fn drop(&mut self) {
            let _ = (self.0)(CALL_FILE_MUTATION_EXIT, json!({"ticket": self.1}));
        }
    }
    let _exit = Exit(call, ticket);
    Ok(f())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::cell::RefCell;
    use std::collections::VecDeque;

    /// A host that answers each call from a script and records the calls it was made, in order.
    struct ScriptedHost {
        calls: RefCell<Vec<(String, Value)>>,
        replies: RefCell<VecDeque<io::Result<Option<Value>>>>,
    }

    impl ScriptedHost {
        fn new(replies: Vec<io::Result<Option<Value>>>) -> ScriptedHost {
            ScriptedHost { calls: RefCell::new(Vec::new()), replies: RefCell::new(replies.into()) }
        }

        fn call(&self, method: &str, args: Value) -> io::Result<Option<Value>> {
            self.calls.borrow_mut().push((method.to_string(), args));
            self.replies.borrow_mut().pop_front().expect("a reply for every call")
        }
    }

    #[test]
    fn the_task_runs_between_enter_and_exit_and_the_path_is_sent_absolute() {
        let host = ScriptedHost::new(vec![Ok(Some(json!({"ticket": "fm-1"}))), Ok(Some(json!({})))]);
        let ran = RefCell::new(Vec::new());
        run_queued(&|method, args| host.call(method, args), Path::new("some/../a.txt"), || {
            // Inside the task only the enter has been made.
            ran.borrow_mut().push(host.calls.borrow().iter().map(|(method, _)| method.clone()).collect::<Vec<_>>());
        })
        .unwrap();
        assert_eq!(*ran.borrow(), vec![vec!["fileMutation.enter".to_string()]]);
        let calls = host.calls.borrow();
        assert_eq!(calls.len(), 2);
        let path = calls[0].1["path"].as_str().unwrap();
        assert!(Path::new(path).is_absolute() && path.ends_with("a.txt") && !path.contains(".."), "path = {path}");
        assert_eq!((calls[1].0.as_str(), &calls[1].1), ("fileMutation.exit", &json!({"ticket": "fm-1"})));
    }

    #[test]
    fn a_failed_enter_returns_the_host_error_and_does_not_run_the_task() {
        let host = ScriptedHost::new(vec![Err(io::Error::other("realpath loop: too many levels of symbolic links"))]);
        let mut ran = false;
        let err = run_queued(&|method, args| host.call(method, args), Path::new("a.txt"), || ran = true).unwrap_err();
        assert!(err.to_string().contains("too many levels of symbolic links"), "{err}");
        assert!(!ran);
        assert_eq!(host.calls.borrow().len(), 1, "no exit for a turn that was never granted");
    }

    #[test]
    fn a_panicking_task_still_exits_its_ticket() {
        let host = ScriptedHost::new(vec![Ok(Some(json!({"ticket": "fm-7"}))), Ok(Some(json!({})))]);
        let outcome = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            run_queued(&|method, args| host.call(method, args), Path::new("a.txt"), || panic!("boom"))
        }));
        assert!(outcome.is_err(), "the panic reaches the caller");
        let calls = host.calls.borrow();
        assert_eq!((calls[1].0.as_str(), &calls[1].1), ("fileMutation.exit", &json!({"ticket": "fm-7"})));
    }

    #[test]
    fn an_enter_without_a_ticket_is_an_error() {
        let host = ScriptedHost::new(vec![Ok(Some(json!({})))]);
        let err = run_queued(&|method, args| host.call(method, args), Path::new("a.txt"), || ()).unwrap_err();
        assert_eq!(err.kind(), io::ErrorKind::InvalidData);
    }
}

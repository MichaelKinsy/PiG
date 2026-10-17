//! Serialize the mutations of one file inside this extension process, as Pi's `withFileMutationQueue` does
//! (`core/tools/file-mutation-queue.ts`).

use std::collections::HashMap;
use std::io;
use std::path::{Component, Path, PathBuf};
use std::sync::{Arc, Condvar, Mutex};

/// A chain link: set once the mutation that owns it has finished.
struct Done {
    finished: Mutex<bool>,
    signal: Condvar,
}

impl Done {
    fn wait(&self) {
        let mut finished = self.finished.lock().unwrap();
        while !*finished {
            finished = self.signal.wait(finished).unwrap();
        }
    }
}

/// The registration lock and the chains: the key is resolved and the position taken under one lock, so admission
/// order equals call order, which Pi gets from its registration queue.
static QUEUES: Mutex<Option<HashMap<PathBuf, Arc<Done>>>> = Mutex::new(None);

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

fn queue_key(path: &Path) -> io::Result<PathBuf> {
    let resolved = resolve(path)?;
    match std::fs::canonicalize(&resolved) {
        Ok(real) => Ok(strip_verbatim_prefix(real)),
        Err(err) if is_missing_path_error(&err) => Ok(resolved),
        Err(err) => Err(err),
    }
}

/// Node's realpath on Windows (libuv `fs__realpath_handle`) returns the final path without the `\\?\` prefix that
/// `GetFinalPathNameByHandleW` and `canonicalize` produce, and turns `\\?\UNC\server` into `\\server`, so an existing
/// file's key equals the key of the same path before the file existed.
fn strip_verbatim_prefix(path: PathBuf) -> PathBuf {
    if !cfg!(windows) {
        return path;
    }
    match path.to_str() {
        Some(text) => PathBuf::from(strip_verbatim_text(text)),
        None => path,
    }
}

fn strip_verbatim_text(text: &str) -> String {
    if let Some(rest) = text.strip_prefix(r"\\?\UNC\") {
        format!(r"\\{rest}")
    } else if let Some(rest) = text.strip_prefix(r"\\?\") {
        rest.to_string()
    } else {
        text.to_string()
    }
}

/// Windows system errors that libuv's `uv_translate_sys_error` names ENOENT, so Node's realpath reports each as a
/// missing path: ERROR_FILE_NOT_FOUND, ERROR_PATH_NOT_FOUND, ERROR_INVALID_DRIVE, ERROR_INVALID_NAME,
/// ERROR_MOD_NOT_FOUND, ERROR_BAD_PATHNAME, ERROR_ENVVAR_NOT_FOUND, ERROR_DIRECTORY, ERROR_INVALID_REPARSE_DATA,
/// WSAHOST_NOT_FOUND and WSANO_DATA.
const WINDOWS_MISSING_PATH_ERRORS: [i32; 11] = [2, 3, 15, 123, 126, 161, 203, 267, 4392, 11001, 11004];

/// Pi's `isMissingPathError`: the error's Node code is ENOENT or ENOTDIR. On Windows that is decided by the system
/// error, not by `ErrorKind::NotFound`, which also covers ERROR_BAD_NETPATH and ERROR_BAD_NET_NAME (libuv: UNKNOWN).
fn is_missing_path_error(err: &io::Error) -> bool {
    match err.raw_os_error() {
        Some(code) if cfg!(windows) => WINDOWS_MISSING_PATH_ERRORS.contains(&code),
        _ => matches!(err.kind(), io::ErrorKind::NotFound | io::ErrorKind::NotADirectory),
    }
}

/// Run `f` once every mutation registered earlier for the same file has finished, then let the next one run, and
/// return its result. Mutations of different files run in parallel.
///
/// Edit and write extension tools wrap their read-modify-write in it so parallel tool calls on one file cannot
/// interleave. The file is keyed by its real path (a symlink and its target share a queue; a path that does not
/// exist yet is keyed by its absolute path) and callers run in the order they called. A path that cannot be
/// resolved for a reason other than a missing file or directory returns that error without running `f`. The queue
/// serializes mutations made through it inside this process only; it does not coordinate with the host's built-in
/// tools or other extension processes.
pub fn with_file_mutation_queue<T>(file_path: impl AsRef<Path>, f: impl FnOnce() -> T) -> io::Result<T> {
    let done = Arc::new(Done { finished: Mutex::new(false), signal: Condvar::new() });
    let (key, previous) = {
        let mut queues = QUEUES.lock().unwrap();
        let chains = queues.get_or_insert_with(HashMap::new);
        let key = queue_key(file_path.as_ref())?;
        let previous = chains.insert(key.clone(), done.clone());
        (key, previous)
    };
    if let Some(previous) = previous {
        previous.wait();
    }
    struct Release {
        key: PathBuf,
        done: Arc<Done>,
    }
    impl Drop for Release {
        fn drop(&mut self) {
            if let Some(chains) = QUEUES.lock().unwrap().as_mut()
                && chains.get(&self.key).is_some_and(|current| Arc::ptr_eq(current, &self.done))
            {
                chains.remove(&self.key);
            }
            *self.done.finished.lock().unwrap() = true;
            self.done.signal.notify_all();
        }
    }
    let _release = Release { key, done };
    Ok(f())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::mpsc;
    use std::time::{Duration, Instant};

    fn temp_dir(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!("pig-fmq-{name}-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        dir
    }

    fn queued_link(key: &Path) -> Option<Arc<Done>> {
        QUEUES.lock().unwrap().as_ref().and_then(|chains| chains.get(key).cloned())
    }

    /// Spin until a caller started after `previous` was observed has taken its queue position, and return its link.
    /// The first caller holds the file until every caller is admitted, so no link is released meanwhile.
    fn await_admission(key: &Path, previous: Option<&Arc<Done>>) -> Arc<Done> {
        let deadline = Instant::now() + Duration::from_secs(10);
        loop {
            if let Some(current) = queued_link(key)
                && previous.is_none_or(|previous| !Arc::ptr_eq(previous, &current))
            {
                return current;
            }
            assert!(Instant::now() < deadline, "a caller never took its queue position");
            std::thread::yield_now();
        }
    }

    #[test]
    fn same_file_callers_run_one_at_a_time_in_call_order() {
        let dir = temp_dir("order");
        let file = dir.join("a.txt");
        let key = queue_key(&file).unwrap();
        let (release_tx, release_rx) = mpsc::channel::<()>();
        let release_rx = Arc::new(Mutex::new(release_rx));
        let running = Arc::new(AtomicUsize::new(0));
        let order = Arc::new(Mutex::new(Vec::new()));
        let mut handles = Vec::new();
        let mut link: Option<Arc<Done>> = None;
        for id in 0..4 {
            let (file, running, order, release_rx) = (file.clone(), running.clone(), order.clone(), release_rx.clone());
            handles.push(std::thread::spawn(move || {
                with_file_mutation_queue(&file, || {
                    assert_eq!(running.fetch_add(1, Ordering::SeqCst), 0, "two mutations of one file overlapped");
                    if id == 0 {
                        let _ = release_rx.lock().unwrap().recv();
                    }
                    order.lock().unwrap().push(id);
                    std::thread::sleep(Duration::from_millis(5));
                    running.fetch_sub(1, Ordering::SeqCst);
                })
                .unwrap();
            }));
            link = Some(await_admission(&key, link.as_ref()));
        }
        drop(link);
        release_tx.send(()).unwrap();
        for handle in handles {
            handle.join().unwrap();
        }
        assert_eq!(*order.lock().unwrap(), vec![0, 1, 2, 3]);
        assert!(queued_link(&key).is_none(), "a drained chain is dropped");
        std::fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn a_panicking_mutation_releases_the_caller_queued_behind_it() {
        let dir = temp_dir("panic");
        let file = dir.join("a.txt");
        let key = queue_key(&file).unwrap();
        let (release_tx, release_rx) = mpsc::channel::<()>();
        let holder = std::thread::spawn({
            let file = file.clone();
            move || {
                let _ = with_file_mutation_queue(&file, || {
                    let _ = release_rx.recv();
                    panic!("boom")
                });
            }
        });
        let held = await_admission(&key, None);
        let (result_tx, result_rx) = mpsc::channel();
        std::thread::spawn({
            let file = file.clone();
            move || {
                let _ = result_tx.send(with_file_mutation_queue(&file, || 7).unwrap());
            }
        });
        drop(await_admission(&key, Some(&held)));
        drop(held);
        release_tx.send(()).unwrap();
        assert!(holder.join().is_err());
        assert_eq!(
            result_rx.recv_timeout(Duration::from_secs(10)).expect("the panicking mutation never released the caller queued behind it"),
            7
        );
        std::fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn a_path_below_a_regular_file_is_keyed_by_its_absolute_path() {
        let dir = temp_dir("enotdir");
        let file = dir.join("a.txt");
        std::fs::write(&file, b"").unwrap();
        let below = file.join("child");
        assert_eq!(queue_key(&below).unwrap(), resolve(&below).unwrap());
        std::fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn verbatim_prefixes_are_stripped_like_libuv_realpath() {
        assert_eq!(strip_verbatim_text(r"\\?\C:\work\a.txt"), r"C:\work\a.txt");
        assert_eq!(strip_verbatim_text(r"\\?\UNC\server\share\a.txt"), r"\\server\share\a.txt");
        assert_eq!(strip_verbatim_text(r"C:\work\a.txt"), r"C:\work\a.txt");
        assert_eq!(strip_verbatim_text("/work/a.txt"), "/work/a.txt");
    }

    /// Node keys an existing file by realpath and a missing one by its resolved path; on Windows both are the same
    /// string, so a file created while its queue is held keeps the queue.
    #[test]
    fn a_created_file_keeps_the_key_it_had_while_missing() {
        let dir = queue_key(&temp_dir("created")).unwrap();
        let file = dir.join("new.txt");
        let missing = queue_key(&file).unwrap();
        std::fs::write(&file, b"").unwrap();
        assert_eq!(queue_key(&file).unwrap(), missing);
        std::fs::remove_dir_all(&dir).unwrap();
    }

    #[test]
    fn missing_path_errors_follow_the_node_code() {
        assert!(is_missing_path_error(&io::Error::from(io::ErrorKind::NotFound)));
        assert!(is_missing_path_error(&io::Error::from(io::ErrorKind::NotADirectory)));
        assert!(!is_missing_path_error(&io::Error::from(io::ErrorKind::PermissionDenied)));
        // ERROR_INVALID_NAME and ERROR_DIRECTORY are ENOENT in libuv; ERROR_BAD_NETPATH is UNKNOWN.
        assert_eq!(is_missing_path_error(&io::Error::from_raw_os_error(123)), cfg!(windows));
        assert_eq!(is_missing_path_error(&io::Error::from_raw_os_error(267)), cfg!(windows));
        if cfg!(windows) {
            assert!(!is_missing_path_error(&io::Error::from_raw_os_error(53)));
        }
    }
}

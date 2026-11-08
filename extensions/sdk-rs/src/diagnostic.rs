//! The extension's log, where the SDK writes its own diagnostics.
//!
//! The host forwards the extension process's stderr to the user's terminal, and Pi writes no stack for a throwing handler (print mode reports one line, interactive mode shows
//! one), so a diagnostic of the SDK's own must not go there: the stack of a panic the SDK caught, a host call it could not make, a frame it dropped. The host names the
//! process's log in `PIG_EXT_LOG` (the log its load and crash diagnostics read, which also receives everything the process writes to stderr), and the SDK appends one whole
//! diagnostic per write. With no log, or one that cannot be written, the diagnostic goes to stderr, as it does when no host started the process. What the extension's own
//! code writes, and what the runtime writes for an uncaught panic, stays on stderr.

use std::cell::Cell;
use std::ffi::OsStr;
use std::fs::OpenOptions;
use std::io::{self, Write};
use std::panic::{AssertUnwindSafe, PanicHookInfo, catch_unwind};
use std::sync::Once;
use std::sync::atomic::{AtomicU64, Ordering};

const EXT_LOG_ENV: &str = "PIG_EXT_LOG";

/// The most the SDK writes to the log. The host bounds what a process writes to stderr, and a handler that panics on every event must not fill the disk through this path.
/// The diagnostic that crosses the bound is the last.
const LOG_MAX: u64 = 1 << 20;

static WRITTEN: AtomicU64 = AtomicU64::new(0);

thread_local! {
    /// How many [`recover`] boundaries the thread is inside: a panic raised there is one the SDK catches and reports itself.
    static RECOVERING: Cell<usize> = const { Cell::new(0) };
}

/// Writes one SDK-owned diagnostic, which carries its own newline.
pub(crate) fn diagnose(text: &str) {
    let path = std::env::var_os(EXT_LOG_ENV).filter(|path| !path.is_empty());
    write_diagnostic(&WRITTEN, path.as_deref(), text.as_bytes());
}

/// `written` counts what the process asked to write to the log, in bytes.
fn write_diagnostic(written: &AtomicU64, path: Option<&OsStr>, bytes: &[u8]) {
    if let Some(path) = path {
        let total = written.fetch_add(bytes.len() as u64, Ordering::SeqCst) + bytes.len() as u64;
        if total > LOG_MAX {
            if total - bytes.len() as u64 <= LOG_MAX {
                let _ = append(path, b"\n[... further SDK diagnostics dropped ...]\n");
            }
            return;
        }
        if append(path, bytes).is_ok() {
            return;
        }
    }
    let _ = io::stderr().write_all(bytes);
}

fn append(path: &OsStr, text: &[u8]) -> io::Result<()> {
    OpenOptions::new().append(true).open(path)?.write_all(text)
}

/// `catch_unwind` for a panic the SDK catches and reports itself, with the report in the log: the hook the SDK installs writes what the default hook would write to stderr
/// to the log for a panic raised inside, and leaves every other panic to the hook that was there.
pub(crate) fn recover<R>(f: impl FnOnce() -> R) -> std::thread::Result<R> {
    install_panic_hook();
    RECOVERING.with(|depth| depth.set(depth.get() + 1));
    let outcome = catch_unwind(AssertUnwindSafe(f));
    RECOVERING.with(|depth| depth.set(depth.get() - 1));
    outcome
}

fn install_panic_hook() {
    static INSTALLED: Once = Once::new();
    INSTALLED.call_once(|| {
        let previous = std::panic::take_hook();
        std::panic::set_hook(Box::new(move |info| {
            if RECOVERING.with(Cell::get) == 0 {
                previous(info);
            } else {
                diagnose(&panic_report(info));
            }
        }));
    });
}

/// The text the default hook writes for a panic: where it happened and what it said, and the backtrace when `RUST_BACKTRACE` asks for one.
fn panic_report(info: &PanicHookInfo<'_>) -> String {
    let payload = info.payload();
    let message = payload
        .downcast_ref::<&str>()
        .map(|text| (*text).to_string())
        .or_else(|| payload.downcast_ref::<String>().cloned())
        .unwrap_or_else(|| "Box<dyn Any>".to_string());
    let thread = std::thread::current();
    let location = info
        .location()
        .map(|at| format!("{}:{}:{}", at.file(), at.line(), at.column()))
        .unwrap_or_else(|| "<unknown>".to_string());
    let backtrace = std::backtrace::Backtrace::capture();
    let trace = if backtrace.status() == std::backtrace::BacktraceStatus::Captured {
        format!("stack backtrace:\n{backtrace}\n")
    } else {
        "note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n"
            .to_string()
    };
    format!(
        "extension: recovered from panic: thread '{}' panicked at {location}:\n{message}\n{trace}",
        thread.name().unwrap_or("<unnamed>")
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn log_in(name: &str) -> (std::path::PathBuf, std::path::PathBuf) {
        let dir = std::env::temp_dir().join(format!("pig-sdk-diag-{name}-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("ext.log");
        std::fs::write(&path, "host wrote this line\n").unwrap();
        (dir, path)
    }

    /// A panic inside [`recover`] is reported in the log PIG_EXT_LOG names, after the line the host wrote, and the panic reaches the caller as a value. This is the only
    /// test that sets the variable; other tests' recovered panics may append to the same log, so it asserts what the log holds and not what it lacks.
    #[test]
    fn a_recovered_panic_is_reported_in_the_log() {
        let (dir, path) = log_in("recover");
        // SAFETY: no other test reads or writes PIG_EXT_LOG, and the tests that start threads that read it only append to whatever log it names.
        unsafe { std::env::set_var(EXT_LOG_ENV, &path) };
        let outcome = recover(|| panic!("probe-panic-boom"));
        unsafe { std::env::remove_var(EXT_LOG_ENV) };
        assert!(outcome.is_err());
        let log = std::fs::read_to_string(&path).unwrap();
        assert!(
            log.starts_with("host wrote this line\n"),
            "the host's line stays where it is: {log}"
        );
        assert!(log.contains("recovered from panic: thread"), "{log}");
        assert!(log.contains("panicked at"), "{log}");
        assert!(log.contains("probe-panic-boom"), "{log}");
        let _ = std::fs::remove_dir_all(dir);
    }

    #[test]
    fn the_log_is_bounded() {
        let (dir, path) = log_in("bound");
        let written = AtomicU64::new(0);
        let chunk = format!("{}\n", "x".repeat(1 << 16));
        for _ in 0..(2 * LOG_MAX as usize / chunk.len()) {
            write_diagnostic(&written, Some(path.as_os_str()), chunk.as_bytes());
        }
        let log = std::fs::read_to_string(&path).unwrap();
        assert!(
            log.len() as u64 <= LOG_MAX + 1024,
            "the log is {} bytes",
            log.len()
        );
        assert_eq!(log.matches("further SDK diagnostics dropped").count(), 1);
        let _ = std::fs::remove_dir_all(dir);
    }
}

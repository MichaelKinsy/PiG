"""The extension's log, where the SDK writes its own diagnostics.

The host forwards the extension process's stderr to the user's terminal, and Pi writes no stack for a throwing handler (print mode reports one line, interactive mode shows
one), so a diagnostic of the SDK's own must not go there: the traceback of a handler that raised, a host call that failed, a frame the SDK dropped. The host names the
process's log in ``PIG_EXT_LOG`` (the log its load and crash diagnostics read, which also receives everything the process writes to stderr), and the SDK appends one whole
diagnostic per write. With no log, or one that cannot be written, the diagnostic goes to stderr, as it does when no host started the process. What the extension's own code
writes stays on stderr.
"""
import os
import sys
import threading

_ENV = "PIG_EXT_LOG"

# The most the SDK writes to the log. The host bounds what a process writes to stderr, and a handler that fails on every event must not fill the disk through this path.
# The diagnostic that crosses the bound is the last.
_LOG_MAX = 1 << 20

_lock = threading.Lock()
_written = 0


def diagnose(text: str) -> None:
    """Write one SDK-owned diagnostic, which carries its own newline. It never raises: a diagnostic that cannot be written does not end the extension."""
    global _written
    path = os.environ.get(_ENV, "")
    if path:
        data = text.encode("utf-8", "replace")
        with _lock:
            before = _written
            _written += len(data)
        if before + len(data) > _LOG_MAX:
            if before <= _LOG_MAX:
                _append(path, b"\n[... further SDK diagnostics dropped ...]\n")
            return
        if _append(path, data):
            return
    try:
        sys.stderr.write(text)
        sys.stderr.flush()
    except (OSError, ValueError):
        pass


def _append(path: str, data: bytes) -> bool:
    try:
        descriptor = os.open(path, os.O_WRONLY | os.O_APPEND)
    except OSError:
        return False
    try:
        # One write, so a diagnostic stays whole beside the host's own writes to the log.
        os.write(descriptor, data)
        return True
    except OSError:
        return False
    finally:
        os.close(descriptor)

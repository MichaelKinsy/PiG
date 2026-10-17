"""Serialize the mutations of one file inside this extension process, as Pi's ``withFileMutationQueue`` does (core/tools/file-mutation-queue.ts)."""
import errno
import os
import threading
from typing import Callable, Dict, TypeVar

T = TypeVar("T")

_registration = threading.Lock()
_chains: Dict[str, threading.Event] = {}


# Windows system errors that libuv's uv_translate_sys_error names ENOENT, so Node's realpath reports each as a missing path: ERROR_FILE_NOT_FOUND,
# ERROR_PATH_NOT_FOUND, ERROR_INVALID_DRIVE, ERROR_INVALID_NAME, ERROR_MOD_NOT_FOUND, ERROR_BAD_PATHNAME, ERROR_ENVVAR_NOT_FOUND, ERROR_DIRECTORY,
# ERROR_INVALID_REPARSE_DATA, WSAHOST_NOT_FOUND and WSANO_DATA.
_WINDOWS_MISSING_PATH_ERRORS = frozenset({2, 3, 15, 123, 126, 161, 203, 267, 4392, 11001, 11004})


def _is_missing_path_error(error: OSError) -> bool:
    """Pi's ``isMissingPathError``: the error's Node code is ENOENT or ENOTDIR (on Windows, decided by the system error)."""
    winerror = getattr(error, "winerror", None)
    if winerror is not None:
        return winerror in _WINDOWS_MISSING_PATH_ERRORS
    return error.errno in (errno.ENOENT, errno.ENOTDIR)


def _queue_key(file_path: str) -> str:
    resolved = os.path.abspath(file_path)
    try:
        return os.path.realpath(resolved, strict=True)
    except OSError as error:
        if _is_missing_path_error(error):
            return resolved
        raise


def with_file_mutation_queue(file_path: str, fn: Callable[[], T]) -> T:
    """Run ``fn`` once every mutation registered earlier for the same file has finished, then let the next one run, and return its result.

    Mutations of different files run in parallel. Edit and write extension tools wrap their read-modify-write in it so parallel tool calls on one
    file cannot interleave. The file is keyed by its real path (a symlink and its target share a queue; a path that does not exist yet is keyed by
    its absolute path) and callers run in the order they called. A path that cannot be resolved for a reason other than a missing file or directory
    raises that ``OSError`` without running ``fn``. The queue serializes mutations made through it inside this process only; it does not coordinate
    with the host's built-in tools or other extension processes.
    """
    # The key is resolved and the position taken under one lock: admission order equals call order, which Pi gets from its registration queue.
    with _registration:
        key = _queue_key(file_path)
        previous = _chains.get(key)
        done = threading.Event()
        _chains[key] = done
    if previous is not None:
        previous.wait()
    try:
        return fn()
    finally:
        with _registration:
            if _chains.get(key) is done:
                del _chains[key]
        done.set()

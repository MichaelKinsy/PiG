"""fatal-replace-native.py is the native (Python SDK) member of the pair that fatal_exit_extension_processes_linux_test.go runs, next to
fatal-replace.mjs: every Session start appends "python <reason> <pid>" to PIG_TEST_REPLACE_LOG, and the process takes a long time to end
once its input closes, as an extension with a slow exit handler does. A pig that exits without ending its extension processes leaves it
running for that time."""
import atexit
import os
import time

import pig_sdk


def _record(reason):
    # newline="" keeps the "\n" record separator on Windows, where text mode writes "\r\n".
    with open(os.environ["PIG_TEST_REPLACE_LOG"], "a", newline="") as log:
        log.write("python %s %d\n" % (reason, os.getpid()))


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("fatal-replace-native")
    ext.on_event("session_start", lambda ctx, data: _record(data.get("reason")))
    return ext


atexit.register(lambda: time.sleep(1.5))

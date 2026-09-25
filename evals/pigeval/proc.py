# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Run one harness process with a timeout and collect its resource usage."""
import os
import signal
import subprocess
import tempfile
import threading
import time


def run(argv, env, cwd, timeout=120.0):
    """Return wall and CPU seconds, peak RSS in MiB (largest process in the tree), exit code, and output."""
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        start = time.perf_counter()
        try:
            child = subprocess.Popen(argv, env=env, cwd=cwd, stdin=subprocess.DEVNULL, stdout=out, stderr=err, start_new_session=True)
        except OSError as error:
            return {"code": 127, "wall": 0.0, "cpu": 0.0, "rss_mib": 0.0, "stdout": "", "stderr": str(error), "timed_out": False}
        expired = threading.Event()

        def kill():
            expired.set()
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except OSError:
                pass

        timer = threading.Timer(timeout, kill)
        timer.start()
        try:
            _, status, usage = os.wait4(child.pid, 0)  # Usage includes descendants the harness waited for.
        finally:
            timer.cancel()
        wall = time.perf_counter() - start
        child.returncode = os.waitstatus_to_exitcode(status)
        try:
            os.killpg(child.pid, signal.SIGKILL)  # Stop helpers a harness left running so runs stay independent.
        except OSError:
            pass
        out.seek(0)
        err.seek(0)
        return {"code": child.returncode, "wall": wall, "cpu": usage.ru_utime + usage.ru_stime, "rss_mib": usage.ru_maxrss / 1024,
                "stdout": out.read().decode(errors="replace"), "stderr": err.read().decode(errors="replace")[-4000:], "timed_out": expired.is_set()}

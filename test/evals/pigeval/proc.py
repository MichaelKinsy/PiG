# SPDX-License-Identifier: MIT
"""Run one harness process with a timeout and collect its resource usage."""
import os
import signal
import subprocess
import sys
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
        peak = _HwmSampler(child.pid) if os.path.isdir("/proc/self") else None
        try:
            if peak:  # Stop sampling before the reap frees the pid for reuse.
                os.waitid(os.P_PID, child.pid, os.WEXITED | os.WNOWAIT)
                peak.stop()
            _, status, usage = os.wait4(child.pid, 0)  # Usage includes descendants the harness waited for.
        finally:
            timer.cancel()
            if peak:
                peak.stop()
        wall = time.perf_counter() - start
        child.returncode = os.waitstatus_to_exitcode(status)
        try:
            os.killpg(child.pid, signal.SIGKILL)  # Stop helpers a harness left running so runs stay independent.
        except OSError:
            pass
        out.seek(0)
        err.seek(0)
        return {"code": child.returncode, "wall": wall, "cpu": usage.ru_utime + usage.ru_stime, "rss_mib": _rss_mib(usage, peak),
                "stdout": out.read().decode(errors="replace"), "stderr": err.read().decode(errors="replace")[-4000:], "timed_out": expired.is_set()}


def _rss_mib(usage, peak):
    """Peak RSS of the largest process in the tree, in MiB. Linux: its VmHWM, sampled while it runs; not ru_maxrss, because exec keeps
    hiwater_rss, so a child forked from this (larger) harness would report the harness's peak. Elsewhere ru_maxrss (macOS bytes)."""
    if peak is not None:
        return peak.kib / 1024
    return usage.ru_maxrss / (1048576 if sys.platform == "darwin" else 1024)


class _HwmSampler:
    """Largest VmHWM over the tree rooted at pid, read from /proc every INTERVAL_S until stop(); the last sample before exit counts."""

    INTERVAL_S = 0.02

    def __init__(self, pid):
        self.pid, self.kib, self._done = pid, 0, threading.Event()
        self._thread = threading.Thread(target=self._loop, daemon=True)
        self._thread.start()

    def _loop(self):
        while True:
            self._sample()
            if self._done.wait(self.INTERVAL_S):
                return

    def _sample(self):
        todo = [self.pid]
        while todo:
            pid = todo.pop()
            try:
                with open(f"/proc/{pid}/status") as f:
                    for line in f:
                        if line.startswith("VmHWM:"):
                            self.kib = max(self.kib, int(line.split()[1]))
                            break
                for task in os.listdir(f"/proc/{pid}/task"):
                    with open(f"/proc/{pid}/task/{task}/children") as f:
                        todo.extend(int(c) for c in f.read().split())
            except (OSError, ValueError):
                continue

    def stop(self):
        self._done.set()
        self._thread.join()

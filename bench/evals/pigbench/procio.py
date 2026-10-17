# SPDX-License-Identifier: MIT
"""Start a harness inside a meter and talk to it: batch print runs, JSONL RPC, and a PTY.

The POSIX terminal modules are imported by Pty only, so batch and RPC runs also work on Windows."""
import json
import os
import select
import struct
import subprocess
import tempfile
import threading
import time

from . import cgroup
from .mock import now_ns


def wait(meter, proc, timeout):
    """Wait for the process; True if it exited before the timeout. Uses wait4 under the rusage meter."""
    try:
        if isinstance(meter, cgroup.RusageMeter):
            meter.wait(timeout)
        else:
            proc.wait(timeout)
        return True
    except subprocess.TimeoutExpired:
        return False


class Batch:
    """One non-interactive process: spawn, wait, collect resources. Output goes to files, so a chatty harness cannot block on a pipe."""

    def __init__(self, argv, env, cwd, meter, timeout=120):
        self.argv, self.env, self.cwd, self.meter, self.timeout = argv, env, cwd, meter, timeout

    def run(self):
        with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
            self.t_spawn = now_ns()
            try:
                self.proc = self.meter.popen(self.argv, env=self.env, cwd=self.cwd, stdin=subprocess.DEVNULL, stdout=out, stderr=err, start_new_session=True)
            except OSError as error:
                return {"code": 127, "timed_out": False, "stdout": "", "stderr": str(error), "t_spawn": self.t_spawn, "t_exit": now_ns()}
            done = wait(self.meter, self.proc, self.timeout)
            self.t_exit = now_ns()
            if not done:
                self.meter.kill()
                wait(self.meter, self.proc, 10)
            result = self.collect()
            out.seek(0)
            err.seek(0)
            result.update({"timed_out": not done, "stdout": out.read().decode(errors="replace"), "stderr": err.read().decode(errors="replace")[-4000:]})
            return result

    def collect(self):
        meter = self.meter
        return {"code": self.proc.returncode, "t_spawn": self.t_spawn, "t_exit": self.t_exit, "wall_ms": (self.t_exit - self.t_spawn) / 1e6, "cpu_ms": meter.cpu_ms(),
                "peak_mem_mib": meter.peak_mem_mib(), "max_proc_rss_mib": meter.max_proc_rss_mib(), "meter": meter.mode}


class Rpc:
    """A harness in `--mode rpc`: JSON Lines on stdin and stdout, every output record time-stamped on arrival."""

    def __init__(self, argv, env, cwd, meter):
        self.meter = meter
        self.records = []  # (t_ns, dict)
        self.cond = threading.Condition()
        self.stderr = tempfile.TemporaryFile()
        self.t_spawn = now_ns()
        self.proc = meter.popen(argv, env=env, cwd=cwd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.stderr, start_new_session=True)
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()
        self._id = 0

    def _read(self):
        for line in self.proc.stdout:
            t = now_ns()
            try:
                record = json.loads(line)
            except ValueError:
                continue
            with self.cond:
                self.records.append((t, record))
                self.cond.notify_all()

    def send(self, command):
        self._id += 1
        command = dict(command, id=f"b{self._id}")
        self.proc.stdin.write((json.dumps(command) + "\n").encode())
        self.proc.stdin.flush()
        return command["id"], now_ns()

    def wait_for(self, predicate, timeout, start=0):
        """Return (t_ns, record, next_index) of the first record at or after `start` matching predicate, or None on timeout or exit."""
        deadline = time.monotonic() + timeout
        i = start
        with self.cond:
            while True:
                while i < len(self.records):
                    t, record = self.records[i]
                    i += 1
                    if predicate(record):
                        return t, record, i
                if self.proc.poll() is not None and i >= len(self.records):
                    return None
                left = deadline - time.monotonic()
                if left <= 0:
                    return None
                self.cond.wait(min(left, 0.25))

    def close(self, grace=10):
        try:
            self.proc.stdin.close()
        except OSError:
            pass
        if not wait(self.meter, self.proc, grace):
            self.meter.kill()
            wait(self.meter, self.proc, 10)
        self.t_exit = now_ns()
        self.stderr.seek(0)
        self.stderr_text = self.stderr.read().decode(errors="replace")[-4000:]
        self.stderr.close()


# Terminal queries a TUI sends at startup and the replies a real terminal gives. Every arm gets the same answers.
QUERIES = [
    (b"\x1b[c", b"\x1b[?62;c"),
    (b"\x1b[>c", b"\x1b[>0;0;0c"),
    (b"\x1b[?u", b"\x1b[?0u"),
    (b"\x1b[6n", b"\x1b[1;1R"),
    (b"\x1b[?2026$p", b"\x1b[?2026;2$y"),
    (b"\x1b[?996n", b"\x1b[?997;1n"),
]


def new_queries(tail, data, query):
    """Occurrences of `query` in tail + data that end inside `data`: a query wholly inside the carried-over tail was answered with the previous chunk."""
    window = tail + data
    n, start = 0, window.find(query)
    while start != -1:
        if start + len(query) > len(tail):
            n += 1
        start = window.find(query, start + 1)
    return n


class Pty:
    """A harness on a pseudo-terminal of fixed size. A reader thread time-stamps each chunk and answers terminal queries."""

    def __init__(self, argv, env, cwd, meter, rows=40, cols=120, echo=True):
        """echo=False starts the terminal without the line discipline's echo: before a TUI enters raw mode the kernel would echo typed keys
        itself, so a key on screen would not prove that the application read it. Raw mode turns echo off anyway, so a TUI sees no difference."""
        import fcntl
        import pty
        import termios
        self.meter = meter
        self.chunks = []  # (t_ns, bytes)
        self.cond = threading.Condition()
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        if not echo:
            attrs = termios.tcgetattr(slave)
            attrs[3] &= ~(termios.ECHO | termios.ECHONL)
            termios.tcsetattr(slave, termios.TCSANOW, attrs)
        self.master = master
        env = dict(env, COLUMNS=str(cols), LINES=str(rows))
        env.pop("NO_COLOR", None)

        def controlling_tty():
            fcntl.ioctl(0, termios.TIOCSCTTY, 0)

        self.t_spawn = now_ns()
        self.proc = meter.popen(argv, env=env, cwd=cwd, stdin=slave, stdout=slave, stderr=slave, start_new_session=True, preexec_fn=controlling_tty)
        os.close(slave)
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()

    def _read(self):
        tail = b""
        while True:
            try:
                ready, _, _ = select.select([self.master], [], [], 0.25)
                if not ready:
                    if self.proc.poll() is not None:
                        return
                    continue
                data = os.read(self.master, 65536)
            except OSError:
                return
            if not data:
                return
            t = now_ns()
            for query, reply in QUERIES:
                n = new_queries(tail, data, query)
                if n:
                    try:
                        os.write(self.master, reply * n)
                    except OSError:
                        pass
            tail = data[-16:]
            with self.cond:
                self.chunks.append((t, data))
                self.cond.notify_all()

    def type(self, text):
        os.write(self.master, text.encode() if isinstance(text, str) else text)

    def text(self):
        with self.cond:
            return b"".join(c for _, c in self.chunks).decode(errors="replace")

    def wait_text(self, needle, timeout, since=0):
        """Time (ns) at which the output first contained `needle` in chunks from index `since`, or None."""
        deadline = time.monotonic() + timeout
        with self.cond:
            while True:
                seen = b""
                for t, c in self.chunks[since:]:
                    seen += c
                    if needle.encode() in seen:
                        return t
                left = deadline - time.monotonic()
                if left <= 0:
                    return None
                self.cond.wait(min(left, 0.25))

    def wait_quiet(self, quiet_s, timeout):
        """Time (ns) of the last chunk once no output has arrived for quiet_s seconds; None on timeout."""
        deadline = time.monotonic() + timeout
        with self.cond:
            while True:
                last = self.chunks[-1][0] if self.chunks else None
                if last is not None and (now_ns() - last) / 1e9 >= quiet_s:
                    return last
                left = deadline - time.monotonic()
                if left <= 0:
                    return None
                self.cond.wait(min(0.05, left))

    def close(self, grace=10):
        if self.proc.poll() is None:
            self.meter.kill()
        wait(self.meter, self.proc, grace)
        try:
            os.close(self.master)
        except OSError:
            pass

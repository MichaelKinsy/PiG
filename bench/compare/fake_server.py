#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Starts a fake model server on a port that is bound before the server process exists.

Picking a free port, closing it and letting the server bind it later leaves a window in which another process can take the port. With
many parallel samples on one host, a sample's client then talks to another sample's server, which that sample kills mid-stream: the run
records a connection error or a truncated record that the code under test did not cause. Here the harness binds and listens first and
hands the listening socket to the server process, which serves on it; the port is never free between the choice and the first request.

The fake model scripts (Pi-Bolt's fake_model*.py, fake_model_ctx.py) build `ThreadingHTTPServer(("127.0.0.1", PORT), Handler)` in their
`__main__` block. Run as a program, this module replaces http.server.ThreadingHTTPServer by a subclass that adopts the inherited socket
instead of binding the address, then runs the script unchanged:

  fake_server.py --listen-fd FD SCRIPT [SCRIPT ARGS...]   (SCRIPT sees argv [SCRIPT, "0", ARGS...]; the port argument is ignored)
"""
import contextlib
import http.server
import runpy
import socket
import subprocess
import sys
import time
from pathlib import Path


def listener() -> socket.socket:
    """A listening TCP socket on 127.0.0.1 with a kernel-chosen port, inheritable by a child process."""
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    try:
        sock.bind(("127.0.0.1", 0))
        sock.listen(128)
        sock.set_inheritable(True)
    except BaseException:
        sock.close()
        raise
    return sock


def start(script, *args, **popen) -> tuple[subprocess.Popen, int]:
    """Starts SCRIPT serving on a freshly bound listener and returns the process and its port. The port is bound before the call
    returns, so a client may connect at once: the kernel queues the connection until the server accepts it."""
    sock = listener()
    try:
        port = sock.getsockname()[1]
        fd = sock.fileno()
        proc = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "--listen-fd", str(fd), str(script), *map(str, args)],
                                pass_fds=(fd,), **popen)
    finally:
        sock.close()
    return proc, port


@contextlib.contextmanager
def serve(script, *args, start_timeout: float = 60):
    """Yields the port of SCRIPT serving on its own bound listener; stops the server on exit. Waits until the server accepts a
    connection, so a server that fails to start is reported here and not as the first request's error."""
    proc, port = start(script, *args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        deadline = time.time() + start_timeout
        while not served(port):
            if proc.poll() is not None or time.time() > deadline:
                raise RuntimeError(f"the fake model server ({Path(script).name}) did not start on port {port}")
            time.sleep(0.05)
        yield port
    finally:
        proc.terminate()
        proc.wait()


def served(port: int) -> bool:
    """Whether a server answers on PORT: an HTTP request gets a status line back (a connection alone is accepted by the backlog)."""
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=0.5) as conn:
            conn.sendall(b"GET /v1/models HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
            return conn.recv(5) == b"HTTP/"
    except OSError:
        return False


def fake_model(h, pace_ms: float = 0, script: str = "fake_model.py"):
    """Pi-Bolt harness.fake_model on a bound-first listener: H is Pi-Bolt's bench/harness.py module."""
    return serve(Path(h.HERE) / script, *(["--pace-ms", pace_ms] if pace_ms else []))


def _adopting_server(fd: int):
    base = http.server.ThreadingHTTPServer

    class AdoptingServer(base):
        """ThreadingHTTPServer serving on the inherited listening socket FD; the requested address is ignored."""

        def __init__(self, server_address, handler_class, bind_and_activate=True):
            super().__init__(server_address, handler_class, bind_and_activate=False)
            self.socket.close()
            self.socket = socket.socket(fileno=fd)
            self.server_address = self.socket.getsockname()
            self.server_name, self.server_port = self.server_address[0], self.server_address[1]

    return AdoptingServer


def main(argv: list[str]) -> None:
    if len(argv) < 3 or argv[0] != "--listen-fd":
        raise SystemExit("usage: fake_server.py --listen-fd FD SCRIPT [ARGS...]")
    fd, script, rest = int(argv[1]), argv[2], argv[3:]
    http.server.ThreadingHTTPServer = _adopting_server(fd)
    sys.argv = [script, "0", *rest]
    sys.path.insert(0, str(Path(script).resolve().parent))
    runpy.run_path(script, run_name="__main__")


if __name__ == "__main__":
    main(sys.argv[1:])

"""A weakref finalizer that releases a host object must not take the write lock.

Python runs a finalizer on whichever thread triggers garbage collection. When that thread is inside `_send` holding the
non-reentrant `_write_lock`, a finalizer that sends would deadlock it, and every later frame, the pong included, would
block behind it. The finalizers queue their frame instead, and the next send or read writes it.
"""

from __future__ import annotations

import gc
import json
import socket
import struct
import threading
import weakref

import pig_sdk
from pig_sdk import autocomplete, provider


def _read_frame(conn: socket.socket) -> dict:
    size = struct.unpack(">I", conn.recv(4, socket.MSG_WAITALL))[0]
    return json.loads(conn.recv(size, socket.MSG_WAITALL))


def _connected() -> tuple[pig_sdk.Extension, socket.socket]:
    ext = pig_sdk.Extension("finalizer-test")
    ext._sock, host = socket.socketpair()
    host.settimeout(5)
    return ext, host


def _send_while_collecting(ext: pig_sdk.Extension, finalize) -> None:
    """Send one frame and run the finalizer while the sending thread holds the write lock, as a GC during serialization does."""
    original = ext._send_locked
    pending = [finalize]

    def send_locked(env):
        if pending:
            pending.pop()()
        original(env)

    ext._send_locked = send_locked
    done = threading.Event()
    thread = threading.Thread(target=lambda: (ext._send({"type": "notify", "notify": {"method": "probe", "args": None}}), done.set()), daemon=True)
    thread.start()
    assert done.wait(2), "_send deadlocked when a finalizer ran on the thread holding the write lock"
    ext._send_locked = original


def test_autocomplete_release_from_a_finalizer_inside_send_is_written_after_it():
    ext, host = _connected()
    _send_while_collecting(ext, lambda: autocomplete._release_current(weakref.ref(ext), "p1"))
    frames = [_read_frame(host), _read_frame(host)]
    assert frames[0]["notify"]["method"] == "probe"
    assert frames[1] == {"type": "notify", "notify": {"method": "ui.autocomplete.release", "args": {"id": "p1"}}}


def test_provider_release_from_a_finalizer_inside_send_is_written_after_it():
    ext, host = _connected()
    _send_while_collecting(ext, lambda: provider._release_provider(weakref.ref(ext), "h1", "t1"))
    frames = [_read_frame(host), _read_frame(host)]
    assert frames[0]["notify"]["method"] == "probe"
    assert frames[1] == {"type": "call", "call": {"method": "provider.release", "args": {"handle": "h1", "token": "t1"}}}


def test_a_collected_autocomplete_provider_queues_its_release():
    ext, host = _connected()

    class Ctx:
        extension = ext

    gc.disable()
    try:
        current = autocomplete._CurrentProvider(Ctx(), {"id": "p2"})
        current.cycle = current
        del current
        gc.collect()
    finally:
        gc.enable()
    ext._flush_finalizer_frames()
    assert _read_frame(host) == {"type": "notify", "notify": {"method": "ui.autocomplete.release", "args": {"id": "p2"}}}

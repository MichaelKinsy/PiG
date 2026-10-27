"""The function options of create_mcp_extension: the shims that serve a transport and a credential store to the host (pig_sdk/mcp_options.py)."""
import threading
import time
from types import SimpleNamespace

import pytest

import pig_sdk
from pig_sdk.mcp_options import _CredentialsShim, _TransportShim, _register


def context():
    return SimpleNamespace(extension=SimpleNamespace(_state_lock=threading.RLock()))


class Transport(pig_sdk.McpTransportEvents):
    def __init__(self):
        super().__init__()
        self.sends, self.version, self.aborted = [], None, threading.Event()

    def start(self):
        pass

    def send(self, message, signal):
        self.sends.append(message)
        if message == {"slow": True}:
            assert signal.wait(5)
            self.aborted.set()
            raise RuntimeError("aborted")

    def close(self):
        self.emit_close()

    def set_protocol_version(self, version):
        self.version = version


def test_receive_answers_everything_queued_in_order_and_only_the_first_close():
    transport = Transport()
    shim = _TransportShim(context(), transport)
    transport.emit_message({"n": 1})
    transport.emit_error(RuntimeError("hiccup"))
    transport.emit_message({"n": 2})
    transport.emit_close()
    transport.emit_message({"n": 3})
    assert shim.serve("receive") == {"events": [
        {"type": "message", "message": {"n": 1}}, {"type": "error", "error": "hiccup"}, {"type": "message", "message": {"n": 2}}, {"type": "close"},
    ]}


def test_a_poll_waits_for_the_next_event_and_close_drops_the_callback():
    ctx = context()
    transport = Transport()
    shim = _TransportShim(ctx, transport)
    answers = []
    poll = threading.Thread(target=lambda: answers.append(shim.serve("receive")))
    poll.start()
    time.sleep(0.05)
    assert answers == []
    transport.emit_message({"n": 1})
    poll.join(5)
    assert answers == [{"events": [{"type": "message", "message": {"n": 1}}]}]
    assert shim.id in ctx.extension._graph_callbacks
    shim.serve("close")
    assert shim.id not in ctx.extension._graph_callbacks


def test_send_reports_placed_and_the_abort_op_sets_the_signal():
    transport = Transport()
    shim = _TransportShim(context(), transport)
    shim.serve("send", 1, {"fast": True}, True)
    failed = []

    def slow():
        try:
            shim.serve("send", 2, {"slow": True}, True)
        except RuntimeError as error:
            failed.append(str(error))

    worker = threading.Thread(target=slow)
    worker.start()
    while 2 not in shim._aborts:
        time.sleep(0.001)
    shim.serve("abort", 2)
    worker.join(5)
    assert failed == ["aborted"] and transport.aborted.is_set()
    assert shim.serve("receive") == {"events": [{"type": "placed", "seq": 1}, {"type": "placed", "seq": 2}]}
    shim.serve("setProtocolVersion", "2025-06-18")
    assert transport.version == "2025-06-18"


class Store:
    def __init__(self):
        self.held = False

    def load(self):
        return None

    def save(self, state):
        pass

    def with_refresh_lock(self, fn):
        self.held = True
        try:
            return fn()
        finally:
            self.held = False


class Credentials:
    def __init__(self, store):
        self.store = store

    def for_server(self, name, server_url):
        return self.store

    def tokens(self, name, server_url):
        return None

    def remove(self, name, server_url):
        return False


def test_refresh_lock_is_held_until_the_host_unlocks():
    store = Store()
    shim = _CredentialsShim(Credentials(store))
    store_id = shim.serve("forServer", "demo", "https://x.example")["store"]
    lock = shim.serve("lock", store_id)["lock"]
    assert store.held
    with pytest.raises(RuntimeError, match="refresh failed"):
        shim.serve("unlock", store_id, lock, "refresh failed", True)
    assert not store.held
    with pytest.raises(RuntimeError, match="unknown refresh lock"):
        shim.serve("unlock", store_id, lock, "", False)


def test_register_names_each_function_and_decodes_nothing_it_was_not_given():
    ctx = context()
    wire = {}
    _register(ctx, {"load_config": lambda c: {"servers": [], "errors": []}, "update_config": lambda entry, patch: None}, wire)
    assert sorted(wire) == ["loadConfig", "updateConfig"]
    assert ctx.extension._graph_callbacks[wire["loadConfig"]]({"cwd": "/p", "isProjectTrusted": True}) == {"servers": [], "errors": []}

"""The ready payload is a connection's first state, and the host writes no state before it. A state notify that reaches an SDK before ready is still applied
to the replica, never dropped and never a load failure, whichever read takes it: a factory-time call on the connection the factory dialled, the registration of a
listener ``run`` sends before the register frame, or the wait for ready.

The Go SDK's tests for the same contract are extensions/sdk/state_before_ready_test.go."""

from __future__ import annotations

import threading
from typing import Any

import pytest
from test_event_bus_factory import _FactoryHost
from test_sdk import _read_frame_raw, _write_frame

import pig_sdk


def _state(name: str) -> dict[str, Any]:
    """A state_update that names the session: a value the replica's fallback, "", differs from."""
    return {"type": "notify", "notify": {"method": "state_update", "args": {"state": {"session": {"sessionId": "early", "sessionName": name}}}}}


def _run(ext: pig_sdk.Extension, path: str, failures: list[BaseException]) -> threading.Thread:
    def serve() -> None:
        try:
            ext.run_with_socket(path)
        except BaseException as exc:  # noqa: BLE001 - the run's failure is the test's evidence
            failures.append(exc)

    runner = threading.Thread(target=serve, daemon=True)
    runner.start()
    return runner


def _session_name_after_ready(fake: _FactoryHost, ext: pig_sdk.Extension, runner: threading.Thread, failures: list[BaseException]) -> str:
    """Send a ready payload that carries no state, wait until the run has handled it, and return the session name the replica holds: what the early state brought."""
    assert fake.conn is not None
    try:
        _write_frame(fake.conn, {"type": "ready", "ready": {"cwd": fake.dir, "width": 80}})
        _write_frame(fake.conn, {"type": "ping", "ping": {"nonce": "after-ready"}})
        pong = _read_frame_raw(fake.conn)
    except (OSError, EOFError, ValueError) as exc:
        runner.join(timeout=5)
        pytest.fail(f"the run ended before it handled the ready: {failures or exc!r}")
    assert pong["type"] == "pong", pong
    name = ext._session_name
    _write_frame(fake.conn, {"type": "shutdown", "shutdown": {"reason": "test"}})
    runner.join(timeout=5)
    assert not failures, failures
    return name


def test_a_state_ahead_of_a_factory_calls_result_is_applied(monkeypatch: pytest.MonkeyPatch) -> None:
    fake = _FactoryHost(monkeypatch, [])
    try:
        ext = pig_sdk.Extension("state-before-register")
        subscription = threading.Thread(target=lambda: ext.events.on("ch", lambda ctx, data: None), daemon=True)
        subscription.start()
        fake.thread.join(timeout=15)
        assert fake.conn is not None, "the factory never connected"
        call = _read_frame_raw(fake.conn)
        assert call["type"] == "call" and call["call"]["method"] == "events.on", call
        # The host flushed its state ahead of the result of the factory's call.
        _write_frame(fake.conn, _state("named before register"))
        _write_frame(fake.conn, {"type": "call_result", "id": call["id"], "call_result": {"result": None}})
        subscription.join(timeout=5)
        assert not subscription.is_alive()
        failures: list[BaseException] = []
        runner = _run(ext, fake.path, failures)
        assert _read_frame_raw(fake.conn)["type"] == "register"
        assert _session_name_after_ready(fake, ext, runner, failures) == "named before register"
    finally:
        fake.close()


def test_a_state_ahead_of_the_result_of_a_listener_registered_at_run_is_applied(monkeypatch: pytest.MonkeyPatch) -> None:
    # With no host to dial, the listener waits for run, which registers it on its own connection before the register frame.
    monkeypatch.delenv("PIG_EXT_SOCKET", raising=False)
    ext = pig_sdk.Extension("state-before-listener-result")
    ext.events.on("ch", lambda ctx, data: None)
    fake = _FactoryHost(monkeypatch, [])
    try:
        failures: list[BaseException] = []
        runner = _run(ext, fake.path, failures)
        fake.thread.join(timeout=15)
        assert fake.conn is not None, "the run never connected"
        call = _read_frame_raw(fake.conn)
        assert call["type"] == "call" and call["call"]["method"] == "events.on", call
        try:
            _write_frame(fake.conn, _state("named before the listener's result"))
            _write_frame(fake.conn, {"type": "call_result", "id": call["id"], "call_result": {"result": None}})
            register = _read_frame_raw(fake.conn)
        except (OSError, EOFError, ValueError) as exc:
            runner.join(timeout=5)
            pytest.fail(f"the run ended before it registered: {failures or exc!r}")
        assert register["type"] == "register", register
        assert _session_name_after_ready(fake, ext, runner, failures) == "named before the listener's result"
    finally:
        fake.close()


def test_a_state_between_the_register_and_the_ready_is_applied_and_the_load_goes_on(monkeypatch: pytest.MonkeyPatch) -> None:
    fake = _FactoryHost(monkeypatch, [])
    try:
        ext = pig_sdk.Extension("state-before-ready")
        failures: list[BaseException] = []
        runner = _run(ext, fake.path, failures)
        fake.thread.join(timeout=15)
        assert fake.conn is not None, "the run never connected"
        assert _read_frame_raw(fake.conn)["type"] == "register"
        _write_frame(fake.conn, _state("named before ready"))
        assert _session_name_after_ready(fake, ext, runner, failures) == "named before ready"
    finally:
        fake.close()

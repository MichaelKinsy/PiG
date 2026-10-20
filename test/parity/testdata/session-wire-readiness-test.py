#!/usr/bin/env python3
"""Deterministic regressions for the readiness barrier of the modal-shutdown probe and the busy session of the disconnect probe."""
import contextlib
import importlib.util
from pathlib import Path
import signal
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location("session_wire", Path(__file__).with_name("session-wire.py"))
WIRE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(WIRE)


class ModalReadinessTest(unittest.TestCase):
    def test_banner_does_not_release_the_command_ready_barrier(self):
        states = []
        state = None

        class Process:
            status = None

            def poll(self):
                return self.status

            def send_signal(self, value):
                self.status = 0
                state["signal"] = value

            def wait(self):
                return self.status

        def setup(root, _url):
            nonlocal state
            state = {"root": root, "reads": 0, "writes": [], "signal": None}
            states.append(state)
            return root, {}, []

        @contextlib.contextmanager
        def child(command, **_kwargs):
            state["command"] = command
            yield Process()

        def read(_fd, _size):
            state["reads"] += 1
            if state["reads"] == 1:
                return b"wire-model"
            if state["reads"] == 2:
                # The model footer is already visible, but command startup has not settled until this independent completion acknowledgement.
                (state["root"] / "ready").write_text("ready")
                return b"startup work completed"
            return b"Model catalogs refreshed."

        def write(_fd, data):
            self.assertTrue((state["root"] / "ready").exists(),
                            "modal command submitted before startup completion acknowledgement")
            state["writes"].append(data)
            return len(data)

        with tempfile.TemporaryDirectory() as directory, contextlib.ExitStack() as stack:
            replacements = {
                "setup": setup,
                "child": child,
                "pty.openpty": lambda: (101, 102),
                "fcntl.ioctl": lambda *_args: None,
                "os.close": lambda _fd: None,
                "select.select": lambda *_args: ([101], [], []),
                "os.read": read,
                "os.write": write,
                "emit": lambda _value: None,
            }
            for name, replacement in replacements.items():
                stack.enter_context(mock.patch.object(WIRE, name, replacement) if "." not in name
                                    else mock.patch("session_wire." + name, replacement))
            WIRE.modal_shutdown_probe(["pig-fixture"], Path(directory))
        self.assertEqual([s["signal"] for s in states], [signal.SIGHUP, signal.SIGTERM])
        for observed in states:
            self.assertEqual(observed["writes"], [b"/scoped-models\r"])
            self.assertEqual(observed["command"][-1], "/wire-ready")
            self.assertEqual(observed["reads"], 3)


class TerminalDisconnectBusyTest(unittest.TestCase):
    def test_hangup_arrives_while_the_session_is_still_writing(self):
        """An idle Pi exits 0 at EOF without writing; only a write after the hangup exits 129 (10-terminal-disconnect-exit)."""
        seen = {}

        class Process:
            def poll(self):
                return 129 if seen.get("closed") else None

            def wait(self):
                return 129

        def setup(root, _url):
            return root, {}, []

        @contextlib.contextmanager
        def child(command, **kwargs):
            seen["env"] = kwargs["env"]
            seen["extension"] = Path(command[command.index("-e") + 1]).read_text()
            yield Process()

        def close(_fd):
            seen["closed"] = True

        with tempfile.TemporaryDirectory() as directory, contextlib.ExitStack() as stack:
            replacements = {
                "setup": setup,
                "child": child,
                "wait_command_ready": lambda *_args: bytearray(),
                "pty.openpty": lambda: (101, 102),
                "fcntl.ioctl": lambda *_args: None,
                "os.close": close,
                "emit": lambda _value: None,
            }
            for name, replacement in replacements.items():
                stack.enter_context(mock.patch.object(WIRE, name, replacement) if "." not in name
                                    else mock.patch("session_wire." + name, replacement))
            WIRE.terminal_probe(["pig-fixture"], Path(directory), "disconnect")
        self.assertEqual(seen["env"]["WIRE_BUSY"], "1")
        self.assertIn("setInterval", seen["extension"])
        self.assertIn("ctx.ui.setStatus", seen["extension"])
        self.assertIn('on("session_shutdown", () => clearInterval(ticker))', seen["extension"])

    def test_modal_probe_fixture_stays_idle(self):
        env = {}
        with tempfile.TemporaryDirectory() as directory:
            WIRE.command_ready_fixture(Path(directory), env)
        self.assertNotIn("WIRE_BUSY", env)


if __name__ == "__main__":
    import sys
    sys.modules["session_wire"] = WIRE
    unittest.main()

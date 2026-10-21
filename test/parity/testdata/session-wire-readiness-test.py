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


class CommandReadyBarrierTest(unittest.TestCase):
    """The barrier ends on the marker, on pig's exit or on the hang bound; it has no wall-clock deadline of its own."""

    class Process:
        def __init__(self, exits_after=None):
            self.polls, self.exits_after, self.returncode = 0, exits_after, None

        def poll(self):
            self.polls += 1
            if self.exits_after is not None and self.polls > self.exits_after:
                self.returncode = 3
            return self.returncode

    def run_barrier(self, process, ready, chunks):
        reads = iter(chunks)
        with mock.patch.object(WIRE.select, "select", lambda *_args: ([101], [], [])), \
                mock.patch.object(WIRE.os, "read", lambda *_args: next(reads)):
            return WIRE.wait_command_ready(101, process, ready)

    def test_barrier_has_no_wall_clock_deadline(self):
        with tempfile.TemporaryDirectory() as directory:
            ready = Path(directory) / "ready"
            polls = 0

            def chunk():
                nonlocal polls
                polls += 1
                if polls == 40:
                    ready.write_text("ready")
                return b"."

            class Chunks:
                def __iter__(self):
                    return self

                def __next__(self):
                    return chunk()

            # A clock that has run an hour per reading: any deadline measured against it has long passed.
            ticks = iter(range(0, 10 ** 9, 3600))
            with mock.patch.object(WIRE.time, "monotonic", lambda: next(ticks)):
                output = self.run_barrier(self.Process(), ready, Chunks())
        self.assertEqual(bytes(output), b"." * 40)

    def test_pig_exit_ends_the_barrier_with_its_status_and_output(self):
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(AssertionError) as raised:
                self.run_barrier(self.Process(exits_after=2), Path(directory) / "ready", [b"banner", b"more", b"unused"])
        self.assertEqual(raised.exception.args[0][:2], ("pig exited before the command-ready barrier", 3))
        self.assertEqual(raised.exception.args[0][2], b"bannermore")

    def test_hang_bound_ends_the_barrier_with_the_output_read_so_far(self):
        def chunks():
            yield b"banner"
            raise WIRE.Hung("SIGTERM")

        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaises(AssertionError) as raised:
                self.run_barrier(self.Process(), Path(directory) / "ready", chunks())
        self.assertEqual(raised.exception.args[0],
                         ("command-ready barrier not reached before the hang bound", "SIGTERM", b"banner"))

    def test_sigterm_handler_raises_hung(self):
        with self.assertRaises(WIRE.Hung):
            WIRE.hang_bound_reached(signal.SIGTERM, None)


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

    def test_repo_disconnect_hangs_up_an_idle_session_in_a_git_repository(self):
        seen = {"git": []}

        class Process:
            def poll(self):
                return 129 if seen.get("closed") else None

            def wait(self):
                return 129

        @contextlib.contextmanager
        def child(command, **kwargs):
            seen["env"] = kwargs["env"]
            seen["controlling_tty"] = "preexec_fn" in kwargs
            yield Process()

        def git(command, **kwargs):
            seen["git"].append((command, kwargs["cwd"]))

        def close(_fd):
            seen["closed"] = True

        with tempfile.TemporaryDirectory() as directory, contextlib.ExitStack() as stack:
            replacements = {
                "setup": lambda root, _url: (root, {}, []),
                "child": child,
                "wait_command_ready": lambda *_args: bytearray(),
                "pty.openpty": lambda: (101, 102),
                "fcntl.ioctl": lambda *_args: None,
                "os.close": close,
                "emit": lambda value: seen.setdefault("emitted", []).append(value),
            }
            for name, replacement in replacements.items():
                stack.enter_context(mock.patch.object(WIRE, name, replacement) if "." not in name
                                    else mock.patch("session_wire." + name, replacement))
            stack.enter_context(mock.patch("session_wire.subprocess.run", git))
            WIRE.terminal_probe(["pig-fixture"], Path(directory), "disconnect-repo")
        self.assertEqual(seen["git"], [(["git", "init", "-q"], Path(directory))])
        self.assertNotIn("WIRE_BUSY", seen["env"])
        self.assertTrue(seen["controlling_tty"])
        self.assertEqual(seen["emitted"], [{"case": "disconnect-repo", "returncode": 129}])

    def test_modal_probe_fixture_stays_idle(self):
        env = {}
        with tempfile.TemporaryDirectory() as directory:
            WIRE.command_ready_fixture(Path(directory), env)
        self.assertNotIn("WIRE_BUSY", env)


if __name__ == "__main__":
    import sys
    sys.modules["session_wire"] = WIRE
    unittest.main()

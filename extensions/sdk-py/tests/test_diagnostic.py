"""An SDK diagnostic goes to the log the host names in PIG_EXT_LOG and not to stderr, which the host forwards to the terminal (Go's sdk/diagnostic_test.go, Rust's
diagnostic.rs tests). Pi writes no stack for a throwing handler."""

from __future__ import annotations

import contextlib
import io
import os
import tempfile
import unittest
from unittest import mock

from pig_sdk import _diagnostic


class Diagnostic(unittest.TestCase):
    def setUp(self) -> None:
        self.dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.dir.cleanup)
        self.path = os.path.join(self.dir.name, "ext.log")
        with open(self.path, "w", encoding="utf-8") as log:
            log.write("host wrote this line\n")
        _diagnostic._written = 0
        self.addCleanup(setattr, _diagnostic, "_written", 0)

    def diagnose_to(self, text: str, path: str) -> str:
        stderr = io.StringIO()
        with mock.patch.dict(os.environ, {"PIG_EXT_LOG": path}), contextlib.redirect_stderr(stderr):
            _diagnostic.diagnose(text)
        return stderr.getvalue()

    def test_goes_to_the_log_and_not_to_stderr(self) -> None:
        stderr = self.diagnose_to("pig: on_loaded handler failed:\nTraceback (most recent call last):\n", self.path)
        self.assertEqual(stderr, "")
        with open(self.path, encoding="utf-8") as log:
            self.assertEqual(log.read(), "host wrote this line\npig: on_loaded handler failed:\nTraceback (most recent call last):\n")

    def test_falls_back_to_stderr_without_a_writable_log(self) -> None:
        self.assertEqual(self.diagnose_to("pig: unwritable\n", os.path.join(self.dir.name, "missing", "ext.log")), "pig: unwritable\n")
        stderr = io.StringIO()
        with mock.patch.dict(os.environ, {}, clear=False), contextlib.redirect_stderr(stderr):
            os.environ.pop("PIG_EXT_LOG", None)
            _diagnostic.diagnose("pig: no host\n")
        self.assertEqual(stderr.getvalue(), "pig: no host\n")

    def test_the_log_is_bounded(self) -> None:
        chunk = "x" * (1 << 16) + "\n"
        for _ in range(2 * _diagnostic._LOG_MAX // len(chunk)):
            self.assertEqual(self.diagnose_to(chunk, self.path), "")
        size = os.path.getsize(self.path)
        self.assertLessEqual(size, _diagnostic._LOG_MAX + 1024)
        with open(self.path, encoding="utf-8") as log:
            self.assertEqual(log.read().count("further SDK diagnostics dropped"), 1)


if __name__ == "__main__":
    unittest.main()

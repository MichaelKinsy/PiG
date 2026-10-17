# SPDX-License-Identifier: MIT
"""Tests of the pin-follow watcher: it follows each new upstream main commit once."""
import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import watch  # noqa: E402

OLD = "a" * 40
NEW = "b" * 40


class WatchTests(unittest.TestCase):
    def test_head_is_parsed_from_ls_remote_output(self):
        self.assertEqual(watch.head(f"{NEW}\trefs/heads/main\n"), NEW)
        with self.assertRaises(ValueError):
            watch.head("")

    def test_a_commit_is_new_until_it_has_a_record_or_an_attempt(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.assertTrue(watch.is_new(root, NEW))
            (root / "automation/pin/records").mkdir(parents=True)
            (root / "automation/pin/records/1.1.0+aaaaaaaa.json").write_text(json.dumps({"commit": OLD}))
            self.assertTrue(watch.is_new(root, NEW))
            self.assertFalse(watch.is_new(root, OLD))
            watch.mark_attempt(root, NEW)
            self.assertFalse(watch.is_new(root, NEW), "a commit whose follow failed is retried by hand, not on every poll")

    def test_poll_follows_a_new_head_once(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls = []
            run = lambda command: calls.append(command) or 0  # noqa: E731
            self.assertEqual(watch.poll(root, f"{NEW}\trefs/heads/main", run), 0)
            self.assertEqual(len(calls), 1)
            self.assertIn(NEW[:8], calls[0])
            self.assertTrue(any(part.endswith("follow.py") for part in calls[0]))
            self.assertEqual(watch.poll(root, f"{NEW}\trefs/heads/main", run), 0)
            self.assertEqual(len(calls), 1)

    def test_an_unreachable_remote_is_one_line_and_a_nonzero_exit_not_a_traceback(self):
        """The poller log held two tracebacks from transient ls-remote failures; the next poll retries."""
        import contextlib
        import io
        import subprocess
        from unittest import mock
        failing = subprocess.CalledProcessError(128, ["git", "ls-remote"], stderr="fatal: unable to access")
        for error in (failing, subprocess.TimeoutExpired(["git", "ls-remote"], 60)):
            err = io.StringIO()
            with mock.patch.object(watch.subprocess, "run", side_effect=error), contextlib.redirect_stderr(err):
                self.assertEqual(watch.main([]), 1)
            self.assertEqual(len(err.getvalue().strip().splitlines()), 1, err.getvalue())
            self.assertIn("ls-remote", err.getvalue())

    def test_a_timeout_reports_its_stderr_text_not_a_bytes_literal(self):
        """subprocess.run's TimeoutExpired holds the raw bytes read so far on POSIX, also with text=True."""
        import contextlib
        import io
        import subprocess
        from unittest import mock
        err = io.StringIO()
        timeout = subprocess.TimeoutExpired(["git", "ls-remote"], 60, stderr=b"fatal: unable to access\n")
        with mock.patch.object(watch.subprocess, "run", side_effect=timeout), contextlib.redirect_stderr(err):
            self.assertEqual(watch.main([]), 1)
        self.assertIn("retries: fatal: unable to access", err.getvalue())
        self.assertNotIn("b'", err.getvalue())

    def test_a_failed_follow_is_reported_and_not_retried(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls = []
            run = lambda command: calls.append(command) or 3  # noqa: E731
            self.assertEqual(watch.poll(root, f"{NEW}\trefs/heads/main", run), 3)
            self.assertEqual(watch.poll(root, f"{NEW}\trefs/heads/main", run), 0)
            self.assertEqual(len(calls), 1)


if __name__ == "__main__":
    unittest.main()

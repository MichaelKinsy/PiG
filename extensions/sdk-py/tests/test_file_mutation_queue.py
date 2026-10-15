"""with_file_mutation_queue keys and releases a file as Pi's withFileMutationQueue does (core/tools/file-mutation-queue.ts).

The concurrent behavior is the shared conformance row TestConformance_FileMutationQueueAgreesAcrossSDKs; these cases pin the key and the error
classification, which differ per platform.
"""

from __future__ import annotations

import errno
import os
import sys
import tempfile
import threading
import unittest

from pig_sdk import file_mutation_queue as fmq


class FileMutationQueueTest(unittest.TestCase):
    def test_a_path_below_a_regular_file_is_keyed_by_its_absolute_path(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            file = os.path.join(directory, "a.txt")
            open(file, "w").close()
            below = os.path.join(file, "child")
            self.assertEqual(fmq._queue_key(below), os.path.abspath(below))

    def test_missing_path_errors_follow_the_node_code(self) -> None:
        self.assertTrue(fmq._is_missing_path_error(FileNotFoundError(errno.ENOENT, "missing")))
        self.assertTrue(fmq._is_missing_path_error(NotADirectoryError(errno.ENOTDIR, "not a directory")))
        self.assertFalse(fmq._is_missing_path_error(OSError(errno.ELOOP, "loop")))
        if sys.platform == "win32":
            # ERROR_INVALID_NAME and ERROR_DIRECTORY are ENOENT in libuv; ERROR_BAD_NETPATH is UNKNOWN.
            self.assertTrue(fmq._is_missing_path_error(OSError(None, "invalid name", None, 123)))
            self.assertTrue(fmq._is_missing_path_error(OSError(None, "directory", None, 267)))
            self.assertFalse(fmq._is_missing_path_error(OSError(None, "bad netpath", None, 53)))

    def test_a_failing_mutation_releases_the_caller_queued_behind_it(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            file = os.path.join(directory, "a.txt")
            key = fmq._queue_key(file)
            release = threading.Event()
            first_error: list[BaseException] = []

            def failing() -> None:
                release.wait(10)
                raise RuntimeError("boom")

            def hold() -> None:
                try:
                    fmq.with_file_mutation_queue(file, failing)
                except RuntimeError as error:
                    first_error.append(error)

            holder = threading.Thread(target=hold)
            holder.start()
            held = self._await_admission(key, None)
            result: list[int] = []
            waiter = threading.Thread(target=lambda: result.append(fmq.with_file_mutation_queue(file, lambda: 7)), daemon=True)
            waiter.start()
            self._await_admission(key, held)
            release.set()
            holder.join(10)
            waiter.join(10)
            self.assertEqual(len(first_error), 1)
            self.assertEqual(result, [7], "the failing mutation never released the caller queued behind it")
            self.assertNotIn(key, fmq._chains, "a drained chain is dropped")

    def _await_admission(self, key: str, previous: threading.Event | None) -> threading.Event:
        """Spin until a caller started after ``previous`` was observed has taken its queue position, and return its link."""
        for _ in range(10_000):
            with fmq._registration:
                current = fmq._chains.get(key)
            if current is not None and current is not previous:
                return current
            threading.Event().wait(0.001)
        self.fail("a caller never took its queue position")


if __name__ == "__main__":
    unittest.main()

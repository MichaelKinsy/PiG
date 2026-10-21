"""with_file_mutation_queue asks the host's queue for the file's turn, as Pi's withFileMutationQueue (core/tools/file-mutation-queue.ts) takes it from the one queue.

The cross-process behavior is the shared conformance row TestConformance_FileMutationQueueSharedWithTheHost and the order row
TestConformance_FileMutationQueueAgreesAcrossSDKs; these cases pin the SDK's side of the wire against a scripted host.
"""

from __future__ import annotations

import os
import unittest
from typing import Any
from unittest import mock

from pig_sdk import HostCallError, file_mutation_queue as fmq, host_helpers


class ScriptedHost:
    """Answers each helper call from a script and records the calls, in order."""

    def __init__(self, *replies: Any) -> None:
        self.calls: list[tuple[str, Any]] = []
        self._replies = list(replies)

    def __call__(self, method: str, args: Any) -> Any:
        self.calls.append((method, args))
        reply = self._replies.pop(0)
        if isinstance(reply, BaseException):
            raise reply
        return reply


class FileMutationQueueTest(unittest.TestCase):
    def test_the_task_runs_between_enter_and_exit_and_the_path_is_sent_absolute(self) -> None:
        host = ScriptedHost({"ticket": "fm-1"}, {})
        seen: list[list[str]] = []
        with mock.patch.object(host_helpers, "_helper_call", host):
            result = fmq.with_file_mutation_queue(os.path.join("some", "..", "a.txt"), lambda: seen.append([m for m, _ in host.calls]) or 7)
        self.assertEqual(result, 7)
        self.assertEqual(seen, [["fileMutation.enter"]], "the task runs after the enter and before the exit")
        self.assertEqual(host.calls[0], ("fileMutation.enter", {"path": os.path.abspath("a.txt")}))
        self.assertEqual(host.calls[1], ("fileMutation.exit", {"ticket": "fm-1"}))

    def test_a_failing_task_still_exits_and_its_error_reaches_the_caller(self) -> None:
        host = ScriptedHost({"ticket": "fm-2"}, {})

        def failing() -> None:
            raise ValueError("boom")

        with mock.patch.object(host_helpers, "_helper_call", host):
            with self.assertRaises(ValueError):
                fmq.with_file_mutation_queue("a.txt", failing)
        self.assertEqual([m for m, _ in host.calls], ["fileMutation.enter", "fileMutation.exit"])

    def test_a_failed_enter_raises_the_host_error_and_does_not_run_the_task(self) -> None:
        host = ScriptedHost(HostCallError("realpath loop: too many levels of symbolic links"))
        ran: list[bool] = []
        with mock.patch.object(host_helpers, "_helper_call", host):
            with self.assertRaises(HostCallError):
                fmq.with_file_mutation_queue("a.txt", lambda: ran.append(True))
        self.assertEqual(ran, [])
        self.assertEqual(len(host.calls), 1, "no exit for a turn that was never granted")

    def test_a_failed_exit_does_not_replace_the_task_result(self) -> None:
        host = ScriptedHost({"ticket": "fm-3"}, RuntimeError("extension connection is closed or replaced"))
        with mock.patch.object(host_helpers, "_helper_call", host):
            self.assertEqual(fmq.with_file_mutation_queue("a.txt", lambda: 9), 9)

    def test_an_exit_whose_connection_stopped_is_given_to_another_running_extension(self) -> None:
        """A packed cell's members share one process and the host's ticket registry for it: when the member whose connection carried the enter stops while another
        member's task holds the turn, the exit must still reach the host over a member that runs, or the file stays held until the whole process ends."""
        host = ScriptedHost({"ticket": "fm-4"}, RuntimeError("extension connection is closed or replaced"))
        stopped, running = mock.Mock(), mock.Mock()
        stopped._call.side_effect = RuntimeError("extension connection is closed or replaced")
        running._call.return_value = {"result": {}}
        with mock.patch.object(host_helpers, "_helper_call", host), mock.patch.object(host_helpers._State, "running", [stopped, running]):
            self.assertEqual(fmq.with_file_mutation_queue("a.txt", lambda: 5), 5)
        running._call.assert_called_once_with("fileMutation.exit", {"ticket": "fm-4"})

    def test_an_enter_without_a_ticket_is_an_error(self) -> None:
        host = ScriptedHost({})
        with mock.patch.object(host_helpers, "_helper_call", host):
            with self.assertRaises(RuntimeError):
                fmq.with_file_mutation_queue("a.txt", lambda: None)


if __name__ == "__main__":
    unittest.main()

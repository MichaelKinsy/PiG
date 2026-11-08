# SPDX-License-Identifier: MIT
"""Tests for automation/ci/bookkeeping_shards.py: which bookkeeping outcomes stop the CI shards."""
from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

CI = Path(__file__).resolve().parent
sys.path.insert(0, str(CI))
import bookkeeping_shards  # noqa: E402


def failure(gate: str, line: int = 99) -> str:
    return f"exit status 1\nmake: *** [automation/make/parity.mk:{line}: {gate}] Error 1\n"


class DecideTests(unittest.TestCase):
    def test_a_passing_run_does_not_block(self) -> None:
        self.assertEqual(bookkeeping_shards.decide("success", "")[0], False)

    def test_known_gaps_drift_alone_does_not_block(self) -> None:
        blocked, reason = bookkeeping_shards.decide("failure", failure("known-gaps-drift"))
        self.assertFalse(blocked)
        self.assertIn("known-gaps-drift", reason)

    def test_the_same_gate_reported_twice_does_not_block(self) -> None:
        self.assertFalse(bookkeeping_shards.decide("failure", failure("known-gaps-drift") + failure("known-gaps-drift"))[0])

    def test_any_other_gate_blocks(self) -> None:
        blocked, reason = bookkeeping_shards.decide("failure", failure("divergences-drift", 12))
        self.assertTrue(blocked)
        self.assertIn("divergences-drift", reason)

    def test_another_gate_beside_known_gaps_drift_blocks(self) -> None:
        blocked, reason = bookkeeping_shards.decide("failure", failure("known-gaps-drift") + failure("docs-drift", 7))
        self.assertTrue(blocked)
        self.assertIn("docs-drift", reason)
        self.assertNotIn("known-gaps-drift", reason)

    def test_a_failure_without_a_named_gate_blocks(self) -> None:
        self.assertTrue(bookkeeping_shards.decide("failure", "make: Target 'ci-bookkeeping' not remade because of errors.\n")[0])
        self.assertTrue(bookkeeping_shards.decide("failure", "")[0])

    def test_a_step_that_never_ran_blocks(self) -> None:
        self.assertTrue(bookkeeping_shards.decide("skipped", "")[0])
        self.assertTrue(bookkeeping_shards.decide("cancelled", failure("known-gaps-drift"))[0])


class CommandTests(unittest.TestCase):
    def run_script(self, outcome: str, log: str) -> tuple[int, str]:
        with tempfile.TemporaryDirectory() as directory:
            log_file, output = Path(directory) / "bookkeeping.log", Path(directory) / "output"
            log_file.write_text(log)
            environment = dict(os.environ, GITHUB_OUTPUT=str(output))
            done = subprocess.run([sys.executable, "-B", str(CI / "bookkeeping_shards.py"), "--outcome", outcome, "--log", str(log_file)], env=environment, capture_output=True, text=True, check=False)
            return done.returncode, output.read_text() if output.exists() else ""

    def test_the_output_is_what_the_workflow_reads(self) -> None:
        self.assertEqual(self.run_script("failure", failure("known-gaps-drift")), (0, "blocked=false\n"))
        self.assertEqual(self.run_script("failure", failure("known-gaps-drift") + failure("lint-scenarios", 3)), (0, "blocked=true\n"))
        self.assertEqual(self.run_script("success", ""), (0, "blocked=false\n"))


class WorkflowTests(unittest.TestCase):
    """The workflow reads the output the script writes, and only that: no shard still keys on the Bookkeeping job's own result."""

    def test_every_shard_condition_reads_the_shards_output(self) -> None:
        workflow = (CI.parents[1] / ".github/workflows/ci.yml").read_text()
        self.assertIn("shards-blocked: ${{ steps.shards.outputs.blocked }}", workflow)
        self.assertNotIn("needs.bookkeeping.result == 'success'", workflow)
        self.assertGreaterEqual(workflow.count("needs.bookkeeping.outputs.shards-blocked == 'false'"), 6)


if __name__ == "__main__":
    unittest.main()

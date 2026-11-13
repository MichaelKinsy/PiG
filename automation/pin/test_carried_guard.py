# SPDX-License-Identifier: MIT
"""The guard of the carried oracle run (carried_guard.py): each piece of the tree's own Pi a run could move is reported."""
import shutil
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import carried_guard  # noqa: E402
import pinlib  # noqa: E402
import vendor  # noqa: E402

PIN = pinlib.Pin("v1.2.3", "ab" * 20, "1.2.3", "1.2.3")


class StateTests(unittest.TestCase):
    def setUp(self):
        self.root = Path(tempfile.mkdtemp()).resolve()
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        (self.root / ".upstream" / PIN.mirror_dir).mkdir(parents=True)
        (self.root / ".upstream" / "current").symlink_to(PIN.mirror_dir)
        (self.root / "oracle").mkdir()
        (self.root / "extensions" / "sdk-ts").mkdir(parents=True)
        (self.root / "extensions" / "sdk-ts" / "node_modules").symlink_to(self.root / "oracle")
        (self.root / vendor.INTEROP / "node_modules").mkdir(parents=True)
        self.status = ""
        patch = mock.patch.object(vendor, "check_installed", lambda consumer, root: [])
        patch.start()
        self.addCleanup(patch.stop)
        self.before = self.state()

    def state(self) -> dict[str, str]:
        return carried_guard.state(self.root, lambda root: self.status)

    def problems(self) -> str:
        return "\n".join(carried_guard.problems(self.root, PIN, self.before, self.state()))

    def test_an_unchanged_tree_has_no_problem(self):
        self.assertEqual(self.problems(), "")

    def test_current_left_at_another_mirror_is_reported(self):
        (self.root / ".upstream" / "v-carried").mkdir()
        (self.root / ".upstream" / "current").unlink()
        (self.root / ".upstream" / "current").symlink_to("v-carried")
        self.assertIn(".upstream/current was", self.problems())
        self.assertIn("not the link to the pin's mirror", self.problems())

    def test_an_interop_install_replaced_by_a_link_is_reported(self):
        modules = self.root / vendor.INTEROP / "node_modules"
        modules.rmdir()
        modules.symlink_to(self.root / "oracle")
        self.assertIn("durable/interop/node_modules was 'directory' and is 'link to", self.problems())

    def test_a_moved_oracle_link_is_reported(self):
        link = self.root / "extensions" / "sdk-ts" / "node_modules"
        link.unlink()
        link.symlink_to(self.root / ".upstream")
        self.assertIn("extensions/sdk-ts/node_modules was", self.problems())

    def test_a_file_the_run_wrote_is_reported(self):
        self.status = "?? automation/pin/frozen/x.closure.json.gz\n"
        self.assertIn("git status --porcelain was '' and is", self.problems())

    def test_an_install_that_is_not_the_vendored_packs_is_reported(self):
        with mock.patch.object(vendor, "check_installed", lambda consumer, root: ["durable/interop/node_modules/x differs"]):
            self.assertIn("x differs", self.problems())


class SessionTests(unittest.TestCase):
    def test_the_live_processes_of_the_session_are_found(self):
        proc = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, proc, ignore_errors=True)
        for pid, stat, program in (
            (10, "10 (make) S 1 10 10 0", "make"),
            (11, "11 (harness.test) R 10 10 10 0", "/tmp/go-build1/b001/harness.test"),
            (12, "12 (go) Z 10 10 10 0", "go"),
            (20, "20 (a b) c) S 1 20 20 0", "other"),
        ):
            (proc / str(pid)).mkdir()
            (proc / str(pid) / "stat").write_text(stat)
            (proc / str(pid) / "cmdline").write_bytes(program.encode() + b"\0-test.run=x\0")
        (proc / "self").mkdir()
        found = carried_guard.session(10, proc)
        self.assertEqual(found, {10: "make", 11: "/tmp/go-build1/b001/harness.test"})
        self.assertTrue(carried_guard.runs_a_test_binary(found))
        self.assertFalse(carried_guard.runs_a_test_binary(carried_guard.session(20, proc)))


if __name__ == "__main__":
    unittest.main()

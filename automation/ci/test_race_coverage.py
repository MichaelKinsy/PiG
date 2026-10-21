"""Regression tests for race-coverage.sh on synthetic repositories: no Go toolchain, network or real home access."""
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("race-coverage.sh")
WORKFLOW = "jobs:\n  race:\n    strategy:\n      matrix:\n        group: [ai, rest]\n"
SPAWNER = "package p\n\nfunc F() { go func() {}() }\n"
TEST = "package p\n"


def run_gate(files: dict[str, str], packages: str, workflow: str = WORKFLOW) -> subprocess.CompletedProcess:
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / "automation/ci").mkdir(parents=True)
        shutil.copy(SCRIPT, root / "automation/ci/race-coverage.sh")
        (root / "automation/ci/race-packages.txt").write_text(packages, encoding="utf-8")
        (root / ".github/workflows").mkdir(parents=True)
        (root / ".github/workflows/nightly.yml").write_text(workflow, encoding="utf-8")
        for name, text in files.items():
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text, encoding="utf-8")
        return subprocess.run(["bash", str(root / "automation/ci/race-coverage.sh")], capture_output=True, text=True, check=False)


class RaceCoverageTests(unittest.TestCase):
    base = {"go.mod": "module m\n", "owner/a.go": SPAWNER, "owner/a_test.go": TEST}

    def test_listed_package_passes(self):
        result = run_gate(self.base, "ai ./owner\n")
        self.assertEqual(0, result.returncode, result.stderr)

    def test_unlisted_goroutine_owner_fails(self):
        result = run_gate(self.base, "")
        self.assertEqual(1, result.returncode)
        self.assertIn("./owner starts goroutines", result.stderr)

    def test_package_without_tests_or_goroutines_needs_no_entry(self):
        files = {"go.mod": "module m\n", "untested/a.go": SPAWNER, "plain/a.go": "package p\n", "plain/a_test.go": TEST}
        self.assertEqual(0, run_gate(files, "").returncode)

    def test_a_timer_counts(self):
        files = {"go.mod": "module m\n", "t/a.go": "package p\n\nfunc F() { time.AfterFunc(0, nil) }\n", "t/a_test.go": TEST}
        self.assertEqual(1, run_gate(files, "").returncode)

    def test_exempt_needs_a_reason(self):
        self.assertEqual(1, run_gate(self.base, "exempt ./owner\n").returncode)
        self.assertEqual(0, run_gate(self.base, "exempt ./owner a reason\n").returncode)

    def test_group_must_be_in_the_workflow_matrix(self):
        result = run_gate(self.base, "slow ./owner\n")
        self.assertEqual(1, result.returncode)
        self.assertIn("group slow", result.stderr)

    def test_stale_entry_and_duplicate_fail(self):
        self.assertEqual(1, run_gate(self.base, "ai ./owner\nai ./gone\n").returncode)
        self.assertEqual(1, run_gate(self.base, "ai ./owner\nrest ./owner\n").returncode)

    def test_a_nested_module_is_one_unit(self):
        files = {"go.mod": "module m\n", "sdk/go.mod": "module sdk\n", "sdk/x/a.go": SPAWNER, "sdk/x/a_test.go": TEST}
        self.assertEqual(1, run_gate(files, "").returncode)
        self.assertEqual(0, run_gate(files, "rest ./sdk\n").returncode)
        self.assertEqual(1, run_gate(files, "rest ./sdk/x\n").returncode)

    def test_test_only_files_are_ignored(self):
        files = {"go.mod": "module m\n", "p/a.go": "package p\n", "p/a_test.go": SPAWNER}
        self.assertEqual(0, run_gate(files, "").returncode)


if __name__ == "__main__":
    unittest.main()

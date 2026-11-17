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


def run_gate(files: dict[str, str], packages: str, workflow: str = WORKFLOW, exclusions: str = "ceiling 0\n") -> subprocess.CompletedProcess:
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        (root / "automation/ci").mkdir(parents=True)
        shutil.copy(SCRIPT, root / "automation/ci/race-coverage.sh")
        (root / "automation/ci/race-packages.txt").write_text(packages, encoding="utf-8")
        (root / "automation/ci/race-exclusions.txt").write_text(exclusions, encoding="utf-8")
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

    def test_package_without_tests_needs_no_entry(self):
        files = {"go.mod": "module m\n", "untested/a.go": SPAWNER, "plain/a.go": "package p\n"}
        self.assertEqual(0, run_gate(files, "").returncode)

    def test_tested_package_without_goroutines_needs_a_group_or_an_exclusion(self):
        files = {"go.mod": "module m\n", "plain/a.go": "package p\n", "plain/a_test.go": TEST}
        result = run_gate(files, "")
        self.assertEqual(1, result.returncode)
        self.assertIn("./plain has tests but is in no -race group", result.stderr)
        self.assertEqual(0, run_gate(files, "ai ./plain\n").returncode)
        self.assertEqual(0, run_gate(files, "", exclusions="ceiling 1\n./plain no shared state\n").returncode)

    def test_the_trees_the_goroutine_scan_skips_still_need_an_entry(self):
        files = {"go.mod": "module m\n", "test/x/a.go": "package p\n", "test/x/a_test.go": TEST}
        self.assertEqual(1, run_gate(files, "").returncode)
        self.assertEqual(0, run_gate(files, "", exclusions="ceiling 1\n./test/x run under race by a job\n").returncode)

    def test_exclusion_needs_a_reason_and_the_ceiling_must_match(self):
        files = {"go.mod": "module m\n", "plain/a.go": "package p\n", "plain/a_test.go": TEST}
        self.assertEqual(1, run_gate(files, "", exclusions="ceiling 1\n./plain\n").returncode)
        result = run_gate(files, "", exclusions="ceiling 2\n./plain why\n")
        self.assertEqual(1, result.returncode)
        self.assertIn("ceiling is 2", result.stderr)
        self.assertEqual(1, run_gate(files, "", exclusions="./plain why\n").returncode)

    def test_the_list_only_shrinks(self):
        files = {"go.mod": "module m\n", "a/a.go": "package p\n", "a/a_test.go": TEST, "b/b.go": "package p\n", "b/b_test.go": TEST}
        self.assertEqual(0, run_gate(files, "", exclusions="ceiling 2\n./a why\n./b why\n").returncode)
        # b joins a group: its stale exclusion fails until the entry and the ceiling go.
        self.assertEqual(1, run_gate(files, "ai ./b\n", exclusions="ceiling 2\n./a why\n./b why\n").returncode)
        self.assertEqual(0, run_gate(files, "ai ./b\n", exclusions="ceiling 1\n./a why\n").returncode)

    def test_stale_exclusions_fail(self):
        files = {"go.mod": "module m\n", "plain/a.go": "package p\n", "plain/a_test.go": TEST, "empty/a.go": "package p\n"}
        self.assertEqual(1, run_gate(files, "", exclusions="ceiling 2\n./plain why\n./gone why\n").returncode)
        self.assertEqual(1, run_gate(files, "", exclusions="ceiling 2\n./plain why\n./empty why\n").returncode)

    def test_a_goroutine_owner_cannot_be_excluded(self):
        result = run_gate(self.base, "", exclusions="ceiling 1\n./owner why\n")
        self.assertEqual(1, result.returncode)
        self.assertIn("cannot be excluded", result.stderr)

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

    def test_group_of_an_include_row_counts(self):
        workflow = WORKFLOW + "        include:\n          - {os: ubuntu-24.04, group: parity}\n"
        self.assertEqual(0, run_gate(self.base, "parity ./owner\n", workflow).returncode)

    def test_stale_entry_and_duplicate_fail(self):
        self.assertEqual(1, run_gate(self.base, "ai ./owner\nai ./gone\n").returncode)
        self.assertEqual(1, run_gate(self.base, "ai ./owner\nrest ./owner\n").returncode)

    def test_a_nested_module_is_one_unit(self):
        files = {"go.mod": "module m\n", "sdk/go.mod": "module sdk\n", "sdk/x/a.go": SPAWNER, "sdk/x/a_test.go": TEST}
        self.assertEqual(1, run_gate(files, "").returncode)
        self.assertEqual(0, run_gate(files, "rest ./sdk\n").returncode)
        self.assertEqual(1, run_gate(files, "rest ./sdk/x\n").returncode)
        files = {"go.mod": "module m\n", "sdk/go.mod": "module sdk\n", "sdk/x/a.go": "package p\n", "sdk/x/a_test.go": TEST}
        self.assertEqual(0, run_gate(files, "", exclusions="ceiling 1\n./sdk why\n").returncode)

    def test_workspace_marker_is_for_a_nested_module_only(self):
        files = {"go.mod": "module m\n", "sdk/go.mod": "module sdk\n", "sdk/x/a.go": SPAWNER, "sdk/x/a_test.go": TEST}
        self.assertEqual(0, run_gate(files, "rest ./sdk workspace\n").returncode)
        self.assertEqual(1, run_gate(self.base, "ai ./owner workspace\n").returncode)
        result = run_gate(files, "rest ./sdk anything else\n")
        self.assertEqual(1, result.returncode)
        self.assertIn("empty or `workspace`", result.stderr)

    def test_exempt_reason_may_be_workspace_text(self):
        self.assertEqual(0, run_gate(self.base, "exempt ./owner workspace only\n").returncode)

    def test_test_only_files_are_ignored(self):
        files = {"go.mod": "module m\n", "p/a.go": "package p\n", "p/a_test.go": SPAWNER}
        self.assertEqual(0, run_gate(files, "", exclusions="ceiling 1\n./p why\n").returncode)


if __name__ == "__main__":
    unittest.main()

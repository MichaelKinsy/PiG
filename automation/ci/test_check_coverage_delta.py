"""Regression tests for check-coverage-delta.sh, the coverage step of `make release-check`; temporary git repositories only."""
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("check-coverage-delta.sh")


def report(behavioral: int, ported: int = 636, extra: str = "") -> str:
    """A coverage report in the form test/parity/cmd/coverage writes."""
    return (
        "# PORT_MAP coverage report\n\n## Status\n\n"
        f"- **Porting:** {ported} / 655 intended-portable entries ✅ (97.1%).\n"
        f"- **Behavior verification:** {behavioral} / {ported} ported entries (100.0%); 0 weak-only, 0 untested.\n"
        + extra
    )


class CoverageDeltaTests(unittest.TestCase):
    def run_delta(self, committed: str, current: str) -> subprocess.CompletedProcess:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "automation/ci").mkdir(parents=True)
            shutil.copy(SCRIPT, root / "automation/ci/check-coverage-delta.sh")
            coverage = root / "test/parity/coverage.md"
            coverage.parent.mkdir(parents=True)
            coverage.write_text(committed, encoding="utf-8")
            git = ["git", "-C", directory, "-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"]
            subprocess.run(git + ["init", "-q"], check=True)
            subprocess.run(git + ["add", "."], check=True)
            subprocess.run(git + ["commit", "-q", "-m", "baseline"], check=True)
            coverage.write_text(current, encoding="utf-8")
            return subprocess.run(["bash", str(root / "automation/ci/check-coverage-delta.sh")], capture_output=True, text=True)

    def test_unchanged_coverage_passes_with_its_count(self):
        result = self.run_delta(report(636), report(636))
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn("coverage OK (636 covered", result.stdout)

    def test_fewer_behavioral_entries_fail(self):
        result = self.run_delta(report(636), report(635))
        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("coverage regressed (636 -> 635)", result.stdout)

    def test_an_unreadable_report_fails_instead_of_counting_zero(self):
        result = self.run_delta(report(636), "# PORT_MAP coverage report\n\nno summary line\n")
        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("no behavioral count", result.stdout)

    def test_a_failing_scenario_fails(self):
        result = self.run_delta(report(636), report(636, extra="| `x` | 3 pass / **1 fail** |\n"))
        self.assertEqual(1, result.returncode, result.stdout + result.stderr)
        self.assertIn("failing scenarios", result.stdout)

    def test_a_report_larger_than_a_pipe_buffer_is_read(self):
        rows = "".join(f"| `pkg{i}` | 1 | 0 | 1 | 1 | 0 | 0 | 100.0% | 100.0% |\n" for i in range(20000))
        result = self.run_delta(report(636, extra=rows), report(636, extra=rows))
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)
        self.assertIn("coverage OK (636 covered", result.stdout)


if __name__ == "__main__":
    unittest.main()

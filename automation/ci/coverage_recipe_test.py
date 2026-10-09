import os
import runpy
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class CoverageRecipeTest(unittest.TestCase):
    def test_drift_ignores_only_the_generated_last_run_column(self):
        strip = runpy.run_path(str(ROOT / "automation/ci/check-coverage-drift.py"))["strip_run_column"]
        report = (ROOT / "test/parity/coverage.md").read_text()
        lines = report.splitlines()
        header = next(line for line in lines if line.startswith("| upstream |"))
        columns = [cell.strip() for cell in header.split("|")]
        row = next(line for line in lines[lines.index(header) + 1 :] if line.startswith("| `"))
        cells = row.split("|")

        def changed(column, value):
            modified = cells.copy()
            modified[columns.index(column)] = f" {value} "
            return report.replace(row, "|".join(modified), 1)

        self.assertEqual(strip(report), strip(changed("last run", "3 pass")))
        for column in ("upstream", "port", "scenarios", "behavioral", "unit tests"):
            with self.subTest(column=column):
                self.assertNotEqual(strip(report), strip(changed(column, "changed evidence")))

    def test_failed_generation_preserves_reports(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            shutil.copy(ROOT / "Makefile", root)
            shutil.copytree(ROOT / "automation" / "make", root / "automation" / "make")
            (root / "coding").mkdir()
            shutil.copy(ROOT / "coding" / "upstream.go", root / "coding" / "upstream.go")
            reports = ["test/parity/coverage.md", "AGENTS.md", ".github/badges/parity-coverage.svg"]
            for name in reports:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_text("original " + name)
            binaries = root / "bin"
            binaries.mkdir()
            go = binaries / "go"
            go.write_text('#!/bin/sh\necho partial-output\necho fixture-error >&2\nexit 1\n')
            go.chmod(0o700)
            env = dict(os.environ, PATH=str(binaries) + os.pathsep + os.environ["PATH"])
            result = subprocess.run(["make", "coverage", "RESULTS="], cwd=root, env=env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
            for name in reports:
                self.assertEqual((root / name).read_text(), "original " + name)


if __name__ == "__main__":
    unittest.main()

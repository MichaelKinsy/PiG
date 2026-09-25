import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class CoverageRecipeTest(unittest.TestCase):
    def test_failed_generation_preserves_reports(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            shutil.copy(ROOT / "Makefile", root)
            shutil.copytree(ROOT / "automation" / "make", root / "automation" / "make")
            (root / "coding").mkdir()
            shutil.copy(ROOT / "coding" / "upstream.go", root / "coding" / "upstream.go")
            reports = ["parity/coverage.md", "AGENTS.md", ".github/badges/parity-coverage.svg"]
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

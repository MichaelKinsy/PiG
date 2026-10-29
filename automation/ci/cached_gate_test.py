# SPDX-License-Identifier: MIT
"""cached-gate.sh must run under macOS /bin/bash 3.2 as well as Bash 5.

The gate script keeps its --out directories in arrays. An unguarded empty-array
expansion under `set -u` aborts Bash 3.2 before the gate can run, which is why
the ordinary no---out caller (port-lint, any-trap, perf-lint) must be covered.
"""
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "automation" / "ci" / "cached-gate.sh"
BASH = "/bin/bash"


class CachedGateBash32Test(unittest.TestCase):
    def run_gate(self, *args):
        with tempfile.TemporaryDirectory() as tmp:
            env = os.environ.copy()
            env.pop("CI", None)
            env.pop("PIG_GATE_CACHE", None)
            env["PIG_CACHE_HOME"] = str(Path(tmp) / "cache")
            return subprocess.run(
                [BASH, str(SCRIPT), *args],
                cwd=ROOT,
                env=env,
                text=True,
                capture_output=True,
            )

    def test_no_out_directories(self):
        done = self.run_gate("bash32-no-out", "--", "true")
        self.assertEqual(done.returncode, 0, done.stderr + done.stdout)

    def test_out_directories(self):
        (ROOT / "build").mkdir(exist_ok=True)
        out = Path(tempfile.mkdtemp(prefix="cached-gate-bash32-", dir=ROOT / "build"))
        try:
            relative = out.relative_to(ROOT)
            done = self.run_gate(
                "bash32-with-out",
                "--out", str(relative),
                "--", "sh", "-c", 'mkdir -p "$1"', "_", str(relative),
            )
        finally:
            for child in sorted(out.rglob("*"), reverse=True):
                child.unlink() if child.is_file() else child.rmdir()
            out.rmdir()
        self.assertEqual(done.returncode, 0, done.stderr + done.stdout)


if __name__ == "__main__":
    unittest.main()

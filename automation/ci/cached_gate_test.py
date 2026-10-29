# SPDX-License-Identifier: MIT
"""cached-gate.sh must run under macOS /bin/bash 3.2 as well as Bash 5.

The gate script keeps its --out directories in arrays. An unguarded empty-array
expansion under `set -u` aborts Bash 3.2 before the gate can run, which is why
the ordinary no---out caller (port-lint, any-trap, perf-lint) must be covered.
"""
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "automation" / "ci" / "cached-gate.sh"
BASH = "/bin/bash"


class CachedGateBash32Test(unittest.TestCase):
    def run_gate(self, cache, *args):
        env = os.environ.copy()
        env.pop("CI", None)
        env.pop("PIG_GATE_CACHE", None)
        env["PIG_CACHE_HOME"] = str(cache)
        return subprocess.run(
            [BASH, str(SCRIPT), *args],
            cwd=ROOT,
            env=env,
            text=True,
            capture_output=True,
        )

    def test_no_out_directories(self):
        with tempfile.TemporaryDirectory() as tmp:
            done = self.run_gate(Path(tmp) / "cache", "bash32-no-out", "--", "true")
        self.assertEqual(done.returncode, 0, done.stderr + done.stdout)

    def test_out_directories_are_restored_on_replay(self):
        (ROOT / "build").mkdir(exist_ok=True)
        out = Path(tempfile.mkdtemp(prefix="cached-gate-bash32-", dir=ROOT / "build"))
        try:
            relative = out.relative_to(ROOT)
            write = 'mkdir -p "$1"; printf one > "$1/copy"'
            with tempfile.TemporaryDirectory() as tmp:
                cache = Path(tmp) / "cache"
                first = self.run_gate(
                    cache,
                    "bash32-with-out",
                    "--out", str(relative),
                    "--", "sh", "-c", write, "_", str(relative),
                )
                self.assertEqual(first.returncode, 0, first.stderr + first.stdout)
                self.assertEqual((out / "copy").read_text(), "one")
                shutil.rmtree(out)
                self.assertFalse(out.exists())
                second = self.run_gate(
                    cache,
                    "bash32-with-out",
                    "--out", str(relative),
                    "--", "sh", "-c", write, "_", str(relative),
                )
                self.assertEqual(second.returncode, 0, second.stderr + second.stdout)
                self.assertIn("[cached-gate] bash32-with-out: unchanged inputs", second.stderr + second.stdout)
                self.assertEqual((out / "copy").read_text(), "one")
        finally:
            if out.exists():
                shutil.rmtree(out)


if __name__ == "__main__":
    unittest.main()

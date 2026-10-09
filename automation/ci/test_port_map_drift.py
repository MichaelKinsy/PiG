# SPDX-License-Identifier: MIT

import importlib.util
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("check-port-map-drift.py")
spec = importlib.util.spec_from_file_location("check_port_map_drift", SCRIPT)
drift = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = drift
spec.loader.exec_module(drift)

PORT_MAP = """# map

## `packages/alpha/src/`

| Upstream | Go | Status |
|---|---|---|
| `packages/alpha/src/a.ts` | `go/a.go` | ✅ |

## `packages/beta/src/`

| Upstream | Go | Status |
|---|---|---|
| `packages/beta/src/b.ts` | `go/b.go` | ✅ |
"""


def build(root: pathlib.Path, packages, extra_files=()):
    (root / "upstream" / "packages").mkdir(parents=True)
    for name in ("alpha", "beta"):
        (root / "upstream" / "packages" / name / "src").mkdir(parents=True)
        (root / "upstream" / "packages" / name / "src" / f"{name[0]}.ts").write_text("export const x = 1\n")
    for relative in extra_files:
        (root / "upstream" / relative).write_text("export const y = 2\n")
    (root / "packages.json").write_text(json.dumps({"packages": [{"key": key, "name": key, "root": f"packages/{key}"} for key in packages]}))
    (root / "PORT_MAP.md").write_text(PORT_MAP)


def run(root: pathlib.Path):
    return subprocess.run(
        [sys.executable, str(SCRIPT), "--upstream", str(root / "upstream"), "--port-map", str(root / "PORT_MAP.md"), "--packages", str(root / "packages.json")],
        capture_output=True, text=True,
    )


class PackageListTests(unittest.TestCase):
    def test_complete_list_and_rows_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            build(root, ["alpha", "beta"])
            self.assertEqual(run(root).returncode, 0, run(root).stderr)

    def test_a_package_missing_from_the_list_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            build(root, ["alpha"])
            result = run(root)
            self.assertEqual(result.returncode, 1)
            self.assertIn("package beta is in the upstream mirror but not in", result.stderr)

    def test_an_unmapped_file_in_any_package_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            build(root, ["alpha", "beta"], extra_files=["packages/beta/src/new.ts"])
            result = run(root)
            self.assertEqual(result.returncode, 1)
            self.assertIn("packages/beta/src/new.ts", result.stderr)

    def test_the_checked_in_list_loads_every_package_root(self):
        roots = drift.load_tracked_roots()
        self.assertIn("packages/env/src", roots)
        self.assertEqual(len(roots), len(set(roots)))


if __name__ == "__main__":
    unittest.main()

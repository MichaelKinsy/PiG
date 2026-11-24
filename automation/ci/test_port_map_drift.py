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


def run(root: pathlib.Path, *arguments: str):
    return subprocess.run(
        [sys.executable, str(SCRIPT), "--upstream", str(root / "upstream"), "--port-map", str(root / "PORT_MAP.md"), "--packages", str(root / "packages.json"), *arguments],
        capture_output=True, text=True,
    )


class PackageListTests(unittest.TestCase):
    def test_complete_list_and_rows_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            build(root, ["alpha", "beta"])
            self.assertEqual(run(root).returncode, 0, run(root).stderr)

    def test_rows_with_several_go_spans_or_an_empty_cell_are_rows(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            build(root, ["alpha", "beta"])
            (root / "PORT_MAP.md").write_text(PORT_MAP.replace("`go/a.go`", "`go/a.go (note)`, `go/a2.go`").replace("`go/b.go`", ""))
            rows = drift.parse_rows(root / "PORT_MAP.md")
            self.assertEqual({k: (r.go, r.status) for k, r in rows.items()}, {
                "packages/alpha/src/a.ts": ("go/a.go (note)`, `go/a2.go", "✅"),
                "packages/beta/src/b.ts": ("", "✅"),
            })
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


APPS_RECORD = "\nOutside the packages: `apps/p2`, `apps/p2-mac`, and `apps/p3` are tracked outside the packages.\n"


def build_apps(root: pathlib.Path, files, record=APPS_RECORD):
    build(root, ["alpha", "beta"])
    for relative in files:
        path = root / "upstream" / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("x\n")
    (root / "PORT_MAP.md").write_text(PORT_MAP + record)


def run_listing(listing: str):
    return subprocess.run([sys.executable, str(SCRIPT), "--docs-only-listing", "-"], input=listing, capture_output=True, text=True)


class AppsTests(unittest.TestCase):
    RECORDED = ["apps/p2/src/main.ts", "apps/p2-mac/Package.swift", "apps/p3/src/main.ts"]
    SYNTHETIC_DOCS_APPS = (drift.App("apps/p2"), drift.App("apps/docs-only", "apps/docs-only/docs", frozenset({".html", ".md"})))
    SYNTHETIC_DOCS_RECORDED = ["apps/p2/src/main.ts", "apps/docs-only/docs/a.md", "apps/docs-only/docs/part/b.html"]

    def test_documents_under_the_documents_directory_pass(self):
        self.assertEqual(drift.apps_problems(self.SYNTHETIC_DOCS_RECORDED, apps=self.SYNTHETIC_DOCS_APPS), [])
        self.assertEqual(drift.apps_problems(self.RECORDED), [])

    def test_a_path_outside_the_documents_directory_fails(self):
        for path in ("apps/docs-only/docsx/a.ts", "apps/docs-only/docsx/a.md", "apps/docs-only/docs.md", "apps/docs-only/src/main.ts", "apps/docs-only/package.json"):
            with self.subTest(path=path):
                problems = drift.apps_problems([*self.SYNTHETIC_DOCS_RECORDED, path], apps=self.SYNTHETIC_DOCS_APPS)
                self.assertEqual(len(problems), 1, problems)
                self.assertIn(f"apps/docs-only holds a path outside apps/docs-only/docs/ ({path})", problems[0])
                self.assertIn("Start a port line for apps/docs-only", problems[0])
                self.assertIn('docs/parity/PORT_MAP.md "Package scope"', problems[0])

    def test_a_file_in_the_documents_directory_that_is_not_a_document_fails(self):
        for path in ("apps/docs-only/docs/tool.ts", "apps/docs-only/docs/part/run.sh", "apps/docs-only/docs/Makefile", "apps/docs-only/docs/a.md.js"):
            with self.subTest(path=path):
                problems = drift.apps_problems([*self.SYNTHETIC_DOCS_RECORDED, path], apps=self.SYNTHETIC_DOCS_APPS)
                self.assertEqual(len(problems), 1, problems)
                self.assertIn(f"apps/docs-only/docs/ holds a file that is not a document ({path}; documents are .html, .md)", problems[0])

    def test_a_directory_under_apps_that_is_not_recorded_fails(self):
        for path in ("apps/p4/docs/a.md", "apps/p3-mac/Package.swift"):
            with self.subTest(path=path):
                root = "/".join(path.split("/")[:2])
                problems = drift.apps_problems([*self.RECORDED, path])
                self.assertEqual(len(problems), 1, problems)
                self.assertIn(f"Pi added {root}: record its disposition in", problems[0])
        self.assertEqual(drift.apps_problems([*self.RECORDED, "apps/README.md"]), [])

    def test_the_mirror_as_recorded_passes(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            build_apps(root, self.RECORDED)
            result = run(root)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("Pi's apps/ directories are as recorded (apps/p2, apps/p2-mac, apps/p3; 3 path names read)", result.stdout)
            self.assertEqual(run(root, "--apps").returncode, 0)

    def test_a_mirror_without_apps_says_it_was_not_checked(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            build(root, ["alpha", "beta"])
            result = run(root, "--apps")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("apps is absent: Pi's apps/ directories were not checked", result.stdout)

    def test_the_mirror_fails_on_an_unrecorded_directory_and_a_removed_app(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            build_apps(root, [*self.RECORDED, "apps/p4/Package.swift"])
            for arguments in ((), ("--apps",)):
                result = run(root, *arguments)
                self.assertEqual(result.returncode, 1, result.stdout)
                self.assertIn("Pi added apps/p4: record its disposition in", result.stderr)
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            build_apps(root, ["apps/p2/src/main.ts", "apps/p2-mac/Package.swift"])
            result = run(root)
            self.assertEqual(result.returncode, 1, result.stdout)
            self.assertIn("apps/p3 is recorded but the upstream mirror has no file under it", result.stderr)

    def test_the_mirror_fails_when_the_record_is_lost(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)
            build_apps(root, self.RECORDED, record="\nOutside the packages: `apps/p2` and `apps/p2-mac` are tracked outside the packages.\n")
            result = run(root)
            self.assertEqual(result.returncode, 1, result.stdout)
            self.assertIn('"Package scope" lost its record of apps/p3', result.stderr)
            self.assertNotIn("lost its record of apps/p2", result.stderr)

    def test_a_listing_from_a_clone_is_checked_without_a_mirror(self):
        problems, note = drift.listing_apps_problems("apps/docs-only/docs/a.md\napps/docs-only/docs/part/b.html\n", apps=self.SYNTHETIC_DOCS_APPS)
        self.assertEqual(problems, [])
        self.assertIn("2 path names read, 2 documents", note)
        problems, _ = drift.listing_apps_problems("apps/docs-only/docs/a.md\napps/docs-only/docsx/a.ts\n", apps=self.SYNTHETIC_DOCS_APPS)
        self.assertEqual(len(problems), 1, problems)
        self.assertIn("apps/docs-only holds a path outside apps/docs-only/docs/ (apps/docs-only/docsx/a.ts)", problems[0])
        problems, _ = drift.listing_apps_problems("", apps=self.SYNTHETIC_DOCS_APPS)
        self.assertTrue(any("nothing was checked" in p for p in problems), problems)
        empty = run_listing("")
        self.assertEqual(empty.returncode, 1, empty.stdout)
        self.assertIn("nothing was checked", empty.stderr)

    def test_the_checked_in_record_names_every_app(self):
        record = (SCRIPT.parents[2] / "docs" / "parity" / "PORT_MAP.md").read_text(encoding="utf-8")
        for app in drift.APPS:
            self.assertIn(f"`{app.root}`", record)


if __name__ == "__main__":
    unittest.main()

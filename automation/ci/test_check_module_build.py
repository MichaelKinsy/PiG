# SPDX-License-Identifier: MIT
"""Exercise the GOWORK=off module build gate against a root module that uses SDK API its required version lacks.

The fixture mirrors the PiG layout: a root module requiring a nested SDK module through a published version, a go.work
that resolves the SDK to a working-tree copy carrying newer API, and a file module proxy serving the published versions.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
import zipfile


SCRIPT = Path(__file__).with_name("check-module-build.py").resolve()
ROOT_MODULE = "example.com/product"
SDK_MODULE = ROOT_MODULE + "/sdk"

SDK_OLD = "package sdk\n\ntype Change struct{ Path string }\n\ntype Result struct{}\n"
SDK_NEW = (
    "package sdk\n\ntype Change struct {\n\tPath    string\n\tCreated bool\n}\n\ntype Result struct{}\n\n"
    "func (Result) Drift() []string { return nil }\n\nfunc Suggest() string { return \"\" }\n"
)
# One missing function, one missing method on a value, one missing struct field.
ROOT_SOURCE = (
    "package main\n\nimport \"" + SDK_MODULE + "\"\n\nfunc main() {\n"
    "\tr := &sdk.Result{}\n\t_ = r.Drift()\n\t_ = sdk.Suggest()\n\t_ = sdk.Change{Path: \"x\", Created: true}\n}\n"
)


class ModuleBuildTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.proxy = self.base / "proxy"
        self.root = self.base / "checkout"
        (self.root / "sdk").mkdir(parents=True)
        (self.root / "sdk" / "go.mod").write_text(f"module {SDK_MODULE}\n\ngo 1.26.0\n")
        (self.root / "sdk" / "sdk.go").write_text(SDK_NEW)
        (self.root / "main.go").write_text(ROOT_SOURCE)
        (self.root / "go.work").write_text("go 1.26.0\n\nuse (\n\t.\n\t./sdk\n)\n")
        self.make_proxy_module("v0.1.0", SDK_OLD)
        self.make_proxy_module("v0.1.1", SDK_NEW)
        self.env = dict(os.environ, GOFLAGS="", GOPROXY=self.proxy.as_uri(), GOSUMDB="off",
                        GOMODCACHE=str(self.base / "cache"), GOWORK=str(self.root / "go.work"))
        self.require("v0.1.0")
        self.addCleanup(lambda: subprocess.run(["chmod", "-R", "u+w", str(self.base)], capture_output=True))

    def make_proxy_module(self, version, source):
        directory = self.proxy / SDK_MODULE / "@v"
        directory.mkdir(parents=True, exist_ok=True)
        manifest = f"module {SDK_MODULE}\n\ngo 1.26.0\n"
        (directory / (version + ".mod")).write_text(manifest)
        (directory / (version + ".info")).write_text(json.dumps({"Version": version, "Time": "2026-01-01T00:00:00Z"}))
        with zipfile.ZipFile(directory / (version + ".zip"), "w") as archive:
            archive.writestr(f"{SDK_MODULE}@{version}/go.mod", manifest)
            archive.writestr(f"{SDK_MODULE}@{version}/sdk.go", source)

    def require(self, version, directory=None):
        directory = directory or self.root
        (directory / "go.mod").write_text(f"module {ROOT_MODULE}\n\ngo 1.26.0\n\nrequire {SDK_MODULE} {version}\n")
        env = dict(self.env, GOWORK="off")
        subprocess.run(["go", "mod", "tidy"], cwd=directory, env=env, check=True, capture_output=True, text=True)

    def gate(self, *flags, directory=None, summary=None):
        env = dict(self.env)
        if summary:
            env["GITHUB_STEP_SUMMARY"] = str(summary)
        return subprocess.run([sys.executable, str(SCRIPT), "--root", str(directory or self.root), *flags],
                              env=env, capture_output=True, text=True, timeout=300)

    def test_workspace_hides_the_mismatch_from_a_workspace_build(self):
        build = subprocess.run(["go", "build", "./..."], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(build.returncode, 0, build.stderr)

    def test_gate_fails_on_api_the_required_version_lacks_and_names_every_symbol(self):
        result = self.gate("--fresh-cache")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        for symbol in ("sdk.Result.Drift", "sdk.Suggest", "sdk.Change.Created"):
            self.assertIn(symbol, result.stdout)
        self.assertIn(SDK_MODULE, result.stdout)
        self.assertIn("main.go:", result.stdout)

    def test_gate_does_not_depend_on_a_warm_module_cache_or_the_ambient_workspace(self):
        warm = self.gate()
        self.assertEqual(warm.returncode, 1, warm.stdout + warm.stderr)

    def test_report_mode_never_fails_and_writes_the_job_summary(self):
        summary = self.base / "summary.md"
        result = self.gate("--report", summary=summary)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("::warning", result.stdout)
        text = summary.read_text()
        for symbol in ("sdk.Result.Drift", "sdk.Suggest", "sdk.Change.Created"):
            self.assertIn(symbol, text)

    def test_report_mode_stays_quiet_when_the_requirement_is_satisfied(self):
        self.require("v0.1.1")
        summary = self.base / "summary.md"
        result = self.gate("--report", summary=summary)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn("::warning", result.stdout)
        self.assertFalse(summary.exists())

    def test_gate_passes_once_the_requirement_names_a_version_with_the_symbols(self):
        self.require("v0.1.1")
        result = self.gate("--fresh-cache")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("every module builds and vets with GOWORK=off", result.stdout)

    def test_gate_passes_on_a_scratch_copy_with_a_local_replace(self):
        scratch = self.base / "scratch"
        shutil.copytree(self.root, scratch)
        subprocess.run(["go", "mod", "edit", "-replace", f"{SDK_MODULE}=./sdk"], cwd=scratch,
                       env=dict(self.env, GOWORK="off"), check=True, capture_output=True, text=True)
        (scratch / "go.work").write_text("go 1.26.0\n\nuse .\n")
        result = self.gate("--fresh-cache", directory=scratch)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("replaced by a local directory", result.stdout)

    def test_gate_vets_test_files_too(self):
        self.require("v0.1.1")
        (self.root / "main_test.go").write_text(
            "package main\n\nimport (\n\t\"testing\"\n\n\t\"" + SDK_MODULE + "\"\n)\n\n"
            "func TestUnknown(t *testing.T) { _ = sdk.Missing() }\n")
        result = self.gate("--fresh-cache")
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("sdk.Missing", result.stdout)


if __name__ == "__main__":
    unittest.main()

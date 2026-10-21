# SPDX-License-Identifier: MIT
"""Exercise the publication gate with a real local Git remote and a file module proxy."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import zipfile


SCRIPT = Path(__file__).with_name("check-module-publication.py").resolve()
MODULE = "example.com/product/sdk"
VERSION = "v0.2.0"
TAG = "sdk/" + VERSION


class PublicationTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.root = self.base / "checkout"
        self.root.mkdir()
        self.remote = self.base / "remote.git"
        self.proxy = self.base / "proxy"
        self.env = dict(os.environ, GOWORK="off", GOFLAGS="", GOPROXY=self.proxy.as_uri(),
                        GOSUMDB="off", GOMODCACHE=str(self.base / "cache"))
        self.run_command("git", "init", "-q")
        self.run_command("git", "init", "-q", "--bare", str(self.remote))
        self.manifest = f"module example.com/product\n\ngo 1.26.0\n\nrequire {MODULE} {VERSION}\n"
        (self.root / "go.mod").write_text(self.manifest)
        (self.root / "go.sum").write_text("")
        self.run_command("git", "add", "go.mod")
        self.run_command("git", "-c", "user.name=Test", "-c", "user.email=test@example.com",
                         "-c", "commit.gpgsign=false", "commit", "-qm", "module fixture")
        self.run_command("git", "-c", "user.name=Test", "-c", "user.email=test@example.com",
                         "-c", "tag.gpgsign=false", "tag", "-a", TAG, "-m", "SDK fixture")
        # A tag in the checkout and a workspace replacement are not publication.
        (self.root / "go.work").write_text("go 1.26.0\nuse .\n")
        self.env["GOWORK"] = str(self.root / "go.work")
        self.env["GOFLAGS"] = "-mod=vendor"
        self.make_proxy_module(MODULE, VERSION)
        seed = self.base / "seed"
        seed.mkdir()
        seed_env = dict(self.env, GOWORK="off", GOFLAGS="")
        result = subprocess.run(["go", "mod", "download", "-json", MODULE + "@" + VERSION],
                                cwd=seed, env=seed_env, capture_output=True, text=True, check=True)
        data = json.loads(result.stdout)
        self.sums = f"{MODULE} {VERSION} {data['Sum']}\n{MODULE} {VERSION}/go.mod {data['GoModSum']}\n"
        (self.root / "go.sum").write_text(self.sums)

    def run_command(self, *args):
        return subprocess.run(args, cwd=self.root, env=self.env, check=True, capture_output=True, text=True)

    def make_proxy_module(self, module, version):
        directory = self.proxy / module / "@v"
        directory.mkdir(parents=True, exist_ok=True)
        manifest = f"module {module}\n\ngo 1.26.0\n"
        (directory / (version + ".mod")).write_text(manifest)
        (directory / (version + ".info")).write_text(json.dumps({"Version": version, "Time": "2026-01-01T00:00:00Z"}))
        with zipfile.ZipFile(directory / (version + ".zip"), "w") as archive:
            archive.writestr(module + "@" + version + "/go.mod", manifest)
            archive.writestr(module + "@" + version + "/sdk.go", "package sdk\n")

    def publish(self):
        self.run_command("git", "push", "-q", str(self.remote), "refs/tags/" + TAG)

    def gate(self):
        before = [(self.root / name).read_bytes() for name in ("go.mod", "go.sum")]
        result = subprocess.run([sys.executable, str(SCRIPT), "--root", str(self.root), "--remote", str(self.remote)],
                                env=self.env, capture_output=True, text=True, timeout=90)
        self.assertEqual(before, [(self.root / name).read_bytes() for name in ("go.mod", "go.sum")])
        return result

    def test_local_tag_workspace_and_cached_module_do_not_hide_unpublished_tag(self):
        self.run_command("git", "-c", "tag.gpgsign=false", "tag", VERSION)
        self.run_command("git", "push", "-q", str(self.remote), "refs/tags/" + VERSION)
        result = self.gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unpublished nested-module tag(s): " + TAG, result.stderr)

    def test_published_tag_downloads_and_verifies_checksums(self):
        self.publish()
        result = self.gate()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("Go download and committed checksums verified", result.stdout)

    def test_published_tag_with_unavailable_download_does_not_use_cached_module(self):
        self.publish()
        (self.proxy / MODULE / "@v" / (VERSION + ".zip")).unlink()
        result = self.gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("mod download", result.stderr)

    def pin_pseudo_version(self, pseudo):
        """Require the nested module at a pseudo-version served by the proxy; no tag names it."""
        self.make_proxy_module(MODULE, pseudo)
        manifest = f"module example.com/product\n\ngo 1.26.0\n\nrequire {MODULE} {pseudo}\n"
        (self.root / "go.mod").write_text(manifest)
        seed = self.base / "seed-pseudo"
        seed.mkdir()
        result = subprocess.run(["go", "mod", "download", "-json", MODULE + "@" + pseudo], cwd=seed,
                                env=dict(self.env, GOWORK="off", GOFLAGS=""), capture_output=True, text=True, check=True)
        data = json.loads(result.stdout)
        self.pseudo_sums = f"{MODULE} {pseudo} {data['Sum']}\n{MODULE} {pseudo}/go.mod {data['GoModSum']}\n"
        (self.root / "go.sum").write_text(self.pseudo_sums)

    def test_pseudo_version_needs_no_tag_but_must_download_with_checksums(self):
        self.pin_pseudo_version("v0.2.1-0.20260101000000-0123456789ab")
        result = self.gate()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("published: v0.2.1-0.20260101000000-0123456789ab; Go download and committed checksums verified", result.stdout)

    def test_pseudo_version_that_cannot_be_downloaded_or_is_unpinned_fails(self):
        pseudo = "v0.2.1-0.20260101000000-0123456789ab"
        self.pin_pseudo_version(pseudo)
        (self.root / "go.sum").write_text("")
        result = self.gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("go.sum does not pin", result.stderr)
        (self.root / "go.sum").write_text(self.pseudo_sums)
        (self.proxy / MODULE / "@v" / (pseudo + ".zip")).unlink()
        result = self.gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("mod download", result.stderr)

    def test_a_tagged_version_that_only_looks_like_a_pseudo_version_still_needs_its_tag(self):
        # Not a pseudo-version: the timestamp has 13 digits.
        (self.root / "go.mod").write_text(f"module example.com/product\n\ngo 1.26.0\n\nrequire {MODULE} v0.2.1-0.2026010100000-0123456789ab\n")
        result = self.gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("unpublished nested-module tag(s): sdk/v0.2.1-0.2026010100000-0123456789ab", result.stderr)

    def test_every_nested_requirement_needs_its_exact_tag(self):
        self.publish()
        (self.root / "go.mod").write_text(self.manifest + "\nrequire example.com/product/other v0.9.0\n")
        result = self.gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("other/v0.9.0", result.stderr)

    def test_missing_or_wrong_checksums_fail(self):
        self.publish()
        for content in ("", self.sums.replace("h1:", "h1:wrong")):
            with self.subTest(content=content):
                (self.root / "go.sum").write_text(content)
                result = self.gate()
                self.assertNotEqual(result.returncode, 0)
                self.assertTrue("go.sum does not pin" in result.stderr or "checksum mismatch" in result.stderr, result.stderr)

    def test_root_replacements_and_exclusions_fail(self):
        for directive in (f"replace {MODULE} => ./sdk", f"exclude {MODULE} {VERSION}"):
            with self.subTest(directive=directive):
                (self.root / "go.mod").write_text(self.manifest + "\n" + directive + "\n")
                result = self.gate()
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("replace or exclude", result.stderr)

    def test_remote_failure_is_not_success(self):
        self.remote = self.base / "missing.git"
        result = self.gate()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("ls-remote", result.stderr)

    def test_empty_and_external_only_requirements_need_no_nested_publication(self):
        for requirement in ("", "require example.com/external v1.0.0\n"):
            with self.subTest(requirement=requirement):
                (self.root / "go.mod").write_text("module example.com/product\n\ngo 1.26.0\n" + requirement)
                result = self.gate()
                self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()

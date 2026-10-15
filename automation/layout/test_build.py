#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Behavioral tests of the layout-build transformation, not workflow snapshots."""

from pathlib import Path
import runpy
import shutil
import subprocess
import tempfile
import unittest


HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
BUILD = runpy.run_path(str(HERE / "build.py"))
rewrite = BUILD["rewrite"]


class PathRewriteTest(unittest.TestCase):
    def test_checkout_paths_change_but_runtime_and_upstream_paths_do_not(self):
        source = '''go test ./agent/... ./ai ./coding/... ./tui
paths="coding/pigversion/pigversion.go $root/coding/upstream.go ${REPO_ROOT}/ai/models.go $(CURDIR)/tui/testdata"
$PIG_HOME/agent/auth.json $home/agent $source_home/agent/settings.json ~/.pig/agent
$agent/dist $ai/dist $tui/dist packages/agent/src packages/ai/src packages/tui/src
@earendil-works/pi-ai/compat pi-tui/utils.js internal/ai/x.go ./internal/tui
keywords = ["ai", "coding", "agent", "tui"]
'''
        expected = '''go test ./internal/agent/... ./internal/ai ./internal/coding/... ./internal/tui
paths="internal/coding/pigversion/pigversion.go $root/internal/coding/upstream.go ${REPO_ROOT}/internal/ai/models.go $(CURDIR)/internal/tui/testdata"
$PIG_HOME/agent/auth.json $home/agent $source_home/agent/settings.json ~/.pig/agent
$agent/dist $ai/dist $tui/dist packages/agent/src packages/ai/src packages/tui/src
@earendil-works/pi-ai/compat pi-tui/utils.js internal/ai/x.go ./internal/tui
keywords = ["ai", "coding", "agent", "tui"]
'''
        self.assertEqual(rewrite("automation/gen/example.sh", source), expected)
        self.assertEqual(rewrite("automation/gen/example.sh", expected), expected)

    def test_security_and_attribution_selectors_keep_their_meaning(self):
        cases = {
            ".gitleaks.toml": ("'''^coding/extension/shims/manifest.json$'''", "'''^internal/coding/extension/shims/manifest.json$'''"),
            ".gitattributes": ("tui/**/testdata/** whitespace=-blank-at-eof\nai/*_generated.go linguist-generated=true\n", "internal/tui/**/testdata/** whitespace=-blank-at-eof\ninternal/ai/*_generated.go linguist-generated=true\n"),
            "REUSE.toml": ('path = ["agent/**", "ai/**", "coding/**", "tui/**"]', 'path = ["internal/agent/**", "internal/ai/**", "internal/coding/**", "internal/tui/**"]'),
            ".github/CODEOWNERS": ("/SECURITY.md @owner\n/MAINTAINERS.md @owner\n", "/.github/SECURITY.md @owner\n/docs/project/MAINTAINERS.md @owner\n"),
            "automation/ci/example.py": ('REQUIRED = ["CONTRIBUTING.md", "QUICKSTART.md"]', 'REQUIRED = [".github/CONTRIBUTING.md", "docs/project/QUICKSTART.md"]'),
        }
        for name, (source, expected) in cases.items():
            with self.subTest(name=name):
                self.assertEqual(rewrite(name, source), expected)
                self.assertEqual(rewrite(name, expected), expected)

    def test_python_path_operands_and_npm_keywords(self):
        source = 'PIGVERSION_GO = HERE.parents[2] / "coding" / "pigversion" / "pigversion.go"\nkeywords = ["ai", "agent"]\n'
        expected = 'PIGVERSION_GO = HERE.parents[2] / "internal" / "coding" / "pigversion" / "pigversion.go"\nkeywords = ["ai", "agent"]\n'
        self.assertEqual(rewrite("automation/release/npm/pack_npm.py", source), expected)
        self.assertEqual(rewrite("automation/release/npm/pack_npm.py", expected), expected)

    def test_divguard_fixture_moves_only_its_repository_path_header(self):
        source = '// divguard:path agent/mapped.go\n// upstream: agent/src/agent-loop.ts:DEFAULT_RETRY_DELAY_MS\n// queueLimit appears in agent/src/mapped.ts.\npackage agent\n'
        expected = source.replace('divguard:path agent/', 'divguard:path internal/agent/')
        name = 'automation/ci/divguard/testdata/magic-literal/good.txt'
        self.assertTrue(BUILD['owned'](name))
        self.assertEqual(rewrite(name, source), expected)
        self.assertEqual(rewrite(name, expected), expected)

    def test_section_ownership(self):
        for path in ("automation/gen/new.sh", "automation/README.md", ".github/CODEOWNERS", ".github/ISSUE_TEMPLATE/bug.yml", ".gitignore", "docs/site/public/install.sh"):
            self.assertTrue(BUILD["owned"](path), path)
        for path in ("automation/layout/build.py", "automation/layout/test_build.py", ".github/AGENTS.md", "automation/ci/divguard/main.go", "parity/scenarios/test.toml", "automation/ci/divguard/testdata/mirror/packages/agent/src/mapped.ts"):
            self.assertFalse(BUILD["owned"](path), path)


class ScannerScopeTest(unittest.TestCase):
    def test_public_claims_scans_each_original_go_file_once_after_moves(self):
        name = "automation/ci/check-public-claims.py"
        source = (ROOT / name).read_text()
        after = {}
        exec(compile(rewrite(name, source), name, "exec"), after)
        files = ("coding/cli/main.go", "internal/core/main.go", "coding/session.go", "agent/agent.go", "ai/ai.go", "tui/tui.go", "coding/session_test.go", "internal/core/testdata/ignored.go")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in files:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.touch()
            expected = {"coding/cli/main.go", "internal/core/main.go", "internal/coding/session.go"}
            for package in ("agent", "ai", "coding", "tui"):
                (root / package).rename(root / "internal" / package)
            found = [str(p.relative_to(root)) for p in after["go_files"](root)]
            self.assertEqual(set(found), expected)
            self.assertEqual(len(found), len(expected))

    def test_public_claims_enumerates_only_existing_prose_in_partial_trees(self):
        name = "automation/ci/check-public-claims.py"
        namespace = {}
        exec(compile(rewrite(name, (ROOT / name).read_text()), name, "exec"), namespace)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.assertEqual(namespace["public_files"](root), [])
            policy = root / ".github/SECURITY.md"
            policy.parent.mkdir()
            policy.touch()
            self.assertEqual(namespace["public_files"](root), [policy])

    def test_moved_community_documents_remain_in_public_claims_scan(self):
        name = "automation/ci/check-public-claims.py"
        namespace = {}
        exec(compile(rewrite(name, (ROOT / name).read_text()), name, "exec"), namespace)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            expected = set(BUILD["DOCUMENT_MOVES"].values())
            for name in expected | {".github/AGENTS.md", "AGENTS.md"}:
                path = root / name
                path.parent.mkdir(parents=True, exist_ok=True)
                path.touch()
            found = [str(p.relative_to(root)) for p in namespace["public_files"](root)]
            self.assertEqual(set(found), expected)
            self.assertEqual(len(found), len(expected))


class ApplicationTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.files = sorted(BUILD["REQUIRED_FILES"] | {"automation/gen/new-tool.sh"})
        for name in self.files:
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            if name == "automation/gen/new-tool.sh":
                path.write_text("#!/bin/sh\ncat coding/pigversion/pigversion.go\n")
                path.chmod(0o755)
            else:
                shutil.copyfile(ROOT / name, path)
        self.git("init", "-q")
        self.git("add", "--", *self.files)

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.root), *args], check=True, capture_output=True).stdout

    def snapshot(self):
        return {name: ((self.root / name).read_bytes(), (self.root / name).stat().st_mode) for name in self.files if (self.root / name).exists()}

    def test_rerun_is_identical_and_does_not_touch_index_or_untracked_files(self):
        untracked = self.root / "automation/ci/private.sh"
        untracked.write_text("go test ./coding/...\n")
        index = (self.root / ".git/index").read_bytes()
        changed = BUILD["apply"](self.root)
        self.assertIn("automation/gen/new-tool.sh", changed)
        first = self.snapshot()
        self.assertEqual(BUILD["apply"](self.root), [])
        self.assertEqual(self.snapshot(), first)
        self.assertEqual((self.root / ".git/index").read_bytes(), index)
        self.assertEqual(untracked.read_text(), "go test ./coding/...\n")
        self.assertEqual((self.root / "automation/gen/new-tool.sh").stat().st_mode & 0o777, 0o755)

    def test_missing_tracked_file_fails_before_any_writes(self):
        (self.root / "automation/gen/new-tool.sh").unlink()
        before = self.snapshot()
        with self.assertRaisesRegex(ValueError, "expected a regular tracked file"):
            BUILD["apply"](self.root)
        self.assertEqual(self.snapshot(), before)

    def test_missing_required_path_fails_before_any_writes(self):
        self.git("rm", "--cached", "--", "Makefile")
        before = self.snapshot()
        with self.assertRaisesRegex(ValueError, "required tracked paths are missing: Makefile"):
            BUILD["apply"](self.root)
        self.assertEqual(self.snapshot(), before)

    def test_unrecognized_scanner_structure_fails_before_any_writes(self):
        path = self.root / "automation/ci/check-public-claims.py"
        source = path.read_text()
        for roots in ('("cmd", "internal", "coding")', '("cmd", "internal")'):
            source = source.replace('GO_DIRS = ' + roots, 'GO_DIRS = ()')
        path.write_text(source)
        before = self.snapshot()
        with self.assertRaisesRegex(ValueError, "expected source fragment is missing"):
            BUILD["apply"](self.root)
        self.assertEqual(self.snapshot(), before)


if __name__ == "__main__":
    unittest.main()

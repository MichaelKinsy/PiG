# SPDX-License-Identifier: MIT
"""Unit and regression tests for automation/ci/changed-packages.py."""
import importlib.util
import re
import subprocess
import tempfile
import unittest
from pathlib import Path, PurePosixPath

spec = importlib.util.spec_from_file_location("changed_packages", Path(__file__).with_name("changed-packages.py"))
cp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cp)

REPO_ROOT = Path(__file__).resolve().parents[2]

# Exact file lists from PR #572 (169daa6911..287c13dbe894) and PR #570 (73585394f40c..169daa6911).
PR_572_FILES = [
    "automation/ci/preflight/README.md",
    "automation/ci/preflight/config.go",
    "automation/ci/preflight/config.yaml",
    "automation/ci/preflight/config_test.go",
    "automation/ci/preflight/gitleaks_scan_test.go",
    "automation/ci/preflight/rules.go",
    "automation/ci/preflight/rules_test.go",
    "automation/ci/preflight/scanner.go",
    "changelog.d/preflight-gitleaks-paths-ca588df48a.md",
    "docs/bug-ledger.json",
    "docs/site/docs/bug-ledger.md",
]

PR_570_FILES = [
    "REUSE.toml",
    "automation/perf/README.md",
    "automation/perf/startup_timing.py",
    "automation/perf/test_startup_timing.py",
    "changelog.d/code-scanning-253-history-size-f2b580916a.md",
    "changelog.d/issue-567-loader-dirty-efb2e5690f.md",
    "chord/nextnode/plugins/plugins_watcher_kqueue_test.go",
    "coding/cli/rpc_listen_heap_test.go",
    "coding/extension/host/subprocess/render_proxy.go",
    "coding/extension/host/subprocess/render_proxy_loader_dirty_test.go",
    "coding/extension/host/subprocess/tool_render_proxy.go",
    "coding/extension/host/subprocess/view_kit.go",
    "coding/extension/host/subprocess/view_wire.go",
    "docs/bug-ledger.json",
    "docs/site/docs/bug-ledger.md",
    "durable/core/history/chain.go",
    "durable/core/history/evict.go",
    "durable/core/history/jscan.go",
    "durable/core/history/record_size_test.go",
    "test/parity/interfaces/mapping-v1.6.0.json",
    "test/parity/interfaces/pig-go.json",
]


class StripGoCommentsTest(unittest.TestCase):
    def test_strips_line_and_block_comments_preserving_literals(self):
        src = (
            'package p\n'
            '// see docs/site/docs/bug-ledger.md\n'
            'const a = "docs/site/docs/keep.md" // trailing automation/ci/preflight\n'
            '/* multi-line\n'
            '   docs/site/docs/other.md\n'
            '*/\n'
            'const b = `// kept inside raw string /* also kept */`\n'
            'const c = "escaped \\" // still string"\n'
            "const r = '/' // after rune\n"
        )
        got = cp.strip_go_comments(src)
        self.assertIn('"docs/site/docs/keep.md"', got)
        self.assertIn('`// kept inside raw string /* also kept */`', got)
        self.assertIn('"escaped \\" // still string"', got)
        self.assertIn("const r = '/'", got)
        self.assertNotIn("bug-ledger.md", got)
        self.assertNotIn("automation/ci/preflight", got)
        self.assertNotIn("other.md", got)


class ReferencingTest(unittest.TestCase):
    def test_matches_unoptimised_scan(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            texts = {
                "a.go": 'package a\nvar x = filepath.Join(root, "docs", "guide.md")\n',
                "b.go": 'package b\nvar p = "test/parity/scenarios/rpc/one.json"\n',
                "c.go": 'package c\nvar p = "../../Makefile"\n',
                "d.go": "package d // nothing\n",
                "e.go": 'package e\nvar p = filepath.Join(root, "Makefile")\n',
                "f.go": 'package f\n// comment mentioning docs/guide.md and test/parity/scenarios/rpc/one.json\n',
            }
            sources = {}
            for n, t in texts.items():
                (tmp / n).write_text(t)
                sources[f"m/{n[0]}"] = {tmp / n: None, tmp / "missing.go": None}
            tested = set(sources) - {"m/e"}
            for name in ("docs/guide.md", "test/parity/scenarios/rpc/two.json", "Makefile", "docs/none.md"):
                cp._TEXT_CACHE.clear()
                self.assertEqual(cp.referencing(name, sources, tested), reference_scan_safe(name, sources, tested), name)
            # Comment-only package m/f must never match.
            cp._TEXT_CACHE.clear()
            self.assertNotIn("m/f", cp.referencing("docs/guide.md", sources, tested))
            self.assertNotIn("m/f", cp.referencing("test/parity/scenarios/rpc/two.json", sources, tested))

    def test_non_fixture_three_segment_dir_does_not_match_sibling_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            texts = {
                "sibling.go": 'package s\nvar p = "docs/site/docs/other-page.md"\n',
                "exact.go": 'package x\nvar p = "docs/site/docs/bug-ledger.md"\n',
                "glob.go": 'package g\nvar p = "docs/site/docs/*.md"\n',
                "readdir.go": 'package r\nconst dir = "../../docs/site/docs"\n',
                "comment.go": 'package c\n// docs/site/docs/bug-ledger.md\n',
                "preflight_sibling.go": 'package ps\nvar p = "automation/ci/preflight/scanner_test.go"\n',
                "fixture_sibling.go": 'package fs\nvar p = "test/parity/scenarios/rpc/one.json"\n',
            }
            sources = {}
            for n, t in texts.items():
                (tmp / n).write_text(t)
                sources[f"m/{n[:-3]}"] = {tmp / n: None}
            tested = set(sources)

            cp._TEXT_CACHE.clear()
            self.assertEqual(
                cp.referencing("docs/site/docs/bug-ledger.md", sources, tested),
                {"m/exact", "m/glob", "m/readdir"},
            )
            cp._TEXT_CACHE.clear()
            self.assertEqual(
                cp.referencing("docs/site/docs/unlisted.md", sources, tested),
                {"m/glob", "m/readdir"},
            )
            cp._TEXT_CACHE.clear()
            self.assertEqual(
                cp.referencing("automation/ci/preflight/README.md", sources, tested),
                set(),
            )
            cp._TEXT_CACHE.clear()
            self.assertEqual(
                cp.referencing("test/parity/scenarios/rpc/two.json", sources, tested),
                {"m/fixture_sibling"},
            )


class RepoRegressionTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        modules = subprocess.run(
            ["go", "list", "-m", "-f", "{{.Path}}\t{{.Dir}}"],
            cwd=REPO_ROOT,
            check=True,
            capture_output=True,
            text=True,
        ).stdout.splitlines()
        cls.main_module = next(
            path
            for path, _, directory in (line.partition("\t") for line in modules)
            if directory and Path(directory).resolve() == REPO_ROOT.resolve()
        )
        cls.runnable = set(
            subprocess.run(
                ["go", "list", "./..."],
                cwd=REPO_ROOT,
                check=True,
                capture_output=True,
                text=True,
            ).stdout.split()
        )
        cls.packages = cp.go_list(REPO_ROOT)

    def _rel(self, pkgs: list[str]) -> set[str]:
        prefix = self.main_module + "/"
        return {p.removeprefix(prefix) for p in pkgs}

    def test_pr_572_selection_stays_narrow_and_sound(self):
        cp._TEXT_CACHE.clear()
        res = cp.select(REPO_ROOT, PR_572_FILES, self.packages, self.main_module, self.runnable)
        direct = self._rel(res["direct"])
        affected = self._rel(res["affected"])

        # Soundness: every package that genuinely reads preflight, changelog.d, or bug-ledger is selected.
        for must_have in ("automation/ci/preflight", "automation/ci/bugledger", "test/docs-drift"):
            self.assertIn(must_have, direct)
        self.assertEqual(res["unmapped"], [])

        # Regression guard: comment/sibling false positives (internal/testenv, ai, coding, etc.) must not leak in.
        for must_not_have in ("internal/testenv", "ai", "coding", "tui", "durable/harness"):
            self.assertNotIn(must_not_have, direct)
            self.assertNotIn(must_not_have, affected)
        self.assertLessEqual(len(affected), 20, f"PR #572 affected unexpectedly wide: {sorted(affected)}")

    def test_pr_570_selection_excludes_docs_and_comment_false_positives(self):
        cp._TEXT_CACHE.clear()
        res = cp.select(REPO_ROOT, PR_570_FILES, self.packages, self.main_module, self.runnable)
        direct = self._rel(res["direct"])
        affected = self._rel(res["affected"])

        # Soundness: real edited packages and ledger/docs readers are selected.
        for must_have in (
            "chord/nextnode/plugins",
            "coding/cli",
            "coding/extension/host/subprocess",
            "durable/core/history",
            "automation/ci/bugledger",
            "test/docs-drift",
            "automation/portlens",
        ):
            self.assertIn(must_have, direct)

        # mapping-v1.6.0.json and bug-ledger.md must remain mapped (not unmapped).
        self.assertNotIn("docs/site/docs/bug-ledger.md", res["unmapped"])
        self.assertNotIn("test/parity/interfaces/mapping-v1.6.0.json", res["unmapped"])

        # Regression guard: docs/site/docs sibling false positives (internal/testenv, bench/k8s/..., etc.) must not be direct.
        for must_not_be_direct in ("internal/testenv", "bench/k8s/cmd/k8sbench", "cmd/generate-schemas"):
            self.assertNotIn(must_not_be_direct, direct)
        self.assertLess(len(affected), 150, f"PR #570 affected unexpectedly wide: {len(affected)}")


def reference_scan_safe(name, sources, tested):
    patterns = [cp.path_pattern(PurePosixPath(name))]
    parent = PurePosixPath(name).parent
    if cp.is_fixture_dir(parent):
        patterns.append(cp.path_pattern(parent))
    needle = re.compile("|".join(patterns))
    hits = set()
    for p, files in sources.items():
        if p not in tested:
            continue
        for f in files:
            try:
                text = cp.strip_go_comments(Path(f).read_text(errors="replace"))
            except OSError:
                continue
            if needle.search(text):
                hits.add(p)
                break
    return hits


if __name__ == "__main__":
    unittest.main()

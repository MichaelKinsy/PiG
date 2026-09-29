# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Behavior tests for the docs codemod; no Pi runtime or workflow-YAML assertions."""

import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest import mock

import docs_prose

spec = importlib.util.spec_from_file_location("layout_docs", Path(__file__).with_name("docs.py"))
docs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(docs)


class DocsTests(unittest.TestCase):
    def setUp(self):
        patcher = mock.patch.dict(docs_prose.PROSE_EDITS, {}, clear=True)
        patcher.start()
        self.addCleanup(patcher.stop)
        self.temp = tempfile.TemporaryDirectory(prefix="layout docs ")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-q")
        self.files = {
            "README.md": "[Guide](QUICKSTART.md#build) [Security](SECURITY.md)\n",
            "AGENTS.md": "Read `ai/AGENTS.md` and `coding/extension/host/subprocess/protocol.go`.\n",
            "PORT_MAP.md": "| `packages/ai/src/types.ts` | `ai/types.go` | 🟡 |\n",
            "DIVERGENCES.md": "Call-site markers: `tui/editor.go`: D27. `agent/src/harness/runtime` is Pi.\n",
            "CHANGELOG.md": "## [Unreleased]\n\n### Fixed\n\n- Current fix.\n\n## [0.2.1] - date\n\n### Fixed\n\n- Existing fix.\n\n## [0.2.0]\n",
            "go.mod": "module github.com/MichaelKinsy/PiG\n",
            "CODE_OF_CONDUCT.md": "# Conduct\n",
            "CONTRIBUTING.md": "[Rules](AGENTS.md) [Quality](docs/project/quality.md) [Conduct](CODE_OF_CONDUCT.md)\n",
            "SECURITY.md": "# Security\n",
            "SUPPORT.md": "Follow `SECURITY.md`.\n",
            "GOVERNANCE.md": "[Maintainers](MAINTAINERS.md) [Security](SECURITY.md)\n",
            "MAINTAINERS.md": "[Governance](GOVERNANCE.md)\n",
            "QUICKSTART.md": "[Movie](media/movie.mp4) [Docs](docs/project/quality.md) [Pin](coding/pigversion/pigversion.go#L1)\n",
            "media/movie.mp4": "fixture media\n",
            "docs/project/quality.md": "[Contribution](../../CONTRIBUTING.md)\n",
            "ai/AGENTS.md": "Read `../docs/project/quality.md`. [Root](../README.md)\n",
            "ai/types.go": "package ai\n",
            "coding/pigversion/pigversion.go": "package pigversion\n",
            "coding/extension/host/subprocess/protocol.go": "package subprocess\n",
            "tui/editor.go": "package tui\n",
            "agent/agent.go": "package agent\n",
            "internal/pigdocs/content/links.md": "[Main](https://github.com/MichaelKinsy/PiG/blob/main/ai/types.go#L1)\n[Historical](https://github.com/MichaelKinsy/PiG/blob/abc123/ai/types.go)\n[Pi](https://github.com/earendil-works/pi/blob/main/packages/ai/src/types.ts)\n",
            "parity/scenarios/family/case.toml": 'covers = ["packages/ai/src/types.ts"]\n# go test ./ai -run TestWire\n[assert]\noutput_equal = true\nruns = 3\n',
            "parity/unit-evidence/family.json": '{"package":"./ai","source":"packages/ai/test/types.test.ts","test":"ai/types_test.go#TestWire"}\n',
            "parity/interface-extractor/test/inventory.test.mjs": 'const pin = new URL("../../../coding/pigversion/pigversion.go", import.meta.url);\n',
            "parity/interface-extractor/test/correspondence-inventory.test.mjs": 'const pin = new URL("../../../coding/pigversion/pigversion.go", import.meta.url);\n',
            "parity/interfaces/pig-go.json": '{"file":"ai/types.go"}\n',
            "parity/interfaces/recommendations-v0.87.1.json": '{"file":"ai/types.go"}\n',
            "automation/ci/example.py": 'PATH = "ai/types.go"\n',
        }
        for name, content in self.files.items():
            self.put(name, content)
        self.git("add", "--", *sorted(self.files))
        self.git("-c", "user.name=Layout Test", "-c", "user.email=layout@example.invalid", "commit", "-qm", "fixture")

    def put(self, name, content):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.root), *args], text=True)

    def read(self, name):
        return (self.root / name).read_text()

    def snapshot(self):
        return {p.relative_to(self.root).as_posix(): p.read_bytes() for p in self.root.rglob("*") if p.is_file() and ".git" not in p.parts}

    def test_moves_rebase_links_and_preserve_oracle_and_assertions(self):
        (self.root / "internal").mkdir(exist_ok=True)
        for name in ("agent", "ai", "coding", "tui"):
            self.git("mv", "--", name, "internal/" + name)
        report = docs.apply(self.root)
        self.assertEqual(set(report["moved"]), {
            ".github/CODE_OF_CONDUCT.md", ".github/CONTRIBUTING.md", ".github/SECURITY.md", ".github/SUPPORT.md",
            "docs/project/GOVERNANCE.md", "docs/project/MAINTAINERS.md", "docs/project/QUICKSTART.md",
        })
        self.assertEqual(self.read("README.md"), "[Guide](docs/project/QUICKSTART.md#build) [Security](.github/SECURITY.md)\n")
        self.assertEqual(self.read(".github/CONTRIBUTING.md"), "[Rules](../AGENTS.md) [Quality](../docs/project/quality.md) [Conduct](CODE_OF_CONDUCT.md)\n")
        self.assertEqual(self.read("docs/project/GOVERNANCE.md"), "[Maintainers](MAINTAINERS.md) [Security](../../.github/SECURITY.md)\n")
        self.assertEqual(self.read("docs/project/QUICKSTART.md"), "[Movie](../../media/movie.mp4) [Docs](quality.md) [Pin](../../internal/coding/pigversion/pigversion.go#L1)\n")
        self.assertEqual(self.read("internal/ai/AGENTS.md"), "Read `../../docs/project/quality.md`. [Root](../../README.md)\n")
        self.assertEqual(self.read("PORT_MAP.md"), "| `packages/ai/src/types.ts` | `internal/ai/types.go` | 🟡 |\n")
        self.assertEqual(self.read("DIVERGENCES.md"), "Call-site markers: `internal/tui/editor.go`: D27. `agent/src/harness/runtime` is Pi.\n")
        self.assertEqual(self.read("parity/scenarios/family/case.toml"), self.files["parity/scenarios/family/case.toml"].replace("./ai", "./internal/ai"))
        self.assertEqual(self.read("parity/unit-evidence/family.json"), '{"package":"./internal/ai","source":"packages/ai/test/types.test.ts","test":"internal/ai/types_test.go#TestWire"}\n')
        links = self.read("internal/pigdocs/content/links.md")
        self.assertIn("/main/internal/ai/types.go#L1", links)
        self.assertIn("/abc123/ai/types.go", links)
        self.assertIn("/pi/blob/main/packages/ai/src/types.ts", links)
        self.assertIn(docs.CHANGELOG_BULLET, self.read("CHANGELOG.md"))
        before, diff = self.snapshot(), self.git("diff", "HEAD", "--binary")
        self.assertEqual(docs.apply(self.root), {"section": "docs", "moved": [], "rewritten": []})
        self.assertEqual(self.snapshot(), before)
        self.assertEqual(self.git("diff", "HEAD", "--binary"), diff)

    def test_layout_bullet_belongs_to_unreleased_not_a_published_release(self):
        source = self.files["CHANGELOG.md"]
        result = docs.changelog(source)
        current, historical = result.split("## [0.2.1]", 1)
        self.assertIn(docs.CHANGELOG_BULLET, current)
        self.assertEqual(historical, source.split("## [0.2.1]", 1)[1])
        self.assertEqual(docs.changelog(result), result)
        with self.assertRaisesRegex(ValueError, "Unreleased"):
            docs.changelog("## [0.3.0]\n\n### Fixed\n")

    def test_missing_expected_file_fails_before_mutation(self):
        (self.root / "SECURITY.md").unlink()
        before = self.snapshot()
        with self.assertRaisesRegex(ValueError, "SECURITY.md"):
            docs.apply(self.root)
        self.assertEqual(self.snapshot(), before)

    def test_collision_fails_before_mutation(self):
        self.put("docs/project/QUICKSTART.md", "unrelated destination")
        before = self.snapshot()
        with self.assertRaisesRegex(ValueError, "both source and destination"):
            docs.apply(self.root)
        self.assertEqual(self.snapshot(), before)

    def test_symlink_destination_is_not_a_completed_move(self):
        self.git("mv", "SECURITY.md", "saved.md")
        (self.root / ".github").mkdir()
        (self.root / ".github/SECURITY.md").symlink_to("../saved.md")
        self.git("add", ".github/SECURITY.md")
        with self.assertRaisesRegex(ValueError, "tracked regular file"):
            docs.apply(self.root)

    def test_symlink_parent_cannot_redirect_moves(self):
        self.put("outside/keep.md", "untouched")
        (self.root / ".github").symlink_to("outside", target_is_directory=True)
        before = self.snapshot()
        with self.assertRaisesRegex(ValueError, "symlink"):
            docs.apply(self.root)
        self.assertEqual(self.snapshot(), before)

    def test_graph_source_regenerates_site_and_embedded_mirrors(self):
        repo = Path(__file__).resolve().parents[2]
        self.put(docs.GRAPH_GENERATOR, (repo / docs.GRAPH_GENERATOR).read_text())
        self.put(docs.GRAPH_SOURCE, '{"entities":[{"id":"host","label":"Host","kind":"runtime","summary":"Host runtime","where":"coding/","inspect":"pig","doc":"README.md"}],"relations":[]}\n')
        self.put("docs/site/docs/install-troubleshooting.md", "# Troubleshooting\n")
        for name in docs.GRAPH_OUTPUTS:
            self.put(name, "old output\n")
        self.git("add", "--", docs.GRAPH_GENERATOR, docs.GRAPH_SOURCE, "docs/site/docs/install-troubleshooting.md", *docs.GRAPH_OUTPUTS)
        report = docs.apply(self.root)
        self.assertIn(docs.GRAPH_SOURCE, report["rewritten"])
        for name in ("docs/site/docs/knowledge-graph.md", "internal/pigdocs/content/knowledge-graph.md", "docs/knowledge-graph/pig-knowledge-graph.jsonld"):
            self.assertIn("internal/coding/", self.read(name))
        before = self.snapshot()
        self.assertEqual(docs.apply(self.root)["rewritten"], [])
        self.assertEqual(self.snapshot(), before)

    def test_partial_move_resumes_and_untracked_files_are_untouched(self):
        (self.root / ".github").mkdir()
        self.git("mv", "SECURITY.md", ".github/SECURITY.md")
        self.put("docs/private.md", "`ai/types.go`")
        report = docs.apply(self.root)
        self.assertNotIn(".github/SECURITY.md", report["moved"])
        self.assertEqual(self.read("docs/private.md"), "`ai/types.go`")
        for name in ("automation/ci/example.py", "parity/interfaces/pig-go.json", "parity/interfaces/recommendations-v0.87.1.json"):
            self.assertEqual(self.read(name), self.files[name])

    def test_references_are_boundary_aware(self):
        rewriter = docs.Rewriter(self.root, self.files)
        before = '`ai/types.go` ./tui/... ./ai:TestWire packages/ai/src/types.ts agent/src/harness/runtime internal/ai/types.go aicoding/file.go @earendil-works/pi-ai/compat github.com/MichaelKinsy/PiG/coding\n'
        after = '`internal/ai/types.go` ./internal/tui/... ./internal/ai:TestWire packages/ai/src/types.ts agent/src/harness/runtime internal/ai/types.go aicoding/file.go @earendil-works/pi-ai/compat github.com/MichaelKinsy/PiG/internal/coding\n'
        self.assertEqual(rewriter.text(before, "README.md"), after)
        self.assertEqual(rewriter.text(after, "README.md"), after)

    def test_non_go_oracle_runner_rebases_its_relative_pin_path(self):
        rewriter = docs.Rewriter(self.root, self.files)
        source = 'const pin = new URL("../../../coding/pigversion/pigversion.go", import.meta.url);\n'
        for name in ("parity/interface-extractor/test/inventory.test.mjs", "parity/interface-extractor/test/correspondence-inventory.test.mjs"):
            with self.subTest(path=name):
                result = rewriter.text(source, name, False)
                expected = 'const pin = new URL("../../../internal/coding/pigversion/pigversion.go", import.meta.url);\n'
                self.assertEqual(result, expected)
                self.assertEqual(rewriter.text(result, name, False), expected)
                with self.assertRaisesRegex(ValueError, "relative Pi pin path"):
                    rewriter.text("const pin = 'unexpected';", name, False)

    def test_relocated_oracle_scripts_keep_their_repository_roots(self):
        cases = {
            "agent/testdata/tool-validation-oracle.mjs": "const pi = new URL('../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/', import.meta.url);\n",
            "tui/testdata/color_detection.mjs": 'const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url));\n',
            "agent/harness/session/testdata/generate-fork-snapshots.mjs": "import {createForkSnapshot} from '../../../../.upstream/current/packages/agent/src/harness/session/fork.ts';\n",
            "agent/harness/testdata/telemetry-schema.mjs": "const root = resolve(import.meta.dirname, '../../..');\nwriteFileSync(resolve(root, 'agent/harness/telemetry_schema_data.go'), json);\n",
        }
        for name, source in cases.items():
            self.put(docs.move_path(name), source)
        self.git("add", "--", *[docs.move_path(name) for name in cases])
        docs.apply(self.root)
        for name, source in cases.items():
            expected = source.replace("../", "../../", 1).replace("'agent/harness/", "'internal/agent/harness/")
            self.assertEqual(self.read(docs.move_path(name)), expected)
        before = self.snapshot()
        self.assertEqual(docs.apply(self.root)["rewritten"], [])
        self.assertEqual(self.snapshot(), before)

    def test_reviewed_format_records_remain_sorted_after_path_moves(self):
        self.put("parity/format-versions.toml", '[[fields]]\nid = "ai/auth.go#Version"\npath = "ai/auth.go"\nclassification = "upstream"\nrationale = "Pi ai/auth/oauth/github-copilot.ts owns this."\n\n[[fields]]\nid = "cmd/pig/main.go#Version"\npath = "cmd/pig/main.go"\nclassification = "release-content"\nrationale = "Release identity."\n')
        self.git("add", "parity/format-versions.toml")
        docs.apply(self.root)
        result = self.read("parity/format-versions.toml")
        self.assertLess(result.index("cmd/pig/main.go#Version"), result.index("internal/ai/auth.go#Version"))
        self.assertIn('rationale = "Pi ai/auth/oauth/github-copilot.ts owns this."', result)
        self.assertIn('classification = "upstream"', result)
        self.assertEqual(docs.apply(self.root)["rewritten"], [])

    def test_package_interface_ids_are_not_repository_paths(self):
        rewriter = docs.Rewriter(self.root, self.files)
        value = '"pkg:ai/.#Message" "pkg:agent/node#nodeOnly" "pkg:tui/.#Text"\n'
        self.assertEqual(rewriter.text(value, "parity/interfaces/mapping-v0.87.1.json", False), value)

    def test_rebased_relative_instruction_stays_relative_on_rerun(self):
        self.put(".github/AGENTS.md", "Read `../SECURITY.md`.\n")
        self.git("add", ".github/AGENTS.md")
        docs.apply(self.root)
        first = self.read(".github/AGENTS.md")
        self.assertEqual(first, "Read `./SECURITY.md`.\n")
        docs.apply(self.root)
        self.assertEqual(self.read(".github/AGENTS.md"), first)


class ProseTests(unittest.TestCase):
    def test_reviewed_edits_apply_to_current_tree_and_are_idempotent(self):
        root = Path(__file__).resolve().parents[2]
        for name in docs_prose.PROSE_EDITS:
            with self.subTest(path=name):
                source = (root / name).read_text()
                result = docs_prose.rewrite_prose(source, name)
                rewriter = docs.Rewriter(root, [name])
                result = rewriter.text(result, name)
                self.assertEqual(docs_prose.rewrite_prose(result, name), result)
                if name.endswith("/sdk.md"):
                    self.assertIn("not a public embedding SDK", result)
                    self.assertNotIn("https://pkg.go.dev/github.com/MichaelKinsy/PiG/coding", result)
                    self.assertIn('"github.com/MichaelKinsy/PiG/internal/coding"', result)

    def test_unknown_prose_is_a_review_error(self):
        with self.assertRaisesRegex(ValueError, "prose contract changed"):
            docs_prose.rewrite_prose("# A redesigned SDK page", "docs/site/docs/sdk.md")


if __name__ == "__main__":
    unittest.main()

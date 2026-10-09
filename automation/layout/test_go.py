#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Hermetic codemod regressions. Run: python3 automation/layout/test_go.py."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("layout_go", HERE / "go.py")
layout = importlib.util.module_from_spec(spec)
spec.loader.exec_module(layout)


class LayoutGoTest(unittest.TestCase):
    def fixture(self, root):
        files = {
            "go.mod": "module github.com/MichaelKinsy/PiG\n\ngo 1.26\n",
            "go.work": "go 1.26\nuse (\n.\n./extensions/sdk\n)\n",
            "extensions/sdk/go.mod": "module github.com/MichaelKinsy/PiG/extensions/sdk\n\ngo 1.26\n",
            "extensions/sdk/sdk.go": "// Staged by coding/extension/host/subprocess (host).\npackage sdk\n",
            "embed.go": '// This is the only file at module root; runtime code lives in cmd/, internal/,\n// coding/. Its sole purpose is to host `//go:embed` directives that need\n// root resources.\npackage pig\nimport _ "embed"\n//go:embed CHANGELOG.md\nvar Changelog string\n',
            "CHANGELOG.md": "release\n",
            "agent/agent.go": '// Package agent uses github.com/MichaelKinsy/PiG/ai.\npackage agent\nimport _ "github.com/MichaelKinsy/PiG/ai"\n',
            "ai/ai.go": 'package ai\nimport _ "embed"\n//go:embed data.json\nvar data string\n',
            "ai/data.json": "{}\n",
            "coding/coding.go": "package coding\n",
            "tui/tui.go": "package tui\n",
            "tui/path_test.go": 'package tui\nimport "path/filepath"\nvar root, err = filepath.Abs("..")\n',
            "coding/extension/host/subprocess/testdata/sdk-fixture/go.mod": "module example.com/fixture\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => ../../../../../../extensions/sdk\n",
            "coding/extension/host/subprocess/path_test.go": 'package subprocess\nimport "path/filepath"\nvar root = filepath.Join("..", "..", "..", "..", "extensions", "sdk")\nvar build = "./coding/extension/host/subprocess/testdata/fixture-ext/"\n',
            "tests/upstream-parity/port_map_drift_test.go": 'package parity\nimport "path/filepath"\nvar impl = filepath.Join(root, "ai", "live.go")\n',
            "tests/docs-drift/public_claims_test.go": 'package docsdrift\nvar documents = []string{"SECURITY.md", "AGENTS.md"}\n',
            "coding/cli/windows.go": '//go:build windows\n\npackage main\nimport a "github.com/MichaelKinsy/PiG/ai"\nvar _ a.Model\n',
            "parity/cmd/gointerfaces/main.go": 'package main\nvar paths = []string{"./ai/...", "./tui"}\n',
        }
        subprocess.run(["git", "init", "-q", str(root)], check=True)
        for path, content in files.items():
            target = root / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(content)
        subprocess.run(["git", "-C", str(root), "add", "--", *files], check=True)
        return files

    def test_moves_imports_embeds_tags_modules_and_idempotence(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            files = self.fixture(root)
            layout.apply(root)
            for package in layout.MOVES:
                self.assertFalse((root / package).exists())
            self.assertIn('// Package agent uses github.com/MichaelKinsy/PiG/internal/ai.', (root / "internal/agent/agent.go").read_text())
            self.assertEqual((root / "internal/ai/data.json").read_text(), "{}\n")
            self.assertIn('a "github.com/MichaelKinsy/PiG/internal/ai"', (root / "coding/cli/windows.go").read_text())
            self.assertTrue((root / "coding/cli/windows.go").read_text().startswith("//go:build windows\n"))
            self.assertIn('"./internal/ai/..."', (root / "parity/cmd/gointerfaces/main.go").read_text())
            self.assertIn('filepath.Abs("../..")', (root / "internal/tui/path_test.go").read_text())
            self.assertIn('"..", "..", "..", "..", "..", "extensions"', (root / "internal/coding/extension/host/subprocess/path_test.go").read_text())
            self.assertIn('"./internal/coding/extension/host/subprocess/testdata/fixture-ext/"', (root / "internal/coding/extension/host/subprocess/path_test.go").read_text())
            self.assertIn('filepath.Join(root, "internal", "ai", "live.go")', (root / "tests/upstream-parity/port_map_drift_test.go").read_text())
            self.assertIn('".github/SECURITY.md", "AGENTS.md"', (root / "tests/docs-drift/public_claims_test.go").read_text())
            self.assertIn("../../../../../../../extensions/sdk", (root / "internal/coding/extension/host/subprocess/testdata/sdk-fixture/go.mod").read_text())
            self.assertEqual((root / "extensions/sdk/sdk.go").read_text(), files["extensions/sdk/sdk.go"])
            before = {str(p.relative_to(root)): p.read_bytes() for p in root.rglob("*") if p.is_file() and ".git" not in p.parts}
            layout.apply(root)
            after = {str(p.relative_to(root)): p.read_bytes() for p in root.rglob("*") if p.is_file() and ".git" not in p.parts}
            self.assertEqual(before, after)

    def test_relative_tracked_fixtures_rebase_across_package_moves(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            files = {
                "internal/codingagent/tools/schema_test.go": 'package tools\nvar fixture = "../../../ai/data.json"\n',
                "tests/extension-conformance/provider_test.go": 'package conformance\nvar fixture = "../../ai/data.json"\n',
                "coding/fixture_test.go": 'package coding\nvar fixture = "../tests/extension-conformance/provider_test.go"\nvar userPath = "../private/config.json"\nvar completionPaths = []string{"./", "../"}\n',
            }
            for name, source in files.items():
                (root / name).parent.mkdir(parents=True, exist_ok=True)
                (root / name).write_text(source)
            subprocess.run(["git", "-C", str(root), "add", "--", *files], check=True)
            layout.apply(root)
            expected = {
                "internal/codingagent/tools/schema_test.go": "../../ai/data.json",
                "tests/extension-conformance/provider_test.go": "../../internal/ai/data.json",
                "internal/coding/fixture_test.go": "../../tests/extension-conformance/provider_test.go",
            }
            for name, reference in expected.items():
                self.assertIn('"' + reference + '"', (root / name).read_text())
                self.assertTrue((root / name).parent.joinpath(reference).is_file())
            self.assertIn('"../private/config.json"', (root / "internal/coding/fixture_test.go").read_text())
            self.assertIn('[]string{"./", "../"}', (root / "internal/coding/fixture_test.go").read_text())
            before = {name: (root / name).read_bytes() for name in expected}
            layout.apply(root)
            self.assertEqual(before, {name: (root / name).read_bytes() for name in expected})

    def test_joined_repository_assets_move_without_rewriting_user_paths(self):
        source = b'package p\nvar theme = filepath.Join("..", "..", "tui", "theme_dark.json")\nvar provider = filepath.Join("..", "..", "coding", "testdata", "producer.mjs")\nvar settings = filepath.Join(home, "agent", "settings.json")\n'
        expected = source.replace(b'"tui", "theme_dark.json"', b'"internal", "tui", "theme_dark.json"').replace(b'"coding", "testdata"', b'"internal", "coding", "testdata"')
        after = layout.rewrite_go("coding/cli/assets_test.go", source, [], False)
        self.assertEqual(after, expected)
        self.assertEqual(layout.rewrite_go("coding/cli/assets_test.go", after, [], False), after)

    def test_repository_references_do_not_rewrite_runtime_agent_storage(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            name = "coding/extension/host/subprocess/storage_test.go"
            source = 'package subprocess\nvar script = `readFileSync("agent/models-store.json"); load("/agent/prompts/review.md"); load("ai/user-data.json"); source("agent/agent.go")`\nvar oracle = filepath.Join("../../../..", ".upstream/current/packages/tui/test", name)\n'
            (root / name).write_text(source)
            subprocess.run(["git", "-C", str(root), "add", "--", name], check=True)
            layout.apply(root)
            expected = source.replace('source("agent/agent.go")', 'source("internal/agent/agent.go")').replace('Join("../../../..",', 'Join("../../../../..",')
            target = root / layout.moved(name)
            formatted = subprocess.run(["gofmt"], input=expected, text=True, capture_output=True, check=True).stdout
            self.assertEqual(target.read_text(), formatted)
            before = target.read_bytes()
            layout.apply(root)
            self.assertEqual(target.read_bytes(), before)

    def test_top_level_root_join_gains_only_one_parent(self):
        source = b'package ai\nvar root = filepath.Join("..", "parity")\n'
        expected = b'package ai\nvar root = filepath.Join("..", "..", "parity")\n'
        self.assertEqual(layout.rewrite_go("ai/paths_test.go", source, [], True), expected)

    def test_missing_path_and_collision_fail_before_editing(self):
        for collision in (False, True):
            with self.subTest(collision=collision), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                files = self.fixture(root)
                if collision:
                    (root / "internal/ai").mkdir(parents=True)
                else:
                    (root / "ai/ai.go").unlink()
                    (root / "ai/data.json").unlink()
                    (root / "ai").rmdir()
                with self.assertRaisesRegex(ValueError, "expected exactly one"):
                    layout.apply(root)
                self.assertEqual((root / "agent/agent.go").read_text(), files["agent/agent.go"])

    def test_symlink_parent_fails_before_editing(self):
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as outside:
            root = Path(directory)
            files = self.fixture(root)
            (root / "internal").symlink_to(outside, target_is_directory=True)
            with self.assertRaisesRegex(ValueError, "symlink"):
                layout.apply(root)
            self.assertEqual((root / "agent/agent.go").read_text(), files["agent/agent.go"])
            self.assertEqual(list(Path(outside).iterdir()), [])

    def test_module_replacements_rebase_targets_without_changing_identities(self):
        cases = [
            ("coding/new/go.mod", 'replace example.com/sdk => "../../extensions/my sdk"\n', 'replace example.com/sdk => "../../../extensions/my sdk"\n'),
            ("examples/new/go.mod", 'replace example.com/core => ../../coding/new\n', 'replace example.com/core => ../../internal/coding/new\n'),
            ("coding/new/go.mod", 'replace example.com/peer => ../peer\n', 'replace example.com/peer => ../peer\n'),
            ("coding/new/go.mod", 'replace example.com/peer => example.com/fork v1.0.0\n', 'replace example.com/peer => example.com/fork v1.0.0\n'),
        ]
        for path, before, after in cases:
            with self.subTest(path=path, before=before):
                self.assertEqual(layout.rewrite_module(path, before.encode()), after.encode())

    def test_malformed_go_fails_before_moving_or_rewriting(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            files = self.fixture(root)
            (root / "tui/tui.go").write_text("not Go source\n")
            with self.assertRaises(subprocess.CalledProcessError):
                layout.apply(root)
            self.assertEqual((root / "agent/agent.go").read_text(), files["agent/agent.go"])
            self.assertFalse((root / "internal").exists())

    def test_runtime_names_upstream_paths_and_sdk_are_not_package_paths(self):
        source = b'package p\nvar names = []string{"agent", "tui.input.submit", "packages/ai/src/a.ts", "ai/src/compat.ts", "pkg:ai/.#Message", "pkg:agent/node#nodeOnly", "github.com/MichaelKinsy/PiG/extensions/sdk"}\n'
        values = ["agent", "tui.input.submit", "packages/ai/src/a.ts", "ai/src/compat.ts", "pkg:ai/.#Message", "pkg:agent/node#nodeOnly", "github.com/MichaelKinsy/PiG/extensions/sdk"]
        literals = []
        for value in values:
            start = source.index(('"' + value + '"').encode())
            literals.append({"Start": start, "End": start + len(value) + 2, "Value": value, "Import": False})
        self.assertEqual(layout.rewrite_go("parity/example.go", source, literals, False), source)


if __name__ == "__main__":
    unittest.main()

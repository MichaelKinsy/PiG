#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Root-cleanup regressions for the owner-selected release layout."""
import subprocess
import tempfile
from pathlib import Path
import unittest

import release


class ReleaseTest(unittest.TestCase):
    def fixture(self, root):
        files = {
            'DIVERGENCES.md': '[map](PORT_MAP.md)\n',
            'PORT_MAP.md': '[evidence](parity/README.md)\n',
            'DIVERGENCE-IDS.txt': '100\n',
            'parity/README.md': '[ledger](../DIVERGENCES.md)\n',
            'parity/testdata/probe.mjs': 'const source = new URL("../../extensions/sdk/go.mod", import.meta.url);\n',
            'parity/probes/check.py': 'ROOT = Path(__file__).resolve().parents[2]\n',
            'parity/runner/root.go': 'package runner\nimport "path/filepath"\nvar root = filepath.Join("..", "..")\nvar scenarios = filepath.Join(root, "parity", "scenarios")\nvar ledger = "PORT_MAP.md"\nvar tier = "parity"\nvar upstreamTest = "/tests/"\nvar helperSegment = "/parity"\nvar artifact = filepath.Join(root, "tmp", "parity", "run")\n',
            'parity/interfaces/upstream-v1.json': '{"path":"tests/example"}\n',
            'automation/make/check.mk': 'run:\n\tgo test ./parity/...\n',
            'automation/ci/check.py': 'args = ["go", "run", "./parity/cmd/coverage"]\n',
            'tests/integration/example.go': 'package integration\nimport _ "github.com/MichaelKinsy/PiG/parity/runner"\nvar fixture = "../../parity/testdata/probe.mjs"\n',
            'evals/README.md': 'evals/README.md\n',
            'media/demo.svg': '<svg/>\n',
            'extensions/sdk/go.mod': 'module example.org/sdk\n',
            'Makefile': 'check:\n\tgo test ./parity/... ./tests/...\n',
            'docs/guide.md': '[demo](../media/demo.svg)\n',
        }
        subprocess.run(['git', 'init', '-q', str(root)], check=True)
        for name, text in files.items():
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
        subprocess.run(['git', '-C', str(root), 'add', '--', *files], check=True)
        return files

    def test_moves_references_and_rerun(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            release.apply(root)
            self.assertEqual((root/'docs/parity/DIVERGENCES.md').read_text(), '[map](PORT_MAP.md)\n')
            self.assertEqual((root/'test/parity/README.md').read_text(), '[ledger](../../docs/parity/DIVERGENCES.md)\n')
            self.assertIn('new URL("../../../extensions/sdk/go.mod"', (root/'test/parity/testdata/probe.mjs').read_text())
            self.assertIn('.parents[3]', (root/'test/parity/probes/check.py').read_text())
            self.assertIn('"test/parity", "scenarios"', (root/'test/parity/runner/root.go').read_text())
            self.assertIn('var tier = "parity"', (root/'test/parity/runner/root.go').read_text())
            self.assertIn('var upstreamTest = "/tests/"', (root/'test/parity/runner/root.go').read_text())
            self.assertIn('var helperSegment = "/parity"', (root/'test/parity/runner/root.go').read_text())
            self.assertIn('filepath.Join(root, "tmp", "parity", "run")', (root/'test/parity/runner/root.go').read_text())
            self.assertEqual((root/'test/parity/interfaces/upstream-v1.json').read_text(), '{"path":"tests/example"}\n')
            self.assertIn('go test ./test/parity/...', (root/'automation/make/check.mk').read_text())
            self.assertIn('"./test/parity/cmd/coverage"', (root/'automation/ci/check.py').read_text())
            self.assertIn('PiG/test/parity/runner', (root/'test/integration/example.go').read_text())
            self.assertIn('"../parity/testdata/probe.mjs"', (root/'test/integration/example.go').read_text())
            self.assertEqual((root/'docs/guide.md').read_text(), '[demo](media/demo.svg)\n')
            paths = subprocess.check_output(['git','-C',str(root),'ls-files']).decode().splitlines()
            before = {p:(root/p).read_bytes() for p in paths}
            release.apply(root)
            self.assertEqual(before, {p:(root/p).read_bytes() for p in paths})

    def test_extractor_working_directory_rebases_its_check_script(self):
        source = 'cd test/parity/interface-extractor && \\\n--source-root ../../.upstream/current \\\n../../automation/ci/check-generated.sh "$tmp" ../interfaces/inventory.json\n'
        expected = source.replace('../../.upstream/', '../../../.upstream/').replace('../../automation/', '../../../automation/')
        self.assertEqual(release.structural(source, 'automation/make/parity.mk'), expected)
        self.assertEqual(release.structural(expected, 'automation/make/parity.mk'), expected)

    def test_ledger_basename_classification_is_not_a_repository_path(self):
        rewriter = release.Rewriter(Path('.'), [], False)
        source = 'if record.ledger.name == "DIVERGENCES.md":\n    path = root / "DIVERGENCES.md"\n'
        expected = 'if record.ledger.name == "DIVERGENCES.md":\n    path = root / "docs/parity/DIVERGENCES.md"\n'
        self.assertEqual(rewriter.text(source, 'automation/ci/check-divergence-quality.py'), expected)
        self.assertEqual(rewriter.text(expected, 'automation/ci/check-divergence-quality.py'), expected)

    def test_nested_fixture_setup_is_idempotent(self):
        source = '\t\t\troot := t.TempDir()\n'
        path = 'parity/cmd/coverage/test_porting_test.go'
        once = release.structural(source, path)
        formatted = subprocess.check_output(['gofmt'], input=('package main\nfunc f() {\nif true {\nif true {\n' + once + '}\n}\n}\n').encode()).decode()
        self.assertEqual(release.structural(formatted, path), formatted)

    def test_coverage_test_porting_uses_repository_root(self):
        source = 'testStats, err := loadTestPortingStats(filepath.Dir(*portMap))'
        expected = 'testStats, err := loadTestPortingStats(filepath.Join(filepath.Dir(*portMap), "..", ".."))'
        # Other anchors in this file are already transformed before this caller.
        source = 'loadUnitEvidence(filepath.Join(filepath.Dir(*portMap), "..", ".."), entries)\n' + source
        self.assertIn(expected, release.structural(source, 'parity/cmd/coverage/main.go'))

    def test_preserves_public_telemetry_dependency(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self.fixture(root)
            path = root / 'telemetry/context.go'
            path.parent.mkdir()
            content = 'package telemetry\ntype TelemetryContext interface{}\n'
            path.write_text(content)
            subprocess.run(['git', '-C', str(root), 'add', '--', str(path)], check=True)
            release.apply(root)
            self.assertEqual(path.read_text(), content)

    def test_explicit_checkout_variables_and_current_urls(self):
        rewriter = release.Rewriter(Path('.'), [], False)
        source = '$(CURDIR)/evals $root/parity ./tests https://github.com/MichaelKinsy/PiG/blob/main/media/demo.svg'
        expected = '$(CURDIR)/test/evals $root/test/parity ./test https://github.com/MichaelKinsy/PiG/blob/main/docs/media/demo.svg'
        self.assertEqual(rewriter.references(source), expected)
        self.assertEqual(rewriter.references(expected), expected)

    def test_scenario_contract_change_fails_before_moves(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            self.fixture(root)
            path=root/'parity/scenarios/family/example.toml'
            path.parent.mkdir(parents=True)
            path.write_text('[assert]\noutput_equal = "tests/example"\n')
            subprocess.run(['git','-C',str(root),'add','--',str(path)],check=True)
            with self.assertRaisesRegex(ValueError, 'would change scenario assert'):
                release.apply(root)
            self.assertTrue((root/'tests').is_dir())
            self.assertFalse((root/'test').exists())

    def test_collision_and_partial_layout_fail_before_edits(self):
        for collision in (True,False):
            with self.subTest(collision=collision), tempfile.TemporaryDirectory() as directory:
                root=Path(directory)
                files=self.fixture(root)
                if collision:
                    (root/'test').mkdir()
                else:
                    (root/'docs/media').parent.mkdir(exist_ok=True)
                    (root/'media').rename(root/'docs/media')
                with self.assertRaises(ValueError):
                    release.apply(root)
                self.assertEqual((root/'DIVERGENCES.md').read_text(),files['DIVERGENCES.md'])

    def test_cleanup_preserves_nonempty_changelog_fragments(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory)
            self.fixture(root)
            (root/'changelog.d').mkdir()
            (root/'changelog.d/entry.md').write_text('pending release note\n')
            release.apply(root)
            self.assertTrue((root/'changelog.d/entry.md').exists())
            (root/'changelog.d/entry.md').unlink()
            release.apply(root)
            self.assertFalse((root/'changelog.d').exists())


if __name__ == '__main__':
    unittest.main()

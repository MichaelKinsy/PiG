"""Regression tests for publication boundaries; no network or real home access."""
import importlib.util
import os
from pathlib import Path
import unittest
import subprocess
import sys
import tempfile
from unittest.mock import patch


def fixture_environment():
    """Keep inherited Git repository/config overrides out of disposable fixtures."""
    environment = {key: value for key, value in os.environ.items() if not key.startswith('GIT_')}
    environment['GIT_CONFIG_GLOBAL'] = os.devnull
    environment['GIT_CONFIG_NOSYSTEM'] = '1'
    return environment

spec = importlib.util.spec_from_file_location('hygiene', Path(__file__).with_name('check-public-hygiene.py'))
hygiene = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hygiene)


class PublicHygieneTests(unittest.TestCase):
    def test_committed_scan_cannot_be_cleaned_by_working_tree_edits(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            environment = fixture_environment()
            def git(*args):
                return subprocess.run(['git', '-C', directory, *args], env=environment, check=True, capture_output=True)
            git('init', '-q')
            git('config', 'user.name', 'Fixture')
            git('config', 'user.email', 'fixture@example.invalid')
            (root / 'fixture.txt').write_text('imla' + 'dris')
            git('add', 'fixture.txt')
            git('-c', 'commit.gpgsign=false', 'commit', '-qm', 'fixture')
            (root / 'fixture.txt').write_text('public')
            checker = str(Path(hygiene.__file__).resolve())
            working = subprocess.run([sys.executable, checker], cwd=root, env=environment, capture_output=True)
            frozen = subprocess.run([sys.executable, checker, '--ref', 'HEAD'], cwd=root, env=environment, capture_output=True)
            self.assertEqual(working.returncode, 0, working.stderr)
            self.assertEqual(frozen.returncode, 1, frozen.stderr)
            self.assertIn(b'fixture.txt:1: private infrastructure', frozen.stderr)

    def test_fixture_git_cannot_retarget_an_inherited_repository(self):
        with tempfile.TemporaryDirectory() as directory:
            victim = Path(directory) / 'victim'
            fixture = Path(directory) / 'fixture'
            victim.mkdir()
            fixture.mkdir()
            environment = fixture_environment()
            subprocess.run(['git', '-C', str(victim), 'init', '-q'], env=environment, check=True)
            subprocess.run(['git', '-C', str(victim), 'config', 'user.name', 'Real Owner'], env=environment, check=True)
            with patch.dict(os.environ, {'GIT_DIR': str(victim / '.git'), 'GIT_WORK_TREE': str(victim)}):
                environment = fixture_environment()
                subprocess.run(['git', '-C', str(fixture), 'init', '-q'], env=environment, check=True)
                subprocess.run(['git', '-C', str(fixture), 'config', 'user.name', 'Fixture'], env=environment, check=True)
            actual = subprocess.check_output(['git', '-C', str(victim), 'config', 'user.name'], env=environment, text=True).strip()
            self.assertEqual(actual, 'Real Owner')
            self.assertTrue((fixture / '.git').is_dir())

    def test_nested_private_paths(self):
        for name in ['lane/REVIEW.md', 'lane/foo-REPORT.md', 'lane/a.TASK.md',
                     'docs/site/docs/.env.production', 'config/key.env',
                     'pig-handoff/proof.txt', '.dev' + 'cache/token',
                     'docs/media/demo/demo-full.mp4', 'docs/media/demo/evidence/doom-pi.png',
                     'docs/media/demo/demo-a.mp4']:
            with self.subTest(name=name):
                self.assertTrue(list(hygiene.findings(name, b'innocent')))

    def test_private_contents_in_text_and_binary(self):
        for secret in ['/home/' + 'kin' + 'sy/work', '/Users/' + 'kin' + 'sy/work',
                       '/var/tmp/' + 'kin' + 'sy-pig/tasks/lane.md', '/tmp/' + 'kin' + 'sy/evidence',
                       'imla' + 'dris', 'pig' + '-staging', 'node.hpe' + 'corp.net',
                       '$HOME/pig' + '-lanes', '.dev' + 'cache/scratch/key']:
            for prefix in [b'', b'\x00\xff']:
                self.assertTrue(list(hygiene.findings('fixture.bin', prefix + secret.encode())))

    def test_long_unicode_binary_lines_preserve_full_content_scan(self):
        # A domain prefix cannot change whether the domain occurs. Scanning a
        # long ICU dictionary line must not retry a greedy prefix at every unit.
        script = r'''
import importlib.util
import sys
spec = importlib.util.spec_from_file_location('hygiene', sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
for suffix in ('', 'hpe' + 'corp.net'):
    payload = b'\x00' + ('学生' * 32768 + suffix).encode()
    found = list(module.findings('fixture.bin', payload))
    assert bool(found) == bool(suffix), found
'''
        result = subprocess.run(
            [sys.executable, '-c', script, str(Path(hygiene.__file__).resolve())],
            capture_output=True, timeout=3, check=False,
        )
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_public_examples_and_product_tasks_remain_allowed(self):
        for name in ['docs/site/docs/index.md', 'test/evals/tasks/example/task.toml',
                     'agent/task.go', 'automation/images/ci-go/metadata.env', 'docs/environment.md', 'docs/media/quickstart/quickstart.mp4']:
            self.assertEqual([], list(hygiene.findings(name, b'/home/example/repo\nHERDR_PANE_ID')))


class GuiProvenanceTests(unittest.TestCase):
    def found(self, path, text):
        return list(hygiene.findings(path, text.encode()))

    def test_provenance_terms_fail_in_gui_paths_only(self):
        self.assertTrue(self.found('coding/uiview/a.go', 'from Main' + 'stAI'))
        self.assertTrue(self.found('extensions/sdk/ui/x.go', 'a hack' + 'athon entry'))
        self.assertEqual(self.found('coding/uiview/a.go', 'mainstream release'), [])
        for path in ('coding/extension/host/subprocess/ui_view_mount.go', 'extensions/sdk-lisp/src/ui-view.lisp',
                     'extensions/sdk-lisp/tests/ui-view-tests.lisp', 'test/extension-conformance/ui_view_extras_test.go'):
            self.assertTrue(self.found(path, 'from Main' + 'stAI'), path)
        self.assertEqual(self.found('docs/any.md', 'github.com/main' + 'stai/pig'), [])

    def test_gui_paths_reject_names_and_employer_but_allow_copyright_header(self):
        name = 'sam' + 'arth'
        self.assertTrue(self.found('coding/uiview/a.go', '// thanks ' + name.title()))
        self.assertEqual(self.found('coding/other/a.go', '// thanks ' + name.title()), [])
        employer = 'Hew' + 'lett'
        self.assertTrue(self.found('extensions/pig-ui/a.ts', '// ' + employer))
        header = '// SPDX-FileCopyrightText: Copyright ' + employer + ' Packard'
        self.assertEqual(self.found('extensions/pig-ui/a.ts', header), [])

    def test_carrier_reference_client_adapter_docs_and_fragments_are_gui_paths(self):
        for path in ('internal/viewcarrier/carrier.go', 'internal/viewcarrier/testdata/transcripts/ui_view.txt', 'internal/chordview/chordview.go',
                     'coding/cli/rpc_ui_view_test.go', 'coding/cli/testdata/rpc-ui-view.mjs', 'examples/ui-view-web/client.js',
                     'docs/ui-view-carrier.md', 'changelog.d/guicarrier-0123456789.md', 'changelog.d/guiext-0123456789.md'):
            self.assertTrue(self.found(path, 'from Main' + 'stAI'), path)
        self.assertEqual(self.found('changelog.d/other-0123456789.md', 'from Main' + 'stAI'), [])

    def test_listed_names_match_compounds_of_words(self):
        import hashlib
        compound = 'zeta' + '-' + 'quux_9'
        listed = hygiene.NAME_HASHES | {hashlib.sha256(compound.encode()).hexdigest()}
        with patch.object(hygiene, 'NAME_HASHES', listed):
            self.assertEqual(list(hygiene.provenance('see the ' + compound.upper() + ' notes')), ['listed name'])
            self.assertEqual(list(hygiene.provenance('zeta and quux_9 apart')), [])

    def test_range_scans_commit_messages_and_added_lines_everywhere(self):
        term = 'Main' + 'stAI'
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            environment = fixture_environment()
            def git(*args):
                return subprocess.run(['git', '-C', directory, *args], env=environment, check=True, capture_output=True, text=True).stdout.strip()
            def commit(message):
                git('add', '-A')
                git('-c', 'commit.gpgsign=false', 'commit', '-qm', message)
                return git('rev-parse', 'HEAD')
            def scan(span):
                return subprocess.run([sys.executable, str(Path(hygiene.__file__).resolve()), '--range', span], cwd=root, env=environment, capture_output=True, text=True)
            git('init', '-q')
            git('config', 'user.name', 'Fixture')
            git('config', 'user.email', 'fixture@example.invalid')
            (root / 'legacy.txt').write_text('ported from ' + term + '\n')
            base = commit('legacy')
            (root / 'legacy.txt').write_text('ported from ' + term + '\nand a clean line\n')
            (root / 'docs').mkdir()
            (root / 'docs' / 'page.md').write_text('a page\n')
            clean = commit('a clean change\n\nwith a clean body')
            result = scan(base + '..' + clean)
            self.assertEqual(result.returncode, 0, result.stderr)
            # An added line in any path: a doc, a changelog fragment, a fixture, a test and its name.
            (root / 'docs' / 'page.md').write_text('a page\nwith ' + term + '\n')
            (root / 'testdata').mkdir()
            (root / 'testdata' / 'fixture.json').write_text('{"from": "' + term + '"}\n')
            (root / 'x_test.go').write_text('package x\n\nfunc Test' + term + 'Port(t *testing.T) {}\n')
            added = commit('a change')
            result = scan(clean + '..' + added)
            self.assertEqual(result.returncode, 1, result.stderr)
            for expected in ('docs/page.md:2: forbidden provenance term', 'testdata/fixture.json:1: forbidden provenance term', 'x_test.go:3: forbidden provenance term'):
                self.assertIn(expected, result.stderr)
            self.assertNotIn('legacy.txt', result.stderr)
            # A commit message, subject or body.
            (root / 'docs' / 'page.md').write_text('a page\n')
            (root / 'testdata' / 'fixture.json').write_text('{}\n')
            (root / 'x_test.go').write_text('package x\n')
            message = commit('remove it\n\nas ' + term + ' had it')
            result = scan(added + '..' + message)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn('commit ' + message[:12] + ': message line 3: forbidden provenance term', result.stderr)
            for line in result.stderr.splitlines():
                self.assertTrue(line.startswith('commit ' + message[:12] + ': message line 3: '), result.stderr)
            # A path: a file named after a listed term.
            (root / (term.lower() + '_fixture.txt')).write_text('clean\n')
            named = commit('a fixture')
            result = scan(message + '..' + named)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertIn('_fixture.txt: path: forbidden provenance term', result.stderr)


if __name__ == '__main__':
    unittest.main()

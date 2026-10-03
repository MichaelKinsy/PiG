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


if __name__ == '__main__':
    unittest.main()

"""Regression tests for publication boundaries; no network or real home access."""
import importlib.util
from pathlib import Path
import unittest
import subprocess
import sys
import tempfile

spec = importlib.util.spec_from_file_location('hygiene', Path(__file__).with_name('check-public-hygiene.py'))
hygiene = importlib.util.module_from_spec(spec)
spec.loader.exec_module(hygiene)


class PublicHygieneTests(unittest.TestCase):
    def test_committed_scan_cannot_be_cleaned_by_working_tree_edits(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            def git(*args):
                return subprocess.run(['git', '-C', directory, *args], check=True, capture_output=True)
            git('init', '-q')
            git('config', 'user.name', 'Fixture')
            git('config', 'user.email', 'fixture@example.invalid')
            (root / 'fixture.txt').write_text('imla' + 'dris')
            git('add', 'fixture.txt')
            git('-c', 'commit.gpgsign=false', 'commit', '-qm', 'fixture')
            (root / 'fixture.txt').write_text('public')
            checker = str(Path(hygiene.__file__).resolve())
            working = subprocess.run([sys.executable, checker], cwd=root, capture_output=True)
            frozen = subprocess.run([sys.executable, checker, '--ref', 'HEAD'], cwd=root, capture_output=True)
            self.assertEqual(working.returncode, 0, working.stderr)
            self.assertEqual(frozen.returncode, 1, frozen.stderr)
            self.assertIn(b'fixture.txt:1: private infrastructure', frozen.stderr)

    def test_nested_private_paths(self):
        for name in ['lane/REVIEW.md', 'lane/foo-REPORT.md', 'lane/a.TASK.md',
                     'docs/site/docs/.env.production', 'config/key.env',
                     'pig-handoff/proof.txt', '.dev' + 'cache/token',
                     'media/demo/demo-full.mp4', 'media/demo/evidence/doom-pi.png']:
            with self.subTest(name=name):
                self.assertTrue(list(hygiene.findings(name, b'innocent')))

    def test_private_contents_in_text_and_binary(self):
        for secret in ['/home/' + 'kin' + 'sy/work', '/Users/' + 'kin' + 'sy/work',
                       'imla' + 'dris', 'pig' + '-staging', 'node.hpe' + 'corp.net',
                       '$HOME/pig' + '-lanes', '.dev' + 'cache/scratch/key']:
            for prefix in [b'', b'\x00\xff']:
                self.assertTrue(list(hygiene.findings('fixture.bin', prefix + secret.encode())))

    def test_public_examples_and_product_tasks_remain_allowed(self):
        for name in ['docs/site/docs/index.md', 'evals/tasks/example/task.toml',
                     'agent/task.go', 'automation/images/ci-go/metadata.env', 'docs/environment.md', 'media/demo/demo-a.mp4']:
            self.assertEqual([], list(hygiene.findings(name, b'/home/example/repo\nHERDR_PANE_ID')))


if __name__ == '__main__':
    unittest.main()

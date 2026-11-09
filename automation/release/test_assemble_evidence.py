# SPDX-License-Identifier: MIT
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('assembler', Path(__file__).with_name('assemble-evidence.py'))
assembler = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assembler)


class ReleaseEvidenceTests(unittest.TestCase):
    def fixture(self, root):
        expected = {}
        for product, target in [(product, t) for product, targets in assembler.PRODUCTS for t in targets]:
            name = f'{product}-0.2.0-{target}'
            prefix = 'source' if target == 'source' else 'sbom'
            archive = name + ('.zip' if target.startswith('windows-') else '.tar.gz')
            names = [archive, archive + '.sigstore.json'] + ['evidence/' + suffix for suffix in
                     [prefix + '.spdx.json', prefix + '.cyclonedx.json', 'licenses.csv',
                      'inventory-validation.json', 'grype-report.sarif']]
            hashes = []
            for relative in names:
                path = root / name / relative
                path.parent.mkdir(parents=True, exist_ok=True)
                data = f'{product}/{target}/{relative}'.encode()
                path.write_bytes(data)
                destination = name + '-' + path.name if relative.startswith('evidence/') else relative
                expected[destination] = data
                if not relative.endswith('.sigstore.json'):
                    hashes.append(f'{hashlib.sha256(data).hexdigest()}  ./{relative}\n')
            (root / name / 'SHA256SUMS').write_text(''.join(hashes))
        return expected

    def test_all_targets_keep_distinct_byte_identical_evidence(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            expected = self.fixture(root / 'candidates')
            assembler.assemble(root / 'candidates', root / 'release', '0.2.0')
            # Each target keeps its archive, its signing bundle and five evidence files.
            self.assertEqual(len(expected), 7 * (len(assembler.TARGETS) + len(assembler.ACP_TARGETS) + len(assembler.STANDARD_TARGETS)))
            self.assertIn('pig-0.2.0-android-arm64.tar.gz', expected)
            self.assertIn('pig-acp-0.2.0-linux-arm64.tar.gz', expected)
            self.assertIn('pig-acp-0.2.0-windows-arm64.zip', expected)
            self.assertNotIn('pig-acp-0.2.0-android-arm64.tar.gz', expected)
            # PiG Standard keeps its own archive, bundle and evidence per target, named apart from Stock's.
            for target in assembler.STANDARD_TARGETS:
                archive = f'pig-standard-0.2.0-{target}' + ('.zip' if target.startswith('windows-') else '.tar.gz')
                self.assertIn(archive, expected)
                self.assertIn(archive + '.sigstore.json', expected)
                for evidence in ('sbom.spdx.json', 'sbom.cyclonedx.json', 'licenses.csv',
                                 'inventory-validation.json', 'grype-report.sarif'):
                    self.assertIn(f'pig-standard-0.2.0-{target}-{evidence}', expected)
            self.assertNotIn('pig-standard-0.2.0-android-arm64.tar.gz', expected)
            actual = {p.name: p.read_bytes() for p in (root / 'release').iterdir() if p.name != 'EVIDENCE-SHA256SUMS'}
            self.assertEqual(actual, expected)
            manifest = (root / 'release/EVIDENCE-SHA256SUMS').read_text().splitlines()
            self.assertEqual(len(manifest), len(expected))
            for line in manifest:
                digest, name = line.split('  ', 1)
                self.assertEqual(digest, hashlib.sha256(expected[name]).hexdigest())

    def test_changed_archive_is_not_reblessed_with_a_new_checksum(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.fixture(root / 'candidates')
            (root / 'candidates/pig-0.2.0-linux-amd64/pig-0.2.0-linux-amd64.tar.gz').write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError, 'Build checksum mismatch'):
                assembler.assemble(root / 'candidates', root / 'release', '0.2.0')
            self.assertFalse((root / 'release').exists())

    def test_missing_acp_archive_refuses_release(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.fixture(root / 'candidates')
            (root / 'candidates/pig-acp-0.2.0-darwin-arm64/pig-acp-0.2.0-darwin-arm64.tar.gz').unlink()
            with self.assertRaisesRegex(ValueError, 'Missing or invalid'):
                assembler.assemble(root / 'candidates', root / 'release', '0.2.0')
            self.assertFalse((root / 'release').exists())

    def test_missing_standard_archive_or_evidence_refuses_release(self):
        for target, missing in [('darwin-arm64', 'pig-standard-0.2.0-darwin-arm64.tar.gz'),
                                ('linux-amd64', 'pig-standard-0.2.0-linux-amd64.tar.gz.sigstore.json'),
                                ('windows-arm64', 'evidence/sbom.spdx.json'),
                                ('linux-arm64', 'evidence/grype-report.sarif')]:
            with self.subTest(target=target, missing=missing), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                self.fixture(root / 'candidates')
                (root / f'candidates/pig-standard-0.2.0-{target}' / missing).unlink()
                with self.assertRaisesRegex(ValueError, 'Missing or invalid'):
                    assembler.assemble(root / 'candidates', root / 'release', '0.2.0')
                self.assertFalse((root / 'release').exists())

    def test_standard_checksum_mismatch_is_not_reblessed(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            self.fixture(root / 'candidates')
            (root / 'candidates/pig-standard-0.2.0-windows-amd64/pig-standard-0.2.0-windows-amd64.zip').write_bytes(b'changed')
            with self.assertRaisesRegex(ValueError, 'Build checksum mismatch'):
                assembler.assemble(root / 'candidates', root / 'release', '0.2.0')
            self.assertFalse((root / 'release').exists())

    def test_missing_bundle_or_inventory_refuses_release(self):
        for missing in ['pig-0.2.0-source.tar.gz.sigstore.json', 'evidence/source.spdx.json',
                        'evidence/licenses.csv', 'evidence/grype-report.sarif']:
            with self.subTest(missing=missing), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                self.fixture(root / 'candidates')
                (root / 'candidates/pig-0.2.0-source' / missing).unlink()
                with self.assertRaisesRegex(ValueError, 'Missing or invalid'):
                    assembler.assemble(root / 'candidates', root / 'release', '0.2.0')
                self.assertFalse((root / 'release').exists())

    def test_workflow_attaches_evidence_without_changing_scan_policy(self):
        root = Path(__file__).resolve().parents[2]
        workflow = (root / '.github/workflows/release-candidate.yml').read_text()
        self.assertIn('assemble-evidence.py candidates release "$VERSION"', workflow)
        self.assertIn('steps.source-attest.outputs.bundle-path', workflow)
        self.assertIn('sha256sum -c EVIDENCE-SHA256SUMS', workflow)
        self.assertNotIn('--require-complete-licenses', workflow)
        self.assertIn('sbom: out/evidence/source.spdx.json\n          fail-build: false', workflow)
        self.assertIn('gh release create "v${VERSION}" release/*', workflow)
        # Publication waits for PiG Standard and takes its candidates from artifacts of their own.
        self.assertIn('needs: [binary, native-smoke, acp, acp-native-smoke, standard, standard-native-smoke, source]', workflow)
        self.assertIn('pattern: pig-standard-${{ inputs.version }}-*', workflow)


if __name__ == '__main__':
    unittest.main()

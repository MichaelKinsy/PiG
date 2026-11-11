# SPDX-License-Identifier: MIT
import base64
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('acpsource', Path(__file__).with_name('pig-acp-source.py'))
acpsource = importlib.util.module_from_spec(spec)
spec.loader.exec_module(acpsource)


def tarball(files, extra=None):
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode='w:gz') as archive:
        for name, data in files.items():
            info = tarfile.TarInfo(name)
            info.size = len(data)
            archive.addfile(info, io.BytesIO(data))
        for info in extra or []:
            archive.addfile(info)
    return buffer.getvalue()


def lock_for(data, **override):
    lock = {
        'package': '@pi-in-go/pigpen-acp',
        'version': '0.2.1',
        'tarball': 'https://registry.example/pigpen-acp-0.2.1.tgz',
        'integrity': 'sha512-' + base64.b64encode(hashlib.sha512(data).digest()).decode(),
        'size': len(data),
    }
    lock.update(override)
    return lock


GOOD = {
    'package/package.json': json.dumps({'name': '@pi-in-go/pigpen-acp', 'version': '0.2.1'}).encode(),
    'package/extensions/acp/cmd/pig-acp/go.mod': b'module example\n',
}


class PigACPSourceTests(unittest.TestCase):
    def fetch(self, data, lock):
        original = acpsource.download
        acpsource.download = lambda url, size: data
        try:
            with tempfile.TemporaryDirectory() as tmp:
                return acpsource.fetch(lock, Path(tmp) / 'src').relative_to(tmp).as_posix()
        finally:
            acpsource.download = original

    def test_pinned_tarball_yields_the_source_directory(self):
        data = tarball(GOOD)
        self.assertEqual(self.fetch(data, lock_for(data)), 'src/extensions/acp/cmd/pig-acp')

    def test_wrong_digest_wrong_size_and_wrong_package_are_refused(self):
        data = tarball(GOOD)
        other = tarball({**GOOD, 'package/x': b'x'})
        with self.assertRaisesRegex(acpsource.SourceError, 'integrity mismatch'):
            self.fetch(other, lock_for(data, size=len(other)))
        with self.assertRaisesRegex(acpsource.SourceError, 'pinned size'):
            self.fetch(data, lock_for(data, size=len(data) + 1))
        with self.assertRaisesRegex(acpsource.SourceError, 'pinned'):
            self.fetch(data, lock_for(data, version='0.2.2'))

    def test_members_outside_the_package_root_or_links_are_refused(self):
        for name, extra in [('../evil', None), ('/evil', None), ('other/x', None)]:
            data = tarball({**GOOD, name: b'x'})
            with self.subTest(name=name), self.assertRaisesRegex(acpsource.SourceError, 'unsafe'):
                self.fetch(data, lock_for(data))
        link = tarfile.TarInfo('package/link')
        link.type = tarfile.SYMTYPE
        link.linkname = '/etc/passwd'
        data = tarball(GOOD, [link])
        with self.assertRaisesRegex(acpsource.SourceError, 'not a regular file'):
            self.fetch(data, lock_for(data))

    def test_lock_must_have_exactly_the_pinned_keys_and_an_https_url(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'lock'
            data = tarball(GOOD)
            for bad in [{**lock_for(data), 'extra': 1}, lock_for(data, tarball='http://x/y.tgz'),
                        lock_for(data, size=0), lock_for(data, integrity='sha1-abc')]:
                path.write_text(json.dumps(bad))
                with self.assertRaises(acpsource.SourceError):
                    acpsource.read_lock(path)

    def test_checked_in_lock_is_valid(self):
        lock = acpsource.read_lock(Path(__file__).with_name('pig-acp.lock'))
        self.assertRegex(lock['version'], r'^\d+\.\d+\.\d+$')


if __name__ == '__main__':
    unittest.main()

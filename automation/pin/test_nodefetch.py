# SPDX-License-Identifier: MIT
import hashlib
import io
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import nodefetch  # noqa: E402
from pinlib import PinError  # noqa: E402

PLAT = "linux-x64"


def tarball(version: str, reports: str) -> bytes:
    """A tar.xz laid out like nodejs.org's, whose bin/node is a script that prints `reports`."""
    out = io.BytesIO()
    with tarfile.open(fileobj=out, mode="w:xz") as tar:
        body = f"#!/bin/sh\necho {reports}\n".encode()
        info = tarfile.TarInfo(f"node-v{version}-{PLAT}/bin/node")
        info.size, info.mode = len(body), 0o755
        tar.addfile(info, io.BytesIO(body))
    return out.getvalue()


class Site:
    """Stands in for nodejs.org: serves SHASUMS256.txt and the tarball, and counts requests."""

    def __init__(self, version: str, reports: str | None = None, lie: bool = False):
        self.data = tarball(version, reports or f"v{version}")
        digest = hashlib.sha256(self.data).hexdigest() if not lie else "0" * 64
        self.files = {
            f"{nodefetch.DIST}/v{version}/SHASUMS256.txt": f"{'1' * 64}  node-v{version}-darwin-arm64.tar.xz\n{digest}  node-v{version}-{PLAT}.tar.xz\n".encode(),
            f"{nodefetch.DIST}/v{version}/node-v{version}-{PLAT}.tar.xz": self.data,
        }
        self.requests: list[str] = []

    def __call__(self, url: str) -> bytes:
        self.requests.append(url)
        return self.files[url]


class EnsureTests(unittest.TestCase):
    def test_the_release_is_downloaded_verified_and_unpacked_once(self):
        with tempfile.TemporaryDirectory() as cache:
            site = Site("24.19.0")
            first = nodefetch.ensure("24.19.0", PLAT, site, Path(cache))
            self.assertEqual(first, Path(cache) / f"node-v24.19.0-{PLAT}" / "bin")
            self.assertEqual(nodefetch.node_version(first / "node"), "v24.19.0")
            requests = len(site.requests)
            self.assertEqual(nodefetch.ensure("24.19.0", PLAT, site, Path(cache)), first)
            self.assertEqual(len(site.requests), requests, "a cached release needs no request")

    def test_a_tarball_that_is_not_the_one_the_shasums_list_is_refused_and_nothing_is_kept(self):
        with tempfile.TemporaryDirectory() as cache:
            with self.assertRaisesRegex(PinError, "sha256 .* is not the 0{64}"):
                nodefetch.ensure("24.19.0", PLAT, Site("24.19.0", lie=True), Path(cache))
            self.assertEqual(list(Path(cache).iterdir()), [])

    def test_a_tarball_that_unpacks_to_another_release_is_refused(self):
        with tempfile.TemporaryDirectory() as cache:
            with self.assertRaisesRegex(PinError, "not v24.19.0"):
                nodefetch.ensure("24.19.0", PLAT, Site("24.19.0", reports="v24.22.2"), Path(cache))
            self.assertEqual(list(Path(cache).iterdir()), [])

    def test_a_platform_the_shasums_do_not_list_is_named(self):
        with tempfile.TemporaryDirectory() as cache:
            site = Site("24.19.0")
            with self.assertRaisesRegex(PinError, "lists no node-v24.19.0-linux-arm64.tar.xz"):
                nodefetch.ensure("24.19.0", "linux-arm64", site, Path(cache))

    def test_the_cache_follows_the_environment_override_then_xdg_then_home(self):
        with mock.patch.dict(nodefetch.os.environ, {"PIG_ORACLE_NODE_CACHE": "/scratch/node", "XDG_CACHE_HOME": "/xdg"}):
            self.assertEqual(nodefetch.cache_root(), Path("/scratch/node"))
        with mock.patch.dict(nodefetch.os.environ, {"XDG_CACHE_HOME": "/xdg"}):
            self.assertEqual(nodefetch.cache_root(), Path("/xdg/pig/node"))
        with mock.patch.dict(nodefetch.os.environ, {}, clear=True):
            self.assertEqual(nodefetch.cache_root(), Path.home() / ".cache" / "pig" / "node")

    def test_the_oracle_version_is_the_one_the_goldens_record(self):
        self.assertEqual(nodefetch.oracle_version(), "24.19.0")


if __name__ == "__main__":
    unittest.main()

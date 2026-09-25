#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Unit tests for pack_npm.py (run: python3 -m unittest automation/release/npm/test_pack_npm.py)."""

from __future__ import annotations

import hashlib
import io
import json
import pathlib
import sys
import tarfile
import tempfile
import unittest
import zipfile

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import pack_npm  # noqa: E402

VERSION = "0.2.0"


def make_archives(directory: pathlib.Path, version: str = VERSION) -> None:
    lines = []
    for goos, goarch in sorted(pack_npm.TARGETS):
        prefix = f"pig-{version}-{goos}-{goarch}"
        files = {
            pack_npm.binary_name(goos): f"binary {goos}/{goarch}".encode(),
            "LICENSE": b"MIT License\n",
            "NOTICE": b"notice\n",
            "THIRD_PARTY_NOTICES.md": b"third party\n",
            "LICENSES/MIT.txt": b"mit\n",
        }
        archive = directory / pack_npm.archive_name(version, goos, goarch)
        if goos == "windows":
            with zipfile.ZipFile(archive, "w") as zf:
                for name, data in files.items():
                    zf.writestr(f"{prefix}/{name}", data)
        else:
            with tarfile.open(archive, "w:gz") as tf:
                for name, data in files.items():
                    info = tarfile.TarInfo(f"{prefix}/{name}")
                    info.size = len(data)
                    info.mode = 0o755
                    tf.addfile(info, io.BytesIO(data))
        lines.append(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
    (directory / "SHA256SUMS").write_text("".join(lines))


class ManifestTest(unittest.TestCase):
    def test_os_cpu_mapping_covers_six_targets(self):
        self.assertEqual(
            sorted(pack_npm.TARGETS.values()),
            [("darwin", "arm64"), ("darwin", "x64"), ("linux", "arm64"), ("linux", "x64"), ("win32", "arm64"), ("win32", "x64")],
        )
        self.assertEqual(pack_npm.TARGETS[("windows", "amd64")], ("win32", "x64"))
        self.assertEqual(pack_npm.TARGETS[("linux", "amd64")], ("linux", "x64"))

    def test_npm_version_strips_build_metadata(self):
        self.assertEqual(pack_npm.npm_version("0.2.0"), "0.2.0")
        self.assertEqual(pack_npm.npm_version("v0.2.0+0.87.1"), "0.2.0")
        self.assertEqual(pack_npm.npm_version("1.0.0-rc.1"), "1.0.0-rc.1")
        with self.assertRaises(pack_npm.PackError):
            pack_npm.npm_version("0.2")

    def test_platform_manifest(self):
        m = pack_npm.platform_manifest("0.2.0", "win32", "arm64", "windows", "0.87.1")
        self.assertEqual(m["name"], "@pi-in-go/pig-win32-arm64")
        self.assertEqual(m["version"], "0.2.0")
        self.assertEqual(m["os"], ["win32"])
        self.assertEqual(m["cpu"], ["arm64"])
        self.assertIn("pig.exe", m["files"])
        self.assertIn("LICENSE", m["files"])
        self.assertIn("NOTICE", m["files"])
        self.assertIn("Pi 0.87.1", m["description"])
        self.assertEqual(m["license"], "MIT")
        self.assertNotIn("scripts", m)

    def test_launcher_manifest(self):
        m = pack_npm.launcher_manifest("0.2.0", "0.87.1")
        self.assertEqual(m["name"], "@pi-in-go/pig")
        self.assertEqual(m["bin"], {"pig": "bin/pig.js"})
        self.assertEqual(m["engines"], {"node": ">=18"})
        self.assertEqual(len(m["optionalDependencies"]), 6)
        self.assertTrue(all(v == "0.2.0" for v in m["optionalDependencies"].values()))
        self.assertIn("@pi-in-go/pig-linux-x64", m["optionalDependencies"])
        self.assertIn("Pi 0.87.1", m["description"])
        self.assertNotIn("scripts", m, "no install scripts: binaries arrive as packages, not downloads")
        self.assertNotIn("dependencies", m)

    def test_launcher_package_names_match_generator(self):
        launcher = (pathlib.Path(pack_npm.HERE) / "launcher" / "bin" / "pig.js").read_text()
        for npm_os, npm_cpu in pack_npm.TARGETS.values():
            self.assertIn(f'"{npm_os} {npm_cpu}": `${{SCOPE}}/pig-{npm_os}-{npm_cpu}`', launcher)

    def test_upstream_version_reads_pigversion(self):
        self.assertRegex(pack_npm.upstream_version() or "", r"^\d+\.\d+\.\d+")


class GenerateTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = pathlib.Path(self.tmp.name)
        self.archives = self.root / "release"
        self.archives.mkdir()
        self.out = self.root / "npm"
        make_archives(self.archives)

    def tearDown(self):
        self.tmp.cleanup()

    def test_generates_seven_packages_in_publish_order(self):
        order = pack_npm.generate(self.archives, VERSION, self.out, "0.87.1")
        self.assertEqual(len(order), 7)
        self.assertEqual(order[-1].name, "pig")
        for pkg_dir in order[:-1]:
            manifest = json.loads((pkg_dir / "package.json").read_text())
            npm_os, npm_cpu = manifest["os"][0], manifest["cpu"][0]
            self.assertEqual(pkg_dir.name, f"pig-{npm_os}-{npm_cpu}")
            binary = pkg_dir / ("pig.exe" if npm_os == "win32" else "pig")
            goos = {"win32": "windows"}.get(npm_os, npm_os)
            goarch = {"x64": "amd64"}.get(npm_cpu, npm_cpu)
            self.assertEqual(binary.read_bytes(), f"binary {goos}/{goarch}".encode())
            self.assertTrue(binary.stat().st_mode & 0o111)
            for notice in ("LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md"):
                self.assertTrue((pkg_dir / notice).is_file(), notice)
            self.assertFalse((pkg_dir / "LICENSES").exists())
        launcher = order[-1]
        self.assertTrue((launcher / "bin" / "pig.js").is_file())
        self.assertIn("npm install -g @pi-in-go/pig", (launcher / "README.md").read_text())

    def test_rejects_checksum_mismatch(self):
        archive = self.archives / pack_npm.archive_name(VERSION, "linux", "arm64")
        archive.write_bytes(archive.read_bytes() + b"tampered")
        with self.assertRaisesRegex(pack_npm.PackError, "checksum mismatch"):
            pack_npm.generate(self.archives, VERSION, self.out, None)

    def test_rejects_archive_missing_from_sums(self):
        sums = self.archives / "SHA256SUMS"
        sums.write_text("".join(l + "\n" for l in sums.read_text().splitlines() if "darwin-x64" not in l and "darwin-amd64" not in l))
        with self.assertRaisesRegex(pack_npm.PackError, "not listed in SHA256SUMS"):
            pack_npm.generate(self.archives, VERSION, self.out, None)

    def test_rejects_missing_archive(self):
        (self.archives / pack_npm.archive_name(VERSION, "windows", "arm64")).unlink()
        with self.assertRaisesRegex(pack_npm.PackError, "missing release archive"):
            pack_npm.generate(self.archives, VERSION, self.out, None)

    def test_rejects_missing_sums(self):
        (self.archives / "SHA256SUMS").unlink()
        with self.assertRaisesRegex(pack_npm.PackError, "SHA256SUMS"):
            pack_npm.generate(self.archives, VERSION, self.out, None)


if __name__ == "__main__":
    unittest.main()

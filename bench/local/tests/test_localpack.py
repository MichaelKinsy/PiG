# SPDX-License-Identifier: MIT
import hashlib
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import localpack  # noqa: E402


class LocalPackTest(unittest.TestCase):
    def test_locks_pin_every_download_by_sha256_and_size(self):
        for spec in localpack.read(localpack.RUNTIMES)["runtimes"].values():
            for asset in spec["assets"].values():
                self.assertEqual(len(asset["sha256"]), 64)
                self.assertGreater(asset["size"], 0)
        for spec in localpack.read(localpack.MODELS)["models"].values():
            self.assertEqual(len(spec["revision"]), 40)  # an exact commit, never a branch
            self.assertEqual(len(spec["sha256"]), 64)

    def test_fetch_verified_accepts_the_pinned_bytes_and_rejects_others(self):
        with tempfile.TemporaryDirectory() as tmp:
            src = os.path.join(tmp, "src.bin")
            data = b"pinned bytes"
            with open(src, "wb") as f:
                f.write(data)
            url = "file://" + src
            good = localpack.fetch_verified(url, os.path.join(tmp, "a"), hashlib.sha256(data).hexdigest(), len(data))
            self.assertEqual(open(good, "rb").read(), data)
            with self.assertRaises(SystemExit):
                localpack.fetch_verified(url, os.path.join(tmp, "b"), "0" * 64, len(data))
            self.assertFalse(os.path.exists(os.path.join(tmp, "b")))  # a failed download leaves nothing behind
            self.assertFalse(os.path.exists(os.path.join(tmp, "b.part")))
            with self.assertRaises(SystemExit):
                localpack.fetch_verified(url, os.path.join(tmp, "c"), hashlib.sha256(data).hexdigest(), len(data) + 1)

    def test_a_cached_file_with_the_pinned_hash_is_not_downloaded_again(self):
        with tempfile.TemporaryDirectory() as tmp:
            dest = os.path.join(tmp, "m")
            data = b"cached"
            with open(dest, "wb") as f:
                f.write(data)
            got = localpack.fetch_verified("file:///does/not/exist", dest, hashlib.sha256(data).hexdigest(), len(data))
            self.assertEqual(got, dest)

    def test_a_host_without_a_pinned_asset_is_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(SystemExit):
                localpack.install_runtime(tmp, key="plan9-mips")

    def test_pinned_archive_installs_and_runs_on_this_host(self):
        if localpack.host_key() != "linux-amd64":
            self.skipTest("the pinned archive for another platform cannot run here")
        cache = os.environ.get("PIGPACK_CACHE")
        if not cache:
            self.skipTest("set PIGPACK_CACHE to run the download-and-serve check")
        import subprocess
        with tempfile.TemporaryDirectory() as tmp:
            binary = localpack.install_runtime(tmp)
            out = subprocess.run([binary, "--version"], capture_output=True, text=True, timeout=60)
            self.assertIn("build 11429", out.stdout + out.stderr)


if __name__ == "__main__":
    unittest.main()

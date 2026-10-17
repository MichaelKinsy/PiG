# SPDX-License-Identifier: MIT
import os
import tempfile
import unittest
from unittest import mock

from pigbench import arms, lock


class ReleaseAssetsTest(unittest.TestCase):
    def test_an_asset_that_is_not_an_archive_is_the_executable(self):
        with tempfile.TemporaryDirectory() as d:
            src = os.path.join(d, "omp-linux-x64")
            with open(src, "wb") as f:
                f.write(b"\x7fELF binary")
            out = os.path.join(d, "out")
            os.makedirs(out)
            arms.extract_member(src, "omp", out, "omp", "omp-linux-x64")
            self.assertEqual(open(os.path.join(out, "omp"), "rb").read(), b"\x7fELF binary")

    def test_a_release_without_a_checksum_file_is_pinned_on_first_use_and_says_so(self):
        entry = {"release": {"base": "https://example.invalid/v{version}/", "assets": {"linux-amd64": "tool-{version}.bin"}, "sums": "first-use", "member": "tool"}}
        with mock.patch.object(lock, "fetch", return_value=b"bytes") as fetch:
            out = lock.bump_release("tool", entry, "1.2.3")
        fetch.assert_called_once_with("https://example.invalid/v1.2.3/tool-1.2.3.bin")  # no checksum file is requested
        self.assertEqual(out["assets"]["linux-amd64"]["sha256"], lock.sha256_bytes(b"bytes"))
        self.assertIn("first use", out["trust"])

    def test_a_named_checksum_file_is_read_and_must_match(self):
        entry = {"release": {"base": "https://example.invalid/v{version}/", "assets": {"linux-amd64": "tool"}, "sums": "SHA256SUMS.txt", "member": "tool"}}
        good = lock.sha256_bytes(b"x")

        def fake(url):
            return b"x" if url.endswith("/tool") else (f"{good}  tool\n").encode()
        with mock.patch.object(lock, "fetch", side_effect=fake):
            self.assertEqual(lock.bump_release("tool", dict(entry), "1")["assets"]["linux-amd64"]["sha256"], good)
        with mock.patch.object(lock, "fetch", side_effect=lambda url: b"y" if url.endswith("/tool") else (f"{good}  tool\n").encode()):
            with self.assertRaises(SystemExit):
                lock.bump_release("tool", dict(entry), "1")



class PgoBuildTest(unittest.TestCase):
    def test_build_flags_name_the_profile_or_turn_pgo_off(self):
        seen = []
        with mock.patch.object(lock, "run", side_effect=lambda argv, **kw: seen.append(argv)):
            arms.build_pig("/tmp/out-default")
            arms.build_pig("/tmp/out-pgo", "/x/pig.pgo")
        self.assertIn("-pgo=off", seen[0])  # a stray default.pgo must not change what a default arm measures
        self.assertIn("-pgo=/x/pig.pgo", seen[1])
        self.assertNotIn("-pgo=off", seen[1])

    def test_lock_verify_rejects_a_profile_that_no_longer_matches(self):
        data = lock.read()
        data["arms"]["pig-0.5-pgo"]["build"]["pgo_sha256"] = "0" * 64
        with mock.patch.object(lock, "read", return_value=data):
            problems = lock.verify()
        self.assertTrue(any("pig-0.5-pgo" in p and "PGO profile" in p for p in problems), problems)

    def test_the_workspace_builds_the_pgo_arm_with_the_locked_profile_and_the_default_arm_without(self):
        loaded = arms.load()
        built = {}

        def fake_build(out, pgo=None):
            built[os.path.basename(os.path.dirname(out))] = pgo
            return out
        env = {k: v for k, v in os.environ.items() if not k.startswith("PIGBENCH_BIN_")}
        with mock.patch.object(arms, "build_pig", side_effect=fake_build), mock.patch.dict(os.environ, env, clear=True):
            with arms.Workspace() as ws:
                pgo_arm = ws.install(loaded["pig-0.5-pgo"])
                default_arm = ws.install(loaded["pig-0.5"])
        profile = os.path.join(lock.EVALS, "locks", "pig-0.5.pgo")
        self.assertEqual(built["pig-0.5-pgo"], profile)
        self.assertIsNone(built["pig-0.5"])
        self.assertEqual(pgo_arm.record["install_method"], "go build -pgo=locks/pig-0.5.pgo")
        self.assertEqual(pgo_arm.record["pgo_sha256"], lock.sha256_file(profile))
        self.assertEqual(default_arm.record["install_method"], "go build -pgo=off")

    def test_the_workspace_refuses_a_profile_that_does_not_match_the_lock(self):
        arm = arms.load()["pig-0.5-pgo"]
        arm.pin = dict(arm.pin, build=dict(arm.pin["build"], pgo_sha256="0" * 64))
        env = {k: v for k, v in os.environ.items() if not k.startswith("PIGBENCH_BIN_")}
        with mock.patch.object(arms, "build_pig") as build, mock.patch.dict(os.environ, env, clear=True), arms.Workspace() as ws:
            with self.assertRaisesRegex(SystemExit, "does not match the locked sha256"):
                ws.install(arm)
        build.assert_not_called()


if __name__ == "__main__":
    unittest.main()

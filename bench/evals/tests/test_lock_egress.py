# SPDX-License-Identifier: MIT
import os
import shutil
import tempfile
import unittest

from pigbench import arms, egress, lock

from . import fakes

PROC_NET = """  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:C350 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 11111 1 0000000000000000 100 0 0 10 0
   1: 0100007F:C351 2F20126A:01BB 01 00000000:00000000 00:00000000 00000000  1000        0 22222 1 0000000000000000 100 0 0 10 0
   2: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 33333 1 0000000000000000 100 0 0 10 0
"""


class LockTest(unittest.TestCase):
    def test_committed_lock_verifies_offline(self):
        self.assertEqual(lock.verify(), [])

    def test_every_toml_arm_is_pinned_with_an_exact_version(self):
        for aid, arm in arms.load().items():
            self.assertRegex(arm.version, r"^\d+\.\d+\.\d+$", aid)

    def _scratch(self):
        """A copy of the lock and its package-lock files that tests may damage; lock.EVALS and lock.LOCK point at it until cleanup."""
        tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, tmp, True)
        shutil.copytree(os.path.join(lock.EVALS, "locks"), os.path.join(tmp, "locks"))
        shutil.copy(lock.LOCK, os.path.join(tmp, "arms.lock"))
        saved = (lock.EVALS, lock.LOCK)
        lock.EVALS, lock.LOCK = tmp, os.path.join(tmp, "arms.lock")
        self.addCleanup(lambda: setattr(lock, "EVALS", saved[0]) or setattr(lock, "LOCK", saved[1]))
        return tmp

    def test_tampered_package_lock_is_detected(self):
        tmp = self._scratch()
        path = os.path.join(tmp, lock.read()["arms"]["pi-1.1.0"]["package_lock"])
        with open(path) as f:
            text = f.read()
        with open(path, "w") as f:
            f.write(text.replace('"1.1.0"', '"1.1.1"', 1))
        self.assertTrue(any("package_lock_sha256" in p for p in lock.verify()))

    def test_a_rewritten_lock_keeps_its_bytes_and_hash(self):
        # Text mode wrote CRLF on Windows: a lock bumped there changed arms_lock_sha256, and aggregate refuses runs of two locks.
        self._scratch()
        with open(lock.LOCK, "rb") as f:
            before, digest = f.read(), lock.lock_hash()
        lock.write(lock.read())
        with open(lock.LOCK, "rb") as f:
            self.assertEqual(f.read(), before)
        self.assertEqual(lock.lock_hash(), digest)

    def test_wrong_integrity_in_lock_is_detected(self):
        self._scratch()
        data = lock.read()
        data["arms"]["codex"]["integrity"] = "sha512-AAAA"
        lock.write(data)
        self.assertTrue(any("integrity" in p for p in lock.verify()))

    def test_a_tool_pin_without_a_checksum_or_size_is_detected(self):
        self._scratch()
        for damage in ({"sha256": "abc"}, {"size": None}):
            data = lock.read()
            data["tools"]["hyperfine"]["assets"]["linux-amd64"].update(damage)
            lock.write(data)
            self.assertTrue(any(p.startswith("tool hyperfine:") for p in lock.verify()), damage)

    def test_hyperfine_checksum_is_the_one_of_the_release_asset(self):
        # sha256 and size of hyperfine-v2.0.0-x86_64-unknown-linux-musl.tar.gz as downloaded from the GitHub release (the release publishes no
        # checksum file), computed by the reviewer independently of the lane's pin.
        a = lock.read()["tools"]["hyperfine"]["assets"]["linux-amd64"]
        self.assertEqual((a["sha256"], a["size"]), ("38e59087bd65520930a13cf513d8a493b7043741e8ad62349a8a9929eb61bc5c", 737116))

    def test_pig_release_checksum_is_the_published_one(self):
        # SHA256SUMS of the PiG v0.4.1 GitHub release (linux-amd64), copied from the release asset, not from this repository's code.
        self.assertEqual(lock.read()["arms"]["pig-0.4.1"]["assets"]["linux-amd64"]["sha256"], "3b4de3587cbfb617def483ec62dddcf35827acc3436b1454cc5694c22dcdaf3f")

    def test_pi_is_locked_to_the_install_pair_its_release_publishes(self):
        # SHA256SUMS of the Pi v1.1.0 GitHub release lists these two files; they are copied from the release, not computed here.
        pi = lock.read()["arms"]["pi-1.1.0"]
        self.assertEqual(pi["package_lock_sha256"], "89a53fa67297177bad42d71e9eb9c7eaeb9c7fc34d3cfb6800c1b38fbc293ebb")
        self.assertEqual(pi["package_json_sha256"], "3ec8bbe1bccbf4284d816a7defc2185c1f7e0d81df10b8dc1fb84743f23175f9")
        self.assertEqual(lock.sha256_file(os.path.join(lock.EVALS, "locks/pi-1.1.0.package-lock.json")), pi["package_lock_sha256"])

    def test_claude_executable_matches_the_vendors_own_manifest(self):
        # linux-x64 entry of https://downloads.claude.ai/claude-code-releases/2.1.296/manifest.json (checksum and size), independent of the npm package.
        claude = lock.read()["arms"]["claude"]
        self.assertEqual(claude["bin_sha256"]["linux-amd64"], "24972e3bc859fab2b46ed4c1e51f7d6130f06d3bd550811a114640de3370d0de")
        self.assertEqual(claude["bin_size"]["linux-amd64"], 257068216)

    def test_machine_level_configuration_fails_the_install(self):
        with tempfile.TemporaryDirectory() as tmp:
            saved = lock.read
            data = saved()
            data["absent_paths"] = {"pi": [tmp]}
            lock.read = lambda: data
            try:
                arm = arms.load()["pi-1.1.0"]
                arm.pin = data["arms"]["pi-1.1.0"]
                with arms.Workspace() as ws:
                    with self.assertRaises(SystemExit) as caught:
                        ws.install(arm)
                self.assertIn("machine-level configuration", str(caught.exception))
            finally:
                lock.read = saved

    def test_version_mismatch_fails_the_install_check(self):
        with tempfile.TemporaryDirectory() as tmp:
            fake = fakes.python_command(os.path.join(tmp, "tool"), "print('9.9.9')\n")
            arm = arms.load()["pi-1.1.0"]
            arm.binary = fake
            with self.assertRaises(SystemExit):
                arms.check_version(arm)


class EgressTest(unittest.TestCase):
    def test_parse_proc_net(self):
        table = egress.parse_proc_net(PROC_NET)
        self.assertEqual(table[11111], ("127.0.0.1", 8080))
        self.assertEqual(table[22222], ("106.18.32.47", 443))  # 0x2F20126A little-endian = 106.18.32.47
        self.assertTrue(egress.is_local(table[11111][0]))
        self.assertFalse(egress.is_local(table[22222][0]))
        self.assertTrue(egress.is_local(table[33333][0]))  # listening: no remote

    def test_scan_flags_only_non_loopback(self):
        orig = egress.socket_inodes
        try:
            egress.socket_inodes = lambda pid: {11111, 22222}
            self.assertEqual(egress.scan([1], read=lambda name: PROC_NET if name == "tcp" else ""), {"106.18.32.47:443"})
            egress.socket_inodes = lambda pid: {11111}
            self.assertEqual(egress.scan([1], read=lambda name: PROC_NET), set())
        finally:
            egress.socket_inodes = orig

    def test_ipv6_loopback(self):
        self.assertTrue(egress.is_local("::1"))
        self.assertTrue(egress.is_local("::ffff:127.0.0.1"))
        self.assertFalse(egress.is_local("2001:db8::1"))


if __name__ == "__main__":
    unittest.main()

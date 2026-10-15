# SPDX-License-Identifier: MIT
"""The variants bench: executables as arms, each with an A/A copy, and the summary that reads the noise floor off the copy."""
import argparse
import json
import os
import tempfile
import unittest
from unittest import mock

from pigbench import arms as armlib
from pigbench import overhead, variants


class ParseArmTest(unittest.TestCase):
    def test_flags_and_environment(self):
        arm = variants.parse_arm("pgo=/bin/true,noext,own,GOGC=200", {"GOMEMLIMIT": "256MiB"})
        self.assertEqual(arm.id, "pgo")
        self.assertEqual(arm.family, "pig")
        self.assertTrue(arm.no_extensions)
        self.assertTrue(arm.spec["own_extensions"])
        self.assertEqual(arm.spec["env"], {"GOMEMLIMIT": "256MiB", "GOGC": "200"})
        self.assertEqual(arm.spec["binary"], "/bin/true")

    def test_plain_arm_has_no_flags(self):
        arm = variants.parse_arm("base=/bin/true")
        self.assertFalse(arm.no_extensions)
        self.assertNotIn("env", arm.spec)
        self.assertNotIn("own_extensions", arm.spec)

    def test_malformed_arms_are_refused(self):
        for bad in ("nopath", "=/bin/true", "id=", "id=/bin/true,bogus"):
            with self.subTest(bad), self.assertRaises(SystemExit):
                variants.parse_arm(bad)

    def test_environment_pairs(self):
        self.assertEqual(variants.parse_env(["A=1", "B=x=y"]), {"A": "1", "B": "x=y"})
        with self.assertRaises(SystemExit):
            variants.parse_env(["NOEQUALS"])


class InstallTest(unittest.TestCase):
    def test_a_variant_runs_its_own_executable_and_is_unpinned(self):
        with tempfile.TemporaryDirectory() as d:
            exe = os.path.join(d, "pig")
            with open(exe, "w") as f:
                f.write("#!/bin/sh\n")
            os.chmod(exe, 0o755)
            arm = variants.parse_arm(f"v={exe}")
            with armlib.Workspace() as ws:
                ws.install(arm)
            self.assertEqual(arm.binary, exe)
            self.assertTrue(arm.unpinned)


class OwnExtensionsTest(unittest.TestCase):
    def test_own_extensions_install_no_fixtures(self):
        plain, own = variants.parse_arm("a=/bin/true"), variants.parse_arm("b=/bin/true,own")
        self.assertEqual(len(overhead.arm_extensions(plain, True)), 10)
        self.assertEqual(overhead.arm_extensions(own, True), ())
        self.assertEqual(overhead.arm_extensions(plain, False), ())


class SummaryTest(unittest.TestCase):
    def test_the_copy_is_the_noise_floor(self):
        raw = {"samples": {"print": {
            "base": [{"cpu_ms": 100.0}, {"cpu_ms": 110.0}, {"cpu_ms": 120.0}],
            "base-b": [{"cpu_ms": 102.0}, {"cpu_ms": 115.0}, {"cpu_ms": 121.0}],
            "fast": [{"cpu_ms": 90.0}, {"cpu_ms": 95.0}, {"cpu_ms": 99.0}],
            "fast-b": [{"cpu_ms": 91.0}, {"cpu_ms": 95.0}, {"cpu_ms": 100.0}]}}}
        text = variants.summarize(raw)
        self.assertIn("| cpu_ms | 110.0 | 115.0 | +4.5% | 95.0 | 95.0 | +0.0% |", text)
        self.assertNotIn("base-b |", text.splitlines()[2])


class RunTest(unittest.TestCase):
    def run_variants(self, out, *arm_args):
        args = argparse.Namespace(arm=list(arm_args), env=["GOGC=200"], out=out, arms=None, aa=None)
        seen = {}

        def fake_run(opts):
            seen.update(arms=list(opts.arms), aa=list(opts.aa), arm_list=list(opts.arm_list))
            return {"ok": True, "samples": {}}
        with mock.patch.object(overhead, "run", fake_run):
            result = variants.run(args)
        return result, seen

    def test_every_variant_runs_with_its_a_a_copy(self):
        with tempfile.TemporaryDirectory() as d:
            result, seen = self.run_variants(d, "a=/bin/true", "b=/bin/sh,GOGC=400")
            self.assertTrue(result["ok"])
            self.assertEqual(seen["arms"], ["a", "b"])
            self.assertEqual(seen["aa"], ["a", "b"])
            self.assertEqual([a.spec["env"] for a in seen["arm_list"]], [{"GOGC": "200"}, {"GOGC": "400"}])
            with open(os.path.join(d, "variants.json")) as f:
                recorded = json.load(f)["arms"]
            self.assertEqual(recorded["b"]["binary"], "/bin/sh")
            self.assertEqual(recorded["b"]["size"], os.path.getsize("/bin/sh"))
            self.assertEqual(len(recorded["b"]["sha256"]), 64)
            self.assertTrue(os.path.exists(os.path.join(d, "variants.md")))

    def test_ids_must_be_unique_and_not_the_copy_suffix(self):
        with tempfile.TemporaryDirectory() as d:
            for arm_args in (("a=/bin/true", "a=/bin/sh"), ("a-b=/bin/true",)):
                with self.assertRaises(SystemExit):
                    self.run_variants(d, *arm_args)


if __name__ == "__main__":
    unittest.main()

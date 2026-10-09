# SPDX-License-Identifier: MIT
import importlib.util
import json
import os
import pathlib
import tempfile
import unittest
from unittest import mock

MODULE = pathlib.Path(__file__).with_name("startup_timing.py")
spec = importlib.util.spec_from_file_location("startup_timing", MODULE)
timing = importlib.util.module_from_spec(spec)
spec.loader.exec_module(timing)


class StartupTimingTests(unittest.TestCase):
    def test_environment_never_inherits_worker_state(self):
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(os.environ, {
                "PIG_CODING_AGENT_DIR": "/worker/auth", "PI_CODING_AGENT_DIR": "/worker/pi", "OPENAI_API_KEY": "worker-secret"}):
            home = pathlib.Path(directory) / "home"
            env = timing.environment(home)
            self.assertEqual(env["HOME"], str(home))
            self.assertEqual(env["PIG_CODING_AGENT_DIR"], str(home / "pig/agent"))
            self.assertEqual(env["PI_CODING_AGENT_DIR"], str(home / "pi-agent"))
            self.assertNotIn("OPENAI_API_KEY", env)
            self.assertTrue(pathlib.Path(env["TMPDIR"]).is_relative_to(directory))

    def test_summary_uses_cache_specific_controls_and_excludes_seeds(self):
        rows = []
        for build, case, cache, value in [("pig", "package", "warm", 110), ("pi", "package", "warm", 100),
                                           ("pig", "package", "cold", 120), ("pi", "package", "cold", 100),
                                           ("pig", "none", "warm", 104), ("baseline", "none", "warm", 100)]:
            rows.append({"build": build, "kind": case, "cache": cache, "request_ms": value, "run": 0, "registration": False})
        rows.append({"build": "pig", "kind": "package", "cache": "warm", "request_ms": 9999, "run": "seed", "registration": False})
        data = timing.summarize(rows, {"extension_over_pi": 1.15, "no_extensions_over_baseline": 1.05})
        pig = {(r["kind"], r["cache"]): r for r in data if r["build"] == "pig"}
        self.assertTrue(pig["package", "warm"]["within_budget"])
        self.assertFalse(pig["package", "cold"]["within_budget"])
        self.assertTrue(pig["none", "warm"]["within_budget"])
        self.assertEqual(pig["package", "warm"]["median_ms"], 110)

    def test_trial_rejects_malformed_output_even_with_valid_final_events(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            binary = root / "fake"
            binary.write_text('#!/usr/bin/env python3\nimport json\nprint("not JSON")\n'
                              'print(json.dumps({"type":"message_start","message":{"role":"assistant"}}))\n'
                              'print(json.dumps({"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"42"}]}}))\n'
                              'print(json.dumps({"type":"agent_end"}))\n')
            binary.chmod(0o755)
            row = timing.trial(str(binary), "pig", "none", "cold", 0, root / "home", root, root)
            self.assertFalse(row["valid"])
            self.assertTrue(row["malformed_json"])
            self.assertEqual(row["returncode"], 0)
            self.assertIn("not JSON", (root / row["stdout"]).read_text())


if __name__ == "__main__":
    unittest.main()

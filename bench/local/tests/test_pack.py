# SPDX-License-Identifier: MIT
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import pack  # noqa: E402


class TurnRowTest(unittest.TestCase):
    def test_separates_harness_time_from_model_time(self):
        records = [{"t_start": 10_200_000_000, "t_first_byte": 10_700_000_000, "t_end": 11_000_000_000, "prompt_tokens": 900, "request_bytes": 4000, "tools": 7, "system_bytes": 2500},
                   {"t_start": 11_100_000_000, "t_first_byte": 11_300_000_000, "t_end": 11_500_000_000, "prompt_tokens": 950}]
        events = [(10_500_000_000, "agent_start"), (10_750_000_000, "message_update"), (11_400_000_000, "message_update")]
        row = pack.turn_row(3, 10_000_000_000, 11_600_000_000, 40, records, events)
        self.assertEqual(row["harness_before_request_ms"], 200.0)       # the harness's own work before the first request left
        self.assertEqual(row["harness_after_first_byte_ms"], 50.0)      # from the server's first byte to the first streamed event
        self.assertEqual((row["model_ms"], row["harness_ms"]), (1200.0, 400.0))  # 0.8 s + 0.4 s in flight; the other 0.4 s of the 1.6 s turn is the harness
        self.assertEqual((row["prompt_tokens"], row["tools"], row["system_bytes"], row["requests"]), (900, 7, 2500, 2))
        self.assertAlmostEqual(row["generation_cpu_share"], 0.0333, places=4)

    def test_a_tool_less_side_request_is_not_the_prompt_cost(self):
        records = [{"t_start": 10_100_000_000, "t_end": 10_200_000_000, "prompt_tokens": 545, "tools": 0, "system_bytes": 90, "request_bytes": 700},
                   {"t_start": 10_300_000_000, "t_end": 11_000_000_000, "prompt_tokens": 19454, "tools": 12, "system_bytes": 9000, "request_bytes": 80000}]
        row = pack.turn_row(0, 10_000_000_000, 11_100_000_000, 1, records, [])
        self.assertEqual((row["prompt_tokens"], row["tools"], row["system_bytes"]), (19454, 12, 9000))
        self.assertEqual(row["harness_before_request_ms"], 100.0)  # the first request of any kind marks when the harness started talking to the model
        self.assertEqual(row["requests"], 2)

    def test_a_turn_without_requests_has_no_model_fields(self):
        row = pack.turn_row(0, 1_000_000_000, 1_200_000_000, 5, [], [])
        self.assertEqual(row["requests"], 0)
        self.assertNotIn("prompt_tokens", row)

    def test_dir_bytes_counts_files_recursively(self):
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            os.makedirs(os.path.join(d, "a", "b"))
            for rel, n in (("x", 3), ("a/y", 5), ("a/b/z", 7)):
                with open(os.path.join(d, rel), "wb") as f:
                    f.write(b"." * n)
            self.assertEqual(pack.dir_bytes(d), 15)


    def test_egress_of_every_run_is_kept_and_summarized(self):
        from types import SimpleNamespace
        out = {"arm": "x", "turns": [], "rss_mib": {}}
        pack.note_egress(out, SimpleNamespace(egress={"203.0.113.9:443"}))
        pack.note_egress(out, SimpleNamespace(egress=set()))
        pack.note_egress(out, SimpleNamespace(egress={"198.51.100.2:443", "203.0.113.9:443"}))
        self.assertEqual(out["egress"], ["198.51.100.2:443", "203.0.113.9:443"])
        self.assertEqual(pack.summarize(out)["egress"], out["egress"])
        clean = {"arm": "y", "turns": [], "rss_mib": {}}
        pack.note_egress(clean, SimpleNamespace(egress=set()))
        self.assertNotIn("egress", clean)


class TaskFixtureTest(unittest.TestCase):
    def test_the_pinned_task_fails_before_and_passes_with_the_fix(self):
        import json
        import shutil
        import subprocess
        import tempfile
        spec = json.load(open(os.path.join(pack.TASKS, "rangesum", "task.json")))
        with tempfile.TemporaryDirectory() as d:
            shutil.copytree(os.path.join(pack.TASKS, "rangesum"), d, dirs_exist_ok=True)
            before = subprocess.run(spec["check"], cwd=d, capture_output=True, text=True, timeout=60)
            self.assertNotEqual(before.returncode, 0)  # the check must not pass on the untouched task
            path = os.path.join(d, "rangesum.py")
            fixed = open(path).read().replace("range(start, end)", "range(start, end + 1)")
            open(path, "w").write(fixed)
            after = subprocess.run(spec["check"], cwd=d, capture_output=True, text=True, timeout=60)
            self.assertEqual(after.returncode, 0, after.stderr)


    def solve(self, d, source=None, tests=None, extra=None):
        import shutil
        shutil.copytree(os.path.join(pack.TASKS, "rangesum"), d, dirs_exist_ok=True)
        if source:
            path = os.path.join(d, "rangesum.py")
            with open(path) as f:
                text = f.read()
            with open(path, "w") as f:
                f.write(text.replace("range(start, end)", source))
        if tests is not None:
            with open(os.path.join(d, "test_rangesum.py"), "w") as f:
                f.write(tests)
        for name, text in (extra or {}).items():
            with open(os.path.join(d, name), "w") as f:
                f.write(text)

    def check(self, **kwargs):
        import json
        import tempfile
        with open(os.path.join(pack.TASKS, "rangesum", "task.json")) as f:
            spec = json.load(f)
        with tempfile.TemporaryDirectory() as d:
            self.solve(d, **kwargs)
            return pack.check_task("rangesum", d, spec, d).returncode

    def test_the_check_sees_only_the_editable_files(self):
        self.assertEqual(self.check(source="range(start, end + 1)"), 0)  # a real fix passes
        self.assertNotEqual(self.check(tests="import unittest\n\nclass T(unittest.TestCase):\n    def test(self):\n        pass\n"), 0)  # rewritten tests do not count
        self.assertNotEqual(self.check(tests=""), 0)  # nor deleted ones
        shadow = "import sys\nsys.exit(0)\n"
        self.assertNotEqual(self.check(extra={"sitecustomize.py": shadow, "unittest.py": shadow}), 0)  # nor a module that shadows the check's


if __name__ == "__main__":
    unittest.main()

# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Unit tests for the bench/compare harness: statistics, failure accounting, the scripted mock, the report and the lock cross-check.

  python3 -m unittest discover -s bench/compare/tests
"""
import concurrent.futures
import json
import os
import statistics
import subprocess
import sys
import tempfile
import time
import unittest
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

import agent_scale  # noqa: E402
import arms  # noqa: E402
import ctx_scale  # noqa: E402
import fake_server  # noqa: E402
import run_suite  # noqa: E402
import tokcost  # noqa: E402


class Mock:
    def __init__(self, *args):
        self.proc, self.port = fake_server.start(HERE / "fake_model_ctx.py", *args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        end = time.time() + 10
        while not fake_server.served(self.port):
            if self.proc.poll() is not None or time.time() > end:
                self.close()
                raise RuntimeError("mock did not start")
            time.sleep(0.02)

    def post(self, body=None):
        data = json.dumps(body or {"messages": [{"role": "user", "content": "hi"}]}).encode()
        req = urllib.request.Request(f"http://127.0.0.1:{self.port}/v1/chat/completions", data=data, headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=10) as r:
                r.read()
                return r.status
        except urllib.error.HTTPError as e:
            return e.code

    def close(self):
        self.proc.terminate()
        self.proc.wait(timeout=10)


class P95(unittest.TestCase):
    def test_two_samples_is_the_larger(self):
        # The at100k stage holds two prompts; the old index int(n*.95)-1 picked the smaller one and the report showed p95 below p50.
        self.assertEqual(ctx_scale.p95([477, 371]), 477)

    def test_never_below_the_median(self):
        for n in range(1, 41):
            xs = [((i * 7919) % 101) + i / 1000 for i in range(n)]
            self.assertGreaterEqual(ctx_scale.p95(xs), statistics.median(xs), n)

    def test_nearest_rank_at_twenty_and_more(self):
        self.assertEqual(ctx_scale.p95(list(range(1, 21))), 19)
        self.assertEqual(ctx_scale.p95(list(range(1, 101))), 95)


class AgentScaleFailures(unittest.TestCase):
    def test_exited_session_is_a_failure(self):
        up, stuck = ["a", "b", "c"], ["b"]
        self.assertEqual(agent_scale.lost_sessions(up, stuck, lambda t: t != "c"), ["b", "c"])

    def test_answered_and_alive_is_not(self):
        self.assertEqual(agent_scale.lost_sessions(["a"], [], lambda t: True), [])


class FakeModel(unittest.TestCase):
    def test_fail_nth_without_dump(self):
        m = Mock("--fail-nth", "2")
        try:
            self.assertEqual([m.post(), m.post(), m.post()], [200, 429, 200])
        finally:
            m.close()

    def test_concurrent_requests_dump_one_file_each(self):
        with tempfile.TemporaryDirectory() as d:
            m = Mock("--dump", d)
            try:
                n = 96
                with concurrent.futures.ThreadPoolExecutor(32) as ex:
                    codes = list(ex.map(lambda i: m.post({"messages": [{"role": "user", "content": f"request {i}"}]}), range(n)))
            finally:
                m.close()
            self.assertEqual(codes, [200] * n)
            files = sorted(os.listdir(d))
            self.assertEqual(len(files), n)
            bodies = {json.loads(Path(d, f).read_text())["messages"][0]["content"] for f in files}
            self.assertEqual(len(bodies), n)


class RunSuite(unittest.TestCase):
    def test_tool_exit_status_is_recorded(self):
        with tempfile.TemporaryDirectory() as d:
            bench, out = Path(d, "bench"), Path(d, "out")
            bench.mkdir()
            out.mkdir()
            (bench / "fails.py").write_text("import sys\nsys.exit(3)\n")
            run_suite.tool(bench, "fails.py", [], out / "fails-r0.txt", 60)
            rows = [json.loads(line) for line in (out / "status.jsonl").read_text().splitlines()]
            self.assertEqual([(r["tool"], r["out"], r["rc"]) for r in rows], [("fails.py", "fails-r0.txt", 3)])


class Report(unittest.TestCase):
    def render(self, files):
        with tempfile.TemporaryDirectory() as d:
            for name, rows in files.items():
                Path(d, name).write_text("".join(json.dumps(r) + "\n" for r in rows) if isinstance(rows, list) else rows)
            subprocess.run([sys.executable, str(HERE / "report.py"), "--out", d, "--arms", "pig-tip,gi,omp"], check=True, capture_output=True, timeout=60)
            return Path(d, "report.md").read_text()

    def row(self, md, prefix):
        return next(line for line in md.splitlines() if line.startswith(f"| {prefix}"))

    def test_long_session_cannot_run_comes_from_the_runner(self):
        long_rows = [{"build": "pig-tip", "prompt": p, "ms_per_prompt": 100 + p, "cpu_ms_per_prompt": 90, "own_mb": 50, "rss_mb": 80, "tokens_m": 1} for p in (25, 50, 75)]
        md = self.render({"long.jsonl": long_rows})
        cells = [c.strip() for c in self.row(md, "Time per prompt, long session").split("|")[2:-1]]
        self.assertEqual(cells, ["175 (n=1)", "cannot run", "cannot run"])
        self.assertIn("gi", run_suite.CANNOT)
        self.assertIn("long", run_suite.CANNOT["omp"])

    def test_failed_tool_invocations_are_listed(self):
        md = self.render({"status.jsonl": [{"tool": "long_session.py", "out": "long-r2.txt", "rc": "timeout", "wall_s": 7200}, {"tool": "idle.py", "out": "idle-r2.txt", "rc": 0}]})
        self.assertIn("| long_session.py | long-r2.txt | timeout | 7200 |", md)
        self.assertNotIn("| idle.py |", md)

    def test_negative_idle_cpu_is_not_pooled(self):
        idle = "\n".join([
            "{'build': 'pig-tip', 'cpu_ms_per_s': 0.5, 'own_mb': [30, 31], 'threads': {}}",
            "{'build': 'omp', 'cpu_ms_per_s': -31.5, 'own_mb': [400, None], 'threads': {}}",
        ]) + "\n"
        md = self.render({"idle-r0.txt": idle})
        cells = [c.strip() for c in self.row(md, "Idle CPU (ms/s)").split("|")[2:-1]]
        self.assertEqual(cells, ["0.50 (n=1)", "-", "invalid; 1 negative reading(s) excluded"])


class Cost(unittest.TestCase):
    def test_bounds(self):
        price = {"in": 3.0, "out": 15.0, "cache_read": 0.3, "cache_write": 3.75}
        no_cache, full_cache = tokcost.session_cost(price, 1_000_000, 800_000, 4, 190)
        out = 4 * 190 * 15.0 / 1e6
        self.assertAlmostEqual(no_cache, 3.0 + out)
        self.assertAlmostEqual(full_cache, 200_000 * 3.0 / 1e6 + 800_000 * 0.3 / 1e6 + out)
        self.assertGreater(no_cache, full_cache)

    def test_prefix_larger_than_billed_input_is_capped(self):
        price = {"in": 1.0, "out": 0.0, "cache_read": 0.1, "cache_write": 0}
        self.assertAlmostEqual(tokcost.session_cost(price, 100, 500, 0, 0)[1], 100 * 0.1 / 1e6)


class EvalsLock(unittest.TestCase):
    EVALS = {"arms": {"pibolt": {"version": "0.7.4", "release": {"base": "https://x/releases/download/bolt-v{version}/"},
                                 "assets": {"linux-amd64": {"name": "pi-bolt-linux-x64.tar.gz", "sha256": "aa"}}}}}

    def test_same_artifact_other_hash_is_reported(self):
        lock = {"arm": {"pibolt-0.7.4": {"url": "https://x/releases/download/bolt-v0.7.4/pi-bolt-linux-x64.tar.gz", "sha256": "bb"}}}
        self.assertEqual(len(arms.evals_mismatches(lock, self.EVALS)), 1)

    def test_other_version_same_file_name_is_not_compared(self):
        lock = {"arm": {"pibolt-0.7.3": {"url": "https://x/releases/download/bolt-v0.7.3/pi-bolt-linux-x64.tar.gz", "sha256": "bb"}}}
        self.assertEqual(arms.evals_mismatches(lock, self.EVALS), [])

    def test_equal_hash_passes(self):
        lock = {"arm": {"pibolt-0.7.4": {"url": "https://x/releases/download/bolt-v0.7.4/pi-bolt-linux-x64.tar.gz", "sha256": "aa"}}}
        self.assertEqual(arms.evals_mismatches(lock, self.EVALS), [])


class SelfArms(unittest.TestCase):
    def test_committed_lock_has_no_problems(self):
        self.assertEqual(arms.self_arm_problems(arms.LOCK), [])

    def test_private_remote_ref_and_short_sha_are_reported(self):
        lock = {"arm": {"pig-x": {"kind": "go-source", "adapter": "pig", "repo": "git@host:o/private.git", "ref": "team/x", "commit": "cd8f1cf524"},
                         "gi": {"kind": "go-source", "adapter": "gi", "repo": "https://github.com/rcarmo/gi", "commit": "7078df0d59"}}}
        self.assertEqual(len(arms.self_arm_problems(lock)), 3)


if __name__ == "__main__":
    unittest.main()

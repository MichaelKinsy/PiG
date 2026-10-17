# SPDX-License-Identifier: MIT
"""Regression tests for bugs the first full overhead run exposed."""
import math
import pathlib
import random
import statistics
import time
import unittest

from pigbench import mock, overhead, report

from . import fakes


class FakeRpc:
    """Replays what Pi 1.1.0 emitted at the 365th turn of a long session: the next prompt arrives while automatic compaction runs."""

    def __init__(self, script):
        self.script, self.records, self.cursor, self.sent = list(script), [], 0, []

    def send(self, cmd):
        self.sent.append(cmd)
        for rec in self.script.pop(0):
            self.records.append((mock.now_ns(), rec))
        return 0, mock.now_ns()

    def wait_for(self, predicate, timeout, start=0):
        for i in range(start, len(self.records)):
            if predicate(self.records[i][1]):
                return self.records[i][0], self.records[i][1], i + 1
        return None


class Meter:
    def cpu_ms(self):
        return 0.0

    def max_proc_rss_mib(self):
        return 1.0


class Opts:
    timeout = 1


class Ctx:
    server = mock.MockServer.__new__(mock.MockServer)
    opts = Opts()


Ctx.server.requests = []

BUSY = {"type": "response", "command": "prompt", "success": False, "error": "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message."}


class DriveTurnsTest(unittest.TestCase):
    def test_prompt_refused_during_compaction_is_resent_after_it_ends(self):
        rpc = FakeRpc([[{"type": "agent_end"}], [{"type": "compaction_start"}, BUSY, {"type": "compaction_end"}, {"type": "agent_settled"}], [{"type": "agent_end"}]])
        rpc.records.append((0, {"type": "response"}))
        series = []
        bad = overhead.drive_turns(Ctx, rpc, Meter(), 2, series)
        self.assertIsNone(bad)
        self.assertEqual(len(series), 2)
        self.assertEqual([c["type"] for c in rpc.sent], ["prompt"] * 3)  # turn 1 was sent twice: once refused, once after compaction_end

    def test_the_compaction_in_progress_refusal_is_also_retried(self):
        busy = dict(BUSY, error="Cannot submit a prompt while compaction is in progress. Wait for compaction to finish and retry.")
        rpc = FakeRpc([[{"type": "agent_end"}], [busy, {"type": "compaction_end"}], [{"type": "agent_end"}]])
        rpc.records.append((0, {"type": "response"}))
        self.assertIsNone(overhead.drive_turns(Ctx, rpc, Meter(), 2, []))

    def test_a_harness_that_never_finishes_is_reported_with_its_last_events(self):
        rpc = FakeRpc([[{"type": "turn_end"}]])
        bad = overhead.drive_turns(Ctx, rpc, Meter(), 1, [])
        self.assertIn("no agent_end", bad)
        self.assertIn("turn_end", bad)


class ExclusionReportTest(unittest.TestCase):
    def test_excluded_cli_egress_is_listed_not_failed(self):
        text = report.egress_block({"mode": "socket-audit", "violations": [{"arm": "claude", "scenario": "pty", "remotes": ["203.0.113.9:443"], "excluded": True}]})
        self.assertIn("excluded from that scenario", text)
        self.assertNotIn("The bench fails", text)
        hard = report.egress_block({"mode": "socket-audit", "violations": [{"arm": "pi-1.1.0", "scenario": "pty", "remotes": ["203.0.113.9:443"]}]})
        self.assertIn("The bench fails", hard)


class IsolationReportTest(unittest.TestCase):
    def test_isolation_and_outside_writes_are_reported(self):
        raw = {"isolation": {"claude": {"set": {"HOME": "<work>/claude/home", "ANTHROPIC_API_KEY": "<dummy key>"}, "scrubbed": ["GITHUB_TOKEN"]}}, "outside_writes": ["~/.claude"], "outside_modified": ["~/.codex"]}
        text = report.isolation_block(raw)
        self.assertIn("`ANTHROPIC_API_KEY=<dummy key>`", text)
        self.assertIn("GITHUB_TOKEN", text)
        self.assertIn("created under the real home by this run: ~/.claude. The bench fails", text)
        self.assertIn("possibly another process", text)
        self.assertEqual(report.isolation_block({}), "")


class DurableVerdictTest(unittest.TestCase):
    RULES = {"loss_if_slower_than": 1.03, "win_if_faster_by": 1.3, "slightly_better_below": 0.97}

    def test_verdict_boundaries(self):
        from pigbench import durable_report as d
        self.assertEqual(d.verdict(1.04, self.RULES), "loss")
        self.assertEqual(d.verdict(1.03, self.RULES), "tie")  # exactly 3% slower is not more than 3%
        self.assertEqual(d.verdict(1.0, self.RULES), "tie")
        self.assertEqual(d.verdict(0.96, self.RULES), "slightly better")
        self.assertEqual(d.verdict(0.8, self.RULES), "slightly better")
        self.assertEqual(d.verdict(1 / 1.3, self.RULES), "win")
        self.assertEqual(d.verdict(0.5, self.RULES), "win")

    def test_seed_fingerprints_are_checked_and_missing_metadata_fails(self):
        import json
        import os
        import tempfile
        from pigbench import durable
        work = {"published_fingerprints": {"50": "b017b487524e44a4", "250": "dcea9f30b0917245", "1000": "ac520308146f2a8f"}}
        with tempfile.TemporaryDirectory() as cache:
            with open(os.path.join(cache, "pi-50.sqlite.json"), "w") as f:
                json.dump({"fingerprint": "b017b487524e44a4"}, f)
            with open(os.path.join(cache, "pi-250.sqlite.json"), "w") as f:
                json.dump({"fingerprint": "0000000000000000"}, f)
            result = {"failures": {}}
            durable.check_seed_fingerprints(result, work, cache, [50, 250, 1000, 7])  # 7 has no published fingerprint
        self.assertEqual(result["seed_fingerprints"], {"50": "b017b487524e44a4", "250": "0000000000000000"})
        self.assertNotIn("50", result["failures"])
        self.assertIn("differs from the published dcea9f30b0917245", result["failures"]["250"]["seed"][0])
        self.assertIn("no readable seed metadata pi-1000.sqlite.json", result["failures"]["1000"]["seed"][0])  # a fixture that cannot be checked fails the run
        self.assertNotIn("7", result["failures"])

    def test_warm_turns_are_pooled_not_medians_of_medians(self):
        from pigbench import durable_report as d
        samples = [{"turns": [900, 1, 1, 1, 1, 1, 1, 1, 1, 1]}, {"turns": [900, 10, 10, 10, 10, 10, 10, 10, 10, 10]}]
        pooled = d.pooled_warm(samples)
        self.assertEqual(len(pooled), 18)  # the cold turn of each sample is excluded
        self.assertEqual(sorted(pooled)[8:10], [1, 10])


class RatioTest(unittest.TestCase):
    def test_ratio_of_medians_and_aa_equivalence(self):
        from pigbench import stats
        r = stats.ratio_of_medians([2.0, 2.2, 1.8, 2.0, 2.1], [1.0, 1.1, 0.9, 1.0, 1.05], seed=1)
        self.assertAlmostEqual(r["ratio"], 2.0 / 1.0)  # medians 2.0 and 1.0
        self.assertLess(r["ci"][0], 2.0)
        self.assertGreater(r["ci"][1], 2.0)
        groups = {"pi-1.1.0": [{"wall_ms": 100.0 + i} for i in range(6)], "pig-0.5": [{"wall_ms": 50.0 + i} for i in range(6)],
                  "pig-0.5-b": [{"wall_ms": 50.0 + i} for i in range(6)]}
        text = report.aa_table(groups, ["wall_ms"], 1)
        self.assertIn("pig-0.5 / pig-0.5-b", text)
        self.assertIn("1.00 [", text)  # identical samples: the point estimate is exactly 1
        self.assertIn("equivalent", text)
        shifted = dict(groups, **{"pig-0.5-b": [{"wall_ms": 80.0 + i} for i in range(6)]})
        self.assertIn("noisy", report.aa_table(shifted, ["wall_ms"], 1))
        self.assertIn("0.51", report.ratio_table(groups, ["wall_ms"], 1))

    def test_a_zero_reference_median_widens_the_interval_instead_of_being_dropped(self):
        from pigbench import stats
        # A resample of [0, 5, 5] has median 0 about a quarter of the time; those resamples have an infinite ratio, so the upper bound is infinite.
        r = stats.ratio_of_medians([10.0, 10.0, 10.0], [0.0, 5.0, 5.0], seed=1)
        self.assertAlmostEqual(r["ratio"], 2.0)
        self.assertEqual(r["ci"][1], math.inf)
        self.assertEqual(stats.ratio_of_medians([1.0, 2.0, 3.0], [0.0, 0.0, 0.0], seed=1)["ratio"], math.inf)  # no exception
        self.assertTrue(all(math.isnan(x) for x in stats.ratio_of_medians([0.0, 0.0], [0.0, 0.0], seed=1)["ci"]))

    def test_the_upper_percentile_index_rounds_up_as_bootstrap_ci_does(self):
        from pigbench import stats
        a, b = [1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0], [1.0] * 7
        r = stats.ratio_of_medians(a, b, seed=4, resamples=10)
        rng, ratios = random.Random(4), []
        for _ in range(10):
            ratios.append(statistics.median(rng.choices(a, k=7)) / statistics.median(rng.choices(b, k=7)))
        ratios.sort()
        self.assertEqual(r["ci"], [ratios[0], ratios[9]])  # 0.025 * 9 -> 0, ceil(0.975 * 9) = 9; int() gave 8, which differs for this seed


class SoakJudgeTest(unittest.TestCase):
    def series(self, f, n=40, step=60):
        return [{"t_s": i * step, "fds": f(i), "rss_mib": 100 + f(i)} for i in range(n)]

    def test_flat_series_passes_and_a_leak_fails(self):
        from pigbench import soak
        flat = soak.judge(self.series(lambda i: 20 + (i % 2)), "fds")
        self.assertTrue(flat["flat"])
        leak = soak.judge(self.series(lambda i: 20 + i), "fds")  # one descriptor per minute: 20 more over the second half
        self.assertFalse(leak["flat"])
        self.assertAlmostEqual(leak["slope_per_h"], 60.0, places=3)

    def test_warm_up_growth_in_the_first_half_is_ignored(self):
        from pigbench import soak
        warm = soak.judge(self.series(lambda i: min(i, 10) * 3 + 20), "fds")  # grows for ten samples, then flat
        self.assertTrue(warm["flat"])

    def test_memory_band_is_relative(self):
        from pigbench import soak
        s = [{"t_s": i * 60, "rss_mib": 200 + (8 if i % 2 else 0)} for i in range(30)]
        self.assertTrue(soak.judge(s, "rss_mib")["flat"])  # 8 MiB of 200 is inside 10%
        s = [{"t_s": i * 60, "rss_mib": 200 + i * 2} for i in range(30)]
        self.assertFalse(soak.judge(s, "rss_mib")["flat"])

    def test_too_few_samples_is_undecided_not_flat(self):
        from pigbench import soak
        self.assertIsNone(soak.judge([{"t_s": 0, "fds": 1}, {"t_s": 60, "fds": 1}], "fds")["flat"])



class DialectRpc:
    """Answers each send with a scripted batch of records; send returns the request id the way procio.Rpc does."""

    def __init__(self, script, dialect=None):
        self.script, self.records, self.cursor, self.sent, self.n = list(script), [], 0, [], 0
        if dialect is not None:
            self.dialect = dialect

    def send(self, cmd):
        self.n += 1
        ident = f"b{self.n}"
        self.sent.append(dict(cmd, id=ident))
        for rec in self.script.pop(0):
            self.records.append((mock.now_ns(), {k: (ident if v == "<id>" else v) for k, v in rec.items()}))
        return ident, mock.now_ns()

    wait_for = FakeRpc.wait_for


class DialectTest(unittest.TestCase):
    def test_get_state_answered_with_only_the_request_id_is_ready(self):
        rpc = DialectRpc([[{"type": "response", "id": "<id>", "success": True}]])  # rpi: no "command" field
        self.assertIsNotNone(overhead.wait_ready(rpc))
        other = DialectRpc([[{"type": "response", "id": "elsewhere", "success": True}]])
        self.assertIsNone(overhead.wait_ready(other))  # a response to another request is not readiness

    def test_prompt_field_follows_the_arm_dialect(self):
        self.assertEqual(overhead.prompt_command(DialectRpc([]), "hi"), {"type": "prompt", "message": "hi"})
        self.assertEqual(overhead.prompt_command(DialectRpc([], {"prompt_field": "content"}), "hi"), {"type": "prompt", "content": "hi"})

    def test_a_busy_harness_without_compaction_events_is_retried_until_it_accepts(self):
        busy = {"type": "response", "command": "prompt", "success": False, "error": "Agent is currently compacting"}
        rpc = DialectRpc([[busy], [busy], [busy], [{"type": "agent_end"}]])
        series = []
        self.assertIsNone(overhead.drive_turns(Ctx, rpc, Meter(), 1, series))
        self.assertEqual(len(rpc.sent), 4)  # three refusals, each retried, then the turn
        self.assertEqual(series[0]["refused_while_busy"], "Agent is currently compacting")

    def test_third_party_pi_compatible_arms_are_not_native(self):
        from pigbench import arms
        loaded = arms.load()
        self.assertTrue(overhead.native(loaded["pi-1.1.0"]))
        self.assertTrue(overhead.native(loaded["pig-0.5"]))
        self.assertFalse(overhead.native(loaded["rpi"]))  # Pi family, but scored as a third-party CLI

    def test_the_readme_fixture_has_the_same_bytes_on_every_host(self):
        # The read tool returns this file, so request_bytes counts it: CRLF on Windows made the requests one byte per line larger.
        import os
        import tempfile
        from types import SimpleNamespace
        from unittest.mock import patch

        class StubHarness:
            def __init__(self, arm, work, server, tag, **kwargs):
                self.cwd = work

        with tempfile.TemporaryDirectory() as tmp:
            with patch.object(overhead, "Harness", StubHarness):
                overhead.Ctx(tmp, None, None).harness(SimpleNamespace(id="a"), "print")
            with open(os.path.join(tmp, "README.md"), "rb") as f:
                self.assertEqual(f.read(), overhead.README.encode())


class ReleaseInstallTest(unittest.TestCase):
    def setUp(self):
        import io
        import os
        import tarfile
        import tempfile
        from pigbench import arms, lock
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(__import__("shutil").rmtree, self.tmp, True)
        payload = b"#!/bin/sh\necho 0.3.9\n"  # extracted, never run
        self.archive = os.path.join(self.tmp, "tool-v0.3.9.tar.gz")
        with tarfile.open(self.archive, "w:gz") as tar:
            info = tarfile.TarInfo("dist/" + fakes.exe_member("tool"))
            info.size = len(payload)
            tar.addfile(info, io.BytesIO(payload))
        self.sha, self.size = lock.sha256_file(self.archive), os.path.getsize(self.archive)
        self.arms, self.key = arms, "-".join(arms.host_platform())

    def install(self, sha=None, size=None):
        import os
        arm = self.arms.load()["rpi"]
        arm.pin = {"source": "release", "version": "0.3.9", "assets": {self.key: {"name": "tool-v0.3.9.tar.gz", "sha256": sha or self.sha, "size": self.size if size is None else size}},
                   "release": {"base": pathlib.Path(self.tmp).as_uri() + "/", "member": "tool"}}
        with self.arms.Workspace() as ws:
            dest = os.path.join(ws.root, "arm")
            os.makedirs(dest)
            ws._install_release(arm, dest, self.key)
            return os.path.basename(arm.binary), os.access(arm.binary, os.X_OK)

    def test_a_pinned_archive_installs_its_member(self):
        self.assertEqual(self.install(), (fakes.exe_member("tool"), True))

    def test_a_checksum_mismatch_stops_the_install(self):
        with self.assertRaises(SystemExit):
            self.install(sha="0" * 64)

    def test_a_size_mismatch_stops_the_install(self):
        with self.assertRaises(SystemExit):
            self.install(size=self.size + 1)

    def test_bump_skips_only_missing_assets(self):
        import urllib.error
        from pigbench import lock
        entry = {"release": {"base": "https://example.invalid/v{version}/", "sums": "sidecar", "member": "tool",
                             "assets": {"linux-amd64": "tool-{version}-linux.tgz", "darwin-arm64": "tool-{version}-mac.tgz"}}}
        data = b"archive"

        def fetch(url, dest=None):
            if url.endswith("mac.tgz"):
                raise urllib.error.HTTPError(url, 404, "Not Found", None, None)
            return (lock.sha256_bytes(data) + "  x\n").encode() if url.endswith(".sha256") else data

        saved = lock.fetch
        lock.fetch = fetch
        try:
            got = lock.bump_release("t", dict(entry), "1.2.3")
            self.assertEqual(sorted(got["assets"]), ["linux-amd64"])  # the release publishes no macOS asset
            def flaky(url, dest=None):
                if url.endswith("mac.tgz"):
                    raise urllib.error.URLError("timed out")
                return fetch(url, dest) if not url.endswith("mac.tgz") else None

            lock.fetch = flaky
            with self.assertRaises(SystemExit):  # a network failure must not silently drop a platform from the lock
                lock.bump_release("t", dict(entry), "1.2.3")
        finally:
            lock.fetch = saved


class MockRetainTest(unittest.TestCase):
    def test_retain_keeps_the_last_requests_and_counts_all(self):
        import http.client
        import json
        with mock.MockServer(retain=2) as srv:
            for _ in range(3):
                conn = http.client.HTTPConnection("127.0.0.1", srv.port, timeout=10)
                conn.request("POST", "/v1/chat/completions", json.dumps({"messages": [{"role": "user", "content": "x"}]}))
                conn.getresponse().read()
                conn.close()
        self.assertEqual(srv.total, 3)
        self.assertEqual([r.index for r in srv.requests], [1, 2])


class SoakSummaryTest(unittest.TestCase):
    def test_too_few_samples_leave_the_arm_undecided_not_flat(self):
        from pigbench import soak
        s = [{"t_s": 0, "processes": 1, "threads": 5, "fds": 10, "rss_mib": 50.0}, {"t_s": 60, "processes": 1, "threads": 5, "fds": 900, "rss_mib": 500.0}]
        self.assertIsNone(soak.summarize("a", {"series": s, "turns": 3})["flat"])
        self.assertFalse(soak.summarize("a", {"series": s, "turns": 3, "failure": "boom"})["flat"])

    def test_a_flat_run_without_pss_is_flat_and_one_growing_series_is_not(self):
        from pigbench import soak
        flat = [{"t_s": i * 60, "processes": 1, "threads": 9, "fds": 10 + i % 2, "rss_mib": 100.0} for i in range(20)]
        self.assertTrue(soak.summarize("a", {"series": flat})["flat"])  # no pss_mib on this host: judged on the required series
        leak = [dict(r, fds=10 + i) for i, r in enumerate(flat)]
        self.assertFalse(soak.summarize("a", {"series": leak})["flat"])

    def test_small_counts_use_the_absolute_floor(self):
        from pigbench import soak
        s = [{"t_s": i * 60, "fds": 10 + (2 if i % 2 else 0)} for i in range(20)]
        self.assertTrue(soak.judge(s, "fds")["flat"])  # 2 descriptors of 10 is within the floor of 2, though above 5%

    def test_each_arm_reports_its_own_egress_and_others_keep_running(self):
        import threading
        from pigbench import soak

        class M:
            mode = "cgroup-direct"

            def __init__(self, remote):
                self.egress = {remote} if remote else set()

            def snapshot(self):
                return {"processes": 1, "threads": 1, "fds": 1, "rss_mib": 1.0, "pss_mib": None, "cpu_ms": 0}

            def close(self):
                pass

        class R:
            def __init__(self):
                self.cond, self.records, self.cursor = threading.Condition(), [], 0
                self.proc = type("P", (), {"poll": lambda self: 0})()

            def close(self, grace=10):
                pass

        class O:
            interval, think_ms, seconds, out, timeout = 0.05, 0, 0.3, None, 1

        import tempfile
        O.out = tempfile.mkdtemp()
        self.addCleanup(__import__("shutil").rmtree, O.out, True)
        meters = {"x": M("192.0.2.1:443"), "y": M(None)}
        saved = (soak.start_rpc, soak.wait_ready, soak.drive_turns)
        soak.start_rpc = lambda ctx, arm, *a: (None, meters[arm.id], R())
        soak.wait_ready = lambda rpc: (0, {}, 1)
        soak.drive_turns = lambda ctx, rpc, meter, n, rows: ("boom" if meter is meters["x"] else None)
        try:
            class C:
                _harnesses = {}

                def harness(self, arm, scenario, **kw):
                    self._harnesses[(arm.id, scenario)] = type("H", (), {"cwd": "/tmp"})()

            outs = {}
            threads = []
            shared = threading.Event()  # as run() passes it: one abort event for every arm
            for aid in ("x", "y"):
                arm = type("A", (), {"id": aid})()
                server = type("S", (), {"reset": lambda self, **kw: None})()
                outs[aid] = {}
                threads.append(threading.Thread(target=soak.run_arm, args=(arm, C(), server, O, shared, outs[aid])))
            for t in threads:
                t.start()
            for t in threads:
                t.join(10)
        finally:
            soak.start_rpc, soak.wait_ready, soak.drive_turns = saved
        self.assertEqual(outs["x"].get("egress"), ["192.0.2.1:443"])
        self.assertNotIn("egress", outs["y"])  # not attributed to the arm that never connected out
        self.assertEqual(outs["x"]["failure"], "boom")
        self.assertGreater(outs["y"]["turns"], 1)  # x failing at once did not stop y
        self.assertFalse(shared.is_set())


class StartupReportTest(unittest.TestCase):
    def test_hyperfine_is_pinned_and_the_report_says_when_counters_are_missing(self):
        from pigbench import durable_report, lock
        spec = lock.read()["tools"]["hyperfine"]
        self.assertEqual(spec["version"], "2.0.0")
        self.assertEqual(len(spec["assets"]["linux-amd64"]["sha256"]), 64)
        raw = {"kind": "startup", "options": {"rounds": 1, "runs_per_round": 3}, "counters": False, "counters_reason": "unavailable (perf_event_paranoid)",
               "env": {"arms": {}, "seed": 1, "arms_lock_sha256": "x", "tools": {"hyperfine": {"version_line": "hyperfine 2.0.0", "sha256": "ab" * 32, "source_url": "u"}}},
               "samples": {"pi-1.1.0": [{"wall_ms": 300.0, "cpu_ms": 400.0, "peak_rss_mib": 90.0}] * 3, "pig-0.5": [{"wall_ms": 15.0, "cpu_ms": 16.0, "peak_rss_mib": 24.0}] * 3},
               "failures": {}, "limitations": {}, "skipped": {}}
        text = durable_report.startup(raw, "/tmp", 1, [])
        self.assertIn("Instruction counters were not available", text)
        self.assertIn("0.05", text)  # 15 / 300

    def test_a_windows_run_reports_time_only_and_its_failures(self):
        from pigbench import durable_report
        raw = {"kind": "startup", "options": {"rounds": 1, "runs_per_round": 3}, "counters": False, "counters_reason": "Error: Metric 'instructions' is unavailable on this platform",
               "memory_reason": "hyperfine 2.0 has no memory metric on Windows",
               "env": {"arms": {}, "seed": 1, "arms_lock_sha256": "x", "cpu_tick_ms": 15.625, "tools": {"hyperfine": {"version_line": "hyperfine 2.0.0", "sha256": "ab" * 32, "source_url": "u"}}},
               "samples": {"pi-1.1.0": [{"wall_ms": 300.0, "cpu_ms": 312.5}] * 3, "pig-0.5": [{"wall_ms": 15.0, "cpu_ms": 0.0}, {"wall_ms": 15.0, "cpu_ms": 15.625}, {"wall_ms": 15.0, "cpu_ms": 15.625}]},
               "failures": {"rpi": ["Error: boom", "exit code 1"]}, "limitations": {}, "skipped": {}}
        text = durable_report.startup(raw, "/tmp", 1, [])
        self.assertNotIn("peak_rss_mib", text)
        self.assertIn("Peak memory is not reported: hyperfine 2.0 has no memory metric on Windows", text)
        self.assertIn("whole clock ticks of 15.625 ms", text)
        self.assertIn("`cpu_ms` pig-0.5 10.4, pi-1.1.0 312.", text)  # pooled means; 10.4 is the mean of 0, 15.625 and 15.625
        self.assertIn("**FAILURE** rpi in `startup` (2x): Error: boom", text)

    def test_a_windows_overhead_report_names_the_job_meter_and_the_cpu_tick(self):
        raw = {"kind": "overhead", "options": {"runs": 3, "warmup": 0, "rpc_turns": 2, "long_turns": 0, "scenarios": ["rpc"]},
               "env": {"arms": {}, "seed": 1, "arms_lock_sha256": "x", "cpu_tick_ms": 15.625}, "egress": {"mode": "none", "reason": "r", "by_arm": {}, "violations": []},
               "samples": {"rpc": {"pig-0.5": [{"turn_cpu_ms": 0.0, "turn_cpu_mean_ms": 3.0, "cpu_ms": 46.875, "ready_pss_mib": 60.0, "meter": "windows-job"},
                                               {"turn_cpu_ms": 0.0, "turn_cpu_mean_ms": 5.0, "cpu_ms": 46.875, "ready_pss_mib": 60.0, "meter": "windows-job"}]}},
               "failures": {}, "limitations": {}, "skipped": {}}
        text = report.overhead(raw, "/tmp", 1, [])
        self.assertIn("The meter is a Windows Job Object", text)
        self.assertIn("`ready_pss_mib` is the tree's private bytes", text)
        # A run's turn_cpu_ms is a median of 0-or-one-tick turns, so its mean says nothing; the mean over the turns does.
        self.assertIn("The pooled mean is finer (ms): `turn_cpu_mean_ms` pig-0.5 4.0; `cpu_ms` pig-0.5 46.9.", text)


# hyperfine 2.0.0's --export-json measurement record, as the pinned release writes it (captured from
# `hyperfine --shell=none --metrics time_wall_clock,time_cpu,memory_peak_resident --export-json ...` on Linux x64).
HF_RECORD = {"time_wall_clock": {"value": 0.022938662, "unit": "second"}, "time_cpu": {"value": 0.030348, "unit": "second"},
             "time_user": {"value": 0.003139, "unit": "second"}, "time_system": {"value": 0.027209, "unit": "second"},
             "memory_peak_resident": {"value": 27525120.0, "unit": "byte"}, "exit_code": 0}
FAKE_HF = """import json, os, sys
a = sys.argv[1:]
if a == ["--version"]:
    print(os.environ.get("FAKE_HF_VERSION", "hyperfine 2.0.0")); sys.exit(0)
metrics = a[a.index("--metrics") + 1]
with open(os.environ["FAKE_HF_LOG"], "a") as f:
    f.write(metrics + "\\n")
for name in ("memory_peak_resident", "instructions") if os.environ.get("FAKE_HF_WINDOWS") else ():
    if name in metrics.split(","):
        sys.stderr.write(f"Error: Metric '{name}' is unavailable on " + ("Windows" if name == "memory_peak_resident" else "this platform") + "\\n"); sys.exit(1)
if "instructions" in metrics and os.environ.get("FAKE_HF_NO_COUNTERS"):
    sys.stderr.write("Error: Metric 'instructions' is unavailable for 'x --version'. Check platform support and hardware-counter permissions.\\n"); sys.exit(1)
rec = json.loads(os.environ["FAKE_HF_RECORD"])
if "memory_peak_resident" not in metrics:
    del rec["memory_peak_resident"]  # hyperfine records only the metrics asked for
if "instructions" in metrics:
    rec["instructions"] = {"value": 1234.0, "unit": "count"}
runs = int(a[a.index("--runs") + 1])
with open(a[a.index("--export-json") + 1], "w") as f:
    json.dump({"results": [{"command": a[-1], "measurements": [rec] * runs, "summary": {}}]}, f)
"""


class StartupMeasureTest(unittest.TestCase):
    def setUp(self):
        import os
        import tempfile
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(__import__("shutil").rmtree, self.tmp, True)
        self.hf = fakes.python_command(os.path.join(self.tmp, "hyperfine"), FAKE_HF)
        self.log = os.path.join(self.tmp, "metrics.log")
        self.env = dict(os.environ, FAKE_HF_LOG=self.log, FAKE_HF_RECORD=__import__("json").dumps(HF_RECORD))

    def arm_once(self, result, env):
        import os
        from types import SimpleNamespace
        from pigbench import startup
        h = SimpleNamespace(env=env, cwd=self.tmp)
        return startup.measure_arm(self.hf, SimpleNamespace(id="a", binary="/bin/true"), h, 3, 0, result, os.path.join(self.tmp, "hf.json"))

    def test_hyperfine_2_measurements_become_ms_and_mib_samples(self):
        from unittest.mock import patch
        from pigbench import startup
        result = {"samples": {}, "failures": {}, "counters": None}
        with patch.object(startup.armlib, "host_platform", return_value=("linux", "amd64")):
            self.assertEqual(len(self.arm_once(result, self.env)), 3)
        self.assertEqual(result["counters"], True)
        s = result["samples"]["a"][0]
        self.assertAlmostEqual(s["wall_ms"], 22.938662)
        self.assertAlmostEqual(s["cpu_ms"], 30.348)
        self.assertAlmostEqual(s["peak_rss_mib"], 26.25)  # 27525120 bytes
        self.assertEqual(s["instructions"], 1234.0)

    def test_unavailable_counters_are_dropped_once_and_the_reason_kept(self):
        env = dict(self.env, FAKE_HF_NO_COUNTERS="1")
        result = {"samples": {}, "failures": {}, "counters": None}
        self.arm_once(result, env)
        self.arm_once(result, env)
        self.assertIs(result["counters"], False)
        self.assertIn("unavailable", result["counters_reason"])
        self.assertEqual(len(result["samples"]["a"]), 6)
        self.assertNotIn("instructions", result["samples"]["a"][0])
        self.assertEqual(result["failures"], {})
        with open(self.log) as f:
            asked = f.read().split()
        self.assertEqual(["instructions" in m for m in asked], [True, False, False])  # the second arm run does not ask again

    def test_the_command_hyperfine_gets_splits_back_into_the_arm_binary(self):
        # hyperfine 2.0.0 --shell=none splits its command with POSIX shell-word rules (checked against the pinned Windows release):
        # the unquoted C:\Users\... ran as C:Users... ("program not found"). shlex.split applies the same rules to what the fake received.
        import json
        import os
        import shlex
        from types import SimpleNamespace
        from pigbench import startup
        out = os.path.join(self.tmp, "hf.json")
        for binary in (r"C:\Users\u\AppData\Local\Temp\pigbench-1\arms\pig-0.4.1\pig.exe", r"C:\Program Files\pig\pig.exe", "/tmp/pigbench-1/arms/pig-0.5/pig"):
            rows, error = startup.measure(self.hf, SimpleNamespace(binary=binary), self.env, self.tmp, 1, 0, False, out)
            self.assertIsNone(error)
            with open(out) as f:
                self.assertEqual(shlex.split(json.load(f)["results"][0]["command"]), [binary, "--version"])

    def test_windows_asks_for_time_only(self):
        # hyperfine 2.0.0 on Windows exits 1 for "Metric 'memory_peak_resident' is unavailable on Windows" and for instructions.
        from unittest.mock import patch
        from pigbench import startup
        env = dict(self.env, FAKE_HF_WINDOWS="1")
        result = {"samples": {}, "failures": {}, "counters": None}
        with patch.object(startup.armlib, "host_platform", return_value=("windows", "amd64")):
            self.assertEqual(len(self.arm_once(result, env)), 3)
        self.assertEqual(result["failures"], {})
        self.assertIs(result["counters"], False)
        self.assertIn("'instructions' is unavailable", result["counters_reason"])
        self.assertEqual(sorted(result["samples"]["a"][0]), ["cpu_ms", "wall_ms"])
        with open(self.log) as f:
            self.assertEqual(f.read().split(), ["time_wall_clock,time_cpu,instructions", "time_wall_clock,time_cpu"])

    def test_a_failed_run_is_recorded_not_sampled(self):
        import json
        env = dict(self.env, FAKE_HF_RECORD=json.dumps(dict(HF_RECORD, exit_code=1)))
        result = {"samples": {}, "failures": {}, "counters": None}
        self.arm_once(result, env)
        self.assertEqual(result["failures"]["a"], ["exit code 1"] * 3)
        self.assertNotIn("a", result["samples"])

    def install(self, sha=None, size=None, version="hyperfine 2.0.0", archive_kind="tar.gz"):
        import io
        import os
        import tarfile
        import zipfile
        from types import SimpleNamespace
        from pigbench import arms, lock, startup
        payload = fakes.echo_payload(version, os.path.join(self.tmp, "go", version.replace(" ", "-")))
        name = f"hyperfine-v2.0.0-test.{archive_kind}"
        rel = os.path.join(self.tmp, "release")
        os.makedirs(rel, exist_ok=True)
        archive = os.path.join(rel, name)
        if archive_kind == "zip":  # the Windows release asset
            with zipfile.ZipFile(archive, "w") as z:
                z.writestr("hyperfine-v2.0.0-test/" + fakes.exe_member("hyperfine"), payload)
        else:
            with tarfile.open(archive, "w:gz") as tar:
                info = tarfile.TarInfo("hyperfine-v2.0.0-test/" + fakes.exe_member("hyperfine"))
                info.size = len(payload)
                tar.addfile(info, io.BytesIO(payload))
        key = "-".join(arms.host_platform())
        spec = {"version": "2.0.0", "assets": {key: {"name": name, "sha256": sha or lock.sha256_file(archive), "size": os.path.getsize(archive) if size is None else size}},
                "release": {"base": pathlib.Path(rel).as_uri() + "/", "member": "hyperfine"}}
        saved = startup.lock.read
        startup.lock.read = lambda: {"tools": {"hyperfine": spec}}
        try:
            ws = os.path.join(self.tmp, "ws")
            os.makedirs(ws, exist_ok=True)
            return startup.install_hyperfine(SimpleNamespace(root=ws))
        finally:
            startup.lock.read = saved

    def test_install_hyperfine_checks_the_pinned_archive_and_version(self):
        import os
        binary, tool = self.install()
        self.assertEqual(os.path.basename(binary), fakes.exe_member("hyperfine"))
        self.assertEqual(tool["version_line"], "hyperfine 2.0.0")
        with self.assertRaises(SystemExit):
            self.install(sha="0" * 64)
        with self.assertRaises(SystemExit):
            self.install(size=1)
        with self.assertRaises(SystemExit):
            self.install(version="hyperfine 1.19.0")

    def test_install_hyperfine_unpacks_a_zip_release(self):
        binary, tool = self.install(archive_kind="zip")
        self.assertEqual(tool["version_line"], "hyperfine 2.0.0")

    def test_batch_and_rpc_import_without_the_posix_terminal_modules(self):
        # Windows has no fcntl, pty or termios; only the PTY scenario needs them.
        import importlib
        import sys
        saved = {m: sys.modules.get(m) for m in ("fcntl", "pty", "termios", "pigbench.procio")}
        try:
            for m in ("fcntl", "pty", "termios"):
                sys.modules[m] = None  # import raises ImportError
            sys.modules.pop("pigbench.procio", None)
            procio = importlib.import_module("pigbench.procio")
            self.assertTrue(hasattr(procio, "Batch") and hasattr(procio, "Rpc"))
        finally:
            for m, mod in saved.items():
                if mod is None:
                    sys.modules.pop(m, None)
                else:
                    sys.modules[m] = mod




class SoakAACopyTest(unittest.TestCase):
    def test_each_aa_id_gets_one_copy_with_its_own_id_and_the_same_binary(self):
        from types import SimpleNamespace
        from pigbench import soak
        arms = [SimpleNamespace(id="pig-0.5", name="PiG", binary="/x/pig"), SimpleNamespace(id="pi-1.1.0", name="Pi", binary="/x/pi")]
        out = soak.with_aa_copies(arms, ["pig-0.5"])
        self.assertEqual([a.id for a in out], ["pig-0.5", "pi-1.1.0", "pig-0.5-b"])
        self.assertEqual(out[2].binary, "/x/pig")
        self.assertEqual(arms[0].id, "pig-0.5")  # the original is not renamed
        with self.assertRaises(SystemExit):
            soak.with_aa_copies(arms, ["codex"])


if __name__ == "__main__":
    unittest.main()

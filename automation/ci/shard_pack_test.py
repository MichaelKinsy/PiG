# SPDX-License-Identifier: MIT
"""shard-pack.py reproduces test-shard-pattern.sh's placement and recovers the cost model it packs with."""
import importlib.util
import json
import os
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("shard_pack", os.path.join(HERE, "shard-pack.py"))
shard_pack = importlib.util.module_from_spec(spec)
spec.loader.exec_module(shard_pack)


class ShardPackTest(unittest.TestCase):
    def test_hash_is_fnv1a32(self):
        self.assertEqual(shard_pack.fnv1a32(""), 2166136261)
        self.assertEqual(shard_pack.fnv1a32("a"), 0xE40C292C)

    def test_fit_recovers_the_model_that_made_the_walls(self):
        shards = [(s, p, 20 + 0.9 * s + 0.3 * p) for s, p in [(100, 50), (80, 120), (150, 10), (60, 200), (120, 90), (40, 40)]]
        overhead, a, b, err, mean = shard_pack.fit(shards)
        self.assertAlmostEqual(overhead, 20, places=4)
        self.assertAlmostEqual(a, 0.9, places=6)
        self.assertAlmostEqual(b, 0.3, places=6)
        self.assertLess(err, 1e-9)
        self.assertLess(mean, 1e-9)

    def test_pack_balances_the_pinned_tests_over_the_hash_placed_rest(self):
        tests = {"TestSlow%d" % i: {"elapsed": 60.0 - i, "subtests": 0.0, "parallel": False} for i in range(8)}
        tests.update({"TestFast%d" % i: {"elapsed": 0.1, "subtests": 0.0, "parallel": False} for i in range(40)})
        pins, load = shard_pack.pack(tests, 4, (10.0, 1.0, 0.3), 5.0, 0.0, 2)
        self.assertEqual(sorted(pins), sorted(n for n in tests if n.startswith("TestSlow")))
        self.assertLess(max(load) - min(load), 15)

    def test_cold_reserve_lightens_the_first_shards_of_each_job(self):
        tests = {"TestSlow%d" % i: {"elapsed": 50.0, "subtests": 0.0, "parallel": False} for i in range(20)}
        pins, load = shard_pack.pack(tests, 8, (0.0, 1.0, 0.3), 5.0, 50.0, 2)
        per_shard = [sum(1 for s in pins.values() if s == i) for i in range(1, 9)]
        self.assertEqual(per_shard, [2, 2, 3, 3, 2, 2, 3, 3])
        self.assertEqual(load, [150.0] * 8)

    def test_compact_timing_table_stands_for_the_run_it_was_written_from(self):
        with tempfile.TemporaryDirectory() as tmp:
            for shard in range(1, 5):
                events = [{"Action": "pause", "Package": "x/pkg", "Test": "TestP%d" % shard}]
                events.append({"Action": "pass", "Package": "x/pkg", "Test": "TestP%d" % shard, "Elapsed": 3.0 * shard})
                events.append({"Action": "pass", "Package": "x/pkg", "Test": "TestS%d" % shard, "Elapsed": 7.5 + shard})
                events.append({"Action": "pass", "Package": "x/pkg", "Test": "TestS%d/sub" % shard, "Elapsed": 99})
                events.append({"Action": "pass", "Package": "x/pkg", "Elapsed": 30.0 + 5 * shard})
                with open(os.path.join(tmp, "shard-%d.json" % shard), "w") as handle:
                    handle.write("\n".join(json.dumps(e) for e in events))
            run = shard_pack.read_timings([tmp], "./pkg")
            table = os.path.join(tmp, "table", "timings.json")
            os.makedirs(os.path.dirname(table))
            shard_pack.write_compact(table, "./pkg", run[0], run[2], run[3])
            again = shard_pack.read_timings([table], "./pkg")
        self.assertEqual(again[0], run[0])
        self.assertEqual(sorted(again[1]), sorted(run[1]))
        self.assertEqual(again[2], run[2])
        self.assertEqual(again[3], run[3])
        self.assertEqual(run[2]["TestS3"], 3)

    def test_command_prints_the_section_test_shard_pattern_reads(self):
        with tempfile.TemporaryDirectory() as tmp:
            for shard in range(1, 5):
                events = []
                for name, elapsed in (("TestA%d" % shard, 30.0 + shard), ("TestB%d" % shard, 0.2)):
                    events.append({"Action": "pass", "Package": "x/pkg", "Test": name, "Elapsed": elapsed})
                events.append({"Action": "pause", "Package": "x/pkg", "Test": "TestP%d" % shard})
                events.append({"Action": "pass", "Package": "x/pkg", "Test": "TestP%d" % shard, "Elapsed": 3.0 * shard * shard})
                events.append({"Action": "pass", "Package": "x/pkg", "Elapsed": 40.0 + 8 * shard * shard})
                with open(os.path.join(tmp, "shard-%d.json" % shard), "w") as handle:
                    handle.write("\n".join(json.dumps(e) for e in events))
            out = subprocess.run(["python3", os.path.join(HERE, "shard-pack.py"), "--package", "./pkg", "--shards", "2", tmp], capture_output=True, text=True).stdout
        lines = out.splitlines()
        self.assertEqual(lines[:2], ["package ./pkg", "shards 2"])
        self.assertEqual(sorted(l.split()[1] for l in lines[2:]), ["TestA1", "TestA2", "TestA3", "TestA4", "TestP2", "TestP3", "TestP4"])

    def test_reads_timestamped_go_test_v_logs_with_timeout_and_fallback(self):
        with tempfile.TemporaryDirectory() as tmp:
            fallback = os.path.join(tmp, "linux.json")
            with open(fallback, "w", encoding="utf-8") as handle:
                json.dump(
                    {
                        "package": "./pkg",
                        "shards": {"1": 100.0},
                        "tests": {
                            "TestSer1": [1, 20.0, 0.0, 0],
                            "TestPar1": [1, 10.0, 10.0, 1],
                            "TestSer2": [1, 15.0, 0.0, 0],
                            "TestPar2": [1, 20.0, 20.0, 1],
                            "TestUnfinished": [1, 30.0, 30.0, 1],
                        },
                    },
                    handle,
                )
            # Two finished shards (one with a serial parent whose parallel subtests hide behind 0.00s) and one timed-out shard.
            log1 = "\n".join(
                [
                    "2026-11-21T06:00:00.0000000Z \x1b[36;1mgo test -v -timeout 30m -run $env:SHARD_RUN ./pkg\x1b[0m",
                    "2026-11-21T06:00:00.1000000Z   SHARD_RUN: ^(TestSer1|TestPar1|TestSerSub)$",
                    "2026-11-21T06:01:00.0000000Z === RUN   TestSer1",
                    "2026-11-21T06:01:40.0000000Z --- PASS: TestSer1 (40.00s)",
                    "2026-11-21T06:01:40.0000000Z === RUN   TestSerSub",
                    "2026-11-21T06:01:40.0000000Z === RUN   TestSerSub/a",
                    "2026-11-21T06:02:10.0000000Z --- PASS: TestSerSub (0.00s)",
                    "2026-11-21T06:02:10.0000000Z     --- PASS: TestSerSub/a (30.00s)",
                    "2026-11-21T06:02:10.0000000Z === RUN   TestPar1",
                    "2026-11-21T06:02:10.0000000Z === PAUSE TestPar1",
                    "2026-11-21T06:02:10.0000000Z === CONT  TestPar1",
                    "2026-11-21T06:02:30.0000000Z --- PASS: TestPar1 (40.00s)",
                    "2026-11-21T06:02:30.0000000Z ok  \texample.com/pkg\t150.000s",
                ]
            )
            log2 = "\n".join(
                [
                    "2026-11-21T06:00:00.0000000Z go test -v -timeout 30m -run $env:SHARD_RUN ./pkg",
                    "2026-11-21T06:00:00.1000000Z   SHARD_RUN: ^(TestSer2|TestPar2)$",
                    "2026-11-21T06:01:00.0000000Z === RUN   TestSer2",
                    "2026-11-21T06:01:30.0000000Z --- PASS: TestSer2 (30.00s)",
                    "2026-11-21T06:01:30.0000000Z === RUN   TestPar2",
                    "2026-11-21T06:01:30.0000000Z === PAUSE TestPar2",
                    "2026-11-21T06:01:30.0000000Z === CONT  TestPar2",
                    "2026-11-21T06:02:10.0000000Z --- PASS: TestPar2 (80.00s)",
                    "2026-11-21T06:02:10.0000000Z ok  \texample.com/pkg\t130.000s",
                ]
            )
            log3 = "\n".join(
                [
                    "2026-11-21T06:00:00.0000000Z go test -v -timeout 30m -run $env:SHARD_RUN ./pkg",
                    "2026-11-21T06:00:00.1000000Z   SHARD_RUN: ^(TestUnfinished|TestUnknownRunning)$",
                    "2026-11-21T06:01:00.0000000Z === RUN   TestUnfinished",
                    "2026-11-21T06:01:00.0000000Z === PAUSE TestUnfinished",
                    "2026-11-21T06:01:00.0000000Z panic: test timed out after 29m0s",
                    "2026-11-21T06:01:00.0000000Z \trunning tests:",
                    "2026-11-21T06:01:00.0000000Z \t\tTestUnknownRunning (1m40s)",
                    "2026-11-21T06:01:00.0000000Z FAIL\texample.com/pkg\t1800.100s",
                ]
            )
            logdir = os.path.join(tmp, "logs")
            os.makedirs(logdir)
            for idx, body in enumerate((log1, log2, log3), 1):
                with open(os.path.join(logdir, "conformance-%d.log" % idx), "w", encoding="utf-8") as handle:
                    handle.write(body + "\n")
            tests, shard_walls, shard_of, walls, warms, estimated = shard_pack.read_timings([logdir], "./pkg", fallback)
            self.assertEqual(estimated, 2)
            # Timed-out shard 3 must not enter the wall model fit.
            self.assertEqual(sorted(walls), [1, 2])
            self.assertEqual(len(shard_walls), 2)
            # Serial parent with parallel subtests uses wall time (30s), not reported 0.00s.
            self.assertAlmostEqual(tests["TestSerSub"]["elapsed"], 30.0, places=1)
            # Fallback scaling: parallel ratio is 4.0 (TestPar1 40/10, TestPar2 80/20), so TestUnfinished (30s) -> 120s.
            self.assertTrue(tests["TestUnfinished"]["parallel"])
            self.assertAlmostEqual(tests["TestUnfinished"]["elapsed"], 120.0, places=1)
            # Unknown running test (100s) gets at least its running time with headroom.
            self.assertGreaterEqual(tests["TestUnknownRunning"]["elapsed"], 135.0)
            # Round-trip through compact table preserves overhead and model.
            compact = os.path.join(tmp, "compact.json")
            ov = round(sum(warms) / len(warms), 2)
            shard_pack.write_compact(compact, "./pkg", tests, shard_of, walls, overhead=ov)
            again = shard_pack.read_timings([compact], "./pkg")
            self.assertEqual(again[0], tests)
            self.assertEqual(again[3], walls)
            self.assertEqual(again[4], [ov])
            res = subprocess.run(
                ["python3", os.path.join(HERE, "shard-pack.py"), "--package", "./pkg", "--shards", "2", "--limit", "1800", "--dry-run", compact],
                capture_output=True,
                text=True,
                check=True,
            )
            self.assertIn("shard  1:", res.stdout)
            self.assertIn("shard  2:", res.stdout)


if __name__ == "__main__":
    unittest.main()

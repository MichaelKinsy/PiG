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


if __name__ == "__main__":
    unittest.main()

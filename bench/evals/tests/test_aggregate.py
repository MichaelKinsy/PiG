# SPDX-License-Identifier: MIT
import json
import os
import tempfile
import unittest

from pigbench import aggregate


def run_file(tmp, name, os_name, machine, pig, pi, lock="L1", commit="c1", kind="overhead"):
    """A raw.json with 5 samples per arm whose medians are pig and pi, so every ratio is known by construction."""
    samples = {"print": {"pig-0.5": [{"wall_ms": pig + d, "peak_rss_mib": 10.0} for d in (-2, -1, 0, 1, 2)], "pi-1.1.0": [{"wall_ms": pi + d, "peak_rss_mib": 100.0} for d in (-2, -1, 0, 1, 2)]}}
    raw = {"kind": kind, "env": {"os": os_name, "machine": machine, "cpu": name, "logical_cpus": 8, "memory_gib": 16, "commit": commit, "arms_lock_sha256": lock, "meter": "test", "arms": {}, "seed": 1},
           "options": {"scenarios": ["print"]}, "samples": samples if kind == "overhead" else samples["print"], "egress": {"mode": "x", "violations": []}}
    path = os.path.join(tmp, name)
    os.makedirs(path)
    with open(os.path.join(path, "raw.json"), "w") as f:
        json.dump(raw, f)
    return path


class AggregateTest(unittest.TestCase):
    def test_arch_keys(self):
        self.assertEqual(aggregate.arch_key({"os": "Linux 6.8.0", "machine": "x86_64"}), "linux-x64")
        self.assertEqual(aggregate.arch_key({"os": "Linux 6.8.0", "machine": "aarch64"}), "linux-arm64")
        self.assertEqual(aggregate.arch_key({"os": "Darwin 25.0", "machine": "arm64"}), "darwin-arm64")
        self.assertEqual(aggregate.arch_key({"os": "Windows 11", "machine": "AMD64"}), "windows-x64")

    def test_four_architectures_one_report_with_known_ratios(self):
        with tempfile.TemporaryDirectory() as tmp:
            paths = [run_file(tmp, "a", "Linux 6", "x86_64", 50, 100), run_file(tmp, "b", "Linux 6", "aarch64", 30, 120),
                     run_file(tmp, "c", "Darwin 25", "arm64", 20, 40), run_file(tmp, "d", "Windows 11", "AMD64", 90, 90)]
            out = os.path.join(tmp, "out")
            text = open(aggregate.aggregate(paths, out)).read()
            csv_text = open(os.path.join(out, "summary.csv")).read()
        for arch in ("linux-x64", "linux-arm64", "darwin-arm64", "windows-x64"):
            self.assertIn(f"## {arch}", text)
        summary = text.split("## Summary")[1].split("## Absolute")[0]
        self.assertIn("0.50 [", summary)   # 50 / 100 on linux-x64
        self.assertIn("0.25 [", summary)   # 30 / 120 on linux-arm64
        self.assertIn("0.50 [", summary)   # 20 / 40 on darwin-arm64
        self.assertIn("1.00 [", summary)   # 90 / 90 on windows-x64: a tie is listed too
        self.assertIn("0.10 [", summary)   # peak RSS 10 / 100 on every machine
        self.assertIn("aggregate,print/windows-x64,pig-0.5,wall_ms_ratio_vs_pi-1.1.0", csv_text)

    def test_mixed_locks_or_commits_are_refused_unless_allowed(self):
        with tempfile.TemporaryDirectory() as tmp:
            paths = [run_file(tmp, "a", "Linux 6", "x86_64", 50, 100), run_file(tmp, "b", "Darwin 25", "arm64", 20, 40, lock="L2", commit="c2")]
            with self.assertRaises(SystemExit) as caught:
                aggregate.aggregate(paths, os.path.join(tmp, "o1"))
            self.assertIn("arms.lock differs", str(caught.exception))
            text = open(aggregate.aggregate(paths, os.path.join(tmp, "o2"), allow_mixed=True)).read()
            self.assertIn("Mixed inputs (allowed explicitly)", text)

    def test_an_architecture_without_a_scenario_is_listed_as_missing(self):
        with tempfile.TemporaryDirectory() as tmp:
            a = run_file(tmp, "a", "Linux 6", "x86_64", 50, 100)
            b = run_file(tmp, "b", "Windows 11", "AMD64", 90, 90, kind="startup")  # a startup run holds the scenario `version`, not `print`
            text = open(aggregate.aggregate([a, b], os.path.join(tmp, "o"))).read()
        self.assertIn("windows-x64: no `print` results", text)
        self.assertIn("linux-x64: no `version (hyperfine)` results", text)

    def test_absolute_numbers_are_shown_but_not_ranked(self):
        with tempfile.TemporaryDirectory() as tmp:
            paths = [run_file(tmp, "a", "Linux 6", "x86_64", 50, 100), run_file(tmp, "b", "Darwin 25", "arm64", 20, 40)]
            text = open(aggregate.aggregate(paths, os.path.join(tmp, "o"))).read()
        absolute = text.split("## Absolute medians")[1].split("\n## ")[0]
        self.assertIn("| print | wall_ms | pig-0.5 | 20 | 50 |", absolute)  # columns sorted by architecture: darwin-arm64, linux-x64
        self.assertIn("never compared across architectures", text)

    def test_differing_commits_alone_are_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            paths = [run_file(tmp, "a", "Linux 6", "x86_64", 50, 100), run_file(tmp, "b", "Darwin 25", "arm64", 20, 40, commit="c2")]
            with self.assertRaises(SystemExit) as caught:
                aggregate.aggregate(paths, os.path.join(tmp, "o"))
        self.assertIn("commits differ", str(caught.exception))

    def test_two_machines_of_one_architecture_are_never_paired(self):
        # PiG measured on one linux-x64 machine and Pi on another: no ratio may be formed, and both machines are listed.
        with tempfile.TemporaryDirectory() as tmp:
            a = run_file(tmp, "fast", "Linux 6", "x86_64", 50, 100)
            b = run_file(tmp, "slow", "Linux 6", "x86_64", 80, 160)
            for path, drop in ((a, "pi-1.1.0"), (b, "pig-0.5")):
                with open(os.path.join(path, "raw.json")) as f:
                    raw = json.load(f)
                del raw["samples"]["print"][drop]
                with open(os.path.join(path, "raw.json"), "w") as f:
                    json.dump(raw, f)
            with open(aggregate.aggregate([a, b], os.path.join(tmp, "o"))) as f:
                text = f.read()
        summary = text.split("## Summary")[1].split("## Absolute")[0]
        self.assertNotIn("0.50 [", summary)  # 50 on one machine over 160 on the other is 0.31; 50/100 would be a same-machine ratio that no run measured
        self.assertNotIn("0.31 [", summary)
        self.assertIn("| linux-x64 | Linux 6 | fast |", text)
        self.assertIn("| linux-x64 #2 | Linux 6 | slow |", text)

    def test_hyperfine_and_overhead_version_samples_stay_apart(self):
        with tempfile.TemporaryDirectory() as tmp:
            over = run_file(tmp, "o", "Linux 6", "x86_64", 50, 100)
            start = run_file(os.path.join(tmp, "startup"), "o", "Linux 6", "x86_64", 10, 20, kind="startup")  # same machine (cpu "o")
            for path in (over,):
                with open(os.path.join(path, "raw.json")) as f:
                    raw = json.load(f)
                raw["samples"] = {"version": raw["samples"]["print"]}
                with open(os.path.join(path, "raw.json"), "w") as f:
                    json.dump(raw, f)
            merged, _ = aggregate.merge(aggregate.load([over, start]))
        self.assertEqual(sorted(merged["linux-x64"]), ["version", "version (hyperfine)"])
        self.assertEqual(len(merged["linux-x64"]["version"]["pig-0.5"]), 5)

    def test_other_run_kinds_are_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = run_file(tmp, "d", "Linux 6", "x86_64", 50, 100, kind="durable")
            with self.assertRaises(SystemExit) as caught:
                aggregate.aggregate([path], os.path.join(tmp, "o"))
        self.assertIn("'durable' run", str(caught.exception))

    def test_ratio_rows_leave_the_percentile_columns_empty(self):
        rows = []
        merged = {"linux-x64": {"print": {"pig-0.5": [{"wall_ms": 50.0 + d} for d in range(5)], "pi-1.1.0": [{"wall_ms": 100.0 + d} for d in range(5)]}}}
        aggregate.cross_table(merged, {"linux-x64": {}}, 1, rows)
        self.assertEqual(rows[0][6:9], [None, None, None])  # p90, min, max
        self.assertLess(rows[0][9], rows[0][5])
        self.assertGreater(rows[0][10], rows[0][5])

    def test_a_windows_machine_ranks_no_cpu_median_of_a_few_ticks_and_says_what_its_cells_mean(self):
        # Windows charges CPU time in 15.625 ms ticks: a turn's median CPU is 0 or one tick, so its ratio (0 / 15.625 = 0.00) ranks the tick.
        def rpc(cpu, mean):
            return [{"turn_cpu_ms": cpu, "turn_cpu_mean_ms": mean + d, "ready_pss_mib": 60.0} for d in (-0.2, -0.1, 0, 0.1, 0.2)]
        with tempfile.TemporaryDirectory() as tmp:
            paths = []
            for name, os_name, extra, pig, pi in (("w", "Windows 10", {"meter": "windows-job", "cpu_tick_ms": 15.625}, rpc(0.0, 4.0), rpc(15.625, 8.0)),
                                                  ("l", "Linux 6", {}, rpc(3.0, 3.0), rpc(6.0, 6.0))):
                path = run_file(tmp, name, os_name, "x86_64", 50, 100)
                with open(os.path.join(path, "raw.json")) as f:
                    raw = json.load(f)
                raw["env"].update(extra)
                raw["samples"]["rpc"] = {"pig-0.5": pig, "pi-1.1.0": pi}
                with open(os.path.join(path, "raw.json"), "w") as f:
                    json.dump(raw, f)
                paths.append(path)
            with open(aggregate.aggregate(paths, os.path.join(tmp, "o"))) as f:
                text = f.read()
        summary = text.split("## Summary")[1].split("## Absolute")[0]
        self.assertIn("| rpc | turn_cpu_ms | 0.50 [0.50, 0.50] | n/a (CPU in 15.625 ms ticks) |", summary)  # linux-x64, then windows-x64
        self.assertIn("| rpc | turn_cpu_mean_ms | 0.50 [", summary)  # 3 / 6 on Linux, 4 / 8 on Windows
        self.assertIn("**windows-x64**: The meter is a Windows Job Object", text)
        self.assertIn("`ready_pss_mib` is the tree's private bytes", text)
        self.assertNotIn("**linux-x64**:", text)
        windows = text.split("## windows-x64")[1]
        self.assertIn("clock ticks of 15.625 ms", windows)
        self.assertNotIn("| turn_cpu_ms |", windows.split("Ratio of medians")[1])


if __name__ == "__main__":
    unittest.main()

# SPDX-License-Identifier: MIT
"""The bench/k8s adapter of the long-history bench: sizes, the generated session and the result line."""
import contextlib
import hashlib
import io
import json
import os
import shutil
import tempfile
import unittest
from unittest import mock

from pigbench import egress, k8s, longhistory

HERE = os.path.dirname(os.path.abspath(__file__))


def profile():
    with open(os.path.join(HERE, "..", "..", "k8s", "cmd", "k8sbench", "profiles", "longhistory.json")) as f:
        return json.load(f)


def sha256(path):
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


class K8sAdapterTests(unittest.TestCase):
    def test_sizes_are_the_two_cases(self):
        self.assertEqual(k8s.case_of(1_000_000), "1m")
        self.assertEqual(k8s.case_of(3_000_000), "3m")
        self.assertEqual(set(k8s.SIZES.values()), set(longhistory.CASES))
        with self.assertRaises(SystemExit):
            k8s.case_of(2_000_000)

    def test_generate_writes_the_meta_k8sbench_reads(self):
        with tempfile.TemporaryDirectory() as d:
            meta = k8s.generate(1_000_000, 7, d)
            with open(os.path.join(d, "k8s-1000000.json")) as f:
                self.assertEqual(json.load(f), meta)
            self.assertEqual(sha256(meta["session"]), meta["sha256"])
            self.assertEqual(k8s.session_fingerprint(meta["session"]), meta["fingerprint"])
            self.assertEqual(meta["bytes"], os.path.getsize(meta["session"]))
            self.assertGreater(meta["seedMs"], 0)
            # The same seed gives the same file: the profile pins its fingerprint.
            with tempfile.TemporaryDirectory() as d2:
                self.assertEqual(k8s.generate(1_000_000, 7, d2)["fingerprint"], meta["fingerprint"])

    def test_the_line_carries_every_turn_and_only_numbers(self):
        rpc = {"open_rpc_ms": 400.0, "open_rpc_cpu_ms": 500.0, "append_turn_walls_ms": [130.0, 140.0, 120.0], "append_turn_cpus_ms": [160.0, 150.0, 155.0],
               "open_rpc_peak_rss_mib": 70.5, "open_rpc_steady_rss_mib": 70.0, "resume_rpc_ms": 540.0, "append_turn_wall_ms": 130.0}
        tui = {"first_output_ms": 430.0, "resize_w100_ms": 350.0, "resize_h30_ms": None, "open_bytes": True, "alt_screen": True, "resize_w100_clears_scrollback": False}
        line = k8s.sample_line("pig", 1_000_000, 2, "a3d78d643f9b7ebd", {"rpc": rpc, "tui": tui})
        self.assertEqual((line["t"], line["target"], line["turns"], line["sample"], line["fingerprint"]), ("sample", "pig", 1_000_000, 2, "a3d78d643f9b7ebd"))
        self.assertEqual(line["open"], 400.0)
        self.assertEqual(line["turn"], [130.0, 140.0, 120.0])
        self.assertEqual(line["turnCpu"], [160.0, 150.0, 155.0])
        self.assertEqual((line["openCpu"], line["peakRss"], line["steadyRss"], line["resumeRpcMs"]), (500.0, 70.5, 70.0, 540.0))
        self.assertEqual((line["tuiFirstOutputMs"], line["resizeWidthMs"]), (430.0, 350.0))
        # A phase that did not measure a value (None) or a flag (bool) leaves it out.
        self.assertNotIn("resizeHeightMs", line)
        self.assertNotIn("tuiOpenBytes", line)  # a bool is an int in Python; a mapped field holding one is not a number
        self.assertFalse(any(isinstance(v, bool) for v in line.values()))

    def test_every_mapped_field_is_one_longhistory_measures(self):
        # Field names longhistory writes, including the resize labels it builds with an f-string.
        import inspect
        source = inspect.getsource(longhistory)
        for phase, fields in k8s.FIELDS.items():
            for src in fields:
                if src.startswith("resize_"):
                    label, _, rest = src[len("resize_"):].partition("_")
                    self.assertIn(f'("{label}",', source, src)
                    self.assertIn('s[f"resize_{label}_' + rest + '"]', source, src)
                else:
                    self.assertIn(f's["{src}"]', source.replace("'", '"'), src)

    def test_the_rpc_fields_the_line_reads_are_ones_longhistory_writes(self):
        import inspect
        source = inspect.getsource(longhistory.one_rpc)
        for src in ("open_rpc_ms", "open_rpc_cpu_ms", "open_rpc_peak_rss_mib"):
            self.assertIn(f's["{src}"]', source, src)
        self.assertIn('s["append_turn_walls_ms"], s["append_turn_cpus_ms"] = walls, cpus', source)

    def test_the_profile_declares_every_metric_it_names(self):
        prof = profile()
        produced = {dst for fields in k8s.FIELDS.values() for dst in fields.values()}
        for m in prof["metrics"]:
            self.assertIn(m["name"], produced, m["name"])
        self.assertEqual(set(prof["fingerprints"]), {str(n) for n in k8s.SIZES})

    def test_the_pinned_fingerprints_do_not_depend_on_where_the_session_is_written(self):
        # A Job sets TMPDIR=/data/tmp, so the project directory (the session header's cwd) is not the one a host run uses.
        # Every target's seed must still reproduce the profile's pins, or the analysis refuses the session.
        pins = profile()["fingerprints"]
        for tokens in sorted(k8s.SIZES):
            with tempfile.TemporaryDirectory() as d:
                with mock.patch.object(k8s, "PROJECT", os.path.join(d, "data", "tmp", "pigbench-history-project")):
                    meta = k8s.generate(tokens, 20261009, os.path.join(d, "fixtures"))
                with open(meta["session"]) as f:
                    self.assertEqual(json.loads(f.readline())["cwd"], os.path.join(d, "data", "tmp", "pigbench-history-project"))
                self.assertEqual(meta["fingerprint"], pins[str(tokens)], tokens)
                self.assertEqual(meta["sha256"], sha256(meta["session"]))


class K8sSampleTests(unittest.TestCase):
    """sample() with stand-in phases: what it gives them, what it refuses and what it prints."""

    TOKENS = 1000

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.tmp, True)
        patches = [mock.patch.dict(longhistory.CASES, {"tiny": {"segments": 1, "tokens_per_segment": 2000, "compactions": 0, "tail_tokens": 0}}),
                   mock.patch.dict(k8s.SIZES, {self.TOKENS: "tiny"}),
                   mock.patch.object(k8s, "PROJECT", os.path.join(self.tmp, "project")),
                   mock.patch.dict(longhistory.PHASES, {"rpc": self.rpc, "tui": self.tui, "resume-tui": self.resume})]
        for p in patches:
            p.start()
            self.addCleanup(p.stop)
        self.dir = os.path.join(self.tmp, "fixtures")
        self.meta = k8s.generate(self.TOKENS, 3, self.dir)
        self.calls = []
        self.tui_result = {"first_output_ms": 10.0}
        self.leak = None
        self.loud = False

    def seen(self, phase, path, mode):
        self.calls.append((phase, path, sha256(path), mode))

    def rpc(self, ctx, arm, case, path, facts):
        self.seen("rpc", path, None)
        with open(path, "a") as f:
            f.write("appended\n")
        return {"open_rpc_ms": 1.0, "open_rpc_cpu_ms": 2.0, "append_turn_walls_ms": [3.0, 4.0], "append_turn_cpus_ms": [5.0, 6.0], "resume_rpc_ms": 7.0}

    def tui(self, ctx, arm, case, path, facts, mode):
        self.seen("tui", path, mode)
        if self.loud:
            print("the phase's own progress")
        if self.leak:
            egress.OBSERVED.add(self.leak)
        return self.tui_result

    def resume(self, ctx, arm, case, path, facts, mode):
        self.seen("resume-tui", path, mode)
        return {"resume_tui_ms": 8.0}

    def sample(self, phases=("rpc", "tui", "resume-tui")):
        return k8s.sample("pig", "pig", "/bin/true", self.TOKENS, 3, self.dir, 2, list(phases), "default", 30)

    def test_every_phase_measures_an_unchanged_copy(self):
        line = self.sample()
        self.assertEqual([c[0] for c in self.calls], ["rpc", "tui", "resume-tui"])
        for phase, path, digest, mode in self.calls:
            self.assertNotEqual(path, self.meta["session"], phase)
            self.assertEqual(digest, self.meta["sha256"], phase)  # the rpc phase's append did not reach the next phase's copy
            self.assertEqual(mode, None if phase == "rpc" else "default", phase)
        self.assertEqual(sha256(self.meta["session"]), self.meta["sha256"])
        self.assertEqual((line["open"], line["turn"], line["turnCpu"], line["resumeRpcMs"], line["tuiFirstOutputMs"], line["resumeTuiMs"], line["fingerprint"]),
                         (1.0, [3.0, 4.0], [5.0, 6.0], 7.0, 10.0, 8.0, self.meta["fingerprint"]))

    def test_a_session_changed_since_it_was_generated_is_refused(self):
        with open(self.meta["session"], "a") as f:
            f.write("\n")
        with self.assertRaises(SystemExit) as e:
            self.sample()
        self.assertIn("changed since it was generated", str(e.exception))
        self.assertEqual(self.calls, [])

    def test_a_connection_outside_the_mock_fails_the_sample(self):
        self.leak = "192.0.2.1:9"
        with self.assertRaises(SystemExit) as e:
            self.sample()
        self.assertIn("tui: connections outside the mock: 192.0.2.1:9", str(e.exception))
        self.assertEqual(egress.take(), set())

    def test_a_failed_phase_fails_the_sample(self):
        self.tui_result = {"failure": "no output"}
        with self.assertRaises(SystemExit) as e:
            self.sample()
        self.assertIn("tui: no output", str(e.exception))
        self.assertEqual([c[0] for c in self.calls], ["rpc", "tui"])

    def test_the_rpc_phase_cannot_be_left_out(self):
        with self.assertRaises(SystemExit):
            self.sample(("tui",))
        self.assertEqual(self.calls, [])

    def test_stdout_carries_only_the_result_line(self):
        self.loud = True
        args = mock.Mock(k8s_cmd="sample", target="pig", family="pig", binary="/bin/true", tokens=self.TOKENS, seed=3, dir=self.dir, sample=1,
                                    phases=["rpc", "tui"], mode="default", timeout=30)
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            self.assertEqual(k8s.main(args), 0)
        lines = out.getvalue().splitlines()
        self.assertEqual(len(lines), 1, lines)
        self.assertEqual(json.loads(lines[0])["sample"], args.sample)
        self.assertIn("the phase's own progress", err.getvalue())


if __name__ == "__main__":
    unittest.main()


def overhead_profile(name):
    with open(os.path.join(HERE, "..", "..", "k8s", "cmd", "k8sbench", "profiles", name + ".json")) as f:
        return json.load(f)


def arg_after(argv, flag):
    return argv[argv.index(flag) + 1]


OVERHEAD_PROFILES = ("overhead-pi", "overhead-codex")


class K8sOverheadTests(unittest.TestCase):
    """The overhead bench as k8sbench seed and sample commands."""

    def test_scenarios_run_in_one_order_and_print_tools_always(self):
        self.assertEqual(k8s.overhead_scenarios(["pty", "print-tools", "version"]), ["version", "print-tools", "pty"])
        for bad in (["version", "print"], ["print-tools", "long-session"]):
            with self.assertRaises(SystemExit):
                k8s.overhead_scenarios(bad)

    def test_the_fingerprint_names_the_workload(self):
        a = k8s.overhead_fingerprint(8, ["version", "print-tools"])
        self.assertEqual(a, k8s.overhead_fingerprint(8, ["print-tools", "version"]))
        self.assertNotEqual(a, k8s.overhead_fingerprint(9, ["version", "print-tools"]))
        self.assertNotEqual(a, k8s.overhead_fingerprint(8, ["version", "print", "print-tools"]))
        with mock.patch("pigbench.overhead.PROMPT", "another prompt"):
            self.assertNotEqual(a, k8s.overhead_fingerprint(8, ["version", "print-tools"]))
        with tempfile.TemporaryDirectory() as d:
            meta = k8s.overhead_seed(8, ["version", "print-tools"], d)
            with open(os.path.join(d, "overhead-8.json")) as f:
                self.assertEqual(json.load(f), meta)
            self.assertEqual(meta["fingerprint"], a)
            with self.assertRaises(SystemExit):
                k8s.overhead_seed(0, ["print-tools"], d)

    def test_the_line_is_print_tools_and_carries_only_numbers(self):
        tools = {"first_request_ms": 60.0, "first_request_cpu_ms": 90.0, "peak_rss_mib": 52.5, "wall_ms": 500.0, "turn_gap_ms": 7.0, "meter": "cgroup",
                 "_series": {"gap_ms": [7.0, 8.0, 6.0], "cpu_turn_ms": [5.0, 6.0, 4.0]}}
        results = {"version": {"wall_ms": 20.0, "cpu_ms": 15.0, "peak_rss_mib": 6.0, "meter": "cgroup"}, "print-tools": tools,
                   "rpc": {"ready_ms": 140.0, "turn_wall_ms": 100.0, "teardown_ms": None}, "pty": {"first_frame_ms": 150.0, "input_to_request_ms": True}}
        line = k8s.overhead_line("pig", 3, 4, "13a870eb85bf000f", results)
        self.assertEqual((line["t"], line["target"], line["turns"], line["sample"], line["fingerprint"]), ("sample", "pig", 3, 4, "13a870eb85bf000f"))
        self.assertEqual((line["open"], line["turn"], line["openCpu"], line["turnCpu"], line["peakRss"]), (60.0, [7.0, 8.0, 6.0], 90.0, [5.0, 6.0, 4.0], 52.5))
        self.assertEqual((line["versionMs"], line["versionCpuMs"], line["toolsMs"], line["toolsGapMs"], line["rpcReadyMs"], line["rpcTurnMs"], line["ptyFirstFrameMs"]),
                         (20.0, 15.0, 500.0, 7.0, 140.0, 100.0, 150.0))
        self.assertNotIn("rpcTeardownMs", line)
        self.assertNotIn("ptyInputMs", line)
        self.assertFalse(any(isinstance(v, (bool, str)) for k, v in line.items() if k not in ("t", "target", "fingerprint")))
        # A turn more or less than the rounds, or no CPU reading at the first request, is a broken measurement.
        with self.assertRaises(SystemExit):
            k8s.overhead_line("pig", 4, 0, "x", results)
        del tools["first_request_cpu_ms"]
        with self.assertRaises(SystemExit):
            k8s.overhead_line("pig", 3, 0, "x", results)

    def test_every_mapped_field_is_one_the_overhead_bench_measures(self):
        import inspect

        from pigbench import overhead
        source = inspect.getsource(overhead).replace("'", '"')
        rpc = inspect.getsource(overhead.one_rpc).replace("'", '"')
        for scenario, fields in k8s.OVERHEAD_FIELDS.items():
            for src in fields:
                if scenario == "rpc" and src.startswith("turn_"):
                    # one_rpc names the medians of its warm turns "turn_" + key.
                    self.assertIn('sample["turn_" + key]', rpc)
                    self.assertIn(f'"{src[len("turn_"):]}"', rpc.split('sample["turn_" + key]')[0].rsplit("for key in", 1)[1], src)
                else:
                    self.assertTrue(f'"{src}"' in source, (scenario, src))
        self.assertIn('out["first_request_cpu_ms"] = reqs[0].hook', source)
        self.assertIn('sample["_series"] = {"gap_ms": gaps, "cpu_turn_ms": cpus}', source)

    def test_the_profiles_pin_the_workload_they_run(self):
        for name in OVERHEAD_PROFILES:
            prof = overhead_profile(name)
            scenarios = None
            for t in prof["targets"]:
                self.assertEqual(t["seed"][2:4], ["k8s", "overhead-seed"], name)
                self.assertEqual(t["sample"][2:4], ["k8s", "overhead-sample"], name)
                self.assertEqual(arg_after(t["sample"], "--target"), t["name"], name)
                self.assertIn(arg_after(t["sample"], "--family"), k8s.OVERHEAD_FAMILIES, name)
                for argv in (t["seed"], t["sample"]):
                    self.assertEqual(arg_after(argv, "--rounds"), "{turns}", name)
                    got = arg_after(argv, "--scenarios")
                    scenarios = scenarios or got
                    self.assertEqual(got, scenarios, f"{name}: every target runs the same scenarios")
                self.assertEqual(t["seedMeta"], "{dir}/overhead-{turns}.json", name)
            for size, fp in prof["fingerprints"].items():
                self.assertEqual(fp, k8s.overhead_fingerprint(int(size), scenarios.split(",")), name)
            produced = {dst for s in k8s.overhead_scenarios(scenarios.split(",")) for dst in k8s.OVERHEAD_FIELDS[s].values()}
            self.assertEqual({m["name"] for m in prof["metrics"]}, produced, name)


class K8sOverheadSampleTests(unittest.TestCase):
    """overhead_sample() with stand-in scenarios: their order, the seed check and the failures."""

    def setUp(self):
        self.dir = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.dir, True)
        self.calls = []
        self.fail = None
        from pigbench import overhead
        patches = [mock.patch.object(overhead, "one_version", self.stand_in("version")), mock.patch.object(overhead, "one_print", self.print_),
                   mock.patch.object(overhead, "one_rpc", self.stand_in("rpc")), mock.patch.object(overhead, "one_pty", self.stand_in("pty"))]
        for p in patches:
            p.start()
            self.addCleanup(p.stop)

    def stand_in(self, scenario):
        def run(ctx, arm):
            self.calls.append((scenario, arm.family, ctx.opts.rpc_turns))
            return {"failure": "boom"} if self.fail == scenario else {"wall_ms": 1.0}
        return run

    def print_(self, ctx, arm, tools=0, ext=False, scenario="print"):
        self.calls.append((scenario, arm.family, tools))
        if self.fail == scenario:
            return {"failure": "boom"}
        return {"first_request_ms": 1.0, "first_request_cpu_ms": 2.0, "_series": {"gap_ms": [3.0] * tools, "cpu_turn_ms": [4.0] * tools}}

    def run_sample(self, family="pig", scenarios=("version", "print", "print-tools", "rpc", "pty"), seed=None):
        k8s.overhead_seed(2, list(seed or scenarios), self.dir)
        return k8s.overhead_sample("t", family, "/bin/true", 2, list(scenarios), self.dir, 1, 30)

    def test_scenarios_run_in_order_with_the_rounds(self):
        line = self.run_sample()
        self.assertEqual(self.calls, [("version", "pig", 2), ("print", "pig", 0), ("print-tools", "pig", 2), ("rpc", "pig", 2), ("pty", "pig", 2)])
        self.assertEqual((line["open"], line["turn"], line["versionMs"]), (1.0, [3.0, 3.0], 1.0))

    def test_another_workload_than_the_seed_is_refused(self):
        with self.assertRaises(SystemExit) as e:
            self.run_sample(scenarios=("print-tools",), seed=("version", "print-tools"))
        self.assertIn("the seed defined workload", str(e.exception))
        self.assertEqual(self.calls, [])

    def test_a_scenario_the_agent_lacks_is_refused_before_anything_runs(self):
        with self.assertRaises(SystemExit) as e:
            self.run_sample(family="codex")
        self.assertIn("codex has no rpc scenario", str(e.exception))
        self.assertEqual(self.calls, [])

    def test_a_failed_scenario_fails_the_sample(self):
        self.fail = "print-tools"
        with self.assertRaises(SystemExit) as e:
            self.run_sample()
        self.assertIn("print-tools: boom", str(e.exception))
        self.assertEqual([c[0] for c in self.calls], ["version", "print", "print-tools"])

    def test_a_connection_outside_the_mock_fails_the_sample(self):
        from pigbench import egress, overhead
        inner = overhead.one_version

        def leaking(ctx, arm):
            egress.OBSERVED.add("198.51.100.9:443")
            return inner(ctx, arm)

        with mock.patch.object(overhead, "one_version", leaking):
            with self.assertRaises(SystemExit) as e:
                self.run_sample()
        self.assertIn("version: connections outside the mock: 198.51.100.9:443", str(e.exception))
        self.assertEqual([c[0] for c in self.calls], ["version"])

    def test_the_cli_prints_only_the_line(self):
        import contextlib
        import io
        from pigbench import __main__ as cli
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            self.assertEqual(cli.main(["k8s", "overhead-seed", "--rounds", "2", "--scenarios", "version,print-tools", "--dir", self.dir]), 0)
        seed = json.loads(out.getvalue())
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            self.assertEqual(cli.main(["k8s", "overhead-sample", "--target", "t", "--family", "pig", "--binary", "/bin/true", "--rounds", "2",
                                       "--scenarios", "version,print-tools", "--dir", self.dir, "--sample", "3"]), 0)
        lines = out.getvalue().splitlines()
        self.assertEqual(len(lines), 1, lines)
        line = json.loads(lines[0])
        self.assertEqual((line["fingerprint"], line["sample"], line["turns"], line["turn"]), (seed["fingerprint"], 3, 2, [3.0, 3.0]))

    def test_the_cli_passes_the_arm(self):
        # --arm names the arms.toml arm; a family that does not match it is refused, so the flag reaches overhead_sample.
        import contextlib
        import io
        from pigbench import __main__ as cli
        from pigbench import arms as armlib
        arm_id = next(i for i, a in armlib.load().items() if a.family == "codex")
        with contextlib.redirect_stdout(io.StringIO()):
            cli.main(["k8s", "overhead-seed", "--rounds", "2", "--scenarios", "version,print-tools", "--dir", self.dir])
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit) as e:
                cli.main(["k8s", "overhead-sample", "--target", "t", "--family", "pig", "--arm", arm_id, "--binary", "/bin/true", "--rounds", "2",
                          "--scenarios", "version,print-tools", "--dir", self.dir])
        self.assertIn(f"arm {arm_id} is of family codex, not pig", str(e.exception))
        self.assertEqual(self.calls, [])

class K8sOverheadArmTests(unittest.TestCase):
    def test_a_plain_agent_has_no_harness_settings(self):
        arm = k8s.overhead_arm("pig", "pig", "/bin/true", None)
        self.assertEqual((arm.id, arm.family, arm.binary, arm.spec), ("pig", "pig", "/bin/true", {}))
        with self.assertRaises(SystemExit):
            k8s.overhead_arm("x", "claude", "/bin/true", None)

    def test_an_arms_toml_arm_brings_its_settings(self):
        from pigbench import arms as armlib
        arm_id, spec = next((i, a) for i, a in armlib.load().items() if a.family == "codex")
        arm = k8s.overhead_arm("codex", "codex", "/bin/true", arm_id)
        self.assertEqual((arm.id, arm.binary, arm.spec), (arm_id, "/bin/true", spec.spec))
        with self.assertRaises(SystemExit):
            k8s.overhead_arm("codex", "pi", "/bin/true", arm_id)
        with self.assertRaises(SystemExit):
            k8s.overhead_arm("codex", "codex", "/bin/true", "no-such-arm")

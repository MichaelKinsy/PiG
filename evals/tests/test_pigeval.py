# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Unit tests for pigeval. Run with: make evals-test"""
import argparse
import json
import os
import shutil
import stat
import subprocess
import sys
import tempfile
import textwrap
import unittest
import urllib.error
import urllib.request

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), ".."))
from pigeval import live, mutate, overhead, perf, registry, report, transcript  # noqa: E402
from pigeval.mockllm import REPLY, MockServer, system_text  # noqa: E402


def post(url, body, stream):
    request = urllib.request.Request(url, data=json.dumps(dict(body, stream=stream)).encode(), headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request, timeout=10) as response:
        return response.read().decode()


def sse(text):
    return [json.loads(line[6:]) for line in text.splitlines() if line.startswith("data: {")]


def script(directory, name, body):
    path = os.path.join(directory, name)
    with open(path, "w") as f:
        f.write(textwrap.dedent(body).lstrip())
    os.chmod(path, os.stat(path).st_mode | stat.S_IEXEC)
    return path


class MockServerTest(unittest.TestCase):
    def test_chat_completions_stream_is_recorded(self):
        with MockServer() as s:
            body = {"model": "bench-1", "messages": [{"role": "system", "content": "sys"}, {"role": "user", "content": "hi"}], "tools": [{"type": "function", "function": {"name": "read"}}]}
            chunks = sse(post(s.base_url + "/v1/chat/completions", body, True))
            self.assertEqual("".join(c["choices"][0]["delta"].get("content", "") for c in chunks if c["choices"]), REPLY)
            self.assertEqual(chunks[-1]["usage"]["completion_tokens"], len(REPLY.split()))
            (request,) = s.recorder.take()
            self.assertEqual((request["protocol"], request["system_bytes"], request["tools"]), ("openai-completions", 3, 1))

    def test_responses_stream_is_ordered(self):
        with MockServer() as s:
            events = sse(post(s.base_url + "/v1/responses", {"model": "bench-1", "instructions": "abc", "input": []}, True))
            self.assertEqual("".join(e["delta"] for e in events if e["type"] == "response.output_text.delta"), REPLY)
            self.assertEqual(events[-1]["type"], "response.completed")
            self.assertEqual([e["sequence_number"] for e in events], list(range(len(events))))
            self.assertEqual(s.recorder.take()[0]["system_bytes"], 3)

    def test_anthropic_stream_and_json(self):
        with MockServer() as s:
            events = sse(post(s.base_url + "/v1/messages?beta=true", {"system": [{"type": "text", "text": "ab"}], "messages": []}, True))
            self.assertEqual("".join(e["delta"]["text"] for e in events if e["type"] == "content_block_delta"), REPLY)
            self.assertEqual(json.loads(post(s.base_url + "/v1/messages", {"messages": []}, False))["content"][0]["text"], REPLY)
            self.assertEqual(s.recorder.take()[0]["system_bytes"], 2)

    def test_unknown_route_is_404_and_recorded(self):
        with MockServer() as s:
            with self.assertRaises(urllib.error.HTTPError) as caught:
                post(s.base_url + "/v1/unknown", {}, False)
            self.assertEqual(caught.exception.code, 404)
            self.assertEqual(s.recorder.take()[0]["protocol"], "other")

    def test_system_text_accepts_content_parts(self):
        self.assertEqual(system_text("openai-completions", {"messages": [{"role": "developer", "content": [{"type": "text", "text": "x"}]}]}), "x")


class RegistryTest(unittest.TestCase):
    def test_repository_registry_pins_every_package(self):
        harnesses = registry.load()
        self.assertTrue({"pig", "pi", "codex", "claude", "opencode", "copilot", "omp"} <= set(harnesses))
        for h in harnesses.values():
            for spec in h.npm:
                self.assertTrue(registry.npm_spec(spec)[1], f"{h.id}: {spec} is not pinned")

    def test_rejects_unknown_keys(self):
        with tempfile.NamedTemporaryFile("w", suffix=".toml", delete=False) as f:
            f.write('[x]\nname = "X"\nbin = "x"\napi = "none"\nprint = ["{prompt}"]\ncolour = "red"\n')
        with self.assertRaisesRegex(ValueError, "unknown keys: colour"):
            registry.load(f.name)
        os.remove(f.name)

    def test_npm_spec_and_substitute(self):
        self.assertEqual(registry.npm_spec("@openai/codex@0.1.0"), ("@openai/codex", "0.1.0"))
        self.assertEqual(registry.npm_spec("opencode-ai@1.2.3"), ("opencode-ai", "1.2.3"))
        self.assertEqual(registry.npm_spec("@scope/pkg"), ("@scope/pkg", ""))
        self.assertEqual(registry.substitute({"a": 1}, ["x{a}y", "{b}"]), ["x1y", "{b}"])


class OverheadTest(unittest.TestCase):
    def test_summary_uses_nearest_rank_p90(self):
        samples = [{"wall": i / 1000, "cpu": 0.001, "rss_mib": 10} for i in range(1, 11)]
        self.assertEqual(overhead.summarize(samples)["wall_ms_p90"], 9.0)
        self.assertEqual(overhead.summarize(samples[:1])["wall_ms_p90"], 1.0)

    def test_end_to_end_with_fake_harness(self):
        work = tempfile.mkdtemp()
        fake = script(work, "fake", """
            #!/usr/bin/env python3
            import json, os, sys, urllib.request
            if "--version" in sys.argv:
                print("fake 1.0"); sys.exit(0)
            base = json.load(open(os.path.join(os.environ["HOME"], ".fake/agent/models.json")))["providers"]["bench"]["baseUrl"]
            body = json.dumps({"model": "bench-1", "stream": True, "messages": [{"role": "system", "content": "s"}, {"role": "user", "content": sys.argv[-1]}]}).encode()
            text = urllib.request.urlopen(urllib.request.Request(base + "/chat/completions", data=body, headers={"Content-Type": "application/json"})).read().decode()
            chunks = [json.loads(l[6:]) for l in text.splitlines() if l.startswith("data: {")]
            print("".join(c["choices"][0]["delta"].get("content", "") for c in chunks if c["choices"]))
        """)
        toml = os.path.join(work, "h.toml")
        with open(toml, "w") as f:
            f.write(f'[fake]\nname = "Fake"\nbin = "{fake}"\napi = "openai-completions"\nconfig = "pi-models"\nconfig_dir = ".fake/agent"\nprint = ["-p", "{{prompt}}"]\n')
        out = os.path.join(work, "o.json")
        args = argparse.Namespace(registry=toml, harnesses="fake", pig=None, runs=2, warmup=0, prompt="hi", session_exchanges=3, out=out, timeout=30)
        self.assertEqual(overhead.run(args), 0)
        rows = {row["scenario"]: row["by"] for row in json.load(open(out))["results"]}
        self.assertEqual(rows["round-trip"]["fake"]["runs"], 2)
        self.assertEqual(rows["round-trip"]["fake"]["requests"], 1)
        self.assertEqual(rows["round-trip"]["fake"]["system_bytes"], 1)
        self.assertIn("Fake", report.overhead(json.load(open(out))))

    def test_own_version_is_recorded_apart_from_version_output(self):
        work = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, work, True)
        fake = script(work, "fakepig", """
            #!/usr/bin/env python3
            import sys
            print("fake: 2.0\\nupstream: 1.0" if sys.argv[1:] == ["version"] else "1.0")
        """)
        h = registry.Harness(id="fake", name="Fake", bin=fake, api="none", print_args=["{prompt}"], own_version_args=["version"])
        entry = overhead.identify(h, fake, dict(os.environ), work, "1.0")
        self.assertEqual(entry, {"version": "2.0", "version_output": "1.0", "version_command": "fakepig --version",
                                 "own_version_command": "fakepig version"})
        plain = registry.Harness(id="plain", name="Plain", bin=fake, api="none", print_args=["{prompt}"])
        self.assertEqual(overhead.identify(plain, fake, dict(os.environ), work, "1.0")["version"], "1.0")

    def test_report_labels_both_versions(self):
        base = {"generated": "t", "host": {"platform": "p", "machine": "m", "cpus": 1}, "warmup": 0, "runs": 1, "prompt": "hi",
                "session_exchanges": 1, "results": []}
        pig = {"name": "PiG", "version": "2.0", "version_output": "1.0", "version_command": "pig --version", "own_version_command": "pig version"}
        pi = {"name": "Pi", "version": "1.0", "version_output": "1.0", "version_command": "pi --version"}
        page = report.overhead(dict(base, harnesses={"pig": pig, "pi": pi}))
        self.assertIn("| PiG | `2.0` | `pig --version`: `1.0` | measured |", page)
        self.assertIn("| Pi | `1.0` | `pi --version`: `1.0` | measured |", page)
        pig["version"] = None
        page = report.overhead(dict(base, harnesses={"pig": pig}))
        self.assertIn("| PiG | not recorded | `pig --version`: `1.0` | measured |", page)

    def test_report_version_note_matches_recorded_pig_output(self):
        base = {"generated": "t", "host": {"platform": "p", "machine": "m", "cpus": 1}, "warmup": 0, "runs": 1, "prompt": "hi",
                "session_exchanges": 1, "results": []}
        pig = {"name": "PiG", "version": None, "version_output": "1.0", "version_command": "pig --version"}
        page = report.overhead(dict(base, harnesses={"pig": pig}))
        self.assertIn("predates the composite version string and printed only the pinned Pi release", page)
        pig["version_output"] = "2.0+1.0"
        page = report.overhead(dict(base, harnesses={"pig": pig}))
        self.assertNotIn("predates", page)
        self.assertIn("PiG's `--version` prints its composite version, `<PiG release>+<Pi release>` (D63).", page)


    def test_repository_registry_reads_pig_own_version(self):
        self.assertEqual(registry.load()["pig"].own_version_args, ["version"])


class LiveTest(unittest.TestCase):
    def run_with(self, body):
        work = tempfile.mkdtemp()
        harness = registry.Harness(id="fake", name="Fake", bin="fake", api="none", print_args=["{prompt}"], live_args=["{prompt}"], mock=False)
        task = live.load_tasks("fix-off-by-one")[0]
        return live.run_task(harness, script(work, "fake.sh", body), task, "x/y", os.path.join(work, "run"), 60)

    def test_fix_passes(self):
        result = self.run_with("#!/usr/bin/env python3\nfrom pathlib import Path\np = Path('stats.py')\np.write_text(p.read_text().replace('len(values) - window)', 'len(values) - window + 1)'))\n")
        self.assertTrue(result["passed"], result["check_output"])

    def test_no_change_fails(self):
        self.assertFalse(self.run_with("#!/bin/sh\ntrue\n")["passed"])

    def test_editing_the_tests_fails(self):
        result = self.run_with("#!/bin/sh\nprintf 'import unittest\\n' > test_stats.py\n")
        self.assertTrue(result["tampered"])
        self.assertFalse(result["passed"])

    def test_every_task_fails_before_the_fix(self):
        for task in live.load_tasks():
            with tempfile.TemporaryDirectory() as d:
                import shutil
                import subprocess
                shutil.copytree(task["files"], d, dirs_exist_ok=True)
                self.assertNotEqual(subprocess.run(["bash", "-c", task["check"]], cwd=d, capture_output=True).returncode, 0, task["id"])


class PerfTest(unittest.TestCase):
    def test_benchcmp_flags_regressions(self):
        work = tempfile.mkdtemp()
        old, new = os.path.join(work, "old"), os.path.join(work, "new")
        open(old, "w").write("BenchmarkA-8 100 1000 ns/op 64 B/op 2 allocs/op\nBenchmarkA-8 100 1010 ns/op 64 B/op 2 allocs/op\n")
        open(new, "w").write("BenchmarkA-8 100 1200 ns/op 64 B/op 2 allocs/op\n")
        self.assertEqual(perf.benchcmp(argparse.Namespace(old=old, new=new, threshold=5.0, fail=True)), 1)
        self.assertEqual(perf.benchcmp(argparse.Namespace(old=old, new=old, threshold=5.0, fail=True)), 0)

    def test_budgets_and_parity_allowance(self):
        work = tempfile.mkdtemp()
        results = os.path.join(work, "r.json")
        json.dump({"results": [{"scenario": "round-trip", "by": {"pig": {"wall_ms_median": 10, "request_bytes": 1300}, "pi": {"request_bytes": 1000}}}]}, open(results, "w"))
        budgets = os.path.join(work, "b.toml")
        open(budgets, "w").write('[pig.round-trip]\nwall_ms_median = 20\n[parity]\nrequest_bytes_over_pi = 256\n')
        self.assertEqual(perf.check(argparse.Namespace(results=results, budgets=budgets)), 1)
        open(budgets, "w").write('[pig.round-trip]\nwall_ms_median = 20\n[parity]\nrequest_bytes_over_pi = 400\n')
        self.assertEqual(perf.check(argparse.Namespace(results=results, budgets=budgets)), 0)


class TranscriptTest(unittest.TestCase):
    def test_polling_detection(self):
        for command, want in [("sleep 5", True), ("sleep 5 && ps aux", True), ("pgrep -f build; top -bn1", True), ("tail -f log", True),
                              ("sleep 5 && make", False), ("go test ./...", False), ("", False)]:
            self.assertEqual(transcript.is_polling(command), want, command)

    def test_pi_format(self):
        msg = lambda cmds, cost: json.dumps({"type": "message_end", "message": {"role": "assistant", "content": [{"type": "toolCall", "name": "bash", "arguments": {"command": c}} for c in cmds],
                                             "usage": {"input": 100, "cacheRead": 50, "cacheWrite": 0, "output": 7, "cost": {"total": cost}}}})
        m = transcript.metrics("\n".join([msg(["./build.sh &", "sleep 5"], 0.01), msg(["sleep 5 && ps aux"], 0.02), msg([], 0.005)]))
        self.assertEqual((m["format"], m["turns"], m["tool_calls"], m["polling_calls"], m["context_tokens"], m["output_tokens"]), ("pi", 3, 3, 2, 450, 21))
        self.assertAlmostEqual(m["cost_usd"], 0.035)

    def test_claude_format(self):
        lines = [json.dumps({"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "Bash", "input": {"command": "sleep 10"}}]}}),
                 json.dumps({"type": "result", "num_turns": 4, "total_cost_usd": 0.12, "usage": {"input_tokens": 10, "cache_creation_input_tokens": 20, "cache_read_input_tokens": 30, "output_tokens": 5}})]
        m = transcript.metrics("\n".join(lines))
        self.assertEqual((m["format"], m["turns"], m["polling_calls"], m["context_tokens"], m["cost_usd"]), ("claude", 4, 1, 60, 0.12))

    def test_codex_format_and_unknown(self):
        lines = [json.dumps({"type": "item.completed", "item": {"type": "command_execution", "command": "pgrep build"}}),
                 json.dumps({"type": "turn.completed", "usage": {"input_tokens": 900, "cached_input_tokens": 100, "output_tokens": 9}})]
        m = transcript.metrics("\n".join(lines))
        self.assertEqual((m["format"], m["polling_calls"], m["context_tokens"], m["cost_usd"]), ("codex", 1, 1000, None))
        self.assertIsNone(transcript.metrics("plain text output"))

    def test_summary_and_report(self):
        mk = lambda run, passed, cost: {"run": run, "passed": passed, "wall_s": 1.0, "metrics": {"cost_usd": cost, "turns": 4, "polling_calls": 0, "tool_calls": 2, "context_tokens": 1000, "output_tokens": 10}}
        s = live.summarize([mk(0, True, 0.1), mk(0, False, 0.3), mk(1, True, 0.2), mk(1, True, 0.2)])
        self.assertEqual((s["passed"], s["pass_rate"]), (3, 75.0))
        self.assertAlmostEqual(s["cost_per_passing_task"], 0.8 / 3)
        text = report.live({"model": "m", "runs": 2, "tasks": ["t"], "generated": "now", "harnesses": {"pig": {"name": "PiG"}}, "summary": {"pig": s}})
        self.assertIn("| PiG | 75% ± 25% (3/4) |", text)


@unittest.skipUnless(shutil.which("gofmt"), "gofmt not installed")
class MutateTest(unittest.TestCase):
    def test_generation_is_seeded_and_every_task_is_valid(self):
        a, b = tempfile.mkdtemp(), tempfile.mkdtemp()
        ids = mutate.generate(a, 6, 7, ["agent"])
        self.assertEqual(len(ids), 6)
        self.assertEqual(ids, mutate.generate(b, 6, 7, ["agent"]))
        for task in live.load_tasks("all", a):
            self.assertIn("The issue is in the `", task["prompt"])
            self.assertNotEqual(subprocess.run(["bash", "-c", task["check"]], cwd=task["files"], capture_output=True).returncode, 0, task["id"])


if __name__ == "__main__":
    unittest.main()

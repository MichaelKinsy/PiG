# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Unit tests for the streaming-shapes driver and its report.

  python3 -m unittest discover -s bench/compare/tests
"""
import contextlib
import json
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
from pathlib import Path
from types import SimpleNamespace

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

import shapes_report  # noqa: E402
import stream_shapes  # noqa: E402


class Bodies(unittest.TestCase):
    def test_exact_size_and_shape_markers(self):
        for size in (1, 6000, 60000):
            for shape in stream_shapes.SHAPES:
                self.assertEqual(len(stream_shapes.body(shape, size)), size, (shape, size))
        b = {shape: stream_shapes.body(shape, 6000) for shape in stream_shapes.SHAPES}
        self.assertIn("```ts\n", b["code"])
        self.assertIn("|---|---:|---|:---:|", b["tables"])
        self.assertIn("\n  - Sub ", b["lists"])
        self.assertIn("## Section 1\n", b["prose"])
        self.assertFalse(any(m in b["prose"] for m in ("```", "|", "\n- ")))
        self.assertEqual(len(set(b.values())), 4)


class RoundOrder(unittest.TestCase):
    def test_every_build_once_and_rotated(self):
        builds = ["a", "b", "c", "d"]
        firsts = []
        for r in range(len(builds)):
            order = stream_shapes.round_order(builds, r)
            self.assertEqual(sorted(order), builds)
            firsts.append(order[0])
        self.assertEqual(sorted(firsts), builds)


class FakeTty:
    def __init__(self, answers, raise_on_send=False):
        self.answers, self.raise_on_send, self.buf, self.pid, self.quit_calls = answers, raise_on_send, b"", 1, 0

    def wait_for(self, text, start, deadline):
        return text == "fake-model" or text in self.answers

    def settle(self, *a):
        pass

    def send(self, data):
        if self.raise_on_send:
            raise OSError("pty closed")

    def quit(self):
        self.quit_calls += 1


def fake_harness(tty):
    nothing = contextlib.nullcontext
    return SimpleNamespace(pi_home=lambda port: nothing("/home"), workdir=lambda: nothing("/w"),
                           Tty=lambda *a, **k: tty, pinned=lambda argv, cpus: argv, pi_env=lambda home: {}, cpu_ms=lambda pid: 0.0,
                           memory_mb=lambda pid: {"own": 1})


class OneSession(unittest.TestCase):
    def run_one(self, tty, sizes=(6000, 60000)):
        build = SimpleNamespace(name="x", argv=["x"])
        files = {("prose", s): f"/f{s}" for s in sizes}
        with mock.patch.object(stream_shapes.fake_server, "fake_model", lambda h, **k: contextlib.nullcontext(1)):
            return stream_shapes.one(fake_harness(tty), build, "prose", "completions", list(sizes), 7, 20, files)

    def test_failed_answer_loses_later_sizes_as_rows(self):
        rows = self.run_one(FakeTty(answers=set()))
        self.assertEqual([(r["size"], "error" in r) for r in rows], [(6000, True), (60000, True)])

    def test_answered_sizes_are_samples(self):
        rows = self.run_one(FakeTty(answers={"Done: mdfile, answer 1.", "Done: mdfile, answer 2."}))
        self.assertEqual([(r["size"], "cpu_s" in r) for r in rows], [(6000, True), (60000, True)])

    def test_session_is_closed_when_a_send_raises(self):
        tty = FakeTty(answers=set(), raise_on_send=True)
        with self.assertRaises(OSError):
            self.run_one(tty)
        self.assertEqual(tty.quit_calls, 1)


def row(build, shape, api, size, cpu=None, error=None):
    r = {"round": 0, "build": build, "shape": shape, "api": api, "size": size}
    r.update({"cpu_s": cpu} if error is None else {"error": error})
    return r


class Report(unittest.TestCase):
    def rows(self):
        out = []
        for api, k in (("completions", 1.0), ("anthropic", 1.5)):
            for r in range(3):
                out += [row("pig", "prose", api, 6000, 1.0 * k), row("pig-b", "prose", api, 6000, 1.05 * k),
                        row("bolt", "prose", api, 6000, 2.0 * k + r * 0.01), row("bolt-copy", "prose", api, 6000, 2.0 * k),
                        row("node", "code", api, 6000, 4.0)]
        out += [row("pig", "code", "completions", 6000, 0.5), row("pig", "code", "anthropic", 6000, 0.5)]
        out += [row("node-aa", "prose", "completions", 6000, error="no answer"), row("node", "prose", "completions", 6000, error="no answer")]
        return out

    def test_pairs_from_suffixes_and_explicit(self):
        self.assertEqual(shapes_report.pairs_of(["pig", "pig-b", "node", "node-aa", "bolt-1", "bolt-copy"], [("bolt-1", "bolt-copy")]),
                         {"pig": "pig-b", "node": "node-aa", "bolt-1": "bolt-copy"})

    def test_missing_cell_is_a_dash_and_failures_are_counted(self):
        text = shapes_report.render(shapes_report.aggregate(self.rows()))
        self.assertIn("samples that did not run: 2", text)
        self.assertIn("node: 1 x no answer", text)
        self.assertIn("prose completions 6000 pig 1.00", text)
        self.assertIn("| node -", text)

    def test_ratio_range_ends_are_the_extreme_cells(self):
        agg = shapes_report.aggregate(self.rows(), base="pig")
        node = [r for r in agg["ratios"] if r[0] == "node"][0]
        self.assertEqual((round(node[2][0], 2), round(node[3][0], 2), node[4]), (8.0, 8.0, 2))  # only the code cells have both
        # bolt/pig: completions 2.01/1.0, anthropic 3.01/1.5 = 2.0067: the low end is the anthropic cell.
        bolt = [r for r in agg["ratios"] if r[0] == "bolt"][0]
        self.assertEqual((round(bolt[2][0], 4), bolt[2][1:], round(bolt[3][0], 4), bolt[3][1:], bolt[4]),
                         (2.0067, ("prose", "anthropic"), 2.01, ("prose", "completions"), 2))

    def test_wire_delta_outside_the_floor_is_flagged(self):
        text = shapes_report.render(shapes_report.aggregate(self.rows()))
        self.assertIn("pig prose 6000: 50.0% (A/A 5.0%) OUTSIDE A/A", text)
        self.assertIn("pig code 6000: 0.0% (A/A -)", text)

    def test_cli_runs_on_rows(self):
        with tempfile.NamedTemporaryFile("w", suffix=".jsonl", delete=False) as f:
            f.write("\n".join(json.dumps(r) for r in self.rows()))
        out = subprocess.run([sys.executable, str(HERE / "shapes_report.py"), f.name, "--base", "pig"], capture_output=True, text=True, timeout=60)
        self.assertEqual(out.returncode, 0, out.stderr)
        self.assertIn("other/pig median ratio", out.stdout)


if __name__ == "__main__":
    unittest.main()

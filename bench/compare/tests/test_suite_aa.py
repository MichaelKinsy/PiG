# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Unit tests for the A/A support of run_suite.py and report.py, .

  python3 -m unittest discover -s bench/compare/tests
"""
import sys
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

import cleanenv  # noqa: E402
import report  # noqa: E402
import run_suite  # noqa: E402


class AADelta(unittest.TestCase):
    def test_copy_column_shows_the_floor_as_percent_of_the_lower_median(self):
        rows = report.aa_delta(["metric", "a", "a-aa", "b"], [["t", "100 (n=5)", "125 (n=5)", "7 (n=5)"]])
        self.assertEqual(rows[0], ["t", "100 (n=5)", "125 (n=5) [A/A 25%]", "7 (n=5)"])

    def test_missing_or_non_numeric_cells_are_left_alone(self):
        rows = report.aa_delta(["metric", "a", "a-aa"], [["t", "cannot run", "cannot run"], ["u", "-", "3 (n=1)"], ["v", "0 (n=1)", "0 (n=1)"]])
        self.assertEqual(rows, [["t", "cannot run", "cannot run"], ["u", "-", "3 (n=1)"], ["v", "0 (n=1)", "0 (n=1)"]])

    def test_a_copy_without_its_arm_is_not_a_pair(self):
        self.assertEqual(report.aa_delta(["metric", "x-aa"], [["t", "5 (n=1)"]]), [["t", "5 (n=1)"]])


class ReleaseColumn(unittest.TestCase):
    H = ["metric", "pig-0.4.1", "pig-0.4.1-aa", "pig-tip", "pig-tip-aa", "pibolt"]

    def col(self, row):
        h, rows = report.release_column(self.H, report.aa_delta(self.H, [row]))
        self.assertTrue(h[-1].startswith("0.4.1 -> pig-tip"))
        return rows[0][-1]

    def test_loss_and_win_outside_the_floor_inside_is_no_difference(self):
        self.assertEqual(self.col(["CPU", "100 (n=5)", "102 (n=5)", "150 (n=5)", "152 (n=5)", "1"]), "1.50x LOSE")
        self.assertEqual(self.col(["CPU", "100 (n=5)", "102 (n=5)", "50 (n=5)", "51 (n=5)", "1"]), "0.50x WIN")
        self.assertEqual(self.col(["CPU", "100 (n=5)", "110 (n=5)", "104 (n=5)", "105 (n=5)", "1"]), "1.04x no measurable difference")

    def test_neutral_rows_show_the_ratio_only_and_missing_cells_a_dash(self):
        self.assertEqual(self.col(["Frames per prompt", "100 (n=5)", "100 (n=5)", "50 (n=5)", "50 (n=5)", "1"]), "0.50x")
        self.assertEqual(self.col(["CPU", "cannot run", "-", "50 (n=5)", "-", "1"]), "-")

    def test_no_release_arm_no_column(self):
        h, rows = report.release_column(["metric", "pig-tip"], [["x", "1"]])
        self.assertEqual((h, rows), (["metric", "pig-tip"], [["x", "1"]]))


class CleanEnv(unittest.TestCase):
    def test_go_runtime_lane_and_agent_variables_are_dropped_and_the_rest_kept(self):
        env = {"GOMAXPROCS": "24", "GOMEMLIMIT": "1GiB", "GOGC": "50", "GOFLAGS": "-x", "PIG_LANE_SLICE": "x", "PIG_CODING_AGENT_DIR": "/a", "PI_MODEL": "m",
               "NODE_OPTIONS": "o", "BUN_JSC_x": "1", "COPILOT_TOKEN": "t", "PATH": "/bin", "HOME": "/h", "LANG": "C", "TMPDIR": "/t", "TERM": "xterm"}
        got = cleanenv.clean_env(env)
        self.assertEqual({k: v for k, v in got.items() if k != "PATH"}, {"HOME": "/h", "LANG": "C", "TMPDIR": "/t", "TERM": "xterm"})
        self.assertTrue(got["PATH"].endswith("/bin"))

    def test_lane_shims_leave_the_path_and_the_pinned_node_comes_first(self):
        got = cleanenv.clean_env({"PATH": "/x/lane-shims:/usr/bin"})["PATH"].split(":")
        self.assertNotIn("/x/lane-shims", got)
        self.assertEqual(got[-1], "/usr/bin")
        if cleanenv.NODE_BIN.is_dir():
            self.assertEqual(got[0], str(cleanenv.NODE_BIN))

    def test_apply_cleans_the_process_environment_and_names_what_it_dropped(self):
        import os
        old = dict(os.environ)
        try:
            os.environ["GOMAXPROCS"] = "24"
            dropped = cleanenv.apply()
            self.assertIn("GOMAXPROCS", dropped)
            self.assertNotIn("GOMAXPROCS", os.environ)
        finally:
            os.environ.clear()
            os.environ.update(old)


class ReleaseVerdictPrecision(unittest.TestCase):
    """The verdict reads medians as displayed: a difference inside their rounding, or inside an A/A floor that rounds to 0%, is not a WIN or LOSE."""
    H = ["metric", "pig-0.4.1", "pig-0.4.1-aa", "pig-tip", "pig-tip-aa"]

    def col(self, row):
        return report.release_column(self.H, report.aa_delta(self.H, [row]))[1][0][-1]

    def test_a_sub_percent_floor_is_not_a_zero_floor(self):
        self.assertEqual(self.col(["CPU", "100.0 (n=5)", "100.4 (n=5)", "100.3 (n=5)", "100.0 (n=5)"]), "1.00x no measurable difference")
        self.assertEqual(self.col(["CPU", "100.0 (n=5)", "100.4 (n=5)", "101.0 (n=5)", "100.9 (n=5)"]), "1.01x LOSE")

    def test_equal_up_to_display_rounding_is_no_difference(self):
        self.assertEqual(self.col(["pi --version", "18 (n=60)", "18 (n=60)", "19 (n=60)", "19 (n=60)"]), "1.06x no measurable difference")
        self.assertEqual(self.col(["pi --version", "18 (n=60)", "18 (n=60)", "20 (n=60)", "20 (n=60)"]), "1.11x LOSE")

    def test_a_lost_copy_sample_leaves_the_ratio_alone(self):
        self.assertEqual(self.col(["CPU", "100 (n=5)", "-", "150 (n=5)", "150.2 (n=5)"]), "1.50x (no A/A)")


class RunSuiteAA(unittest.TestCase):
    def test_every_arm_gets_its_copy_next_to_it(self):
        spec, arms = run_suite.with_aa({"a": "cmd-a", "b": "cmd-b"}, ["a", "b"])
        self.assertEqual(arms, ["a", "a-aa", "b", "b-aa"])
        self.assertEqual(spec, {"a": "cmd-a", "a-aa": "cmd-a", "b": "cmd-b", "b-aa": "cmd-b"})

    def test_a_copy_cannot_run_what_its_arm_cannot_run(self):
        self.assertEqual(run_suite.runnable(["gi", "gi-aa", "pig-tip", "pig-tip-aa"], "headless"), ["pig-tip", "pig-tip-aa"])
        self.assertEqual(run_suite.runnable(["gi", "gi-aa"]), ["gi", "gi-aa"])


class ReportCannotRunCopy(unittest.TestCase):
    def test_copy_column_says_cannot_run_like_its_arm(self):
        import json
        import tempfile
        from unittest import mock
        with tempfile.TemporaryDirectory() as d:
            rows = [{"build": b, "scenario": "interactive", "run": 0, "ok": True, "tti_ms": 100, "turns_ms": 1, "cpu_ms": 1, "peak_mb": 1} for b in ("pig-0.4.1", "gi", "gi-aa")]
            Path(d, "benchmark.jsonl").write_text("".join(json.dumps(r) + "\n" for r in rows))
            with mock.patch.object(sys, "argv", ["report.py", "--out", d]), mock.patch("builtins.print"):
                report.main()
            line = next(l for l in Path(d, "report.md").read_text().splitlines() if l.startswith("| pi -p: one prompt"))
        self.assertEqual([c.strip() for c in line.strip("|").split("|")][1:], ["-", "cannot run", "cannot run"])


class IdleSettled(unittest.TestCase):
    def run_idle(self, pumps_before_title):
        import subprocess
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            # A Tty whose program writes its terminal title on the given pump (None: never).
            Path(d, "harness.py").write_text(
                f"class Tty:\n    calls = []\n    buf = b'frame fake-model'\n    pumps = 0\n    title_at = {pumps_before_title!r}\n"
                "    def wait_for(self, text, since, deadline):\n        Tty.calls.append(('wait', text, since))\n        return True\n"
                "    def pump(self, timeout):\n        Tty.pumps += 1\n"
                "        if Tty.pumps == Tty.title_at:\n            Tty.buf += b'\\x1b]0;PiG - work\\x07'\n        return True\n"
                "    def settle(self, quiet, deadline):\n        Tty.calls.append(('settle', quiet))\n        return True\n")
            Path(d, "idle.py").write_text(
                "import sys\nfrom harness import Tty\nt = Tty()\n"
                "t.wait_for('fake-model', 0, 1e12)\nTty.calls.append(('pumped', Tty.pumps))\n"
                "t.wait_for('(prompt 1)', 5, 1e12)\nt.wait_for('fake-model', 7, 1e12)\nt.settle(2.0, 1e12)\n"
                "print(repr((Tty.calls, sys.argv[1:])))\n")
            p = subprocess.run([sys.executable, "-c", "import sys, runpy; sys.path.insert(0, sys.argv[1]); import startup_ready; "
                                "startup_ready.MARKER_WAIT_S = 0.2; sys.argv = sys.argv[1:]; runpy.run_path(sys.argv[0] + '/idle_settled.py', run_name='__main__')",
                                str(HERE), d, "--seconds", "30"], capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stderr)
        return eval(p.stdout)  # noqa: S307 - our own repr

    def test_waits_for_the_terminal_title_after_the_first_model_name_and_passes_arguments_through(self):
        calls, argv = self.run_idle(3)
        self.assertEqual(calls, [("wait", "fake-model", 0), ("pumped", 3), ("wait", "(prompt 1)", 5), ("wait", "fake-model", 7), ("settle", 2.0)])
        self.assertEqual(argv, ["--seconds", "30"])

    def test_an_arm_that_never_sets_a_title_gets_a_quiet_settle(self):
        calls, _ = self.run_idle(None)
        self.assertEqual([c for c in calls if c[0] != "pumped"], [("wait", "fake-model", 0), ("settle", 2.0), ("wait", "(prompt 1)", 5), ("wait", "fake-model", 7), ("settle", 2.0)])


class ScreenReady(unittest.TestCase):
    def test_the_model_name_counts_only_once_the_pane_title_changed(self):
        import subprocess
        import tempfile
        with tempfile.TemporaryDirectory() as d:
            # A tmux whose pane shows the model name from the first poll and gets the TUI's title on the fourth.
            Path(d, "e2e_screen.py").write_text(
                "import types\nstate = {'polls': 0}\nlog = []\n"
                "def tmux(*args):\n"
                "    if args[0] == 'kill-server':\n        state['polls'] = 0\n"
                "    if args[0] == 'capture-pane':\n        state['polls'] += 1\n"
                "    titled = state['polls'] >= 4\n"
                "    out = {'capture-pane': 'footer fake-model', 'display-message': 'PiG - work' if titled else 'host'}.get(args[0], '')\n"
                "    return types.SimpleNamespace(stdout=out)\n"
                "def main():\n"
                "    for run in range(2):\n        tmux('kill-server')\n        tmux('new-session', '-d')\n"
                "        n = 0\n        while 'fake-model' not in tmux('capture-pane', '-p').stdout:\n            n += 1\n"
                "        log.append(n)\n        log.append('fake-model' in tmux('capture-pane', '-p').stdout)\n"
                "    import sys\n    print(repr((log, sys.argv[1:])))\n")
            p = subprocess.run([sys.executable, str(HERE / "screen_ready.py"), d, "--widths", "80"], capture_output=True, text=True, timeout=60)
        self.assertEqual(p.returncode, 0, p.stderr)
        log, argv = eval(p.stdout)  # noqa: S307 - our own repr
        # Each run (a new tmux server): polls 1-3 hide the name (title unchanged), poll 4 shows it; later polls are untouched.
        self.assertEqual(log, [3, True, 3, True])
        self.assertEqual(argv, ["--widths", "80"])


if __name__ == "__main__":
    unittest.main()

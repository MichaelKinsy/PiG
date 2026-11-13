# SPDX-License-Identifier: MIT
import io
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import bump  # noqa: E402
import pinlib  # noqa: E402

OLD = pinlib.Pin("v1.1.0", "a" * 40, "1.1.0", "1.1.0")
NEW = pinlib.Pin("b" * 40, "b" * 40, "1.1.0", "1.1.0+bbbbbbbb")


class Runner:
    def __init__(self, fail: str | None = None):
        self.calls: list[str] = []
        self.fail = fail

    def __call__(self, command, env, cwd):
        text = " ".join(command)
        self.calls.append(text)
        return 1 if self.fail and self.fail in text else 0


def make_bump(root: Path, runner, earlier=OLD, **kwargs) -> bump.Bump:
    moved = []
    out = io.StringIO()
    b = bump.Bump(root, NEW, earlier, runner=runner, retire_fn=kwargs.get("retire_fn", lambda pin: {"backports": {}, "carried_oracle": None, "changed": []}),
                  move_fn=lambda pin: moved.append(pin) or 0, summarize=lambda d: ["a decision"], env={"PI_UPSTREAM_REF": "x", "PUBLIC_MAIN_REF": "refs/x"}, out=out)
    b.moved, b.output = moved, out
    return b


class StepTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.root = Path(self.dir.name)
        (self.root / "coding").mkdir()
        (self.root / "coding/upstream.go").write_text('const UpstreamReviewedVersion = "0.84.0"\n')

    def tearDown(self):
        self.dir.cleanup()

    def test_one_run_does_every_step_in_order_and_records_time_and_exit_code(self):
        runner = Runner()
        b = make_bump(self.root, runner)
        self.assertEqual(b.run(), 0)
        joined = "\n".join(runner.calls)
        order = [joined.index(marker) for marker in ("upstream-mirror", "interface-proposals", "pin-ledgers PIN_FROM=v1.1.0", "vendor-pi-packs", "regenerate-observation-oracles.sh", "pin.py gaps --updates 2", "sdksurface", "make --no-print-directory generate")]
        self.assertEqual(order, sorted(order))
        state = json.loads((self.root / "tmp/pin-bump" / ("b" * 8 + ".json")).read_text())
        self.assertEqual([s["name"] for s in state["steps"]], bump.Bump.ORDER)
        self.assertTrue(all(s["rc"] == 0 and "seconds" in s for s in state["steps"]))
        self.assertEqual(len(b.moved), 1)
        self.assertIn("carried work decided", b.output.getvalue())

    def test_the_sdk_vectors_are_regenerated_after_the_oracle_steps_and_before_the_surface_page_needs_them(self):
        runner = Runner()
        make_bump(self.root, runner).run()
        joined = "\n".join(runner.calls)
        vectors = joined.index("node automation/gen/generate-sdk-vectors.mjs")
        self.assertLess(joined.index("regenerate-observation-oracles.sh"), vectors)
        self.assertLess(vectors, joined.index("sdksurface"))

    def test_no_step_sees_the_pi_upstream_ref_override(self):
        seen = []
        b = make_bump(self.root, lambda command, env, cwd: seen.append(env) or 0)
        b.run()
        self.assertTrue(seen and all("PI_UPSTREAM_REF" not in env for env in seen))

    def test_a_failed_step_stops_the_run_and_a_rerun_resumes_at_it(self):
        failing = Runner(fail="vendor-pi-packs")
        self.assertEqual(make_bump(self.root, failing).run(), 1)
        state = json.loads((self.root / "tmp/pin-bump" / ("b" * 8 + ".json")).read_text())
        self.assertEqual([s["name"] for s in state["steps"] if s["rc"] == 0], ["retire", "move", "mirror", "inventories", "ledgers"])
        self.assertEqual(bump.Bump.ORDER[5:5 + len(bump.VENDOR_TARGETS)], bump.VENDOR_TARGETS, "each vendor target is a step with its own resume mark")
        self.assertNotIn("observation", " ".join(failing.calls))
        again = Runner()
        b = make_bump(self.root, again, earlier=None)  # the pin file moved: the earlier pin comes from the record
        self.assertEqual(b.run(), 0)
        self.assertNotIn("upstream-mirror", " ".join(again.calls))
        self.assertNotIn("pin-ledgers", " ".join(again.calls))
        self.assertIn("vendor-pi-packs", " ".join(again.calls))
        self.assertEqual(b.moved, [], "move passed earlier")
        self.assertEqual(b.state["from"]["ref"], "v1.1.0")

    def test_a_finished_bump_is_a_no_op(self):
        make_bump(self.root, Runner()).run()
        again = Runner()
        b = make_bump(self.root, again, earlier=None)
        self.assertEqual(b.run(), 0)
        self.assertEqual(again.calls, [])
        self.assertIn("already bumped", b.output.getvalue())

    def test_a_retire_failure_stops_before_the_pin_moves(self):
        def conflict(pin):
            raise pinlib.PinError("backport 0e1e3fc81 neither applies nor is carried")
        b = make_bump(self.root, Runner(), retire_fn=conflict)
        with mock.patch("sys.stderr", new_callable=io.StringIO) as err:
            self.assertEqual(b.run(), 1)
        self.assertEqual(b.moved, [])
        self.assertIn("0e1e3fc81", err.getvalue())

    def test_the_decisions_are_in_the_state_file(self):
        decisions = {"backports": {"abc": {"state": "carried", "files": ["x"], "detail": ""}}, "carried_oracle": None, "changed": []}
        make_bump(self.root, Runner(), retire_fn=lambda pin: decisions).run()
        state = json.loads((self.root / "tmp/pin-bump" / ("b" * 8 + ".json")).read_text())
        self.assertEqual(state["decisions"], decisions)

    def test_the_summary_says_which_provider_data_was_used_and_a_first_build_says_it_hydrates(self):
        frozen = self.root / "automation/pin/frozen"
        frozen.mkdir(parents=True)
        (frozen / ("b" * 40 + ".provider-data.tgz.sha256")).write_text("abc123\n")
        b = make_bump(self.root, Runner())
        b.run()
        text = b.output.getvalue()
        self.assertIn("the frozen snapshot of bbbbbbbb (digest abc123", text)
        self.assertIn("provider data: frozen, digest abc123", text)

    def test_a_commit_without_frozen_data_names_the_network_the_first_build_needs_and_the_digest_it_froze(self):
        runner_calls = []

        def runner(command, env, cwd):
            runner_calls.append(command)
            if "upstream-mirror" in command:  # the first oracle build freezes the hydrated data
                frozen = self.root / "automation/pin/frozen"
                frozen.mkdir(parents=True, exist_ok=True)
                (frozen / ("b" * 40 + ".provider-data.tgz.sha256")).write_text("f00d\n")
            return 0
        b = make_bump(self.root, runner)
        b.run()
        text = b.output.getvalue()
        self.assertIn("hydrates it from live catalog services (models.dev, openrouter.ai, ai-gateway.vercel.sh)", text)
        self.assertIn("depends on the day it runs", text)
        self.assertIn("provider data: hydrated, digest f00d", text)
        self.assertEqual(json.loads((self.root / "tmp/pin-bump" / ("b" * 8 + ".json")).read_text())["provider_data"], {"source": "hydrated", "digest": "f00d"})

    def test_a_clone_without_public_main_fails_first_with_the_remedy(self):
        subprocess.run(["git", "init", "-q"], cwd=self.root, check=True)
        b = bump.Bump(self.root, NEW, OLD, retire_fn=lambda pin: {}, move_fn=lambda pin: 0, env={}, out=io.StringIO())
        with self.assertRaisesRegex(pinlib.PinError, r"no refs/remotes/origin/main.*set PUBLIC_MAIN_REF="):
            b.run()
        self.assertFalse((self.root / "tmp").exists(), "nothing ran")


class EarlierPinTests(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        self.root = Path(self.dir.name)
        git = lambda *a: subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@t", *a], cwd=self.root, check=True, capture_output=True)  # noqa: E731
        git("init", "-q")
        (self.root / ".upstream").mkdir()
        (self.root / ".upstream/pin").write_text(pinlib.render_pin_file(OLD))
        git("add", ".")
        git("commit", "-q", "-m", "x")

    def tearDown(self):
        self.dir.cleanup()

    def test_the_committed_pin_is_the_earlier_one_before_the_move(self):
        with mock.patch.object(pinlib, "committed", lambda: OLD):
            self.assertEqual(bump.earlier_pin(self.root, NEW, None), OLD)

    def test_once_the_pin_file_moved_head_still_names_the_earlier_pin(self):
        (self.root / ".upstream/pin").write_text(pinlib.render_pin_file(NEW))
        with mock.patch.object(pinlib, "committed", lambda: NEW):
            self.assertEqual(bump.earlier_pin(self.root, NEW, None), OLD)

    def test_a_pin_committed_without_a_record_has_no_earlier_pin_unless_named(self):
        (self.root / ".upstream/pin").write_text(pinlib.render_pin_file(NEW))
        subprocess.run(["git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qam", "bump"], cwd=self.root, check=True)
        with mock.patch.object(pinlib, "committed", lambda: NEW), mock.patch.object(pinlib, "resolve", lambda ref, base: OLD):
            self.assertIsNone(bump.earlier_pin(self.root, NEW, None))
            self.assertEqual(bump.earlier_pin(self.root, NEW, "v1.1.0"), OLD)

    def test_a_record_of_the_bump_names_the_earlier_pin_itself(self):
        (self.root / "tmp/pin-bump").mkdir(parents=True)
        (self.root / "tmp/pin-bump" / ("b" * 8 + ".json")).write_text("{}")
        with mock.patch.object(pinlib, "committed", lambda: NEW):
            self.assertIsNone(bump.earlier_pin(self.root, NEW, None))


class SnapshotTests(unittest.TestCase):
    def test_only_the_new_pin_and_the_reviewed_baseline_stay_in_the_index(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "coding").mkdir()
            (root / "coding/upstream.go").write_text('const UpstreamReviewedVersion = "0.84.0"\n')
            names = ["mapping-v1.1.0.json", "mapping-v1.1.0+bbbbbbbb.json", "upstream-v0.84.0.json", "delta-v0.84.0-v1.1.0.json", "delta-v0.84.0-v1.1.0+bbbbbbbb.json", "pig-go.json", "go.mod"]
            (root / "test/parity/interfaces").mkdir(parents=True)
            for name in names:
                (root / "test/parity/interfaces" / name).write_text("{}")
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            subprocess.run(["git", "add", "-f", "."], cwd=root, check=True)
            self.assertEqual(bump.obsolete_snapshots(root, NEW), ["test/parity/interfaces/delta-v0.84.0-v1.1.0.json", "test/parity/interfaces/mapping-v1.1.0.json"])


if __name__ == "__main__":
    unittest.main()

# SPDX-License-Identifier: MIT
"""Tests of the pin-follow pipeline. No network, no build: commands go through a fake runner."""
import datetime as dt
import json
import os
import sys
import tempfile
import time
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import follow  # noqa: E402

UTC = dt.timezone.utc
COMMIT = "4ac0bd8c" + "7" * 32
UPSTREAM_TIME = dt.datetime(2026, 10, 10, 15, 35, tzinfo=UTC)
# The mirror directory name the pipeline derives from its version (follow.py: .upstream/v<version>); the fixture builds it the same way.
MIRROR_VERSION = "1.1.0+4ac0bd8c"


def tree(root: Path, files: dict[str, str]) -> None:
    for rel, text in files.items():
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")


class Clock:
    def __init__(self, start: dt.datetime, tick: int = 60):
        self.now, self.tick = start, dt.timedelta(seconds=tick)

    def __call__(self) -> dt.datetime:
        self.now += self.tick
        return self.now


class FakeRunner:
    def __init__(self, fail: str | None = None):
        self.calls: list[list[str]] = []
        self.fail = fail

    def __call__(self, command, env, timeout, cwd):
        self.calls.append(list(command))
        return 1 if self.fail and self.fail in " ".join(command) else 0


class GroupingTests(unittest.TestCase):
    def test_gap_rows_group_by_the_file_that_declares_the_symbol(self):
        with tempfile.TemporaryDirectory() as directory:
            mirror = Path(directory)
            tree(mirror, {
                "packages/durable/src/harness/tool.ts": "export type ToolTaskInput = {};\nexport interface ToolTaskResult {}\n",
                "packages/ai/src/providers/compat-schema.ts": "export const Schema = 1;\n",
                "packages/ai/src/types.ts": "export interface Model {}\n",
            })
            tsv = "\n".join([
                "pkg:durable/.#ToolTaskInput\tdurable\ttype-mismatch\tdurable/x.go#ToolTaskInput",
                "pkg:durable/.#ToolTaskResult::property:output\tdurable\tchild-gap\t",
                "pkg:ai/providers/compat-schema#Schema\tai\tundecidable\t",
                "pkg:ai/.#Model::property:promptCache\tai\tundecidable\t",
                "pkg:ai/.#Gone\tai\tno-go-symbol\t",
            ])
            groups = follow.group_gaps(tsv, mirror)
        by_file = {g.file: g for g in groups}
        self.assertEqual(sorted(by_file), [
            "packages/ai/src/providers/compat-schema.ts",
            "packages/ai/src/types.ts",
            "packages/durable/src/harness/tool.ts",
            "unlocated:ai",
        ])
        self.assertEqual(len(by_file["packages/durable/src/harness/tool.ts"].rows), 2)
        self.assertEqual(by_file["unlocated:ai"].rows[0].id, "pkg:ai/.#Gone")

    def test_pending_delta_files_group_by_package(self):
        toml = '\n'.join([
            'from = "1.1.0+42a3497d"', 'to = "1.1.0+4ac0bd8c"', '',
            '[[files]]', 'path = "packages/durable/src/harness/scheduler.ts"', 'change = "modified"', 'disposition = "pending"', '',
            '[[files]]', 'path = "packages/durable/src/types.ts"', 'change = "modified"', 'disposition = "ported"', '',
            '[[files]]', 'path = "packages/codemode/src/cloudflare.ts"', 'change = "added"', 'disposition = "pending"', '',
        ])
        groups = follow.group_delta(toml)
        self.assertEqual({g.package: [f.path for f in g.files] for g in groups}, {
            "durable": ["packages/durable/src/harness/scheduler.ts"],
            "codemode": ["packages/codemode/src/cloudflare.ts"],
        })


class TaskTests(unittest.TestCase):
    def test_tasks_name_one_bounded_scope_each(self):
        gap = follow.GapGroup("packages/ai/src/types.ts", [follow.GapRow("pkg:ai/.#Model", "child-gap")])
        delta = follow.DeltaGroup("codemode", [follow.DeltaFile("packages/codemode/src/cloudflare.ts", "added")])
        tasks = follow.make_tasks([gap], [delta])
        self.assertEqual([t.task for t in tasks], ["closure packages/ai/src/types.ts", "delta codemode"])
        self.assertEqual([t.kind for t in tasks], ["closure", "delta"])


class RunCommandTests(unittest.TestCase):
    def test_a_step_never_reads_the_callers_stdin(self):
        """A Porter task's pig read the driver's task list through the inherited stdin and ended the loop after one task."""
        import os

        read_end, write_end = os.pipe()
        os.write(write_end, b"the rest of the task list\n")
        os.close(write_end)
        saved = os.dup(0)
        os.dup2(read_end, 0)
        try:
            rc = follow.run_command([sys.executable, "-c", "import sys; sys.exit(0 if sys.stdin.read() == '' else 1)"], dict(os.environ), 30, Path.cwd())
        finally:
            os.dup2(saved, 0)
            os.close(saved)
            os.close(read_end)
        self.assertEqual(rc, 0)

    def test_a_timeout_is_exit_124(self):
        self.assertEqual(follow.run_command([sys.executable, "-c", "import time; time.sleep(30)"], {}, 1, Path.cwd()), 124)


class CommitTimeTests(unittest.TestCase):
    def test_the_commit_time_is_read_from_the_source_tarball_not_the_github_api(self):
        pin = follow.pinlib.Pin(COMMIT, COMMIT, "1.1.0", "1.1.0+4ac0bd8c")
        seen = []

        def commit_date(given):
            seen.append(given)
            return "2026-10-10T15:35:12Z"
        self.assertEqual(follow.upstream_commit_time(pin, commit_date), dt.datetime(2026, 10, 10, 15, 35, 12, tzinfo=UTC))
        self.assertEqual(seen, [pin])


class BriefTests(unittest.TestCase):
    def test_a_closure_brief_lists_each_row_with_reason_and_go_target(self):
        with tempfile.TemporaryDirectory() as directory:
            mirror = Path(directory)
            tree(mirror, {"packages/ai/src/types.ts": "export interface Model {}\n"})
            tsv = "pkg:ai/.#Model\tai\tchild-gap\tai/types.go#Model\npkg:ai/.#Model::property:promptCache\tai\tundecidable\tai/types.go#Model.PromptCache\n"
            brief = follow.closure_brief("packages/ai/src/types.ts", tsv, mirror)
        self.assertEqual(brief.splitlines()[0], "closure packages/ai/src/types.ts: 2 rows")
        self.assertIn("pkg:ai/.#Model::property:promptCache\tundecidable\tai/types.go#Model.PromptCache", brief)

    def test_an_unknown_file_has_an_empty_brief_that_says_so(self):
        brief = follow.closure_brief("packages/ai/src/none.ts", "", Path("/nonexistent"))
        self.assertEqual(brief, "closure packages/ai/src/none.ts: 0 rows")

    def test_a_delta_brief_lists_the_pending_files_of_the_package(self):
        toml = 'from = "a"\nto = "b"\n\n[[files]]\npath = "packages/durable/src/harness/scheduler.ts"\nchange = "modified"\ndisposition = "pending"\n'
        brief = follow.delta_brief("durable", toml)
        self.assertEqual(brief.splitlines(), ["delta durable: 1 files", "modified\tpackages/durable/src/harness/scheduler.ts"])


class ProcessGroupTests(unittest.TestCase):
    def test_a_timed_out_command_leaves_no_descendant_running(self):
        # `make` runs go, node and npm as grandchildren; killing only make would leave them holding the heavy slot.
        with tempfile.TemporaryDirectory() as directory:
            pidfile = Path(directory) / "pid"
            rc = follow.run_command(["bash", "-c", f"sleep 60 & echo $! > {pidfile}; wait"], dict(os.environ), 1, Path(directory))
            self.assertEqual(rc, 124)
            pid = int(pidfile.read_text())
            deadline, running = time.monotonic() + 5, True
            while running and time.monotonic() < deadline:
                running = alive(pid)
                time.sleep(0.05)
            self.assertFalse(running, "the grandchild of a timed-out command must be killed")


def alive(pid: int) -> bool:
    """Whether pid still runs; a killed process awaiting its reaper (zombie or dead state) does not."""
    try:
        os.kill(pid, 0)
        state = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[0]
    except (ProcessLookupError, FileNotFoundError):
        return False
    return state not in ("Z", "X", "x")


class ChunkTests(unittest.TestCase):
    """A 12-row group ran 60 minutes, hit the task timeout with 36 files changed and wrote no report: one task is at most MAX_ROWS rows."""

    def rows(self, n):
        return [follow.GapRow(f"pkg:x/.#R{i:03}", "undecidable") for i in range(n)]

    def test_a_group_over_the_limit_is_split_into_equal_parts(self):
        tasks = follow.make_tasks([follow.GapGroup("packages/ai/src/big.ts", self.rows(74))], [])
        self.assertEqual([t.task for t in tasks], [f"closure packages/ai/src/big.ts part {i}/3" for i in (1, 2, 3)])
        self.assertEqual([t.rows for t in tasks], [25, 25, 24])

    def test_a_group_at_the_limit_stays_whole(self):
        tasks = follow.make_tasks([follow.GapGroup("packages/ai/src/ok.ts", self.rows(follow.MAX_ROWS))], [])
        self.assertEqual([t.task for t in tasks], ["closure packages/ai/src/ok.ts"])

    def test_a_part_brief_lists_exactly_its_rows(self):
        with tempfile.TemporaryDirectory() as directory:
            mirror = Path(directory)
            tree(mirror, {"packages/ai/src/big.ts": "".join(f"export const R{i:03} = 1;\n" for i in range(60))})
            tsv = "".join(f"pkg:ai/.#R{i:03}\tai\tundecidable\t\n" for i in range(60))
            parts = [follow.closure_brief("packages/ai/src/big.ts", tsv, mirror, (i, 3)) for i in (1, 2, 3)]
        ids = [line.split("\t")[0] for part in parts for line in part.splitlines()[1:]]
        self.assertEqual(ids, [f"pkg:ai/.#R{i:03}" for i in range(60)])
        self.assertEqual(parts[0].splitlines()[0], "closure packages/ai/src/big.ts part 1/3: 20 rows")


class FrozenPartsTests(unittest.TestCase):
    """Review F1 (b1d99cd14a): every closure task ends with make ledger-update, which removes the closed rows from the baseline, so a
    part slice of the CURRENT list shrinks: a 74-row group listed 25/17/10 rows and 22 rows were never listed."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.mirror = Path(self.tmp.name) / "mirror"
        tree(self.mirror, {"packages/ai/src/big.ts": "".join(f"export const R{i:03} = 1;\n" for i in range(60))})
        self.full = "".join(f"pkg:ai/.#R{i:03}\tai\tundecidable\tgo/x.go#R{i:03}\n" for i in range(60))
        self.rows_dir = Path(self.tmp.name) / "rows"

    def ids(self, brief):
        return [line.split("\t")[0] for line in brief.splitlines()[1:]]

    def test_later_parts_list_the_rows_frozen_before_any_task_closed_one(self):
        follow.freeze_rows(self.full, self.mirror, self.rows_dir)
        shrunk = "".join(f"pkg:ai/.#R{i:03}\tai\tundecidable\t\n" for i in range(20, 60))  # part 1 closed R000-R019
        parts = [follow.closure_brief("packages/ai/src/big.ts", shrunk, self.mirror, (i, 3), self.rows_dir) for i in (1, 2, 3)]
        self.assertEqual([i for p in parts for i in self.ids(p)], [f"pkg:ai/.#R{i:03}" for i in range(60)])
        self.assertIn("go/x.go#R045", parts[2], "the frozen line keeps the detector's bound symbol")

    def test_freezing_never_overwrites_a_frozen_group(self):
        follow.freeze_rows(self.full, self.mirror, self.rows_dir)
        follow.freeze_rows("", self.mirror, self.rows_dir)
        self.assertEqual(len((self.rows_dir / "packages__ai__src__big.ts.tsv").read_text().splitlines()), 60)

    def test_a_part_listed_without_a_frozen_group_freezes_the_current_rows_first(self):
        first = follow.closure_brief("packages/ai/src/big.ts", self.full, self.mirror, (1, 3), self.rows_dir)
        self.assertEqual(len(self.ids(first)), 20)
        self.assertTrue((self.rows_dir / "packages__ai__src__big.ts.tsv").exists())

    def test_without_a_part_the_brief_is_the_live_list(self):
        shrunk = "".join(f"pkg:ai/.#R{i:03}\tai\tundecidable\t\n" for i in range(50, 60))
        follow.freeze_rows(self.full, self.mirror, self.rows_dir)
        self.assertEqual(len(self.ids(follow.closure_brief("packages/ai/src/big.ts", shrunk, self.mirror, None, self.rows_dir))), 10)


class DeltaPartsTests(unittest.TestCase):
    """Review R10-2: delta durable (35 pending files) ran twice into the one-hour task limit (rc=124) with 48 files changed and no report.
    A big delta is split into part tasks, and each part lists the files frozen before any task ran (a delta task sets files to ported, which
    removes them from the pending list)."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.rows_dir = Path(self.tmp.name) / "rows"
        self.toml = 'from = "a"\nto = "b"\n\n' + "".join(
            f'[[files]]\npath = "packages/durable/src/f{i:02}.ts"\nchange = "modified"\ndisposition = "pending"\n\n' for i in range(20))

    def paths(self, brief):
        return [line.split("\t")[1] for line in brief.splitlines()[1:]]

    def test_a_delta_over_the_limit_is_split_into_part_tasks(self):
        deltas = follow.group_delta(self.toml)
        tasks = follow.make_tasks([], deltas)
        self.assertEqual([t.task for t in tasks], ["delta durable part 1/3", "delta durable part 2/3", "delta durable part 3/3"])
        self.assertEqual(sum(t.rows for t in tasks), 20)
        self.assertLessEqual(max(t.rows for t in tasks), follow.MAX_DELTA_FILES)

    def test_a_small_delta_stays_one_task(self):
        small = follow.group_delta(self.toml.split("[[files]]")[0] + "[[files]]" + "[[files]]".join(self.toml.split("[[files]]")[1:4]))
        self.assertEqual([t.task for t in follow.make_tasks([], small)], ["delta durable"])

    def test_later_parts_list_the_files_frozen_before_any_task_ported_one(self):
        follow.freeze_rows("", Path("/nonexistent"), self.rows_dir, self.toml)
        shrunk = self.toml.replace('disposition = "pending"', 'disposition = "ported"', 7)  # part 1 ported f00-f06
        parts = [follow.delta_brief("durable", shrunk, (i, 3), self.rows_dir) for i in (1, 2, 3)]
        self.assertEqual([p for brief in parts for p in self.paths(brief)], [f"packages/durable/src/f{i:02}.ts" for i in range(20)])

    def test_a_restarted_run_does_not_refreeze_a_shrunk_delta(self):
        # The runner freezes at every start (--freeze-rows); a run resumed after part 1 ported files must keep the first frozen list.
        follow.freeze_rows("", Path("/nonexistent"), self.rows_dir, self.toml)
        shrunk = self.toml.replace('disposition = "pending"', 'disposition = "ported"', 7)
        follow.freeze_rows("", Path("/nonexistent"), self.rows_dir, shrunk)
        parts = [follow.delta_brief("durable", shrunk, (i, 3), self.rows_dir) for i in (1, 2, 3)]
        self.assertEqual([p for brief in parts for p in self.paths(brief)], [f"packages/durable/src/f{i:02}.ts" for i in range(20)])

    def test_the_part_brief_without_a_frozen_file_freezes_the_current_files_first(self):
        brief = follow.delta_brief("durable", self.toml, (1, 3), self.rows_dir)
        self.assertEqual(len(self.paths(brief)), 7)
        self.assertTrue((self.rows_dir / "delta__durable.tsv").exists())


class LatencyTests(unittest.TestCase):
    def test_budget_report_names_the_two_targets(self):
        t0 = UPSTREAM_TIME
        report = follow.latency_report(t0, {"gaps": t0 + dt.timedelta(minutes=25), "porter": t0 + dt.timedelta(minutes=190)})
        self.assertEqual(report["gaps"], {"minutes": 25.0, "target": 30, "met": True})
        self.assertEqual(report["porter"], {"minutes": 190.0, "target": 120, "met": False})

    def test_missing_step_is_not_met(self):
        report = follow.latency_report(UPSTREAM_TIME, {})
        self.assertFalse(report["gaps"]["met"])
        self.assertIsNone(report["gaps"]["minutes"])


class PipelineTests(unittest.TestCase):
    def setup_root(self, directory: str) -> Path:
        root = Path(directory)
        tree(root, {
            "test/parity/interface-closure/gaps/interface-gaps-v1.1.0+4ac0bd8c.tsv": "pkg:ai/.#Model\tai\tchild-gap\tai/types.go#Model\n",
            "test/parity/upstream-sync/v1.1.0+4ac0bd8c.toml": 'from = "a"\nto = "b"\n\n[[files]]\npath = "packages/codemode/src/cloudflare.ts"\nchange = "added"\ndisposition = "pending"\n',
            ".upstream/v%s/packages/ai/src/types.ts" % MIRROR_VERSION: "export interface Model {}\n",
            "automation/pin/frozen/%s.provider-data.tgz" % COMMIT: "x",
        })
        return root

    def pipeline(self, root, runner, **kwargs):
        return follow.Pipeline(
            root=root, ref="4ac0bd8c", commit=COMMIT, version="1.1.0+4ac0bd8c", upstream_time=UPSTREAM_TIME,
            runner=runner, clock=Clock(UPSTREAM_TIME + dt.timedelta(minutes=5)), ready_log=root / "ready.log", **kwargs)

    def test_steps_run_in_order_and_are_recorded_with_times(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            runner = FakeRunner()
            result = self.pipeline(root, runner).run()
            joined = [" ".join(c) for c in runner.calls]
            order = [next(i for i, c in enumerate(joined) if marker in c) for marker in ("install-oracle", "upstream-mirror", "interface-proposals", "test-inventory-generate", "pin.py ledgers", "ledger-check")]
            self.assertEqual(order, sorted(order))
            state = json.loads((root / "tmp/pin-follow/1.1.0+4ac0bd8c/state.json").read_text())
            self.assertEqual([s["name"] for s in state["steps"]][:3], ["resolve", "oracle", "freeze"])
            self.assertTrue(all(s["rc"] == 0 and s["start"] < s["end"] for s in state["steps"]))
            self.assertTrue((root / "tmp/pin-follow/1.1.0+4ac0bd8c/tasks.tsv").exists())
            self.assertEqual(result.failed, None)

    def test_every_command_is_exported_with_the_pin_override_and_a_timeout(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            seen = []

            def runner(command, env, timeout, cwd):
                seen.append((env.get("PI_UPSTREAM_REF"), timeout))
                return 0
            self.pipeline(root, runner).run()
            self.assertTrue(seen)
            self.assertTrue(all(ref == "4ac0bd8c" and timeout and timeout > 0 for ref, timeout in seen), seen)

    def test_the_inventory_step_extracts_incrementally_and_no_other_step_does(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            seen = {}

            def runner(command, env, timeout, cwd):
                seen[" ".join(command)] = env.get("PIG_INTERFACE_EXTRACT_INCREMENTAL")
                return 0
            self.pipeline(root, runner).run()
            self.assertEqual(seen["make interface-proposals"], "1")
            self.assertEqual([c for c, v in seen.items() if v == "1"], ["make interface-proposals"])

    def test_a_failed_step_stops_the_pipeline_and_posts_a_finding(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            runner = FakeRunner(fail="interface-proposals")
            result = self.pipeline(root, runner).run()
            self.assertEqual(result.failed, "inventories")
            self.assertFalse(any("pin.py ledgers" in " ".join(c) for c in runner.calls))
            lines = (root / "ready.log").read_text().splitlines()
            self.assertEqual(len(lines), 1)
            self.assertIn("FINDING", lines[0])
            self.assertIn("inventories", lines[0])

    def test_resume_skips_steps_that_passed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            self.pipeline(root, FakeRunner(fail="interface-proposals")).run()
            runner = FakeRunner()
            self.pipeline(root, runner).run()
            joined = " ".join(" ".join(c) for c in runner.calls)
            self.assertNotIn("install-oracle", joined)
            self.assertIn("interface-proposals", joined)

    def test_the_oracle_build_freezes_missing_provider_data_and_the_pipeline_reports_it(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            frozen = root / "automation/pin/frozen" / f"{COMMIT}.provider-data.tgz"
            frozen.unlink()

            def runner(command, env, timeout, cwd):
                if "install-oracle" in " ".join(command):
                    frozen.write_text("snapshot")
                return 0
            self.pipeline(root, runner).run()
            self.assertIn("froze", (root / "ready.log").read_text())

    def test_an_oracle_that_leaves_no_frozen_data_fails_the_pipeline(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            (root / "automation/pin/frozen" / f"{COMMIT}.provider-data.tgz").unlink()
            result = self.pipeline(root, FakeRunner()).run()
            self.assertEqual(result.failed, "freeze")

    def test_the_default_ready_log_is_the_run_state_directory_or_the_named_file(self):
        # The repository does not know an operator's coordination paths: without --ready-log or PIG_PIN_FOLLOW_READY_LOG
        # the READY and FINDING lines stay with the run's state.
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            saved = os.environ.pop("PIG_PIN_FOLLOW_READY_LOG", None)
            try:
                follow.Pipeline(root=root, ref="4ac0bd8c", commit=COMMIT, version="1.1.0+4ac0bd8c", upstream_time=UPSTREAM_TIME,
                                runner=FakeRunner(), clock=Clock(UPSTREAM_TIME)).run()
                self.assertIn("READY", (root / "tmp/pin-follow/1.1.0+4ac0bd8c/ready.log").read_text())
                os.environ["PIG_PIN_FOLLOW_READY_LOG"] = str(root / "named.log")
                follow.Pipeline(root=root, ref="4ac0bd8c", commit=COMMIT, version="1.1.0+4ac0bd8c", upstream_time=UPSTREAM_TIME,
                                runner=FakeRunner(), clock=Clock(UPSTREAM_TIME)).run()
                self.assertIn("READY", (root / "named.log").read_text())
            finally:
                os.environ.pop("PIG_PIN_FOLLOW_READY_LOG", None)
                if saved is not None:
                    os.environ["PIG_PIN_FOLLOW_READY_LOG"] = saved
        self.assertNotIn("pig-lanes", Path(follow.__file__).read_text())
        doc = (Path(follow.__file__).resolve().parents[2] / "docs/parity/upstream-pin.md").read_text()
        self.assertIn("PIG_PIN_FOLLOW_READY_LOG", doc, "the pin-follow documentation names where READY and FINDING lines go")

    def test_a_finished_pipeline_writes_the_pin_record_beside_the_frozen_artifacts(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            self.pipeline(root, FakeRunner()).run()
            record = json.loads((root / "automation/pin/records/1.1.0+4ac0bd8c.json").read_text())
            self.assertEqual(record["commit"], COMMIT)
            self.assertEqual(record["version"], "1.1.0+4ac0bd8c")
            self.assertEqual(record["package_version"], "1.1.0")
            self.assertEqual(record["gap_rows"], 1)
            self.assertEqual([s["name"] for s in record["steps"]][:2], ["resolve", "oracle"])
            self.assertEqual(sorted(record["frozen"]), [f"{COMMIT}.provider-data.tgz"])
            self.assertIn("gaps", record["latency"])

    def test_a_failed_pipeline_writes_no_record(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            self.pipeline(root, FakeRunner(fail="ledger-check")).run()
            self.assertFalse((root / "automation/pin/records").exists())

    def test_tasks_tsv_names_the_frozen_rows_file_of_each_closure_task_and_the_rows_exist(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            self.pipeline(root, FakeRunner()).run()
            out = root / "tmp/pin-follow/1.1.0+4ac0bd8c"
            lines = [line.split("\t") for line in (out / "tasks.tsv").read_text().splitlines()]
            closure = next(line for line in lines if line[0] == "closure")
            self.assertEqual(closure[3], "rows/packages__ai__src__types.ts.tsv")
            self.assertTrue((out / closure[3]).exists())

    def test_until_stops_after_the_named_step_and_a_rerun_continues(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            runner = FakeRunner()
            result = self.pipeline(root, runner, until="mirror").run()
            joined = " ".join(" ".join(c) for c in runner.calls)
            self.assertIn("upstream-mirror", joined)
            self.assertNotIn("interface-proposals", joined)
            self.assertEqual(result.failed, None)
            self.assertFalse((root / "ready.log").exists(), "an early stop is not a finished pipeline")
            runner = FakeRunner()
            self.pipeline(root, runner).run()
            joined = " ".join(" ".join(c) for c in runner.calls)
            self.assertNotIn("upstream-mirror", joined)
            self.assertIn("interface-proposals", joined)

    def test_until_a_step_that_already_passed_runs_nothing_after_it(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            self.pipeline(root, FakeRunner(), until="mirror").run()
            runner = FakeRunner()
            result = self.pipeline(root, runner, until="mirror").run()
            self.assertEqual(runner.calls, [], "a rerun with the same --until must stop at the step it already passed")
            self.assertEqual(result.failed, None)
            self.assertFalse((root / "ready.log").exists())

    def test_a_dry_run_plan_stops_at_until(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            plan = self.pipeline(root, FakeRunner(), dry_run=True, until="mirror").run().plan
            self.assertEqual(plan[-1], "make upstream-mirror")

    def test_dry_run_runs_nothing_and_writes_no_state(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            runner = FakeRunner()
            result = self.pipeline(root, runner, dry_run=True).run()
            self.assertEqual(runner.calls, [])
            self.assertFalse((root / "tmp").exists())
            self.assertGreater(len(result.plan), 5)

    def test_porter_step_runs_one_task_per_group_and_ready_line_reports_budget(self):
        with tempfile.TemporaryDirectory() as directory:
            root = self.setup_root(directory)
            runner = FakeRunner()
            self.pipeline(root, runner, run_porter=True).run()
            porter = [c for c in runner.calls if "porter-task" in " ".join(c)]
            self.assertEqual(len(porter), 2)
            self.assertTrue(any("closure packages/ai/src/types.ts" in " ".join(c) for c in porter))
            ready = (root / "ready.log").read_text()
            self.assertIn("READY", ready)
            self.assertIn("pin-follow", ready)


if __name__ == "__main__":
    unittest.main()

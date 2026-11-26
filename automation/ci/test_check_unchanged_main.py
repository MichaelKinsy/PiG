# SPDX-License-Identifier: MIT
"""Unit tests for automation/ci/check-unchanged-main.py."""
from __future__ import annotations

import importlib.util
import os
import unittest
from pathlib import Path
from unittest import mock

SCRIPT = Path(__file__).resolve().parent / "check-unchanged-main.py"
SPEC = importlib.util.spec_from_file_location("check_unchanged_main", SCRIPT)
assert SPEC and SPEC.loader
MOD = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MOD)


FULL_CI_JOBS = [
    {"name": "Changed paths", "conclusion": "success"},
    {"name": "Bookkeeping", "conclusion": "success"},
    {"name": "Linux / test-fast", "conclusion": "success"},
    {"name": "Linux verification", "conclusion": "success"},
    {"name": "Windows native-1", "conclusion": "success"},
    {"name": "macOS native", "conclusion": "success"},
    {"name": "CI result", "conclusion": "success"},
]

RED_FULL_CI_JOBS = [
    {"name": "Changed paths", "conclusion": "success"},
    {"name": "Bookkeeping", "conclusion": "success"},
    {"name": "Linux / test-fast", "conclusion": "failure"},
    {"name": "Linux verification", "conclusion": "failure"},
    {"name": "Windows native-1", "conclusion": "success"},
    {"name": "macOS native", "conclusion": "success"},
    {"name": "CI result", "conclusion": "failure"},
]

SKIPPED_CI_JOBS = [
    {"name": "Changed paths", "conclusion": "success"},
    {"name": "Bookkeeping", "conclusion": "success"},
    {"name": "Linux verification", "conclusion": "success"},
    {"name": "CI result", "conclusion": "success"},
]

LIGHT_PR_JOBS = [
    {"name": "Changed paths", "conclusion": "success"},
    {"name": "Bookkeeping", "conclusion": "success"},
    {"name": "Linux / test-fast", "conclusion": "success"},
    {"name": "Linux verification", "conclusion": "success"},
    {"name": "CI result", "conclusion": "success"},
]

GREEN_DET_NIGHTLY_JOBS = [
    {"name": "Check unchanged main state", "conclusion": "success"},
    {"name": "Race / ubuntu-24.04 / ai", "conclusion": "failure"},  # stochastic job flaked
    {"name": "Oracle sweeps / full products", "conclusion": "success"},
    {"name": "Pi-Bolt compiler / build and start a compiled host", "conclusion": "success"},
    {"name": "Allocation ratchet / allocs per op", "conclusion": "success"},
    {"name": "Piglet strip matrix / API tags and all tags", "conclusion": "success"},
    {"name": "Piglet strip tests / the sets", "conclusion": "success"},
    {"name": "Piglet strip tests / pig_strip_tool_search", "conclusion": "success"},
    {"name": "Piglet strip tests / pig_strip_mcp", "conclusion": "success"},
    {"name": "PORT_MAP / portmap-1", "conclusion": "success"},
]

SKIPPED_DET_NIGHTLY_JOBS = [
    {"name": "Check unchanged main state", "conclusion": "success"},
    {"name": "Race / ubuntu-24.04 / ai", "conclusion": "success"},
    {"name": "Oracle sweeps / full products", "conclusion": "skipped"},
    {"name": "PORT_MAP / portmap-1", "conclusion": "skipped"},
]


class CheckUnchangedMainCITests(unittest.TestCase):
    def setUp(self) -> None:
        self._env_patch = mock.patch.dict(os.environ, {"PIG_SCHEDULE_NOW_UTC": "2026-11-26T06:41:00Z"})
        self._env_patch.start()

    def tearDown(self) -> None:
        self._env_patch.stop()

    def test_non_schedule_events_never_skip(self) -> None:
        for ev in ("workflow_dispatch", "pull_request", "push", "merge_group"):
            skip, _ = MOD.decide_ci(ev, "o/r", "aaa", 100, "tree1", 5)
            self.assertFalse(skip, ev)

    def test_skips_when_same_sha_green_full_and_cache_fresh(self) -> None:
        run_200 = {
            "id": 200,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-25T06:41:00Z",
        }

        def fake_api(endpoint: str):
            if "branch=main&event=schedule" in endpoint:
                return {"workflow_runs": [run_200]}
            if "branch=main&event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "workflows/ci.yml/runs?status=completed" in endpoint:
                return {"workflow_runs": [run_200]}
            if "/actions/runs/200/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertTrue(skip, reason)
        self.assertIn("same sha", reason)

    def test_skips_when_merged_pr_had_exact_same_tree_sha_and_cache_fresh(self) -> None:
        run_250 = {
            "id": 250,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "oldmain",
            "conclusion": "success",
            "created_at": "2026-11-24T06:41:00Z",
        }
        run_301 = {
            "id": 301,
            "event": "pull_request",
            "head_branch": "feat/x",
            "head_sha": "prhead222",
            "conclusion": "success",
            "created_at": "2026-11-26T01:00:00Z",
        }

        def fake_api(endpoint: str):
            if "branch=main&event=schedule" in endpoint:
                return {"workflow_runs": [run_250]}
            if "branch=main&event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "workflows/ci.yml/runs?status=completed" in endpoint:
                return {"workflow_runs": [run_301, run_250]}
            if "/actions/runs/250/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            if "/actions/runs/301/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            if "/commits/prhead222" in endpoint:
                return {"commit": {"tree": {"sha": "treeSAME"}}}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "squashed333", 999, "treeSAME", 5)
        self.assertTrue(skip, reason)
        self.assertIn("exact tree", reason)

    def test_busy_pr_days_still_find_fresh_main_schedule_cache(self) -> None:
        # Even if the general 40-run window contains only PR runs (no main schedule run),
        # the dedicated branch=main&event=schedule query finds the fresh main cache run.
        run_250 = {
            "id": 250,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "oldmain",
            "conclusion": "success",
            "created_at": "2026-11-24T06:41:00Z",
        }
        run_301 = {
            "id": 301,
            "event": "pull_request",
            "head_branch": "feat/x",
            "head_sha": "prhead222",
            "conclusion": "success",
            "created_at": "2026-11-26T01:00:00Z",
        }

        def fake_api(endpoint: str):
            if "branch=main&event=schedule" in endpoint:
                return {"workflow_runs": [run_250]}
            if "branch=main&event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "workflows/ci.yml/runs?status=completed" in endpoint:
                return {"workflow_runs": [run_301]}
            if "/actions/runs/250/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            if "/actions/runs/301/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            if "/commits/prhead222" in endpoint:
                return {"commit": {"tree": {"sha": "treeSAME"}}}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "squashed333", 999, "treeSAME", 5)
        self.assertTrue(skip, reason)
        self.assertIn("run 250", reason)

    def test_newer_red_run_stops_skip_even_with_older_green_run(self) -> None:
        fresh_main = {
            "id": 200,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-24T06:41:00Z",
        }
        newer_red = {
            "id": 310,
            "event": "workflow_dispatch",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "failure",
            "created_at": "2026-11-25T18:00:00Z",
        }

        def fake_api(endpoint: str):
            if "branch=main&event=schedule" in endpoint:
                return {"workflow_runs": [fresh_main]}
            if "branch=main&event=workflow_dispatch" in endpoint:
                return {"workflow_runs": [newer_red]}
            if "workflows/ci.yml/runs?status=completed" in endpoint:
                return {"workflow_runs": [newer_red, fresh_main]}
            if "/actions/runs/200/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            if "/actions/runs/310/jobs" in endpoint:
                return {"jobs": RED_FULL_CI_JOBS}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertFalse(skip, reason)
        self.assertIn("newest completed CI run", reason)

    def test_newer_green_run_skips_despite_older_red_run(self) -> None:
        newer_green = {
            "id": 320,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-25T18:00:00Z",
        }
        older_red = {
            "id": 200,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "failure",
            "created_at": "2026-11-24T06:41:00Z",
        }

        def fake_api(endpoint: str):
            if "branch=main&event=schedule" in endpoint:
                return {"workflow_runs": [newer_green, older_red]}
            if "branch=main&event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "workflows/ci.yml/runs?status=completed" in endpoint:
                return {"workflow_runs": [newer_green, older_red]}
            if "/actions/runs/320/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertTrue(skip, reason)
        self.assertIn("run 320", reason)

    def test_does_not_skip_on_light_pr_run_only(self) -> None:
        run_250 = {
            "id": 250,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "oldmain",
            "conclusion": "success",
            "created_at": "2026-11-24T06:41:00Z",
        }
        run_301 = {
            "id": 301,
            "event": "pull_request",
            "head_branch": "feat/x",
            "head_sha": "prhead222",
            "conclusion": "success",
            "created_at": "2026-11-26T01:00:00Z",
        }

        def fake_api(endpoint: str):
            if "branch=main&event=schedule" in endpoint:
                return {"workflow_runs": [run_250]}
            if "branch=main&event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "workflows/ci.yml/runs?status=completed" in endpoint:
                return {"workflow_runs": [run_301, run_250]}
            if "/actions/runs/250/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            if "/actions/runs/301/jobs" in endpoint:
                return {"jobs": LIGHT_PR_JOBS}
            if "/commits/prhead222" in endpoint:
                return {"commit": {"tree": {"sha": "treeSAME"}}}
            if "/commits/oldmain" in endpoint:
                return {"commit": {"tree": {"sha": "treeOLD"}}}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "squashed333", 999, "treeSAME", 5)
        self.assertFalse(skip, reason)

    def test_forces_full_run_when_cache_refresh_window_expired(self) -> None:
        run_205 = {
            "id": 205,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-25T06:41:00Z",
        }
        run_200 = {
            "id": 200,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-19T06:41:00Z",  # 7 days old (> 5d)
        }

        def fake_api(endpoint: str):
            if "branch=main&event=schedule" in endpoint:
                return {"workflow_runs": [run_205, run_200]}
            if "branch=main&event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "/actions/runs/205/jobs" in endpoint:
                # Previous day was itself a skip-unchanged run -> did NOT refresh caches
                return {"jobs": SKIPPED_CI_JOBS}
            if "/actions/runs/200/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertFalse(skip, reason)
        self.assertIn("refresh caches", reason)

    def test_api_error_fails_open(self) -> None:
        with mock.patch.object(MOD, "_gh_api_json", side_effect=RuntimeError("503")):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertFalse(skip, reason)
        self.assertIn("fail-open", reason)


    def test_newer_early_failure_stops_skip_even_with_older_full_green_run(self) -> None:
        # Newest run failed early (only pre-flight/changes ran; Windows & macOS skipped):
        # must stop the skip immediately without falling through to an older full green run.
        early_red = {
            "id": 315,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "failure",
            "created_at": "2026-11-25T18:00:00Z",
        }
        older_green = {
            "id": 200,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-24T06:41:00Z",
        }

        def fake_api(endpoint: str):
            if "event=schedule" in endpoint:
                return {"workflow_runs": [older_green]}
            if "event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "/workflows/ci.yml/runs?" in endpoint:
                return {"workflow_runs": [early_red, older_green]}
            if "/actions/runs/200/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            if "/actions/runs/315/jobs" in endpoint:
                return {
                    "jobs": [
                        {"name": "Changed paths", "conclusion": "success"},
                        {"name": "Pre-flight verdict", "conclusion": "failure"},
                    ]
                }
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertFalse(skip, reason)
        self.assertIn("failure", reason)

    def test_jobs_api_error_on_matched_run_fails_open_without_falling_through(self) -> None:
        newest = {
            "id": 316,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-25T18:00:00Z",
        }
        older_green = {
            "id": 200,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-24T06:41:00Z",
        }

        def fake_api(endpoint: str):
            if "event=schedule" in endpoint:
                return {"workflow_runs": [older_green]}
            if "event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "/workflows/ci.yml/runs?" in endpoint:
                return {"workflow_runs": [newest, older_green]}
            if "/actions/runs/316/jobs" in endpoint:
                raise RuntimeError("transient 502")
            if "/actions/runs/200/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertFalse(skip, reason)
        self.assertIn("fail-open", reason)

class CheckUnchangedMainNightlyTests(unittest.TestCase):
    def test_workflow_dispatch_always_runs_deterministic(self) -> None:
        run_det, _ = MOD.decide_nightly("workflow_dispatch", "o/r", "aaa111", 999)
        self.assertTrue(run_det)

    def test_schedule_skips_deterministic_when_already_green_on_head(self) -> None:
        def fake_api(endpoint: str):
            if "workflows/nightly.yml/runs" in endpoint:
                return {
                    "workflow_runs": [
                        {"id": 500, "event": "schedule", "head_sha": "aaa111", "conclusion": "failure"}
                    ]
                }
            if "/actions/runs/500/jobs" in endpoint:
                return {"jobs": GREEN_DET_NIGHTLY_JOBS}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            run_det, reason = MOD.decide_nightly("schedule", "o/r", "aaa111", 999)
        self.assertFalse(run_det, reason)
        self.assertIn("stochastic jobs only", reason)

    def test_schedule_requires_portmap_jobs_to_skip_deterministic(self) -> None:
        no_portmap = [j for j in GREEN_DET_NIGHTLY_JOBS if not j["name"].startswith("PORT_MAP /")]

        def fake_api(endpoint: str):
            if "workflows/nightly.yml/runs" in endpoint:
                return {
                    "workflow_runs": [
                        {"id": 500, "event": "schedule", "head_sha": "aaa111", "conclusion": "success"}
                    ]
                }
            if "/actions/runs/500/jobs" in endpoint:
                return {"jobs": no_portmap}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            run_det, reason = MOD.decide_nightly("schedule", "o/r", "aaa111", 999)
        self.assertTrue(run_det, reason)

    def test_schedule_runs_deterministic_when_portmap_failed(self) -> None:
        bad_jobs = list(GREEN_DET_NIGHTLY_JOBS)
        bad_jobs[-1] = {"name": "PORT_MAP / portmap-1", "conclusion": "failure"}

        def fake_api(endpoint: str):
            if "workflows/nightly.yml/runs" in endpoint:
                return {
                    "workflow_runs": [
                        {"id": 500, "event": "schedule", "head_sha": "aaa111", "conclusion": "failure"}
                    ]
                }
            if "/actions/runs/500/jobs" in endpoint:
                return {"jobs": bad_jobs}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            run_det, _ = MOD.decide_nightly("schedule", "o/r", "aaa111", 999)
        self.assertTrue(run_det)

    def test_newer_red_nightly_stops_skip_even_with_older_green(self) -> None:
        bad_jobs = list(GREEN_DET_NIGHTLY_JOBS)
        bad_jobs[-1] = {"name": "PORT_MAP / portmap-1", "conclusion": "failure"}

        def fake_api(endpoint: str):
            if "workflows/nightly.yml/runs" in endpoint:
                return {
                    "workflow_runs": [
                        {"id": 510, "event": "workflow_dispatch", "head_sha": "aaa111", "conclusion": "failure"},
                        {"id": 500, "event": "schedule", "head_sha": "aaa111", "conclusion": "success"},
                    ]
                }
            if "/actions/runs/510/jobs" in endpoint:
                return {"jobs": bad_jobs}
            if "/actions/runs/500/jobs" in endpoint:
                return {"jobs": GREEN_DET_NIGHTLY_JOBS}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            run_det, reason = MOD.decide_nightly("schedule", "o/r", "aaa111", 999)
        self.assertTrue(run_det, reason)
        self.assertIn("510", reason)

    def test_newer_green_nightly_skips_despite_older_red(self) -> None:
        bad_jobs = list(GREEN_DET_NIGHTLY_JOBS)
        bad_jobs[-1] = {"name": "PORT_MAP / portmap-1", "conclusion": "failure"}

        def fake_api(endpoint: str):
            if "workflows/nightly.yml/runs" in endpoint:
                return {
                    "workflow_runs": [
                        {"id": 520, "event": "schedule", "head_sha": "aaa111", "conclusion": "success"},
                        {"id": 510, "event": "schedule", "head_sha": "aaa111", "conclusion": "success"},
                        {"id": 500, "event": "schedule", "head_sha": "aaa111", "conclusion": "failure"},
                    ]
                }
            if "/actions/runs/520/jobs" in endpoint:
                # Most recent run skipped deterministic jobs; should look at newest run that actually ran them (510).
                return {"jobs": SKIPPED_DET_NIGHTLY_JOBS}
            if "/actions/runs/510/jobs" in endpoint:
                return {"jobs": GREEN_DET_NIGHTLY_JOBS}
            if "/actions/runs/500/jobs" in endpoint:
                return {"jobs": bad_jobs}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            run_det, reason = MOD.decide_nightly("schedule", "o/r", "aaa111", 999)
        self.assertFalse(run_det, reason)
        self.assertIn("510", reason)

    def test_schedule_runs_deterministic_when_head_is_new(self) -> None:
        with mock.patch.object(MOD, "_gh_api_json", return_value={"workflow_runs": []}):
            run_det, _ = MOD.decide_nightly("schedule", "o/r", "bbb222", 999)
        self.assertTrue(run_det)

    def test_api_error_fails_open(self) -> None:
        with mock.patch.object(MOD, "_gh_api_json", side_effect=RuntimeError("timeout")):
            run_det, reason = MOD.decide_nightly("schedule", "o/r", "aaa111", 999)
        self.assertTrue(run_det)
        self.assertIn("fail-open", reason)


if __name__ == "__main__":
    unittest.main()

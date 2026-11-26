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

        seen = []
        def tracking_api(endpoint: str):
            seen.append(endpoint)
            return fake_api(endpoint)

        with mock.patch.object(MOD, "_gh_api_json", side_effect=tracking_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "squashed333", 999, "treeSAME", 5)
        self.assertFalse(skip, reason)
        self.assertIn("no prior full green CI run found", reason)
        self.assertTrue(any("/actions/runs/301/jobs" in ep for ep in seen), seen)

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

    def test_unparseable_cache_timestamp_fails_open_to_run_ci(self) -> None:
        bad_run = {
            "id": 210,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "not-a-timestamp",
        }
        with mock.patch.object(MOD, "_gh_api_json", return_value={"workflow_runs": [bad_run]}):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertFalse(skip, reason)
        self.assertIn("unparseable created_at", reason)

    def test_candidate_tree_api_error_fails_open_without_falling_through(self) -> None:
        fresh_main = {
            "id": 200,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-25T06:41:00Z",
        }
        newer_pr = {
            "id": 310,
            "event": "pull_request",
            "head_branch": "feat/x",
            "head_sha": "prsha1",
            "conclusion": "failure",
            "created_at": "2026-11-25T18:00:00Z",
        }

        def fake_api(endpoint: str):
            if "event=schedule" in endpoint:
                return {"workflow_runs": [fresh_main]}
            if "event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "/workflows/ci.yml/runs?" in endpoint:
                return {"workflow_runs": [newer_pr, fresh_main]}
            if "/actions/runs/200/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            if "/commits/prsha1" in endpoint:
                raise RuntimeError("502 bad gateway")
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertFalse(skip, reason)
        self.assertIn("fail-open", reason)

    def test_tree_check_budget_exhaustion_fails_open(self) -> None:
        fresh_main = {
            "id": 200,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-25T06:41:00Z",
        }
        pr_runs = [
            {
                "id": 400 + i,
                "event": "pull_request",
                "head_branch": f"pr/{i}",
                "head_sha": f"prsha{i}",
                "conclusion": "success",
                "created_at": f"2026-11-25T18:{i:02d}:00Z",
            }
            for i in range(16)
        ]

        def fake_api(endpoint: str):
            if "event=schedule" in endpoint:
                return {"workflow_runs": [fresh_main]}
            if "event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "/workflows/ci.yml/runs?" in endpoint:
                return {"workflow_runs": pr_runs + [fresh_main]}
            if "/actions/runs/200/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            if "/commits/prsha" in endpoint:
                return {"commit": {"tree": {"sha": "treeOTHER"}}}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertFalse(skip, reason)
        self.assertIn("tree-check budget reached", reason)

    def test_attempt_scoped_jobs_endpoint_used_and_multiline_output_sanitized(self) -> None:
        run_200 = {
            "id": 200,
            "run_attempt": 2,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-25T06:41:00Z",
        }
        seen = []

        def fake_api(endpoint: str):
            seen.append(endpoint)
            if "event=schedule" in endpoint:
                return {"workflow_runs": [run_200]}
            if "event=workflow_dispatch" in endpoint:
                return {"workflow_runs": []}
            if "/workflows/ci.yml/runs?" in endpoint:
                return {"workflow_runs": [run_200]}
            if "/actions/runs/200/attempts/2/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertTrue(skip, reason)
        self.assertTrue(any("/actions/runs/200/attempts/2/jobs" in ep for ep in seen), seen)
        self.assertEqual(MOD._sanitize_output_value("line1\nline2\r\nline3"), "line1 line2 line3")

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


    def test_jobs_api_error_on_matched_nightly_run_keeps_deterministic_enabled(self) -> None:
        newer = {"id": 602, "run_attempt": 1, "event": "schedule", "head_sha": "head1", "conclusion": "failure"}
        older = {"id": 601, "run_attempt": 1, "event": "schedule", "head_sha": "head1", "conclusion": "success"}

        def fake_api(endpoint: str):
            if "workflows/nightly.yml/runs" in endpoint:
                return {"workflow_runs": [newer, older]}
            if "/actions/runs/602/attempts/1/jobs" in endpoint:
                raise RuntimeError("transient 502 on page 2")
            if "/actions/runs/601/attempts/1/jobs" in endpoint:
                return {"jobs": FULL_DET_NIGHTLY_JOBS}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            run_det, reason = MOD.decide_nightly("schedule", "o/r", "head1", 999)
        self.assertTrue(run_det, reason)
        self.assertIn("fail-open", reason)

class FailOpenInjectionTableTests(unittest.TestCase):
    """Table-driven verification that every lookup error, parse error, budget cap, unread jobs page,
    or attempt mismatch fails open to run (never returns skip)."""

    def setUp(self) -> None:
        self._now = mock.patch.object(
            MOD,
            "_now_utc",
            return_value=MOD._parse_iso("2026-11-26T06:41:00Z"),
        )
        self._now.start()

    def tearDown(self) -> None:
        self._now.stop()

    def test_ci_failure_injection_table_always_runs(self) -> None:
        fresh_main = {
            "id": 200,
            "run_attempt": 1,
            "event": "schedule",
            "head_branch": "main",
            "head_sha": "aaa111",
            "conclusion": "success",
            "created_at": "2026-11-25T06:41:00Z",
        }
        pr_run = {
            "id": 310,
            "run_attempt": 2,
            "event": "pull_request",
            "head_branch": "feat/x",
            "head_sha": "prsha1",
            "conclusion": "success",
            "created_at": "2026-11-25T18:00:00Z",
        }

        cases = [
            ("list_main_runs_error", "main_list", RuntimeError("502 main list")),
            ("cache_created_at_unparseable", "bad_ts", None),
            ("cache_jobs_api_error", "cache_jobs", RuntimeError("502 cache jobs")),
            ("list_all_runs_error", "all_list", RuntimeError("502 all list")),
            ("head_tree_api_error", "head_tree", RuntimeError("502 head tree")),
            ("candidate_tree_api_error", "cand_tree", RuntimeError("502 cand tree")),
            ("tree_budget_exhausted", "budget", None),
            ("matched_jobs_api_error", "matched_jobs", RuntimeError("502 matched jobs")),
            ("matched_jobs_unread_page_total_count", "unread_total", None),
            ("matched_jobs_unread_page_cap", "unread_cap", None),
            ("matched_jobs_attempt_mismatch", "attempt_mismatch", None),
        ]

        for name, fault, exc in cases:
            with self.subTest(fault=name):
                def fake_api(endpoint: str, _fault=fault, _exc=exc):
                    if "event=schedule" in endpoint:
                        if _fault == "main_list":
                            raise _exc
                        if _fault == "bad_ts":
                            return {"workflow_runs": [{**fresh_main, "created_at": "bad-date"}]}
                        return {"workflow_runs": [fresh_main]}
                    if "event=workflow_dispatch" in endpoint:
                        return {"workflow_runs": []}
                    if "/actions/runs/200/" in endpoint:
                        if _fault == "cache_jobs":
                            raise _exc
                        return {"jobs": FULL_CI_JOBS, "total_count": len(FULL_CI_JOBS)}
                    if "/workflows/ci.yml/runs?status=completed" in endpoint:
                        if _fault == "all_list":
                            raise _exc
                        if _fault == "budget":
                            many = [
                                {**pr_run, "id": 400 + i, "head_sha": f"other{i}"}
                                for i in range(MOD.MAX_TREE_LOOKUPS + 1)
                            ]
                            return {"workflow_runs": many + [fresh_main]}
                        return {"workflow_runs": [pr_run, fresh_main]}
                    if "/commits/aaa111" in endpoint:
                        if _fault == "head_tree":
                            raise _exc
                        return {"commit": {"tree": {"sha": "treeAAA"}}}
                    if "/commits/prsha1" in endpoint:
                        if _fault == "cand_tree":
                            raise _exc
                        return {"commit": {"tree": {"sha": "treeAAA"}}}
                    if "/commits/other" in endpoint:
                        return {"commit": {"tree": {"sha": "treeOTHER"}}}
                    if "/actions/runs/310/" in endpoint:
                        if _fault == "matched_jobs":
                            raise _exc
                        if _fault == "unread_total":
                            return {"jobs": FULL_CI_JOBS, "total_count": len(FULL_CI_JOBS) + 5}
                        if _fault == "unread_cap":
                            return {"jobs": [{"name": f"Linux / s{i}", "conclusion": "success"} for i in range(100)]}
                        if _fault == "attempt_mismatch":
                            return {"jobs": [{**j, "run_attempt": 3} for j in FULL_CI_JOBS]}
                        return {"jobs": FULL_CI_JOBS, "total_count": len(FULL_CI_JOBS)}
                    raise AssertionError(f"unexpected endpoint: {endpoint}")

                head_tree = "" if fault == "head_tree" else "treeAAA"
                with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
                    skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, head_tree, 5)
                self.assertFalse(skip, f"{name}: unexpectedly skipped: {reason}")

    def test_nightly_failure_injection_table_always_runs(self) -> None:
        newer = {"id": 602, "run_attempt": 2, "event": "schedule", "head_sha": "head1", "conclusion": "success"}
        older = {"id": 601, "run_attempt": 1, "event": "schedule", "head_sha": "head1", "conclusion": "success"}
        cases = [
            "list_runs_error",
            "page1_error",
            "page2_error",
            "unread_total_count",
            "unread_page_cap",
            "attempt_mismatch",
        ]
        for fault in cases:
            with self.subTest(fault=fault):
                def fake_api(endpoint: str, _fault=fault):
                    if "workflows/nightly.yml/runs" in endpoint:
                        if _fault == "list_runs_error":
                            raise RuntimeError("502 list")
                        return {"workflow_runs": [newer, older]}
                    if "/actions/runs/602/attempts/2/jobs" in endpoint:
                        if _fault == "page1_error":
                            raise RuntimeError("502 page 1")
                        if _fault == "page2_error":
                            if "page=2" in endpoint:
                                raise RuntimeError("502 page 2")
                            return {"jobs": [{"name": f"Race {i}", "conclusion": "success"} for i in range(100)]}
                        if _fault == "unread_total_count":
                            return {"jobs": FULL_DET_NIGHTLY_JOBS, "total_count": len(FULL_DET_NIGHTLY_JOBS) + 10}
                        if _fault == "unread_page_cap":
                            return {"jobs": [{"name": f"Race {i}", "conclusion": "success"} for i in range(100)]}
                        if _fault == "attempt_mismatch":
                            return {"jobs": [{**j, "run_attempt": 1} for j in FULL_DET_NIGHTLY_JOBS]}
                    if "/actions/runs/601/" in endpoint:
                        return {"jobs": FULL_DET_NIGHTLY_JOBS, "total_count": len(FULL_DET_NIGHTLY_JOBS)}
                    raise AssertionError(f"unexpected endpoint: {endpoint}")

                with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
                    run_det, reason = MOD.decide_nightly("schedule", "o/r", "head1", 999)
                self.assertTrue(run_det, f"{fault}: unexpectedly skipped deterministic jobs: {reason}")

if __name__ == "__main__":
    unittest.main()

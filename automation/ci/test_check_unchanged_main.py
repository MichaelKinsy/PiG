# SPDX-License-Identifier: MIT
"""Unit tests for automation/ci/check-unchanged-main.py."""
from __future__ import annotations

import importlib.util
import os
import sys
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
        def fake_api(endpoint: str):
            if "workflows/ci.yml/runs" in endpoint:
                return {
                    "workflow_runs": [
                        {
                            "id": 200,
                            "event": "schedule",
                            "head_branch": "main",
                            "head_sha": "aaa111",
                            "conclusion": "success",
                            "created_at": "2026-11-25T06:41:00Z",
                        }
                    ]
                }
            if "/actions/runs/200/jobs" in endpoint:
                return {"jobs": FULL_CI_JOBS}
            raise AssertionError(f"unexpected endpoint: {endpoint}")

        with mock.patch.object(MOD, "_gh_api_json", side_effect=fake_api):
            skip, reason = MOD.decide_ci("schedule", "o/r", "aaa111", 999, "treeAAA", 5)
        self.assertTrue(skip, reason)
        self.assertIn("same sha", reason)

    def test_skips_when_merged_pr_had_exact_same_tree_sha_and_cache_fresh(self) -> None:
        def fake_api(endpoint: str):
            if "workflows/ci.yml/runs" in endpoint:
                return {
                    "workflow_runs": [
                        {
                            "id": 301,
                            "event": "pull_request",
                            "head_branch": "feat/x",
                            "head_sha": "prhead222",
                            "conclusion": "success",
                            "created_at": "2026-11-26T01:00:00Z",
                        },
                        {
                            "id": 250,
                            "event": "schedule",
                            "head_branch": "main",
                            "head_sha": "oldmain",
                            "conclusion": "success",
                            "created_at": "2026-11-24T06:41:00Z",
                        },
                    ]
                }
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

    def test_does_not_skip_on_light_pr_run_only(self) -> None:
        def fake_api(endpoint: str):
            if "workflows/ci.yml/runs" in endpoint:
                return {
                    "workflow_runs": [
                        {
                            "id": 301,
                            "event": "pull_request",
                            "head_branch": "feat/x",
                            "head_sha": "prhead222",
                            "conclusion": "success",
                            "created_at": "2026-11-26T01:00:00Z",
                        },
                        {
                            "id": 250,
                            "event": "schedule",
                            "head_branch": "main",
                            "head_sha": "oldmain",
                            "conclusion": "success",
                            "created_at": "2026-11-24T06:41:00Z",
                        },
                    ]
                }
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
        def fake_api(endpoint: str):
            if "workflows/ci.yml/runs" in endpoint:
                return {
                    "workflow_runs": [
                        {
                            "id": 205,
                            "event": "schedule",
                            "head_branch": "main",
                            "head_sha": "aaa111",
                            "conclusion": "success",
                            "created_at": "2026-11-25T06:41:00Z",
                        },
                        {
                            "id": 200,
                            "event": "schedule",
                            "head_branch": "main",
                            "head_sha": "aaa111",
                            "conclusion": "success",
                            "created_at": "2026-11-19T06:41:00Z",  # 7 days old (> 5d)
                        },
                    ]
                }
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

    def test_schedule_runs_deterministic_when_any_deterministic_job_failed(self) -> None:
        bad_jobs = list(GREEN_DET_NIGHTLY_JOBS)
        bad_jobs[-1] = {"name": "Piglet strip tests / pig_strip_mcp", "conclusion": "failure"}

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

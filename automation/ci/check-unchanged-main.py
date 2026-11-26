#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Decide whether a scheduled workflow run on an unchanged main commit can skip redundant work.

Used ONLY on `schedule` events (never `workflow_dispatch`, `pull_request`, `push`, or `merge_group`).
Fails open (runs everything) on any API error, unexpected response, or missing input so a transient
GitHub API failure never silently skips verification.

Subcommands:
  ci      Check whether `ci.yml` already has a completed `success` full run for HEAD_SHA or for a
          commit with the exact same git tree SHA (e.g. a squash-merged PR head), AND whether the
          last full scheduled CI run on `main` is fresh enough (within --cache-refresh-days, default 5)
          to keep GitHub Actions caches from expiring (7-day LRU TTL).
          Writes `skip-unchanged=true|false` and `skip-reason=...` to GITHUB_OUTPUT (or stdout).

  nightly Check whether `nightly.yml` already has a completed `success` run on `main` for HEAD_SHA
          whose deterministic jobs (strip-matrix, strip-tests, allocations, oracle-sweeps,
          pig-bolt-compiler) all passed. Stochastic/repetition jobs (race, fuzz,
          windows-concurrency-stress) always run on schedule even when main is unchanged.
          Writes `run-deterministic=true|false` and `skip-reason=...` to GITHUB_OUTPUT (or stdout).
"""
from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import subprocess
import sys
from typing import Any


DETERMINISTIC_NIGHTLY_JOB_PREFIXES = (
    "Oracle sweeps /",
    "Pi-Bolt compiler /",
    "Allocation ratchet /",
    "Piglet strip matrix /",
    "Piglet strip tests /",
)

DETERMINISTIC_NIGHTLY_REQUIRED_PREFIXES = (
    "Oracle sweeps /",
    "Pi-Bolt compiler /",
    "Allocation ratchet /",
    "Piglet strip matrix /",
    "Piglet strip tests / the sets",
    "Piglet strip tests / pig_strip_",
)


def _gh_api_json(endpoint: str) -> Any:
    cmd = ["gh", "api", endpoint]
    proc = subprocess.run(cmd, capture_output=True, text=True, check=False)
    if proc.returncode != 0:
        raise RuntimeError(f"gh api {endpoint} failed ({proc.returncode}): {proc.stderr.strip()}")
    return json.loads(proc.stdout)


def _parse_iso(ts: str) -> dt.datetime:
    if ts.endswith("Z"):
        ts = ts[:-1] + "+00:00"
    parsed = dt.datetime.fromisoformat(ts)
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=dt.timezone.utc)
    return parsed


def _now_utc() -> dt.datetime:
    override = os.environ.get("PIG_SCHEDULE_NOW_UTC", "").strip()
    if override:
        return _parse_iso(override)
    return dt.datetime.now(dt.timezone.utc)


def _commit_tree_sha(repo: str, sha: str, cache: dict[str, str]) -> str:
    if sha in cache:
        return cache[sha]
    data = _gh_api_json(f"repos/{repo}/commits/{sha}")
    tree_sha = str(data.get("commit", {}).get("tree", {}).get("sha", ""))
    if tree_sha:
        cache[sha] = tree_sha
    return tree_sha


def _write_outputs(outputs: dict[str, str]) -> None:
    gh_out = os.environ.get("GITHUB_OUTPUT", "").strip()
    lines = [f"{k}={v}" for k, v in outputs.items()]
    if gh_out:
        with open(gh_out, "a", encoding="utf-8") as fh:
            for line in lines:
                fh.write(line + "\n")
    for line in lines:
        print(line)


def decide_ci(
    event: str,
    repo: str,
    head_sha: str,
    current_run_id: int,
    head_tree_sha: str,
    cache_refresh_days: int,
) -> tuple[bool, str]:
    if event != "schedule":
        return False, f"event={event} (only schedule may skip)"
    if not repo or not head_sha:
        return False, "missing repo or head_sha"

    now = _now_utc()
    max_cache_age = dt.timedelta(days=cache_refresh_days)
    tree_cache: dict[str, str] = {}
    if head_tree_sha:
        tree_cache[head_sha] = head_tree_sha

    try:
        runs_payload = _gh_api_json(
            f"repos/{repo}/actions/workflows/ci.yml/runs?status=completed&per_page=40"
        )
        runs = runs_payload.get("workflow_runs") or []
    except Exception as exc:
        return False, f"api error listing ci.yml runs (fail-open): {exc}"

    # 1. Keep caches alive: find the most recent green full run on main that actually saved caches
    # (schedule or workflow_dispatch on main, where jobs were not skipped via skip-unchanged).
    # We check that a recent green schedule/workflow_dispatch run on main happened within cache_refresh_days
    # and ran the full suite (checked via its job count / Linux verification job not skipped).
    fresh_cache_run_id: int | None = None
    for run in runs:
        run_id = int(run.get("id") or 0)
        if run_id == current_run_id:
            continue
        if run.get("conclusion") != "success":
            continue
        if run.get("head_branch") != "main":
            continue
        if run.get("event") not in ("schedule", "workflow_dispatch"):
            continue
        created_raw = run.get("created_at") or run.get("run_started_at") or ""
        if not created_raw:
            continue
        try:
            age = now - _parse_iso(created_raw)
        except Exception:
            continue
        if age > max_cache_age:
            continue
        # Verify this run was a real full run (not an unchanged-main skip) so its CACHE_SAVE steps ran.
        try:
            jobs_payload = _gh_api_json(f"repos/{repo}/actions/runs/{run_id}/jobs?per_page=100")
            jobs = jobs_payload.get("jobs") or []
        except Exception:
            continue
        if any(j.get("name") == "Linux verification" and j.get("conclusion") == "success" for j in jobs):
            # Also confirm at least one actual Linux shard ran (not skipped).
            if any(
                str(j.get("name", "")).startswith("Linux / ") and j.get("conclusion") == "success"
                for j in jobs
            ):
                fresh_cache_run_id = run_id
                break

    if fresh_cache_run_id is None:
        return (
            False,
            f"no full green main CI run within {cache_refresh_days}d; running to refresh caches and catch drift",
        )

    # 2. Check whether head_sha ALREADY has a completed green full CI run (on main or matching exact tree SHA).
    try:
        target_tree = _commit_tree_sha(repo, head_sha, tree_cache)
    except Exception as exc:
        return False, f"api error reading commit tree for {head_sha} (fail-open): {exc}"
    if not target_tree:
        return False, f"empty tree SHA for {head_sha}"

    checked_trees = 0
    for run in runs:
        run_id = int(run.get("id") or 0)
        if run_id == current_run_id:
            continue
        if run.get("conclusion") != "success":
            continue
        run_event = str(run.get("event") or "")
        run_sha = str(run.get("head_sha") or "")
        if not run_sha:
            continue

        matched_how = ""
        if run_sha == head_sha and run_event in ("schedule", "workflow_dispatch", "push", "merge_group"):
            matched_how = f"same sha ({run_event} run {run_id})"
        elif run_event in ("pull_request", "merge_group", "workflow_dispatch", "schedule"):
            if checked_trees >= 15:
                continue
            checked_trees += 1
            try:
                cand_tree = _commit_tree_sha(repo, run_sha, tree_cache)
            except Exception:
                continue
            if cand_tree == target_tree:
                matched_how = f"exact tree {target_tree[:12]} ({run_event} run {run_id} on {run_sha[:10]})"

        if not matched_how:
            continue

        # Confirm the matching run actually ran a full CI pass (not a light PR run, docs-only run,
        # or a previous unchanged-main skip): Windows + macOS + Linux verification all succeeded.
        try:
            jobs_payload = _gh_api_json(f"repos/{repo}/actions/runs/{run_id}/jobs?per_page=100")
            jobs = jobs_payload.get("jobs") or []
        except Exception:
            continue
        has_linux = any(
            str(j.get("name", "")).startswith("Linux / ") and j.get("conclusion") == "success"
            for j in jobs
        )
        has_windows = any(
            str(j.get("name", "")).startswith("Windows ") and j.get("conclusion") == "success"
            for j in jobs
        )
        has_macos = any(
            str(j.get("name", "")).startswith("macOS ") and j.get("conclusion") == "success"
            for j in jobs
        )
        has_result = any(
            j.get("name") == "CI result" and j.get("conclusion") == "success"
            for j in jobs
        )
        if has_linux and has_windows and has_macos and has_result:
            return (
                True,
                f"unchanged main already green via {matched_how}; caches fresh from run {fresh_cache_run_id}",
            )

    return False, f"no prior full green CI run found for {head_sha[:10]} (tree {target_tree[:12]})"


def decide_nightly(
    event: str,
    repo: str,
    head_sha: str,
    current_run_id: int,
) -> tuple[bool, str]:
    """Return (run_deterministic, reason). True means run deterministic jobs; False means skip them."""
    if event != "schedule":
        return True, f"event={event} (only schedule may skip deterministic nightly jobs)"
    if not repo or not head_sha:
        return True, "missing repo or head_sha"

    try:
        runs_payload = _gh_api_json(
            f"repos/{repo}/actions/workflows/nightly.yml/runs?branch=main&head_sha={head_sha}&status=completed&per_page=20"
        )
        runs = runs_payload.get("workflow_runs") or []
    except Exception as exc:
        return True, f"api error listing nightly.yml runs (fail-open): {exc}"

    for run in runs:
        run_id = int(run.get("id") or 0)
        if run_id == current_run_id:
            continue
        if run.get("head_sha") != head_sha:
            continue
        if run.get("event") not in ("schedule", "workflow_dispatch"):
            continue
        # Inspect jobs: even if a stochastic job (race/fuzz/windows-stress) flaked in an earlier
        # run on this exact head_sha, check whether every deterministic job already ran and succeeded.
        jobs: list[dict[str, Any]] = []
        try:
            page = 1
            while page <= 3:
                payload = _gh_api_json(
                    f"repos/{repo}/actions/runs/{run_id}/jobs?per_page=100&page={page}"
                )
                batch = payload.get("jobs") or []
                jobs.extend(batch)
                if len(batch) < 100:
                    break
                page += 1
        except Exception:
            continue

        det_jobs = [
            j
            for j in jobs
            if any(str(j.get("name", "")).startswith(p) for p in DETERMINISTIC_NIGHTLY_JOB_PREFIXES)
        ]
        if not det_jobs:
            continue
        if not all(j.get("conclusion") == "success" for j in det_jobs):
            continue
        if all(
            any(str(j.get("name", "")).startswith(req) for j in det_jobs)
            for req in DETERMINISTIC_NIGHTLY_REQUIRED_PREFIXES
        ):
            return (
                False,
                f"deterministic nightly jobs ({len(det_jobs)}) already succeeded on {head_sha[:10]} in run {run_id}; running stochastic jobs only",
            )

    return True, f"no prior green deterministic nightly run on {head_sha[:10]}"


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="mode", required=True)

    ci_p = sub.add_parser("ci")
    ci_p.add_argument("--event", required=True)
    ci_p.add_argument("--repo", required=True)
    ci_p.add_argument("--head-sha", required=True)
    ci_p.add_argument("--current-run-id", type=int, default=0)
    ci_p.add_argument("--head-tree-sha", default="")
    ci_p.add_argument("--cache-refresh-days", type=int, default=5)

    n_p = sub.add_parser("nightly")
    n_p.add_argument("--event", required=True)
    n_p.add_argument("--repo", required=True)
    n_p.add_argument("--head-sha", required=True)
    n_p.add_argument("--current-run-id", type=int, default=0)

    args = parser.parse_args(argv)
    if args.mode == "ci":
        skip, reason = decide_ci(
            event=args.event,
            repo=args.repo,
            head_sha=args.head_sha,
            current_run_id=args.current_run_id,
            head_tree_sha=args.head_tree_sha,
            cache_refresh_days=args.cache_refresh_days,
        )
        _write_outputs(
            {
                "skip-unchanged": "true" if skip else "false",
                "skip-reason": reason,
            }
        )
        return 0

    run_det, reason = decide_nightly(
        event=args.event,
        repo=args.repo,
        head_sha=args.head_sha,
        current_run_id=args.current_run_id,
    )
    _write_outputs(
        {
            "run-deterministic": "true" if run_det else "false",
            "skip-reason": reason,
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

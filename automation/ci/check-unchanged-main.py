#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Check whether a scheduled main run can skip redundant work on an unchanged HEAD.

Two subcommands:
  ci      - For .github/workflows/ci.yml on `schedule`:
            Skip only when BOTH:
              1. The newest completed CI run for `head_sha` (or matching exact git tree SHA
                 via GitHub Commits API) is a full green CI run (Linux + Windows + macOS +
                 CI result). An earlier failure, API error, unparseable value, exhausted
                 lookup budget, unread jobs page, or attempt mismatch always fails open
                 (runs CI).
              2. A full green main CI run (schedule or workflow_dispatch) refreshed caches
                 within `--cache-refresh-days` (default 5, safely inside GitHub Actions'
                 7-day LRU cache eviction window).

  nightly - For .github/workflows/nightly.yml on `schedule`:
            Determine whether deterministic jobs (oracle-sweeps, pig-bolt-compiler,
            allocations, strip-matrix, strip-tests, portmap) already succeeded on `head_sha`
            in the newest completed nightly run that executed deterministic jobs. Stochastic
            jobs (race, fuzz, windows-concurrency-stress) ALWAYS run regardless.

Non-schedule events (workflow_dispatch, pull_request, push, merge_group) NEVER skip.
Structurally fail-open: every error, unparseable value, exhausted budget, unread page, or
attempt mismatch raises `_MustRun` so `decide_ci` has exactly one `skip=True` return and
`decide_nightly` has exactly one `run_deterministic=False` return.
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
    "PORT_MAP /",
)

DETERMINISTIC_NIGHTLY_REQUIRED_PREFIXES = (
    "Oracle sweeps /",
    "Pi-Bolt compiler /",
    "Allocation ratchet /",
    "Piglet strip matrix /",
    "Piglet strip tests / the sets",
    "Piglet strip tests / pig_strip_",
    "PORT_MAP /",
)

MAX_TREE_LOOKUPS = 15
MAX_JOB_PAGES = 5


class _MustRun(Exception):
    """Raised whenever a check cannot prove a safe skip; caller returns run."""


def _now_utc() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def _parse_iso(ts: str) -> dt.datetime:
    if not ts or not isinstance(ts, str):
        raise ValueError("empty timestamp")
    return dt.datetime.fromisoformat(ts.replace("Z", "+00:00"))


def _gh_api_json(endpoint: str) -> Any:
    proc = subprocess.run(
        ["gh", "api", endpoint],
        check=True,
        capture_output=True,
        text=True,
        timeout=30,
    )
    return json.loads(proc.stdout)


def _commit_tree_sha(repo: str, sha: str, cache: dict[str, str]) -> str:
    if sha in cache:
        return cache[sha]
    data = _gh_api_json(f"repos/{repo}/commits/{sha}")
    tree_sha = str((data.get("commit") or {}).get("tree", {}).get("sha") or "")
    if not tree_sha:
        raise ValueError(f"empty tree SHA for commit {sha}")
    cache[sha] = tree_sha
    return tree_sha


def _sanitize_output_value(value: str) -> str:
    return " ".join(str(value).splitlines()).strip()


def _write_outputs(outputs: dict[str, str]) -> None:
    gh_out = os.environ.get("GITHUB_OUTPUT", "").strip()
    lines = [f"{k}={_sanitize_output_value(v)}" for k, v in outputs.items()]
    if gh_out:
        with open(gh_out, "a", encoding="utf-8") as fh:
            for line in lines:
                fh.write(line + "\n")
    for line in lines:
        print(line)


def _run_jobs_endpoint(repo: str, run: dict[str, Any], *, page: int = 1) -> str:
    run_id = int(run.get("id") or 0)
    attempt = run.get("run_attempt")
    if isinstance(attempt, int) and attempt > 0:
        base = f"repos/{repo}/actions/runs/{run_id}/attempts/{attempt}/jobs?per_page=100"
    else:
        base = f"repos/{repo}/actions/runs/{run_id}/jobs?per_page=100"
    return f"{base}&page={page}"


def _fetch_all_run_jobs(repo: str, run: dict[str, Any]) -> list[dict[str, Any]]:
    """Fetch every job page for run's listed attempt; raise _MustRun on any API error, unread page, or attempt mismatch."""
    run_id = int(run.get("id") or 0)
    expected_attempt = run.get("run_attempt")
    jobs: list[dict[str, Any]] = []
    total_count: int | None = None
    for page in range(1, MAX_JOB_PAGES + 1):
        try:
            payload = _gh_api_json(_run_jobs_endpoint(repo, run, page=page))
        except Exception as exc:
            raise _MustRun(f"api error reading jobs for run {run_id} page {page} (fail-open): {exc}") from exc
        if not isinstance(payload, dict):
            raise _MustRun(f"invalid jobs payload for run {run_id} page {page} (fail-open)")
        if total_count is None and payload.get("total_count") is not None:
            try:
                total_count = int(payload["total_count"])
            except Exception as exc:
                raise _MustRun(f"invalid total_count for run {run_id} (fail-open): {exc}") from exc
        batch = payload.get("jobs")
        if batch is None:
            batch = []
        if not isinstance(batch, list):
            raise _MustRun(f"invalid jobs list for run {run_id} page {page} (fail-open)")
        for job in batch:
            if isinstance(expected_attempt, int) and expected_attempt > 0:
                job_attempt = job.get("run_attempt")
                if job_attempt is not None and job_attempt != expected_attempt:
                    raise _MustRun(
                        f"attempt mismatch on run {run_id}: expected {expected_attempt}, got {job_attempt} (fail-open)"
                    )
        jobs.extend(batch)
        if len(batch) < 100:
            break
    else:
        raise _MustRun(f"unread jobs pages past page {MAX_JOB_PAGES} for run {run_id} (fail-open)")

    if total_count is not None and len(jobs) < total_count:
        raise _MustRun(
            f"unread jobs for run {run_id}: fetched {len(jobs)} of {total_count} (fail-open)"
        )
    return jobs


def _is_full_ci_run_jobs(jobs: list[dict[str, Any]]) -> bool:
    """Return True when jobs represent an actual full CI run (Linux + Windows + macOS + CI result)."""
    ran_conclusions = {"success", "failure", "timed_out"}
    has_linux = any(
        str(j.get("name", "")).startswith("Linux / ") and j.get("conclusion") in ran_conclusions
        for j in jobs
    )
    has_windows = any(
        str(j.get("name", "")).startswith("Windows ") and j.get("conclusion") in ran_conclusions
        for j in jobs
    )
    has_macos = any(
        str(j.get("name", "")).startswith("macOS ") and j.get("conclusion") in ran_conclusions
        for j in jobs
    )
    has_result = any(
        j.get("name") == "CI result" and j.get("conclusion") in ran_conclusions
        for j in jobs
    )
    return has_linux and has_windows and has_macos and has_result


def _is_green_full_ci_run_jobs(jobs: list[dict[str, Any]]) -> bool:
    """Return True when jobs prove a full green CI pass (Linux + Windows + macOS + CI result)."""
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
    return has_linux and has_windows and has_macos and has_result


def _prove_ci_skip(
    event: str,
    repo: str,
    head_sha: str,
    current_run_id: int,
    head_tree_sha: str,
    cache_refresh_days: int,
) -> str:
    """Return skip reason ONLY when every lookup succeeds and proves a safe skip; otherwise raise _MustRun."""
    if event != "schedule":
        raise _MustRun(f"event={event} (only schedule may skip)")
    if not repo or not head_sha:
        raise _MustRun("missing repo or head_sha")

    now = _now_utc()
    max_cache_age = dt.timedelta(days=cache_refresh_days)
    tree_cache: dict[str, str] = {}
    if head_tree_sha:
        tree_cache[head_sha] = head_tree_sha

    main_runs_by_id: dict[int, dict[str, Any]] = {}
    for main_event in ("schedule", "workflow_dispatch"):
        try:
            payload = _gh_api_json(
                f"repos/{repo}/actions/workflows/ci.yml/runs?branch=main&event={main_event}&status=completed&per_page=20"
            )
        except Exception as exc:
            raise _MustRun(f"api error listing main ci.yml runs (fail-open): {exc}") from exc
        for run in payload.get("workflow_runs") or []:
            rid = int(run.get("id") or 0)
            if rid:
                main_runs_by_id[rid] = run

    main_runs = sorted(
        main_runs_by_id.values(),
        key=lambda r: str(r.get("created_at") or r.get("run_started_at") or ""),
        reverse=True,
    )

    fresh_cache_run_id: int | None = None
    for run in main_runs:
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
        try:
            age = now - _parse_iso(str(created_raw))
        except Exception as exc:
            raise _MustRun(
                f"unparseable created_at {created_raw!r} on main CI run {run_id} (fail-open): {exc}"
            ) from exc
        if age > max_cache_age:
            continue
        jobs = _fetch_all_run_jobs(repo, run)
        if any(j.get("name") == "Linux verification" and j.get("conclusion") == "success" for j in jobs) and any(
            str(j.get("name", "")).startswith("Linux / ") and j.get("conclusion") == "success"
            for j in jobs
        ):
            fresh_cache_run_id = run_id
            break

    if fresh_cache_run_id is None:
        raise _MustRun(
            f"no full green main CI run within {cache_refresh_days}d; running to refresh caches and catch drift"
        )

    try:
        runs_payload = _gh_api_json(
            f"repos/{repo}/actions/workflows/ci.yml/runs?status=completed&per_page=40"
        )
        runs = runs_payload.get("workflow_runs") or []
    except Exception as exc:
        raise _MustRun(f"api error listing ci.yml runs (fail-open): {exc}") from exc

    try:
        target_tree = _commit_tree_sha(repo, head_sha, tree_cache)
    except Exception as exc:
        raise _MustRun(f"api error reading commit tree for {head_sha} (fail-open): {exc}") from exc

    checked_trees = 0
    for run in runs:
        run_id = int(run.get("id") or 0)
        if run_id == current_run_id:
            continue
        conclusion = str(run.get("conclusion") or "")
        if conclusion not in ("success", "failure", "timed_out"):
            continue
        run_event = str(run.get("event") or "")
        run_sha = str(run.get("head_sha") or "")
        if not run_sha:
            raise _MustRun(f"missing head_sha on completed CI run {run_id} (fail-open)")

        matched_how = ""
        if run_sha == head_sha and run_event in ("schedule", "workflow_dispatch", "push", "merge_group"):
            matched_how = f"same sha ({run_event} run {run_id})"
        elif run_event in ("pull_request", "merge_group", "workflow_dispatch", "schedule"):
            if checked_trees >= MAX_TREE_LOOKUPS:
                raise _MustRun(
                    f"tree-check budget reached ({MAX_TREE_LOOKUPS}) before classifying newer candidate run {run_id} (fail-open); re-running"
                )
            checked_trees += 1
            try:
                cand_tree = _commit_tree_sha(repo, run_sha, tree_cache)
            except Exception as exc:
                raise _MustRun(
                    f"api error reading commit tree for candidate run {run_id} ({run_sha[:10]}) (fail-open): {exc}"
                ) from exc
            if cand_tree == target_tree:
                matched_how = f"exact tree {target_tree[:12]} ({run_event} run {run_id} on {run_sha[:10]})"

        if not matched_how:
            continue

        if conclusion in ("failure", "timed_out"):
            raise _MustRun(
                f"newest completed CI run on same content was {conclusion} via {matched_how}; re-running"
            )

        jobs = _fetch_all_run_jobs(repo, run)
        if not _is_full_ci_run_jobs(jobs):
            continue

        if _is_green_full_ci_run_jobs(jobs):
            return f"unchanged main already green via {matched_how}; caches fresh from run {fresh_cache_run_id}"
        raise _MustRun(
            f"newest completed full CI run on same content had non-green jobs via {matched_how}; re-running"
        )

    raise _MustRun(f"no prior full green CI run found for {head_sha[:10]} (tree {target_tree[:12]})")


def decide_ci(
    event: str,
    repo: str,
    head_sha: str,
    current_run_id: int,
    head_tree_sha: str,
    cache_refresh_days: int,
) -> tuple[bool, str]:
    """Return (skip, reason). Default is always False (run); skip=True is returned in exactly one place."""
    try:
        reason = _prove_ci_skip(event, repo, head_sha, current_run_id, head_tree_sha, cache_refresh_days)
        return True, reason
    except _MustRun as exc:
        return False, str(exc)
    except Exception as exc:
        return False, f"unexpected error deciding ci skip (fail-open): {exc}"


def _prove_nightly_skip(
    event: str,
    repo: str,
    head_sha: str,
    current_run_id: int,
) -> str:
    """Return skip reason ONLY when every lookup succeeds and proves deterministic nightly jobs passed; else raise _MustRun."""
    if event != "schedule":
        raise _MustRun(f"event={event} (only schedule may skip deterministic nightly jobs)")
    if not repo or not head_sha:
        raise _MustRun("missing repo or head_sha")

    try:
        runs_payload = _gh_api_json(
            f"repos/{repo}/actions/workflows/nightly.yml/runs?branch=main&head_sha={head_sha}&status=completed&per_page=20"
        )
        runs = runs_payload.get("workflow_runs") or []
    except Exception as exc:
        raise _MustRun(f"api error listing nightly.yml runs (fail-open): {exc}") from exc

    for run in runs:
        run_id = int(run.get("id") or 0)
        if run_id == current_run_id:
            continue
        if run.get("head_sha") != head_sha:
            continue
        if run.get("event") not in ("schedule", "workflow_dispatch"):
            continue

        jobs = _fetch_all_run_jobs(repo, run)
        det_jobs = [
            j
            for j in jobs
            if any(str(j.get("name", "")).startswith(p) for p in DETERMINISTIC_NIGHTLY_JOB_PREFIXES)
        ]
        ran_det_jobs = [j for j in det_jobs if j.get("conclusion") not in (None, "", "skipped")]
        if not ran_det_jobs:
            continue

        if not all(j.get("conclusion") == "success" for j in ran_det_jobs):
            raise _MustRun(
                f"newest deterministic nightly run {run_id} on {head_sha[:10]} had non-success deterministic jobs; re-running"
            )
        if all(
            any(str(j.get("name", "")).startswith(req) for j in ran_det_jobs)
            for req in DETERMINISTIC_NIGHTLY_REQUIRED_PREFIXES
        ):
            return (
                f"deterministic nightly jobs ({len(ran_det_jobs)}) already succeeded on {head_sha[:10]} in run {run_id}; running stochastic jobs only"
            )
        raise _MustRun(
            f"newest deterministic nightly run {run_id} on {head_sha[:10]} was incomplete; re-running"
        )

    raise _MustRun(f"no prior green deterministic nightly run on {head_sha[:10]}")


def decide_nightly(
    event: str,
    repo: str,
    head_sha: str,
    current_run_id: int,
) -> tuple[bool, str]:
    """Return (run_deterministic, reason). Default is always True (run); skip (False) is returned in exactly one place."""
    try:
        reason = _prove_nightly_skip(event, repo, head_sha, current_run_id)
        return False, reason
    except _MustRun as exc:
        return True, str(exc)
    except Exception as exc:
        return True, f"unexpected error deciding nightly skip (fail-open): {exc}"


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
            "reason": reason,
        }
    )
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

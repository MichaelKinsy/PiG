#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Follow each new upstream Pi main commit once. Run it from cron (every 5 minutes) under a non-blocking flock:

    */5 * * * * flock -n /tmp/pin-watch.lock python3 /path/to/automation/pin/watch.py --run-porter

It reads the head of Pi's main branch with `git ls-remote` (no API token), skips a commit that has a pin record or an earlier
attempt, and runs `follow.py <commit>`. A follow that failed is not retried on the next poll: the FINDING line it posted asks for a
person, and the rerun (`follow.py <commit>`) resumes after the last passing step. It never changes the committed pin and never merges.
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path
from typing import Callable

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pinlib  # noqa: E402

ATTEMPTS = "tmp/pin-follow/attempts.txt"


def head(output: str) -> str:
    for line in output.splitlines():
        sha, _, _ = line.partition("\t")
        if pinlib.FULL_RE.match(sha):
            return sha
    raise ValueError("git ls-remote returned no commit")


def followed(root: Path) -> set[str]:
    commits = set()
    for record in sorted((root / "automation/pin/records").glob("*.json")):
        commits.add(json.loads(record.read_text(encoding="utf-8")).get("commit", ""))
    attempts = root / ATTEMPTS
    if attempts.exists():
        commits.update(attempts.read_text(encoding="utf-8").split())
    return commits


def is_new(root: Path, commit: str) -> bool:
    return commit not in followed(root)


def mark_attempt(root: Path, commit: str) -> None:
    path = root / ATTEMPTS
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as handle:
        handle.write(commit + "\n")


def poll(root: Path, ls_remote_output: str, run: Callable[[list[str]], int], run_porter: bool = False) -> int:
    commit = head(ls_remote_output)
    if not is_new(root, commit):
        return 0
    mark_attempt(root, commit)
    command = [sys.executable, str(root / "automation/pin/follow.py"), commit[:8]]
    if run_porter:
        command.append("--run-porter")
    return run(command)


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--run-porter", action="store_true")
    parser.add_argument("--branch", default="main")
    args = parser.parse_args(argv)
    root = Path(__file__).resolve().parents[2]
    try:
        out = subprocess.run(["git", "ls-remote", f"https://github.com/{pinlib.UPSTREAM_REPO}.git", f"refs/heads/{args.branch}"],
                             check=True, capture_output=True, text=True, timeout=60).stdout
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        stderr = getattr(error, "stderr", None)
        # TimeoutExpired carries the undecoded bytes read before the timeout on POSIX, even with text=True.
        if isinstance(stderr, bytes):
            stderr = stderr.decode("utf-8", "replace")
        detail = (stderr or str(error)).strip().splitlines()
        print(f"watch: git ls-remote failed, the next poll retries: {detail[0] if detail else error}", file=sys.stderr)
        return 1
    return poll(root, out, lambda command: subprocess.run(command, cwd=root, check=False).returncode, args.run_porter)


if __name__ == "__main__":
    sys.exit(main())

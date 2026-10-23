#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Regenerate the shard weights from the go test events of recent full CI runs.

Usage: aggregate-test-timings.py [--repo OWNER/NAME] [--runs N] [--from DIR] [--cache DIR] [--open-pr]

Every go test shard of ci.yml uploads its `go test -json` events as the artifact
test-json-<job>-<shard> (see automation/ci/go-test.sh). This script finds the last N
(default 20) successful runs of ci.yml that were full runs, reads their artifacts
through `gh api`, and computes the median and the 90th percentile of the duration
of every top-level test and of every shard (the wall-clock span of the shard's
events). It then regenerates:

- automation/ci/test-shard-weights.txt, the tests pinned to shards by measured
  duration (read by test-shard-pattern.sh), and
- SHARDED_SPEC in automation/ci/test-grouped.sh, the shard count of each heavy package.

A run is a full run when it has an artifact for every Linux test shard of
automation/ci/ci-tiers.txt (the light pull-request run lacks the queue-tier ones).
Only the Linux test shards feed the weights, so every test is measured once per
run, on one platform.

Without --open-pr the script prints the shard report and the proposed diff and
changes nothing. With --open-pr it commits the regenerated files on a new branch
cut from origin/main in a temporary worktree, pushes the branch and opens a pull
request for review; it never commits to main. --from DIR reads artifacts
already on disk (DIR/<run>/<artifact name>/*.json) instead of calling gh, and
--cache DIR keeps what gh downloaded there for the next run.

The placement and shard-count rules are in plan_package and choose_shard_count.
They are PROVISIONAL: they stand in for the rule of section 7.2 of the "Test
impact graph" draft spec, which was not available when this was written, and the
swap is those two functions.
"""

import argparse
import difflib
import io
import json
import math
import re
import statistics
import subprocess
import sys
import tempfile
import zipfile
from collections import defaultdict
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CI_DIR = ROOT / "automation" / "ci"
WEIGHTS = CI_DIR / "test-shard-weights.txt"
GROUPED = CI_DIR / "test-grouped.sh"
TIERS = CI_DIR / "ci-tiers.txt"
ARTIFACT_PREFIX = "test-json-"
LINUX_TEST_PREFIX = "test-json-linux-test-"

# Tests at least this slow (p90, seconds) are pinned by measured duration; every other test stays placed by name hash.
# These are the thresholds the existing weights file documents: 10 s for cmd/pig and 5 s for the other packages.
PIN_SECONDS = {"./coding/cli": 10.0}
DEFAULT_PIN_SECONDS = 5.0
# A shard is one go test process under go test's 10-minute package timeout, which is never raised. A package gets more shards
# when its predicted busiest shard would run longer than this share of that timeout.
SHARD_BUDGET_SECONDS = 420.0
MAX_SHARDS = 16
TEST_NAME = re.compile(r"^(Test|Example|Fuzz)[A-Za-z0-9_]*$")


def gh_api(repo_path: str, *, binary: bool = False) -> bytes | dict:
    out = subprocess.run(["gh", "api", repo_path], check=True, capture_output=True).stdout
    return out if binary else json.loads(out)


def required_artifacts() -> set[str]:
    """Return the artifact names a full run has: one per Linux test shard of ci-tiers.txt."""
    names = set()
    for line in TIERS.read_text().splitlines():
        if line.lstrip().startswith("#") or not line.strip():
            continue
        job, shard, *_ = [field.strip() for field in line.split("|")]
        if job == "linux" and shard.startswith("test-"):
            names.add(f"{ARTIFACT_PREFIX}linux-{shard}")
    return names


def fetch_full_runs(repo: str, count: int, cache: Path, scan_limit: int) -> list[Path]:
    """Download the artifacts of the newest `count` full runs of ci.yml into cache/<run id>/<artifact name>/ and return those directories."""
    need = required_artifacts()
    runs: list[Path] = []
    scanned = 0
    page = 1
    while len(runs) < count and scanned < scan_limit:
        listing = gh_api(f"repos/{repo}/actions/workflows/ci.yml/runs?status=success&per_page=100&page={page}")
        if not listing["workflow_runs"]:
            break
        for run in listing["workflow_runs"]:
            if len(runs) >= count or scanned >= scan_limit:
                break
            scanned += 1
            destination = cache / str(run["id"])
            artifacts = {a["name"]: a for a in gh_api(f"repos/{repo}/actions/runs/{run['id']}/artifacts?per_page=100")["artifacts"] if not a["expired"]}
            if not need <= artifacts.keys():
                continue
            for name, artifact in artifacts.items():
                if not name.startswith(ARTIFACT_PREFIX) or (destination / name).is_dir():
                    continue
                data = gh_api(f"repos/{repo}/actions/artifacts/{artifact['id']}/zip", binary=True)
                with zipfile.ZipFile(io.BytesIO(data)) as archive:
                    archive.extractall(destination / name)
            runs.append(destination)
        page += 1
    return runs


def module_path() -> str:
    for line in (ROOT / "go.mod").read_text().splitlines():
        if line.startswith("module "):
            return line.split()[1]
    raise SystemExit("aggregate-test-timings: go.mod names no module")


def percentile(values: list[float], fraction: float) -> float:
    """Return the nearest-rank percentile, so a p90 is a duration that was observed."""
    ordered = sorted(values)
    return ordered[max(0, math.ceil(fraction * len(ordered)) - 1)]


class Measurements:
    """Durations collected from the artifacts of the runs."""

    def __init__(self, module: str):
        self.module = module
        self.tests: dict[tuple[str, str], list[float]] = defaultdict(list)  # (package, test) -> seconds, one per run
        self.shards: dict[str, list[float]] = defaultdict(list)  # artifact name -> wall-clock seconds, one per run

    def add_run(self, run: Path) -> None:
        for artifact in sorted(run.iterdir()):
            if not artifact.is_dir() or not artifact.name.startswith(ARTIFACT_PREFIX):
                continue
            first = last = None
            durations: dict[tuple[str, str], float] = {}
            for events in sorted(artifact.glob("*.json")):
                for line in events.read_text(encoding="utf-8", errors="replace").splitlines():
                    try:
                        event = json.loads(line)
                    except ValueError:
                        continue
                    stamp = event.get("Time")
                    if stamp:
                        moment = datetime.fromisoformat(stamp)
                        first = moment if first is None or moment < first else first
                        last = moment if last is None or moment > last else last
                    test = event.get("Test")
                    if event.get("Action") in ("pass", "fail") and test and "/" not in test and TEST_NAME.match(test):
                        package = "./" + event["Package"].removeprefix(self.module + "/") if event["Package"] != self.module else "."
                        durations[(package, test)] = float(event.get("Elapsed") or 0)
            if first and last:
                self.shards[artifact.name].append((last - first).total_seconds())
            if artifact.name.startswith(LINUX_TEST_PREFIX):
                for key, seconds in durations.items():
                    self.tests[key].append(seconds)

    def p90_by_package(self) -> dict[str, dict[str, float]]:
        packages: dict[str, dict[str, float]] = defaultdict(dict)
        for (package, test), seconds in self.tests.items():
            packages[package][test] = percentile(seconds, 0.9)
        return packages


def fnv1a32(name: str) -> int:
    """Return the hash test-shard-pattern.sh places an unpinned test by."""
    value = 2166136261
    for char in name:
        value = ((value ^ ord(char)) * 16777619) & 0xFFFFFFFF
    return value


def plan_package(package: str, shards: int, p90: dict[str, float]) -> tuple[dict[str, int], list[float]]:
    """PROVISIONAL (stands in for the rule of section 7.2 of the draft spec): return each pinned test's shard and every shard's predicted load.

    A test whose p90 reaches the package's pin threshold is pinned; every other test stays where test-shard-pattern.sh hashes it,
    and counts toward its shard's load at its p90. The pinned tests, slowest first, each go to the currently lightest shard
    (the lowest-numbered on a tie), which is longest-processing-time-first packing on top of the hash-placed rest.
    """
    threshold = PIN_SECONDS.get(package, DEFAULT_PIN_SECONDS)
    loads = [0.0] * shards
    pinned = sorted(((seconds, name) for name, seconds in p90.items() if seconds >= threshold), key=lambda item: (-item[0], item[1]))
    pinned_names = {name for _, name in pinned}
    for name, seconds in p90.items():
        if name not in pinned_names:
            loads[fnv1a32(name) % shards] += seconds
    placement: dict[str, int] = {}
    for seconds, name in pinned:
        shard = loads.index(min(loads))
        placement[name] = shard + 1
        loads[shard] += seconds
    return placement, loads


def choose_shard_count(package: str, current: int, p90: dict[str, float]) -> int:
    """PROVISIONAL (see plan_package): return the smallest count, never below the current one, whose predicted busiest shard fits the budget.

    A shard is never dropped to save time, so the count only grows; it stops growing at MAX_SHARDS.
    """
    shards = current
    while shards < MAX_SHARDS and max(plan_package(package, shards, p90)[1]) > SHARD_BUDGET_SECONDS:
        shards += 1
    return shards


def read_sharded_spec(text: str) -> dict[str, int]:
    block = re.search(r"^SHARDED_SPEC=\(\n(.*?)^\)", text, re.S | re.M)
    if not block:
        raise SystemExit("aggregate-test-timings: test-grouped.sh has no SHARDED_SPEC")
    return {pkg: int(count) for pkg, count in re.findall(r"^\s+(\S+):(\d+)$", block.group(1), re.M)}


def read_weight_sections(text: str) -> dict[tuple[str, int], list[str]]:
    """Return each (package, shard count) section of the weights file with its lines, "package" and "shards" lines included."""
    sections: dict[tuple[str, int], list[str]] = {}
    package = None
    body: list[str] = []
    for line in text.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[0] == "package":
            package, body = fields[1], [line]
        elif package and body:
            body.append(line)
            if len(fields) == 2 and fields[0] == "shards":
                sections[(package, int(fields[1]))] = body
    return sections


def header_of(text: str) -> str:
    lines = text.splitlines()
    end = next(i for i, line in enumerate(lines) if line.startswith("package "))
    return "\n".join(lines[:end]) + "\n"


def regenerate(measured: Measurements) -> tuple[str, str, list[str]]:
    """Return the new text of test-shard-weights.txt and test-grouped.sh, and a report of what the rule decided."""
    weights_text, grouped_text = WEIGHTS.read_text(), GROUPED.read_text()
    p90 = measured.p90_by_package()
    spec = read_sharded_spec(grouped_text)
    report: list[str] = []
    new_spec: dict[str, int] = {}
    for package, current in spec.items():
        if package not in p90:
            new_spec[package] = current
            report.append(f"{package}: no measurements, keeps {current} shards")
            continue
        new_spec[package] = choose_shard_count(package, current, p90[package])
        report.append(f"{package}: {current} -> {new_spec[package]} shards")
    # A section stays for every (package, shard count) the weights file already has (the Windows and macOS jobs use counts that differ from the Linux ones), and the Linux counts of every sharded package and cmd/pig's eight are added.
    existing = read_weight_sections(weights_text)
    wanted: dict[tuple[str, int], None] = dict.fromkeys(existing)
    for package, count in new_spec.items():
        wanted[(package, count)] = None
    wanted[("./coding/cli", 8)] = None
    lines = [header_of(weights_text).rstrip("\n")]
    for package, count in sorted(wanted):
        if package not in p90:
            # No run measured this package: keep its section as it is rather than drop the pins.
            lines += existing.get((package, count), [])
            continue
        placement, loads = plan_package(package, count, p90[package])
        report.append(f"{package} x{count}: {len(placement)} pinned, predicted shard seconds (p90) " + " ".join(f"{load:.0f}" for load in loads))
        if placement:
            lines.append(f"package {package}\nshards {count}")
            lines += [f"{shard} {name}" for name, shard in sorted(placement.items(), key=lambda item: (item[1], item[0]))]
    new_weights = "\n".join(lines) + "\n"
    new_grouped = re.sub(
        r"^(SHARDED_SPEC=\(\n)(.*?)(^\))",
        lambda m: m.group(1) + "".join(f"  {package}:{count}\n" for package, count in new_spec.items()) + m.group(3),
        grouped_text,
        count=1,
        flags=re.S | re.M,
    )
    return new_weights, new_grouped, report


def shard_report(measured: Measurements) -> list[str]:
    lines = ["shard (artifact)                              runs   median s    p90 s"]
    for name in sorted(measured.shards):
        seconds = measured.shards[name]
        lines.append(f"{name[len(ARTIFACT_PREFIX):]:44} {len(seconds):5} {statistics.median(seconds):10.0f} {percentile(seconds, 0.9):8.0f}")
    lines.append("")
    lines.append("slowest tests (Linux test shards)                                         runs   median s    p90 s")
    slowest = sorted(measured.tests.items(), key=lambda item: -percentile(item[1], 0.9))[:25]
    for (package, test), seconds in slowest:
        lines.append(f"{package + ' ' + test:74} {len(seconds):5} {statistics.median(seconds):10.1f} {percentile(seconds, 0.9):8.1f}")
    return lines


def diff_of(path: Path, new: str) -> str:
    old = path.read_text()
    relative = path.relative_to(ROOT).as_posix()
    return "".join(difflib.unified_diff(old.splitlines(keepends=True), new.splitlines(keepends=True), f"a/{relative}", f"b/{relative}"))


def open_pull_request(repo: str, files: dict[Path, str], body: str) -> None:
    """Commit the files on a new branch cut from origin/main, in a temporary worktree, push it and open a pull request."""
    stamp = datetime.now(timezone.utc).strftime("%Y%m%d")
    branch = f"chore/test-shard-weights-{stamp}"
    subprocess.run(["git", "fetch", "origin", "main"], cwd=ROOT, check=True)
    with tempfile.TemporaryDirectory() as parent:
        tree = Path(parent) / "worktree"
        subprocess.run(["git", "worktree", "add", "-b", branch, str(tree), "origin/main"], cwd=ROOT, check=True)
        try:
            for path, text in files.items():
                (tree / path.relative_to(ROOT)).write_text(text)
            subprocess.run(["git", "add", "automation/ci"], cwd=tree, check=True)
            subprocess.run(["git", "commit", "--signoff", "-m", "ci: regenerate the shard weights from measured timings", "-m", body], cwd=tree, check=True)
            subprocess.run(["git", "push", "origin", branch], cwd=tree, check=True)
            subprocess.run(["gh", "pr", "create", "--repo", repo, "--base", "main", "--head", branch, "--title", "ci: regenerate the shard weights from measured timings", "--body", body], cwd=tree, check=True)
        finally:
            subprocess.run(["git", "worktree", "remove", "--force", str(tree)], cwd=ROOT, check=False)


def default_repo() -> str:
    return subprocess.run(["gh", "repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--repo", help="OWNER/NAME (default: the repository gh resolves)")
    parser.add_argument("--runs", type=int, default=20, help="how many full runs to read")
    parser.add_argument("--scan-limit", type=int, default=300, help="how many runs to look through for full ones")
    parser.add_argument("--from", dest="source", type=Path, help="read DIR/<run>/<artifact>/*.json instead of calling gh")
    parser.add_argument("--cache", type=Path, help="keep downloaded artifacts in this directory")
    parser.add_argument("--open-pr", action="store_true", help="commit the regenerated files on a new branch and open a pull request")
    args = parser.parse_args()

    if args.source:
        return report_and_propose(args, sorted(path for path in args.source.iterdir() if path.is_dir())[-args.runs :])
    repo = args.repo or default_repo()
    with tempfile.TemporaryDirectory() as scratch:
        cache = args.cache or Path(scratch)
        cache.mkdir(parents=True, exist_ok=True)
        args.repo = repo
        return report_and_propose(args, fetch_full_runs(repo, args.runs, cache, args.scan_limit))


def report_and_propose(args: argparse.Namespace, runs: list[Path]) -> int:
    if not runs:
        raise SystemExit("aggregate-test-timings: no full run with test-json artifacts (they expire after 30 days)")
    measured = Measurements(module_path())
    for path in runs:
        measured.add_run(path)
    new_weights, new_grouped, decisions = regenerate(measured)
    print(f"{len(runs)} full run(s): {', '.join(path.name for path in runs)}\n")
    print("\n".join(shard_report(measured)))
    print("\n" + "\n".join(decisions) + "\n")
    diff = diff_of(WEIGHTS, new_weights) + diff_of(GROUPED, new_grouped)
    print(diff or "no change to the weights or the shard counts")
    if args.open_pr and diff:
        body = f"Regenerated by automation/ci/aggregate-test-timings.py from {len(runs)} full CI runs ({', '.join(path.name for path in runs)}).\n\n```\n" + "\n".join(decisions) + "\n```\n"
        open_pull_request(args.repo or default_repo(), {WEIGHTS: new_weights, GROUPED: new_grouped}, body)
    return 0


if __name__ == "__main__":
    sys.exit(main())

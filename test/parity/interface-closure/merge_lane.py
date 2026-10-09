#!/usr/bin/env python3
"""Merge a closure lane branch into this branch without taking its mapping edits.

Usage: python3 test/parity/interface-closure/merge_lane.py <lane ref> [<lane ref>...]
For each lane: check out every file the lane changed except the generated ledgers (mapping, pig-go, recommendations, gap
baseline), refusing when this branch also changed one of those files since the merge base, then apply every `specs/ledger-*.json` with close.py (idempotent). Afterwards run, by hand: read the lane's doc for
withdrawn rows and revert them with revert.py; `make interface-go interface-recommendations-generate interface-gaps-update`; prodreach;
reproduce.py --apply --public; audit.py; `make interface-mapping-quality`; runevidence.py on each new spec.
"""
import subprocess
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]


def run(*args, check=True):
    return subprocess.run(args, cwd=root, capture_output=True, text=True, check=check).stdout


skip = ("interfaces/mapping-v", "interfaces/pig-go.json", "interfaces/recommendations", "interface-closure/gaps/")
for lane in sys.argv[1:]:
    base = run("git", "merge-base", "HEAD", lane).strip()
    files = [f for f in run("git", "diff", "--name-only", base, lane).splitlines() if not any(s in f for s in skip)]

    def blob(rev, f):
        return run("git", "rev-parse", "-q", "--verify", f"{rev}:{f}", check=False).strip()

    # A file this branch changed since the merge base would lose that change when the lane's copy replaces it.
    clobbered = [f for f in files if blob("HEAD", f) not in (blob(base, f), blob(lane, f))]
    if clobbered:
        sys.exit(f"{lane}: both branches changed {', '.join(clobbered)}; merge these by hand, then rerun")
    for f in files:
        run("git", "checkout", lane, "--", f)
    print(f"{lane}: took {len(files)} files")
specs = sorted((root / "test/parity/interface-closure/specs").glob("ledger-*.json"))
for spec in specs:
    out = subprocess.run([sys.executable, "test/parity/interface-closure/close.py", str(spec)], cwd=root, capture_output=True, text=True)
    last = (out.stdout + out.stderr).strip().splitlines()[-1:] or [""]
    if out.returncode or "closed 0 rows" not in last[0]:
        print(f"{spec.name}: {last[0][:200]}")

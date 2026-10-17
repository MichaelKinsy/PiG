#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Normalizes, summarizes and ratchets the LW benchmark rows that bench/lw/row.sh runs with Pi-Bolt's bench tools.

  rows.py collect --out RUNS.jsonl --row ROW --round N --raw RAW.txt [--jsonl TOOL.jsonl] [--meta KEY=VALUE ...]
      appends one record per measurement: {"row", "key", "build", "arm", "replica", "round", "metrics": {...}, ...meta}
  rows.py summary RUNS.jsonl [--ratchet ratchet.toml]
      prints the table: per row and key, the median of each arm over every round and replica, the A/A spread
      (median over rounds of |a - b| / mean of the two replicas of an arm) and pig's median over Pi-Bolt's
  rows.py check RUNS.jsonl ratchet.toml
      fails when a ratio has a ceiling `lw.row.<row>.<key>.<metric>.ratio_vs_bolt` in [ceilings] and exceeds it

large_write.py and long_answer.py print a table and write no JSONL, so collect parses their stdout; benchmark.py and
long_session.py write JSONL (--out), which collect reads. benchmark.py's peak_mb is the run's ru_maxrss (the peak resident
set of the pig or Pi-Bolt process): collect stores it as peak_rss_mb.
"""

import argparse
import json
import re
import statistics
import sys
import tomllib
from pathlib import Path

# The metric each row's ratio and ratchet read, and the metrics the table shows.
PRIMARY = {"large_write": "cpu_s", "long_answer": "cpu_s", "interactive": "cpu_ms", "long_session": "cpu_ms_per_prompt",
           "headless": "cpu_ms", "startup": "cpu_ms"}
SHOWN = {
    "large_write": ["cpu_s", "wall_s"],
    "long_answer": ["cpu_s", "wall_s", "own_mb"],
    "interactive": ["cpu_ms", "wall_ms", "tti_ms", "peak_rss_mb"],
    "long_session": ["cpu_ms_per_prompt", "ms_per_prompt", "own_mb"],
    "headless": ["cpu_ms", "wall_ms", "peak_rss_mb", "instr_m"],
    "startup": ["cpu_ms", "wall_ms", "peak_rss_mb", "instr_m"],
}
# Ceilings that hold on one platform only (row.sh's --meta platform=, darwin-arm64 or linux-x64); every other ceiling holds on both.
# The peak RSS ratios are linux-only: on macOS Pi-Bolt's ru_maxrss is not comparable with pig's.
PLATFORMS = {
    "lw.row.headless.headless.instr_m.ratio_vs_bolt": "darwin",
    "lw.row.headless.headless.cpu_ms.ratio_vs_bolt": "linux",
    "lw.row.headless.headless.peak_rss_mb.ratio_vs_bolt": "linux",
    "lw.row.interactive.interactive.peak_rss_mb.ratio_vs_bolt": "linux",
}


def applies(gate, runs):
    """Whether the ceiling gate holds for runs: a platform-only ceiling holds when the runs were taken on that platform."""
    platform = PLATFORMS.get(gate)
    return platform is None or any(r.get("platform", "").startswith(platform) for r in runs)


def split_build(build):
    """pig-a -> (pig, a); a build without a replica suffix is its own arm."""
    arm, _, replica = build.rpartition("-")
    if arm and len(replica) == 1:
        return arm, replica
    return build, ""


def parse_large_write(text):
    # "{build:12} {size:5} KB {wall:8.1f} {cpu:8.1f}  result"
    for m in re.finditer(r"^(\S+)\s+(\d+) KB\s+([\d.]+)\s+([\d.]+)\s+(.*)$", text, re.M):
        yield m[1], f"{m[2]}KB", {"wall_s": float(m[3]), "cpu_s": float(m[4]), "ok": m[5].strip() == "ok"}


def parse_long_answer(text):
    # "{build:12} {size:10} {wall:8.1f} {used:8.2f} {pct:9.0f}% {own:7.0f}"
    for m in re.finditer(r"^(\S+)\s+(\d+)\s+([\d.]+)\s+([\d.]+)\s+(\d+)%\s+([\d.]+)\s*$", text, re.M):
        yield m[1], f"{m[2]}ch", {"wall_s": float(m[3]), "cpu_s": float(m[4]), "core_pct": float(m[5]), "own_mb": float(m[6]), "ok": True}


def from_jsonl(path, row):
    for line in Path(path).read_text().splitlines():
        if not line.strip():
            continue
        r = json.loads(line)
        build = r.pop("build")
        if row in ("interactive", "headless", "startup"):
            key = r.pop("scenario", row)
            r.pop("run", None)
            if "peak_mb" in r:
                r["peak_rss_mb"] = r.pop("peak_mb")
        else:
            key = f"prompt{r.pop('prompt', '?')}"
            r["ok"] = r.pop("exit", 0) == 0 and "error" not in r
        yield build, key, {k: v for k, v in r.items() if isinstance(v, (int, float, bool))}


def collect(a):
    if a.row == "large_write":
        found = list(parse_large_write(Path(a.raw).read_text()))
    elif a.row == "long_answer":
        found = list(parse_long_answer(Path(a.raw).read_text()))
    elif a.jsonl and Path(a.jsonl).exists():
        found = list(from_jsonl(a.jsonl, a.row))
    else:
        found = []
    meta = dict(kv.split("=", 1) for kv in a.meta or [])
    with open(a.out, "a") as f:
        for build, key, metrics in found:
            arm, replica = split_build(build)
            f.write(json.dumps({"row": a.row, "key": key, "build": build, "arm": arm, "replica": replica, "round": a.round,
                                "metrics": metrics, **meta}) + "\n")
    if not found:
        sys.exit(f"rows.py: no measurements of {a.row} in {a.raw}")


def load(path):
    return [json.loads(line) for line in Path(path).read_text().splitlines() if line.strip()]


def aggregate(runs):
    """{(row, key, metric): {arm: {"median", "n", "aa"}}}"""
    values, pairs = {}, {}
    for r in runs:
        for metric, v in r["metrics"].items():
            if isinstance(v, bool):
                continue
            values.setdefault((r["row"], r["key"], metric), {}).setdefault(r["arm"], []).append(v)
            pairs.setdefault((r["row"], r["key"], metric, r["arm"], r["round"]), {})[r["replica"]] = v
    spread = {}
    for (row, key, metric, arm, _), byrep in pairs.items():
        if len(byrep) == 2:
            x, y = byrep.values()
            mean = (x + y) / 2
            if mean:
                spread.setdefault((row, key, metric, arm), []).append(abs(x - y) / mean)
    out = {}
    for (row, key, metric), arms in values.items():
        out[(row, key, metric)] = {arm: {"median": statistics.median(vs), "n": len(vs),
                                         "aa": statistics.median(spread[(row, key, metric, arm)]) if (row, key, metric, arm) in spread else None}
                                   for arm, vs in arms.items()}
    return out


def ratio_id(row, key, metric):
    return f"lw.row.{row}.{key}.{metric}.ratio_vs_bolt"


def summary(a):
    runs = load(a.runs)
    agg = aggregate(runs)
    ceilings = tomllib.loads(Path(a.ratchet).read_text()).get("ceilings", {}) if a.ratchet else {}
    rounds = len({r["round"] for r in runs})
    print(f"# LW rows: {rounds} round(s), {len(runs)} measurements ({a.runs})\n")
    print("| row | key | metric | pig | A/A pig | bolt | A/A bolt | pig / bolt | ceiling |")
    print("|---|---|---|---:|---:|---:|---:|---:|---:|")
    fmt = lambda v: "-" if v is None else (f"{v:.3g}" if abs(v) < 100 else f"{v:.0f}")
    pct = lambda v: "-" if v is None else f"{100 * v:.0f}%"
    for (row, key, metric), arms in sorted(agg.items()):
        if metric not in SHOWN.get(row, [metric]):
            continue
        pig, bolt = arms.get("pig"), arms.get("bolt")
        ratio = pig["median"] / bolt["median"] if pig and bolt and bolt["median"] else None
        ceiling = ceilings.get(ratio_id(row, key, metric))
        print(f"| {row} | {key} | {metric} | {fmt(pig and pig['median'])} | {pct(pig and pig['aa'])} | {fmt(bolt and bolt['median'])} "
              f"| {pct(bolt and bolt['aa'])} | {'-' if ratio is None else f'{ratio:.2f}x'} | {'-' if ceiling is None else f'{ceiling}x'} |")
    failed = sum(1 for r in runs if r["metrics"].get("ok") is False)
    if failed:
        print(f"\n{failed} measurement(s) did not end ok: see the raw logs.")


def check(a):
    runs = load(a.runs)
    agg = aggregate(runs)
    ceilings = tomllib.loads(Path(a.ratchet).read_text()).get("ceilings", {})
    bad = 0
    for (row, key, metric), arms in sorted(agg.items()):
        ceiling = ceilings.get(ratio_id(row, key, metric))
        if ceiling is None or not applies(ratio_id(row, key, metric), runs) or "pig" not in arms or not arms.get("bolt", {}).get("median"):
            continue
        ratio = arms["pig"]["median"] / arms["bolt"]["median"]
        print(f"lw-row: {ratio_id(row, key, metric)}={ratio:.2f} ceiling={ceiling}")
        if ratio > ceiling:
            bad += 1
            print(f"lw-row: {ratio_id(row, key, metric)} = {ratio:.2f}, ceiling {ceiling}", file=sys.stderr)
    sys.exit(1 if bad else 0)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    c = sub.add_parser("collect")
    c.add_argument("--out", required=True)
    c.add_argument("--row", required=True, choices=sorted(PRIMARY))
    c.add_argument("--round", type=int, required=True)
    c.add_argument("--raw", required=True)
    c.add_argument("--jsonl")
    c.add_argument("--meta", action="append")
    s = sub.add_parser("summary")
    s.add_argument("runs")
    s.add_argument("--ratchet")
    k = sub.add_parser("check")
    k.add_argument("runs")
    k.add_argument("ratchet")
    a = ap.parse_args()
    {"collect": collect, "summary": summary, "check": check}[a.cmd](a)


if __name__ == "__main__":
    main()

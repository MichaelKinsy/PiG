# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Check overhead results against evals/budgets.toml, and compare Go benchmark runs."""
import json
import re
import statistics
import tomllib

BENCH_LINE = re.compile(r"^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+(.*)$")


def check(args):
    with open(args.results) as f:
        report = json.load(f)
    with open(args.budgets, "rb") as f:
        budgets = tomllib.load(f)
    rows = {row["scenario"]: row["by"] for row in report["results"]}
    failures = 0
    for hid, scenarios in budgets.items():
        if hid in ("parity", "slop"):  # Not harness tables.
            continue
        for sid, limits in scenarios.items():
            got = rows.get(sid, {}).get(hid, {})
            for metric, ceiling in limits.items():
                value = got.get(metric)
                ok = value is not None and value <= ceiling
                failures += not ok
                print(f"budget: {'ok  ' if ok else 'FAIL'} {hid}/{sid} {metric} = {value} (ceiling {ceiling})")
    allowance = budgets.get("parity", {}).get("request_bytes_over_pi")
    if allowance is not None:
        pig = rows.get("round-trip", {}).get("pig", {}).get("request_bytes")
        pi = rows.get("round-trip", {}).get("pi", {}).get("request_bytes")
        if pig is None or pi is None:
            print("budget: skip parity request size (run the overhead evals with pig and pi)")
        else:
            ok = pig - pi <= allowance
            failures += not ok
            print(f"budget: {'ok  ' if ok else 'FAIL'} parity request bytes: pig {pig} - pi {pi} = {pig - pi:+d} (allowance {allowance})")
    return 1 if failures else 0


def parse_bench(path):
    """Return {benchmark: {unit: [values]}} from `go test -bench` output."""
    results = {}
    with open(path) as f:
        for line in f:
            match = BENCH_LINE.match(line.strip())
            if not match:
                continue
            fields = match.group(2).split()
            metrics = results.setdefault(match.group(1), {})
            for value, unit in zip(fields[0::2], fields[1::2]):
                try:
                    metrics.setdefault(unit, []).append(float(value))
                except ValueError:
                    pass
    return results


def benchcmp(args):
    old, new = parse_bench(args.old), parse_bench(args.new)
    regressions = 0
    print(f"{'benchmark':48} {'unit':10} {'old':>12} {'new':>12} {'delta':>8}")
    for name in sorted(set(old) & set(new)):
        for unit in sorted(set(old[name]) & set(new[name])):
            a, b = statistics.median(old[name][unit]), statistics.median(new[name][unit])
            delta = (b - a) / a * 100 if a else 0.0
            worse = unit in ("ns/op", "B/op", "allocs/op") and delta > args.threshold
            regressions += worse
            print(f"{name[:48]:48} {unit:10} {a:12.1f} {b:12.1f} {delta:+7.1f}%{'  REGRESSION' if worse else ''}")
    only = sorted(set(old) ^ set(new))
    if only:
        print(f"benchcmp: present in one run only: {', '.join(only)}")
    print(f"benchcmp: {regressions} regression(s) above {args.threshold}% (medians; use -count=6 or more for stable medians)")
    return 1 if regressions and args.fail else 0

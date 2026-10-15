# SPDX-License-Identifier: MIT
"""Aggregate reports: raw.json files from several machines become one report with per-architecture tables and a cross-architecture summary.

The architecture key is `<os>-<arch>` (linux-x64, linux-arm64, darwin-arm64, windows-x64), taken from each run's environment block. Runs of one
machine (same architecture, OS, CPU model, logical CPUs and memory) share a column; a second machine of the same architecture gets its own column
(`linux-x64 #2`), so ratios are only formed within one machine, against the reference arm run on that same machine. Absolute numbers of different architectures are listed
side by side and never ranked: they come from different hardware. Runs built from a different arms.lock or commit are refused unless allowed.
"""
import csv
import json
import os

from . import report as R
from . import stats

OS_KEYS = {"linux": "linux", "darwin": "darwin", "windows": "windows"}
ARCH_KEYS = {"x86_64": "x64", "amd64": "x64", "aarch64": "arm64", "arm64": "arm64"}
HEADLINE = ["wall_ms", "first_request_ms", "first_frame_ms", "first_input_accepted_ms", "ready_ms", "ready_pss_mib", "peak_rss_mib", "turn_cpu_ms", "turn_cpu_mean_ms", "turn_harness_added_ms"]
SUBJECT = "pig-0.5"


def arch_key(env):
    system = (env.get("os") or "").split()[0].lower()
    machine = (env.get("machine") or "").lower()
    return f"{OS_KEYS.get(system, system)}-{ARCH_KEYS.get(machine, machine)}"


KINDS = ("overhead", "startup")


def load(paths):
    runs = []
    for p in paths:
        path = os.path.join(p, "raw.json") if os.path.isdir(p) else p
        with open(path) as f:
            raw = json.load(f)
        if raw.get("kind") not in KINDS:
            raise SystemExit(f"refusing to aggregate {path}: it is a {raw.get('kind')!r} run; aggregate reads {' and '.join(KINDS)} runs")
        runs.append((path, raw))
    return runs


def machine_of(env):
    """What identifies one machine in a run's environment block (the block names no host, by design)."""
    return (arch_key(env), env.get("os"), env.get("cpu"), env.get("logical_cpus"), env.get("memory_gib"))


def check_mixed(runs, allow_mixed):
    locks = {raw["env"].get("arms_lock_sha256") for _, raw in runs}
    commits = {raw["env"].get("commit") for _, raw in runs}
    notes = []
    if len(locks) > 1:
        notes.append(f"arms.lock differs between the runs ({', '.join(sorted(str(x)[:12] for x in locks))})")
    if len(commits) > 1:
        notes.append(f"commits differ between the runs ({', '.join(sorted(str(x)[:10] for x in commits))})")
    if notes and not allow_mixed:
        raise SystemExit("refusing to aggregate: " + "; ".join(notes) + ". Rerun on one commit, or pass --allow-mixed to say so in the report.")
    return notes


STARTUP_SCENARIO = "version (hyperfine)"


def groups_of(raw):
    """{scenario: {arm: [samples]}} for an overhead run; a startup run is the single scenario STARTUP_SCENARIO. It is not the overhead bench's
    `version` scenario: hyperfine and the overhead driver measure `--version` with different meters, so their samples never share a cell."""
    if raw["kind"] == "overhead":
        return raw["samples"]
    return {STARTUP_SCENARIO: raw["samples"]}


def values(samples, metric):
    return [s[metric] for s in samples if isinstance(s.get(metric), (int, float)) and not isinstance(s.get(metric), bool)]


def merge(runs):
    """{machine: {scenario: {arm: [samples]}}}; runs of one machine (overhead and startup) merge by scenario. The machine label is its
    architecture key, numbered (`linux-x64 #2`) when another machine of the same architecture came first."""
    out, envs, labels = {}, {}, {}
    for _, raw in runs:
        ident = machine_of(raw["env"])
        if ident not in labels:
            n = sum(1 for other in labels if other[0] == ident[0]) + 1
            labels[ident] = ident[0] if n == 1 else f"{ident[0]} #{n}"
        key = labels[ident]
        envs.setdefault(key, raw["env"])
        for scenario, arms in groups_of(raw).items():
            slot = out.setdefault(key, {}).setdefault(scenario, {})
            for arm, samples in arms.items():
                slot.setdefault(arm, []).extend(samples)
    return out, envs


def cross_table(merged, envs, seed, csv_rows):
    archs = sorted(merged)
    lines = ["| scenario | metric | " + " | ".join(f"{a}: {SUBJECT} / {R.REFERENCE} (CI)" for a in archs) + " |", "|---|---|" + "---|" * len(archs)]
    for scenario in sorted({s for a in merged.values() for s in a}):
        for metric in HEADLINE:
            row, any_ = [], False
            for a in archs:
                arms = merged[a].get(scenario, {})
                xs, ys = values(arms.get(SUBJECT, []), metric), values(arms.get(R.REFERENCE, []), metric)
                if len(xs) < 3 or len(ys) < 3 or not stats.median(ys):
                    row.append("n/a")
                    continue
                tick = envs[a].get("cpu_tick_ms")
                if R.coarse_cpu([stats.median(xs), stats.median(ys)], metric, tick):
                    row.append(f"n/a (CPU in {tick:g} ms ticks)")
                    any_ = True
                    continue
                r = stats.ratio_of_medians(xs, ys, seed=seed)
                row.append(f"{r['ratio']:.2f} [{r['ci'][0]:.2f}, {r['ci'][1]:.2f}]")
                csv_rows.append(["aggregate", f"{scenario}/{a}", SUBJECT, f"{metric}_ratio_vs_{R.REFERENCE}", len(xs), r["ratio"], None, None, None, r["ci"][0], r["ci"][1]])  # a ratio has no p90, min or max
                any_ = True
            if any_:
                lines.append(f"| {scenario} | {metric} | " + " | ".join(row) + " |")
    return "\n".join(lines) if len(lines) > 2 else ""


def absolute_table(merged):
    archs = sorted(merged)
    lines = ["| scenario | metric | arm | " + " | ".join(archs) + " |", "|---|---|---|" + "---|" * len(archs)]
    for scenario in sorted({s for a in merged.values() for s in a}):
        for metric in HEADLINE:
            for arm in R.arm_order({x for a in merged.values() for x in a.get(scenario, {})}):
                cells, any_ = [], False
                for a in archs:
                    vs = values(merged[a].get(scenario, {}).get(arm, []), metric)
                    cells.append(f"{stats.median(vs):.4g}" if vs else "n/a")
                    any_ = any_ or bool(vs)
                if any_:
                    lines.append(f"| {scenario} | {metric} | {arm} | " + " | ".join(cells) + " |")
    return "\n".join(lines) if len(lines) > 2 else ""


def machine_note(env):
    """What a cell means on a machine whose meter or CPU accounting differs from a cgroup's, or ""."""
    parts = []
    if env.get("meter") == "windows-job":
        parts.append(R.WINDOWS_METER + " hyperfine has no memory metric there, so `version (hyperfine)` has no peak memory.")
    if env.get("cpu_tick_ms"):
        parts.append(f"CPU time is charged in whole clock ticks of {env['cpu_tick_ms']:g} ms: a CPU median under {R.MIN_TICKS} ticks is not ranked, and `turn_cpu_mean_ms` is the mean over a run's turns.")
    return " ".join(parts)


def aggregate(paths, out, seed=0, allow_mixed=False):
    runs = load(paths)
    notes = check_mixed(runs, allow_mixed)
    merged, envs = merge(runs)
    rows = []
    md = ["# Cross-architecture report", "", "No model was called and no live key was used. Each column is one machine (a second machine of an architecture is numbered); ratios are formed only within one machine, against Pi on that machine. "
          "Absolute numbers are listed for reference and are never compared across architectures.", ""]
    if notes:
        md += ["**Mixed inputs (allowed explicitly):** " + "; ".join(notes) + ".", ""]
    md += ["## Machines", "", "| architecture | os | cpu | logical cpus | memory GiB | commit | arms.lock sha256 | meter |", "|---|---|---|---|---|---|---|---|"]
    for a in sorted(envs):
        e = envs[a]
        md.append(f"| {a} | {e.get('os')} | {e.get('cpu')} | {e.get('logical_cpus')} | {e.get('memory_gib')} | `{str(e.get('commit'))[:10]}` | `{str(e.get('arms_lock_sha256'))[:12]}` | {e.get('meter')} |")
    meters = [f"- **{a}**: {machine_note(envs[a])}" for a in sorted(envs) if machine_note(envs[a])]
    if meters:
        md += ["", "What a cell means differs on these machines:", ""] + meters
    md += ["", f"## Summary: {SUBJECT} against {R.REFERENCE} on each architecture", "", "Ratio of medians with a 95% bootstrap interval; below 1 is lower than Pi. Every headline metric is listed, losses included.", "", cross_table(merged, envs, seed, rows), ""]
    md += ["## Absolute medians (not comparable across architectures)", "", absolute_table(merged), ""]
    for a in sorted(merged):
        md += [f"## {a}", ""]
        tick = envs[a].get("cpu_tick_ms")
        for scenario, groups in sorted(merged[a].items()):
            md += [f"### {scenario}", "", R.table(groups, R.OVERHEAD_METRICS, seed, rows, "aggregate", f"{scenario}/{a}"), ""]
            note = R.cpu_tick_note(groups, tick)
            if note:
                md += [note, ""]
            ratio = R.ratio_table(groups, R.ranked(groups, R.OVERHEAD_METRICS, tick), seed)
            if ratio:
                md += ["Ratio of medians against Pi 1.1.0 on this machine:", "", ratio, ""]
    missing = []
    wanted = {s for a in merged.values() for s in a}
    for a in sorted(merged):
        for s in sorted(wanted - set(merged[a])):
            missing.append(f"- {a}: no `{s}` results")
        for s in sorted(wanted & set(merged[a])):
            for arm in sorted({x for m in merged.values() for x in m.get(s, {})} - set(merged[a][s])):
                missing.append(f"- {a}: no `{arm}` results in `{s}`")
    if missing:
        md += ["## Missing on some architectures", ""] + missing + [""]
    os.makedirs(out, exist_ok=True)
    with open(os.path.join(out, "report.md"), "w") as f:
        f.write("\n".join(md) + "\n")
    with open(os.path.join(out, "summary.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["bench", "group", "arm", "metric", "n", "median", "p90", "min", "max", "ci95_lo", "ci95_hi"])
        w.writerows(rows)
    return os.path.join(out, "report.md")

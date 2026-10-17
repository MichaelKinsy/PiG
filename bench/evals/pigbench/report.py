# SPDX-License-Identifier: MIT
"""Render a bench's raw.json into report.md and CSV. One function per bench kind; every number comes from raw.json and the seed in it."""
import csv
import json
import os

from . import stats

ORDER = ["pig-0.5", "pig-core", "pig-min", "pig-0.4.1", "pi-1.1.0", "claude", "codex", "copilot"]
REFERENCE = "pi-1.1.0"


def arm_order(ids):
    return sorted(ids, key=lambda a: ORDER.index(a) if a in ORDER else len(ORDER))


def unit(metric):
    if metric.endswith("_ms") or "_ms_" in metric:
        return "ms"
    if metric.endswith("_mib"):
        return "MiB"
    if metric.endswith("_bytes"):
        return "bytes"
    return ""


def fmt(x):
    if x is None:
        return "n/a"
    if isinstance(x, bool):
        return "yes" if x else "no"
    if isinstance(x, (int, float)):
        if abs(x) >= 1e6:
            return f"{x / 1e6:.2f}M"
        return f"{x:,.0f}" if abs(x) >= 100 else f"{x:.1f}"
    return str(x)


def cell(values, seed):
    """median [95% bootstrap CI] (n) for a list of numbers."""
    vals = [v for v in values if isinstance(v, (int, float)) and not isinstance(v, bool)]
    if not vals:
        flags = [v for v in values if isinstance(v, bool)]
        return ("yes" if all(flags) else "no" if not any(flags) else "mixed") if flags else "n/a"
    s = stats.summarize(vals)
    if len(vals) < 3:
        return f"{fmt(s['median'])} (n={len(vals)})"
    lo, hi = stats.bootstrap_ci(vals, seed=seed)
    return f"{fmt(s['median'])} [{fmt(lo)}, {fmt(hi)}] (n={len(vals)})"


def label(ratio, lo_hi_ok):
    """Qualitative label for blog use: no numbers, only direction and size, and only when the intervals do not overlap.

    Overlapping intervals are no evidence of parity (that needs an equivalence test with a declared margin), so they are labeled inconclusive."""
    if ratio is None or not lo_hi_ok:
        return "inconclusive"
    if ratio < 0.5:
        return "much lower"
    if ratio < 0.9:
        return "lower"
    if ratio <= 1.1:
        return "about the same"
    if ratio <= 2:
        return "higher"
    return "much higher"


def env_block(env):
    lines = ["## Environment", "", "| field | value |", "|---|---|"]
    for k in ("generated_utc", "commit", "commit_dirty", "os", "machine", "cpu", "logical_cpus", "memory_gib", "cpu_governor", "load_average_1m", "python", "go", "node", "meter", "seed", "arms_lock_sha256"):
        lines.append(f"| {k} | {env.get(k)} |")
    lines += ["", "### Arms", "", "| arm | pinned version | version line | pinned | executable sha256 | footprint | licence |", "|---|---|---|---|---|---|---|"]
    for a in arm_order(env["arms"]):
        i = env["arms"][a]
        fp = i.get("footprint") or {}
        size = f"{fp['bytes'] / 1e6:.1f} MB" + (f" + {fp['runtime']} {fp['runtime_bytes'] / 1e6:.0f} MB" if fp.get("runtime") and fp.get("runtime_bytes") else "") if fp else "n/a"
        if "skipped" in i:
            lines.append(f"| {a} | {i.get('pinned_version')} | skipped: {i['skipped']} | | | | {i.get('license')} |")
        else:
            lines.append(f"| {a} | {i.get('pinned_version')} | {i.get('version_line')} | {'yes' if i.get('pinned') else '**no (override)**'} | `{(i.get('executable_sha256') or '')[:16]}` | {size} | {i.get('license')} |")
    rec = [(a, env["arms"][a]) for a in arm_order(env["arms"]) if env["arms"][a].get("source_url")]
    if rec:
        lines += ["", "### Install record", "", "| arm | method | platform | source | artifact sha256 |", "|---|---|---|---|---|"]
        lines += [f"| {a} | {i.get('install_method')} | {i.get('platform')} | {i.get('source_url')} | `{(i.get('artifact_sha256') or '')[:16]}` |" for a, i in rec]
    notes = [(a, i.get("license_note")) for a, i in env["arms"].items() if i.get("license_note")]
    if notes:
        lines += ["", "Licence notes:", ""] + [f"- **{a}**: {n}" for a, n in notes]
    return "\n".join(lines)


def isolation_block(raw):
    iso = raw.get("isolation")
    if not iso:
        return ""
    lines = ["### Isolation environment", "", "Variables each harness is started with beyond the host's environment (the throwaway root is `<work>`). Credential-looking and proxy variables of the host are removed, and `HOME`, the XDG directories and every state directory point into the throwaway root. These settings are part of the configuration being compared.", ""]
    for a in arm_order(iso):
        v = iso[a]
        lines.append(f"- **{a}**: " + ", ".join(f"`{k}={val}`" for k, val in v["set"].items() if k not in ("NO_COLOR", "TERM", "NO_PROXY", "no_proxy")) + (f"; removed from the host environment: {', '.join(v['scrubbed'])}" if v.get("scrubbed") else ""))
    if raw.get("outside_writes"):
        lines.append(f"\n**State directories created under the real home by this run: {', '.join(raw['outside_writes'])}. The bench fails.**")
    if raw.get("outside_modified"):
        lines.append(f"\nState directories under the real home that changed during the run (possibly another process on the host): {', '.join(raw['outside_modified'])}.")
    return "\n".join(lines)


def egress_block(eg):
    lines = ["## Egress", "", f"Mode: **{eg['mode']}**. {eg.get('note', '')}", ""]
    hard = [v for v in eg["violations"] if not v.get("excluded")]
    excluded = [v for v in eg["violations"] if v.get("excluded")]
    if hard:
        lines.append(f"**{len(hard)} violation(s): a harness connected outside the loopback. The bench fails.**")
        lines += [f"- {json.dumps(v)}" for v in hard]
    if excluded:
        lines.append(f"{len(excluded)} third-party CLI run(s) contacted the network on their own. Each such arm is excluded from that scenario and listed under limitations; no key or login was added to make it work:")
        lines += [f"- {v['arm']} in {v.get('scenario') or v.get('phase') or v.get('fault')}: {', '.join(v['remotes'])}" for v in excluded]
    if not eg["violations"]:
        lines.append("No harness connected to any address outside the loopback. No live key or vendor login was used: every arm ran against the local mock endpoint with a dummy key and an isolated HOME.")
    return "\n".join(lines)


def table(groups, metrics, seed, csv_rows, bench, group):
    """groups: {arm: [sample dicts]}. One row per metric, one column per arm."""
    arms = arm_order(groups)
    lines = ["| metric | " + " | ".join(arms) + " |", "|---|" + "---|" * len(arms)]
    for m in metrics:
        vals = {a: [s.get(m) for s in groups[a]] for a in arms}
        if not any(any(isinstance(v, (int, float)) and not isinstance(v, bool) for v in vs) or any(isinstance(v, bool) for v in vs) for vs in vals.values()):
            continue
        lines.append(f"| {m} ({unit(m)}) | " + " | ".join(cell(vals[a], seed) for a in arms) + " |" if unit(m) else f"| {m} | " + " | ".join(cell(vals[a], seed) for a in arms) + " |")
        for a in arms:
            nums = [v for v in vals[a] if isinstance(v, (int, float)) and not isinstance(v, bool)]
            if nums:
                sm = stats.summarize(nums)
                lo, hi = stats.bootstrap_ci(nums, seed=seed) if len(nums) > 2 else (None, None)
                csv_rows.append([bench, group, a, m, sm["n"], sm["median"], sm["p90"], sm["min"], sm["max"], lo, hi])
    return "\n".join(lines)


AA_MARGIN = 0.05


def ratio_table(groups, metrics, seed):
    if REFERENCE not in groups:
        return ""
    others = [a for a in arm_order(groups) if a != REFERENCE and not a.endswith("-b")]
    if not others:
        return ""
    lines = ["| metric | " + " | ".join(others) + " |", "|---|" + "---|" * len(others)]
    for m in metrics:
        ref = [s[m] for s in groups[REFERENCE] if isinstance(s.get(m), (int, float)) and not isinstance(s.get(m), bool)]
        if len(ref) < 3 or not stats.median(ref):
            continue
        row = []
        for a in others:
            xs = [s[m] for s in groups[a] if isinstance(s.get(m), (int, float)) and not isinstance(s.get(m), bool)]
            if len(xs) < 3:
                row.append("n/a")
                continue
            r = stats.ratio_of_medians(xs, ref, seed=seed)
            row.append(f"{r['ratio']:.2f} [{r['ci'][0]:.2f}, {r['ci'][1]:.2f}]")
        lines.append(f"| {m} | " + " | ".join(row) + " |")
    return "\n".join(lines) if len(lines) > 2 else ""


def aa_table(groups, metrics, seed):
    pairs = [(a[:-2], a) for a in arm_order(groups) if a.endswith("-b") and a[:-2] in groups]
    if not pairs:
        return ""
    lines = ["| metric | " + " | ".join(f"{a} / {b}" for a, b in pairs) + " |", "|---|" + "---|" * len(pairs)]
    for m in metrics:
        row, any_ = [], False
        for a, b in pairs:
            xs = [s[m] for s in groups[b] if isinstance(s.get(m), (int, float)) and not isinstance(s.get(m), bool)]
            ys = [s[m] for s in groups[a] if isinstance(s.get(m), (int, float)) and not isinstance(s.get(m), bool)]
            if len(xs) < 3 or len(ys) < 3 or not stats.median(ys):
                row.append("n/a")
                continue
            r = stats.ratio_of_medians(xs, ys, seed=seed)
            ok = 1 - AA_MARGIN <= r["ci"][0] and r["ci"][1] <= 1 + AA_MARGIN
            row.append(f"{r['ratio']:.2f} [{r['ci'][0]:.2f}, {r['ci'][1]:.2f}] {'equivalent' if ok else 'noisy'}")
            any_ = True
        if any_:
            lines.append(f"| {m} | " + " | ".join(row) + " |")
    return "\n".join(lines) if len(lines) > 2 else ""


def qualitative(groups, metrics, seed):
    """Per metric, a label for each non-reference arm against Pi, only when the 95% intervals do not overlap."""
    if REFERENCE not in groups or len(groups) < 2:
        return ""
    lines = ["| metric | " + " | ".join(a for a in arm_order(groups) if a != REFERENCE) + " |", "|---|" + "---|" * (len(groups) - 1)]
    for m in metrics:
        ref = [s[m] for s in groups[REFERENCE] if isinstance(s.get(m), (int, float))]
        if len(ref) < 3:
            continue
        row = []
        for a in arm_order(groups):
            if a == REFERENCE:
                continue
            xs = [s[m] for s in groups[a] if isinstance(s.get(m), (int, float))]
            if len(xs) < 3:
                row.append("n/a")
                continue
            ratio = stats.median(xs) / stats.median(ref) if stats.median(ref) else None
            xl, xh = stats.bootstrap_ci(xs, seed=seed)
            rl, rh = stats.bootstrap_ci(ref, seed=seed)
            row.append(label(ratio, xh < rl or xl > rh))
        lines.append(f"| {m} | " + " | ".join(row) + " |")
    return "\n".join(lines) if len(lines) > 2 else ""


OVERHEAD_METRICS = ["first_request_ms", "first_frame_ms", "first_input_accepted_ms", "ready_ms", "ready_pss_mib", "wall_ms", "teardown_ms", "turn_gap_ms", "turn_cpu_ms", "turn_wall_ms", "turn_harness_added_ms", "cpu_ms", "peak_rss_mib", "peak_mem_mib", "ready_rss_mib", "input_to_request_ms", "input_to_reply_visible_ms", "request_bytes", "compactions", "cpu_ms_per_turn", "first_turn_cpu_ms", "last_turn_cpu_ms", "first_turn_wall_ms", "last_turn_wall_ms", "first_turn_harness_added_ms", "last_turn_harness_added_ms", "session_bytes", "total_wall_ms"]

OVERHEAD_METHOD = """## Method

Each CLI is installed at the version pinned in `bench/evals/arms.lock` into a throwaway directory, with its integrity and version checked, and deleted afterwards. It runs inside its own cgroup v2 (where the host allows; otherwise rusage) against a scripted model endpoint on the loopback that answers instantly, so what is measured is the harness. Arms are interleaved: each repetition rotates the arm order. Warm-up repetitions are discarded. A cell is the median with a 95% percentile-bootstrap interval over the repetitions (seeded). *First request* is spawn to the endpoint receiving the first request. *Turn gap* is the time from the end of one reply to the arrival of the next request (harness work plus the identical read tool). *Turn CPU* is the CPU of the whole process tree between two requests, read from the cgroup. *Peak RSS* sums each process's peak resident set, sampled every 25 ms; *peak mem* is the cgroup's `memory.peak`, which leaves out file pages that another cgroup already holds. *Harness added* is the time from the end of the last reply to the harness reporting the turn finished. The `pty` scenario drives a real pseudo-terminal at 120x40 and answers terminal queries; *first frame* is the first visible text, *ready* is 0.6 s without output. The `long-session` scenario sends the configured number of turns on one RPC process with automatic compaction on a 32k-token window; it fails if no compaction happens."""

WINDOWS_METER = ("The meter is a Windows Job Object (`windows-job`), not a cgroup. *Turn CPU* and `cpu_ms` are the job's user plus kernel time. *Peak mem* is the job's `PeakJobMemoryUsed`, committed memory rather than resident pages. "
                 "*Peak RSS* sums each process's peak working set: the started process's is read from its handle after it exits, its descendants' are sampled every 25 ms. "
                 "Windows has no PSS, so `ready_pss_mib` is the tree's private bytes (`PrivateUsage`, the commit charge). A process joins the job right after it starts, so a child it spawns in that first instant is not counted.")


def cpu_tick_note(groups, tick):
    """For a host that charges process CPU time in whole clock ticks (Windows): the pooled mean of each CPU metric, which a median of a few ticks hides."""
    if not tick:
        return ""
    metrics = list(dict.fromkeys(k for a in arm_order(groups) for s in groups[a] for k in s if "cpu" in k and unit(k) == "ms"))
    metrics = [m for m in metrics if m[:-3] + "_mean_ms" not in metrics]  # a run's median of ticks cannot be averaged back: use its mean
    rows = []
    for m in metrics:
        means = []
        for a in arm_order(groups):
            xs = [s[m] for s in groups[a] if isinstance(s.get(m), (int, float)) and not isinstance(s.get(m), bool)]
            if xs:
                means.append(f"{a} {fmt(sum(xs) / len(xs))}")
        rows.append(f"`{m}` {', '.join(means)}")
    if not rows:
        return ""
    return (f"This host charges process CPU time in whole clock ticks of {tick:g} ms, so every CPU value is a multiple of {tick:g} ms and a median of a few ticks is coarse. "
            "The pooled mean is finer (ms): " + "; ".join(rows) + f". Ratios and labels leave out a CPU median under {MIN_TICKS} ticks.")


MIN_TICKS = 10  # a CPU median of at least 10 ticks is within 10% of its value


def coarse_cpu(medians, metric, tick):
    """True when, on a host that charges CPU time in whole ticks, a CPU metric's smallest median is under MIN_TICKS ticks: ranking it ranks the tick."""
    return bool(tick) and "cpu" in metric and unit(metric) == "ms" and not metric.endswith("_mean_ms") and (not medians or min(medians) < MIN_TICKS * tick)


def ranked(groups, metrics, tick):
    """The metrics a ratio or label may rank: without the coarse CPU medians of a tick-charging host."""
    def medians(m):
        return [stats.median(xs) for a in groups if (xs := [s[m] for s in groups[a] if isinstance(s.get(m), (int, float)) and not isinstance(s.get(m), bool)])]
    return [m for m in metrics if not coarse_cpu(medians(m), m, tick)]


def overhead(raw, out, seed, csv_rows):
    md = ["# Overhead bench", "", "No model was called and no live key was used.", "", env_block(raw["env"]), "", isolation_block(raw), "", egress_block(raw["egress"]), "", OVERHEAD_METHOD, ""]
    opts = raw["options"]
    md.append(f"Options: runs={opts['runs']} warmup={opts['warmup']} rpc_turns={opts['rpc_turns']} long_turns={opts['long_turns']} seed={seed}\n")
    modes = sorted({s["meter"] for by in raw["samples"].values() for samples in by.values() for s in samples if s.get("meter")})
    if len(modes) > 1:
        md.append(f"**Meter modes differ between samples ({', '.join(modes)})**: a cgroup could not be created for some runs, so CPU and memory of those runs come from rusage and RSS sums. Compare cells only within one mode.\n")
    if "windows-job" in modes:
        md.append(WINDOWS_METER + "\n")
    tick = raw["env"].get("cpu_tick_ms")
    for scenario in opts["scenarios"]:
        groups = raw["samples"].get(scenario)
        md += [f"## {scenario}", ""]
        if not groups:
            md += ["No arm produced a result.", ""]
        else:
            md += [table(groups, OVERHEAD_METRICS, seed, csv_rows, "overhead", scenario), ""]
            note = cpu_tick_note(groups, tick)
            if note:
                md += [note, ""]
            q = qualitative(groups, ranked(groups, ["first_request_ms", "first_frame_ms", "wall_ms", "turn_gap_ms", "turn_cpu_ms", "peak_rss_mib", "turn_harness_added_ms"], tick), seed)
            if q:
                md += ["Qualitative labels against Pi 1.1.0 (only where the 95% intervals do not overlap):", "", q, ""]
            r = ratio_table(groups, ranked(groups, OVERHEAD_METRICS, tick), seed)
            if r:
                md += ["Ratio of medians against Pi 1.1.0 (arm / Pi) with a 95% bootstrap interval; below 1 is lower than Pi. Every metric is listed, including those PiG loses:", "", r, ""]
            aa = aa_table(groups, ranked(groups, OVERHEAD_METRICS, tick), seed)
            if aa:
                md += [f"A/A noise floor: each arm that ran twice (`-b` is a second copy of the same binary with its own home) against itself. Two runs are declared equivalent when the interval of the ratio lies inside {1 - AA_MARGIN:.2f}-{1 + AA_MARGIN:.2f}; a difference between arms smaller than this floor is not a finding.", "", aa, ""]
    footprints = {a: i.get("footprint") for a, i in raw["env"]["arms"].items() if i.get("footprint")}
    if footprints:
        md += ["## Binary size", "", "| arm | installed bytes | runtime |", "|---|---|---|"]
        for a in arm_order(footprints):
            fp = footprints[a]
            md.append(f"| {a} | {fp['bytes']:,} | {fp['runtime'] or 'none (static)'} |")
            csv_rows.append(["overhead", "footprint", a, "installed_bytes", 1, fp["bytes"], fp["bytes"], fp["bytes"], fp["bytes"], None, None])
        md.append("")
    md += limitations(raw)
    return "\n".join(md)


def limitations(raw):
    md = ["## Limitations, exclusions and failures", ""]
    empty = True
    for arm, d in sorted(raw.get("limitations", {}).items()):
        for sc, text in d.items():
            kept = len(raw.get("samples", {}).get(sc, {}).get(arm, []))
            if kept:
                md.append(f"- **{arm}** had a failed run in `{sc}`; the {kept} successful run(s) are reported. Exact error: `{text[:300]}`.")
            else:
                md.append(f"- **{arm}** excluded from `{sc}`: the CLI would not run headless against the mock without a real login or key, contacted the network on its own, or failed. Exact error: `{text[:300]}`. No key was added.")
            empty = False
    for arm, d in sorted(raw.get("skipped", {}).items()):
        for sc, text in d.items():
            md.append(f"- **{arm}** skipped `{sc}`: {text}")
            empty = False
    for sc, by in sorted(raw.get("failures", {}).items()):
        for arm, fails in by.items():
            first = fails[0]["failure"] if isinstance(fails[0], dict) else fails[0]
            md.append(f"- **FAILURE** {arm} in `{sc}` ({len(fails)}x): {first}")
            empty = False
    if empty:
        md.append("None.")
    return md + [""]


def reliability(raw, out, seed, csv_rows):
    md = ["# Reliability bench", "", "No model was called and no live key was used. Expectations come from the Pi arm.", "", env_block(raw["env"]), "", egress_block(raw["egress"]), ""]
    arms = arm_order({a for by in raw["verdicts"].values() for a in by})
    md += ["## Pass or fail per fault per arm", "", "| fault | " + " | ".join(arms) + " |", "|---|" + "---|" * len(arms)]
    for name, by in raw["verdicts"].items():
        md.append(f"| {name} | " + " | ".join(("PASS" if by[a]["pass"] else "**FAIL**") if a in by else "n/a" for a in arms) + " |")
        for a in arms:
            if a in by:
                csv_rows.append(["reliability", name, a, "pass", 1, int(by[a]["pass"]), None, None, None, None, None])
    md += ["", "PASS means the invariants held on every repetition, and for PiG and Pi arms the signature equals the Pi 1.1.0 signature. `n/a` means the fault does not apply to that CLI. Claude Code, Codex and Copilot are scored on invariants only.", "", "## Reasons for failures", ""]
    bad = [(n, a, v) for n, by in raw["verdicts"].items() for a, v in by.items() if not v["pass"]]
    md += [f"- **{n} / {a}**: {'; '.join(v['reasons'])}" for n, a, v in bad] or ["None."]
    md += ["", "## Signatures observed", "", "| fault | arm | signature |", "|---|---|---|"]
    for name, by in raw["verdicts"].items():
        for a in arms:
            if a in by:
                md.append(f"| {name} | {a} | `{json.dumps(by[a]['signature'], sort_keys=True)}` |")
    return "\n".join(md)


LH_METRICS = ["first_output_ms", "open_settled_ms", "open_rpc_ms", "open_rpc_cpu_ms", "resume_rpc_ms", "resume_rpc_cpu_ms", "resume_tui_ms", "resume_tui_cpu_ms",
              "open_peak_rss_mib", "steady_rss_mib", "open_rpc_steady_rss_mib", "peak_rss_mib", "rpc_peak_rss_mib", "open_bytes", "key_idle_ms_p50", "key_idle_ms_p95", "key_streaming_ms_p50",
              "key_streaming_ms_p95", "stream_frames", "frame_interval_ms_p50", "frame_interval_ms_p95", "frame_cpu_ms", "frame_bytes_p50", "resize_w100_ms", "resize_w100_cpu_ms",
              "resize_w100_bytes", "resize_w100_clears_scrollback", "resize_w120_ms", "resize_w120_bytes", "resize_h30_ms", "resize_h30_bytes", "resize_h30_clears_scrollback",
              "append_turn_wall_ms", "append_turn_cpu_ms", "append_harness_added_ms", "append_request_bytes", "append_file_growth_bytes", "alt_screen"]


def longhistory(raw, out, seed, csv_rows):
    md = ["# Long-history bench", "", "No model was called and no live key was used.", "", env_block(raw["env"]), "", egress_block(raw["egress"]), ""]
    md += [(__import__("pigbench.longhistory", fromlist=["x"]).__doc__ or "").split('"""')[0], ""]
    md += ["## Sessions", "", "| case | entries | file bytes | history tokens | tokens in context (what the TUI renders) | compactions | sha256 |", "|---|---|---|---|---|---|---|"]
    for c, f in raw["sessions"].items():
        md.append(f"| {c} | {f['entries']:,} | {f['bytes']:,} | {f['history_tokens']:,} | {f['context_tokens']:,} | {f['compactions']} | `{f['sha256'][:16]}` |")
    md += ["", f"Generated with seed {seed}; the same seed rebuilds byte-identical files (the digest is recorded above). Tokens are characters / 4.", ""]
    for key, groups in raw["samples"].items():
        md += [f"## {key}", "", table(groups, LH_METRICS, seed, csv_rows, "longhistory", key), ""]
    md += limitations(raw)
    return "\n".join(md)


KINDS = {"overhead": overhead, "reliability": reliability, "long-history": longhistory}


def render(directory):
    with open(os.path.join(directory, "raw.json")) as f:
        raw = json.load(f)
    seed = raw["env"].get("seed")
    rows = []
    kind = KINDS.get(raw["kind"])
    if kind is None:
        from . import durable_report
        kind = durable_report.KINDS[raw["kind"]]
    text = kind(raw, directory, seed, rows)
    text += f"\n\n---\nRegenerate: `make bench-{raw['kind'].replace('long-history', 'longhistory')}` with seed {seed}; arms.lock sha256 `{raw['env'].get('arms_lock_sha256')}`; commit `{raw['env'].get('commit')}`.\n"
    with open(os.path.join(directory, "report.md"), "w") as f:
        f.write(text)
    with open(os.path.join(directory, "summary.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["bench", "group", "arm", "metric", "n", "median", "p90", "min", "max", "ci95_lo", "ci95_hi"])
        w.writerows(rows)
    print(f"report: {os.path.join(directory, 'report.md')}")

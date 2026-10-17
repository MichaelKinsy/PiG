# SPDX-License-Identifier: MIT
"""Report for the durable bench."""
from . import report as R
from . import stats


def verdict(ratio, rules):
    """pig/pi ratio of pooled warm medians, in durable-report's terms: a loss is more than 3% slower, a win is 1.3x faster or better."""
    if ratio is None:
        return "n/a"
    if ratio > rules["loss_if_slower_than"]:
        return "loss"
    if ratio <= 1 / rules["win_if_faster_by"]:
        return "win"
    if ratio < rules["slightly_better_below"]:
        return "slightly better"
    return "tie"


def pooled_warm(samples):
    return [t for s in samples for t in s["turns"][1:]]


def durable(raw, out, seed, csv_rows):
    w = raw["workload"]
    md = ["# Durable bench", "", "No model was called and no live key was used.", "", R.env_block(raw["env"]), ""]
    md += ["## Workload", "", f"{w['name']}. Sizes {w['sizes']}, {w['measured_turns']} measured turns of {w['tool_calls_per_measured_turn']} tool calls, history pattern {w['history_pattern_tool_calls']} tool calls per turn, "
           f"cold = {w['cold']}, warm = {w['warm']}. CPU-idle gate: busy fraction at most {w['cpu_idle_gate']['max_busy_fraction']} over {w['cpu_idle_gate']['window_s']} s. {w['reference']}.", ""]
    md += ["Runtimes: " + "; ".join(f"**{k}**: {v}" for k, v in w["runtimes"].items()), ""]
    md += ["## Latency (ms), median with 95% bootstrap interval over samples", "", "| turns | metric | pig-durable | pi-durable | ratio pig/pi |", "|---|---|---|---|---|"]
    for size, by in raw["samples"].items():
        for m in ("cold_ms", "warm_ms", "open_ms", "peak_rss_mib", "cpu_s", "bytes"):
            vals = {rt: [s[m] for s in by.get(rt, []) if s.get(m) is not None] for rt in ("pig-durable", "pi-durable")}
            cells = [R.cell(vals[rt], seed) for rt in ("pig-durable", "pi-durable")]
            ratio = (stats.median(vals["pig-durable"]) / stats.median(vals["pi-durable"])) if vals["pig-durable"] and vals["pi-durable"] and stats.median(vals["pi-durable"]) else None
            md.append(f"| {size} | {m} | {cells[0]} | {cells[1]} | {R.fmt(ratio)} |")
            for rt, vs in vals.items():
                if vs:
                    sm = stats.summarize(vs)
                    csv_rows.append(["durable", size, rt, m, sm["n"], sm["median"], sm["p90"], sm["min"], sm["max"], None, None])
    rules = w["reporting"]
    md += ["", "## Warm turns, pooled over samples (ms)", "",
           f"Every sample contributes its nine warm turns. A cell needs {rules['min_warm_turns_per_cell']} of them to meet durable-report's rule ({rules['rounds_for_that']} rounds); cells below that are marked and are not for publication. "
           f"Verdict on the ratio of the pooled medians: loss if pig is more than {round((rules['loss_if_slower_than'] - 1) * 100)}% slower, win if {rules['win_if_faster_by']}x faster or better, slightly better below {rules['slightly_better_below']}, otherwise tie. Only ratios from this session are claims.", "",
           "| turns | runtime | n warm | p50 | p95 | max | p50 95% interval |", "|---|---|---|---|---|---|---|"]
    verdicts = []
    for size, by in raw["samples"].items():
        pooled = {rt: pooled_warm(by.get(rt, [])) for rt in ("pig-durable", "pi-durable")}
        for rt, vals in pooled.items():
            if not vals:
                continue
            lo, hi = stats.bootstrap_ci(vals, seed=seed) if len(vals) >= 3 else (None, None)
            below = " (below the rule)" if len(vals) < rules["min_warm_turns_per_cell"] else ""
            md.append(f"| {size} | {rt} | {len(vals)}{below} | {R.fmt(stats.median(vals))} | {R.fmt(stats.percentile(vals, 95))} | {R.fmt(max(vals))} | [{R.fmt(lo)}, {R.fmt(hi)}] |")
            csv_rows.append(["durable", size, rt, "warm_pooled_ms", len(vals), stats.median(vals), stats.percentile(vals, 95), min(vals), max(vals), lo, hi])
        if pooled["pig-durable"] and pooled["pi-durable"] and stats.median(pooled["pi-durable"]):
            ratio = stats.median(pooled["pig-durable"]) / stats.median(pooled["pi-durable"])
            verdicts.append(f"| {size} | {R.fmt(ratio)} | {verdict(ratio, rules)} |")
    if verdicts:
        md += ["", "| turns | ratio pig/pi (warm p50) | verdict |", "|---|---|---|"] + verdicts
    md += ["", rules["rules"], "", w.get("not_measured_here", ""), ""]
    gates = raw["gate"]
    passed = sum(1 for g in gates if g["passed"])
    md += ["", "## CPU-idle gate", "", f"{passed} of {len(gates)} samples started with the gate passed" + (f"; the busiest reading was {max(g['busy'] for g in gates if 'busy' in g)}." if any('busy' in g for g in gates) else "; the gate was off."), ""]
    md += R.limitations(raw)
    return "\n".join(md)


def soak(raw, out, seed, csv_rows):
    o = raw["options"]
    md = ["# Soak bench", "", "No model was called and no live key was used.", "", R.env_block(raw["env"]), "", R.egress_block(raw["egress"]), ""]
    md += [f"Every arm ran the same scripted turns at the same time for {o['hours']:g} h, one RPC process each, with compaction on a 32k-token window so the transcript stays bounded: growth is a leak, not the work. "
           f"The process tree's processes, threads, descriptors and memory were sampled every {o['interval_s']:g} s. A series is flat when its second half stays within a band: processes, threads and descriptors within max({o['bands']['count'][0]}, "
           f"{o['bands']['count'][1] * 100:g}% of the median), resident memory within {o['bands']['rss'] * 100:g}% of the median. Goroutines are not sampled live (PiG exposes no count); see `PIG_PROFILE=goroutine`.", "",
           "| arm | turns | samples | verdict | processes | threads | fds | rss MiB | pss MiB |", "|---|---|---|---|---|---|---|---|---|"]
    for aid in R.arm_order(raw["arms"]):
        a = raw["arms"][aid]
        cells = []
        for m in ("processes", "threads", "fds", "rss_mib", "pss_mib"):
            v = a["verdict"][m]
            cells.append("n/a" if v["flat"] is None else f"{R.fmt(v['median'])} (spread {R.fmt(v['spread'])}, {R.fmt(v['slope_per_h'])}/h) {'flat' if v['flat'] else '**growing**'}")
            for r in a["series"]:
                if r.get(m) is not None:
                    csv_rows.append(["soak", m, aid, "t=%s" % r["t_s"], 1, r[m], r[m], r[m], r[m], None, None])
        state = ("failed: " + a["failure"][:80]) if a["failure"] else {True: "flat", False: "**not flat**", None: "undecided (too few samples)"}[a["flat"]]
        md.append(f"| {aid} | {a['turns']} | {a['samples']} | {state} | " + " | ".join(cells) + " |")
    md += [""]
    return "\n".join(md)


def startup(raw, out, seed, csv_rows):
    o = raw["options"]
    tool = raw["env"]["tools"]["hyperfine"]
    groups = raw["samples"]
    memory = any("peak_rss_mib" in s for rows in groups.values() for s in rows)
    metrics = ["wall_ms", "cpu_ms"] + (["peak_rss_mib"] if memory else []) + (["instructions"] if raw.get("counters") else [])
    md = ["# Startup bench (hyperfine)", "", "No model was called and no live key was used.", "", R.env_block(raw["env"]), "",
          f"Measured with {tool['version_line']} (`{tool['sha256'][:16]}`, from {tool['source_url']}): `<arm> --version` with `--shell=none`, {o['rounds']} rounds of {o['runs_per_round']} runs per arm after a warm-up, the arm order rotating every round, all runs pooled. "
          "`wall_ms` is exec to exit, `cpu_ms` user plus system" + (", `peak_rss_mib` the kernel's peak resident set of the process." if memory else "."), ""]
    if not memory:
        md += [f"Peak memory is not reported: {raw.get('memory_reason', 'hyperfine recorded no memory metric')}. The overhead bench's `version` scenario meters the same command's peak memory.", ""]
    note = R.cpu_tick_note(groups, raw["env"].get("cpu_tick_ms"))
    if note:
        md += [note, ""]
    if raw.get("counters"):
        md.append("Instruction counters were available and are listed; they rank an implementation against itself only, never across languages.")
    else:
        md.append(f"Instruction counters were not available on this host and are not reported: {raw.get('counters_reason', 'not requested')}")
    md += ["", R.table(groups, metrics, seed, csv_rows, "startup", "version"), ""]
    ratios = R.ratio_table(groups, R.ranked(groups, metrics, raw["env"].get("cpu_tick_ms")), seed)
    if ratios:
        md += ["Ratio of medians against Pi 1.1.0 (arm / Pi) with a 95% bootstrap interval; below 1 is lower than Pi. Every metric is listed, including those PiG loses:", "", ratios, ""]
    md += R.limitations(dict(raw, failures={"startup": raw["failures"]} if raw.get("failures") else {}))  # startup keeps {arm: [errors]} for its one scenario
    return "\n".join(md)


KINDS = {"durable": durable, "soak": soak, "startup": startup}

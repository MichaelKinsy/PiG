#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Derived from the bolt-host spike (bench/bolt-host/bench/summarize.py). Summarize bench.py runs.jsonl into a Markdown table: medians with [min-max] over rounds. usage: summarize.py runs.jsonl"""
import json, statistics, sys
runs = [json.loads(l) for l in open(sys.argv[1])]
arms = []
for r in runs:
    if r["arm"] not in arms: arms.append(r["arm"])
LABEL = {"node": "Node", "bun": "Bun 1.4.2", "aot": "aot (set compiled in)"}
def fmt(vals, unit="", nd=0):
    vals = [v for v in vals if v is not None]
    if not vals: return "n/a"
    m = statistics.median(vals); f = (lambda x: f"{x:.{nd}f}")
    return f"{f(m)}{unit} [{f(min(vals))}-{f(max(vals))}]"
rows = [("Runs ok / total", lambda rs: None),
        ("Launch to ready (ms)", lambda rs: [r["ready_ms"] for r in rs]),
        ("Extension private dirty MB at ready", lambda rs: [r["mem_ready"].get("ext_private_dirty_kb", 0) / 1024 for r in rs]),
        ("Extension PSS MB at ready", lambda rs: [r["mem_ready"]["ext_pss_kb"] / 1024 for r in rs]),
        ("Extension private dirty MB after 5 prompts", lambda rs: [r["mem_after5"].get("ext_private_dirty_kb", 0) / 1024 for r in rs]),
        ("Extension PSS MB after 5 prompts", lambda rs: [r["mem_after5"]["ext_pss_kb"] / 1024 for r in rs]),
        ("pig PSS MB after 5 prompts", lambda rs: [r["mem_after5"]["pig_pss_kb"] / 1024 for r in rs]),
        ("/words in extension (ms)", lambda rs: [r["words_ext_ms"] for r in rs]),
        ("/words extension CPU (ms)", lambda rs: [r["words_ext_cpu_ms"] for r in rs]),
        ("/words wall from prompt (ms)", lambda rs: [r["words_wall_ms"] for r in rs]),
        ("Tool turn gap (ms, 5 per run)", lambda rs: [g for r in rs for g in r["tool_gap_ms"]]),
        ("Prompt wall (ms, 5 per run)", lambda rs: [w for r in rs for w in r["prompt_wall_ms"]])]
print("| Metric | " + " | ".join(LABEL.get(a, a) for a in arms) + " |")
print("|---|" + "---:|" * len(arms))
for name, get in rows:
    cells = []
    for a in arms:
        rs = [r for r in runs if r["arm"] == a]
        ok = [r for r in rs if "error" not in r]
        if name.startswith("Runs"): cells.append(f"{len(ok)}/{len(rs)}"); continue
        cells.append(fmt(get(ok), nd=1 if "MB" in name else 0))
    print(f"| {name} | " + " | ".join(cells) + " |")
loads = [float(r["loadavg"][0]) for r in runs if "loadavg" in r]
if loads: print(f"\nHost load average (1 min) during runs: median {statistics.median(loads):.0f}, range {min(loads):.0f}-{max(loads):.0f}.")

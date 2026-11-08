#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""A/A floor and era delta of a basket.py session with repeat arms (bench/bolt-aot/basket.py): for each set and metric, the paired A/A differences of every arm and its `2` repeat
(per round), and the BEFORE-to-AFTER change of the pooled arm pairs (bun+bun2 against bun-a+bun-a2, node+node2 against node-a+node-a2): medians, the median of the per-round
differences with a 95% bootstrap interval over rounds, and a two-sided Mann-Whitney test (normal approximation with tie correction) of the pooled samples.
usage: basket_aa.py runs.jsonl"""
import json, random, statistics, sys
runs = [json.loads(l) for l in open(sys.argv[1])]
runs = [r for r in runs if r.get("type") == "run" and r.get("round", -1) >= 0 and "error" not in r]
HZ = 100
metrics = {"launch to ready (ms)": lambda r: r["ready_ms"], "Go host CPU to ready (ms)": lambda r: (r["ready_snap"]["pig"]["utime"] + r["ready_snap"]["pig"]["stime"]) * 1000 / HZ,
           "extension host CPU to ready (ms)": lambda r: (r["ready_snap"]["ext"]["utime"] + r["ready_snap"]["ext"]["stime"]) * 1000 / HZ,
           "turn 1 wall (ms)": lambda r: r["turn_wall_ms"][0], "steady turn wall (ms)": lambda r: statistics.median(r["turn_wall_ms"][5:])}
def val(s, arm, rnd, f):
    for r in runs:
        if r["set"] == s and r["arm"] == arm and r["round"] == rnd:
            return f(r)
def mw(x, y):  # Mann-Whitney U, two-sided, normal approximation with tie correction
    import math
    allv = sorted([(v, 0) for v in x] + [(v, 1) for v in y])
    ranks = {}
    i = 0
    while i < len(allv):
        j = i
        while j < len(allv) and allv[j][0] == allv[i][0]: j += 1
        for k in range(i, j): ranks[k] = (i + j + 1) / 2
        i = j
    r1 = sum(ranks[k] for k, (v, g) in enumerate(allv) if g == 0)
    n1, n2 = len(x), len(y)
    u = r1 - n1 * (n1 + 1) / 2
    tie = 0; i = 0
    while i < len(allv):
        j = i
        while j < len(allv) and allv[j][0] == allv[i][0]: j += 1
        tie += (j - i) ** 3 - (j - i); i = j
    n = n1 + n2
    sd = math.sqrt(n1 * n2 / 12 * ((n + 1) - tie / (n * (n - 1)))) or 1
    z = (u - n1 * n2 / 2) / sd
    return 2 * (1 - 0.5 * (1 + math.erf(abs(z) / math.sqrt(2))))
random.seed(1)
for s in ("five", "ten"):
    print("##", s)
    for name, f in metrics.items():
        row = {}
        for pair in (("bun", "bun2"), ("bun-a", "bun-a2"), ("node", "node2"), ("node-a", "node-a2")):
            d = [val(s, pair[0], r, f) - val(s, pair[1], r, f) for r in range(12) if val(s, pair[0], r, f) is not None and val(s, pair[1], r, f) is not None]
            row[pair[0]] = d
        out = [name]
        for a, b in (("bun", "bun-a"), ("node", "node-a")):
            # per-round era delta: mean of the two repeats before minus after
            dl = []
            for r in range(12):
                v = [val(s, k, r, f) for k in (a, a + "2", b, b + "2")]
                if None in v: continue
                dl.append((v[2] + v[3]) / 2 - (v[0] + v[1]) / 2)
            med = statistics.median(dl)
            boots = sorted(statistics.median(random.choices(dl, k=len(dl))) for _ in range(10000))
            lo, hi = boots[250], boots[9750]
            x = [val(s, k, r, f) for k in (a, a + "2") for r in range(12)]; y = [val(s, k, r, f) for k in (b, b + "2") for r in range(12)]
            x = [v for v in x if v is not None]; y = [v for v in y if v is not None]
            out.append(f"{a}->{b}: median {statistics.median(x):.0f} -> {statistics.median(y):.0f} (pooled n={len(x)}/{len(y)}); per-round delta median {med:+.0f} ms, 95% bootstrap CI [{lo:+.0f}, {hi:+.0f}]; Mann-Whitney p={mw(x, y):.3g}")
        aa = {k: (statistics.median(v), statistics.median([abs(t) for t in v]), max(abs(t) for t in v)) for k, v in row.items()}
        out.append("A/A paired diffs (median, median |d|, max |d|): " + "; ".join(f"{k}-{k}2 {m:+.1f}, {a:.1f}, {mx:.1f}" for k, (m, a, mx) in aa.items()))
        print("\n  ".join(out))

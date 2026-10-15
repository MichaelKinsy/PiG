#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Aggregates stream_shapes.py rows.

  shapes_report.py shapes.jsonl --base pig-tip [--pair ARM=COPY ...]

Prints, in this order: every sample that did not run (by build and reason); per cell (shape, wire API, size) each arm's CPU s median
(min-max) n and its A/A delta (|arm - copy| as % of the lower median); per other arm and size the range of other/base median ratios
over the cells, with the cells at both ends; per arm and size the Chat Completions vs Anthropic delta next to the arm's A/A floor.
Every range a summary quotes comes from here, not from hand arithmetic. Cells, shapes, APIs and sizes come from the rows. An A/A copy
is the build named ARM-aa, ARM-b or ARM-copy, or one given with --pair. A cell with no sample prints "-"."""
import argparse
import collections
import json
import statistics as st

COPY_SUFFIXES = ("-aa", "-b", "-copy")
SHAPE_ORDER = ("prose", "code", "tables", "lists")  # stream_shapes.SHAPES
API_ORDER = ("completions", "anthropic")  # stream_shapes.APIS


def load(path):
    with open(path) as f:
        return [json.loads(line) for line in f if line.strip()]


def pairs_of(builds, explicit):
    """Primary arm -> its A/A copy, in first-seen order; a build that is a copy is not a primary arm."""
    pairs = dict(explicit)
    for b in builds:
        for suffix in COPY_SUFFIXES:
            if b.endswith(suffix) and b[: -len(suffix)] in builds and b[: -len(suffix)] not in pairs:
                pairs[b[: -len(suffix)]] = b
    copies = set(pairs.values())
    return {b: pairs.get(b) for b in builds if b not in copies}


def ordered(values, first=()):
    seen = list(dict.fromkeys(values))
    return [v for v in first if v in seen] + [v for v in seen if v not in first]


def aggregate(rows, base=None, explicit=()):
    samples = collections.defaultdict(list)
    failed = collections.Counter()
    for r in rows:
        if "cpu_s" in r:
            samples[(r["build"], r["shape"], r["api"], r["size"])].append(r["cpu_s"])
        else:
            failed[(r["build"], r.get("error", "no cpu_s"))] += 1
    builds = ordered(r["build"] for r in rows)
    shapes, apis = ordered((r["shape"] for r in rows), SHAPE_ORDER), ordered((r["api"] for r in rows), API_ORDER)
    sizes = sorted(set(r["size"] for r in rows if "size" in r))
    arms = pairs_of(builds, explicit)
    base = base or next(iter(arms), None)
    med = {k: st.median(v) for k, v in samples.items()}

    def aa(arm, cell):
        a, b = med.get((arm, *cell)), med.get((arms.get(arm), *cell))
        return None if a is None or b is None else abs(a - b) / min(a, b) * 100

    cells = [(sh, api, size) for sh in shapes for api in apis for size in sizes]
    table = []
    for cell in cells:
        table.append((cell, [(arm, med.get((arm, *cell)), samples.get((arm, *cell), []), aa(arm, cell)) for arm in arms]))
    ratios = []
    for arm in arms:
        if arm == base:
            continue
        for size in sizes:
            rs = [(med[(arm, sh, api, size)] / med[(base, sh, api, size)], sh, api) for sh in shapes for api in apis
                  if (arm, sh, api, size) in med and (base, sh, api, size) in med]
            if rs:
                ratios.append((arm, size, min(rs), max(rs), len(rs)))
    wire = []
    if "completions" in apis and "anthropic" in apis:
        for arm in arms:
            for sh in shapes:
                for size in sizes:
                    c, a = med.get((arm, sh, "completions", size)), med.get((arm, sh, "anthropic", size))
                    if c is not None and a is not None:
                        floor = max((x for x in (aa(arm, (sh, "completions", size)), aa(arm, (sh, "anthropic", size))) if x is not None), default=None)
                        wire.append((arm, sh, size, abs(c - a) / min(c, a) * 100, floor))
    return {"failed": failed, "table": table, "ratios": ratios, "wire": wire, "base": base, "arms": arms}


def render(agg):
    out = [f"samples that did not run: {sum(agg['failed'].values())}"]
    out += [f"  {b}: {n} x {why}" for (b, why), n in sorted(agg["failed"].items())]
    out.append("CPU s median (min-max) n, A/A delta per cell:")
    for (sh, api, size), row in agg["table"]:
        cols = []
        for arm, m, v, d in row:
            cols.append(f"{arm} -" if m is None else f"{arm} {m:.2f} ({min(v):.2f}-{max(v):.2f}) n={len(v)} AA{'-' if d is None else f'{d:.0f}%'}")
        out.append(f"{sh} {api} {size} " + " | ".join(cols))
    out.append(f"other/{agg['base']} median ratio over cells (>1: {agg['base']} uses less CPU):")
    for arm, size, lo, hi, n in agg["ratios"]:
        out.append(f"  {arm} {size}: {lo[0]:.2f}x ({lo[1]} {lo[2]}) to {hi[0]:.2f}x ({hi[1]} {hi[2]}), {n} cells")
    if agg["wire"]:
        out.append("Chat Completions vs Anthropic per arm, shape, size (delta; A/A floor = larger of the two cells' A/A):")
        for arm, sh, size, d, floor in agg["wire"]:
            out.append(f"  {arm} {sh} {size}: {d:.1f}% (A/A {'-' if floor is None else f'{floor:.1f}%'}){' OUTSIDE A/A' if floor is not None and d > floor else ''}")
    return "\n".join(out)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("rows")
    ap.add_argument("--base", help="arm the ratios are taken against (default: the first primary arm in the rows)")
    ap.add_argument("--pair", action="append", default=[], help="ARM=COPY (repeatable)")
    a = ap.parse_args()
    print(render(aggregate(load(a.rows), a.base, [p.split("=", 1) for p in a.pair])))


if __name__ == "__main__":
    main()

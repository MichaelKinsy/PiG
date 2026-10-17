#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Memory half of the per-TEST-TIP scaling sweep.

Runs test/perfgate's TestMemoryScalingSweep* (memory_sweep_test.go: Pi-Bolt's long row at 0/5/50 prompts, its tmux row at 1/5/50, a
resumed session at 0/200/2000 turns of history behind a fixed 8-turn context) several times, takes medians, and flags:
  superlinear   retained bytes grow faster from 1x to 10x than from 0 to 1x (per unit of history), beyond noise;
  history       retained bytes follow the history, not the context: the resumed row costs more than RESUME_SLOPE per history byte (M7's bound);
  regression    (compare) a point or a per-unit slope rose against the previous TEST-TIP beyond noise.
"retained" is heap live less the context pig last sent (long, resume), or the idle process's live heap and Go memory not returned (tmux).

  bench/memsweep/memsweep.py run --out tip.json [--repo .] [--runs 3] [--cpus 192-195] [--gomaxprocs default|N]
  bench/memsweep/memsweep.py compare prev.json cur.json
Exit status: 0 no flags, 1 flags, 2 usage or run failure. Linux only (the test is //go:build linux). The test builds pig under $TMPDIR:
keep it on disk, not tmpfs. For --gomaxprocs default, pin more CPUs than pig's cap (e.g. 14): on a host whose cgroup cpusets change, a
mask of 2 or fewer lets the runtime follow a reset mask, and the points that did are dropped.
"""
import argparse, json, os, statistics, subprocess, sys
from pathlib import Path

SUPERLINEAR = 1.25   # 10x step may cost at most 1.25x the 1x step's per-unit growth
RESUME_SLOPE = 0.02  # retained bytes per history byte (M7 red test's bound)
REL_NOISE = 0.01     # a regression must also exceed 1% of the previous value
PIG_CAP = 2         # internal/procpolicy.Cap


def metrics(p):
    out = {}
    if p["scenario"] == "tmux_idle":
        out["heap_live"] = p["heap_live_bytes"]
        out["go_nonreleased"] = p.get("go_nonreleased_bytes", 0)
    else:
        out["retained"] = p["heap_live_bytes"] - p["context_bytes"]
        out["heap_live"] = p["heap_live_bytes"]
    return out


def summarize(points):
    by = {}
    for p in points:
        by.setdefault((p["scenario"], p["n"]), []).append(p)
    rows = {}
    for (sc, n), ps in sorted(by.items()):
        ms = [metrics(p) for p in ps]
        row = {"n": n, "runs": len(ps), "history_bytes": statistics.median(p["history_bytes"] for p in ps),
               "context_bytes": statistics.median(p["context_bytes"] for p in ps)}
        for k in ms[0]:
            xs = [m[k] for m in ms]
            row[k] = statistics.median(xs)
            row[k + "_spread"] = max(xs) - min(xs)
        rows.setdefault(sc, []).append(row)
    summary, flags = {}, []
    for sc, rs in rows.items():
        rs.sort(key=lambda r: r["n"])
        entry = {"points": rs}
        if len(rs) == 3:
            r0, r1, r10 = rs
            for k in [k for k in r0 if k + "_spread" in r0]:
                noise = 2 * max(r[k + "_spread"] for r in rs)
                lo, hi = r1[k] - r0[k], r10[k] - r1[k]
                units = (r10["n"] - r1["n"]) / (r1["n"] - r0["n"])
                entry[k + "_per_unit_1x"] = lo / (r1["n"] - r0["n"])
                entry[k + "_per_unit_10x"] = hi / (r10["n"] - r1["n"])
                dh = r10["history_bytes"] - r1["history_bytes"]
                entry[k + "_per_history_byte"] = hi / dh if dh else None
                if hi > SUPERLINEAR * units * max(lo, 0) + noise:
                    flags.append(f"superlinear {sc}.{k}: 0->1x +{lo:,.0f} B, 1x->10x +{hi:,.0f} B "
                                 f"(> {SUPERLINEAR} x {units:g} x the 1x step + noise {noise:,.0f})")
            if sc == "resume" and entry.get("retained_per_history_byte") is not None and entry["retained_per_history_byte"] > RESUME_SLOPE:
                flags.append(f"history resume.retained: {entry['retained_per_history_byte']:.4f} B per history byte (bound {RESUME_SLOPE}; M7)")
        summary[sc] = entry
    return summary, flags


def run(a):
    repo = Path(a.repo).resolve()
    if not (repo / "test" / "perfgate" / "memory_sweep_test.go").exists():
        print(f"memsweep: {repo} has no test/perfgate/memory_sweep_test.go", file=sys.stderr)
        return 2
    out = Path(a.out).resolve()
    raw = out.with_suffix(".points.jsonl")
    raw.unlink(missing_ok=True)
    env = dict(os.environ, PIG_MEMSWEEP_OUT=str(raw), CGO_ENABLED="0")
    env.setdefault("GOFLAGS", "-p=4")
    if a.gomaxprocs:
        env["PIG_MEMSWEEP_GOMAXPROCS"] = a.gomaxprocs
    cmd = ["go", "test", "-count=1", "-timeout=60m", "-v", "-run", "^TestMemoryScalingSweep", "./test/perfgate"]
    if a.cpus:
        cmd = ["taskset", "-c", a.cpus, "nice", *cmd]
        env["PIG_MEMSWEEP_CPUS"] = a.cpus
    for i in range(a.runs):
        r = subprocess.run(cmd, cwd=repo, env=env, capture_output=True, text=True)
        if r.returncode != 0:
            sys.stderr.write(r.stdout[-6000:] + r.stderr[-3000:])
            print(f"memsweep: run {i + 1} failed ({r.returncode})", file=sys.stderr)
            return 2
    points = [json.loads(l) for l in raw.read_text().splitlines() if l.strip()]
    notes = []
    if a.cpus and a.gomaxprocs == "default" and cpu_count(a.cpus) <= PIG_CAP:
        # With GOMAXPROCS unset and a mask of at most pig's cap, pig does not set GOMAXPROCS, so the runtime follows the CPU mask; a cgroup
        # cpuset update resets the masks of running processes (taskset undone; on the Linux lab host every process alive for ~30 s), the runtime
        # grows to the host's width, and its P set stays live (+3 MB live, +24 MB not returned at 164 CPUs). Such points measure the host.
        # With an explicit GOMAXPROCS, or a mask wider than the cap (pig sets GOMAXPROCS itself), the mask no longer moves the width.
        want = cpu_count(a.cpus)
        kept = [p for p in points if p.get("cpus_allowed", want) == want]
        if len(kept) != len(points):
            bad = sorted({p["cpus_allowed"] for p in points if p.get("cpus_allowed", want) != want})
            notes.append(f"dropped {len(points) - len(kept)} of {len(points)} points whose pig ran on {bad} CPUs, not the {want} pinned")
        missing = {(p["scenario"], p["n"]) for p in points} - {(p["scenario"], p["n"]) for p in kept}
        if missing:
            print(f"memsweep: every run of {sorted(missing)} lost its CPU pinning; rerun", file=sys.stderr)
            return 2
        points = kept
    head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=repo, capture_output=True, text=True).stdout.strip()
    summary, flags = summarize(points)
    doc = {"commit": head, "runs": a.runs, "cpus": a.cpus, "gomaxprocs": a.gomaxprocs or "1", "summary": summary, "flags": flags, "notes": notes}
    out.write_text(json.dumps(doc, indent=1) + "\n")
    report(doc)
    return 1 if flags else 0


def cpu_count(spec):
    """The number of CPUs in a taskset -c list such as 192-195,200."""
    n = 0
    for part in spec.split(","):
        first, _, last = part.partition("-")
        n += int(last or first) - int(first) + 1
    return n


def report(doc):
    print(f"memsweep {doc['commit'][:10]} runs={doc['runs']} cpus={doc['cpus']} gomaxprocs={doc['gomaxprocs']}")
    for sc, e in doc["summary"].items():
        for r in e["points"]:
            vals = "  ".join(f"{k}={r[k] / 1e6:.2f}MB" for k in r if k + "_spread" in r)
            print(f"  {sc:14} n={r['n']:<5} history={r['history_bytes'] / 1e6:7.2f}MB context={r['context_bytes'] / 1e6:6.2f}MB  {vals}")
        for k, v in e.items():
            if k.endswith(("_per_unit_1x", "_per_unit_10x", "_per_history_byte")) and v is not None:
                print(f"  {sc:14} {k} = {v:,.4f}" if abs(v) < 10 else f"  {sc:14} {k} = {v:,.0f}")
    for n in doc.get("notes", []):
        print("NOTE", n)
    for f in doc["flags"]:
        print("FLAG", f)


def compare(a):
    prev, cur = (json.loads(Path(p).read_text()) for p in (a.prev, a.cur))
    flags = list(cur["flags"])
    for sc, e in cur["summary"].items():
        pe = prev["summary"].get(sc)
        if not pe:
            continue
        pp = {r["n"]: r for r in pe["points"]}
        for r in e["points"]:
            q = pp.get(r["n"])
            if not q:
                continue
            for k in [k for k in r if k + "_spread" in r and k in q]:
                noise = max(2 * max(r[k + "_spread"], q[k + "_spread"]), REL_NOISE * q[k])
                if r[k] - q[k] > noise:
                    flags.append(f"regression {sc}.{k} n={r['n']}: {q[k]:,.0f} -> {r[k]:,.0f} B (+{r[k] - q[k]:,.0f}, noise {noise:,.0f})")
        for k, v in e.items():
            if k.endswith("_per_unit_10x") and pe.get(k) is not None and v is not None:
                base = k[: -len("_per_unit_10x")]
                n10 = e["points"][2]["n"] - e["points"][1]["n"]
                noise = max(2 * max(r[base + "_spread"] for r in e["points"] + pe["points"]) / n10, REL_NOISE * abs(pe[k]))
                if v - pe[k] > noise:
                    flags.append(f"regression {sc}.{k}: {pe[k]:,.0f} -> {v:,.0f} B per unit (noise {noise:,.0f})")
    print(f"memsweep compare {prev['commit'][:10]} -> {cur['commit'][:10]}")
    report(cur)
    for f in flags[len(cur["flags"]):]:
        print("FLAG", f)
    return 1 if flags else 0


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run")
    r.add_argument("--repo", default=".")
    r.add_argument("--out", required=True)
    r.add_argument("--runs", type=int, default=3)
    r.add_argument("--cpus", default="")
    r.add_argument("--gomaxprocs", default="", help='"default" (pig\'s own policy), a number, or empty for the perfgate homes\' 1')
    c = sub.add_parser("compare")
    c.add_argument("prev")
    c.add_argument("cur")
    a = ap.parse_args()
    sys.exit(run(a) if a.cmd == "run" else compare(a))


if __name__ == "__main__":
    main()

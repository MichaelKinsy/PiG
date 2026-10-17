# SPDX-License-Identifier: MIT
"""Soak bench: scripted turns against one RPC process per arm for hours, with the process tree's resources sampled at a fixed interval.

Every arm runs at the same time on the same host, each in its own cgroup against its own mock endpoint, so all of them see the same load.
Each interval the bench records processes, threads, open file descriptors, resident and proportional memory and CPU of the whole tree.
Compaction on a small context window keeps the transcript bounded, so growth in any of these is a leak, not the work. After the run
a series is judged flat when its second half stays within a declared band: descriptors, threads and processes within max(2, 5% of the
median), resident memory within 10% of the median. Goroutines are not sampled during the run (PiG exposes no live count); PiG's
`PIG_PROFILE=goroutine` profile is written when the process exits.
"""
import copy
import json
import os
import threading
import time

from . import arms as armlib
from . import checkpoint, egress, envinfo, stats
from .mock import MockServer, agentic_policy
from .overhead import Ctx, drive_turns, start_rpc, wait_ready, native

FLAT_COUNT_BAND = (2, 0.05)  # absolute floor, fraction of the median
FLAT_RSS_BAND = 0.10
COMPACTION = {"enabled": True, "reserveTokens": 8000, "keepRecentTokens": 6000}


def second_half(values):
    return values[len(values) // 2:]


def slope_per_hour(times, values):
    """Least-squares slope of values over times (seconds), per hour. 0.0 for fewer than two points."""
    n = len(times)
    if n < 2:
        return 0.0
    mt, mv = sum(times) / n, sum(values) / n
    den = sum((t - mt) ** 2 for t in times)
    return 0.0 if den == 0 else 3600 * sum((t - mt) * (v - mv) for t, v in zip(times, values)) / den


def judge(series, metric):
    """{'flat': bool, 'median', 'spread', 'slope_per_h'} for one metric over the second half of a sampled series."""
    rows = [(r["t_s"], r[metric]) for r in series if r.get(metric) is not None]
    half = second_half(rows)
    if len(half) < 3:
        return {"flat": None, "reason": "fewer than 3 samples in the second half"}
    vals = [v for _, v in half]
    med = stats.median(vals)
    spread = max(vals) - min(vals)
    if metric in ("rss_mib", "pss_mib"):
        band = FLAT_RSS_BAND * med
    else:
        band = max(FLAT_COUNT_BAND[0], FLAT_COUNT_BAND[1] * med)
    return {"flat": spread <= band, "median": med, "spread": spread, "band": band, "slope_per_h": slope_per_hour([t for t, _ in half], vals)}


def partial_name(arm_id):
    return f"raw.partial.{arm_id}.json"


def run_arm(arm, ctx, server, opts, stop, out):
    """One arm's soak loop and sampler. Fills out['series'], out['turns'], out['failure'].

    `stop` is an external abort that this function only reads, so one Event may be shared by every arm: an arm that fails or finishes
    ends only its own run. Its own loop and sampler stop on `done`."""
    done = threading.Event()
    ctx.harness(arm, "soak", extensions=(), compaction=COMPACTION, context_window=32000)
    server.reset(policy=agentic_policy(ctx._harnesses[(arm.id, "soak")].cwd, tool_turns=1))
    h, meter, rpc = start_rpc(ctx, arm, "soak", False, COMPACTION, 32000)
    out["series"], out["turns"] = [], 0
    try:
        if wait_ready(rpc) is None:
            out["failure"] = "no get_state response"
            return
        t0 = time.monotonic()

        def sampler():
            nxt = t0
            while not done.is_set():
                snap = meter.snapshot()
                if snap is None:
                    out["failure"] = "this host has no per-process /proc accounting for the meter"
                    done.set()
                    return
                out["series"].append(dict(snap, t_s=round(time.monotonic() - t0, 1), turns=out["turns"]))
                checkpoint.save({"kind": "soak-partial", "arm": arm.id, "turns": out["turns"], "series": list(out["series"])}, opts.out, partial_name(arm.id))
                nxt += opts.interval
                done.wait(max(0, nxt - time.monotonic()))

        thread = threading.Thread(target=sampler, daemon=True)
        thread.start()
        while not done.is_set() and not stop.is_set() and time.monotonic() - t0 < opts.seconds:
            rows = []
            bad = drive_turns(ctx, rpc, meter, 1, rows)
            if bad:
                out["failure"] = bad
                break
            out["turns"] += 1
            with rpc.cond:  # the reader keeps every record; a soak run must not
                del rpc.records[:]
                rpc.cursor = 0
            if opts.think_ms:
                done.wait(opts.think_ms / 1000)
        done.set()
        thread.join(5)
        rpc.close()
    finally:
        done.set()
        if rpc.proc.poll() is None:
            rpc.close(grace=1)
        meter.close()
        # This meter's own observations: the module-wide egress.OBSERVED is shared by the arms running in parallel threads.
        if getattr(meter, "egress", None):
            out["egress"] = sorted(meter.egress)


REQUIRED = ("processes", "threads", "fds", "rss_mib")  # pss_mib is judged where the host has smaps_rollup


def summarize(arm_id, out):
    """flat is True only when every required series was judged flat; a series too short to judge leaves the arm undecided (None), never flat."""
    series = out.get("series", [])
    verdict = {m: judge(series, m) for m in REQUIRED + ("pss_mib",)}
    if out.get("failure") or any(verdict[m]["flat"] is False for m in verdict):
        flat = False
    elif any(verdict[m]["flat"] is None for m in REQUIRED):
        flat = None
    else:
        flat = True
    return {"turns": out.get("turns", 0), "samples": len(series), "failure": out.get("failure"), "verdict": verdict, "flat": flat}


def with_aa_copies(arms, ids):
    """The arms plus an A/A copy `<id>-b` of each id: the same binary with its own home and cgroup, so the difference between a pair is the noise floor."""
    out = list(arms)
    for aid in ids:
        base = next((a for a in arms if a.id == aid), None)
        if base is None:
            raise SystemExit(f"--aa {aid}: not a selected, installed arm")
        twin = copy.copy(base)
        twin.id, twin.name = aid + "-b", base.name + " (A/A copy)"
        out.append(twin)
    return out


def run(opts):
    opts.seconds = opts.hours * 3600 if opts.minutes is None else opts.minutes * 60
    arm_list = [a for a in armlib.select(armlib.load(), opts.arms) if a.family in ("pig", "pi")]
    result = {"kind": "soak", "options": {"hours": opts.seconds / 3600, "interval_s": opts.interval, "think_ms": opts.think_ms, "seed": opts.seed,
                                          "bands": {"count": FLAT_COUNT_BAND, "rss": FLAT_RSS_BAND}}, "arms": {}, "failures": {}, "egress": dict(egress.describe(), violations=[])}
    with armlib.Workspace() as ws:
        ws.install_all(arm_list)
        result["env"] = envinfo.collect(arm_list, seed=opts.seed)
        runnable = [a for a in arm_list if a.binary]
        runnable = with_aa_copies(runnable, opts.aa)
        stop = threading.Event()  # an external abort only; run_arm never sets it
        outs, servers, threads = {}, [], []
        ctx = Ctx(ws.work, None, opts)
        for a in runnable:
            server = MockServer(retain=64)
            server.__enter__()
            servers.append(server)
            outs[a.id] = {}
            arm_ctx = Ctx(ws.work, server, opts)
            arm_ctx._harnesses = ctx._harnesses
            threads.append(threading.Thread(target=run_arm, args=(a, arm_ctx, server, opts, stop, outs[a.id])))
        for t in threads:
            t.start()
        for t in threads:
            t.join(opts.seconds + 600)
        for s in servers:
            s.__exit__()
    for aid, out in outs.items():
        s = summarize(aid, out)
        result["arms"][aid] = dict(s, series=out.get("series", []))
        if out.get("egress"):
            result["egress"]["violations"].append({"arm": aid, "remotes": out["egress"]})
        if s["failure"] and native(next(a for a in runnable if a.id == aid)):
            result["failures"][aid] = s["failure"]
    result["ok"] = not result["failures"] and not result["egress"]["violations"] and all(a["flat"] is True for a in result["arms"].values())
    os.makedirs(opts.out, exist_ok=True)
    with open(os.path.join(opts.out, "raw.json"), "w") as f:
        json.dump(result, f, indent=1)
    for aid in outs:
        checkpoint.clear(opts.out, partial_name(aid))
    return result

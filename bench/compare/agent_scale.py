#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Agent-scale scenario (ADDENDUM 2 row 2): N concurrent interactive sessions of one arm on one host, each in its own pseudo-terminal, agent
directory and HOME, all served by one scripted mock (fake_model_ctx.py). Starts in batches of --batch, waits until every session shows its
prompt ("ready"), then drives --prompts prompts in lockstep (send to all, wait for all). Records, per N: sessions that came up, memory
(sum and per-session RSS/PSS/private-dirty) at ready and after the prompts, total CPU seconds and CPU per session, p50/p95/max prompt
latency, failures (never ready / no answer / exited), and the wall time.

Guard: a measured per-session PSS at N=10 projects the memory of larger N; when the projection exceeds --mem-guard-gb the run is recorded as
"not run" with the projection (a shared host's lane cap, not a result about the arm). Uses Pi-Bolt's harness (unmodified) for ptys and readings.
"""
import argparse
import contextlib
import json
import os
import select
import shlex
import shutil
import statistics
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import cleanenv

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import ctx_scale as cs  # noqa: E402


def pump_all(h, ttys, timeout):
    fds = {t.fd: t for t in ttys}
    r, _, _ = select.select(list(fds), [], [], timeout)
    for fd in r:
        fds[fd].pump(0)


def wait_all(h, ttys, needle_of, starts, timeout):
    """Until every tty shows its needle after its start offset; returns the ttys that did not."""
    pending = set(range(len(ttys)))
    end = time.perf_counter() + timeout
    seen = {}
    while pending and time.perf_counter() < end:
        pump_all(h, [ttys[i] for i in pending if h.alive(ttys[i].pid)], 0.05)
        for i in list(pending):
            t = ttys[i]
            if not h.alive(t.pid):
                pending.discard(i)
                continue
            tail_from = max(starts[i], len(t.buf) - 16384)
            if needle_of(i).encode() in h.ANSI.sub(b"", t.buf[tail_from:]):
                pending.discard(i)
                seen[i] = time.perf_counter()
    return [ttys[i] for i in pending], seen


def lost_sessions(up, stuck, alive):
    """Sessions that were up before a prompt and did not answer it: still running without the answer (stuck) or exited meanwhile."""
    return [t for t in up if t in stuck or not alive(t)]


def run_n(h, name, argv, n, a, rows):
    with tempfile.TemporaryDirectory(prefix="scale-") as tmp:
        log = os.path.join(tmp, "log")
        with cs.model_server(h, log) as port:
            homes, cwds, ttys = [], [], []
            t_start = time.perf_counter()
            try:
                for i in range(n):
                    home = cs.pi_home_window(port, 200_000)
                    cwd = Path(tempfile.mkdtemp(prefix="pibolt-work-", dir=tmp))
                    shutil.copytree(h.FIXTURES, cwd / "fixture")
                    homes.append(home)
                    cwds.append(cwd)
                    ttys.append(h.Tty(h.pinned([*argv, "--no-session", *h.MODEL_ARGS], a.cpus), h.pi_env(home), cwd))
                    if (i + 1) % a.batch == 0:
                        wait_all(h, ttys[-a.batch:], lambda j: "fake-model", [0] * a.batch, 120)
                dead, _ = wait_all(h, ttys, lambda j: "fake-model", [0] * n, 180)
                for t in ttys:
                    t.settle(0.05, time.perf_counter() + 1)
                up = [t for t in ttys if h.alive(t.pid) and t not in dead]
                ready = {"n": n, "up": len(up), "failed_ready": n - len(up), "ready_wall_s": round(time.perf_counter() - t_start, 1)}
                sm = [cs.tree_smaps(t.pid) for t in up]
                ready.update({k: round(sum(s.get(k, 0) for s in sm), 1) for k in ("rss", "pss", "private_dirty")})
                cpu_ready = sum(h.cpu_ms(t.pid) for t in up)
                rows.append({"arm": name, "stage": "ready", **ready, "cpu_s": round(cpu_ready / 1000, 2)})
                if not up:
                    return
                lats, failures = [], 0
                for p in range(1, a.prompts + 1):
                    starts = [len(t.buf) for t in ttys]
                    t0 = time.perf_counter()
                    for t in up:
                        t.send(h.PROMPT.encode() + b"\r")
                    stuck, seen = wait_all(h, ttys, lambda j, p=p: h.done(p), starts, a.stage_timeout)
                    lost = lost_sessions(up, stuck, lambda t: h.alive(t.pid))
                    failures += len(lost)
                    lats += [seen[i] - t0 for i in seen if ttys[i] in up]
                    for i, t in enumerate(ttys):
                        t.buf = t.buf[-65536:]
                    up = [t for t in up if t not in lost]
                    if not up:
                        break
                sm = [cs.tree_smaps(t.pid) for t in up]
                lats.sort()
                post = {"n": n, "up": len(up), "prompt_failures": failures, "prompts": a.prompts,
                        "lat_p50_s": round(statistics.median(lats), 2) if lats else None,
                        "lat_p95_s": round(cs.p95(lats), 2) if lats else None, "lat_max_s": round(lats[-1], 2) if lats else None}
                post.update({k: round(sum(s.get(k, 0) for s in sm), 1) for k in ("rss", "pss", "private_dirty")})
                cpu_end = sum(h.cpu_ms(t.pid) for t in up)
                post["cpu_s"] = round(cpu_end / 1000, 2)
                post["cpu_s_per_session"] = round(cpu_end / 1000 / max(1, len(up)), 2)
                post["pss_mb_per_session"] = round(post["pss"] / max(1, len(up)), 1)
                post["rss_mb_per_session"] = round(post["rss"] / max(1, len(up)), 1)
                post["wall_s"] = round(time.perf_counter() - t_start, 1)
                rows.append({"arm": name, "stage": f"after{a.prompts}", **post})
            finally:
                for t in ttys:
                    with contextlib.suppress(Exception):
                        os.kill(t.pid, 9)
                        os.waitpid(t.pid, 0)
                        os.close(t.fd)
                for hm in homes:
                    shutil.rmtree(hm, ignore_errors=True)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", required=True, type=Path)
    ap.add_argument("--arms", required=True)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--ns", default="1,10,100,1000")
    ap.add_argument("--prompts", type=int, default=5)
    ap.add_argument("--batch", type=int, default=10)
    ap.add_argument("--cpus")
    ap.add_argument("--round", type=int, default=0)
    ap.add_argument("--stage-timeout", type=float, default=300)
    ap.add_argument("--mem-guard-gb", type=float, default=16)
    a = ap.parse_args()
    dropped = cleanenv.apply()
    print(f"clean env: dropped {len(dropped)} variables ({', '.join(dropped)}); GOMAXPROCS unset: each arm sizes itself from its pinned cores", file=sys.stderr, flush=True)
    root = a.root.resolve()
    h = cs.load_harness(root)
    specs = dict(l.split("=", 1) for l in subprocess.run([sys.executable, str(HERE / "arms.py"), "specs", "--root", str(root)], capture_output=True, text=True, check=True).stdout.splitlines())
    names = a.arms.split(",")
    names = names[a.round % len(names):] + names[:a.round % len(names)]
    for name in names:
        argv = shlex.split(specs[name])
        per_session = None
        for n in [int(x) for x in a.ns.split(",")]:
            rows = []
            if per_session and per_session * n / 1024 > a.mem_guard_gb:
                rows.append({"arm": name, "stage": "not-run", "n": n, "reason": f"projected {per_session * n / 1024:.0f} GB PSS (from N=10) exceeds guard {a.mem_guard_gb} GB (shared host, lane cap)"})
            else:
                print(f"[{time.strftime('%H:%M:%S')}] scale {name} N={n}", flush=True)
                try:
                    run_n(h, name, argv, n, a, rows)
                except Exception as e:  # noqa: BLE001
                    rows.append({"arm": name, "stage": "error", "n": n, "error": repr(e)})
                if n == 10:
                    for r in rows:
                        if r["stage"].startswith("after") and r.get("pss_mb_per_session"):
                            per_session = r["pss_mb_per_session"]
            with open(a.out, "a") as f:
                for r in rows:
                    f.write(json.dumps({"round": a.round, **r}) + "\n")
            print("   ", rows[-1] if rows else None, flush=True)


if __name__ == "__main__":
    main()

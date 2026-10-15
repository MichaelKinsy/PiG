#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Interactive CPU/wall/memory for N prompts in one session (Pi-Bolt's interactive scenario at N = 5 and 20, which exposes growth with
history): per arm and round one TUI session, interleaved, arm order rotated. Reports CPU ms of the prompts (all threads, from /proc),
wall ms, final RSS/PSS/private dirty and peak RSS. Uses Pi-Bolt's harness (unmodified) for the pty and readings.

  interactive_n.py --root ROOT --arms a,b --ns 5,20 --rounds 5 --out FILE.jsonl [--cpus 150-153]
"""
import argparse
import json
import os
import shlex
import subprocess
import sys
import time
from pathlib import Path

import cleanenv

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import ctx_scale as cs  # noqa: E402
import fake_server  # noqa: E402


def one(h, argv, n, cpus):
    with fake_server.fake_model(h) as port, h.pi_home(port) as home, h.workdir() as cwd:
        t0 = time.perf_counter()
        tty = h.Tty(h.pinned([*argv, "--no-session", *h.MODEL_ARGS], cpus), h.pi_env(home), cwd)
        deadline = t0 + 600
        if not tty.wait_for("fake-model", 0, deadline):
            tty.quit(1)
            return {"ok": False}
        tti = (time.perf_counter() - t0) * 1000
        tty.settle(0.05, deadline)
        c0, w0 = h.cpu_ms(tty.pid), time.perf_counter()
        for i in range(1, n + 1):
            start = len(tty.buf)
            tty.send(h.PROMPT.encode() + b"\r")
            if not tty.wait_for(h.done(i), start, deadline):
                tty.quit(1)
                return {"ok": False, "stalled_at": i}
            tty.settle(0.03, deadline)
        cpu, wall = h.cpu_ms(tty.pid) - c0, (time.perf_counter() - w0) * 1000
        mem = cs.tree_smaps(tty.pid)
        hwm = vm_hwm_mb(tty.pid)
        status, ru = tty.quit()
        return {"ok": status == 0, "tti_ms": round(tti), "prompts_cpu_ms": round(cpu), "prompts_wall_ms": round(wall),
                "peak_mb": round(hwm if hwm is not None else h.maxrss_mb(ru), 1), **mem}


def vm_hwm_mb(pid):
    """The process's own peak RSS (VmHWM) in MiB, or None without /proc. On Linux the harness's ru_maxrss (Pi-Bolt's maxrss_mb) is not
    the child's: exec keeps hiwater_rss, so a child forked from a larger Python parent reports the parent's peak."""
    try:
        with open(f"/proc/{pid}/status") as f:
            for line in f:
                if line.startswith("VmHWM:"):
                    return int(line.split()[1]) / 1024
    except (OSError, ValueError):
        pass
    return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", required=True, type=Path)
    ap.add_argument("--arms", required=True)
    ap.add_argument("--ns", default="5,20")
    ap.add_argument("--rounds", type=int, default=5)
    ap.add_argument("--out", required=True)
    ap.add_argument("--cpus")
    a = ap.parse_args()
    dropped = cleanenv.apply()
    print(f"clean env: dropped {len(dropped)} variables ({', '.join(dropped)}); GOMAXPROCS unset: each arm sizes itself from its pinned cores", file=sys.stderr, flush=True)
    root = a.root.resolve()
    h = cs.load_harness(root)
    specs = dict(l.split("=", 1) for l in subprocess.run([sys.executable, str(HERE / "arms.py"), "specs", "--root", str(root)], capture_output=True, text=True, check=True).stdout.splitlines())
    arms = a.arms.split(",")
    for r in range(a.rounds):
        for n in [int(x) for x in a.ns.split(",")]:
            order = arms[r % len(arms):] + arms[:r % len(arms)]
            for name in order:
                res = one(h, shlex.split(specs[name]), n, a.cpus)
                row = {"round": r, "arm": name, "prompts": n, **res}
                with open(a.out, "a") as f:
                    f.write(json.dumps(row) + "\n")
                print(row, flush=True)


if __name__ == "__main__":
    main()

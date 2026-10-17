#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Context-scale memory scenario (owner rows 1 of ADDENDUM 2): one interactive session per arm grows the conversation with the
scripted task of fake_model_ctx.py (about 56k tokens per prompt: four file reads and an answer) and is sampled each time the request the harness sends
crosses a target: 100k, 500k, 850k (85% of 1M), 1M, 2M and 4M tokens (request bytes / 4, which is also what the mock reports as usage).
At 1M it then runs five more prompts. Each sample records RSS, PSS and private-dirty memory of the arm's process, CPU per prompt and the
p50/p95 latency of the prompts since the last sample. Memory at ready is sampled before the first prompt.

The declared window is 5,000,000 tokens for every arm (so no arm compacts before 4M; the window per stage in the report states what each stage
is a percentage of). An arm that stops answering, exits, or exceeds --stage-timeout is recorded with the stage it reached: "cannot run" is a
result, never a gap. Uses Pi-Bolt's harness module (pty, memory and CPU readings) from the suite checkout, unmodified.
"""
import argparse
import contextlib
import json
import math
import os
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
import fake_server  # noqa: E402
TARGETS = [100_000, 500_000, 850_000, 1_000_000, 2_000_000, 4_000_000]
WINDOW = 5_000_000


def p95(values):
    """Nearest-rank 95th percentile: the smallest sample with at least 95% of the samples at or below it (the maximum for fewer than 20 samples)."""
    s = sorted(values)
    return s[math.ceil(0.95 * len(s)) - 1]


def load_harness(root: Path):
    sys.path.insert(0, str(root / "src/Pi-Bolt/bench"))
    import harness
    return harness


def smaps(pid: int) -> dict:
    out = {}
    with contextlib.suppress(OSError):
        for line in open(f"/proc/{pid}/smaps_rollup"):
            k, _, v = line.partition(":")
            if k in ("Rss", "Pss", "Private_Dirty", "Swap"):
                out[k.lower()] = round(int(v.split()[0]) / 1024, 1)
    return out


def tree_smaps(pid: int) -> dict:
    """Sum over the process and its children (a harness that forks workers is charged for them)."""
    total = smaps(pid)
    with contextlib.suppress(OSError):
        for tid in os.listdir(f"/proc/{pid}/task"):
            for c in open(f"/proc/{pid}/task/{tid}/children").read().split():
                for k, v in tree_smaps(int(c)).items():
                    total[k] = round(total.get(k, 0) + v, 1)
    return total


@contextlib.contextmanager
def model_server(h, log):
    with fake_server.serve(HERE / "fake_model_ctx.py", "--log", log, start_timeout=30) as port:
        yield port


def pi_home_window(port: int, window: int) -> Path:
    home = Path(tempfile.mkdtemp(prefix="pibolt-home-"))
    models = [{"id": "fake-model", "contextWindow": window, "maxTokens": 8192}]
    (home / "models.json").write_text(json.dumps({"providers": {"fake": {"baseUrl": f"http://127.0.0.1:{port}/v1", "api": "openai-completions",
                                                                         "apiKey": "fake", "models": models}}}, indent=2))
    (home / "settings.json").write_text(json.dumps({"lastChangelogVersion": "9999.0.0", "theme": "dark"}))
    (home / "auth.json").write_text("{}")
    return home


def last_tokens(log: str) -> int:
    try:
        with open(log, "rb") as f:
            f.seek(0, 2)
            f.seek(max(0, f.tell() - 600))
            line = f.read().splitlines()[-1]
        return json.loads(line)["bytes"] // 4
    except (OSError, IndexError, ValueError):
        return 0


def session(h, name, argv, cpus, max_target, stage_timeout, out_rows, sink=None):
    log = tempfile.NamedTemporaryFile(prefix="ctx-log-", delete=False).name
    cwd = None
    home = None
    tty = None
    try:
        with model_server(h, log) as port:
            home = pi_home_window(port, WINDOW)
            cwd = Path(tempfile.mkdtemp(prefix="pibolt-work-"))
            shutil.copytree(h.FIXTURES, cwd / "fixture")
            env = h.pi_env(home)
            t0 = time.perf_counter()
            tty = h.Tty(h.pinned([*argv, "--no-session", *h.MODEL_ARGS], cpus), env, cwd)
            def row(stage, **kw):
                r = {"arm": name, "stage": stage, "window": WINDOW, **kw}
                out_rows.append(r)
                if sink:
                    sink(r)
            if not tty.wait_for("fake-model", 0, time.perf_counter() + 120):
                row("ready", error="never became interactive")
                return
            tty.settle(0.5, time.perf_counter() + 10)
            row("ready", ready_ms=round((time.perf_counter() - t0) * 1000), **tree_smaps(tty.pid))
            lat, cpu, targets, n, extra = [], [], [t for t in TARGETS if t <= max_target], 0, 0
            while targets or extra:
                n += 1
                start = len(tty.buf)
                ts, c0 = time.perf_counter(), h.cpu_ms(tty.pid)
                tty.send(h.PROMPT.encode() + b"\r")
                if not tty.wait_for(h.done(n), start, time.perf_counter() + stage_timeout):
                    row("stalled", prompt=n, tokens=last_tokens(log), error="no answer within stage timeout", alive=h.alive(tty.pid))
                    return
                tty.settle(0.03, time.perf_counter() + 30)
                lat.append((time.perf_counter() - ts) * 1000)
                cpu_per = h.cpu_ms(tty.pid) - c0
                cpu.append(cpu_per)
                tok = last_tokens(log)
                if extra:
                    extra -= 1
                    lat_x.append(lat[-1])
                    cpu_x.append(cpu_per)
                    if not extra:
                        row("after5@1M", tokens=tok, prompt=n, p50_ms=round(statistics.median(lat_x)), p95_ms=round(p95(lat_x)),
                            cpu_ms_per_prompt=round(statistics.mean(cpu_x), 1), **tree_smaps(tty.pid))
                        lat, cpu = [], []
                    continue
                if targets and tok >= targets[0]:
                    t = targets.pop(0)
                    row(f"at{t // 1000}k", target=t, tokens=tok, prompt=n, p50_ms=round(statistics.median(lat)), p95_ms=round(p95(lat)),
                        cpu_ms_per_prompt=round(statistics.mean(cpu), 1), **tree_smaps(tty.pid))
                    lat, cpu = [], []
                    if t == 1_000_000:
                        extra, lat_x, cpu_x = 5, [], []
            row("done", prompt=n, tokens=last_tokens(log))
    finally:
        if tty:
            with contextlib.suppress(Exception):
                tty.quit(5)
        for p in (home, cwd):
            if p:
                shutil.rmtree(p, ignore_errors=True)
        with contextlib.suppress(OSError):
            os.unlink(log)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", required=True, type=Path)
    ap.add_argument("--arms", required=True)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--cpus")
    ap.add_argument("--round", type=int, default=0)
    ap.add_argument("--max-target", type=int, default=4_000_000)
    ap.add_argument("--stage-timeout", type=float, default=900)
    a = ap.parse_args()
    dropped = cleanenv.apply()
    print(f"clean env: dropped {len(dropped)} variables ({', '.join(dropped)}); GOMAXPROCS unset: each arm sizes itself from its pinned cores", file=sys.stderr, flush=True)
    root = a.root.resolve()
    h = load_harness(root)
    specs = dict(l.split("=", 1) for l in subprocess.run([sys.executable, str(HERE / "arms.py"), "specs", "--root", str(root)], capture_output=True, text=True, check=True).stdout.splitlines())
    names = a.arms.split(",")
    names = names[a.round % len(names):] + names[:a.round % len(names)]
    for name in names:
        rows = []
        argv = [os.path.expanduser(x) for x in __import__("shlex").split(specs[name])]
        print(f"[{time.strftime('%H:%M:%S')}] ctx {name} round {a.round}", flush=True)
        try:
            def sink(r, name=name):
                with open(a.out, "a") as f:
                    f.write(json.dumps({"round": a.round, **r}) + "\n")
            session(h, name, argv, a.cpus, a.max_target, a.stage_timeout, rows, sink)
        except Exception as e:  # noqa: BLE001 - a crashed arm is a result
            rows.append({"arm": name, "stage": "error", "error": repr(e)})
        if rows and rows[-1]["stage"] == "error":
            sink(rows[-1])
        print("   ", [(r["stage"], r.get("tokens"), r.get("rss"), r.get("pss")) for r in rows], flush=True)


if __name__ == "__main__":
    main()

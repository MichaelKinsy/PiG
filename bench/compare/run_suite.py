#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Runs Pi-Bolt's published benchmark tools, unmodified, over every arm: interleaved rounds, arm order rotated each round.

  run_suite.py --root ROOT --out DIR [--arms a,b] [--rounds 5] [--cpus 128-131] [--scenarios benchmark,long,...] [--aa]

ROOT is an `arms.py install` root; the suite itself is cloned at the commit in arms.lock. Each round invokes each tool once with
all arms (the tool interleaves them itself where it can), so host load drifts across arms equally. The raw output of every tool
goes to DIR; report.py reads it. An arm that cannot run a scenario (gi has no print mode) is recorded as "cannot run", not skipped.
Run it under `lane-run --heavy --` on a shared host. Everything it measures is PROVISIONAL.
"""
import argparse
import json
import os
import subprocess
import sys
import time
import tomllib
from pathlib import Path

import cleanenv

HERE = Path(__file__).resolve().parent
LOCK = tomllib.loads((HERE / "arms.lock").read_text())
# scenario -> arms that cannot run it, with the reason (checked against the arm, not assumed: see CANNOT in the report)
CANNOT = {
    "gi": {"headless": "no print mode (-p)", "large_write": "no print mode (-p)",
           "long": "stalls at prompt 47 (~2.6M tokens): gi holds the next submission (\"draft: held submission; /draft check before another send\") and waits for the user"},
    "omp": {"long": "compacts the conversation itself at the 200k window (about prompt 8-9; its own token estimate, not the mock's usage): the mock's \"(prompt N)\" counter then restarts and the harness waits for a number that never comes; measured with a 5M window in ctx_scale instead"},
}
DEFAULT_ARMS = "pig-0.4.1,pig-tip,pibolt-0.7.4,pibolt-0.7.3,pi-node,pi-bun,gi,omp"


def suite_dir(root: Path) -> Path:
    d = root / "src" / "Pi-Bolt"
    if not d.exists():
        d.parent.mkdir(parents=True, exist_ok=True)
        subprocess.run(["git", "clone", "--quiet", LOCK["suite"]["repo"], str(d)], check=True, timeout=600)
    subprocess.run(["git", "-C", str(d), "checkout", "--quiet", LOCK["suite"]["commit"]], check=True, timeout=120)
    return d / LOCK["suite"]["path"]


def specs(root: Path, arms: list[str]) -> dict[str, str]:
    out = subprocess.run([sys.executable, str(HERE / "arms.py"), "specs", "--root", str(root), *sum([["--arm", a] for a in arms], [])],
                         capture_output=True, text=True, check=True, timeout=60).stdout
    return dict(line.split("=", 1) for line in out.splitlines() if "=" in line)


def with_aa(spec: dict[str, str], arms: list[str]) -> tuple[dict[str, str], list[str]]:
    """Adds an A/A copy `<arm>-aa` (the same command, its own build) next to every arm."""
    return {**spec, **{f"{n}-aa": spec[n] for n in arms}}, [x for n in arms for x in (n, f"{n}-aa")]


def runnable(order: list[str], skip: str | None = None) -> list[str]:
    """The builds of `order` that can run scenario `skip`; an A/A copy shares its arm's CANNOT entry."""
    return [n for n in order if not (skip and skip in CANNOT.get(n.removesuffix("-aa"), {}))]


def rotate(xs, r):
    r %= len(xs)
    return xs[r:] + xs[:r]


def tool(bench: Path, name: str, args: list[str], out: Path, timeout: int, pin: str | None = None):
    t0 = time.time()
    print(f"[{time.strftime('%H:%M:%S')}] {name} {' '.join(a for a in args if not a.startswith('--build'))[:100]}", flush=True)
    with open(out, "w") as f:
        try:
            p = subprocess.run([*(["taskset", "-c", pin] if pin else []), sys.executable, str(bench / name), *args], stdout=f, stderr=subprocess.STDOUT, timeout=timeout,
                               env={k: v for k, v in os.environ.items() if not k.startswith(("PIG_", "PI_", "NODE_", "BUN_"))})
            rc = p.returncode
        except subprocess.TimeoutExpired:
            rc = "timeout"
    print(f"   -> {rc} in {time.time() - t0:.0f}s", flush=True)
    # The exit status of every invocation goes next to its output: a crashed or timed-out tool is a result report.py shows, not a silent gap.
    with open(out.parent / "status.jsonl", "a") as f:
        f.write(json.dumps({"tool": name, "out": out.name, "rc": rc, "wall_s": round(time.time() - t0, 1)}) + "\n")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", required=True, type=Path)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--arms", default=DEFAULT_ARMS)
    ap.add_argument("--rounds", type=int, default=5)
    ap.add_argument("--cpus", default=None)
    ap.add_argument("--scenarios", default="benchmark,long,large_write,long_answer,tmux,idle")
    ap.add_argument("--runs", type=int, default=5, help="runs per arm per round of benchmark.py (warmup 1)")
    ap.add_argument("--prompts", type=int, default=75)
    ap.add_argument("--first-round", type=int, default=0, help="index of the first round (arm rotation and file names continue from an earlier run)")
    ap.add_argument("--aa", action="store_true", help="add an A/A copy <arm>-aa of every arm: the same command run as its own build, so the report can state each arm's run-to-run floor")
    a = ap.parse_args()
    dropped = cleanenv.apply()
    print(f"clean env: dropped {len(dropped)} variables ({', '.join(dropped)}); GOMAXPROCS unset: each arm sizes itself from its pinned cores", file=sys.stderr, flush=True)
    root, out = a.root.resolve(), a.out.resolve()
    out.mkdir(parents=True, exist_ok=True)
    bench = suite_dir(root)
    arms = a.arms.split(",")
    spec = specs(root, arms)
    if a.aa:
        spec, arms = with_aa(spec, arms)
    (out / "arms.json").write_text(subprocess.run([sys.executable, str(HERE / "arms.py"), "record", "--root", str(root)], capture_output=True, text=True, timeout=300).stdout)
    cpus = ["--cpus", a.cpus] if a.cpus else []
    scen = a.scenarios.split(",")
    for r in range(a.first_round, a.first_round + a.rounds):
        order = rotate(arms, r)

        def builds(skip=None):
            return sum([["--build", f"{n}={spec[n]}"] for n in runnable(order, skip)], [])
        print(f"=== round {r} order {order}", flush=True)
        if "benchmark" in scen:
            # one invocation per scenario: a failure in one cannot take the others with it
            for sc in ("startup", "headless", "interactive"):
                tool(bench, "benchmark.py", ["--runs", str(a.runs), "--warmup", "1", "--scenarios", sc, *cpus, "--out", str(out / "benchmark.jsonl"), "--baseline", runnable(order, sc)[0], *builds(sc)],
                     out / f"benchmark-{sc}-r{r}.txt", 3600)
        if "long" in scen:
            tool(bench, "long_session.py", ["--prompts", str(a.prompts), "--every", "25", *cpus, "--out", str(out / "long.jsonl"), *builds("long")], out / f"long-r{r}.txt", 7200)
        if "large_write" in scen:
            tool(bench, "large_write.py", ["--sizes", "50,200", "--timeout", "900", *cpus, *builds("large_write")], out / f"large_write-r{r}.txt", 7200)
        if "long_answer" in scen:
            tool(bench, "long_answer.py", [*cpus, *builds()], out / f"long_answer-r{r}.txt", 3600)
        if "tmux" in scen:
            tool(bench, "tmux_check.py", ["--prompts", "4", "--rounds", "1", *cpus, "--out", str(out / "tmux.jsonl"), *builds()], out / f"tmux-r{r}.txt", 3600)
        if "idle" in scen:
            # idle_settled.py = idle.py whose first prompt waits for the end of startup (startup_ready.py). idle.py has no --cpus and stops at the first build that does not answer: one pinned invocation per build, so a failure loses one sample
            for k, n in enumerate(runnable(order)):
                tool(HERE, "idle_settled.py", [str(bench), "--seconds", "30", "--rounds", "1", "--build", f"{n}={spec[n]}"], out / f"idle-r{r}-{k}.txt", 900, pin=a.cpus)
        (out / "round").write_text(str(r + 1))


if __name__ == "__main__":
    main()

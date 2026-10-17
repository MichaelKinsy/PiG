#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Cost of the arm adapters (launchers) themselves: `--version` through the launcher against the same binary exec'd directly with the same environment,
interleaved, --runs each. The difference is what an adapter adds to every wall and CPU figure the suite reports for that arm (the launcher execs, so memory
is the arm's own). Prints median wall and CPU in ms for both and the difference.

  overhead.py --root ROOT --arms pig-0.4.1,pig-tip,gi,omp [--runs 200] [--cpus 150-153]
"""
import argparse
import os
import re
import resource
import statistics
import subprocess
import sys
import tempfile
import time
from pathlib import Path

import cleanenv


def run(argv, env, cpus):
    before = resource.getrusage(resource.RUSAGE_CHILDREN)
    t0 = time.perf_counter()
    subprocess.run(["taskset", "-c", cpus, *argv] if cpus else argv, env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, stdin=subprocess.DEVNULL, timeout=60)
    wall = (time.perf_counter() - t0) * 1000
    after = resource.getrusage(resource.RUSAGE_CHILDREN)
    return wall, (after.ru_utime - before.ru_utime + after.ru_stime - before.ru_stime) * 1000


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", required=True, type=Path)
    ap.add_argument("--arms", required=True)
    ap.add_argument("--runs", type=int, default=200)
    ap.add_argument("--cpus")
    a = ap.parse_args()
    dropped = cleanenv.apply()
    print(f"clean env: dropped {len(dropped)} variables ({', '.join(dropped)}); GOMAXPROCS unset: each arm sizes itself from its pinned cores", file=sys.stderr, flush=True)
    for name in a.arms.split(","):
        launcher = a.root / "bin" / name
        text = launcher.read_text()
        exe = re.search(r'exec "([^"]+)"', text).group(1)
        home = tempfile.mkdtemp(prefix="overhead-")
        (Path(home) / "models.json").write_text("{}")
        base = {k: v for k, v in os.environ.items() if not k.startswith(("PIG_", "PI_"))}
        env = {**base, "PI_CODING_AGENT_DIR": home}
        # what the launcher sets, applied by hand for the direct run
        direct_env = dict(env)
        if "PIG_CODING_AGENT_DIR" in text:
            direct_env.update(PIG_CODING_AGENT_DIR=home, PIG_OFFLINE="1", PIG_TELEMETRY="0", PIG_SKIP_VERSION_CHECK="1")
        direct_env["HOME"] = f"{home}/home"
        os.makedirs(direct_env["HOME"], exist_ok=True)
        for _ in range(5):
            run([str(launcher), "--version"], env, a.cpus)
        via, direct = [], []
        for _ in range(a.runs):
            via.append(run([str(launcher), "--version"], env, a.cpus))
            direct.append(run([exe, "--version"], direct_env, a.cpus))
        m = lambda xs, i: statistics.median(x[i] for x in xs)
        print(f"{name}: launcher wall {m(via, 0):.2f} ms cpu {m(via, 1):.2f} ms | direct wall {m(direct, 0):.2f} ms cpu {m(direct, 1):.2f} ms | adapter adds wall {m(via, 0) - m(direct, 0):+.2f} ms cpu {m(via, 1) - m(direct, 1):+.2f} ms (n={a.runs})")


if __name__ == "__main__":
    main()

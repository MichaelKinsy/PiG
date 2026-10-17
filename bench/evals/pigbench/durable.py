# SPDX-License-Identifier: MIT
"""Durable bench: the durable-bench workload on PiG Durable and on pi-durable, with durable-bench's cold/warm definitions and CPU-idle gate.

The workload is defined in workloads/durable-bench.json: histories of 50, 250, 1000 and 3500 turns (a one-tool, one-tool, zero-tool pattern),
then ten measured turns of eight tool calls each on a fresh copy of the seeded store. Cold is the open plus the first measured turn; warm is
the median of the next nine. Before each sample the bench waits for the CPUs it may use to be idle (busy fraction at most 8% over one second),
as durable-bench's gate does, and records the reading. Each sample runs in a fresh process, pinned with taskset when --cpus is given.
The method is re-implemented from the published description; no durable-bench code is copied.

Samples run as rounds: rounds are the outer loop, the sizes the inner one (rotated every round) and the order of the two runtimes alternates, so every size
sees the whole session's load. The warm turns of all samples are pooled for the reported median, p95 and maximum. `--rounds 12` gives the
100 warm turns per cell that durable-report's rules require; the default of 3 is durable-bench's own and is marked as below that rule.

Runtimes: `pig-durable` is bench/durableperf built from this checkout; `pi-durable` is durable/interop/bench.mjs on Node with the locked pi-durable
packages. The PiG 0.4.1, Pi, Claude Code, Codex and Copilot CLIs have no Durable layer, so they are not arms of this bench.
"""
import json
import os
import statistics
import subprocess
import time

from . import arms as armlib
from . import envinfo

WORKLOAD = os.path.join(armlib.lock.EVALS, "workloads", "durable-bench.json")


def load_workload():
    with open(WORKLOAD) as f:
        return json.load(f)


def cpu_times(cpus=None):
    """(busy, total) jiffies summed over `cpus` (all CPUs when None) from /proc/stat."""
    busy = total = 0
    with open("/proc/stat") as f:
        for line in f:
            if not line.startswith("cpu") or line.startswith("cpu "):
                continue
            name, *vals = line.split()
            if cpus is not None and int(name[3:]) not in cpus:
                continue
            v = list(map(int, vals))
            idle = v[3] + (v[4] if len(v) > 4 else 0)
            busy += sum(v[:8]) - idle
            total += sum(v[:8])
    return busy, total


def wait_idle(gate, cpus=None):
    """Block until the CPUs are idle enough; return {'busy': fraction, 'waited_s': seconds, 'passed': bool}."""
    start = time.monotonic()
    while True:
        b0, t0 = cpu_times(cpus)
        time.sleep(gate["window_s"])
        b1, t1 = cpu_times(cpus)
        busy = (b1 - b0) / max(1, t1 - t0)
        waited = time.monotonic() - start
        if busy <= gate["max_busy_fraction"]:
            return {"busy": round(busy, 4), "waited_s": round(waited, 1), "passed": True}
        if waited >= gate["max_wait_s"]:
            return {"busy": round(busy, 4), "waited_s": round(waited, 1), "passed": False}


def parse_cpus(text):
    cpus = set()
    for part in (text or "").split(","):
        if "-" in part:
            a, b = part.split("-")
            cpus |= set(range(int(a), int(b) + 1))
        elif part:
            cpus.add(int(part))
    return cpus or None


def summarize(sample):
    turns = sample["turn"]
    return {"cold_ms": sample["open"] + turns[0], "warm_ms": statistics.median(turns[1:]), "open_ms": sample["open"], "peak_rss_mib": sample.get("peakRss"),
            "cpu_s": sample.get("cpuSeconds"), "bytes": sample.get("bytes"), "fingerprint": sample.get("fingerprint"), "turns": turns}


def check_seed_fingerprints(result, work, cache, sizes):
    """Compare each seeded fixture's fingerprint with the published one. A fixture without its metadata is unverified, so it fails the run."""
    for size in sizes:
        meta = os.path.join(cache, f"pi-{size}.sqlite.json")
        want = work["published_fingerprints"].get(str(size))
        if not want:
            continue
        try:
            with open(meta) as f:
                got = json.load(f).get("fingerprint")
        except (OSError, ValueError) as error:
            result["failures"].setdefault(str(size), {}).setdefault("seed", []).append(f"no readable seed metadata {os.path.basename(meta)} ({type(error).__name__}); the fixture cannot be checked against the published {want}")
            continue
        result.setdefault("seed_fingerprints", {})[str(size)] = got
        if got != want:
            result["failures"].setdefault(str(size), {}).setdefault("seed", []).append(f"seeded history fingerprint {got} differs from the published {want}")


def run(opts):
    work = load_workload()
    sizes = [int(s) for s in (opts.sizes or work["sizes"])]
    samples = opts.rounds or work["samples"]
    cpus = parse_cpus(opts.cpus)
    prefix = ["taskset", "-c", opts.cpus] if opts.cpus else []
    cache = os.environ.get("PIGBENCH_DURABLE_CACHE") or os.path.join(armlib.REPO, "tmp", "bench", "durable-fixtures")
    result = {"kind": "durable", "workload": work, "options": {"sizes": sizes, "rounds": samples, "cpus": opts.cpus, "gate": not opts.no_gate}, "samples": {}, "failures": {}, "gate": []}
    with armlib.Workspace() as ws:
        binary = os.path.join(ws.root, "durableperf")
        armlib.lock.run(["go", "build", "-o", binary, "./bench/durableperf"], cwd=armlib.REPO, timeout=1800)
        deps = subprocess.run(["python3", "automation/ci/npm-locked.py", "durable/interop"], cwd=armlib.REPO, capture_output=True, text=True, timeout=900)
        if deps.returncode:
            raise SystemExit(f"durable/interop dependencies did not install:\n{deps.stderr[-1500:]}")
        os.makedirs(cache, exist_ok=True)
        missing = [s for s in sizes if not os.path.exists(os.path.join(cache, f"pi-{s}.sqlite"))]
        if missing:
            print(f"bench: seeding {missing} with pi-durable (cached in {cache})", flush=True)
            armlib.lock.run(["node", "--no-warnings", "durable/interop/bench.mjs", "seed", cache, ",".join(map(str, missing))], cwd=armlib.REPO, timeout=7200)
        result["env"] = envinfo.collect([], seed=opts.seed)
        with open(os.path.join(armlib.REPO, "durable", "interop", "node_modules", "@earendil-works", "pi-durable", "package.json")) as f:
            result["env"]["node_pi_durable"] = json.load(f)["version"]
        for i in range(samples):
            shift = i % len(sizes)
            for size in sizes[shift:] + sizes[:shift]:
                fixture = os.path.join(cache, f"pi-{size}.sqlite")
                order = ["pig-durable", "pi-durable"] if i % 2 == 0 else ["pi-durable", "pig-durable"]
                for rt in order:
                    gate = wait_idle(work["cpu_idle_gate"], cpus) if not opts.no_gate else {"passed": None}
                    result["gate"].append({"runtime": rt, "size": size, "sample": i, **gate})
                    if rt == "pig-durable":
                        argv = [*prefix, binary, "sample", "-model", "walk", "-provider", "faux", "-fixture", fixture, "-turns", str(size), "-sample", str(i)]
                    else:
                        argv = [*prefix, "node", "--no-warnings", "durable/interop/bench.mjs", "sample", fixture, str(size), str(i)]
                    done = subprocess.run(argv, cwd=armlib.REPO, capture_output=True, text=True, timeout=3600)
                    if done.returncode:
                        result["failures"].setdefault(str(size), {}).setdefault(rt, []).append(f"sample {i}: {done.stderr[-300:]}")
                        print(f"bench: {rt} {size} sample {i} FAILED", flush=True)
                        continue
                    line = json.loads(done.stdout.strip().splitlines()[-1])
                    s = summarize(line)
                    # Both runtimes run the same scripted turns on the same seeded store, so the transcript fingerprint after the measured turns must agree.
                    seen = result.setdefault("fingerprints", {}).setdefault(str(size), {})
                    seen.setdefault(rt, set()).add(s["fingerprint"])
                    result["samples"].setdefault(str(size), {}).setdefault(rt, []).append(s)
                    print(f"bench: round {i + 1}/{samples} {rt:12} {size:5} cold {s['cold_ms']:.0f} ms warm {s['warm_ms']:.0f} ms", flush=True)
    for size, by in result.get("fingerprints", {}).items():
        merged = {rt: sorted(v) for rt, v in by.items()}
        by.clear()
        by.update(merged)
        if len(merged) == 2 and (len(merged["pig-durable"]) != 1 or merged["pig-durable"] != merged["pi-durable"]):
            result["failures"].setdefault(size, {}).setdefault("fingerprint", []).append(f"transcript fingerprints differ between runtimes: {merged}")
    check_seed_fingerprints(result, work, cache, sizes)
    result["ok"] = not result["failures"] and all(g["passed"] is not False for g in result["gate"])
    os.makedirs(opts.out, exist_ok=True)
    with open(os.path.join(opts.out, "raw.json"), "w") as f:
        json.dump(result, f, indent=1)
    return result

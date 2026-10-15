# SPDX-License-Identifier: MIT
"""Startup bench with hyperfine 2.0: `<arm> --version` with --shell=none, in rotated rounds, with wall time, CPU time and peak resident memory.
hyperfine 2.0 has no memory metric on Windows, so there the bench measures time only and the report says so.

hyperfine comes from the pinned release in arms.lock (tools.hyperfine), installed into the throwaway workspace and checked against its sha256.
Each round runs every arm once through hyperfine (a fixed number of runs after a warm-up), the arm order rotating every round, and all
measurements of an arm are pooled. Hardware instruction counters are requested too and recorded when the kernel allows them
(perf_event_paranoid); where it does not, the report says so. Counts rank an implementation against itself only, never across languages.
"""
import json
import os
import shlex
import subprocess

from . import arms as armlib
from . import checkpoint, envinfo, lock
from .harness import Harness
from .mock import MockServer


def install_hyperfine(ws):
    spec = lock.read()["tools"]["hyperfine"]
    system, machine = armlib.host_platform()
    asset = spec["assets"].get(f"{system}-{machine}")
    if not asset:
        raise SystemExit(f"hyperfine {spec['version']} has no pinned asset for {system}-{machine}")
    dest = os.path.join(ws.root, "hyperfine")
    os.makedirs(dest, exist_ok=True)
    archive = os.path.join(dest, asset["name"])
    lock.fetch(spec["release"]["base"] + asset["name"], archive)
    got = lock.sha256_file(archive)
    size = os.path.getsize(archive)
    if got != asset["sha256"] or size != asset["size"]:
        raise SystemExit(f"hyperfine archive has sha256 {got} and {size} bytes, the lock pins {asset['sha256']} and {asset['size']} bytes")
    member = spec["release"]["member"] + (".exe" if system == "windows" else "")
    armlib.extract_member(archive, member, dest, "hyperfine", asset["name"])  # tar.gz, or zip on Windows
    binary = os.path.join(dest, member)
    os.chmod(binary, 0o755)
    version = subprocess.run([binary, "--version"], capture_output=True, text=True, timeout=30).stdout.strip()
    if spec["version"] not in version:
        raise SystemExit(f"hyperfine --version printed {version!r}, the lock pins {spec['version']}")
    return binary, {"version_line": version, "sha256": got, "source_url": spec["release"]["base"] + asset["name"]}


# hyperfine 2.0.0 on Windows: "Error: Metric 'memory_peak_resident' is unavailable on Windows".
MEMORY_UNAVAILABLE = {"windows": "hyperfine 2.0 has no memory metric on Windows (`memory_peak_resident` is unavailable there)"}


def command(binary):
    """The benchmarked command line. --shell=none splits it with POSIX shell-word rules, so a Windows path's backslashes need quoting."""
    return f"{shlex.quote(binary)} --version"


def measure(hyperfine, arm, env, cwd, runs, warmup, with_counters, out_json):
    memory = armlib.host_platform()[0] not in MEMORY_UNAVAILABLE
    metrics = "time_wall_clock,time_cpu" + (",memory_peak_resident" if memory else "") + (",instructions" if with_counters else "")
    argv = [hyperfine, "--shell=none", "--warmup", str(warmup), "--runs", str(runs), "--metrics", metrics, "--export-json", out_json, "--style", "none", command(arm.binary)]
    done = subprocess.run(argv, env=env, cwd=cwd, capture_output=True, text=True, timeout=600)
    if done.returncode:
        return None, done.stderr.strip()[-300:]
    with open(out_json) as f:
        return json.load(f)["results"][0]["measurements"], None


def sample_of(m):
    """One hyperfine 2.0 measurement (seconds, bytes) as a bench sample (ms, MiB)."""
    sample = {"wall_ms": m["time_wall_clock"]["value"] * 1000, "cpu_ms": m["time_cpu"]["value"] * 1000}
    if "memory_peak_resident" in m:
        sample["peak_rss_mib"] = m["memory_peak_resident"]["value"] / 2**20
    if "instructions" in m:
        sample["instructions"] = m["instructions"]["value"]
    return sample


def measure_arm(hyperfine, arm, h, runs, warmup, result, out_json):
    """Measure one arm once. Instruction counters are asked for until hyperfine reports them unavailable; then result['counters'] is False,
    the reason is kept, and this and every later measurement runs without them. Returns hyperfine's measurements, or None after recording the failure."""
    want_counters = result["counters"] is not False
    rows, error = measure(hyperfine, arm, h.env, h.cwd, runs, warmup, want_counters, out_json)
    if rows is None and want_counters and "unavailable" in (error or ""):
        result["counters"] = False
        result["counters_reason"] = error
        rows, error = measure(hyperfine, arm, h.env, h.cwd, runs, warmup, False, out_json)
    elif rows is not None and want_counters:
        result["counters"] = True
    if rows is None:
        result["failures"].setdefault(arm.id, []).append(error)
        return None
    for m in rows:
        if m.get("exit_code") not in (0, None):
            result["failures"].setdefault(arm.id, []).append(f"exit code {m['exit_code']}")
            continue
        result["samples"].setdefault(arm.id, []).append(sample_of(m))
    return rows


def run(opts):
    arm_list = armlib.select(armlib.load(), opts.arms)
    result = {"kind": "startup", "options": {"rounds": opts.rounds, "runs_per_round": opts.runs, "warmup": opts.warmup, "seed": opts.seed}, "samples": {}, "failures": {}, "counters": None}
    with armlib.Workspace() as ws:
        hyperfine, tool = install_hyperfine(ws)
        ws.install_all(arm_list)
        result["env"] = envinfo.collect(arm_list, seed=opts.seed)
        result["env"]["tools"] = {"hyperfine": tool}
        if armlib.host_platform()[0] in MEMORY_UNAVAILABLE:
            result["memory_reason"] = MEMORY_UNAVAILABLE[armlib.host_platform()[0]]
        runnable = [a for a in arm_list if a.binary]
        if not runnable:
            raise SystemExit("bench startup: no selected arm could be installed")
        with MockServer() as server:
            harness = {a.id: Harness(a, ws.work, server, tag="startup") for a in runnable}
            tmp_json = os.path.join(ws.root, "hf.json")
            for rnd in range(opts.rounds):
                shift = rnd % len(runnable)
                for a in runnable[shift:] + runnable[:shift]:
                    rows = measure_arm(hyperfine, a, harness[a.id], opts.runs, opts.warmup if rnd == 0 else 1, result, tmp_json)
                    if rows is None:
                        print(f"bench: round {rnd + 1}/{opts.rounds} {a.id} FAILED: {result['failures'][a.id][-1]}", flush=True)
                        continue
                    print(f"bench: round {rnd + 1}/{opts.rounds} {a.id:12} {len(rows)} runs", flush=True)
                    checkpoint.save(result, opts.out)
    result["ok"] = not result["failures"]
    os.makedirs(opts.out, exist_ok=True)
    with open(os.path.join(opts.out, "raw.json"), "w") as f:
        json.dump(result, f, indent=1)
    checkpoint.clear(opts.out)
    return result

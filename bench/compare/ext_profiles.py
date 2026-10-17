#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Extension-profile scenario (ADDENDUM 2 row 3): the same extension set on every arm that can load it.

Profiles: none; ts (Pi 1.1.0's own todo.ts and subagent example plus ext/ts/heavy-tool-call.ts, a tool_call handler doing ~2 ms of work per call);
go (PiG: ext/go); multi (PiG only: Go + Rust + Python + TypeScript, each with the same ~2 ms tool_call handler). Every handler appends a line to
$BENCHEXT_COUNTER, so "loaded" is proven by invocations (5 prompts x 4 tool calls = 20 per handler), not by the absence of an error. An arm whose
session cannot start, shows a load error, or never invokes the handlers is recorded as "cannot load" with the reason.

Per (profile, arm), --rounds interleaved interactive sessions of --prompts prompts: time to interactive, per-prompt p50/p95 and CPU, PSS/RSS/private dirty at
ready and after the prompts (process tree: a Node, Python or Rust runner counts), handler invocations. The first session of a PiG profile that compiles an
extension is a cold build and is recorded separately (cold_ms), not pooled.

  ext_profiles.py --root ROOT --arms pig-tip,pi-node,pibolt-0.7.4,omp --profiles none,ts,go,multi --rounds 5 --out FILE.jsonl
"""
import argparse
import json
import os
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
KEEP_HOME = Path(tempfile.gettempdir()) / "bolt-bench-ext-home"
HANDLERS = {"none": 0, "ts": 1, "go": 1, "multi": 4}  # tool_call handlers per profile; each runs once per tool call (4 per prompt)
sys.path.insert(0, str(HERE))
import ctx_scale as cs  # noqa: E402
import fake_server  # noqa: E402


def stage(root: Path):
    """Copy the extension sources into the pig-tip tree (their go.mod / Cargo.toml / sys.path reach the SDK by relative path) and return profile -> {arm kind: args}."""
    tip = root / "src" / "pig-tip" / "examples" / "extensions"
    for name, src in (("bench-go", "go"), ("bench-py", "py"), ("bench-rs", "rs")):
        dst = tip / name
        shutil.rmtree(dst, ignore_errors=True)
        shutil.copytree(HERE / "ext" / src, dst)
    pi_ex = root / "arms/pi-node/node_modules/@earendil-works/pi-coding-agent/examples/extensions"
    ts = ["-e", str(pi_ex / "todo.ts"), "-e", str(pi_ex / "subagent" / "index.ts"), "-e", str(HERE / "ext" / "ts" / "heavy-tool-call.ts")]
    return {
        "none": {"all": []},
        "ts": {"all": ts},
        "go": {"pig": ["-e", str(tip / "bench-go")]},
        "multi": {"pig": ["-e", str(tip / "bench-go"), "-e", str(tip / "bench-py"), "-e", str(tip / "bench-rs"), *ts]},
    }


def session(h, argv, extra, n, cpus, counter):
    with fake_server.fake_model(h) as port, h.pi_home(port) as home, h.workdir() as cwd:
        # A persistent HOME for PiG (its extension build cache lives under it) and the real toolchain homes, so a session does not rebuild or refetch.
        keep = {"BENCH_HOME": str(KEEP_HOME), "CARGO_HOME": os.environ.get("CARGO_HOME", os.path.expanduser("~/.cargo")), "RUSTUP_HOME": os.environ.get("RUSTUP_HOME", os.path.expanduser("~/.rustup")),
                "GOPATH": os.environ.get("GOPATH", os.path.expanduser("~/go"))}
        env = h.pi_env(home, {"BENCHEXT_COUNTER": counter, **keep})
        t0 = time.perf_counter()
        tty = h.Tty(h.pinned([*argv, *extra, "--no-session", *h.MODEL_ARGS], cpus), env, cwd)
        deadline = t0 + 400
        if not tty.wait_for("fake-model", 0, deadline):
            screen = h.ANSI.sub(b" ", tty.buf).decode("utf8", "replace")
            tty.quit(1)
            return {"ok": False, "reason": "never became interactive: " + " ".join(screen.split())[-200:]}
        tti = (time.perf_counter() - t0) * 1000
        tty.settle(0.5, deadline)
        screen = " ".join(h.ANSI.sub(b" ", tty.buf).decode("utf8", "replace").split())
        err = [w for w in ("Failed to load", "failed to load", "Extension error", "extension error", "Cannot find", "Error loading", "unknown option", "Unknown option") if w in screen]
        ready = cs.tree_smaps(tty.pid)
        lat, cpu = [], []
        for i in range(1, n + 1):
            start = len(tty.buf)
            c0, ts = h.cpu_ms(tty.pid), time.perf_counter()
            tty.send(h.PROMPT.encode() + b"\r")
            if not tty.wait_for(h.done(i), start, deadline):
                tty.quit(1)
                return {"ok": False, "reason": f"no answer to prompt {i}", "tti_ms": round(tti)}
            lat.append((time.perf_counter() - ts) * 1000)
            tty.settle(0.03, deadline)
            cpu.append(h.cpu_ms(tty.pid) - c0)
        after = cs.tree_smaps(tty.pid)
        tty.quit()
        calls = len(Path(counter).read_text().splitlines()) if Path(counter).exists() else 0
        lat.sort()
        return {"ok": True, "tti_ms": round(tti), "p50_ms": round(statistics.median(lat)), "p95_ms": round(lat[-1]), "cpu_ms_per_prompt": round(statistics.mean(cpu), 1),
                **{f"ready_{k}": v for k, v in ready.items()}, **{f"after_{k}": v for k, v in after.items()}, "handler_calls": calls, "load_errors": err}


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", required=True, type=Path)
    ap.add_argument("--arms", required=True)
    ap.add_argument("--profiles", default="none,ts,go,multi")
    ap.add_argument("--rounds", type=int, default=5)
    ap.add_argument("--prompts", type=int, default=5)
    ap.add_argument("--cpus")
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    dropped = cleanenv.apply()
    print(f"clean env: dropped {len(dropped)} variables ({', '.join(dropped)}); GOMAXPROCS unset: each arm sizes itself from its pinned cores", file=sys.stderr, flush=True)
    root = a.root.resolve()
    h = cs.load_harness(root)
    specs = dict(l.split("=", 1) for l in subprocess.run([sys.executable, str(HERE / "arms.py"), "specs", "--root", str(root)], capture_output=True, text=True, check=True).stdout.splitlines())
    profiles = stage(root)
    arms = a.arms.split(",")
    with tempfile.TemporaryDirectory(prefix="extprof-") as tmp:
        cold = {}
        for r in range(-1, a.rounds):  # round -1: warm-up / cold build, not pooled
            for prof in a.profiles.split(","):
                for name in arms[r % len(arms):] + arms[:r % len(arms)] if r >= 0 else arms:
                    kind = "pig" if name.startswith("pig") else "other"
                    args = profiles[prof].get("all") if "all" in profiles[prof] else profiles[prof].get(kind)
                    if args is None:
                        row = {"profile": prof, "arm": name, "round": r, "ok": False, "reason": "cannot load: the profile needs PiG's Go/Rust/Python extension runtime"}
                        if r == 0:
                            open(a.out, "a").write(json.dumps(row) + "\n")
                        continue
                    counter = os.path.join(tmp, f"{name}-{prof}-{r}.count")
                    ts = time.perf_counter()
                    res = session(h, shlex.split(specs[name]), args, a.prompts, a.cpus, counter)
                    want = a.prompts * 4 * HANDLERS[prof]
                    if res.get("ok") and (res["handler_calls"] < want or res["load_errors"]):
                        res.update(ok=False, reason=f"cannot load: {res['handler_calls']} handler calls (expected {want}), errors {res['load_errors']}")
                    row = {"profile": prof, "arm": name, "round": r, **res}
                    if r < 0:
                        row["cold_ms"] = round((time.perf_counter() - ts) * 1000)
                    with open(a.out, "a") as f:
                        f.write(json.dumps(row) + "\n")
                    print({k: v for k, v in row.items() if k in ("profile", "arm", "round", "ok", "reason", "tti_ms", "p50_ms", "cpu_ms_per_prompt", "after_pss", "handler_calls", "cold_ms")}, flush=True)


if __name__ == "__main__":
    main()

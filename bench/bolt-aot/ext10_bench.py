#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Ready time, per-turn CPU split and RSS/PSS of PiG's TypeScript runtimes on the ten pigbench extensions (the rpc-ext10 shape).
Arms: node; bun; aot = the compiled-extensions Piglet with the ten extensions compiled into its host at build time; aot-dyn = the prebuilt aot
host (jiti compiled in, no extension compiled in) that loads the ten from source at run time. Each run: pig --mode rpc with the ten extensions, a scripted
model, launch to the ten commands listed (ready), then TURNS tool-call turns ("call NN": two model requests and one tool call per turn). CPU is /proc
utime+stime of the pig process (the Go host) and of its descendants (the extension host), at ready and over the turns. Memory is RSS and PSS of both.
Arms are interleaved with the order rotated each round after a warm-up. usage: ext10_bench.py --out DIR --rounds N --pig PIG --slot SLOT --dyn-slot SLOT [--arms ...]"""
import argparse, json, os, shutil, socket, statistics, subprocess, sys, tempfile, time, glob
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import bench, equivalence

TURNS = 20
HZ = os.sysconf("SC_CLK_TCK")
EXE = {"node": "node", "node2": "node", "bun": "bun", "aot": "pig-ts-host", "aot-dyn": "pig-ts-host"}
SLOT = {}

def env_for(arm):
    if arm in ("node", "node2"): return {"PIG_TS_RUNTIME": "node"}
    return {"PIG_TS_RUNTIME": "bun" if arm == "bun" else "aot", "PIG_TS_RUNTIMES_DIR": SLOT[arm]}

def split(pid):
    procs = {p: bench.mem(p) for p in bench.proc_tree(pid)}
    procs = {p: m for p, m in procs.items() if m}
    ext = [m for p, m in procs.items() if p != pid]
    return {"pig_ticks": procs[pid]["cpu_ticks"], "ext_ticks": sum(m["cpu_ticks"] for m in ext),
            "pig_rss": procs[pid]["Rss"] / 1024, "pig_pss": procs[pid]["Pss"] / 1024,
            "ext_rss": sum(m["Rss"] for m in ext) / 1024, "ext_pss": sum(m["Pss"] for m in ext) / 1024,
            "ext_exes": sorted({m["exe"] for m in ext}), "ext_n": len(ext)}

def run_once(arm, agent_dir, home, work, exts, model):
    env = {"PATH": os.environ["PATH"], "HOME": home, "PIG_HOME": f"{home}/.pig", "PIG_CODING_AGENT_DIR": agent_dir, "PI_CODING_AGENT_DIR": agent_dir, "PIG_OFFLINE": "1",
           "PI_OFFLINE": "1", "PIG_TELEMETRY": "0", "PIG_SKIP_VERSION_CHECK": "1", "TERM": "dumb", "TMPDIR": f"{home}/tmp", **env_for(arm)}
    os.makedirs(env["TMPDIR"], exist_ok=True)
    argv = [bench.CFG["pig"], "--mode", "rpc", "--no-session", "--offline", "-ne", "--provider", "fake", "--model", "fake-model"]
    for e in exts: argv += ["-e", e]
    t0 = time.time(); rpc = bench.Rpc(argv, env, work); out = {"arm": arm, "loadavg": open("/proc/loadavg").read().split()[:3]}
    try:
        rid = rpc.send({"type": "get_commands"})
        ev = rpc.until(lambda e: e.get("type") == "response" and e.get("id") == rid)
        out["ready_ms"] = (time.time() - t0) * 1000
        if sum(1 for c in (ev.get("data") or {}).get("commands", []) if str(c.get("name", "")).startswith("bench-")) != 10: raise RuntimeError("the ten extensions did not load")
        time.sleep(0.3); a = split(rpc.p.pid)
        if EXE[arm] not in a["ext_exes"]: raise RuntimeError(f"arm {arm} ran {a['ext_exes']}, not {EXE[arm]}")
        out.update(ready_pig_cpu_ms=a["pig_ticks"] * 1000 / HZ, ready_ext_cpu_ms=a["ext_ticks"] * 1000 / HZ, ready_pig_rss=a["pig_rss"], ready_pig_pss=a["pig_pss"], ready_ext_rss=a["ext_rss"], ready_ext_pss=a["ext_pss"], ext_n=a["ext_n"])
        tw = time.time()
        for i in range(TURNS):
            rpc.send({"type": "prompt", "message": f"call {i % 10 + 1:02d}"}); rpc.until(lambda e: e.get("type") == "agent_end", 120)
        out["turn_wall_ms"] = (time.time() - tw) * 1000 / TURNS
        time.sleep(0.3); b = split(rpc.p.pid)
        out.update(turn_pig_cpu_ms=(b["pig_ticks"] - a["pig_ticks"]) * 1000 / HZ / TURNS, turn_ext_cpu_ms=(b["ext_ticks"] - a["ext_ticks"]) * 1000 / HZ / TURNS,
                   after_pig_rss=b["pig_rss"], after_pig_pss=b["pig_pss"], after_ext_rss=b["ext_rss"], after_ext_pss=b["ext_pss"])
    finally:
        rpc.close()
    return out

METRICS = [("ready_ms", "Launch to ready (ms)"), ("ready_pig_cpu_ms", "CPU to ready, Go host (ms)"), ("ready_ext_cpu_ms", "CPU to ready, extension host (ms)"),
           ("turn_pig_cpu_ms", "CPU per turn, Go host (ms)"), ("turn_ext_cpu_ms", "CPU per turn, extension host (ms)"), ("turn_wall_ms", "Wall per turn (ms)"),
           ("ready_pig_rss", "RSS at ready, Go host (MB)"), ("ready_ext_rss", "RSS at ready, extension host (MB)"), ("ready_ext_pss", "PSS at ready, extension host (MB)"),
           ("after_pig_rss", f"RSS after {TURNS} turns, Go host (MB)"), ("after_ext_rss", f"RSS after {TURNS} turns, extension host (MB)"), ("after_ext_pss", f"PSS after {TURNS} turns, extension host (MB)")]

def summarize(runs, arms):
    lines = ["| Metric | " + " | ".join(arms) + " |", "|---|" + "---:|" * len(arms)]
    ok = {a: [r for r in runs if r["arm"] == a and "error" not in r] for a in arms}
    lines.append("| Runs ok / total | " + " | ".join(f"{len(ok[a])}/{sum(1 for r in runs if r['arm'] == a)}" for a in arms) + " |")
    for k, label in METRICS:
        cells = []
        for a in arms:
            v = [r[k] for r in ok[a] if k in r]
            cells.append(f"{statistics.median(v):.0f} [{min(v):.0f}-{max(v):.0f}]" if v else "-")
        lines.append(f"| {label} | " + " | ".join(cells) + " |")
    return "\n".join(lines)

def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--out", required=True); ap.add_argument("--rounds", type=int, default=12); ap.add_argument("--arms", default="node,bun,aot,aot-dyn")
    ap.add_argument("--pig", required=True); ap.add_argument("--slot", required=True, help="slot with stock Bun and the aot image built for the ten extensions"); ap.add_argument("--dyn-slot", required=True)
    ap.add_argument("--exts", default=equivalence.DEFAULT_EXTS)
    a = ap.parse_args(); os.makedirs(a.out, exist_ok=True)
    bench.CFG.update(pig=os.path.abspath(a.pig)); SLOT.update({"bun": os.path.abspath(a.slot), "aot": os.path.abspath(a.slot), "aot-dyn": os.path.abspath(a.dyn_slot)})
    exts = sorted(glob.glob(os.path.join(os.path.abspath(a.exts), "bench-ext-*.ts"))); assert len(exts) == 10
    arms = a.arms.split(",")
    base = tempfile.mkdtemp(prefix="ext10-", dir=os.environ.get("TMPDIR", "/tmp")); work = f"{base}/work"; os.makedirs(work)
    port = (lambda s: (s.bind(("127.0.0.1", 0)), s.getsockname()[1], s.close())[1])(socket.socket()); model = equivalence.Model(port)
    homes = {}
    for arm in arms:
        h = f"{base}/{arm}"; ad = f"{h}/agent"; os.makedirs(ad, exist_ok=True)
        json.dump({"providers": {"fake": {"baseUrl": f"http://127.0.0.1:{port}/v1", "api": "openai-completions", "apiKey": "fake", "models": [{"id": "fake-model", "name": "fake", "contextWindow": 200000, "maxTokens": 8192, "input": ["text"], "reasoning": False, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}}, open(f"{ad}/models.json", "w"))
        json.dump({"lastChangelogVersion": "9999.0.0"}, open(f"{ad}/settings.json", "w")); open(f"{ad}/auth.json", "w").write("{}"); homes[arm] = (ad, h)
    runs = []
    try:
        for arm in arms:
            try: run_once(arm, *homes[arm], work, exts, model); print("warm", arm, flush=True)
            except Exception as e: print("warm FAIL", arm, e, flush=True)
        for r in range(a.rounds):
            for arm in arms[r % len(arms):] + arms[:r % len(arms)]:
                try: res = run_once(arm, *homes[arm], work, exts, model)
                except Exception as e: res = {"arm": arm, "error": str(e)[:1000]}
                res["round"] = r; runs.append(res); print(json.dumps({k: res.get(k) for k in ("arm", "round", "ready_ms", "turn_pig_cpu_ms", "turn_ext_cpu_ms", "error")}), flush=True)
                with open(f"{a.out}/runs.jsonl", "a") as f: f.write(json.dumps(res) + "\n")
    finally:
        model.server.shutdown(); shutil.rmtree(base, ignore_errors=True)
    md = summarize(runs, arms) + f"\n\nHost load average (1 min) during runs: median {statistics.median(float(r['loadavg'][0]) for r in runs if 'loadavg' in r):.0f}\n"
    open(f"{a.out}/summary.md", "w").write(md); print(md)

if __name__ == "__main__":
    main()

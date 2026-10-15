#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Benchmark of PiG's TypeScript extension runtimes: Node, Bun and the compiled aot host, on one set of five extensions.
Derived from the Pi-Bolt hosting spike's harness (its bench/bolt-host/bench/bench.py), reduced to
the tiers PiG ships and driven by PIG_TS_RUNTIME and a runtime slot instead of a `node` shim on PATH.
Full path: pig --mode rpc with the extension set, a scripted local model, interleaved arms with the order rotated each round.
Per run: launch-to-ready (spawn -> get_commands answered with the extension commands), memory (RSS/PSS/private) of pig and
of the extension process(es) at ready and after 5 prompts, /words hot loop (in-extension ms, wall, extension CPU), tool turn
gap (model response with the tool call -> next model request) and prompt wall time.
The aot arm needs compiled images for the set: run once with --build, which launches the set so that PiG records the build
request and runs the Piglet's worker over the slot (PIG_AOT_BENCH_BUILD, see README.md).
usage: bench.py --out DIR --rounds N --arms node,bun,aot --pig PIG --slot SLOT --exts DIR --bun BUN"""
import argparse, json, os, random, shutil, signal, socket, statistics, subprocess, sys, tempfile, time
CFG = {}
TIER_EXE = {"node": "node", "bun": "bun", "aot": "pig-ts-host"}

def exts():
    n = f"{CFG['exts']}/node_modules"
    here = os.path.dirname(os.path.abspath(__file__))
    upstream = f"{here}/../../.upstream/current/packages/coding-agent/examples/extensions"
    return [f"{n}/@tintinweb/pi-subagents", f"{os.path.normpath(upstream)}/todo.ts", f"{os.path.normpath(upstream)}/permission-gate.ts", f"{here}/exts/heavy-toolcall.ts", f"{here}/exts/word-count.ts"]

def arm_env(arm):
    if arm == "node": return {"PIG_TS_RUNTIME": "node"}
    if arm in ("bun", "aot"): return {"PIG_TS_RUNTIME": arm, "PIG_TS_RUNTIMES_DIR": CFG["slot"]}
    raise SystemExit(f"unknown arm {arm}")

def proc_tree(root):
    kids = {}
    for p in os.listdir("/proc"):
        if not p.isdigit(): continue
        try:
            st = open(f"/proc/{p}/stat").read(); ppid = int(st[st.rindex(")") + 2:].split()[1])
        except Exception: continue
        kids.setdefault(ppid, []).append(int(p))
    out, todo = [], [root]
    while todo:
        q = todo.pop(); out.append(q); todo.extend(kids.get(q, []))
    return out

def mem(pid):
    r = {}
    try:
        for line in open(f"/proc/{pid}/smaps_rollup"):
            k, _, v = line.partition(":")
            if k in ("Rss", "Pss", "Private_Clean", "Private_Dirty"): r[k] = int(v.split()[0])
        st = open(f"/proc/{pid}/stat").read(); f = st[st.rindex(")") + 2:].split()
        r["cpu_ticks"] = int(f[11]) + int(f[12])
        r["comm"] = open(f"/proc/{pid}/comm").read().strip()
        r["exe"] = os.path.basename(os.readlink(f"/proc/{pid}/exe"))
    except Exception: return None
    return r

def snapshot(pig_pid):
    procs = {p: mem(p) for p in proc_tree(pig_pid)}
    procs = {p: m for p, m in procs.items() if m}
    ext = {p: m for p, m in procs.items() if p != pig_pid}
    tot = lambda d, k: sum(m.get(k, 0) for m in d.values())
    return {"pig_rss_kb": procs.get(pig_pid, {}).get("Rss"), "pig_pss_kb": procs.get(pig_pid, {}).get("Pss"),
            "ext_n": len(ext), "ext_rss_kb": tot(ext, "Rss"), "ext_pss_kb": tot(ext, "Pss"),
            "ext_private_kb": tot(ext, "Private_Dirty") + tot(ext, "Private_Clean"), "ext_private_dirty_kb": tot(ext, "Private_Dirty"),
            "ext_cpu_ticks": tot(ext, "cpu_ticks"),
            "ext_exes": sorted({m["exe"] for m in ext.values()})}

class Rpc:
    def __init__(self, argv, env, cwd):
        self.p = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, cwd=cwd, start_new_session=True)
        self.n = 0
    def send(self, obj):
        self.n += 1; obj["id"] = f"r{self.n}"
        self.p.stdin.write((json.dumps(obj) + "\n").encode()); self.p.stdin.flush(); return obj["id"]
    def until(self, pred, timeout=120):
        end = time.time() + timeout
        while time.time() < end:
            line = self.p.stdout.readline()
            if not line: raise RuntimeError("pig exited: " + self.p.stderr.read().decode()[-2000:])
            try: ev = json.loads(line)
            except Exception: continue
            if pred(ev): return ev
        raise TimeoutError("rpc timeout")
    def close(self):
        try: os.killpg(self.p.pid, signal.SIGTERM); self.p.wait(10)
        except Exception:
            try: os.killpg(self.p.pid, signal.SIGKILL)
            except Exception: pass

def run_once(arm, agent_dir, home, work, port, big, model_log):
    env = {"PATH": os.environ["PATH"], "HOME": home, "PIG_HOME": f"{home}/.pig", "PIG_CODING_AGENT_DIR": agent_dir,
           "PI_CODING_AGENT_DIR": agent_dir, "PIG_OFFLINE": "1", "PI_OFFLINE": "1", "PIG_TELEMETRY": "0", "PIG_SKIP_VERSION_CHECK": "1",
           "PI_TIMING": "1", "TERM": "dumb", "TMPDIR": f"{home}/tmp", **arm_env(arm)}
    os.makedirs(env["TMPDIR"], exist_ok=True)
    argv = [CFG["pig"], "--mode", "rpc", "--no-session", "--offline", "-ne", "--provider", "fake", "--model", "fake-model"]
    for e in exts(): argv += ["-e", e]
    t0 = time.time(); rpc = Rpc(argv, env, work)
    out = {"arm": arm, "loadavg": open("/proc/loadavg").read().split()[:3]}
    try:
        rid = rpc.send({"type": "get_commands"})
        ev = rpc.until(lambda e: e.get("type") == "response" and e.get("id") == rid)
        out["ready_ms"] = (time.time() - t0) * 1000
        names = [c.get("name") for c in (ev.get("data") or {}).get("commands", [])]
        out["has_words"] = "words" in names
        if not out["has_words"]: raise RuntimeError("the /words command is missing: the extension set did not load")
        time.sleep(0.3)
        out["mem_ready"] = snapshot(rpc.p.pid)
        # An arm that silently fell back to another tier would measure the wrong thing.
        if TIER_EXE[arm] not in out["mem_ready"]["ext_exes"]:
            raise RuntimeError(f"arm {arm} ran {out['mem_ready']['ext_exes']}, not {TIER_EXE[arm]}")
        # /words hot loop
        cpu0 = snapshot(rpc.p.pid)["ext_cpu_ticks"]; tw = time.time()
        rpc.send({"type": "prompt", "message": f"/words {big}"})
        ev = rpc.until(lambda e: e.get("type") == "extension_ui_request" and "words (" in str(e.get("message", "")))
        out["words_wall_ms"] = (time.time() - tw) * 1000
        msg = ev["message"]; out["words_ext_ms"] = float(msg.rsplit("(", 1)[1].split(" ms")[0])
        out["words_ext_cpu_ms"] = (snapshot(rpc.p.pid)["ext_cpu_ticks"] - cpu0) * 1000 / os.sysconf("SC_CLK_TCK")
        # 5 prompts with a tool call each
        nlog0 = sum(1 for _ in open(model_log))
        walls = []
        for i in range(5):
            tp = time.time(); rpc.send({"type": "prompt", "message": f"prompt {i}"})
            rpc.until(lambda e: e.get("type") == "agent_end", 120); walls.append((time.time() - tp) * 1000)
        out["prompt_wall_ms"] = walls
        recs = [json.loads(l) for l in open(model_log)][nlog0:]
        gaps = [(recs[k + 1]["t_req"] - recs[k]["t_end"]) * 1000 for k in range(len(recs) - 1) if recs[k]["kind"] == "toolcall"]
        out["tool_gap_ms"] = gaps
        time.sleep(0.3)
        out["mem_after5"] = snapshot(rpc.p.pid)
    finally:
        stderr_tail = None
        rpc.close()
        try: stderr_tail = rpc.p.stderr.read().decode(errors="replace")
        except Exception: pass
        if stderr_tail: out["timing"] = [l for l in stderr_tail.splitlines() if "module import" in l or "factory" in l or "extensions" in l.lower()][:40]
    return out

def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--out", required=True); ap.add_argument("--rounds", type=int, default=5)
    ap.add_argument("--arms", default="node,bun,aot"); ap.add_argument("--port", type=int, default=0)
    ap.add_argument("--pig", required=True); ap.add_argument("--slot", required=True); ap.add_argument("--exts", required=True)
    ap.add_argument("--build-cmd", help="command that runs the Piglet's worker over the slot: it installs the aot manifest and the compiler, then builds the requests a launch recorded")
    a = ap.parse_args(); os.makedirs(a.out, exist_ok=True)
    CFG.update(pig=os.path.abspath(a.pig), slot=os.path.abspath(a.slot), exts=os.path.abspath(a.exts))
    arms = a.arms.split(",")
    base = tempfile.mkdtemp(prefix="aot-bench-", dir=os.environ.get("TMPDIR", "/tmp"))
    work = f"{base}/work"; os.makedirs(work)
    big = f"{base}/big.txt"; small = f"{work}/small.txt"
    rnd = random.Random(7); words = ["alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta\n", "iota\t"]
    with open(big, "w") as f:
        n = 0
        while n < 16_800_000:
            s = " ".join(rnd.choice(words) for _ in range(1000)) + "\n"; f.write(s); n += len(s)
    open(small, "w").write("one two three\nfour five\n")
    port = a.port or (lambda s: (s.bind(("127.0.0.1", 0)), s.getsockname()[1], s.close())[1])(socket.socket())
    model_log = f"{a.out}/model.jsonl"; open(model_log, "a").close()
    fm = subprocess.Popen([sys.executable, os.path.join(os.path.dirname(os.path.abspath(__file__)), "fake_model.py"), str(port), small, model_log])
    time.sleep(0.5)
    homes = {}
    for arm in arms:
        h = f"{base}/{arm}"; ad = f"{h}/agent"; os.makedirs(ad, exist_ok=True)
        json.dump({"providers": {"fake": {"baseUrl": f"http://127.0.0.1:{port}/v1", "api": "openai-completions", "apiKey": "fake",
                   "models": [{"id": "fake-model", "name": "fake", "contextWindow": 200000, "maxTokens": 8192, "input": ["text"], "reasoning": False,
                               "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}}, open(f"{ad}/models.json", "w"))
        json.dump({"lastChangelogVersion": "9999.0.0"}, open(f"{ad}/settings.json", "w")); open(f"{ad}/auth.json", "w").write("{}")
        homes[arm] = (ad, h)
    results = []
    try:
        for arm in arms:  # warm-up: launcher build, jiti and compile caches
            if arm == "aot" and a.build_cmd:
                # The Piglet's worker installs the runtime's manifest and the compiler; the next launch runs on Bun and records the build request;
                # the worker builds it; the launch after that runs on the compiled host.
                subprocess.run(a.build_cmd, shell=True, check=True)
                try: run_once(arm, homes[arm][0], homes[arm][1], work, port, big, model_log)
                except RuntimeError as e: print("warm aot before its build:", e, flush=True)
                subprocess.run(a.build_cmd, shell=True, check=True)
            try: run_once(arm, homes[arm][0], homes[arm][1], work, port, big, model_log); print("warm", arm, flush=True)
            except Exception as e: print("warm FAIL", arm, e, flush=True)
        for r in range(a.rounds):
            order = arms[r % len(arms):] + arms[:r % len(arms)]
            for arm in order:
                try:
                    res = run_once(arm, homes[arm][0], homes[arm][1], work, port, big, model_log); res["round"] = r
                except Exception as e:
                    res = {"arm": arm, "round": r, "error": str(e)[:2000]}
                results.append(res); print(json.dumps({k: res.get(k) for k in ("arm", "round", "ready_ms", "words_ext_ms", "error")}), flush=True)
                with open(f"{a.out}/runs.jsonl", "a") as f: f.write(json.dumps(res) + "\n")
    finally:
        fm.terminate()
        shutil.rmtree(base, ignore_errors=True)

if __name__ == "__main__":
    main()

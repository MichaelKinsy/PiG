#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Equivalence of PiG's TypeScript extension runtimes on the ten pigbench extensions (bench/evals/fixtures/extensions/bench-ext-NN.ts).
Each arm runs the same scripted session through pig --mode rpc: get_commands, the tools the first model request declares, one prompt per extension
whose scripted model calls bench_tool_NN (the tool result and details are recorded), the /bench-NN commands, and a last prompt that reads every
handler's cumulative event count back. Each section is canonicalised to JSON and hashed; the arms are equivalent when every hash is equal.
node2 repeats node as the A/A control: it shows the session is deterministic before it is used to compare runtimes.
usage: equivalence.py --out DIR --pig PIG --slot SLOT [--exts DIR] [--arms node,node2,bun,aot] [--build-cmd CMD]"""
import argparse, glob, hashlib, json, os, shutil, socket, subprocess, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import bench

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_EXTS = os.path.join(HERE, "..", "evals", "fixtures", "extensions")
N = 10


class Model:
    """OpenAI-compatible scripted model: a prompt "call NN" answers with a bench_tool_NN call, the tool result answers with text."""
    def __init__(self, port):
        self.requests = []
        outer = self
        class H(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"
            def log_message(self, *a): pass
            def do_GET(self):
                b = json.dumps({"object": "list", "data": [{"id": "fake-model", "object": "model"}]}).encode()
                self.send_response(200); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
                outer.requests.append(body)
                msgs = body["messages"]
                last = msgs[-1]
                self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.send_header("Transfer-Encoding", "chunked"); self.end_headers()
                def w(b): self.wfile.write(f"{len(b):x}\r\n".encode() + b + b"\r\n"); self.wfile.flush()
                def chunk(delta, finish=None):
                    return ("data: " + json.dumps({"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "fake-model", "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}) + "\n\n").encode()
                text = last["content"] if isinstance(last.get("content"), str) else "".join(p.get("text", "") for p in last.get("content") or [])
                if last["role"] == "user" and text.startswith("call "):
                    nn = text.split()[1]
                    w(chunk({"role": "assistant", "tool_calls": [{"index": 0, "id": f"call_{nn}", "type": "function", "function": {"name": f"bench_tool_{nn}", "arguments": json.dumps({"value": f"v{nn}"})}}]}))
                    w(chunk({}, "tool_calls"))
                else:
                    w(chunk({"role": "assistant", "content": "Done."})); w(chunk({}, "stop"))
                w(b"data: [DONE]\n\n"); self.wfile.write(b"0\r\n\r\n"); self.wfile.flush()
        self.server = ThreadingHTTPServer(("127.0.0.1", port), H)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()


def canon(x):
    return json.dumps(x, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def session(arm, agent_dir, home, work, exts, model):
    env = {"PATH": os.environ["PATH"], "HOME": home, "PIG_HOME": f"{home}/.pig", "PIG_CODING_AGENT_DIR": agent_dir, "PI_CODING_AGENT_DIR": agent_dir,
           "PIG_OFFLINE": "1", "PI_OFFLINE": "1", "PIG_TELEMETRY": "0", "PIG_SKIP_VERSION_CHECK": "1", "TERM": "dumb", "TMPDIR": f"{home}/tmp", **bench.arm_env(arm)}
    os.makedirs(env["TMPDIR"], exist_ok=True)
    argv = [bench.CFG["pig"], "--mode", "rpc", "--no-session", "--offline", "-ne", "--provider", "fake", "--model", "fake-model"]
    for e in exts: argv += ["-e", e]
    rpc = bench.Rpc(argv, env, work)
    obs, n0 = {}, len(model.requests)
    try:
        rid = rpc.send({"type": "get_commands"})
        ev = rpc.until(lambda e: e.get("type") == "response" and e.get("id") == rid)
        cmds = (ev.get("data") or {}).get("commands", [])
        obs["commands"] = sorted(({k: c.get(k) for k in ("name", "description", "source")} for c in cmds if str(c.get("name", "")).startswith("bench-")), key=lambda c: c["name"])
        if len(obs["commands"]) != N: raise RuntimeError(f"{len(obs['commands'])} of {N} bench commands listed")
        time.sleep(0.3)
        snap = bench.snapshot(rpc.p.pid)
        if bench.TIER_EXE[arm] not in snap["ext_exes"]: raise RuntimeError(f"arm {arm} ran {snap['ext_exes']}, not {bench.TIER_EXE[arm]}")
        obs_exe = snap["ext_exes"]
        results = []
        def call(nn):
            ends = []
            rpc.send({"type": "prompt", "message": f"call {nn}"})
            def pred(e):
                if e.get("type") == "tool_execution_end": ends.append(e)
                return e.get("type") == "agent_end"
            rpc.until(pred, 120)
            if len(ends) != 1: raise RuntimeError(f"call {nn}: {len(ends)} tool executions")
            r = ends[0]
            return {"toolName": r.get("toolName"), "isError": r.get("isError"), "result": r.get("result")}
        for i in range(1, N + 1): results.append(call(f"{i:02d}"))
        for i in range(1, N + 1):  # each command runs and answers without an error response
            rid = rpc.send({"type": "prompt", "message": f"/bench-{i:02d}"})
            ev = rpc.until(lambda e: e.get("type") == "response" and e.get("id") == rid)
            results.append({"command": f"bench-{i:02d}", "success": ev.get("success"), "error": ev.get("error")})
        for i in range(1, N + 1): results.append(call(f"{i:02d}"))  # cumulative event counts: every handler of every extension has run in between
        obs["calls"] = results
        first = model.requests[n0]
        obs["first_request_tools"] = sorted((t.get("function") or {} for t in first.get("tools") or [] if str((t.get("function") or {}).get("name", "")).startswith("bench_tool_")), key=lambda f: f["name"])
        if len(obs["first_request_tools"]) != N: raise RuntimeError(f"first request declared {len(obs['first_request_tools'])} of {N} bench tools")
        obs["tool_messages_to_the_model"] = [m["content"] for r in model.requests[n0:] for m in r["messages"][-1:] if m["role"] == "tool"]
    finally:
        rpc.close()
    return obs, obs_exe


def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--out", required=True); ap.add_argument("--arms", default="node,node2,bun,aot")
    ap.add_argument("--pig", required=True); ap.add_argument("--slot", required=True); ap.add_argument("--exts", default=DEFAULT_EXTS); ap.add_argument("--build-cmd")
    a = ap.parse_args(); os.makedirs(a.out, exist_ok=True)
    bench.CFG.update(pig=os.path.abspath(a.pig), slot=os.path.abspath(a.slot), exts=os.path.abspath(a.exts))
    exts = sorted(glob.glob(os.path.join(os.path.abspath(a.exts), "bench-ext-*.ts")))
    if len(exts) != N: raise SystemExit(f"{a.exts}: expected {N} extension fixtures, found {len(exts)}")
    arms = a.arms.split(",")
    base = tempfile.mkdtemp(prefix="aot-equiv-", dir=os.environ.get("TMPDIR", "/tmp")); work = f"{base}/work"; os.makedirs(work)
    port = (lambda s: (s.bind(("127.0.0.1", 0)), s.getsockname()[1], s.close())[1])(socket.socket())
    model = Model(port)
    homes = {}
    for arm in arms:
        h = f"{base}/{arm}"; ad = f"{h}/agent"; os.makedirs(ad, exist_ok=True)
        json.dump({"providers": {"fake": {"baseUrl": f"http://127.0.0.1:{port}/v1", "api": "openai-completions", "apiKey": "fake",
                   "models": [{"id": "fake-model", "name": "fake", "contextWindow": 200000, "maxTokens": 8192, "input": ["text"], "reasoning": False,
                               "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}}, open(f"{ad}/models.json", "w"))
        json.dump({"lastChangelogVersion": "9999.0.0"}, open(f"{ad}/settings.json", "w")); open(f"{ad}/auth.json", "w").write("{}")
        homes[arm] = (ad, h)
    report = {"extensions": [os.path.basename(e) for e in exts], "extension_sha256": {os.path.basename(e): hashlib.sha256(open(e, "rb").read()).hexdigest()[:16] for e in exts}, "arms": {}}
    try:
        if "aot" in arms and a.build_cmd:
            subprocess.run(a.build_cmd, shell=True, check=True)
            try: session("aot", *homes["aot"], work, exts, model)
            except RuntimeError as e: print("aot before its build:", e, flush=True)
            subprocess.run(a.build_cmd, shell=True, check=True)
        for arm in arms:
            try:
                obs, exe = session(arm, *homes[arm], work, exts, model)
                report["arms"][arm] = {"extension_host": exe, "sections": {k: hashlib.sha256(canon(v).encode()).hexdigest()[:16] for k, v in obs.items()}, "observed": obs}
                print(arm, exe, {k: v for k, v in report["arms"][arm]["sections"].items()}, flush=True)
            except Exception as e:
                report["arms"][arm] = {"error": str(e)[:2000]}; print(arm, "FAIL", e, flush=True)
    finally:
        model.server.shutdown(); shutil.rmtree(base, ignore_errors=True)
    ok = [x for x in arms if "observed" in report["arms"][x]]
    ref = ok[0] if ok else None
    report["reference_arm"] = ref
    report["equivalent"] = {x: (report["arms"][x]["sections"] == report["arms"][ref]["sections"]) for x in ok}
    json.dump(report, open(f"{a.out}/equivalence.json", "w"), indent=1, sort_keys=True)
    sections = sorted(report["arms"][ref]["sections"]) if ref else []
    lines = ["| section | " + " | ".join(arms) + " |", "|---|" + "---|" * len(arms)]
    for s in sections:
        lines.append(f"| {s} | " + " | ".join(report["arms"][x].get("sections", {}).get(s, "FAIL") for x in arms) + " |")
    lines.append("| extension host | " + " | ".join(",".join(report["arms"][x].get("extension_host", ["FAIL"])) for x in arms) + " |")
    lines.append("| equals " + str(ref) + " | " + " | ".join(str(report["equivalent"].get(x, False)) for x in arms) + " |")
    open(f"{a.out}/equivalence.md", "w").write("\n".join(lines) + "\n"); print("\n".join(lines))
    sys.exit(0 if ref and all(report["equivalent"].values()) and len(ok) == len(arms) else 1)


if __name__ == "__main__":
    main()

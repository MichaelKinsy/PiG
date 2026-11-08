#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Equivalence of PiG's TypeScript extension runtimes (Node, stock Bun, the compiled aot host) on the extension packages the Piglet used to refuse, because they
read files next to their own location: @juicesharp/rpiv-todo and pi-mcp-adapter (pinned in rewrite-exts/package.json), plus rewrite-exts/locating, a fixture whose tool
reads data and skills files beside its modules at run time.

Each arm runs the same scripted session through pig --mode rpc: get_commands, the tools the first model request declares, one prompt per scripted tool call
(the tool result and details are recorded), the commands of the three extensions, and the tool messages the model received. Each section is canonicalised to JSON, the
arm's home directory is replaced by <HOME>, and the sections are hashed; the arms are equivalent when every hash is equal. node2 repeats node as the A/A control.
Each arm is verified to have run the tier it names: the extension host process (`ext_exes` of the process tree) and the `Selected:` line of `pig extensions runtime
status`; the aot arm's image manifest must list the original extension files as its inputs and none of the build's copies. The aot arm also fails when an extension
warned that it could not load a file beside itself (rpiv-todo's locales, read through import.meta.url by rpiv-i18n).

--mutate-arm ARM makes the fixture's tool answer differently on that arm (LOCATING_MUTATE=1): the comparison must then fail, which proves it can.
--hide-locale moves rpiv-todo's locales/de.json away for the run: every arm must then report the warning of the locale loader (which proves the check for that warning can fail, and that the files are read as the extension runs, from where it is installed).
usage: equivalence_pkgs.py --out DIR --pig PIG --slot SLOT --bun BUN [--exts bench/bolt-aot/rewrite-exts] [--arms node,node2,bun,aot] [--build-cmd CMD] [--mutate-arm ARM] [--hide-locale]"""
import argparse, glob, hashlib, json, os, re, shutil, socket, subprocess, sys, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import bench

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.normpath(os.path.join(HERE, "..", ".."))
STATUS_TIER = {"node": "node", "bun": "bun", "aot": "aot (executable"}

# The scripted tool calls: (tool, arguments). Each is one prompt, "call <tool> <json arguments>", which the scripted model answers with that tool call.
SCRIPT = [
    ("todo", {"action": "create", "tasks": [{"subject": "write the rewrite"}, {"subject": "prove it"}]}),
    ("todo", {"action": "update", "id": 1, "status": "in_progress", "activeForm": "writing the rewrite"}),
    ("todo", {"action": "list"}),
    ("locating_read", {}),
    ("locating_read", {"name": "legacy.txt"}),
    ("mcp", {}),
    ("mcp", {"search": "nothing-matches-this"}),
    ("todo", {"action": "list"}),
]
COMMANDS = ["todos", "mcp", "locating"]


class QuietServer(ThreadingHTTPServer):
    def handle_error(self, request, client_address): pass  # a session that is stopped resets its connection


class Model:
    """OpenAI-compatible scripted model: a prompt "call <tool> <json>" answers with that tool call, a tool result answers with text."""
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
                last = body["messages"][-1]
                self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.send_header("Transfer-Encoding", "chunked"); self.end_headers()
                def w(b): self.wfile.write(f"{len(b):x}\r\n".encode() + b + b"\r\n"); self.wfile.flush()
                def chunk(delta, finish=None):
                    return ("data: " + json.dumps({"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "fake-model", "choices": [{"index": 0, "delta": delta, "finish_reason": finish}]}) + "\n\n").encode()
                text = last["content"] if isinstance(last.get("content"), str) else "".join(p.get("text", "") for p in last.get("content") or [])
                if last["role"] == "user" and text.startswith("call "):
                    _, tool, args = text.split(" ", 2)
                    w(chunk({"role": "assistant", "tool_calls": [{"index": 0, "id": "call_" + tool, "type": "function", "function": {"name": tool, "arguments": args}}]}))
                    w(chunk({}, "tool_calls"))
                else:
                    w(chunk({"role": "assistant", "content": "Done."})); w(chunk({}, "stop"))
                w(b"data: [DONE]\n\n"); self.wfile.write(b"0\r\n\r\n"); self.wfile.flush()
        self.server = QuietServer(("127.0.0.1", port), H)
        threading.Thread(target=self.server.serve_forever, daemon=True).start()


def canon(x, home):
    return json.dumps(x, sort_keys=True, separators=(",", ":"), ensure_ascii=False).replace(json.dumps(home)[1:-1], "<HOME>")


def arm_env_for(arm, agent_dir, home, mutate_arm):
    env = {"PATH": os.environ["PATH"], "HOME": home, "PIG_HOME": f"{home}/.pig", "PIG_CODING_AGENT_DIR": agent_dir, "PI_CODING_AGENT_DIR": agent_dir,
           "PIG_OFFLINE": "1", "PI_OFFLINE": "1", "PIG_TELEMETRY": "0", "PIG_SKIP_VERSION_CHECK": "1", "TERM": "dumb", "TMPDIR": f"{home}/tmp",
           **bench.arm_env("bun" if arm == "bun" else "aot" if arm == "aot" else "node")}
    if arm == mutate_arm: env["LOCATING_MUTATE"] = "1"
    os.makedirs(env["TMPDIR"], exist_ok=True)
    return env


def runtime_status(arm, agent_dir, home, mutate_arm):
    out = subprocess.run([bench.CFG["pig"], "extensions", "runtime", "status"], env=arm_env_for(arm, agent_dir, home, mutate_arm), capture_output=True, text=True, timeout=60)
    return out.stdout + out.stderr


def session(arm, agent_dir, home, work, exts, model, mutate_arm, expect_tier=True):
    env = arm_env_for(arm, agent_dir, home, mutate_arm)
    argv = [bench.CFG["pig"], "--mode", "rpc", "--no-session", "--offline", "-ne", "--provider", "fake", "--model", "fake-model"]
    for e in exts: argv += ["-e", e]
    rpc = bench.Rpc(argv, env, work)
    obs, n0, tier = {}, len(model.requests), {}
    try:
        rid = rpc.send({"type": "get_commands"})
        ev = rpc.until(lambda e: e.get("type") == "response" and e.get("id") == rid)
        cmds = (ev.get("data") or {}).get("commands", [])
        obs["commands"] = sorted(({k: c.get(k) for k in ("name", "description", "source")} for c in cmds if c.get("name") in COMMANDS), key=lambda c: c["name"])
        if len(obs["commands"]) != len(COMMANDS): raise RuntimeError(f"commands listed: {[c['name'] for c in cmds]}")
        time.sleep(0.5)
        snap = bench.snapshot(rpc.p.pid)
        tier["ext_exes"] = snap["ext_exes"]
        want_exe = bench.TIER_EXE["node" if arm == "node2" else arm]
        if expect_tier and want_exe not in snap["ext_exes"]: raise RuntimeError(f"arm {arm} ran {snap['ext_exes']}, not {want_exe}")
        results = []
        for tool, args in SCRIPT:
            ends = []
            rpc.send({"type": "prompt", "message": f"call {tool} {json.dumps(args)}"})
            def pred(e):
                if e.get("type") == "tool_execution_end": ends.append(e)
                return e.get("type") == "agent_end"
            rpc.until(pred, 120)
            if len(ends) != 1: raise RuntimeError(f"call {tool}: {len(ends)} tool executions")
            r = ends[0]
            results.append({"toolName": r.get("toolName"), "isError": r.get("isError"), "result": r.get("result")})
        for name in COMMANDS:  # each command runs and answers without an error response
            rid = rpc.send({"type": "prompt", "message": f"/{name}"})
            ev = rpc.until(lambda e: e.get("type") == "response" and e.get("id") == rid)
            results.append({"command": name, "success": ev.get("success"), "error": ev.get("error")})
        obs["calls"] = results
        first = model.requests[n0]
        obs["first_request_tools"] = sorted((t.get("function") or {} for t in first.get("tools") or []), key=lambda f: f["name"])
        wanted = {"todo", "mcp", "locating_read"}
        if not wanted <= {t["name"] for t in obs["first_request_tools"]}: raise RuntimeError(f"first request declared {[t['name'] for t in obs['first_request_tools']]}")
        obs["tool_messages_to_the_model"] = [m["content"] for r in model.requests[n0:] for m in r["messages"][-1:] if m["role"] == "tool"]
    finally:
        rpc.close()
        try: tier["stderr"] = rpc.p.stderr.read().decode(errors="replace")
        except Exception: tier["stderr"] = ""
    return obs, tier


def image_report(slot, exts_dir):
    """The aot image manifest: its inputs must be the original extension files and none of the build's copies."""
    path = os.path.join(slot, "aot", "runtime.json")
    m = json.load(open(path))
    sets = m.get("sets") or {}
    if len(sets) != 1: raise RuntimeError(f"{len(sets)} images in the aot manifest, want 1: {sorted(sets)}")
    (key, image), = sets.items()
    inputs = [i["path"] for i in image.get("inputs") or []]
    for want in ("locating/index.ts", "locating/legacy.cjs", "node_modules/@juicesharp/rpiv-todo/index.ts", "node_modules/pi-mcp-adapter/index.ts"):
        if not any(p == os.path.join(exts_dir, want) for p in inputs): raise RuntimeError(f"the image's inputs do not list {want}")
    bad = [p for p in inputs if not p.startswith(exts_dir + os.sep)]
    if bad: raise RuntimeError(f"inputs outside the extension directory (copies?): {bad[:3]}")
    return {"key": key, "inputs": len(inputs), "executable": image.get("executable")}


def verify_bun(path):
    lock = open(os.path.join(ROOT, "coding", "extension", "host", "tsruntime", "lock.go")).read()
    want = re.search(r'Version: "1\.4\.2", Platform: "linux-x64", Variant: "bun-linux-x64", SHA256: "([0-9a-f]{64})"', lock).group(1)
    got = hashlib.sha256(open(path, "rb").read()).hexdigest()
    if got != want: raise SystemExit(f"{path} has digest {got}, the runtime lock pins {want} for Bun 1.4.2 linux-x64")
    return got


def main():
    ap = argparse.ArgumentParser(); ap.add_argument("--out", required=True); ap.add_argument("--arms", default="node,node2,bun,aot")
    ap.add_argument("--pig", required=True); ap.add_argument("--slot", required=True); ap.add_argument("--bun", required=True)
    ap.add_argument("--exts", default=os.path.join(HERE, "rewrite-exts")); ap.add_argument("--build-cmd"); ap.add_argument("--mutate-arm"); ap.add_argument("--hide-locale", action="store_true")
    a = ap.parse_args(); os.makedirs(a.out, exist_ok=True)
    exts_dir = os.path.abspath(a.exts); slot = os.path.abspath(a.slot)
    bench.CFG.update(pig=os.path.abspath(a.pig), slot=slot, exts=exts_dir)
    digest = verify_bun(a.bun)
    os.makedirs(f"{slot}/bun", exist_ok=True); shutil.copy(a.bun, f"{slot}/bun/bun"); os.chmod(f"{slot}/bun/bun", 0o755)
    json.dump({"id": "bun", "kind": "bun", "version": "1.4.2", "platform": "linux-x64", "provider": "bolt-aot-equivalence", "image": {"executable": "bun", "sha256": digest}}, open(f"{slot}/bun/runtime.json", "w"))
    exts = [f"{exts_dir}/node_modules/@juicesharp/rpiv-todo", f"{exts_dir}/node_modules/pi-mcp-adapter", f"{exts_dir}/locating/index.ts"]
    for e in exts:
        if not os.path.exists(e): raise SystemExit(f"{e} is missing: run `npm ci --ignore-scripts` in {exts_dir}")
    arms = a.arms.split(",")
    build_cmd = a.build_cmd or f"cd {ROOT}/piglets/pig-bolt && GOWORK=off PIG_AOT_BUILD_SLOT={slot} go test ./extensions/boltruntime -run '^TestBuildSlotRequests$' -count=1"
    base = tempfile.mkdtemp(prefix="aot-equiv-pkgs-", dir=os.environ.get("TMPDIR", "/tmp")); work = f"{base}/work"; os.makedirs(work)
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
    pins = {p: json.load(open(f"{exts_dir}/node_modules/{p}/package.json"))["version"] for p in ("@juicesharp/rpiv-todo", "@juicesharp/rpiv-i18n", "pi-mcp-adapter")}
    report = {"bun_sha256": digest, "packages": pins, "mutate_arm": a.mutate_arm, "arms": {}}
    locale = f"{exts_dir}/node_modules/@juicesharp/rpiv-todo/locales/de.json"
    if a.hide_locale: os.rename(locale, locale + ".hidden")  # the files an extension reads beside itself are read as it runs: every arm must notice this one is gone
    try:
        if "aot" in arms:
            subprocess.run(build_cmd, shell=True, check=True)  # installs the manifest and the compiler
            try: session("aot", *homes["aot"], work, exts, model, a.mutate_arm)
            except RuntimeError as e: print("aot before its build:", e, flush=True)  # records the build request
            subprocess.run(build_cmd, shell=True, check=True)
        for arm in arms:
            try:
                status = runtime_status(arm, *homes[arm], a.mutate_arm)
                obs, tier = session(arm, *homes[arm], work, exts, model, a.mutate_arm)
                selected = next((l for l in status.splitlines() if l.startswith("Selected:")), "")
                # node and bun are selected for any set; the aot runtime holds an image per extension set, so its status names the images it holds (the empty set has none) and the
                # extension host process of the session (ext_exes above) is what shows the compiled host ran.
                if arm == "aot":
                    held = next((l for l in status.splitlines() if l.strip().startswith("aot  executable")), "")
                    if "Requested: aot" not in status or "1 extension sets" not in held: raise RuntimeError(f"`pig extensions runtime status` for arm aot: {status!r}")
                    selected = held.strip()
                elif STATUS_TIER["node" if arm == "node2" else arm] not in selected: raise RuntimeError(f"`pig extensions runtime status` for arm {arm}: {selected!r}")
                warnings = [l for l in tier["stderr"].splitlines() if "failed to load" in l or "falling back" in l]
                if warnings: raise RuntimeError(f"extensions could not read their files: {warnings[:3]}")
                info = {"extension_host": tier["ext_exes"], "selected": selected, "sections": {k: hashlib.sha256(canon(v, homes[arm][1]).encode()).hexdigest()[:16] for k, v in obs.items()}, "observed": json.loads(canon(obs, homes[arm][1]))}
                if arm == "aot": info["image"] = image_report(slot, exts_dir)
                report["arms"][arm] = info
                print(arm, tier["ext_exes"], selected, info["sections"], info.get("image", ""), flush=True)
            except Exception as e:
                report["arms"][arm] = {"error": str(e)[:2000]}; print(arm, "FAIL", e, flush=True)
    finally:
        model.server.shutdown(); shutil.rmtree(base, ignore_errors=True)
        if a.hide_locale: os.rename(locale + ".hidden", locale)
    ok = [x for x in arms if "observed" in report["arms"][x]]
    ref = ok[0] if ok else None
    report["reference_arm"] = ref
    report["equivalent"] = {x: (report["arms"][x]["sections"] == report["arms"][ref]["sections"]) for x in ok}
    json.dump(report, open(f"{a.out}/equivalence_pkgs.json", "w"), indent=1, sort_keys=True)
    lines = ["| section | " + " | ".join(arms) + " |", "|---|" + "---|" * len(arms)]
    for s in (sorted(report["arms"][ref]["sections"]) if ref else []):
        lines.append(f"| {s} | " + " | ".join(report["arms"][x].get("sections", {}).get(s, "FAIL") for x in arms) + " |")
    lines.append("| extension host | " + " | ".join(",".join(report["arms"][x].get("extension_host", ["FAIL"])) for x in arms) + " |")
    lines.append(f"| equals {ref} | " + " | ".join(str(report["equivalent"].get(x, False)) for x in arms) + " |")
    open(f"{a.out}/equivalence_pkgs.md", "w").write("\n".join(lines) + "\n"); print("\n".join(lines))
    sys.exit(0 if ref and all(report["equivalent"].values()) and len(ok) == len(arms) else 1)


if __name__ == "__main__":
    main()

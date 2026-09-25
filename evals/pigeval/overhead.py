# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Measure harness overhead: startup, one prompt round trip, and a resumed large Session."""
import json
import os
import platform
import shutil
import statistics
import tempfile
import time
import uuid

from . import proc, registry
from .mockllm import MODEL, REPLY, MockServer

SCENARIOS = [
    ("startup", "Startup (--version)"),
    ("round-trip", "One prompt, streamed reply"),
    ("resumed-session", "One prompt after resuming a large Session"),
]
CREDENTIAL_PREFIXES = ("OPENAI_", "ANTHROPIC_", "GEMINI_", "GOOGLE_", "AZURE_", "AWS_", "PI_", "PIG_", "CODEX_", "CLAUDE_", "OPENCODE_", "COPILOT_", "GH_", "GITHUB_")


def pi_models(base_url):
    return {"providers": {"bench": {"baseUrl": f"{base_url}/v1", "api": "openai-completions", "apiKey": "bench-key",
                                    "models": [{"id": MODEL, "name": "Bench 1", "contextWindow": 128000, "maxTokens": 4096}]}}}


def opencode_config(base_url):
    return {"$schema": "https://opencode.ai/config.json", "autoupdate": False, "share": "disabled", "model": f"bench/{MODEL}",
            "provider": {"bench": {"npm": "@ai-sdk/openai-compatible", "name": "Bench", "options": {"baseURL": f"{base_url}/v1", "apiKey": "bench-key"},
                                   "models": {MODEL: {"name": "Bench 1"}}}}}


def prepare(h, work, base_url, keep_credentials=False):
    """Create an isolated HOME and configuration for h. Return (env, cwd, placeholder values)."""
    home = os.path.join(work, h.id, "home")
    cwd = os.path.join(work, h.id, "project")
    os.makedirs(home, exist_ok=True)
    os.makedirs(cwd, exist_ok=True)
    env = dict(os.environ) if keep_credentials else {k: v for k, v in os.environ.items() if not k.startswith(CREDENTIAL_PREFIXES) and not k.endswith(("_API_KEY", "_TOKEN"))}
    env.update({"HOME": home, "XDG_CONFIG_HOME": os.path.join(home, ".config"), "XDG_DATA_HOME": os.path.join(home, ".local", "share"),
                "XDG_CACHE_HOME": os.path.join(home, ".cache"), "XDG_STATE_HOME": os.path.join(home, ".local", "state"), "NO_COLOR": "1", "TERM": "dumb"})
    # A harness installed by pigeval finds its own helpers (for example bun for oh-my-pi) first.
    local_bin = os.path.join(registry.dev_home(), "harnesses", h.id, "node_modules", ".bin")
    if os.path.isdir(local_bin):
        env["PATH"] = local_bin + os.pathsep + env.get("PATH", "")
    values = {"base_url": base_url, "home": home}
    env.update(dict(zip(h.env, registry.substitute(values, h.env.values()))))
    if h.config == "pi-models":
        agent = os.path.join(home, h.config_dir)
        os.makedirs(agent, exist_ok=True)
        with open(os.path.join(agent, "models.json"), "w") as f:
            json.dump(pi_models(base_url), f)
    elif h.config == "opencode":
        path = os.path.join(home, ".config", "opencode", "opencode.json")
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            json.dump(opencode_config(base_url), f)
        env["OPENCODE_CONFIG"] = path
    return env, cwd, values


def write_session(path, exchanges):
    """Write a Pi-format Session with the given number of user/assistant exchanges."""
    parent = None
    with open(path, "w") as f:
        f.write(json.dumps({"type": "session", "version": 3, "id": str(uuid.uuid4()), "timestamp": "2026-09-23T00:00:00Z", "cwd": os.path.dirname(path)}) + "\n")
        for i in range(exchanges):
            uid, aid = f"u{i:07d}", f"a{i:07d}"
            f.write(json.dumps({"type": "message", "id": uid, "parentId": parent, "timestamp": "2026-09-23T00:00:01Z",
                                "message": {"role": "user", "content": [{"type": "text", "text": f"Question {i}: explain step {i}."}], "timestamp": 1790000000000 + 2 * i}}) + "\n")
            text = f"Step {i} keeps every entry in order.\n\n```go\nfunc step{i}() error {{ return nil }}\n```\n"
            f.write(json.dumps({"type": "message", "id": aid, "parentId": uid, "timestamp": "2026-09-23T00:00:02Z",
                                "message": {"role": "assistant", "content": [{"type": "text", "text": text}], "api": "openai-completions", "provider": "bench", "model": MODEL,
                                            "stopReason": "stop", "timestamp": 1790000000001 + 2 * i,
                                            "usage": {"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2,
                                                      "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}}}) + "\n")
            parent = aid


def summarize(samples):
    walls = sorted(s["wall"] for s in samples)
    p90 = walls[min(len(walls) - 1, max(0, -(-len(walls) * 9 // 10) - 1))]
    return {"runs": len(samples), "wall_ms_median": round(statistics.median(walls) * 1000, 1), "wall_ms_p90": round(p90 * 1000, 1),
            "wall_ms_min": round(walls[0] * 1000, 1), "cpu_ms_median": round(statistics.median(s["cpu"] for s in samples) * 1000, 1),
            "rss_mib_median": round(statistics.median(s["rss_mib"] for s in samples), 1)}


def identify(h, binary, env, cwd, version_output):
    """Record the harness's own version and what its version argv printed.

    Most harnesses print their own version for --version. A harness that prints
    another tool's version there names a second argv, own_version, whose first
    line ends with its own version.
    """
    name = os.path.basename(binary)
    entry = {"version": version_output, "version_output": version_output, "version_command": " ".join([name, *h.version_args])}
    if not h.own_version_args:
        return entry
    own = proc.run([binary, *h.own_version_args], env, cwd, 60)
    lines = own["stdout"].strip().splitlines()
    words = lines[0].split() if lines else []
    if own["code"] != 0 or own["timed_out"] or not words:
        entry["version"] = "unknown"
    else:
        entry["version"] = words[-1]
    entry["own_version_command"] = " ".join([name, *h.own_version_args])
    return entry


def host():
    return {"platform": platform.platform(), "machine": platform.machine(), "cpus": os.cpu_count(), "python": platform.python_version()}


def run(args):
    harnesses = registry.select(registry.load(args.registry), args.harnesses)
    report = {"kind": "overhead", "generated": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "host": host(), "runs": args.runs,
              "warmup": args.warmup, "prompt": args.prompt, "session_exchanges": args.session_exchanges, "harnesses": {}, "results": []}
    work = tempfile.mkdtemp(prefix="pigeval-")
    session = os.path.join(work, "large-session.jsonl")
    write_session(session, args.session_exchanges)
    pristine = session + ".pristine"
    shutil.copy(session, pristine)
    with MockServer() as server:
        ready = {}
        for h in harnesses:
            entry = report["harnesses"].setdefault(h.id, {"name": h.name, "npm": h.npm})
            binary = registry.resolve(h, args.pig)
            if not h.mock:
                entry["skipped"] = "cannot use a custom model endpoint; measured by pigeval live only"
            elif binary is None:
                entry["skipped"] = f"not installed; run: make setup SETUP_ARGS=--harnesses={h.id}"
            else:
                env, cwd, values = prepare(h, work, server.base_url)
                version = proc.run([binary, *h.version_args], env, cwd, 60)
                text = (version["stdout"].strip() or version["stderr"].strip()).splitlines()
                if version["code"] != 0 or version["timed_out"]:
                    entry["skipped"] = f"does not start: {text[-1] if text else 'exit ' + str(version['code'])}"
                else:
                    entry.update(identify(h, binary, env, cwd, text[-1] if text else "unknown"))
                    ready[h.id] = (h, binary, env, cwd, values)
            print(f"pigeval: {h.id:9} {entry.get('version') or entry.get('skipped')}", flush=True)
        for sid, title in SCENARIOS:
            row = {"scenario": sid, "title": title, "by": {}}
            for hid, (h, binary, env, cwd, values) in ready.items():
                if sid == "startup":
                    argv = h.version_args
                elif sid == "round-trip":
                    argv = h.print_args
                elif h.session_args:
                    argv = h.session_args
                else:
                    continue
                argv = [binary, *registry.substitute(dict(values, prompt=args.prompt, session=session), argv)]
                samples, failures, requests = [], [], []
                for i in range(args.warmup + args.runs):
                    if sid == "resumed-session":
                        shutil.copy(pristine, session)
                    server.recorder.take()
                    sample = proc.run(argv, env, cwd, args.timeout)
                    got = [r for r in server.recorder.take() if r["protocol"] != "other"]
                    if sid != "startup" and (sample["code"] != 0 or not got or REPLY.split()[0] not in sample["stdout"]):
                        failures.append({"code": sample["code"], "timed_out": sample["timed_out"], "requests": len(got), "stderr": sample["stderr"][-600:], "stdout": sample["stdout"][-300:]})
                        if len(failures) >= 2:
                            break
                        continue
                    if i >= args.warmup:
                        samples.append(sample)
                        requests = got
                result = summarize(samples) if samples else {"runs": 0}
                if requests:
                    result.update({"requests": len(requests), "request_bytes": requests[0]["bytes"], "bytes_sent": sum(r["bytes"] for r in requests),
                                   "system_bytes": requests[0]["system_bytes"], "tools": requests[0]["tools"]})
                if failures:
                    result["failures"] = failures
                row["by"][hid] = result
                print(f"pigeval: {sid:16} {hid:9} {result.get('wall_ms_median', 'FAILED')} ms  {result.get('request_bytes', '')}", flush=True)
            report["results"].append(row)
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
    with open(args.out, "w") as f:
        json.dump(report, f, indent=2)
    print(f"pigeval: wrote {args.out}")
    return 1 if any(r.get("failures") and not r.get("runs") for row in report["results"] for r in row["by"].values()) else 0

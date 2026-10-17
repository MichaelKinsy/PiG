#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Token usage and cost on the scripted mock (ADDENDUM 4: no live keys, no paid runs).

  tokcost.py run  --root ROOT --arms a,b --out DIR      drive each arm through the same scripted sessions; every request body goes to DIR/<arm>-<session>/
  tokcost.py cost --out DIR [--catalog ai/models_generated.go] [--models id,id]   count tokens (tok.mjs) and price them

Sessions (same scripted replies for every arm, fake_model_ctx.py): base (3 prompts, window 200k), compact (6 prompts, window 24k, so each arm
compacts), retry (2 prompts, the 3rd request answered 429 once). Measured: what each harness SENDS per request (system prompt, tool schemas, conversation
tokens) with the o200k_base and cl100k_base tokenizers, the prefix shared with the previous request, compaction and retry requests, cache-marker fields.
Cost = tokens x the per-million prices of the Pi/PiG model catalog (input, output, cache read, cache write) for the chosen models; caching is
modelled as implicit exact-prefix reuse (OpenAI/Gemini style); Anthropic-style cache breakpoints are not measured (the arms talk openai-completions to the mock).
Claude and Gemini tokenizers are not available offline: their counts here use the o200k count as an ESTIMATE and are labelled so.
"""
import argparse
import json
import os
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import cleanenv  # noqa: E402
import ctx_scale as cs  # noqa: E402
import fake_server  # noqa: E402

SESSIONS = {"base": dict(prompts=3, window=200_000, fail=0), "compact": dict(prompts=6, window=24_000, fail=0), "retry": dict(prompts=2, window=200_000, fail=3)}
OUT_TOKENS_PER_REQUEST = None


def run_copilot(h, argv, sess, spec, d, port):
    """Copilot CLI 1.0.95 BYOK to the mock: one process per prompt (-p), later prompts continue the first's session. Offline, no token, own cache and home."""
    base = Path(tempfile.mkdtemp(prefix="copilot-"))
    cwd = base / "work"
    cwd.mkdir()
    shutil.copytree(h.FIXTURES, cwd / "fixture")
    env = {k: v for k, v in os.environ.items() if not k.startswith(("PIG_", "PI_", "NODE_", "BUN_", "GH_", "GITHUB_", "COPILOT_"))}
    env.update(COPILOT_HOME=str(base / "home"), XDG_CACHE_HOME=str(base / "cache"), HOME=str(base), COPILOT_AUTO_UPDATE="false", COPILOT_OFFLINE="true",
               COPILOT_PROVIDER_BASE_URL=f"http://127.0.0.1:{port}/v1", COPILOT_PROVIDER_TYPE="openai", COPILOT_PROVIDER_API_KEY="fake", COPILOT_MODEL="fake-model",
               COPILOT_PROVIDER_MAX_PROMPT_TOKENS=str(spec["window"]))
    done = 0
    try:
        for i in range(1, spec["prompts"] + 1):
            cmd = [*argv, "-p", h.PROMPT, "--allow-all-tools", "--no-custom-instructions", *(["--continue"] if i > 1 else [])]
            p = subprocess.run(cmd, cwd=cwd, env=env, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=300)
            if h.DONE not in p.stdout:
                return {"error": f"prompt {i}: no final answer (exit {p.returncode})", "completed": done}
            done = i
        return {"completed": done}
    finally:
        shutil.rmtree(base, ignore_errors=True)


def run_opencode(h, argv, sess, spec, d, port):
    """OpenCode through its launcher in `run` mode: one process per prompt, later prompts --continue the first's session (the TUI cannot be driven through the harness's byte stream)."""
    base = Path(tempfile.mkdtemp(prefix="opencode-"))
    home = cs.pi_home_window(port, spec["window"])
    cwd = base / "work"
    cwd.mkdir()
    shutil.copytree(h.FIXTURES, cwd / "fixture")
    env = h.pi_env(home, {"BENCH_OC_DIR": str(base / "oc")})
    done = 0
    try:
        for i in range(1, spec["prompts"] + 1):
            p = subprocess.run([*argv, "-p", *(["--continue"] if i > 1 else []), h.PROMPT], cwd=cwd, env=env, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=300)
            if h.DONE not in p.stdout:
                return {"error": f"prompt {i}: no final answer (exit {p.returncode})", "completed": done}
            done = i
        return {"completed": done}
    finally:
        shutil.rmtree(base, ignore_errors=True)
        shutil.rmtree(home, ignore_errors=True)


def run_session(h, name, argv, sess, spec, out_dir, cpus):
    d = out_dir / f"{name}-{sess}"
    shutil.rmtree(d, ignore_errors=True)
    d.mkdir(parents=True)
    args = ["--dump", d]
    if spec["fail"]:
        args += ["--fail-nth", spec["fail"]]
    mock, port = fake_server.start(HERE / "fake_model_ctx.py", *args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    tty = None
    home = cwd = None
    try:
        end = time.time() + 30
        while not fake_server.served(port):
            if mock.poll() is not None or time.time() > end:
                raise RuntimeError("mock did not start")
            time.sleep(0.05)
        if name.startswith("copilot"):
            return run_copilot(h, argv, sess, spec, d, port)
        if name.startswith("opencode"):
            return run_opencode(h, argv, sess, spec, d, port)
        home = cs.pi_home_window(port, spec["window"])
        cwd = Path(tempfile.mkdtemp(prefix="pibolt-work-"))
        shutil.copytree(h.FIXTURES, cwd / "fixture")
        tty = h.Tty(h.pinned([*argv, "--no-session", *h.MODEL_ARGS], cpus), h.pi_env(home), cwd)
        deadline = time.perf_counter() + 300
        if not tty.wait_for("fake-model", 0, deadline):
            return {"error": "never became interactive"}
        tty.settle(0.3, deadline)
        completed = 0
        for i in range(1, spec["prompts"] + 1):
            start = len(tty.buf)
            tty.send(h.PROMPT.encode() + b"\r")
            if not tty.wait_for(h.done(i), start, time.perf_counter() + 180):
                return {"error": f"no answer to prompt {i}", "completed": completed}
            completed = i
            tty.settle(0.5, deadline)
        return {"completed": completed}
    finally:
        if tty:
            try:
                tty.quit(5)
            except Exception:  # noqa: BLE001
                pass
        mock.terminate()
        mock.wait()
        for p in (home, cwd):
            if p:
                shutil.rmtree(p, ignore_errors=True)


def analyse(out_dir: Path):
    env = {**os.environ, "TOK_RESOLVE_FROM": str(HERE.parents[1] / "x")}
    tokdir = os.environ.get("TOK_NODE_MODULES_DIR")
    if tokdir:
        env["TOK_RESOLVE_FROM"] = str(Path(tokdir) / "x.js")
    p = subprocess.run(["node", str(HERE / "tok.mjs"), str(out_dir)], capture_output=True, text=True, env=env, timeout=600)
    if p.returncode:
        sys.exit(p.stderr)
    return json.loads(p.stdout)


def session_cost(price, billed_input, cacheable_prefix, billed_requests, out_tok):
    """The two bounds of ADDENDUM 5, USD: (no-cache: every billed input token at the input price, full-cache: the reusable prefix at the cache-read price).
    Cache writes are not charged in either bound, so the full-cache bound is a floor for providers that bill them (Anthropic)."""
    out = billed_requests * out_tok * price["out"]
    cached = min(cacheable_prefix, billed_input)
    no_cache = (billed_input * price["in"] + out) / 1e6
    full_cache = ((billed_input - cached) * price["in"] + cached * (price["cache_read"] or price["in"]) + out) / 1e6
    return no_cache, full_cache


def catalog_prices(path: Path, ids):
    s = path.read_text()
    ents = re.findall(r"\n\t\{\n(.*?)\n\t\},?\n", s, flags=re.S)
    up = re.search(r"Upstream: (.*)", s).group(1)
    out = {}
    for e in ents:
        g = lambda k: (re.search(r"\t\t" + k + r":\s+([^,\n]+),", e) or [None, None])[1]
        mid = (g("ID") or "").strip('"')
        prov = (g("Provider") or "").strip('"')
        if mid in ids and mid not in out and prov in ("anthropic", "openai", "google"):
            out[mid] = {"provider": prov, "in": float(g("InputCostPerMTokens")), "out": float(g("OutputCostPerMTokens")), "cache_read": float(g("CacheReadCost")),
                        "cache_write": float(g("CacheWriteCost"))}
    return out, up


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("cmd", choices=["run", "cost"])
    ap.add_argument("--root", type=Path)
    ap.add_argument("--arms")
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--cpus")
    ap.add_argument("--catalog", type=Path)
    ap.add_argument("--models", default="claude-sonnet-4-6,claude-opus-5,gpt-5.6-sol,gemini-3.7-flash")
    ap.add_argument("--sessions", default="base,compact,retry")
    a = ap.parse_args()
    a.out.mkdir(parents=True, exist_ok=True)
    if a.cmd == "run":
        dropped = cleanenv.apply()  # the arms send what they send on a user's machine: no lane PIG_*/PI_*/Go/agent variables
        print(f"clean env: dropped {len(dropped)} variables ({', '.join(dropped)})", file=sys.stderr, flush=True)
        root = a.root.resolve()
        h = cs.load_harness(root)
        specs = dict(l.split("=", 1) for l in subprocess.run([sys.executable, str(HERE / "arms.py"), "specs", "--root", str(root)], capture_output=True, text=True, check=True).stdout.splitlines())
        res = {}
        for name in a.arms.split(","):
            for sess in a.sessions.split(","):
                print(f"[{time.strftime('%H:%M:%S')}] tokcost {name} {sess}", flush=True)
                res[f"{name}-{sess}"] = run_session(h, name, shlex.split(specs[name]), sess, SESSIONS[sess], a.out, a.cpus)
                print("   ", res[f"{name}-{sess}"], flush=True)
        (a.out / "run.json").write_text(json.dumps(res, indent=1))
        return
    stats = analyse(a.out)
    prices, upstream = catalog_prices(a.catalog, set(a.models.split(",")))
    # scripted reply per request: ~120 words of prose plus a tool call or the answer; identical for every arm and request
    out_tok = 190
    md = [f"## Token usage and cost on the mock (PROVISIONAL)\n\nCatalog: {upstream} ({a.catalog}); output tokens are the scripted reply, ~{out_tok} per request, equal for all arms. "
          "Tokenizers: o200k_base / cl100k_base (gpt-tokenizer); Claude and Gemini rows use the o200k count (ESTIMATE). Implicit exact-prefix caching assumed.\n"]
    rows = []
    for sess, reqs in sorted(stats.items()):
        arm, _, kind = sess.rpartition("-")
        ok = [r for r in reqs if not r["file"].endswith("-429.json")]
        failed = [r for r in reqs if r["file"].endswith("-429.json")]
        tot = lambda k, t="o200k": sum(r[k][t] for r in reqs)
        main = max(reqs, key=lambda r: (r["tools"], r["total"]["o200k"])) if reqs else None  # the agent request (OpenCode also sends a tool-less title request)
        titles = sum(1 for r in reqs if r["tools"] == 0)
        rows.append({"arm": arm, "session": kind, "requests": len(reqs), "failed": len(failed), "system": main["system"]["o200k"] if main else 0, "tool_less_requests": titles,
                     "tool_schemas": main["toolSchemas"]["o200k"] if main else 0, "tools": main["tools"] if main else 0,
                     "sent": tot("total"), "sent_cl100k": tot("total", "cl100k"), "shared_prefix": sum(r["sharedPrefix"]["o200k"] for r in ok),
                     "cache_markers": sum(r["cacheMarkers"] for r in reqs), "body_keys": sorted({k for r in reqs for k in r["bodyKeys"]}), "billed_requests": len(ok)})
    md.append("| arm | session | requests (429; tool-less) | tools | system tok | tool-schema tok | input tokens sent o200k (cl100k) | prefix reusable tok | cache-marker fields |")
    md.append("|---|---|---|---|---|---|---|---|---|")
    for r in rows:
        md.append(f"| {r['arm']} | {r['session']} | {r['requests']} ({r['failed']}; {r['tool_less_requests']}) | {r['tools']} | {r['system']} | {r['tool_schemas']} | {r['sent']} ({r['sent_cl100k']}) | {r['shared_prefix']} | {r['cache_markers']} |")
    md.append("\n### Cost per session (USD, catalog prices x tokens; `no-cache / full-cache` bounds; failed 429 requests not billed; cache writes not charged)\n")
    mids = list(prices)
    md.append("| arm | session | " + " | ".join(mids) + " |")
    md.append("|---|---|" + "---|" * len(mids))
    for r in rows:
        cells = []
        for m in mids:
            p = prices[m]
            # billed input = tokens of billed requests; cached part from the shared prefix at cache-read price, rest at input price
            billed = r["sent"] - sum(q["total"]["o200k"] for q in stats[f"{r['arm']}-{r['session']}"] if q["file"].endswith("-429.json"))
            no_cache, full_cache = session_cost(p, billed, r["shared_prefix"], r["billed_requests"], out_tok)
            cells.append(f"${no_cache:.4f} / ${full_cache:.4f}")
        md.append(f"| {r['arm']} | {r['session']} | " + " | ".join(cells) + " |")
    md.append("\nPrices ($/M tokens: in/out/cache-read/cache-write): " + "; ".join(f"{m} {p['in']}/{p['out']}/{p['cache_read']}/{p['cache_write']}" for m, p in prices.items()))
    text = "\n".join(md)
    (a.out / "tokcost.md").write_text(text)
    (a.out / "tokcost.json").write_text(json.dumps({"rows": rows, "prices": prices, "catalog": upstream}, indent=1))
    print(text)


if __name__ == "__main__":
    main()

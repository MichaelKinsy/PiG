# SPDX-License-Identifier: MIT
"""Reliability bench: faults injected by the mock endpoint and by the host, compared with Pi's behaviour.

No model is called. The mock answers a scripted turn but fails chosen requests with HTTP 429 or 500, or drops the connection before a
reply or part-way through a stream; the host kills the harness while a tool call runs, or sends SIGTERM during a stream.

Expectations come from Pi 1.1.0, never from PiG. Every fault has an invariant that must hold for every arm (the process ends within
the time limit; a session file stays parseable) and a signature of what the user observes (exit class, whether the reply arrived,
how many times it printed, and for a kill, the message roles the model receives after resume). An arm passes a fault when the
invariants hold and its signature equals the Pi arm's signature for the same fault. The Pi arm passes when the invariants hold.
CLIs that are not Pi-compatible (Claude Code, Codex, Copilot) are scored on the invariants only; their signatures are listed for
information because their retry and session designs differ by design.

Faults
  http-429-retry        request 0 answers 429 with Retry-After: 0, then the scripted reply
  http-500-retry        request 0 answers 500, then the scripted reply
  http-429-exhausted    every request answers 429
  disconnect-before     the connection drops before any byte of request 0's reply
  disconnect-midstream  request 0's stream ends after 3 events with no terminator: a reset, a FIN on Windows (see mock.Fault)
  kill-mid-tool         SIGKILL of the whole harness tree while a bash tool call runs; then resume the session
  sigterm-midstream     SIGTERM to the harness while a reply streams
"""
import json
import os
import signal
import subprocess
import time

from . import arms as armlib
from . import cgroup, egress, envinfo, procio
from .mock import REPLY, Fault, MockServer, Step, agentic_policy, scripted_policy
from .overhead import Ctx, native

FAULTS = ["http-429-retry", "http-500-retry", "http-429-exhausted", "disconnect-before", "disconnect-midstream", "kill-mid-tool", "sigterm-midstream"]
NATIVE_ONLY = {"kill-mid-tool", "sigterm-midstream"}
PROMPT = "Say hello."


def fault_plan(name):
    if name == "http-429-retry":
        return {0: Fault(status=429, retry_after=0)}
    if name == "http-500-retry":
        return {0: Fault(status=500)}
    if name == "http-429-exhausted":
        return {i: Fault(status=429, retry_after=0) for i in range(30)}
    if name == "disconnect-before":
        return {0: Fault(drop_before=True)}
    if name == "disconnect-midstream":
        return {0: Fault(drop_after=3)}
    return {}


def session_ok(path):
    """A session file is intact when every line parses as JSON and the first line is a session header."""
    try:
        with open(path) as f:
            lines = [ln for ln in f.read().split("\n") if ln]
        records = [json.loads(ln) for ln in lines]
    except (OSError, ValueError):
        return False, 0
    return bool(records) and records[0].get("type") == "session", len(records)


def role_signature(request):
    """What the model receives: the role of every message, with tool results marked as errors or not."""
    out = []
    for m in request.body.get("messages") or []:
        role = m.get("role")
        if role == "tool":
            text = m.get("content") if isinstance(m.get("content"), str) else json.dumps(m.get("content"))
            out.append("tool:error" if any(w in (text or "").lower() for w in ("error", "abort", "no result", "interrupt")) else "tool")
        elif role == "assistant" and m.get("tool_calls"):
            out.append("assistant:toolcall")
        elif role in ("system", "developer"):
            continue
        else:
            out.append(role)
    return out


def run_print_fault(ctx, arm, name):
    h = ctx.harness(arm, "rel-" + name)
    srv = ctx.server
    srv.reset(policy=agentic_policy(h.cwd, tool_turns=0), faults=fault_plan(name))
    meter = cgroup.new_meter()
    try:
        res = procio.Batch(h.print_argv(PROMPT), h.env, h.cwd, meter, ctx.opts.timeout).run()
    finally:
        meter.close()
    replies = res["stdout"].count(REPLY.split()[0])
    sig = {"ended": not res["timed_out"], "exit": "ok" if res["code"] == 0 else "error", "reply_printed": min(replies, 2),
           "retried": len([r for r in srv.requests if r.fault is None]) >= 1 and len(srv.requests) > 1,
           "attempts": len(srv.requests) if native(arm) else None}
    inv = print_invariants(name, res, replies, ctx.opts.timeout)
    return {"signature": sig, "invariant_failures": inv, "requests": len(srv.requests), "stderr_tail": res["stderr"][-200:]}


def print_invariants(name, res, replies, timeout):
    """Invariants of a print-mode fault run that hold for every CLI, whatever its retry design."""
    inv = []
    if res["timed_out"]:
        inv.append(f"hung: no exit within {timeout:.0f}s")
    if "Traceback" in res["stderr"] or "panic:" in res["stderr"] or "goroutine " in res["stderr"]:
        inv.append("crash trace on stderr")
    # Every request fails, so no reply can reach stdout; exit status 0 would report a failed run as a success (Pi exits 1: print-mode.ts:145-147).
    if name == "http-429-exhausted" and res["code"] == 0 and not replies:
        inv.append("reported success with no reply from the model")
    return inv


def run_kill_mid_tool(ctx, arm):
    """Kill the harness tree while a bash tool call runs, then resume the same session file."""
    h = ctx.harness(arm, "rel-kill")
    srv = ctx.server
    started = os.path.join(h.cwd, "STARTED")
    if os.path.exists(started):
        os.unlink(started)
    tool = ("bash", {"command": f"touch {started}; sleep 120"})
    srv.reset(policy=scripted_policy([Step(tool_calls=[tool]), Step(text=REPLY)]))
    session = os.path.join(h.sessions, "kill.jsonl")
    if os.path.exists(session):
        os.unlink(session)
    argv = [arm.binary, "--provider", "bench", "--model", "bench-1", "--session", session, "-p", PROMPT]
    meter = cgroup.new_meter()
    proc = meter.popen(argv, env=h.env, cwd=h.cwd, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    deadline = time.monotonic() + 60
    while not os.path.exists(started) and time.monotonic() < deadline and proc.poll() is None:
        time.sleep(0.02)
    if not os.path.exists(started):
        meter.close()
        return {"signature": {}, "invariant_failures": ["the tool call never started"], "requests": len(srv.requests)}
    time.sleep(0.3)
    meter.kill()
    procio.wait(meter, proc, 10)
    meter.close()
    ok, records = session_ok(session)
    inv = [] if ok else ["session file is not intact after SIGKILL"]
    # Resume: a new process on the same file; the scripted model now answers with text.
    srv.reset(policy=scripted_policy([Step(text=REPLY)]))
    meter = cgroup.new_meter()
    try:
        res = procio.Batch([arm.binary, "--provider", "bench", "--model", "bench-1", "--session", session, "-p", "Continue."], h.env, h.cwd, meter, ctx.opts.timeout).run()
    finally:
        meter.close()
    sig = {"session_intact_after_kill": ok, "resume_exit": "ok" if res["code"] == 0 else "error", "resume_reply": REPLY.split()[0] in res["stdout"],
           "roles_after_resume": role_signature(srv.requests[0]) if srv.requests else None}
    if res["timed_out"]:
        inv.append("resume hung")
    return {"signature": sig, "invariant_failures": inv, "requests": len(srv.requests), "records_after_kill": records}


def run_sigterm(ctx, arm):
    h = ctx.harness(arm, "rel-term")
    srv = ctx.server
    srv.reset(policy=scripted_policy([Step(text=" ".join(["streaming"] * 600), event_delay_ms=10)]))
    session = os.path.join(h.sessions, "term.jsonl")
    if os.path.exists(session):
        os.unlink(session)
    meter = cgroup.new_meter()
    proc = meter.popen([arm.binary, "--provider", "bench", "--model", "bench-1", "--session", session, "-p", PROMPT], env=h.env, cwd=h.cwd, stdin=subprocess.DEVNULL,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    try:
        if not srv.wait_requests(1, 60):
            meter.kill()
            return {"signature": {}, "invariant_failures": ["no request reached the endpoint"], "requests": 0}
        time.sleep(1.0)
        t0 = time.perf_counter()
        proc.send_signal(signal.SIGTERM)
        ended = procio.wait(meter, proc, 15)
        took = time.perf_counter() - t0
    finally:
        meter.kill()
        meter.close()
    ok, records = session_ok(session) if os.path.exists(session) else (True, 0)
    sig = {"exited": ended, "exit_class": "signal" if proc.returncode in (-signal.SIGTERM, 143) else ("ok" if proc.returncode == 0 else "error"), "session_intact": ok}
    inv = [] if ended and ok else (["did not exit on SIGTERM"] if not ended else ["session file is not intact after SIGTERM"])
    return {"signature": sig, "invariant_failures": inv, "requests": len(srv.requests), "seconds_to_exit": round(took, 2)}


def run_fault(ctx, arm, name):
    if name == "kill-mid-tool":
        return run_kill_mid_tool(ctx, arm)
    if name == "sigterm-midstream":
        return run_sigterm(ctx, arm)
    return run_print_fault(ctx, arm, name)


def run(opts):
    arm_list = armlib.select(armlib.load(), opts.arms)
    faults = opts.faults or FAULTS
    result = {"kind": "reliability", "options": {"reps": opts.reps, "faults": faults, "oracle": "pi-1.1.0", "timeout": opts.timeout}, "cells": {}, "egress": dict(egress.describe(), violations=[]),
              "limitations": {}}
    with armlib.Workspace() as ws:
        ws.install_all(arm_list)
        result["env"] = envinfo.collect(arm_list, seed=opts.seed)
        runnable = [a for a in arm_list if a.binary]
        with MockServer() as server:
            ctx = Ctx(ws.work, server, opts)
            for name in faults:
                for a in runnable:
                    if name in NATIVE_ONLY and not native(a):
                        continue
                    runs = []
                    for rep in range(opts.reps):
                        t0 = time.monotonic()
                        cell = run_fault(ctx, a, name)
                        left = sorted(egress.take())
                        if left:
                            result["egress"]["violations"].append({"arm": a.id, "fault": name, "remotes": left})
                            cell["invariant_failures"].append(f"egress to {', '.join(left)}")
                        runs.append(cell)
                        print(f"bench: {name:22} {a.id:10} rep {rep + 1}/{opts.reps} {time.monotonic() - t0:.1f}s {'FAIL ' + '; '.join(cell['invariant_failures']) if cell['invariant_failures'] else ''}", flush=True)
                    result["cells"].setdefault(name, {})[a.id] = runs
    judge(result)
    os.makedirs(opts.out, exist_ok=True)
    with open(os.path.join(opts.out, "raw.json"), "w") as f:
        json.dump(result, f, indent=1)
    return result


def stable_signature(runs):
    """The signature when every repetition agrees, else None (a flaky outcome is itself a failure)."""
    sigs = [json.dumps(r["signature"], sort_keys=True) for r in runs]
    return json.loads(sigs[0]) if len(set(sigs)) == 1 else None


def judge(result):
    """Add verdicts: pass/fail per fault per arm against the invariants and the Pi arm's signature."""
    oracle = result["options"]["oracle"]
    verdicts = {}
    for name, by_arm in result["cells"].items():
        want = stable_signature(by_arm[oracle]) if oracle in by_arm else None
        for arm_id, runs in by_arm.items():
            sig = stable_signature(runs)
            reasons = sorted({f for r in runs for f in r["invariant_failures"]})
            if sig is None:
                reasons.append("outcome differs between repetitions")
            compare = arm_id.startswith(("pig", "pi-"))
            if compare and arm_id != oracle:
                if want is None:
                    reasons.append(f"no stable {oracle} signature to compare with")
                elif sig is not None and sig != want:
                    reasons.append(f"differs from {oracle}: {json.dumps(sig, sort_keys=True)} vs {json.dumps(want, sort_keys=True)}")
            verdicts.setdefault(name, {})[arm_id] = {"pass": not reasons, "reasons": reasons, "signature": sig, "scored_against": oracle if compare and arm_id != oracle else ("invariants" if compare else "invariants only")}
    result["verdicts"] = verdicts
    result["ok"] = all(v["pass"] for by in verdicts.values() for a, v in by.items() if a.startswith(("pig", "pi-"))) and not result["egress"]["violations"]
    return result

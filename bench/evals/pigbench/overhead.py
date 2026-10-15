# SPDX-License-Identifier: MIT
"""Overhead bench: what the harness itself costs against a scripted model that answers instantly.

No model is called and no money is spent. Each arm runs inside its own meter (a cgroup v2 where the host allows it).
Arms are interleaved across repetitions, so drift in host load hits every arm alike.

Scenarios
  version       `--version`: process start and exit
  print         -p, one prompt, one text reply: time to the first request, total wall, teardown, CPU, memory
  print-tools   -p, one prompt, eight tool-call rounds then text: harness-added latency and CPU per turn
  pty           interactive TUI on a PTY: first visible frame, settled, then one prompt
  rpc           --mode rpc: ready time, then turns of one tool call and a reply
  print-ext10   print with ten trivial extensions loaded
  rpc-ext10     rpc with ten trivial extensions loaded
  long-session  rpc, 1,000 or more turns with automatic compaction, on one process
"""
import copy
import glob
import json
import os
import re
import time

from . import arms as armlib
from . import cgroup, checkpoint, egress, envinfo, procio, stats
from .harness import Harness
from .mock import REPLY, MockServer, agentic_policy, now_ns

PROMPT = "Read README.md and tell me in one sentence what it says."
README = ("Benchmark fixture. " * 90).strip() + "\n"
EXT_DIR = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "fixtures", "extensions")
ANSI = re.compile(rb"\x1b\[[0-9;?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][A-Za-z0-9]|\x1b[=>78]")

SCENARIOS = ["version", "print", "print-tools", "pty", "rpc", "print-ext10", "rpc-ext10", "long-session"]


class Skip(Exception):
    pass


class Ctx:
    def __init__(self, work, server, opts):
        self.work, self.server, self.opts = work, server, opts
        self._harnesses = {}

    def harness(self, arm, scenario, **kwargs):
        key = (arm.id, scenario)
        if key in self._harnesses and kwargs and self._harnesses[key].options != kwargs:
            raise RuntimeError(f"harness for {key} already created with different options")
        if key not in self._harnesses:
            h = Harness(arm, self.work, self.server, tag=scenario, **kwargs)
            h.options = kwargs
            with open(os.path.join(h.cwd, "README.md"), "w", newline="\n") as f:  # the read tool returns the same bytes on every host
                f.write(README)
            self._harnesses[key] = h
        return self._harnesses[key]

    def harnesses_for(self, arm):
        return [h for (aid, _), h in self._harnesses.items() if aid == arm.id]

    def supports(self, arm, scenario):
        return Harness(arm, self.work, self.server, tag="probe").supports(scenario)

    def policy(self, arm, tool_turns, scenario):
        return agentic_policy(self._harnesses[(arm.id, scenario)].cwd, tool_turns=tool_turns)

    def main_requests(self, arm):
        """Requests that belong to the scripted turns. Other CLIs send side requests (titles, token counts) without tools."""
        return [r for r in self.server.requests if r.fault is None and (arm.family in ("pig", "pi") or r.tools)]


def arm_extensions(arm, ext):
    """The TypeScript fixtures an extension scenario installs for this arm: none for a build that carries its own extensions (`own_extensions`)."""
    return extensions() if ext and not arm.spec.get("own_extensions") else ()


def extensions():
    paths = sorted(glob.glob(os.path.join(EXT_DIR, "bench-ext-*.ts")))
    if len(paths) != 10:
        raise SystemExit(f"{EXT_DIR}: expected ten extension fixtures, found {len(paths)}")
    return paths


def cpu_hook(meter):
    return lambda request: meter.cpu_ms()


def ms(a, b):
    return (b - a) / 1e6


def request_metrics(reqs, t_spawn):
    """Per-request timing common to print runs: first request, gaps between a reply's end and the next request, CPU between requests."""
    out = {"requests": len(reqs)}
    if reqs:
        out["first_request_ms"] = ms(t_spawn, reqs[0].t_recv)
        if reqs[0].hook is not None:
            out["first_request_cpu_ms"] = reqs[0].hook
    gaps = [ms(a.t_end, b.t_recv) for a, b in zip(reqs, reqs[1:]) if a.t_end]
    cpus = [b.hook - a.hook for a, b in zip(reqs, reqs[1:]) if a.hook is not None and b.hook is not None]
    return out, gaps, cpus, reqs


def check_print(res, srv, expect_requests, arm):
    if res["timed_out"] or res["code"] != 0:
        return f"exit {res['code']}{' (timed out)' if res['timed_out'] else ''}: {res['stderr'][-200:].strip()}"
    if arm.family in ("pig", "pi") and len(srv.requests) != expect_requests:
        return f"expected {expect_requests} requests, saw {len(srv.requests)}"
    if not srv.requests:
        return "no request reached the endpoint"
    if REPLY.split()[0] not in res["stdout"]:
        return "the final reply did not reach stdout"
    return None


def metrics_of(res):
    return {"wall_ms": res["wall_ms"], "cpu_ms": res["cpu_ms"], "peak_mem_mib": res["peak_mem_mib"], "peak_rss_mib": res["max_proc_rss_mib"], "meter": res["meter"]}


def one_version(ctx, arm):
    h = ctx.harness(arm, "version")
    meter = cgroup.new_meter()
    try:
        res = procio.Batch([arm.binary, "--version"], h.env, h.cwd, meter, 60).run()
    finally:
        meter.close()
    if res["code"] != 0:
        return {"failure": f"exit {res['code']}: {res['stderr'][-200:]}"}
    return metrics_of(res)


def one_print(ctx, arm, tools=0, ext=False, scenario="print"):
    if ext and arm.no_extensions:
        raise Skip("the extension runtime is stripped from this build")
    if ext and arm.family not in ("pig", "pi"):
        raise Skip("it does not load Pi extensions")
    h = ctx.harness(arm, scenario, extensions=arm_extensions(arm, ext))
    srv = ctx.server
    srv.reset(policy=ctx.policy(arm, tools, scenario))
    meter = cgroup.new_meter()
    srv.on_request = cpu_hook(meter)
    try:
        res = procio.Batch(h.print_argv(PROMPT), h.env, h.cwd, meter, ctx.opts.timeout).run()
    finally:
        meter.close()
        srv.on_request = None
    bad = check_print(res, srv, tools + 1, arm)
    if bad:
        return {"failure": bad}
    if ext and len(srv.requests[0].tools) < 4 + 10:
        return {"failure": f"extensions did not load: the first request declared {len(srv.requests[0].tools)} tools"}
    sample = metrics_of(res)
    extra, gaps, cpus, reqs = request_metrics(ctx.main_requests(arm), res["t_spawn"])
    sample.update(extra)
    sample["teardown_ms"] = ms(reqs[-1].t_end, res["t_exit"]) if reqs and reqs[-1].t_end else None
    sample["request_bytes"] = reqs[0].bytes
    if gaps:
        sample["turn_gap_ms"] = stats.median(gaps)
        sample["_series"] = {"gap_ms": gaps, "cpu_turn_ms": cpus}
    if cpus:
        sample["turn_cpu_ms"] = stats.median(cpus)
    return sample


def visible(chunks):
    """Length of the visible text in byte chunks once escape sequences are removed."""
    return len(ANSI.sub(b"", b"".join(chunks)).strip())


def one_pty(ctx, arm):
    if not ctx.supports(arm, "pty"):
        raise Skip("no interactive scenario for this arm")
    h = ctx.harness(arm, "pty")
    srv = ctx.server
    srv.reset(policy=ctx.policy(arm, 0, "pty"))
    meter = cgroup.new_meter()
    srv.on_request = cpu_hook(meter)
    term = procio.Pty(h.tui_argv(), h.env, h.cwd, meter, echo=False)  # first_input_accepted_ms must see the application's echo, not the kernel's
    sample = {}
    try:
        t_first = None
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline and t_first is None:
            with term.cond:
                term.cond.wait(0.05)
                seen = []
                for t, c in term.chunks:
                    seen.append(c)
                    if visible(seen) > 0:
                        t_first = t
                        break
        if t_first is None:
            return {"failure": "no visible output within 60 s"}
        sample["first_frame_ms"] = ms(term.t_spawn, t_first)
        # First accepted input: the first moment a typed key is echoed. Keys are sent every 40 ms from the first frame on; an arm that ignores them for 20 s fails.
        t_accept, probe_deadline, mark_in = None, time.monotonic() + 20, len(term.chunks)
        while t_accept is None and time.monotonic() < probe_deadline:
            term.type("x")
            with term.cond:
                term.cond.wait(0.04)
                for t, c in term.chunks[mark_in:]:
                    if b"x" in ANSI.sub(b"", c):
                        t_accept = t
                        break
        if t_accept is None:
            return {"failure": "no typed key was echoed within 20 s of the first frame"}
        sample["first_input_accepted_ms"] = ms(term.t_spawn, t_accept)
        term.type("\x15" * 2)  # clear the line (Ctrl-U) before the real prompt
        t_ready = term.wait_quiet(0.6, 60)
        if t_ready is None:
            return {"failure": "the screen never settled"}
        sample["ready_ms"] = ms(term.t_spawn, t_ready)
        sample["ready_cpu_ms"] = meter.cpu_ms()
        sample["ready_rss_mib"] = meter.max_proc_rss_mib()
        sample["ready_pss_mib"] = meter.current_pss_mib()
        mark = len(term.chunks)
        term.type("hello")
        time.sleep(0.15)
        t_input = now_ns()
        term.type("\r")
        if not srv.wait_requests(1, 60):
            return {"failure": "the prompt never reached the endpoint"}
        sample["input_to_request_ms"] = ms(t_input, srv.requests[0].t_recv)
        t_reply = term.wait_text(REPLY.split()[0], 60, since=mark)
        if t_reply is None:
            return {"failure": "the reply never appeared on screen"}
        sample["input_to_reply_visible_ms"] = ms(t_input, t_reply)
        term.wait_quiet(0.6, 30)
        term.type("\x04")
        procio.wait(meter, term.proc, 5)
        sample.update({"cpu_ms": meter.cpu_ms(), "peak_mem_mib": meter.peak_mem_mib(), "peak_rss_mib": meter.max_proc_rss_mib(), "meter": meter.mode})
        return sample
    finally:
        term.close()
        meter.close()
        srv.on_request = None


def start_rpc(ctx, arm, scenario, ext=False, compaction=None, context_window=128000):
    h = ctx.harness(arm, scenario, extensions=arm_extensions(arm, ext), compaction=compaction, context_window=context_window)
    meter = cgroup.new_meter()
    ctx.server.on_request = cpu_hook(meter)
    rpc = procio.Rpc(h.rpc_argv(session=scenario == "long-session"), h.env, h.cwd, meter)
    rpc.cursor = 0
    rpc.dialect = arm.spec.get("rpc", {})
    return h, meter, rpc


def wait_ready(rpc):
    ident, _ = rpc.send({"type": "get_state"})
    # Pi names the command in the response; the Rust port rpi answers with only the request id.
    got = rpc.wait_for(lambda r: r.get("type") == "response" and (r.get("command") == "get_state" or r.get("id") == ident), 60)
    if got is not None:
        rpc.cursor = got[2]
    return got


# A prompt sent while automatic compaction runs after agent_end: Pi 1.1.0 refuses it with "Agent is already processing" (agent-session.ts:2014-2018),
# because the run is still active and only manual compaction sets _compactionAbortController (:1986-1990, :2766). PiG's RPC admission answers
# "Cannot submit a prompt while compaction is in progress" instead, because it checks every compaction (coding/cli/rpc_admission.go, promptInput):
# a PiG divergence, not a second Pi message. Both are accepted so the turn can be measured; each turn row records the message it got.
# "Agent is currently compacting" is the third-party Rust port's refusal.
BUSY_MESSAGES = ("Agent is already processing", "Cannot submit a prompt while compaction is in progress", "Agent is currently compacting")


def is_busy_rejection(r):
    return r.get("type") == "response" and r.get("command") == "prompt" and r.get("success") is False and any(m in str(r.get("error", "")) for m in BUSY_MESSAGES)


def drive_turns(ctx, rpc, meter, turns, series, sample_every=0):
    """Send `turns` prompts one after another and record each. Returns a failure string or None."""
    srv = ctx.server
    last_cpu = meter.cpu_ms()
    for i in range(turns):
        first_req = len(srv.requests)
        _, t_send = rpc.send(prompt_command(rpc, f"Turn {i}. {PROMPT}"))
        got = rpc.wait_for(lambda r: is_turn_end(rpc, r) or is_busy_rejection(r), ctx.opts.timeout, start=rpc.cursor)
        refused = None
        deadline = time.monotonic() + ctx.opts.timeout
        while got is not None and got[1].get("type") == "response":
            refused = refused if refused is not None else str(got[1].get("error", ""))
            # The previous turn's automatic compaction is still running, so the harness refuses a new prompt (Pi: "Agent is already processing").
            # A user would send it again once it ends; the wait is not part of the turn. A harness that reports no compaction event
            # (pi_agent_rust) is retried every 50 ms until the deadline.
            rpc.cursor = got[2]
            if time.monotonic() > deadline:
                return f"turn {i}: the harness stayed busy for {ctx.opts.timeout}s after refusing a prompt"
            done = rpc.wait_for(lambda r: r.get("type") in ("compaction_end", "agent_settled"), 0.2, start=rpc.cursor)
            if done is not None:
                rpc.cursor = done[2]
            else:
                time.sleep(0.05)
            first_req = len(srv.requests)
            _, t_send = rpc.send(prompt_command(rpc, f"Turn {i}. {PROMPT}"))
            got = rpc.wait_for(lambda r: is_turn_end(rpc, r) or is_busy_rejection(r), ctx.opts.timeout, start=rpc.cursor)
        if got is None:
            tail = [{k: r.get(k) for k in ("type", "command", "success", "error") if r.get(k) is not None} for _, r in rpc.records[-6:]]
            return f"turn {i}: no agent_end within {ctx.opts.timeout}s; last records {json.dumps(tail)}"
        t_end, _, rpc.cursor = got
        cpu = meter.cpu_ms()
        mine = [r for r in srv.requests[first_req:] if r.fault is None and r.tools]
        row = {"turn": i, "wall_ms": ms(t_send, t_end), "cpu_ms": cpu - last_cpu, "requests": len(srv.requests) - first_req}
        if refused is not None:
            row["refused_while_busy"] = refused
        if mine and mine[-1].t_end:
            row["harness_added_ms"] = ms(mine[-1].t_end, t_end)
        if len(mine) > 1:
            row["turn_gap_ms"] = stats.median([ms(a.t_end, b.t_recv) for a, b in zip(mine, mine[1:]) if a.t_end])
        if sample_every and i % sample_every == 0:
            row["rss_mib"] = meter.max_proc_rss_mib()
        last_cpu = cpu
        series.append(row)
    return None


def lock_sha(arm):
    return armlib.lock.sha256_file(os.path.realpath(arm.binary))


def count_events(rpc, kind):
    return sum(1 for _, r in rpc.records if r.get("type") == kind)


def one_rpc(ctx, arm, ext=False, scenario="rpc"):
    if ext and arm.no_extensions:
        raise Skip("the extension runtime is stripped from this build")
    if not ctx.supports(arm, "rpc"):
        raise Skip("no RPC mode")
    ctx.harness(arm, scenario, extensions=arm_extensions(arm, ext), compaction=None, context_window=128000)
    ctx.server.reset(policy=ctx.policy(arm, 1, scenario))
    h, meter, rpc = start_rpc(ctx, arm, scenario, ext)
    try:
        got = wait_ready(rpc)
        if got is None:
            return {"failure": "no get_state response"}
        sample = {"ready_ms": ms(rpc.t_spawn, got[0]), "ready_cpu_ms": meter.cpu_ms(), "ready_rss_mib": meter.max_proc_rss_mib(), "ready_pss_mib": meter.current_pss_mib(), "meter": meter.mode}
        if ext:
            # Pi answers get_state before its extensions finish loading; poll get_commands until the ten extension commands are listed.
            deadline = time.monotonic() + 60
            loaded, cmds = [], None
            while len(loaded) < 10 and time.monotonic() < deadline:
                rpc.send({"type": "get_commands"})
                cmds = rpc.wait_for(lambda r: r.get("type") == "response" and r.get("command") == "get_commands", 30, start=rpc.cursor)
                if cmds is None:
                    return {"failure": "no get_commands response"}
                rpc.cursor = cmds[2]
                loaded = [c for c in (cmds[1].get("data") or {}).get("commands", []) if str(c.get("name", "")).startswith("bench-")]
                if len(loaded) < 10:
                    time.sleep(0.05)
            if len(loaded) < 10:
                return {"failure": f"extensions did not load: {len(loaded)} of 10 bench commands listed after 60 s"}
            sample.update(extensions_ready_ms=ms(rpc.t_spawn, cmds[0]), extensions_ready_cpu_ms=meter.cpu_ms(), extensions_ready_rss_mib=meter.max_proc_rss_mib())
        series = []
        bad = drive_turns(ctx, rpc, meter, ctx.opts.rpc_turns, series)
        if bad:
            return {"failure": bad}
        if ext and len(ctx.server.requests[0].tools) < 4 + 10:
            return {"failure": f"extensions did not load: the first request declared {ctx.server.requests[0].tools}"}
        warm = series[2:] if len(series) > 4 else series
        for key in ("wall_ms", "cpu_ms", "harness_added_ms", "turn_gap_ms"):
            vals = [r[key] for r in warm if key in r]
            if vals:
                sample["turn_" + key] = stats.median(vals)
        cpus = [r["cpu_ms"] for r in warm]
        if cpus:  # where CPU time is charged in whole clock ticks (Windows), a turn's median is 0 or one tick; the mean is not
            sample["turn_cpu_mean_ms"] = sum(cpus) / len(cpus)
        sample["peak_rss_mib"] = meter.max_proc_rss_mib()
        t_close = now_ns()
        rpc.close()
        sample["teardown_ms"] = ms(t_close, rpc.t_exit)
        sample["cpu_ms"] = meter.cpu_ms()
        sample["peak_mem_mib"] = meter.peak_mem_mib()
        sample["_series"] = {"turns": series}
        return sample
    finally:
        if rpc.proc.poll() is None:
            rpc.close(grace=1)
        meter.close()
        ctx.server.on_request = None


def one_long(ctx, arm):
    if not ctx.supports(arm, "rpc"):
        raise Skip("no RPC mode")
    turns = ctx.opts.long_turns
    compaction = {"enabled": True, "reserveTokens": 8000, "keepRecentTokens": 6000}
    ctx.harness(arm, "long-session", extensions=(), compaction=compaction, context_window=32000)
    ctx.server.reset(policy=ctx.policy(arm, 1, "long-session"))
    h, meter, rpc = start_rpc(ctx, arm, "long-session", compaction=compaction, context_window=32000)
    series = []
    try:
        got = wait_ready(rpc)
        if got is None:
            return {"failure": "no get_state response"}
        sample = {"ready_ms": ms(rpc.t_spawn, got[0]), "meter": meter.mode}
        t0 = now_ns()
        bad = drive_turns(ctx, rpc, meter, turns, series, sample_every=50)
        if bad:
            return {"failure": bad, "_series": {"turns": series}}
        sample["total_wall_ms"] = ms(t0, now_ns())
        sample["turns"] = turns
        sample["compactions"] = count_events(rpc, "compaction_end")
        if sample["compactions"] < 1:
            return {"failure": "no compaction happened, so the run does not exercise compaction", "_series": {"turns": series}}
        edge = max(10, turns // 20)
        for key in ("cpu_ms", "wall_ms", "harness_added_ms"):
            a = [r[key] for r in series[:edge] if key in r]
            b = [r[key] for r in series[-edge:] if key in r]
            if a and b:
                sample[f"first_turn_{key}"] = stats.median(a)
                sample[f"last_turn_{key}"] = stats.median(b)
        sample["peak_rss_mib"] = meter.max_proc_rss_mib()
        sample["cpu_ms"] = meter.cpu_ms()
        sample["cpu_ms_per_turn"] = sample["cpu_ms"] / turns
        sample["session_bytes"] = sum(os.path.getsize(os.path.join(b, f)) for b, _, fs in os.walk(h.sessions) for f in fs)
        rpc.close()
        sample["peak_mem_mib"] = meter.peak_mem_mib()
        sample["_series"] = {"turns": series}
        return sample
    finally:
        if rpc.proc.poll() is None:
            rpc.close(grace=1)
        meter.close()
        ctx.server.on_request = None


RUNNERS = {
    "version": one_version,
    "print": lambda c, a: one_print(c, a, 0, False, "print"),
    "print-tools": lambda c, a: one_print(c, a, 8, False, "print-tools"),
    "pty": one_pty,
    "rpc": lambda c, a: one_rpc(c, a, False, "rpc"),
    "print-ext10": lambda c, a: one_print(c, a, 0, True, "print-ext10"),
    "rpc-ext10": lambda c, a: one_rpc(c, a, True, "rpc-ext10"),
    "long-session": one_long,
}


def native(arm):
    return arm.family in ("pig", "pi") and not arm.spec.get("third_party")


def prompt_command(rpc, text):
    """Pi's RPC prompt carries `message`; rpi's carries `content` (rpi-cli modes.rs, rpc_prompt)."""
    return {"type": "prompt", getattr(rpc, "dialect", {}).get("prompt_field", "message"): text}


def is_turn_end(rpc, record):
    return record.get("type") == getattr(rpc, "dialect", {}).get("turn_end", "agent_end")


def run(opts):
    """Run the selected scenarios and write raw.json. Returns the result; result["ok"] is False on a failure of a native arm or any egress."""
    arm_list = list(getattr(opts, "arm_list", None) or armlib.select(armlib.load(), opts.arms))
    scenarios = opts.scenarios or SCENARIOS
    result = {"kind": "overhead", "options": {"runs": opts.runs, "warmup": opts.warmup, "rpc_turns": opts.rpc_turns, "long_turns": opts.long_turns, "long_runs": opts.long_runs,
                                              "timeout": opts.timeout, "scenarios": scenarios, "prompt": PROMPT},
              "samples": {}, "failures": {}, "skipped": {}, "limitations": {}, "egress": dict(egress.describe(), violations=[])}
    outside = armlib.outside_snapshot()
    with armlib.Workspace() as ws:
        ws.install_all(arm_list)
        for aid in opts.aa:
            base = next((a for a in arm_list if a.id == aid), None)
            if base is None or not base.binary:
                raise SystemExit(f"--aa {aid}: not a selected, installed arm")
            twin = copy.copy(base)
            twin.id, twin.name = aid + "-b", base.name + " (A/A copy)"
            arm_list.append(twin)
        for a in arm_list:
            if not a.binary:
                result["skipped"][a.id] = {"*": a.skip}
        result["env"] = envinfo.collect(arm_list, seed=opts.seed)
        runnable = [a for a in arm_list if a.binary]
        if not runnable:
            raise SystemExit("no arm is available on this host")
        with MockServer() as server:
            ctx = Ctx(ws.work, server, opts)
            streak = {}
            for scenario in scenarios:
                runs, warmup = (opts.long_runs, 0) if scenario == "long-session" else (opts.runs, opts.warmup)
                alive = list(runnable)
                for rep in range(warmup + runs):
                    shift = rep % len(alive) if alive else 0
                    for a in alive[shift:] + alive[:shift]:  # rotate the start so no arm always follows a pause
                        t0 = time.monotonic()
                        try:
                            sample = RUNNERS[scenario](ctx, a)
                        except Skip as why:
                            result["skipped"].setdefault(a.id, {})[scenario] = str(why)
                            alive = [x for x in alive if x is not a]
                            continue
                        left = sorted(egress.take())
                        label = f"{scenario:12} {a.id:10} rep {rep + 1}/{warmup + runs}{' (warmup)' if rep < warmup else ''}"
                        if left:
                            note = f"egress: the harness connected to {', '.join(left)}"
                            excluded = not native(a)
                            result["egress"]["violations"].append({"arm": a.id, "scenario": scenario, "remotes": left, "excluded": excluded})
                            print(f"bench: {label} FAILED: {note}", flush=True)
                            if excluded:
                                # A third-party CLI that contacts the network on its own is excluded from this scenario, with the destinations recorded.
                                result["limitations"].setdefault(a.id, {})[scenario] = note
                                result["samples"].get(scenario, {}).pop(a.id, None)
                                alive = [x for x in alive if x is not a]
                            else:
                                result["failures"].setdefault(scenario, {}).setdefault(a.id, []).append({"rep": rep, "failure": note, "series": None})
                            continue
                        if "failure" in sample:
                            print(f"bench: {label} FAILED: {sample['failure']}", flush=True)
                            if native(a):
                                result["failures"].setdefault(scenario, {}).setdefault(a.id, []).append({"rep": rep, "failure": sample["failure"], "series": sample.get("_series")})
                            else:
                                # A CLI that will not run headless without a real login or key is excluded, never given a key. A single failed run of a
                                # third-party CLI is kept as a note; three in a row (or a failed first measured run with no success yet) exclude it.
                                streak[(a.id, scenario)] = streak.get((a.id, scenario), 0) + 1
                                result["limitations"].setdefault(a.id, {})[scenario] = sample["failure"]
                                if streak[(a.id, scenario)] >= 3 or (rep >= warmup and not result["samples"].get(scenario, {}).get(a.id)):
                                    result["samples"].get(scenario, {}).pop(a.id, None)
                                    alive = [x for x in alive if x is not a]
                            continue
                        streak[(a.id, scenario)] = 0
                        print(f"bench: {label} {time.monotonic() - t0:.1f}s", flush=True)
                        if rep >= warmup:
                            result["samples"].setdefault(scenario, {}).setdefault(a.id, []).append(sample)
                            checkpoint.save(result, opts.out)
                    if not alive:
                        break
            result["isolation"] = {a.id: ctx.harnesses_for(a)[0].isolation_env() for a in runnable if ctx.harnesses_for(a)}
            result["outside_writes"], result["outside_modified"] = armlib.outside_changes(outside, armlib.outside_snapshot())
            for a in runnable:  # the executable must be what was verified at install
                if a.binary and not a.unpinned and a.source == "npm":
                    want = a.pin.get("bin_sha256", {}).get("-".join(armlib.host_platform()))
                    if want and lock_sha(a) != want:
                        result["failures"].setdefault("post-check", {})[a.id] = [{"rep": 0, "failure": "the executable changed during the run", "series": None}]
    result["ok"] = not result["failures"] and not result["outside_writes"] and not [v for v in result["egress"]["violations"] if not v.get("excluded")]
    os.makedirs(opts.out, exist_ok=True)
    with open(os.path.join(opts.out, "raw.json"), "w") as f:
        json.dump(result, f, indent=1)
    checkpoint.clear(opts.out)
    return result

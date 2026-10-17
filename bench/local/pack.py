# SPDX-License-Identifier: MIT
"""One harness against a local model server through the recording proxy: what the harness costs while a real model answers.

For each arm the pack starts a proxy in front of the model server, runs the arm's RPC mode in its own meter (cgroup, systemd scope or rusage) and sends
`turns` prompts. Per turn it records the harness CPU and wall time, and from the proxy the prompt tokens the server counted, the request size, the tool
count and the system-prompt size. It also records the time the harness adds before the first request and between the server's first byte and its first
streamed event, the harness's peak RSS (idle, after 5 turns, at the end) and the bytes its session and agent directories grew.
"""
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "evals"))
sys.path.insert(0, HERE)
from pigbench import cgroup, procio, stats  # noqa: E402
from pigbench.harness import Harness  # noqa: E402
from pigbench.overhead import is_busy_rejection, is_turn_end, prompt_command, wait_ready  # noqa: E402
import proxy as proxymod  # noqa: E402

PROMPT = "Reply with the single word: ok"
STREAM_EVENTS = ("message_update", "message_start")


def dir_bytes(path):
    total = 0
    for base, _, files in os.walk(path):
        for name in files:
            try:
                total += os.path.getsize(os.path.join(base, name))
            except OSError:
                pass
    return total


def note_egress(out, meter):
    """Add the remotes outside the loopback that the meter saw the harness's process tree connect to (its socket audit) to out["egress"].
    The proxy runs in this process, not in the harness's tree, so a hosted upstream does not count; only the harness's own connections do."""
    seen = set(out.get("egress", ())) | set(getattr(meter, "egress", ()) or ())
    if seen:
        out["egress"] = sorted(seen)


def turn_row(i, t_send, t_end, cpu_ms, records, events):
    """One turn from the proxy's records of its requests and the harness's streamed events as (time, type) pairs."""
    row = {"turn": i, "wall_ms": round((t_end - t_send) / 1e6, 1), "cpu_ms": cpu_ms, "requests": len(records)}
    sent = [r for r in records if "t_start" in r]
    if sent:
        # A harness may send a tool-less side request first (OpenCode asks for a session title): the prompt cost is that of the first request that declares tools.
        first = min([r for r in sent if r.get("tools")] or sent, key=lambda r: r["t_start"])
        row["harness_before_request_ms"] = round((min(r["t_start"] for r in sent) - t_send) / 1e6, 1)
        row["prompt_tokens"] = first.get("prompt_tokens")
        row["request_bytes"], row["tools"], row["system_bytes"] = first.get("request_bytes"), first.get("tools"), first.get("system_bytes")
        if "t_first_byte" in first:
            streamed = [t for t, kind in events if kind in STREAM_EVENTS and t >= first["t_first_byte"]]
            if streamed:
                row["harness_after_first_byte_ms"] = round((min(streamed) - first["t_first_byte"]) / 1e6, 1)
        model_ms = sum(r["t_end"] - r["t_start"] for r in sent if "t_end" in r) / 1e6
        row["model_ms"] = round(model_ms, 1)
        row["harness_ms"] = round(row["wall_ms"] - model_ms, 1)
        row["generation_cpu_share"] = round(cpu_ms / model_ms, 4) if model_ms > 0 else None
    return row


def run_arm(arm, ws, upstream, turns=10, api_key=None, context_window=16384, timeout=300, log=lambda *_: None):
    """Returns {"turns": [...], "rss_mib": {...}, "storage_bytes": ..., "failure": ...}. Nothing here spends money unless `upstream` is a hosted model."""
    out = {"arm": arm.id, "turns": [], "rss_mib": {}}
    with proxymod.Proxy(upstream, api_key=api_key) as px:
        h = Harness(arm, ws.work, px, context_window=context_window, tag="local")
        with open(os.path.join(h.cwd, "README.md"), "w") as f:
            f.write("# Project\n")
        meter = cgroup.new_meter()
        rpc = procio.Rpc(h.rpc_argv(session=True), h.env, h.cwd, meter)
        rpc.cursor = 0
        rpc.dialect = arm.spec.get("rpc", {})
        try:
            if wait_ready(rpc) is None:
                out["failure"] = "no get_state response"
                return out
            out["rss_mib"]["ready"] = meter.max_proc_rss_mib()
            before = dir_bytes(h.agent_dir) + dir_bytes(h.sessions)
            last_cpu = meter.cpu_ms()
            for i in range(turns):
                seen = len(px.recorder.snapshot())
                _, t_send = rpc.send(prompt_command(rpc, f"Turn {i}. {PROMPT}"))
                got = rpc.wait_for(lambda r: is_turn_end(rpc, r) or is_busy_rejection(r), timeout, start=rpc.cursor)
                if got is None or got[1].get("type") == "response":
                    out["failure"] = f"turn {i}: {'no agent_end in %ds' % timeout if got is None else 'prompt refused: ' + str(got[1].get('error'))}"
                    break
                t_end, _, rpc.cursor = got
                cpu = meter.cpu_ms()
                time.sleep(0.05)
                events = [(t, r.get("type")) for t, r in rpc.records if t >= t_send]
                out["turns"].append(turn_row(i, t_send, t_end, cpu - last_cpu, px.recorder.snapshot()[seen:], events))
                last_cpu = cpu
                if i == 4:
                    out["rss_mib"]["after_5_turns"] = meter.max_proc_rss_mib()
            out["rss_mib"]["end"] = meter.max_proc_rss_mib()
            out["storage_bytes"] = dir_bytes(h.agent_dir) + dir_bytes(h.sessions) - before
        finally:
            rpc.close()
            meter.close()
            note_egress(out, meter)
    return out


def run_print_arm(arm, ws, upstream, runs=3, api_key=None, context_window=16384, timeout=600):
    """A harness without an RPC mode (or one run as `-p`): `runs` one-shot prompts, each a fresh process. The proxy gives the same prompt-cost fields as the RPC turns."""
    out = {"arm": arm.id, "mode": "print", "turns": [], "rss_mib": {}}
    with proxymod.Proxy(upstream, api_key=api_key) as px:
        h = Harness(arm, ws.work, px, context_window=context_window, tag="local-print")
        with open(os.path.join(h.cwd, "README.md"), "w") as f:
            f.write("# Project\n")
        before = dir_bytes(h.home)
        for i in range(runs):
            seen = len(px.recorder.snapshot())
            meter = cgroup.new_meter()
            try:
                res = procio.Batch(h.print_argv(f"Run {i}. {PROMPT}"), h.env, h.cwd, meter, timeout).run()
            finally:
                meter.close()
                note_egress(out, meter)
            records = px.recorder.snapshot()[seen:]
            if res["code"] != 0 or res["timed_out"] or not any(r.get("status") == 200 for r in records):
                out["failure"] = f"run {i}: exit {res['code']}, timed out {res['timed_out']}, {len(records)} requests ({[r.get('status') or r.get('error') for r in records][:4]}); stderr: {res['stderr'][-300:]}; stdout: {res['stdout'][-300:]}"
                break
            row = turn_row(i, res["t_spawn"], res["t_exit"], res["cpu_ms"], records, [])
            row["peak_rss_mib"] = res["max_proc_rss_mib"]
            out["turns"].append(row)
        out["storage_bytes"] = dir_bytes(h.home) - before
        if out["turns"]:
            out["rss_mib"]["peak"] = max(r["peak_rss_mib"] for r in out["turns"])
    return out


TASKS = os.path.join(HERE, "tasks")


def check_task(task_id, workdir, spec, home):
    """Run the task's pinned check on a fresh copy of the pinned task into which only the files the task lets the harness edit (`editable`) are
    copied from workdir. A harness that rewrites or deletes the tests, or adds a module that shadows one the check imports, therefore changes
    nothing the check sees. Returns the CompletedProcess."""
    with tempfile.TemporaryDirectory(prefix="pigpack-check-") as clean:
        shutil.copytree(os.path.join(TASKS, task_id), clean, dirs_exist_ok=True, ignore=shutil.ignore_patterns("task.json", "__pycache__"))
        for rel in spec["editable"]:
            src = os.path.join(workdir, rel)
            if os.path.isfile(src):
                shutil.copyfile(src, os.path.join(clean, rel))
            elif os.path.exists(os.path.join(clean, rel)):
                os.unlink(os.path.join(clean, rel))  # the harness deleted it
        return subprocess.run(spec["check"], cwd=clean, capture_output=True, text=True, timeout=120, env={"PATH": os.environ["PATH"], "HOME": home, "PYTHONDONTWRITEBYTECODE": "1"})


def run_task(arm, ws, upstream, task_id="rangesum", attempts=3, api_key=None, context_window=16384, timeout=300):
    """Task completion: the harness gets a pinned task directory with a failing test and one prompt, with its default tools, and the pinned check decides.

    Each attempt starts from a fresh copy of the task. The check runs after the harness exits, outside the harness, on a fresh copy of the pinned task
    holding only the files the harness may edit (check_task). A pass is the check's exit status 0; the harness's own report of success does not count."""
    with open(os.path.join(TASKS, task_id, "task.json")) as f:
        spec = json.load(f)
    out = {"arm": arm.id, "task": task_id, "attempts": []}
    with proxymod.Proxy(upstream, api_key=api_key) as px:
        h = Harness(arm, ws.work, px, context_window=context_window, tag="task")
        for i in range(attempts):
            for name in os.listdir(h.cwd):
                path = os.path.join(h.cwd, name)
                shutil.rmtree(path) if os.path.isdir(path) else os.unlink(path)
            shutil.copytree(os.path.join(TASKS, task_id), h.cwd, dirs_exist_ok=True, ignore=shutil.ignore_patterns("task.json", "__pycache__"))
            seen = len(px.recorder.snapshot())
            meter = cgroup.new_meter()
            try:
                res = procio.Batch(h.print_argv(spec["prompt"]), h.env, h.cwd, meter, timeout).run()
            finally:
                meter.close()
                note_egress(out, meter)
            records = px.recorder.snapshot()[seen:]
            check = check_task(task_id, h.cwd, spec, h.home)
            out["attempts"].append({"attempt": i, "passed": check.returncode == 0, "exit": res["code"], "timed_out": res["timed_out"], "wall_ms": round(res["wall_ms"], 1), "cpu_ms": res["cpu_ms"],
                                    "requests": len(records), "prompt_tokens": sum(r.get("prompt_tokens") or 0 for r in records), "completion_tokens": sum(r.get("completion_tokens") or 0 for r in records)})
    out["passed"], out["attempted"] = sum(a["passed"] for a in out["attempts"]), len(out["attempts"])
    return out


def summarize(out):
    rows = out["turns"]
    def med(key, digits=1):
        values = [r[key] for r in rows if r.get(key) is not None]
        return round(stats.median(values), digits) if values else None
    return {"arm": out["arm"], "mode": out.get("mode", "rpc"), "turns": len(rows), "failure": out.get("failure"), "prompt_tokens_first": rows[0].get("prompt_tokens") if rows else None,
            "prompt_tokens_last": rows[-1].get("prompt_tokens") if rows else None, "tools": rows[0].get("tools") if rows else None, "system_bytes": rows[0].get("system_bytes") if rows else None,
            "harness_before_request_ms": med("harness_before_request_ms"), "harness_after_first_byte_ms": med("harness_after_first_byte_ms"), "harness_ms": med("harness_ms"),
            "cpu_ms": med("cpu_ms"), "generation_cpu_share": med("generation_cpu_share", 5), "rss_mib": out["rss_mib"], "storage_bytes": out.get("storage_bytes"), "egress": out.get("egress")}

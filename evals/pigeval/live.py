# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Run fixed coding tasks with a real model and check each result with the task's own command."""
import hashlib
import json
import os
import shutil
import statistics
import subprocess
import tempfile
import time
import tomllib

from . import proc, registry, transcript
from .overhead import host, prepare

TASKS_DIR = os.path.join(registry.EVALS_DIR, "tasks")


def load_tasks(spec="all", root=TASKS_DIR):
    wanted = None if spec in ("", "all") else set(spec.split(","))
    tasks = []
    for tid in sorted(os.listdir(root)):
        path = os.path.join(root, tid, "task.toml")
        if not os.path.isfile(path) or (wanted is not None and tid not in wanted):
            continue
        with open(path, "rb") as f:
            task = tomllib.load(f)
        for key in ("prompt", "check"):
            if key not in task:
                raise ValueError(f"{path}: missing {key}")
        task.update(id=tid, files=os.path.join(root, tid, "files"))
        task.setdefault("timeout", 600)
        task.setdefault("protected", [])
        tasks.append(task)
    if wanted and wanted - {t["id"] for t in tasks}:
        raise SystemExit(f"pigeval: unknown task(s): {', '.join(sorted(wanted - {t['id'] for t in tasks}))}")
    return tasks


def digest(root, names):
    h = hashlib.sha256()
    for name in sorted(names):
        path = os.path.join(root, name)
        h.update(name.encode() + b"\0" + (open(path, "rb").read() if os.path.exists(path) else b"<missing>"))
    return h.hexdigest()


def tokens(stdout):
    """Sum assistant usage from Pi-format --mode json events. Return None when the output has none."""
    total = None
    for line in stdout.splitlines():
        if not line.startswith("{"):
            continue
        try:
            event = json.loads(line)
        except ValueError:
            continue
        message = event.get("message") if isinstance(event, dict) else None
        if event.get("type") == "message_end" and isinstance(message, dict) and message.get("role") == "assistant" and isinstance(message.get("usage"), dict):
            usage = message["usage"]
            total = total or {"input": 0, "output": 0, "total": 0}
            total["input"] += usage.get("input", 0) + usage.get("cacheRead", 0) + usage.get("cacheWrite", 0)
            total["output"] += usage.get("output", 0)
            total["total"] += usage.get("totalTokens", 0)
    return total


def run_task(h, binary, task, model, work, timeout, real_home=False, run_index=0):
    repo = os.path.join(work, f"{h.id}-{task['id']}")
    shutil.copytree(task["files"], repo)
    if shutil.which("git"):
        for argv in (["git", "init", "-q"], ["git", "add", "-A"], ["git", "-c", "user.name=pigeval", "-c", "user.email=pigeval@localhost", "commit", "-qm", "task"]):
            subprocess.run(argv, cwd=repo, capture_output=True)
    before = digest(repo, task["protected"])
    env, _, values = prepare(h, os.path.join(work, "homes"), "", keep_credentials=True)
    if real_home:
        env["HOME"] = os.path.expanduser("~")
    model_id = model.split("/", 1)[1] if "/" in model else model
    argv = [binary, *registry.substitute(dict(values, prompt=task["prompt"], model=model, model_id=model_id), h.live_args)]
    result = proc.run(argv, env, repo, timeout)
    check = subprocess.run(["bash", "-c", task["check"]], cwd=repo, capture_output=True, text=True, timeout=300)
    tampered = digest(repo, task["protected"]) != before
    return {"harness": h.id, "task": task["id"], "passed": check.returncode == 0 and not tampered and not result["timed_out"],
            "exit": result["code"], "timed_out": result["timed_out"], "tampered": tampered, "wall_s": round(result["wall"], 2),
            "cpu_s": round(result["cpu"], 2), "rss_mib": round(result["rss_mib"], 1), "run": run_index, "metrics": transcript.metrics(result["stdout"]),
            "check_output": (check.stdout + check.stderr)[-400:]}


def _mean_sem(values):
    values = [v for v in values if v is not None]
    if not values:
        return None, None
    mean = statistics.fmean(values)
    sem = statistics.stdev(values) / len(values) ** 0.5 if len(values) > 1 else 0.0
    return mean, sem


def summarize(results):
    """Per-harness summary. Pass rate's error is the spread across runs, as in oh-my-pi's charts."""
    runs = sorted({r["run"] for r in results})
    pass_rate, pass_sem = _mean_sem([100 * statistics.fmean(r["passed"] for r in results if r["run"] == i) for i in runs])
    metric = lambda key: _mean_sem([(r["metrics"] or {}).get(key) for r in results])
    costs = [(r["metrics"] or {}).get("cost_usd") for r in results]
    passes = sum(r["passed"] for r in results)
    summary = {"passed": passes, "attempts": len(results), "pass_rate": pass_rate, "pass_rate_sem": pass_sem,
               "wall_s_median": round(statistics.median(r["wall_s"] for r in results), 2),
               "cost_per_passing_task": sum(costs) / passes if passes and None not in costs else None}
    for key in ("cost_usd", "turns", "polling_calls", "tool_calls", "context_tokens", "output_tokens"):
        summary[key], summary[key + "_sem"] = metric(key)
    return summary


def run(args):
    harnesses = registry.select(registry.load(args.registry), args.harnesses)
    tasks = load_tasks(args.tasks, args.tasks_dir or TASKS_DIR)
    report = {"kind": "live", "generated": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), "host": host(),
              "model": args.model, "runs": args.runs, "tasks": [t["id"] for t in tasks], "harnesses": {}, "results": [], "summary": {}}
    work = tempfile.mkdtemp(prefix="pigeval-live-")
    for h in harnesses:
        binary = registry.resolve(h, args.pig)
        entry = report["harnesses"].setdefault(h.id, {"name": h.name})
        if not h.live_args or binary is None:
            entry["skipped"] = "no live argv in harnesses.toml" if not h.live_args else f"not installed; run: make setup SETUP_ARGS=--harnesses={h.id}"
            print(f"live: {h.id:9} skipped: {entry['skipped']}")
            continue
        for task in tasks:
            for i in range(args.runs):
                r = run_task(h, binary, task, args.model, os.path.join(work, f"run{i}"), args.timeout or task["timeout"], args.real_home, i)
                report["results"].append(r)
                m = r['metrics'] or {}
                print(f"live: {h.id:9} {task['id'][:30]:30} {'pass' if r['passed'] else 'FAIL'}  {r['wall_s']:>7} s  turns {m.get('turns', '-')}  polls {m.get('polling_calls', '-')}  cost {m.get('cost_usd', '-')}", flush=True)
        report["summary"][h.id] = summarize([r for r in report["results"] if r["harness"] == h.id])
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
    with open(args.out, "w") as f:
        json.dump(report, f, indent=2)
    print(f"live: wrote {args.out}")
    return 0

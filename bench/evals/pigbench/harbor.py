# SPDX-License-Identifier: MIT
"""Harbor integration, prepared and not run: job configs, a converter-compatibility check, and the paired analysis of finished jobs.

Nothing here starts a model job. `plan` writes job configs and validates them against Harbor's own schema; `compat` checks, with no model and
no key, that PiG writes what Harbor's Pi converter needs; `analyze` turns the trial results of a finished Harbor job into the paired report.
Running a job costs money and needs an explicit decision to spend it: the commands are printed, never executed.

Design: same model snapshot, context window, reasoning effort and tool set for the
arms of a comparison; MCP and web tools off; one fixed timeout; one gateway for all arms; arms interleaved; a Pi-vs-Pi A/A run for the noise
floor; the infrastructure-error rate per arm. Pairs: PiG against Claude Code on one Claude snapshot, PiG against Codex on one GPT snapshot, and
PiG against Copilot CLI (BYOK) and Pi on both. SWE-bench Pro public split (history-pruned, network blocked, k=3) and Terminal-Bench 2.1 (k=5).
"""
import json
import os
import subprocess

from . import arms as armlib
from . import cgroup, lock, procio, stats
from .harness import DUMMY_KEY, Harness
from .mock import MockServer, agentic_policy

HARBOR_VERSION = "0.24.0"
HARBOR_WHEEL_SHA256 = "23b7ba616a3aae4eff561ced5e2c51c7186f2981e53a770dea9c689d3969877c"
GATEWAY_HOST = "gateway"
GATEWAY_PORT = 8080
# The value every agent sends as its API key. The gateway replaces it with the real key, so the real key never enters a task container.
# It must be set in each agent's env: Harbor's Pi adapter refuses a custom endpoint without an API-key variable (pi.py, _build_custom_models_json),
# and without it Harbor resolves the key from the host environment (model_connection.py, resolve_model_connection) and passes the real key in.
GATEWAY_PLACEHOLDER_KEY = "pigbench-gateway-replaces-this-key"
HARBOR_PROVIDER = "harbor-endpoint"  # the provider name Harbor's Pi adapter writes into models.json (pi.py, _CUSTOM_PROVIDER)
# What PigAgent adds to every command (pigharbor/agents.py, PIG_ENV): PiG divergence D2 makes PiG read PIG_CODING_AGENT_DIR unless
# PIG_USE_PI_DIRS=1 selects the PI_CODING_AGENT_DIR that Harbor's Pi adapter sets.
PIG_ENV = {"PIG_USE_PI_DIRS": "1"}

# The registry names below are the ones to confirm with `harbor datasets list` before the first run; `plan` does not need them to validate.
DATASETS = {
    "swebench-pro": {"dataset": {"name": "swebench-pro", "version": "public"}, "n_attempts": 3, "note": "731 tasks; history-pruned images; network blocked (answer leaks found in two 2026 audits)"},
    "terminal-bench-2.1": {"dataset": {"name": "terminal-bench", "version": "2.1"}, "n_attempts": 5, "note": "89 tasks"},
}
PAIRS = {
    "claude": {"provider": "anthropic", "model_api": "anthropic-messages", "base_env": "ANTHROPIC_BASE_URL", "key_env": "ANTHROPIC_API_KEY", "arms": ["pig-0.5", "pig-0.4.1", "pi-1.1.0", "claude", "copilot"]},
    "gpt": {"provider": "openai", "model_api": "openai-responses", "base_env": "OPENAI_BASE_URL", "key_env": "OPENAI_API_KEY", "arms": ["pig-0.5", "pig-0.4.1", "pi-1.1.0", "codex", "copilot"]},
}


def agent_config(arm_id, pair, model, pig_binary=None, label=None):
    """One Harbor AgentConfig (as a dict) for an arm, pointed at the gateway, with no key in it."""
    pins = lock.read()["arms"]
    p = PAIRS[pair]
    base = f"http://{GATEWAY_HOST}:{GATEWAY_PORT}/{label or arm_id}"
    env = {"PIGBENCH_ARM": label or arm_id, p["base_env"]: base, p["key_env"]: GATEWAY_PLACEHOLDER_KEY}
    cfg = {"model_name": f"{p['provider']}/{model}", "env": env, "extra_allowed_hosts": [GATEWAY_HOST], "kwargs": {}}
    if arm_id in ("pi-1.1.0",):
        cfg.update(name="pi")
        cfg["kwargs"].update(version=pins[arm_id]["version"], model_api=p["model_api"])
    elif arm_id == "pig-0.5":
        cfg.update(import_path="pigharbor.agents:PigAgent")
        cfg["kwargs"].update(pig_binary=pig_binary or "<path to a static linux/amd64 build of this checkout>", model_api=p["model_api"])
    elif arm_id == "pig-0.4.1":
        e = pins[arm_id]
        cfg.update(import_path="pigharbor.agents:PigAgent")
        cfg["kwargs"].update(pig_url=e["release"]["base"].format(version=e["version"]) + e["assets"]["linux-amd64"]["name"], pig_sha256=e["assets"]["linux-amd64"]["sha256"], model_api=p["model_api"])
    elif arm_id == "claude":
        cfg.update(name="claude-code")
        cfg["kwargs"].update(version=pins[arm_id]["version"])
    elif arm_id == "codex":
        cfg.update(name="codex")
        cfg["kwargs"].update(version=pins[arm_id]["version"])
    elif arm_id == "copilot":
        cfg.update(import_path="pigharbor.agents:CopilotByokAgent")
        cfg["kwargs"].update(version=pins[arm_id]["version"])
        env.update({"COPILOT_PROVIDER_BASE_URL": base + "/v1", "COPILOT_PROVIDER_TYPE": "anthropic" if p["provider"] == "anthropic" else "openai", "COPILOT_OFFLINE": "true",
                    "COPILOT_PROVIDER_API_KEY": GATEWAY_PLACEHOLDER_KEY})
    else:
        raise SystemExit(f"no Harbor configuration for arm {arm_id}")
    return cfg


def job_config(name, dataset, agents, jobs_dir="jobs", concurrency=4):
    d = DATASETS[dataset]
    return {"job_name": name, "jobs_dir": jobs_dir, "n_attempts": d["n_attempts"], "n_concurrent_trials": concurrency, "timeout_multiplier": 1.0,
            "retry": {"max_retries": 0}, "environment": {"type": "docker", "delete": True, "override_cpus": 4, "override_memory_mb": 8192},
            "datasets": [d["dataset"]], "agents": agents}


def plan(out_dir, claude_model, gpt_model, pig_binary=None):
    """Write one job config per pairing and dataset, plus the Pi-vs-Pi A/A configs. Returns the list of written paths."""
    os.makedirs(out_dir, exist_ok=True)
    written = []
    for dataset in DATASETS:
        for pair, model in (("claude", claude_model), ("gpt", gpt_model)):
            agents = [agent_config(a, pair, model, pig_binary) for a in PAIRS[pair]["arms"]]
            path = os.path.join(out_dir, f"{dataset}-{pair}.json")
            write_json(path, job_config(f"{dataset}-{pair}", dataset, agents))
            written.append(path)
        aa = [agent_config("pi-1.1.0", "claude", claude_model, label="pi-a"), agent_config("pi-1.1.0", "claude", claude_model, label="pi-b")]
        path = os.path.join(out_dir, f"{dataset}-aa-pi-vs-pi.json")
        write_json(path, job_config(f"{dataset}-aa", dataset, aa))
        written.append(path)
    return written


def write_json(path, data):
    with open(path, "w", newline="\n") as f:
        json.dump(data, f, indent=2, sort_keys=True)
        f.write("\n")


VALIDATE = """
import json, sys
from harbor.models.job.config import JobConfig
for path in sys.argv[1:]:
    JobConfig.model_validate(json.load(open(path)))
    print("valid", path)
"""


def harbor_python():
    return os.environ.get("HARBOR_PYTHON")


def validate(paths):
    """Validate job configs with Harbor's own JobConfig (needs HARBOR_PYTHON). Returns (ok, message)."""
    py = harbor_python()
    if not py:
        return None, "HARBOR_PYTHON is not set; configs were written but not validated against Harbor"
    done = subprocess.run([py, "-c", VALIDATE, *paths], capture_output=True, text=True, timeout=300, env=dict(os.environ, PYTHONPATH=os.path.join(lock.EVALS, "harbor")))
    return done.returncode == 0, (done.stdout + done.stderr).strip()


def compat(opts):
    """Run PiG and Pi with Harbor's exact print/JSON flags against the mock and convert their logs with Harbor's own Pi adapter."""
    py = harbor_python()
    if not py:
        raise SystemExit("HARBOR_PYTHON must name a Python with harbor installed (pip install harbor==%s)" % HARBOR_VERSION)
    wanted = [a for a in armlib.select(armlib.load(), ["pig-0.5", "pig-0.4.1", "pi-1.1.0"]) if not opts.arms or opts.arms == ["all"] or a.id in opts.arms]
    result = {"kind": "harbor-compat", "harbor": HARBOR_VERSION, "arms": {}, "problems": []}
    dirs = {}
    with armlib.Workspace() as ws:
        ws.install_all(wanted)
        with MockServer() as server:
            for arm in wanted:
                h = Harness(arm, ws.work, server, tag="harbor")
                with open(os.path.join(h.cwd, "README.md"), "w", newline="\n") as f:
                    f.write("fixture\n")
                server.reset(policy=agentic_policy(h.cwd, tool_turns=2))
                logs = os.path.join(ws.work, f"logs-{arm.id}")
                sessions = os.path.join(logs, "pi", "sessions")
                os.makedirs(sessions)
                # The configuration Harbor's Pi adapter writes for a custom endpoint (pi.py, _build_custom_models_json and run): a models.json with
                # one provider whose apiKey references a variable, in the directory PI_CODING_AGENT_DIR names, and `--provider harbor-endpoint`.
                agent_dir = os.path.join(ws.work, f"harbor-agent-{arm.id}")
                os.makedirs(agent_dir)
                write_json(os.path.join(agent_dir, "models.json"), {"providers": {HARBOR_PROVIDER: {"baseUrl": server.base_url, "apiKey": "$ANTHROPIC_API_KEY", "api": "anthropic-messages",
                                                                                                    "models": [{"id": "bench-1"}]}}})
                env = {k: v for k, v in h.env.items() if k not in ("PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR")}
                env.update(PI_CODING_AGENT_DIR=agent_dir, ANTHROPIC_API_KEY=DUMMY_KEY)
                if arm.family == "pig":
                    env.update(PIG_ENV)
                meter = cgroup.new_meter()
                try:
                    res = procio.Batch([arm.binary, "--print", "--mode", "json", "--session-dir", sessions, "--provider", HARBOR_PROVIDER, "--model", "bench-1", "Read README.md and report."], env, h.cwd, meter, 120).run()
                finally:
                    meter.close()
                with open(os.path.join(logs, "pi.txt"), "w") as f:
                    f.write(res["stdout"])
                creds = sorted({r.auth or "" for r in server.requests})
                result["arms"][arm.id] = {"exit": res["code"], "stderr": res["stderr"][-200:], "requests": len(server.requests), "protocols": sorted({r.protocol for r in server.requests}),
                                          "key_resolved": bool(server.requests) and all(DUMMY_KEY in c for c in creds)}
                dirs[arm.id] = logs
        done = subprocess.run([py, os.path.join(lock.EVALS, "harbor", "convert_check.py"), *dirs.values()], capture_output=True, text=True, timeout=300)
        if done.returncode:
            raise SystemExit(f"Harbor's converter failed:\n{done.stderr[-1500:]}")
        converted = json.loads(done.stdout.strip().splitlines()[-1])
    for aid, logs in dirs.items():
        result["arms"][aid]["converted"] = converted[logs]
    oracle = result["arms"].get("pi-1.1.0")
    for aid, r in result["arms"].items():
        if r["exit"] != 0:
            result["problems"].append(f"{aid}: exit {r['exit']} with Harbor's flags")
        if not r["key_resolved"]:
            result["problems"].append(f"{aid}: the requests did not carry the key that models.json references as $ANTHROPIC_API_KEY ({r['requests']} requests)")
        c = r["converted"]
        if c["trajectory"] is None:
            result["problems"].append(f"{aid}: Harbor's converter produced no trajectory from the session")
        if oracle and aid != "pi-1.1.0":
            o = oracle["converted"]
            for field in ("event_types", "usage_keys"):
                if c[field] != o[field]:
                    result["problems"].append(f"{aid}: {field} {c[field]} differ from Pi's {o[field]}")
            if c["trajectory"] and o["trajectory"]:
                for field in ("steps", "final_metrics_keys", "metrics_per_call", "metric_keys"):
                    if c["trajectory"][field] != o["trajectory"][field]:
                        result["problems"].append(f"{aid}: trajectory {field} {c['trajectory'][field]} differ from Pi's {o['trajectory'][field]}")
    result["ok"] = not result["problems"]
    os.makedirs(opts.out, exist_ok=True)
    with open(os.path.join(opts.out, "compat.json"), "w") as f:
        json.dump(result, f, indent=1)
    return result


# ---- analysis of finished jobs ----



def trial_row(result):
    """One flat row from a Harbor trial result.json (a dict), or None when it is not a trial result."""
    if "task_name" not in result or "agent_info" not in result:
        return None
    cfg_env = ((result.get("config") or {}).get("agent") or {}).get("env") or {}
    arm = cfg_env.get("PIGBENCH_ARM") or result["agent_info"].get("name")
    rewards = (result.get("verifier_result") or {}).get("rewards") or {}
    reward = rewards.get("reward", (sum(rewards.values()) / len(rewards)) if rewards else None)
    ag = result.get("agent_result") or {}
    exc = result.get("exception_info")
    start, end = (result.get("agent_execution") or {}).get("started_at"), (result.get("agent_execution") or {}).get("finished_at")
    seconds = None
    if start and end:
        from datetime import datetime
        seconds = (datetime.fromisoformat(end.replace("Z", "+00:00")) - datetime.fromisoformat(start.replace("Z", "+00:00"))).total_seconds()
    return {"arm": arm, "task": result["task_name"], "reward": reward, "infra_error": bool(exc) and reward is None, "exception": (exc or {}).get("exception_type"),
            "input_tokens": ag.get("n_input_tokens"), "output_tokens": ag.get("n_output_tokens"), "cost_usd": ag.get("cost_usd"), "seconds": seconds}


def per_task(rows, arm, field):
    """{task: [values over attempts]} for one arm, leaving out infrastructure errors and missing values."""
    out = {}
    for r in rows:
        if r["arm"] == arm and not r["infra_error"] and r.get(field) is not None:
            out.setdefault(r["task"], []).append(float(r[field]))
    return out


def analyze(rows, reference, margin, seed, aa_pair=None):
    """Paired comparisons of every arm with the reference arm, task-level, plus infrastructure-error rates and the A/A floor."""
    arms = sorted({r["arm"] for r in rows})
    report = {"reference": reference, "margin": margin, "seed": seed, "arms": {}, "aa": None}
    for a in arms:
        mine = [r for r in rows if r["arm"] == a]
        infra = sum(r["infra_error"] for r in mine)
        ok = [r for r in mine if not r["infra_error"] and r["reward"] is not None]
        report["arms"][a] = {"trials": len(mine), "infra_errors": infra, "infra_rate": infra / len(mine) if mine else None, "infra_ci": stats.wilson(infra, len(mine)),
                             "scored": len(ok), "pass_rate": sum(1 for r in ok if r["reward"] >= 1) / len(ok) if ok else None}
    pvals, names = [], []
    for a in arms:
        if a == reference:
            continue
        x, y = stats.task_means(per_task(rows, a, "reward")), stats.task_means(per_task(rows, reference, "reward"))
        tasks, diffs = stats.paired_diffs(x, y)
        entry = {"tasks": len(tasks)}
        if diffs:
            boot = stats.paired_bootstrap(diffs, seed=seed)
            boot.pop("_means")
            entry.update(mean_diff=boot["mean"], ci=boot["ci"], sign_flip_p=stats.sign_flip_p(diffs, seed=seed), tost=stats.tost(diffs, margin, seed=seed))
            pvals.append(entry["sign_flip_p"])
            names.append(a)
        for field in ("cost_usd", "seconds", "input_tokens"):
            cx, cy = stats.task_means(per_task(rows, a, field)), stats.task_means(per_task(rows, reference, field))
            entry[f"ratio_{field}"] = stats.ratio_of_totals(cx, cy, seed=seed)
        report["arms"][a]["vs_reference"] = entry
    for name, adj in zip(names, stats.holm(pvals)):
        report["arms"][name]["vs_reference"]["holm_p"] = adj
    if aa_pair:
        x, y = stats.task_means(per_task(rows, aa_pair[0], "reward")), stats.task_means(per_task(rows, aa_pair[1], "reward"))
        _, diffs = stats.paired_diffs(x, y)
        report["aa"] = stats.noise_floor(diffs, seed=seed) if diffs else None
    return report


def load_rows(job_dirs):
    rows = []
    for d in job_dirs:
        for base, _, files in os.walk(d):
            if "result.json" in files:
                try:
                    with open(os.path.join(base, "result.json")) as f:
                        row = trial_row(json.load(f))
                except ValueError:
                    continue
                if row:
                    rows.append(row)
    return rows

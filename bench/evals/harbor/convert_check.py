# SPDX-License-Identifier: MIT
"""Run under Harbor's Python: convert saved Pi-style logs with Harbor's own Pi adapter and print what the converter extracted.

usage: python convert_check.py LOGS_DIR [LOGS_DIR ...]
Each LOGS_DIR holds pi.txt (the --mode json stdout) and pi/sessions/*.jsonl, as Harbor's adapter lays them out.
"""
import json
import pathlib
import sys

from harbor.agents.installed.pi import Pi

out = {}
for arg in sys.argv[1:]:
    logs = pathlib.Path(arg)
    agent = Pi(logs_dir=logs, model_name="bench/bench-1")
    loaded = agent._load_trajectory()
    events = [json.loads(line) for line in (logs / "pi.txt").read_text().splitlines() if line.strip().startswith("{")]
    usage_keys = sorted({k for e in events if e.get("type") == "message_end" and (e.get("message") or {}).get("role") == "assistant" for k in (e["message"].get("usage") or {})})
    entry = {"event_types": sorted({e.get("type") for e in events}), "usage_keys": usage_keys, "trajectory": None}
    if loaded:
        trajectory, metrics = loaded
        data = trajectory.to_json_dict()
        entry["trajectory"] = {"steps": len(data.get("steps", [])), "final_metrics_keys": sorted((data.get("final_metrics") or {}).keys()), "metrics_per_call": len(metrics),
                               "metric_keys": sorted({k for m in metrics for k in m.model_dump() if m.model_dump()[k] is not None}),
                               "prompt_tokens_total": (data.get("final_metrics") or {}).get("total_prompt_tokens"), "completion_tokens_total": (data.get("final_metrics") or {}).get("total_completion_tokens")}
    out[arg] = entry
print(json.dumps(out))

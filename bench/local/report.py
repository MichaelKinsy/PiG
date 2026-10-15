# SPDX-License-Identifier: MIT
"""python3 bench/local/report.py RAW.json [REFERENCE_ARM] -> report.md next to it.

One table per mode: for every arm the median over the rounds of each cost, the ratio to the reference arm with a 95% bootstrap interval when
there are at least two rounds, and every failure listed under Losses and limitations. The report is PROVISIONAL until the run is repeated on a quiet machine.
"""
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(os.path.dirname(HERE), "evals"))
from pigbench import stats  # noqa: E402

COLUMNS = [("prompt_tokens_first", "prompt tokens (1st request)"), ("tools", "tools"), ("system_bytes", "system prompt bytes"), ("harness_before_request_ms", "harness before 1st request (ms)"),
           ("harness_after_first_byte_ms", "harness after server's 1st byte (ms)"), ("cpu_ms", "harness CPU per turn (ms)"), ("generation_cpu_share", "harness CPU / model time"),
           ("rss_end", "RSS at end (MiB)"), ("storage_bytes", "state growth (bytes)")]


def value(row, key):
    if key == "rss_end":
        rss = row.get("rss_mib") or {}
        return rss.get("end", rss.get("peak"))
    return row.get(key)


def collect(raw):
    by_arm = {}
    for i, rnd in enumerate(raw["rounds"]):  # by position: two rounds with equal results would share one summary under rounds.index()
        for row in raw["summary"][i] if "summary" in raw else rnd:
            by_arm.setdefault(row["arm"], []).append(row)
    return by_arm


def fmt(x):
    return f"{x:,.0f}" if abs(x) >= 100 else (f"{x:.4g}" if abs(x) < 1 else f"{x:.1f}")


def cell(rows, key, ref_rows):
    values = [v for v in (value(r, key) for r in rows) if v is not None]
    if not values:
        return "n/a"
    text = fmt(stats.median(values))
    ref = [v for v in (value(r, key) for r in ref_rows or []) if v is not None]
    if ref and rows is not ref_rows and len(values) >= 2 and len(ref) >= 2:
        ratio = stats.ratio_of_medians(values, ref)
        low, high = ratio["ci"]
        text += f" ({ratio['ratio']:.2f}x [{low:.2f}, {high:.2f}])"
    elif ref and rows is not ref_rows and stats.median(ref):
        text += f" ({stats.median(values) / stats.median(ref):.2f}x)"
    return text


def render(raw, reference="pi-1.1.0"):
    by_arm = collect(raw)
    ref_rows = by_arm.get(reference)
    model = raw.get("model", {})
    lines = ["# Local-model harness cost (PROVISIONAL)", "",
             f"Model: {model.get('name') or model.get('hosted', 'unknown')} (revision {str(model.get('revision', ''))[:12]}, sha256 {str(model.get('sha256', ''))[:12]}). "
             f"Runtime: llama.cpp {raw.get('runtime', {}).get('version', 'hosted endpoint')}. Rounds: {len(raw['rounds'])}. Reference arm: {reference} (ratios in parentheses, 95% bootstrap interval with two or more rounds).", "",
             "Run on a shared machine unless the environment block says otherwise; repeat on a quiet one before quoting any number. State growth includes each harness's first-run caches.", "",
             ]
    # An RPC turn and a one-shot print run cost different things (a print run pays process start-up), so each mode gets its own table and
    # ratios only compare arms of the reference arm's mode.
    modes = {}
    for arm, rows in by_arm.items():
        if "attempts" not in rows[0]:
            modes.setdefault(rows[0].get("mode", "rpc"), []).append(arm)
    for mode, arms in modes.items():
        mode_ref = ref_rows if ref_rows and ref_rows[0].get("mode", "rpc") == mode else None
        lines += [f"## Harness cost, {mode} mode", "", "| arm | " + " | ".join(label for _, label in COLUMNS) + " |", "|---" * (len(COLUMNS) + 1) + "|"]
        lines += [f"| {arm} | " + " | ".join(cell(by_arm[arm], key, mode_ref) for key, _ in COLUMNS) + " |" for arm in arms]
        lines.append("")
    tasks = {arm: rows for arm, rows in by_arm.items() if rows and "attempts" in rows[0]}
    if tasks:
        lines += ["", "## Task completion (the pinned check decides)", "", "| arm | task | passed / attempted | median wall (s) | prompt tokens per attempt |", "|---|---|---|---|---|"]
        for arm, rows in tasks.items():
            attempts = [a for r in rows for a in r["attempts"]]
            lines.append(f"| {arm} | {rows[0]['task']} | {sum(a['passed'] for a in attempts)} / {len(attempts)} | {stats.median([a['wall_ms'] for a in attempts]) / 1000:.1f} | {fmt(stats.median([a['prompt_tokens'] for a in attempts]))} |")
    lines += ["", "## Losses and limitations", ""]
    failures = [(arm, r["failure"]) for arm, rows in by_arm.items() for r in rows if r.get("failure")]
    failures += [(arm, "connected outside the loopback: " + ", ".join(r["egress"])) for arm, rows in by_arm.items() for r in rows if r.get("egress")]
    lines += [f"- {arm}: {message}" for arm, message in dict.fromkeys(failures)] or ["- none recorded"]
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    path = sys.argv[1]
    raw = json.load(open(path))
    with open(os.path.join(os.path.dirname(path), "report.md"), "w") as f:
        f.write(render(raw, *sys.argv[2:3]))

# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Render pigeval JSON results as the Markdown benchmark page."""
import json

from .overhead import SCENARIOS


def _ms(v):
    return f"{v:,.1f} ms" if isinstance(v, (int, float)) else "n/a"


def _row(h, r):
    return f"| {h} | {_ms(r.get('wall_ms_median'))} | {_ms(r.get('wall_ms_p90'))} | {_ms(r.get('cpu_ms_median'))} | {r.get('rss_mib_median', 'n/a')} MiB |"


def _harnesses(report):
    lines = ["| Harness | Version | Version command output | Status |", "|---|---|---|---|"]
    for h in report["harnesses"].values():
        own = f"`{h['version']}`" if h.get("version") else "not recorded"
        printed = f"`{h['version_command']}`: `{h['version_output']}`" if h.get("version_command") else "not recorded"
        lines.append(f"| {h['name']} | {own} | {printed} | {h.get('skipped', 'measured')} |")
    lines += ["", "Version is each harness's own version. Version command output records the startup command separately. "
              + _pig_version_note(report["harnesses"].get("pig"))]
    return lines


def _pig_version_note(pig):
    """Describe PiG's --version output without contradicting the recorded run.

    Current PiG prints `<PiG release>+<Pi release>` (D63). A run recorded from a
    binary that printed only the Pi release says so instead of claiming the
    composite form for output that does not show it.
    """
    composite = "PiG's `--version` prints its composite version, `<PiG release>+<Pi release>` (D63)."
    output = (pig or {}).get("version_output") or ""
    if not output or "+" in output:
        return composite
    return ("The PiG binary measured in this run predates the composite version string and printed only the pinned Pi release. "
            "Current " + composite)


def overhead(report, title="Harness benchmarks"):
    host = report["host"]
    lines = [f"# {title}", "",
             "Every harness below talks to the same local mock model. The mock answers each request with one fixed reply over the "
             "harness's own wire protocol (OpenAI Chat Completions, OpenAI Responses, or Anthropic Messages), so the model adds no variance. "
             "The differences come from the harness: process startup, request construction, streaming, Session I/O, and teardown.", "",
             "These numbers do not measure model quality or task success. `pigeval live` runs fixed coding tasks with a real model for that.", "",
             f"Measured {report['generated']} on {host['platform']} ({host['machine']}, {host['cpus']} CPUs). Each scenario ran "
             f"{report['warmup']} warm-up run and {report['runs']} measured runs. Prompt: `{report['prompt']}`.", "",
             "## Harnesses", ""]
    lines += _harnesses(report)
    by_id = {row["scenario"]: row for row in report["results"]}
    names = {hid: h["name"] for hid, h in report["harnesses"].items()}
    for sid, title_text in SCENARIOS:
        row = by_id.get(sid)
        if not row or not row["by"]:
            continue
        if sid == "resumed-session":
            title_text += f" ({report['session_exchanges']:,} exchanges)"
        lines += ["", f"## {title_text}", ""]
        if sid == "startup":
            lines += ["| Harness | Median | p90 | CPU | Peak RSS |", "|---|---:|---:|---:|---:|"]
            lines += [_row(names[hid], r) for hid, r in row["by"].items() if r.get("runs")]
        else:
            lines += ["| Harness | Median | p90 | CPU | Peak RSS | Requests | First request | System prompt | Tools |", "|---|---:|---:|---:|---:|---:|---:|---:|---:|"]
            for hid, r in row["by"].items():
                if r.get("runs"):
                    lines.append(_row(names[hid], r) + f" {r.get('requests', 'n/a')} | {r.get('request_bytes', 0):,} B | {r.get('system_bytes', 0):,} B | {r.get('tools', 'n/a')} |")
        failed = [names[hid] for hid, r in row["by"].items() if not r.get("runs")]
        if failed:
            lines += ["", f"Did not complete: {', '.join(failed)}. The JSON results record each failure."]
    lines += ["", "Peak RSS is the largest single process in the harness's process tree. Request sizes are the bytes of the HTTP request body.", "",
              "## Reproduce", "", "```bash", "make setup SETUP_ARGS=--harnesses=all   # installs the pinned versions above", "make evals", "```", "",
              "Results depend on the machine. Compare numbers from one run only. The harness registry, including every command line, "
              "is `evals/harnesses.toml`; the method is in `evals/README.md`.", ""]
    return "\n".join(lines)


def _pm(mean, sem, fmt):
    if mean is None:
        return "n/a"
    return fmt.format(mean) + (f" ± {fmt.format(sem)}" if sem else "")


def live(report):
    lines = ["# Live task results", "",
             f"Model `{report['model']}`, {report['runs']} run(s) per task, {len(report['tasks'])} tasks, measured {report['generated']}. "
             "Every run starts a fresh session in a copy of the task. Pass rate's ± is the standard error across runs; other ± values are the "
             "standard error across attempts. A polling call is a shell command that only waits on or inspects a running job "
             "(`sleep`, `ps`, `pgrep`, `top`, `watch`, `wait`).", "",
             "| Harness | Pass rate | Cost per task | Cost per passing task | Turns per task | Polling calls per task | Context tokens per task | Median wall time |",
             "|---|---:|---:|---:|---:|---:|---:|---:|"]
    for hid, s in report["summary"].items():
        cpp = "n/a" if s.get("cost_per_passing_task") is None else f"${s['cost_per_passing_task']:.3f}"
        lines.append(f"| {report['harnesses'][hid]['name']} | {_pm(s.get('pass_rate'), s.get('pass_rate_sem'), '{:.0f}%')} ({s['passed']}/{s['attempts']}) | "
                     f"{_pm(s.get('cost_usd'), s.get('cost_usd_sem'), '${:.3f}')} | {cpp} | {_pm(s.get('turns'), s.get('turns_sem'), '{:.1f}')} | "
                     f"{_pm(s.get('polling_calls'), s.get('polling_calls_sem'), '{:.1f}')} | {_pm(s.get('context_tokens'), s.get('context_tokens_sem'), '{:,.0f}')} | {s['wall_s_median']} s |")
    skipped = [f"{h['name']} ({h['skipped']})" for h in report["harnesses"].values() if h.get("skipped")]
    if skipped:
        lines += ["", "Not run: " + "; ".join(skipped) + "."]
    lines += ["", "Harnesses without machine-readable output report pass rate and wall time only. Codex reports no cost."]
    return "\n".join(lines) + "\n"


def run(args):
    with open(args.results) as f:
        report = json.load(f)
    text = overhead(report) if report.get("kind") == "overhead" else live(report)
    if args.out == "-":
        print(text)
    else:
        with open(args.out, "w") as f:
            f.write(text)
        print(f"report: wrote {args.out}")
    return 0

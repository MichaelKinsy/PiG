#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Aggregates a run directory (run_suite.py, ctx_scale.py, agent_scale.py output) into one Markdown matrix: arms as columns, every cell the
median over all samples with the range of the per-round medians, and "cannot run" where the arm has no way to run the scenario. PROVISIONAL.

  report.py --out DIR [--arms a,b,c] [--runs 5]       writes DIR/report.md and prints it
"""
import argparse
import json
import re
import statistics
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from run_suite import CANNOT as RUN_CANNOT  # noqa: E402 - one list of arms that cannot run a scenario, shared with the runner

CANNOT = {(arm, scen): why for arm, scens in RUN_CANNOT.items() for scen, why in scens.items()}


def cannot(arm, scen):
    """An A/A copy `<arm>-aa` cannot run what its arm cannot run (run_suite.runnable skips both)."""
    return (arm.removesuffix("-aa"), scen) in CANNOT


NOTES = """## Notes: outputs that differ between arms (from bolt-audit; compare PiG on equal outputs only)
1. Pi-Bolt throttles tool-call argument re-parsing: `toolcall_delta`/`message_update` partial arguments lag up to 12.5% above 1 KB (final arguments equal). The 200 KB write row is therefore different work for Pi-Bolt; PiG vs Pi (Node/Bun) is the like-for-like comparison there. Repro: stream a `write` call with `fake_model_stress.py SCENARIO write:200` under `--mode json` and diff `toolcall_delta` partials against Pi 1.1.0.
2. Pi-Bolt streamed Markdown with `$$` LaTeX blocks can lex differently from a full lex (68/1,500 fuzzed documents; minimal repro `" two\\n$$x$$\\n2text\\n===\\n"`). The 60,000-char answer in this suite contains no `$$`, so that row is equal work.
3. Pi-Bolt alt-screen frames use scroll regions (different bytes, same picture; `PI_TUI_SCROLL_ROWS=0` disables); this suite uses the native-scrollback TUI.
4. OpenCode's `read` tool takes `filePath`; Pi-Bolt's mock sends `path`, so OpenCode's reads fail through the unmodified suite (its suite rows are not the same work). gi: no print mode, stalls at prompt 47 of the long session. omp: partial Pi-extension loading.
"""


def load(p):
    return [json.loads(l) for l in open(p)] if Path(p).exists() else []


def med(v):
    v = [x for x in v if x is not None]
    return statistics.median(v) if v else None


def cell(per_round, fmt="{:.0f}"):
    """per_round: list of lists of samples (one list per round)."""
    allv = [x for r in per_round for x in r if x is not None]
    if not allv:
        return "-"
    meds = [statistics.median([x for x in r if x is not None]) for r in per_round if any(x is not None for x in r)]
    m = fmt.format(statistics.median(allv))
    if len(meds) > 1:
        return f"{m} ({fmt.format(min(meds))}-{fmt.format(max(meds))}, n={len(allv)})"
    return f"{m} (n={len(allv)})"


def lead(c):
    m = re.match(r"\s*(-?[\d.]+)", str(c))
    return float(m.group(1)) if m else None


def aa_delta(header, rows):
    """An A/A copy column `<arm>-aa` shows the run-to-run floor: its cell gets `[A/A d%]`, |arm - copy| as a percentage of the lower median."""
    rows = [list(r) for r in rows]
    for j, h in enumerate(header):
        if h.endswith("-aa") and h[:-3] in header:
            i = header.index(h[:-3])
            for r in rows:
                x, y = lead(r[i]), lead(r[j])
                if x is not None and y is not None and min(x, y) > 0:
                    r[j] = f"{r[j]} [A/A {abs(x - y) / min(x, y) * 100:.0f}%]"
    return rows


RELEASE_ARM = "pig-0.4.1"
NEUTRAL_ROWS = ("Frames per prompt", "KB written", "Conversation size", "Checks failed")  # no better/worse direction: the ratio only


def release_pair(header):
    """(release, tip) column names for the 0.4.1 -> 0.5.0 column: the pinned PiG 0.4.1 release and the first other PiG build (not an A/A copy) in the table."""
    if RELEASE_ARM not in header:
        return None
    tips = [h for h in header if h.startswith("pig-") and h != RELEASE_ARM and not h.endswith("-aa")]
    return (RELEASE_ARM, tips[0]) if tips else None


def half_unit(c):
    """Half a unit of the last digit a cell shows: the rounding error of its displayed median."""
    m = re.match(r"\s*-?\d*(?:\.(\d+))?", str(c))
    return 0.5 * 10 ** -len(m.group(1) or "")


def aa_floor(row, header, name):
    """|arm - copy| / the lower of the two displayed medians, unrounded (the `[A/A d%]` label rounds a 0.4% floor to 0%); None without a copy sample."""
    if name + "-aa" not in header:
        return None
    x, y = lead(row[header.index(name)]), lead(row[header.index(name + "-aa")])
    return abs(x - y) / min(x, y) if x is not None and y is not None and min(x, y) > 0 else None


def release_column(header, rows):
    """Adds `0.4.1 -> 0.5.0`: tip / release per row, WIN or LOSE when the ratio is outside the larger A/A floor of the two arms even at the extremes the displayed
    medians' rounding allows, `no measurable difference` inside it (or, when either arm has no A/A sample on the row, the ratio alone). Losses stay visible: a
    ratio above 1 on a lower-is-better row is LOSE."""
    pair = release_pair(header)
    if not pair:
        return header, rows
    i, j = header.index(pair[0]), header.index(pair[1])
    out = []
    for r in rows:
        a, b = lead(r[i]), lead(r[j])
        if a is None or b is None or a <= 0:
            out.append([*r, "-"])
            continue
        ratio = b / a
        floors = [aa_floor(r, header, pair[0]), aa_floor(r, header, pair[1])]
        # The extreme ratios the displayed (rounded) medians allow: a difference inside their rounding is not measured.
        ha, hb = half_unit(r[i]), half_unit(r[j])
        lo, hi = (b - hb) / (a + ha), ((b + hb) / (a - ha) if a > ha else float("inf"))
        if str(r[0]).startswith(NEUTRAL_ROWS):
            verdict = f"{ratio:.2f}x"
        elif None in floors:
            verdict = f"{ratio:.2f}x (no A/A)"
        elif lo > 1 + max(floors):
            verdict = f"{ratio:.2f}x LOSE"
        elif hi < 1 - max(floors):
            verdict = f"{ratio:.2f}x WIN"
        else:
            verdict = f"{ratio:.2f}x no measurable difference"
        out.append([*r, verdict])
    return [*header, f"{pair[0].removeprefix('pig-')} -> {pair[1]} (tip/release)"], out


def table(header, rows):
    rows = aa_delta(header, rows)
    header, rows = release_column(header, rows)
    out = ["| " + " | ".join(header) + " |", "|" + "---|" * len(header)]
    out += ["| " + " | ".join(str(c) for c in r) + " |" for r in rows]
    return "\n".join(out)


def by_round_bench(rows, runs=None):
    """benchmark.py numbers its runs 0..n-1 per invocation and run_suite.py makes one invocation per round: a new round starts when the run index resets."""
    prev = {}
    rnd = defaultdict(int)
    out = defaultdict(lambda: defaultdict(list))
    for r in rows:
        k = (r["build"], r["scenario"])
        if k in prev and r["run"] <= prev[k]:
            rnd[k] += 1
        prev[k] = r["run"]
        if r.get("ok"):
            out[k][rnd[k]].append(r)
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument("--arms")
    ap.add_argument("--runs", type=int, default=5)
    a = ap.parse_args()
    d = a.out
    rec = json.loads((d / "arms.json").read_text()) if (d / "arms.json").exists() else {}
    bench = load(d / "benchmark.jsonl")
    arms = a.arms.split(",") if a.arms else list(dict.fromkeys(r["build"] for r in bench)) or [k for k in rec if k not in ("suite", "bun")]
    md = ["# Pi-harness comparison (PROVISIONAL)", ""]
    md.append("PROVISIONAL: measured on a shared, loaded host; ratios between arms within one run are meaningful, absolute times are not comparable with other machines. "
              "Cells are `median (min-max of per-round medians, n=samples)`. A `-` means no sample; `cannot run` means the arm has no way to run it.")
    md.append("")
    md.append("## Machine and run")
    try:
        lscpu = subprocess.run(["lscpu"], capture_output=True, text=True).stdout
        model = re.search(r"Model name:\s*(.*)", lscpu).group(1)
    except Exception:  # noqa: BLE001
        model = "unknown"
    cm = load(d / "cpumon.jsonl")
    md.append(f"- CPU: {model}; kernel {subprocess.run(['uname', '-r'], capture_output=True, text=True).stdout.strip()}")
    if cm:
        md.append(f"- Pinned cores busy (all work, median/max of {len(cm)} samples): {statistics.median(c['pinned_busy'] for c in cm):.3f} / {max(c['pinned_busy'] for c in cm):.3f}; "
                  f"host load1 median {statistics.median(c['load1'] for c in cm):.0f}, host busy median {statistics.median(c['host_busy'] for c in cm):.2f}")
    md.append("")
    md.append("## Arms")
    md.append(table(["arm", "version", "artifact sha256 (lock)", "binary sha256", "source", "commit"],
                    [[k, v.get("version"), (v.get("sha256_artifact") or "")[:16], (v.get("binary_sha256") or "")[:16], v.get("source"), (v.get("commit") or "")[:12]] for k, v in rec.items() if k not in ("suite", "bun")]))
    md.append("")
    br = by_round_bench(bench)

    def bcol(arm, scen, key):
        if cannot(arm, scen):
            return "cannot run"
        rounds = [[r.get(key) for r in rs] for _, rs in sorted(br[(arm, scen)].items())]
        return cell(rounds)

    def section(title, rows, fmt="{:.0f}"):
        md.append(f"### {title}")
        md.append(table(["metric", *arms], [[name, *[fn(arm) for arm in arms]] for name, fn in rows]))
        md.append("")

    md.append("## Pi-Bolt suite (unmodified tools)")
    long_rows = defaultdict(list)
    for r in load(d / "long.jsonl"):
        if "error" not in r:
            long_rows[r["build"]].append(r)
    last = max((r["prompt"] for rs in long_rows.values() for r in rs), default=None)

    def long_rounds(arm, key):
        if cannot(arm, "long"):
            return "cannot run"
        rounds, cur, prev = [], [], 10**9
        for r in long_rows[arm]:
            if r["prompt"] <= prev and cur:
                rounds.append(cur)
                cur = []
            prev = r["prompt"]
            if r["prompt"] == last:
                cur.append(r.get(key))
        rounds.append(cur)
        return cell([c for c in rounds if c])

    tmux = defaultdict(list)
    for r in load(d / "tmux.jsonl"):
        tmux[r["build"]].append(r)

    def tmx(arm, fn, fmt="{:.0f}"):
        return cell([[fn(r)] for r in tmux[arm]], fmt) if tmux[arm] else "-"

    section("Time (ms)", [
        ("Launch to interactive (TUI)", lambda x: bcol(x, "interactive", "tti_ms")),
        ("pi --version", lambda x: bcol(x, "startup", "wall_ms")),
        ("pi -p: one prompt, 4 tool calls", lambda x: bcol(x, "headless", "wall_ms")),
        ("5 prompts, interactive (sum of turns)", lambda x: bcol(x, "interactive", "turns_ms")),
        (f"Time per prompt, long session (prompt {last})", lambda x: long_rounds(x, "ms_per_prompt")),
    ])
    section("CPU (ms, all threads)", [
        ("Interactive session: 5 prompts", lambda x: bcol(x, "interactive", "cpu_ms")),
        ("pi -p: one prompt", lambda x: bcol(x, "headless", "cpu_ms")),
        ("pi --version", lambda x: bcol(x, "startup", "cpu_ms")),
        (f"Per prompt, long session (prompt {last})", lambda x: long_rounds(x, "cpu_ms_per_prompt")),
        ("Streaming replies (tmux), CPU per prompt", lambda x: tmx(x, lambda r: r.get("prompt_cpu_ms"))),
    ])
    section("Memory (MB)", [
        ("Peak memory (maxrss), interactive 5 prompts", lambda x: bcol(x, "interactive", "peak_mb")),
        ("Own (private dirty) memory, tmux session at end", lambda x: tmx(x, lambda r: r.get("memory_at_end", {}).get("own"))),
        ("RSS, tmux session at end", lambda x: tmx(x, lambda r: r.get("memory_at_end", {}).get("rss"))),
        (f"Own memory, end of long session ({last} prompts)", lambda x: long_rounds(x, "own_mb")),
        (f"RSS, end of long session", lambda x: long_rounds(x, "rss_mb")),
        ("Conversation size at end (M tokens, request bytes/4)", lambda x: long_rounds(x, "tokens_m")),
    ])
    section("tmux session (human-paced model)", [
        ("Keystroke latency median (ms)", lambda x: tmx(x, lambda r: r.get("key_ms_median"), "{:.1f}")),
        ("Keystroke latency p95 (ms)", lambda x: tmx(x, lambda r: r.get("key_ms_p95"), "{:.1f}")),
        ("20 KB paste (ms)", lambda x: tmx(x, lambda r: r.get("paste_ms"))),
        ("Prompt wall while streaming (ms)", lambda x: tmx(x, lambda r: r.get("prompt_wall_ms"))),
        ("Frames per prompt", lambda x: tmx(x, lambda r: r.get("prompt_frames"))),
        ("KB written to terminal per prompt", lambda x: tmx(x, lambda r: r.get("prompt_kb_written"), "{:.0f}")),
        ("Idle CPU (ms/s; negative readings excluded)", lambda x: tmx(x, lambda r: r.get("idle_cpu_ms_per_s") if (r.get("idle_cpu_ms_per_s") or 0) >= 0 else None, "{:.2f}")),
        ("Idle CPU negative (broken) readings, rounds", lambda x: sum(1 for r in tmux[x] if (r.get("idle_cpu_ms_per_s") or 0) < 0) if tmux[x] else "-"),
        ("Checks failed (resize/escape/abort/quit/errors), rounds", lambda x: sum(1 for r in tmux[x] if r.get("resize_while_streaming") != "ok" or r.get("escape_aborts") != "ok" or r.get("quit") != "ok" or r.get("errors_on_screen"))
         if tmux[x] else "-"),
    ])
    rows = defaultdict(lambda: defaultdict(list))
    for kind in ("long_answer", "large_write"):
        for f in sorted(d.glob(f"{kind}-r*.txt")):
            rn = int(re.search(r"-r(\d+)", f.name).group(1))
            for line in f.read_text().splitlines()[1:]:
                p = line.split()
                if len(p) >= 4 and p[0] in arms:
                    if kind == "long_answer":
                        rows[(kind, p[0], p[1], "cpu")][rn].append(float(p[3]))
                        rows[(kind, p[0], p[1], "wall")][rn].append(float(p[2]))
                    elif "FAILED" not in line:
                        rows[(kind, p[0], p[1], "wall")][rn].append(float(p[3]))
                        rows[(kind, p[0], p[1], "cpu")][rn].append(float(p[4]))

    def tcell(kind, arm, size, key, fmt="{:.2f}"):
        if cannot(arm, kind):
            return "cannot run"
        v = rows.get((kind, arm, size, key))
        return cell([v[k] for k in sorted(v)], fmt) if v else "-"

    section("Streaming and large writes (s)", [
        ("CPU, 5,000-char answer", lambda x: tcell("long_answer", x, "5000", "cpu")),
        ("CPU, 20,000-char answer", lambda x: tcell("long_answer", x, "20000", "cpu")),
        ("CPU, 60,000-char answer", lambda x: tcell("long_answer", x, "60000", "cpu")),
        ("Wall, 60,000-char answer", lambda x: tcell("long_answer", x, "60000", "wall")),
        ("Write 50 KB through a tool call (wall)", lambda x: tcell("large_write", x, "50", "wall")),
        ("Write 200 KB through a tool call (wall)", lambda x: tcell("large_write", x, "200", "wall")),
        ("Write 200 KB through a tool call (CPU)", lambda x: tcell("large_write", x, "200", "cpu")),
    ])
    idle = defaultdict(list)
    for f in sorted(d.glob("idle-r*.txt")):
        for line in f.read_text().splitlines():
            m = re.match(r"\{'build': '([^']+)', 'cpu_ms_per_s': (-?[\d.]+), 'own_mb': \[([^,]+), ([^\]]+)\]", line)
            if m:
                own = m.group(4).strip()
                idle[m.group(1)].append((float(m.group(2)), None if own == "None" else float(own)))
    def idle_cpu(x):
        # idle.py subtracts two CPU readings of the process tree; a child that exits in between takes its CPU with it, so a negative delta is a broken
        # reading, never a measurement: it is counted and shown, not pooled.
        if not idle[x]:
            return "-"
        good = [[v[0]] for v in idle[x] if v[0] >= 0]
        bad = len(idle[x]) - len(good)
        text = cell(good, "{:.2f}") if good else "invalid"
        return f"{text}; {bad} negative reading(s) excluded" if bad else text

    if idle:
        section("Idle at the prompt after one answer (30 s)", [
            ("Idle CPU (ms/s)", idle_cpu),
            ("Own memory (MB)", lambda x: cell([[v[1]] for v in idle[x]]) if idle[x] else "-"),
        ])
    ctx = load(d / "ctx.jsonl")
    if ctx:
        md.append("## Context scale (window declared 5M; conversation grown with the scripted task)")
        stages = ["ready", "at100k", "at500k", "at850k", "at1000k", "after5@1M", "at2000k", "at4000k"]
        for metric in ("pss", "rss", "private_dirty"):
            md.append(f"### {metric.upper()} (MB)")
            md.append(table(["stage", *arms], [[s, *[cell([[r.get(metric)] for r in ctx if r["arm"] == x and r["stage"] == s], "{:.0f}") or "-" if any(r["arm"] == x and r["stage"] == s for r in ctx) else
                                                     ("cannot run" if any(r["arm"] == x and r["stage"] in ("stalled", "error") for r in ctx) else "-") for x in arms]] for s in stages]))
            md.append("")
        md.append("### Turn latency p50/p95 (ms) and CPU per prompt (ms) since the previous stage")
        md.append(table(["stage", *arms], [[s, *[("{}/{} ms, {} cpu".format(med([r.get("p50_ms") for r in ctx if r["arm"] == x and r["stage"] == s]), med([r.get("p95_ms") for r in ctx if r["arm"] == x and r["stage"] == s]),
                                                                           med([r.get("cpu_ms_per_prompt") for r in ctx if r["arm"] == x and r["stage"] == s])) if any(r["arm"] == x and r["stage"] == s for r in ctx) else "-") for x in arms]] for s in stages[1:]]))
        bad = [(r["arm"], r["stage"], r.get("tokens"), r.get("error")) for r in ctx if r["stage"] in ("stalled", "error")]
        if bad:
            md.append("\nStopped early: " + "; ".join(map(str, bad)))
        md.append("")
    sc = load(d / "scale.jsonl")
    if sc:
        md.append("## Agent scale (concurrent interactive sessions on one host)")
        ns = sorted({r["n"] for r in sc})
        md.append(table(["arm", "N", "up", "PSS MB/session", "RSS MB/session", "private dirty MB/session", "total CPU s", "CPU s/session", "lat p50/p95/max s", "failures", "note"],
                        [[r["arm"], r["n"], r.get("up"), r.get("pss_mb_per_session"), r.get("rss_mb_per_session"),
                          round(r["private_dirty"] / max(1, r["up"]), 1) if "private_dirty" in r else "-", r.get("cpu_s"), r.get("cpu_s_per_session"),
                          f"{r.get('lat_p50_s')}/{r.get('lat_p95_s')}/{r.get('lat_max_s')}", r.get("prompt_failures", r.get("failed_ready")), r.get("reason", r.get("error", ""))]
                         for r in sc if r["stage"].startswith(("after", "not-run", "error"))]))
        md.append("")
    ext = load(d / "ext.jsonl")
    if ext:
        md.append("## Extension profiles (5 interactive prompts, 4 tool calls each; a `tool_call` handler doing ~2 ms of work per call proves the extension ran)")
        md.append("Rounds -1 (cold build/compile) excluded from medians; `cannot load` rows are the arm's actual outcome.\n")
        profs = list(dict.fromkeys(r["profile"] for r in ext))
        rows = []
        for p in profs:
            for x in arms:
                rs = [r for r in ext if r["profile"] == p and r["arm"] == x and r["round"] >= 0]
                if not rs:
                    continue
                okr = [r for r in rs if r.get("ok")]
                if not okr:
                    rows.append([p, x, "cannot load", "", "", "", "", rs[0].get("reason", "")[:120]])
                    continue
                m = lambda k, f="{:.0f}": cell([[r.get(k) for r in okr]], f)
                note = "" if len(okr) == len(rs) else f"{len(rs) - len(okr)} of {len(rs)} rounds failed: {next(r for r in rs if not r.get('ok')).get('reason', '')[:80]}"
                rows.append([p, x, len(okr), m("tti_ms"), m("p50_ms"), m("cpu_ms_per_prompt"), m("after_pss"), note])
        md.append(table(["profile", "arm", "rounds ok", "TTI ms", "p50 prompt ms", "CPU ms/prompt", "PSS MB after 5 prompts", "note"], rows))
        md.append("")
    failed = [r for r in load(d / "status.jsonl") if r.get("rc") != 0]
    if failed:
        md.append("## Tool invocations that did not exit 0 (their rows hold fewer samples or none)")
        md.append(table(["tool", "output", "exit", "wall s"], [[r["tool"], r["out"], r["rc"], r.get("wall_s")] for r in failed]))
        md.append("")
    tc = d / "tokcost.md"
    if tc.exists():
        md.append(tc.read_text())
        md.append("")
    md.append(NOTES)
    text = "\n".join(md)
    (d / "report.md").write_text(text)
    print(text)


if __name__ == "__main__":
    main()

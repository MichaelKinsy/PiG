# SPDX-License-Identifier: MIT
"""Markdown, CSV and raw JSON for `harbor-analyze`: task-level paired statistics on finished Harbor jobs."""
import csv
import json
import os

from . import arms as armlib
from . import envinfo, harbor
from . import report as R


def pct(x):
    return "n/a" if x is None else f"{100 * x:.1f}%"


def pp(x):
    return "n/a" if x is None else f"{100 * x:+.1f} pp"


def ci(pair, f=pp):
    return "n/a" if not pair or pair[0] != pair[0] else f"[{f(pair[0])}, {f(pair[1])}]"


def render(analysis, env, rows_n, jobs):
    md = ["# Task-completion bench (Harbor)", "", f"Jobs: {', '.join(os.path.basename(j.rstrip('/')) for j in jobs)}. Trials read: {rows_n}.", "", R.env_block(env), ""]
    md += ["## Method", "", "Arms are paired by task. A task's score for an arm is the mean over its attempts; the unit of resampling is the task, never the trial. Mean differences have a 95% task-level bootstrap interval "
           f"and a two-sided sign-flip test (seed {analysis['seed']}). Parity is tested by two one-sided tests (TOST) with the declared margin of {100 * analysis['margin']:.1f} pp: an arm is "
           "equivalent when the 90% interval of the paired difference lies inside the margin. Cost, time and tokens are compared as a ratio of totals with a task-level bootstrap interval, and as the geometric mean of per-task ratios. "
           "p-values for the pass-rate difference are adjusted across arms with Holm. Infrastructure errors (a trial that raised and has no verifier result) are counted per arm and left out of the pass rates. "
           "A parity claim means parity on the test-passing rate, not on the quality of the patches.", ""]
    md += [f"## Arms (reference: {analysis['reference']})", "", "| arm | trials | infrastructure errors | rate [95% Wilson] | scored | pass rate |", "|---|---|---|---|---|---|"]
    for a, v in analysis["arms"].items():
        md.append(f"| {a} | {v['trials']} | {v['infra_errors']} | {pct(v['infra_rate'])} {ci(v['infra_ci'], pct)} | {v['scored']} | {pct(v['pass_rate'])} |")
    md += ["", "## Paired against the reference", "", f"| arm | tasks | pass-rate difference | 95% CI | sign-flip p | Holm p | equivalent within ±{100 * analysis['margin']:.0f} pp (TOST p) | cost ratio of totals [CI] | time ratio of totals [CI] |", "|---|---|---|---|---|---|---|---|---|"]
    for a, v in analysis["arms"].items():
        e = v.get("vs_reference")
        if not e:
            continue
        t = e.get("tost") or {}
        def ratio(k, e=e):
            r = e.get(k) or {}
            return "n/a" if not r.get("n") else f"{R.fmt(r['ratio_of_totals'])} {ci(r.get('ci'), R.fmt)}"
        md.append(f"| {a} | {e['tasks']} | {pp(e.get('mean_diff'))} | {ci(e.get('ci'))} | {R.fmt(e.get('sign_flip_p'))} | {R.fmt(e.get('holm_p'))} | "
                  f"{'yes' if t.get('equivalent') else 'no'} (p={R.fmt(t.get('p'))}) | {ratio('ratio_cost_usd')} | {ratio('ratio_seconds')} |")
    md.append("")
    if analysis.get("aa"):
        aa = analysis["aa"]
        md += ["## A/A noise floor (the same harness against itself)", "", f"Mean difference {pp(aa.get('mean'))}, 95% interval {ci(aa.get('ci'))} over {aa['n']} tasks"
               + (f"; difference standard deviation {pp(aa.get('sd'))}; smallest detectable paired difference at 80% power {pp(aa.get('mde'))}." if aa.get("sd") is not None else "."),
               "", "A difference between two arms smaller than this floor is not distinguishable from run-to-run noise.", ""]
    else:
        md += ["## A/A noise floor", "", "No A/A pair was given (`--aa A B`). Run the Pi-vs-Pi A/A job and pass its two labels.", ""]
    return "\n".join(md)


def main(args):
    rows = harbor.load_rows(args.jobs)
    if not rows:
        raise SystemExit("no trial result.json found under the given job directories")
    analysis = harbor.analyze(rows, args.reference, args.margin, args.seed, tuple(args.aa) if args.aa else None)
    out = args.out or os.path.join(armlib.REPO, "tmp", "bench", "harbor-analysis")
    os.makedirs(out, exist_ok=True)
    env = envinfo.collect([], seed=args.seed)
    env["harbor"] = harbor.HARBOR_VERSION
    with open(os.path.join(out, "analysis.json"), "w") as f:
        json.dump({"env": env, "analysis": analysis, "trials": rows}, f, indent=1, default=list)
    with open(os.path.join(out, "report.md"), "w") as f:
        f.write(render(analysis, env, len(rows), args.jobs) + f"\n\n---\nRegenerate: `python3 -m pigbench harbor-analyze {' '.join(args.jobs)} --reference {args.reference} --margin {args.margin} --seed {args.seed}`; arms.lock sha256 `{env['arms_lock_sha256']}`.\n")
    with open(os.path.join(out, "summary.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["arm", "trials", "infra_errors", "pass_rate", "mean_diff", "ci_lo", "ci_hi", "sign_flip_p", "holm_p"])
        for a, v in analysis["arms"].items():
            e = v.get("vs_reference") or {}
            lo, hi = (e.get("ci") or (None, None))
            w.writerow([a, v["trials"], v["infra_errors"], v["pass_rate"], e.get("mean_diff"), lo, hi, e.get("sign_flip_p"), e.get("holm_p")])
    print(f"harbor-analyze: {os.path.join(out, 'report.md')}")
    return 0

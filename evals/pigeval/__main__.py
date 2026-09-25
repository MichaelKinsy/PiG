# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Command line for pigeval. See `make help` (group Evals) for the usual invocations."""
import argparse
import os
import sys

from . import capture, live, mutate, overhead, perf, profile, registry, report


def main(argv=None):
    parser = argparse.ArgumentParser(prog="pigeval", description="Measure coding-agent harnesses against a deterministic local model.")
    parser.add_argument("--registry", default=registry.DEFAULT_REGISTRY, help="harness registry (default: evals/harnesses.toml)")
    sub = parser.add_subparsers(dest="command", required=True)
    tmp = os.path.join(registry.REPO_ROOT, "tmp", "evals")

    def harness_options(p, default="pig,pi"):
        p.add_argument("--harnesses", default=default, help=f"comma-separated ids or all (default: {default})")
        p.add_argument("--pig", help="pig binary (default: bin/pig)")
        p.add_argument("--timeout", type=float, default=120, help="seconds before a run is killed")

    p = sub.add_parser("overhead", help="startup, round-trip, and resumed-Session overhead against the mock model")
    harness_options(p)
    p.add_argument("--runs", type=int, default=10)
    p.add_argument("--warmup", type=int, default=1)
    p.add_argument("--prompt", default="Say hello")
    p.add_argument("--session-exchanges", type=int, default=2000)
    p.add_argument("--out", default=os.path.join(tmp, "overhead.json"))
    p.set_defaults(func=overhead.run)

    p = sub.add_parser("requests", help="save the request bodies each harness sends; --diff compares the first two")
    harness_options(p)
    p.add_argument("--prompt", default="Say hello")
    p.add_argument("--out", default=os.path.join(tmp, "requests"))
    p.add_argument("--diff", action="store_true")
    p.set_defaults(func=capture.run)

    p = sub.add_parser("live", help="run evals/tasks with a real model and check each result")
    harness_options(p)
    p.add_argument("--model", required=True, help="provider/model, for example anthropic/claude-sonnet-5")
    p.add_argument("--tasks", default="all")
    p.add_argument("--tasks-dir", help="task directory (default: evals/tasks); pigeval mutate writes one")
    p.add_argument("--runs", type=int, default=1)
    p.add_argument("--real-home", action="store_true", help="use your HOME so subscription logins work (default: isolated HOME, API keys from the environment)")
    p.add_argument("--out", default=os.path.join(tmp, "live.json"))
    p.set_defaults(func=live.run, timeout=0)

    p = sub.add_parser("mutate", help="generate seeded bug-fix tasks from real Go files (oh-my-pi's edit benchmark method)")
    p.add_argument("--count", type=int, default=30)
    p.add_argument("--seed", type=int, default=1)
    p.add_argument("--roots", default="agent,ai,tui,coding", help="comma-separated corpus directories")
    p.add_argument("--out", default=os.path.join(tmp, "mutation-tasks"))
    p.set_defaults(func=mutate.run)

    p = sub.add_parser("report", help="render a results JSON file as Markdown")
    p.add_argument("results")
    p.add_argument("--out", default="-")
    p.set_defaults(func=report.run)

    p = sub.add_parser("check", help="apply evals/budgets.toml to an overhead results file")
    p.add_argument("results", nargs="?", default=os.path.join(tmp, "overhead.json"))
    p.add_argument("--budgets", default=os.path.join(registry.EVALS_DIR, "budgets.toml"))
    p.set_defaults(func=perf.check)

    p = sub.add_parser("benchcmp", help="compare two `go test -bench` outputs by median")
    p.add_argument("old")
    p.add_argument("new")
    p.add_argument("--threshold", type=float, default=5.0, help="percent increase that counts as a regression")
    p.add_argument("--fail", action="store_true", help="exit 1 when a regression is found")
    p.set_defaults(func=perf.benchcmp)

    p = sub.add_parser("profile", help="profile pig round trips with PIG_PROFILE and summarize them")
    p.add_argument("--pig", help="pig binary (default: bin/pig)")
    p.add_argument("--kinds", default="cpu,heap", help="PIG_PROFILE kinds")
    p.add_argument("--runs", type=int, default=1)
    p.add_argument("--prompt", default="Say hello")
    p.add_argument("--timeout", type=float, default=120)
    p.add_argument("--out", default=os.path.join(tmp, "profile"))
    p.add_argument("--merge", help="merge the CPU profiles into this file (for PGO)")
    p.set_defaults(func=profile.run)

    p = sub.add_parser("install", help="install pinned harness packages (make setup calls this)")
    p.add_argument("--harnesses", default="all")
    p.add_argument("--prefix", default=os.path.join(registry.dev_home(), "harnesses"))
    p.add_argument("--check", action="store_true")
    p.set_defaults(func=lambda a: 0 if registry.install(registry.select(registry.load(a.registry), a.harnesses), a.prefix, a.check) else 1)

    p = sub.add_parser("list", help="list registered harnesses and whether each is installed")
    p.add_argument("--pig")
    p.set_defaults(func=lambda a: [print(f"{h.id:9} {h.name:20} {h.api:20} {registry.resolve(h, a.pig) or 'not installed'}") for h in registry.load(a.registry).values()] and 0)

    args = parser.parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    sys.exit(main())

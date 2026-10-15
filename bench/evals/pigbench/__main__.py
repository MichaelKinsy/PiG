# SPDX-License-Identifier: MIT
"""pigbench command line: `python3 -m pigbench <bench> [options]` from the bench/ directory (the Makefile does this)."""
import argparse
import json
import time
import os
import sys

from . import arms as armlib

DEFAULT_SEED = 20261009


def common(p):
    p.add_argument("--arms", nargs="*", default=["all"], help="arm ids (default: all that are installed)")
    p.add_argument("--out", help="output directory (default tmp/bench/<bench>)")
    p.add_argument("--seed", type=int, default=DEFAULT_SEED, help="seed for every random choice and bootstrap (recorded in the report)")
    p.add_argument("--timeout", type=float, default=120, help="per-turn / per-process timeout, seconds")


def main(argv=None):
    ap = argparse.ArgumentParser(prog="pigbench")
    sub = ap.add_subparsers(dest="cmd", required=True)

    p = sub.add_parser("arms", help="install every arm into a throwaway directory, check its pinned checksum and version, then delete it")
    p.add_argument("--arms", nargs="*", default=["all"])

    p = sub.add_parser("lock", help="verify or bump arms.lock")
    p.add_argument("action", choices=["verify", "bump", "pin-binaries", "show"])
    p.add_argument("arm", nargs="?")
    p.add_argument("version", nargs="?")
    p.add_argument("--online", action="store_true", help="verify: re-download each primary tarball and compare sha256")

    p = sub.add_parser("overhead", help="harness overhead against a scripted model")
    common(p)
    p.add_argument("--scenarios", nargs="*", help="subset of overhead scenarios")
    p.add_argument("--runs", type=int, default=10)
    p.add_argument("--warmup", type=int, default=2)
    p.add_argument("--rpc-turns", type=int, default=20)
    p.add_argument("--long-turns", type=int, default=1000)
    p.add_argument("--long-runs", type=int, default=1)
    p.add_argument("--aa", nargs="*", default=[], help="arm ids to run twice (the copy is `<id>-b`, same binary, its own home): the A/A noise floor")

    p = sub.add_parser("variants", help="build variants as arms (PGO, GOAMD64, stripped Piglets, GOGC): the overhead scenarios on id=path executables, each with an A/A copy")
    p.add_argument("--out", help="output directory (default tmp/bench/variants)")
    p.add_argument("--seed", type=int, default=DEFAULT_SEED, help="seed for every random choice and bootstrap (recorded in the report)")
    p.add_argument("--timeout", type=float, default=120, help="per-turn / per-process timeout, seconds")
    p.add_argument("--arm", action="append", required=True, metavar="ID=PATH[,noext][,own][,K=V...]", help="an executable to measure; repeat for each variant")
    p.add_argument("--env", action="append", default=[], metavar="K=V", help="an environment variable for every arm")
    p.add_argument("--scenarios", nargs="*", help="subset of overhead scenarios")
    p.add_argument("--runs", type=int, default=12)
    p.add_argument("--warmup", type=int, default=2)
    p.add_argument("--rpc-turns", type=int, default=20)
    p.add_argument("--long-turns", type=int, default=1000)
    p.add_argument("--long-runs", type=int, default=1)

    p = sub.add_parser("startup", help="`--version` of every arm under hyperfine 2.0 (--shell=none): wall, CPU, peak resident memory, instruction counters where allowed")
    p.add_argument("--arms", nargs="*", default=["all"])
    p.add_argument("--rounds", type=int, default=4, help="rounds; each runs every arm once through hyperfine, the order rotating")
    p.add_argument("--runs", type=int, default=5, help="hyperfine runs per arm per round (rounds x runs = samples per arm)")
    p.add_argument("--warmup", type=int, default=3)
    p.add_argument("--seed", type=int, default=DEFAULT_SEED)
    p.add_argument("--out", default="tmp/bench/startup")
    p = sub.add_parser("aggregate", help="one report from the raw.json files of several machines: per-architecture tables and a cross-architecture summary")
    p.add_argument("inputs", nargs="+", help="directories holding raw.json, or raw.json files (overhead and startup runs)")
    p.add_argument("--out", default="tmp/bench/aggregate")
    p.add_argument("--seed", type=int, default=DEFAULT_SEED)
    p.add_argument("--allow-mixed", action="store_true", help="accept runs from different arms.lock files or commits and say so in the report")
    p = sub.add_parser("soak", help="steady scripted turns for hours; sample processes, threads, descriptors and memory each interval and judge them flat")
    p.add_argument("--arms", nargs="*", default=["pig-0.5", "pi-1.1.0"])
    p.add_argument("--hours", type=float, default=24)
    p.add_argument("--minutes", type=float, help="override --hours for a short run")
    p.add_argument("--interval", type=float, default=60, help="seconds between samples")
    p.add_argument("--think-ms", type=int, default=500, help="pause between turns")
    p.add_argument("--aa", nargs="*", default=[], help="arm ids to run twice (copy `<id>-b`): the A/A noise floor")
    p.add_argument("--timeout", type=float, default=120)
    p.add_argument("--seed", type=int, default=DEFAULT_SEED)
    p.add_argument("--out")
    p = sub.add_parser("longhistory", help="sessions with ~1M tokens of history and ~3M across 3 compactions")
    common(p)
    p.add_argument("--cases", nargs="*", default=["1m", "3m"])
    p.add_argument("--phases", nargs="*", default=["rpc", "tui", "resume-tui"])
    p.add_argument("--modes", nargs="*", default=["fullscreen", "regular"], help="tuiMode setting for the TUI phases")
    p.add_argument("--runs", type=int, default=3)
    p.add_argument("--history-timeout", type=float, default=300)

    p = sub.add_parser("reliability", help="faults injected by the mock endpoint and the host, scored against Pi")
    common(p)
    p.add_argument("--faults", nargs="*")
    p.add_argument("--reps", type=int, default=3)

    p = sub.add_parser("durable", help="the durable-bench workload on PiG Durable and pi-durable")
    common(p)
    p.add_argument("--sizes", nargs="*")
    p.add_argument("--rounds", "--samples", dest="rounds", type=int, help="rounds per size (default 3, durable-bench's own; 12 gives the 100 warm turns per cell durable-report requires)")
    p.add_argument("--cpus", help="pin each sample with taskset -c, and gate on those CPUs")
    p.add_argument("--no-gate", action="store_true")

    p = sub.add_parser("harbor-plan", help="write and validate Harbor job configs (prepared, never run)")
    p.add_argument("--claude-model", default="<claude-snapshot>")
    p.add_argument("--gpt-model", default="<gpt-snapshot>")
    p.add_argument("--pig-binary")
    p.add_argument("--out")

    p = sub.add_parser("harbor-compat", help="check Harbor's Pi converter against PiG and Pi, no model")
    common(p)

    p = sub.add_parser("harbor-analyze", help="paired analysis of finished Harbor job directories")
    p.add_argument("jobs", nargs="+")
    p.add_argument("--reference", default="pi-1.1.0")
    p.add_argument("--margin", type=float, default=0.05, help="declared equivalence margin on the pass rate (TOST)")
    p.add_argument("--aa", nargs=2, metavar=("A", "B"))
    p.add_argument("--seed", type=int, default=DEFAULT_SEED)
    p.add_argument("--out")

    from . import k8s
    k8s.add_parser(sub)

    p = sub.add_parser("report", help="render report.md and CSV from a raw.json")
    p.add_argument("dir")

    args = ap.parse_args(argv)
    if args.cmd == "arms":
        selected = armlib.select(armlib.load(), args.arms)
        before = armlib.outside_snapshot()
        records = {}
        with armlib.Workspace() as ws:
            ws.install_all(selected)
            for a in selected:
                print(f"{a.id:10} {getattr(a, 'version_line', None) or a.skip}")
                records[a.id] = dict(a.record, requested_version=a.version, observed_version=getattr(a, "version_line", None), skipped=a.skip,
                                     executable_sha256=armlib.lock.sha256_file(os.path.realpath(a.binary)) if a.binary else None,
                                     installed_utc=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
        created, modified = armlib.outside_changes(before, armlib.outside_snapshot())
        leftovers = [a.install_dir for a in selected if a.install_dir and os.path.exists(a.install_dir)]
        out = {"lock_sha256": armlib.lock.lock_hash(), "arms": records, "outside_created": created, "outside_modified": modified, "throwaway_left_behind": leftovers}
        print(json.dumps(out, indent=1))
        return 1 if created or leftovers else 0
    if args.cmd == "lock":
        from . import lock
        if args.action == "verify":
            problems = lock.verify(online=args.online)
            for p in problems:
                print("lock:", p)
            print(f"lock: {len(lock.read()['arms'])} arms, sha256 {lock.lock_hash()}", "OK" if not problems else "FAILED")
            return 1 if problems else 0
        if args.action == "bump":
            if not (args.arm and args.version):
                ap.error("lock bump needs an arm id and a version")
            entry = lock.bump(args.arm, args.version)
            print(f"lock: {args.arm} pinned to {entry['version']}")
            if entry["source"] == "npm":
                armlib.pin_binaries(args.arm)
            print("lock: review the diff of bench/evals/arms.lock and its package-lock file")
            return 0
        if args.action == "pin-binaries":
            return armlib.pin_binaries(args.arm)
        print(open(lock.LOCK).read())
        return 0
    if args.cmd == "k8s":
        return k8s.main(args)
    if args.cmd == "overhead":
        from . import overhead, report
        args.out = args.out or os.path.join(armlib.REPO, "tmp", "bench", "overhead")
        result = overhead.run(args)
        report.render(args.out)
        return 0 if result["ok"] else 1
    if args.cmd == "variants":
        from . import variants
        args.out = os.path.abspath(args.out or os.path.join(armlib.REPO, "tmp", "bench", "variants"))
        return 0 if variants.run(args)["ok"] else 1
    if args.cmd == "aggregate":
        from . import aggregate
        print("report:", aggregate.aggregate(args.inputs, os.path.abspath(args.out), args.seed, args.allow_mixed))
        return 0
    if args.cmd == "startup":
        from . import report, startup
        args.out = os.path.abspath(args.out)
        result = startup.run(args)
        report.render(args.out)
        return 0 if result["ok"] else 1
    if args.cmd == "soak":
        from . import report, soak
        args.out = os.path.abspath(args.out or os.path.join(armlib.REPO, "tmp", "bench", "soak"))
        result = soak.run(args)
        report.render(args.out)
        return 0 if result["ok"] else 1
    if args.cmd == "longhistory":
        from . import longhistory, report
        args.out = args.out or os.path.join(armlib.REPO, "tmp", "bench", "longhistory")
        result = longhistory.run(args)
        report.render(args.out)
        return 0 if result["ok"] else 1
    if args.cmd == "reliability":
        from . import reliability, report
        args.out = args.out or os.path.join(armlib.REPO, "tmp", "bench", "reliability")
        result = reliability.run(args)
        report.render(args.out)
        return 0 if result["ok"] else 1
    if args.cmd == "durable":
        from . import durable, report
        args.out = args.out or os.path.join(armlib.REPO, "tmp", "bench", "durable")
        result = durable.run(args)
        report.render(args.out)
        return 0 if result["ok"] else 1
    if args.cmd == "harbor-plan":
        from . import harbor
        args.out = args.out or os.path.join(armlib.REPO, "tmp", "bench", "harbor", "jobs")
        paths = harbor.plan(args.out, args.claude_model, args.gpt_model, args.pig_binary)
        ok, message = harbor.validate(paths)
        print(message)
        print(f"harbor: wrote {len(paths)} job configs to {args.out}. Not run: a job spends money and needs an explicit decision to spend it. Command: harbor run -c <config>")
        return 0 if ok in (True, None) else 1
    if args.cmd == "harbor-compat":
        from . import harbor
        args.out = args.out or os.path.join(armlib.REPO, "tmp", "bench", "harbor")
        result = harbor.compat(args)
        for p in result["problems"]:
            print("harbor-compat:", p)
        print("harbor-compat:", "OK" if result["ok"] else "FAILED")
        return 0 if result["ok"] else 1
    if args.cmd == "harbor-analyze":
        from . import harbor_report
        return harbor_report.main(args)
    if args.cmd == "report":
        from . import report
        report.render(args.dir)
        return 0
    return 2


if __name__ == "__main__":
    sys.exit(main())

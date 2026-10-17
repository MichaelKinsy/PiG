# SPDX-License-Identifier: MIT
"""Build variants as arms: measure arbitrary PiG executables (a PGO build, a GOAMD64 build, a stripped Piglet Binary, the same binary under another GOGC) with the overhead bench's scenarios.

Every variant runs beside an A/A copy of itself (the same binary and settings in its own home), so each number carries its own noise floor. `--arm id=path[,flag...]`
takes `noext` (the build cannot load extensions), `own` (the build carries its own extensions, so the extension scenarios install no TypeScript fixtures) and `K=V`
(an environment variable for this arm; the harness drops inherited `PIG_` variables, so a `PIG_` setting belongs here). Arms are unpinned: the report says so.
"""
import json
import os
import statistics

from . import arms as armlib
from . import overhead


def parse_arm(text, shared_env=None):
    """`id=path[,noext][,own][,K=V...]` -> an unpinned pig-family Arm that runs the executable at `path`."""
    ident, sep, rest = text.partition("=")
    if not sep or not ident or not rest:
        raise SystemExit(f"--arm wants id=path[,flag...], got {text!r}")
    path, *flags = rest.split(",")
    env, spec = dict(shared_env or {}), {"name": ident, "family": "pig", "binary": os.path.abspath(path)}
    for flag in flags:
        if flag == "noext":
            spec["no_extensions"] = True
        elif flag == "own":
            spec["own_extensions"] = True
        elif "=" in flag:
            key, value = flag.split("=", 1)
            env[key] = value
        else:
            raise SystemExit(f"--arm {ident}: unknown flag {flag!r} (noext, own or K=V)")
    if env:
        spec["env"] = env
    return armlib.Arm(ident, spec, {"source": "binary", "version": "variant"})


def parse_env(items):
    pairs = [item.partition("=") for item in items or ()]
    for key, sep, _ in pairs:
        if not sep or not key:
            raise SystemExit(f"--env wants K=V, got {key!r}")
    return {key: value for key, _, value in pairs}


def summarize(raw):
    """Markdown: per scenario and metric, each variant's median beside its A/A copy's, and the A/A difference as the noise floor."""
    lines = []
    for scenario, per_arm in raw["samples"].items():
        ids = [i for i in per_arm if not i.endswith("-b")]
        metrics = sorted({k for rows in per_arm.values() for row in rows for k, v in row.items()
                          if isinstance(v, (int, float)) and not isinstance(v, bool) and not k.startswith("_")})
        lines += [f"### {scenario}", "", "| metric | " + " | ".join(f"{i} | {i} A/A | A/A diff" for i in ids) + " |", "|---|" + "---|---|---|" * len(ids)]
        for metric in metrics:
            cells = []
            for ident in ids:
                a, b = (median(per_arm.get(j, []), metric) for j in (ident, ident + "-b"))
                cells += [fmt(a), fmt(b), "-" if a is None or b is None or a == 0 else f"{(b - a) / a * 100:+.1f}%"]
            lines.append(f"| {metric} | " + " | ".join(cells) + " |")
        lines.append("")
    return "\n".join(lines)


def median(rows, metric):
    values = [row[metric] for row in rows if isinstance(row.get(metric), (int, float))]
    return statistics.median(values) if values else None


def fmt(x):
    return "-" if x is None else f"{x:,.1f}"


def run(args):
    arms = [parse_arm(a, parse_env(args.env)) for a in args.arm]
    ids = [a.id for a in arms]
    if len(set(ids)) != len(ids) or any(i.endswith("-b") for i in ids):
        raise SystemExit("--arm ids must be unique and must not end in -b (the A/A copy's suffix)")
    args.arm_list, args.arms, args.aa = arms, ids, ids
    result = overhead.run(args)
    with open(os.path.join(args.out, "variants.md"), "w") as f:
        f.write(summarize(result))
    with open(os.path.join(args.out, "variants.json"), "w") as f:
        json.dump({"arms": {a.id: {"binary": a.spec["binary"], "sha256": armlib.lock.sha256_file(a.spec["binary"]), "size": os.path.getsize(a.spec["binary"]), "env": a.spec.get("env", {})} for a in arms}}, f, indent=1)
    return result

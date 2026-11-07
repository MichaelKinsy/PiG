#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Pack the slow tests of a sharded package into balanced shards from go test -json timings.

Usage: shard-pack.py --package ./pkg [--shards N | --shards auto] [--replace-shards K] [--write WEIGHTS] TIMINGS...

TIMINGS are files or directories (every *.json inside) of `go test -json` output that together cover every test of the package once: the shards of one
run, or a single run of the whole package. The run must be warm: a test that paid a one-time setup (a cell build, a fixture) is measured with it, and
the packing would treat that setup as the test's own cost.

The cost of a shard is modelled from the same data, not from constants: a least-squares fit of each shard's wall to
    overhead + a * (elapsed of its serial tests) + b * (the larger of elapsed and subtests' sum, of its t.Parallel tests)
over the shards of the run (a serial test occupies the shard for its elapsed time; a parallel test shares the cores with the others). Tests that cost at
least --min-pin seconds are pinned by greedy longest-first packing on top of the hash-placed rest (test-shard-pattern.sh places every other test by the
FNV-1a hash of its name, which this script reproduces), and the pins print as the section test-shard-weights.txt reads.

--cold SECONDS reserves the one-time cost of a cold job (the first shards of each job build the extension cells, which warm timings lack) on --cold-shards
shards of each job, so the packing leaves them lighter.

--shards auto picks the fewest even shard counts (two CI jobs take half each) whose modelled worst shard stays within
--share * --limit / --slowdown / --error seconds: --limit is go test's package timeout, --slowdown the slowest runner measured against the host that took the
timings, --error the largest model error seen. The summary on stderr names the model, the loads and the two jobs' totals.
"""

import argparse
import glob
import json
import os
import sys

FNV_OFFSET, FNV_PRIME = 2166136261, 16777619


def fnv1a32(name):
    h = FNV_OFFSET
    for byte in name.encode():
        h = ((h ^ byte) * FNV_PRIME) & 0xFFFFFFFF
    return h


def read_timings(paths, package_suffix):
    """Return ({test: record}, [(serial, parallel, wall)] per shard file)."""
    files = []
    for path in paths:
        files += sorted(glob.glob(os.path.join(path, "*.json"))) if os.path.isdir(path) else [path]
    tests, shards = {}, []
    for file in files:
        top, sub, parallel, wall = {}, {}, set(), None
        with open(file, encoding="utf-8") as handle:
            for line in handle:
                try:
                    event = json.loads(line)
                except ValueError:
                    continue
                if not event.get("Package", "").endswith(package_suffix.lstrip("./")):
                    continue
                name, action = event.get("Test"), event.get("Action")
                if name is None:
                    if action in ("pass", "fail") and "Elapsed" in event:
                        wall = event["Elapsed"]
                    continue
                if action == "pause" and "/" not in name:
                    parallel.add(name)
                if action in ("pass", "fail", "skip") and "Elapsed" in event:
                    if "/" not in name:
                        top[name] = event["Elapsed"]
                    elif name.count("/") == 1:
                        sub[name.split("/")[0]] = sub.get(name.split("/")[0], 0.0) + event["Elapsed"]
        serial = par = 0.0
        for name, elapsed in top.items():
            if name in tests:
                print("shard-pack: %s is timed twice (second time in %s)" % (name, file), file=sys.stderr)
                sys.exit(1)
            record = {"elapsed": elapsed, "subtests": sub.get(name, 0.0), "parallel": name in parallel}
            tests[name] = record
            if record["parallel"]:
                par += max(elapsed, record["subtests"])
            else:
                serial += elapsed
        if wall is not None and top:
            shards.append((serial, par, wall))
    return tests, shards


def solve(matrix, vector):
    n = len(matrix)
    rows = [matrix[i][:] + [vector[i]] for i in range(n)]
    for col in range(n):
        pivot = max(range(col, n), key=lambda r: abs(rows[r][col]))
        rows[col], rows[pivot] = rows[pivot], rows[col]
        for r in range(n):
            if r != col:
                factor = rows[r][col] / rows[col][col]
                rows[r] = [x - factor * y for x, y in zip(rows[r], rows[col])]
    return [rows[i][n] / rows[i][i] for i in range(n)]


def fit(shards):
    """Least squares of wall = overhead + a*serial + b*parallel; returns (overhead, a, b, worst relative error)."""
    if len(shards) < 4:
        print("shard-pack: %d shard timings are too few to fit the model; give the shards of a run" % len(shards), file=sys.stderr)
        sys.exit(1)
    rows = [(1.0, s, p) for s, p, _ in shards]
    walls = [w for _, _, w in shards]
    gram = [[sum(r[i] * r[j] for r in rows) for j in range(3)] for i in range(3)]
    rhs = [sum(r[i] * w for r, w in zip(rows, walls)) for i in range(3)]
    overhead, a, b = solve(gram, rhs)
    worst = max(abs(overhead + a * s + b * p - w) / w for s, p, w in shards)
    return overhead, a, b, worst


def cost(record, a, b):
    return b * max(record["elapsed"], record["subtests"]) if record["parallel"] else a * record["elapsed"]


def pack(tests, shards, model, min_pin, cold, cold_shards):
    overhead, a, b = model
    load = [overhead] * shards
    # A job's first shards build the extension cells once for the whole job (testenv.SharedCellCacheDir), which the warm timings do not include.
    for first in (1, shards // 2 + 1):
        for index in range(first - 1, min(first - 1 + cold_shards, shards)):
            load[index] += cold
    pinned = []
    for name, record in tests.items():
        if max(record["elapsed"], record["subtests"] if record["parallel"] else 0.0) >= min_pin:
            pinned.append(name)
        else:
            load[fnv1a32(name) % shards] += cost(record, a, b)
    pins = {}
    for name in sorted(pinned, key=lambda n: (-cost(tests[n], a, b), n)):
        target = min(range(shards), key=lambda i: load[i])
        load[target] += cost(tests[name], a, b)
        pins[name] = target + 1
    return pins, load


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("timings", nargs="+")
    parser.add_argument("--package", required=True)
    parser.add_argument("--shards", default="auto")
    parser.add_argument("--min-pin", type=float, default=5.0, help="pin tests of at least this many seconds (default 5)")
    parser.add_argument("--limit", type=float, default=600.0)
    parser.add_argument("--share", type=float, default=0.5)
    parser.add_argument("--slowdown", type=float, default=1.4)
    parser.add_argument("--error", type=float, default=1.1)
    parser.add_argument("--cold", type=float, default=0.0, help="seconds of one-time setup (cell builds) a cold job adds to each of its first --cold-shards shards")
    parser.add_argument("--cold-shards", type=int, default=2)
    parser.add_argument("--replace-shards", type=int, help="with --write, the shard count of the section to replace")
    parser.add_argument("--write", help="test-shard-weights.txt to rewrite")
    args = parser.parse_args()

    tests, walls = read_timings(args.timings, args.package)
    if not tests:
        print("shard-pack: no test of %s in the timings" % args.package, file=sys.stderr)
        sys.exit(1)
    overhead, a, b, err = fit(walls)
    budget = args.share * args.limit / args.slowdown / args.error
    if args.shards == "auto":
        count = 2
        while True:
            pins, load = pack(tests, count, (overhead, a, b), args.min_pin, args.cold, args.cold_shards)
            if max(load) <= budget or count > 200:
                break
            count += 2
    else:
        count = int(args.shards)
        pins, load = pack(tests, count, (overhead, a, b), args.min_pin, args.cold, args.cold_shards)
    half = count // 2
    print(
        "shard-pack: %d tests, %d timed shards; model wall = %.1f + %.3f*serial + %.3f*parallel (worst error %.0f%%)\n"
        "shard-pack: %d shards, %d pins; modelled load %.0f..%.0f s (budget %.0f s = %.0f%% of %.0f s on a %.1fx slower runner with %.0f%% model error); "
        "first %d shards %.0f s, rest %.0f s"
        % (len(tests), len(walls), overhead, a, b, err * 100, count, len(pins), min(load), max(load), budget, args.share * 100, args.limit,
           args.slowdown, (args.error - 1) * 100, half, sum(load[:half]), sum(load[half:])),
        file=sys.stderr,
    )
    section = "package %s\nshards %d\n" % (args.package, count) + "".join("%d %s\n" % (s, n) for n, s in sorted(pins.items(), key=lambda kv: (kv[1], kv[0])))
    if not args.write:
        sys.stdout.write(section)
        return
    if args.replace_shards is None:
        print("shard-pack: --write needs --replace-shards", file=sys.stderr)
        sys.exit(2)
    with open(args.write, encoding="utf-8") as handle:
        lines = handle.read().split("\n")
    start = next((i for i, l in enumerate(lines) if l == "package " + args.package and lines[i + 1] == "shards %d" % args.replace_shards), None)
    if start is None:
        print("shard-pack: no section %s shards %d in %s" % (args.package, args.replace_shards, args.write), file=sys.stderr)
        sys.exit(1)
    end = start
    while end < len(lines) and lines[end] != "":
        end += 1
    lines[start:end] = section.rstrip("\n").split("\n")
    with open(args.write, "w", encoding="utf-8") as handle:
        handle.write("\n".join(lines))


if __name__ == "__main__":
    main()

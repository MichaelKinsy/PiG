#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Pack the slow tests of a sharded package into balanced shards from go test -json or -v timings.

Usage: shard-pack.py --package ./pkg [--shards N | --shards auto] [--replace-shards K] [--write WEIGHTS] TIMINGS...

TIMINGS are files or directories (every *.json or *.log inside) of `go test -json` output or `go test -v` logs (including GitHub Actions logs with
leading ISO-8601 timestamps) that together cover every test of the package once: the shards of one run, or a single run of the whole package. A compact
timing table (the format --emit-timings writes, kept in automation/ci/test-shard-timings/) stands for such a run, so the weights can be regenerated,
and checked, from what is committed. The run must be warm unless each CI shard runs on its own cold runner: a test that paid a one-time setup (a cell
build, a fixture) is measured with it, and the packing would treat that setup as the test's own cost.

The cost of a shard is modelled from the same data, not from constants: a least-squares fit of each finished shard's wall to
    overhead + a * (elapsed of its serial tests) + b * (the larger of elapsed and subtests' sum, of its t.Parallel tests)
over the finished shards of the run (a serial test occupies the shard for its elapsed time; a parallel test shares the cores with the others). When
fewer than four finished shards are available (for instance when some shards of a CI run timed out), serial weight is fixed at a = 1.0 and overhead
and b are fitted from the finished shards (falling back to measured pre-test warm-up overhead when unconstrained regression would give negative overhead).
Tests that cost at least --min-pin seconds are pinned by greedy longest-first packing on top of the hash-placed rest (test-shard-pattern.sh places
every other test by the FNV-1a hash of its name, which this script reproduces), and the pins print as the section test-shard-weights.txt reads.

When a `go test -v` shard log timed out before every assigned test finished, --fallback-timings FILE (a compact timing table, such as the Linux
timings in automation/ci/test-shard-timings/) estimates each unfinished assigned test from its fallback time multiplied by the median ratio of
measured to fallback times across the tests that did finish (separately for serial and parallel tests), bounded below by any running time reported in
the timeout panic. Timed-out shards are excluded from the wall-time model fit.

--cold SECONDS reserves the one-time cost of a cold job (the first shards of each job build the extension cells, which warm timings lack) on --cold-shards
shards of each job, so the packing leaves them lighter.

--shards auto picks the fewest even shard counts (two CI jobs take half each) whose modelled worst shard stays within
--share * --limit / --slowdown / --error seconds: --limit is go test's package timeout, --slowdown the slowest runner measured against the host that took the
timings, --error the largest model error seen. The summary on stderr names the model, the loads and the two jobs' totals.
"""

import argparse
from datetime import datetime
import glob
import json
import os
import re
import statistics
import sys

FNV_OFFSET, FNV_PRIME = 2166136261, 16777619

ANSI_RE = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")
TS_RE = re.compile(r"^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(\.\d+)?Z (.*)$")
RUN_RE = re.compile(r"^=== RUN   ([^\s/]+)$")
PAUSE_RE = re.compile(r"^=== PAUSE ([^\s/]+)$")
RESULT_RE = re.compile(r"^\s*--- (PASS|FAIL|SKIP): ([^\s]+) \(([0-9.]+)s\)$")
SHARD_RUN_RE = re.compile(r"(?:SHARD_RUN[:=]\s*|\s-(?:test\.)?run(?:=|\s+))\^\(([^)]+)\)\$")
PKG_WALL_RE = re.compile(r"^(ok|FAIL)\s+(\S+)\s+([0-9.]+)s$")
RUNNING_TEST_RE = re.compile(r"^([^\s/]+) \(([^)]+)\)$")
GO_CMD_RE = re.compile(r"\bgo test\b|\b-test\.run\b")
SHARD_NUM_RE = re.compile(r"(?:shard|conformance|host|cli)-(\d+)")


def fnv1a32(name):
    h = FNV_OFFSET
    for byte in name.encode():
        h = ((h ^ byte) * FNV_PRIME) & 0xFFFFFFFF
    return h


def parse_iso(ts_head, frac):
    us = (frac[1:7].ljust(6, "0")) if frac else "000000"
    return datetime.fromisoformat("%s.%s+00:00" % (ts_head, us)).timestamp()


def parse_go_duration(text):
    sec = 0.0
    m = re.search(r"(\d+)h", text)
    if m:
        sec += int(m.group(1)) * 3600
    m = re.search(r"(\d+)m(?!s)", text)
    if m:
        sec += int(m.group(1)) * 60
    m = re.search(r"([0-9.]+)s", text)
    if m:
        sec += float(m.group(1))
    return sec


def read_compact(file):
    """Return the compact timing table in file, or None when it is not a compact table."""
    try:
        with open(file, encoding="utf-8") as handle:
            table = json.load(handle)
    except (ValueError, UnicodeDecodeError):
        return None
    return table if isinstance(table, dict) and "tests" in table and "shards" in table else None


def write_compact(path, package, tests, shard_of, walls, overhead=None):
    """Write the timing table: one line a test, [shard, elapsed, subtests' sum, parallel], and the wall of every shard."""
    ov = (', "overhead": %.2f' % overhead) if overhead is not None else ""
    lines = ['{"package": %s, "shards": %s%s, "tests": {' % (json.dumps(package), json.dumps({str(k): round(v, 2) for k, v in sorted(walls.items())}), ov)]
    rows = ['%s: [%d, %.2f, %.2f, %d]' % (json.dumps(n), shard_of[n], r["elapsed"], r["subtests"], 1 if r["parallel"] else 0) for n, r in sorted(tests.items())]
    lines.append(",\n".join(rows))
    lines.append("}}")
    with open(path, "w", encoding="utf-8") as handle:
        handle.write("\n".join(lines) + "\n")


def read_verbose_log(file, package_suffix, shard_number):
    """Parse one `go test -v` log (plain or GitHub Actions timestamped).

    Returns (top_records, (serial, parallel, wall_or_None), warm_or_None, assigned, paused, running, timed_out).
    """
    pkg_tail = package_suffix.lstrip("./")
    assigned = []
    run_ts = {}
    paused = set()
    top = {}
    top_wall = {}
    sub = {}
    sub_max = {}
    wall = None
    timed_out = False
    running = {}
    in_running = False
    cmd_ts = None
    first_run_ts = None

    with open(file, encoding="utf-8", errors="replace") as handle:
        for raw in handle:
            raw = raw.rstrip("\r\n")
            m_ts = TS_RE.match(raw)
            if m_ts:
                ts = parse_iso(m_ts.group(1), m_ts.group(2))
                line = ANSI_RE.sub("", m_ts.group(3))
            else:
                ts = None
                line = ANSI_RE.sub("", raw)
            if not assigned:
                m = SHARD_RUN_RE.search(line)
                if m:
                    assigned = m.group(1).split("|")
            if cmd_ts is None and pkg_tail in line and GO_CMD_RE.search(line):
                cmd_ts = ts
            if first_run_ts is None and line.startswith("=== RUN   "):
                first_run_ts = ts
            m = RUN_RE.match(line)
            if m:
                run_ts[m.group(1)] = ts
                continue
            m = PAUSE_RE.match(line)
            if m:
                paused.add(m.group(1))
                continue
            m = RESULT_RE.match(line)
            if m:
                name, sec = m.group(2), float(m.group(3))
                if "/" not in name:
                    top[name] = sec
                    if name not in paused and ts is not None and run_ts.get(name) is not None:
                        top_wall[name] = max(sec, ts - run_ts[name])
                elif name.count("/") == 1:
                    parent = name.split("/", 1)[0]
                    sub[parent] = sub.get(parent, 0.0) + sec
                    sub_max[parent] = max(sub_max.get(parent, 0.0), sec)
                continue
            if line.startswith("panic: test timed out after"):
                timed_out = True
                continue
            if line.strip() == "running tests:":
                in_running = True
                continue
            if in_running:
                stripped = line.strip()
                if stripped and line[:1] in ("\t", " "):
                    rm = RUNNING_TEST_RE.match(stripped)
                    if rm:
                        running[rm.group(1)] = parse_go_duration(rm.group(2))
                    continue
                in_running = False
            m = PKG_WALL_RE.match(line)
            if m and m.group(2).endswith(pkg_tail):
                wall = float(m.group(3))

    warm = (first_run_ts - cmd_ts) if (cmd_ts is not None and first_run_ts is not None and first_run_ts >= cmd_ts) else None
    records = {}
    serial = par = 0.0
    for name, sec in top.items():
        is_p = name in paused
        sb = round(sub.get(name, 0.0), 2)
        if is_p:
            el = round(sec, 2)
            par += max(el, sb)
        else:
            # A non-parallel parent whose subtests call t.Parallel() reports ~0.00s for the parent itself;
            # its serial occupancy is its wall time (or at least its slowest subtest).
            el = round(top_wall.get(name, max(sec, sub_max.get(name, 0.0))), 2)
            serial += el
        records[name] = {"elapsed": el, "subtests": sb, "parallel": is_p}
    if wall is not None:
        wall = round(wall, 2)
    return records, (serial, par, wall), warm, assigned, paused, running, timed_out


def is_json_lines(file):
    """Return True when file starts with a JSON object line (go test -json stream)."""
    with open(file, encoding="utf-8", errors="replace") as handle:
        for line in handle:
            s = line.strip()
            if not s:
                continue
            if not s.startswith("{"):
                return False
            try:
                return isinstance(json.loads(s), dict)
            except ValueError:
                return False
    return False


def shard_number_of(file, walls):
    named = SHARD_NUM_RE.search(os.path.basename(file))
    return int(named.group(1)) if named else len(walls) + 1


def collect_files(paths):
    files = []
    for path in paths:
        if os.path.isdir(path):
            found = sorted(glob.glob(os.path.join(path, "*.json")))
            if not found:
                found = sorted(glob.glob(os.path.join(path, "*.log")))
            files += found
        else:
            files.append(path)
    return files


def read_timings(paths, package_suffix, fallback_path=None):
    """Return ({test: record}, [(serial, parallel, wall)] per finished shard, shard_of, walls) and metadata."""
    files = collect_files(paths)
    tests, shards, shard_of, walls = {}, [], {}, {}
    unfinished = []
    warms = []

    for file in files:
        table = read_compact(file)
        if table is not None:
            if table.get("overhead") is not None:
                warms.append(float(table["overhead"]))
            per_shard = {}
            for name, (shard, elapsed, sub, parallel) in table["tests"].items():
                if name in tests:
                    print("shard-pack: %s is timed twice (second time in %s)" % (name, file), file=sys.stderr)
                    sys.exit(1)
                tests[name] = {"elapsed": elapsed, "subtests": sub, "parallel": bool(parallel)}
                shard_of[name] = shard
                acc = per_shard.setdefault(shard, [0.0, 0.0])
                acc[1 if parallel else 0] += max(elapsed, sub) if parallel else elapsed
            for shard, wall in table["shards"].items():
                shards.append((per_shard.get(int(shard), [0.0, 0.0])[0], per_shard.get(int(shard), [0.0, 0.0])[1], wall))
                walls[int(shard)] = wall
            continue

        shard_number = shard_number_of(file, walls)
        if not is_json_lines(file):
            records, (serial, par, wall), warm, assigned, paused, running, timed_out = read_verbose_log(file, package_suffix, shard_number)
            if warm is not None:
                warms.append(warm)
            for name, record in records.items():
                if name in tests:
                    print("shard-pack: %s is timed twice (second time in %s)" % (name, file), file=sys.stderr)
                    sys.exit(1)
                tests[name] = record
                shard_of[name] = shard_number
            for name in assigned:
                if name not in records:
                    unfinished.append((name, shard_number, name in paused, running.get(name, 0.0)))
            if wall is not None and records and not timed_out:
                shards.append((serial, par, wall))
                walls[shard_number] = wall
            continue

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
            shard_of[name] = shard_number
            if record["parallel"]:
                par += max(elapsed, record["subtests"])
            else:
                serial += elapsed
        if wall is not None and top:
            shards.append((serial, par, wall))
            walls[shard_number] = wall

    if unfinished:
        fallback = read_compact(fallback_path) if fallback_path else None
        fb_tests = fallback["tests"] if fallback else {}
        ratios_serial, ratios_parallel = [], []
        for name, rec in tests.items():
            if name not in fb_tests:
                continue
            _, fe, fs, fp = fb_tests[name]
            fv = max(fe, fs) if fp else fe
            mv = max(rec["elapsed"], rec["subtests"]) if rec["parallel"] else rec["elapsed"]
            if fv >= 1.0:
                (ratios_parallel if fp else ratios_serial).append(mv / fv)
        med_s = statistics.median(ratios_serial) if ratios_serial else 1.0
        med_p = statistics.median(ratios_parallel) if ratios_parallel else med_s
        for name, shard_number, was_paused, run_sec in unfinished:
            if name in tests:
                continue
            if name in fb_tests:
                _, fe, fs, fp = fb_tests[name]
                is_p = bool(fp)
                ratio = med_p if is_p else med_s
                el = max(fe * ratio, run_sec * 1.2)
                sb = max(fs * ratio, run_sec * 1.2 if is_p else 0.0)
            else:
                is_p = was_paused
                # Unfinished test absent from fallback: use running time (with headroom for unfinished work) or conservative 25 s default.
                el = max(run_sec * 1.35, 25.0)
                sb = el if is_p else 0.0
            tests[name] = {"elapsed": round(el, 2), "subtests": round(sb, 2), "parallel": is_p}
            shard_of[name] = shard_number

    return tests, shards, shard_of, walls, warms, len(unfinished)


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


def fit(shards, measured_overhead=None):
    """Least squares of wall = overhead + a*serial + b*parallel; returns (overhead, a, b, worst relative error, mean relative error)."""
    if len(shards) < 2:
        print("shard-pack: %d shard timings are too few to fit the model; give the shards of a run" % len(shards), file=sys.stderr)
        sys.exit(1)
    if len(shards) >= 4:
        rows = [(1.0, s, p) for s, p, _ in shards]
        walls = [w for _, _, w in shards]
        gram = [[sum(r[i] * r[j] for r in rows) for j in range(3)] for i in range(3)]
        rhs = [sum(r[i] * w for r, w in zip(rows, walls)) for i in range(3)]
        overhead, a, b = solve(gram, rhs)
    else:
        # With 2 or 3 finished shards (e.g. a CI run where some shards timed out), serial tests occupy 1.0x of their wall time; fit overhead and b.
        a = 1.0
        n = len(shards)
        mx = sum(p for _, p, _ in shards) / n
        my = sum(w - s for s, _, w in shards) / n
        denom = sum((p - mx) ** 2 for _, p, _ in shards)
        if denom > 0:
            b = sum((p - mx) * ((w - s) - my) for s, p, w in shards) / denom
            overhead = my - b * mx
        else:
            overhead, b = my, 0.0
        if overhead < 0:
            overhead = measured_overhead if measured_overhead is not None else 0.0
            sum_p = sum(p for _, p, _ in shards)
            b = max(0.0, sum(w - s - overhead for s, _, w in shards) / sum_p) if sum_p > 0 else 0.0
    errors = [abs(overhead + a * s + b * p - w) / w for s, p, w in shards]
    return overhead, a, b, max(errors), sum(errors) / len(errors)


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
    parser.add_argument("--fallback-timings", help="compact timing table used to estimate unfinished tests from timed-out logs")
    parser.add_argument("--emit-timings", help="write the compact timing table of the inputs to this file and exit")
    parser.add_argument("--replace-shards", type=int, help="with --write, the shard count of the section to replace")
    parser.add_argument("--write", help="test-shard-weights.txt to rewrite")
    parser.add_argument("--dry-run", action="store_true", help="print each shard's test count and modelled seconds instead of the weights section")
    args = parser.parse_args()

    tests, shard_walls, shard_of, walls, warms, estimated = read_timings(args.timings, args.package, args.fallback_timings)
    if not tests:
        print("shard-pack: no test of %s in the timings" % args.package, file=sys.stderr)
        sys.exit(1)
    measured_overhead = round(sum(warms) / len(warms), 2) if warms else None
    if args.emit_timings:
        write_compact(args.emit_timings, args.package, tests, shard_of, walls, overhead=measured_overhead)
        return
    overhead, a, b, err, mean_err = fit(shard_walls, measured_overhead=measured_overhead)
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
    est_note = (", %d estimated" % estimated) if estimated else ""
    print(
        "shard-pack: %d tests%s, %d timed shards; model wall = %.1f + %.3f*serial + %.3f*parallel (error on the fitted shards: mean %.0f%%, worst %.0f%%)\n"
        "shard-pack: %d shards, %d pins; modelled load %.0f..%.0f s (budget %.0f s = %.0f%% of %.0f s on a %.1fx slower runner with %.0f%% model error); "
        "first %d shards %.0f s, rest %.0f s"
        % (len(tests), est_note, len(shard_walls), overhead, a, b, mean_err * 100, err * 100, count, len(pins), min(load), max(load), budget, args.share * 100, args.limit,
           args.slowdown, (args.error - 1) * 100, half, sum(load[:half]), sum(load[half:])),
        file=sys.stderr,
    )
    if args.dry_run:
        counts = [0] * count
        for name in tests:
            place = pins.get(name, fnv1a32(name) % count + 1)
            counts[place - 1] += 1
        for idx in range(count):
            margin = (args.limit - load[idx]) / args.limit * 100.0 if args.limit > 0 else 0.0
            print("shard %2d: %3d tests, %6.1f s (%.1f%% margin under %.0f s)" % (idx + 1, counts[idx], load[idx], margin, args.limit))
        return
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

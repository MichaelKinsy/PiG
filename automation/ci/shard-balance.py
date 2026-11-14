#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Balance and gate the shards of a sharded Go test package by recorded test durations.

automation/ci/test-shard-timings.txt records, for each top-level test of a package, how long it ran on a CI-sized runner
(4 CPUs): `serial <seconds> <Test>` for a test that runs alone, `parallel <seconds> <Test>` for one that calls t.Parallel.
automation/ci/test-shard-weights.txt pins every recorded test to a shard; test-shard-pattern.sh reads those pins. This tool
keeps the two files, the shard count and go test's 10-minute package timeout consistent:

  record  --package P LOG...      Write the package's timing section from `go test -v` logs of its shards (the median of
                                  the logs), with the cost model fitted to the shard times the logs show.
  pack    --package P --shards N  Print the weights section that packs every recorded test into N shards, longest first
                                  onto the lightest shard.
  check   --package P --shards N  Fail unless the weights section is exactly what pack prints, and the busiest shard's
                                  predicted time, counting a test the timings do not name at the default cost wherever
                                  test-shard-pattern.sh hashes it, leaves HEADROOM of the timeout. The package's tests are
                                  the Test, Example and Fuzz functions of its *_test.go files under --root (the repository).

A shard's predicted time is `fixed + serial * (sum of the serial tests' seconds) + parallel * (sum of the parallel tests'
seconds)`: the tests that run alone add their time, and the t.Parallel tests run together at the end, so they add a fraction
of theirs. fixed (building and starting the test binary), serial and parallel are fitted by `record` to the shard times of
the logs. HEADROOM is a share of the timeout that no shard may use, so that a
runner slower than the one measured (CI runners differ by 1.6x) does not turn a shard into a timeout.
"""
import argparse
import os
import re
import statistics
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
TIMINGS = os.path.join(HERE, "test-shard-timings.txt")
WEIGHTS = os.path.join(HERE, "test-shard-weights.txt")
TIMEOUT_SECONDS = 600.0  # go test's default package timeout, which no sharded run raises
HEADROOM = 0.40
MIN_COST = 0.3  # seconds
MODEL_KEYS = ("fixed", "serial-factor", "parallel-factor", "default")
TEST_NAME = re.compile(r"^(Test|Example|Fuzz)[A-Za-z0-9_]*$")


def die(message):
    print(f"shard-balance: {message}", file=sys.stderr)
    sys.exit(1)


def package_matches(section, package):
    # CI passes ./coding/cli, make passes the import path; both name the package.
    return section == package or package.endswith("/" + section.removeprefix("./"))


def read_timings(path, package):
    """Return (model, tests): model is {fixed, serial, parallel, default}, tests maps name -> (kind, seconds)."""
    model, tests, active = {}, {}, False
    with open(path, encoding="utf-8") as handle:
        for raw in handle:
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            fields = line.split()
            if fields[0] == "package":
                active = package_matches(fields[1], package)
            elif not active:
                continue
            elif fields[0] in MODEL_KEYS and len(fields) == 2:
                model[fields[0]] = float(fields[1])
            elif fields[0] in ("serial", "parallel") and len(fields) == 3 and TEST_NAME.match(fields[2]):
                if fields[2] in tests:
                    die(f"{path} records {fields[2]} twice")
                tests[fields[2]] = (fields[0], float(fields[1]))
            else:
                die(f"bad line in {path}: {line}")
    if not tests:
        die(f"{path} has no timings for {package}")
    for key in MODEL_KEYS:
        if key not in model:
            die(f"{path} has no {key} for {package}")
    return model, tests


def cost(model, kind, seconds):
    """What a test adds to its shard: never less than MIN_COST, so near-free tests spread over the shards instead of piling onto the lightest."""
    return max(seconds * model["serial-factor" if kind == "serial" else "parallel-factor"], MIN_COST)


def pack(model, tests, shards):
    """Longest first onto the lightest shard; ties by name and shard number, so the result is deterministic."""
    loads = [0.0] * shards
    placed = {}
    for name, (kind, seconds) in sorted(tests.items(), key=lambda item: (-cost(model, *item[1]), item[0])):
        shard = min(range(shards), key=lambda index: (loads[index], index))
        loads[shard] += cost(model, kind, seconds)
        placed[name] = shard + 1
    return placed


def fnv1a32(name):
    # The same hash as test-shard-pattern.sh, which reads the names byte by byte (ASCII only).
    value = 2166136261
    for byte in name.encode("ascii"):
        value = ((value ^ byte) * 16777619) & 0xFFFFFFFF
    return value


def weights_section(package, shards, placed, header):
    lines = [f"package {package}", f"shards {shards}"]
    lines += [f"{shard} {name}" for name, shard in sorted(placed.items(), key=lambda item: (item[1], item[0]))]
    return header + "\n".join(lines) + "\n"


def read_weights_section(path, package, shards):
    """Return the pins of the section for package at this shard count, or None, and the section's text."""
    pins, text, active, found = {}, [], False, False
    with open(path, encoding="utf-8") as handle:
        for raw in handle:
            fields = raw.split()
            if fields[:1] == ["package"]:
                active = bool(fields[1:]) and package_matches(fields[1], package)
                continue
            if fields[:1] == ["shards"]:
                active = active and fields[1:] == [str(shards)]
                found = found or active
                continue
            if active and len(fields) == 2 and fields[0].isdigit():
                pins[fields[1]] = int(fields[0])
    return (pins if found else None)


def predicted(model, tests, placed_by, names, shards):
    """Predicted seconds of each shard: names is every test of the package; placed_by(name) its shard."""
    totals = [model["fixed"]] * shards
    for name in names:
        shard = placed_by(name) - 1
        kind, seconds = tests.get(name, ("serial", model["default"]))
        totals[shard] += cost(model, kind, seconds)
    return totals


def command_pack(args):
    model, tests = read_timings(args.timings, args.package)
    sys.stdout.write(weights_section(args.package, args.shards, pack(model, tests, args.shards), ""))


DECLARATION = re.compile(r"^func ((?:Test|Example|Fuzz)(?:[A-Z0-9_][A-Za-z0-9_]*)?)\(", re.MULTILINE)


def list_tests(root, package):
    """The top-level tests of the package at root/package, read from source: what `go test -list .` prints, without building.

    A file that a build constraint leaves out of some platform still counts, so the prediction is never optimistic."""
    directory = os.path.join(root, package.removeprefix("./"))
    names = set()
    for entry in sorted(os.listdir(directory)):
        if entry.endswith("_test.go"):
            with open(os.path.join(directory, entry), encoding="utf-8") as handle:
                names.update(DECLARATION.findall(handle.read()))
    names.discard("TestMain")
    return sorted(names)


def command_check(args):
    model, tests = read_timings(args.timings, args.package)
    names = list_tests(args.root, args.package)
    if not names:
        die(f"{args.package} has no tests under {args.root}")
    pins = read_weights_section(args.weights, args.package, args.shards)
    if pins is None:
        die(f"{args.weights} has no section for {args.package} at {args.shards} shards; run shard-balance.py pack")
    expected = pack(model, tests, args.shards)
    if pins != expected:
        drift = sorted(set(pins.items()) ^ set(expected.items()))
        die(
            f"{args.weights} pins for {args.package} at {args.shards} shards are not what the timings in {args.timings} pack to "
            f"({len(drift)} differing pins, first {drift[:3]}); regenerate them with shard-balance.py pack"
        )
    placed_by = lambda name: pins[name] if name in pins else fnv1a32(name) % args.shards + 1
    times = predicted(model, tests, placed_by, names, args.shards)
    limit = (1 - HEADROOM) * TIMEOUT_SECONDS
    unrecorded = [name for name in names if name not in tests]
    summary = ", ".join(f"{index + 1}:{seconds:.0f}" for index, seconds in enumerate(times))
    print(f"shard-balance: {args.package} predicted seconds per shard (limit {limit:.0f} of {TIMEOUT_SECONDS:.0f}): {summary}; {len(unrecorded)} tests unrecorded")
    over = [(index + 1, seconds) for index, seconds in enumerate(times) if seconds > limit]
    if over:
        die(
            f"{args.package} shards {', '.join(f'{shard} ({seconds:.0f} s)' for shard, seconds in over)} exceed {limit:.0f} s, "
            f"{int(HEADROOM * 100)}% under go test's {TIMEOUT_SECONDS:.0f} s timeout. Record the suite's durations again "
            f"(see {os.path.basename(args.timings)}) and raise the shard count in test-grouped.sh and the make targets if they still do not fit"
        )


SHARD_MARK = re.compile(r">>> sharded (\S+) shard (\d+) of (\d+)")
PACKAGE_OK = re.compile(r"^ok\s+(\S+)\s+([0-9.]+)s")
PAUSE = re.compile(r"^=== PAUSE (\S+)$")
RESULT = re.compile(r"^--- (PASS|FAIL|SKIP): (\S+) \(([0-9.]+)s\)")


def parse_log(path):
    """Return ({test: (kind, seconds)}, [(serial_sum, parallel_sum, wall)]) for the log's passing shards."""
    tests, shards = {}, []
    paused, serial, parallel, mark = set(), 0.0, 0.0, False
    with open(path, encoding="utf-8", errors="replace") as handle:
        for line in handle:
            if SHARD_MARK.search(line):
                paused, serial, parallel, mark = set(), 0.0, 0.0, True
                continue
            match = PAUSE.match(line)
            if match:
                if "/" not in match.group(1):
                    paused.add(match.group(1))
                continue
            match = RESULT.match(line)
            if match and "/" not in match.group(2):
                name, seconds = match.group(2), float(match.group(3))
                kind = "parallel" if name in paused else "serial"
                tests[name] = (kind, seconds)
                if kind == "serial":
                    serial += seconds
                else:
                    parallel += seconds
                continue
            match = PACKAGE_OK.match(line)
            if match and mark:
                shards.append((serial, parallel, float(match.group(2))))
                mark = False
    return tests, shards


def fit(points):
    """Least squares wall = fixed + serial_factor * serial + parallel_factor * parallel over (serial, parallel, wall) points."""
    rows = [[1.0, serial, parallel] for serial, parallel, _ in points]
    walls = [wall for _, _, wall in points]
    normal = [[sum(row[i] * row[j] for row in rows) for j in range(3)] for i in range(3)]
    rhs = [sum(row[i] * wall for row, wall in zip(rows, walls)) for i in range(3)]
    for i in range(3):  # Gaussian elimination with partial pivoting
        pivot = max(range(i, 3), key=lambda r: abs(normal[r][i]))
        if abs(normal[pivot][i]) < 1e-9:
            die("the logs do not show enough different shard loads to fit a cost model")
        normal[i], normal[pivot], rhs[i], rhs[pivot] = normal[pivot], normal[i], rhs[pivot], rhs[i]
        for r in range(i + 1, 3):
            factor = normal[r][i] / normal[i][i]
            normal[r] = [a - factor * b for a, b in zip(normal[r], normal[i])]
            rhs[r] -= factor * rhs[i]
    coefficients = [0.0] * 3
    for i in reversed(range(3)):
        coefficients[i] = (rhs[i] - sum(normal[i][c] * coefficients[c] for c in range(i + 1, 3))) / normal[i][i]
    fixed, serial_factor, parallel_factor = coefficients
    residual = max(abs(fixed + serial_factor * s + parallel_factor * p - w) for s, p, w in points)
    return fixed, max(serial_factor, 0.0), max(parallel_factor, 0.0), residual


def command_record(args):
    runs, points = [], []
    for log in args.logs:
        tests, shards = parse_log(log)
        runs.append(tests)
        points += shards
    if not points:
        die("no log holds a passing sharded run (a '>>> sharded' line, then 'ok <package> <seconds>s')")
    fixed, serial_factor, parallel_factor, residual = fit(points)
    kinds, seconds = {}, {}
    for tests in runs:
        for name, (kind, value) in tests.items():
            if kind == "parallel":
                kinds[name] = "parallel"
            else:
                kinds.setdefault(name, "serial")
            seconds.setdefault(name, []).append(value)
    medians = {name: statistics.median(values) for name, values in seconds.items() if TEST_NAME.match(name)}
    default = statistics.fmean(sorted(medians.values())[: max(1, len(medians) * 9 // 10)])
    print(
        f"shard-balance: {len(medians)} tests from {len(args.logs)} logs; {len(points)} shard runs fit "
        f"fixed {fixed:.1f} s, serial {serial_factor:.3f}, parallel {parallel_factor:.3f}, worst residual {residual:.0f} s",
        file=sys.stderr,
    )
    print(f"package {args.package}")
    print(f"fixed {fixed:.1f}")
    print(f"serial-factor {serial_factor:.3f}")
    print(f"parallel-factor {parallel_factor:.3f}")
    print(f"default {default:.2f}")
    for name, value in sorted(medians.items(), key=lambda item: (-item[1] * (1.0 if kinds[item[0]] == "serial" else parallel_factor / max(serial_factor, 1e-9)), item[0])):
        print(f"{kinds[name]} {value:.2f} {name}")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="command", required=True)
    for name, handler in (("pack", command_pack), ("check", command_check), ("record", command_record)):
        command = sub.add_parser(name)
        command.add_argument("--package", required=True)
        command.add_argument("--timings", default=TIMINGS)
        command.add_argument("--weights", default=WEIGHTS)
        command.add_argument("--root", default=os.path.dirname(os.path.dirname(HERE)))
        if name == "record":
            command.add_argument("logs", nargs="+")
        else:
            command.add_argument("--shards", type=int, required=True)
        command.set_defaults(handler=handler)
    args = parser.parse_args()
    args.handler(args)


if __name__ == "__main__":
    main()

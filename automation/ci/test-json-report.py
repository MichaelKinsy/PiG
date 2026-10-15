#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Summarize `go test -json` output as it streams.

Usage: go test -json ... | test-json-report.py [--out FILE] [--slowest N]

Prints one line per finished package (ok, FAIL, cached, no test files), the
complete output of every failed test, of every test still running when its
package failed (a timeout or a crash), and of every build failure, and at the
end the N slowest tests and packages with their elapsed seconds. The raw
event stream is copied to FILE when --out is given, so the run can be
inspected later with any test2json tool. The exit status is 0; the caller
takes the status from go test itself.
"""

import argparse
import json
import sys
from collections import defaultdict


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--out")
    parser.add_argument("--slowest", type=int, default=10)
    args = parser.parse_args()
    raw = open(args.out, "a", encoding="utf-8") if args.out else None
    outputs: dict[tuple[str, str], list[str]] = defaultdict(list)
    build_output: dict[str, list[str]] = defaultdict(list)
    tests: list[tuple[float, str, str]] = []
    packages: list[tuple[float, str]] = []
    failed_packages = 0
    for line in sys.stdin:
        if raw:
            raw.write(line)
        try:
            event = json.loads(line)
        except ValueError:
            sys.stdout.write(line)
            continue
        action = event.get("Action")
        if action == "build-output":
            build_output[event.get("ImportPath", "")].append(event.get("Output", ""))
            continue
        if action == "build-fail":
            path = event.get("ImportPath", "")
            sys.stdout.write("".join(build_output.pop(path, [])))
            continue
        package = event.get("Package", "")
        test = event.get("Test") or ""
        if action == "output":
            outputs[(package, test)].append(event.get("Output", ""))
            continue
        if action not in ("pass", "fail", "skip"):
            continue
        elapsed = float(event.get("Elapsed") or 0)
        if test:
            if "/" not in test:
                tests.append((elapsed, package, test))
            if action == "fail":
                sys.stdout.write("".join(outputs.get((package, test), [])))
            outputs.pop((package, test), None)
            continue
        text = "".join(outputs.pop((package, ""), []))
        # A test still running when its package ends never gets its own pass or fail event: the package timed out, panicked outside a test, or the binary exited. Its output holds the reason (the timeout panic names the running tests) and is printed with a failure.
        unfinished = [key for key in outputs if key[0] == package]
        unfinished_text = "".join("".join(outputs.pop(key)) for key in unfinished)
        if action == "fail":
            failed_packages += 1
            sys.stdout.write(unfinished_text)
            # Package-level output carries panics, timeouts and TestMain failures that no single test owns.
            sys.stdout.write("".join(line for line in text.splitlines(keepends=True) if not line.startswith(("FAIL\t", "ok  \t"))))
            print(f"FAIL\t{package}\t{elapsed:.2f}s")
        elif "(cached)" in text:
            print(f"ok  \t{package}\t(cached)")
        elif "[no test files]" in text or action == "skip":
            print(f"?   \t{package}\t[no test files]")
        else:
            packages.append((elapsed, package))
            print(f"ok  \t{package}\t{elapsed:.2f}s")
        sys.stdout.flush()
    if raw:
        raw.close()
    if args.slowest > 0 and tests:
        print(f"slowest tests (top {args.slowest}):")
        for elapsed, package, test in sorted(tests, reverse=True)[: args.slowest]:
            print(f"  {elapsed:8.2f}s  {package} {test}")
    if args.slowest > 0 and packages:
        print(f"slowest packages (top {args.slowest}):")
        for elapsed, package in sorted(packages, reverse=True)[: args.slowest]:
            print(f"  {elapsed:8.2f}s  {package}")
    if failed_packages:
        print(f"{failed_packages} package(s) failed")
    return 0


if __name__ == "__main__":
    sys.exit(main())

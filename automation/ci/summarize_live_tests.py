#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Summarize provider-marked tests from go test -json without exposing output."""

import collections
import json
import re
import sys

PROVIDER = re.compile(r"\blive provider: ([a-z0-9-]+)\s*$")


def summarize(lines):
    providers = collections.defaultdict(set)
    outcomes = {}
    for line in lines:
        event = json.loads(line)
        test = event.get("Test")
        if not test:
            continue
        key = (event["Package"], test)
        if event["Action"] == "output":
            match = PROVIDER.search(event.get("Output", ""))
            if match:
                providers[key].add(match[1])
        elif event["Action"] in ("pass", "fail", "skip"):
            outcomes[key] = event["Action"]

    counts = collections.defaultdict(collections.Counter)
    for test, names in providers.items():
        for name in names:
            counts[name][outcomes.get(test, "incomplete")] += 1
    if not counts:
        return "## Live providers\n\nNo provider-marked live tests were selected or reached.\n"
    result = [
        "## Live providers", "",
        "Counts describe tests, not API requests. A failed shared test can reference more than one provider.", "",
        "| Provider | State | Passed | Failed | Skipped | Incomplete |",
        "|---|---|---:|---:|---:|---:|",
    ]
    for name, count in sorted(counts.items()):
        state = "ran" if count["pass"] + count["fail"] else "skipped"
        if count["incomplete"]:
            state = state + "; incomplete" if count["pass"] + count["fail"] + count["skip"] else "incomplete"
        result.append(
            f"| {name} | {state} | {count['pass']} | {count['fail']} | {count['skip']} | {count['incomplete']} |"
        )
    result.extend(["", "Skipped tests do not prove live-provider acceptance.", ""])
    return "\n".join(result)


if __name__ == "__main__":
    with open(sys.argv[1], encoding="utf-8") as events:
        print(summarize(events), end="")

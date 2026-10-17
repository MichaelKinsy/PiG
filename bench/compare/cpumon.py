#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Records host load while a suite runs: every --every seconds one JSON line with the load average and the busy fraction (all work, ours
included) of the pinned cores and of the whole host. Lets a report say how contended the pinned cores were."""
import argparse
import json
import os
import time


def cores(spec):
    out = []
    for part in spec.split(","):
        a, _, b = part.partition("-")
        out += range(int(a), int(b or a) + 1)
    return out


def snap():
    r = {}
    for line in open("/proc/stat"):
        if line.startswith("cpu") and line[3].isdigit():
            p = line.split()
            v = list(map(int, p[1:]))
            r[int(p[0][3:])] = (sum(v), v[3] + v[4])
    return r


ap = argparse.ArgumentParser()
ap.add_argument("--cpus", required=True)
ap.add_argument("--out", required=True)
ap.add_argument("--every", type=float, default=10)
a = ap.parse_args()
pin = cores(a.cpus)
prev = snap()
while True:
    time.sleep(a.every)
    cur = snap()
    busy = lambda ids: round(1 - sum(cur[c][1] - prev[c][1] for c in ids) / max(1, sum(cur[c][0] - prev[c][0] for c in ids)), 3)
    with open(a.out, "a") as f:
        f.write(json.dumps({"t": round(time.time()), "load1": os.getloadavg()[0], "pinned_busy": busy(pin), "host_busy": busy(list(cur))}) + "\n")
    prev = cur

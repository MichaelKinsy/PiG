#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Streaming CPU by answer shape and wire API. Same method as Pi-Bolt's bench/long_answer.py (its Tty harness and its fake model,
24-character chunks every --pace-ms), but the answer is one of four shapes (prose, code blocks, tables, lists) written to a file and
streamed through the mock's `mdfile:` scenario, over either wire API (`--model fake/fake-model` = Chat Completions,
`fake-anthropic/fake-model` = Anthropic Messages). Each answer size runs in one TUI session, smallest first. Tasks (round, build,
shape, api) run one per core in --cpus, each pinned to its core; builds are interleaved and their order is rotated every
round. Rows go to --out as JSON lines (one per sample), so a partial run is usable. A sample that did not run is a row with "error".

  stream_shapes.py --root ROOT --arms pig-tip,pibolt-0.7.4,pi-node,rpi,omp --aa --cpus 129,130,131 --out shapes.jsonl
  shapes_report.py shapes.jsonl --base pig-tip

--aa adds an A/A copy `<name>-aa` of every build: the same command, run as its own build, so the report can state the run-to-run
floor of each arm. The harness comes from ROOT/src/Pi-Bolt/bench (arms.py install puts it there)."""
import argparse
import concurrent.futures as cf
import hashlib
import itertools
import json
import os
import queue
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

import cleanenv

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import ctx_scale as cs  # noqa: E402
import fake_server  # noqa: E402

WORDS = ("stream render buffer token layout width column cursor frame paint scroll cache parse chunk delta yield flush "
         "queue event handler session model prompt answer window terminal screen style border margin theme").split()


def sentence(n, k):
    w = [WORDS[(n * 7 + k * 3 + j * 5) % len(WORDS)] for j in range(9 + (n + k) % 6)]
    return f"{w[0].capitalize()} {' '.join(w[1:])} {n * 31 + k}."


def prose(n):
    return f"## Section {n}\n\n" + " ".join(sentence(n, k) for k in range(5)) + "\n\n" + " ".join(sentence(n + 1000, k) for k in range(4)) + "\n\n"


def code(n):
    return (f"### Function {n}\n\nThe helper below handles case {n}.\n\n```ts\nexport function parse{n}(input: string): number[] {{\n"
            f"\tconst out: number[] = [];\n\tfor (let i = 0; i < input.length; i++) {{\n\t\tif (input.charCodeAt(i) === {40 + n % 50}) out.push(i * {n});\n\t}}\n"
            f"\treturn out;\n}}\n```\n\nIt returns the offsets found.\n\n").replace("{n}", str(n))


def table(n):
    rows = "\n".join(f"| item{n}-{r} | {r * n % 997} | {sentence(n, r)[:40]} | {'yes' if r % 2 else 'no'} |" for r in range(8))
    return f"### Table {n}\n\n| name | value | note | flag |\n|---|---:|---|:---:|\n{rows}\n\n"


def lists(n):
    out = [f"### List {n}\n"]
    for i in range(5):
        out.append(f"- Item {n}.{i}: {sentence(n, i)[:50]}")
        out += [f"  - Sub {i}.{j}: {sentence(i, j)[:40]}" for j in range(3)]
    out += [f"{i + 1}. Step {i + 1} of {n}: {sentence(n, i + 9)[:45]}" for i in range(4)]
    return "\n".join(out) + "\n\n"


SHAPES = {"prose": prose, "code": code, "tables": table, "lists": lists}
APIS = {"completions": "fake/fake-model", "anthropic": "fake-anthropic/fake-model"}


def body(shape, size):
    text, n = "", 0
    while len(text) < size:
        n += 1
        text += SHAPES[shape](n)
    return text[:size]


def round_order(builds, r):
    """Every build once per round, the order rotated by one each round."""
    return [builds[(i + r) % len(builds)] for i in range(len(builds))]


def one(h, build, shape, api, sizes, cpu, pace, files):
    row = {"build": build.name, "shape": shape, "api": api, "cpu_core": cpu}
    out = []
    with fake_server.fake_model(h, pace_ms=pace, script="fake_model_stress.py") as port, h.pi_home(port) as home, h.workdir() as cwd:
        tty = h.Tty(h.pinned([*build.argv, "--no-session", "--model", APIS[api]], str(cpu)), h.pi_env(home), cwd, cols=160, rows=48)
        try:
            if not tty.wait_for("fake-model", 0, time.perf_counter() + 90):
                return [{**row, "size": size, "error": "tui did not start"} for size in sizes]
            tty.settle(0.5, time.perf_counter() + 5)
            for answer, size in enumerate(sizes, 1):
                start, c0, began = len(tty.buf), h.cpu_ms(tty.pid), time.perf_counter()
                tty.send(f"SCENARIO mdfile:{files[(shape, size)]} CWD {cwd}".encode() + b"\r")
                if not tty.wait_for(f"Done: mdfile, answer {answer}.", start, time.perf_counter() + 1800):
                    # The later sizes of this session are lost too: they are rows, so the report counts them.
                    out.append({**row, "size": size, "error": "no answer"})
                    out += [{**row, "size": s, "error": "not run: an earlier answer in the session failed"} for s in sizes[answer:]]
                    break
                wall = time.perf_counter() - began
                tty.settle(0.3, time.perf_counter() + 5)
                out.append({**row, "size": size, "wall_s": round(wall, 2), "cpu_s": round((h.cpu_ms(tty.pid) - c0) / 1000, 3),
                            "own_mb": round(h.memory_mb(tty.pid).get("own", 0))})
        finally:
            tty.quit()
    return out


def arm_builds(h, root, arms, extra, aa):
    specs = {}
    if arms:
        lines = subprocess.run([sys.executable, str(HERE / "arms.py"), "specs", "--root", str(root), *[x for n in arms for x in ("--arm", n)]],
                               capture_output=True, text=True, check=True, timeout=600).stdout.splitlines()
        specs = dict(line.split("=", 1) for line in lines)
    named = [f"{n}={specs[n]}" for n in arms] + list(extra)
    if aa:
        named += [f"{s.split('=', 1)[0]}-aa={s.split('=', 1)[1]}" for s in named]
    return h.parse_builds(named)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", required=True, type=Path, help="arms root (arms.py install --root)")
    ap.add_argument("--arms", default="", help="comma list of arms.lock arms")
    ap.add_argument("--build", action="append", default=[], help="extra name=command (repeatable)")
    ap.add_argument("--env", action="append", default=[], help="KEY=VALUE added to every build's environment after the clean-up (repeatable; e.g. PIG_PROFILE=cpu,allocs)")
    ap.add_argument("--aa", action="store_true", help="add an A/A copy <name>-aa of every build")
    ap.add_argument("--shapes", default=",".join(SHAPES))
    ap.add_argument("--apis", default=",".join(APIS))
    ap.add_argument("--sizes", default="6000,60000", help="N and 10N characters")
    ap.add_argument("--pace-ms", type=float, default=20)
    ap.add_argument("--cpus", required=True, help="comma list of cores, one worker each (e.g. 129,130,131)")
    ap.add_argument("--rounds", type=int, default=12)
    ap.add_argument("--first-round", type=int, default=0)
    ap.add_argument("--out", required=True)
    a = ap.parse_args()
    dropped = cleanenv.apply()
    print(f"clean env: dropped {len(dropped)} variables ({', '.join(dropped)}); GOMAXPROCS unset: each arm sizes itself from its pinned cores", file=sys.stderr, flush=True)
    for kv in a.env:
        k, _, v = kv.partition("=")
        os.environ[k] = v
    root = a.root.resolve()
    h = cs.load_harness(root)
    builds = arm_builds(h, root, [x for x in a.arms.split(",") if x], a.build, a.aa)
    if not builds:
        sys.exit("no builds: give --arms and/or --build")
    sizes = [int(s) for s in a.sizes.split(",")]
    shapes, apis = a.shapes.split(","), a.apis.split(",")
    tmp = Path(tempfile.mkdtemp(prefix="shapes-"))
    files = {}
    for shape, size in itertools.product(shapes, sizes):
        p = tmp / f"{shape}-{size}.md"
        p.write_text(body(shape, size))
        files[(shape, size)] = str(p)
    free = queue.Queue()
    for c in a.cpus.split(","):
        free.put(int(c))
    lock = threading.Lock()

    def task(r, build, shape, api):
        cpu = free.get()
        try:
            rows = one(h, build, shape, api, sizes, cpu, a.pace_ms, files)
        except Exception as e:  # a failed sample is a recorded sample
            rows = [{"build": build.name, "shape": shape, "api": api, "size": size, "error": repr(e)} for size in sizes]
        finally:
            free.put(cpu)
        with lock, open(a.out, "a") as f:
            for row in rows:
                f.write(json.dumps({"round": r, **row}) + "\n")

    with cf.ThreadPoolExecutor(max_workers=len(a.cpus.split(","))) as ex:
        futs = []
        for r in range(a.first_round, a.first_round + a.rounds):
            for build in round_order(builds, r):
                for shape, api in itertools.product(shapes, apis):
                    futs.append(ex.submit(task, r, build, shape, api))
        for f in futs:
            f.result()
    print("sha256 of shape files:", {k[0] + str(k[1]): hashlib.sha256(Path(v).read_bytes()).hexdigest()[:12] for k, v in files.items()})


if __name__ == "__main__":
    main()

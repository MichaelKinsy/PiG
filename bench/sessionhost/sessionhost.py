#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Memory of N concurrent RPC sessions: one `pig --mode rpc --listen` process (arm `listen`) against N `pig --mode rpc` processes (arm `rpc`).

Both arms drive N sessions through the same scripted model (scripted_model.py), TURNS prompts each (the five prompts of jcode's
scripts/bench_headless_memory.py), and sample the memory of the whole process tree after every turn. With --extensions the agent directory
also holds the two extension fixtures of coding/cli/testdata (a Go and a Node counter), so every session loads both.
Every process the bench starts gets its own scratch PIG_CODING_AGENT_DIR, PI_CODING_AGENT_DIR, PIG_HOME and PI_HOME.
"""
import argparse
import json
import os
import re
import select
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = HERE.parent.parent
sys.path.insert(0, str(REPO / "bench" / "compare"))
import fake_server  # noqa: E402

TURNS = [
    "Use a tool to list the files in the current directory, then reply with just the number of entries.",
    "Read the first 20 lines of README.md with a tool and reply with its first heading only.",
    "Search the repository for the string 'fn main' with a tool and reply with how many files matched.",
    "In one sentence, summarize what you have learned about this repository so far.",
    "Reply with exactly the word: done",
]
DARWIN = sys.platform == "darwin"
TURN_TIMEOUT = 180


class Chan:
    """A line-oriented channel over a socket or a pipe pair, with a read deadline."""

    def __init__(self, rfd, write, close):
        self.rfd, self.write, self.close, self.buf = rfd, write, close, b""

    def send(self, obj):
        self.write((json.dumps(obj) + "\n").encode())

    def readline(self, deadline):
        while b"\n" not in self.buf:
            left = deadline - time.time()
            if left <= 0:
                raise TimeoutError("no record before the deadline")
            if not select.select([self.rfd], [], [], left)[0]:
                continue
            data = os.read(self.rfd, 65536)
            if not data:
                raise EOFError("stream closed")
            self.buf += data
        line, self.buf = self.buf.split(b"\n", 1)
        return json.loads(line)


def run_turn(chan, prompt):
    """Sends one prompt and reads to its agent_end; returns an error string or None."""
    deadline = time.time() + TURN_TIMEOUT
    try:
        chan.send({"type": "prompt", "message": prompt})
        while True:
            rec = chan.readline(deadline)
            if rec.get("type") == "response" and rec.get("success") is False:
                return f"prompt refused: {rec.get('error')}"
            if rec.get("type") == "message_end" and (rec.get("message") or {}).get("stopReason") in ("error", "aborted"):
                return f"assistant stopped: {rec['message'].get('errorMessage') or rec['message']['stopReason']}"
            if rec.get("type") == "agent_end":
                return None
    except (TimeoutError, EOFError, OSError, ValueError) as e:
        return f"{type(e).__name__}: {e}"


def process_table():
    out = subprocess.run(["ps", "-axo", "pid=,ppid=,rss=,command="], capture_output=True, text=True, check=True).stdout
    table = {}
    for line in out.splitlines():
        f = line.split(None, 3)
        if len(f) == 4:
            table[int(f[0])] = (int(f[1]), int(f[2]) * 1024, f[3])
    return table


def tree(table, roots):
    """Roots and all their descendants."""
    kids = {}
    for pid, (ppid, _, _) in table.items():
        kids.setdefault(ppid, []).append(pid)
    seen, todo = [], list(roots)
    while todo:
        pid = todo.pop()
        if pid in table and pid not in seen:
            seen.append(pid)
            todo += kids.get(pid, [])
    return seen


def private_bytes(pids):
    """phys_footprint (macOS) or PSS (Linux) summed over pids."""
    if DARWIN:
        total = 0
        for i in range(0, len(pids), 64):
            out = subprocess.run(["footprint", "--noCategories", "-f", "bytes", *map(str, pids[i:i + 64])],
                                 capture_output=True, text=True).stdout
            m = re.search(r"Summary Footprint: (\d+) B", out)
            total += int(m.group(1)) if m else sum(int(x) for x in re.findall(r"Footprint: (\d+) B", out))
        return total
    total = 0
    for pid in pids:
        try:
            for line in Path(f"/proc/{pid}/smaps_rollup").read_text().splitlines():
                if line.startswith("Pss:"):
                    total += int(line.split()[1]) * 1024
        except FileNotFoundError:
            pass  # the process exited between the listing and the read; any other error is a measurement failure
    return total


def sample(roots):
    table = process_table()
    pids = tree(table, roots)
    return {"processes": len(pids), "extension_processes": len(pids) - len(roots),
            "rss": sum(table[p][1] for p in pids), "private": private_bytes(pids)}


def requests_served(port):
    with urllib.request.urlopen(f"http://127.0.0.1:{port}/count", timeout=10) as r:
        return json.load(r)["requests"]


def requests_with_fixtures(port):
    with urllib.request.urlopen(f"http://127.0.0.1:{port}/count", timeout=10) as r:
        return json.load(r)["with_fixtures"]


def setup_home(root, port, extensions, workspace):
    agent = root / "agent"
    agent.mkdir(parents=True)
    for d in ("piagent", "home", "pihome"):
        (root / d).mkdir()
    (agent / "models.json").write_text(json.dumps({"providers": {"fake": {
        "baseUrl": f"http://127.0.0.1:{port}/v1", "api": "openai-completions", "apiKey": "fake",
        "models": [{"id": "fake-model", "contextWindow": 128000, "maxTokens": 8192}]}}}))
    (agent / "settings.json").write_text(json.dumps({"lastChangelogVersion": "9999.0.0", "theme": "dark", "quietStartup": True}))
    (agent / "auth.json").write_text("{}")
    if extensions:
        testdata = REPO / "coding" / "cli" / "testdata"
        goext = agent / "extensions" / "listen-counter-go"
        goext.mkdir(parents=True)
        shutil.copy(testdata / "listen-counter-go" / "extension.go", goext)
        gomod = (testdata / "listen-counter-go" / "go.mod").read_text().replace("../../../../extensions/sdk", str(REPO / "extensions" / "sdk"))
        (goext / "go.mod").write_text(gomod)
        nodeext = agent / "extensions" / "listen-counter"
        nodeext.mkdir()
        shutil.copy(testdata / "listen-counter.mjs", nodeext / "index.js")
    (workspace / "README.md").write_text("# Bench workspace\n\nA README for the scripted read tool.\n")


def env_for(root):
    env = dict(os.environ)
    env.update(PIG_CODING_AGENT_DIR=str(root / "agent"), PI_CODING_AGENT_DIR=str(root / "piagent"),
               PIG_HOME=str(root / "home"), PI_HOME=str(root / "pihome"), LISTEN_FIXTURE_LOG=str(root / "shutdown.log"))
    return env


def run_arm(arm, n, args, root, workspace, port):
    base = [str(Path(args.pig).resolve()), "--mode", "rpc", "--model", "fake/fake-model", "--no-session"]
    env = env_for(root)
    procs, chans, sock_path = [], [], None
    served0 = requests_served(port)
    fixtures0 = requests_with_fixtures(port)
    try:
        if arm == "listen":
            sock_path = tempfile.mkdtemp(prefix="sh-") + "/pig.sock"
            procs.append(subprocess.Popen(base + ["--listen", sock_path], cwd=workspace, env=env, stdin=subprocess.DEVNULL,
                                          stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL))
            for _ in range(600):
                if os.path.exists(sock_path):
                    break
                time.sleep(0.05)
            for _ in range(n):
                s = socket.socket(socket.AF_UNIX)
                s.connect(sock_path)
                chans.append(Chan(s.fileno(), s.sendall, s.close))
                chans[-1].sock = s
        else:
            for _ in range(n):
                p = subprocess.Popen(base, cwd=workspace, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, bufsize=0)
                procs.append(p)
                chans.append(Chan(p.stdout.fileno(), p.stdin.write, p.stdin.close))
        roots = [p.pid for p in procs]
        points, errors = [], []
        time.sleep(1)
        points.append({"turn": 0, **sample(roots)})
        for i, prompt in enumerate(TURNS[:args.turns], 1):
            with ThreadPoolExecutor(max_workers=max(1, n)) as pool:
                for e in pool.map(lambda c: run_turn(c, prompt), chans):
                    if e:
                        errors.append(f"turn {i}: {e}")
            time.sleep(0.5)
            points.append({"turn": i, **sample(roots)})
        served = requests_served(port) - served0
        if served != 2 * args.turns * n:
            errors.append(f"model requests {served}, want {2 * args.turns * n} (2 per turn per session)")
        if getattr(args, "extensions", False):
            with_fixtures = requests_with_fixtures(port) - fixtures0
            if with_fixtures != served:
                errors.append(f"model requests carrying both fixture tools {with_fixtures}, want {served} (a session did not load both extensions)")
        return {"arm": arm, "sessions": n, "points": points, "model_requests": served, "errors": errors}
    finally:
        for c in chans:
            try:
                c.close()
            except OSError:
                pass
        for p in procs:
            if arm == "listen":
                p.send_signal(signal.SIGTERM)
            try:
                p.wait(timeout=60)
            except subprocess.TimeoutExpired:
                p.kill()
                p.wait()
        if sock_path:
            shutil.rmtree(os.path.dirname(sock_path), ignore_errors=True)


def slopes(runs, arm):
    rs = sorted((r for r in runs if r["arm"] == arm), key=lambda r: r["sessions"])
    if len(rs) < 2:
        return None
    a, b = rs[0], rs[-1]
    d = b["sessions"] - a["sessions"]
    return {k: (b["points"][-1][k] - a["points"][-1][k]) / d for k in ("private", "rss")} | {"from": a["sessions"], "to": b["sessions"]}


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--pig", required=True)
    ap.add_argument("--sessions", type=int, nargs="+", default=[1, 10, 50, 100])
    ap.add_argument("--turns", type=int, default=5, choices=range(1, 6))
    ap.add_argument("--extensions", action="store_true")
    ap.add_argument("--arms", nargs="+", default=["listen", "rpc"], choices=["listen", "rpc"])
    ap.add_argument("--json-out", required=True)
    args = ap.parse_args()
    runs = []
    with fake_server.serve(HERE / "scripted_model.py") as port:
        scratch = Path(tempfile.mkdtemp(prefix="sessionhost-"))
        try:
            workspace = scratch / "workspace"
            workspace.mkdir()
            root = scratch / "root"
            setup_home(root, port, args.extensions, workspace)
            if args.extensions:  # warm the Go extension's build cache so no arm pays for it
                warm = run_arm("rpc", 1, argparse.Namespace(pig=args.pig, turns=1, extensions=True), root, workspace, port)
                if warm["errors"]:
                    sys.exit("warm-up failed, the measured arms would pay for the build: " + "; ".join(warm["errors"]))
            for n in args.sessions:
                for arm in args.arms:
                    r = run_arm(arm, n, args, root, workspace, port)
                    runs.append(r)
                    last = r["points"][-1]
                    print(f"{arm:6} N={n:<4} private={last['private'] / 1e6:8.1f} MB rss={last['rss'] / 1e6:8.1f} MB "
                          f"processes={last['processes']} ext={last['extension_processes']} errors={len(r['errors'])}", flush=True)
        finally:
            shutil.rmtree(scratch, ignore_errors=True)
    out = {"platform": sys.platform, "private_kind": "phys_footprint" if DARWIN else "pss", "extensions": args.extensions, "turns": args.turns,
           "prompts": TURNS[:args.turns], "runs": runs, "slope_bytes_per_session": {a: slopes(runs, a) for a in args.arms}}
    Path(args.json_out).write_text(json.dumps(out, indent=2))
    print(json.dumps(out["slope_bytes_per_session"]))
    bad = [r for r in runs if r["errors"]]
    for r in bad:
        print(f"ERRORS {r['arm']} N={r['sessions']}: {r['errors'][:3]}", file=sys.stderr)
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Record isolated startup trials. Timing budgets are advisory in CI, never a blocking unit assertion."""
import argparse
import hashlib
import json
import math
import os
import pathlib
import re
import selectors
import shutil
import signal
import statistics
import subprocess
import tempfile
import time

ROOT = pathlib.Path(__file__).resolve().parents[2]
PROMPT = "What is 20+22?"
CASES = ("none", "ts", "package")


def environment(home):
    for name in ("cwd", "pig/agent", "pi-agent", "cache", "config", "data", "tmp"):
        (home / name).mkdir(parents=True, exist_ok=True)
    temporary = home.parent / ("t-" + hashlib.sha256(str(home).encode()).hexdigest()[:12])
    temporary.mkdir(parents=True, exist_ok=True)
    return {
        "PATH": os.environ["PATH"], "HOME": str(home), "USERPROFILE": str(home),
        "PIG_HOME": str(home / "pig"), "PIG_CODING_AGENT_DIR": str(home / "pig/agent"),
        "PI_CODING_AGENT_DIR": str(home / "pi-agent"), "TMPDIR": str(temporary),
        "XDG_CACHE_HOME": str(home / "cache"), "XDG_CONFIG_HOME": str(home / "config"),
        "XDG_DATA_HOME": str(home / "data"), "PIG_TEST_FAUX": "1", "PIG_TEST_FAUX_SCENARIO": "parity-basic",
        "PIG_OFFLINE": "1", "PI_OFFLINE": "1", "PI_TELEMETRY": "0", "NO_COLOR": "1", "TERM": "dumb", "LC_ALL": "C",
    }


def command(binary, label, case, home, fixtures, rpc=False):
    args = [binary, "--offline", "--no-session", "--model", "test-faux/faux-1"]
    if label == "pi":
        args += ["-e", str(ROOT / "test/parity/testdata/test-faux-provider.ts")]
    if case == "package":
        settings = json.dumps({"packages": [str(fixtures / "package")]})
        (home / "pig/agent/settings.json").write_text(settings)
        (home / "pi-agent/settings.json").write_text(settings)
    else:
        args += ["--no-extensions"]
        if case == "ts":
            args += ["-e", str(fixtures / "alpha.ts")]
    if rpc:
        return args + ["--mode", "rpc"]
    return args + ["--mode", "json", "-p", PROMPT]


def trial(binary, label, case, cache, index, home, fixtures, output, rpc=False):
    env = environment(home)
    argv = command(binary, label, case, home, fixtures, rpc)
    started = time.perf_counter_ns()
    process = subprocess.Popen(argv, cwd=home / "cwd", env=env, stdin=subprocess.PIPE if rpc else subprocess.DEVNULL,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
    streams = {"stdout": bytearray(), "stderr": bytearray()}
    selector = selectors.DefaultSelector()
    selector.register(process.stdout, selectors.EVENT_READ, "stdout")
    selector.register(process.stderr, selectors.EVENT_READ, "stderr")
    if rpc:
        process.stdin.write(b'{"id":"startup-commands","type":"get_commands"}\n')
        process.stdin.flush()
    pending = bytearray()
    first_assistant = None
    ended = False
    answer = None
    commands = None
    timed_out = False
    malformed = False
    deadline = time.monotonic() + 15
    try:
        while selector.get_map():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                timed_out = True
                os.killpg(process.pid, signal.SIGKILL)
                break
            for key, _ in selector.select(remaining):
                chunk = os.read(key.fileobj.fileno(), 65536)
                now = time.perf_counter_ns()
                if not chunk:
                    selector.unregister(key.fileobj)
                    continue
                streams[key.data].extend(chunk)
                if key.data != "stdout":
                    continue
                pending.extend(chunk)
                while b"\n" in pending:
                    line, _, pending = pending.partition(b"\n")
                    try:
                        event = json.loads(line)
                    except (ValueError, UnicodeDecodeError):
                        malformed = True
                        continue
                    if not isinstance(event, dict):
                        malformed = True
                        continue
                    if rpc and event.get("id") == "startup-commands":
                        commands = event
                        process.stdin.close()
                    message = event.get("message") or {}
                    if not isinstance(message, dict):
                        malformed = True
                        continue
                    if event.get("type") == "message_start" and message.get("role") == "assistant" and first_assistant is None:
                        first_assistant = (now - started) / 1e6
                    if event.get("type") == "message_end" and message.get("role") == "assistant":
                        answer = "".join(block.get("text", "") for block in message.get("content", []) if block.get("type") == "text")
                    if event.get("type") == "agent_end":
                        ended = True
        try:
            process.wait(timeout=max(0, deadline - time.monotonic()))
        except subprocess.TimeoutExpired:
            timed_out = True
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
    finally:
        selector.close()
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGKILL)
            process.wait()
        for stream in (process.stdout, process.stderr, process.stdin if rpc else None):
            if stream is not None and not stream.closed:
                stream.close()
    stem = f"{label}-{case}-{cache}-{index}" + ("-registration" if rpc else "")
    for name, data in streams.items():
        (output / f"{stem}.{name}").write_bytes(data)
    valid = not timed_out and not malformed and process.returncode == 0
    if rpc:
        expected = "alpha-entry" if case == "ts" else "widget-factory-probe"
        valid = valid and commands is not None and commands.get("success") is True and expected in json.dumps(commands)
    else:
        valid = valid and first_assistant is not None and ended and answer == "42"
    if any(word in streams["stderr"].lower() for word in (b"error", b"failed", b"cannot")):
        valid = False
    return {"build": label, "kind": case, "cache": cache, "run": index, "registration": rpc,
            "request_ms": first_assistant, "total_ms": (time.perf_counter_ns() - started) / 1e6,
            "valid": valid, "timeout": timed_out, "malformed_json": malformed, "returncode": process.returncode,
            "command": argv, "stdout": f"{stem}.stdout", "stderr": f"{stem}.stderr"}


def summarize(rows, budgets):
    groups = {}
    for row in rows:
        if isinstance(row["run"], int) and not row["registration"]:
            groups.setdefault((row["build"], row["kind"], row["cache"]), []).append(row["request_ms"])
    summary = []
    for (build, kind, cache), values in groups.items():
        entry = {"build": build, "kind": kind, "cache": cache, "runs": len(values),
                 "median_ms": statistics.median(values), "p90_ms": sorted(values)[math.ceil(.9 * (len(values) - 1))]}
        if build == "pig":
            reference = "baseline" if kind == "none" else "pi"
            if (reference, kind, cache) in groups:
                entry["ratio"] = entry["median_ms"] / statistics.median(groups[reference, kind, cache])
                entry["budget"] = budgets["no_extensions_over_baseline" if kind == "none" else "extension_over_pi"]
                entry["within_budget"] = entry["ratio"] <= entry["budget"]
        summary.append(entry)
    return summary


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--pig", required=True)
    parser.add_argument("--pi", required=True)
    parser.add_argument("--baseline", required=True)
    parser.add_argument("--runs", type=int, default=25)
    parser.add_argument("--out", required=True)
    args = parser.parse_args()
    if args.runs < 1:
        parser.error("--runs must be positive")
    output = pathlib.Path(args.out).resolve()
    output.mkdir(parents=True, exist_ok=True)
    builds = {name: str(pathlib.Path(value).resolve()) for name, value in
              (("baseline", args.baseline), ("pig", args.pig), ("pi", args.pi))}
    provenance = {"source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
                  "runs": args.runs, "binaries": {name: {"path": path, "sha256": hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()}
                                                for name, path in builds.items()},
                  "node": subprocess.check_output(["node", "--version"], text=True).strip(),
                  "go": subprocess.check_output(["go", "version"], text=True).strip()}
    (output / "provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")
    budgets = json.loads((ROOT / "automation/perf/startup-budgets.json").read_text())
    rows = []
    with tempfile.TemporaryDirectory(prefix="pig-startup-") as directory, (output / "samples.jsonl").open("w") as log:
        root = pathlib.Path(directory)
        version_home = root / "versions"
        version_env = environment(version_home)
        versions = {name: subprocess.check_output([binary, "--version"], cwd=version_home / "cwd", env=version_env, text=True, timeout=15).strip()
                    for name, binary in builds.items()}
        pin = re.search(r'const UpstreamVersion = "([^"]+)"', (ROOT / "internal/coding/pigversion/pigversion.go").read_text()).group(1)
        if versions["pi"] != pin:
            raise SystemExit(f"Pi oracle version {versions['pi']} does not match {pin}")
        provenance["versions"] = versions
        (output / "provenance.json").write_text(json.dumps(provenance, indent=2) + "\n")
        fixtures = root / "fixtures"
        shutil.copytree(ROOT / "automation/perf/testdata/startup", fixtures)
        for case in CASES:
            selected = [name for name in builds if name != "pi" or case != "none"]
            for name in selected:
                if case != "none":
                    row = trial(builds[name], name, case, "cold", "registration", root / f"verify-{name}-{case}", fixtures, output, True)
                    log.write(json.dumps(row) + "\n"); log.flush()
                    if not row["valid"]:
                        raise SystemExit(f"fixture registration failed: {row}")
                for i in range(3):
                    row = trial(builds[name], name, case, "warm", f"seed-{i}", root / f"warm-{name}-{case}", fixtures, output)
                    log.write(json.dumps(row) + "\n"); log.flush()
                    if not row["valid"]:
                        raise SystemExit(f"warm seed failed: {row}")
            for cache in ("warm", "cold"):
                for i in range(args.runs):
                    order = selected[i % len(selected):] + selected[:i % len(selected)]
                    for name in order:
                        home = root / (f"warm-{name}-{case}" if cache == "warm" else f"cold-{name}-{case}-{i}")
                        row = trial(builds[name], name, case, cache, i, home, fixtures, output)
                        rows.append(row); log.write(json.dumps(row) + "\n"); log.flush()
                        if not row["valid"]:
                            raise SystemExit(f"startup failed without retry: {row}")
    summary = summarize(rows, budgets)
    (output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    lines = ["# Startup timing", "", f"Source: `{provenance['source_commit']}`. Each cell has {args.runs} runs. Application caches are cold/warm; OS caches are not dropped.", "",
             "| Build | Workload | Cache | Median ms | p90 ms | Ratio / budget |", "|---|---|---|---:|---:|---:|"]
    for row in summary:
        ratio = f"{row['ratio']:.3f} / {row['budget']:.3f}" if "ratio" in row else "—"
        lines.append(f"| {row['build']} | {row['kind']} | {row['cache']} | {row['median_ms']:.2f} | {row['p90_ms']:.2f} | {ratio} |")
    (output / "summary.md").write_text("\n".join(lines) + "\n")
    # The workflow marks this job non-blocking. Preserve a nonzero outcome so a miss is visible.
    return 2 if any(row.get("within_budget") is False for row in summary) else 0


if __name__ == "__main__":
    raise SystemExit(main())

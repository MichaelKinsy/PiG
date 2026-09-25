#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Verify local quickstart commands with a fresh HOME and the existing Go toolchain."""
import hashlib
import json
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile


def verify(pig):
    goroot = subprocess.check_output(["go", "env", "GOROOT"], text=True).strip()
    gocache = subprocess.check_output(["go", "env", "GOCACHE"], text=True).strip()
    gopath = subprocess.check_output(["go", "env", "GOPATH"], text=True).strip()
    work = Path(tempfile.mkdtemp(prefix="pig-quickstart-check."))
    home = work / "home"
    demo = home / "demo"
    demo.mkdir(parents=True)
    env = {
        "HOME": str(home), "PATH": f"{goroot}/bin:/usr/local/bin:/usr/bin:/bin",
        "GOROOT": goroot, "GOCACHE": gocache, "GOPATH": gopath,
        "GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off",
        "PIG_OFFLINE": "1", "PI_OFFLINE": "1", "PI_TELEMETRY": "0", "PIG_TEST_FAUX": "1",
    }

    def run(name, *args, expected=0):
        result = subprocess.run([str(pig), *args], cwd=demo, env=env, stdin=subprocess.DEVNULL, capture_output=True, text=True, timeout=120)
        (work / f"{name}.stdout").write_text(result.stdout)
        (work / f"{name}.stderr").write_text(result.stderr)
        print(f"$ pig {shlex.join(args)}\nexit={result.returncode}")
        assert result.returncode == expected, result.stderr
        return result.stdout, result.stderr

    version, _ = run("version", "--version")
    print(version, end="")
    answer, errors = run("question", "--model", "test-faux/faux-1", "-p", "What is 20+22?")
    assert answer == "42\n" and errors == "", (answer, errors)
    print(answer, end="")
    trace, errors = run("tool", "--model", "test-faux/faux-1", "--mode", "json", "-p", "Run: expr 20 + 22")
    assert errors == "", errors
    events = [json.loads(line) for line in trace.splitlines()]
    starts = [e for e in events if e["type"] == "tool_execution_start"]
    ends = [e for e in events if e["type"] == "tool_execution_end"]
    assert len(starts) == len(ends) == 1, "the tutorial requests exactly one Bash execution"
    assert starts[0]["toolName"] == "bash" and starts[0]["args"] == {"command": "expr 20 + 22"}
    assert starts[0]["toolCallId"] == ends[0]["toolCallId"]
    assert ends[0]["isError"] is False
    assert ends[0]["result"]["content"] == [{"type": "text", "text": "42\n"}]
    assert events.index(starts[0]) < events.index(ends[0])
    for event in (starts[0], ends[0]):
        print(json.dumps(event))
    scaffold, errors = run("scaffold", "extension", "init", "./hello", "--lang", "go")
    assert 'Created go extension "hello"' in scaffold and errors == "", (scaffold, errors)
    print(scaffold, end="")
    trace, errors = run("extension", "-e", "./hello", "--model", "test-faux/faux-1", "--mode", "json", "-p", "What is 20+22?")
    assert errors == "", errors
    events = [json.loads(line) for line in trace.splitlines()]
    tools = [tool["name"] for event in events for tool in event.get("message", {}).get("toolsAdded", [])]
    assert "hello_ping" in tools, "explicit -e did not activate the scaffold's tool"
    print("PASS: hello_ping is in the active model tool schemas")
    before = hashlib.sha256(pig.read_bytes()).digest()
    output, errors = run("update", "update", expected=1)
    print(output + errors, end="")
    assert "cannot self-update this installation" in output + errors
    assert hashlib.sha256(pig.read_bytes()).digest() == before, "update changed the source binary"
    assert list((home / ".pig/agent/sessions").rglob("*.jsonl")), "no persisted sessions"
    print(f"PASS: local quickstart; evidence retained in {work}")


if __name__ == "__main__":
    verify(Path(sys.argv[1]).resolve())

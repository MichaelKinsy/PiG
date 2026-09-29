#!/usr/bin/env python3
"""Real CLI probes for Session records and process wait status against Pi 0.87.1.

No record fields are discarded. Random entry identities are aliased injectively
only after validating their wire format; imported identities and times are fixed.
"""
import argparse
import contextlib
import fcntl
import http.server
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import tempfile
import termios
import threading
import time

ROOT = Path(__file__).resolve().parents[3]


def emit(value):
    print(json.dumps(value, sort_keys=True, separators=(",", ":")), flush=True)


def setup(root, url):
    home, agent, cwd = root / "home", root / "agent", root / "work"
    for path in (home, agent, cwd):
        path.mkdir()
    model = {"id": "wire-model", "name": "Wire model", "api": "openai-completions",
             "reasoning": True, "input": ["text"], "contextWindow": 128000,
             "maxTokens": 4096, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}
    (agent / "models.json").write_text(json.dumps({"providers": {"wire-audit": {
        "baseUrl": url, "api": "openai-completions", "apiKey": "not-a-secret", "models": [model]}}}))
    (agent / "auth.json").write_text("{}")
    (agent / "settings.json").write_text(json.dumps({"theme": "dark", "quietStartup": True,
        "enableInstallTelemetry": False, "compaction": {"enabled": False}, "retry": {"enabled": False}}))
    env = {"PATH": os.environ["PATH"], "HOME": str(home), "LANG": "C.UTF-8", "TERM": "xterm-256color",
           "PI_CODING_AGENT_DIR": str(agent), "PIG_CODING_AGENT_DIR": str(agent),
           "PIG_HOME": str(home / ".pig"), "PI_OFFLINE": "1", "PIG_OFFLINE": "1", "PI_TELEMETRY": "0"}
    args = ["--provider", "wire-audit", "--model", "wire-model", "--thinking", "medium",
            "--system-prompt", "Wire audit.", "--no-context-files", "--no-skills", "--no-extensions",
            "--no-prompt-templates", "--no-themes"]
    return cwd, env, args


@contextlib.contextmanager
def child(command, **kwargs):
    proc = subprocess.Popen(command, start_new_session=True, **kwargs)
    try:
        yield proc
    finally:
        if proc.poll() is None:
            os.killpg(proc.pid, signal.SIGKILL)
        proc.wait(timeout=10)
        for stream in (proc.stdin, proc.stdout, proc.stderr):
            if stream:
                stream.close()


def session_probe(binary, root):
    cwd, env, args = setup(root, "http://127.0.0.1:1/v1")
    with open(root / "stderr", "wb") as stderr, child([*binary, *args, "--mode", "rpc"], cwd=cwd, env=env,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=stderr, bufsize=0) as proc:
        counter = 0
        def command(kind, **fields):
            nonlocal counter
            counter += 1
            proc.stdin.write((json.dumps({"id": str(counter), "type": kind, **fields}) + "\n").encode())
            proc.stdin.flush()
            while True:
                assert select.select([proc.stdout], [], [], 15)[0], f"{kind}: timeout"
                line = proc.stdout.readline()
                assert line, f"{kind}: EOF"
                record = json.loads(line)
                if record.get("id") == str(counter):
                    assert record["success"], record
                    return record
        generated = command("get_entries")
        entries = generated["data"]["entries"]
        aliases = {}
        for entry in entries:
            assert re.fullmatch(r"[0-9a-f]{8}", entry["id"]), entry
            assert entry["id"] not in aliases, entry
            aliases[entry["id"]] = f"ENTRY{len(aliases)}"
            stamp = entry["timestamp"]
            assert re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z", stamp), entry
        for entry in entries:
            entry["id"] = aliases[entry["id"]]
            if entry["parentId"] is not None:
                entry["parentId"] = aliases[entry["parentId"]]
            entry["timestamp"] = "VALID_MILLISECOND_ISO"
        generated["data"]["leafId"] = aliases[generated["data"]["leafId"]]
        emit({"case": "generated", "record": generated})
        rows = []
        def add(kind, **fields):
            i = len(rows) + 1
            rows.append({"type": kind, "id": f"{i:08x}", "parentId": rows[-1]["id"] if rows else None,
                         "timestamp": f"2026-01-01T00:00:00.{i:03}Z", **fields})
            return rows[-1]["id"]
        add("model_change", provider="wire-audit", modelId="wire-model")
        add("thinking_level_change", thinkingLevel="medium")
        old = add("message", message={"role": "user", "content": "old", "timestamp": 1})
        boundary = add("label", targetId=old, label="obsolete")
        kept = add("message", message={"role": "user", "content": "kept", "timestamp": 2})
        add("compaction", summary="summary", firstKeptEntryId=boundary, tokensBefore=100)
        add("label", targetId=old)
        add("label", targetId=kept, label="checkpoint")
        add("message", message={"role": "user", "content": "after", "timestamp": 3})
        source = root / "source.jsonl"
        header = {"type": "session", "version": 3, "id": "01900000-0000-7000-8000-000000000000",
                  "timestamp": "2026-01-01T00:00:00.000Z", "cwd": str(cwd)}
        source.write_text("".join(json.dumps(row) + "\n" for row in [header, *rows]))
        command("switch_session", sessionPath=str(source))
        command("clone")
        result = command("get_entries")
        entries = result["data"]["entries"]
        # Only reconstructed label IDs are random; their original timestamps must survive unchanged.
        ids = {row["id"] for row in rows}
        aliases = {}
        for entry in entries:
            if entry["type"] == "label":
                assert re.fullmatch(r"[0-9a-f]{8}", entry["id"]), entry
                assert entry["id"] not in ids, entry
                aliases[entry["id"]] = f"LABEL{len(aliases)}"
        for entry in entries:
            entry["id"] = aliases.get(entry["id"], entry["id"])
            entry["parentId"] = aliases.get(entry["parentId"], entry["parentId"])
        result["data"]["leafId"] = aliases.get(result["data"]["leafId"], result["data"]["leafId"])
        emit({"case": "clone", "record": result})
        proc.stdin.close()
        assert proc.wait(timeout=10) == 0


def signal_probe(binary, root, mode):
    begun, release = threading.Event(), threading.Event()
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_POST(self):
            self.rfile.read(int(self.headers["Content-Length"]))
            begun.set()
            release.wait(15)
        def log_message(self, *_):
            pass
    server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever)
    worker.start()
    try:
        cwd, env, args = setup(root, f"http://127.0.0.1:{server.server_port}/v1")
        mode_args = ["-p"] if mode == "print" else ["--mode", "json"]
        with open(root / "stdout", "wb") as stdout, open(root / "stderr", "wb") as stderr, child(
                [*binary, *args, *mode_args, "hold"], cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                stdout=stdout, stderr=stderr) as proc:
            assert begun.wait(15), "provider request did not begin"
            proc.send_signal(signal.SIGINT)
            status = proc.wait(timeout=10)
            emit({"case": mode + "-SIGINT", "returncode": status})
            assert status == -signal.SIGINT, f"expected signal termination, got {status}"
    finally:
        release.set()
        server.shutdown()
        worker.join()
        server.server_close()


def command_ready_fixture(root, env):
    ready = root / "ready"
    env["WIRE_READY"] = str(ready)
    extension = root / "ready.mjs"
    extension.write_text('import {writeFileSync} from "node:fs"; export default function(pi) {'
                         'pi.registerCommand("wire-ready", {handler: async () => {'
                         'writeFileSync(process.env.WIRE_READY,"ready");}});}')
    return ready, extension


def wait_command_ready(master, proc, ready):
    output = bytearray()
    end = time.monotonic() + 15
    while not ready.exists():
        assert time.monotonic() < end and proc.poll() is None, ("command-ready barrier not reached", bytes(output))
        if select.select([master], [], [], 0.02)[0]:
            output.extend(os.read(master, 65536))
    return output


def terminal_probe(binary, root, action):
    cwd, env, args = setup(root, "http://127.0.0.1:1/v1")
    ready, extension = command_ready_fixture(root, env)
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 35, 100, 0, 0))
    try:
        with child([*binary, *args, "-e", str(extension), "/wire-ready"], cwd=cwd, env=env,
                   stdin=slave, stdout=slave, stderr=slave) as proc:
            wait_command_ready(master, proc, ready)
            if action == "disconnect":
                os.close(master)
                master = None
            else:
                proc.send_signal(signal.SIGHUP)
            # Continue draining the live terminal while Pi restores it.
            end = time.monotonic() + 10
            while proc.poll() is None:
                assert time.monotonic() < end, "terminal shutdown did not finish"
                if master is not None and select.select([master], [], [], 0.02)[0]:
                    os.read(master, 65536)
                else:
                    time.sleep(0.02)
            status = proc.wait()
            emit({"case": action, "returncode": status})
            assert status == (129 if action == "disconnect" else 0), status
    finally:
        if master is not None:
            os.close(master)
        os.close(slave)


def modal_shutdown_probe(binary, root):
    # Pi interactive-mode.ts:4146-4157 disposes and exits even with a selector open.
    # A live signal removes the EOF-vs-cancellation race of closing a tmux pane.
    for action in ("SIGHUP", "SIGTERM"):
        case_root = root / action
        case_root.mkdir()
        cwd, env, args = setup(case_root, "http://127.0.0.1:1/v1")
        ready, extension = command_ready_fixture(case_root, env)
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 35, 100, 0, 0))
        try:
            with child([*binary, *args, "-e", str(extension), "/wire-ready"], cwd=cwd, env=env, stdin=slave, stdout=slave, stderr=slave,
                       preexec_fn=lambda: fcntl.ioctl(slave, termios.TIOCSCTTY, 0)) as proc:
                # Pi interactive-mode.ts:1029,1164-1179 installs submit handling before dispatching initial commands. A footer is not that acknowledgement.
                output = wait_command_ready(master, proc, ready)
                def wait_output(marker):
                    end = time.monotonic() + 15
                    while marker not in output:
                        assert time.monotonic() < end and proc.poll() is None, (marker, bytes(output))
                        if select.select([master], [], [], 0.02)[0]:
                            output.extend(os.read(master, 65536))
                os.write(master, b"/scoped-models\r")
                wait_output(b"Model catalogs refreshed.")
                proc.send_signal(getattr(signal, action))
                end = time.monotonic() + 10
                while proc.poll() is None:
                    assert time.monotonic() < end, f"{action}: selector blocked shutdown"
                    if master is not None and select.select([master], [], [], 0.02)[0]:
                        output.extend(os.read(master, 65536))
                    else:
                        time.sleep(0.02)
                status = proc.wait()
                assert status == 0, status
                emit({"case": "scoped-models-" + action, "returncode": status})
        finally:
            if master is not None:
                os.close(master)
            os.close(slave)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--bin", default=os.environ.get("PIG_PARITY_PIG_BIN"))
    parser.add_argument("--case", choices=["session", "print", "json", "disconnect", "live-hup", "modal-shutdown"], required=True, action="append")
    options = parser.parse_args()
    if not options.bin:
        parser.error("--bin or PIG_PARITY_PIG_BIN is required")
    path = Path(options.bin).resolve()
    binary = [str(path)]
    if path.suffix in (".js", ".mjs", ".cjs"):
        # Resolve the installed Node before the child receives a private HOME.
        node = subprocess.check_output(["node", "-p", "process.execPath"], text=True).strip()
        binary.insert(0, node)
    for case in options.case:
        with tempfile.TemporaryDirectory(prefix="session-wire-") as directory:
            root = Path(directory)
            if case == "session":
                session_probe(binary, root)
            elif case in ("print", "json"):
                signal_probe(binary, root, case)
            elif case == "modal-shutdown":
                modal_shutdown_probe(binary, root)
            else:
                terminal_probe(binary, root, case)


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Deterministic built-PiG channel probe; also a tiny controllable stdio server.

No network service or credentials required: the model is a loopback SSE fixture.
Run: python3 scripts/probe-mcp-channels.py --pig ./bin/pig
Server alone: python3 scripts/probe-mcp-channels.py --server /tmp/control.sock
Send JSON params to that Unix datagram socket after MCP initialization.
"""

import argparse
import http.server
import json
import os
import socket
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path


def serve_stdio(control):
    lock = threading.Lock()

    def emit(message):
        with lock:
            print(json.dumps({"jsonrpc": "2.0", **message}), flush=True)

    def pushes():
        with socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM) as sock:
            sock.bind(control)
            while True:
                params = json.loads(sock.recv(65536))
                emit({"method": "notifications/claude/channel", "params": params})

    for line in sys.stdin:
        req = json.loads(line)
        method = req.get("method")
        if method == "notifications/initialized":
            threading.Thread(target=pushes, daemon=True).start()
            continue
        if "id" not in req:
            continue
        result = {}
        if method == "initialize":
            result = {
                "protocolVersion": "2025-06-18",
                "serverInfo": {"name": "probe", "version": "1"},
                "capabilities": {"tools": {}, "experimental": {"claude/channel": {}}},
            }
        elif method == "tools/list":
            result = {"tools": []}
        emit({"id": req["id"], "result": result})


def until(predicate, description, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.01)
    raise RuntimeError("timeout: " + description)


def probe(binary):
    script = Path(__file__).resolve()
    root = script.parents[1]
    requests, events, errors = [], [], []
    release = threading.Event()
    busy = threading.Event()

    class Model(http.server.BaseHTTPRequestHandler):
        def log_message(self, format, *args):
            pass

        def do_POST(self):
            request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            requests.append(request)
            index = len(requests)
            if index == 1:
                busy.set()
                if not release.wait(20):
                    errors.append("model release timeout")
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            for delta, finish in [
                ({"role": "assistant", "content": "fixture reply " + str(index)}, None),
                ({}, "stop"),
            ]:
                chunk = {
                    "id": "probe",
                    "object": "chat.completion.chunk",
                    "created": 0,
                    "model": "test",
                    "choices": [{"index": 0, "delta": delta, "finish_reason": finish}],
                }
                self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()

    model = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Model)
    threading.Thread(target=model.serve_forever, daemon=True).start()
    # Keep all transient config and session writes within this worktree.
    with tempfile.TemporaryDirectory(prefix="channel-probe-", dir=root / "bin") as work:
        work = Path(work)
        agent = work / "agent"
        agent.mkdir()
        (agent / "settings.json").write_text(
            json.dumps({"steeringMode": "one-at-a-time"})
        )
        (agent / "models.json").write_text(
            json.dumps(
                {
                    "providers": {
                        "fixture": {
                            "api": "openai-completions",
                            "baseUrl": f"http://127.0.0.1:{model.server_port}/v1",
                            "apiKey": "fixture",
                            "models": [
                                {
                                    "id": "test",
                                    "name": "Test",
                                    "contextWindow": 100000,
                                    "maxTokens": 4096,
                                }
                            ],
                        }
                    }
                }
            )
        )
        # Relative Unix socket paths avoid the kernel's 108-byte path limit.
        os.chdir(work)
        controls = {name: name + ".sock" for name in ("push", "off")}
        servers = {
            name: {
                "command": sys.executable,
                "args": [str(script), "--server", path],
                "channel": name == "push",
            }
            for name, path in controls.items()
        }
        (agent / "mcp.json").write_text(json.dumps({"mcpServers": servers}))
        env = {
            **os.environ,
            "HOME": str(work),
            "PIG_HOME": str(work / "home"),
            "PI_HOME": str(work / "home"),
            "PIG_CODING_AGENT_DIR": str(agent),
            "PI_CODING_AGENT_DIR": str(agent),
            "PIG_OFFLINE": "1",
            "PI_SKIP_VERSION_CHECK": "1",
        }
        command = [
            str(binary),
            "--mode",
            "rpc",
            "--model",
            "fixture/test",
            "--no-skills",
            "--no-prompt-templates",
            "--no-context-files",
            "--session-dir",
            str(work / "sessions"),
        ]
        process = subprocess.Popen(
            command,
            cwd=work,
            env=env,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
        )

        stdout, stderr_pipe = process.stdout, process.stderr
        assert stdout is not None and stderr_pipe is not None

        def collect():
            for line in stdout:
                try:
                    events.append(json.loads(line))
                except ValueError:
                    errors.append("unexpected stdout: " + line)

        threading.Thread(target=collect, daemon=True).start()

        def send(name, content):
            with socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM) as sock:
                sock.sendto(
                    json.dumps(
                        {
                            "content": content,
                            "meta": {"event_id": content, "bad-key": "drop"},
                        }
                    ).encode(),
                    controls[name],
                )

        def channels():
            return [
                e
                for e in events
                if e.get("type") == "message_end"
                and e.get("message", {}).get("customType") == "mcp-channel"
            ]

        try:
            until(
                lambda: all(Path(p).exists() for p in controls.values()),
                "MCP initialized",
            )
            send("off", "DROP_IDLE")
            send("push", "IDLE_WAKE")
            until(busy.is_set, "idle event starts model request")
            print("idle: IDLE_WAKE started model request 1 without a user prompt")
            send("push", "BUSY_1")
            send("off", "DROP_BUSY")
            send("push", 'BUSY_2: Say "don\'t" & </channel>')
            # RPC get_state is an admission barrier; allow transport readers to process the datagrams.
            time.sleep(0.2)
            assert len(requests) == 1 and len(channels()) == 1, (requests, channels())
            print(
                "busy: model request 1 held; BUSY_1/BUSY_2 not delivered or interrupting"
            )
            release.set()
            until(
                lambda: any(e.get("type") == "agent_end" for e in events),
                "channel continuation settled",
            )
            assert len(requests) == 2, (
                "queued events must be grouped, even in one-at-a-time mode"
            )
            second = json.dumps(requests[1]["messages"])
            assert second.index("BUSY_1") < second.index("BUSY_2")
            assert "DROP_" not in json.dumps(requests)
            print("between turns: request 2 includes BUSY_1 then BUSY_2 as one group")
            print("opt-out: DROP_IDLE and DROP_BUSY absent from every model request")
            files = list((work / "sessions").rglob("*.jsonl"))
            entries = [
                json.loads(line)
                for path in files
                for line in path.read_text().splitlines()
            ]
            channel_entries = [
                e for e in entries if e.get("customType") == "mcp-channel"
            ]
            assert len(channel_entries) == 3, entries
            assert all(
                e["type"] == "custom_message" and e["details"]["source"] == "push"
                for e in channel_entries
            )
            assert all("bad-key" not in e["details"]["meta"] for e in channel_entries)
            assert channel_entries[2]["content"] == (
                '<channel source="push" event_id="BUSY_2: Say &#34;don&#39;t&#34; &amp; &lt;/channel&gt;">'
                'BUSY_2: Say "don\'t" &amp; &lt;/channel&gt;</channel>'
            )
            assert json.dumps(channel_entries[2]["content"])[1:-1] in second
            print(
                "escaping: content quotes preserved; attributes escaped; wrapper cannot be closed"
            )
            print("JSONL: " + json.dumps(channel_entries[0], sort_keys=True))
            print(
                "PASS: idle, busy FIFO/grouping, opt-out, meta filtering, distinct JSONL provenance"
            )
            assert not errors, errors
        finally:
            release.set()
            process.terminate()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
            stderr = stderr_pipe.read()
            if process.returncode not in (0, -15):
                print(stderr, file=sys.stderr)
            model.shutdown()
            model.server_close()
            os.chdir(root)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--server", help="run the stdio server with this Unix datagram control socket"
    )
    parser.add_argument("--pig", type=Path, default=Path("bin/pig"))
    args = parser.parse_args()
    if args.server:
        serve_stdio(args.server)
    else:
        probe(args.pig.resolve())

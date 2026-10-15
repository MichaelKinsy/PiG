"""Drive the real interactive CLI and capture its first provider request after startup.

Pi sdk.ts:231-255 selects the Session level; interactive-mode and footer.ts use
that state. Keep the PTY open until the assistant replies, then exit and inspect
the persisted Session. All credentials are fake and HTTP stays on loopback.
"""
import errno
import fcntl
import http.server
import json
import os
from pathlib import Path
import pty
import re
import select
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time


SIDE = sys.argv[1]
ROOT = Path(__file__).resolve().parents[3]
BINARY = ([os.environ.get("PIG_PARITY_PIG_BIN", str(ROOT / "bin/pig"))]
          if SIDE == "pig" else [os.environ.get("PIG_PARITY_PI_BIN", str(ROOT / "extensions/sdk-ts/node_modules/.bin/pi"))])


def drain_until_exit(child, master, output):
    """Read the PTY until the child exits so a small PTY buffer (macOS) never blocks the exit message write."""
    deadline = time.monotonic() + 20
    while True:
        if time.monotonic() >= deadline:
            raise AssertionError(f"CLI did not exit: {output.decode(errors='replace')}")
        if select.select([master], [], [], 0.05)[0]:
            try:
                chunk = os.read(master, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                chunk = b""
            if chunk:
                output.extend(chunk)
                continue
            child.wait(timeout=max(0.1, deadline - time.monotonic()))
            return
        if child.poll() is not None:
            return


def run_case(root, provider, model, api, requested, expected, resumed=False, suffix=False):
    agent = root / "agent"
    agent.mkdir(exist_ok=True)
    requests = []
    reply = "wire-resumed" if resumed else "wire-ok"

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            requests.append(request)
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()

            def emit(event):
                self.wfile.write(("data: " + json.dumps(event) + "\n\n").encode())

            if api == "openai-completions":
                emit({"id": "completion", "choices": [{"index": 0, "delta": {"role": "assistant", "content": reply}, "finish_reason": None}]})
                emit({"id": "completion", "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]})
                self.wfile.write(b"data: [DONE]\n\n")
            else:
                item = {"id": "message", "type": "message", "role": "assistant", "status": "completed", "content": [{"type": "output_text", "text": reply, "annotations": []}]}
                emit({"type": "response.created", "response": {"id": "response", "status": "in_progress", "output": []}})
                emit({"type": "response.output_item.added", "output_index": 0, "item": {**item, "status": "in_progress", "content": []}})
                emit({"type": "response.content_part.added", "output_index": 0, "content_index": 0, "part": {"type": "output_text", "text": "", "annotations": []}})
                emit({"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": reply})
                emit({"type": "response.output_item.done", "output_index": 0, "item": item})
                emit({"type": "response.completed", "response": {"id": "response", "status": "completed", "output": [item], "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
            self.wfile.flush()

    server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    try:
        config = {"api": api, "baseUrl": f"http://127.0.0.1:{server.server_port}/v1", "apiKey": "test-key"}
        if provider != "deepseek":
            levels = {"minimal": None, "low": None, "medium": None, "high": "high"} if not requested else {"low": "low", "medium": "medium", "high": "high"}
            config["models"] = [{"id": model, "reasoning": True, "contextWindow": 128000, "maxTokens": 1024, "thinkingLevelMap": levels}]
            config["compat"] = {"supportsReasoningEffort": True}
        (agent / "models.json").write_text(json.dumps({"providers": {provider: config}}))
        (agent / "settings.json").write_text(json.dumps({"defaultThinkingLevel": "off" if resumed else "medium", "quietStartup": True, "enableInstallTelemetry": False}))
        env = {**os.environ, "HOME": str(root), "PIG_CODING_AGENT_DIR": str(agent), "PI_CODING_AGENT_DIR": str(agent), "PI_SKIP_VERSION_CHECK": "1", "TERM": "xterm-256color"}
        spec = f"{provider}/{model}"
        if requested and suffix:
            spec += ":" + requested
        args = [*BINARY, "--offline", "--no-extensions", "--no-tools", "--model", spec]
        if requested and not suffix:
            args.extend(["--thinking", requested])
        if resumed:
            args.append("--continue")
        args.append("hello")
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
        child = subprocess.Popen(args, cwd=root, env=env, stdin=slave, stdout=slave, stderr=slave)
        os.close(slave)
        output = bytearray()

        def wait_for(marker):
            deadline = time.monotonic() + 20
            while marker not in output:
                if time.monotonic() >= deadline:
                    raise AssertionError(f"timeout waiting for {marker!r}: {output.decode(errors='replace')}")
                if select.select([master], [], [], max(0, deadline - time.monotonic()))[0]:
                    try:
                        chunk = os.read(master, 65536)
                    except OSError as error:
                        if error.errno != errno.EIO:
                            raise
                        chunk = b""
                    if not chunk:
                        raise AssertionError(f"CLI ended before {marker!r}: {output.decode(errors='replace')}")
                    output.extend(chunk)

        try:
            wait_for(reply.encode())
            plain = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", output.decode(errors="replace"))
            footer_level = "thinking off" if expected == "off" else expected
            os.write(master, b"\x04")
            drain_until_exit(child, master, output)
            assert child.returncode == 0, output.decode(errors="replace")
        finally:
            if child.poll() is None:
                child.kill()
                child.wait()
            os.close(master)
        assert len(requests) == 1, requests
        request = requests[0]
        effort = request.get("reasoning_effort") if api == "openai-completions" else request.get("reasoning", {}).get("effort")
        assert effort == expected, request
        assert f"{model} • {footer_level}" in plain, plain
        if provider == "deepseek":
            assert request.get("thinking") == {"type": "enabled"}, request
        sessions = list((agent / "sessions").rglob("*.jsonl"))
        assert len(sessions) == 1, sessions
        entries = [json.loads(line) for line in sessions[0].read_text().splitlines()]
        levels = [entry["thinkingLevel"] for entry in entries if entry["type"] == "thinking_level_change"]
        assert levels == [expected], levels
        assert any(entry.get("message", {}).get("role") == "assistant" and entry["message"].get("stopReason") == "stop" for entry in entries), entries
        print(json.dumps({"resumed": resumed, "provider": provider, "api": api, "model": request["model"], "footer": footer_level, "session": levels, "wire": effort, "thinking": request.get("thinking")}, sort_keys=True))
    finally:
        server.shutdown()
        thread.join()
        server.server_close()


with tempfile.TemporaryDirectory(prefix="interactive-thinking-") as directory:
    for index, (provider, model, api, requested, expected, suffix) in enumerate([
        ("deepseek", "deepseek-flash", "openai-completions", None, "high", False),
        ("local-completions", "sparse", "openai-completions", None, "high", False),
        ("local-responses", "sparse", "openai-responses", None, "high", False),
        ("local-completions", "explicit", "openai-completions", "high", "high", False),
        ("local-responses", "explicit", "openai-responses", "high", "high", False),
        ("local-completions", "suffix", "openai-completions", "high", "high", True),
        ("local-responses", "suffix", "openai-responses", "high", "high", True),
    ]):
        root = Path(directory) / str(index)
        root.mkdir()
        run_case(root, provider, model, api, requested, expected, suffix=suffix)
        run_case(root, provider, model, api, requested, expected, resumed=True, suffix=suffix)

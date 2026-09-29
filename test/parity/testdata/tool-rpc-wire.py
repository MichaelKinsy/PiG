"""Compare complete tool-result and direct-bash records through the real RPC processes.

Pi 0.87.1: agent-loop.ts:863-894, agent-session.ts:3498-3508.
The shared typed JSON comparator owns fixture identities. This probe validates
clock ranges and repeated message timestamps without rewriting any record.
Other event families are outside this scenario and remain in the raw artifact.
"""
import argparse
import http.server
import json
import os
from pathlib import Path
import queue
import re
import signal
import subprocess
import sys
import tempfile
import threading
import time

CASES = [
    ("write", {"path": "written", "content": "hello\n"}),
    ("read", {"path": "written"}),
    ("read", {"path": "missing"}),
    ("write", {"path": ".", "content": "no"}),
    ("edit", {"path": "written", "edits": [{"oldText": "absent", "newText": "no"}]}),
    ("bash", {"command": "printf bad; exit 7"}),
    ("grep", {"pattern": "["}),
    ("find", {"pattern": "*", "path": "missing"}),
    ("ls", {"path": "missing"}),
    ("unknown", {}),
    ("write", {"path": "empty", "content": ""}),
    ("read", {"path": "empty"}),
]


class Provider(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        messages = body["messages"]
        content = next(m["content"] for m in reversed(messages) if m["role"] == "user")
        index = int(content if isinstance(content, str) else "".join(block.get("text", "") for block in content))
        name, args = CASES[index]
        followup = messages[-1]["role"] == "tool"
        delta = {"content": "done"} if followup else {"tool_calls": [{"index": 0, "id": f"call-{index}", "type": "function", "function": {"name": name, "arguments": json.dumps(args)}}]}
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for change, finish in [(delta, None), ({}, "stop" if followup else "tool_calls")]:
            chunk = {"id": "wire-response", "object": "chat.completion.chunk", "created": 1700000000, "model": "wire-model", "choices": [{"index": 0, "delta": change, "finish_reason": finish}]}
            self.wfile.write(("data: " + json.dumps(chunk) + "\n\n").encode())
        self.wfile.write(b"data: [DONE]\n\n")


def run(binary, directory):
    home, cwd, agent = directory / "home", directory / "work", directory / "agent"
    for path in (home, cwd, agent):
        path.mkdir()
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Provider)
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    model = {"id": "wire-model", "name": "Wire", "reasoning": False, "input": ["text"], "contextWindow": 128000, "maxTokens": 4096, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}
    (agent / "models.json").write_text(json.dumps({"providers": {"wire": {"baseUrl": f"http://127.0.0.1:{server.server_port}/v1", "api": "openai-completions", "apiKey": "not-a-secret", "models": [model]}}}))
    (agent / "settings.json").write_text(json.dumps({"quietStartup": True, "enableInstallTelemetry": False, "compaction": {"enabled": False}, "retry": {"enabled": False}}))
    (agent / "auth.json").write_text("{}")
    env = {"PATH": os.environ["PATH"], "HOME": str(home), "LANG": "C.UTF-8", "TERM": "dumb", "PI_CODING_AGENT_DIR": str(agent), "PIG_CODING_AGENT_DIR": str(agent), "PIG_HOME": str(home / ".pig"), "PI_OFFLINE": "1", "PIG_OFFLINE": "1", "PI_TELEMETRY": "0"}
    args = [*binary, "--mode", "rpc", "--provider", "wire", "--model", "wire-model", "--thinking", "off", "--system-prompt", "Wire.", "--no-context-files", "--no-skills", "--no-extensions", "--no-prompt-templates", "--no-themes", "--tools", "read,write,edit,bash,grep,find,ls"]
    started = time.time() * 1000
    records, raw_lines, inbox = [], [], queue.Queue()
    with (directory / "stderr.txt").open("w") as stderr:
        process = subprocess.Popen(args, cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=stderr, text=True, start_new_session=True)
        def reader():
            for line in process.stdout:
                raw_lines.append(line)
                records.append(json.loads(line))
                inbox.put(records[-1])
            inbox.put(None)
        reader_thread = threading.Thread(target=reader)
        reader_thread.start()
        def send(kind, ident, **fields):
            process.stdin.write(json.dumps({"type": kind, "id": ident, **fields}) + "\n")
            process.stdin.flush()
        def wait(predicate):
            deadline = time.monotonic() + 15
            while True:
                record = inbox.get(timeout=max(0, deadline - time.monotonic()))
                assert record is not None, "unexpected EOF"
                if predicate(record):
                    return record
        def response(ident):
            record = wait(lambda r: r.get("type") == "response" and r.get("id") == ident)
            assert record["success"], record
            return record
        try:
            for index in range(len(CASES)):
                send("prompt", f"prompt-{index}", message=str(index))
                wait(lambda r: r.get("type") == "agent_settled")
            bash_results = []
            send("bash", "bash-ok", command="printf 'direct\n'; exit 7")
            bash_results.append(response("bash-ok"))
            send("bash", "bash-cancel", command="echo BASH_READY; sleep 60")
            wait(lambda r: r.get("type") == "bash_execution_update" and r.get("id") == "bash-cancel" and "BASH_READY\n" in r["delta"])
            send("abort_bash", "abort")
            bash_results.append(response("bash-cancel"))
            send("get_entries", "entries")
            entries = response("entries")["data"]["entries"]
            send("get_messages", "messages")
            messages = response("messages")["data"]["messages"]
            process.stdin.close()
            assert process.wait(timeout=5) == 0
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
            reader_thread.join()
            server.shutdown()
            server.server_close()
            thread.join()
            child_stderr = (directory / "stderr.txt").read_text()
            (directory / "raw.json").write_text(json.dumps({"stdout": "".join(raw_lines), "stderr": child_stderr}))
            sys.stderr.write(child_stderr)
    tool_ends = [record for record in records if record.get("type") == "tool_execution_end"]
    assert [record["toolCallId"] for record in tool_ends] == [f"call-{i}" for i in range(len(CASES))], tool_ends
    message_times = {}
    def validate(value, key=""):
        if isinstance(value, dict) and value.get("role") in ("toolResult", "bashExecution"):
            ident = value.get("toolCallId", value.get("command"))
            timestamp = value["timestamp"]
            assert type(timestamp) in (int, float), value
            assert message_times.setdefault(ident, timestamp) == timestamp, "message timestamp changed across event/query/disk"
        if key == "timestamp":
            if isinstance(value, str):
                from datetime import datetime
                assert re.fullmatch(r"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z", value), value
                assert started - 1000 <= datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp() * 1000 <= time.time() * 1000
            else:
                assert type(value) in (int, float) and started - 1000 <= value <= time.time() * 1000, value
        if isinstance(value, dict):
            for k, v in value.items():
                validate(v, k)
        elif isinstance(value, list):
            for v in value:
                validate(v)
    def emit(kind, value):
        validate(value)
        print(json.dumps({"surface": kind, "record": value}, separators=(",", ":")))
    def owned(message):
        return message.get("role") in ("toolResult", "bashExecution")
    for record in records:
        if record.get("type") == "tool_execution_end" or (record.get("type") in ("message_start", "message_end") and record.get("message", {}).get("role") == "toolResult"):
            emit("event", record)
    for record in bash_results:
        emit("bash response", record)
    for entry in entries:
        if owned(entry.get("message", {})):
            emit("entry", entry)
    for message in messages:
        if owned(message):
            emit("message", message)
    files = list(agent.rglob("*.jsonl"))
    assert len(files) == 1, files
    by_id = {entry["id"]: entry for entry in entries}
    for line in files[0].read_text().splitlines():
        entry = json.loads(line)
        if owned(entry.get("message", {})):
            assert entry == by_id[entry["id"]], "disk entry differs from queried entry"
            emit("disk", entry)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("host", choices=("pig", "pi"))
    parser.add_argument("--evidence", type=Path)
    options = parser.parse_args()
    repo = Path(__file__).resolve().parents[3]
    binary = Path(os.environ.get("PIG_PARITY_PIG_BIN", repo / "bin/pig-parity")) if options.host == "pig" else repo / "extensions/sdk-ts/node_modules/.bin/pi"
    command = [str(binary.resolve())]
    if options.host == "pi":
        # Resolve Node before the private HOME so a manager shim cannot select or install another runtime.
        node = subprocess.check_output(["node", "-p", "process.execPath"], text=True).strip()
        command.insert(0, node)
        assert subprocess.check_output([*command, "--version"], text=True).strip() == "0.87.1"
    if options.evidence:
        options.evidence.mkdir(parents=True)
        run(command, options.evidence.resolve())
    else:
        with tempfile.TemporaryDirectory(prefix="tool-rpc-wire-") as root:
            run(command, Path(root))

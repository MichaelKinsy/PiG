#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Scripted OpenAI-compatible model for the context-scale and agent-scale scenarios (PiG's own code; same task script as Pi-Bolt's
fake_model.py so every arm does the same work): after each user prompt it reads four fixture files with the `read` tool, one per
turn, then answers "Done: read all four files (prompt N)". Differences from Pi-Bolt's server, all deliberate:
  * usage.prompt_tokens is the request body length / 4, so a harness that trusts the provider's usage sees the real context size
    (Pi-Bolt's reports 1,000 to 5,000 whatever the context holds); the declared window is then what bounds compaction.
  * every request is appended to --log (JSONL: unix time, body bytes, request headers' user-agent, messages, tools, system prompt
    bytes) so what each harness SENDS can be counted afterwards (token/cost scenario).
  * --pace-ms 0 streams the whole answer at once, as Pi-Bolt's default.
  * --dump DIR writes every request body to DIR/req-NNNN.json (the token/cost scenario counts them with real tokenizers).
Usage: fake_model_ctx.py PORT [--log FILE] [--dump DIR] [--fail-nth K] [--pace-ms N]
"""
import json
import os
import socket
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TOOL_TURNS = 4
FILES = ["fixture/agent-session.ts", "fixture/interactive-mode.ts", "fixture/tui.ts", "fixture/openai-completions.ts"]
WORDS = ("Looking at the code structure the module wires the session state into the renderer and "
         "then streams tool results back through the agent loop before compaction runs ").split()
LOG = None
DUMP = None
FAIL_NTH = 0
PACE = 0.0
SEQ = [0]
SEQ_LOCK = threading.Lock()  # ThreadingHTTPServer: concurrent requests (agent scale, OpenCode's title request) each get their own number


def chunk(delta=None, finish=None, usage=None):
    body = {"id": "chatcmpl-bench", "object": "chat.completion.chunk", "created": 0, "model": "fake-model",
            "choices": [] if usage else [{"index": 0, "delta": delta or {}, "finish_reason": finish}]}
    if usage:
        body["usage"] = usage
    return f"data: {json.dumps(body)}\n\n".encode()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def setup(self):
        super().setup()
        self.connection.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)

    def log_message(self, *a):
        pass

    def do_GET(self):
        body = json.dumps({"object": "list", "data": [{"id": "fake-model", "object": "model"}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        req = json.loads(raw or b"{}")
        with SEQ_LOCK:
            SEQ[0] += 1
            seq = SEQ[0]
        if DUMP:
            with open(f"{DUMP}/req-{seq:04d}.json", "wb") as f:
                f.write(raw)
        if FAIL_NTH and seq == FAIL_NTH:
            if DUMP:
                os.rename(f"{DUMP}/req-{seq:04d}.json", f"{DUMP}/req-{seq:04d}-429.json")
            body = json.dumps({"error": {"message": "rate limited (scripted, once)", "type": "rate_limit_error", "code": "rate_limit_exceeded"}}).encode()
            self.send_response(429)
            self.send_header("Content-Type", "application/json")
            self.send_header("Retry-After", "0")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        msgs = req.get("messages", [])
        turn = prompts = 0
        for m in msgs:
            if m.get("role") == "user":
                turn, prompts = 0, prompts + 1
            elif m.get("role") == "tool":
                turn += 1
        if LOG:
            system = sum(len(json.dumps(m.get("content", ""))) for m in msgs if m.get("role") in ("system", "developer"))
            with open(LOG, "a") as f:
                f.write(json.dumps({"t": time.time(), "bytes": len(raw), "ua": self.headers.get("User-Agent"), "path": self.path,
                                    "messages": len(msgs), "tools": len(req.get("tools") or []),
                                    "tools_bytes": len(json.dumps(req.get("tools") or [])), "system_bytes": system,
                                    "prompt": prompts, "turn": turn}) + "\n")
        out = [chunk({"role": "assistant", "content": ""})]
        for i in range(120):
            out.append(chunk({"content": WORDS[i % len(WORDS)] + " "}))
        if turn < TOOL_TURNS:
            # The file-reading tool is whatever the harness calls it (Pi family `read`, Copilot CLI `view`) and its path parameter whatever its schema names it.
            tools = {t.get("function", {}).get("name"): t.get("function", {}) for t in req.get("tools") or []}
            read_tool = "read" if "read" in tools or "view" not in tools else "view"
            props = (tools.get(read_tool, {}).get("parameters") or {}).get("properties") or {}
            key = next((k for k in ("path", "filePath", "file_path") if k in props), "path")  # Pi `path`, OpenCode `filePath`
            args = json.dumps({key: FILES[turn]})
            out.append(chunk({"tool_calls": [{"index": 0, "id": f"call_{turn}", "type": "function", "function": {"name": read_tool, "arguments": ""}}]}))
            for i in range(0, len(args), 8):
                out.append(chunk({"tool_calls": [{"index": 0, "function": {"arguments": args[i:i + 8]}}]}))
            out.append(chunk(finish="tool_calls"))
        else:
            out.append(chunk({"content": f"\n\nDone: read all four files (prompt {prompts})."}))
            out.append(chunk(finish="stop"))
        ptok = max(1, len(raw) // 4)
        out.append(chunk(usage={"prompt_tokens": ptok, "completion_tokens": 150, "total_tokens": ptok + 150}))
        out.append(b"data: [DONE]\n\n")
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        if PACE:
            for piece in out:
                self.wfile.write(f"{len(piece):x}\r\n".encode() + piece + b"\r\n")
                self.wfile.flush()
                time.sleep(PACE)
            self.wfile.write(b"0\r\n\r\n")
        else:
            self.wfile.write(b"".join(f"{len(p):x}\r\n".encode() + p + b"\r\n" for p in out) + b"0\r\n\r\n")
        self.wfile.flush()


if __name__ == "__main__":
    port = int(sys.argv[1])
    if "--log" in sys.argv:
        LOG = sys.argv[sys.argv.index("--log") + 1]
    if "--dump" in sys.argv:
        DUMP = sys.argv[sys.argv.index("--dump") + 1]
    if "--fail-nth" in sys.argv:
        FAIL_NTH = int(sys.argv[sys.argv.index("--fail-nth") + 1])
    if "--pace-ms" in sys.argv:
        PACE = float(sys.argv[sys.argv.index("--pace-ms") + 1]) / 1000
    ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()

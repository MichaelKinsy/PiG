#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Scripted OpenAI-compatible model for sessionhost.py. bench/compare/fake_model_ctx.py answers each prompt with four tool turns, so the
bench could not assert two model requests per turn; this server answers every prompt with exactly one `read` tool call on README.md and
then one text answer, whatever the prompt says. GET /count returns {"requests": N, "with_fixtures": M}: the completions served, and those whose
tool list declared both extension fixture tools of the --extensions self-check.
Run through bench/compare/fake_server.py (which hands it a pre-bound listening socket): scripted_model.py PORT
"""
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

COUNT = [0, 0]  # completions served; of them, requests declaring both --extensions fixture tools
FIXTURE_TOOLS = {"node_counter", "go_counter"}
LOCK = threading.Lock()


def chunk(delta=None, finish=None, usage=None):
    body = {"id": "chatcmpl-bench", "object": "chat.completion.chunk", "created": 0, "model": "fake-model",
            "choices": [] if usage else [{"index": 0, "delta": delta or {}, "finish_reason": finish}]}
    if usage:
        body["usage"] = usage
    return f"data: {json.dumps(body)}\n\n".encode()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _json(self, obj):
        body = json.dumps(obj).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/count":
            self._json({"requests": COUNT[0], "with_fixtures": COUNT[1]})
        else:
            self._json({"object": "list", "data": [{"id": "fake-model", "object": "model"}]})

    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length", 0)))
        body = json.loads(raw or b"{}")
        msgs = body.get("messages", [])
        tools = {(t.get("function") or t).get("name") for t in body.get("tools") or []}
        with LOCK:
            COUNT[0] += 1
            # The bench's --extensions self-check: a request from a session that loaded both fixtures declares both tools.
            COUNT[1] += FIXTURE_TOOLS <= tools
        out = [chunk({"role": "assistant", "content": ""})]
        if msgs and msgs[-1].get("role") == "tool":
            out.append(chunk({"content": "done"}))
            out.append(chunk(finish="stop"))
        else:
            out.append(chunk({"tool_calls": [{"index": 0, "id": "call_0", "type": "function",
                                              "function": {"name": "read", "arguments": json.dumps({"path": "README.md"})}}]}))
            out.append(chunk(finish="tool_calls"))
        ptok = max(1, len(raw) // 4)
        out.append(chunk(usage={"prompt_tokens": ptok, "completion_tokens": 20, "total_tokens": ptok + 20}))
        out.append(b"data: [DONE]\n\n")
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        self.wfile.write(b"".join(f"{len(p):x}\r\n".encode() + p + b"\r\n" for p in out) + b"0\r\n\r\n")
        self.wfile.flush()


if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()

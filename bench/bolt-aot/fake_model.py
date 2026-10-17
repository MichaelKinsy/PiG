#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Scripted OpenAI-compatible chat-completions model for the bolt-aot benchmark (derived from the bolt-host spike).
Each user prompt: turn 1 calls word_count on the small fixture (so the heavy tool_call handler and the word_count tool run
in the extension process); turn 2 answers text. Logs (unix time, kind) per request and per response end to --log, so the
tool turn gap (response end -> next request) is the full-path tool_call round trip.
usage: fake_model.py PORT FIXTURE LOG"""
import json, socket, sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
PORT, FIXTURE, LOG = int(sys.argv[1]), sys.argv[2], sys.argv[3]
logf = open(LOG, "a", buffering=1)

def chunk(delta=None, finish=None):
    return ("data: " + json.dumps({"id": "c", "object": "chat.completion.chunk", "created": 0, "model": "fake-model",
            "choices": [{"index": 0, "delta": delta or {}, "finish_reason": finish}]}) + "\n\n").encode()

class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def setup(self):
        super().setup(); self.connection.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
    def log_message(self, *a): pass
    def do_GET(self):
        b = json.dumps({"object": "list", "data": [{"id": "fake-model", "object": "model"}]}).encode()
        self.send_response(200); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def do_POST(self):
        t_req = time.time()
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
        last = body["messages"][-1]["role"]
        tools = [t.get("function", {}).get("name") for t in body.get("tools") or []]
        self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.send_header("Cache-Control", "no-cache")
        self.send_header("Transfer-Encoding", "chunked"); self.end_headers()
        def w(b):
            self.wfile.write(f"{len(b):x}\r\n".encode() + b + b"\r\n"); self.wfile.flush()
        if last == "user" and "word_count" in tools:
            kind = "toolcall"
            w(chunk({"role": "assistant", "tool_calls": [{"index": 0, "id": f"call_{int(t_req*1e6)}", "type": "function",
               "function": {"name": "word_count", "arguments": json.dumps({"path": FIXTURE})}}]}))
            w(chunk(finish="tool_calls"))
        else:
            kind = "text"
            w(chunk({"role": "assistant", "content": "Done."})); w(chunk(finish="stop"))
        w(b"data: [DONE]\n\n"); self.wfile.write(b"0\r\n\r\n"); self.wfile.flush()
        logf.write(json.dumps({"t_req": t_req, "t_end": time.time(), "last": last, "kind": kind, "ntools": len(tools)}) + "\n")

ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()

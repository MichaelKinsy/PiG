# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""A deterministic model server for OpenAI Chat Completions, OpenAI Responses, and Anthropic Messages.

Every request gets the same short reply, so a benchmark measures the harness
and not the model. The server records each request body for size reporting
and for request diffs between harnesses.
"""
import gzip
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

REPLY = "Hello from the benchmark provider."
MODEL = "bench-1"


def deltas(text):
    words = text.split(" ")
    return [w + " " for w in words[:-1]] + [words[-1]]


def _text(content):
    if isinstance(content, str):
        return content
    return "".join(part.get("text", "") for part in content or [] if isinstance(part, dict))


def system_text(protocol, body):
    """Return the system prompt a request carries."""
    if protocol == "anthropic-messages":
        return _text(body.get("system"))
    if protocol == "openai-responses":
        items = body.get("input") if isinstance(body.get("input"), list) else []
        return (body.get("instructions") or "") + "".join(_text(i.get("content")) for i in items if isinstance(i, dict) and i.get("role") in ("system", "developer"))
    return "".join(_text(m.get("content")) for m in body.get("messages", []) if m.get("role") in ("system", "developer"))


class Recorder:
    def __init__(self):
        self._lock = threading.Lock()
        self._requests = []

    def add(self, entry):
        with self._lock:
            self._requests.append(entry)

    def take(self):
        with self._lock:
            taken, self._requests = self._requests, []
        return taken


def handler(recorder, reply):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def _body(self):
            if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
                raw = b""
                while True:
                    size = int(self.rfile.readline().strip() or b"0", 16)
                    if size == 0:
                        self.rfile.readline()
                        break
                    raw += self.rfile.read(size)
                    self.rfile.readline()
            else:
                raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
            if self.headers.get("Content-Encoding", "").lower() == "gzip":
                raw = gzip.decompress(raw)
            return raw

        def _json(self, status, payload):
            data = json.dumps(payload).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def _stream(self):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()

        def _event(self, data, name=None):
            if name:
                self.wfile.write(f"event: {name}\n".encode())
            self.wfile.write(b"data: " + json.dumps(data, separators=(",", ":")).encode() + b"\n\n")

        def do_HEAD(self):
            self.send_response(200)
            self.end_headers()

        def do_GET(self):
            if self.path.split("?", 1)[0].endswith("/models"):
                return self._json(200, {"object": "list", "data": [{"id": MODEL, "object": "model", "created": 0, "owned_by": "pigeval"}]})
            recorder.add({"protocol": "other", "method": "GET", "path": self.path, "bytes": 0, "body": {}})
            self._json(404, {"error": {"message": f"pigeval mock: no route for GET {self.path}"}})

        def do_POST(self):
            raw = self._body()
            path = self.path.split("?", 1)[0]
            try:
                body = json.loads(raw or b"{}")
            except ValueError:
                body = {}
            if path.endswith("/messages/count_tokens"):
                return self._json(200, {"input_tokens": max(1, len(raw) // 4)})
            protocol = ("openai-completions" if path.endswith("/chat/completions") else "openai-responses" if path.endswith("/responses")
                        else "anthropic-messages" if path.endswith("/messages") else "other")
            recorder.add({"time": time.time(), "method": "POST", "path": path, "protocol": protocol, "bytes": len(raw), "body": body,
                          "system_bytes": len(system_text(protocol, body).encode()), "tools": len(body.get("tools") or []), "stream": bool(body.get("stream"))})
            tokens_in = max(1, len(raw) // 4)
            if protocol == "openai-completions":
                return self._chat(body, tokens_in)
            if protocol == "openai-responses":
                return self._responses(body, tokens_in)
            if protocol == "anthropic-messages":
                return self._anthropic(body, tokens_in)
            self._json(404, {"error": {"message": f"pigeval mock: no route for POST {path}"}})

        def _chat(self, body, tokens_in):
            parts = deltas(reply)
            usage = {"prompt_tokens": tokens_in, "completion_tokens": len(parts), "total_tokens": tokens_in + len(parts)}
            model = body.get("model", MODEL)
            if not body.get("stream"):
                return self._json(200, {"id": "chatcmpl-bench", "object": "chat.completion", "created": 0, "model": model, "usage": usage,
                                        "choices": [{"index": 0, "message": {"role": "assistant", "content": reply}, "finish_reason": "stop"}]})
            self._stream()
            base = {"id": "chatcmpl-bench", "object": "chat.completion.chunk", "created": 0, "model": model}
            for i, part in enumerate(parts):
                delta = {"role": "assistant", "content": part} if i == 0 else {"content": part}
                self._event(dict(base, choices=[{"index": 0, "delta": delta, "finish_reason": None}]))
            self._event(dict(base, choices=[{"index": 0, "delta": {}, "finish_reason": "stop"}]))
            self._event(dict(base, choices=[], usage=usage))
            self.wfile.write(b"data: [DONE]\n\n")

        def _responses(self, body, tokens_in):
            parts = deltas(reply)
            model = body.get("model", MODEL)
            item = {"id": "msg_bench", "type": "message", "status": "completed", "role": "assistant", "content": [{"type": "output_text", "text": reply, "annotations": []}]}
            usage = {"input_tokens": tokens_in, "input_tokens_details": {"cached_tokens": 0}, "output_tokens": len(parts),
                     "output_tokens_details": {"reasoning_tokens": 0}, "total_tokens": tokens_in + len(parts)}
            final = {"id": "resp_bench", "object": "response", "created_at": 0, "status": "completed", "model": model, "output": [item], "usage": usage}
            if not body.get("stream"):
                return self._json(200, final)
            self._stream()
            seq = iter(range(1_000_000))
            emit = lambda name, data: self._event(dict(data, type=name, sequence_number=next(seq)), name)
            emit("response.created", {"response": dict(final, status="in_progress", output=[], usage=None)})
            emit("response.in_progress", {"response": dict(final, status="in_progress", output=[], usage=None)})
            emit("response.output_item.added", {"output_index": 0, "item": dict(item, status="in_progress", content=[])})
            emit("response.content_part.added", {"item_id": "msg_bench", "output_index": 0, "content_index": 0, "part": {"type": "output_text", "text": "", "annotations": []}})
            for part in parts:
                emit("response.output_text.delta", {"item_id": "msg_bench", "output_index": 0, "content_index": 0, "delta": part, "logprobs": []})
            emit("response.output_text.done", {"item_id": "msg_bench", "output_index": 0, "content_index": 0, "text": reply, "logprobs": []})
            emit("response.content_part.done", {"item_id": "msg_bench", "output_index": 0, "content_index": 0, "part": item["content"][0]})
            emit("response.output_item.done", {"output_index": 0, "item": item})
            emit("response.completed", {"response": final})

        def _anthropic(self, body, tokens_in):
            parts = deltas(reply)
            model = body.get("model", MODEL)
            usage = {"input_tokens": tokens_in, "output_tokens": len(parts), "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
            message = {"id": "msg_bench", "type": "message", "role": "assistant", "model": model, "stop_reason": "end_turn", "stop_sequence": None, "usage": usage,
                       "content": [{"type": "text", "text": reply}]}
            if not body.get("stream"):
                return self._json(200, message)
            self._stream()
            self._event({"type": "message_start", "message": dict(message, content=[], stop_reason=None, usage=dict(usage, output_tokens=1))}, "message_start")
            self._event({"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}, "content_block_start")
            for part in parts:
                self._event({"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": part}}, "content_block_delta")
            self._event({"type": "content_block_stop", "index": 0}, "content_block_stop")
            self._event({"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence": None}, "usage": {"output_tokens": len(parts)}}, "message_delta")
            self._event({"type": "message_stop"}, "message_stop")

    return Handler


class MockServer:
    """Serve the mock on 127.0.0.1 with an ephemeral port for the life of a with-block."""

    def __init__(self, reply=REPLY):
        self.recorder = Recorder()
        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), handler(self.recorder, reply))
        self.httpd.daemon_threads = True
        self.port = self.httpd.server_address[1]
        self.base_url = f"http://127.0.0.1:{self.port}"

    def __enter__(self):
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()
        return self

    def __exit__(self, *_):
        self.httpd.shutdown()
        self.httpd.server_close()

# SPDX-License-Identifier: MIT
"""Recording proxy between a harness and an OpenAI-compatible model server.

The harness talks to the proxy on 127.0.0.1; the proxy forwards each request unchanged to the upstream and streams the reply back, stamping
monotonic nanosecond times (the clock of pigbench.mock.now_ns). Per request it records the request size, the tool and message counts, the first-byte and last-byte times, and the token usage the
server reports. Request and response bodies, and every credential, are never stored. When the upstream needs a key (a hosted model), the proxy
replaces the harness's dummy Authorization header with the key it was given, so the key never reaches a harness or a record.
"""
import http.client
import json
import threading
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HOP_BY_HOP = {"connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailers", "transfer-encoding", "upgrade", "host", "content-length"}


def summarize_request(body):
    """The fields that make up the prompt cost of one turn: sizes and counts only."""
    try:
        data = json.loads(body)
    except ValueError:
        return {}
    messages = data.get("messages") or data.get("input") or []
    first = messages[0] if isinstance(messages, list) and messages else {}
    # The Messages wire carries the system prompt in a top-level `system` (Pi anthropic-messages.ts:1169) and the Codex Responses wire in
    # `instructions` (Pi openai-codex-responses.ts:557); chat completions and the other Responses requests put it in a leading system/developer message.
    system = data.get("system") if data.get("system") is not None else data.get("instructions")
    if system is None:
        system = first.get("content") if isinstance(first, dict) and first.get("role") in ("system", "developer") else None
    return {"messages": len(messages) if isinstance(messages, list) else 0, "tools": len(data.get("tools") or []), "tools_bytes": len(json.dumps(data.get("tools") or [])),
            "system_bytes": len(system if isinstance(system, str) else json.dumps(system or "")), "stream": bool(data.get("stream"))}


def usage_of(text):
    """The usage of a JSON body or an SSE stream, and llama.cpp's timings when present.

    Later usage fields replace earlier ones and absent or null fields keep theirs, as Pi merges them (anthropic-messages.ts:680, :827-840): the
    Messages wire reports input_tokens in message_start's message.usage and only output_tokens in message_delta."""
    usage, timings = None, None
    for line in text.splitlines():
        line = line.strip()
        if line.startswith("data:"):
            line = line[5:].strip()
        if not line.startswith("{"):
            continue
        try:
            data = json.loads(line)
        except ValueError:
            continue
        if isinstance(data, dict):
            for holder in (data, data.get("response"), data.get("message")):  # Responses API: response.completed; Messages: message_start
                found = holder.get("usage") if isinstance(holder, dict) else None
                if isinstance(found, dict):
                    usage = dict(usage or {}, **{k: v for k, v in found.items() if v is not None})
            timings = data.get("timings") or timings
    return usage, timings


class Recorder:
    def __init__(self):
        self.lock = threading.Lock()
        self.requests = []

    def add(self, record):
        with self.lock:
            record["index"] = len(self.requests)
            self.requests.append(record)

    def snapshot(self):
        with self.lock:
            return list(self.requests)


class Proxy:
    """`with Proxy(upstream_base_url) as p:` serves p.base_url (the upstream's path prefix included) and fills p.recorder."""

    def __init__(self, upstream, api_key=None):
        parts = urllib.parse.urlsplit(upstream)
        self.scheme, self.host, self.prefix = parts.scheme, parts.netloc, parts.path.rstrip("/")
        self.api_key = api_key
        self.recorder = Recorder()
        proxy = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *_):
                return

            def handle(self):
                try:
                    super().handle()
                except (ConnectionError, TimeoutError):  # a harness that closes its end (a killed arm) is not an error of the proxy
                    pass

            def do_POST(self):
                proxy.forward(self)

            do_GET = do_POST

        self.httpd = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.httpd.daemon_threads = True
        self.port = self.httpd.server_address[1]
        self.origin = f"http://127.0.0.1:{self.port}"
        self.base_url = self.origin + self.prefix
        self.thread = threading.Thread(target=self.httpd.serve_forever, daemon=True)

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *_):
        self.httpd.shutdown()
        self.httpd.server_close()

    def forward(self, handler):
        started = time.monotonic_ns()
        length = int(handler.headers.get("Content-Length") or 0)
        body = handler.rfile.read(length) if length else b""
        headers = {k: v for k, v in handler.headers.items() if k.lower() not in HOP_BY_HOP}
        headers = {k: v for k, v in headers.items() if k.lower() != "accept-encoding"}
        headers["Accept-Encoding"] = "identity"  # the usage fields are read from the bytes in transit, so a compressed reply would hide them
        if self.api_key:
            headers = {k: v for k, v in headers.items() if k.lower() not in ("authorization", "x-api-key")}
            headers["Authorization"] = "Bearer " + self.api_key
        conn = (http.client.HTTPSConnection if self.scheme == "https" else http.client.HTTPConnection)(self.host, timeout=600)
        record = {"path": handler.path, "request_bytes": len(body), "t_start": started, **summarize_request(body)}
        captured = bytearray()
        try:
            conn.request(handler.command, handler.path, body or None, headers)
            response = conn.getresponse()
            record["status"] = response.status
            handler.send_response(response.status)
            for k, v in response.getheaders():
                if k.lower() not in HOP_BY_HOP:
                    handler.send_header(k, v)
            handler.send_header("Transfer-Encoding", "chunked")
            handler.end_headers()
            while True:
                chunk = response.read1(65536)
                if not chunk:
                    break
                record.setdefault("t_first_byte", time.monotonic_ns())
                captured += chunk
                del captured[:-262144]  # only the tail holds the usage; a long reply must not grow the proxy without bound
                handler.wfile.write(b"%x\r\n%s\r\n" % (len(chunk), chunk))
                handler.wfile.flush()
            handler.wfile.write(b"0\r\n\r\n")
            handler.wfile.flush()
        except (OSError, http.client.HTTPException) as err:
            record["error"] = type(err).__name__
            if "status" not in record:
                handler.send_error(502, "upstream unreachable")
        finally:
            conn.close()
            record["t_end"] = time.monotonic_ns()
            usage, timings = usage_of(bytes(captured[-262144:]).decode("utf8", "replace"))
            if usage:  # chat completions name the counts prompt_/completion_tokens; the Responses and Messages wires input_/output_tokens
                record["prompt_tokens"] = usage.get("prompt_tokens", usage.get("input_tokens"))
                record["completion_tokens"] = usage.get("completion_tokens", usage.get("output_tokens"))
            if timings:
                record["server_prompt_ms"], record["server_predicted_ms"] = timings.get("prompt_ms"), timings.get("predicted_ms")
            self.recorder.add(record)

# SPDX-License-Identifier: MIT
"""A metering gateway: one endpoint every arm talks to, so tokens, latency and failures are counted in one place.

Each arm gets its own base URL, `http://<host>:<port>/<arm>`. The gateway strips the arm prefix, replaces the client's dummy key with
the real upstream key (read from the host environment, never written to disk or logged), forwards the request unchanged, streams the
reply back, and appends one JSON line per request to the log: arm, path, status, request and response bytes, time to first byte,
duration, and the token usage found in the reply. It is generic HTTP; run it as a container on the benchmark's internal network so
the task containers have no other route out (Harbor `network_mode: allowlist` with only this host allowed).

Environment: PIGBENCH_GATEWAY_UPSTREAM (for example https://api.example.com), PIGBENCH_GATEWAY_KEY, PIGBENCH_GATEWAY_LOG,
PIGBENCH_GATEWAY_KEY_HEADER (default Authorization with a Bearer scheme; x-api-key for Anthropic-style upstreams).
"""
import http.client
import json
import os
import re
import threading
import time
import urllib.parse
import zlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HOP = {"connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailers", "transfer-encoding", "upgrade", "host", "content-length"}


def read_body(rfile, headers):
    """The request body, whether the client sent Content-Length or Transfer-Encoding: chunked (a streamed request body)."""
    if "chunked" in headers.get("Transfer-Encoding", "").lower():
        body = bytearray()
        while True:
            size = int(rfile.readline().split(b";", 1)[0].strip() or b"0", 16)
            if size == 0:
                while rfile.readline().strip():  # trailers end with an empty line
                    pass
                return bytes(body)
            body += rfile.read(size)
            rfile.readline()
    length = int(headers.get("Content-Length") or 0)
    return rfile.read(length) if length else b""


def decoded(body, encoding):
    """The reply as the metering sees it: gzip and deflate are decoded (the client still gets the original bytes); None when it cannot be read."""
    encoding = (encoding or "").strip().lower()
    if encoding in ("", "identity"):
        return body
    if encoding in ("gzip", "x-gzip", "deflate"):
        try:
            return zlib.decompressobj(47).decompress(body)  # 47: zlib or gzip header, detected
        except zlib.error:
            try:
                return zlib.decompressobj(-15).decompress(body)  # raw deflate
            except zlib.error:
                return None
    return None


def find_usage(body):
    """Token usage from a JSON body or the last `data:` event of an SSE body, across the three protocols; {} when none."""
    candidates = []
    try:
        candidates.append(json.loads(body))
    except ValueError:
        for line in body.decode("utf-8", "replace").splitlines():
            if line.startswith("data:") and "usage" in line:
                try:
                    candidates.append(json.loads(line[5:].strip()))
                except ValueError:
                    continue
    usage = {}
    for obj in candidates:
        found = obj.get("usage") or (obj.get("response") or {}).get("usage") or (obj.get("message") or {}).get("usage") or {}
        for k, v in found.items():
            if isinstance(v, (int, float)):
                usage[k] = max(usage.get(k, 0), v)
    return usage


class Gateway(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, addr, upstream, key, log_path, key_header="authorization"):
        super().__init__(addr, make_handler())
        self.upstream, self.key, self.key_header = urllib.parse.urlsplit(upstream), key, key_header.lower()
        self.log_path, self.lock = log_path, threading.Lock()

    def log(self, entry):
        with self.lock, open(self.log_path, "a") as f:
            f.write(json.dumps(entry) + "\n")


def make_handler():
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        # The gateway forwards a stream in many small writes; with Nagle on, each waits for a delayed ACK on a reused connection.
        disable_nagle_algorithm = True

        def log_message(self, *_):
            pass

        def do_POST(self):
            self._forward()

        def do_GET(self):
            self._forward()

        def _forward(self):
            gw = self.server
            t0 = time.perf_counter()
            match = re.match(r"^/([A-Za-z0-9._-]+)(/.*)?$", self.path)
            if not match:
                return self._fail(404, "no arm prefix")
            arm, rest = match.group(1), match.group(2) or "/"
            body = read_body(self.rfile, self.headers)
            headers = {k: v for k, v in self.headers.items() if k.lower() not in HOP and k.lower() not in ("authorization", "x-api-key")}
            headers["Authorization" if gw.key_header == "authorization" else gw.key_header] = ("Bearer " + gw.key) if gw.key_header == "authorization" else gw.key
            conn_cls = http.client.HTTPSConnection if gw.upstream.scheme == "https" else http.client.HTTPConnection
            entry = {"t": time.time(), "arm": arm, "method": self.command, "path": rest, "request_bytes": len(body)}
            try:
                conn = conn_cls(gw.upstream.netloc, timeout=600)
                conn.request(self.command, (gw.upstream.path.rstrip("/") + rest), body=body or None, headers=headers)
                resp = conn.getresponse()
            except OSError as error:
                entry.update({"status": 0, "error": type(error).__name__, "duration_s": round(time.perf_counter() - t0, 4)})
                gw.log(entry)
                return self._fail(502, "upstream unreachable")
            entry["ttfb_s"] = round(time.perf_counter() - t0, 4)
            self.send_response(resp.status)
            for k, v in resp.getheaders():
                if k.lower() not in HOP:
                    self.send_header(k, v)
            self.send_header("Transfer-Encoding", "chunked")
            self.end_headers()
            seen = bytearray()
            try:
                while True:
                    chunk = resp.read(8192)
                    if not chunk:
                        break
                    if len(seen) < 8 << 20:
                        seen += chunk
                    self.wfile.write(f"{len(chunk):x}\r\n".encode() + chunk + b"\r\n")
                    self.wfile.flush()
                self.wfile.write(b"0\r\n\r\n")
            except OSError:
                entry["client_disconnected"] = True
            finally:
                conn.close()
            readable = decoded(bytes(seen), resp.getheader("Content-Encoding"))
            entry.update({"status": resp.status, "response_bytes": len(seen), "duration_s": round(time.perf_counter() - t0, 4), "usage": find_usage(readable) if readable is not None else {}})
            if readable is None:
                entry["usage_unreadable"] = resp.getheader("Content-Encoding")
            gw.log(entry)

        def _fail(self, status, message):
            data = json.dumps({"error": message}).encode()
            self.send_response(status)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    return Handler


def main():
    upstream, key, log = os.environ["PIGBENCH_GATEWAY_UPSTREAM"], os.environ["PIGBENCH_GATEWAY_KEY"], os.environ.get("PIGBENCH_GATEWAY_LOG", "gateway.jsonl")
    host, port = os.environ.get("PIGBENCH_GATEWAY_HOST", "0.0.0.0"), int(os.environ.get("PIGBENCH_GATEWAY_PORT", "8080"))
    Gateway((host, port), upstream, key, log, os.environ.get("PIGBENCH_GATEWAY_KEY_HEADER", "authorization")).serve_forever()


if __name__ == "__main__":
    main()

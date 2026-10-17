# SPDX-License-Identifier: MIT
"""A scripted model endpoint (OpenAI Chat Completions, OpenAI Responses, Anthropic Messages) with request timing and fault injection.

The server answers from a policy, so a benchmark measures the harness and not a
model. It records, for every request, when the request arrived and when the
reply finished, which is what the overhead bench needs to separate harness time
from model time. A fault plan makes selected requests fail with an HTTP status
or drop the connection part-way through a stream, which is what the reliability
bench needs.

Time is `now_ns()` taken in the server's threads; every bench timestamp that is
compared with a request time comes from it. Latencies of a few hundred
microseconds are inside the scheduling noise of a Python thread; the reports use
medians over many turns for that reason.
"""
import json
import os
import posixpath
import socket
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODEL = "bench-1"
REPLY = "Hello from the benchmark provider."


def now_ns():
    # perf_counter is QueryPerformanceCounter on Windows, where monotonic before Python 3.13 is GetTickCount64 and steps by 15.6 ms;
    # elsewhere the two are the same clock.
    return time.perf_counter_ns()


class Fault:
    """What to do to one request instead of answering it.

    status      answer with this HTTP status and a JSON error body
    drop_after  send this many SSE events, then close the connection with no terminator (mid-stream disconnect): a reset, except on
                Windows, where a reset discards the bytes the client has not read yet, so the close there is a FIN
    drop_before close the connection with a reset before sending any byte
    retry_after value for the Retry-After header, when status is set
    """

    def __init__(self, status=None, drop_after=None, drop_before=False, retry_after=None):
        self.status, self.drop_after, self.drop_before, self.retry_after = status, drop_after, drop_before, retry_after

    def __repr__(self):
        return f"Fault({self.status or ''}{' drop_after=' + str(self.drop_after) if self.drop_after is not None else ''}{' drop_before' if self.drop_before else ''})"


class Step:
    """One model reply: text, tool calls, or both. `usage_prompt` overrides the reported prompt tokens."""

    def __init__(self, text=None, tool_calls=(), usage_prompt=None, delay_ms=0, event_delay_ms=0):
        self.text, self.tool_calls, self.usage_prompt, self.delay_ms = text, list(tool_calls), usage_prompt, delay_ms
        self.event_delay_ms = event_delay_ms  # pause between streamed events, to model a long streaming reply


def chat_text(content):
    if isinstance(content, str):
        return content
    return "".join(p.get("text", "") for p in content or [] if isinstance(p, dict))


PROTOCOLS = {"/chat/completions": "openai-completions", "/responses": "openai-responses", "/messages": "anthropic-messages"}


def _blocks(content):
    return content if isinstance(content, list) else [{"type": "text", "text": content or ""}]


class Request:
    """What the policy and the recorder see of one POST, normalised across the three protocols."""

    def __init__(self, index, protocol, body, raw_bytes, t_recv):
        self.index, self.protocol, self.body, self.bytes, self.t_recv = index, protocol, body, raw_bytes, t_recv
        self.stream = bool(body.get("stream"))
        if protocol == "openai-completions":
            messages = body.get("messages") or []
            self.tools = [t.get("function", {}).get("name") for t in body.get("tools") or []]
            self.schemas = {t.get("function", {}).get("name"): t.get("function", {}).get("parameters") or {} for t in body.get("tools") or []}
            self.roles = ["tool" if m.get("role") == "tool" else m.get("role") for m in messages]
        elif protocol == "openai-responses":
            items = body.get("input") if isinstance(body.get("input"), list) else []
            self.tools = [t.get("name") for t in body.get("tools") or []]
            self.schemas = {t.get("name"): t.get("parameters") or {} for t in body.get("tools") or []}
            self.roles = ["tool" if i.get("type") == "function_call_output" else ("assistant" if i.get("type") in ("function_call", "reasoning") else i.get("role")) for i in items]
        else:
            messages = body.get("messages") or []
            self.tools = [t.get("name") for t in body.get("tools") or []]
            self.schemas = {t.get("name"): t.get("input_schema") or {} for t in body.get("tools") or []}
            self.roles = []
            for m in messages:
                if m.get("role") == "user" and any(b.get("type") == "tool_result" for b in _blocks(m.get("content")) if isinstance(b, dict)):
                    self.roles.append("tool")
                else:
                    self.roles.append(m.get("role"))
        self.messages = len(self.roles)
        self.last_role = self.roles[-1] if self.roles else None
        self.t_start = None  # reply started
        # Reply end: stamped just before the reply's last write (the body of a JSON reply, the terminating chunk of a stream, or the drop),
        # so a client cannot have finished reading the reply before t_end. Gaps measured from it (turn gap, harness-added time, teardown)
        # therefore never undercount the harness's time; a stamp taken after the write did, by however long the mock's handler thread
        # waited to run again after the client already had the reply.
        self.t_end = None
        self.fault = None
        self.hook = None  # whatever on_request returned (for example a CPU reading)
        self.auth = None  # the credential header the client sent (Authorization or x-api-key)

    def rounds(self):
        """Tool-result messages since the last real user message."""
        n = 0
        for role in reversed(self.roles):
            if role == "user":
                break
            if role == "tool":
                n += 1
        return n


READ_TOOLS = ("read", "Read", "view", "read_file")
SHELL_TOOLS = ("bash", "Bash", "shell", "exec_command", "shell_command", "local_shell")
PATH_KEYS = ("path", "file_path", "filePath", "file")


def read_call(request, path):
    """A tool call that reads `path`, built from whatever read or shell tool this request declares, or None."""
    for name in READ_TOOLS:
        props = (request.schemas.get(name) or {}).get("properties") or {}
        if name in request.schemas:
            key = next((k for k in PATH_KEYS if k in props), "path")
            return name, {key: path}
    for name in SHELL_TOOLS:
        props = (request.schemas.get(name) or {}).get("properties") or {}
        if name not in request.schemas:
            continue
        for key in ("command", "cmd"):
            if key in props:
                if props[key].get("type") == "array":
                    return name, {key: ["cat", path]}
                return name, {key: f"cat {path}"}
    return None


def agentic_policy(cwd, tool_turns=1, reply=REPLY, file="README.md"):
    """Default policy: answer a user turn with `tool_turns` rounds of one tool call that reads `file`, then text.

    A request that declares no tools (a compaction or summary request) always gets text. The policy counts
    rounds from the last user message, so it is stateless and survives retries. The file path is absolute because
    some harnesses require it.
    """
    # A POSIX workspace such as a container's /work keeps its separator when the mock runs on a Windows host.
    path = posixpath.join(cwd, file) if cwd.startswith("/") else os.path.join(cwd, file)

    def policy(request):
        if not request.tools:
            return Step(text="Summary: the earlier turns read files and reported the results.")
        call = read_call(request, path) if request.rounds() < tool_turns else None
        return Step(tool_calls=[call]) if call else Step(text=reply)

    return policy


def scripted_policy(steps):
    """Pop one step per request, in order; repeat the last step forever."""
    lock = threading.Lock()
    queue = list(steps)

    def policy(request):
        with lock:
            return queue.pop(0) if len(queue) > 1 else queue[0]

    return policy


def approx_tokens(raw_bytes):
    return max(1, raw_bytes // 4)


class Chunked:
    """Write HTTP/1.1 chunked bodies on a handler's wfile."""

    def __init__(self, wfile):
        self.wfile = wfile

    def write(self, data):
        self.wfile.write(f"{len(data):x}\r\n".encode() + data + b"\r\n")
        self.wfile.flush()

    def end(self):
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()


def make_handler(server):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        # A streamed response is several small sends. With Nagle on, each send after the first waits for the client's
        # ACK, which Linux delays up to 40 ms once a connection is past its start, so every turn on a reused connection
        # (PiG's shared transport, Pi's undici dispatcher) idled about 40 ms.
        disable_nagle_algorithm = True

        def handle_one_request(self):
            # A harness that exits with an idle keep-alive connection resets it on Windows (an EOF elsewhere): the client left, which is no
            # mock error. A reset in the middle of a request still reaches handle_error and prints.
            try:
                self.rfile.peek(1)
            except (ConnectionResetError, ConnectionAbortedError):
                self.close_connection = True
                return
            except OSError:
                pass  # the request read below meets and reports it
            super().handle_one_request()

        def log_message(self, *_):
            pass

        def _read(self):
            if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
                raw = b""
                while True:
                    size = int(self.rfile.readline().strip() or b"0", 16)
                    if size == 0:
                        self.rfile.readline()
                        return raw
                    raw += self.rfile.read(size)
                    self.rfile.readline()
            return self.rfile.read(int(self.headers.get("Content-Length") or 0))

        def _json(self, status, payload, headers=None, request=None):
            data = json.dumps(payload).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            for k, v in (headers or {}).items():
                self.send_header(k, v)
            self.end_headers()
            if request is not None:
                request.t_end = now_ns()  # before the body's last write: see Request.t_end
            self.wfile.write(data)
            self.wfile.flush()

        def _drop(self, reset=True):
            self.close_connection = True
            try:
                if reset:
                    self.connection.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, b"\x01\x00\x00\x00\x00\x00\x00\x00")  # RST on close
                else:
                    self.connection.shutdown(socket.SHUT_WR)
                self.connection.close()
            except OSError:
                pass

        def do_GET(self):
            if self.path.split("?", 1)[0].rstrip("/").endswith("/models"):
                return self._json(200, {"object": "list", "data": [{"id": MODEL, "object": "model", "created": 0, "owned_by": "pigbench"}]})
            self._json(404, {"error": {"message": f"pigbench mock: no route for GET {self.path}"}})

        def do_POST(self):
            raw = self._read()
            t_recv = now_ns()
            path = self.path.split("?", 1)[0]
            if path.endswith("/messages/count_tokens"):
                return self._json(200, {"input_tokens": approx_tokens(len(raw))})
            protocol = next((p for suffix, p in PROTOCOLS.items() if path.endswith(suffix)), None)
            if protocol is None:
                return self._json(404, {"error": {"message": f"pigbench mock: no route for POST {path}"}})
            try:
                body = json.loads(raw or b"{}")
            except ValueError:
                body = {}
            request = server.begin(protocol, body, len(raw), t_recv)
            request.auth = self.headers.get("Authorization") or self.headers.get("x-api-key")
            fault = server.fault_for(request)
            if fault:
                request.fault = fault
                if fault.drop_before:
                    request.t_end = now_ns()
                    return self._drop()
                if fault.status:
                    headers = {"Retry-After": str(fault.retry_after)} if fault.retry_after is not None else {}
                    return self._json(fault.status, error_body(protocol, fault.status), headers, request)
            step = server.policy(request)
            if step.delay_ms:
                time.sleep(step.delay_ms / 1000)
            tokens_in = step.usage_prompt if step.usage_prompt is not None else approx_tokens(len(raw))
            self._reply(request, step, tokens_in, fault)

        def _reply(self, request, step, tokens_in, fault):
            events, final = ENCODERS[request.protocol](request, step, tokens_in)
            if not request.stream:
                request.t_start = now_ns()
                self._json(200, final, request=request)
                return
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("Transfer-Encoding", "chunked")
            self.end_headers()
            out = Chunked(self.wfile)
            request.t_start = now_ns()
            try:
                for n, (name, data) in enumerate(events):
                    if step.event_delay_ms and n:
                        time.sleep(step.event_delay_ms / 1000)
                    if fault and fault.drop_after is not None and n >= fault.drop_after:
                        request.t_end = now_ns()
                        return self._drop(reset=os.name != "nt")
                    head = f"event: {name}\n".encode() if name else b""
                    out.write(head + b"data: " + json.dumps(data, separators=(",", ":")).encode() + b"\n\n")
                if request.protocol == "openai-completions":
                    out.write(b"data: [DONE]\n\n")
                request.t_end = now_ns()  # before the terminating chunk: see Request.t_end
                out.end()
            except OSError:
                if request.t_end is None:
                    request.t_end = now_ns()

    return Handler


def error_body(protocol, status):
    kind = {429: "rate_limit_error", 529: "overloaded_error"}.get(status, "api_error")
    if protocol == "anthropic-messages":
        return {"type": "error", "error": {"type": kind, "message": f"pigbench injected {status}"}}
    return {"error": {"message": f"pigbench injected {status}", "type": kind, "code": kind}}


def words(text):
    parts = [w + " " for w in (text or "").split(" ")] if text else []
    if parts:
        parts[-1] = parts[-1].rstrip(" ")
    return parts


def halves(text):
    mid = len(text) // 2
    return text[:mid], text[mid:]


def encode_chat(request, step, tokens_in):
    ids = [f"call_{request.index}_{i}" for i in range(len(step.tool_calls))]
    parts = words(step.text)
    completion = len(parts) + 12 * len(step.tool_calls)
    usage = {"prompt_tokens": tokens_in, "completion_tokens": completion, "total_tokens": tokens_in + completion}
    finish = "tool_calls" if step.tool_calls else "stop"
    model = request.body.get("model", MODEL)
    message = {"role": "assistant", "content": step.text}
    if step.tool_calls:
        message["tool_calls"] = [{"id": cid, "type": "function", "function": {"name": n, "arguments": json.dumps(a)}} for cid, (n, a) in zip(ids, step.tool_calls)]
    final = {"id": "chatcmpl-bench", "object": "chat.completion", "created": 0, "model": model, "usage": usage, "choices": [{"index": 0, "message": message, "finish_reason": finish}]}
    base = {"id": "chatcmpl-bench", "object": "chat.completion.chunk", "created": 0, "model": model}
    events, first = [], True
    for part in parts:
        delta = {"content": part}
        if first:
            delta["role"] = "assistant"
            first = False
        events.append((None, dict(base, choices=[{"index": 0, "delta": delta, "finish_reason": None}])))
    for i, (cid, (name, arguments)) in enumerate(zip(ids, step.tool_calls)):
        head = {"tool_calls": [{"index": i, "id": cid, "type": "function", "function": {"name": name, "arguments": ""}}]}
        if first:
            head["role"] = "assistant"
            first = False
        events.append((None, dict(base, choices=[{"index": 0, "delta": head, "finish_reason": None}])))
        for piece in halves(json.dumps(arguments)):
            events.append((None, dict(base, choices=[{"index": 0, "delta": {"tool_calls": [{"index": i, "function": {"arguments": piece}}]}, "finish_reason": None}])))
    events.append((None, dict(base, choices=[{"index": 0, "delta": {}, "finish_reason": finish}])))
    events.append((None, dict(base, choices=[], usage=usage)))
    return events, final


def encode_responses(request, step, tokens_in):
    model = request.body.get("model", MODEL)
    parts = words(step.text)
    completion = len(parts) + 12 * len(step.tool_calls)
    usage = {"input_tokens": tokens_in, "input_tokens_details": {"cached_tokens": 0}, "output_tokens": completion, "output_tokens_details": {"reasoning_tokens": 0}, "total_tokens": tokens_in + completion}
    items = []
    if step.text:
        items.append({"id": f"msg_{request.index}", "type": "message", "status": "completed", "role": "assistant", "content": [{"type": "output_text", "text": step.text, "annotations": []}]})
    for i, (name, arguments) in enumerate(step.tool_calls):
        items.append({"id": f"fc_{request.index}_{i}", "type": "function_call", "status": "completed", "call_id": f"call_{request.index}_{i}", "name": name, "arguments": json.dumps(arguments)})
    final = {"id": f"resp_{request.index}", "object": "response", "created_at": 0, "status": "completed", "model": model, "output": items, "usage": usage}
    seq = iter(range(1_000_000))
    events = []

    def emit(name, data):
        events.append((name, dict(data, type=name, sequence_number=next(seq))))

    shell = dict(final, status="in_progress", output=[], usage=None)
    emit("response.created", {"response": shell})
    emit("response.in_progress", {"response": shell})
    for index, item in enumerate(items):
        if item["type"] == "message":
            emit("response.output_item.added", {"output_index": index, "item": dict(item, status="in_progress", content=[])})
            emit("response.content_part.added", {"item_id": item["id"], "output_index": index, "content_index": 0, "part": {"type": "output_text", "text": "", "annotations": []}})
            for part in parts:
                emit("response.output_text.delta", {"item_id": item["id"], "output_index": index, "content_index": 0, "delta": part, "logprobs": []})
            emit("response.output_text.done", {"item_id": item["id"], "output_index": index, "content_index": 0, "text": step.text, "logprobs": []})
            emit("response.content_part.done", {"item_id": item["id"], "output_index": index, "content_index": 0, "part": item["content"][0]})
        else:
            emit("response.output_item.added", {"output_index": index, "item": dict(item, status="in_progress", arguments="")})
            for piece in halves(item["arguments"]):
                emit("response.function_call_arguments.delta", {"item_id": item["id"], "output_index": index, "delta": piece})
            emit("response.function_call_arguments.done", {"item_id": item["id"], "output_index": index, "arguments": item["arguments"]})
        emit("response.output_item.done", {"output_index": index, "item": item})
    emit("response.completed", {"response": final})
    return events, final


def encode_anthropic(request, step, tokens_in):
    model = request.body.get("model", MODEL)
    parts = words(step.text)
    completion = len(parts) + 12 * len(step.tool_calls)
    usage = {"input_tokens": tokens_in, "output_tokens": completion, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0}
    stop = "tool_use" if step.tool_calls else "end_turn"
    blocks = []
    if step.text:
        blocks.append({"type": "text", "text": step.text})
    for i, (name, arguments) in enumerate(step.tool_calls):
        blocks.append({"type": "tool_use", "id": f"toolu_{request.index}_{i}", "name": name, "input": arguments})
    final = {"id": f"msg_{request.index}", "type": "message", "role": "assistant", "model": model, "stop_reason": stop, "stop_sequence": None, "usage": usage, "content": blocks}
    events = [("message_start", {"type": "message_start", "message": dict(final, content=[], stop_reason=None, usage=dict(usage, output_tokens=1))})]
    for index, block in enumerate(blocks):
        if block["type"] == "text":
            events.append(("content_block_start", {"type": "content_block_start", "index": index, "content_block": {"type": "text", "text": ""}}))
            for part in parts:
                events.append(("content_block_delta", {"type": "content_block_delta", "index": index, "delta": {"type": "text_delta", "text": part}}))
        else:
            events.append(("content_block_start", {"type": "content_block_start", "index": index, "content_block": dict(block, input={})}))
            for piece in halves(json.dumps(block["input"])):
                events.append(("content_block_delta", {"type": "content_block_delta", "index": index, "delta": {"type": "input_json_delta", "partial_json": piece}}))
        events.append(("content_block_stop", {"type": "content_block_stop", "index": index}))
    events.append(("message_delta", {"type": "message_delta", "delta": {"stop_reason": stop, "stop_sequence": None}, "usage": {"output_tokens": completion}}))
    events.append(("message_stop", {"type": "message_stop"}))
    return events, final


ENCODERS = {"openai-completions": encode_chat, "openai-responses": encode_responses, "anthropic-messages": encode_anthropic}


class _Backlogged(ThreadingHTTPServer):
    request_queue_size = 256  # the default backlog of 5 drops connections when a loaded host starts many harnesses at once


class MockServer:
    """Serve the mock on 127.0.0.1 with an ephemeral port for the life of a with-block.

    on_request(request) runs in the server thread the moment a request is read; its
    return value is stored as request.hook (the overhead bench reads the harness's CPU there).
    """

    def __init__(self, policy=None, faults=None, on_request=None, retain=None):
        self.policy = policy or scripted_policy([Step(text=REPLY)])
        self.faults = dict(faults or {})  # request index (0-based) -> Fault
        self.on_request = on_request
        self.requests = []
        self.retain = retain  # keep only the last N requests (a soak run would otherwise hold every body); `total` still counts all
        self.total = 0
        self.cond = threading.Condition()
        self.httpd = _Backlogged(("127.0.0.1", 0), make_handler(self))
        self.httpd.daemon_threads = True
        self.port = self.httpd.server_address[1]
        self.origin = f"http://127.0.0.1:{self.port}"
        self.base_url = self.origin + "/v1"

    def begin(self, protocol, body, raw_bytes, t_recv):
        with self.cond:
            request = Request(self.total, protocol, body, raw_bytes, t_recv)
            self.total += 1
            self.requests.append(request)
            if self.retain and len(self.requests) > self.retain:
                del self.requests[:len(self.requests) - self.retain]
            self.cond.notify_all()
        if self.on_request:
            request.hook = self.on_request(request)
        return request

    def fault_for(self, request):
        return self.faults.get(request.index)

    def wait_requests(self, count, timeout):
        """Block until `count` requests have been received; return whether they were."""
        deadline = time.monotonic() + timeout
        with self.cond:
            while len(self.requests) < count:
                left = deadline - time.monotonic()
                if left <= 0:
                    return False
                self.cond.wait(left)
        return True

    def reset(self, policy=None, faults=None):
        with self.cond:
            self.requests = []
            self.total = 0
        if policy:
            self.policy = policy
        self.faults = dict(faults or {})

    def __enter__(self):
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()
        return self

    def __exit__(self, *_):
        self.httpd.shutdown()
        self.httpd.server_close()

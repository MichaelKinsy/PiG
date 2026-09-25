#!/usr/bin/env python3
"""Scripted OpenAI-compatible model server for rehearsing and rendering the PiG launch demo.

No credentials, no external network: Pi and PiG both reach it through a models.json
provider ("demo", api "openai-completions", baseUrl http://127.0.0.1:PORT/v1).
It plays the subagent chain (scout -> planner -> worker) against the
fix-off-by-one repo, streaming text at a readable pace.

    python3 demo-model.py [--port 7878] [--pace 0.03]

The role is read from the system prompt (the subagent example appends the
agent's own prompt: "You are a scout", ...). The step within a turn is the
number of tool results since the last user message.
"""
import argparse
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PACE = 0.03


def text_of(content):
    if isinstance(content, str):
        return content
    return "".join(p.get("text", "") for p in content or [] if isinstance(p, dict))


def role_of(messages):
    system = "".join(text_of(m.get("content")) for m in messages if m.get("role") in ("system", "developer"))
    markers = {"scout": "You are a scout", "planner": "You are a planning specialist",
               "worker": "You are a worker agent", "reviewer": "You are a senior code reviewer"}
    for name, marker in markers.items():
        if marker in system:
            return name
    return "main"


def step_of(messages):
    n = 0
    for m in reversed(messages):
        if m.get("role") == "user":
            break
        if m.get("role") == "tool":
            n += 1
    return n


def last_user(messages):
    for m in reversed(messages):
        if m.get("role") == "user":
            return text_of(m.get("content"))
    return ""


def tool(name, args):
    return {"tool": name, "args": args}


SCOUT_FINDINGS = """## Findings
- `stats.py`: `moving_average(values, window)` builds windows with `range(len(values) - window)`.
- `test_stats.py`: expects **3 windows** for 4 values and window 2, and 1 window for 2 values.
- The range stops one short: the last window is never produced. Classic off-by-one."""

PLAN = """## Plan
1. In `stats.py`, change `range(len(values) - window)` to `range(len(values) - window + 1)`.
2. Keep the `window <= 0` guard unchanged.
3. Run `python3 -m unittest -q test_stats`; all 3 tests must pass."""

WORKER_DONE = """Fixed `moving_average`: the window loop now includes the last window.
`python3 -m unittest -q test_stats`: **3 tests OK**."""

MAIN_DONE = """Chain finished: **scout** found the off-by-one, **planner** wrote a 3-step plan, **worker** fixed `stats.py` and the tests pass."""


def script(messages):
    role, step, user = role_of(messages), step_of(messages), last_user(messages)
    if messages and messages[-1].get("role") == "tool":
        result = text_of(messages[-1].get("content"))
        if result.startswith(("Error:", "error:")) or any(marker in result for marker in ("FAILED (", "Command exited with code")):
            return "The tool failed. Stop the take and inspect the tool output; this is a scripted demo, not a successful fix."
    if role == "scout":
        if step == 0:
            return tool("bash", {"command": "ls && grep -n 'range' stats.py"})
        return SCOUT_FINDINGS
    if role == "planner":
        if step == 0:
            return tool("read", {"path": "stats.py"})
        return PLAN
    if role == "worker":
        if step == 0:
            return tool("edit", {"path": "stats.py", "edits": [{"oldText": "range(len(values) - window)", "newText": "range(len(values) - window + 1)"}]})
        if step == 1:
            return tool("bash", {"command": "python3 -m unittest -q test_stats"})
        return WORKER_DONE
    if role == "reviewer":
        return "Looks good: minimal change, tests pass."
    # main agent: /chain steps ("[chain N/4 · step] ...")
    if user.startswith("[chain "):
        name = user.split("· ", 1)[1].split("]", 1)[0] if "· " in user else ""
        if name == "scout":
            return tool("bash", {"command": "ls && grep -n 'range' stats.py"}) if step == 0 else SCOUT_FINDINGS
        if name == "plan":
            return tool("read", {"path": "stats.py"}) if step == 0 else PLAN
        if name == "build":
            if step == 0:
                return tool("edit", {"path": "stats.py", "edits": [{"oldText": "range(len(values) - window)", "newText": "range(len(values) - window + 1)"}]})
            return "Applied the fix: `range(len(values) - window + 1)` now yields the last window."
        if name == "test":
            return tool("bash", {"command": "python3 -m unittest -v test_stats"}) if step == 0 else "All **3 tests pass**. The chain is done."
    # main agent
    if step == 0 and ("subagent" in user or "chain" in user.lower() or "fix" in user.lower()):
        task = "the failing moving_average tests in test_stats.py"
        return tool("subagent", {"chain": [
            {"agent": "scout", "task": f"Find all code relevant to {task}"},
            {"agent": "planner", "task": f"Create an implementation plan for {task} using this context:\n{{previous}}"},
            {"agent": "worker", "task": "Implement this plan:\n{previous}"},
        ]})
    if step > 0:
        return MAIN_DONE
    if "piglet" in user.lower() or "hello" in user.lower() or "who" in user.lower():
        return "Hello from the local **scripted faux provider**. No hosted model or credentials are used in this demo."
    return "Ready. Try `/implement fix the failing tests`."


def words(text):
    out, cur = [], ""
    for ch in text:
        cur += ch
        if ch in " \n":
            out.append(cur)
            cur = ""
    if cur:
        out.append(cur)
    return out


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_):
        pass

    def _json(self, status, payload):
        data = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path.split("?")[0].endswith("/models"):
            return self._json(200, {"object": "list", "data": [{"id": "demo-1", "object": "model", "created": 0, "owned_by": "demo"}]})
        self._json(404, {"error": {"message": "no route"}})

    def _event(self, data):
        chunk = b"data: " + json.dumps(data, separators=(",", ":")).encode() + b"\n\n"
        self.wfile.write(b"%x\r\n%s\r\n" % (len(chunk), chunk))
        self.wfile.flush()

    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        try:
            body = json.loads(raw or b"{}")
        except ValueError:
            body = {}
        if not self.path.split("?")[0].endswith("/chat/completions"):
            return self._json(404, {"error": {"message": f"no route for {self.path}"}})
        messages = body.get("messages") or []
        out = script(messages)
        last = messages[-1] if messages else {}
        print(json.dumps({"role": role_of(messages), "step": step_of(messages), "last_role": last.get("role"),
                          "last": text_of(last.get("content"))[:300], "reply": out if isinstance(out, dict) else out[:60]}), flush=True)
        model = body.get("model", "demo-1")
        base = {"id": "chatcmpl-demo", "object": "chat.completion.chunk", "created": int(time.time()), "model": model}
        usage = {"prompt_tokens": max(1, len(raw) // 4), "completion_tokens": 40, "total_tokens": max(1, len(raw) // 4) + 40}
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        time.sleep(PACE * 5)
        if isinstance(out, str):
            for i, w in enumerate(words(out)):
                delta = {"role": "assistant", "content": w} if i == 0 else {"content": w}
                self._event(dict(base, choices=[{"index": 0, "delta": delta, "finish_reason": None}]))
                time.sleep(PACE)
            finish = "stop"
        else:
            args = json.dumps(out["args"])
            call_id = f"call_{out['tool']}_{int(time.time() * 1000) % 100000}"
            self._event(dict(base, choices=[{"index": 0, "delta": {"role": "assistant", "tool_calls": [
                {"index": 0, "id": call_id, "type": "function", "function": {"name": out["tool"], "arguments": ""}}]}, "finish_reason": None}]))
            for i in range(0, len(args), 24):
                self._event(dict(base, choices=[{"index": 0, "delta": {"tool_calls": [
                    {"index": 0, "function": {"arguments": args[i:i + 24]}}]}, "finish_reason": None}]))
                time.sleep(PACE / 3)
            finish = "tool_calls"
        self._event(dict(base, choices=[{"index": 0, "delta": {}, "finish_reason": finish}]))
        self._event(dict(base, choices=[], usage=usage))
        done = b"data: [DONE]\n\n"
        self.wfile.write(b"%x\r\n%s\r\n0\r\n\r\n" % (len(done), done))
        self.wfile.flush()


def main():
    global PACE
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=7878)
    ap.add_argument("--pace", type=float, default=PACE)
    a = ap.parse_args()
    PACE = a.pace
    srv = ThreadingHTTPServer(("127.0.0.1", a.port), Handler)
    srv.daemon_threads = True
    print(f"demo model on http://127.0.0.1:{a.port}/v1", flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()

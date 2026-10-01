"""Drive real CLI startup and requests against a loopback provider, never a mock model builder."""

import contextlib
import http.server
import json
import os
import pathlib
import queue
import subprocess
import sys
import tempfile
import threading


class Gateway(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        self.server.requests.append((self.path, self.headers.get("Authorization", "")))
        if self.path == "/v1/oauth/token":
            self.send_response(400)
            self.end_headers()
            self.wfile.write(b'{"error":"invalid_grant","error_description":"fixture refresh rejected"}')
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        if self.path == "/v1/responses":
            self.wfile.write(b'event: response.completed\ndata: {"type":"response.completed","response":{"id":"resp-fixture","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":0}}}\n\n')
        else:
            self.wfile.write(b'data: {"id":"chatcmpl-fixture","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}\n\ndata: [DONE]\n\n')

    def log_message(self, *_args):
        pass


def run(binary, api):
    radius = api == "pi-messages"
    with tempfile.TemporaryDirectory(prefix="credential-lifecycle-") as root, contextlib.ExitStack() as cleanup:
        home = pathlib.Path(root)
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Gateway)
        server.requests = []
        cleanup.callback(server.server_close)
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        cleanup.callback(thread.join)
        cleanup.callback(server.shutdown)
        base = f"http://127.0.0.1:{server.server_port}/v1"
        provider = "radius-dev" if radius else "openai"
        model = "auto" if radius else "gpt-4o-mini"
        config = {"baseUrl": base}
        if radius:
            config.update(oauth="radius", api="pi-messages", models=[{"id": model}])
        else:
            config.update(api=api, models=[{"id": model, "api": api}])
        (home / "models.json").write_text(json.dumps({"providers": {provider: config}}))
        credential = {"type": "oauth", "access": "stale", "refresh": "stale", "expires": 1} if radius else {"type": "api_key", "key": "stored-key"}
        (home / "auth.json").write_text(json.dumps({provider: credential}))
        (home / "settings.json").write_text('{"enableInstallTelemetry":false}')
        env = dict(os.environ, PIG_CODING_AGENT_DIR=root, PI_CODING_AGENT_DIR=root, PI_SKIP_VERSION_CHECK="1", OPENAI_API_KEY="env-key", RADIUS_API_KEY="env-radius")  # gitleaks:allow -- synthetic fixture keys
        for name in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"):
            env.pop(name, None)
        process = subprocess.Popen([binary, "--mode", "rpc", "--offline", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-session", "--model", f"{provider}/{model}"], cwd=root, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        lines = queue.Queue()

        def read_output():
            try:
                for line in process.stdout:
                    lines.put(json.loads(line))
            except json.JSONDecodeError as error:
                lines.put(error)
            finally:
                lines.put(None)

        reader = threading.Thread(target=read_output)
        reader.start()

        def command(payload, terminal):
            process.stdin.write(json.dumps(payload) + "\n")
            process.stdin.flush()
            events = []
            while True:
                event = lines.get(timeout=20)
                if isinstance(event, Exception):
                    raise event
                if event is None:
                    raise AssertionError(f"CLI exited before {terminal}: {process.stderr.read()}")
                events.append(event)
                if event.get("type") == terminal:
                    return events

        def prompt(payload):
            # Pi clears the active-run state only when it emits agent_settled, after agent_end (agent-session.ts:_emitAgentSettled), and rejects a prompt sent in between ("Agent is already processing"). Wait for agent_settled like rpc-client.ts:waitForIdle, then return the events through agent_end.
            events = command(payload, "agent_settled")
            end = next((i for i, event in enumerate(events) if event.get("type") == "agent_end"), None)
            assert end is not None, events
            return events[: end + 1]

        try:
            state = command({"type": "get_state", "id": "state"}, "response")[-1]
            assert state["success"], state
            assert state["data"]["model"]["id"] == model, state
            assert server.requests == [], server.requests
            assert state["data"]["model"]["api"] == api, state
            print(f"{provider}/{api}: startup model={model} requests=0")
            events = prompt({"type": "prompt", "message": "hello", "id": "first"})
            if radius:
                assert server.requests == [("/v1/oauth/token", "")], server.requests
                encoded = json.dumps(events)
                assert "refresh" in encoded.lower() and "invalid_grant" in encoded, encoded
                assert '"stopReason": "error"' in encoded, encoded
                print("radius-dev: first request refresh=failed ambient-fallback=false")
            else:
                path = "/v1/responses" if api == "openai-responses" else "/v1/chat/completions"
                assert server.requests == [(path, "Bearer stored-key")], server.requests
                assert events[-1]["messages"][-1]["stopReason"] == "stop", events
                # /logout deletes the credential; do not rebuild the running startup model.
                (home / "auth.json").write_text("{}")
                events = prompt({"type": "prompt", "message": "again", "id": "second"})
                assert events[-1]["messages"][-1]["stopReason"] == "stop", events
                assert server.requests == [(path, "Bearer stored-key"), (path, "Bearer env-key")], server.requests
                print(f"openai/{api}: request keys=stored-key,env-key after credential deletion")
        finally:
            process.stdin.close()
            shutdown_timed_out = False
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
                shutdown_timed_out = True
            reader.join()
            process.stdout.close()
            process.stderr.close()
            assert not shutdown_timed_out, "CLI did not stop after stdin EOF"


binary = os.environ["PIG_PARITY_PIG_BIN"] if sys.argv[1] == "pig" else os.environ["PIG_PARITY_PI_BIN"]
for api in ("pi-messages", "openai-completions", "openai-responses"):
    run(binary, api)

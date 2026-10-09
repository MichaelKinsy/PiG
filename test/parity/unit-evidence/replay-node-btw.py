#!/usr/bin/env python3
"""Replay the locked pi-btw package against Pi and PiG with a hermetic provider."""
import argparse
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import time
import uuid


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--npm", type=Path, required=True, help="locked corpus npm directory")
    parser.add_argument("--pi", type=Path, required=True)
    parser.add_argument("--pig", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--trace", action="store_true", help="record PiG host calls for diagnosis")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    manifest = json.loads((args.npm / "node_modules/pi-btw/package.json").read_text())
    lock = args.npm / "package-lock.json"
    lock_data = json.loads(lock.read_text())
    records = []
    cancel_received = threading.Event()
    release_cancel = threading.Event()

    class Provider(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["content-length"])))
            records.append(body)
            users = [m for m in body["messages"] if m["role"] == "user"]
            text = json.dumps(users[-1]["content"])
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            if "cancel-side" in text:
                self.wfile.flush()
                cancel_received.set()
                release_cancel.wait()
                return
            answer = "SIDE ANSWER" if "side-question" in text else "MAIN ANSWER"
            chunk = {"id": "corpus", "choices": [{"delta": {"content": answer}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18}}
            self.wfile.write(("data: " + json.dumps(chunk) + "\n\ndata: [DONE]\n\n").encode())
            self.wfile.flush()

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Provider)
    worker = threading.Thread(target=server.serve_forever)
    worker.start()
    label = "parity-child-corpus-" + uuid.uuid4().hex[:10]
    def tmux(*cmd):
        return subprocess.run(["tmux", "-L", label, *cmd], check=True, capture_output=True, text=True).stdout
    results = {"package": manifest["name"], "version": manifest["version"], "lock_sha256": hashlib.sha256(lock.read_bytes()).hexdigest(), "integrity": lock_data["packages"]["node_modules/pi-btw"]["integrity"], "runs": []}
    try:
        with tempfile.TemporaryDirectory(prefix="parity-child-corpus-") as root:
            for repetition in range(3):
                for variant in ("pi", "pig"):
                    home = Path(root) / f"{variant}-{repetition}"
                    cwd = home / "work"
                    cwd.mkdir(parents=True)
                    agent = home / (".pi" if variant == "pi" else ".pig") / "agent"
                    agent.mkdir(parents=True)
                    shutil.copytree(args.npm, agent / "npm", symlinks=True)
                    assert hashlib.sha256((agent / "npm/package-lock.json").read_bytes()).hexdigest() == results["lock_sha256"]
                    probe = home / "corpus-ready"
                    probe.mkdir()
                    (probe / "package.json").write_text(json.dumps({"name": "corpus-ready", "pi": {"extensions": ["ready.mjs"]}}))
                    ready_source = 'export default pi => pi.on("session_start", (_event, ctx) => ctx.ui.setStatus("corpus-ready", "CORPUS READY"));\n'
                    if args.trace and variant == "pig":
                        ready_source = f'import {{ appendFileSync }} from "node:fs";\nconst traceFile = {json.dumps(str(home / "trace.log"))};\n' + '''import * as sdk from "@earendil-works/pi-coding-agent";
const trace = (...args) => appendFileSync(traceFile, JSON.stringify(args) + "\\n");
trace("paths", sdk.getAgentDir(), process.cwd(), process.env.PIG_CODING_AGENT_DIR);
for (const [target, method] of [[sdk.ModelRuntime, "create"], [sdk.ModelRuntime.prototype, "getAuth"], [sdk.ModelRuntime.prototype, "refresh"], [sdk.DefaultResourceLoader.prototype, "reload"]]) {
 const original = target[method];
 target[method] = async function(...args) {
  trace("sdk enter", method, args);
  try { const result = await original.apply(this, args); trace("sdk done", method); return result; }
  catch (error) { trace("sdk error", method, String(error)); throw error; }
 };
}
''' + ready_source
                    (probe / "ready.mjs").write_text(ready_source)
                    (agent / "settings.json").write_text(json.dumps({"lastChangelogVersion": "0.87.1", "packages": ["npm:pi-btw", str(probe)], "theme": "dark", "cacheWarming": {"mode": "off"}}))
                    (agent / "models.json").write_text(json.dumps({"providers": {"e2e": {"api": "openai-completions", "apiKey": "corpus-key", "baseUrl": f"http://127.0.0.1:{server.server_port}/v1", "models": [{"id": "e2e-model", "name": "E2E", "reasoning": False, "input": ["text"], "contextWindow": 32768, "maxTokens": 1024, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}]}}}))
                    out = args.output / f"{variant}-{repetition}"
                    out.mkdir()
                    session = "parity-btw-" + variant + str(repetition)
                    env = {k: v for k, v in os.environ.items() if k in ("PATH", "LANG", "LC_ALL", "TZ")}
                    env.update(HOME=str(home), TERM="xterm-256color", COLORTERM="truecolor", PI_OFFLINE="1", PIG_OFFLINE="1", PI_SKIP_VERSION_CHECK="1", NODE_DISABLE_COMPILE_CACHE="1", TMPDIR=root)
                    import shlex
                    binary = args.pi if variant == "pi" else args.pig
                    command = "exec env -i " + " ".join(shlex.quote(k + "=" + v) for k, v in env.items()) + " " + shlex.join([str(binary.resolve()), "--model", "e2e/e2e-model"]) + " 2>" + shlex.quote(str((out / "stderr").resolve()))
                    start = len(records)
                    cancel_received.clear()
                    release_cancel.clear()
                    try:
                        tmux("-f", "/dev/null", "new-session", "-d", "-s", session, "-x", "170", "-y", "55", "-c", str(cwd), command)
                        def pane():
                            return tmux("capture-pane", "-p", "-t", session)
                        def wait(text, absent=False):
                            deadline = time.monotonic() + 25
                            while time.monotonic() < deadline:
                                value = pane()
                                if (text in value) != absent:
                                    return value
                                time.sleep(0.05)
                            raise AssertionError(f"{variant}: did not reach {text!r}:\n{pane()}")
                        def send(text):
                            tmux("send-keys", "-t", session, "-l", text)
                            tmux("send-keys", "-t", session, "Enter")
                        def capture(stage):
                            for escaped in (False, True):
                                (out / (stage + (".ansi" if escaped else ".txt"))).write_text(tmux("capture-pane", "-p", *(["-e"] if escaped else []), "-t", session, "-S", "-300"))
                        wait("CORPUS READY")
                        send("/btw side-question")
                        wait("SIDE ANSWER")
                        wait("Ready for a follow-up.")
                        capture("side")
                        send("cancel-side")
                        assert cancel_received.wait(25), "cancel-side did not reach the provider"
                        tmux("send-keys", "-t", session, "Escape")
                        wait("Aborted")
                        capture("abort")
                        tmux("send-keys", "-t", session, "Escape")
                        wait("BTW ·", absent=True)
                        send("main-question")
                        wait("MAIN ANSWER")
                        capture("main")
                        own = records[start:]
                        assert len(own) == 3, own
                        assert "side-question" not in json.dumps(own[-1]["messages"]), "child conversation leaked into parent"
                        (out / "requests.json").write_text(json.dumps(own, indent=2) + "\n")
                        results["runs"].append({"variant": variant, "repetition": repetition, "side": "SIDE ANSWER", "abort": True, "main": "MAIN ANSWER", "parentIndependent": True})
                        if variant == "pig":
                            def panel(path):
                                lines = path.read_text().splitlines()
                                top = next(index for index, line in enumerate(lines) if "┌" in line)
                                left = lines[top].index("┌")
                                right = lines[top].index("┐") + 1
                                bottom = next(index for index in range(top, len(lines)) if "└" in lines[index])
                                return "\n".join(line[left:right] for line in lines[top:bottom + 1])
                            # Compare the complete owning panel without normalizing its whitespace.
                            # Parent identity/footer text outside the panel remains in the raw panes.
                            assert panel(args.output / f"pi-{repetition}" / "side.txt") == panel(out / "side.txt")
                            results["runs"][-1]["overlayEqual"] = True
                    finally:
                        if (home / "trace.log").exists():
                            shutil.copy2(home / "trace.log", out / "trace.log")
                        (out / "requests.json").write_text(json.dumps(records[start:], indent=2) + "\n")
                        capture("final")
                        release_cancel.set()
                        tmux("kill-session", "-t", session)
    finally:
        release_cancel.set()
        server.shutdown()
        worker.join()
        server.server_close()
        (args.output / "result.json").write_text(json.dumps(results, indent=2) + "\n")


if __name__ == "__main__":
    main()

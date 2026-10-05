#!/usr/bin/env python3
"""Real CLI probe for models.json samplingParamsByThinkingLevel (Pi 1.0.2 #9776).

Each case runs the real CLI in print mode against a local OpenAI-compatible
server with one models.json custom provider. The server records the sampling
keys and reasoning fields of the request body; every recorded field is printed.
"""
import argparse
import http.server
import json
import os
from pathlib import Path
import subprocess
import tempfile
import threading

ROOT = Path(__file__).resolve().parents[3]
FIELDS = ("temperature", "top_p", "top_k", "min_p", "reasoning_effort", "reasoning")

MODELS = {"providers": {"sampling": {
    "api": "openai-completions", "apiKey": "not-a-secret",
    "models": [
        # low and medium are unsupported, so they clamp up to high.
        {"id": "completions", "name": "Completions", "reasoning": True, "input": ["text"],
         "contextWindow": 128000, "maxTokens": 4096,
         "thinkingLevelMap": {"low": None, "medium": None},
         "samplingParams": {"temperature": 1, "top_p": 0.95},
         "samplingParamsByThinkingLevel": {"off": {"temperature": 0.7}, "high": {"temperature": 0.8, "top_k": 64}}},
        # xhigh is unsupported without a thinkingLevelMap entry, so it clamps down to high.
        {"id": "responses", "name": "Responses", "api": "openai-responses", "reasoning": True, "input": ["text"],
         "contextWindow": 128000, "maxTokens": 4096,
         "samplingParams": {"top_p": 0.9},
         "samplingParamsByThinkingLevel": {"low": {"temperature": 0.6}, "high": {"temperature": 0.4}, "xhigh": {"temperature": 0.3}}},
        {"id": "plain", "name": "Plain", "reasoning": False, "input": ["text"],
         "contextWindow": 128000, "maxTokens": 4096,
         "samplingParamsByThinkingLevel": {"off": {"top_k": 5}, "high": {"top_k": 50}}},
    ],
    # Override entries merge per level and per key over the model's own levels.
    "modelOverrides": {"completions": {"samplingParamsByThinkingLevel": {"high": {"top_k": 20, "min_p": 0.05}, "max": {"temperature": 2}}}},
}}}

# Pi 1.0.2 provider-composer.ts extensionModelFromDefinition spreads a registered
# chat definition, so an extension model's samplingParamsByThinkingLevel applies too.
EXTENSION = """export default function (pi) {
  pi.registerProvider("sampling-ext", {
    baseUrl: process.env.SAMPLING_URL, apiKey: "not-a-secret", api: "openai-completions",
    models: [{ id: "extension", name: "Extension", reasoning: true, input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096,
      samplingParams: { top_p: 0.5 },
      samplingParamsByThinkingLevel: { off: { temperature: 0.1 }, low: { temperature: 0.3, min_p: 0.2 }, high: { temperature: 0.9, top_k: 7 } } }],
  });
}
"""


class Recorder(http.server.BaseHTTPRequestHandler):
    bodies = []

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        Recorder.bodies.append({key: body[key] for key in FIELDS if key in body})
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        if self.path.endswith("/responses"):
            events = [
                {"type": "response.output_item.added", "output_index": 0, "item": {"type": "message", "id": "m", "role": "assistant", "status": "in_progress", "content": []}},
                {"type": "response.content_part.added", "output_index": 0, "content_index": 0, "item_id": "m", "part": {"type": "output_text", "text": "", "annotations": []}},
                {"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": "m", "delta": "ok"},
                {"type": "response.output_item.done", "output_index": 0, "item": {"type": "message", "id": "m", "role": "assistant", "status": "completed", "content": [{"type": "output_text", "text": "ok", "annotations": []}]}},
                {"type": "response.completed", "response": {"status": "completed", "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
            ]
        else:
            events = [{"choices": [{"index": 0, "delta": {"content": "ok"}, "finish_reason": None}]},
                      {"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]}]
        self.wfile.write("".join(f"data: {json.dumps(event)}\n\n" for event in events).encode())
        self.wfile.write(b"data: [DONE]\n\n")

    def log_message(self, *_):
        pass


def run(binary, model, thinking, url):
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        home, agent, cwd = root / "home", root / "agent", root / "work"
        for path in (home, agent, cwd):
            path.mkdir()
        config = json.loads(json.dumps(MODELS))
        config["providers"]["sampling"]["baseUrl"] = url
        (agent / "models.json").write_text(json.dumps(config))
        (agent / "auth.json").write_text("{}")
        (agent / "settings.json").write_text(json.dumps({"quietStartup": True, "enableInstallTelemetry": False,
                                                         "compaction": {"enabled": False}, "retry": {"enabled": False}}))
        provider, extension = "sampling", []
        if model == "extension":
            (root / "sampling-ext.mjs").write_text(EXTENSION)
            provider, extension = "sampling-ext", ["-e", str(root / "sampling-ext.mjs")]
        env = {"PATH": os.environ["PATH"], "HOME": str(home), "LANG": "C.UTF-8", "SAMPLING_URL": url,
               "PI_CODING_AGENT_DIR": str(agent), "PIG_CODING_AGENT_DIR": str(agent),
               "PIG_HOME": str(home / ".pig"), "PI_OFFLINE": "1", "PIG_OFFLINE": "1", "PI_TELEMETRY": "0"}
        args = [*extension, "--provider", provider, "--model", model, "--thinking", thinking, "--no-session",
                "--system-prompt", "Sampling audit.", "--no-context-files", "--no-skills", "--no-extensions",
                "--no-prompt-templates", "--no-themes", "-p", "hi"]
        Recorder.bodies.clear()
        result = subprocess.run([*binary, *args], cwd=cwd, env=env, capture_output=True, timeout=60)
        return {"model": model, "thinking": thinking, "exit": result.returncode,
                "stdout": result.stdout.decode(), "requests": list(Recorder.bodies)}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--bin", default=os.environ.get("PIG_PARITY_PIG_BIN"))
    options = parser.parse_args()
    if not options.bin:
        parser.error("--bin or PIG_PARITY_PIG_BIN is required")
    binary = [str((ROOT / options.bin).resolve()) if not os.path.isabs(options.bin) else options.bin]
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Recorder)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    url = f"http://127.0.0.1:{server.server_address[1]}/v1"
    try:
        for model in ("completions", "responses", "plain", "extension"):
            for thinking in ("off", "minimal", "low", "medium", "high", "xhigh"):
                print(json.dumps(run(binary, model, thinking, url), sort_keys=True, separators=(",", ":")), flush=True)
    finally:
        server.shutdown()


if __name__ == "__main__":
    main()

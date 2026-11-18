#!/usr/bin/env python3
"""FA-09 end-to-end probe: a loopback fake model streams an `edit` call whose arguments carry the gpt-6.1-sol
`"oldText">` / `"newText">` corruption. Passes when pig applies the edit, the next request carries a successful tool
result, and <agentDir>/logs/tool-argument-repairs.jsonl has exactly one tag-arrow line. Usage: probe.py --pig BIN"""

import http.server
import json
import os
import subprocess
import sys
import tempfile
import threading
from pathlib import Path


def main():
    binary = Path(sys.argv[sys.argv.index("--pig") + 1]).resolve()
    requests = []
    with tempfile.TemporaryDirectory(prefix="fa09-probe-") as work:
        work = Path(work)
        target = work / "cache.py"
        target.write_text("def get(index: int) -> int:\n    return index\n")
        args = (
            '{"path": "'
            + str(target)
            + '", "edits": [{"oldText">def get(index: int) -> int:\\n    return index\\n", '
            '"newText">def get(index: int) -> int:\\n    return index + 1\\n"}]}'
        )

        class Model(http.server.BaseHTTPRequestHandler):
            def log_message(self, format: str, *args: object) -> None:
                pass

            def do_POST(self):
                requests.append(
                    json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                )
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.end_headers()

                def chunk(delta, finish=None):
                    c = {
                        "id": "p",
                        "object": "chat.completion.chunk",
                        "created": 0,
                        "model": "test",
                        "choices": [
                            {"index": 0, "delta": delta, "finish_reason": finish}
                        ],
                    }
                    self.wfile.write(("data: " + json.dumps(c) + "\n\n").encode())

                if len(requests) == 1:
                    chunk(
                        {
                            "role": "assistant",
                            "tool_calls": [
                                {
                                    "index": 0,
                                    "id": "call_1",
                                    "type": "function",
                                    "function": {"name": "edit", "arguments": ""},
                                }
                            ],
                        }
                    )
                    for i in range(
                        0, len(args), 17
                    ):  # stream the arguments in pieces, as providers do
                        chunk(
                            {
                                "tool_calls": [
                                    {
                                        "index": 0,
                                        "function": {"arguments": args[i : i + 17]},
                                    }
                                ]
                            }
                        )
                    chunk({}, "tool_calls")
                else:
                    chunk({"role": "assistant", "content": "done"})
                    chunk({}, "stop")
                self.wfile.write(b"data: [DONE]\n\n")
                self.wfile.flush()

        model = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Model)
        threading.Thread(target=model.serve_forever, daemon=True).start()
        agent = work / "agent"
        agent.mkdir()
        (agent / "settings.json").write_text("{}")
        (agent / "models.json").write_text(
            json.dumps(
                {
                    "providers": {
                        "fixture": {
                            "api": "openai-completions",
                            "baseUrl": f"http://127.0.0.1:{model.server_port}/v1",
                            "apiKey": "fixture",
                            "models": [
                                {
                                    "id": "test",
                                    "name": "Test",
                                    "contextWindow": 100000,
                                    "maxTokens": 4096,
                                }
                            ],
                        }
                    }
                }
            )
        )
        env = {
            **os.environ,
            "HOME": str(work),
            "PIG_HOME": str(work / "home"),
            "PI_HOME": str(work / "home"),
            "PIG_CODING_AGENT_DIR": str(agent),
            "PI_CODING_AGENT_DIR": str(agent),
            "PIG_OFFLINE": "1",
            "PI_SKIP_VERSION_CHECK": "1",
        }
        out = subprocess.run(
            [
                str(binary),
                "--model",
                "fixture/test",
                "--no-skills",
                "--no-prompt-templates",
                "--no-context-files",
                "--no-session",
                "--approve",
                "-p",
                "edit cache.py",
            ],
            check=False,
            cwd=work,
            env=env,
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            timeout=120,
        )
        model.shutdown()
        checks = {}
        checks["file edited"] = (
            target.read_text() == "def get(index: int) -> int:\n    return index + 1\n"
        )
        tool_msgs = [
            m
            for m in (requests[1]["messages"] if len(requests) > 1 else [])
            if m.get("role") == "tool"
        ]
        result = json.dumps(tool_msgs)
        checks["tool result ok"] = (
            bool(tool_msgs)
            and "Validation failed" not in result
            and "Expected ':'" not in result
        )
        log = agent / "logs" / "tool-argument-repairs.jsonl"
        lines = log.read_text().splitlines() if log.exists() else []
        checks["one repair logged"] = len(lines) == 1 and json.loads(lines[0])[
            "keys"
        ] == ["oldText", "newText"]
        for k, v in checks.items():
            print(("PASS " if v else "FAIL ") + k)
        if not all(checks.values()):
            print(
                "pig exit",
                out.returncode,
                out.stdout[-500:],
                out.stderr[-800:],
                result[:500],
            )
            sys.exit(1)


if __name__ == "__main__":
    main()

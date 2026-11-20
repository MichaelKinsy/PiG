#!/usr/bin/env python3
"""Probe missing-finish diagnostics with a loopback synthetic completions model.

The persisted failed message must retain unchanged error text, request identity,
recent records and timing without credential headers. Usage: --pig BIN.
"""

import http.server
import json
import os
import subprocess
import sys
import tempfile
import threading
from pathlib import Path
from typing import NotRequired, TypedDict, cast, override


class TraceDetails(TypedDict):
    httpStatus: int
    headers: dict[str, str]
    doneReceived: bool
    records: int
    bytes: int
    msSinceRecord: int | None
    lastRecords: list[str]


class Diagnostic(TypedDict):
    type: str
    details: TraceDetails


class Message(TypedDict):
    errorMessage: NotRequired[str]
    diagnostics: NotRequired[list[Diagnostic]]


class Entry(TypedDict):
    message: NotRequired[Message]


def main() -> None:
    binary = Path(sys.argv[sys.argv.index("--pig") + 1]).resolve()
    hits: list[int] = []

    class Model(http.server.BaseHTTPRequestHandler):
        @override
        def log_message(self, format: str, *args: object) -> None:
            pass

        def do_POST(self) -> None:
            _ = self.rfile.read(int(self.headers["Content-Length"]))
            hits.append(1)
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("X-Request-Id", f"req-{len(hits)}")
            self.send_header("X-GitHub-Request-Id", f"GH:{len(hits)}")
            self.send_header("Set-Cookie", "s=secret")
            self.end_headers()
            for text in ["Hel", "lo"]:
                c = {
                    "id": "p",
                    "object": "chat.completion.chunk",
                    "created": 0,
                    "model": "test",
                    "choices": [{"index": 0, "delta": {"content": text}}],
                }
                _ = self.wfile.write(("data: " + json.dumps(c) + "\n\n").encode())
            self.wfile.flush()  # then close: no finish_reason, no [DONE]

    model = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Model)
    threading.Thread(target=model.serve_forever, daemon=True).start()
    with tempfile.TemporaryDirectory(prefix="nofinish-probe-") as work:
        work = Path(work)
        agent = work / "agent"
        agent.mkdir()
        _ = (agent / "settings.json").write_text(
            json.dumps({"retry": {"enabled": False}})
        )
        _ = (agent / "models.json").write_text(
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
        _ = subprocess.run(
            [
                str(binary),
                "--model",
                "fixture/test",
                "--no-skills",
                "--no-prompt-templates",
                "--no-context-files",
                "--session-dir",
                str(work / "sessions"),
                "-p",
                "hi",
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
        model.server_close()
        found: list[tuple[str | None, TraceDetails]] = []
        for session in (work / "sessions").rglob("*.jsonl"):
            for line in session.read_text().splitlines():
                entry = cast(Entry, json.loads(line))
                message = entry.get("message")
                if message is not None:
                    for diagnostic in message.get("diagnostics", []):
                        if diagnostic["type"] == "stream_ended_without_finish_reason":
                            found.append(
                                (message.get("errorMessage"), diagnostic["details"])
                            )
        checks: dict[str, bool] = {"diagnostic saved in session entry": bool(found)}
        if found:
            err, det = found[0]
            checks["error message unchanged"] = (
                err == "Stream ended without finish_reason"
            )
            checks["status + request ids"] = (
                det["httpStatus"] == 200
                and det["headers"].get("x-request-id", "").startswith("req-")
                and det["headers"].get("x-github-request-id", "").startswith("GH:")
            )
            checks["no cookie"] = "set-cookie" not in det["headers"]
            checks["done flag false"] = det["doneReceived"] is False
            checks["last records"] = (
                len(det["lastRecords"]) == 2 and '"lo"' in det["lastRecords"][1]
            )
            checks["timing"] = (
                isinstance(det.get("msSinceRecord"), int)
                and det["records"] == 2
                and det["bytes"] > 0
            )
            print(json.dumps(det)[:600])
        for k, v in checks.items():
            print(("PASS " if v else "FAIL ") + k)
        sys.exit(0 if all(checks.values()) else 1)


if __name__ == "__main__":
    main()

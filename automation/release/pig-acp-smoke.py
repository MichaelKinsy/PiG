#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Smoke-test an extracted pig-acp release archive through ACP.

Starts the archive's pig-acp from a working directory outside the archive,
sends `initialize` and `session/new` over stdio, and requires a protocol
version, a started session, and a clean exit once stdin closes. `session/new`
makes pig-acp start the archive's own `pig --mode rpc` and ask it for its models,
so the check proves the two bundled executables work together. It then repeats the session with a
relative `--pig ./pig` and with no `--pig`, from an unrelated directory and a PATH without any pig, and
requires pig-acp to find the pig beside itself. A started session
is a session id, or, when pig has no credentials (CI), pig-acp's authentication
error offering the terminal login. The caller names both executables.

    pig-acp-smoke.py --version X.Y.Z --pig-acp PATH --pig PATH
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import subprocess
import tempfile
import threading

TIMEOUT = 60


def request(identifier: int, method: str, params: dict) -> str:
    return json.dumps({"jsonrpc": "2.0", "id": identifier, "method": method, "params": params}) + "\n"


def smoke(pig_acp: pathlib.Path, pig: pathlib.Path, version: str) -> None:
    reported = subprocess.run([str(pig_acp), "--version"], capture_output=True, text=True, timeout=TIMEOUT, check=True).stdout
    if not reported.startswith(f"pig-acp {version} "):
        raise SystemExit(f"pig-acp --version printed {reported!r}, want it to start with 'pig-acp {version} '")
    session(pig_acp, ["--pig", str(pig)])
    # A client that starts `./pig-acp` or `pig-acp` without an absolute --pig relies on the pig beside
    # pig-acp: a relative --pig and the default must resolve there, not against the session's working
    # directory or PATH.
    if pig.parent != pig_acp.parent:
        raise SystemExit(f"{pig} is not beside {pig_acp}")
    session(pig_acp, ["--pig", f"./{pig.name}"], path_without_pig=pig.name)
    session(pig_acp, [], path_without_pig=pig.name)


def session(pig_acp: pathlib.Path, args: list[str], path_without_pig: str | None = None) -> None:
    with tempfile.TemporaryDirectory() as home, tempfile.TemporaryDirectory() as cwd:
        env = {**os.environ, "PIG_HOME": home, "PI_HOME": home, "PIG_CODING_AGENT_DIR": home, "PI_CODING_AGENT_DIR": home}
        if path_without_pig:
            env["PATH"] = os.pathsep.join(d for d in os.environ.get("PATH", "").split(os.pathsep) if d and not (pathlib.Path(d) / path_without_pig).exists())
            env.pop("PIG_ACP_PIG_COMMAND", None)
            env.pop("PI_ACP_PI_COMMAND", None)
        proc = subprocess.Popen([str(pig_acp), *args], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, env=env, cwd=cwd)
        timer = threading.Timer(TIMEOUT, proc.kill)
        timer.start()
        try:
            proc.stdin.write(request(1, "initialize", {"protocolVersion": 1, "clientCapabilities": {}}))
            proc.stdin.flush()
            initialize = read_response(proc, 1)
            if initialize.get("result", {}).get("protocolVersion") != 1:
                raise SystemExit(f"initialize returned {initialize!r}")
            proc.stdin.write(request(2, "session/new", {"cwd": cwd, "mcpServers": []}))
            proc.stdin.flush()
            created = read_response(proc, 2)
            if not session_started(created):
                raise SystemExit(f"session/new returned {created!r}")
            proc.stdin.close()
            if proc.wait(timeout=TIMEOUT) != 0:
                raise SystemExit(f"pig-acp exited {proc.returncode} after stdin closed: {proc.stderr.read()}")
        finally:
            timer.cancel()
            if proc.poll() is None:
                proc.kill()
                proc.wait()


def session_started(response: dict) -> bool:
    """A session id, or the authentication error pig-acp returns once the bundled pig answered
    over RPC but has no model credentials. CI has none; a pig that did not start fails with a
    different error ("Could not start pig")."""
    if response.get("result", {}).get("sessionId"):
        return True
    error = response.get("error") or {}
    methods = (error.get("data") or {}).get("authMethods") or []
    return error.get("code") == -32000 and str(error.get("message", "")).startswith("Authentication required") and any(m.get("id") == "pi_terminal_login" for m in methods)


def read_response(proc: subprocess.Popen, identifier: int) -> dict:
    # Notifications and agent requests may precede the response; skip them.
    for line in proc.stdout:
        message = json.loads(line)
        if message.get("id") == identifier and "method" not in message:
            return message
    raise SystemExit(f"pig-acp closed stdout before answering request {identifier}: {proc.stderr.read()}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--version", required=True, help="pig-acp version the archive must report")
    parser.add_argument("--pig-acp", type=pathlib.Path, required=True)
    parser.add_argument("--pig", type=pathlib.Path, required=True)
    args = parser.parse_args()
    smoke(args.pig_acp.resolve(), args.pig.resolve(), args.version)
    print("pig-acp smoke: ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

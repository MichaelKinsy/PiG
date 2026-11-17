"""Observe real offline startup in print and RPC mode where the agent directory cannot be made."""

import json
import os
import re
from pathlib import Path
import subprocess
import sys
import tempfile

binary = os.environ["PIG_PARITY_PIG_BIN" if sys.argv[1] == "pig" else "PIG_PARITY_PI_BIN"]
with tempfile.TemporaryDirectory(prefix="startup-unmakeable-") as root:
    root_path = Path(root)
    (root_path / "file").write_bytes(b"x")
    locked = root_path / "locked"
    locked.mkdir(mode=0o500)
    locked.chmod(0o500)
    try:
        (locked / "probe").write_bytes(b"")
        (locked / "probe").unlink()
        writable = True
    except OSError:
        writable = False
    cases = [("file where the directory should be", root_path / "file" / "agent")]
    if not writable:
        cases.append(("unwritable parent", locked / "agent"))
    else:
        print("unwritable parent: this process can write there, so the case is not made")
    flags = ["--offline", "--model", "openai/gpt-4o-mini", "--api-key", "startup-fixture-key",
             "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes"]

    def run(agent, extra, stdin):
        env = dict(os.environ, HOME=root, PIG_HOME=str(root_path / ".pig"),
                   PIG_CODING_AGENT_DIR=str(agent), PI_CODING_AGENT_DIR=str(agent))
        return subprocess.run([binary, *flags, *extra], cwd=root, env=env, input=stdin,
                              capture_output=True, timeout=60)

    def error_of(data):
        # Pi ends with an uncaught Node error, whose text is a crash dump with a stack; PiG prints the same error as one line.
        # The comparison is the exit status and the Node mkdir error itself, with the session directory's encoded working directory name masked.
        match = re.search(r"[A-Z]+: [a-z ]+, mkdir '[^']*'", data.decode().replace(root, "<root>"))
        return re.sub(r"/--[^/']*--'", "/--<cwd>--'", match.group(0)) if match else data.decode().strip()

    for name, agent in cases:
        # --no-session: the session manager is in memory, and the startup ends at the project trust store's lock, which makes the agent directory.
        result = run(agent, ["--print", "--no-session"], b"")
        print(f"{name}: print --no-session: exit={result.returncode} stdout={result.stdout.decode()!r} error={error_of(result.stderr)!r}")
        # The default session manager makes <agent>/sessions/<cwd> when the startup creates the session, before the trust store.
        result = run(agent, ["--print"], b"")
        print(f"{name}: print default session: exit={result.returncode} stdout={result.stdout.decode()!r} error={error_of(result.stderr)!r}")
        result = run(agent, ["--mode", "rpc", "--no-session"], b'{"id":"1","type":"get_state"}\n')
        print(f"{name}: rpc --no-session: exit={result.returncode} stdout={result.stdout.decode()!r} error={error_of(result.stderr)!r}")
        result = run(agent, ["--mode", "rpc"], b'{"id":"1","type":"get_state"}\n')
        print(f"{name}: rpc default session: exit={result.returncode} stdout={result.stdout.decode()!r} error={error_of(result.stderr)!r}")

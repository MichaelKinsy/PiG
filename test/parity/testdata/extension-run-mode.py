"""Run the real CLI in interactive (PTY), print, json and rpc mode with an extension that records ctx.mode at session_start.

Pi binds its extensions with mode "tui" (interactive-mode.ts), "rpc" (rpc-mode.ts), and "json" or "print" (print-mode.ts), so the extension
reads that mode in each run. A host that never sends the mode leaves every extension on its "print" default. The output is the recorded mode
per run mode, identical for Pi and PiG.
"""
import errno
import json
import os
from pathlib import Path
import pty
import select
import struct
import subprocess
import sys
import tempfile
import time
import fcntl
import termios


SIDE = sys.argv[1]
ROOT = Path(__file__).resolve().parents[3]
BINARY = ([os.environ.get("PIG_PARITY_PIG_BIN", str(ROOT / "bin/pig"))]
          if SIDE == "pig" else [os.environ.get("PIG_PARITY_PI_BIN", str(ROOT / "extensions/sdk-ts/node_modules/.bin/pi"))])
EXTENSION = str(Path(__file__).resolve().parent / "extension-run-mode.mjs")


def recorded(report):
    if not report.exists():
        return None
    lines = report.read_text().split()
    return lines[0] if len(lines) == 1 else lines


def run(root, name, args, interactive):
    agent = root / name / "agent"
    agent.mkdir(parents=True)
    work = root / name / "work"
    work.mkdir()
    report = root / name / "report"
    (agent / "settings.json").write_text(json.dumps({"quietStartup": True, "enableInstallTelemetry": False}))
    env = {**os.environ, "HOME": str(root / name), "PIG_CODING_AGENT_DIR": str(agent), "PI_CODING_AGENT_DIR": str(agent),
           "PI_SKIP_VERSION_CHECK": "1", "TERM": "xterm-256color", "RUN_MODE_REPORT": str(report)}
    command = [*BINARY, "--offline", "--no-extensions", "--no-skills", "--no-prompt-templates", "-e", EXTENSION, *args]
    if not interactive:
        done = subprocess.run(command, cwd=work, env=env, input=b"", capture_output=True, timeout=60)
        assert done.returncode == 0, done.stderr.decode(errors="replace")
        return recorded(report)
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
    child = subprocess.Popen(command, cwd=work, env=env, stdin=slave, stdout=slave, stderr=slave)
    os.close(slave)
    output = bytearray()
    try:
        deadline = time.monotonic() + 30
        # session_start has run once the report exists; the TUI keeps drawing, so read the PTY meanwhile.
        while not report.exists():
            assert time.monotonic() < deadline, "no session_start: " + output.decode(errors="replace")
            if select.select([master], [], [], 0.05)[0]:
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    chunk = b""
                assert chunk or child.poll() is None, "CLI ended: " + output.decode(errors="replace")
                output.extend(chunk)
        time.sleep(0.3)
        os.write(master, b"\x04")
        deadline = time.monotonic() + 20
        while child.poll() is None:
            assert time.monotonic() < deadline, "CLI did not exit: " + output.decode(errors="replace")
            if select.select([master], [], [], 0.05)[0]:
                try:
                    output.extend(os.read(master, 65536))
                except OSError as error:
                    if error.errno != errno.EIO:
                        raise
                    break
        child.wait(timeout=20)
    finally:
        if child.poll() is None:
            child.kill()
            child.wait()
        os.close(master)
    return recorded(report)


with tempfile.TemporaryDirectory(prefix="extension-run-mode-") as directory:
    root = Path(directory)
    result = {
        "tui": run(root, "tui", [], True),
        "print": run(root, "print", ["--print"], False),
        "json": run(root, "json", ["--mode", "json"], False),
        "rpc": run(root, "rpc", ["--mode", "rpc"], False),
    }
    print(json.dumps(result, sort_keys=True))

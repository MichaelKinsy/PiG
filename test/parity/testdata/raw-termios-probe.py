#!/usr/bin/env python3
"""Probe the terminal mode a program puts its terminal in while it runs, and hands back when it ends.

usage: raw-termios-probe.py <pig|pi> [--bin PATH]

The program (the pig binary, or the pinned Pi build) is started on a new pseudo-terminal whose cooked mode the probe has set to one of two extremes,
every flag the table below names set ("ones") or cleared ("zeros"), so each flag the program changes shows in one of them and each flag it leaves
alone shows in neither. Once the program has drawn its screen the probe reads the terminal's mode and prints the flags that differ from the cooked
mode (+NAME set, -NAME cleared) and the control characters VMIN and VTIME; it then ends the program with Ctrl+D and prints the flags that
differ after the program has gone, which must be none.

Pi's mode is libuv's UV_TTY_MODE_RAW (Node's setRawMode(true), deps/uv/src/unix/tty.c in the libuv Node v24 carries): the same flags, byte for byte, in
the pseudo-terminal of the lab's Linux host. The Go test tui/terminal_raw_output_unix_test.go compares tui.MakeRaw with the fixture this probe
recorded from Pi (tui/testdata/pi-raw-termios.json), and the parity scenario startup/25-raw-mode-termios compares both binaries live."""
import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

ROOT = Path(__file__).resolve().parents[3]
FLAGS = {
    "iflag": ["IGNBRK", "BRKINT", "IGNPAR", "PARMRK", "INPCK", "ISTRIP", "INLCR", "IGNCR", "ICRNL", "IXON", "IXOFF", "IXANY", "IMAXBEL"],
    "oflag": ["OPOST", "ONLCR", "OCRNL", "ONOCR", "ONLRET"],
    "cflag": ["PARENB", "PARODD", "CSTOPB", "CREAD", "HUPCL", "CLOCAL"],
    "lflag": ["ISIG", "ICANON", "ECHO", "ECHOE", "ECHOK", "ECHONL", "NOFLSH", "TOSTOP", "IEXTEN", "ECHOCTL", "ECHOKE"],
}
WORDS = ["iflag", "oflag", "cflag", "lflag"]
READY = b"can explain"


def cc_value(value):
    return value if isinstance(value, int) else value[0]


def variant(cooked, ones):
    attrs = list(cooked)
    for index, word in enumerate(WORDS):
        for name in FLAGS[word]:
            bit = getattr(termios, name)
            attrs[index] = attrs[index] | bit if ones else attrs[index] & ~bit
    attrs[6] = list(cooked[6])
    # Different from what raw mode sets (VMIN 1, VTIME 0), so that both show.
    attrs[6][termios.VMIN] = 3
    attrs[6][termios.VTIME] = 7
    return attrs


def delta(before, after):
    flags = []
    for index, word in enumerate(WORDS):
        for name in FLAGS[word]:
            bit = getattr(termios, name)
            if (before[index] & bit) != (after[index] & bit):
                flags.append(("+" if after[index] & bit else "-") + name)
    return flags


def run(command, env, cwd, ones):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 35, 100, 0, 0))
    termios.tcsetattr(slave, termios.TCSANOW, variant(termios.tcgetattr(slave), ones))
    # The terminal's own answer is the cooked mode the program starts in: the kernel keeps some flags of a pseudo-terminal fixed.
    cooked = termios.tcgetattr(slave)
    process = subprocess.Popen(command, cwd=cwd, env=env, stdin=slave, stdout=slave, stderr=slave, start_new_session=True,
                               preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
    seen = b""
    deadline = time.monotonic() + 60
    try:
        while READY not in seen:
            if process.poll() is not None:
                raise RuntimeError(f"the program ended before it drew its screen: {seen[-300:]!r}")
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError(f"the program did not draw its screen: {seen[-300:]!r}")
            if select.select([master], [], [], min(remaining, 1))[0]:
                seen += os.read(master, 65536)
        running = termios.tcgetattr(slave)
        os.write(master, b"\x04")
        while process.poll() is None:
            if select.select([master], [], [], 1)[0]:
                try:
                    os.read(master, 65536)
                except OSError:
                    break
        process.wait(timeout=30)
        after = termios.tcgetattr(slave)
    finally:
        if process.poll() is None:
            os.killpg(process.pid, signal.SIGKILL)
        os.close(slave)
        os.close(master)
    return {
        "running": delta(cooked, running),
        "vmin": cc_value(running[6][termios.VMIN]),
        "vtime": cc_value(running[6][termios.VTIME]),
        "after_exit": delta(cooked, after),
        "after_exit_vmin": cc_value(after[6][termios.VMIN]) == cc_value(cooked[6][termios.VMIN]),
        "after_exit_vtime": cc_value(after[6][termios.VTIME]) == cc_value(cooked[6][termios.VTIME]),
    }


def main():
    kind = sys.argv[1]
    binary = None
    if "--bin" in sys.argv:
        binary = sys.argv[sys.argv.index("--bin") + 1]
    with tempfile.TemporaryDirectory(prefix="raw-termios-") as temporary:
        home = Path(temporary)
        agent = home / "agent"
        work = home / "work"
        agent.mkdir()
        work.mkdir()
        (agent / "settings.json").write_text(json.dumps({"theme": "dark", "tuiMode": "regular", "enableInstallTelemetry": False,
                                                          "lastChangelogVersion": "99.0.0", "compaction": {"enabled": False}, "retry": {"enabled": False}}))
        env = {"PATH": os.environ["PATH"], "HOME": str(home), "LANG": "C.UTF-8", "TERM": "xterm-256color", "COLORTERM": "truecolor",
               "PI_CODING_AGENT_DIR": str(agent), "PIG_CODING_AGENT_DIR": str(agent), "PIG_HOME": str(home / ".pig"),
               "PI_OFFLINE": "1", "PIG_OFFLINE": "1", "PI_TELEMETRY": "0", "PI_SKIP_VERSION_CHECK": "1"}
        if kind == "pig":
            binary = binary or os.environ.get("PIG_PARITY_PIG_BIN") or os.environ.get("PIG_BIN")
            env["PIG_TEST_FAUX"] = "1"
            command = [binary, "--no-extensions", "--no-session", "--model", "test-faux/faux-1"]
        else:
            binary = binary or str(ROOT / "extensions/sdk-ts/node_modules/.bin/pi")
            command = [binary, "--no-session", "--no-extensions", "-e", str(ROOT / "test/parity/testdata/test-faux-provider.ts"), "--model", "test-faux/faux-1"]
        result = {}
        for ones in (True, False):
            result["cooked_all_flags_" + ("set" if ones else "cleared")] = run(command, env, work, ones)
    print(json.dumps(result, sort_keys=True, indent=1))


main()

#!/usr/bin/env python3
"""Probe real SIGINT/SIGCONT delivery with acknowledgement barriers, not sleeps or reruns."""
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import sys
import tempfile
import threading

kind = sys.argv[1]
root = Path(__file__).resolve().parents[3]
with tempfile.TemporaryDirectory(prefix="parity-suspend-order-") as temporary:
    if kind == "pig":
        executable = str(Path(temporary) / "signal-order.test")
        subprocess.run(["go", "test", "-c", "-o", executable, "./internal/codingagent"], cwd=root, check=True)
        command = [executable, "-test.run=^TestSuspendSignalOrderChild$"]
    else:
        command = ["node", str(root / "test/parity/testdata/suspend-signal-order-pi.mjs")]
    for order in ["interrupt-before-continue", "continue-before-interrupt"]:
        env = dict(os.environ, PIG_SUSPEND_SIGNAL_CHILD="1")
        process = subprocess.Popen(command, cwd=root, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, start_new_session=True)
        lines = queue.Queue()
        trace = []
        def read_output():
            for line in process.stdout:
                lines.put(line.rstrip("\n"))
            lines.put(None)
        reader = threading.Thread(target=read_output)
        reader.start()
        def until(marker):
            while True:
                line = lines.get(timeout=30)
                if line is None:
                    raise RuntimeError(f"child exited before {marker}: {trace}")
                trace.append(line)
                if line == marker:
                    return
        try:
            until("armed")
            if order == "interrupt-before-continue":
                process.send_signal(signal.SIGINT)
                until("ignored")
            process.send_signal(signal.SIGCONT)
            until("render:true")
            if order == "continue-before-interrupt":
                process.send_signal(signal.SIGINT)
            else:
                process.stdin.write("finish\n")
                process.stdin.flush()
            code = process.wait(timeout=30)
            reader.join()
            while not lines.empty():
                line = lines.get_nowait()
                if line is not None:
                    trace.append(line)
            # A Unix signal termination and exit(128+signal) have the same shell-visible status. D51 owns Pig's terminal restoration before exit130.
            exit_code = code if code >= 0 else 128-code
            expected = ["stopped", "armed"]
            if order == "interrupt-before-continue":
                expected.append("ignored")
            expected.extend(["resumed", "render:true"])
            assert trace == expected, (order, trace)
            assert exit_code == (0 if order == "interrupt-before-continue" else 130), (order, exit_code)
            print("SUSPEND_SIGNAL " + json.dumps([order, trace, exit_code], separators=(",", ":")))
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()
            reader.join()
            process.stdin.close()
            process.stdout.close()

#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Pi-Bolt's bench/idle.py, unmodified, with one adapter-level change for every arm: after the TUI first shows the model name, wait for the
end of its startup before the first prompt.

  idle_settled.py BENCH_DIR [idle.py arguments]

idle.py sends its first prompt the moment "fake-model" is on screen. Pi (interactive-mode.ts handleStartupSubmit) keeps an Enter typed before
startup finishes in the editor and shows "Startup is still in progress"; PiG ports that window, so a prompt sent at the first paint can be
kept and idle.py ends with "no answer" (Pi 1.2.0 1 of 4 trials, PiG 4 of 4, Linux lab host 2026-10-13). The wait ends at the terminal title the TUI
sets when startup has bound the session (startup_ready.py), the same rule for every arm; it does not touch the measured idle window, which
starts after the first answer."""
import runpy
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from startup_ready import wait_for_startup  # noqa: E402

bench = Path(sys.argv[1]).resolve()
sys.argv = [str(bench / "idle.py"), *sys.argv[2:]]
sys.path.insert(0, str(bench))
import harness  # noqa: E402

_wait_for = harness.Tty.wait_for


def wait_for(self, needle, start, deadline, *a, **k):
    ok = _wait_for(self, needle, start, deadline, *a, **k)
    if ok and needle == "fake-model" and start == 0:
        return wait_for_startup(self, deadline)
    return ok


harness.Tty.wait_for = wait_for
runpy.run_path(str(bench / "idle.py"), run_name="__main__")

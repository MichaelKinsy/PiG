#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Pi-Bolt's bench/e2e_screen.py, unmodified, with one adapter-level change for every arm: the scenario prompt is typed after the end of the
TUI's startup, not the moment the model name shows.

  screen_ready.py BOLT_CHECKOUT/bench [e2e_screen.py arguments]

e2e_screen.py polls the tmux pane every 0.2 s and types its prompt once "fake-model" is on it, which can land in Pi's startup submit window
(see startup_ready.py). Here the poll reports the model name only once the pane's title changed from the one tmux gave it: the title the TUI
sets when startup has bound the session. A pane whose title never changes falls back after startup_ready.MARKER_WAIT_S to the first poll that
shows the model name. BOLT_CHECKOUT must be a whole Pi-Bolt checkout: the scenario streams its scripts/lib/training.md. tmux passes the
build only PI_* variables, so PiG runs through bench/compare/adapters/pig (arms.py specs)."""
import importlib.util
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from startup_ready import MARKER_WAIT_S  # noqa: E402

bench = Path(sys.argv[1]).resolve()
sys.argv = [str(bench / "e2e_screen.py"), *sys.argv[2:]]
sys.path.insert(0, str(bench))
spec = importlib.util.spec_from_file_location("e2e_screen", bench / "e2e_screen.py")
screen = importlib.util.module_from_spec(spec)
spec.loader.exec_module(screen)

_tmux = screen.tmux
state = {"title": None, "since": 0.0}


def title():
    return _tmux("display-message", "-p", "#{pane_title}").stdout.strip()


def tmux(*args):
    result = _tmux(*args)
    if args and args[0] == "new-session":
        state["title"], state["since"] = title(), time.time()
    elif args == ("capture-pane", "-p") and state["title"] is not None and "fake-model" in result.stdout:
        if title() != state["title"] or time.time() - state["since"] >= MARKER_WAIT_S:
            state["title"] = None  # startup ended: the run's later polls are not touched
        else:
            result.stdout = result.stdout.replace("fake-model", "")
    return result


screen.tmux = tmux
screen.main()

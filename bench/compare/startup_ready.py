# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""The end of a Pi-family TUI's startup, as a harness sees it.

Pi (interactive-mode.ts init()) paints the editor and footer, model name included, before startup completes: until managed-tool setup ends
it keeps an Enter in the editor and shows "Startup is still in progress" (handleStartupSubmit), and PiG ports that window. A harness that
types the moment the model name shows can land in it. Startup then binds the session (rebindCurrentSession), whose last step sets the
terminal title (updateTerminalTitle: OSC 0, "π - <cwd>" or "PiG - <cwd>"), after the submit handler is installed. That title is the
marker: once it is written, input is handled as at any later prompt. Arms that never set a title fall back to a quiet settle."""
import re
import time

# OSC 0 or OSC 2: set the window (and icon) title.
TITLE = re.compile(rb"\x1b\][02];")
# How long to wait for the marker before an arm counts as one that never writes it.
MARKER_WAIT_S = 10.0
# The settle for such arms: output quiet this long.
FALLBACK_QUIET_S = 2.0


def wait_for_startup(tty, deadline):
    """Pumps a Pi-Bolt harness Tty until the TUI wrote its terminal title (see the module docstring). Returns whether startup ended:
    the title, or, for an arm with no title within MARKER_WAIT_S, a settle of FALLBACK_QUIET_S."""
    marker_deadline = min(deadline, time.perf_counter() + MARKER_WAIT_S)
    while not TITLE.search(tty.buf):
        if time.perf_counter() >= marker_deadline:
            return tty.settle(FALLBACK_QUIET_S, deadline)
        tty.pump(0.01)
    return True

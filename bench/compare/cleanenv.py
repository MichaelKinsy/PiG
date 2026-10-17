# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""The environment every arm runs in: nothing from the lane shell that changes what a build does.

A lane shell exports GOMAXPROCS (24), GOMEMLIMIT, GOFLAGS, GOTRACEBACK and lane/agent variables. A Go arm pinned to one core with GOMAXPROCS=24 pays
scheduler and GC overhead for 24 Ps on one core (complexity-audit measured 15-25% of PiG's 6k-char streaming CPU lost to rpi from it), and Pi-Bolt's harness
strips only PI_*, BUN_* and NODE_*. clean_env() drops every Go runtime/toolchain variable and every PIG_*, PI_*, NODE_*, BUN_*, LANE* and agent-credential
variable, so each arm sizes itself from the cores it is pinned to (Go reads the affinity mask) exactly as it would on a user's machine; the arm launchers
(bench/compare/adapters) set what an arm needs (HOME, PIG_CODING_AGENT_DIR, ...)."""
import os
from pathlib import Path

NODE_BIN = Path(os.environ.get("BENCH_NODE_BIN", Path.home() / ".local/share/mise/installs/node/24.19.0/bin"))  # the pinned Node of arms.lock (24.19.0)

DROP_PREFIXES = ("GO", "PIG_", "PI_", "NODE_", "BUN_", "LANE", "COPILOT", "GITHUB", "OPENAI", "ANTHROPIC", "GEMINI", "CODEX", "CLAUDE", "MISE_", "RUST", "CARGO", "TINYGO")

def clean_env(env=None):
    """A copy of env (default os.environ) without the variables above."""
    out = {k: v for k, v in (os.environ if env is None else env).items() if not k.startswith(DROP_PREFIXES)}
    if "PATH" in out:
        # The lane shims (node, go, make, ...) wrap the real tools: an arm that runs `node` must run the pinned Node itself, not the lane's wrapper.
        entries = [e for e in out["PATH"].split(":") if not e.endswith("/lane-shims")]
        out["PATH"] = ":".join(([str(NODE_BIN)] if NODE_BIN.is_dir() else []) + entries)
    return out


def apply():
    """Cleans os.environ in place (the harness's children inherit it) and returns the dropped names, for the run record."""
    cleaned = clean_env()
    dropped = sorted(k for k in os.environ if k not in cleaned)
    for k in dropped:
        del os.environ[k]
    os.environ["PATH"] = cleaned.get("PATH", os.environ.get("PATH", ""))
    return dropped

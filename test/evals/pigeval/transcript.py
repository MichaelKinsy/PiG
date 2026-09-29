# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Per-run metrics from a harness's machine-readable output.

Formats: Pi-family `--mode json` (pig, pi, omp), Claude Code `stream-json`,
and Codex `exec --json`. Other harnesses report pass or fail and wall time only.
Metrics follow the no-poll comparison in oh-my-pi: turns, tool calls, polling
calls, context tokens, and cost.
"""
import json
import re

POLLING = re.compile(r"^\s*(sleep|ps|pgrep|top|watch|wait|jobs)\b|^\s*tail\s+-f\b")


def is_polling(command):
    """A polling call waits on or inspects a running job instead of doing work."""
    return bool(command) and all(POLLING.match(part) for part in re.split(r"&&|;|\|\|", command) if part.strip())


def events(stdout):
    for line in stdout.splitlines():
        if line.startswith("{"):
            try:
                yield json.loads(line)
            except ValueError:
                continue


def metrics(stdout):
    """Return a metrics dict, or None when the output has no known format."""
    m = {"format": None, "turns": 0, "tool_calls": 0, "polling_calls": 0, "context_tokens": 0, "output_tokens": 0, "cost_usd": None}

    def tool(command):
        m["tool_calls"] += 1
        if is_polling(command):
            m["polling_calls"] += 1

    for e in events(stdout):
        kind = e.get("type")
        message = e.get("message") if isinstance(e.get("message"), dict) else {}
        if kind == "message_end" and message.get("role") == "assistant":
            m["format"] = "pi"
            m["turns"] += 1
            usage = message.get("usage") or {}
            m["context_tokens"] += usage.get("input", 0) + usage.get("cacheRead", 0) + usage.get("cacheWrite", 0)
            m["output_tokens"] += usage.get("output", 0)
            cost = (usage.get("cost") or {}).get("total")
            if cost is not None:
                m["cost_usd"] = (m["cost_usd"] or 0) + cost
            for part in message.get("content") or []:
                if part.get("type") == "toolCall":
                    tool((part.get("arguments") or {}).get("command", ""))
        elif kind == "assistant" and message:
            m["format"] = "claude"
            m["turns"] += 1
            for part in message.get("content") or []:
                if part.get("type") == "tool_use":
                    tool((part.get("input") or {}).get("command", ""))
        elif kind == "result" and "total_cost_usd" in e:
            m["format"] = "claude"
            usage = e.get("usage") or {}
            m["context_tokens"] = usage.get("input_tokens", 0) + usage.get("cache_creation_input_tokens", 0) + usage.get("cache_read_input_tokens", 0)
            m["output_tokens"] = usage.get("output_tokens", 0)
            m["cost_usd"] = e.get("total_cost_usd")
            m["turns"] = e.get("num_turns") or m["turns"]
        elif kind == "item.completed" and (e.get("item") or {}).get("type") == "command_execution":
            m["format"] = "codex"
            tool(e["item"].get("command", ""))
        elif kind == "turn.completed":
            m["format"] = "codex"
            usage = e.get("usage") or {}
            m["turns"] += 1
            m["context_tokens"] += usage.get("input_tokens", 0) + usage.get("cached_input_tokens", 0)
            m["output_tokens"] += usage.get("output_tokens", 0)
    return m if m["format"] else None

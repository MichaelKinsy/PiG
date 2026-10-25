#!/usr/bin/env python3
"""The Python standalone (isolated cell) of the Nix acceptance check."""
import pig_sdk

ext = pig_sdk.Extension("python-isolated")
ext.on_event("session_start", lambda ctx, data: ctx.set_status("python-isolated", "python-isolated: ready"))


def probe(ctx, _args):
    ctx.append_entry("nix-probe", {"by": "python-isolated"})
    n = sum(1 for entry in ctx.get_entries() if entry.get("customType") == "nix-probe" and entry.get("data", {}).get("by") == "python-isolated")
    ctx.notify(f"python-isolated: entries={n}", "info")


ext.command("python-isolated-probe", "Append an entry and count it", probe)
ext.run()

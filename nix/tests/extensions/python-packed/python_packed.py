"""The Python factory (packed cell) of the Nix acceptance check."""
import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("python-packed")
    ext.on_event("session_start", lambda ctx, data: ctx.set_status("python-packed", "python-packed: ready"))

    def probe(ctx, _args):
        ctx.append_entry("nix-probe", {"by": "python-packed"})
        n = sum(1 for entry in ctx.get_entries() if entry.get("customType") == "nix-probe" and entry.get("data", {}).get("by") == "python-packed")
        ctx.notify(f"python-packed: entries={n}", "info")

    ext.command("python-packed-probe", "Append an entry and count it", probe)
    return ext

"""The Python SDK version of the robust-notes fixture (../node/index.mjs)."""
from __future__ import annotations

import pig_sdk
import robust_words


class Picker:
    def __init__(self) -> None:
        self.items = ["alpha", "beta", "gamma"]
        self.selected = 0

    def render(self, width: int) -> list[str]:
        lines = [f"robust pick ({width})"]
        for index, item in enumerate(self.items):
            lines.append(f"{'>' if index == self.selected else ' '} {item}")
        return lines

    def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
        if data == "j":
            self.selected = (self.selected + 1) % len(self.items)
        elif data == "s":
            return pig_sdk.RemoteComponentResult(done=True, value=self.items[self.selected])
        elif data == "q":
            return pig_sdk.RemoteComponentResult(done=True)
        return pig_sdk.RemoteComponentResult()


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("python")
    tool_runs = 0
    ext.flag("robust-prefix", "Prefix for robust_words output", "string", "rw")

    def words(ctx, params):
        found = robust_words.split(params["line"])
        return f"{ctx.get_flag('robust-prefix')}: {'|'.join(found)}"

    ext.tool(
        "robust_words",
        "Split a command line into words.",
        {"type": "object", "properties": {"line": {"type": "string"}}, "required": ["line"]},
        words,
    )

    def session_start(ctx, _data):
        ctx.set_status("robust", "robust: ready")

    def tool_result(ctx, data):
        nonlocal tool_runs
        if data.get("toolName") != "robust_words":
            return None
        tool_runs += 1
        ctx.set_status("robust", f"robust: tools={tool_runs}")
        return None

    ext.on_event("session_start", session_start)
    ext.on_event("tool_result", tool_result)

    def show_settings(ctx, _args):
        settings = ctx.get_settings()
        greeting = (settings.get("robustNotes") or {}).get("greeting", "none")
        ctx.notify(f"robust settings: greeting={greeting} prefix={ctx.get_flag('robust-prefix')}", "info")

    def save_note(ctx, args):
        ctx.append_entry("robust-note", {"text": args})
        notes = [entry for entry in ctx.get_entries() if entry.get("type") == "custom" and entry.get("customType") == "robust-note"]
        ctx.notify(f"robust note {len(notes)}: {args}", "info")

    def pick(ctx, _args):
        choice = ctx.custom(Picker(), {"overlay": True})
        ctx.notify(f"robust picked: {choice if choice is not None else 'nothing'}", "info")

    ext.command("robust-settings", "Show the robust-notes settings", show_settings)
    ext.command("robust-note", "Save a note", save_note)
    ext.command("robust-pick", "Pick an item in an overlay", pick)
    return ext

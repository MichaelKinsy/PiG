"""The Python SDK version of the writer-start fixture (../node/index.mjs)."""
from __future__ import annotations

import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("python")

    def session_start(ctx, _data):
        ctx.append_entry("writer-start", {"text": "from-start"})
        ctx.set_session_name("start-name")
        ctx.send_message("writer-start", "start-message", False, False)
        ctx.set_status("writer", "writer: ready")

    def after(ctx, _args):
        ctx.append_entry("writer-after", {"text": "from-command"})
        ctx.set_session_name("after-name")
        ctx.send_message("writer-after", "after-message", False, False)

    ext.on_event("session_start", session_start)
    ext.command("writer-after", "Append, name and send from a command", after)
    return ext

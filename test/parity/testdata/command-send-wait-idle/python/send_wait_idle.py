"""The Python SDK version of rpc/testdata/command-send-wait-idle.mjs: a command that sends a user message and then waits for the Session to be idle."""
from __future__ import annotations

import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("python")

    def ask(ctx, _args):
        ctx.send_user_message("Say hello")
        ctx.wait_for_idle()

    ext.command("ask", "Send a user message and wait for idle", ask)
    return ext

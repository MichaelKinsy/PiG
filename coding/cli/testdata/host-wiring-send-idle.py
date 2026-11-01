"""host-wiring-send-idle.py sends a message that starts a run from its session_start handler, blocks for a while without awaiting anything, and records the ctx.is_idle() it reads then."""
import json
import os
import time

import pig_sdk


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("host-wiring-send-idle")

    def on_start(ctx, data):
        ctx.send_message("probe", "What is 20+22?", True, True)
        time.sleep(1.5)
        idle = ctx.is_idle()
        # newline="" keeps the "\n" record separator on Windows, where text mode writes "\r\n".
        with open(os.environ["WIRING_REPORT"], "a", newline="") as report:
            report.write(json.dumps({"event": "idle", "sync": idle}, separators=(",", ":")) + "\n")

    ext.on_event("session_start", on_start)
    return ext

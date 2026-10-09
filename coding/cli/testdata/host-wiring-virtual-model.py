"""host-wiring-virtual-model.py registers a virtual model while its factory runs
and reports from session_start whether the Session's model registry has it.

Pi queues the registration until the runner binds (loader.ts:480-490) and
flushes it in bindCore (runner.ts:497-513), which _buildRuntime runs before
bindExtensions emits session_start (agent-session.ts:3562, 3194)."""
import json
import os

import pig_sdk


def _log(record):
    # newline="" keeps the "\n" record separator on Windows, where text mode writes "\r\n".
    with open(os.environ["WIRING_REPORT"], "a", newline="") as report:
        report.write(json.dumps(record, separators=(",", ":")) + "\n")


def _route(ctx, request):
    return {"model": {"provider": "probe", "id": "probe-model"}, "thinkingLevel": "off"}


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("host-wiring-virtual-model")
    ext.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="auto", name="Auto", route=_route))

    def on_start(ctx, data):
        found = ctx.model_registry.find("router", "auto")
        _log({"event": "virtual_model", "value": None if found is None else found.get("id")})

    ext.on_event("session_start", on_start)
    return ext

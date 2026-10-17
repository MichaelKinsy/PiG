"""host-wiring-session-actions.py is the Python twin of host-wiring-session-actions.mjs: from session_start it calls the Session actions Pi binds in every mode
(agent-session.ts _bindExtensionCore) and records each answer, then records the model and thinking-level entries the Session wrote. It registers the only
provider with credentials, so Pi and Pig both start on probe/probe-model, and switches to probe/probe-model-2. A second provider has no credentials, so
setModel to its model answers false and changes nothing."""
import json
import os
import threading

import pig_sdk


def _log(record):
    # newline="" keeps the "\n" record separator on Windows, where text mode writes "\r\n".
    with open(os.environ["WIRING_REPORT"], "a", newline="") as report:
        report.write(json.dumps(record, separators=(",", ":")) + "\n")


def _models(ids):
    return [{"id": i, "name": i, "reasoning": True, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096} for i in ids]


def _identity(model):
    provider = model.get("provider")
    if isinstance(provider, dict):
        provider = provider.get("id")
    return "%s/%s" % (provider, model.get("id"))


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("host-wiring-session-actions")
    ext.register_provider("probe", {"baseUrl": "http://127.0.0.1:9/v1", "apiKey": "probe-key", "api": "openai-completions", "models": _models(["probe-model", "probe-model-2"])})
    ext.register_provider("probe-noauth", {"baseUrl": "http://127.0.0.1:9/v1", "api": "openai-completions", "models": _models(["noauth-model"])})
    ext.on_event("model_select", lambda ctx, data: _log({"event": "model_select", "model": _identity(data["model"])}))
    ext.on_event("thinking_level_select", lambda ctx, data: _log({"event": "thinking_level_select", "level": data["level"]}))

    def on_start(ctx, data):
        _log({"event": "trusted", "value": ctx.is_project_trusted()})
        ok, error = ctx.set_model("probe-noauth/noauth-model")
        _log({"event": "setModelNoAuth", "value": ok})
        ok, error = ctx.set_model("probe/probe-model-2")
        _log({"event": "setModel", "value": ok})
        done = threading.Event()
        outcome = {}

        def complete(result):
            outcome["record"] = {"event": "compact", "value": "complete"}
            done.set()

        def failed(exc):
            outcome["record"] = {"event": "compact", "error": str(exc)}
            done.set()

        ctx.compact(on_complete=complete, on_error=failed)
        done.wait(60)
        _log(outcome.get("record", {"event": "compact", "error": "no outcome"}))
        ctx.set_thinking_level("high")

    ext.on_event("session_start", on_start)

    def on_shutdown(ctx, data):
        entries = []
        for entry in ctx.session_manager.get_entries():
            if entry.get("type") == "model_change":
                entries.append("model_change:%s/%s" % (entry.get("provider"), entry.get("modelId")))
            elif entry.get("type") == "thinking_level_change":
                entries.append("thinking_level_change:%s" % entry.get("thinkingLevel"))
        _log({"event": "entries", "value": entries})

    ext.on_event("session_shutdown", on_shutdown)
    return ext

"""model-after-new.py is the Python SDK realization of model-after-new.mjs: it registers an extension provider whose models carry metadata no default
produces (a window, an output limit, costs, reasoning and an image input) over the server PIG_TEST_MODEL_AFTER_NEW_URL names, and records the model each
model_select event carries and the thinking level the extension reads then, as lines of PIG_TEST_REPLACE_LOG. Every Session start runs the factory again
in a fresh process."""
import json
import os

import pig_sdk

COST = {"input": 3.25, "output": 16.5, "cacheRead": 0.325, "cacheWrite": 4.0625}


def _log(record):
    # newline="" keeps the "\n" record separator on Windows, where text mode writes "\r\n".
    with open(os.environ["PIG_TEST_REPLACE_LOG"], "a", newline="") as report:
        report.write(json.dumps(record, separators=(",", ":")) + "\n")


def _describe(model):
    # A provider reaches the extension as its id or as an object carrying it.
    provider = model.get("provider")
    if isinstance(provider, dict):
        provider = provider.get("id")
    return {"id": "%s/%s" % (provider, model.get("id")), "name": model.get("name"), "contextWindow": model.get("contextWindow"),
            "maxTokens": model.get("maxTokens"), "reasoning": model.get("reasoning"), "input": model.get("input"), "cost": model.get("cost")}


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("model-after-new")
    ext.register_provider("metaprov", {
        "api": "openai-completions", "baseUrl": os.environ.get("PIG_TEST_MODEL_AFTER_NEW_URL", ""), "apiKey": "probe-key",
        "models": [
            {"id": "model-a", "name": "Model A", "reasoning": True, "input": ["text", "image"], "cost": COST, "contextWindow": 777000, "maxTokens": 55555},
            {"id": "model-b", "name": "Model B", "reasoning": True, "input": ["text"], "cost": COST, "contextWindow": 555000, "maxTokens": 44444},
        ],
    })
    ext.on_event("session_start", lambda ctx, data: _log({"event": "session_start", "reason": data.get("reason")}))
    ext.on_event("model_select", lambda ctx, data: _log({"event": "model_select", "source": data.get("source"), "model": _describe(data["model"]), "thinking": ctx.get_thinking_level()}))
    return ext

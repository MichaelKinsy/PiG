"""The Python ModelRegistry's provider-state reads and ``find``, against Pi's model-registry.ts.

Pi's ``find`` answers undefined only when no model matches (model-registry.ts:72), so a host failure is an error here, never ``None``.
``hasConfiguredAuth`` and ``isUsingOAuth`` read ``model.provider`` in the configured and OAuth sets (model-registry.ts:85-87, 206-208;
model-runtime.ts:543-553): a model with no provider has neither. ``getAvailable`` is the models of the providers with configured auth,
narrowed to a provider's ``availableModelIds`` (model-runtime.ts getAvailableSnapshot).
"""

from __future__ import annotations

from typing import Any

import pytest
from test_extension_api_099 import _command_ext, _drive, _host

STATE = {
    "models": [
        {"id": "a", "provider": "listed"}, {"id": "b", "provider": "listed"},
        {"id": "c", "provider": "open"}, {"id": "d", "provider": "off"}, {"id": "e", "provider": "none"},
    ],
    "providers": {
        "listed": {"configured": True, "usingOAuth": True, "availableModelIds": ["a"]},
        "open": {"configured": True, "usingOAuth": False},
        "off": {"configured": False, "usingOAuth": False},
        "none": {"configured": True, "usingOAuth": True, "availableModelIds": []},
    },
}


def _run(handler: Any, answers: dict[str, Any]) -> list[dict[str, Any]]:
    ext, ready = _command_ext("py-registry-edges", handler)
    host = _host(ext, ready)
    try:
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"}, answers)
        assert response["response"]["error"] is None, response
        return calls
    finally:
        host.close()


def test_find_is_absent_only_when_no_model_matches_and_a_host_failure_raises() -> None:
    seen: dict[str, Any] = {}

    def handler(ctx: Any) -> None:
        registry = ctx.model_registry
        seen["found"] = registry.find("listed", "a")
        seen["absent"] = registry.find("listed", "zzz")
        try:
            registry.find("listed", "a")
        except Exception as error:  # noqa: BLE001 - the test inspects what is raised
            seen["failure"] = str(error)

    ext, ready = _command_ext("py-find", handler)
    host = _host(ext, ready)
    try:
        answers = iter([{"id": "a", "provider": "listed"}, None, RuntimeError("boom")])
        host.request("r1", {"method": "command", "tool": "go"})
        while True:
            env = host.read()
            if env["type"] == "response":
                assert env["response"]["error"] is None, env
                break
            assert env["call"]["method"] == "getModel"
            answer = next(answers)
            if isinstance(answer, Exception):
                host.answer(env, error={"message": str(answer)})
            else:
                host.answer(env, answer)
    finally:
        host.close()
    assert seen["found"] == {"id": "a", "provider": "listed"}
    assert seen["absent"] is None
    assert "boom" in seen["failure"], seen


def test_provider_state_reads_follow_the_models_provider() -> None:
    seen: dict[str, Any] = {}

    def handler(ctx: Any) -> None:
        registry = ctx.model_registry
        models = [*registry.get_all(), {}]
        seen["available"] = [model["id"] for model in registry.get_available()]
        seen["auth"] = [registry.has_configured_auth(model) for model in models]
        seen["oauth"] = [registry.is_using_oauth(model) for model in models]

    _run(handler, {"getModelRegistryState": STATE})
    assert seen["available"] == ["a", "c"], seen
    assert seen["auth"] == [True, True, True, False, True, False], seen
    assert seen["oauth"] == [True, True, False, False, True, False], seen

"""The typed model operations of Pi 0.99.2 in the Python SDK.

Every test runs an extension against a fake host socket and asserts the wire frames of the contract in
coding/extension/host/subprocess (getAvailableOfType, classify, provider_operation) and how the SDK decodes the host's answers.
Each docstring cites the upstream line it mirrors (.upstream/v0.99.2/packages/coding-agent/src/core/).
"""

from __future__ import annotations

import json
from typing import Any

import pytest
from test_extension_api_099 import _command_ext, _drive, _host, _write_frame

import pig_sdk

STATE: dict[str, Any] = {
    "models": [
        {"id": "gpt", "provider": "openai", "api": "openai-completions"},
        {"id": "opus", "provider": "anthropic", "api": "anthropic-messages"},
    ],
    "typedModels": [
        {"type": "image", "id": "flux", "provider": "openrouter", "api": "openrouter-images", "output": ["image"]},
        {"type": "classifier", "id": "jev-latest", "provider": "typesafe", "api": "typesafe-system-one", "contextWindow": 64000},
        {"type": "classifier", "id": "~typesafe/jev-latest", "provider": "openrouter", "api": "typesafe-system-one", "contextWindow": 64000},
    ],
    "providers": {},
    "registered": [],
}

JEV = {"type": "classifier", "id": "jev-latest", "provider": "typesafe", "api": "typesafe-system-one", "contextWindow": 64000}

APPROVAL = {
    "state": {"text": "Looks good"},
    "questions": {
        "tone": {"type": "choice", "instructions": "Which tone?", "criteria": {"warm": "Warm", "cold": "Cold"}},
        "approved": {"type": "bool", "instructions": "Does this express approval?", "criteria": {"true": "Approval", "false": "No approval"}},
    },
}


def _ids(models: list[dict[str, Any]]) -> list[str]:
    return [f"{m['provider']}/{m['id']}" for m in models]


def test_typed_reads_answer_from_the_registry_state() -> None:
    """model-registry.ts:145-161: chat models come from `models`, every other type from `typedModels`, in the host's order."""
    seen: dict[str, Any] = {}

    def handler(ctx: pig_sdk.Context) -> None:
        registry = ctx.model_registry
        seen["classifiers"] = registry.get_models_of_type("classifier")
        seen["chat"] = registry.get_models_of_type("chat")
        seen["images"] = registry.get_models_of_type("image", "openrouter")
        seen["none"] = registry.get_models_of_type("audio")
        seen["in_provider"] = registry.get_models_of_type("classifier", "typesafe")
        seen["found"] = registry.find_of_type("classifier", "typesafe", "jev-latest")
        seen["missing"] = registry.find_of_type("classifier", "typesafe", "missing")
        seen["via_get"] = registry.get_model_of_type("image", "openrouter", "flux")
        seen["chat_model"] = registry.get_model_of_type("chat", "openai", "gpt")

    ext, ready = _command_ext("py-typed-reads", handler)
    host = _host(ext, ready)
    try:
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"}, {"getModelRegistryState": STATE})
        assert response["response"]["error"] is None, response
        assert {c["call"]["method"] for c in calls} == {"getModelRegistryState"}, "a typed read must answer from the registry state"
    finally:
        host.close()
    assert _ids(seen["classifiers"]) == ["typesafe/jev-latest", "openrouter/~typesafe/jev-latest"]
    assert _ids(seen["chat"]) == ["openai/gpt", "anthropic/opus"]
    assert _ids(seen["images"]) == ["openrouter/flux"]
    assert seen["none"] == []
    assert _ids(seen["in_provider"]) == ["typesafe/jev-latest"]
    assert seen["found"]["contextWindow"] == 64000 and seen["found"]["type"] == "classifier"
    assert seen["missing"] is None
    assert seen["via_get"]["api"] == "openrouter-images"
    assert seen["chat_model"]["id"] == "gpt"


def test_get_available_of_type_calls_the_host() -> None:
    """model-registry.ts:135-143: the type and the provider are what the host filters on; its error is the caller's."""
    seen: dict[str, Any] = {}

    def handler(ctx: pig_sdk.Context) -> None:
        registry = ctx.model_registry
        seen["classifiers"] = registry.get_available_of_type("classifier", "typesafe")
        seen["images"] = registry.get_available_of_type("image")
        try:
            registry.get_available_of_type("audio")
        except Exception as error:  # noqa: BLE001
            seen["audio"] = str(error)

    ext, ready = _command_ext("py-available", handler)
    host = _host(ext, ready)
    try:
        host.request("r1", {"method": "command", "tool": "go"})
        calls = []
        while True:
            env = host.read()
            if env["type"] == "response":
                break
            if env["type"] == "call":
                calls.append(env["call"])
                kind = env["call"]["args"]["type"]
                if kind == "classifier":
                    host.answer(env, [{"type": "classifier", "id": "jev-latest", "provider": "typesafe"}])
                elif kind == "image":
                    host.answer(env, [])
                else:
                    host.answer(env, error={"message": "Unknown model type: audio"})
        assert [c["method"] for c in calls] == ["getAvailableOfType"] * 3
        assert calls[0]["args"] == {"type": "classifier", "provider": "typesafe"}
        assert calls[1]["args"] == {"type": "image"}
    finally:
        host.close()
    assert seen["classifiers"][0]["id"] == "jev-latest" and seen["images"] == []
    assert "Unknown model type: audio" in seen["audio"]


def test_classify_round_trips_ordered_questions_and_answers() -> None:
    """model-registry.ts:170-177, model-runtime.ts:800-815: the questions and the answers keep their order; classify never raises."""
    seen: dict[str, Any] = {}

    def handler(ctx: pig_sdk.Context) -> None:
        registry = ctx.model_registry
        seen["ok"] = registry.classify(JEV, APPROVAL, {"apiKey": "sk-explicit", "headers": {"X-Trace": None}, "maxRetries": 2, "temperature": 0.5})
        seen["failed"] = registry.classify(JEV, APPROVAL)

    ext, ready = _command_ext("py-classify", handler)
    host = _host(ext, ready)
    ok_result = '{"api":"typesafe-system-one","provider":"typesafe","model":"jev-latest","answers":{"approved":{"type":"bool","probability":0.8},"tone":{"type":"choice","choice":"warm","probabilities":{"warm":0.7,"cold":0.3},"confidence":0.4}},"usage":{"input":12,"output":3,"cacheRead":0,"cacheWrite":0,"totalTokens":15,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":7}'
    try:
        host.request("r1", {"method": "command", "tool": "go"})
        calls = []
        while True:
            env = host.read()
            if env["type"] == "response":
                break
            if env["type"] == "call":
                calls.append(env["call"])
                if env["call"]["args"].get("options", {}).get("apiKey") == "sk-explicit":
                    host.answer(env, json.loads(ok_result))
                else:
                    host.answer(env, error={"message": "host went away"})
        assert [c["method"] for c in calls] == ["classify", "classify"]
        assert calls[0]["args"]["model"] == JEV
        assert calls[0]["args"]["context"] == APPROVAL
        assert list(calls[0]["args"]["context"]["questions"]) == ["tone", "approved"], "the questions lost their order"
        assert calls[0]["args"]["options"] == {"apiKey": "sk-explicit", "headers": {"X-Trace": None}, "maxRetries": 2, "temperature": 0.5}
        assert "options" not in calls[1]["args"], "omitted options were sent"
    finally:
        host.close()
    assert seen["ok"]["stopReason"] == "stop" and seen["ok"]["usage"]["totalTokens"] == 15
    assert list(seen["ok"]["answers"]) == ["approved", "tone"], "the answers must keep the service's order"
    failed = seen["failed"]
    assert failed["stopReason"] == "error" and "host went away" in failed["errorMessage"]
    assert (failed["provider"], failed["model"], failed["api"]) == ("typesafe", "jev-latest", "typesafe-system-one")
    assert failed["timestamp"] > 0


def test_classify_reports_a_cancelled_request_as_an_aborted_result() -> None:
    """model-runtime.ts:800-815: an aborted classification is a result with stopReason aborted, not an exception; the request lifetime is the signal."""
    seen: dict[str, Any] = {}

    def handler(ctx: pig_sdk.Context) -> None:
        seen["result"] = ctx.model_registry.classify(JEV, APPROVAL)

    ext, ready = _command_ext("py-classify-cancel", handler)
    host = _host(ext, ready)
    try:
        host.request("r1", {"method": "command", "tool": "go"})
        call = host.read()
        assert call["call"]["method"] == "classify"
        _write_frame(host.conn, {"type": "cancel", "id": "r1", "cancel": {"request_id": "r1", "reason": "user"}})
        assert host.read()["type"] == "response"
    finally:
        host.close()
    result = seen["result"]
    assert result["stopReason"] == "aborted" and result["answers"] == {}
    assert (result["provider"], result["model"], result["api"]) == ("typesafe", "jev-latest", "typesafe-system-one")


def _route(ctx: pig_sdk.Context, request: dict[str, Any]) -> dict[str, Any]:
    return {"model": {"provider": "p", "id": "m"}, "thinkingLevel": "off"}


def test_registry_virtual_models_reach_the_host_like_the_context_ones() -> None:
    """model-registry.ts:161-168: registerVirtualModel and unregisterVirtualModel of the facade are the runtime's (loader.ts:480-495)."""
    seen: dict[str, Any] = {}

    def handler(ctx: pig_sdk.Context) -> None:
        registry = ctx.model_registry
        registry.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="late", name="Late", route=_route, context_window=1000))
        try:
            registry.register_virtual_model(pig_sdk.VirtualModel(provider="router", id="claimed", name="Claimed", route=_route))
        except Exception as error:  # noqa: BLE001
            seen["refused"] = str(error)
        registry.unregister_virtual_model("router", "late")

    ext, ready = _command_ext("py-registry-vm", handler)
    host = _host(ext, ready)
    try:
        host.request("r1", {"method": "command", "tool": "go"})
        calls = []
        while True:
            env = host.read()
            if env["type"] == "response":
                break
            if env["type"] == "call":
                calls.append(env["call"])
                if env["call"]["args"].get("id") == "claimed":
                    host.answer(env, error={"message": "virtual model router/claimed is the id of a physical model"})
                else:
                    host.answer(env, {})
        assert [(c["method"], c["args"]) for c in calls] == [
            ("registerVirtualModel", {"provider": "router", "id": "late", "name": "Late", "contextWindow": 1000}),
            ("registerVirtualModel", {"provider": "router", "id": "claimed", "name": "Claimed"}),
            ("unregisterVirtualModel", {"provider": "router", "id": "late"}),
        ]
    finally:
        host.close()
    assert "is the id of a physical model" in seen["refused"]


def test_provider_config_implementations_are_declared_and_run_in_the_extension() -> None:
    """types.ts:1896-1898: images and classifiers are implementations keyed by API. The callables stay in the extension; the register frame names the APIs."""
    seen: dict[str, Any] = {}

    def images(model: dict[str, Any], request: dict[str, Any], options: pig_sdk.ProviderOperationOptions) -> dict[str, Any]:
        seen["image"] = (model, request, options.values, options.signal.is_set())
        return {"api": "test-images", "provider": "ops", "model": model["id"], "stopReason": "stop", "timestamp": 1, "key": options.values.get("apiKey")}

    def classify(model: dict[str, Any], request: dict[str, Any], options: pig_sdk.ProviderOperationOptions) -> dict[str, Any]:
        if request["state"]["text"] == "fail":
            raise RuntimeError("classifier failed")
        seen["order"] = list(request["questions"])
        return {"api": "test-classifier", "provider": "ops", "model": model["id"], "answers": {"b": {"type": "bool", "probability": 0.5}, "a": {"type": "bool", "probability": 0.25}}, "stopReason": "stop", "timestamp": 2}

    ext = pig_sdk.Extension("py-ops")
    ext.register_provider("ops", {
        "baseUrl": "https://ops.test/v1", "apiKey": "key",
        "models": [
            {"id": "flux", "name": "Flux", "type": "image", "api": "test-images", "input": ["text"], "output": ["image"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}},
            {"id": "cls", "name": "Cls", "type": "classifier", "api": "test-classifier", "input": ["text"], "contextWindow": 1000, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}},
        ],
        "images": {"test-images": images},
        "classifiers": {"test-classifier": classify},
    })
    host = _host(ext)
    try:
        provider = host.register["providers"][0]
        assert provider["image_apis"] == ["test-images"] and provider["classifier_apis"] == ["test-classifier"]
        assert "images" not in provider["config"] and "classifiers" not in provider["config"], provider
        assert provider["config"]["models"][0]["type"] == "image" and provider["config"]["models"][1]["contextWindow"] == 1000

        image = {"kind": "images", "api": "test-images", "model": {"type": "image", "id": "flux", "provider": "ops", "api": "test-images"}, "context": {"input": [{"type": "text", "text": "a red circle"}]}, "options": {"apiKey": "sk-ops"}}
        _, response = _drive(host, "img-1", {"method": "provider_operation", "tool": "ops", "args": image})
        assert response["response"]["error"] is None, response
        assert response["response"]["result"]["model"] == "flux" and response["response"]["result"]["key"] == "sk-ops"
        model, request, values, cancelled = seen["image"]
        assert model["id"] == "flux" and request["input"][0]["text"] == "a red circle" and values["apiKey"] == "sk-ops" and cancelled is False

        question = {"kind": "classifiers", "api": "test-classifier", "model": {"type": "classifier", "id": "cls", "provider": "ops"}, "context": {"state": {"text": "ok"}, "questions": {"z": {"type": "bool"}, "y": {"type": "bool"}}}}
        _, response = _drive(host, "cls-1", {"method": "provider_operation", "tool": "ops", "args": question})
        assert seen["order"] == ["z", "y"], "the callback lost the question order"
        assert list(response["response"]["result"]["answers"]) == ["b", "a"]

        failing = {**question, "context": {"state": {"text": "fail"}, "questions": {}}}
        _, response = _drive(host, "cls-2", {"method": "provider_operation", "tool": "ops", "args": failing})
        assert "classifier failed" in response["response"]["error"]["message"]
        unknown = {**question, "api": "other"}
        _, response = _drive(host, "cls-3", {"method": "provider_operation", "tool": "ops", "args": unknown})
        # upstream: provider-composer.ts composeModelProvider, `Provider ${providerId} has no classifier implementation for "${model.api}"`.
        assert response["response"]["error"]["message"] == 'Provider ops has no classifier implementation for "other"', response
    finally:
        host.close()


def test_provider_re_registration_keeps_implementations_it_does_not_redefine() -> None:
    """model-runtime.ts registerProvider merges a re-registration's defined values, so a second registration without images keeps the first's; the host keeps the APIs it wired."""
    ran: list[str] = []
    ext = pig_sdk.Extension("py-ops")
    ext.register_provider("ops", {
        "baseUrl": "https://ops.test/v1",
        "images": {"test-images": lambda model, request, options: ran.append("first images") or {"stopReason": "stop"}},
        "classifiers": {"test-classifier": lambda model, request, options: ran.append("first classifier") or {"stopReason": "stop"}},
    })
    ext.register_provider("ops", {
        "baseUrl": "https://ops.test/v2",
        "classifiers": {"test-classifier": lambda model, request, options: ran.append("second classifier") or {"stopReason": "stop"}},
    })
    host = _host(ext)
    try:
        image = {"kind": "images", "api": "test-images", "model": {"id": "flux"}, "context": {"input": []}}
        _, response = _drive(host, "img-1", {"method": "provider_operation", "tool": "ops", "args": image})
        assert response["response"]["error"] is None, response
        question = {"kind": "classifiers", "api": "test-classifier", "model": {"id": "cls"}, "context": {"state": {}, "questions": {}}}
        _, response = _drive(host, "cls-1", {"method": "provider_operation", "tool": "ops", "args": question})
        assert response["response"]["error"] is None, response
        assert ran == ["first images", "second classifier"], ran
    finally:
        host.close()


def test_provider_config_rejects_a_non_callable_implementation() -> None:
    """The callables are the implementation: a value that cannot run is refused at registration, before anything is declared."""
    ext = pig_sdk.Extension("py-ops-bad")
    with pytest.raises(TypeError, match="test-images"):
        ext.register_provider("ops", {"images": {"test-images": "not callable"}})
    with pytest.raises(TypeError, match="test-classifier"):
        ext.register_provider("ops", {"classifiers": {"test-classifier": 3}})


def _object_provider(with_operations: bool) -> pig_sdk.Provider:
    auth = pig_sdk.ProviderAuth(api_key=pig_sdk.APIKeyAuth(name="Pixels key", resolve=lambda _input: pig_sdk.AuthResult(auth={})))
    provider = pig_sdk.Provider(
        id="pixels", name="Pixels", auth=auth,
        get_models=lambda: [{"id": "flux", "name": "Flux", "type": "image", "api": "test-images"}],
        stream=lambda *_: None, stream_simple=lambda *_: None,
    )
    if with_operations:
        provider.generate_images = lambda model, request, options: {"model": model["id"], "n": len(request["input"]), "key": options.values.get("apiKey"), "stopReason": "stop"}
        provider.classify = lambda model, request, options: {"model": model["id"], "order": list(request["questions"]), "answers": {"q": {"type": "bool", "probability": 0.5}}, "stopReason": "stop"}
    return provider


def test_provider_object_declares_and_runs_generate_images_and_classify() -> None:
    """types.ts:1896-1898 for a Provider object: generateImages and classify are members, declared with its other methods and run through provider_call."""
    ext = pig_sdk.Extension("py-objects")
    ext.register_provider(_object_provider(True))
    host = _host(ext)
    try:
        native = host.register["providers"][0]["native"]
        assert "generateImages" in native["methods"] and "classify" in native["methods"], native["methods"]
        call = {"method": "provider_call", "tool": native["key"], "args": {"method": "generateImages", "params": {"model": {"id": "flux"}, "context": {"input": [{"type": "text", "text": "x"}]}, "options": {"apiKey": "sk"}}}}
        _, response = _drive(host, "obj-1", call)
        assert response["response"]["result"] == {"model": "flux", "n": 1, "key": "sk", "stopReason": "stop"}, response
        call["args"] = {"method": "classify", "params": {"model": {"id": "cls"}, "context": {"state": {}, "questions": {"q2": {"type": "bool"}, "q1": {"type": "bool"}}}, "options": {}}}
        _, response = _drive(host, "obj-2", call)
        assert response["response"]["result"]["order"] == ["q2", "q1"] and response["response"]["result"]["model"] == "cls"
    finally:
        host.close()

    bare = pig_sdk.Extension("py-objects-bare")
    bare.register_provider(_object_provider(False))
    host = _host(bare)
    try:
        methods = host.register["providers"][0]["native"]["methods"]
        assert "generateImages" not in methods and "classify" not in methods, methods
    finally:
        host.close()

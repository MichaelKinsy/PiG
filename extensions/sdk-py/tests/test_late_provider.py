"""A provider registered after the factory finished keeps its images, classifiers and streamSimple in the Python SDK.

Pi's `pi.registerProvider` takes the whole ProviderConfig, callbacks included, at any time (.upstream/v0.99.2/packages/coding-agent/src/core/extensions/types.ts:1766-1803,1875-1903, loader.ts:449-457, runner.ts:517-523). The callables stay in the extension; the host call names them as the register payload does (`image_apis`, `classifier_apis`, `stream_simple`) and the host runs each through a `provider_operation` or `provider_stream_simple` request.
"""

from __future__ import annotations

from typing import Any

import pytest
from test_extension_api_099 import _command_ext, _drive, _host

import pig_sdk

ZERO = {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}


def _config(ran: list[str]) -> dict[str, Any]:
    def images(model: dict[str, Any], request: dict[str, Any], options: pig_sdk.ProviderOperationOptions) -> dict[str, Any]:
        ran.append(f"images:{model['id']}:{options.values['apiKey']}")
        return {"api": "late-images", "provider": "late", "model": "flux", "output": [], "stopReason": "stop", "timestamp": 1}

    def classify(model: dict[str, Any], request: dict[str, Any], options: pig_sdk.ProviderOperationOptions) -> dict[str, Any]:
        ran.append("classifier")
        return {"api": "late-classifier", "provider": "late", "model": "cls", "answers": {}, "stopReason": "stop", "timestamp": 2}

    def stream_simple(ctx: pig_sdk.Context, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any]) -> pig_sdk.ModelEventStream:
        ran.append(f"stream:{model['id']}:{options['apiKey']}")
        stream = pig_sdk.ModelEventStream()
        message = {"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": [{"type": "text", "text": "late"}], "stopReason": "stop", "timestamp": 1}
        stream.push({"type": "done", "reason": "stop", "message": message})
        return stream

    return {
        "api": "late-chat-api", "baseUrl": "https://late.test/v1", "apiKey": "late-key",
        "models": [
            {"id": "chat", "name": "Chat", "reasoning": False, "input": ["text"], "cost": ZERO, "contextWindow": 1000, "maxTokens": 100},
            {"id": "flux", "name": "Flux", "type": "image", "api": "late-images", "input": ["text"], "output": ["image"], "cost": ZERO},
        ],
        "images": {"late-images": images},
        "classifiers": {"late-classifier": classify},
        "streamSimple": stream_simple,
    }


def _image(api: str = "late-images") -> dict[str, Any]:
    return {"kind": "images", "api": api, "model": {"type": "image", "id": "flux", "provider": "late", "api": api}, "context": {"input": []}, "options": {"apiKey": "sk-late"}}


def _classify() -> dict[str, Any]:
    return {"kind": "classifiers", "api": "late-classifier", "model": {"id": "cls"}, "context": {"state": {}, "questions": {}}}


def _stream() -> dict[str, Any]:
    return {"model": {"id": "chat", "provider": "late", "api": "late-chat-api"}, "context": {"messages": []}, "options": {"apiKey": "sk-stream"}}


def test_model_registry_register_provider_keeps_operations_after_the_factory() -> None:
    """The late host call carries the declaration; the callables never cross the wire and run when the host asks."""
    ran: list[str] = []
    ext, ready = _command_ext("py-late", lambda ctx: ctx.model_registry.register_provider("late", _config(ran)))
    host = _host(ext, ready)
    try:
        assert host.register["providers"] == [], "the factory registered nothing"
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"}, {"registerProvider": None})
        assert response["response"]["error"] is None, response
        assert [c["call"]["method"] for c in calls] == ["registerProvider"]
        args = calls[0]["call"]["args"]
        assert not {"images", "classifiers", "streamSimple"} & set(args["config"]), args
        assert (args["name"], args["stream_simple"], args["image_apis"], args["classifier_apis"]) == ("late", True, ["late-images"], ["late-classifier"]), args
        assert args["config"]["baseUrl"] == "https://late.test/v1" and len(args["config"]["models"]) == 2

        _, response = _drive(host, "img-1", {"method": "provider_operation", "tool": "late", "args": _image()})
        assert response["response"]["error"] is None, response
        _, response = _drive(host, "cls-1", {"method": "provider_operation", "tool": "late", "args": _classify()})
        assert response["response"]["error"] is None, response
        _, response = _drive(host, "stream-1", {"method": "provider_stream_simple", "tool": "late", "args": _stream()})
        assert response["response"]["error"] is None and response["response"]["result"]["content"][0]["text"] == "late", response
        assert ran == ["images:flux:sk-late", "classifier", "stream:chat:sk-stream"]
    finally:
        host.close()


def test_model_registry_register_provider_merges_operations_like_the_factory() -> None:
    """model-runtime.ts:753-766: a late registration that defines no operation keeps the implementations of the one before."""
    ran: list[str] = []

    def handler(ctx: pig_sdk.Context) -> None:
        ctx.model_registry.register_provider("late", _config(ran))
        ctx.model_registry.register_provider("late", {"baseUrl": "https://late.test/v2"})

    ext, ready = _command_ext("py-late-twice", handler)
    host = _host(ext, ready)
    try:
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"}, {"registerProvider": None})
        assert response["response"]["error"] is None and len(calls) == 2, (calls, response)
        second = calls[1]["call"]["args"]
        assert "image_apis" not in second and "classifier_apis" not in second and not second.get("stream_simple"), second
        _, response = _drive(host, "img-1", {"method": "provider_operation", "tool": "late", "args": _image()})
        assert response["response"]["error"] is None and ran == ["images:flux:sk-late"], (response, ran)
    finally:
        host.close()


def test_model_registry_unregister_provider_drops_its_operations() -> None:
    """types.ts:1805-1819: after unregisterProvider no request finds a callable the extension held for it."""
    ran: list[str] = []

    def handler(ctx: pig_sdk.Context) -> None:
        ctx.model_registry.register_provider("late", _config(ran))
        ctx.model_registry.unregister_provider("late")

    ext, ready = _command_ext("py-late-gone", handler)
    host = _host(ext, ready)
    try:
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"}, {"registerProvider": None, "unregisterProvider": None})
        assert response["response"]["error"] is None and [c["call"]["method"] for c in calls] == ["registerProvider", "unregisterProvider"], (calls, response)
        _, response = _drive(host, "img-1", {"method": "provider_operation", "tool": "late", "args": _image()})
        assert response["response"]["error"] is not None, response
        _, response = _drive(host, "stream-1", {"method": "provider_stream_simple", "tool": "late", "args": _stream()})
        assert response["response"]["error"] is not None, response
        assert ran == []
    finally:
        host.close()


def test_model_registry_register_provider_refuses_a_non_callable_before_the_host_hears_of_it() -> None:
    """The factory registration refuses a value that cannot run (TypeError); the late call does the same and sends nothing."""
    seen: dict[str, Any] = {}

    def handler(ctx: pig_sdk.Context) -> None:
        for name, config in (("images", {"images": {"late-images": "not callable"}}), ("classifiers", {"classifiers": {"late-classifier": 3}}), ("streamSimple", {"streamSimple": "no"})):
            with pytest.raises(TypeError, match="callable"):
                ctx.model_registry.register_provider("late", config)
            seen[name] = True

    ext, ready = _command_ext("py-late-bad", handler)
    host = _host(ext, ready)
    try:
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"}, {"registerProvider": None})
        assert response["response"]["error"] is None and calls == [] and set(seen) == {"images", "classifiers", "streamSimple"}, (calls, response, seen)
    finally:
        host.close()


def test_model_registry_refused_registration_keeps_the_held_callables() -> None:
    """model-registry.test.ts:1320-1343: a registration the host refuses leaves the provider as it was, so the callables of the refused config do not replace the held ones."""
    ran: list[str] = []
    outcome: dict[str, Any] = {}

    def handler(ctx: pig_sdk.Context) -> None:
        ctx.model_registry.register_provider("late", _config(ran))
        second = _config(ran)
        second["images"] = {"late-images": lambda model, request, options: ran.append("refused images") or {"stopReason": "stop"}}
        try:
            ctx.model_registry.register_provider("late", second)
        except Exception as error:
            outcome["error"] = str(error)

    ext, ready = _command_ext("py-late-refused", handler)
    host = _host(ext, ready)
    try:
        host.request("r1", {"method": "command", "tool": "go"})
        registrations = 0
        while True:
            env = host.read()
            if env["type"] == "response":
                assert env["response"]["error"] is None, env
                break
            registrations += 1
            if registrations == 2:
                host.answer(env, error={"message": "invalid model definitions"})
            else:
                host.answer(env, None)
        assert registrations == 2 and "invalid model definitions" in outcome["error"], (registrations, outcome)
        _, response = _drive(host, "img-1", {"method": "provider_operation", "tool": "late", "args": _image()})
        assert response["response"]["error"] is None and ran == ["images:flux:sk-late"], (response, ran)
    finally:
        host.close()


def test_extension_register_provider_after_run_sends_the_host_call() -> None:
    """Pi's pi.registerProvider takes effect at once after the factory (types.ts:1766-1803, runner.ts:517-523): Extension.register_provider called once the extension runs sends the call ModelRegistry sends, instead of queueing a registration nothing sends."""
    ran: list[str] = []
    ext = pig_sdk.Extension("py-late-ext")
    ext.command("go", "run", lambda ctx, _args: ext.register_provider("late", _config(ran)))
    host = _host(ext)
    try:
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"}, {"registerProvider": None})
        assert response["response"]["error"] is None and [c["call"]["method"] for c in calls] == ["registerProvider"], (calls, response)
        args = calls[0]["call"]["args"]
        assert (args["name"], args["stream_simple"], args["image_apis"], args["classifier_apis"]) == ("late", True, ["late-images"], ["late-classifier"]), args
        _, response = _drive(host, "img-1", {"method": "provider_operation", "tool": "late", "args": _image()})
        assert response["response"]["error"] is None and ran == ["images:flux:sk-late"], (response, ran)
    finally:
        host.close()


def test_extension_register_provider_after_run_raises_a_refusal_and_keeps_the_held_callables() -> None:
    """A late registration the host refuses throws in Pi (model-runtime.ts registerProvider validates before it stores) and leaves the provider as it was."""
    ran: list[str] = []
    outcome: dict[str, Any] = {}
    ext = pig_sdk.Extension("py-late-ext-refused")

    def handler(_ctx: pig_sdk.Context, _args: str) -> None:
        ext.register_provider("late", _config(ran))
        second = _config(ran)
        second["images"] = {"late-images": lambda model, request, options: ran.append("refused images") or {"stopReason": "stop"}}
        try:
            ext.register_provider("late", second)
        except Exception as error:
            outcome["error"] = str(error)

    ext.command("go", "run", handler)
    host = _host(ext)
    try:
        host.request("r1", {"method": "command", "tool": "go"})
        registrations = 0
        while True:
            env = host.read()
            if env["type"] == "response":
                break
            registrations += 1
            if registrations == 2:
                host.answer(env, error={"message": "invalid model definitions"})
            else:
                host.answer(env, None)
        assert registrations == 2 and "invalid model definitions" in outcome.get("error", ""), (registrations, outcome)
        _, response = _drive(host, "img-1", {"method": "provider_operation", "tool": "late", "args": _image()})
        assert response["response"]["error"] is None and ran == ["images:flux:sk-late"], (response, ran)
    finally:
        host.close()


def test_extension_unregister_provider_after_run_sends_the_host_call() -> None:
    """Pi's pi.unregisterProvider also takes effect at once after the factory (types.ts:1805-1819)."""
    ran: list[str] = []
    ext = pig_sdk.Extension("py-late-ext-gone")

    def handler(_ctx: pig_sdk.Context, _args: str) -> None:
        ext.register_provider("late", _config(ran))
        ext.unregister_provider("late")

    ext.command("go", "run", handler)
    host = _host(ext)
    try:
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"}, {"registerProvider": None, "unregisterProvider": None})
        assert response["response"]["error"] is None and [c["call"]["method"] for c in calls] == ["registerProvider", "unregisterProvider"], (calls, response)
        assert calls[1]["call"]["args"] == {"name": "late"}
        _, response = _drive(host, "img-1", {"method": "provider_operation", "tool": "late", "args": _image()})
        assert response["response"]["error"] is not None and ran == [], (response, ran)
    finally:
        host.close()


def test_extension_register_oauth_provider_after_run_sends_the_host_call() -> None:
    """An OAuth registration after run takes effect at once too; the closures stay here and the host asks for them by provider name."""
    ext = pig_sdk.Extension("py-late-oauth")
    oauth = pig_sdk.OAuthProvider(name="Late SSO", login=lambda callbacks: {"access": "a", "refresh": "r", "expires": 1}, get_api_key=lambda credentials: "key-" + credentials["access"])
    ext.command("go", "run", lambda ctx, _args: ext.register_oauth_provider("late-oauth", {"baseUrl": "https://late.test"}, oauth))
    host = _host(ext)
    try:
        calls, response = _drive(host, "r1", {"method": "command", "tool": "go"}, {"registerProvider": None})
        assert response["response"]["error"] is None and [c["call"]["method"] for c in calls] == ["registerProvider"], (calls, response)
        config = calls[0]["call"]["args"]["config"]
        assert config["oauth"]["name"] == "Late SSO" and config["oauth"]["has_login"] is True, config
        assert ext._oauth_providers["late-oauth"] is oauth
    finally:
        host.close()


def test_extension_register_provider_before_run_queues() -> None:
    """Before run the registration is queued for the register payload."""
    ext = pig_sdk.Extension("py-queued")
    ext.register_provider("queued", _config([]))
    ext.register_provider("dropped", {"baseUrl": "https://dropped.test"})
    ext.unregister_provider("dropped")
    host = _host(ext)
    try:
        assert [(p["name"], p.get("stream_simple"), p.get("image_apis")) for p in host.register["providers"]] == [("queued", True, ["late-images"])]
    finally:
        host.close()

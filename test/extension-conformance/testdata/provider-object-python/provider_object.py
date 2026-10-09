import json
import os
import threading
from dataclasses import asdict
import pig_sdk as sdk


def new_extension():
    extension = sdk.Extension("provider-object-python")
    if os.environ.get("CARRIER_ROLE") != "reader":
        extension.register_native_provider(make_provider())
    extension.command("remote-carrier-probe", "Exercise a foreign Provider", probe)
    return extension


def make_provider():
    lock = threading.Lock()
    model = {"id": "carrier-model", "name": "Carrier", "provider": "carrier-provider", "api": "openai-completions", "baseUrl": "http://127.0.0.1:9", "reasoning": False, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 100}
    models = [model]
    def stream(model, context, options):
        meta = options.values.get("metadata", {})
        if meta.get("fail"):
            raise RuntimeError("carrier stream failed")
        # Pi types.ts:1917-1920: streamSimple invokes options.onPayload before the request and options.onResponse after the response.
        if options.on_payload is not None:
            options.on_payload({"marker": "carrier-payload"}, model)
        if options.on_response is not None:
            options.on_response({"status": 207, "headers": {"x-carrier": "carrier-response"}}, model)
        events = sdk.ModelEventStream()
        message = {"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": [], "usage": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}, "stopReason": "stop", "timestamp": 1}
        if meta.get("wait"):
            def abort():
                message.update(stopReason="aborted", errorMessage="carrier cancelled")
                events.push({"type": "error", "reason": "aborted", "error": message})
            options.signal.subscribe(abort)
        else:
            message["content"] = [{"type": "text", "text": meta.get("method", "carrier answer")}]
            events.push({"type": "done", "reason": "stop", "message": message})
        return events
    def resolve(input):
        key = (input.credential or {}).get("key", input.ctx.env("CARRIER_KEY"))
        return sdk.AuthResult({"apiKey": key} if key is not None else {}, source="caller context")
    def get_models():
        with lock:
            return list(models)
    def get_all_models():
        with lock:
            return [*models, {**model, "id": "carrier-all-only"}]
    def refresh(input):
        if not input.force:
            return
        def update():
            with lock:
                models.append({**model, "id": "refreshed"})
        input.publish(sdk.ModelsPublication(None, update))
    def cancel_deferred(model, handle, options):
        assert handle["id"] == "deferred"
    return sdk.Provider("carrier-provider", "Carrier Provider", sdk.ProviderAuth(
        api_key=sdk.APIKeyAuth("Carrier API key", resolve,
            check=lambda input: sdk.AuthCheck("api_key", input.ctx.env("CARRIER_SOURCE")),
            login=lambda input: {"type": "api_key", "key": input.prompt({"type": "secret", "message": "Carrier key"})}),
        oauth=sdk.OAuthAuth("Carrier OAuth",
            login=lambda input: {"type": "oauth", "access": input.prompt({"type": "text", "message": "Carrier OAuth"}), "refresh": "refresh", "expires": 100},
            refresh=lambda credential, signal: {**credential, "access": "rotated", "expires": 200},
            to_auth=lambda credential: {"apiKey": credential["access"], "baseUrl": "https://carrier.invalid"},
            is_subscription=True, login_label="Carrier login")),
        get_models, stream, lambda model, context, options: stream(model, context, sdk.ProviderStreamOptions({"metadata": {"method": "simple"}}, options.signal, options.on_payload, options.on_response)),
        base_url=model["baseUrl"], headers={"X-Carrier": "present"},
        filter_models=lambda models, credential: models[:1] if (credential or {}).get("key") == "selected" else [],
        get_all_models=get_all_models,
        filter_all_models=lambda models, credential: models if (credential or {}).get("key") == "all" else [],
        refresh_models=refresh,
        fetch_deferred=lambda model, handle, options: stream(model, {}, sdk.ProviderStreamOptions({"metadata": {"method": handle["id"]}}, options.signal)),
        cancel_deferred=cancel_deferred)


def probe(ctx, path):
    provider = ctx.model_registry.get_registered_native_provider("carrier-provider")
    assert provider is not None
    assert ctx.model_registry.get_provider(provider.id) is provider
    models = provider.get_models()
    model = models[0]
    assert provider.filter_models(models, {"type": "api_key", "key": "selected"})[0] is model
    assert provider.filter_models(models, None) == []
    signal = sdk.ProviderSignal()
    input = sdk.APIKeyAuthInput(sdk.AuthContext(lambda name: "injected:" + name, lambda path: False), signal=signal)
    check = provider.auth.api_key.check(input)
    auth = provider.auth.api_key.resolve(input)
    interaction = sdk.AuthInteraction(lambda prompt: prompt["message"], lambda event: None, signal)
    api_login = provider.auth.api_key.login(interaction)
    login = provider.auth.oauth.login(interaction)
    rotated = provider.auth.oauth.refresh(login, signal)
    oauth = provider.auth.oauth.to_auth(rotated)
    order = []
    def publish(publication):
        assert publication.persist is None
        order.append("persist")
        publication.update()
        order.append("update")
        return True
    provider.refresh_models(sdk.RefreshModelsContext(publish, force=True, signal=signal))
    result = provider.stream(model, {"messages": []}, sdk.ProviderStreamOptions()).result()
    simple = provider.stream_simple(model, {"messages": []}, sdk.ProviderStreamOptions()).result()
    deferred = provider.fetch_deferred(model, {"id": "deferred"}, sdk.ProviderStreamOptions()).result()
    provider.cancel_deferred(model, {"id": "deferred"}, sdk.ProviderStreamOptions())
    cancellation = sdk.ProviderSignal()
    waiting = provider.stream(model, {"messages": []}, sdk.ProviderStreamOptions({"metadata": {"wait": True}}, cancellation))
    cancellation.set()
    cancelled = waiting.result()
    clean = lambda value: {key: item for key, item in asdict(value).items() if item is not None}
    with open(path, "w") as output:
        json.dump({"headers": provider.headers, "check": clean(check), "auth": clean(auth), "apiLogin": api_login, "login": login, "rotated": rotated, "oauth": oauth, "order": order, "models": [model["id"] for model in provider.get_models()], "result": result["content"], "simple": simple["content"], "deferred": deferred["content"], "cancelled": cancelled["stopReason"]}, output, separators=(",", ":"))

"""Pi Provider objects and connection-owned callback carriers."""
from __future__ import annotations

from dataclasses import dataclass, field, asdict
import json
import threading
from typing import Any, Callable
import uuid
import weakref


class _ProviderLease:
    pass


def _release_provider(extension, handle, token):
    owner = extension()
    if owner is None or owner._shutdown.is_set():
        return
    # A finalizer may run on a thread that holds the write lock; queue the release instead of sending it.
    owner._send_from_finalizer({"type": "call", "call": {"method": "provider.release", "args": {"handle": handle, "token": token}}})


class ProviderSignal(threading.Event):
    def __init__(self):
        super().__init__()
        self._listeners_lock = threading.RLock()
        self._listeners = set()

    def set(self):
        with self._listeners_lock:
            if self.is_set():
                return
            super().set()
            listeners = list(self._listeners)
            self._listeners.clear()
        for listener in listeners:
            listener()

    def subscribe(self, listener):
        with self._listeners_lock:
            self._listeners.add(listener)
            if self.is_set():
                listener()
        def unsubscribe():
            with self._listeners_lock:
                self._listeners.discard(listener)
        return unsubscribe


@dataclass
class AuthContext:
    env: Callable[[str], str | None]
    file_exists: Callable[[str], bool]


@dataclass
class APIKeyAuthInput:
    ctx: AuthContext
    credential: dict | None = None
    signal: ProviderSignal | None = None


@dataclass
class AuthResult:
    auth: dict
    env: dict | None = None
    source: str | None = None


@dataclass
class AuthCheck:
    type: str
    source: str | None = None


@dataclass
class AuthInteraction:
    prompt: Callable[[dict], str]
    notify: Callable[[dict], None]
    signal: ProviderSignal | None = None


@dataclass
class APIKeyAuth:
    name: str
    resolve: Callable[[APIKeyAuthInput], AuthResult | None]
    check: Callable[[APIKeyAuthInput], AuthCheck | None] | None = None
    login: Callable[[AuthInteraction], dict] | None = None


@dataclass
class OAuthAuth:
    name: str
    login: Callable[[AuthInteraction], dict]
    refresh: Callable[[dict, ProviderSignal], dict]
    to_auth: Callable[[dict], dict]
    is_subscription: bool | None = None
    login_label: str | None = None


@dataclass
class ProviderAuth:
    api_key: APIKeyAuth | None = None
    oauth: OAuthAuth | None = None


@dataclass
class ProviderStreamOptions:
    values: dict = field(default_factory=dict)
    signal: ProviderSignal | None = None
    on_payload: Callable[[Any, dict], Any] | None = None
    on_response: Callable[[dict, dict], None] | None = None
    transform_headers: Callable[[dict], dict] | None = None


@dataclass
class ProviderOperationOptions:
    """What an image or classifier implementation receives besides the model and the request: the resolved request options, and the request's cancellation."""
    values: dict = field(default_factory=dict)
    signal: ProviderSignal | None = None


OMITTED = object()


@dataclass
class ModelsPublication:
    persist: Any = OMITTED
    update: Callable[[], None] | None = None


@dataclass
class RefreshModelsContext:
    publish: Callable[[ModelsPublication], bool]
    credential: dict | None = None
    stored: dict | None = None
    allow_network: bool = False
    force: bool | None = None
    signal: ProviderSignal | None = None


@dataclass
class Provider:
    id: str
    name: str
    auth: ProviderAuth
    get_models: Callable[[], list[dict]]
    stream: Callable[[dict, dict, ProviderStreamOptions], Any]
    stream_simple: Callable[[dict, dict, ProviderStreamOptions], Any]
    base_url: str | None = None
    headers: dict | None = None
    filter_models: Callable[[list[dict], dict | None], list[dict]] | None = None
    refresh_models: Callable[[RefreshModelsContext], None] | None = None
    fetch_deferred: Callable[[dict, dict, ProviderStreamOptions], Any] | None = None
    cancel_deferred: Callable[[dict, dict, ProviderStreamOptions], None] | None = None
    # ``(model, request, options: ProviderOperationOptions) -> dict``: the provider's image generation, and its classification (``request`` is ``{"state", "questions"}``).
    generate_images: Callable[[dict, dict, ProviderOperationOptions], dict] | None = None
    classify: Callable[[dict, dict, ProviderOperationOptions], dict] | None = None


def _method(provider, method):
    aliases = {"getModels": "get_models", "filterModels": "filter_models", "refreshModels": "refresh_models", "streamSimple": "stream_simple", "fetchDeferred": "fetch_deferred", "cancelDeferred": "cancel_deferred", "generateImages": "generate_images", "apiKey": "api_key", "toAuth": "to_auth"}
    obj = provider
    for part in method.split("."):
        obj = getattr(obj, aliases.get(part, part), None)
        if obj is None:
            break
    return obj


METHODS = ("getModels", "filterModels", "refreshModels", "stream", "streamSimple", "fetchDeferred", "cancelDeferred", "generateImages", "classify", "auth.apiKey.check", "auth.apiKey.resolve", "auth.apiKey.login", "auth.oauth.login", "auth.oauth.refresh", "auth.oauth.toAuth")


def declaration(provider, key):
    if not provider.id.strip():
        raise ValueError("Provider id must not be empty")
    try:
        models = provider.get_models()
    except Exception:
        # Pi Models treats a throwing getModels as an empty catalog at registration.
        models = []
    result = {"id": provider.id, "key": key, "name": provider.name, "models": models, "auth": {}, "methods": [m for m in METHODS if callable(_method(provider, m))]}
    if provider.base_url is not None:
        result["baseUrl"] = provider.base_url
    if provider.headers is not None:
        result["headers"] = provider.headers
    if provider.auth.api_key:
        result["auth"]["apiKey"] = {"name": provider.auth.api_key.name}
    if provider.auth.oauth:
        auth = provider.auth.oauth
        result["auth"]["oauth"] = {"name": auth.name}
        if auth.is_subscription is not None:
            result["auth"]["oauth"]["isSubscription"] = auth.is_subscription
        if auth.login_label is not None:
            result["auth"]["oauth"]["loginLabel"] = auth.login_label
        result["oauth"] = {"name": auth.name, "isSubscription": auth.is_subscription is True, "has_login": True, "has_refresh": True, "has_get_api_key": True}
    return result


def register_native(extension, provider):
    key = uuid.uuid4().hex
    result = declaration(provider, key)
    with extension._provider_lock:
        extension._native_providers[key] = provider
    return result


def dispatch_callback(extension, request):
    with extension._provider_lock:
        callbacks = extension._provider_callbacks.get(request["tool"])
    if callbacks is None:
        raise RuntimeError("Provider callback is no longer active")
    args = request["args"]
    return callbacks[args["method"]](args.get("params") or {})


def dispatch_provider(extension, ctx, request):
    method = request["args"]["method"]
    args = request["args"].get("params") or {}
    key = request["tool"]
    if method == "update":
        with extension._provider_lock:
            update = extension._provider_updates.get(args["token"])
        if update is None:
            raise RuntimeError("Provider publication is no longer active")
        return update()
    with extension._provider_lock:
        provider = extension._native_providers.get(key)
    if provider is None:
        raise RuntimeError("Provider object is no longer registered")
    def callback(method, params):
        return ctx._call("provider.callback", {"provider": key, "method": method, "params": params}).get("result")
    fn = _method(provider, method)
    if fn is None:
        raise RuntimeError(f"Provider method {method} is absent")
    signal = ctx._cancelled
    if method == "getModels":
        return fn()
    if method == "filterModels":
        models = args["models"]
        result = fn(models, args.get("credential"))
        return {"models": result, "indices": [next((i for i, original in enumerate(models) if original is model), -1) for model in result]}
    if method in ("auth.apiKey.check", "auth.apiKey.resolve"):
        result = fn(APIKeyAuthInput(AuthContext(lambda name: callback("env", {"name": name}), lambda path: callback("fileExists", {"path": path})), args.get("credential"), signal))
        return {k: v for k, v in asdict(result).items() if v is not None} if result else None
    if method.endswith(".login"):
        return fn(AuthInteraction(lambda prompt: callback("prompt", {"prompt": prompt}), lambda event: callback("notify", {"event": event}), signal))
    if method == "auth.oauth.refresh":
        return fn(args["credential"], signal)
    if method == "auth.oauth.toAuth":
        return fn(args["credential"])
    if method == "refreshModels":
        def publish(publication):
            value = {} if publication.persist is OMITTED else {"persist": publication.persist}
            token = uuid.uuid4().hex
            if publication.update:
                with extension._provider_lock:
                    extension._provider_updates[token] = publication.update
            try:
                return callback("publish", {"publication": value, **({"token": token} if publication.update else {})})
            finally:
                with extension._provider_lock:
                    extension._provider_updates.pop(token, None)
        return fn(RefreshModelsContext(publish, args.get("credential"), args.get("stored"), args.get("allowNetwork", False), args.get("force"), signal))
    if method in ("generateImages", "classify"):
        return fn(args["model"], args["context"], ProviderOperationOptions(args.get("options") or {}, signal))
    options = ProviderStreamOptions(args.get("options") or {}, signal)
    if args.get("aborted"):
        options.signal = ProviderSignal()
        options.signal.set()
    for wire, field_name in (("onPayload", "on_payload"), ("onResponse", "on_response"), ("transformHeaders", "transform_headers")):
        if wire in args.get("callbacks", []):
            setattr(options, field_name, lambda value, model=None, wire=wire: callback(wire, {"value": value, "model": model}))
    if method == "cancelDeferred":
        return fn(args["model"], args["handle"], options)
    stream = fn(args["model"], args["handle"] if method == "fetchDeferred" else args["context"], options)
    extension._notify("tool_update", {"request_id": ctx.request_id, "result": {"type": "provider_started"}})
    from . import _forward_model_stream
    return _forward_model_stream(
        stream,
        extension._shutdown,
        lambda event: extension._notify("tool_update", {"request_id": ctx.request_id, "result": event}),
    )


def dispatch_operation(extension, ctx, request):
    """Run the image or classifier implementation of a provider config that a provider_operation request names (types.ts:1896-1898). An error is the request's error."""
    args = request.get("args") or {}
    provider = request.get("tool", "")
    kind = args.get("kind")
    if kind not in ("images", "classifiers"):
        raise RuntimeError(f"Unknown provider operation {kind!r}")
    implementation = (extension._provider_operations.get(provider) or {}).get(kind, {}).get(args.get("api"))
    if implementation is None:
        label = "image" if kind == "images" else "classifier"
        # upstream: provider-composer.ts composeModelProvider quotes the API with double quotes.
        raise RuntimeError(f"Provider {provider} has no {label} implementation for {json.dumps(args.get('api'))}")
    return implementation(args.get("model") or {}, args.get("context") or {}, ProviderOperationOptions(args.get("options") or {}, ctx._cancelled))


def remote_provider(context, decl):
    extension = context.extension
    decl = dict(decl)
    lease = _ProviderLease()
    token = uuid.uuid4().hex
    extension._call("provider.retain", {"handle": decl["handle"], "token": token})
    weakref.finalize(lease, _release_provider, weakref.ref(extension), decl["handle"], token)
    decl["_lease"] = lease
    def begin(method, params, callbacks, signal, stream_id=""):
        callback_id = stream_id or uuid.uuid4().hex
        with extension._provider_lock:
            extension._provider_callbacks[callback_id] = callbacks
        pending = extension._begin_call("provider.object", {"handle": decl["handle"], "method": method, "params": params, "callbackId": callback_id, "streamId": stream_id})
        def cancel():
            extension._send({"type": "call", "call": {"method": "cancelModelStream", "args": {"streamId": callback_id}}})
        unsubscribe = signal.subscribe(cancel) if signal else lambda: None
        def cleanup(lease=lease):
            unsubscribe()
            with extension._provider_lock:
                extension._provider_callbacks.pop(callback_id, None)
        return pending, cleanup
    def invoke(method, params=None, callbacks=None, signal=None):
        pending, cleanup = begin(method, params or {}, callbacks or {}, signal)
        try:
            return extension._wait_call(*pending, "provider.object").get("result")
        finally:
            cleanup()
    def auth_callbacks(input):
        return {"env": lambda args: input.ctx.env(args["name"]), "fileExists": lambda args: input.ctx.file_exists(args["path"])}
    def interaction(input):
        return {"prompt": lambda args: input.prompt(args["prompt"]), "notify": lambda args: input.notify(args["event"])}
    auth = ProviderAuth()
    methods = decl["methods"]
    if decl["auth"].get("apiKey"):
        def resolve(input):
            result = invoke("auth.apiKey.resolve", {"credential": input.credential}, auth_callbacks(input), input.signal)
            return AuthResult(**result) if result is not None else None
        def check(input):
            result = invoke("auth.apiKey.check", {"credential": input.credential}, auth_callbacks(input), input.signal)
            return AuthCheck(**result) if result is not None else None
        auth.api_key = APIKeyAuth(decl["auth"]["apiKey"]["name"], resolve, check if "auth.apiKey.check" in methods else None, (lambda input: invoke("auth.apiKey.login", {}, interaction(input), input.signal)) if "auth.apiKey.login" in methods else None)
    if decl["auth"].get("oauth"):
        data = decl["auth"]["oauth"]
        auth.oauth = OAuthAuth(data["name"], lambda input: invoke("auth.oauth.login", {}, interaction(input), input.signal), lambda credential, signal: invoke("auth.oauth.refresh", {"credential": credential}, signal=signal), lambda credential: invoke("auth.oauth.toAuth", {"credential": credential}), data.get("isSubscription"), data.get("loginLabel"))
    def streaming(method):
        def start(model, transcript, options=None):
            from . import ModelEventStream, _model_stream_error_event
            options = options or ProviderStreamOptions()
            stream = ModelEventStream()
            stream_id = uuid.uuid4().hex
            with extension._model_stream_lock:
                extension._model_streams[stream_id] = stream
            callbacks = {}
            for wire, name in (("onPayload", "on_payload"), ("onResponse", "on_response"), ("transformHeaders", "transform_headers")):
                fn = getattr(options, name)
                if fn:
                    callbacks[wire] = (lambda args, fn=fn: fn(args["value"])) if wire == "transformHeaders" else (lambda args, fn=fn: fn(args["value"], model))
            params = {"model": model, "handle" if method == "fetchDeferred" else "context": transcript, "options": options.values, "callbacks": list(callbacks), "aborted": bool(options.signal and options.signal.is_set())}
            pending, cleanup = begin(method, params, callbacks, options.signal, stream_id)
            def run():
                try:
                    extension._wait_call(*pending, "provider.object")
                    error = RuntimeError("Provider stream ended without a terminal event")
                except Exception as exc:
                    error = exc
                finally:
                    cleanup()
                stream.mark_started(error)
                stream.push(_model_stream_error_event(error, model))
                with extension._model_stream_lock:
                    extension._model_streams.pop(stream_id, None)
                with extension._request_threads_lock:
                    extension._request_threads.discard(threading.current_thread())
            worker = threading.Thread(target=run, name="pig-provider-stream", daemon=True)
            with extension._request_threads_lock:
                extension._request_threads.add(worker)
            worker.start()
            stream._started.wait()
            if stream._start_error:
                raise stream._start_error
            return stream
        return start
    provider = Provider(decl["id"], decl["name"], auth, lambda: invoke("getModels"), streaming("stream"), streaming("streamSimple"), decl.get("baseUrl"), decl.get("headers"))
    for wire, field_name in (("generateImages", "generate_images"), ("classify", "classify")):
        if wire in methods:
            setattr(provider, field_name, lambda model, request, options=None, wire=wire: invoke(wire, {"model": model, "context": request, "options": options.values if options else {}}, signal=options.signal if options else None))
    if "filterModels" in methods:
        def filter_models(models, credential):
            result = invoke("filterModels", {"models": models, "credential": credential})
            for i, index in enumerate(result["indices"]):
                if index >= 0:
                    models[index].clear()
                    models[index].update(result["models"][i])
                    result["models"][i] = models[index]
            return result["models"]
        provider.filter_models = filter_models
    if "refreshModels" in methods:
        def refresh(input):
            def publish(args):
                value = args["publication"]
                return input.publish(ModelsPublication(value.get("persist", OMITTED), (lambda: invoke("update", {"token": args["token"]})) if args.get("token") else None))
            return invoke("refreshModels", {"credential": input.credential, "stored": input.stored, "allowNetwork": input.allow_network, "force": input.force}, {"publish": publish}, input.signal)
        provider.refresh_models = refresh
    if "fetchDeferred" in methods:
        provider.fetch_deferred = streaming("fetchDeferred")
    if "cancelDeferred" in methods:
        provider.cancel_deferred = lambda model, handle, options=None: invoke("cancelDeferred", {"model": model, "handle": handle, "options": options.values if options else {}}, signal=options.signal if options else None)
    provider._provider_handle = decl["handle"]
    return provider

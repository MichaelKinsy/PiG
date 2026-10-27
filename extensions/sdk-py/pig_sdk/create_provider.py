"""pi-ai's ``createProvider`` (packages/ai/src/models.ts:1077): builds a :class:`Provider` from parts.

A single API streams every chat model, a dict of APIs dispatches on ``model["api"]``, and a model whose api has no entry produces a stream error; one-shot operation dicts dispatch on ``model["api"]`` the same way. The
returned Provider is the object ``Extension.register_native_provider`` takes. Pure and synchronous: it calls no host function.
"""

from __future__ import annotations

import threading
import time
from dataclasses import dataclass
from typing import Any, Callable

from .provider import ModelsPublication, Provider, ProviderAuth, ProviderOperationOptions, ProviderStreamOptions, RefreshModelsContext


@dataclass(kw_only=True)
class ProviderStreams:
    """pi-ai's ProviderStreams: the chat implementation of one API. ``fetch_deferred`` and ``cancel_deferred`` are optional."""

    stream: Callable[[dict, dict, ProviderStreamOptions], Any]
    stream_simple: Callable[[dict, dict, ProviderStreamOptions], Any]
    fetch_deferred: Callable[[dict, dict, ProviderStreamOptions], Any] | None = None
    cancel_deferred: Callable[[dict, dict, ProviderStreamOptions], None] | None = None


def _model_type(model: dict) -> str:
    kind = model.get("type")
    return kind if isinstance(kind, str) else "chat"


def _setup_error_stream(model: dict, error: BaseException | str) -> Any:
    """pi-ai's lazyStream (api/lazy.ts) of a setup that threw: the stream ends with an error event carrying the error message of an empty assistant message."""
    from . import create_assistant_message_event_stream

    message = {
        "role": "assistant", "content": [], "api": model.get("api"), "provider": model.get("provider"), "model": model.get("id"),
        "usage": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
        "stopReason": "error", "errorMessage": str(error), "timestamp": int(time.time() * 1000),
    }
    stream = create_assistant_message_event_stream()
    stream.push({"type": "error", "reason": "error", "error": message})
    return stream


def create_provider(
    *,
    id: str,
    auth: ProviderAuth,
    models: list[dict],
    name: str | None = None,
    base_url: str | None = None,
    headers: dict | None = None,
    fetch_models: Callable[[RefreshModelsContext], list[dict]] | None = None,
    filter_models: Callable[[list[dict], dict | None], list[dict]] | None = None,
    filter_all_models: Callable[[list[dict], dict | None], list[dict]] | None = None,
    api: ProviderStreams | dict[str, ProviderStreams] | None = None,
    images: dict[str, Callable[[dict, dict, ProviderOperationOptions], dict]] | None = None,
    classifiers: dict[str, Callable[[dict, dict, ProviderOperationOptions], dict]] | None = None,
) -> Provider:
    """``createProvider(options)``. ``auth`` is required: every provider has auth semantics, even ambient or keyless ones. ``models`` is the static baseline of every type (a model without ``type`` is a chat model).
    ``fetch_models`` fetches a dynamic model overlay of every type, which is restored and published transactionally; models of unknown types are dropped."""
    single = api if isinstance(api, ProviderStreams) else None
    by_api = {} if single is not None or api is None else dict(api)
    streams = [single] if single is not None else [entry for entry in by_api.values() if entry is not None]
    images = images or {}
    classifiers = classifiers or {}
    if not streams and not images and not classifiers:
        raise ValueError(f'Provider {id}: at least one of "api", "images", or "classifiers" is required.')
    lock = threading.Lock()
    dynamic: list[dict] = []

    def current_models() -> list[dict]:
        with lock:
            merged = list(models)
            for model in dynamic:
                for index, entry in enumerate(merged):
                    if _model_type(entry) == _model_type(model) and entry.get("id") == model.get("id"):
                        merged[index] = model
                        break
                else:
                    merged.append(model)
            return merged

    def api_for(model: dict) -> ProviderStreams | None:
        return single if single is not None else by_api.get(model.get("api"))

    def set_dynamic(value: list[dict]) -> Callable[[], None]:
        def update() -> None:
            nonlocal dynamic
            with lock:
                dynamic = value

        return update

    def refresh_models(context: RefreshModelsContext) -> None:
        if context.stored is not None:
            restored = [model for model in context.stored.get("models", []) if model.get("provider") == id]
            if not context.publish(ModelsPublication(update=set_dynamic(restored))):
                return
        if not context.allow_network or (context.signal is not None and context.signal.is_set()):
            return
        fetched = fetch_models(context)
        if context.signal is not None and context.signal.is_set():
            return
        refreshed = [model for model in fetched if _model_type(model) in ("chat", "image", "classifier")]
        context.publish(ModelsPublication(persist={"models": refreshed, "checkedAt": int(time.time() * 1000)}, update=set_dynamic(refreshed)))

    def dispatch(simple: bool) -> Callable[[dict, dict, ProviderStreamOptions], Any]:
        def run(model: dict, context: dict, options: ProviderStreamOptions) -> Any:
            implementation = api_for(model)
            if implementation is None:
                return _setup_error_stream(model, f'Provider {id} has no API implementation for "{_js_text(model.get("api"))}"')
            return (implementation.stream_simple if simple else implementation.stream)(model, context, options)

        return run

    provider = Provider(
        id=id, name=name if name is not None else id, auth=auth,
        get_models=lambda: [model for model in current_models() if _model_type(model) == "chat"],
        stream=dispatch(False), stream_simple=dispatch(True),
        base_url=base_url, headers=headers,
        filter_models=filter_models, filter_all_models=filter_all_models,
        get_all_models=current_models,
        refresh_models=refresh_models if fetch_models is not None else None,
    )
    if any(entry.fetch_deferred is not None for entry in streams):
        def fetch_deferred(model: dict, handle: dict, options: ProviderStreamOptions) -> Any:
            implementation = api_for(model)
            if implementation is None or implementation.fetch_deferred is None:
                return _setup_error_stream(model, f'Provider {id} does not support deferred responses for "{_js_text(model.get("api"))}"')
            try:
                return implementation.fetch_deferred(model, handle, options)
            except Exception as error:  # noqa: BLE001 - a setup that raised ends the stream with its message, as lazyStream does
                return _setup_error_stream(model, error)

        provider.fetch_deferred = fetch_deferred
    if any(entry.cancel_deferred is not None for entry in streams):
        def cancel_deferred(model: dict, handle: dict, options: ProviderStreamOptions) -> None:
            implementation = api_for(model)
            if implementation is None or implementation.cancel_deferred is None:
                raise RuntimeError(f'Provider {id} cannot cancel deferred responses for "{_js_text(model.get("api"))}"')
            implementation.cancel_deferred(model, handle, options)

        provider.cancel_deferred = cancel_deferred
    if images:
        def generate_images(model: dict, request: dict, options: ProviderOperationOptions) -> dict:
            implementation = images.get(model.get("api"))
            if implementation is None:
                return {
                    "api": model.get("api"), "provider": model.get("provider"), "model": model.get("id"), "output": [], "stopReason": "error",
                    "errorMessage": f'Provider {id} has no image generation implementation for "{_js_text(model.get("api"))}"', "timestamp": int(time.time() * 1000),
                }
            return implementation(model, request, options)

        provider.generate_images = generate_images
    if classifiers:
        def classify(model: dict, request: dict, options: ProviderOperationOptions) -> dict:
            implementation = classifiers.get(model.get("api"))
            if implementation is None:
                return {
                    "api": model.get("api"), "provider": model.get("provider"), "model": model.get("id"), "answers": {}, "stopReason": "error",
                    "errorMessage": f'Provider {id} has no classifier implementation for "{_js_text(model.get("api"))}"', "timestamp": int(time.time() * 1000),
                }
            return implementation(model, request, options)

        provider.classify = classify
    return provider


def _js_text(value: Any) -> str:
    """A model's api as JavaScript's template literal prints it: ``undefined`` for an absent one."""
    return "undefined" if value is None else str(value)

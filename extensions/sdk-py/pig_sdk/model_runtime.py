"""Pi's ``ModelRuntime`` class (core/model-runtime.ts): the configured model collection an extension builds over its own credential and model files, for models, provider auth, completions and the registrations a Session shares. The host owns the object for the extension's connection; this object holds its name. Models are Pi's Model objects as dicts and every result is Pi's in the JSON shape of Pi's types.

The Provider objects of Pi's class (``getProviders``, ``getProvider``, ``getRegisteredNativeProvider``, ``registerNativeProvider``) are not here: a Provider holds functions the host cannot run on the extension's behalf (the Provider surface D78 records). ``register_provider`` takes a configuration without functions. ``streamDeferred``, ``fetchDeferred`` and ``cancelDeferred`` are not here either."""
from __future__ import annotations

from typing import Any, Callable

from ._graph import _GraphObject, _callback_id


class ModelRuntime(_GraphObject):
    def __init__(self, context: Any, handle: str) -> None:
        raise TypeError("construct a ModelRuntime with ModelRuntime.create(context, ...)")

    @classmethod
    def create(
        cls,
        context: Any,
        *,
        auth_path: str | None = None,
        models_path: str | None = ...,  # type: ignore[assignment]
        models_store_path: str | None = None,
        allow_model_network: bool = False,
        model_refresh_timeout_ms: int | None = None,
        catalog_base_url: str | None = None,
        refresh_on_create: bool | None = None,
    ) -> "ModelRuntime":
        """``ModelRuntime.create(options)``. ``models_path=None`` is Pi's ``modelsPath: null`` (no models.json, an in-memory store); leaving it out is the agent directory's models.json."""
        options: dict[str, Any] = {}
        if auth_path is not None:
            options["authPath"] = auth_path
        if models_path is not ...:
            options["modelsPath"] = models_path
        if models_store_path is not None:
            options["modelsStorePath"] = models_store_path
        if allow_model_network:
            options["allowModelNetwork"] = True
        if model_refresh_timeout_ms is not None:
            options["modelRefreshTimeoutMs"] = model_refresh_timeout_ms
        if catalog_base_url is not None:
            options["catalogBaseUrl"] = catalog_base_url
        if refresh_on_create is not None:
            options["refreshOnCreate"] = refresh_on_create
        runtime = cls.__new__(cls)
        _GraphObject.__init__(runtime, context, "ModelRuntime", "create", options)
        return runtime

    def get_models(self, provider: str | None = None) -> list[dict[str, Any]]:
        return self._invoke("getModels", provider)

    def get_model(self, provider: str, model_id: str) -> dict[str, Any] | None:
        return self._invoke("getModel", provider, model_id)

    def get_physical_model(self, provider: str, model_id: str) -> dict[str, Any] | None:
        """A catalog chat model that is not virtual, ``None`` when there is none."""
        return self._invoke("getPhysicalModel", provider, model_id)

    def get_models_of_type(self, model_type: str, provider: str | None = None) -> list[dict[str, Any]]:
        return self._invoke("getModelsOfType", model_type, provider)

    def get_model_of_type(self, model_type: str, provider: str, model_id: str) -> dict[str, Any] | None:
        return self._invoke("getModelOfType", model_type, provider, model_id)

    def get_all_models(self, provider: str | None = None) -> list[dict[str, Any]]:
        return self._invoke("getAllModels", provider)

    def get_available_of_type(self, model_type: str, provider: str | None = None) -> list[dict[str, Any]]:
        return self._invoke_async("getAvailableOfType", model_type, provider)

    def get_all_available(self, provider: str | None = None) -> list[dict[str, Any]]:
        return self._invoke_async("getAllAvailable", provider)

    def get_available(self, provider: str | None = None) -> list[dict[str, Any]]:
        """The available chat models after an availability refresh, or of one provider."""
        return self._invoke_async("getAvailable", provider)

    def get_available_snapshot(self) -> list[dict[str, Any]]:
        """The available chat models as of the last availability pass, without waiting."""
        return self._invoke("getAvailableSnapshot")

    def get_error(self) -> str | None:
        return self._invoke("getError")

    def check_auth(self, provider: str) -> dict[str, Any] | None:
        """``{"type", "source"?}`` of the credential the provider's auth resolves to, ``None`` when it is unconfigured."""
        return self._invoke_async("checkAuth", provider)

    def get_registered_provider_ids(self) -> list[str]:
        return self._invoke("getRegisteredProviderIds")

    def get_registered_provider_config(self, provider: str) -> dict[str, Any] | None:
        return self._invoke("getRegisteredProviderConfig", provider)

    def get_compatibility_request_config(self, model: dict[str, Any]) -> dict[str, Any]:
        return self._invoke("getCompatibilityRequestConfig", model)

    def is_using_oauth(self, provider: str) -> bool:
        return self._invoke("isUsingOAuth", provider)

    def is_using_subscription(self, provider: str) -> bool:
        return self._invoke("isUsingSubscription", provider)

    def has_configured_auth(self, provider: str) -> bool:
        return self._invoke("hasConfiguredAuth", provider)

    def get_provider_auth_status(self, provider: str) -> dict[str, Any]:
        return self._invoke("getProviderAuthStatus", provider)

    def get_auth(self, provider_or_model: str | dict[str, Any], overrides: dict[str, Any] | None = None) -> dict[str, Any] | None:
        """Request auth for a provider id or a model object as Pi's AuthResult (``{"auth": {...}, "env"?, "source"?}``), ``None`` when it is unconfigured. ``overrides`` are Pi's ``apiKey``, ``env`` and ``minOAuthValidityMs``."""
        if not isinstance(provider_or_model, (str, dict)):
            raise TypeError("get_auth takes a provider id or a model object")
        return self._invoke_async("getAuth", provider_or_model, overrides)

    def set_runtime_api_key(self, provider: str, api_key: str) -> None:
        self._invoke_async("setRuntimeApiKey", provider, api_key)

    def remove_runtime_api_key(self, provider: str) -> None:
        self._invoke_async("removeRuntimeApiKey", provider)

    def list_credentials(self) -> list[dict[str, Any]]:
        return self._invoke_async("listCredentials")

    def login(self, provider: str, kind: str, prompt: Callable[[dict[str, Any]], str], notify: Callable[[dict[str, Any]], None] | None = None, agent_name: str | None = None, get_device_id: Callable[[], str] | None = None) -> dict[str, Any]:
        """Authenticate ``provider`` with the auth method of ``kind`` (``"api_key"`` or ``"oauth"``); ``prompt`` answers Pi's login prompts and ``notify`` receives its events. Returns Pi's Credential."""
        interaction = {"prompt": _callback_id(self._context, prompt), "notify": _callback_id(self._context, notify)}
        options = None if agent_name is None and get_device_id is None else {"agentName": agent_name, "getDeviceId": _callback_id(self._context, get_device_id)}
        return self._invoke_async("login", provider, kind, interaction, options)

    def logout(self, provider: str) -> None:
        self._invoke_async("logout", provider)

    def refresh(self, options: dict[str, Any] | None = None) -> dict[str, Any]:
        """``{"aborted", "errors"}``; ``options`` are Pi's ``allowNetwork``, ``providers`` and ``force``."""
        return self._invoke_async("refresh", options or {})

    def register_provider(self, provider: str, config: dict[str, Any]) -> None:
        """Register a provider by a configuration without functions (name, baseUrl, apiKey, api, headers, authHeader, models)."""
        self._invoke("registerProvider", provider, config)

    def unregister_provider(self, provider: str) -> None:
        self._invoke("unregisterProvider", provider)

    def register_virtual_model(self, model: Any) -> None:
        """Register a :class:`VirtualModel`; its ``route(ctx, request)`` runs in this process when a request resolves the model."""
        context = self._context
        route = model.route

        def routed(request: dict[str, Any]) -> dict[str, Any]:
            result = route(context, request)
            if not isinstance(result, dict) or not result.get("model"):
                raise RuntimeError(f"virtual model {model.provider}/{model.id} routed to no model")
            if result.get("state") is None:
                result = {key: value for key, value in result.items() if key != "state"}
            return result

        definition = {
            "provider": model.provider, "id": model.id, "name": model.name, "thinkingLevels": model.thinking_levels,
            "contextWindow": model.context_window, "maxTokens": model.max_tokens, "input": model.input, "route": _callback_id(context, routed),
        }
        self._invoke("registerVirtualModel", definition)

    def unregister_virtual_model(self, provider: str, model_id: str) -> None:
        self._invoke("unregisterVirtualModel", provider, model_id)

    def resolve_model(self, model: dict[str, Any], messages: list[dict[str, Any]], options: dict[str, Any]) -> dict[str, Any]:
        """Pi's ModelRoute (``{"model", "thinkingLevel", "state"?}``) of one request to a virtual model; ``options`` are ``reason``, ``thinkingLevel``, ``failed`` and ``state``."""
        return self._invoke_async("resolveModel", model, messages, options)

    def classify(self, model: dict[str, Any], context: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any]:
        """Never raises: a failure is a result with ``stopReason`` ``error`` or ``aborted``."""
        return self._never_fails("classify", model, "answers", {}, context, options)

    def generate_images(self, model: dict[str, Any], context: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any]:
        """Never raises: a failure is a result with ``stopReason`` ``error`` or ``aborted``."""
        return self._never_fails("generateImages", model, "output", [], context, options)

    def _never_fails(self, method: str, model: dict[str, Any], field: str, empty: Any, context: dict[str, Any], options: dict[str, Any] | None) -> dict[str, Any]:
        import time

        try:
            return self._invoke_async(method, model, context, options)
        except Exception as error:  # noqa: BLE001 - Pi reports every failure as a result
            return {
                "api": model.get("api"), "provider": model.get("provider"), "model": model.get("id"), field: empty,
                "stopReason": "aborted" if self._context._cancelled.is_set() else "error", "errorMessage": str(error), "timestamp": int(time.time() * 1000),
            }

    def _registry(self) -> Any:
        from . import ModelRegistry

        return ModelRegistry(self._context)

    def stream(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> Any:
        """Stream a model response through the runtime (``request`` and ``options`` as for ``ModelRegistry.stream``)."""
        return self._registry()._stream(model, request, options, False, self.handle)

    def stream_simple(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> Any:
        return self._registry()._stream(model, request, options, True, self.handle)

    def complete(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any] | None:
        return self.stream(model, request, options).result()

    def complete_simple(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any] | None:
        return self.stream_simple(model, request, options).result()

"""Pi's ``new ModelRegistry(runtime)`` (core/model-registry.ts:48-52): the compatibility facade over a :class:`ModelRuntime` the extension built. Every member forwards to the runtime as Pi's does, so it answers from that runtime and not from the Session's registry. A provider is registered by configuration without functions (see ``ModelRuntime.register_provider``)."""
from __future__ import annotations

from typing import Any

from . import ModelRegistry
from .model_runtime import ModelRuntime


class RuntimeModelRegistry(ModelRegistry):
    def __init__(self, runtime: ModelRuntime) -> None:
        super().__init__(runtime._context)
        self._runtime = runtime

    def refresh(self, options: dict[str, Any] | None = None) -> dict[str, Any]:
        return self._runtime.refresh(options)

    def get_error(self) -> str | None:
        return self._runtime.get_error()

    def get_all(self) -> list[dict[str, Any]]:
        return self._runtime.get_models()

    def get_available(self) -> list[dict[str, Any]]:
        return self._runtime.get_available_snapshot()

    def find(self, provider_id: str, model_id: str) -> dict[str, Any] | None:
        model = self._runtime.get_model(provider_id, model_id)
        return model if isinstance(model, dict) else None

    def find_of_type(self, model_type: str, provider: str, model_id: str) -> dict[str, Any] | None:
        return self._runtime.get_model_of_type(model_type, provider, model_id)

    def has_configured_auth(self, model: dict[str, Any]) -> bool:
        return self._runtime.has_configured_auth(model.get("provider"))

    def is_using_oauth(self, model: dict[str, Any]) -> bool:
        return self._runtime.is_using_oauth(model.get("provider"))

    def get_api_key_and_headers(self, model: dict[str, Any]) -> dict[str, Any]:
        """Pi's getApiKeyAndHeaders over the runtime (model-registry.ts:80-119): ``{"ok": True, "apiKey"?, "headers"?, "baseUrl"?, "env"?}`` or ``{"ok": False, "error"}``; it never raises."""
        provider = model.get("provider")

        def failed(message: str) -> dict[str, Any]:
            if message == "authHeader requires a resolved API key":
                message = f'No API key found for "{provider}"'
            return {"ok": False, "error": message}

        try:
            resolution = self._runtime.get_auth(model)
            if resolution is None:
                compat = self._runtime.get_compatibility_request_config(model)
                if compat.get("authHeader"):
                    return {"ok": False, "error": f'No API key found for "{provider}"'}
                out: dict[str, Any] = {"ok": True}
                if compat.get("headers") is not None:
                    out["headers"] = compat["headers"]
                return out
        except Exception as error:  # noqa: BLE001 - Pi reports every failure as a result
            return failed(str(error))
        out = {"ok": True}
        auth = resolution.get("auth") or {}
        for key in ("apiKey", "headers"):
            if auth.get(key) is not None:
                out[key] = auth[key]
        if auth.get("baseUrl"):
            out["baseUrl"] = auth["baseUrl"]
        if resolution.get("env") is not None:
            out["env"] = resolution["env"]
        return out

    def get_provider_auth_status(self, provider: str) -> dict[str, Any]:
        return self._runtime.get_provider_auth_status(provider)

    def get_provider(self, provider: str) -> Any:
        return self._runtime.get_provider(provider)

    def stream(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> Any:
        return self._runtime.stream(model, request, options)

    def stream_simple(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> Any:
        return self._runtime.stream_simple(model, request, options)

    def complete(self, model: dict[str, Any], request: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any] | None:
        return self._runtime.complete(model, request, options)

    def get_models_of_type(self, model_type: str, provider: str | None = None) -> list[dict[str, Any]]:
        return self._runtime.get_models_of_type(model_type, provider)

    def get_available_of_type(self, model_type: str, provider: str | None = None) -> list[dict[str, Any]]:
        return self._runtime.get_available_of_type(model_type, provider)

    def get_model_of_type(self, model_type: str, provider: str, model_id: str) -> dict[str, Any] | None:
        return self._runtime.get_model_of_type(model_type, provider, model_id)

    def classify(self, model: dict[str, Any], context: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any]:
        return self._runtime.classify(model, context, options)

    def generate_images(self, model: dict[str, Any], context: dict[str, Any], options: dict[str, Any] | None = None) -> dict[str, Any]:
        return self._runtime.generate_images(model, context, options)

    def get_provider_display_name(self, provider: str) -> str:
        return self._runtime._provider_display_name(provider)

    def get_provider_auth(self, provider: str) -> dict[str, Any] | None:
        return self._runtime.get_auth(provider)

    def get_api_key_for_provider(self, provider: str) -> str | None:
        try:
            resolution = self._runtime.get_auth(provider)
        except Exception:  # noqa: BLE001 - Pi answers undefined when auth resolution fails
            return None
        return ((resolution or {}).get("auth") or {}).get("apiKey")

    def register_provider(self, name: Any, config: dict[str, Any] | None = None) -> None:
        if isinstance(name, str):
            if config is None:
                raise ValueError("Provider config is required when registering by name")
            self._runtime.register_provider(name, config)
            return
        self._runtime.register_native_provider(name)

    def register_native_provider(self, provider: Any) -> None:
        self._runtime.register_native_provider(provider)

    def unregister_provider(self, name: str) -> None:
        self._runtime.unregister_provider(name)

    def register_virtual_model(self, model: Any) -> None:
        self._runtime.register_virtual_model(model)

    def unregister_virtual_model(self, provider: str, model_id: str) -> None:
        self._runtime.unregister_virtual_model(provider, model_id)

    def get_registered_provider_config(self, provider: str) -> dict[str, Any] | None:
        return self._runtime.get_registered_provider_config(provider)

    def get_registered_native_provider(self, provider: str) -> Any:
        return self._runtime.get_registered_native_provider(provider)

    def get_registered_provider_ids(self) -> list[str]:
        return self._runtime.get_registered_provider_ids()

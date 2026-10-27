"""Pi's ``createExtensionRuntime()`` result (core/extensions/loader.ts): the runtime state every extension of a load registers against, which an ``ExtensionRunner`` over those extensions binds its actions into. The host owns the object. A ``DefaultResourceLoader`` given it as ``runtime`` loads its extensions against it, and ``DefaultResourceLoader.get_extensions`` returns the runtime of the load as ``LoadExtensionsResult.runtime``.

Pi's runtime is a record of functions and Maps; this holds its data members: the flag values, the provider, native provider and virtual model registrations queued before a runner bound, the registered MCP servers and the stale state. The action functions (sendMessage and the rest) are the runner's, bound by ``ExtensionRunner.bind_core``."""
from __future__ import annotations

from typing import Any

from ._graph import _GraphObject


class ExtensionRuntime(_GraphObject):
    def __init__(self, context: Any, kind_op: str = "create", *args: Any) -> None:
        super().__init__(context, "ExtensionRuntime", kind_op, *args)

    @classmethod
    def _of_loader(cls, context: Any, loader_handle: str) -> "ExtensionRuntime":
        return cls(context, "of", loader_handle)

    def get_flag_values(self) -> dict[str, Any]:
        """``new Map(runtime.flagValues)`` as a dict: the value of each flag that has one, as the registrations' defaults and later ``set_flag_value`` calls left them. The host lists them by name."""
        return self._invoke("getFlagValues")

    def set_flag_value(self, name: str, value: bool | str) -> None:
        """``runtime.flagValues.set(name, value)``."""
        self._invoke("setFlagValue", name, value)

    def pending_provider_registrations(self) -> list[dict[str, Any]]:
        """``runtime.pendingProviderRegistrations``: ``[{name, config, extensionPath}]`` in registration order, the providers an extension registered while loading, which the runner's ``bind_core`` applies."""
        return self._invoke("getPendingProviderRegistrations")

    def pending_native_provider_registrations(self) -> list[dict[str, Any]]:
        """``runtime.pendingNativeProviderRegistrations``: ``[{provider: {id, name}, extensionPath}]`` in registration order."""
        return self._invoke("getPendingNativeProviderRegistrations")

    def pending_virtual_model_registrations(self) -> list[dict[str, Any]]:
        """``runtime.pendingVirtualModelRegistrations``: ``[{definition, extensionPath}]`` in registration order, without the route function."""
        return self._invoke("getPendingVirtualModelRegistrations")

    def mcp_servers(self) -> list[dict[str, Any]]:
        """``runtime.mcpServers.list()``: the MCP servers extensions registered, ``[{name, config, extensionPath}]`` in registration order."""
        return self._invoke("getMcpServers")

    def assert_active(self) -> None:
        """``runtime.assertActive()``: it raises with the stale message once the runtime was invalidated."""
        self._invoke("assertActive")

    def invalidate(self, message: str | None = None) -> None:
        """``runtime.invalidate(message?)``: later uses of the runtime fail with ``message``, or Pi's default one. A second call keeps the first message."""
        self._invoke("invalidate", message)


def create_extension_runtime(context: Any) -> ExtensionRuntime:
    """``createExtensionRuntime()``."""
    return ExtensionRuntime(context)

"""Pi's ``createCodemodeExtension``, ``createToolSearchExtension`` and ``createMcpExtension`` (extensions/codemode/index.ts, tool-search/index.ts, mcp/index.ts).

Pi's functions return an ExtensionFactory, a function its DefaultResourceLoader runs in its own process. A function cannot cross the process boundary, so these return a reference to the compiled-in extension of that name: pass
it in the ``extension_factories`` list of :class:`DefaultResourceLoader` and the host runs its own factory of that name, with the same options, for the extensions the loader loads.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Callable


@dataclass(frozen=True)
class ExtensionFactory:
    """A reference to a compiled-in extension of the host, which :class:`DefaultResourceLoader` takes in ``extension_factories``."""

    builtin: str
    options: dict[str, Any] = field(default_factory=dict)
    # The function options of create_mcp_extension, named to the host when a loader is built (mcp_options.py).
    functions: dict[str, Any] = field(default_factory=dict)

    def _wire(self, context: Any) -> dict[str, Any]:
        """The ``{builtin, options}`` entry of a loader's ``extensionFactories``: the data options and each function named to the host once per loader."""
        options = dict(self.options)
        if self.functions:
            from .mcp_options import _register

            _register(context, self.functions, options)
        return {"builtin": self.builtin, "options": options}


def create_codemode_extension(*, mode: str | None = None, inline_budget: float | None = None, models: bool | None = None) -> ExtensionFactory:
    """``createCodemodeExtension(options)``: the ``codemode`` tool, registered inactive. ``mode`` ("on" or "only") and ``inline_budget`` override the ``codemode`` settings; ``models=False`` hides the model catalog from scripts. ``None`` is Pi's undefined."""
    options: dict[str, Any] = {}
    for value, key in ((mode, "mode"), (inline_budget, "inlineBudget"), (models, "models")):
        if value is not None:
            options[key] = value
    return ExtensionFactory("codemode", options)


def create_tool_search_extension() -> ExtensionFactory:
    """``createToolSearchExtension()``: the ``tool_search`` tool, registered inactive."""
    return ExtensionFactory("tool-search", {})


def create_mcp_extension(
    *,
    load_config: Callable[[dict[str, Any]], dict[str, Any]] | None = None,
    create_transport: Callable[[dict[str, Any], str, Any], Any] | None = None,
    credentials: Any = None,
    log_path: str | None = None,
    open_url: Callable[[str], None] | None = None,
    update_config: Callable[[dict[str, Any], dict[str, Any]], None] | None = None,
    startup_wait_ms: float | None = None,
) -> ExtensionFactory:
    """``createMcpExtension(options)``: the MCP extension; every option is Pi's under its snake_case name and ``None`` is Pi's undefined. The functions run in this process (see mcp_options.py).

    ``load_config(ctx)`` receives ``{"cwd", "isProjectTrusted"}`` (the part of Pi's ExtensionContext it reads) and returns Pi's ``LoadedMcpConfig`` dict; an exception is the throw of Pi's ``loadConfig``. ``create_transport(entry, cwd, auth_provider)`` returns an :class:`McpTransport` (``auth_provider`` is an :class:`McpAuthProvider`, ``None`` for a server without OAuth). ``credentials`` is an :class:`McpOAuthCredentialStore`. ``open_url(url)`` opens the OAuth authorization URL (default: nothing). ``update_config(entry, patch)`` saves ``/mcp`` changes to the server's config file; an exception is the throw of Pi's ``updateConfig``."""
    options: dict[str, Any] = {}
    for value, key in ((log_path, "logPath"), (startup_wait_ms, "startupWaitMs")):
        if value is not None:
            options[key] = value
    functions = {name: value for name, value in (("load_config", load_config), ("create_transport", create_transport), ("credentials", credentials), ("open_url", open_url), ("update_config", update_config)) if value is not None}
    return ExtensionFactory("mcp", options, functions)

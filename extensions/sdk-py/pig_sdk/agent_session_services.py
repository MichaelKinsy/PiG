"""Pi's ``createAgentSessionServices`` and ``createAgentSessionFromServices`` (core/agent-session-services.ts).

The function builds the objects of one session cwd as Pi's does (``ModelRuntime.create``, ``SettingsManager.create``, ``DefaultResourceLoader`` and its ``reload``), then asks the host for the two steps that
need its state: the resolved paths, and the registrations and extension flag values of the extensions the loader loaded (agent-session-services.ts:138-139 and :157-193).

Async contract (Pi's promises, kept as blocking calls): both functions return once the host has finished; a failure raises. Pi's ``modelRuntimeSignal`` is the call itself: a cancelled request ends the creation, as it does
``ModelRuntime.create``.
"""

from __future__ import annotations

import os
from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Any

from ._graph import _GraphObject
from .child_session import AgentSession, CreateAgentSessionOptions, SessionStartEvent
from .model_runtime import ModelRuntime
from .resource_loader import DefaultResourceLoader
from .settings_manager import SettingsManager

if TYPE_CHECKING:
    from . import ToolDefinition


@dataclass(kw_only=True)
class CreateAgentSessionServicesOptions:
    """Pi's CreateAgentSessionServicesOptions. ``None`` is Pi's undefined."""

    cwd: str
    agent_dir: str | None = None
    settings_manager: SettingsManager | None = None
    model_runtime: ModelRuntime | None = None
    #: Pi's ``extensionFlagValues`` Map<string, boolean | string> as ``(name, value)`` pairs in command-line order (``True`` for a boolean flag, a string for a string flag), which is the order of the diagnostics.
    extension_flag_values: list[tuple[str, bool | str]] | None = None
    #: The DefaultResourceLoader options of Pi (keyword names of :class:`DefaultResourceLoader`) other than ``cwd``, ``agent_dir`` and ``settings_manager``, which are the services' own.
    resource_loader_options: dict[str, Any] | None = None
    #: Pi's ResourceLoaderReloadOptions: ``{"resolveProjectTrust": callable}``.
    resource_loader_reload_options: dict[str, Any] | None = None


@dataclass(kw_only=True)
class AgentSessionServices:
    """Pi's AgentSessionServices: the coherent cwd-bound services of one session cwd. The session itself is created separately (:func:`create_agent_session_from_services`)."""

    cwd: str
    agent_dir: str
    model_runtime: ModelRuntime
    settings_manager: SettingsManager
    resource_loader: DefaultResourceLoader
    #: Pi's AgentSessionRuntimeDiagnostic list: dicts with ``type`` ("info", "warning" or "error") and ``message``.
    diagnostics: list[dict[str, str]] = field(default_factory=list)


def create_agent_session_services(context: Any, options: CreateAgentSessionServicesOptions) -> AgentSessionServices:
    """``createAgentSessionServices(options)``: cwd-bound services plus the diagnostics found while creating them. It does not create an AgentSession."""
    paths = _GraphObject._static(context, "AgentSessionServices", "resolve", options.cwd, options.agent_dir)
    cwd, agent_dir = paths["cwd"], paths["agentDir"]
    model_runtime = options.model_runtime
    if model_runtime is None:
        model_runtime = ModelRuntime.create(context, auth_path=os.path.join(agent_dir, "auth.json"), models_path=os.path.join(agent_dir, "models.json"))
    settings_manager = options.settings_manager
    if settings_manager is None:
        settings_manager = SettingsManager.create(context, cwd, agent_dir)
    loader_options = dict(options.resource_loader_options or {})
    loader_options.update(cwd=cwd, agent_dir=agent_dir, settings_manager=settings_manager)
    resource_loader = DefaultResourceLoader(context, **loader_options)
    resource_loader.reload(options.resource_loader_reload_options)
    flags = [[name, value] for name, value in (options.extension_flag_values or [])]
    diagnostics = _GraphObject._static(
        context, "AgentSessionServices", "apply", model_runtime.handle, settings_manager.handle, resource_loader.handle, cwd, agent_dir, flags
    )
    return AgentSessionServices(
        cwd=cwd, agent_dir=agent_dir, model_runtime=model_runtime, settings_manager=settings_manager, resource_loader=resource_loader, diagnostics=list(diagnostics or [])
    )


@dataclass(kw_only=True)
class CreateAgentSessionFromServicesOptions:
    """Pi's CreateAgentSessionFromServicesOptions. ``None`` is Pi's undefined."""

    services: AgentSessionServices
    session_manager: Any
    session_start_event: SessionStartEvent | None = None
    model: dict[str, Any] | None = None
    thinking_level: str | None = None
    scoped_models: list[dict[str, Any]] | None = None
    tools: list[str] | None = None
    exclude_tools: list[str] | None = None
    no_tools: str | None = None
    custom_tools: list["ToolDefinition"] | None = None


def create_agent_session_from_services(context: Any, options: CreateAgentSessionFromServicesOptions) -> AgentSession:
    """``createAgentSessionFromServices(options)``: an AgentSession over previously created services, which keeps session creation separate so the caller can resolve the model, thinking level and tools against the target cwd first."""
    services = options.services
    return context.create_agent_session(
        CreateAgentSessionOptions(
            cwd=services.cwd,
            agent_dir=services.agent_dir,
            model_runtime=services.model_runtime,
            settings_manager=services.settings_manager,
            resource_loader=services.resource_loader,
            session_manager=options.session_manager,
            model=options.model,
            thinking_level=options.thinking_level,
            scoped_models=options.scoped_models,
            tools=options.tools,
            exclude_tools=options.exclude_tools,
            no_tools=options.no_tools,
            custom_tools=options.custom_tools,
            session_start_event=options.session_start_event,
        )
    )

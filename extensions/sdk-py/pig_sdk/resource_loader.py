"""Pi's ``DefaultResourceLoader`` class (core/resource-loader.ts): the skills, prompt templates, themes, context files, system prompt and extensions of a project and agent directory. The host owns the loader; a loader passed to ``create_agent_session`` is the very loader the child session reads. Call ``reload`` before reading, as Pi requires. Results are Pi's JSON shapes as dicts (a theme is its ``name``, ``sourcePath`` and ``sourceInfo``, an extension its ``path``, ``resolvedPath``, ``sourceInfo`` and ``tools``: the objects themselves hold functions that live in the host)."""
from __future__ import annotations

from typing import Any, Callable

from ._graph import _GraphObject, _callback_id
from .extension_factories import ExtensionFactory
from .extension_runtime import ExtensionRuntime

_UNSUPPORTED = (("extensions_override", "it returns live extension objects, which live in the host"),)


class LoadedExtension(dict):
    """One extension of a load: its ``path``, ``resolvedPath``, ``sourceInfo`` and ``tools`` as data (the extension object holds functions that live in the host), and the loader it was read from. It reads as that data; ``ExtensionRunner`` takes it."""

    _loader: str = ""


class LoadExtensionsResult(dict):
    """Pi's ``LoadExtensionsResult``: ``{extensions, errors, warnings}`` as data, with the ``runtime`` the extensions share. ``extensions`` holds ``LoadedExtension`` values."""

    runtime: ExtensionRuntime

    @property
    def extensions(self) -> list[LoadedExtension]:
        return self["extensions"]


class DefaultResourceLoader(_GraphObject):
    def __init__(
        self,
        context: Any,
        *,
        cwd: str,
        agent_dir: str,
        settings_manager: Any = None,
        # pig additive (D201): the runtime the loader loads its extensions against; Pi's loader takes none (its loadExtensions does).
        runtime: ExtensionRuntime | None = None,
        additional_extension_paths: list[str] | None = None,
        additional_skill_paths: list[str] | None = None,
        additional_prompt_template_paths: list[str] | None = None,
        additional_theme_paths: list[str] | None = None,
        extension_factories: list[Any] | None = None,
        no_extensions: bool = False,
        disabled_builtin_extensions: list[str] | None = None,
        no_skills: bool = False,
        no_prompt_templates: bool = False,
        no_themes: bool = False,
        no_context_files: bool = False,
        system_prompt: str | None = None,
        append_system_prompt: list[str] | None = None,
        extensions_override: Callable[[dict[str, Any]], dict[str, Any]] | None = None,
        skills_override: Callable[[dict[str, Any]], dict[str, Any]] | None = None,
        prompts_override: Callable[[dict[str, Any]], dict[str, Any]] | None = None,
        themes_override: Callable[[dict[str, Any]], dict[str, Any]] | None = None,
        agents_files_override: Callable[[dict[str, Any]], dict[str, Any]] | None = None,
        system_prompt_override: Callable[[str | None], str | None] | None = None,
        append_system_prompt_override: Callable[[list[str]], list[str]] | None = None,
    ) -> None:
        """``new DefaultResourceLoader(options)``; every option is Pi's under its snake_case name and ``None`` is Pi's undefined. ``extension_factories`` and ``extensions_override`` raise: they are not supported yet (an owner decision is pending, gap ``SdkSessionGraph`` in test/parity/sdk-exports.toml); use ``additional_extension_paths``, or ``create_agent_session`` custom tools."""
        given = {"extensions_override": extensions_override}
        for name, why in _UNSUPPORTED:
            if given[name]:
                raise RuntimeError(f"DefaultResourceLoader option {name} is not supported by the Python SDK: {why} (decision pending, gap SdkSessionGraph)")
        for factory in extension_factories or []:
            if not isinstance(factory, ExtensionFactory):
                raise RuntimeError(
                    "DefaultResourceLoader option extension_factories takes only the references create_codemode_extension, create_tool_search_extension and create_mcp_extension return: an inline factory runs in this process "
                    "but the child session's extensions load in the host (decision pending, gap SdkSessionGraph); use additional_extension_paths, or create_agent_session custom tools"
                )
        wire = {
            "cwd": cwd, "agentDir": agent_dir,
            "additionalExtensionPaths": additional_extension_paths, "additionalSkillPaths": additional_skill_paths,
            "additionalPromptTemplatePaths": additional_prompt_template_paths, "additionalThemePaths": additional_theme_paths,
            "noExtensions": no_extensions, "noSkills": no_skills, "noPromptTemplates": no_prompt_templates, "noThemes": no_themes, "noContextFiles": no_context_files,
            "disabledBuiltinExtensions": disabled_builtin_extensions, "systemPrompt": system_prompt, "appendSystemPrompt": append_system_prompt,
            "skillsOverride": _callback_id(context, skills_override), "promptsOverride": _callback_id(context, prompts_override),
            "themesOverride": _callback_id(context, themes_override), "agentsFilesOverride": _callback_id(context, agents_files_override),
            "systemPromptOverride": _callback_id(context, system_prompt_override), "appendSystemPromptOverride": _callback_id(context, append_system_prompt_override),
        }
        if extension_factories:
            wire["extensionFactories"] = [factory._wire(context) for factory in extension_factories]
        if settings_manager is not None:
            wire["settingsManager"] = settings_manager.handle
        if runtime is not None:
            wire["runtime"] = runtime.handle
        super().__init__(context, "DefaultResourceLoader", "new", wire)

    def _extensions_result(self, data: dict[str, Any]) -> LoadExtensionsResult:
        result = LoadExtensionsResult(data)
        extensions = []
        for extension in data.get("extensions") or []:
            loaded = LoadedExtension(extension)
            loaded._loader = self.handle
            extensions.append(loaded)
        result["extensions"] = extensions
        result.runtime = ExtensionRuntime._of_loader(self._context, self.handle)
        return result

    def get_extensions(self) -> LoadExtensionsResult:
        """The extensions of the last ``reload``, as data, with the runtime they share (``result.runtime``)."""
        return self._extensions_result(self._invoke("getExtensions"))

    def get_skills(self) -> dict[str, Any]:
        """``{"skills", "diagnostics"}`` of the last ``reload``."""
        return self._invoke("getSkills")

    def get_prompts(self) -> dict[str, Any]:
        """``{"prompts", "diagnostics"}`` of the last ``reload``."""
        return self._invoke("getPrompts")

    def get_themes(self) -> dict[str, Any]:
        """``{"themes", "diagnostics"}`` of the last ``reload``."""
        return self._invoke("getThemes")

    def get_agents_files(self) -> dict[str, Any]:
        """``{"agentsFiles": [{"path", "content"}]}``: the context files of the last ``reload``."""
        return self._invoke("getAgentsFiles")

    def get_system_prompt(self) -> str | None:
        return self._invoke("getSystemPrompt")

    def get_system_prompt_source(self) -> dict[str, Any] | None:
        """``{"path"}`` of the file the system prompt came from, ``None`` when it did not come from one."""
        return self._invoke("getSystemPromptSource")

    def get_append_system_prompt(self) -> list[str]:
        return self._invoke("getAppendSystemPrompt")

    def get_append_system_prompt_sources(self) -> list[dict[str, Any]]:
        return self._invoke("getAppendSystemPromptSources")

    def extend_resources(self, paths: dict[str, Any]) -> None:
        """Add the skills, prompt templates and themes extensions contributed: ``{"skillPaths", "promptPaths", "themePaths"}`` lists of ``{"path", "metadata": {"source", "scope", "origin", "baseDir"?}}``."""
        self._invoke("extendResources", paths)

    def load_project_trust_extensions(self) -> LoadExtensionsResult:
        """The bootstrap extension set, loaded with project settings untrusted, that project trust is decided with."""
        return self._extensions_result(self._invoke_async("loadProjectTrustExtensions"))

    def reload(self, options: dict[str, Any] | None = None) -> None:
        """Rediscover every resource. ``options["resolveProjectTrust"]`` is a callable taking the bootstrap extension set and returning whether the project is trusted."""
        wire: dict[str, Any] = {}
        if options and options.get("resolveProjectTrust") is not None:
            wire["resolveProjectTrust"] = _callback_id(self._context, options["resolveProjectTrust"])
        self._invoke_async("reload", wire)

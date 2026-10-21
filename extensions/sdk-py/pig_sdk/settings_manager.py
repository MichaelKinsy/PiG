"""Pi's ``SettingsManager`` class (core/settings-manager.ts): the global and project settings an extension reads and writes, or in-memory settings it builds for a child session. The host owns the object; a SettingsManager passed to ``create_agent_session`` is the very settings object the child session reads. Every method is Pi's under its snake_case name; ``None`` is Pi's undefined."""
from __future__ import annotations

import queue
import threading
from typing import Any

from ._graph import _GraphObject, _callback_id


class _StorageCallback:
    """The function the host runs for every ``with_lock`` of a storage: ``("begin", scope)`` starts the storage's ``with_lock`` and answers its current text, and ``("finish", result)`` gives its ``fn`` the host's result and waits for ``with_lock`` to return."""

    def __init__(self, storage: Any) -> None:
        self._storage = storage
        self._pending: tuple[queue.Queue, queue.Queue, queue.Queue] | None = None
        self._lock = threading.Lock()

    def __call__(self, op: str, argument: Any = None) -> Any:
        if op == "begin":
            current: queue.Queue = queue.Queue()
            finish: queue.Queue = queue.Queue()
            done: queue.Queue = queue.Queue()

            def fn(text: str | None) -> str | None:
                current.put(text)
                result = finish.get()
                if result.get("error"):
                    raise RuntimeError(result["error"])
                return result["text"] if result.get("write") else None

            def run() -> None:
                try:
                    self._storage.with_lock(argument, fn)
                    done.put(None)
                except BaseException as error:  # noqa: BLE001 - the host reports it as the storage's failure
                    done.put(error)

            threading.Thread(target=run, daemon=True).start()
            while True:
                try:
                    text = current.get(timeout=0.01)
                except queue.Empty:
                    if not done.empty():
                        error = done.get()
                        raise error if error is not None else RuntimeError("settings storage: with_lock returned without calling fn")
                    continue
                with self._lock:
                    self._pending = (current, finish, done)
                return {"current": text}
        if op == "finish":
            with self._lock:
                pending, self._pending = self._pending, None
            if pending is None:
                raise RuntimeError("settings storage callback: finish without begin")
            _, finish, done = pending
            finish.put(argument or {})
            error = done.get()
            if error is not None:
                raise error
            return None
        raise RuntimeError(f"settings storage callback: unknown operation {op!r}")


class SettingsManager(_GraphObject):
    @classmethod
    def create(cls, context: Any, cwd: str, agent_dir: str | None = None, options: dict[str, Any] | None = None) -> "SettingsManager":
        """``SettingsManager.create(cwd, agentDir = getAgentDir(), options = {})``: settings loaded from the files of ``agent_dir`` (global) and ``cwd`` (project). ``options`` is Pi's SettingsManagerCreateOptions (``projectTrusted``). ``None`` for ``agent_dir`` is the host's agent directory."""
        return cls(context, "SettingsManager", "create", cwd, agent_dir, options)

    @classmethod
    def from_storage(cls, context: Any, storage: Any, options: dict[str, Any] | None = None) -> "SettingsManager":
        """``SettingsManager.fromStorage(storage, options = {})``: settings kept by ``storage``, an object of this process with ``with_lock(scope, fn)`` that the host calls back whenever the manager reads or writes a scope. ``with_lock`` runs ``fn(current)`` on the scope's current text (``None`` when there is none) while it holds its lock; ``fn`` returns the text to store or ``None``, and raises for a settings file that does not parse, which ``with_lock`` lets propagate."""
        return cls(context, "SettingsManager", "fromStorage", {"withLock": _callback_id(context, _StorageCallback(storage))}, options)

    @classmethod
    def in_memory(cls, context: Any, settings: dict[str, Any] | None = None, options: dict[str, Any] | None = None) -> "SettingsManager":
        """``SettingsManager.inMemory(settings = {}, options = {})``: settings that never touch a file."""
        return cls(context, "SettingsManager", "inMemory", settings if settings is not None else {}, options)

    def drain_errors(self) -> list[dict[str, Any]]:
        """The errors loading or writing the settings files since the last call, as Pi's SettingsError: ``scope``, ``path`` (when a file is behind it) and ``error`` (the message)."""
        return self._invoke("drainErrors") or []

    def get_settings(self) -> dict[str, Any]:
        return self._invoke("getSettings")

    def get_global_settings(self) -> dict[str, Any]:
        return self._invoke("getGlobalSettings")

    def get_project_settings(self) -> dict[str, Any]:
        return self._invoke("getProjectSettings")

    def is_project_trusted(self) -> bool:
        return self._invoke("isProjectTrusted")

    def set_project_trusted(self, trusted: bool) -> None:
        self._invoke("setProjectTrusted", trusted)

    def reload(self) -> None:
        self._invoke_async("reload")

    def apply_overrides(self, overrides: dict[str, Any]) -> None:
        self._invoke("applyOverrides", overrides)

    def flush(self) -> None:
        self._invoke_async("flush")

    def get_last_changelog_version(self) -> str | None:
        return self._invoke("getLastChangelogVersion")

    def set_last_changelog_version(self, version: str) -> None:
        self._invoke("setLastChangelogVersion", version)

    def get_session_dir(self) -> str | None:
        return self._invoke("getSessionDir")

    def get_default_provider(self) -> str | None:
        return self._invoke("getDefaultProvider")

    def get_default_model(self) -> str | None:
        return self._invoke("getDefaultModel")

    def set_default_provider(self, provider: str) -> None:
        self._invoke("setDefaultProvider", provider)

    def set_default_model(self, model_id: str) -> None:
        self._invoke("setDefaultModel", model_id)

    def set_default_model_and_provider(self, provider: str, model_id: str) -> None:
        self._invoke("setDefaultModelAndProvider", provider, model_id)

    def get_steering_mode(self) -> str:
        return self._invoke("getSteeringMode")

    def set_steering_mode(self, mode: str) -> None:
        self._invoke("setSteeringMode", mode)

    def get_follow_up_mode(self) -> str:
        return self._invoke("getFollowUpMode")

    def set_follow_up_mode(self, mode: str) -> None:
        self._invoke("setFollowUpMode", mode)

    def get_theme_setting(self) -> str | None:
        return self._invoke("getThemeSetting")

    def get_theme(self) -> str | None:
        return self._invoke("getTheme")

    def set_theme(self, theme: str) -> None:
        self._invoke("setTheme", theme)

    def get_default_thinking_level(self) -> str | None:
        return self._invoke("getDefaultThinkingLevel")

    def set_default_thinking_level(self, level: str) -> None:
        self._invoke("setDefaultThinkingLevel", level)

    def get_model_thinking_level(self, provider: str, model_id: str) -> str | None:
        return self._invoke("getModelThinkingLevel", provider, model_id)

    def get_all_model_thinking_levels(self) -> dict[str, Any]:
        return self._invoke("getAllModelThinkingLevels")

    def set_model_thinking_level(self, provider: str, model_id: str, level: str) -> None:
        self._invoke("setModelThinkingLevel", provider, model_id, level)

    def remove_model_thinking_level(self, provider: str, model_id: str) -> None:
        self._invoke("removeModelThinkingLevel", provider, model_id)

    def get_transport(self) -> str:
        return self._invoke("getTransport")

    def set_transport(self, transport: str) -> None:
        self._invoke("setTransport", transport)

    def get_session_share_destination(self) -> str:
        return self._invoke("getSessionShareDestination")

    def set_session_share_destination(self, destination: str) -> None:
        self._invoke("setSessionShareDestination", destination)

    def get_compaction_enabled(self) -> bool:
        return self._invoke("getCompactionEnabled")

    def set_compaction_enabled(self, enabled: bool) -> None:
        self._invoke("setCompactionEnabled", enabled)

    def get_compaction_reserve_tokens(self, model: dict[str, Any] | None = None) -> int:
        return self._invoke("getCompactionReserveTokens", model)

    def get_compaction_keep_recent_tokens(self, model: dict[str, Any] | None = None) -> int:
        return self._invoke("getCompactionKeepRecentTokens", model)

    def get_compaction_settings(self, model: dict[str, Any] | None = None) -> dict[str, Any]:
        return self._invoke("getCompactionSettings", model)

    def get_branch_summary_settings(self) -> dict[str, Any]:
        return self._invoke("getBranchSummarySettings")

    def get_branch_summary_skip_prompt(self) -> bool:
        return self._invoke("getBranchSummarySkipPrompt")

    def get_retry_enabled(self) -> bool:
        return self._invoke("getRetryEnabled")

    def set_retry_enabled(self, enabled: bool) -> None:
        self._invoke("setRetryEnabled", enabled)

    def get_retry_settings(self) -> dict[str, Any]:
        return self._invoke("getRetrySettings")

    def get_http_idle_timeout_ms(self) -> float:
        return self._invoke("getHttpIdleTimeoutMs")

    def set_http_idle_timeout_ms(self, timeout_ms: float) -> None:
        self._invoke("setHttpIdleTimeoutMs", timeout_ms)

    def get_cache_warming_mode(self) -> str:
        return self._invoke("getCacheWarmingMode")

    def set_cache_warming_mode(self, mode: str) -> None:
        self._invoke("setCacheWarmingMode", mode)

    def get_provider_retry_settings(self) -> dict[str, Any]:
        return self._invoke("getProviderRetrySettings")

    def get_web_socket_connect_timeout_ms(self) -> float | None:
        return self._invoke("getWebSocketConnectTimeoutMs")

    def get_hide_thinking_block(self) -> bool:
        return self._invoke("getHideThinkingBlock")

    def get_show_cache_miss_notices(self) -> bool:
        return self._invoke("getShowCacheMissNotices")

    def get_external_editor_command(self) -> str:
        return self._invoke("getExternalEditorCommand")

    def set_hide_thinking_block(self, hide: bool) -> None:
        self._invoke("setHideThinkingBlock", hide)

    def set_show_cache_miss_notices(self, show: bool) -> None:
        self._invoke("setShowCacheMissNotices", show)

    def get_shell_path(self) -> str | None:
        return self._invoke("getShellPath")

    def set_shell_path(self, path: str | None = None) -> None:
        self._invoke("setShellPath", path)

    def get_quiet_startup(self) -> Any:
        return self._invoke("getQuietStartup")

    def set_quiet_startup(self, quiet: Any) -> None:
        self._invoke("setQuietStartup", quiet)

    def get_default_project_trust(self) -> str:
        return self._invoke("getDefaultProjectTrust")

    def set_default_project_trust(self, default_project_trust: str) -> None:
        self._invoke("setDefaultProjectTrust", default_project_trust)

    def get_shell_command_prefix(self) -> str | None:
        return self._invoke("getShellCommandPrefix")

    def set_shell_command_prefix(self, prefix: str | None = None) -> None:
        self._invoke("setShellCommandPrefix", prefix)

    def get_npm_command(self) -> list[str] | None:
        return self._invoke("getNpmCommand")

    def set_npm_command(self, command: list[str] | None = None) -> None:
        self._invoke("setNpmCommand", command)

    def get_collapse_changelog(self) -> bool:
        return self._invoke("getCollapseChangelog")

    def set_collapse_changelog(self, collapse: bool) -> None:
        self._invoke("setCollapseChangelog", collapse)

    def get_enable_install_telemetry(self) -> bool:
        return self._invoke("getEnableInstallTelemetry")

    def set_enable_install_telemetry(self, enabled: bool) -> None:
        self._invoke("setEnableInstallTelemetry", enabled)

    def get_enable_analytics(self) -> bool:
        return self._invoke("getEnableAnalytics")

    def get_tracking_id(self) -> str | None:
        return self._invoke("getTrackingId")

    def set_enable_analytics(self, enabled: bool) -> None:
        self._invoke("setEnableAnalytics", enabled)

    def get_or_create_device_id(self) -> str:
        return self._invoke("getOrCreateDeviceId")

    def get_packages(self) -> list[Any]:
        return self._invoke("getPackages")

    def set_packages(self, packages: list[Any]) -> None:
        self._invoke("setPackages", packages)

    def set_project_packages(self, packages: list[Any]) -> None:
        self._invoke("setProjectPackages", packages)

    def get_extension_paths(self) -> list[str]:
        return self._invoke("getExtensionPaths")

    def set_extension_paths(self, paths: list[str]) -> None:
        self._invoke("setExtensionPaths", paths)

    def set_project_extension_paths(self, paths: list[str]) -> None:
        self._invoke("setProjectExtensionPaths", paths)

    def get_skill_paths(self) -> list[str]:
        return self._invoke("getSkillPaths")

    def set_skill_paths(self, paths: list[str]) -> None:
        self._invoke("setSkillPaths", paths)

    def set_project_skill_paths(self, paths: list[str]) -> None:
        self._invoke("setProjectSkillPaths", paths)

    def get_prompt_template_paths(self) -> list[str]:
        return self._invoke("getPromptTemplatePaths")

    def set_prompt_template_paths(self, paths: list[str]) -> None:
        self._invoke("setPromptTemplatePaths", paths)

    def set_project_prompt_template_paths(self, paths: list[str]) -> None:
        self._invoke("setProjectPromptTemplatePaths", paths)

    def get_theme_paths(self) -> list[str]:
        return self._invoke("getThemePaths")

    def set_theme_paths(self, paths: list[str]) -> None:
        self._invoke("setThemePaths", paths)

    def set_project_theme_paths(self, paths: list[str]) -> None:
        self._invoke("setProjectThemePaths", paths)

    def get_enable_skill_commands(self) -> bool:
        return self._invoke("getEnableSkillCommands")

    def set_enable_skill_commands(self, enabled: bool) -> None:
        self._invoke("setEnableSkillCommands", enabled)

    def get_thinking_budgets(self) -> dict[str, Any]:
        return self._invoke("getThinkingBudgets")

    def get_terminal_capability_overrides(self) -> dict[str, Any]:
        return self._invoke("getTerminalCapabilityOverrides")

    def get_show_images(self) -> bool:
        return self._invoke("getShowImages")

    def set_show_images(self, show: bool) -> None:
        self._invoke("setShowImages", show)

    def get_image_width_cells(self) -> int:
        return self._invoke("getImageWidthCells")

    def set_image_width_cells(self, width: int) -> None:
        self._invoke("setImageWidthCells", width)

    def get_clear_on_shrink(self) -> bool:
        return self._invoke("getClearOnShrink")

    def set_clear_on_shrink(self, enabled: bool) -> None:
        self._invoke("setClearOnShrink", enabled)

    def get_show_terminal_progress(self) -> bool:
        return self._invoke("getShowTerminalProgress")

    def set_show_terminal_progress(self, enabled: bool) -> None:
        self._invoke("setShowTerminalProgress", enabled)

    def get_tui_mode(self) -> str:
        return self._invoke("getTuiMode")

    def set_tui_mode(self, mode: str) -> None:
        self._invoke("setTuiMode", mode)

    def get_fullscreen_exit_output(self) -> str:
        return self._invoke("getFullscreenExitOutput")

    def set_fullscreen_exit_output(self, output: str) -> None:
        self._invoke("setFullscreenExitOutput", output)

    def get_fullscreen_scrollbar(self) -> str:
        return self._invoke("getFullscreenScrollbar")

    def set_fullscreen_scrollbar(self, mode: str) -> None:
        self._invoke("setFullscreenScrollbar", mode)

    def get_fullscreen_copy_on_select(self) -> bool:
        return self._invoke("getFullscreenCopyOnSelect")

    def set_fullscreen_copy_on_select(self, enabled: bool) -> None:
        self._invoke("setFullscreenCopyOnSelect", enabled)

    def get_fullscreen_wheel_scroll_lines(self) -> Any:
        return self._invoke("getFullscreenWheelScrollLines")

    def set_fullscreen_wheel_scroll_lines(self, lines: Any) -> None:
        self._invoke("setFullscreenWheelScrollLines", lines)

    def get_image_auto_resize(self) -> bool:
        return self._invoke("getImageAutoResize")

    def set_image_auto_resize(self, enabled: bool) -> None:
        self._invoke("setImageAutoResize", enabled)

    def get_block_images(self) -> bool:
        return self._invoke("getBlockImages")

    def set_block_images(self, blocked: bool) -> None:
        self._invoke("setBlockImages", blocked)

    def get_enabled_models(self) -> list[str] | None:
        return self._invoke("getEnabledModels")

    def get_default_tools(self) -> list[str] | None:
        return self._invoke("getDefaultTools")

    def set_enabled_models(self, patterns: list[str] | None = None) -> None:
        self._invoke("setEnabledModels", patterns)

    def get_double_escape_action(self) -> str:
        return self._invoke("getDoubleEscapeAction")

    def set_double_escape_action(self, action: str) -> None:
        self._invoke("setDoubleEscapeAction", action)

    def get_tree_filter_mode(self) -> str:
        return self._invoke("getTreeFilterMode")

    def set_tree_filter_mode(self, mode: str) -> None:
        self._invoke("setTreeFilterMode", mode)

    def get_show_hardware_cursor(self) -> bool:
        return self._invoke("getShowHardwareCursor")

    def set_show_hardware_cursor(self, enabled: bool) -> None:
        self._invoke("setShowHardwareCursor", enabled)

    def get_editor_padding_x(self) -> int:
        return self._invoke("getEditorPaddingX")

    def set_editor_padding_x(self, padding: int) -> None:
        self._invoke("setEditorPaddingX", padding)

    def get_output_pad(self) -> int:
        return self._invoke("getOutputPad")

    def set_output_pad(self, padding: int) -> None:
        self._invoke("setOutputPad", padding)

    def get_autocomplete_max_visible(self) -> int:
        return self._invoke("getAutocompleteMaxVisible")

    def set_autocomplete_max_visible(self, max_visible: int) -> None:
        self._invoke("setAutocompleteMaxVisible", max_visible)

    def get_code_block_indent(self) -> str:
        return self._invoke("getCodeBlockIndent")

    def get_mermaid_rendering_mode(self) -> str:
        return self._invoke("getMermaidRenderingMode")

    def set_mermaid_rendering_mode(self, mode: str) -> None:
        self._invoke("setMermaidRenderingMode", mode)

    def get_warnings(self) -> dict[str, Any]:
        return self._invoke("getWarnings")

    def set_warnings(self, warnings: dict[str, Any]) -> None:
        self._invoke("setWarnings", warnings)

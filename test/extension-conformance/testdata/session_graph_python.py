# The Python fixture of the session graph conformance rows (session_graph_test.go): the script of testfixture/sessiongraph (Go), testdata/session-graph.mjs (Node, Pi's own classes) and testdata/session-graph-rust.
import json
import os

import pig_sdk
from pig_sdk import ProjectTrustStore, SessionManager, SettingsManager, has_trust_requiring_project_resources


def shape(entries):
    index = {entry["id"]: i for i, entry in enumerate(entries)}
    return [f'{entry["type"]}<{index.get(entry.get("parentId"), -1) if isinstance(entry.get("parentId"), str) else -1}' for entry in entries]


def failure(fn, contains):
    try:
        fn()
    except Exception as error:
        return f"error: {contains}" if contains in str(error) else f"error: {error}"
    return "no error"


def infos(listing):
    out = [
        {
            "id": info["id"], "messageCount": info["messageCount"], "name": info.get("name"), "firstMessage": info["firstMessage"], "cwd": info["cwd"],
            "createdRecent": int(info["created"].timestamp() * 1000) > 1767225600000, "modifiedMs": round(info["modified"].timestamp() * 1000), "fileInDir": info["path"] != "",
        }
        for info in listing
    ]
    return sorted(out, key=lambda info: info["id"])


ASSISTANT = {
    "role": "assistant", "content": [{"type": "text", "text": "hi"}], "api": "test", "provider": "p", "model": "m",
    "usage": {"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
    "stopReason": "stop", "timestamp": 2,
}


def script(ctx, args):
    cwd, directory, other_dir, junk = args["cwd"], args["dir"], args["otherDir"], args["junk"]
    r = {}
    fixed = "fixed-session-1"
    m = SessionManager.create(ctx, cwd, directory, {"id": fixed})
    file = m.get_session_file()
    r["created"] = {
        "id": m.get_session_id(), "persisted": m.is_persisted(), "dir": m.get_session_dir(), "fileSuffix": file.endswith("_fixed-session-1.jsonl"),
        "fileInDir": os.path.dirname(file) == directory, "cwd": m.get_cwd(), "defaultDir": m.uses_default_session_dir(), "entries": m.get_entry_count(), "leaf": m.get_leaf_id(),
    }
    first = m.append_message({"role": "user", "content": "hello", "timestamp": 1})
    m.append_message(ASSISTANT)
    m.append_session_info("graph")
    m.append_label_change(first, "start")
    m.append_custom_entry("note", {"a": 1, "b": 2})
    m.append_thinking_level_change("high")
    last = m.append_model_change("p", "m")
    entries = m.get_entries()
    header = m.get_header()
    custom = next((entry for entry in entries if entry["type"] == "custom"), {})
    r["log"] = {
        "name": m.get_session_name(), "label": m.get_label(first), "count": m.get_entry_count(), "shape": shape(entries), "leafIsLast": m.get_leaf_id() == last,
        "entryType": m.get_entry(first)["type"], "missing": m.get_entry("nope") is None,
        "headerType": header["type"], "headerVersion": header["version"], "headerId": header["id"], "headerCwd": header["cwd"],
        "customType": custom.get("customType"), "customData": custom.get("data"),
    }
    built = m.build_session_context()
    r["context"] = {"messages": len(built["messages"]), "thinkingLevel": built["thinkingLevel"], "model": built["model"]}
    tree = m.get_tree()
    r["tree"] = {"roots": len(tree), "firstChildren": len(tree[0]["children"])}

    msg_id = m.append_custom_message_entry("note-msg", "body", True, {"k": 1})
    usage = m.append_usage("test", "p", "m", {"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}})
    r["extra"] = {"msgType": m.get_entry(msg_id)["type"], "msgCustomType": m.get_entry(msg_id)["customType"], "usageType": usage["type"], "usageKind": usage["kind"]}

    m.branch(first)
    alt = m.append_message({"role": "user", "content": "alt", "timestamp": 3})
    r["branch"] = {"shape": shape(m.get_branch()), "children": len(m.get_children(first)), "leafIsAlt": m.get_leaf_id() == alt}
    m.reset_leaf()
    r["reset"] = {"leaf": m.get_leaf_id()}
    summary = m.branch_with_summary(first, "summary text")
    summary_entry = m.get_entry(summary)
    r["summary"] = {"type": summary_entry["type"], "parentIsFirst": summary_entry["parentId"] == first, "text": summary_entry["summary"], "fromId": summary_entry["fromId"] == first}
    branched = m.create_branched_session(summary)
    r["branched"] = {
        "isFile": branched is not None and branched.endswith(".jsonl"), "inDir": branched is not None and os.path.dirname(branched) == directory,
        "newFile": branched is not None and branched != file, "idChanged": m.get_session_id() != fixed, "count": m.get_entry_count(), "shape": shape(m.get_entries()),
    }

    opened = SessionManager.open(ctx, file)
    r["opened"] = {
        "id": opened.get_session_id(), "count": opened.get_entry_count(), "cwd": opened.get_cwd(), "headerCwd": opened.get_header()["cwd"],
        "dirIsFileDir": opened.get_session_dir() == directory, "file": opened.get_session_file() == file, "name": opened.get_session_name(),
    }
    overridden = SessionManager.open(ctx, file, None, other_dir)
    r["override"] = {"cwd": overridden.get_cwd(), "id": overridden.get_session_id()}
    recent = SessionManager.continue_recent(ctx, cwd, directory)
    recent_file = recent.get_session_file()
    r["recent"] = {"inDir": recent_file is not None and os.path.dirname(recent_file) == directory, "hasEntries": recent.get_entry_count() > 0, "cwd": recent.get_cwd()}

    mem = SessionManager.in_memory(ctx, cwd, {"id": "mem-1"})
    before = mem.get_leaf_id()
    mem.append_message({"role": "user", "content": "x", "timestamp": 1})
    r["memory"] = {
        "id": mem.get_session_id(), "persisted": mem.is_persisted(), "file": mem.get_session_file(), "dir": mem.get_session_dir(),
        "defaultDir": mem.uses_default_session_dir(), "leafBefore": before, "count": mem.get_entry_count(), "cwd": mem.get_cwd(),
    }
    seeded = SessionManager.in_memory(ctx, cwd, None, [{"type": "message", "id": "e1", "parentId": None, "timestamp": "2026-01-01T00:00:00.000Z", "message": {"role": "user", "content": "seed", "timestamp": 1}}])
    r["seeded"] = {"count": seeded.get_entry_count(), "leaf": seeded.get_leaf_id(), "hasId": seeded.get_session_id() != ""}

    fork_dir = os.path.join(other_dir, "sessions")
    forked = SessionManager.fork_from(ctx, file, other_dir, fork_dir, {"id": "forked-1"})
    r["fork"] = {
        "id": forked.get_session_id(), "count": forked.get_entry_count(), "parent": forked.get_header().get("parentSession") == file, "cwd": forked.get_cwd() == other_dir,
        "inDir": os.path.dirname(forked.get_session_file()) == fork_dir, "shape": shape(forked.get_entries()),
    }

    r["list"] = infos(SessionManager.list(ctx, cwd, directory))
    r["listAll"] = infos(SessionManager.list_all(ctx, directory))
    r["listFork"] = infos(SessionManager.list(ctx, other_dir, fork_dir))
    found = SessionManager.find_by_id(ctx, cwd, fixed, directory)
    r["find"] = {"found": found == file, "missing": SessionManager.find_by_id(ctx, cwd, "nope", directory) is None}

    r["errors"] = {
        "badId": failure(lambda: SessionManager.create(ctx, cwd, directory, {"id": "bad id!"}), "Session id must be"),
        "notSession": failure(lambda: SessionManager.open(ctx, junk), "not a valid"),
        "noFork": failure(lambda: SessionManager.fork_from(ctx, os.path.join(directory, "missing.jsonl"), cwd, fork_dir), "Cannot fork"),
    }
    return r


def session_manager_tool(ctx, params):
    return json.dumps(script(ctx, params))


def read_json(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def read_all(s):
    return {
        "getDefaultProvider": s.get_default_provider(),
        "getDefaultModel": s.get_default_model(),
        "getDefaultThinkingLevel": s.get_default_thinking_level(),
        "getTheme": s.get_theme(),
        "getThemeSetting": s.get_theme_setting(),
        "getSteeringMode": s.get_steering_mode(),
        "getFollowUpMode": s.get_follow_up_mode(),
        "getTransport": s.get_transport(),
        "getSessionShareDestination": s.get_session_share_destination(),
        "getCompactionEnabled": s.get_compaction_enabled(),
        "getBranchSummarySkipPrompt": s.get_branch_summary_skip_prompt(),
        "getRetryEnabled": s.get_retry_enabled(),
        "getHttpIdleTimeoutMs": s.get_http_idle_timeout_ms(),
        "getCacheWarmingMode": s.get_cache_warming_mode(),
        "getWebSocketConnectTimeoutMs": s.get_web_socket_connect_timeout_ms(),
        "getHideThinkingBlock": s.get_hide_thinking_block(),
        "getShowCacheMissNotices": s.get_show_cache_miss_notices(),
        "getExternalEditorCommand": s.get_external_editor_command(),
        "getShellPath": s.get_shell_path(),
        "getQuietStartup": s.get_quiet_startup(),
        "getDefaultProjectTrust": s.get_default_project_trust(),
        "getShellCommandPrefix": s.get_shell_command_prefix(),
        "getNpmCommand": s.get_npm_command(),
        "getCollapseChangelog": s.get_collapse_changelog(),
        "getEnableInstallTelemetry": s.get_enable_install_telemetry(),
        "getEnableAnalytics": s.get_enable_analytics(),
        "getPackages": s.get_packages(),
        "getExtensionPaths": s.get_extension_paths(),
        "getSkillPaths": s.get_skill_paths(),
        "getPromptTemplatePaths": s.get_prompt_template_paths(),
        "getThemePaths": s.get_theme_paths(),
        "getEnableSkillCommands": s.get_enable_skill_commands(),
        "getThinkingBudgets": s.get_thinking_budgets(),
        "getTerminalCapabilityOverrides": s.get_terminal_capability_overrides(),
        "getShowImages": s.get_show_images(),
        "getImageWidthCells": s.get_image_width_cells(),
        "getClearOnShrink": s.get_clear_on_shrink(),
        "getShowTerminalProgress": s.get_show_terminal_progress(),
        "getTuiMode": s.get_tui_mode(),
        "getFullscreenExitOutput": s.get_fullscreen_exit_output(),
        "getFullscreenScrollbar": s.get_fullscreen_scrollbar(),
        "getFullscreenCopyOnSelect": s.get_fullscreen_copy_on_select(),
        "getFullscreenWheelScrollLines": s.get_fullscreen_wheel_scroll_lines(),
        "getImageAutoResize": s.get_image_auto_resize(),
        "getBlockImages": s.get_block_images(),
        "getEnabledModels": s.get_enabled_models(),
        "getDefaultTools": s.get_default_tools(),
        "getDoubleEscapeAction": s.get_double_escape_action(),
        "getTreeFilterMode": s.get_tree_filter_mode(),
        "getShowHardwareCursor": s.get_show_hardware_cursor(),
        "getEditorPaddingX": s.get_editor_padding_x(),
        "getOutputPad": s.get_output_pad(),
        "getAutocompleteMaxVisible": s.get_autocomplete_max_visible(),
        "getCodeBlockIndent": s.get_code_block_indent(),
        "getMermaidRenderingMode": s.get_mermaid_rendering_mode(),
        "getWarnings": s.get_warnings(),
        "getAllModelThinkingLevels": s.get_all_model_thinking_levels(),
        "getBranchSummarySettings": s.get_branch_summary_settings(),
        "getRetrySettings": s.get_retry_settings(),
        "getProviderRetrySettings": s.get_provider_retry_settings(),
        "getLastChangelogVersion": s.get_last_changelog_version(),
        "getSessionDir": s.get_session_dir(),
        "getSettings": s.get_settings(),
        "getGlobalSettings": s.get_global_settings(),
        "getProjectSettings": s.get_project_settings(),
        "isProjectTrusted": s.is_project_trusted(),
    }


def settings_script(ctx, args):
    cwd, agent_dir, config_dir, broken_cwd = args["cwd"], args["agentDir"], args["configDir"], args["brokenCwd"]
    r = {}
    s = SettingsManager.create(ctx, cwd, agent_dir)
    r["initial"] = read_all(s)
    r["modelBudget"] = {
        "reserve": s.get_compaction_reserve_tokens({"provider": "p", "id": "m"}), "reserveNone": s.get_compaction_reserve_tokens(), "keep": s.get_compaction_keep_recent_tokens(),
        "settings": s.get_compaction_settings({"provider": "p", "id": "m"}), "thinking": s.get_model_thinking_level("p", "m"),
    }
    s.set_default_provider("openai")
    s.set_default_model("gpt")
    s.set_default_thinking_level("high")
    s.set_theme("light")
    s.set_steering_mode("one-at-a-time")
    s.set_follow_up_mode("one-at-a-time")
    s.set_compaction_enabled(True)
    s.set_retry_enabled(False)
    s.set_packages(["npm:a", {"source": "npm:b", "extensions": ["x"]}])
    s.set_extension_paths(["a", "b"])
    s.set_skill_paths(["s1"])
    s.set_prompt_template_paths(["p1"])
    s.set_theme_paths(["t1"])
    s.set_enabled_models(["m1", "m2"])
    s.set_quiet_startup("header")
    s.set_shell_path("/bin/sh")
    s.set_shell_command_prefix("set -e")
    s.set_npm_command(["pnpm", "dlx"])
    s.set_model_thinking_level("p", "m", "low")
    s.set_http_idle_timeout_ms(5000)
    s.set_image_width_cells(40)
    s.set_output_pad(0)
    s.set_tui_mode("fullscreen")
    s.set_fullscreen_wheel_scroll_lines(3)
    s.set_warnings({"anthropicExtraUsage": True})
    s.set_default_project_trust("never")
    s.set_last_changelog_version("9.9.9")
    s.set_enable_analytics(False)
    s.set_transport("sse")
    s.set_cache_warming_mode("idle")
    s.set_hide_thinking_block(True)
    s.set_show_images(False)
    s.set_clear_on_shrink(True)
    s.set_double_escape_action("tree")
    s.set_tree_filter_mode("user-only")
    s.set_mermaid_rendering_mode("off")
    s.set_editor_padding_x(2)
    s.set_autocomplete_max_visible(7)
    s.set_collapse_changelog(True)
    s.set_block_images(True)
    s.set_session_share_destination("gist")
    s.set_fullscreen_exit_output("resume-hint")
    s.set_fullscreen_scrollbar("hidden")
    s.set_fullscreen_copy_on_select(False)
    s.set_show_hardware_cursor(True)
    s.set_image_auto_resize(False)
    s.set_show_terminal_progress(True)
    s.set_enable_skill_commands(False)
    s.set_enable_install_telemetry(False)
    s.set_show_cache_miss_notices(False)
    s.set_default_model_and_provider("anthropic", "opus")
    s.set_project_packages(["npm:proj"])
    s.set_project_extension_paths(["pe"])
    s.set_project_skill_paths(["ps"])
    s.set_project_prompt_template_paths(["pp"])
    s.set_project_theme_paths(["pt"])
    s.flush()
    r["afterSet"] = read_all(s)
    r["modelThinking"] = s.get_model_thinking_level("p", "m")
    s.remove_model_thinking_level("p", "m")
    r["removed"] = {"thinking": s.get_model_thinking_level("p", "m"), "trackingId": s.get_tracking_id(), "deviceId": len(s.get_or_create_device_id()) > 0}
    s.flush()
    r["files"] = {"global": read_json(os.path.join(agent_dir, "settings.json")), "project": read_json(os.path.join(cwd, config_dir, "settings.json"))}

    persisted = SettingsManager.create(ctx, cwd, agent_dir)
    r["persisted"] = read_all(persisted)
    with open(os.path.join(agent_dir, "settings.json"), "w", encoding="utf-8") as handle:
        handle.write('{"defaultProvider":"external"}')
    persisted.reload()
    r["reload"] = {"provider": persisted.get_default_provider(), "model": persisted.get_default_model(), "merged": persisted.get_settings()}
    persisted.set_shell_path(None)
    persisted.set_shell_command_prefix(None)
    persisted.set_npm_command(None)
    persisted.set_enabled_models(None)
    persisted.set_default_model("kept")
    persisted.flush()
    r["cleared"] = {
        "shellPath": persisted.get_shell_path(), "prefix": persisted.get_shell_command_prefix(), "npm": persisted.get_npm_command(), "enabled": persisted.get_enabled_models(),
        "file": read_json(os.path.join(agent_dir, "settings.json")),
    }

    memory = SettingsManager.in_memory(ctx, {"defaultProvider": "mem", "compaction": {"enabled": False}, "retry": {"maxRetries": 9}})
    r["memory"] = {
        "provider": memory.get_default_provider(), "compaction": memory.get_compaction_settings(), "retry": memory.get_retry_settings(), "merged": memory.get_settings(),
        "trusted": memory.is_project_trusted(),
    }
    memory.set_default_model("mm")
    memory.apply_overrides({"defaultModel": "ov"})
    r["memoryAfter"] = {"model": memory.get_default_model(), "global": memory.get_global_settings(), "merged": memory.get_settings()}
    untrusted = SettingsManager.in_memory(ctx, None, {"projectTrusted": False})
    r["untrustedMemory"] = {"trusted": untrusted.is_project_trusted(), "write": failure(lambda: untrusted.set_project_packages(["x"]), "refusing to write project settings")}

    broken = SettingsManager.create(ctx, broken_cwd, agent_dir)
    drained = broken.drain_errors()
    entry = {"count": len(drained)}
    if drained:
        entry["scope"] = drained[0]["scope"]
        entry["pathSuffix"] = isinstance(drained[0].get("path"), str) and drained[0]["path"].endswith(os.path.join(config_dir, "settings.json"))
        entry["hasMessage"] = bool(drained[0].get("error"))
    entry["again"] = len(broken.drain_errors())
    r["errors"] = entry

    distrust = SettingsManager.create(ctx, cwd, agent_dir, {"projectTrusted": False})
    r["distrusted"] = {
        "trusted": distrust.is_project_trusted(), "project": distrust.get_project_settings(), "merged": distrust.get_settings(),
        "write": failure(lambda: distrust.set_project_extension_paths(["z"]), "refusing to write project settings"),
    }
    distrust.set_project_trusted(True)
    r["retrusted"] = {"trusted": distrust.is_project_trusted(), "projectModel": distrust.get_project_settings().get("defaultModel")}
    return r


def settings_manager_tool(ctx, params):
    return json.dumps(settings_script(ctx, params))


def trust_script(ctx, args):
    root, cwd, agent_dir, other = args["root"], args["cwd"], args["agentDir"], args["otherDir"]
    sub, anc, anc_project, bare = os.path.join(cwd, "sub"), os.path.join(root, "anc"), os.path.join(root, "anc", "proj"), os.path.join(root, "bare")
    r = {}
    store = ProjectTrustStore(ctx, agent_dir)
    r["initial"] = {"get": store.get(cwd), "entry": store.get_entry(cwd)}
    store.set(cwd, True)
    store.set(other, False)
    r["saved"] = {"get": store.get(cwd), "entry": store.get_entry(sub), "otherGet": store.get(other), "file": read_json(os.path.join(agent_dir, "trust.json"))}
    store.set_many([{"path": cwd, "decision": None}, {"path": anc, "decision": True}])
    r["many"] = {"cwd": store.get(cwd), "anc": store.get(anc_project), "entryAnc": store.get_entry(anc_project), "file": read_json(os.path.join(agent_dir, "trust.json"))}
    store.set(sub, False)
    r["nested"] = {"sub": store.get(sub), "parent": store.get(cwd), "file": read_json(os.path.join(agent_dir, "trust.json"))}

    def failure_of_get(directory, contains):
        return failure(lambda: ProjectTrustStore(ctx, os.path.join(root, directory)).get(cwd), contains)

    r["errors"] = {
        "notObject": failure_of_get("agent-array", "Invalid trust store"), "unreadable": failure_of_get("agent-bad-json", "Failed to read trust store"),
        "badValue": failure_of_get("agent-bad-value", "must be true, false, or null"),
    }
    r["resources"] = {
        "config": has_trust_requiring_project_resources(ctx, cwd), "bare": has_trust_requiring_project_resources(ctx, bare), "ancestor": has_trust_requiring_project_resources(ctx, anc_project),
    }
    return r


def trust_tool(ctx, params):
    return json.dumps(trust_script(ctx, params))


def new_extension():
    ext = pig_sdk.Extension("session-graph")
    ext.register_tool(pig_sdk.ToolDefinition(name="session_manager", label="session_manager", description="Runs the SessionManager script.", parameters={"type": "object"}, execute=session_manager_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="settings_manager", label="settings_manager", description="Runs the SettingsManager script.", parameters={"type": "object"}, execute=settings_manager_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="project_trust_store", label="project_trust_store", description="Runs the ProjectTrustStore script.", parameters={"type": "object"}, execute=trust_tool))
    return ext

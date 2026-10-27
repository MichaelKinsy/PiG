# The Python fixture of the session graph conformance rows (session_graph_test.go): the script of testfixture/sessiongraph (Go), testdata/session-graph.mjs (Node, Pi's own classes) and testdata/session-graph-rust.
import json
import os
import threading

import pig_sdk
from pig_sdk import DefaultPackageManager, DefaultResourceLoader, ModelRuntime, ProjectTrustStore, SessionManager, SettingsManager, has_trust_requiring_project_resources


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
    list_progress, list_all_progress = [], []
    r["listAll"] = infos(SessionManager.list_all(ctx, directory, lambda loaded, total, partial: list_all_progress.append([loaded, total])))
    infos(SessionManager.list(ctx, cwd, directory, lambda loaded, total, partial: list_progress.append([loaded, total])))
    r["progress"] = {"list": {"calls": len(list_progress) > 0, "last": list_progress[-1] if list_progress else None}, "listAll": {"calls": len(list_all_progress) > 0, "last": list_all_progress[-1] if list_all_progress else None}}
    r["listFork"] = infos(SessionManager.list(ctx, other_dir, fork_dir))
    found = SessionManager.find_by_id(ctx, cwd, fixed, directory)
    r["find"] = {"found": found == file, "missing": SessionManager.find_by_id(ctx, cwd, "nope", directory) is None}

    compaction_id = m.append_compaction("sum", first, 100, {"k": 1})
    compaction = m.get_entry(compaction_id)
    edit_id = m.append_context_edit(first, {"content": "edited"})
    edit = m.get_entry(edit_id)
    clear_id = m.append_context_edit(first, None)
    switched = SessionManager.create(ctx, cwd, directory, {"id": "reopen-1"})
    switched.set_session_file(file)
    r["compaction"] = {
        "type": compaction["type"], "summary": compaction["summary"], "firstKept": compaction["firstKeptEntryId"] == first, "tokensBefore": compaction["tokensBefore"], "details": compaction.get("details"), "fromHook": compaction.get("fromHook"),
        "editType": edit["type"], "replacement": edit.get("replacement"), "target": edit["targetId"] == first, "cleared": m.get_entry(clear_id).get("replacement"), "leaf": m.get_leaf_id() == clear_id,
        "badTarget": failure(lambda: m.append_context_edit("nope", None), "not found"), "badReplacement": failure(lambda: m.append_context_edit(first, {"content": 5}), "must be null or contain"),
        "switched": {"id": switched.get_session_id() == m.get_session_id(), "file": switched.get_session_file() == file, "shape": ",".join(shape(switched.get_entries())) == ",".join(shape(m.get_entries())), "count": len(switched.get_entries())},
        "badSwitch": failure(lambda: switched.set_session_file(junk), "not a valid"),
    }

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



class MemoryStorage:
    def __init__(self, store, calls, fail=False):
        self.store, self.calls, self.fail = store, calls, fail

    def with_lock(self, scope, fn):
        if self.fail:
            raise RuntimeError("disk full")
        self.calls.append(f'{scope}:{"none" if self.store.get(scope) is None else "text"}')
        nxt = fn(self.store.get(scope))
        if nxt is not None:
            self.calls.append(f"{scope}:write")
            self.store[scope] = nxt


def parsed_text(text):
    return None if text is None else json.loads(text)


def storage_report(ctx):
    store = {"global": '{"theme":"dark","quietStartup":true}', "project": None}
    calls = []
    storage = MemoryStorage(store, calls)
    out = {}
    manager = SettingsManager.from_storage(ctx, storage)
    out["loaded"] = {"theme": manager.get_theme(), "quiet": manager.get_quiet_startup(), "calls": list(calls), "errors": len(manager.drain_errors())}
    calls.clear()
    manager.set_theme("light")
    manager.flush()
    out["wrote"] = {"calls": list(calls), "global": parsed_text(store["global"]), "project": store["project"] is None, "theme": manager.get_theme()}
    store["global"] = '{"theme":"solarized"}'
    calls.clear()
    manager.reload()
    out["reloaded"] = {"theme": manager.get_theme(), "calls": list(calls)}
    calls.clear()
    manager.set_project_extension_paths(["./e"])
    manager.flush()
    out["projectWrote"] = {"project": parsed_text(store["project"]), "calls": list(calls), "paths": manager.get_project_settings().get("extensions")}
    calls.clear()
    untrusted = SettingsManager.from_storage(ctx, storage, {"projectTrusted": False})
    out["untrusted"] = {"calls": list(calls), "theme": untrusted.get_theme(), "trusted": untrusted.is_project_trusted()}
    store["global"] = "{bad"
    broken = SettingsManager.from_storage(ctx, storage)
    out["broken"] = [{"scope": entry["scope"], "noPath": entry.get("path") is None, "hasMessage": entry["error"] != ""} for entry in broken.drain_errors()]
    failing = SettingsManager.from_storage(ctx, MemoryStorage({}, [], True))
    out["failing"] = [{"scope": entry["scope"], "message": entry["error"]} for entry in failing.drain_errors()]
    return out

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
    with open(os.path.join(agent_dir, "settings.json"), "w", encoding="utf-8", newline="") as handle:
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
    r["storage"] = storage_report(ctx)
    return r


def settings_manager_tool(ctx, params):
    return json.dumps(settings_script(ctx, params))


def trust_script(ctx, args):
    root, cwd, agent_dir, other = args["root"], args["cwd"], args["agentDir"], args["otherDir"]
    sub, anc, anc_project, bare = os.path.join(cwd, "sub"), os.path.join(root, "anc"), os.path.join(root, "anc", "proj"), os.path.join(root, "bare")
    r = {}
    store = ProjectTrustStore(ctx, agent_dir)
    r["initial"] = {"get": store.get(cwd), "entry": store.get_entry(cwd)}
    ProjectTrustStore.set(store, cwd, True)
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


def skill_report(result):
    skills = sorted(
        (
            {
                "name": skill["name"], "description": skill["description"], "disable": bool(skill.get("disableModelInvocation")), "file": os.path.basename(skill["filePath"]),
                "baseDir": os.path.basename(skill["baseDir"]), "scope": skill["sourceInfo"]["scope"], "origin": skill["sourceInfo"]["origin"], "source": skill["sourceInfo"]["source"],
            }
            for skill in result["skills"]
        ),
        key=lambda skill: skill["name"],
    )
    return {"skills": skills, "diagnostics": [f'{d["type"]}: {d["message"]}' for d in result["diagnostics"]]}


def prompt_report(result):
    prompts = sorted(
        (
            {
                "name": prompt["name"], "description": prompt["description"], "argumentHint": prompt.get("argumentHint") or "", "content": prompt["content"],
                "file": os.path.basename(prompt["filePath"]), "scope": prompt["sourceInfo"]["scope"],
            }
            for prompt in result["prompts"]
        ),
        key=lambda prompt: prompt["name"],
    )
    return {"prompts": prompts, "diagnostics": [f'{d["type"]}: {d["message"]}' for d in result["diagnostics"]]}


def resource_report(loader):
    extensions = loader.get_extensions()
    source = loader.get_system_prompt_source()
    return {
        "extensions": len(extensions["extensions"]), "extensionErrors": len(extensions["errors"]), "themes": len(loader.get_themes()["themes"]),
        "skills": skill_report(loader.get_skills()), "prompts": prompt_report(loader.get_prompts()),
        "agentsFiles": [{"path": file["path"], "content": file["content"]} for file in loader.get_agents_files()["agentsFiles"]],
        "systemPrompt": loader.get_system_prompt(), "systemPromptSource": source["path"] if source else None,
        "appendSystemPrompt": loader.get_append_system_prompt(), "appendSources": [item["path"] for item in loader.get_append_system_prompt_sources()],
    }


def loader_script(ctx, args):
    root, cwd, agent_dir = args["root"], args["cwd"], args["loaderAgentDir"]
    extra_skills, extra_prompts = os.path.join(root, "extra-skills"), os.path.join(root, "extra-prompts")
    r = {}
    plain = DefaultResourceLoader(ctx, cwd=cwd, agent_dir=agent_dir)
    plain.reload()
    r["plain"] = resource_report(plain)

    def append_override(base):
        return list(base) + ["z"]

    def skills_override(base):
        return {"skills": [skill for skill in base["skills"] if skill["name"] != "beta"], "diagnostics": base["diagnostics"]}

    def prompts_override(base):
        return {"prompts": [{**prompt, "description": "overridden " + prompt["name"]} for prompt in base["prompts"]], "diagnostics": base["diagnostics"]}

    overridden = DefaultResourceLoader(
        ctx, cwd=cwd, agent_dir=agent_dir, no_skills=True, additional_skill_paths=[extra_skills], no_prompt_templates=True, additional_prompt_template_paths=[extra_prompts],
        no_context_files=True, system_prompt="inline prompt", append_system_prompt=["a1", "a2"],
        system_prompt_override=lambda base: None if base is None else base + "!", append_system_prompt_override=append_override,
        skills_override=skills_override, prompts_override=prompts_override,
        agents_files_override=lambda base: {"agentsFiles": [{"path": "/virtual/AGENTS.md", "content": "virtual"}]},
    )
    overridden.reload()
    r["overridden"] = resource_report(overridden)

    plain.extend_resources({"skillPaths": [{"path": os.path.join(extra_skills, "beta"), "metadata": {"source": "test", "scope": "project", "origin": "top-level"}}]})
    r["extended"] = skill_report(plain.get_skills())

    settings = SettingsManager.in_memory(ctx, {"skills": [extra_skills]})
    via_settings = DefaultResourceLoader(ctx, cwd=cwd, agent_dir=agent_dir, settings_manager=settings)
    via_settings.reload()
    r["viaSettings"] = skill_report(via_settings.get_skills())

    calls = {"n": 0}

    def resolve(_result):
        calls["n"] += 1
        return True

    trusting = DefaultResourceLoader(ctx, cwd=cwd, agent_dir=agent_dir)
    trusting.reload({"resolveProjectTrust": resolve})
    r["trust"] = {"calls": calls["n"], "skills": len(trusting.get_skills()["skills"])}
    bootstrap = trusting.load_project_trust_extensions()
    r["projectTrust"] = {"extensions": len(bootstrap["extensions"]), "errors": len(bootstrap["errors"])}
    return r


def loader_tool(ctx, params):
    return json.dumps(loader_script(ctx, params))


def resolved_report(paths):
    return {
        "extensions": len(paths["extensions"]),
        "skills": [{"file": os.path.basename(skill["path"]), "enabled": skill["enabled"], "source": skill["metadata"]["source"], "scope": skill["metadata"]["scope"], "origin": skill["metadata"]["origin"]} for skill in paths["skills"]],
        "prompts": len(paths["prompts"]), "themes": len(paths["themes"]),
    }


def packages_script(ctx, args):
    root, cwd, agent_dir = args["root"], args["pmCwd"], args["pmAgentDir"]
    pkg = os.path.join(root, "pkgs", "localpkg")
    r = {}
    settings = SettingsManager.create(ctx, cwd, agent_dir)
    pm = DefaultPackageManager(ctx, cwd=cwd, agent_dir=agent_dir, settings_manager=settings)
    r["initial"] = {"list": len(pm.list_configured_packages()), "installed": pm.get_installed_path(pkg, "user")}
    added = pm.add_source_to_settings(pkg)
    again = pm.add_source_to_settings(pkg)
    r["added"] = {"first": added, "again": again, "list": pm.list_configured_packages(), "packages": settings.get_packages(), "installed": pm.get_installed_path(pkg, "user")}
    r["resolved"] = resolved_report(pm.resolve())

    events = []
    pm.set_progress_callback(lambda event: events.append(event["type"] + ":" + event["action"]))
    pm.install_and_persist(pkg, True)
    pm.remove(pkg, True)
    r["removeOnly"] = {"list": len(pm.list_configured_packages())}
    r["installed"] = {"list": pm.list_configured_packages(), "events": list(events)}
    r["removed"] = {
        "project": pm.remove_and_persist(pkg, True), "list": len(pm.list_configured_packages()),
        "settings": pm.remove_source_from_settings(pkg), "settingsAgain": pm.remove_source_from_settings(pkg),
    }

    pm.set_progress_callback(None)
    r["temporary"] = resolved_report(pm.resolve_extension_sources([pkg], False, True))
    r["updates"] = len(pm.check_for_available_updates())

    missing = []
    pm.add_source_to_settings("npm:not-installed-pkg")

    def on_missing(source):
        missing.append(source)
        return "skip"

    skipped = pm.resolve(on_missing)
    r["missing"] = {"calls": missing, "skills": len(skipped["skills"])}
    return r


def packages_tool(ctx, params):
    return json.dumps(packages_script(ctx, params))


def pick_model(model):
    if model is None:
        return None
    return {"id": model["id"], "provider": model["provider"], "api": model["api"], "name": model["name"], "reasoning": bool(model.get("reasoning")), "contextWindow": model["contextWindow"], "maxTokens": model["maxTokens"]}


def error_text(fn):
    try:
        fn()
    except Exception as error:
        return f"error: {error}"
    return "no error"


def message_text(message):
    return "".join(block.get("text", "") for block in message["content"])


def model_runtime_script(ctx, args):
    r = {}
    auth, models, store, server = args["mrAuth"], args["mrModels"], args["mrStore"], args["mrServer"]
    rt = ModelRuntime.create(ctx, auth_path=auth, models_path=models, models_store_path=store)
    local = rt.get_model("mr-local", "m1")
    r["catalog"] = {
        "error": rt.get_error(), "models": [pick_model(m) for m in rt.get_models("mr-local")], "all": len(rt.get_all_models("mr-local")), "one": pick_model(local),
        "missing": rt.get_model("mr-local", "nope") is None, "builtinListed": any(m["provider"] == "anthropic" for m in rt.get_models()),
        "ofType": [m["id"] for m in rt.get_models_of_type("chat", "mr-local")], "oneOfType": pick_model(rt.get_model_of_type("chat", "mr-local", "m1")),
        "imagesOfType": len(rt.get_models_of_type("image", "mr-local")), "physical": pick_model(rt.get_physical_model("mr-local", "m1")),
        "physicalMissing": rt.get_physical_model("mr-local", "nope") is None,
    }
    r["auth"] = {
        "configured": rt.has_configured_auth("mr-local"), "unconfigured": rt.has_configured_auth("nope-prov"), "oauth": rt.is_using_oauth("mr-local"), "subscription": rt.is_using_subscription("mr-local"),
        "status": rt.get_provider_auth_status("mr-local"), "storedStatus": rt.get_provider_auth_status("stored-prov"), "unknownStatus": rt.get_provider_auth_status("nope-prov"),
        "byProvider": rt.get_auth("mr-local"), "byModel": rt.get_auth(local), "unconfiguredAuth": rt.get_auth("nope-prov"),
        "overridden": rt.get_auth("mr-local", {"apiKey": "override"}),
        "check": rt.check_auth("mr-local"), "uncheckedProvider": rt.check_auth("nope-prov"), "compat": rt.get_compatibility_request_config(local),
    }
    rt.set_runtime_api_key("rt-prov", "rk")
    with_key = {"status": rt.get_provider_auth_status("rt-prov"), "configured": rt.has_configured_auth("rt-prov"), "auth": rt.get_auth("rt-prov")}
    rt.remove_runtime_api_key("rt-prov")
    r["runtimeKey"] = {"withKey": with_key, "removed": rt.get_provider_auth_status("rt-prov"), "configuredAfter": rt.has_configured_auth("rt-prov")}
    r["credentials"] = sorted(({"providerId": c["providerId"], "type": c["type"]} for c in rt.list_credentials()), key=lambda c: c["providerId"])

    before = rt.get_registered_provider_ids()
    rt.register_provider("reg-prov", {
        "baseUrl": server, "apiKey": "regkey", "api": "openai-completions", "headers": {"x-reg": "1"},
        "models": [{"id": "r1", "name": "Reg One", "reasoning": False, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 500}],
    })
    rt.register_provider("reg-prov", {"baseUrl": server + "/v2"})
    config = rt.get_registered_provider_config("reg-prov")
    r["registered"] = {
        "before": before, "ids": rt.get_registered_provider_ids(), "models": [pick_model(m) for m in rt.get_models("reg-prov")], "baseUrl": config["baseUrl"], "apiKey": config["apiKey"], "api": config["api"], "headers": config["headers"], "modelIds": [m["id"] for m in config["models"]],
        "configured": rt.has_configured_auth("reg-prov"), "unregisteredConfig": rt.get_registered_provider_config("nope-prov") is None,
        "rejected": error_text(lambda: rt.register_provider("bad-prov", {"models": [{"id": "x"}]})),
    }
    rt.unregister_provider("reg-prov")
    r["registered"]["after"] = {"ids": rt.get_registered_provider_ids(), "models": len(rt.get_models("reg-prov")), "config": rt.get_registered_provider_config("reg-prov") is None}

    requests = []

    def route(_ctx, request):
        requests.append({"model": request["model"]["id"], "thinkingLevel": request["thinkingLevel"], "reason": request["reason"], "messages": len(request["messages"]), "state": request.get("state")})
        return {"model": rt.get_physical_model("mr-local", "m1"), "thinkingLevel": "off", "state": {"n": len(requests)}}

    rt.register_virtual_model(pig_sdk.VirtualModel(provider="mr-virtual", id="v1", name="Virtual One", route=route, thinking_levels=["off", "high"], context_window=1000, max_tokens=100, input=["text"]))
    virtual = rt.get_model("mr-virtual", "v1")
    messages = [{"role": "user", "content": "hi", "timestamp": 1}]
    routed = rt.resolve_model(virtual, messages, {"reason": "user", "thinkingLevel": "high"})
    second = rt.resolve_model(virtual, messages, {"reason": "continuation", "thinkingLevel": "off", "state": {"n": 1}})
    r["virtual"] = {
        "model": pick_model(virtual), "thinkingLevels": virtual.get("thinkingLevels"), "route": {"model": pick_model(routed["model"]), "thinkingLevel": routed["thinkingLevel"], "state": routed.get("state")}, "second": second.get("state"), "requests": requests,
        "physicalIsNull": rt.get_physical_model("mr-virtual", "v1") is None, "unknown": error_text(lambda: rt.resolve_model(local, [], {"reason": "user", "thinkingLevel": "off"})),
    }
    rt.unregister_virtual_model("mr-virtual", "v1")
    r["virtual"]["after"] = rt.get_model("mr-virtual", "v1") is None

    refreshed = rt.refresh({"allowNetwork": False})
    r["refresh"] = {"aborted": refreshed["aborted"], "errors": refreshed["errors"]}
    r["available"] = {
        "ofProvider": [m["id"] for m in rt.get_available("mr-local")], "all": [m["id"] for m in rt.get_available() if m["provider"] == "mr-local"], "snapshot": [m["id"] for m in rt.get_available_snapshot() if m["provider"] == "mr-local"],
        "allOfProvider": [m["id"] for m in rt.get_all_available("mr-local")], "ofType": [m["id"] for m in rt.get_available_of_type("chat", "mr-local")], "unavailableProvider": len(rt.get_available("nope-prov")),
    }

    conversation = {"systemPrompt": "Be brief.", "messages": [{"role": "user", "content": "say hello", "timestamp": 1}]}
    options = {"maxTokens": 50}
    stream = rt.stream_simple(local, conversation, options)
    events = [event["type"] for event in stream.events()]
    message = stream.result()
    completed = rt.complete_simple(local, conversation, options)
    full = rt.complete(local, conversation, options)
    r["stream"] = {
        "events": events, "stopReason": message["stopReason"], "text": message_text(message), "provider": message["provider"], "model": message["model"], "api": message["api"],
        "completed": message_text(completed), "full": full["stopReason"], "unknownProvider": rt.complete_simple({**local, "provider": "nope-prov"}, conversation, {})["stopReason"],
    }
    classified = rt.classify(local, {"state": {}, "questions": {}})
    imaged = rt.generate_images(local, {"input": [{"type": "text", "text": "a cat"}]})
    r["typed"] = {"classifyStop": classified["stopReason"], "classifyError": classified.get("errorMessage"), "imagesStop": imaged["stopReason"], "imagesError": imaged.get("errorMessage")}

    seen, events2 = [], []

    def prompt(p):
        seen.append(p["type"])
        return "sk-login"

    credential = rt.login("openai", "api_key", prompt, lambda event: events2.append(event["type"]))
    r["login"] = {"credential": credential, "prompts": seen, "events": events2, "listed": sorted(c["providerId"] for c in rt.list_credentials()), "status": rt.get_provider_auth_status("openai")}
    rt.logout("openai")
    r["login"]["afterLogout"] = sorted(c["providerId"] for c in rt.list_credentials())
    with open(auth) as handle:
        r["stored"] = json.load(handle)
    return r


def model_runtime_tool(ctx, params):
    return json.dumps(model_runtime_script(ctx, params))


def model_registry_script(ctx, args):
    """``ModelRegistry(runtime)`` over the parts of testdata/session-graph.mjs modelRegistryScript; the report is Pi's own run's except ``builtin`` (D78: Pi answers a builtin provider's Provider object, this SDK fails with the D78 error and the test expects that)."""
    r = {}
    auth, models, store, server = args["mrAuth"], args["mrModels"], args["mrStore"], args["mrServer"]
    rt = ModelRuntime.create(ctx, auth_path=auth, models_path=models, models_store_path=store)
    registry = pig_sdk.ModelRegistry(rt)

    def of(models_list, provider):
        return [m for m in models_list if m["provider"] == provider]

    local = registry.find("mr-local", "m1")
    available_of_type = [m["id"] for m in registry.get_available_of_type("chat", "mr-local")]
    r["catalog"] = {
        "error": registry.get_error(), "all": [pick_model(m) for m in of(registry.get_all(), "mr-local")], "builtinListed": len(of(registry.get_all(), "anthropic")) > 0,
        "available": [m["id"] for m in of(registry.get_available(), "mr-local")], "one": pick_model(local), "missing": registry.find("mr-local", "nope") is None,
        "findOfType": pick_model(registry.find_of_type("chat", "mr-local", "m1")), "ofType": [m["id"] for m in registry.get_models_of_type("chat", "mr-local")], "oneOfType": pick_model(registry.get_model_of_type("chat", "mr-local", "m1")),
        "oneOfTypeMissing": registry.get_model_of_type("image", "mr-local", "m1") is None, "imagesOfType": len(registry.get_models_of_type("image", "mr-local")), "availableOfType": available_of_type,
    }
    unknown = {**local, "provider": "nope-prov"}
    r["auth"] = {
        "configured": registry.has_configured_auth(local), "unconfigured": registry.has_configured_auth(unknown), "oauth": registry.is_using_oauth(local),
        "status": registry.get_provider_auth_status("mr-local"), "storedStatus": registry.get_provider_auth_status("stored-prov"), "unknownStatus": registry.get_provider_auth_status("nope-prov"),
        "displayLocal": registry.get_provider_display_name("mr-local"), "displayBuiltin": registry.get_provider_display_name("anthropic"), "displayUnknown": registry.get_provider_display_name("nope-prov"),
        "providerAuth": registry.get_provider_auth("mr-local"), "providerAuthNone": registry.get_provider_auth("nope-prov"),
        "apiKey": registry.get_api_key_for_provider("mr-local"), "apiKeyNone": registry.get_api_key_for_provider("nope-prov"),
        "request": registry.get_api_key_and_headers(local), "requestUnconfigured": registry.get_api_key_and_headers(unknown),
    }

    before = registry.get_registered_provider_ids()
    registry.register_provider("reg-prov", {
        "baseUrl": server, "apiKey": "regkey", "api": "openai-completions", "headers": {"x-reg": "1"},
        "models": [{"id": "r1", "name": "Reg One", "reasoning": False, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 500}],
    })
    config = registry.get_registered_provider_config("reg-prov")
    r["registered"] = {
        "before": before, "ids": registry.get_registered_provider_ids(), "models": [pick_model(m) for m in of(registry.get_all(), "reg-prov")], "baseUrl": config["baseUrl"], "apiKey": config["apiKey"], "api": config["api"], "headers": config["headers"], "modelIds": [m["id"] for m in config["models"]],
        "unregisteredConfig": registry.get_registered_provider_config("nope-prov") is None, "rejected": error_text(lambda: registry.register_provider("bad-prov", {"models": [{"id": "x"}]})),
    }
    registry.unregister_provider("reg-prov")
    r["registered"]["after"] = {"ids": registry.get_registered_provider_ids(), "models": len(of(registry.get_all(), "reg-prov")), "config": registry.get_registered_provider_config("reg-prov") is None}

    native_model = {"id": "n1", "name": "N One", "api": "nat-api", "provider": "nreg", "reasoning": False, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 500}

    def message(model, text):
        return {
            "role": "assistant", "content": [{"type": "text", "text": text}], "api": model.get("api"), "provider": model.get("provider"), "model": model.get("id"),
            "usage": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
            "stopReason": "stop", "timestamp": 1,
        }

    def scripted(tag):
        def run(model, _context, _options):
            stream = pig_sdk.create_assistant_message_event_stream()
            stream.push({"type": "done", "reason": "stop", "message": message(model, tag + ":" + model["id"])})
            return stream
        return run

    provider = pig_sdk.create_provider(
        id="nreg", name="Native Reg", auth=pig_sdk.ProviderAuth(api_key=pig_sdk.APIKeyAuth(name="Key", check=lambda _input: pig_sdk.AuthCheck(type="api_key", source="ext check"), resolve=lambda _input: pig_sdk.AuthResult(auth={"apiKey": "nk"}))),
        models=[native_model], api={"nat-api": pig_sdk.ProviderStreams(stream=scripted("n.stream"), stream_simple=scripted("n.simple"))},
    )
    registry.register_provider(provider)
    conversation = {"systemPrompt": "Be brief.", "messages": [{"role": "user", "content": "say hello", "timestamp": 1}]}
    native_stream = registry.stream_simple(native_model, conversation, {})
    native_events = [event["type"] for event in native_stream.events()]
    native_message = native_stream.result()
    registry.refresh({"allowNetwork": False})

    def provider_id(found):
        return None if found is None else found.id

    registered = registry.get_registered_native_provider("nreg")
    r["native"] = {
        "registered": provider_id(registered), "same": registered is provider, "viaGetProvider": provider_id(registry.get_provider("nreg")), "viaRuntime": provider_id(rt.get_provider("nreg")),
        "viaRuntimeRegistered": provider_id(rt.get_registered_native_provider("nreg")), "none": registry.get_registered_native_provider("nope-prov") is None, "noneProvider": registry.get_provider("nope-prov") is None,
        "ids": registry.get_registered_provider_ids(), "displayName": registry.get_provider_display_name("nreg"), "models": [m["id"] for m in of(registry.get_all(), "nreg")],
        "auth": registry.get_provider_auth("nreg"), "request": registry.get_api_key_and_headers(native_model),
        "events": native_events, "text": message_text(native_message), "stopReason": native_message["stopReason"], "completed": registry.complete(native_model, conversation, {})["stopReason"],
        "configured": registry.has_configured_auth(native_model),
    }
    registry.unregister_provider("nreg")
    r["native"]["after"] = {"registered": registry.get_registered_native_provider("nreg") is None, "provider": registry.get_provider("nreg") is None, "models": len(of(registry.get_all(), "nreg"))}
    # A stored credential makes registration mark the provider configured at once (model-runtime.ts markProvisionallyConfigured); its auth check finds no key, so the availability refresh the registration launched replaces the mark and the settled state is unconfigured.
    stored_model = dict(native_model, provider="stored-prov")
    stored_provider = pig_sdk.create_provider(
        id="stored-prov", name="Stored Native", auth=pig_sdk.ProviderAuth(api_key=pig_sdk.APIKeyAuth(name="Key", check=lambda _input: None, resolve=lambda _input: None)),
        models=[stored_model], api={"nat-api": pig_sdk.ProviderStreams(stream=scripted("s.stream"), stream_simple=scripted("s.simple"))},
    )
    registry.register_provider(stored_provider)
    registry.refresh({"allowNetwork": False})
    r["native"]["storedSettled"] = {
        "configured": registry.has_configured_auth(stored_model), "status": registry.get_provider_auth_status("stored-prov"),
        "available": any(model["provider"] == "stored-prov" for model in registry.get_available()),
    }
    registry.unregister_provider("stored-prov")
    registry.refresh({"allowNetwork": False})
    builtin = {}
    try:
        builtin["provider"] = registry.get_provider("anthropic").id
    except Exception as error:  # noqa: BLE001
        builtin["provider"] = "error: " + str(error)
    try:
        builtin["providers"] = "anthropic" if any(candidate.id == "anthropic" for candidate in rt.get_providers()) else "absent"
    except Exception as error:  # noqa: BLE001
        builtin["providers"] = "error: " + str(error)
    r["builtin"] = builtin

    registry.register_virtual_model(pig_sdk.VirtualModel(
        provider="mr-virtual", id="v1", name="Virtual One", thinking_levels=["off", "high"], context_window=1000, max_tokens=100, input=["text"],
        route=lambda _ctx, _request: {"model": rt.get_physical_model("mr-local", "m1"), "thinkingLevel": "off"},
    ))
    virtual = registry.find("mr-virtual", "v1")
    r["virtual"] = {"model": pick_model(virtual), "thinkingLevels": virtual.get("thinkingLevels")}
    registry.unregister_virtual_model("mr-virtual", "v1")
    r["virtual"]["after"] = registry.find("mr-virtual", "v1") is None

    refreshed = registry.refresh({"allowNetwork": False})
    r["refresh"] = {"aborted": refreshed["aborted"], "errors": refreshed["errors"]}
    options = {"maxTokens": 50}
    stream = registry.stream_simple(local, conversation, options)
    events = [event["type"] for event in stream.events()]
    result = stream.result()
    full = registry.complete(local, conversation, options)
    classified = registry.classify(local, {"state": {}, "questions": {}})
    imaged = registry.generate_images(local, {"input": [{"type": "text", "text": "a cat"}]})
    r["stream"] = {
        "events": events, "stopReason": result["stopReason"], "text": message_text(result), "provider": result["provider"], "model": result["model"], "api": result["api"], "full": full["stopReason"],
        "classifyStop": classified["stopReason"], "classifyError": classified.get("errorMessage"), "imagesStop": imaged["stopReason"], "imagesError": imaged.get("errorMessage"),
    }
    return r


def model_registry_tool(ctx, params):
    return json.dumps(model_registry_script(ctx, params))


def child_handles_script(ctx, args):
    """createAgentSession over the objects the extension built; the report is Pi's own run's (testdata/session-graph.mjs childHandlesScript)."""
    import gc
    import time

    r = {}
    cwd, directory, agent_dir = args["cwd"], args["dir"], args["agentDir"]

    def entry_kinds(manager):
        return [entry["type"] + (":" + entry["message"]["role"] if entry["type"] == "message" else "") for entry in manager.get_entries()]

    def last_text(session):
        message = session.messages()[-1]
        return "".join(block.get("text", "") for block in message["content"] if block.get("type") == "text")

    def observe(options, manager, settings, bind):
        options.session_manager, options.settings_manager = manager, settings
        session = ctx.create_agent_session(options)
        events = []
        session.subscribe(lambda event: events.append(event["type"] + (":" + event["message"]["role"] if event["type"].startswith("message_") else "")))
        if bind:
            session.bind_extensions()
        created = {
            "model": {"provider": session.model["provider"], "id": session.model["id"]}, "thinkingLevel": session.thinking_level, "activeTools": session.active_tools,
            "fallback": session.model_fallback_message, "extensionErrors": len(session.extension_errors), "appendix": "Handle appendix" in session.system_prompt,
            "sessionId": session.session_id == manager.get_session_id(), "sessionFile": session.session_file == manager.get_session_file(), "persisted": session.session_file is not None,
        }
        session.prompt("hello")
        state = session.state()
        return session, {"created": created, "events": list(events), "final": state.get("lastAssistantText"), "roles": [message["role"] for message in session.messages()], "entries": entry_kinds(manager), "text": last_text(session)}

    def build():
        rt = ModelRuntime.create(ctx, auth_path=args["mrAuth"], models_path=args["mrModels"], models_store_path=args["mrStore"])
        settings = SettingsManager.in_memory(ctx, {
            "compaction": {"enabled": False}, "retry": {"enabled": False}, "defaultProvider": "mr-local", "defaultModel": "m1", "defaultThinkingLevel": "high",
        })
        quiet = dict(cwd=cwd, agent_dir=agent_dir, settings_manager=settings, no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True)
        loader = DefaultResourceLoader(ctx, additional_extension_paths=[args["probeExt"]], append_system_prompt=["Handle appendix"], **quiet)
        loader.reload()
        memory = SessionManager.in_memory(ctx, cwd)
        scoped = [{"model": rt.get_model("mr-local", "m1"), "thinkingLevel": "off"}]
        first, first_report = observe(pig_sdk.CreateAgentSessionOptions(resource_loader=loader, model_runtime=rt, scoped_models=scoped), memory, settings, True)
        first_report["settings"] = {"defaultModel": settings.get_default_model(), "compaction": settings.get_compaction_enabled()}
        r["memory"] = first_report
        file = SessionManager.create(ctx, cwd, directory)
        plain_loader = DefaultResourceLoader(ctx, **quiet)
        plain_loader.reload()
        second, second_report = observe(pig_sdk.CreateAgentSessionOptions(resource_loader=plain_loader, model_runtime=rt), file, settings, False)
        second_report["onDisk"] = os.path.exists(file.get_session_file())
        second_report["samePath"] = second.session_file == file.get_session_file()
        r["file"] = second_report
        return first, second, memory, file

    first, second, memory, file = build()
    # The extension let go of every object it built (the runtime, settings and loaders are collected); the sessions still run over them.
    for _ in range(3):
        gc.collect()
        time.sleep(0.1)
    first.prompt("again")
    r["afterDrop"] = {"text": last_text(first), "entries": len(entry_kinds(memory)), "fileEntries": len(entry_kinds(file))}
    first.dispose()
    second.dispose()
    return r


def child_handles_tool(ctx, params):
    return json.dumps(child_handles_script(ctx, params))


def services_script(ctx, args):
    """createAgentSessionServices and createAgentSessionFromServices over the options of testdata/session-graph.mjs servicesScript; the report is Pi's own run's."""
    r = {}
    cwd, agent_dir = args["cwd"], args["agentDir"]
    rt = ModelRuntime.create(ctx, auth_path=args["mrAuth"], models_path=args["mrModels"], models_store_path=args["mrStore"])
    settings = SettingsManager.in_memory(ctx, {
        "compaction": {"enabled": False}, "retry": {"enabled": False}, "defaultProvider": "mr-local", "defaultModel": "m1", "defaultThinkingLevel": "high",
    })
    quiet = dict(no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True)
    services = pig_sdk.create_agent_session_services(ctx, pig_sdk.CreateAgentSessionServicesOptions(
        cwd=os.path.join(cwd, "sub", ".."), agent_dir=agent_dir, settings_manager=settings, model_runtime=rt,
        resource_loader_options=dict(quiet, additional_extension_paths=[args["servicesProbe"]], append_system_prompt=["Services appendix"]),
        extension_flag_values=[("probe-flag", True), ("missing-flag", "x"), ("probe-text", True), ("other", False)],
    ))
    loaded = services.resource_loader.get_extensions()
    r["services"] = {
        "cwd": services.cwd, "agentDir": services.agent_dir, "sameRuntime": services.model_runtime is rt, "sameSettings": services.settings_manager is settings,
        "diagnostics": services.diagnostics, "extensions": [extension["path"] for extension in loaded["extensions"]], "extensionErrors": len(loaded["errors"]),
        "appendix": services.resource_loader.get_append_system_prompt(),
        "providers": {"ids": sorted(identifier for identifier in rt.get_registered_provider_ids() if identifier.startswith("svc-")), "models": [model["id"] for model in rt.get_models("svc-prov")]},
    }
    own = pig_sdk.create_agent_session_services(ctx, pig_sdk.CreateAgentSessionServicesOptions(cwd=cwd, agent_dir=args["servicesAgentDir"], resource_loader_options=quiet))
    r["own"] = {
        "cwd": own.cwd, "agentDir": own.agent_dir, "diagnostics": own.diagnostics, "models": [model["id"] for model in own.model_runtime.get_models("svc-local")],
        "defaultModel": own.settings_manager.get_default_model(), "project": own.settings_manager.get_project_settings(), "appendix": own.resource_loader.get_append_system_prompt(),
        "registered": own.model_runtime.get_registered_provider_ids(),
    }
    defaulted = pig_sdk.create_agent_session_services(ctx, pig_sdk.CreateAgentSessionServicesOptions(cwd=cwd, settings_manager=settings, model_runtime=rt, resource_loader_options=quiet))
    r["defaulted"] = {"agentDir": defaulted.agent_dir == pig_sdk.get_agent_dir(), "cwd": defaulted.cwd}
    memory = SessionManager.in_memory(ctx, cwd)
    session = pig_sdk.create_agent_session_from_services(ctx, pig_sdk.CreateAgentSessionFromServicesOptions(
        services=services, session_manager=memory, session_start_event=pig_sdk.SessionStartEvent(reason="fork", previous_session_file="/previous.jsonl"),
        scoped_models=[{"model": rt.get_model("mr-local", "m1"), "thinkingLevel": "off"}],
    ))
    events = []
    session.subscribe(lambda event: events.append(event["type"] + (":" + event["message"]["role"] if event["type"].startswith("message_") else "")))
    session.bind_extensions()
    created = {
        "model": {"provider": session.model["provider"], "id": session.model["id"]}, "thinkingLevel": session.thinking_level, "fallback": session.model_fallback_message,
        "extensionErrors": len(session.extension_errors), "appendix": "Services appendix" in session.system_prompt, "sessionId": session.session_id == memory.get_session_id(),
    }
    session.prompt("hello")
    messages = session.messages()
    text = "".join(block.get("text", "") for block in messages[-1]["content"] if block.get("type") == "text")
    r["session"] = {
        "created": created, "events": events, "roles": [message["role"] for message in messages], "text": text,
        "started": [entry["data"] for entry in memory.get_entries() if entry["type"] == "custom" and entry.get("customType") == "probe_start"],
    }
    session.dispose()
    return r


def services_tool(ctx, params):
    return json.dumps(services_script(ctx, params))


def create_provider_script(ctx, args):
    """pi-ai's createProvider over the parts of testdata/session-graph.mjs createProviderScript; the report is Pi's own run's."""
    r = {}
    auth = pig_sdk.ProviderAuth(api_key=pig_sdk.APIKeyAuth(name="Key", resolve=lambda _input: pig_sdk.AuthResult(auth={"apiKey": "k"})))

    def M(identifier, api, **extra):
        return {"id": identifier, "name": identifier, "api": api, "provider": "cp", **extra}

    def message(model, text):
        return {
            "role": "assistant", "content": [{"type": "text", "text": text}], "api": model.get("api"), "provider": model.get("provider"), "model": model.get("id"),
            "usage": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
            "stopReason": "stop", "timestamp": 1,
        }

    def scripted(tag):
        def run(model, _context, _options):
            stream = pig_sdk.create_assistant_message_event_stream()
            stream.push({"type": "done", "reason": "stop", "message": message(model, tag + ":" + model["id"])})
            return stream
        return run

    def impl(tag, **extra):
        return pig_sdk.ProviderStreams(stream=scripted(tag + ".stream"), stream_simple=scripted(tag + ".simple"), **extra)

    def drain(stream):
        types = [event["type"] for event in stream.events()]
        result = stream.result()
        return {
            "types": types, "text": "".join(block.get("text", "") for block in result["content"]), "stopReason": result["stopReason"], "error": result.get("errorMessage"), "api": result.get("api"),
            "provider": result["provider"], "model": result["model"], "usageZero": result["usage"]["totalTokens"] == 0, "timestampIsNumber": isinstance(result["timestamp"], (int, float)),
        }

    def thrown(fn):
        try:
            fn()
        except Exception as error:  # noqa: BLE001
            return str(error)
        return "no error"

    def kind(value):
        return "function" if value is not None else "undefined"

    base = [M("c1", "api-a"), M("c2", "api-b"), M("i1", "img-a", type="image"), M("k1", "cls-a", type="classifier")]
    p = pig_sdk.create_provider(id="cp", auth=auth, models=base, api={"api-a": impl("a"), "api-b": impl("b")}, base_url="http://x", headers={"h": "1"})
    named = pig_sdk.create_provider(id="cp2", name="Named", auth=auth, models=[], api=impl("s"))
    r["meta"] = {
        "id": p.id, "name": p.name, "baseUrl": p.base_url, "headers": p.headers, "named": named.name,
        "methods": {"fetchDeferred": kind(p.fetch_deferred), "cancelDeferred": kind(p.cancel_deferred), "generateImages": kind(p.generate_images), "classify": kind(p.classify),
                    "refreshModels": kind(p.refresh_models), "filterModels": kind(p.filter_models), "filterAllModels": kind(p.filter_all_models)},
    }
    r["models"] = {"chat": [model["id"] for model in p.get_models()], "all": [model["id"] for model in p.get_all_models()]}
    options = pig_sdk.ProviderStreamOptions()
    sp = pig_sdk.create_provider(id="sp", auth=auth, models=[], api=impl("s"))
    r["dispatch"] = {
        "a": drain(p.stream(base[0], {}, options)), "b": drain(p.stream_simple(base[1], {}, options)), "missing": drain(p.stream(M("c9", "api-z"), {}, options)),
        "noApi": drain(p.stream_simple({"id": "c8", "provider": "cp"}, {}, options)), "single": drain(sp.stream(M("x", "anything"), {}, options)),
    }
    r["invalid"] = {
        "none": thrown(lambda: pig_sdk.create_provider(id="e1", auth=auth, models=[])),
        "empty": thrown(lambda: pig_sdk.create_provider(id="e2", auth=auth, models=[], api={}, images={}, classifiers={})),
    }
    cancelled = []

    def cancel_a(_model, handle, _options):
        cancelled.append(handle["id"])

    def broken_fetch(_model, _handle, _options):
        raise RuntimeError("fetch boom")

    d = pig_sdk.create_provider(id="dp", auth=auth, models=[], api={
        "api-a": impl("a", fetch_deferred=lambda model, handle, o: scripted("a.fetch." + handle["id"])(model, handle, o), cancel_deferred=cancel_a),
        "api-b": impl("b"),
    })
    broken = pig_sdk.create_provider(id="bp", auth=auth, models=[], api={"api-a": impl("a", fetch_deferred=broken_fetch)})
    r["deferred"] = {
        "fetched": drain(d.fetch_deferred(M("c1", "api-a"), {"id": "h1"}, options)), "unsupported": drain(d.fetch_deferred(M("c2", "api-b"), {"id": "h2"}, options)),
        "noApi": drain(d.fetch_deferred(M("c3", "api-z"), {"id": "h3"}, options)), "thrown": drain(broken.fetch_deferred(M("c1", "api-a"), {"id": "h4"}, options)),
        "cancelOk": thrown(lambda: d.cancel_deferred(M("c1", "api-a"), {"id": "h1"}, options)), "cancelled": cancelled,
        "cancelUnsupported": thrown(lambda: d.cancel_deferred(M("c2", "api-b"), {"id": "h2"}, options)), "cancelNoApi": thrown(lambda: d.cancel_deferred(M("c3", "api-z"), {"id": "h3"}, options)),
    }

    def pick(result):
        return {
            "api": result.get("api"), "provider": result["provider"], "model": result["model"], "stopReason": result["stopReason"], "error": result.get("errorMessage"), "output": result.get("output"),
            "answers": result.get("answers"), "timestampIsNumber": isinstance(result.get("timestamp"), (int, float)),
        }

    o = pig_sdk.create_provider(
        id="op", auth=auth, models=[],
        images={"img-a": lambda model, _request, _options: {"api": model["api"], "provider": model["provider"], "model": model["id"], "output": [{"type": "image", "data": "x"}], "stopReason": "stop", "timestamp": 1}},
        classifiers={"cls-a": lambda model, _request, _options: {"api": model["api"], "provider": model["provider"], "model": model["id"], "answers": {"q": "a"}, "stopReason": "stop", "timestamp": 1}},
    )
    operation_options = pig_sdk.ProviderOperationOptions()
    r["operations"] = {
        "methods": {"stream": "function", "generateImages": kind(o.generate_images), "classify": kind(o.classify)},
        "image": pick(o.generate_images(M("i1", "img-a", type="image"), {}, operation_options)), "imageMissing": pick(o.generate_images(M("i2", "img-z", type="image"), {}, operation_options)),
        "classify": pick(o.classify(M("k1", "cls-a", type="classifier"), {}, operation_options)), "classifyMissing": pick(o.classify(M("k2", "cls-z", type="classifier"), {}, operation_options)),
    }
    fetch_calls = []
    publications = []

    def fetch_models(context):
        fetch_calls.append(context.allow_network)
        return [M("c1", "api-a", name="Fetched One"), M("c3", "api-a"), M("u1", "api-a", type="future"), M("i1", "img-a", type="image")]

    f = pig_sdk.create_provider(id="fp", auth=auth, models=[M("c1", "api-a", name="Base One"), M("c2", "api-a")], api=impl("f"), fetch_models=fetch_models)

    def catalog():
        return [f"{model['id']}:{model['name']}:{model.get('type', 'chat')}" for model in f.get_all_models()]

    def publish(publication):
        persist = publication.persist
        publications.append({
            "persist": "omitted" if persist is pig_sdk.provider.OMITTED else {"models": [model["id"] for model in persist["models"]], "checkedAt": "number" if isinstance(persist["checkedAt"], (int, float)) else "other"},
            "update": "function" if publication.update is not None else "undefined",
        })
        if publication.update is not None:
            publication.update()
        return True

    def refresh_context(allow_network=True, signal=None, stored=None, publisher=publish):
        return pig_sdk.RefreshModelsContext(publish=publisher, stored=stored, allow_network=allow_network, signal=signal or pig_sdk.ProviderSignal())

    stored = {"models": [M("c1", "api-a", name="Stored One", provider="fp"), M("z9", "api-a", provider="other")]}
    refresh = {"present": kind(f.refresh_models), "absent": kind(p.refresh_models), "initial": catalog()}
    f.refresh_models(refresh_context(False, stored=stored))
    refresh["restoredOffline"] = {"catalog": catalog(), "fetchCalls": list(fetch_calls), "publications": list(publications)}
    publications.clear()
    aborted = pig_sdk.ProviderSignal()
    aborted.set()
    f.refresh_models(refresh_context(True, signal=aborted, stored=stored))
    refresh["aborted"] = {"fetchCalls": list(fetch_calls), "publications": list(publications)}
    publications.clear()
    f.refresh_models(refresh_context(True, stored=stored, publisher=lambda _publication: False))
    refresh["refused"] = {"catalog": catalog(), "fetchCalls": list(fetch_calls)}
    f.refresh_models(refresh_context(True))
    refresh["fetched"] = {"catalog": catalog(), "fetchCalls": list(fetch_calls), "publications": list(publications), "chat": [model["id"] for model in f.get_models()]}
    r["refresh"] = refresh
    return r


def create_provider_tool(ctx, params):
    return json.dumps(create_provider_script(ctx, params))


def extension_refs_script(ctx, args):
    """The compiled-in extension references over a DefaultResourceLoader, as testdata/session-graph.mjs extensionRefsScript does with Pi's factories; the report is Pi's own run's."""
    r = {}
    cwd, agent_dir = args["cwd"], args["agentDir"]
    rt = ModelRuntime.create(ctx, auth_path=args["mrAuth"], models_path=args["mrModels"], models_store_path=args["mrStore"])
    settings = SettingsManager.in_memory(ctx, {"compaction": {"enabled": False}, "retry": {"enabled": False}, "defaultProvider": "mr-local", "defaultModel": "m1"})
    quiet = dict(cwd=cwd, agent_dir=agent_dir, settings_manager=settings, no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True, additional_extension_paths=[args["refsProbe"]])

    def observe(factories, tools):
        loader = DefaultResourceLoader(ctx, extension_factories=factories, **quiet)
        loader.reload()
        manager = SessionManager.in_memory(ctx, cwd)
        session = ctx.create_agent_session(pig_sdk.CreateAgentSessionOptions(session_manager=manager, settings_manager=settings, model_runtime=rt, resource_loader=loader, tools=tools))
        session.bind_extensions()
        seen = [entry["data"] for entry in manager.get_entries() if entry["type"] == "custom" and entry.get("customType") == "refs_seen"]
        session.dispose()
        return seen

    def factories():
        return [
            pig_sdk.create_codemode_extension(mode="only", inline_budget=123, models=False),
            pig_sdk.create_tool_search_extension(),
            pig_sdk.create_mcp_extension(log_path=os.path.join(args["dir"], "mcp.log"), startup_wait_ms=0),
        ]

    both = ["codemode", "tool_search"]
    r["inactive"] = observe(factories(), None)
    r["active"] = observe(factories(), both)
    r["defaults"] = observe([pig_sdk.create_codemode_extension(), pig_sdk.create_tool_search_extension()], both)
    r["none"] = observe([], both)
    return r


def extension_refs_tool(ctx, params):
    return json.dumps(extension_refs_script(ctx, params))


class McpScriptTransport(pig_sdk.McpTransportEvents):
    """The in-process peer behind create_transport."""

    def __init__(self, log, events, auth):
        super().__init__()
        self.log, self.events, self.auth = log, events, auth

    def start(self):
        self.log.add(self.events, "start")
        if self.auth is not None:
            token = self.auth.token()
            self.log.add(self.events, "token undefined" if token is None else f"token {token}")

    def send(self, message, signal):
        self.log.add(self.events, f"send {message.get('method', 'response')}")
        reply = mcp_peer_reply(message)
        if reply is not None:
            self.emit_message(reply)

    def close(self):
        self.log.add(self.events, "close")
        self.emit_close()


class McpScriptLog:
    def __init__(self):
        self.lock = threading.Lock()
        self.demo, self.remote, self.credentials, self.load_config = [], [], [], []

    def server(self, name):
        return self.remote if name == "remote" else self.demo

    def add(self, entries, entry):
        with self.lock:
            entries.append(entry)


def mcp_peer_reply(message):
    """Answers one message of the MCP handshake, the tool list and a tool call, as testdata/session-graph.mjs mcpPeerReply does."""
    if "id" not in message:
        return None

    def reply(result):
        return {"jsonrpc": "2.0", "id": message["id"], "result": result}

    method = message.get("method")
    if method == "initialize":
        return reply({"protocolVersion": message["params"]["protocolVersion"], "capabilities": {"tools": {}}, "serverInfo": {"name": "fake", "version": "1"}})
    if method == "tools/list":
        return reply({"tools": [{"name": "echo", "description": "Echo text", "inputSchema": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}}]})
    if method == "tools/call":
        return reply({"content": [{"type": "text", "text": "echo:" + message["params"]["arguments"]["text"]}]})
    if method == "ping":
        return reply({})
    return {"jsonrpc": "2.0", "id": message["id"], "error": {"code": -32601, "message": "method not found"}}


class McpScriptServerStore:
    def __init__(self, log, name, state):
        self.log, self.name, self.state = log, name, state

    def load(self):
        self.log.add(self.log.credentials, f"load {self.name}")
        return self.state

    def save(self, state):
        self.log.add(self.log.credentials, f"save {self.name}")

    def with_refresh_lock(self, fn):
        self.log.add(self.log.credentials, f"lock {self.name}")
        return fn()


class McpScriptCredentials:
    def __init__(self, log, state):
        self.log, self.state = log, state

    def for_server(self, name, server_url):
        self.log.add(self.log.credentials, f"forServer {name} {server_url}")
        return McpScriptServerStore(self.log, name, self.state)

    def tokens(self, name, server_url):
        self.log.add(self.log.credentials, f"tokens {name} {server_url}")
        return {"access_token": "tok-1", "token_type": "Bearer"}

    def remove(self, name, server_url):
        self.log.add(self.log.credentials, f"remove {name} {server_url}")
        return True


def mcp_options_script(ctx, args):
    """Passes load_config, create_transport and credentials to create_mcp_extension, as testdata/session-graph.mjs mcpOptionsScript does with Pi's factory; the report is Pi's own run's."""
    cwd, agent_dir = args["cwd"], args["agentDir"]
    log = McpScriptLog()
    rt = ModelRuntime.create(ctx, auth_path=args["mrAuth"], models_path=args["mrToolModels"], models_store_path=args["mrStore"])
    settings = SettingsManager.in_memory(ctx, {"compaction": {"enabled": False}, "retry": {"enabled": False}, "defaultProvider": "mr-tool", "defaultModel": "m1"})
    state = {"serverUrl": "http://127.0.0.1:9/mcp", "tokens": {"access_token": "tok-1", "token_type": "Bearer"}}

    def load_config(c):
        with log.lock:
            log.load_config.append({"cwd": c["cwd"], "isProjectTrusted": c["isProjectTrusted"]})
        return {"servers": [
            {"name": "demo", "config": {"command": "unused", "exposure": "direct"}, "source": "<config>", "scope": "global"},
            {"name": "remote", "config": {"url": "http://127.0.0.1:9/mcp", "exposure": "direct"}, "source": "<config>", "scope": "global"},
        ], "errors": []}

    def create_transport(entry, transport_cwd, auth):
        events = log.server(entry["name"])
        log.add(events, f"create {entry['name']} cwd={transport_cwd} auth={'true' if auth is not None else 'false'}")
        return McpScriptTransport(log, events, auth)

    factory = pig_sdk.create_mcp_extension(
        load_config=load_config, create_transport=create_transport, credentials=McpScriptCredentials(log, state),
        open_url=lambda url: None, update_config=lambda entry, patch: None, log_path=os.path.join(agent_dir, "mcp.log"),
    )
    loader = DefaultResourceLoader(
        ctx, cwd=cwd, agent_dir=agent_dir, settings_manager=settings, no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True,
        additional_extension_paths=[args["mcpProbe"]], extension_factories=[factory],
    )
    loader.reload()
    manager = SessionManager.in_memory(ctx, cwd)
    session = ctx.create_agent_session(pig_sdk.CreateAgentSessionOptions(session_manager=manager, settings_manager=settings, model_runtime=rt, resource_loader=loader))
    session.bind_extensions()
    for prompt in ("call the tool", "/mcp reconnect demo", "/mcp logout remote"):
        session.prompt(prompt)
    messages = ["".join(part["text"] for part in message["content"]) for message in session.messages() if message.get("role") == "toolResult"]
    seen = [entry["data"] for entry in manager.get_entries() if entry["type"] == "custom" and entry.get("customType") == "mcp_seen"]
    with log.lock:
        report = {"messages": messages, "seen": seen, "log": {"demo": list(log.demo), "remote": list(log.remote), "credentials": list(log.credentials), "loadConfig": list(log.load_config)}}
    session.dispose()
    return report


def mcp_options_tool(ctx, params):
    return json.dumps(mcp_options_script(ctx, params))


def _call_view(found):
    return "function" if found else "undefined"


def _renderers_view(found):
    if found is None:
        return None
    return {"renderShell": found.get("renderShell"), "renderCall": _call_view(found.get("renderCall") is True), "renderResult": _call_view(found.get("renderResult") is True)}


def _runtime_view(runtime):
    return {
        "flagValues": runtime.get_flag_values(), "providers": len(runtime.pending_provider_registrations()), "nativeProviders": len(runtime.pending_native_provider_registrations()),
        "virtualModels": len(runtime.pending_virtual_model_registrations()), "mcpServers": len(runtime.mcp_servers()),
    }


def extension_runner_script(ctx, args):
    import time

    cwd, agent_dir = args["cwd"], args["agentDir"]
    r = {}

    fresh = pig_sdk.create_extension_runtime(ctx)
    runtime_report = {"initial": _runtime_view(fresh), "active": error_text(fresh.assert_active)}
    fresh.set_flag_value("x", True)
    fresh.set_flag_value("a", "b")
    runtime_report["afterSet"] = _runtime_view(fresh)
    fresh.invalidate("runtime is stale")
    fresh.invalidate("a second message is ignored")
    runtime_report["stale"] = error_text(fresh.assert_active)
    r["runtime"] = runtime_report

    rt = ModelRuntime.create(ctx, auth_path=args["mrAuth"], models_path=args["mrModels"], models_store_path=args["mrStore"])
    settings = SettingsManager.in_memory(ctx, {"compaction": {"enabled": False}, "retry": {"enabled": False}, "defaultProvider": "mr-local", "defaultModel": "m1"})
    loader_runtime = pig_sdk.create_extension_runtime(ctx)
    loader = DefaultResourceLoader(
        ctx, cwd=cwd, agent_dir=agent_dir, settings_manager=settings, runtime=loader_runtime, no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True,
        no_context_files=True, additional_extension_paths=[args["runnerProbeA"], args["runnerProbeB"]],
    )
    loader.reload()
    loaded = loader.get_extensions()
    r["loaded"] = {"paths": [ext["path"] for ext in loaded.extensions], "tools": [ext["tools"] for ext in loaded.extensions], "errors": loaded["errors"], "sources": [ext["sourceInfo"]["source"] for ext in loaded.extensions], "runtime": _runtime_view(loaded.runtime)}

    manager = SessionManager.in_memory(ctx, cwd)
    runner = pig_sdk.ExtensionRunner(ctx, loaded.extensions, loaded.runtime, cwd, manager, rt)

    keybindings = {"app.interrupt": ["ctrl+c"], "app.other": ["ctrl+k"], "app.exit": ["ctrl+d"]}
    noop = runner.get_ui_context()
    tool_views = [
        {"name": tool["definition"]["name"], "description": tool["definition"]["description"], "scope": tool["sourceInfo"]["scope"], "origin": tool["sourceInfo"]["origin"], "path": tool["sourceInfo"]["path"]}
        for tool in runner.get_all_registered_tools()
    ]
    shared = runner.get_tool_definition("shared")
    shortcuts = [{"key": entry[0], "description": entry[1].get("description"), "extensionPath": entry[1]["extensionPath"]} for entry in runner.get_shortcuts(keybindings)]
    commands = [{"name": c["name"], "invocationName": c["invocationName"], "description": c.get("description"), "path": c["sourceInfo"]["path"]} for c in runner.get_registered_commands()]

    def invocation(name):
        found = runner.get_command(name)
        return None if found is None else found["invocationName"]

    # An extension process answers a tool renderer resolution on a later turn (D89): ask until the answer is the extension's.
    alpha = None
    for _ in range(100):
        alpha = runner.resolve_tool_renderers("alpha", lambda: None)
        if alpha is not None:
            break
        time.sleep(0.05)
    context_of = runner.create_context()
    registry_same = context_of.model_registry() is rt
    manager_same = context_of.session_manager() is manager
    command_before = runner.create_command_context()
    command_before.wait_for_idle()
    new_before = command_before.new_session()
    command_before.reload()
    nested_before = runner.create_tool_context("call-0").execute_tool("alpha", {})
    twin2 = runner.get_command("twin:2")
    reads = {
        "extensionPaths": runner.get_extension_paths(), "tools": tool_views, "toolDefinition": shared["description"], "missingTool": runner.get_tool_definition("nope"),
        "flags": runner.get_flags(), "flagValues": runner.get_flag_values(), "shortcuts": shortcuts, "shortcutDiagnostics": runner.get_shortcut_diagnostics(),
        "commands": commands, "commandDiagnostics": runner.get_command_diagnostics(),
        "commandByName": {"hello": invocation("hello"), "twin": invocation("twin"), "twin:2": None if twin2 is None else twin2.get("description"), "nope": invocation("nope")},
        "hasHandlers": {"tool_call": runner.has_handlers("tool_call"), "session_shutdown": runner.has_handlers("session_shutdown")},
        "renderers": {
            "message": _call_view(runner.get_message_renderer("probe-message")), "missingMessage": _call_view(runner.get_message_renderer("nope")),
            "entry": _call_view(runner.get_entry_renderer("probe-entry")), "missingEntry": _call_view(runner.get_entry_renderer("nope")), "markdownTransformers": runner.get_markdown_transformers(),
        },
        "toolRenderers": {
            "alpha": _renderers_view(alpha),
            "other": _renderers_view(runner.resolve_tool_renderers("other", lambda: {"renderResult": True})),
            "none": _renderers_view(runner.resolve_tool_renderers("other", lambda: None)),
        },
        "hasUI": runner.has_ui(),
        "noUI": {"select": noop.select("t", ["a"]), "confirm": noop.confirm("t", "m"), "input": noop.input("t"), "editorText": noop.get_editor_text()},
        "registryIsSame": runner.get_model_registry() is rt,
        "activeTools": error_text(runner.get_active_tools), "mode": context_of.mode(), "cwd": context_of.cwd(),
        "sessionManagerIsSame": manager_same, "modelRegistryIsSame": registry_same,
        "commandContextBeforeBind": {"newSession": new_before}, "toolContextBeforeBind": nested_before["result"]["content"][0]["text"],
    }
    runner.set_flag_value("probe-flag", "changed")
    reads["flagValuesAfterSet"] = runner.get_flag_values()
    runner.set_flag_value("probe-bool", True)
    reads["flagValuesAfterBool"] = runner.get_flag_values()
    r["reads"] = reads

    log = []

    def record(name, *call_args):
        log.append([name, *call_args])

    model = rt.get_model("mr-local", "m1")
    source_info = {"path": "p", "source": "local", "scope": "temporary", "origin": "top-level"}
    actions = {
        "send_message": lambda message, options=None: record("sendMessage", message, options),
        "send_user_message": lambda content, options=None: record("sendUserMessage", content, options),
        "append_entry": lambda custom_type, data=None: record("appendEntry", custom_type, data),
        "set_session_name": lambda name: record("setSessionName", name),
        "get_session_name": lambda: "session-name",
        "set_label": lambda entry_id, label=None: record("setLabel", entry_id, label),
        "get_active_tools": lambda: ["alpha", "beta"],
        "get_all_tools": lambda: [{"name": "alpha", "description": "Alpha tool", "parameters": {"type": "object"}, "sourceInfo": source_info}],
        "get_settings": lambda: {"theme": "dark"},
        "set_active_tools": lambda names: record("setActiveTools", names),
        "refresh_tools": lambda: record("refreshTools"),
        "get_commands": lambda: [{"name": "hello", "description": "d", "source": "extension", "sourceInfo": source_info}],
        "set_model": lambda m: (record("setModel", m["id"]), True)[1],
        "get_thinking_level": lambda: "medium",
        "set_thinking_level": lambda level: record("setThinkingLevel", level),
    }
    context_actions = {
        "get_model": lambda: model,
        "get_scoped_models": lambda: [{"model": model, "thinkingLevel": "low"}],
        "is_idle": lambda: False,
        "is_project_trusted": lambda: True,
        "get_signal": lambda: None,
        "abort": lambda: record("abort"),
        "has_pending_messages": lambda: True,
        "shutdown": lambda: record("shutdown"),
        "get_context_usage": lambda: {"tokens": 10, "contextWindow": 100, "percent": 10.0},
        "compact": lambda options=None: record("compact", (options or {}).get("customInstructions")),
        "get_system_prompt": lambda: "bound system prompt",
        "get_system_prompt_options": lambda: {"cwd": cwd, "customPrompt": "custom"},
        "execute_tool": lambda caller_id, name, params, _options=None: {"toolCall": {"type": "toolCall", "id": f"{caller_id}/1", "name": name, "arguments": params}, "result": {"content": [{"type": "text", "text": f"ran {name}"}], "details": {}}, "isError": False},
        "get_callable_tools": lambda: [{"name": "alpha", "label": "Alpha", "description": "Alpha tool", "parameters": {"type": "object"}}],
    }
    runner.bind_core(actions, context_actions)
    r["bound"] = {"activeTools": runner.get_active_tools(), "errors": []}
    reported = []
    off = runner.on_error(lambda report: reported.append({"extensionPath": report["extensionPath"], "event": report["event"], "error": report["error"]}))
    runner.report_unhandled_mcp_servers()

    emits = {}

    def put(key, value):
        if value is not None:
            emits[key] = value

    runner.emit({"type": "session_start", "reason": "startup"})
    emits["sessionBeforeSwitch"] = runner.emit({"type": "session_before_switch", "reason": "new"})
    runner.emit({"type": "agent_start"})
    put("toolCall", runner.emit_tool_call({"type": "tool_call", "toolCallId": "t1", "toolName": "bash", "input": {"command": "ls"}}))
    put("toolCallBlocked", runner.emit_tool_call({"type": "tool_call", "toolCallId": "t2", "toolName": "bash", "input": {"command": "forbidden"}}))
    put("toolResult", runner.emit_tool_result({"type": "tool_result", "toolCallId": "t1", "toolName": "bash", "input": {"command": "ls"}, "content": [{"type": "text", "text": "out"}], "isError": False}))
    emits["inputTransform"] = runner.emit_input("t:x", None, "interactive")
    emits["inputHandled"] = runner.emit_input("handled-me", None, "interactive")
    emits["inputContinue"] = runner.emit_input("plain", None, "rpc")
    emits["context"] = runner.emit_context([{"role": "user", "content": "keep", "timestamp": 1}, {"role": "user", "content": "drop-me", "timestamp": 2}])
    started = runner.emit_before_agent_start({"messageType": "user", "prompt": "do it"}, {"cwd": cwd, "customPrompt": "base"})
    forced = started["systemPromptOptions"].get("forceSystemPrompt") or ""
    emits["beforeAgentStart"] = {"messages": started["messages"], "forced": {"startsWithBase": forced.startswith("base"), "endsWithA": forced.endswith("[A]")}, "customPrompt": started["systemPromptOptions"].get("customPrompt")}
    put("messageEnd", runner.emit_message_end({"type": "message_end", "message": {"role": "user", "content": "hi", "timestamp": 1}}))
    emits["providerRequest"] = runner.emit_before_provider_request({"model": "m"})
    emits["providerHeaders"] = runner.emit_before_provider_headers({"x-del": "d", "x-keep": "k"})
    put("userBash", runner.emit_user_bash({"type": "user_bash", "command": "ls", "excludeFromContext": False, "cwd": cwd}))
    emits["resourcesDiscover"] = runner.emit_resources_discover(cwd, "startup")
    emits["cacheWarming"] = runner.emit_cache_warming_decision({"type": "cache_warming_decision", "warmCost": 1, "missCost": 2, "continuationProbability": 0.5, "action": "warm"})
    built = []

    def build_context(entries):
        built.append(len(entries))
        return {"contextEntries": [], "contextMessages": [], "llmMessages": [], "pendingMessages": [], "canContinue": True}

    emits["boundary"] = runner.emit_boundary(
        {"type": "turn_end", "turnIndex": 0, "message": {"role": "user", "content": "x", "timestamp": 1}, "toolResults": [], "messageEntryId": "m1", "toolResultEntryIds": [], "outcome": "completed"},
        build_context,
    )
    emits["boundaryBuilt"] = list(built)
    runner.emit_error({"extensionPath": "/x/ext.mjs", "event": "manual", "error": "reported by hand"})
    r["emits"] = emits
    r["errors"] = list(reported)

    live = runner.create_context()
    signal = live.signal()
    contexts = {
        "mode": live.mode(), "hasUI": live.has_ui(), "cwd": live.cwd(), "model": live.model()["id"], "scopedModels": [[e["model"]["id"], e.get("thinkingLevel")] for e in live.scoped_models()],
        "thinkingLevel": live.thinking_level(), "isIdle": live.is_idle(), "isProjectTrusted": live.is_project_trusted(), "signal": None if signal is None else {"aborted": signal.is_set()},
        "hasPendingMessages": live.has_pending_messages(), "contextUsage": live.get_context_usage(), "systemPrompt": live.get_system_prompt(),
    }
    live.abort()
    live.shutdown()
    live.compact({"customInstructions": "short"})
    runner.shutdown()
    command = runner.create_command_context()
    runner.bind_command_context({
        "wait_for_idle": lambda: record("waitForIdle"),
        "new_session": lambda options=None: (record("newSession", (options or {}).get("parentSession")), {"cancelled": False})[1],
        "fork": lambda entry_id, options=None: (record("fork", entry_id, (options or {}).get("position")), {"cancelled": True})[1],
        "navigate_tree": lambda target, options=None: (record("navigateTree", target, (options or {}).get("label")), {"cancelled": False})[1],
        "switch_session": lambda path, _options=None: (record("switchSession", path), {"cancelled": False})[1],
        "reload": lambda: record("reload"),
    })
    command_report = {"systemPromptOptions": command.get_system_prompt_options()}
    command.wait_for_idle()
    command_report["waitForIdle"] = None
    command_report["newSession"] = command.new_session({"parentSession": "parent.jsonl"})
    command_report["fork"] = command.fork("e1", {"position": "at"})
    command_report["navigateTree"] = command.navigate_tree("e2", {"label": "l"})
    command_report["switchSession"] = command.switch_session("other.jsonl")
    command.reload()
    command_report["reload"] = None
    contexts["command"] = command_report
    tool_context = runner.create_tool_context("call-1")
    contexts["tool"] = {"tools": [tool["name"] for tool in tool_context.tools()], "executeTool": tool_context.execute_tool("alpha", {"text": "x"})}
    r["contexts"] = contexts

    ui_log = []

    def note(*call):
        ui_log.append(list(call))

    def select(title, options, _opts=None):
        note("select", title, options)
        return options[1] if len(options) > 1 else None

    def confirm(title, message, _opts=None):
        note("confirm", title, message)
        return True

    def input_(title, placeholder=None, _opts=None):
        note("input", title, placeholder)
        return "typed"

    def editor(title, prefill=None):
        note("editor", title, prefill)
        return f"{prefill or ''}!"

    ui = {
        "select": select, "confirm": confirm, "input": input_, "editor": editor,
        "notify": lambda message, kind=None: note("notify", message, kind),
        "set_status": lambda key, text=None: note("setStatus", key, text),
        "set_working_message": lambda message=None: note("setWorkingMessage", message),
        "set_working_visible": lambda visible: note("setWorkingVisible", visible),
        "set_hidden_thinking_label": lambda label=None: note("setHiddenThinkingLabel", label),
        "set_widget": lambda key, content, options=None: note("setWidget", key, content, (options or {}).get("placement")),
        "set_title": lambda title: note("setTitle", title),
        "paste_to_editor": lambda text: note("pasteToEditor", text),
        "set_editor_text": lambda text: note("setEditorText", text),
        "get_editor_text": lambda: "editor text",
        "get_tools_expanded": lambda: True,
        "set_tools_expanded": lambda expanded: note("setToolsExpanded", expanded),
    }
    runner.set_ui_context(ui, "tui")
    live_ui = runner.get_ui_context()
    dialogs = {"select": live_ui.select("Pick", ["x", "y"]), "confirm": live_ui.confirm("Sure?", "really"), "input": live_ui.input("Name", "placeholder"), "editor": live_ui.editor("Edit", "pre")}
    live_ui.notify("hello", "info")
    live_ui.set_status("k", "v")
    live_ui.set_working_message("working")
    live_ui.set_working_visible(False)
    live_ui.set_hidden_thinking_label("hidden")
    live_ui.set_widget("w", ["line"], {"placement": "belowEditor"})
    live_ui.set_title("title")
    live_ui.paste_to_editor("pasted")
    live_ui.set_editor_text("set")
    live_ui.set_tools_expanded(False)
    reads_ui = {"editorText": live_ui.get_editor_text(), "toolsExpanded": live_ui.get_tools_expanded()}
    # The prompt events are delivered on a later turn: wait for both of each dialog.
    for _ in range(100):
        if sum(1 for entry in log if entry[0] == "appendEntry" and entry[1] == "a_ui") >= 8:
            break
        time.sleep(0.05)
    after = runner.create_context()
    ui_report = {"mode": after.mode(), "hasUI": runner.has_ui(), "contextHasUI": after.has_ui(), "dialogs": dialogs, "reads": reads_ui, "calls": list(ui_log)}
    runner.set_ui_context(None, "")
    cleared = runner.create_context()
    ui_report["afterClear"] = {"hasUI": runner.has_ui(), "mode": cleared.mode()}
    r["ui"] = ui_report

    off()
    runner.emit_error({"extensionPath": "/x/ext.mjs", "event": "after-off", "error": "not delivered"})
    r["errors"] = list(reported)
    runner.invalidate("runner is stale")
    runner.invalidate("a second message is ignored")
    stale_context = runner.create_context()
    r["stale"] = {
        "cwd": error_text(stale_context.cwd), "isIdle": error_text(stale_context.is_idle), "activeTools": error_text(runner.get_active_tools),
        "commandContext": error_text(runner.create_command_context().wait_for_idle), "toolContext": error_text(lambda: runner.create_tool_context("c").execute_tool("alpha", {})),
        "ui": error_text(stale_context.ui), "emit": error_text(lambda: runner.emit({"type": "agent_start"})),
    }
    r["log"] = list(log)
    return r


def extension_runner_tool(ctx, params):
    return json.dumps(extension_runner_script(ctx, params))


def _entry_text(message):
    content = message.get("content")
    if isinstance(content, str):
        return content
    return "".join(block.get("text", "") for block in content or [] if block.get("type") == "text")


def agent_session_runtime_script(ctx, args):
    """createAgentSessionRuntime and AgentSessionRuntime through every replacement method, as testdata/agent-session-runtime.mjs does with Pi's own classes; the report is Pi's own run's."""
    cwd, agent_dir, root = args["cwd"], args["agentDir"], args["root"]
    r = {}
    rt = ModelRuntime.create(ctx, auth_path=args["mrAuth"], models_path=args["mrModels"], models_store_path=args["mrStore"])
    settings = SettingsManager.in_memory(ctx, {"compaction": {"enabled": False}, "retry": {"enabled": False}, "defaultProvider": "mr-local", "defaultModel": "m1"})
    quiet = dict(no_extensions=True, no_skills=True, no_prompt_templates=True, no_themes=True, no_context_files=True)
    session_dir = os.path.join(root, "runtime-sessions")
    log_file, cancel_file = os.path.join(cwd, "runtime-events.log"), os.path.join(cwd, "cancel-next")
    os.makedirs(cwd, exist_ok=True)
    calls = []

    def factory(options):
        start = options.session_start_event
        calls.append({"cwd": options.cwd, "reason": None if start is None else start.reason, "previous": bool(start and start.previous_session_file), "persisted": options.session_manager.is_persisted()})
        services = pig_sdk.create_agent_session_services(ctx, pig_sdk.CreateAgentSessionServicesOptions(
            cwd=options.cwd, agent_dir=options.agent_dir, settings_manager=settings, model_runtime=rt,
            resource_loader_options=dict(quiet, additional_extension_paths=[args["runtimeProbe"]]),
        ))
        session = pig_sdk.create_agent_session_from_services(ctx, pig_sdk.CreateAgentSessionFromServicesOptions(services=services, session_manager=options.session_manager, session_start_event=start))
        return pig_sdk.CreateAgentSessionRuntimeResult(session=session, services=services, diagnostics=services.diagnostics, model_fallback_message=session.model_fallback_message)

    manager = SessionManager.create(ctx, cwd, session_dir)
    run_file = manager.get_session_file()
    runtime = pig_sdk.create_agent_session_runtime(ctx, factory, pig_sdk.CreateAgentSessionRuntimeOptions(cwd=cwd, agent_dir=agent_dir, session_manager=manager))
    counts = {"rebinds": 0, "invalidations": 0}

    def rebind(session):
        counts["rebinds"] += 1
        session.bind_extensions()

    def invalidated():
        counts["invalidations"] += 1

    runtime.set_rebind_session(rebind)
    runtime.set_before_session_invalidate(invalidated)
    runtime.session.bind_extensions()
    seen = {manager.get_session_id()}

    def current():
        return runtime.session.session_manager()

    def is_new():
        identifier = current().get_session_id()
        fresh = identifier not in seen
        seen.add(identifier)
        return fresh

    def kinds():
        return [entry["type"] for entry in current().get_entries()]

    def roles():
        return [message["role"] for message in runtime.session.messages()]

    def user_entry(wanted):
        return next(entry for entry in current().get_entries() if entry["type"] == "message" and entry["message"]["role"] == "user" and _entry_text(entry["message"]) == wanted)

    def assistant_id():
        return next(entry["id"] for entry in current().get_entries() if entry["type"] == "message" and entry["message"]["role"] == "assistant")

    def cancelling(run):
        with open(cancel_file, "w", newline=""):
            pass
        try:
            return run()
        finally:
            os.remove(cancel_file)

    def parent_is(file):
        return (current().get_header() or {}).get("parentSession") == file

    r["initial"] = {
        "cwd": runtime.cwd, "diagnostics": runtime.diagnostics, "fallback": runtime.model_fallback_message, "sameLog": current().get_session_id() == manager.get_session_id(),
        "servicesCwd": runtime.services.cwd, "servicesAgentDir": runtime.services.agent_dir == agent_dir, "calls": len(calls),
    }
    runtime.session.prompt("one")
    r["prompted"] = {"kinds": kinds(), "persisted": os.path.exists(run_file)}

    cancelled_new = cancelling(lambda: runtime.new_session())
    r["newCancelled"] = {"result": cancelled_new, "sameSession": not is_new(), "calls": len(calls), "rebinds": counts["rebinds"], "invalidations": counts["invalidations"]}
    created = runtime.new_session({"parentSession": run_file, "setup": lambda sm: sm.append_custom_message_entry("setup-note", "from setup", True)})
    r["newSession"] = {
        "result": created, "fresh": is_new(), "parentIsFirst": parent_is(run_file), "kinds": kinds(), "roles": roles(), "calls": list(calls[1:]),
        "rebinds": counts["rebinds"], "invalidations": counts["invalidations"],
    }
    runtime.session.prompt("two")

    second_file = current().get_session_file()
    two_id = user_entry("two")["id"]
    r["forkCancelled"] = {"result": cancelling(lambda: runtime.fork(two_id)), "sameSession": not is_new()}
    forked = runtime.fork(two_id)
    r["fork"] = {"result": forked, "fresh": is_new(), "parentIsNotSecond": current().get_session_file() != second_file, "roles": roles(), "kinds": kinds(), "calls": len(calls) - 2}
    runtime.session.prompt("three")
    forked_at = runtime.fork(assistant_id(), {"position": "at"})
    r["forkAt"] = {"result": forked_at, "fresh": is_new(), "roles": roles()}
    r["forkErrors"] = {"missing": error_text(lambda: runtime.fork("no-such-entry")), "notUser": error_text(lambda: runtime.fork(assistant_id()))}

    r["switchCancelled"] = {"result": cancelling(lambda: runtime.switch_session(run_file)), "sameSession": not is_new()}
    switched = runtime.switch_session(run_file)
    r["switch"] = {"result": switched, "backOnFirst": current().get_session_id() == manager.get_session_id(), "file": os.path.basename(current().get_session_file()) == os.path.basename(run_file), "roles": roles()}

    emptied = runtime.fork(user_entry("one")["id"])
    r["forkRoot"] = {"result": emptied, "fresh": is_new(), "parentIsFirst": parent_is(run_file), "kinds": kinds()}

    import_dir = os.path.join(root, "import")
    os.makedirs(import_dir, exist_ok=True)
    import_path = os.path.join(import_dir, "imported.jsonl")
    with open(run_file, "rb") as source, open(import_path, "wb") as target:
        target.write(source.read())
    imported = runtime.import_from_jsonl(import_path)
    imported_file = current().get_session_file()
    r["import"] = {
        "result": imported, "fresh": is_new(), "inSessionDir": imported_file.startswith(session_dir), "name": os.path.basename(imported_file), "roles": roles(), "sourceKept": os.path.exists(import_path),
        "missing": error_text(lambda: runtime.import_from_jsonl(os.path.join(import_dir, "missing.jsonl"))),
    }

    runtime.dispose()
    r["counts"] = {"rebinds": counts["rebinds"], "invalidations": counts["invalidations"], "factoryCalls": len(calls), "reasons": [("null" if call["reason"] is None else call["reason"]) + ("+previous" if call["previous"] else "") for call in calls]}
    with open(log_file) as handle:
        r["events"] = handle.read().rstrip("\n").split("\n")
    return r


def agent_session_runtime_tool(ctx, params):
    return json.dumps(agent_session_runtime_script(ctx, params))


def new_extension():
    ext = pig_sdk.Extension("session-graph")
    ext.register_tool(pig_sdk.ToolDefinition(name="session_manager", label="session_manager", description="Runs the SessionManager script.", parameters={"type": "object"}, execute=session_manager_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="settings_manager", label="settings_manager", description="Runs the SettingsManager script.", parameters={"type": "object"}, execute=settings_manager_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="project_trust_store", label="project_trust_store", description="Runs the ProjectTrustStore script.", parameters={"type": "object"}, execute=trust_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="resource_loader", label="resource_loader", description="Runs the DefaultResourceLoader script.", parameters={"type": "object"}, execute=loader_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="model_runtime", label="model_runtime", description="Runs the ModelRuntime script.", parameters={"type": "object"}, execute=model_runtime_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="model_registry", label="model_registry", description="Runs the ModelRegistry over a ModelRuntime script.", parameters={"type": "object"}, execute=model_registry_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="package_manager", label="package_manager", description="Runs the DefaultPackageManager script.", parameters={"type": "object"}, execute=packages_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="child_handles", label="child_handles", description="Runs the createAgentSession handle options script.", parameters={"type": "object"}, execute=child_handles_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="create_provider", label="create_provider", description="Runs the createProvider script.", parameters={"type": "object"}, execute=create_provider_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="extension_refs", label="extension_refs", description="Runs the compiled-in extension factories script.", parameters={"type": "object"}, execute=extension_refs_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="mcp_options", label="mcp_options", description="Runs the createMcpExtension function options script.", parameters={"type": "object"}, execute=mcp_options_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="extension_runner", label="extension_runner", description="Runs the createExtensionRuntime and ExtensionRunner script.", parameters={"type": "object"}, execute=extension_runner_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="agent_session_runtime", label="agent_session_runtime", description="Runs the createAgentSessionRuntime script.", parameters={"type": "object"}, execute=agent_session_runtime_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="services", label="services", description="Runs the createAgentSessionServices script.", parameters={"type": "object"}, execute=services_tool))
    return ext

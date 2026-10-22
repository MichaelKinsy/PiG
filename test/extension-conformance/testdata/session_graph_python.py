# The Python fixture of the session graph conformance rows (session_graph_test.go): the script of testfixture/sessiongraph (Go), testdata/session-graph.mjs (Node, Pi's own classes) and testdata/session-graph-rust.
import json
import os

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


def new_extension():
    ext = pig_sdk.Extension("session-graph")
    ext.register_tool(pig_sdk.ToolDefinition(name="session_manager", label="session_manager", description="Runs the SessionManager script.", parameters={"type": "object"}, execute=session_manager_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="settings_manager", label="settings_manager", description="Runs the SettingsManager script.", parameters={"type": "object"}, execute=settings_manager_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="project_trust_store", label="project_trust_store", description="Runs the ProjectTrustStore script.", parameters={"type": "object"}, execute=trust_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="resource_loader", label="resource_loader", description="Runs the DefaultResourceLoader script.", parameters={"type": "object"}, execute=loader_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="model_runtime", label="model_runtime", description="Runs the ModelRuntime script.", parameters={"type": "object"}, execute=model_runtime_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="package_manager", label="package_manager", description="Runs the DefaultPackageManager script.", parameters={"type": "object"}, execute=packages_tool))
    ext.register_tool(pig_sdk.ToolDefinition(name="child_handles", label="child_handles", description="Runs the createAgentSession handle options script.", parameters={"type": "object"}, execute=child_handles_tool))
    return ext

# The Python fixture of the session graph conformance rows (session_graph_test.go): the script of testfixture/sessiongraph (Go), testdata/session-graph.mjs (Node, Pi's own classes) and testdata/session-graph-rust.
import json
import os

import pig_sdk
from pig_sdk import SessionManager


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


def new_extension():
    ext = pig_sdk.Extension("session-graph")
    ext.register_tool(pig_sdk.ToolDefinition(name="session_manager", label="session_manager", description="Runs the SessionManager script.", parameters={"type": "object"}, execute=session_manager_tool))
    return ext

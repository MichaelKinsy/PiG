//! Fixture for the session graph conformance rows (test/extension-conformance/session_graph_test.go): the script of testfixture/sessiongraph (Go), testdata/session-graph.mjs (Node, Pi's own classes) and testdata/session_graph_python.py.

use pig_sdk::{empty_schema, Context, Extension, NewSessionOptions, SessionInfo, SessionManager, ToolResult};
use serde_json::{json, Value};
use std::io;
use std::path::Path;
use std::time::{SystemTime, UNIX_EPOCH};

fn text(value: &Value) -> &str {
    value.as_str().unwrap_or_default()
}

fn shape(entries: &Value) -> Vec<String> {
    let entries = entries.as_array().map(Vec::as_slice).unwrap_or_default();
    entries
        .iter()
        .map(|entry| {
            let parent = entry["parentId"]
                .as_str()
                .and_then(|id| entries.iter().position(|candidate| candidate["id"].as_str() == Some(id)))
                .map_or(-1, |at| at as i64);
            format!("{}<{}", text(&entry["type"]), parent)
        })
        .collect()
}

fn failure<T>(result: io::Result<T>, contains: &str) -> String {
    match result {
        Ok(_) => "no error".into(),
        Err(error) if error.to_string().contains(contains) => format!("error: {contains}"),
        Err(error) => format!("error: {error}"),
    }
}

fn infos(list: Vec<SessionInfo>) -> Value {
    let mut out: Vec<Value> = list
        .into_iter()
        .map(|info| {
            json!({
                "id": info.id, "messageCount": info.message_count, "name": info.name, "firstMessage": info.first_message, "cwd": info.cwd,
                "createdRecent": millis(info.created) > 1767225600000, "modifiedMs": millis(info.modified), "fileInDir": !info.path.is_empty(),
            })
        })
        .collect();
    out.sort_by(|a, b| text(&a["id"]).cmp(text(&b["id"])));
    Value::Array(out)
}

fn millis(time: SystemTime) -> i64 {
    match time.duration_since(UNIX_EPOCH) {
        Ok(after) => after.as_millis() as i64,
        Err(before) => -(before.duration().as_millis() as i64),
    }
}

fn dir_of(path: &str) -> String {
    Path::new(path).parent().map(|parent| parent.to_string_lossy().into_owned()).unwrap_or_default()
}

fn assistant() -> Value {
    json!({
        "role": "assistant", "content": [{"type": "text", "text": "hi"}], "api": "test", "provider": "p", "model": "m",
        "usage": {"input": 1, "output": 1, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 2, "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}},
        "stopReason": "stop", "timestamp": 2,
    })
}

fn id(options: &str) -> NewSessionOptions {
    NewSessionOptions { id: Some(options.into()), parent_session: None }
}

fn script(ctx: &Context, args: &Value) -> io::Result<Value> {
    let (cwd, dir, other_dir, junk) = (text(&args["cwd"]), text(&args["dir"]), text(&args["otherDir"]), text(&args["junk"]));
    let mut r = serde_json::Map::new();
    let fixed = "fixed-session-1";
    let m = SessionManager::create(ctx, cwd, Some(dir), Some(&id(fixed)))?;
    let file = text(&m.get_session_file()?).to_string();
    r.insert("created".into(), json!({
        "id": m.get_session_id()?, "persisted": m.is_persisted()?, "dir": m.get_session_dir()?, "fileSuffix": file.ends_with("_fixed-session-1.jsonl"),
        "fileInDir": dir_of(&file) == dir, "cwd": m.get_cwd()?, "defaultDir": m.uses_default_session_dir()?, "entries": m.get_entry_count()?, "leaf": m.get_leaf_id()?,
    }));
    let first = text(&m.append_message(json!({"role": "user", "content": "hello", "timestamp": 1}))?).to_string();
    m.append_message(assistant())?;
    m.append_session_info("graph")?;
    m.append_label_change(&first, Some("start"))?;
    m.append_custom_entry("note", Some(json!({"a": 1, "b": 2})))?;
    m.append_thinking_level_change("high")?;
    let last = text(&m.append_model_change("p", "m")?).to_string();
    let entries = m.get_entries()?;
    let header = m.get_header()?;
    let custom = entries.as_array().and_then(|list| list.iter().find(|entry| entry["type"] == "custom")).cloned().unwrap_or(json!({}));
    r.insert("log".into(), json!({
        "name": m.get_session_name()?, "label": m.get_label(&first)?, "count": m.get_entry_count()?, "shape": shape(&entries), "leafIsLast": m.get_leaf_id()?.as_str() == Some(&last),
        "entryType": m.get_entry(&first)?["type"], "missing": m.get_entry("nope")?.is_null(),
        "headerType": header["type"], "headerVersion": header["version"], "headerId": header["id"], "headerCwd": header["cwd"],
        "customType": custom["customType"], "customData": custom["data"],
    }));
    let built = m.build_session_context()?;
    r.insert("context".into(), json!({"messages": built["messages"].as_array().map_or(0, Vec::len), "thinkingLevel": built["thinkingLevel"], "model": built["model"]}));
    let tree = m.get_tree()?;
    r.insert("tree".into(), json!({"roots": tree.as_array().map_or(0, Vec::len), "firstChildren": tree[0]["children"].as_array().map_or(0, Vec::len)}));

    m.branch(&first)?;
    let alt = text(&m.append_message(json!({"role": "user", "content": "alt", "timestamp": 3}))?).to_string();
    r.insert("branch".into(), json!({"shape": shape(&m.get_branch(None)?), "children": m.get_children(&first)?.as_array().map_or(0, Vec::len), "leafIsAlt": m.get_leaf_id()?.as_str() == Some(&alt)}));
    m.reset_leaf()?;
    r.insert("reset".into(), json!({"leaf": m.get_leaf_id()?}));
    let summary = text(&m.branch_with_summary(Some(&first), "summary text", None, None, None)?).to_string();
    let summary_entry = m.get_entry(&summary)?;
    r.insert("summary".into(), json!({"type": summary_entry["type"], "parentIsFirst": summary_entry["parentId"] == json!(first), "text": summary_entry["summary"], "fromId": summary_entry["fromId"] == json!(first)}));
    let branched = m.create_branched_session(&summary)?;
    let branched = branched.as_str();
    r.insert("branched".into(), json!({
        "isFile": branched.is_some_and(|path| path.ends_with(".jsonl")), "inDir": branched.is_some_and(|path| dir_of(path) == dir), "newFile": branched.is_some_and(|path| path != file),
        "idChanged": text(&m.get_session_id()?) != fixed, "count": m.get_entry_count()?, "shape": shape(&m.get_entries()?),
    }));

    let opened = SessionManager::open(ctx, &file, None, None)?;
    r.insert("opened".into(), json!({
        "id": opened.get_session_id()?, "count": opened.get_entry_count()?, "cwd": opened.get_cwd()?, "headerCwd": opened.get_header()?["cwd"],
        "dirIsFileDir": text(&opened.get_session_dir()?) == dir, "file": text(&opened.get_session_file()?) == file, "name": opened.get_session_name()?,
    }));
    let overridden = SessionManager::open(ctx, &file, None, Some(other_dir))?;
    r.insert("override".into(), json!({"cwd": overridden.get_cwd()?, "id": overridden.get_session_id()?}));
    let recent = SessionManager::continue_recent(ctx, cwd, Some(dir))?;
    let recent_file = recent.get_session_file()?;
    r.insert("recent".into(), json!({"inDir": recent_file.as_str().is_some_and(|path| dir_of(path) == dir), "hasEntries": recent.get_entry_count()?.as_u64().unwrap_or(0) > 0, "cwd": recent.get_cwd()?}));

    let mem = SessionManager::in_memory(ctx, Some(cwd), Some(&id("mem-1")), None)?;
    let before = mem.get_leaf_id()?;
    mem.append_message(json!({"role": "user", "content": "x", "timestamp": 1}))?;
    r.insert("memory".into(), json!({
        "id": mem.get_session_id()?, "persisted": mem.is_persisted()?, "file": mem.get_session_file()?, "dir": mem.get_session_dir()?,
        "defaultDir": mem.uses_default_session_dir()?, "leafBefore": before, "count": mem.get_entry_count()?, "cwd": mem.get_cwd()?,
    }));
    let seeded = SessionManager::in_memory(ctx, Some(cwd), None, Some(vec![json!({"type": "message", "id": "e1", "parentId": null, "timestamp": "2026-01-01T00:00:00.000Z", "message": {"role": "user", "content": "seed", "timestamp": 1}})]))?;
    r.insert("seeded".into(), json!({"count": seeded.get_entry_count()?, "leaf": seeded.get_leaf_id()?, "hasId": text(&seeded.get_session_id()?) != ""}));

    let fork_dir = Path::new(other_dir).join("sessions").to_string_lossy().into_owned();
    let forked = SessionManager::fork_from(ctx, &file, other_dir, Some(&fork_dir), Some(&id("forked-1")))?;
    r.insert("fork".into(), json!({
        "id": forked.get_session_id()?, "count": forked.get_entry_count()?, "parent": forked.get_header()?["parentSession"] == json!(file), "cwd": text(&forked.get_cwd()?) == other_dir,
        "inDir": dir_of(text(&forked.get_session_file()?)) == fork_dir, "shape": shape(&forked.get_entries()?),
    }));

    r.insert("list".into(), infos(SessionManager::list(ctx, cwd, Some(dir))?));
    r.insert("listAll".into(), infos(SessionManager::list_all(ctx, Some(dir))?));
    r.insert("listFork".into(), infos(SessionManager::list(ctx, other_dir, Some(&fork_dir))?));
    let found = SessionManager::find_by_id(ctx, cwd, fixed, Some(dir))?;
    r.insert("find".into(), json!({"found": found.as_deref() == Some(file.as_str()), "missing": SessionManager::find_by_id(ctx, cwd, "nope", Some(dir))?.is_none()}));

    r.insert("errors".into(), json!({
        "badId": failure(SessionManager::create(ctx, cwd, Some(dir), Some(&id("bad id!"))), "Session id must be"),
        "notSession": failure(SessionManager::open(ctx, junk, None, None), "not a valid"),
        "noFork": failure(SessionManager::fork_from(ctx, &Path::new(dir).join("missing.jsonl").to_string_lossy(), cwd, Some(&fork_dir), None), "Cannot fork"),
    }));
    Ok(Value::Object(r))
}

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("session-graph");
    ext.tool("session_manager", "Runs the SessionManager script.", empty_schema(), |ctx, args| match script(ctx, &args) {
        Ok(report) => ToolResult::text(report.to_string()),
        Err(error) => ToolResult::Error(error.to_string()),
    });
    ext
}

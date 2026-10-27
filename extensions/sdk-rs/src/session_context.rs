//! Pi's `buildSessionContext` (`core/session-manager.ts`, exported from `index.ts`).

use crate::session_entry_messages::session_entry_to_context_messages;
use serde_json::{Map, Value, json};
use std::collections::HashMap;
use std::io;

/// Pi's `leafId` argument of `buildSessionContext`: omitted (the last entry), `null` (no path: an empty context) or an id (the entry to walk back from; an id no entry has, or
/// the empty string, falls back to the last entry as omitted does).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum SessionLeaf<'a> {
    Last,
    None,
    At(&'a str),
}

/// A `Map` key for an entry id as JavaScript compares it: a string, a number or a boolean, told apart.
#[derive(Hash, PartialEq, Eq)]
enum IdKey<'a> {
    Text(&'a str),
    Number(u64),
    Bool(bool),
}

fn key(value: &Value) -> Option<IdKey<'_>> {
    match value {
        Value::String(text) => Some(IdKey::Text(text)),
        Value::Number(number) => number.as_f64().map(|n| IdKey::Number(if n == 0.0 { 0 } else { n.to_bits() })),
        Value::Bool(flag) => Some(IdKey::Bool(*flag)),
        _ => None,
    }
}

fn truthy(value: Option<&Value>) -> bool {
    match value {
        None | Some(Value::Null) | Some(Value::Bool(false)) => false,
        Some(Value::String(text)) => !text.is_empty(),
        Some(Value::Number(number)) => number.as_f64().is_some_and(|n| n != 0.0 && !n.is_nan()),
        Some(_) => true,
    }
}

/// `a[a_key] === b[b_key]` for the values a decoded entry holds: undefined (a missing member) equals only undefined.
fn same(a: &Value, a_key: &str, b: &Value, b_key: &str) -> bool {
    match (a.get(a_key), b.get(b_key)) {
        (None, None) => true,
        (Some(Value::Null), Some(Value::Null)) => true,
        (Some(left), Some(right)) => key(left).is_some() && key(left) == key(right),
        _ => false,
    }
}

fn entry_type(entry: &Value) -> Option<&str> {
    entry.get("type").and_then(Value::as_str)
}

fn invalid(message: &str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, message.to_string())
}

fn entry_path<'a>(entries: &'a [Value], leaf: SessionLeaf<'_>, by_id: Option<&'a Map<String, Value>>) -> Vec<&'a Value> {
    if leaf == SessionLeaf::None {
        return Vec::new();
    }
    let mut index: HashMap<IdKey<'a>, &'a Value> = HashMap::new();
    if by_id.is_none() {
        for entry in entries {
            if let Some(id) = entry.get("id").and_then(key) {
                index.insert(id, entry);
            }
        }
    }
    let lookup = |value: &Value| -> Option<&'a Value> {
        match by_id {
            Some(map) => value.as_str().and_then(|id| map.get(id)),
            None => key(value).and_then(|id| index.get(&id).copied()),
        }
    };
    let mut current = match leaf {
        SessionLeaf::At(id) if !id.is_empty() => lookup(&Value::String(id.to_string())),
        _ => None,
    };
    if current.is_none() {
        current = entries.last();
    }
    let limit = entries.len() + by_id.map_or(0, Map::len);
    let mut path = Vec::new();
    // A parent chain longer than every entry there is has looped, where Pi would never return.
    while let Some(entry) = current {
        if path.len() > limit {
            break;
        }
        path.push(entry);
        current = if truthy(entry.get("parentId")) { entry.get("parentId").and_then(&lookup) } else { None };
    }
    path.reverse();
    path
}

/// `entry.message.role`: Pi's `TypeError` for a null or missing message, no role for one that is not an object.
fn role(entry: &Value) -> io::Result<Option<&str>> {
    match entry.get("message") {
        None => Err(invalid("TypeError: Cannot read properties of undefined (reading 'role')")),
        Some(Value::Null) => Err(invalid("TypeError: Cannot read properties of null (reading 'role')")),
        Some(message) => Ok(message.get("role").and_then(Value::as_str)),
    }
}

/// `{out: source[in]}` for the members `source` has: `JSON.stringify` drops the ones Pi reads as undefined.
fn members(source: &Value, names: [(&str, &str); 2]) -> Value {
    let mut out = Map::new();
    for (out_name, in_name) in names {
        if let Some(value) = source.get(in_name) {
            out.insert(out_name.to_string(), value.clone());
        }
    }
    Value::Object(out)
}

fn context_entries<'a>(path: Vec<&'a Value>) -> Vec<&'a Value> {
    let Some(compaction) = path.iter().rev().find(|entry| entry_type(entry) == Some("compaction")).copied() else { return path };
    let Some(compaction_index) = path.iter().position(|entry| same(entry, "id", compaction, "id")) else { return path };
    let mut context = vec![compaction];
    let mut found_first_kept = false;
    for entry in &path[..compaction_index] {
        if same(entry, "id", compaction, "firstKeptEntryId") {
            found_first_kept = true;
        }
        let system = entry_type(entry) == Some("message") && entry.get("message").and_then(|m| m.get("role")).and_then(Value::as_str) == Some("system");
        if found_first_kept && !system {
            context.push(entry);
        }
    }
    context.extend(&path[compaction_index + 1..]);
    context
}

fn apply_edit(messages: Vec<Value>, edit: &Value) -> io::Result<Vec<Value>> {
    let replacement = edit.get("replacement");
    if matches!(replacement, Some(Value::Null)) {
        return Ok(Vec::new());
    }
    let mut out = Vec::with_capacity(messages.len());
    for message in messages {
        let message_role = message.get("role").and_then(Value::as_str).map(str::to_string);
        let message_role = message_role.as_deref();
        if !matches!(message_role, Some("user" | "assistant" | "toolResult" | "custom")) {
            out.push(message);
            continue;
        }
        let Some(replacement) = replacement else { return Err(invalid("TypeError: Cannot read properties of undefined (reading 'content')")) };
        let content = replacement.get("content");
        let mut copied = message;
        let object = copied.as_object_mut().expect("a message with a role is an object");
        match content {
            Some(Value::String(text)) if matches!(message_role, Some("assistant" | "toolResult")) => {
                object.insert("content".into(), json!([{"type": "text", "text": text}]));
            }
            Some(content) => {
                object.insert("content".into(), content.clone());
            }
            None => {
                object.remove("content");
            }
        }
        out.push(copied);
    }
    Ok(out)
}

/// Pi's `buildSessionContext`: the model context of a session as the entries on the path from the leaf to the root, with compaction, branch summaries and context edits
/// applied (Pi's `buildSessionProjection` without its provenance). The result has `messages` (the context's messages in order), `thinkingLevel` (the last
/// `thinking_level_change` on the path, `"off"` without one) and `model` (the last `model_change` or assistant message's `{provider, modelId}`, null without one). `by_id`
/// is an index of the entries by id that the walk reads instead of building one from `entries`. A message entry with a null or missing message is Pi's `TypeError`, an
/// `InvalidData` error here.
pub fn build_session_context(entries: &[Value], leaf: SessionLeaf<'_>, by_id: Option<&Map<String, Value>>) -> io::Result<Value> {
    let path = entry_path(entries, leaf, by_id);
    let mut thinking = Some(json!("off"));
    let mut model = Value::Null;
    for entry in &path {
        match entry_type(entry) {
            Some("thinking_level_change") => thinking = entry.get("thinkingLevel").cloned(),
            Some("model_change") => model = members(entry, [("provider", "provider"), ("modelId", "modelId")]),
            Some("message") if role(entry)? == Some("assistant") => model = members(&entry["message"], [("provider", "provider"), ("modelId", "model")]),
            _ => {}
        }
    }
    let context = context_entries(path);
    let mut edits: HashMap<IdKey<'_>, &Value> = HashMap::new();
    for entry in &context {
        if entry_type(entry) == Some("context_edit") {
            if let Some(target) = entry.get("targetId").and_then(key) {
                edits.insert(target, entry);
            }
        }
    }
    let mut messages = Vec::new();
    for (index, entry) in context.iter().enumerate() {
        if entry_type(entry) == Some("compaction") && index > 0 {
            continue;
        }
        let mut projected = session_entry_to_context_messages(entry)?;
        if let Some(edit) = entry.get("id").and_then(key).and_then(|id| edits.get(&id)) {
            projected = apply_edit(projected, edit)?;
        }
        messages.extend(projected);
    }
    let mut result = Map::new();
    result.insert("messages".into(), Value::Array(messages));
    if let Some(thinking) = thinking {
        result.insert("thinkingLevel".into(), thinking);
    }
    result.insert("model".into(), model);
    Ok(Value::Object(result))
}

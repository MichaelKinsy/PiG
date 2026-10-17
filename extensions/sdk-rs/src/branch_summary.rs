//! Pi's branch summarization helpers (`core/compaction/branch-summarization.ts`): the entries a tree navigation leaves behind and the messages and file
//! operations that make of them. The model call that writes the summary is not here.

use serde_json::{Map, Value};
use std::io;

use crate::compaction_estimate::estimate_tokens_with;
use crate::host_helpers::parse_date;
use crate::session_manager::SessionManager;

/// Pi's `FileOperations`: the files tool calls read, wrote and edited. Each is a JavaScript Set kept in insertion order: a value is added once (SameValueZero),
/// and an element is not always a string, because a branch summary's recorded details can hold any JSON value.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct FileOperations {
    pub read: Vec<Value>,
    pub written: Vec<Value>,
    pub edited: Vec<Value>,
}

/// Pi's `BranchPreparation`: the messages to summarize (oldest first), their file operations and their estimated size.
#[derive(Debug, Clone, PartialEq)]
pub struct BranchPreparation {
    pub messages: Vec<Value>,
    pub file_ops: FileOperations,
    pub total_tokens: i64,
}

/// Pi's `CollectEntriesResult`: the entries to summarize (oldest first) and the deepest entry on both the old and the new path.
#[derive(Debug, Clone, PartialEq)]
pub struct CollectEntriesResult {
    pub entries: Vec<Value>,
    pub common_ancestor_id: Option<String>,
}

/// The part of Pi's `ReadonlySessionManager` that [`collect_entries_for_branch_summary`] reads. [`SessionManager`] is one.
pub trait SessionReader {
    fn get_entry(&self, id: &str) -> io::Result<Value>;
    fn get_branch(&self, from_id: Option<&str>) -> io::Result<Value>;
}

impl SessionReader for SessionManager<'_> {
    fn get_entry(&self, id: &str) -> io::Result<Value> {
        SessionManager::get_entry(self, id)
    }
    fn get_branch(&self, from_id: Option<&str>) -> io::Result<Value> {
        SessionManager::get_branch(self, from_id)
    }
}

fn entries_of(value: Value) -> Vec<Value> {
    match value {
        Value::Array(entries) => entries,
        _ => Vec::new(),
    }
}

fn id_of(entry: &Value) -> Option<&str> {
    entry.get("id").and_then(Value::as_str)
}

/// Pi's `collectEntriesForBranchSummary`: the entries from `old_leaf_id` back to its common ancestor with `target_id`, oldest first. A compaction on the way is
/// included, not a boundary. With no old position there is nothing to summarize.
pub fn collect_entries_for_branch_summary(session: &impl SessionReader, old_leaf_id: Option<&str>, target_id: &str) -> io::Result<CollectEntriesResult> {
    let Some(old_leaf_id) = old_leaf_id.filter(|id| !id.is_empty()) else {
        return Ok(CollectEntriesResult { entries: Vec::new(), common_ancestor_id: None });
    };
    let old_branch = entries_of(session.get_branch(Some(old_leaf_id))?);
    let old_path: Vec<&str> = old_branch.iter().filter_map(id_of).collect();
    let target_path = entries_of(session.get_branch(Some(target_id))?);
    let common_ancestor_id = target_path.iter().rev().filter_map(id_of).find(|id| old_path.contains(id)).map(str::to_string);
    let mut entries = Vec::new();
    let mut current = old_leaf_id.to_string();
    while !current.is_empty() && Some(&current) != common_ancestor_id.as_ref() {
        let entry = session.get_entry(&current)?;
        if !entry.is_object() {
            break;
        }
        current = entry.get("parentId").and_then(Value::as_str).unwrap_or("").to_string();
        entries.push(entry);
    }
    entries.reverse();
    Ok(CollectEntriesResult { entries, common_ancestor_id })
}

fn truthy(value: Option<&Value>) -> bool {
    match value {
        None | Some(Value::Null) => false,
        Some(Value::Bool(flag)) => *flag,
        Some(Value::String(text)) => !text.is_empty(),
        Some(Value::Number(number)) => number.as_f64().is_some_and(|n| n != 0.0 && !n.is_nan()),
        Some(_) => true,
    }
}

/// Pi's `prepareBranchEntries`: entries (oldest first) become the messages to summarize, newest first until `token_budget` (0 is no limit) is reached, with the
/// file operations of every entry, including those that did not fit. A branch summary Pi wrote (not one an extension did) contributes the files it recorded. An
/// entry the budget stops at is dropped, unless it is a compaction or a branch summary and the messages so far use under 90% of the budget. Timestamps are
/// `new Date(timestamp).getTime()`, which the host's date parser answers ([`parse_date`]).
pub fn prepare_branch_entries(entries: &[Value], token_budget: i64) -> io::Result<BranchPreparation> {
    prepare(entries, token_budget, &[])
}

fn prepare(entries: &[Value], token_budget: i64, stand_ins: &[char]) -> io::Result<BranchPreparation> {
    let mut newest_first: Vec<Value> = Vec::new();
    let mut file_ops = FileOperations::default();
    let mut total_tokens = 0i64;
    for entry in entries {
        if entry.get("type").and_then(Value::as_str) == Some("branch_summary") && !truthy(entry.get("fromHook")) && truthy(entry.get("details")) {
            let details = &entry["details"];
            if let Some(read) = details.get("readFiles").and_then(Value::as_array) {
                read.iter().for_each(|file| add(&mut file_ops.read, file));
            }
            if let Some(modified) = details.get("modifiedFiles").and_then(Value::as_array) {
                modified.iter().for_each(|file| add(&mut file_ops.edited, file));
            }
        }
    }
    for entry in entries.iter().rev() {
        let Some(message) = message_from_entry(entry)? else { continue };
        extract_file_ops(&message, &mut file_ops);
        let tokens = estimate_tokens_with(&message, stand_ins);
        if token_budget > 0 && total_tokens + tokens > token_budget {
            if matches!(entry.get("type").and_then(Value::as_str), Some("compaction" | "branch_summary")) && (total_tokens as f64) < token_budget as f64 * 0.9 {
                newest_first.push(message);
                total_tokens += tokens;
            }
            break;
        }
        newest_first.push(message);
        total_tokens += tokens;
    }
    newest_first.reverse();
    Ok(BranchPreparation { messages: newest_first, file_ops, total_tokens })
}

/// [`prepare_branch_entries`] over the JSON text of the entries (an array, oldest first) that answers with JSON text, `{"messages":[...],"fileOps":{"read":[],"written":[],
/// "edited":[]},"totalTokens":N}`. A JavaScript string in the entries may hold an unpaired surrogate, which `JSON.stringify` writes as an escape and a `Value` cannot hold;
/// this path keeps every unit, so the messages come back as the entries held them. [`crate::Context::get_entries_json`] and [`crate::Context::get_branch_json`] read such text.
pub fn prepare_branch_entries_json(entries_json: &str, token_budget: i64) -> io::Result<String> {
    let (text, protected) = crate::lossless_json::protect(entries_json);
    let entries: Vec<Value> = match serde_json::from_str(&text) {
        Ok(entries) => entries,
        Err(err) => return Err(io::Error::new(io::ErrorKind::InvalidData, format!("the entries are not a JSON array: {err}"))),
    };
    let prepared = prepare(&entries, token_budget, &protected.stand_in_chars())?;
    let file_ops = serde_json::json!({"read": prepared.file_ops.read, "written": prepared.file_ops.written, "edited": prepared.file_ops.edited});
    let result = serde_json::json!({"messages": prepared.messages, "fileOps": file_ops, "totalTokens": prepared.total_tokens});
    Ok(protected.restore(&result.to_string()))
}

/// Pi's `getMessageFromEntry`: a tool result adds nothing, because its context is in the assistant's call, and neither do the entry types that carry no
/// conversation content.
fn message_from_entry(entry: &Value) -> io::Result<Option<Value>> {
    Ok(match entry.get("type").and_then(Value::as_str) {
        Some("message") => match entry.get("message") {
            Some(message) if message.is_object() => (message.get("role").and_then(Value::as_str) != Some("toolResult")).then(|| message.clone()),
            Some(Value::Null) => return Err(type_error("Cannot read properties of null (reading 'role')")),
            None => return Err(type_error("Cannot read properties of undefined (reading 'role')")),
            Some(_) => return Err(type_error("a session entry's message is not an object")),
        },
        Some("custom_message") => Some(created("custom", entry, &["customType", "content", "display", "details"])),
        Some("branch_summary") => Some(created("branchSummary", entry, &["summary", "fromId"])),
        Some("compaction") => Some(created("compactionSummary", entry, &["summary", "tokensBefore"])),
        _ => None,
    })
}

fn type_error(message: &str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, format!("TypeError: {message}"))
}

/// Pi's `create*Message`: an absent member is `undefined`, which JSON drops. The timestamp is last, as Pi writes it, and NaN is `null` on the wire.
fn created(role: &str, entry: &Value, names: &[&str]) -> Value {
    let mut message = Map::new();
    message.insert("role".to_string(), Value::from(role));
    for name in names {
        if let Some(value) = entry.get(*name) {
            message.insert((*name).to_string(), value.clone());
        }
    }
    message.insert("timestamp".to_string(), date_time(entry));
    Value::Object(message)
}

/// `new Date(entry.timestamp).getTime()`: a string is parsed by the host, a number is clipped to the time range, `null` is the epoch and anything else,
/// including an absent timestamp, is NaN (`null` on the wire).
fn date_time(entry: &Value) -> Value {
    let milliseconds = |ms: f64| if ms.fract() == 0.0 && ms.abs() < 9.0e15 { Value::from(ms as i64) } else { serde_json::Number::from_f64(ms).map_or(Value::Null, Value::Number) };
    match entry.get("timestamp") {
        Some(Value::String(text)) => parse_date(text).ok().flatten().map_or(Value::Null, milliseconds),
        Some(Value::Number(number)) => number.as_f64().filter(|n| n.abs() <= 8.64e15).map_or(Value::Null, |n| milliseconds(n.trunc() + 0.0)),
        Some(Value::Bool(flag)) => Value::from(i64::from(*flag)),
        Some(Value::Null) => Value::from(0),
        _ => Value::Null,
    }
}

/// Pi's `extractFileOpsFromMessage`: the tool calls of an assistant message. Pi also reads the calls a codemode script made from a tool result, but `prepareBranchEntries` never passes one.
fn extract_file_ops(message: &Value, file_ops: &mut FileOperations) {
    match message.get("role").and_then(Value::as_str) {
        Some("assistant") => {
            for block in message.get("content").and_then(Value::as_array).into_iter().flatten() {
                if block.get("type").and_then(Value::as_str) == Some("toolCall") && block.get("arguments").is_some() && block.get("name").is_some() {
                    add_file_op(block.get("name"), block.get("arguments"), file_ops);
                }
            }
        }
        _ => {}
    }
}

fn add_file_op(tool_name: Option<&Value>, arguments: Option<&Value>, file_ops: &mut FileOperations) {
    let Some(path) = arguments.and_then(|arguments| arguments.get("path")).and_then(Value::as_str).filter(|path| !path.is_empty()) else { return };
    let path = Value::from(path);
    match tool_name.and_then(Value::as_str) {
        Some("read") => add(&mut file_ops.read, &path),
        Some("write") => add(&mut file_ops.written, &path),
        Some("edit") => add(&mut file_ops.edited, &path),
        _ => {}
    }
}

/// `Set.prototype.add`: the value joins at the end unless an equal one (SameValueZero) is in. Objects and arrays decoded from JSON are all distinct.
fn add(values: &mut Vec<Value>, value: &Value) {
    let same = |existing: &Value| match (existing, value) {
        (Value::Number(a), Value::Number(b)) => a.as_f64() == b.as_f64(),
        (Value::Array(_) | Value::Object(_), _) | (_, Value::Array(_) | Value::Object(_)) => false,
        _ => existing == value,
    };
    if values.iter().any(same) {
        return;
    }
    values.push(value.clone());
}

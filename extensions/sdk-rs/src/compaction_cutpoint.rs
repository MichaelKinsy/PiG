//! Pi's `findCutPoint` and `findTurnStartIndex` (`core/compaction/compaction.ts`) over the session entries an extension gets (for example from
//! [`crate::SessionManager::get_entries`]), each a decoded JSON object with a `type`.

use crate::compaction_estimate::estimate_tokens;
use serde_json::{Map, Value, json};
use std::borrow::Borrow;

/// Pi's `CutPointResult`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct CutPointResult {
    /// The index of the first entry to keep.
    pub first_kept_entry_index: usize,
    /// The index of the message that starts the turn being split, or `None` (Pi's -1) when no turn is split.
    pub turn_start_index: Option<usize>,
    /// The cut point is not a turn start.
    pub is_split_turn: bool,
}

/// Pi's `sessionEntryToContextMessages` (`core/session-manager.ts`) for the entries the cut point logic reads. Session files are parsed without
/// validation, so a message with null or missing content reads as an empty one.
fn context_messages(entry: &Value) -> Vec<Value> {
    match entry.get("type").and_then(Value::as_str) {
        Some("message") => {
            // A null or missing message, on which Pi throws, has no context message. A message that is not an object is still one: it has no role, so it is
            // neither a cut point nor a turn start and weighs nothing, but it stops the cut's move back over metadata entries.
            let Some(message) = entry.get("message").filter(|message| !message.is_null()) else { return Vec::new() };
            if !message.is_object() {
                return vec![json!({})];
            }
            let role = message.get("role").and_then(Value::as_str).unwrap_or("");
            if message.get("content").is_none_or(Value::is_null) {
                let empty = match role {
                    "system" => Some(json!("")),
                    "user" | "assistant" | "toolResult" => Some(json!([])),
                    _ => None,
                };
                if let Some(content) = empty {
                    let mut copy: Map<String, Value> = message.as_object().cloned().unwrap_or_default();
                    copy.insert("content".into(), content);
                    return vec![Value::Object(copy)];
                }
            }
            vec![message.clone()]
        }
        Some("custom_message") => {
            let content = entry.get("content").filter(|content| !content.is_null()).cloned().unwrap_or_else(|| json!([]));
            vec![json!({"role": "custom", "content": content})]
        }
        Some("branch_summary") => match entry.get("summary").and_then(Value::as_str) {
            Some(summary) if !summary.is_empty() => vec![json!({"role": "branchSummary", "summary": summary})],
            _ => Vec::new(),
        },
        Some("compaction") => {
            let summary = entry.get("summary").and_then(Value::as_str).unwrap_or("");
            let mut messages = Vec::new();
            if let Some(system) = entry.get("systemMessage").filter(|system| system.is_object()) {
                messages.push(system.clone());
            }
            messages.push(json!({"role": "compactionSummary", "summary": summary}));
            messages
        }
        _ => Vec::new(),
    }
}

fn is_compaction(entry: &Value) -> bool {
    entry.get("type").and_then(Value::as_str) == Some("compaction")
}

fn role_in(message: &Value, roles: &[&str]) -> bool {
    message.get("role").and_then(Value::as_str).is_some_and(|role| roles.contains(&role))
}

fn is_turn_start_entry(entry: &Value) -> bool {
    !is_compaction(entry) && context_messages(entry).iter().any(|message| role_in(message, &["user", "bashExecution", "custom", "branchSummary", "compactionSummary"]))
}

/// Pi's `findTurnStartIndex`: the index of the context-visible message that starts the turn holding `entry_index`, or `None` when none starts it at or
/// after `start_index`.
pub fn find_turn_start_index<E: Borrow<Value>>(entries: &[E], entry_index: usize, start_index: usize) -> Option<usize> {
    (start_index..=entry_index.min(entries.len().saturating_sub(1))).rev().find(|&i| is_turn_start_entry(entries[i].borrow()))
}

/// Pi's `findCutPoint`: it walks back from the newest entry before `end_index`, adding estimated message sizes until `keep_recent_tokens` is reached, and cuts
/// at the closest valid cut point at or after that entry (never at a tool result), then moves the cut back over adjacent entries that do not affect the context.
pub fn find_cut_point<E: Borrow<Value>>(entries: &[E], start_index: usize, end_index: usize, keep_recent_tokens: i64) -> CutPointResult {
    let end_index = end_index.min(entries.len());
    let cut_points: Vec<usize> = (start_index..end_index)
        .filter(|&i| {
            let entry: &Value = entries[i].borrow();
            !is_compaction(entry) && context_messages(entry).iter().any(|message| role_in(message, &["user", "assistant", "bashExecution", "custom", "branchSummary", "compactionSummary"]))
        })
        .collect();
    let Some(&first) = cut_points.first() else {
        return CutPointResult { first_kept_entry_index: start_index, turn_start_index: None, is_split_turn: false };
    };
    let mut accumulated_tokens = 0i64;
    let mut cut_index = first;
    for i in (start_index..end_index).rev() {
        let message_tokens: i64 = context_messages(entries[i].borrow()).iter().map(estimate_tokens).sum();
        if message_tokens == 0 {
            continue;
        }
        accumulated_tokens += message_tokens;
        if accumulated_tokens >= keep_recent_tokens {
            cut_index = cut_points.iter().copied().find(|&candidate| candidate >= i).unwrap_or(cut_points[cut_points.len() - 1]);
            break;
        }
    }
    while cut_index > start_index {
        let previous: &Value = entries[cut_index - 1].borrow();
        if is_compaction(previous) || !context_messages(previous).is_empty() {
            break;
        }
        cut_index -= 1;
    }
    let starts_turn = is_turn_start_entry(entries[cut_index].borrow());
    let turn_start_index = if starts_turn { None } else { find_turn_start_index(entries, cut_index, start_index) };
    CutPointResult { first_kept_entry_index: cut_index, turn_start_index, is_split_turn: !starts_turn && turn_start_index.is_some() }
}

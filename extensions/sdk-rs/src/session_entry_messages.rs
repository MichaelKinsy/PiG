//! Pi's `sessionEntryToContextMessages` (`core/session-manager.ts`, exported from `index.ts`).

use serde_json::{Map, Value, json};
use std::io;

/// `new Date(entry.timestamp).getTime()`: `Date.parse` of a string (read by the host), a number's own time value (clipped as `TimeClip` does), and JSON null, the `null`
/// `JSON.stringify` writes for the NaN Pi holds, when it is neither.
fn timestamp(entry: &Value) -> io::Result<Value> {
    Ok(match entry.get("timestamp") {
        Some(Value::String(text)) => crate::parse_date(text)?.map_or(Value::Null, |ms| json!(ms)),
        Some(Value::Number(number)) => match number.as_f64() {
            Some(value) if value.abs() <= 8.64e15 => json!(value.trunc() + 0.0),
            _ => Value::Null,
        },
        _ => Value::Null,
    })
}

fn truthy_object(value: Option<&Value>) -> bool {
    matches!(value, Some(Value::Object(_) | Value::Array(_)))
}

/// Pi's `sessionEntryToContextMessages`: the LLM and runtime messages a session entry contributes to the context. A message entry is its message, where a null or missing
/// content reads as the empty one Pi supplies (an empty string for a system message, an empty array for a user, assistant or toolResult one). A `custom_message` entry is a
/// custom message, a `branch_summary` entry with a summary a `branchSummary` message, and a `compaction` entry a `compactionSummary` message after the system message it
/// carries, if any. Every other entry (a label, a model change, a plain custom entry) is no message. The entry's timestamp string becomes the epoch milliseconds of
/// `Date.parse`, which the host reads, and a timestamp `Date.parse` cannot read is null in the message, as `JSON.stringify` writes the NaN Pi holds. A message entry with a
/// null or missing message is Pi's `TypeError`, an `InvalidData` error here.
pub fn session_entry_to_context_messages(entry: &Value) -> io::Result<Vec<Value>> {
    match entry.get("type").and_then(Value::as_str) {
        Some("message") => {
            let message = match entry.get("message") {
                None | Some(Value::Null) => return Err(io::Error::new(io::ErrorKind::InvalidData, "TypeError: Cannot read properties of undefined (reading 'role')")),
                Some(message) => message,
            };
            let Value::Object(object) = message else { return Ok(vec![message.clone()]) };
            if object.get("content").is_none_or(Value::is_null) {
                let content = match object.get("role").and_then(Value::as_str) {
                    Some("system") => Some(json!("")),
                    Some("user" | "assistant" | "toolResult") => Some(json!([])),
                    _ => None,
                };
                if let Some(content) = content {
                    let mut copy = object.clone();
                    copy.insert("content".into(), content);
                    return Ok(vec![Value::Object(copy)]);
                }
            }
            Ok(vec![message.clone()])
        }
        Some("custom_message") => {
            let mut message = Map::new();
            message.insert("role".into(), json!("custom"));
            if let Some(custom_type) = entry.get("customType") {
                message.insert("customType".into(), custom_type.clone());
            }
            message.insert("content".into(), entry.get("content").filter(|content| !content.is_null()).cloned().unwrap_or_else(|| json!([])));
            for key in ["display", "details"] {
                if let Some(value) = entry.get(key) {
                    message.insert(key.into(), value.clone());
                }
            }
            message.insert("timestamp".into(), timestamp(entry)?);
            Ok(vec![Value::Object(message)])
        }
        Some("branch_summary") => {
            let Some(summary) = entry.get("summary").and_then(Value::as_str).filter(|summary| !summary.is_empty()) else { return Ok(Vec::new()) };
            let mut message = Map::new();
            message.insert("role".into(), json!("branchSummary"));
            message.insert("summary".into(), json!(summary));
            if let Some(from_id) = entry.get("fromId") {
                message.insert("fromId".into(), from_id.clone());
            }
            message.insert("timestamp".into(), timestamp(entry)?);
            Ok(vec![Value::Object(message)])
        }
        Some("compaction") => {
            let mut summary = Map::new();
            summary.insert("role".into(), json!("compactionSummary"));
            summary.insert("summary".into(), entry.get("summary").cloned().unwrap_or(Value::Null));
            if let Some(tokens) = entry.get("tokensBefore") {
                summary.insert("tokensBefore".into(), tokens.clone());
            }
            summary.insert("timestamp".into(), timestamp(entry)?);
            let summary = Value::Object(summary);
            Ok(match entry.get("systemMessage") {
                system @ Some(_) if truthy_object(system) => vec![system.unwrap().clone(), summary],
                _ => vec![summary],
            })
        }
        _ => Ok(Vec::new()),
    }
}

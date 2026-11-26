//! Pi's `convertToLlm` (`core/messages.ts:148`, exported from `index.ts`): the coding agent's custom message roles become user messages for the model.

use serde_json::{Map, Value, json};

use crate::compaction_estimate::{js_number_string, js_truthy};

const COMPACTION_SUMMARY_PREFIX: &str = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n";
const COMPACTION_SUMMARY_SUFFIX: &str = "\n</summary>";
const BRANCH_SUMMARY_PREFIX: &str =
    "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n";
const BRANCH_SUMMARY_SUFFIX: &str = "</summary>";

/// Pi's `convertToLlm`: `bashExecution`, `custom`, `branchSummary` and `compactionSummary` messages become user messages, `system`, `user`, `assistant` and
/// `toolResult` messages are returned as they are, and a `bashExecution` message with `excludeFromContext` set, or a message with any other role, is
/// dropped. A message is the flat role-discriminated JSON object the wire carries. A message that is not an object has no role and is dropped; Pi throws a
/// TypeError on `null` there, which a typed slice cannot express.
pub fn convert_to_llm(messages: &[Value]) -> Vec<Value> {
    messages.iter().filter_map(convert_message).collect()
}

fn convert_message(message: &Value) -> Option<Value> {
    match message.get("role").and_then(Value::as_str).unwrap_or("") {
        "bashExecution" => {
            if js_truthy(&message["excludeFromContext"]) {
                return None;
            }
            Some(user_text_message(message, bash_execution_to_text(message)))
        }
        "custom" => {
            let mut converted = Map::new();
            converted.insert("role".into(), json!("user"));
            match message.get("content") {
                Some(Value::String(text)) => {
                    converted.insert("content".into(), json!([{ "type": "text", "text": text }]));
                }
                Some(content) => {
                    converted.insert("content".into(), content.clone());
                }
                None => {}
            }
            copy_timestamp(&mut converted, message);
            Some(Value::Object(converted))
        }
        "branchSummary" => Some(user_text_message(
            message,
            format!(
                "{BRANCH_SUMMARY_PREFIX}{}{BRANCH_SUMMARY_SUFFIX}",
                js_to_string(message.get("summary"))
            ),
        )),
        "compactionSummary" => Some(user_text_message(
            message,
            format!(
                "{COMPACTION_SUMMARY_PREFIX}{}{COMPACTION_SUMMARY_SUFFIX}",
                js_to_string(message.get("summary"))
            ),
        )),
        "system" | "user" | "assistant" | "toolResult" => Some(message.clone()),
        _ => None,
    }
}

fn user_text_message(source: &Value, text: String) -> Value {
    let mut converted = Map::new();
    converted.insert("role".into(), json!("user"));
    converted.insert("content".into(), json!([{ "type": "text", "text": text }]));
    copy_timestamp(&mut converted, source);
    Value::Object(converted)
}

/// Carries `m.timestamp` over; an undefined timestamp is absent from the JSON a message travels as.
fn copy_timestamp(converted: &mut Map<String, Value>, source: &Value) {
    if let Some(timestamp) = source.get("timestamp") {
        converted.insert("timestamp".into(), timestamp.clone());
    }
}

/// Pi's `bashExecutionToText` (`core/messages.ts:76-92`).
fn bash_execution_to_text(message: &Value) -> String {
    let mut text = format!("Ran `{}`\n", js_to_string(message.get("command")));
    if js_truthy(&message["output"]) {
        text += &format!("```\n{}\n```", js_to_string(message.get("output")));
    } else {
        text += "(no output)";
    }
    if js_truthy(&message["cancelled"]) {
        text += "\n\n(command cancelled)";
    } else if let Some(exit_code) = message
        .get("exitCode")
        .filter(|code| !code.is_null() && code.as_f64() != Some(0.0))
    {
        text += &format!(
            "\n\nCommand exited with code {}",
            js_to_string(Some(exit_code))
        );
    }
    if js_truthy(&message["truncated"]) {
        if js_truthy(&message["fullOutputPath"]) {
            text += &format!(
                "\n\n[Output truncated. Full output: {}]",
                js_to_string(message.get("fullOutputPath"))
            );
        } else if js_truthy(&message["fullOutputError"]) {
            text += &format!(
                "\n\n[Output truncated. Could not save the full output: {}]",
                js_to_string(message.get("fullOutputError"))
            );
        } else {
            text += "\n\n[Output truncated]";
        }
    }
    text
}

/// The string a template literal or `+` makes of a value decoded from JSON: JavaScript's ToString, or `undefined` for an absent key.
pub(crate) fn js_to_string(value: Option<&Value>) -> String {
    match value {
        None => "undefined".to_string(),
        Some(Value::Null) => "null".to_string(),
        Some(Value::String(text)) => text.clone(),
        Some(Value::Bool(flag)) => flag.to_string(),
        Some(Value::Number(number)) => js_number_to_string(number.as_f64().unwrap_or(f64::NAN)),
        Some(Value::Array(elements)) => elements
            .iter()
            .map(|element| {
                if element.is_null() {
                    String::new()
                } else {
                    js_to_string(Some(element))
                }
            })
            .collect::<Vec<_>>()
            .join(","),
        Some(Value::Object(_)) => "[object Object]".to_string(),
    }
}

/// `Number::toString`, which spells a non-finite number as `NaN`, `Infinity` or `-Infinity` where `JSON.stringify` writes `null`.
pub(crate) fn js_number_to_string(value: f64) -> String {
    if value.is_nan() {
        "NaN".to_string()
    } else if value == f64::INFINITY {
        "Infinity".to_string()
    } else if value == f64::NEG_INFINITY {
        "-Infinity".to_string()
    } else {
        js_number_string(value)
    }
}

#[cfg(test)]
mod tests {
    use super::convert_to_llm;
    use serde_json::json;

    #[test]
    fn bash_execution_truncation_notes() {
        for (msg, want) in [
            (
                json!({"role": "bashExecution", "command": "ls", "output": "a", "truncated": true, "fullOutputPath": "/tmp/out.log"}),
                "[Output truncated. Full output: /tmp/out.log]",
            ),
            (
                json!({"role": "bashExecution", "command": "ls", "output": "a", "truncated": true, "fullOutputError": "ENOSPC: no space left"}),
                "[Output truncated. Could not save the full output: ENOSPC: no space left]",
            ),
            (
                json!({"role": "bashExecution", "command": "ls", "output": "a", "truncated": true}),
                "[Output truncated]",
            ),
        ] {
            let out = convert_to_llm(&[msg]);
            assert_eq!(out.len(), 1);
            let text = out[0]["content"][0]["text"].as_str().unwrap();
            assert!(
                text.ends_with(&format!("\n\n{want}")),
                "text={text:?} want suffix {want:?}"
            );
        }
    }

    #[test]
    fn user_bash_event_result_drops_null_full_output_error() {
        let out = crate::user_bash::user_bash_event_result(Some(json!({
            "result": {
                "output": "ok",
                "exitCode": 0,
                "cancelled": false,
                "truncated": true,
                "fullOutputPath": null,
                "fullOutputError": null
            }
        })))
        .unwrap();
        let result = out["result"].as_object().unwrap();
        assert!(!result.contains_key("fullOutputPath"));
        assert!(!result.contains_key("fullOutputError"));
    }
}

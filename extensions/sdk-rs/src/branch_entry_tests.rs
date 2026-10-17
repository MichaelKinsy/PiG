use crate::context::{BranchEntry, ToolCallInfo};
use serde_json::{Value, json};

fn decode(entry: Value) -> Option<BranchEntry> {
    serde_json::from_value(entry).ok()
}

fn call(name: &str, id: &str, args: &str) -> (String, String, String) {
    (name.to_owned(), id.to_owned(), args.to_owned())
}

fn calls(entry: &BranchEntry) -> Vec<(String, String, String)> {
    entry.tool_calls.iter().map(|ToolCallInfo { name, id, args }| call(name, id, args)).collect()
}

// Pi's assistant message: ToolCall blocks carry their arguments as an object (packages/ai/src/types.ts:423-431).
#[test]
fn an_assistant_message_with_pi_tool_calls_is_flattened() {
    let entry = decode(json!({"id":"e2","parentId":"e1","type":"message","message":{"role":"assistant","content":[
        {"type":"thinking","thinking":"hm "},{"type":"text","text":"let me look"},
        {"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a.txt","n":2}},
        {"type":"thinking","thinking":"again"},
        {"type":"toolCall","id":"c2","name":"ls","arguments":"{\"dir\":\".\"}"},
        {"type":"toolCall","id":"c3","name":"pwd","arguments":null},
        {"type":"tool_use","id":"c4","name":"bash","input":{"command":"ls"}},
        {"type":"text","text":" now"}],
        "provider":"p","model":"m","usage":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"totalTokens":10,"cost":{"total":0.5}}}}))
    .unwrap();
    assert_eq!((entry.id.as_str(), entry.entry_type.as_str(), entry.role.as_str()), ("e2", "message", "assistant"));
    assert_eq!((entry.content.as_str(), entry.thinking.as_str()), ("let me look now", "hm again"));
    assert_eq!(
        calls(&entry),
        vec![
            call("read", "c1", r#"{"path":"a.txt","n":2}"#),
            call("ls", "c2", r#"{"dir":"."}"#),
            call("pwd", "c3", ""),
            call("bash", "c4", r#"{"command":"ls"}"#),
        ]
    );
    assert_eq!((entry.provider.as_str(), entry.model.as_str()), ("p", "m"));
    let usage = entry.usage.unwrap();
    assert_eq!((usage.input, usage.output, usage.cache_read, usage.cache_write, usage.total_tokens), (1, 2, 3, 4, 10));
}

#[test]
fn a_tool_result_and_a_string_message_are_flattened() {
    let result = decode(json!({"id":"e3","type":"message","message":{"role":"toolResult","toolCallId":"c1","toolName":"read","isError":true,"content":[{"type":"text","text":"boom"},{"type":"image","data":"x"}]}})).unwrap();
    assert_eq!((result.role.as_str(), result.content.as_str(), result.tool_call_id.as_str(), result.tool_name.as_str(), result.is_error), ("toolResult", "boom", "c1", "read", true));
    let user = decode(json!({"type":"message","message":{"role":"user","content":"hi there"}})).unwrap();
    assert_eq!((user.role.as_str(), user.content.as_str(), user.id.as_str()), ("user", "hi there", ""));
    let blocks = decode(json!({"type":"message","message":{"role":"user","content":[{"type":"tool_result","text":"t"},{"type":"text","text":"u"}]}})).unwrap();
    assert_eq!(blocks.content, "tu");
}

#[test]
fn compaction_summary_and_custom_entries_take_their_text() {
    let compaction = decode(json!({"id":"e4","type":"compaction","summary":"sum","firstKeptEntryId":"e2","tokensBefore":5})).unwrap();
    assert_eq!((compaction.content.as_str(), compaction.first_kept_entry_id.as_str(), compaction.id.as_str()), ("sum", "e2", "e4"));
    let custom = decode(json!({"type":"custom_message","customType":"x","content":"shown","summary":"ignored"})).unwrap();
    assert_eq!(custom.content, "shown");
    let branch_summary = decode(json!({"type":"branch_summary","summary":"abandoned work"})).unwrap();
    assert_eq!(branch_summary.content, "abandoned work");
    let model_change = decode(json!({"type":"model_change","provider":"p","modelId":"m"})).unwrap();
    assert_eq!((model_change.entry_type.as_str(), model_change.content.as_str(), model_change.model.as_str()), ("model_change", "", ""));
}

// What does not decode is left empty, not guessed: Go leaves a message's fields empty when they do not decode and drops an entry that is not an object.
#[test]
fn an_entry_that_does_not_decode_is_dropped_or_left_empty_as_in_go() {
    assert!(decode(json!("assistant")).is_none());
    assert!(decode(json!([1])).is_none());
    assert!(decode(json!({"id":5,"type":"message"})).is_none());
    assert!(decode(json!({"type":"message","summary":5})).is_none());
    let numeric_role = decode(json!({"id":"e1","type":"message","message":{"role":5,"content":"x"}})).unwrap();
    assert_eq!((numeric_role.id.as_str(), numeric_role.entry_type.as_str(), numeric_role.role.as_str(), numeric_role.content.as_str()), ("e1", "message", "", ""));
    let bad_block = decode(json!({"type":"message","message":{"role":"assistant","provider":"p","content":[{"type":"text","text":5},{"type":"toolCall","id":"c","name":"n","arguments":{}}]}})).unwrap();
    assert_eq!((bad_block.role.as_str(), bad_block.provider.as_str(), bad_block.content.as_str(), bad_block.tool_calls.len()), ("assistant", "p", "", 0));
    let not_an_object = decode(json!({"type":"message","content":"flat","message":"nested?"})).unwrap();
    assert_eq!(not_an_object.content, "flat");
    let odd_content = decode(json!({"type":"message","message":{"role":"user","content":5}})).unwrap();
    assert_eq!((odd_content.role.as_str(), odd_content.content.as_str()), ("user", ""));
    let bad_flag = decode(json!({"type":"message","message":{"role":"tool","isError":"yes","content":"x"}})).unwrap();
    assert_eq!((bad_flag.role.as_str(), bad_flag.content.as_str()), ("", ""));
    let bad_usage = decode(json!({"type":"message","message":{"role":"assistant","content":"x","usage":{"input":"many"}}})).unwrap();
    assert_eq!((bad_usage.role.as_str(), bad_usage.content.as_str(), bad_usage.usage.is_none()), ("", "", true));
    let stray_block = decode(json!({"type":"message","message":{"role":"assistant","content":["stray",{"type":"text","text":"kept?"}]}})).unwrap();
    assert_eq!((stray_block.role.as_str(), stray_block.content.as_str()), ("assistant", ""));
}

// Pi's custom_message content is `string | (TextContent | ImageContent)[]` (session-manager.ts CustomMessageEntry): block content gives the text of its text
// blocks; it used to fail the decode, so the entry vanished from the branch. Go decodes a null block as an empty one.
#[test]
fn a_custom_message_with_block_content_takes_its_text() {
    let custom = decode(json!({"id":"c1","type":"custom_message","customType":"x","content":[{"type":"text","text":"a"},{"type":"image","data":"AA==","mimeType":"image/png"},{"type":"text","text":"b"}],"display":true})).unwrap();
    assert_eq!((custom.id.as_str(), custom.content.as_str()), ("c1", "ab"));
    let images_only = decode(json!({"type":"custom_message","content":[{"type":"image","data":"AA==","mimeType":"image/png"}],"summary":"s"})).unwrap();
    assert_eq!(images_only.content, "s");
    let null_content = decode(json!({"type":"custom_message","content":null,"summary":"s"})).unwrap();
    assert_eq!(null_content.content, "s");
    assert!(decode(json!({"type":"custom_message","content":5})).is_none());
    assert!(decode(json!({"type":"custom_message","content":{"text":"x"}})).is_none());
    let null_block = decode(json!({"type":"message","message":{"role":"user","content":[null,{"type":"text","text":"kept"}]}})).unwrap();
    assert_eq!(null_block.content, "kept");
}

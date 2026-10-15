use pig_sdk::{Extension, ToolResult};
use serde_json::{Value, json};

/// new_extension is the conventional factory entry point.
pub fn new_extension() -> Extension {
    let mut ext = Extension::new("rust-factory");
    ext.tool(
        "hello",
        "Return a hello greeting",
        json!({"type": "object"}),
        |_ctx, _params: Value| ToolResult::Json(json!({"content": "hello from rust"})),
    );
    // A tool with a parameter: the schema tells the model what to pass, the handler checks it, and
    // ToolResult::Error is the tool call's error result.
    ext.tool(
        "count_words",
        "Count the words in a text",
        json!({
            "type": "object",
            "properties": {"text": {"type": "string", "description": "The text to count"}},
            "required": ["text"],
        }),
        |_ctx, params: Value| match params["text"].as_str().filter(|text| !text.is_empty()) {
            Some(text) => ToolResult::Json(json!({"content": format!("{} words", text.split_whitespace().count())})),
            None => ToolResult::Error("text is required".to_string()),
        },
    );
    ext
}

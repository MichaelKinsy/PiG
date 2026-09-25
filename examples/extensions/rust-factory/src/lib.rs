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
    ext
}

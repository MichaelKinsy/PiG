use pig_sdk::{Extension, ToolDefinition, ToolResult};
use serde_json::json;

pub fn new_extension() -> Extension {
    let mut extension = Extension::new("provider-diag");
    let model = |id: &str| json!([{"id": id, "name": id, "reasoning": false, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4096, "maxTokens": 1024}]);
    extension.register_provider("diag-good", json!({"api": "openai-completions", "baseUrl": "https://good.invalid/v1", "apiKey": "key", "models": model("good-model")}));
    extension.register_provider("diag-bad", json!({"baseUrl": "https://bad.invalid/v1", "apiKey": "key", "models": model("bad-model")}));
    extension.register_tool(ToolDefinition::new("diag_ping", "diag_ping", "diag_ping", json!({"type": "object"}), |_ctx, _args: serde_json::Value| ToolResult::text("pong".to_string())));
    extension
}

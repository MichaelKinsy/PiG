use pig_sdk::{CommandResult, Extension};
use serde_json::json;

pub fn new_extension() -> Extension {
    let mut extension = Extension::new("model-call");
    extension.command("model-call", "Make one model call", |ctx, args| {
        let result = ctx.model_registry().complete(
            json!({"provider": args, "modelId": "m", "api": "openai-completions"}),
            json!({"messages": [{"role": "user", "content": "hello", "timestamp": 1}]}),
            json!({"sessionId": "sdk-session"}),
        );
        match result {
            Some(message) if message["stopReason"] == "stop" => CommandResult::Ok,
            other => CommandResult::Error(format!("model call = {other:?}")),
        }
    });
    extension
}

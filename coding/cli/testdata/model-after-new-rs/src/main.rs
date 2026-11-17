//! model-after-new-rs is the Rust SDK realization of testdata/model-after-new.mjs: it registers an extension provider whose models carry
//! metadata no default produces (a window, an output limit, costs, reasoning and an image input) over the server PIG_TEST_MODEL_AFTER_NEW_URL
//! names, and records the model each model_select event carries and the thinking level the extension reads then, as lines of
//! PIG_TEST_REPLACE_LOG. Every Session start runs the factory again in a fresh process.
use pig_sdk::Extension;
use serde_json::{Value, json};
use std::io::Write;

fn record(line: Value) {
    if let Ok(path) = std::env::var("PIG_TEST_REPLACE_LOG")
        && let Ok(mut file) = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(path)
    {
        // One write call per record: Display on a Value writes it in many fragments, which a reader could see half way.
        let _ = file.write_all(format!("{line}\n").as_bytes());
    }
}

// describe is the model the way the other fixtures log it; a provider reaches the extension as its id or as an object carrying it.
fn describe(model: &Value) -> Value {
    let provider = if model["provider"].is_object() {
        &model["provider"]["id"]
    } else {
        &model["provider"]
    };
    json!({
        "id": format!("{}/{}", provider.as_str().unwrap_or_default(), model["id"].as_str().unwrap_or_default()),
        "name": model["name"],
        "contextWindow": model["contextWindow"],
        "maxTokens": model["maxTokens"],
        "reasoning": model["reasoning"],
        "input": model["input"],
        "cost": model["cost"],
    })
}

fn main() {
    let mut ext = Extension::new("model-after-new-rs");
    let cost = json!({"input": 3.25, "output": 16.5, "cacheRead": 0.325, "cacheWrite": 4.0625});
    ext.register_provider(
        "metaprov",
        json!({
            "api": "openai-completions",
            "baseUrl": std::env::var("PIG_TEST_MODEL_AFTER_NEW_URL").unwrap_or_default(),
            "apiKey": "probe-key",
            "models": [
                {"id": "model-a", "name": "Model A", "reasoning": true, "input": ["text", "image"], "cost": cost, "contextWindow": 777000, "maxTokens": 55555},
                {"id": "model-b", "name": "Model B", "reasoning": true, "input": ["text"], "cost": cost, "contextWindow": 555000, "maxTokens": 44444},
            ],
        }),
    );
    ext.on_event("session_start", |_, data| {
        record(json!({"event": "session_start", "reason": data["reason"]}));
        None
    });
    ext.on_event("model_select", |ctx, data| {
        let thinking = ctx.get_thinking_level().unwrap_or_else(|error| format!("error: {error}"));
        record(json!({"event": "model_select", "source": data["source"], "model": describe(&data["model"]), "thinking": thinking}));
        None
    });
    if let Err(error) = ext.run() {
        eprintln!("model-after-new-rs: {error}");
        std::process::exit(1);
    }
}

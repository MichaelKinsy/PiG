//! Fixture for the typed model operation conformance rows (test/extension-conformance/model_types_test.go). It carries the tools of
//! testfixture/modeltypes (Go) and of the inline Python fixture, so one table of rows proves every SDK. Every value a row asserts is one the
//! host or this code produces and no SDK fallback can.

use pig_sdk::{
    APIKeyAuth, AuthResult, Color, Extension, ModelRoute, Provider, ProviderAuth, ProviderOperationFn, ProviderOperations,
    TextAttributes, ThemeAppearance, ThemeSlot, ThemeStyle, ToolResult, VirtualModel, empty_schema,
};
use serde_json::{Value, json};
use std::sync::Arc;
use std::sync::atomic::{AtomicI64, Ordering};

static CANCELLED: AtomicI64 = AtomicI64::new(0);

fn image_impl(model: Arc<Value>, request: Value, options: pig_sdk::ProviderOperationOptions) -> Result<Value, String> {
    let prompt = request["input"][0]["text"].as_str().unwrap_or_default();
    if prompt == "fail" {
        return Err("image failed".into());
    }
    Ok(json!({
        "api": model["api"], "provider": model["provider"], "model": model["id"], "responseId": options.values["apiKey"],
        "output": [{"type": "image", "data": format!("img:{}:{}", model["id"].as_str().unwrap_or_default(), prompt), "mimeType": "image/png"}],
        "stopReason": "stop", "timestamp": 1,
    }))
}

fn classify_impl(model: Arc<Value>, request: Value, options: pig_sdk::ProviderOperationOptions) -> Result<Value, String> {
    let state = request["state"]["text"].as_str().unwrap_or_default();
    match state {
        "fail" => return Err("classifier failed".into()),
        "hang" => {
            options.signal.wait();
            CANCELLED.fetch_add(1, Ordering::SeqCst);
            return Err("classifier cancelled".into());
        }
        _ => {}
    }
    let mut answers = serde_json::Map::new();
    for key in request["questions"].as_object().map(|q| q.keys().rev().cloned().collect::<Vec<_>>()).unwrap_or_default() {
        answers.insert(key, json!({"type": "bool", "probability": state.len() as f64 / 100.0}));
    }
    Ok(json!({"api": model["api"], "provider": model["provider"], "model": model["id"], "answers": answers, "stopReason": "stop", "timestamp": 2}))
}

fn approval_context(state: &str) -> Value {
    json!({
        "state": {"text": state},
        "questions": {
            "tone": {"type": "choice", "instructions": "Which tone?", "criteria": {"warm": "Warm", "cold": "Cold"}},
            "approved": {"type": "bool", "instructions": "Does this express approval?", "criteria": {"true": "Approval", "false": "No approval"}},
        }
    })
}

fn ids(models: &Value) -> Vec<String> {
    models.as_array().map(|list| list.iter().map(|m| format!("{}/{}", m["provider"].as_str().unwrap_or_default(), m["id"].as_str().unwrap_or_default())).collect()).unwrap_or_default()
}

fn pixels() -> Provider {
    let cost = json!({"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0});
    let models = vec![
        Arc::new(json!({"id": "flux", "name": "Flux", "type": "image", "api": "test-images", "input": ["text"], "output": ["image"], "cost": cost, "baseUrl": "https://pixels.test/v1"})),
        Arc::new(json!({"id": "cls", "name": "Cls", "type": "classifier", "api": "test-classifier", "input": ["text"], "contextWindow": 1000, "cost": cost, "baseUrl": "https://pixels.test/v1"})),
    ];
    let unused: pig_sdk::ProviderStreamFn = Arc::new(|_, _, _| Err("unused".into()));
    let generate: ProviderOperationFn = Arc::new(image_impl);
    let classify: ProviderOperationFn = Arc::new(classify_impl);
    Provider {
        id: "pixels".into(),
        name: "Pixels".into(),
        base_url: None,
        headers: None,
        auth: ProviderAuth {
            api_key: Some(APIKeyAuth { name: "Pixels key".into(), check: None, resolve: Arc::new(|_| Ok(Some(AuthResult { auth: json!({"apiKey": "pixels-key"}), env: None, source: None }))), login: None }),
            oauth: None,
        },
        get_models: Arc::new(move || Ok(models.clone())),
        filter_models: None,
        refresh_models: None,
        stream: unused.clone(),
        stream_simple: unused,
        fetch_deferred: None,
        cancel_deferred: None,
        generate_images: Some(generate),
        classify: Some(classify),
    }
}

fn color_from(value: &Value) -> Option<Color> {
    let number = |key: &str| value[key].as_f64().unwrap_or_default();
    match value["kind"].as_str()? {
        "indexed" => Some(Color::Indexed { index: number("index") as u8 }),
        "rgb" => Some(Color::Rgb { r: number("r"), g: number("g"), b: number("b") }),
        "oklch" => Some(Color::Oklch { l: number("l"), c: number("c"), h: number("h") }),
        _ => None,
    }
}

fn color_json(color: &Color) -> Value {
    match color {
        Color::Indexed { index } => json!({"kind": "indexed", "index": index}),
        Color::Rgb { r, g, b } => json!({"kind": "rgb", "r": r, "g": g, "b": b}),
        Color::Oklch { l, c, h } => json!({"kind": "oklch", "l": l, "c": c, "h": h}),
    }
}

fn slot_from(value: &Value) -> Option<ThemeSlot> {
    match value {
        Value::String(token) => Some(ThemeSlot::Token(token.clone())),
        Value::Object(_) => color_from(value).map(ThemeSlot::Color),
        _ => None,
    }
}

fn theme_style_from(options: &Value) -> ThemeStyle {
    let flag = |key: &str| options[key].as_bool().unwrap_or(false);
    ThemeStyle {
        attributes: TextAttributes { bold: flag("bold"), dim: flag("dim"), italic: flag("italic"), underline: flag("underline"), inverse: flag("inverse"), strikethrough: flag("strikethrough") },
        fg: slot_from(&options["fg"]),
        bg: slot_from(&options["bg"]),
    }
}

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("model-types");
    let cost = json!({"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0});
    ext.register_provider_operations(
        "ops",
        json!({
            "baseUrl": "https://ops.test/v1", "apiKey": "ops-key",
            "models": [
                {"id": "flux", "name": "Flux", "type": "image", "api": "test-images", "input": ["text"], "output": ["image", "text"], "cost": cost},
                {"id": "cls", "name": "Cls", "type": "classifier", "api": "test-classifier", "input": ["text"], "contextWindow": 1000, "cost": cost},
            ],
        }),
        ProviderOperations::new().images("test-images", image_impl).classifier("test-classifier", classify_impl),
    );
    ext.register_native_provider(Arc::new(pixels())).unwrap();

    ext.tool("typed_reads", "Reads typed models from the registry.", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        let report = (|| -> std::io::Result<Value> {
            let found = registry.find_of_type("classifier", "typesafe", "jev-latest")?;
            let missing = registry.find_of_type("classifier", "typesafe", "missing")?;
            Ok(json!({
                "classifiers": ids(&registry.get_models_of_type("classifier", None)?),
                "chat": ids(&registry.get_models_of_type("chat", None)?),
                "images": ids(&registry.get_models_of_type("image", Some("openrouter"))?),
                "available": ids(&registry.get_available_of_type("classifier", Some("typesafe"))?),
                "foundWindow": found.map(|model| model["contextWindow"].clone()),
                "missing": missing,
            }))
        })();
        match report {
            Ok(report) => ToolResult::text(report.to_string()),
            Err(error) => ToolResult::Error(error.to_string()),
        }
    });
    ext.tool("classify_probe", "Classifies through the registry.", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        let model = match registry.find_of_type("classifier", "typesafe", "jev-latest") {
            Ok(Some(model)) => model,
            other => return ToolResult::Error(format!("no classifier model: {other:?}")),
        };
        let result = registry.classify(&model, approval_context("Looks good"), Some(json!({"apiKey": "sk-conf"})));
        let failed = registry.classify(&model, approval_context("fail"), None);
        ToolResult::text(json!({
            "stop": result["stopReason"], "answers": result["answers"].as_object().map(|a| a.keys().cloned().collect::<Vec<_>>()),
            "approved": result["answers"]["approved"]["probability"], "model": result["model"],
            "failedStop": failed["stopReason"], "failedMessage": failed["errorMessage"], "failedProvider": failed["provider"],
        }).to_string())
    });
    ext.tool("virtual_probe", "Registers virtual models through the registry.", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        let route = |_: &pig_sdk::Context, _: pig_sdk::ModelRouteRequest| Ok(ModelRoute { model: json!({"provider": "p", "id": "m"}), thinking_level: "off".into(), state: None });
        let mut late = VirtualModel::new("router", "late", "Late", route);
        late.context_window = Some(1000);
        let first = registry.register_virtual_model(late);
        let refused = registry.register_virtual_model(VirtualModel::new("router", "claimed", "Claimed", route));
        let _ = registry.unregister_virtual_model("router", "late");
        ToolResult::text(json!({"first": first.is_ok(), "refused": refused.err().map(|e| e.to_string()).unwrap_or_default().trim()}).to_string())
    });
    ext.tool("theme_probe", "Reads the host's theme through ctx.ui.theme.", empty_schema(), |ctx, args| {
        let theme = ctx.theme();
        let styles: Vec<Value> = args["cases"].as_array().map(|cases| cases.iter().map(|options| match theme.style("x", &theme_style_from(options)) {
            Ok(out) => json!({"ok": out}),
            Err(error) => json!({"error": error}),
        }).collect()).unwrap_or_default();
        let colors = theme.colors();
        let tokens: serde_json::Map<String, Value> = args["tokens"].as_array().map(|tokens| tokens.iter().filter_map(Value::as_str).map(|token| (token.to_string(), colors.get(token).map(color_json).unwrap_or(Value::Null))).collect()).unwrap_or_default();
        let fgs: serde_json::Map<String, Value> = args["fgTokens"].as_array().map(|tokens| tokens.iter().filter_map(Value::as_str).map(|token| (token.to_string(), Value::String(theme.fg(token, "x")))).collect()).unwrap_or_default();
        let appearance = theme.appearance().map(|value| match value { ThemeAppearance::Light => "light", ThemeAppearance::Dark => "dark" });
        ToolResult::text(json!({"appearance": appearance, "colors": tokens, "styles": styles, "fgs": fgs}).to_string())
    });
    ext.tool("late_provider", "Registers a provider with operations after the factory finished.", empty_schema(), |ctx, args| {
        let registry = ctx.model_registry();
        let cost = json!({"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0});
        let operations = ProviderOperations::new()
            .images("late-images", image_impl)
            .classifier("late-classifier", classify_impl)
            .stream_simple(|_, model, _, options| {
                let stream = Arc::new(pig_sdk::ModelEventStream::new());
                let text = format!("late:{}:{}", model["id"].as_str().unwrap_or_default(), options["apiKey"].as_str().unwrap_or_default());
                let message = json!({"role": "assistant", "api": model["api"], "provider": model["provider"], "model": model["id"], "content": [{"type": "text", "text": text}], "stopReason": "stop", "timestamp": 1});
                stream.push(json!({"type": "done", "reason": "stop", "message": message}));
                Ok(stream)
            });
        let config = json!({
            "api": "late-chat-api", "baseUrl": "https://late.test/v1", "apiKey": "late-key",
            "models": [
                {"id": "chat", "name": "Chat", "reasoning": false, "input": ["text"], "cost": cost, "contextWindow": 1000, "maxTokens": 100},
                {"id": "flux", "name": "Flux", "type": "image", "api": "late-images", "input": ["text"], "output": ["image"], "cost": cost},
                {"id": "cls", "name": "Cls", "type": "classifier", "api": "late-classifier", "input": ["text"], "contextWindow": 1000, "cost": cost},
            ],
        });
        let mode = args["mode"].as_str().unwrap_or_default();
        let outcome = (|| -> std::io::Result<()> {
            registry.register_provider_operations("late", config, operations)?;
            if mode == "again" {
                registry.register_provider_operations("late", json!({"baseUrl": "https://late.test/v2"}), ProviderOperations::new())?;
            }
            if mode == "gone" {
                registry.unregister_provider("late")?;
            }
            Ok(())
        })();
        match outcome {
            Ok(()) => ToolResult::text(json!({"mode": mode}).to_string()),
            Err(error) => ToolResult::Error(error.to_string()),
        }
    });
    ext.tool("ops_status", "Reports what the host's provider requests did in the extension.", empty_schema(), |_, _| {
        ToolResult::text(json!({"cancelled": CANCELLED.load(Ordering::SeqCst)}).to_string())
    });
    ext
}

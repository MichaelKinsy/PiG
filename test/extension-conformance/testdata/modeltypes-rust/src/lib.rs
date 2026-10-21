//! Fixture for the typed model operation conformance rows (test/extension-conformance/model_types_test.go). It carries the tools of
//! testfixture/modeltypes (Go) and of the inline Python fixture, so one table of rows proves every SDK. Every value a row asserts is one the
//! host or this code produces and no SDK fallback can.

use pig_sdk::tui;
use pig_sdk::{
    APIKeyAuth, AuthResult, Color, Extension, ModelRoute, Provider, ProviderAuth, ProviderOperationFn, ProviderOperations,
    TextAttributes, ThemeAppearance, Theme, ThemeSlot, ThemeStyle, ToolResult, VirtualModel, empty_schema,
};
use serde_json::{Value, json};
use std::sync::Arc;
use std::sync::atomic::{AtomicI64, Ordering};

static CANCELLED: AtomicI64 = AtomicI64::new(0);

fn strings_of(value: &Value) -> Vec<String> {
    value.as_array().map(|items| items.iter().map(|item| item.as_str().unwrap_or_default().to_string()).collect()).unwrap_or_default()
}

fn apply_all(style: &pig_sdk::StyleFn, samples: &[String]) -> Value {
    json!(samples.iter().map(|sample| style(sample)).collect::<Vec<_>>())
}

fn apply_pairs(style: &pig_sdk::SelectedStyleFn, samples: &[String]) -> Value {
    json!(samples.iter().map(|sample| [style(sample, false), style(sample, true)]).collect::<Vec<_>>())
}

/// pi-coding-agent's theme helpers and key hints over the host's theme and keybindings (tui_theme_probe): the styles of each on the samples the test passes, and
/// the lines highlightCode gets from the host.
fn tui_theme_probe(args: &Value) -> ToolResult {
    let samples = strings_of(&args["samples"]);
    let result = match args["op"].as_str().unwrap_or_default() {
        "markdown" => {
            let theme = pig_sdk::get_markdown_theme();
            let styles = json!({
                "heading": apply_all(&theme.heading, &samples), "link": apply_all(&theme.link, &samples), "linkUrl": apply_all(&theme.link_url, &samples),
                "code": apply_all(&theme.code, &samples), "codeBlock": apply_all(&theme.code_block, &samples), "codeBlockBorder": apply_all(&theme.code_block_border, &samples),
                "quote": apply_all(&theme.quote, &samples), "quoteBorder": apply_all(&theme.quote_border, &samples), "hr": apply_all(&theme.hr, &samples),
                "listBullet": apply_all(&theme.list_bullet, &samples), "bold": apply_all(&theme.bold, &samples), "italic": apply_all(&theme.italic, &samples),
                "underline": apply_all(&theme.underline, &samples), "strikethrough": apply_all(&theme.strikethrough, &samples),
            });
            let highlight: Vec<Value> = args["highlight"].as_array().map(|pairs| pairs.iter().map(|pair| {
                let code = pair[0].as_str().unwrap_or_default();
                json!((theme.highlight_code)(code, pair[1].as_str()))
            }).collect()).unwrap_or_default();
            json!({"styles": styles, "highlight": highlight})
        }
        "select" => {
            let theme = pig_sdk::get_select_list_theme();
            json!({"styles": {
                "selectedPrefix": apply_all(&theme.selected_prefix, &samples), "selectedText": apply_all(&theme.selected_text, &samples),
                "description": apply_all(&theme.description, &samples), "scrollInfo": apply_all(&theme.scroll_info, &samples), "noMatch": apply_all(&theme.no_match, &samples),
            }})
        }
        "settings" => {
            let theme = pig_sdk::get_settings_list_theme();
            json!({"styles": {
                "label": apply_pairs(&theme.label, &samples), "value": apply_pairs(&theme.value, &samples), "description": apply_all(&theme.description, &samples),
                "cursor": theme.cursor, "hint": apply_all(&theme.hint, &samples),
            }})
        }
        "highlight" => {
            if let Some(name) = args["theme"].as_str().filter(|name| !name.is_empty()) {
                if let Err(error) = pig_sdk::init_theme(Some(name)) {
                    return ToolResult::Error(error.to_string());
                }
            }
            json!({"lines": pig_sdk::highlight_code(args["code"].as_str().unwrap_or_default(), args["lang"].as_str())})
        }
        "init_theme" => {
            if let Err(error) = pig_sdk::init_theme(args["theme"].as_str()) {
                return ToolResult::Error(error.to_string());
            }
            let theme = pig_sdk::get_markdown_theme();
            json!({"bold": apply_all(&theme.bold, &samples), "heading": apply_all(&theme.heading, &samples)})
        }
        "key_hint" => {
            let id = args["id"].as_str().unwrap_or_default();
            json!({"keyHint": pig_sdk::key_hint(id, args["description"].as_str().unwrap_or_default()), "keyText": pig_sdk::key_text(id)})
        }
        "raw_key_hint" => json!({"rawKeyHint": pig_sdk::raw_key_hint(args["key"].as_str().unwrap_or_default(), args["description"].as_str().unwrap_or_default())}),
        other => return ToolResult::Error(format!("unknown op {other}")),
    };
    ToolResult::text(result.to_string())
}

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
    let images = request["images"].as_array().map(|images| images.len()).unwrap_or(0);
    let mut answers = serde_json::Map::new();
    for key in request["questions"].as_object().map(|q| q.keys().rev().cloned().collect::<Vec<_>>()).unwrap_or_default() {
        answers.insert(key, json!({"type": "bool", "probability": (state.len() + 10 * images) as f64 / 100.0}));
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
        get_all_models: None,
        filter_all_models: None,
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
        "indexed" => Some(Color::Indexed(number("index") as u8)),
        "rgb" => Some(Color::Rgb { r: number("r"), g: number("g"), b: number("b") }),
        "oklch" => Some(Color::Oklch { l: number("l"), c: number("c"), h: number("h") }),
        _ => None,
    }
}

fn color_json(color: &Color) -> Value {
    match color {
        Color::Indexed(index) => json!({"kind": "indexed", "index": index}),
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
            let of_type = registry.get_model_of_type("classifier", "typesafe", "jev-latest")?;
            let of_type_missing = registry.get_model_of_type("classifier", "typesafe", "missing")?;
            Ok(json!({
                "ofTypeWindow": of_type.map(|model| model["contextWindow"].clone()),
                "ofTypeMissing": of_type_missing,
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
        let mut shown = approval_context("Looks good");
        shown["images"] = json!([{"type": "image", "data": "aW1hZ2U=", "mimeType": "image/png"}]);
        let with_images = registry.classify(&model, shown, None);
        ToolResult::text(json!({
            "stop": result["stopReason"], "answers": result["answers"].as_object().map(|a| a.keys().cloned().collect::<Vec<_>>()),
            "approved": result["answers"]["approved"]["probability"], "imagesApproved": with_images["answers"]["approved"]["probability"], "model": result["model"],
            "failedStop": failed["stopReason"], "failedMessage": failed["errorMessage"], "failedProvider": failed["provider"],
        }).to_string())
    });
    ext.tool("images_probe", "Generates images through the registry.", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        let model = match registry.find_of_type("image", "openrouter", "flux") {
            Ok(Some(model)) => model,
            other => return ToolResult::Error(format!("no image model: {other:?}")),
        };
        let prompt = json!({"input": [{"type": "text", "text": "a red circle"}]});
        let result = registry.generate_images(&model, prompt.clone(), Some(json!({"apiKey": "sk-img"})));
        let mut missing = model.clone();
        missing["id"] = json!("gone");
        let failed = registry.generate_images(&missing, prompt, None);
        ToolResult::text(json!({
            "stop": result["stopReason"], "model": result["model"], "output": result["output"],
            "failedStop": failed["stopReason"], "failedMessage": failed["errorMessage"], "failedModel": failed["model"], "failedOutput": failed["output"],
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
    ext.tool("virtual_visible", "Registers a virtual model and reads the registry on the next line.", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        let route = |_: &pig_sdk::Context, _: pig_sdk::ModelRouteRequest| Ok(ModelRoute { model: json!({"provider": "p", "id": "m"}), thinking_level: "off".into(), state: None });
        let mut seen = VirtualModel::new("router", "seen", "Seen", route);
        seen.context_window = Some(1000);
        if let Err(error) = registry.register_virtual_model(seen) {
            return ToolResult::text(json!({"error": error.to_string()}).to_string());
        }
        let listed = || registry.get_all().ok().and_then(|models| models.as_array().map(|models| models.iter().any(|m| m["provider"] == "router" && m["id"] == "seen"))).unwrap_or(false);
        let (registered, is_listed) = (registry.find("router", "seen").ok().flatten().is_some(), listed());
        let _ = registry.unregister_virtual_model("router", "seen");
        ToolResult::text(json!({"registered": registered, "listed": is_listed, "gone": registry.find("router", "seen").ok().flatten().is_none(), "unlisted": !listed()}).to_string())
    });
    ext.tool("theme_probe", "Reads the host's theme through ctx.ui.theme.", empty_schema(), |ctx, args| {
        let theme = ctx.theme();
        let styles: Vec<Value> = args["cases"].as_array().map(|cases| cases.iter().map(|options| match theme.style("x", &theme_style_from(options)) {
            Ok(out) => json!({"ok": out}),
            Err(error) => json!({"error": error}),
        }).collect()).unwrap_or_default();
        let colors = theme.colors();
        let tokens: serde_json::Map<String, Value> = args["tokens"].as_array().map(|tokens| tokens.iter().filter_map(Value::as_str).map(|token| (token.to_string(), colors.get(token).map(color_json).unwrap_or(Value::Null))).collect()).unwrap_or_default();
        let fgs: serde_json::Map<String, Value> = args["fgTokens"].as_array().map(|tokens| tokens.iter().filter_map(Value::as_str).map(|token| (token.to_string(), Value::String(theme_fg(&theme, token)))).collect()).unwrap_or_default();
        let appearance = theme.appearance().map(|value| match value { ThemeAppearance::Light => "light", ThemeAppearance::Dark => "dark" });
        ToolResult::text(json!({"appearance": appearance, "colors": tokens, "styles": styles, "fgs": fgs, "queries": theme_queries(&theme, &args)}).to_string())
    });
    ext.tool("tui_theme_probe", "Runs pi-coding-agent's theme and key hint helpers.", empty_schema(), |_, args| tui_theme_probe(&args));
    ext.tool("tui_probe", "Reads pi-tui's keybindings and terminal capabilities.", empty_schema(), |_, args| ToolResult::text(tui_probe(&args).to_string()));
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
    register_host_surface(&mut ext);
    ext
}

// theme.fg(token, "x"), or "throw:" and the message when the SDK panics, so one unknown token does not hide the others.
/// `fn()`, or "throw:" and the message when it panics or returns an error.
fn attempt(f: impl FnOnce() -> Result<String, String> + std::panic::UnwindSafe) -> String {
    match std::panic::catch_unwind(f) {
        Ok(Ok(value)) => value,
        Ok(Err(message)) => format!("throw:{message}"),
        Err(payload) => {
            let message = payload.downcast_ref::<String>().cloned().or_else(|| payload.downcast_ref::<&str>().map(|text| text.to_string())).unwrap_or_default();
            format!("throw:{message}")
        }
    }
}

/// The rest of ctx.ui.theme: modifiers, get_fg_ansi/get_bg_ansi/bg for the tokens the test passes, get_color_mode, the border colors and source_path.
fn theme_queries(theme: &Theme, args: &Value) -> Value {
    if args["queries"] != json!(true) {
        return Value::Null;
    }
    let strings = |key: &str| -> Vec<String> { args[key].as_array().map(|items| items.iter().filter_map(Value::as_str).map(String::from).collect()).unwrap_or_default() };
    let tokens = strings("ansiTokens");
    let texts = strings("modifierTexts");
    let by_token = |f: &dyn Fn(&Theme, &str) -> Result<String, String>| -> serde_json::Map<String, Value> {
        tokens.iter().map(|token| {
            let theme = theme.clone();
            let name = token.clone();
            let value = attempt(std::panic::AssertUnwindSafe(|| f(&theme, &name)));
            (token.clone(), Value::String(value))
        }).collect()
    };
    let thinking: serde_json::Map<String, Value> = strings("levels").into_iter().map(|level| {
        let value = attempt(std::panic::AssertUnwindSafe(|| Ok(theme.get_thinking_border_color(&level)("x"))));
        (level, Value::String(value))
    }).collect();
    json!({
        "modifiers": {"bold": theme.bold("x"), "italic": theme.italic("x"), "underline": theme.underline("x"), "inverse": theme.inverse("x"), "strikethrough": theme.strikethrough("x")},
        "modifierTexts": {
            "bold": texts.iter().map(|text| theme.bold(text)).collect::<Vec<_>>(),
            "italic": texts.iter().map(|text| theme.italic(text)).collect::<Vec<_>>(),
            "underline": texts.iter().map(|text| theme.underline(text)).collect::<Vec<_>>(),
            "inverse": texts.iter().map(|text| theme.inverse(text)).collect::<Vec<_>>(),
            "strikethrough": texts.iter().map(|text| theme.strikethrough(text)).collect::<Vec<_>>(),
        },
        "fgAnsi": by_token(&|theme, token| theme.get_fg_ansi(token)),
        "bgAnsi": by_token(&|theme, token| theme.get_bg_ansi(token)),
        "bgs": by_token(&|theme, token| Ok(theme.bg(token, "x"))),
        "mode": theme.get_color_mode(),
        "thinking": thinking,
        "bash": attempt(std::panic::AssertUnwindSafe(|| Ok(theme.get_bash_mode_border_color()("x")))),
        "sourcePath": theme.source_path,
        "sourceInfo": theme.source_info,
    })
}

fn theme_fg(theme: &Theme, token: &str) -> String {
    std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| theme.fg(token, "x"))).unwrap_or_else(|payload| {
        let message = payload.downcast_ref::<String>().cloned().or_else(|| payload.downcast_ref::<&str>().map(|text| text.to_string())).unwrap_or_default();
        format!("throw:{message}")
    })
}

fn strings(value: &Value) -> Vec<String> {
    value.as_array().map(|items| items.iter().filter_map(Value::as_str).map(String::from).collect()).unwrap_or_default()
}

fn tui_caps(caps: tui::TerminalCapabilities) -> Value {
    let images = caps.images.map(|protocol| match protocol { tui::ImageProtocol::Kitty => "kitty", tui::ImageProtocol::ITerm2 => "iterm2" });
    json!({"images": images, "trueColor": caps.true_color, "hyperlinks": caps.hyperlinks})
}

fn color_mode(mode: tui::TerminalColorMode) -> &'static str {
    match mode { tui::TerminalColorMode::TrueColor => "truecolor", tui::TerminalColorMode::Color256 => "256color" }
}

/// tui_probe: what pi-tui's keybindings and capabilities answer once the host published its state (the Go, Node and Python fixtures carry the same tool).
fn tui_probe(args: &Value) -> Value {
    let ids = strings(&args["ids"]);
    let keybindings = tui::get_keybindings();
    let keys: serde_json::Map<String, Value> = ids.iter().map(|id| (id.clone(), json!(keybindings.get_keys(id)))).collect();
    let definitions: serde_json::Map<String, Value> = ids.iter().map(|id| (id.clone(), keybindings.get_definition(id).and_then(|d| d.description.clone()).map(Value::String).unwrap_or(Value::Null))).collect();
    let matches: Vec<Value> = args["inputs"].as_array().map(|inputs| inputs.iter().map(|input| json!(keybindings.matches(input["data"].as_str().unwrap_or_default(), input["id"].as_str().unwrap_or_default()))).collect()).unwrap_or_default();
    let user: serde_json::Map<String, Value> = keybindings.get_user_bindings().into_iter().map(|(id, bound)| (id, json!(bound))).collect();
    let conflicts: Vec<Value> = keybindings.get_conflicts().into_iter().map(|c| json!({"key": c.key, "keybindings": c.keybindings})).collect();
    let mut resolved_ids: Vec<String> = keybindings.get_resolved_bindings().into_iter().map(|(id, _)| id).collect();
    resolved_ids.sort();
    let defaults_table = tui::tui_keybindings();
    let default_keys = |id: &str| -> Vec<String> { defaults_table.iter().find(|(key, _)| key == id).map(|(_, d)| d.default_keys.clone()).unwrap_or_default() };
    let caps = tui::get_capabilities();
    let mut report = json!({
        "keys": keys, "matches": matches, "definitions": definitions, "user": user, "conflicts": conflicts, "resolvedIds": resolved_ids,
        "defaults": {"count": defaults_table.len(), "tui.input.submit": default_keys("tui.input.submit"), "tui.select.confirm": default_keys("tui.select.confirm")},
        "key": {"ctrl": tui::Key::ctrl("x"), "alt": tui::Key::alt(tui::Key::ENTER), "shift": tui::Key::shift(tui::Key::TAB), "escape": tui::Key::ESCAPE, "ctrlShift": tui::Key::ctrl_shift("p")},
        "capabilities": tui_caps(caps), "colorMode": color_mode(tui::get_terminal_color_mode(None)),
    });

    // Install, then put back what the host published.
    let original = (*keybindings).clone();
    tui::set_keybindings(tui::KeybindingsManager::new(vec![("x.probe".to_string(), tui::KeybindingDefinition { default_keys: vec!["ctrl+q".to_string()], description: None })], Vec::new()));
    let mut installed = json!({"keys": tui::get_keybindings().get_keys("x.probe"), "hostKeys": tui::get_keybindings().get_keys("tui.input.submit")});
    let mut rebound = tui::KeybindingsManager::new(vec![("x.probe".to_string(), tui::KeybindingDefinition { default_keys: vec!["ctrl+q".to_string()], description: None })], Vec::new());
    rebound.set_user_bindings(vec![("x.probe".to_string(), vec!["ctrl+w".to_string()])]);
    tui::set_keybindings(rebound);
    let rebound_keys = tui::get_keybindings().get_keys("x.probe");
    tui::set_keybindings(original);
    installed["rebound"] = json!(rebound_keys);
    installed["restored"] = json!(tui::get_keybindings().get_keys("tui.input.submit"));
    report["installed"] = installed;

    tui::set_capabilities(tui::TerminalCapabilities { images: Some(tui::ImageProtocol::Kitty), true_color: false, hyperlinks: false });
    let mut swapped = json!({"capabilities": tui_caps(tui::get_capabilities()), "colorMode": color_mode(tui::get_terminal_color_mode(None))});
    tui::set_capability_overrides(tui::CapabilityOverrides { hyperlinks: Some(true), ..Default::default() });
    swapped["overridden"] = json!(tui::get_capabilities().hyperlinks);
    tui::set_capability_overrides(tui::CapabilityOverrides::default());
    tui::reset_capabilities_cache();
    // detectCapabilities reads the environment, which the host's state does not replace; the probe only proves the call answers.
    let _ = tui::detect_capabilities(Some(&|| false));
    swapped["detected"] = json!(true);
    tui::set_capabilities(caps);
    swapped["restored"] = tui_caps(tui::get_capabilities());
    let before = tui::is_kitty_protocol_active();
    tui::set_kitty_protocol_active(true);
    let after = tui::is_kitty_protocol_active();
    tui::set_kitty_protocol_active(before);
    swapped["kitty"] = json!([before, after, tui::is_kitty_protocol_active()]);
    report["swapped"] = swapped;
    report
}

/// The tools of the host-surface rows (host_surface_test.go): on_theme_change, on_tools_expanded_change, set_widget_renderer, bell, tool_call_index, tool_input and tool_params, extension_source_info, on_loaded and a factory-time events.emit. testfixture/modeltypes (Go), the inline Python fixture and testdata/model-types.mjs carry the same tools.
fn register_host_surface(ext: &mut Extension) {
    use pig_sdk::HostSubscription;
    use std::sync::Mutex;
    use std::time::Duration;

    static THEME_LOG: Mutex<Vec<String>> = Mutex::new(Vec::new());
    static EXPANDED_LOG: Mutex<Vec<bool>> = Mutex::new(Vec::new());
    static FACTORY_HEARD: Mutex<Vec<Value>> = Mutex::new(Vec::new());
    // How many emits the factory's own listener had heard when the factory's emit returned, and whether the factory's emit on "error", which nobody hears, failed.
    static HEARD_AT_EMIT: Mutex<Option<usize>> = Mutex::new(None);
    static ERROR_EMIT_FAILED: Mutex<Option<bool>> = Mutex::new(None);
    // When the factory's own c3:cross listener ran and when the factory's emit on c3:cross returned, in Unix milliseconds: an extension loaded before this one that listens on c3:cross runs first and finishes before emit returns.
    static CROSS_OWN_AT: Mutex<Option<u128>> = Mutex::new(None);
    static CROSS_RETURNED_AT: Mutex<Option<u128>> = Mutex::new(None);
    // The c3:ack emits the factory's listener heard (an extension loaded before this one answers the c3:cross emit, from inside its listener, with a c3:ack emit), and how many it had heard when the c3:cross emit returned.
    static ACK_HEARD: Mutex<Vec<Value>> = Mutex::new(Vec::new());
    static ACK_AT_CROSS: Mutex<Option<usize>> = Mutex::new(None);
    fn now_ms() -> u128 {
        std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_millis()
    }
    static LOADED: Mutex<Option<Value>> = Mutex::new(None);
    static THEME_SUB: Mutex<Option<HostSubscription>> = Mutex::new(None);
    static EXPANDED_SUB: Mutex<Option<HostSubscription>> = Mutex::new(None);

    ext.events()
        .on("c3:factory", |_, data| {
            FACTORY_HEARD.lock().unwrap().push(data.clone());
            Ok(())
        })
        .expect("subscribe c3:factory");
    ext.events().emit("c3:factory", json!({"n": 1, "from": "factory"})).expect("emit c3:factory");
    // Pi's emit is synchronous: the listener has run when emit returns, and an emit on "error" that nobody hears throws in the factory.
    *HEARD_AT_EMIT.lock().unwrap() = Some(FACTORY_HEARD.lock().unwrap().len());
    *ERROR_EMIT_FAILED.lock().unwrap() = Some(ext.events().emit("error", json!({"from": "factory"})).is_err());
    ext.events()
        .on("c3:ack", |_, data| {
            ACK_HEARD.lock().unwrap().push(data.clone());
            Ok(())
        })
        .expect("subscribe c3:ack");
    ext.events()
        .on("c3:cross", |_, _| {
            *CROSS_OWN_AT.lock().unwrap() = Some(now_ms());
            Ok(())
        })
        .expect("subscribe c3:cross");
    ext.events().emit("c3:cross", json!({"from": "factory"})).expect("emit c3:cross");
    *CROSS_RETURNED_AT.lock().unwrap() = Some(now_ms());
    *ACK_AT_CROSS.lock().unwrap() = Some(ACK_HEARD.lock().unwrap().len());
    // The handler takes its time; a request that ran before it finished would read null.
    ext.on_loaded(|ctx| {
        std::thread::sleep(Duration::from_millis(300));
        let expanded = ctx.get_tools_expanded();
        *LOADED.lock().unwrap() = Some(json!({"cwd": ctx.cwd(), "mode": ctx.mode(), "hostCall": expanded.is_ok(), "expanded": expanded.unwrap_or(false)}));
    });

    ext.tool("c3_theme_arm", "c3_theme_arm", empty_schema(), |ctx, _| {
        let subscription = ctx.on_theme_change(|theme| {
            let line = format!("{}|{}", theme.name, theme.fg("accent", "x"));
            THEME_LOG.lock().unwrap().push(line);
        });
        *THEME_SUB.lock().unwrap() = Some(subscription);
        ToolResult::text(json!({"ok": true}).to_string())
    });
    ext.tool("c3_theme_disarm", "c3_theme_disarm", empty_schema(), |_, _| {
        THEME_SUB.lock().unwrap().take();
        ToolResult::text(json!({"ok": true}).to_string())
    });
    ext.tool("c3_theme_log", "c3_theme_log", empty_schema(), |_, _| ToolResult::text(json!({"log": *THEME_LOG.lock().unwrap()}).to_string()));
    ext.tool("c3_expanded_arm", "c3_expanded_arm", empty_schema(), |ctx, _| {
        let subscription = ctx.on_tools_expanded_change(|expanded| EXPANDED_LOG.lock().unwrap().push(expanded));
        *EXPANDED_SUB.lock().unwrap() = Some(subscription);
        ToolResult::text(json!({"ok": true}).to_string())
    });
    ext.tool("c3_expanded_disarm", "c3_expanded_disarm", empty_schema(), |_, _| {
        EXPANDED_SUB.lock().unwrap().take();
        ToolResult::text(json!({"ok": true}).to_string())
    });
    ext.tool("c3_expanded_log", "c3_expanded_log", empty_schema(), |_, _| ToolResult::text(json!({"log": *EXPANDED_LOG.lock().unwrap()}).to_string()));
    ext.tool("c3_widget_arm", "c3_widget_arm", empty_schema(), |ctx, args| {
        let placement = args["placement"].as_str().unwrap_or_default().to_string();
        let live = ctx.clone();
        match ctx.set_widget_renderer("c3w", Some(move |width| vec![format!("w={width}|{}", live.theme().name)]), Some(json!({"placement": placement}))) {
            Ok(()) => ToolResult::text(json!({"ok": true}).to_string()),
            Err(error) => ToolResult::Error(error.to_string()),
        }
    });
    ext.tool("c3_widget_clear", "c3_widget_clear", empty_schema(), |ctx, _| {
        match ctx.set_widget_renderer("c3w", None::<fn(u32) -> Vec<String>>, None) {
            Ok(()) => ToolResult::text(json!({"ok": true}).to_string()),
            Err(error) => ToolResult::Error(error.to_string()),
        }
    });
    ext.tool("c3_bell", "c3_bell", empty_schema(), |ctx, _| {
        let outcome = match ctx.bell() {
            Ok(()) => "<nil>".to_string(),
            Err(error) => error.to_string(),
        };
        ToolResult::text(json!({"error": outcome}).to_string())
    });
    ext.tool("c3_tool_call", "c3_tool_call", empty_schema(), |ctx, _| {
        let params = ctx.tool_params();
        let keys: Vec<String> = params.as_ref().map(|params| params.keys().cloned().collect()).unwrap_or_default();
        let meta = params.as_ref().and_then(|params| params.get("meta")).map(|meta| meta.to_string());
        ToolResult::text(json!({"index": ctx.tool_call_index(), "input": ctx.tool_input(), "keys": keys, "meta": meta}).to_string())
    });
    ext.tool("c3_source", "c3_source", empty_schema(), |ctx, _| ToolResult::text(json!({"info": ctx.extension_source_info()}).to_string()));
    ext.tool("c3_loaded", "c3_loaded", empty_schema(), |_, _| {
        ToolResult::text(json!({"loaded": *LOADED.lock().unwrap(), "factoryHeard": *FACTORY_HEARD.lock().unwrap(), "heardAtEmit": *HEARD_AT_EMIT.lock().unwrap(), "errorEmitFailed": *ERROR_EMIT_FAILED.lock().unwrap(), "crossOwnAt": (*CROSS_OWN_AT.lock().unwrap()).map(|ms| ms as u64), "crossReturnedAt": (*CROSS_RETURNED_AT.lock().unwrap()).map(|ms| ms as u64), "ackHeard": *ACK_HEARD.lock().unwrap(), "ackAtCross": *ACK_AT_CROSS.lock().unwrap()}).to_string())
    });
}

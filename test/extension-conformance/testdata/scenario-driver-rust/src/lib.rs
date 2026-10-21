//! The Rust scenario driver: it registers, through the Rust SDK's public API, the
//! extension the scenario Spec describes (sdkscenarios.Spec). The Go, Node and
//! Python drivers read the same file and do the same, so one scenario table
//! exercises every SDK. Keep each driver a straight reading of the Spec: behavior
//! belongs in the scenario, not here.
use pig_sdk::*;
use serde_json::{Value, json};
use std::collections::HashMap;
use std::sync::Arc;

/// A "${name}" in a string is the answer of the prompt kept as name.
fn substitute(value: Value, vars: &HashMap<String, String>) -> Value {
    match value {
        Value::String(text) => {
            let mut out = String::new();
            let mut rest = text.as_str();
            while let Some(start) = rest.find("${") {
                out.push_str(&rest[..start]);
                let after = &rest[start + 2..];
                match after.find('}') {
                    Some(end) if !after[..end].is_empty() && after[..end].chars().all(|c| c.is_alphanumeric() || c == '_') => {
                        out.push_str(vars.get(&after[..end]).map(String::as_str).unwrap_or(""));
                        rest = &after[end + 1..];
                    }
                    _ => {
                        out.push_str("${");
                        rest = after;
                    }
                }
            }
            out.push_str(rest);
            Value::String(out)
        }
        Value::Array(items) => Value::Array(items.into_iter().map(|item| substitute(item, vars)).collect()),
        Value::Object(fields) => Value::Object(
            fields
                .into_iter()
                .map(|(key, item)| (key, substitute(item, vars)))
                .collect(),
        ),
        other => other,
    }
}

fn parse(text: &str) -> Result<Value, String> {
    serde_json::from_str(text).map_err(|e| e.to_string())
}

fn run_flow(flow: &Value, interaction: &AuthInteraction, options: &LoginOptions) -> Result<Value, String> {
    let mut vars = HashMap::new();
    for step in flow["steps"].as_array().cloned().unwrap_or_default() {
        if step["deviceId"] == json!(true) {
            let id = match &options.get_device_id {
                Some(get) => get()?,
                None => "<none>".to_string(),
            };
            vars.insert(step["as"].as_str().unwrap_or("").to_string(), id);
        } else if let Some(notify) = step["notify"].as_str() {
            (interaction.notify)(substitute(parse(notify)?, &vars))?;
        } else {
            let answer = (interaction.prompt)(parse(step["prompt"].as_str().unwrap_or("{}"))?)?;
            vars.insert(step["as"].as_str().unwrap_or("").to_string(), answer);
        }
    }
    Ok(substitute(parse(flow["result"].as_str().unwrap_or("{}"))?, &vars))
}

fn provider_object(provider: &Value) -> Arc<Provider> {
    let id = provider["id"].as_str().unwrap().to_string();
    let name = provider["name"].as_str().unwrap().to_string();
    let model = Arc::new(json!({
        "id": format!("{id}-model"), "name": format!("{name} model"), "provider": id, "api": "openai-completions",
        "baseUrl": "http://127.0.0.1:9", "reasoning": false, "input": ["text"],
        "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 100
    }));
    let unused: ProviderStreamFn =
        Arc::new(|_, _, _| Err("the scenario provider streams nothing".into()));
    let mut auth = ProviderAuth::default();
    if let Some(spec) = provider.get("apiKey").filter(|spec| spec.is_object()) {
        let login = spec.get("login").filter(|flow| flow.is_object()).cloned();
        auth.api_key = Some(APIKeyAuth {
            name: spec["name"].as_str().unwrap().into(),
            check: spec["checkSource"].as_str().map(|source| {
                let source = source.to_owned();
                Arc::new(move |_: APIKeyAuthInput| {
                    Ok(Some(AuthCheck { kind: "api_key".into(), source: Some(source.clone()) }))
                }) as Arc<dyn Fn(APIKeyAuthInput) -> ProviderResult<Option<AuthCheck>> + Send + Sync>
            }),
            resolve: Arc::new(|input| {
                let key = input
                    .credential
                    .as_ref()
                    .and_then(|credential| credential["key"].as_str())
                    .filter(|key| !key.is_empty())
                    .map(str::to_owned);
                Ok(key.map(|key| AuthResult {
                    auth: json!({"apiKey": format!("resolved-{key}")}),
                    env: None,
                    source: Some("stored credential".into()),
                }))
            }),
            login: login.map(|flow| {
                Arc::new(move |interaction: AuthInteraction| run_flow(&flow, &interaction, &LoginOptions::default()))
                    as Arc<dyn Fn(AuthInteraction) -> ProviderResult<Value> + Send + Sync>
            }),
        });
    }
    if let Some(spec) = provider.get("oauth").filter(|spec| spec.is_object()) {
        let flow = spec["login"].clone();
        let refresh = spec["refresh"].as_str().map(|text| parse(text).unwrap());
        auth.oauth = Some(OAuthAuth {
            name: spec["name"].as_str().unwrap().into(),
            is_subscription: spec["subscription"].as_bool().filter(|set| *set),
            login_label: spec["loginLabel"].as_str().filter(|label| !label.is_empty()).map(str::to_owned),
            login: Arc::new(move |interaction, options| run_flow(&flow, &interaction, &options)),
            refresh: Arc::new(move |mut credential, _| {
                if let Some(Value::Object(fields)) = &refresh {
                    for (key, value) in fields {
                        credential[key] = value.clone();
                    }
                }
                Ok(credential)
            }),
            to_auth: Arc::new(|credential| Ok(json!({"apiKey": credential["access"]}))),
        });
    }
    Arc::new(Provider {
        id,
        name,
        base_url: None,
        headers: None,
        auth,
        get_models: Arc::new(move || Ok(vec![model.clone()])),
        filter_models: None,
        get_all_models: None,
        filter_all_models: None,
        refresh_models: None,
        stream: unused.clone(),
        stream_simple: unused,
        fetch_deferred: None,
        cancel_deferred: None,
        generate_images: None,
        classify: None,
    })
}

/// Records that a tool or command started, for the harness to wait on.
fn mark(name: &str) {
    std::fs::write(format!(".started-{name}"), "").unwrap();
}

fn forever() -> ! {
    loop {
        std::thread::sleep(std::time::Duration::from_secs(3600));
    }
}

fn register_tool(extension: &mut Extension, tool: &Value) {
    let name = tool["name"].as_str().unwrap().to_string();
    let behavior = tool["behavior"].as_str().unwrap().to_string();
    let started = name.clone();
    extension.tool(
        name,
        tool["description"].as_str().unwrap(),
        match tool["schema"].as_str() {
            Some(schema) => parse(schema).unwrap(),
            None => json!({"type": "object", "properties": {"text": {"type": "string"}}}),
        },
        move |ctx, params: Value| match behavior.as_str() {
            "echo" => ToolResult::text(format!("echo:{}", params["text"].as_str().unwrap_or(""))),
            "fail" => ToolResult::error("boom"),
            "crash" => panic!("tool crash"),
            "details" => ToolResult::text("with details").with_details(json!({"count": 2, "nested": {"a": [1, 2]}})),
            "soft-error" => ToolResult::text("soft failure").with_is_error(true),
            "empty" => ToolResult::Json(json!({"content": []})),
            "blocks" => ToolResult::Json(json!({"content": [{"type": "text", "text": "a"}, {"type": "text", "text": "b"}]})),
            "cooperative" => {
                mark(&started);
                while !ctx.is_cancelled() {
                    std::thread::sleep(std::time::Duration::from_millis(10));
                }
                ToolResult::text("cooperative stopped")
            }
            "hang" => {
                mark(&started);
                forever()
            }
            other => ToolResult::error(format!("unknown tool behavior {other}")),
        },
    );
}

/// pick projects the fields of an object the scenario compares.
fn pick(object: &Value, keys: &[&str]) -> Value {
    Value::Object(keys.iter().map(|key| (key.to_string(), object[*key].clone())).collect())
}

fn query(ctx: &Context, name: &str) -> std::io::Result<Value> {
    Ok(match name {
        "idle" => json!(ctx.is_idle()?),
        "pending" => json!(ctx.has_pending_messages()?),
        "system-prompt" => json!(ctx.get_system_prompt()?),
        "thinking" => json!(ctx.get_thinking_level()?),
        "all-tools" => Value::Array(ctx.get_all_tools()?.iter().map(|tool| json!({"name": tool.name, "description": tool.description})).collect()),
        "session-header" => pick(&ctx.session_manager().get_header()?, &["type", "id", "cwd"]),
        "leaf-entry" => pick(&ctx.session_manager().get_leaf_entry()?, &["type", "id"]),
        "session-manager-id" => ctx.session_manager().get_session_id()?,
        other => return Err(std::io::Error::other(format!("unknown query {other}"))),
    })
}

fn registry_query(ctx: &Context, arg: &str) -> std::io::Result<Value> {
    let (query, provider) = arg.split_once(':').unwrap_or((arg, ""));
    let registry = ctx.model_registry();
    let model = json!({"provider": provider, "id": "m"});
    Ok(match query {
        "has-auth" => json!(registry.has_configured_auth(&model)?),
        "using-oauth" => json!(registry.is_using_oauth(&model)?),
        "provider-name" => match registry.get_provider(provider)? {
            Some(found) => json!(found.name),
            None => Value::Null,
        },
        other => return Err(std::io::Error::other(format!("unknown registry query {other}"))),
    })
}

fn action(ctx: &Context, name: &str) -> Result<(), String> {
    match name {
        "abort" => ctx.abort(),
        "shutdown" => ctx.shutdown(),
        "compact" => ctx.compact(json!({"customInstructions": "be brief"})),
        other => return Err(format!("unknown action {other}")),
    }
    Ok(())
}

static FOOTER_SUBSCRIPTIONS: std::sync::Mutex<Vec<BranchChangeSubscription>> = std::sync::Mutex::new(Vec::new());

fn append_line(path: &str, line: &str) {
    use std::io::Write;
    let mut file = std::fs::OpenOptions::new().create(true).append(true).open(path).unwrap();
    writeln!(file, "{line}").unwrap();
}

fn footer_data(ctx: &Context, path: &str) -> CommandResult {
    let data = ctx.footer_data();
    let mut subscriptions = FOOTER_SUBSCRIPTIONS.lock().unwrap();
    if subscriptions.is_empty() {
        let reader = data.clone();
        subscriptions.push(data.on_branch_change(move || append_line(".branch-changes", &reader.git_branch().unwrap_or_else(|| "null".to_string()))));
        data.on_branch_change(|| append_line(".branch-changes", "unsubscribed-callback-ran")).unsubscribe();
    }
    let statuses: serde_json::Map<String, Value> = data.extension_statuses().into_iter().map(|(key, text)| (key, Value::String(text))).collect();
    write(path, &json!({"branch": data.git_branch(), "statuses": statuses, "count": data.available_provider_count()}).to_string())
}

fn late_register(ctx: &Context, kind: &str) -> std::io::Result<()> {
    match kind {
        "command" => ctx.register_command("late-command", "Registered late", |_, args| write(args, "ran late")),
        "shortcut" => ctx.register_shortcut("ctrl+alt+l", "Registered late", |_| write(".shortcut-fired", "late")),
        "flag" => ctx.register_flag("late-flag", FlagOptions { description: "Registered late".into(), flag_type: FlagType::String, default: Some(json!("late")) }),
        "message-renderer" => ctx.register_message_renderer("late-type", |_, _, _, _| Ok(Vec::new())),
        "entry-renderer" => ctx.register_entry_renderer("late-type", |_, _, _, _| Ok(Vec::new())),
        other => Err(std::io::Error::new(std::io::ErrorKind::InvalidInput, format!("unknown late registration {other}"))),
    }
}

fn register_command(extension: &mut Extension, command: &Value) {
    let name = command["name"].as_str().unwrap().to_string();
    let op = command["op"].as_str().unwrap().to_string();
    let arg = command["arg"].as_str().unwrap_or("").to_string();
    extension.command(
        name.clone(),
        command["description"].as_str().unwrap(),
        move |ctx, path| match op.as_str() {
            "noop" => CommandResult::Ok,
            "error" => CommandResult::Error("command boom".to_string()),
            "crash" => panic!("command crash"),
            "ping" => write(path, "pong"),
            "echo" => write(".echo-args", &path),
            "footer-data" => footer_data(ctx, path),
            "late-register" => match late_register(ctx, &arg) {
                Ok(()) => write(path, "registered"),
                Err(error) => CommandResult::Error(error.to_string()),
            },
            "registered-provider" => match ctx.model_registry().get_registered_native_provider(&arg) {
                Ok(provider) => write(path, &provider.map(|p| p.name.clone()).unwrap_or_else(|| "none".into())),
                Err(err) => CommandResult::Error(err.to_string()),
            },
            "flag" => match ctx.get_flag(&arg) {
                Ok(value) => write(path, &serde_json::to_string(&value.unwrap_or(Value::Null)).unwrap()),
                Err(err) => CommandResult::Error(err.to_string()),
            },
            "hang" => {
                mark(&name);
                forever()
            }
            "signal-wait" => {
                mark(&name);
                let signal = ctx.signal().expect("a run is active");
                while !signal.is_cancelled() {
                    std::thread::sleep(std::time::Duration::from_millis(5));
                }
                write(path, "signal aborted while running")
            }
            "signal" => write(
                path,
                match ctx.signal() {
                    None => "none",
                    Some(signal) if signal.is_cancelled() => "aborted",
                    Some(_) => "live",
                },
            ),
            "query" => match query(ctx, &arg) {
                Ok(value) => write(path, &serde_json::to_string(&value).unwrap()),
                Err(err) => CommandResult::Error(err.to_string()),
            },
            "registry" => match registry_query(ctx, &arg) {
                Ok(value) => write(path, &serde_json::to_string(&value).unwrap()),
                Err(err) => CommandResult::Error(err.to_string()),
            },
            "action" => match action(ctx, &arg) {
                Ok(()) => write(path, "called"),
                Err(err) => CommandResult::Error(err),
            },
            "reload-stale" => {
                if let Err(err) = ctx.reload() {
                    return CommandResult::Error(err.to_string());
                }
                let probe = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                    let _ = ctx.cwd();
                }));
                let report = match probe {
                    Ok(()) => "cwd live".to_string(),
                    Err(payload) => format!(
                        "cwd threw: {}",
                        payload.downcast_ref::<String>().cloned().or_else(|| payload.downcast_ref::<&str>().map(|text| text.to_string())).unwrap_or_default()
                    ),
                };
                write(path, &report)
            }
            other => CommandResult::Error(format!("unknown command op {other}")),
        },
    );
}

fn write(path: &str, text: &str) -> CommandResult {
    match std::fs::write(path, text) {
        Ok(()) => CommandResult::Ok,
        Err(err) => CommandResult::Error(err.to_string()),
    }
}

/// One scenario event op: what a registered event handler returns for the payload it received.
fn event_op(op: &str, tag: &str, data: &Value) -> Result<Option<Value>, String> {
    match op {
        "throw" => Err("event boom".to_string()),
        "crash" => panic!("event crash"),
        "block" => Ok(Some(json!({"block": true, "reason": "event blocked"}))),
        "set-content" => Ok(Some(json!({"content": [{"type": "text", "text": "patched"}]}))),
        "set-error" => Ok(Some(json!({"isError": true}))),
        "record" => {
            append_line(".events", &format!("{tag}:{data}"));
            Ok(None)
        }
        "record-counted" => {
            static RECORDED: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
            let n = RECORDED.fetch_add(1, std::sync::atomic::Ordering::SeqCst) + 1;
            append_line(".events", &format!("{tag}@{n}:{data}"));
            Ok(None)
        }
        "set-prompt" => Ok(Some(json!({"systemPrompt": "base prompt"}))),
        "append-prompt" => Ok(Some(json!({"systemPrompt": format!("{}+", data["systemPrompt"].as_str().unwrap_or_default())}))),
        "note" => Ok(Some(json!({"message": {"customType": "note", "content": "hello", "display": true}}))),
        "see-prompt" => Ok(Some(json!({"message": {"customType": "saw", "content": data["prompt"], "display": false}}))),
        "transform" => Ok(Some(json!({"action": "transform", "text": format!("{}!", data["text"].as_str().unwrap_or_default())}))),
        "handled" => Ok(Some(json!({"action": "handled"}))),
        "continue" => Ok(Some(json!({"action": "continue"}))),
        "see-content" => Ok(Some(json!({"details": {"saw": data["content"][0]["text"]}}))),
        _ => Ok(None),
    }
}

pub fn new_extension() -> Extension {
    let spec: Value = parse(&std::fs::read_to_string(".sdk-scenario.json").unwrap()).unwrap();
    let mut extension = Extension::new("scenario-driver-rust");
    for provider in spec["providers"].as_array().cloned().unwrap_or_default() {
        extension.register_native_provider(provider_object(&provider)).unwrap();
    }
    for tool in spec["tools"].as_array().cloned().unwrap_or_default() {
        register_tool(&mut extension, &tool);
    }
    for command in spec["commands"].as_array().cloned().unwrap_or_default() {
        register_command(&mut extension, &command);
    }
    for shortcut in spec["shortcuts"].as_array().cloned().unwrap_or_default() {
        let key = shortcut["key"].as_str().unwrap().to_string();
        let fired = key.clone();
        extension.shortcut(key, shortcut["description"].as_str().unwrap(), move |_ctx| write(".shortcut-fired", &fired));
    }
    for event in spec["events"].as_array().cloned().unwrap_or_default() {
        let op = event["op"].as_str().unwrap_or_default().to_string();
        let tag = event["tag"].as_str().unwrap_or_default().to_string();
        let name = event["name"].as_str().unwrap().to_string();
        if name == EVENT_BEFORE_AGENT_START {
            // The ops read the event as Pi's object, with the prompt and options as they are now.
            extension.on_before_agent_start(move |_ctx, event| {
                event_op(&op, &tag, &event.snapshot().map_err(|e| e.to_string())?)
            });
        } else {
            extension.on_event_result(name, move |_ctx, data| event_op(&op, &tag, data));
        }
    }
    for flag in spec["flags"].as_array().cloned().unwrap_or_default() {
        let description = flag["description"].as_str().unwrap_or("").to_string();
        let flag_type = if flag["type"].as_str() == Some("boolean") { FlagType::Boolean } else { FlagType::String };
        let options = FlagOptions { description, flag_type, default: flag.get("default").cloned() };
        extension.flag(flag["name"].as_str().unwrap(), options);
    }
    extension
}

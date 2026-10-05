use pig_sdk::{
    AutocompleteItem, CommandResult, ConstrainedSampling, Extension, LoginDefinition,
    OAuthCredentialStatus,
    OAuthCredentialStore, OAuthCredentials, OAuthDeviceCodeInfo, OAuthPrompt, OAuthProvider,
    ProjectTrustDecision, ProjectTrustResult, RemoteComponent, RemoteComponentInvalidate,
    RemoteComponentResult, SpriteDefinition, TerminalInputResult, TerminalInputSubscription, ToolRenderShell,
    ToolResult,
};
use serde_json::{Value, json};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicBool, Ordering},
};
use std::{thread, time::Duration};

struct FocusedList {
    items: [&'static str; 3],
    selected: usize,
    disposed: Arc<AtomicBool>,
}

impl RemoteComponent for FocusedList {
    fn render(&self, width: u32) -> Vec<String> {
        let mut lines = vec![format!("focused width={width}")];
        for (index, item) in self.items.iter().enumerate() {
            let prefix = if index == self.selected { "> " } else { "  " };
            lines.push(format!("{prefix}{item}"));
        }
        lines
    }

    fn handle_input(&mut self, data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
        match data.to_string().as_deref() {
            Ok("\u{1b}[A") => self.selected = (self.selected + self.items.len() - 1) % self.items.len(),
            Ok("\u{1b}[B") => self.selected = (self.selected + 1) % self.items.len(),
            Ok("\u{1b}[6~") => self.selected = (self.selected + 2).min(self.items.len() - 1),
            Ok("\r" | "\n") => {
                return Ok(RemoteComponentResult::done(Some(json!(
                    self.items[self.selected]
                ))));
            }
            Ok("\u{1b}") => return Ok(RemoteComponentResult::done(None)),
            _ => {}
        }
        Ok(RemoteComponentResult::pending())
    }

    fn dispose(&mut self) {
        self.disposed.store(true, Ordering::Relaxed);
    }
}

struct TimerFocused {
    frame: Arc<Mutex<u64>>,
    invalidate: Arc<Mutex<Option<RemoteComponentInvalidate>>>,
    stop: Arc<AtomicBool>,
    disposed: Arc<AtomicBool>,
    detached: Arc<AtomicBool>,
    worker: Option<thread::JoinHandle<()>>,
}

impl TimerFocused {
    fn new() -> (Self, Arc<AtomicBool>, Arc<AtomicBool>) {
        let frame = Arc::new(Mutex::new(0));
        let invalidate = Arc::new(Mutex::new(None::<RemoteComponentInvalidate>));
        let stop = Arc::new(AtomicBool::new(false));
        let disposed = Arc::new(AtomicBool::new(false));
        let detached = Arc::new(AtomicBool::new(false));
        let worker = {
            let frame = frame.clone();
            let invalidate = invalidate.clone();
            let stop = stop.clone();
            thread::spawn(move || {
                while !stop.load(Ordering::Acquire) {
                    thread::sleep(Duration::from_millis(20));
                    if stop.load(Ordering::Acquire) {
                        return;
                    }
                    *frame.lock().unwrap() += 1;
                    let callback = invalidate.lock().unwrap().clone();
                    if let Some(callback) = callback {
                        callback();
                    }
                }
            })
        };
        (
            Self {
                frame,
                invalidate,
                stop,
                disposed: disposed.clone(),
                detached: detached.clone(),
                worker: Some(worker),
            },
            disposed,
            detached,
        )
    }
}

impl RemoteComponent for TimerFocused {
    fn render(&self, width: u32) -> Vec<String> {
        vec![format!(
            "timer frame={} width={width}",
            *self.frame.lock().unwrap()
        )]
    }

    fn handle_input(&mut self, data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
        if data == "\r" {
            return Ok(RemoteComponentResult::done(Some(json!(
                *self.frame.lock().unwrap()
            ))));
        }
        Ok(RemoteComponentResult::pending())
    }

    fn set_invalidate(&mut self, invalidate: Option<RemoteComponentInvalidate>) {
        self.detached.store(invalidate.is_none(), Ordering::Release);
        *self.invalidate.lock().unwrap() = invalidate;
    }

    fn dispose(&mut self) {
        self.stop.store(true, Ordering::Release);
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
        self.disposed.store(true, Ordering::Release);
    }
}

fn conformance_login_definition() -> LoginDefinition {
    LoginDefinition {
        brand: vec!["A".repeat(41); 5],
        hero: vec!["A".repeat(32); 14],
        mascot: vec!["A".repeat(16); 14],
        palette: [("A".to_string(), "#123ABC".to_string())]
            .into_iter()
            .collect(),
        name: "Conformance Pig".to_string(),
        description: "Cross-language login fixture".to_string(),
        tagline: "One canonical definition across every SDK".to_string(),
    }
}

fn conformance_sprite_definition() -> SpriteDefinition {
    SpriteDefinition {
        id: "conformance-pig".to_string(),
        name: "Conformance Pig".to_string(),
        tagline: "One canonical sprite across every SDK".to_string(),
        mascot: vec!["A".repeat(16); 14],
        palette: [("A".to_string(), "#123ABC".to_string())]
            .into_iter()
            .collect(),
    }
}

fn main() {
    let mut ext = Extension::new("rust-sdk-fixture");
    let schema_rejected = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        ext.tool("schema-invalid", "Must not register", Value::Null, |_, _| ToolResult::text("bad"));
    })).is_err();
    ext.command("schema-probe", "Report schema rejection", move |ctx, _args| {
        ctx.notify(&format!("schema-rejected:{schema_rejected}"), "info");
        CommandResult::Ok
    });
    ext.flag("flag-true", pig_sdk::FlagOptions::boolean("", true));
    ext.flag("flag-false", pig_sdk::FlagOptions::boolean("", false));
    ext.flag("flag-string", pig_sdk::FlagOptions::string("", "default"));
    ext.flag("flag-empty", pig_sdk::FlagOptions::string("", ""));
    ext.flag("flag-unset", pig_sdk::FlagOptions { description: String::new(), flag_type: pig_sdk::FlagType::String, default: None });
    ext.command("flag-probe", "Report registered flag values", |ctx, _args| {
        let values: Vec<_> = ["flag-true", "flag-false", "flag-string", "flag-empty", "flag-unset", "unregistered"].iter().map(|name| ctx.get_flag(name).unwrap()).collect();
        ctx.notify(&serde_json::to_string(&values).unwrap(), "info");
        CommandResult::Ok
    });
    ext.command("timeout-probe", "Send JavaScript-number timeouts", |ctx, _| {
        for timeout in [0.5, 4294967296.5, 1e21] {
            if let Err(err) = ctx.exec_with_options("timeout-command", &[], &pig_sdk::ExecOptions { timeout: Some(timeout), cwd: None }) {
                return CommandResult::Error(err);
            }
        }
        match ctx.select_with_options("timeout", &["a"], &pig_sdk::DialogOptions { timeout: Some(1500.5) }) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("exec-reject-probe", "Report exec outcomes", |ctx, args| {
        let commands: Vec<String> = match serde_json::from_str(args) {
            Ok(commands) => commands,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        for command in commands {
            match ctx.exec(&command, &[]) {
                Ok(result) => ctx.notify(&format!("resolved:{}", result.exit_code), "info"),
                Err(err) => ctx.notify(&format!("rejected:{err}"), "info"),
            }
        }
        CommandResult::Ok
    });
    ext.command("session-identity", "Read context identity accessors", |ctx, _| {
        let identity = (|| -> std::io::Result<Value> { Ok(json!([ctx.get_session_id()?, ctx.get_session_file()?, ctx.get_leaf_id()?, ctx.get_session_name()?])) })();
        match identity {
            Ok(identity) => { ctx.notify(&identity.to_string(), "info"); CommandResult::Ok }
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("registry-session", "Read registry and session facades", |ctx, _| {
        let run = || -> std::io::Result<()> {
            let s = ctx.session_manager();
            let r = ctx.model_registry();
            let out = json!({
                "cwd":s.get_cwd()?, "dir":s.get_session_dir()?, "id":s.get_session_id()?, "name":s.get_session_name()?, "leaf":s.get_leaf_id()?,
                "entry":s.get_entry("one")?, "missing":s.get_entry("missing")?, "label":s.get_label("one")?,
                "entries":s.get_entries()?, "branch":s.get_branch(Some("one"))?, "tree":s.get_tree()?,
                "contextEntries":s.build_context_entries()?, "projection":s.build_session_projection()?,
                "models":r.get_all()?, "available":r.get_available()?, "status":r.get_provider_auth_status("registry-probe")?,
                "display":r.get_provider_display_name("registry-probe")?, "error":r.get_error()?,
                "config":r.get_registered_provider_config("registry-probe")?, "ids":r.get_registered_provider_ids()?,
                "auth":r.get_provider_auth("registry-probe")?, "apiKey":r.get_api_key_for_provider("registry-probe"),
                "missingKey":r.get_api_key_for_provider("missing"), "refresh":r.refresh(json!({"allowNetwork":false}))?
            });
            ctx.notify(&out.to_string(), "info");
            Ok(())
        };
        match run() { Ok(()) => CommandResult::Ok, Err(error) => CommandResult::Error(error.to_string()) }
    });
    ext.command("session-order", "Read the session as Pi returns it", |ctx, _| {
        let run = || -> std::io::Result<()> {
            let s = ctx.session_manager();
            let mut out = serde_json::Map::new();
            out.insert("getEntries".into(), s.get_entries()?);
            out.insert("getEntry".into(), s.get_entry("a4")?);
            out.insert("getLeafEntry".into(), s.get_leaf_entry()?);
            out.insert("getBranch".into(), s.get_branch(Some("a4"))?);
            out.insert("getChildren".into(), s.get_children(Some("a1"))?);
            out.insert("getTree".into(), s.get_tree()?);
            out.insert("buildContextEntries".into(), s.build_context_entries()?);
            out.insert("buildSessionProjection".into(), s.build_session_projection()?);
            out.insert("buildSessionContext".into(), s.build_session_context()?);
            ctx.notify(&Value::Object(out).to_string(), "info");
            Ok(())
        };
        match run() { Ok(()) => CommandResult::Ok, Err(error) => CommandResult::Error(error.to_string()) }
    });
    ext.message_renderer("conformance-message", |_ctx, message, options, width| {
        let content = message.get("content").and_then(Value::as_str).unwrap_or("");
        if content == "padding-options" {
            return Ok(vec![serde_json::to_string(&options).map_err(|e| e.to_string())?]);
        }
        Ok(vec![format!(
            "renderer:{content}:expanded={}:width={width}",
            options.expanded
        )])
    });
    ext.markdown_transformer(|markdown, context| {
        Some(format!(
            "md:{markdown}:{}:streaming={}:width={}",
            context.message_type, context.is_streaming, context.available_width
        ))
    });
    fn resolved_line(prefix: &'static str, tool: &str) -> pig_sdk::ToolRendererSet {
        let tool = tool.to_string();
        pig_sdk::ToolRendererSet::with_call(move |_ctx, args, _render, _width| {
            let q = args.get("q").and_then(Value::as_str).unwrap_or("");
            Ok(vec![format!("{prefix}:{tool}:{q}")])
        })
    }
    ext.tool_renderer(|tool, next| match tool {
        "conformance_tool_renderer" => Some(resolved_line("resolved", tool)),
        "conformance_no_renderer" => None,
        "conformance_fill" => next().or_else(|| Some(resolved_line("filled", tool))),
        "conformance_wrap" => {
            let mut wrapped = next()?;
            wrapped.render_call = resolved_line("wrapped", tool).render_call;
            Some(wrapped)
        }
        _ => next(),
    });
    ext.command("late_tool_renderer", "Register a tool renderer resolver after loading", |ctx, _args| {
        match ctx.register_tool_renderer(|tool, next| if tool == "conformance_late" { Some(resolved_line("late", tool)) } else { next() }) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.entry_renderer("conformance-entry", |_ctx, entry, options, width| {
        let data = entry.get("data").and_then(Value::as_str).unwrap_or("");
        Ok(vec![format!(
            "entryrenderer:{data}:expanded={}:width={width}",
            options.expanded
        )])
    });
    ext.tool(
        "render_probe",
        "Render its own tool card",
        json!({"type": "object", "properties": {}}),
        |_ctx, _params| ToolResult::text("render ok"),
    );
    ext.tool_render_shell("render_probe", ToolRenderShell::SelfShell);
    ext.render_tool_call("render_probe", |_ctx, args, render, width| {
        let calls = render.state.get("calls").and_then(Value::as_u64).unwrap_or(0) + 1;
        render.state.insert("calls".to_string(), json!(calls));
        let topic = args.get("topic").and_then(Value::as_str).unwrap_or("");
        Ok(vec![format!(
            "toolrender:call:{topic}:partial={}:calls={calls}:width={width}",
            render.is_partial
        )])
    });
    ext.render_tool_result("render_probe", |_ctx, result, options, render, width| {
        let text = result.content[0].get("text").and_then(Value::as_str).unwrap_or("");
        let key = result.details.get("k").and_then(Value::as_str).unwrap_or("");
        let calls = render.state.get("calls").cloned().unwrap_or(Value::Null);
        Ok(vec![format!(
            "toolrender:result:{text}:{key}:expanded={}:calls={calls}:width={width}",
            options.expanded
        )])
    });
    ext.tool(
        "update_tool",
        "Stream two partial results",
        json!({"type": "object", "properties": {}}),
        |ctx, _params: Value| {
            let _ = ctx.on_update(ToolResult::Json(json!({"content": [{"type": "text", "text": "step 1"}]})));
            let _ = ctx.on_update(ToolResult::Json(json!({"content": [{"type": "text", "text": "step 2"}]})));
            ToolResult::Json(json!({"content": [{"type": "text", "text": "done"}]}))
        },
    );
    ext.tool(
        "ordered_details",
        "Return details whose members are not in alphabetical order",
        json!({"type": "object", "properties": {}}),
        |ctx, _params: Value| {
            let _ = ctx.on_update(ToolResult::Json(json!({"content": [{"type": "text", "text": "partial"}], "details": {"zeta": 1.0, "alpha": {"yy": 2, "bb": 3}, "mid": [{"qq": 1, "aa": 2}]}})));
            ToolResult::Json(json!({"content": [{"type": "text", "text": "done"}], "details": {"zeta": 1.0, "alpha": {"yy": 2, "bb": 3}, "mid": [{"qq": 1, "aa": 2}]}}))
        },
    );
    ext.tool(
        "ordered_result",
        "Return a result whose members are not in the declared order",
        json!({"type": "object", "properties": {}}),
        |ctx, _params: Value| {
            let _ = ctx.on_update(ToolResult::Json(json!({"details": {"k": 1}, "content": [{"type": "text", "text": "partial"}]})));
            ToolResult::Json(json!({"details": {"k": 1}, "is_error": true, "content": [{"type": "text", "text": "done"}]}))
        },
    );
    let abort_observed = Arc::new(AtomicBool::new(false));
    let abort_set = abort_observed.clone();
    ext.tool(
        "abort_tool",
        "Wait for the abort signal",
        json!({"type": "object", "properties": {}}),
        move |ctx, _params: Value| {
            let _ = ctx.on_update(ToolResult::Json(json!({"content": [{"type": "text", "text": "waiting"}]})));
            while !ctx.is_cancelled() {
                thread::sleep(Duration::from_millis(10));
            }
            abort_set.store(true, Ordering::SeqCst);
            ToolResult::text("aborted")
        },
    );
    ext.command(
        "abort_probe",
        "Report whether abort_tool saw its abort signal",
        move |ctx, _args| {
            ctx.notify(&format!("abort:{}", abort_observed.load(Ordering::SeqCst)), "info");
            CommandResult::Ok
        },
    );
    ext.tool(
        "rich_tool",
        "Return text, image, and terminate",
        json!({"type": "object", "properties": {}}),
        |_ctx, _params: Value| {
            ToolResult::Json(json!({
                "content": [
                    {"type": "text", "text": "  padded  "},
                    {"type": "image", "data": "aW1n", "mimeType": "image/png"},
                    {"type": "text", "text": "tail\n"}
                ],
                "terminate": true
            }))
        },
    );
    ext.tool(
        "echo",
        "Echo back the input",
        json!({
            "type": "object",
            "required": ["text"],
            "properties": {"text": {"type": "string", "description": "Text to echo"}, "offset": {"type": "number"}}
        }),
        |_ctx, params: Value| {
            let text = params.get("text").and_then(Value::as_str).unwrap_or("");
            ToolResult::text(format!("echo: {text}"))
        },
    );
    ext.tool_with_prepare_arguments(
        "prepared_tool",
        "Transform legacy arguments before execution",
        json!({"type": "object", "required": ["text"], "properties": {"text": {"type": "string"}}}),
        |params| Ok(json!({"text": params.get("legacy").cloned().unwrap_or(Value::Null)})),
        |_ctx, params| {
            let text = params.get("text").and_then(Value::as_str).unwrap_or("");
            ToolResult::text(format!("prepared:{text}"))
        },
    );
    // Reports the order in which calls start: the number of calls that started before it, plus its own argument.
    static STARTED_CALLS: std::sync::atomic::AtomicUsize = std::sync::atomic::AtomicUsize::new(0);
    ext.tool(
        "start_order",
        "Report the order in which calls start",
        json!({"type": "object", "properties": {"n": {"type": "number"}}}),
        |_ctx, params: Value| {
            let number = STARTED_CALLS.fetch_add(1, std::sync::atomic::Ordering::SeqCst) + 1;
            ToolResult::text(format!("start#{number} n={}", params.get("n").map(Value::to_string).unwrap_or_default()))
        },
    );
    ext.tool(
        "tool_error",
        "Return a thrown tool error",
        json!({"type": "object"}),
        |_ctx, _params: Value| ToolResult::Error("tool exploded".to_string()),
    );
    ext.tool(
        "tool_is_error",
        "Return a structured tool error result",
        json!({"type": "object"}),
        |_ctx, _params: Value| {
            ToolResult::Json(json!({"content": "soft tool error", "is_error": true}))
        },
    );
    ext.tool_with_guidelines(
        "guided_tool",
        "Tool with prompt guidelines",
        json!({"type": "object"}),
        vec!["Use guided_tool when the user asks for guided behavior.".to_string()],
        |_ctx, _params: Value| ToolResult::Json(json!({"content": "guided"})),
    );
    ext.tool_prompt_snippet("guided_tool", " \u{feff}Guided\r\n tool\t summary ");
    ext.tool_with_source(
        "sourced_tool",
        "Tool with explicit source",
        json!({"type": "object"}),
        "mcp:test-server",
        vec!["Use sourced_tool to test per-tool source attribution.".to_string()],
        |_ctx, _params: Value| ToolResult::Json(json!({"content": "sourced"})),
    );
    let mut disabled = pig_sdk::ToolDefinition::new("sampling_disabled", "sampling_disabled", "Disable constrained sampling", json!({"type":"object"}), |_, _| ToolResult::text("disabled"));
    disabled.constrained_sampling = Some(pig_sdk::ToolConstrainedSampling::Disabled);
    ext.register_tool(disabled);
    ext.tool_with_constrained_sampling(
        "grammar_tool",
        "Tool with a grammar constrained sampling request",
        json!({"type": "object"}),
        ConstrainedSampling {
            kind: "grammar".to_string(),
            strict: None,
            variants: Some(
                [("openai_lark".to_string(), "start: NUMBER".to_string())]
                    .into_iter()
                    .collect(),
            ),
        },
        |_ctx, _params: Value| ToolResult::Json(json!({"content": "grammar"})),
    );
    ext.command("complete_probe", "Complete its arguments", |_ctx, _args| CommandResult::Ok);
    ext.command_argument_completions("complete_probe", |prefix| {
        let items: Vec<AutocompleteItem> = [
            AutocompleteItem { value: "alpha".into(), label: Some("alpha — first".into()), description: None },
            AutocompleteItem { value: "apple".into(), label: None, description: Some("fruit".into()) },
            AutocompleteItem { value: "beta".into(), label: None, description: None },
        ]
        .into_iter()
        .filter(|item| item.value.starts_with(prefix.trim()))
        .collect();
        (!items.is_empty()).then_some(items)
    });
    ext.command("ping", "Respond with pong", |ctx, _args| {
        ctx.notify("pong", "info");
        CommandResult::Ok
    });
    ext.command("model-stream-probe", "Exercise model streaming", |ctx, _args| {
        let registry = ctx.model_registry();
        let Some(current) = registry.find("conformance", "current") else { return CommandResult::Error("find current returned no model".into()); };
        if current["id"] != "current" { return CommandResult::Error(format!("find current = {current}")); }
        let Some(found) = registry.find("conformance", "declared") else { return CommandResult::Error("find declared returned no model".into()); };
        if found["id"] != "declared" || found["provider"] != "conformance" { return CommandResult::Error(format!("find declared = {found}")); }
        let Some(slash) = registry.find("conformance", "org/model/name") else { return CommandResult::Error("find slash returned no model".into()); };
        if slash["id"] != "org/model/name" { return CommandResult::Error(format!("find slash = {slash}")); }
        let expected_limits = json!({"maxRequestBytes":12345,"images":{"maxPerMessage":7,"maxPerRequest":11,"resize":{"maxWidth":321,"maxHeight":123,"maxBytes":45678,"jpegQuality":67}}});
        if slash["inputLimits"] != expected_limits || ctx.get_model_info().unwrap().and_then(|model| model.input_limits) != Some(expected_limits) { return CommandResult::Error(format!("model inputLimits = {slash}")); }
        for field in ["baseUrl", "input", "cost", "thinkingLevelMap", "promptCache", "contextWindow", "maxTokens", "samplingParams", "headers", "compat"] {
            if slash.get(field).is_none() { return CommandResult::Error(format!("find slash missing {field}: {slash}")); }
        }
        if slash["input"].as_array().map(Vec::len) != Some(0) || slash["cost"]["input"] != 0 || slash["cost"]["tiers"].as_array().map(Vec::len) != Some(1) || slash["compat"]["supportsStrictMode"] != false { return CommandResult::Error(format!("find slash shape = {slash}")); }
        if registry.find("conformance", "missing").is_some() { return CommandResult::Error("find missing returned a model".into()); }
        if registry.find("conformance", "override-only").is_some() { return CommandResult::Error("find override-only returned a model".into()); }
        let auth = match registry.get_api_key_and_headers(&found) {
            Ok(value) => value,
            Err(error) => return CommandResult::Error(format!("get auth: {error}")),
        };
        let expected_auth = json!({"ok":true,"apiKey":"conformance-key","headers":{"X-Conformance-Auth":"yes"},"baseUrl":"https://models.invalid/v1","env":{"CONFORMANCE_AUTH":"yes"}});
        if auth != expected_auth { return CommandResult::Error(format!("auth = {auth}")); }
        let model = json!({"provider":"conformance", "modelId":"declared", "api":"openai-responses"});
        let request = json!({
            "systemPrompt":"conformance-system",
            "messages":[
                {"role":"system", "content":[{"type":"text", "text":"signed system", "textSignature":"system-signature"}], "sections":{"zeta":"last-first","alpha":null,"middle":"middle"}, "timestamp":41},
                {"role":"user", "content":"hello", "timestamp":42},
                {"role":"assistant", "content":[{"type":"text", "text":"prior", "textSignature":"signed"}], "api":"openai-responses", "provider":"prior-provider", "model":"prior-model", "usage":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"totalTokens":10,"cost":{"input":0.1,"output":0.2,"cacheRead":0.3,"cacheWrite":0.4,"total":1.0}}, "stopReason":"stop", "timestamp":43}
            ],
            "tools":[{"name":"lookup", "description":"lookup", "parameters":{"type":"object"}, "constrainedSampling":{"type":"grammar","variants":{"openai_lark":"start: NUMBER"}}}]
        });
        let options = json!({
            "timeoutMs":0,"websocketConnectTimeoutMs":1234,"maxRetries":2,"maxRetryDelayMs":3000,
            "maxTokens":321, "temperature":0.65, "samplingParams":{"topP":0.8},
            "thinkingBudgets":{"minimal":11,"low":22,"medium":33,"high":44}, "reasoning":"high", "isReasoning":true,
            "env":{"WIRE_ENV":"request-value","SECOND_ENV":"distinct-value"}, "headers":{"X-Wire":"yes","X-Remove":null}, "sessionId":"conformance-session", "transport":"sse"
        });
        let stream = ctx.model_registry().stream(model.clone(), request.clone(), options.clone());
        let mut types = Vec::new();
        while let Some(event) = stream.next() { types.push(event["type"].as_str().unwrap_or_default().to_string()); }
        if types.join(",") != "start,text_start,text_delta,text_end,done" { return CommandResult::Error(format!("stream events = {types:?}")); }
        let result = stream.result().unwrap_or_default();
        if result["content"][0]["text"] != "streamed" { return CommandResult::Error(format!("stream result = {result}")); }
        let simple = ctx.model_registry().stream_simple(model.clone(), request.clone(), options.clone());
        types.clear();
        while let Some(event) = simple.next() { types.push(event["type"].as_str().unwrap_or_default().to_string()); }
        if types.join(",") != "start,text_start,text_delta,text_end,done" { return CommandResult::Error(format!("simple events = {types:?}")); }
        if simple.result().unwrap_or_default()["stopReason"] != "stop" { return CommandResult::Error("simple did not stop".into()); }
        if ctx.model_registry().complete(model.clone(), request.clone(), options.clone()).unwrap_or_default()["stopReason"] != "stop" { return CommandResult::Error("complete did not stop".into()); }
        if ctx.model_registry().stream(model, request.clone(), options.clone()).result().unwrap_or_default()["stopReason"] != "stop" { return CommandResult::Error("result without iteration did not stop".into()); }
        let unknown = ctx.model_registry().complete(json!({"provider":"conformance", "modelId":"unknown", "api":"openai-responses"}), request.clone(), options.clone()).unwrap_or_default();
        if unknown["stopReason"] != "error" || !unknown["errorMessage"].as_str().unwrap_or_default().contains("unknown model") { return CommandResult::Error(format!("unknown result = {unknown}")); }
        let mut transport_error = ctx.model_registry().complete(json!({"provider":"conformance", "modelId":"protocol-error", "api":"openai-responses"}), request, options).unwrap_or_default();
        let timestamp = transport_error.get("timestamp").and_then(Value::as_u64).unwrap_or_default();
        if timestamp == 0 { return CommandResult::Error(format!("transport timestamp = {}", transport_error["timestamp"])); }
        transport_error.as_object_mut().map(|value| value.remove("timestamp"));
        let expected_transport = json!({
            "role":"assistant", "content":[], "api":"openai-responses", "provider":"conformance", "model":"protocol-error",
            "usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},
            "stopReason":"error", "errorMessage":"transport boom"
        });
        if transport_error != expected_transport { return CommandResult::Error(format!("transport result = {transport_error}")); }
        ctx.notify("model-stream=ok", "info");
        CommandResult::Ok
    });
    ext.command(
        "liveness_host_call",
        "Exercise an awaited host call",
        |ctx, _args| match ctx.wait_for_idle() {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "liveness_user_call",
        "Exercise an interactive host call",
        |ctx, _args| match ctx.input("Question", "Answer") {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "liveness_fire_call",
        "Exercise a no-result UI host call",
        |ctx, _args| {
            ctx.set_title("Conformance title");
            CommandResult::Ok
        },
    );
    ext.command("command_error", "Return a command error", |_ctx, _args| {
        CommandResult::Error("command exploded".to_string())
    });
    ext.command(
        "command_awaited_error",
        "Return an error after awaited work",
        |_ctx, _args| {
            thread::sleep(Duration::from_millis(150));
            CommandResult::Error("awaited command exploded".to_string())
        },
    );
    ext.command("surface_footer", "Install a footer renderer", |ctx, _args| {
        match ctx.set_footer_renderer(Some(|width: u32| vec![format!("footer@{width}")])) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("surface_header", "Install a header renderer", |ctx, _args| {
        match ctx.set_header_renderer(Some(|width: u32| vec![format!("header@{width}")])) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("surface_static_footer", "Push static footer rows", |ctx, _args| {
        match ctx.set_footer(vec![format!("static@{}", ctx.width())]) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("surface_widget", "Set a string list widget wider than the pane", |ctx, _args| {
        match ctx.set_widget("wide", vec![format!("{} tail", "A".repeat(60)), "short".to_string()]) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command(
        "report_geometry",
        "Report observed terminal geometry",
        |ctx, _args| {
            ctx.notify(
                &format!("geometry:{}x{}", ctx.width(), ctx.height()),
                "info",
            );
            CommandResult::Ok
        },
    );
    // A width handler makes a host call the way any other handler does; the host's reply must reach it (conformance TestConformance_WidthHandlerHostCall).
    let width_probe = std::sync::Mutex::new(None);
    ext.command("arm_width_probe", "Notify from a width handler", move |ctx, _args| {
        let mut armed = width_probe.lock().unwrap();
        if armed.is_none() {
            let retained = ctx.clone();
            *armed = Some(ctx.on_width_change(move |width| {
                retained.notify(&format!("width-probe:{width}"), "info");
                retained.notify(&format!("width-probe-returned:{width}"), "info");
            }));
        }
        CommandResult::Ok
    });
    ext.command("status", "Set a status entry", |ctx, _args| {
        ctx.set_status("conformance", "ok");
        CommandResult::Ok
    });
    ext.command(
        "status_burst",
        "Set one status repeatedly without awaiting",
        |ctx, _args| {
            for i in 0..200 {
                ctx.set_status("burst", &i.to_string());
            }
            CommandResult::Ok
        },
    );
    let term_sub: Arc<Mutex<Option<TerminalInputSubscription>>> = Arc::new(Mutex::new(None));
    let sub_for_subscribe = term_sub.clone();
    ext.command(
        "term_subscribe",
        "Subscribe to raw terminal input",
        move |ctx, _args| {
            let input_ctx = ctx.clone();
            match ctx.on_terminal_input(move |data: &pig_sdk::JsString| {
                if matches!(data.as_units(), [0xd83d] | [0xde00] | [0xd83d, 0xde00]) {
                    let mut units: Vec<u16> = "seen:".encode_utf16().collect();
                    units.extend_from_slice(data.as_units());
                    return TerminalInputResult { consume: false, data: Some(pig_sdk::JsString::from_units(units)) };
                }
                match data.to_string().as_deref() {
                Ok("\x1b[96~") => TerminalInputResult {
                    consume: false,
                    data: Some(
                        serde_json::to_string(&(input_ctx.get_editor_text().unwrap(), input_ctx.get_tools_expanded().unwrap())).unwrap().into(),
                    ),
                },
                Ok("\x1b[98~") => TerminalInputResult {
                    consume: false,
                    data: Some("rewritten".into()),
                },
                Ok("\x1b[97~") => {
                    thread::sleep(Duration::from_millis(200));
                    TerminalInputResult {
                        consume: true,
                        data: None,
                    }
                }
                _ => TerminalInputResult {
                    consume: data == "\x1b[99~",
                    data: None,
                },
            }}) {
                Ok(sub) => {
                    *sub_for_subscribe.lock().unwrap() = Some(sub);
                    CommandResult::Ok
                }
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    let sub_for_release = term_sub.clone();
    ext.command(
        "term_unsubscribe",
        "Release the raw input subscription",
        move |_ctx, _args| {
            if let Some(sub) = sub_for_release.lock().unwrap().take() {
                sub.unsubscribe();
            }
            CommandResult::Ok
        },
    );
    ext.command(
        "send_message",
        "Send a custom message",
        |ctx, _args| match ctx.send_message("notice", "hello-custom", true, Some(true), Some("steer")) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "send_message_default",
        "Send a custom message with default options",
        |ctx, _args| match ctx.send_message("notice", "default", true, None, None) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "send_message_no_turn",
        "Send a custom message that never starts a turn",
        |ctx, _args| match ctx.send_message("notice", "no-turn", true, Some(false), None) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "send_user_message",
        "Send a user message",
        |ctx, args| {
            let content = if args.is_empty() { serde_json::Value::String("hello-user".into()) } else {
                match serde_json::from_str::<serde_json::Value>(&args) { Ok(value) => value, Err(err) => return CommandResult::Error(err.to_string()) }
            };
            match ctx.send_user_message(content, "followUp") {
                Ok(_) => CommandResult::Ok,
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    ext.command(
        "set_session_name",
        "Set the session name",
        |ctx, _args| match ctx.set_session_name("conformance-session") {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "append_entry",
        "Append a custom entry",
        |ctx, _args| match ctx.append_entry("conformance-entry", json!("hello-entry")) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "sprite-probe",
        "Exercise sprite registration and host errors",
        |ctx, _args| {
            let mut definition = conformance_sprite_definition();
            if let Err(err) = ctx.register_sprite(&definition) {
                return CommandResult::Error(err.to_string());
            }
            definition.mascot[0].pop();
            match ctx.register_sprite(&definition) {
                Ok(()) => CommandResult::Error("invalid sprite definition was accepted".to_string()),
                Err(err) => {
                    ctx.notify(&err.to_string(), "error");
                    CommandResult::Ok
                }
            }
        },
    );
    ext.command(
        "login-probe",
        "Exercise semantic login submission and host errors",
        |ctx, _args| {
            let mut definition = conformance_login_definition();
            if let Err(err) = ctx.set_login(&definition) {
                return CommandResult::Error(err.to_string());
            }
            definition.brand[0].pop();
            match ctx.set_login(&definition) {
                Ok(()) => CommandResult::Error("invalid login definition was accepted".to_string()),
                Err(err) => {
                    ctx.notify(&err.to_string(), "error");
                    CommandResult::Ok
                }
            }
        },
    );
    ext.command("scoped-models-probe", "Report the model scope", |ctx, _args| {
        match ctx.scoped_models() {
            Ok(models) => { ctx.notify(&serde_json::to_string(&models).unwrap(), "info"); CommandResult::Ok }
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("ui-availability", "Probe bound UI and headless defaults", |ctx, _args| {
        let result = (|| -> std::io::Result<()> {
            let (selected, _) = ctx.select("Pick", &["first", "second"])?;
            ctx.notify("availability-notify", "info");
            if !ctx.has_ui() {
                assert_eq!(ctx.input("Input", "placeholder")?, (String::new(), false));
                assert_eq!(ctx.editor("Editor", "prefill")?, (String::new(), false));
                assert!(!ctx.confirm("Confirm", "message")?);
                struct NoUIComponent;
                impl pig_sdk::RemoteComponent for NoUIComponent {
                    fn render(&self, _: u32) -> Vec<String> { panic!("headless render") }
                    fn handle_input(&mut self, _: &pig_sdk::JsString) -> Result<pig_sdk::RemoteComponentResult, String> { panic!("headless input") }
                    fn dispose(&mut self) { panic!("headless disposal") }
                }
                assert!(ctx.custom_component(NoUIComponent, json!({}))?.is_none());
                let _off = ctx.on_terminal_input(|_| panic!("headless terminal"))?;
                ctx.add_autocomplete_provider(std::sync::Arc::new(|_, _| panic!("headless autocomplete factory")))?;
                ctx.set_editor_text("ignored");
                ctx.set_tools_expanded(true);
                assert_eq!(ctx.get_editor_text()?, "");
                assert!(!ctx.get_tools_expanded()?);
                assert!(ctx.get_all_themes()?.is_empty());
                assert!(ctx.get_theme("dark")?.is_none());
                assert_eq!(ctx.set_theme("dark"), (false, "UI not available".to_string()));
            }
            ctx.append_entry("ui-availability", json!(format!("hasUI={} selected={selected}", ctx.has_ui())))?;
            Ok(())
        })();
        match result { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err.to_string()) }
    });
    ext.command("ui-state-barrier", "Read UI state after a dialog", |ctx, _args| {
        let result = (|| -> std::io::Result<()> {
            let (selected, _) = ctx.select("expand", &["chosen"])?;
            let named = ctx.get_theme("light")?.unwrap();
            let missing = ctx.get_theme("missing")?;
            let (success, message) = ctx.set_theme("missing");
            ctx.notify(&format!("selected={selected} expanded={} named={} missing={} success={success} error={message}", ctx.get_tools_expanded()?, named["name"].as_str().unwrap(), missing.is_none()), "info");
            Ok(())
        })();
        match result { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err.to_string()) }
    });
    ext.command("autocomplete-register", "Register retained provider wrappers", |ctx, _| {
        let result = (|| -> std::io::Result<()> {
            for tag in ["A", "B"] {
                ctx.add_autocomplete_provider(std::sync::Arc::new(move |ctx, current| {
                    ctx.notify(&format!("factory:{tag}"), "info");
                    let calls = std::sync::atomic::AtomicUsize::new(0);
                    let get = current.clone();
                    let apply = current.clone();
                    Ok(std::sync::Arc::new(pig_sdk::AutocompleteProvider {
                        trigger_characters: if tag == "A" { vec!["$".into()] } else { vec!["#".into(), "$".into()] },
                        get_suggestions: std::sync::Arc::new(move |ctx, lines, line, col, force| {
                            let count = calls.fetch_add(1, std::sync::atomic::Ordering::SeqCst) + 1;
                            let Some(mut result) = (get.get_suggestions)(ctx, lines, line, col, force)? else { return Ok(None); };
                            if tag == "A" {
                                result.items.retain(|item| item.value != "drop");
                                for item in &mut result.items { item.label = Some(format!("{}:{count}", item.value)); }
                            } else { result.items.push(pig_sdk::AutocompleteItem { value: "tail".into(), label: Some(format!("tail:{count}")), description: None }); }
                            Ok(Some(result))
                        }),
                        apply_completion: std::sync::Arc::new(move |ctx, lines, line, col, item, prefix| {
                            let mut result = (apply.apply_completion)(ctx, lines, line, col, item, prefix)?;
                            result.lines[result.cursor_line].push_str(&format!("-{tag}"));
                            result.cursor_col += 2;
                            Ok(result)
                        }),
                        should_trigger_file_completion: Some(std::sync::Arc::new(move |ctx, lines, line, col| current.should_trigger_file_completion.as_ref().unwrap()(ctx, lines, line, col))),
                    }))
                }))?;
                ctx.notify(&format!("registered:{tag}"), "info");
            }
            Ok(())
        })();
        match result { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err.to_string()) }
    });
    ext.command("signal-probe", "Report ctx.signal", |ctx, _args| {
        let state = match ctx.signal() {
            None => "none",
            Some(signal) if signal.is_cancelled() => "aborted",
            Some(_) => "live",
        };
        ctx.notify(&format!("signal:{state}"), "info");
        CommandResult::Ok
    });
    ext.command("signal-wait", "Wait for ctx.signal to abort", |ctx, _args| {
        let Some(signal) = ctx.signal() else {
            ctx.notify("wait:none", "info");
            return CommandResult::Ok;
        };
        ctx.notify("wait:start", "info");
        let deadline = std::time::Instant::now() + Duration::from_secs(10);
        while !signal.is_cancelled() && std::time::Instant::now() < deadline {
            thread::sleep(Duration::from_millis(5));
        }
        ctx.notify(if signal.is_cancelled() { "wait:aborted" } else { "wait:timeout" }, "info");
        CommandResult::Ok
    });
    ext.command("signal-poll", "Poll ctx.signal until it is live or none", |ctx, args| {
        ctx.notify("poll:start", "info");
        let deadline = std::time::Instant::now() + Duration::from_secs(10);
        while std::time::Instant::now() < deadline {
            if ctx.signal().is_some() == (args == "live") {
                ctx.notify(&format!("poll:{args}"), "info");
                return CommandResult::Ok;
            }
            thread::sleep(Duration::from_millis(5));
        }
        ctx.notify("poll:timeout", "info");
        CommandResult::Ok
    });
    ext.command("usage-probe", "Report context usage", |ctx, _args| {
        let usage = ctx.get_context_usage().unwrap().map(|u| json!({"tokens": u.tokens, "contextWindow": u.context_window, "percent": u.percent}));
        ctx.notify(&serde_json::to_string(&usage).unwrap(), "info");
        CommandResult::Ok
    });
    ext.command(
        "context-probe",
        "Report ctx.mode + ctx.getSystemPromptOptions()",
        |ctx, _args| {
            let opts = ctx.get_system_prompt_options().unwrap();
            let prompt = opts
                .get("customPrompt")
                .and_then(Value::as_str)
                .unwrap_or("");
            let cwd = opts.get("cwd").and_then(Value::as_str).unwrap_or("");
            let tools = opts
                .get("selectedTools")
                .and_then(Value::as_array)
                .map(|a| {
                    a.iter()
                        .filter_map(Value::as_str)
                        .collect::<Vec<_>>()
                        .join(",")
                })
                .unwrap_or_default();
            let shape = |value: Option<&Value>| match value {
                None | Some(Value::Null) => "absent".to_string(),
                Some(Value::Array(items)) => format!("array:{}", items.len()),
                Some(Value::Object(fields)) => format!("object:{}", fields.len()),
                Some(Value::String(text)) => format!("string:{}", text.encode_utf16().count()),
                Some(other) => format!("other:{other}"),
            };
            let shapes = ["selectedTools", "toolSnippets", "toolGuidelines", "promptGuidelines", "appendSystemPrompt", "sections", "contextFiles", "skills"]
                .iter()
                .map(|key| format!("{key}:{}", shape(opts.get(*key))))
                .collect::<Vec<_>>()
                .join(",");
            ctx.notify(
                &format!(
                    "mode={} trusted={} spo_prompt={} spo_cwd={} spo_tools={} spo_shape={} spo_guidelines={} spo_skill_scope={} spo_force_empty={} spo_custom_present={}",
                    ctx.mode(),
                    ctx.is_project_trusted().unwrap(),
                    prompt,
                    cwd,
                    tools,
                    shapes,
                    opts["toolGuidelines"]["read"].as_array().map(|v| v.iter().filter_map(Value::as_str).collect::<Vec<_>>().join(",")).unwrap_or_default(),
                    opts["skills"][0]["sourceInfo"]["scope"].as_str().unwrap_or(""),
                    opts["forceSystemPrompt"].as_str() == Some(""),
                    opts.get("customPrompt").is_some()
                ),
                "info",
            );
            CommandResult::Ok
        },
    );
    ext.command(
        "session-log-probe",
        "Read a paged session log",
        |ctx, _args| {
            ctx.notify(
                &format!(
                    "session entries={} branch={}",
                    ctx.get_entries().unwrap().len(),
                    ctx.get_branch().unwrap().len()
                ),
                "info",
            );
            CommandResult::Ok
        },
    );
    ext.command(
        "dialog-probe",
        "Exercise interactive dialog responses",
        |ctx, _args| {
            let result = (|| -> std::io::Result<String> {
                let (selected, _) = ctx.select("Pick", &["first", "second"])?;
                let (input, _) = ctx.input("Input", "placeholder")?;
                let (edited, _) = ctx.editor("Editor", "prefill")?;
                let confirmed = ctx.confirm("Confirm", "message")?;
                Ok(format!(
                    "select={selected} input={input} editor={edited} confirm={confirmed}"
                ))
            })();
            match result {
                Ok(summary) => {
                    ctx.notify(&summary, "info");
                    CommandResult::Ok
                }
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    ext.command(
        "focused-probe",
        "Exercise focused subprocess UI",
        |ctx, _args| {
            let disposed = Arc::new(AtomicBool::new(false));
            let component = FocusedList {
                items: ["alpha", "beta", "gamma"],
                selected: 0,
                disposed: disposed.clone(),
            };
            match ctx.custom_component(
                component,
                json!({"title": "Focused", "widthFraction": 0.5, "heightFraction": 0.5}),
            ) {
                Ok(value) => {
                    let selected = value
                        .and_then(|v| v.as_str().map(str::to_string))
                        .unwrap_or_default();
                    ctx.notify(
                        &format!(
                            "focused={selected} disposed={}",
                            disposed.load(Ordering::Relaxed)
                        ),
                        "info",
                    );
                    CommandResult::Ok
                }
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    ext.command(
        "timer-focused-probe",
        "Exercise timer-driven focused UI",
        |ctx, _args| {
            let (component, disposed, detached) = TimerFocused::new();
            match ctx.custom_component(component, json!({"title": "Timer"})) {
                Ok(value) => {
                    let frame = value.and_then(|v| v.as_u64()).unwrap_or(0);
                    ctx.notify(
                        &format!(
                            "timer={frame} disposed={} detached={}",
                            disposed.load(Ordering::Acquire),
                            detached.load(Ordering::Acquire),
                        ),
                        "info",
                    );
                    CommandResult::Ok
                }
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    // Typed tool events (upstream PowerShellToolCallEvent/BashToolCallEvent and
    // their result variants): record the input and details, block the
    // conformance sentinel command, and replace a result's content.
    ext.on_event("user_bash", false, |_ctx, data| {
        let mut value =
            json!({"result":{"output":"handled","exitCode":7,"cancelled":false,"truncated":false}});
        match data["command"].as_str().unwrap_or("") {
            "valid" => {}
            "undefined" => value["result"]["exitCode"] = Value::Null,
            "undefined-path" => value["result"]["fullOutputPath"] = Value::Null,
            "missing" => {
                value["result"].as_object_mut().unwrap().remove("exitCode");
            }
            "invalid" => value["result"]["exitCode"] = json!("invalid"),
            "null-operations" => value["operations"] = Value::Null,
            _ => return None,
        }
        Some(value)
    });

    ext.on_event("tool_call", false, |ctx, data| {
        let name = data["toolName"].as_str().unwrap_or_default();
        if name != "powershell" && name != "bash" {
            return None;
        }
        let command = data["input"]["command"].as_str().unwrap_or_default();
        ctx.notify(
            &format!(
                "tool-call={}:{}:{}",
                name,
                command,
                data["input"]["timeout"].as_u64().unwrap_or_default()
            ),
            "info",
        );
        if command == "blocked-command" {
            return Some(json!({"block": true, "reason": format!("blocked {name}")}));
        }
        None
    });
    ext.on_event("tool_result", false, |ctx, data| {
        let name = data["toolName"].as_str().unwrap_or_default();
        if name != "powershell" && name != "bash" {
            return None;
        }
        ctx.notify(
            &format!(
                "tool-result={}:{}:{}:{}",
                name,
                data["details"]["fullOutputPath"]
                    .as_str()
                    .unwrap_or_default(),
                data["details"]["truncation"]["totalLines"]
                    .as_u64()
                    .unwrap_or_default(),
                data["content"][0]["text"].as_str().unwrap_or_default()
            ),
            "info",
        );
        Some(json!({"content": [{"type": "text", "text": format!("{name} redacted")}]}))
    });
    ext.on_event("after_provider_response", false, |ctx, data| {
        ctx.notify(&format!("provider-response={}:{}:{}", data["type"].as_str().unwrap_or_default(), data["status"], data["headers"]["x-probe"].as_str().unwrap_or_default()), "info");
        Some(json!({"cancel":true}))
    });
    ext.on_event("after_provider_response", false, |ctx, _| { ctx.notify("provider-response=second", "info"); None });
    ext.on_event("message_update", false, |ctx, data| {
        let assistant = &data["assistantMessageEvent"];
        ctx.notify(
            &format!(
                "message-update={}:{}:{}:{}",
                assistant["type"].as_str().unwrap_or_default(),
                assistant["contentIndex"].as_u64().unwrap_or_default(),
                assistant["delta"].as_str().unwrap_or_default(),
                assistant.get("assistantMessageEvent").is_some()
            ),
            "info",
        );
        None
    });
    ext.on_event("tool_execution_update", false, |ctx, data| {
        if data["toolName"] == "production_tool" {
            ctx.notify(
                &format!(
                    "tool-update={}:{}:{}:{}:{}",
                    data["toolName"].as_str().unwrap_or_default(),
                    data["args"]["path"].as_str().unwrap_or_default(),
                    data["args"]["nested"]["depth"].as_u64().unwrap_or_default(),
                    data["partialResult"]["content"][0]["text"]
                        .as_str()
                        .unwrap_or_default(),
                    data["partialResult"]["details"]["progress"]
                        .as_u64()
                        .unwrap_or_default()
                ),
                "info",
            );
            return None;
        }
        ctx.notify(
            &format!(
                "tool-update={}:{}:{}:{}",
                data["toolName"].as_str().unwrap_or_default(),
                data["args"],
                data["partialResult"]["content"][0]["text"]
                    .as_str()
                    .unwrap_or_default(),
                data["partialResult"]["details"]["progress"]
                    .as_u64()
                    .unwrap_or_default()
            ),
            "info",
        );
        None
    });
    ext.on_event("tool_execution_end", false, |ctx, data| {
        if data["toolName"] == "production_tool" {
            ctx.notify(
                &format!(
                    "tool-end={}:{}:{}:{}:{}:{}:{}",
                    data["toolName"].as_str().unwrap_or_default(),
                    data["result"]["content"][0]["text"]
                        .as_str()
                        .unwrap_or_default(),
                    data["result"]["content"]
                        .as_array()
                        .map(Vec::len)
                        .unwrap_or_default(),
                    data["result"]["content"][1]["data"]
                        .as_str()
                        .unwrap_or_default(),
                    data["result"]["content"][1]["mimeType"]
                        .as_str()
                        .unwrap_or_default(),
                    data["result"]["details"]["nested"]["value"]
                        .as_str()
                        .unwrap_or_default(),
                    data["isError"].as_bool().unwrap_or_default()
                ),
                "info",
            );
            return None;
        }
        ctx.notify(
            &format!(
                "tool-end={}:{}:{}:{}:{}",
                data["toolName"].as_str().unwrap_or_default(),
                data["result"]["content"]
                    .as_array()
                    .map(Vec::len)
                    .unwrap_or_default(),
                data["result"]["content"][1]["data"]
                    .as_str()
                    .unwrap_or_default(),
                data["result"]["details"]["nested"]["value"]
                    .as_str()
                    .unwrap_or_default(),
                data["isError"].as_bool().unwrap_or_default()
            ),
            "info",
        );
        None
    });
    ext.on_project_trust(|_, _| Err("trust-boom".to_string()));
    ext.on_project_trust(|_, _| {
        Ok(ProjectTrustResult {
            trusted: ProjectTrustDecision::Undecided,
            remember: None,
        })
    });
    ext.on_project_trust(|_, _| {
        Ok(ProjectTrustResult {
            trusted: ProjectTrustDecision::Yes,
            remember: Some(true),
        })
    });
    ext.on_event("cache_warming_decision", false, |_, data| {
        if data["warmCost"] != json!(0.05) || data["missCost"] != json!(0.5) || data["continuationProbability"] != json!(0.15) || data["action"] != json!("warm") {
            return Some(json!({"error": "unexpected cache decision"}));
        }
        Some(json!({"action": "stop"}))
    });
    ext.on_event("agent_before_settle", false, |ctx, mut data| {
        let outcome = data.get("outcome").and_then(Value::as_str).unwrap_or("");
        let entries = data.get("entries").and_then(Value::as_array).map_or(0, Vec::len);
        let continuation = data.get("continue").and_then(Value::as_bool).unwrap_or(false);
        let preview = data.get("context").and_then(Value::as_object);
        let context_entries = preview
            .and_then(|value| value.get("contextEntries"))
            .and_then(Value::as_array)
            .map_or(0, Vec::len);
        let can_continue = preview
            .and_then(|value| value.get("canContinue"))
            .and_then(Value::as_bool)
            .unwrap_or(false);
        ctx.notify(
            &format!("agent_before_settle:{outcome}:{entries}:{continuation}:{context_entries}:{can_continue}"),
            "info",
        );
        data["entries"].as_array_mut().unwrap().push(json!({"type": "custom", "customType": "kept"}));
        None
    });
    ext.on_event("agent_before_settle", false, |_, mut data| {
        data["entries"].as_array_mut().unwrap().push(json!({"type": "custom", "customType": "before-error"}));
        panic!("boundary failed");
    });
    ext.on_event("agent_before_settle", false, |_, data| {
        assert_eq!(data["entries"].as_array().unwrap().len(), 2);
        assert_eq!(data["entries"][0]["customType"], "kept");
        assert_eq!(data["entries"][1]["customType"], "before-error");
        assert_eq!(data["context"]["contextEntries"].as_array().unwrap().len(), 2);
        Some(json!({
            "entries": [{"type": "custom", "customType": "conformance-boundary"}],
            "continue": true
        }))
    });
    ext.on_event("session_start", false, |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        ctx.notify(&format!("session_start:{reason}"), "info");
        if let Some(path) = data.get("previousSessionFile").and_then(Value::as_str).filter(|s| !s.is_empty()) {
            ctx.notify(&format!("previous:{path}"), "info");
        }
        None
    });
    ext.on_event("session_shutdown", false, |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        ctx.notify(&format!("session_shutdown:{reason}"), "info");
        if let Some(path) = data.get("targetSessionFile").and_then(Value::as_str).filter(|s| !s.is_empty()) {
            ctx.notify(&format!("target:{path}"), "info");
        }
        None
    });
    ext.on_event("session_info_changed", false, |ctx, data| {
        let name = data.get("name").and_then(Value::as_str).unwrap_or("");
        ctx.notify(&format!("session_info_changed:{name}"), "info");
        None
    });
    ext.on_event("session_before_compact", false, |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        let will_retry = data
            .get("willRetry")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        ctx.notify(
            &format!("session_before_compact:{reason}:{will_retry}"),
            "info",
        );
        None
    });
    ext.on_event("session_compact", false, |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        let will_retry = data
            .get("willRetry")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        let from_extension = data
            .get("fromExtension")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        ctx.notify(
            &format!("session_compact:{reason}:{will_retry}:{from_extension}"),
            "info",
        );
        None
    });
    ext.on_event("session_compact_failed", false, |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        let error = data
            .get("errorMessage")
            .and_then(Value::as_str)
            .unwrap_or("");
        let aborted = data
            .get("aborted")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        let will_retry = data
            .get("willRetry")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        let from_extension = data
            .get("fromExtension")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        ctx.notify(
            &format!("session_compact_failed:{reason}:{error}:{aborted}:{will_retry}:{from_extension}"),
            "info",
        );
        None
    });
    ext.on_event("turn_end", false, |_, data| {
        if data["messageEntryId"] != "boundary-assistant" { return None; }
        data["entries"].as_array_mut().unwrap().push(json!({"type":"custom", "customType":"mutated"}));
        panic!("turn-boundary-failure");
    });
    ext.on_event("turn_end", false, |_, data| {
        if data["messageEntryId"] != "boundary-assistant" { return None; }
        Some(json!({"entries":[{"type":"custom", "customType":"turn-boundary", "data": data}], "continue":true}))
    });
    ext.on_event("turn_end", false, |ctx, data| {
        let message_id = data.get("messageEntryId").and_then(Value::as_str).unwrap_or("");
        let tool_id = data
            .get("toolResultEntryIds")
            .and_then(Value::as_array)
            .and_then(|ids| ids.first())
            .and_then(Value::as_str)
            .unwrap_or("");
        ctx.notify(&format!("turn_end:{message_id}:{tool_id}"), "info");
        None
    });
    let prompt_sequence = Arc::new(Mutex::new(0));
    for name in ["ui_prompt_start", "ui_prompt_end"] {
        let prompt_sequence = prompt_sequence.clone();
        ext.on_event(name, false, move |ctx, data| {
            let sequence = { let mut counter = prompt_sequence.lock().unwrap(); let n = *counter; *counter += 1; n };
            let field = |key: &str| data.get(key).and_then(Value::as_str).unwrap_or("").to_string();
            let title = data.get("title").and_then(Value::as_str).unwrap_or("(none)");
            if title.starts_with("fifo:") {
                ctx.notify(&format!("fifo:{sequence}:{}:{title}", field("type")), "info");
                return None;
            }
            ctx.notify(
                &format!("ui_prompt:{}:{}:{}:{title}", field("type"), field("reason"), field("kind")),
                "info",
            );
            None
        });
    }
    register_conformance_oauth(&mut ext);
    register_conformance_oauth_object(&mut ext);
    register_conformance_oauth_large(&mut ext);
    ext.run().unwrap();
}

/// Owns credential persistence for the conformance OAuth provider. Its returned
/// values must match the Go and Python fixtures so the recordings compare equal.
struct ConformanceStore;

impl OAuthCredentialStore for ConformanceStore {
    fn credential_status(&self) -> OAuthCredentialStatus {
        OAuthCredentialStatus {
            present: true,
            auth_type: "oauth".to_string(),
            source: "conformance".to_string(),
        }
    }
    fn store_credentials(&self, creds: OAuthCredentials) -> Result<String, String> {
        if creds.account_id != "account-store" || creds.scope != "scope-store" {
            return Err("credential metadata lost".to_string());
        }
        Ok("/conf/creds.json".to_string())
    }
    fn delete_credentials(&self) -> Result<bool, String> {
        Ok(true)
    }
}

/// Contributes a provider whose callbacks treat credentials as Pi's complete token object: login returns
/// a fractional expiry and provider-owned keys, and refresh spreads its input as `{ ...creds, access,
/// expires }` does. Behavior must match the Go, Python and Node fixtures.
fn register_conformance_oauth_object(ext: &mut Extension) {
    ext.register_oauth_provider(
        "conformance-oauth-object",
        json!({"name": "Conformance OAuth Object"}),
        OAuthProvider {
            name: "Conformance OAuth Object".to_string(),
            login: Box::new(|_cb| {
                let mut creds = OAuthCredentials {
                    access: "object-access".to_string(),
                    refresh: "object-refresh".to_string(),
                    ..Default::default()
                };
                creds.extra.insert("meta".into(), json!({"k": [1, null, ""]}));
                creds.extra.insert("projectId".into(), json!(""));
                creds.set_expires_millis(1_700_000_000_000.25);
                Ok(creds)
            }),
            refresh_token: Some(Box::new(|creds: OAuthCredentials| {
                let mut next = creds.clone();
                next.access = format!("refreshed-{}", creds.refresh);
                next.set_expires_millis(creds.expires_millis().unwrap_or(f64::NAN) + 0.5);
                Ok(next)
            })),
            get_api_key: Some(Box::new(|creds: OAuthCredentials| {
                let typed = if creds.extra.get("type") == Some(&json!("oauth")) { "typed" } else { "untyped" };
                let meta = if creds.extra.contains_key("meta") { "meta" } else { "nometa" };
                format!("key:{typed}:{meta}")
            })),
            is_subscription: false,
            credential_store: None,
        },
    );
}

/// Contributes a provider whose expiry is the double 2**60: JSON.parse reads the wire digits 1152921504606847000 as that double, so get_api_key reports a distance of 0 from 2**60 for the number and for its integer projection. Behavior must match the Go, Python and Node fixtures.
fn register_conformance_oauth_large(ext: &mut Extension) {
    const TWO_60: f64 = 1_152_921_504_606_846_976.0;
    ext.register_oauth_provider(
        "conformance-oauth-large",
        json!({"name": "Conformance OAuth Large"}),
        OAuthProvider {
            name: "Conformance OAuth Large".to_string(),
            login: Box::new(|_cb| {
                let mut creds = OAuthCredentials { access: "large-access".to_string(), refresh: "large-refresh".to_string(), ..Default::default() };
                creds.set_expires_millis(TWO_60);
                Ok(creds)
            }),
            refresh_token: None,
            get_api_key: Some(Box::new(|creds: OAuthCredentials| format!("key:{}:{}", (creds.expires_millis().unwrap_or(f64::NAN) - TWO_60) as i64, creds.expires - (1_i64 << 60)))),
            is_subscription: false,
            credential_store: None,
        },
    );
}

/// Contributes the canonical OAuth provider the cross-transport conformance
/// suite drives. Behavior must match the Go and Python fixtures byte-for-byte.
fn register_conformance_oauth(ext: &mut Extension) {
    ext.register_oauth_provider(
        "conformance-oauth",
        json!({"name": "Conformance OAuth"}),
        OAuthProvider {
            name: "Conformance OAuth".to_string(),
            is_subscription: true,
            login: Box::new(|cb| {
                cb.on_device_code(OAuthDeviceCodeInfo {
                    user_code: "CONF-USER-CODE".to_string(),
                    verification_uri: "https://conf.example/verify".to_string(),
                    ..Default::default()
                });
                cb.on_progress("waiting");
                let value = cb.on_prompt(OAuthPrompt {
                    message: "paste the code".to_string(),
                    ..Default::default()
                })?;
                Ok(OAuthCredentials {
                    access: format!("access-{value}"),
                    refresh: "refresh-tok".to_string(),
                    expires: 4242,
                    account_id: "account-login".to_string(),
                    scope: "scope-login".to_string(),
                    ..Default::default()
                })
            }),
            refresh_token: Some(Box::new(|creds: OAuthCredentials| {
                Ok(OAuthCredentials {
                    access: format!("refreshed-{}", creds.refresh),
                    refresh: creds.refresh,
                    expires: 9999,
                    account_id: creds.account_id,
                    scope: creds.scope,
                    ..Default::default()
                })
            })),
            get_api_key: Some(Box::new(|creds: OAuthCredentials| {
                if creds.access == "boom" {
                    panic!("getApiKey exploded");
                }
                format!("key:{}", creds.access)
            })),
            credential_store: Some(Box::new(ConformanceStore)),
        },
    );
}

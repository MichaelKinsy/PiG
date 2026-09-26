use pig_sdk::{
    CommandResult, ConstrainedSampling, Extension, LoginDefinition, OAuthCredentialStatus,
    OAuthCredentialStore, OAuthCredentials, OAuthDeviceCodeInfo, OAuthPrompt, OAuthProvider,
    ProjectTrustDecision, ProjectTrustResult, RemoteComponent, RemoteComponentInvalidate,
    RemoteComponentResult, TerminalInputResult, TerminalInputSubscription, ToolRenderShell,
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

    fn handle_input(&mut self, data: &str) -> Result<RemoteComponentResult, String> {
        match data {
            "\u{1b}[A" => self.selected = (self.selected + self.items.len() - 1) % self.items.len(),
            "\u{1b}[B" => self.selected = (self.selected + 1) % self.items.len(),
            "\u{1b}[6~" => self.selected = (self.selected + 2).min(self.items.len() - 1),
            "\r" | "\n" => {
                return Ok(RemoteComponentResult::done(Some(json!(
                    self.items[self.selected]
                ))));
            }
            "\u{1b}" => return Ok(RemoteComponentResult::done(None)),
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

    fn handle_input(&mut self, data: &str) -> Result<RemoteComponentResult, String> {
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

fn main() {
    let mut ext = Extension::new("rust-sdk-fixture");
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
            "properties": {"text": {"type": "string", "description": "Text to echo"}}
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
    ext.tool_with_source(
        "sourced_tool",
        "Tool with explicit source",
        json!({"type": "object"}),
        "mcp:test-server",
        vec!["Use sourced_tool to test per-tool source attribution.".to_string()],
        |_ctx, _params: Value| ToolResult::Json(json!({"content": "sourced"})),
    );
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
        if slash["inputLimits"] != expected_limits || ctx.get_model_info().and_then(|model| model.input_limits) != Some(expected_limits) { return CommandResult::Error(format!("model inputLimits = {slash}")); }
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
            "maxTokens":321, "temperature":0.65, "samplingParams":{"topP":0.8},
            "thinkingBudgets":{"minimal":11,"low":22,"medium":33,"high":44}, "thinking":"high", "isReasoning":true,
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
        move |ctx, _args| match ctx.on_terminal_input(|data: &str| match data {
            "\x1b[98~" => TerminalInputResult {
                consume: false,
                data: Some("rewritten".to_string()),
            },
            "\x1b[97~" => {
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
        }) {
            Ok(sub) => {
                *sub_for_subscribe.lock().unwrap() = Some(sub);
                CommandResult::Ok
            }
            Err(err) => CommandResult::Error(err.to_string()),
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
    ext.command(
        "context-probe",
        "Report ctx.mode + ctx.getSystemPromptOptions()",
        |ctx, _args| {
            let opts = ctx.get_system_prompt_options();
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
            ctx.notify(
                &format!(
                    "mode={} trusted={} spo_prompt={} spo_cwd={} spo_tools={}",
                    ctx.mode(),
                    ctx.is_project_trusted(),
                    prompt,
                    cwd,
                    tools
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
                    ctx.get_entries().len(),
                    ctx.get_branch().len()
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
                    data["partialResult"]["content"]
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
                data["partialResult"]["content"]
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
        None
    });
    ext.on_event("session_shutdown", false, |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        ctx.notify(&format!("session_shutdown:{reason}"), "info");
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
    fn store_credentials(&self, _creds: OAuthCredentials) -> Result<String, String> {
        Ok("/conf/creds.json".to_string())
    }
    fn delete_credentials(&self) -> Result<bool, String> {
        Ok(true)
    }
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
                    ..Default::default()
                })
            }),
            refresh_token: Some(Box::new(|creds: OAuthCredentials| {
                Ok(OAuthCredentials {
                    access: format!("refreshed-{}", creds.refresh),
                    refresh: creds.refresh,
                    expires: 9999,
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

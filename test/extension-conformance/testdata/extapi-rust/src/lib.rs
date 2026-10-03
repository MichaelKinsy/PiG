//! Fixture for the Pi 0.99.1 extension API conformance rows (test/extension-conformance/extension_api_rust_test.go).
//! Every value a row asserts is one the host or the fixture produces and no SDK fallback can: a constant would fail.

use pig_sdk::{
    AgentToolResult, ExecuteToolOptions, Extension, ModelRoute, ToolAnnotations, ToolDefinition, ToolExposure,
    ToolLoadoutChanges, ToolNamespace, ToolResult, VirtualModel, empty_schema,
};
use serde_json::json;
use std::sync::{Arc, Mutex};

pub fn new_extension() -> Extension {
    let mut ext = Extension::new("extapi");

    let mut lookup = ToolDefinition::new("lookup", "Lookup", "Look a thing up", empty_schema(), |_, _| {
        ToolResult::text("found").with_structured_content(json!({"hits": 3}))
    });
    lookup.output_schema = Some(json!({"type":"object","properties":{"hits":{"type":"number"}}}));
    lookup.exposure = Some(ToolExposure::Codemode);
    lookup.namespace = Some(ToolNamespace { name: "mcp__conformance".into(), description: Some("Conformance server".into()), instructions: Some("Use the conformance lookup.".into()) });
    lookup.annotations = Some(ToolAnnotations { read_only_hint: Some(true), open_world_hint: Some(false), ..Default::default() });
    lookup.default_active = Some(false);
    ext.register_tool(lookup);

    ext.tool("failing", "Failing", empty_schema(), |_, _| {
        ToolResult::text("upstream said no")
            .with_details(json!({"status": 503}))
            .with_structured_content(json!({"ok": false}))
            .with_is_error(true)
    });

    ext.tool("probe", "Probe", empty_schema(), |ctx, _| {
        ToolResult::json(json!({
            "content": "probe",
            "details": {"settings": ctx.get_settings().ok(), "servers": ctx.get_mcp_servers(), "tools": ctx.tools().ok()},
        }))
    });

    ext.tool("nested", "Nested", empty_schema(), |ctx, _| {
        let updates = Arc::new(Mutex::new(Vec::<String>::new()));
        let recorded = updates.clone();
        let outcome = ctx.execute_tool(
            "target",
            json!({"q": "conformance"}),
            ExecuteToolOptions {
                signal: None,
                on_update: Some(Arc::new(move |partial: AgentToolResult| {
                    recorded.lock().unwrap().push(partial.content[0]["text"].as_str().unwrap_or_default().to_string());
                })),
            },
        );
        match outcome {
            Ok(outcome) => ToolResult::json(json!({
                "content": "nested",
                "details": {"outcome": outcome, "updates": *updates.lock().unwrap()},
            })),
            Err(err) => ToolResult::Error(err.to_string()),
        }
    });

    ext.tool("live_tools", "Live tools", empty_schema(), |ctx, _| {
        let names = || -> String { ctx.tools().map(|tools| tools.iter().map(|tool| tool.name.clone()).collect::<Vec<_>>().join(",")).unwrap_or_default() };
        let before = names();
        ctx.set_active_tools(&["only"]);
        let after = names();
        ToolResult::text(format!("{before}|{after}|{}", names()))
    });

    ext.tool("nested_throw", "Nested throw", empty_schema(), |ctx, _| {
        let updates = Arc::new(Mutex::new(Vec::<String>::new()));
        let recorded = updates.clone();
        let outcome = ctx.execute_tool(
            "target",
            json!({}),
            ExecuteToolOptions {
                signal: None,
                on_update: Some(Arc::new(move |partial: AgentToolResult| {
                    let first = {
                        let mut seen = recorded.lock().unwrap();
                        seen.push(partial.content[0]["text"].as_str().unwrap_or_default().to_string());
                        seen.len() == 1
                    };
                    if first {
                        panic!("callback boom");
                    }
                })),
            },
        );
        let seen = updates.lock().unwrap().join(",");
        match outcome {
            Ok(_) => ToolResult::text(format!("no rejection after {seen}")),
            Err(err) => ToolResult::text(format!("rejected: {err} after {seen}")),
        }
    });

    // Starts a nested call on a thread and returns once the test reports the host has it pending (writes `started` in params.dir).
    ext.tool("detached_call", "Detached call", empty_schema(), |ctx, args| {
        let dir = std::path::PathBuf::from(args["dir"].as_str().unwrap_or_default());
        let nested = ctx.clone();
        let out = dir.join("out");
        std::thread::spawn(move || {
            let record = match nested.execute_tool("wait_release", json!({}), ExecuteToolOptions::default()) {
                Ok(outcome) => format!("ok:{}", outcome.result.content.first().and_then(|block| block["text"].as_str()).unwrap_or_default()),
                Err(err) => format!("err:{err}"),
            };
            let _ = std::fs::write(out, record);
        });
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(10);
        while std::time::Instant::now() < deadline {
            if dir.join("started").exists() {
                return ToolResult::text("returned");
            }
            std::thread::sleep(std::time::Duration::from_millis(5));
        }
        ToolResult::Error("the nested call never started".into())
    });

    ext.tool("register_late", "Register late", empty_schema(), |ctx, _| {
        match ctx.register_mcp_server("late", json!({"url": "https://mcp.invalid/late", "exposure": "codemode-deferred", "toolExposure": {"t*": "codemode-deferred"}})) {
            Ok(()) => ToolResult::json(json!({"content": "registered", "details": ctx.get_mcp_servers()})),
            Err(err) => ToolResult::Error(err.to_string()),
        }
    });
    ext.tool("register_invalid", "Register invalid", empty_schema(), |ctx, _| {
        match ctx.register_mcp_server("not valid", json!({"url": "https://mcp.invalid/x"})) {
            Ok(()) => ToolResult::text("registered"),
            Err(err) => ToolResult::Error(err.to_string()),
        }
    });
    ext.tool("unregister_late", "Unregister late", empty_schema(), |ctx, _| match ctx.unregister_mcp_server("late") {
        Ok(()) => ToolResult::json(json!({"content": "unregistered", "details": ctx.get_mcp_servers()})),
        Err(err) => ToolResult::Error(err.to_string()),
    });

    let mut orchestrate = ToolDefinition::new("orchestrate", "Orchestrate", "Runs tools", empty_schema(), |_, _| {
        ToolResult::text("orchestrated")
    });
    orchestrate.prepare_loadout = Some(Box::new(|loadout| {
        let callable: Vec<_> = loadout.callable.iter().map(|tool| tool.name.as_str()).collect();
        Some(ToolLoadoutChanges {
            descriptions: Some(
                [(
                    "orchestrate".to_string(),
                    format!(
                        "Runs {} | {} | {:?}",
                        callable.join(","),
                        loadout.get_exposure("conf-deferred").as_str(),
                        loadout.get_namespace("conf-deferred").map(|namespace| namespace.name.clone()),
                    ),
                )]
                .into(),
            ),
            hidden_declarations: Some(loadout.declared.iter().skip(1).map(|tool| tool.name.clone()).collect()),
        })
    }));
    ext.register_tool(orchestrate);

    let cost = json!({"input": 1, "output": 2, "cacheRead": 0.1, "cacheWrite": 1.25});
    ext.register_provider("conformance-media", json!({
        "baseUrl": "https://media.invalid",
        "apiKey": "conformance-key",
        "api": "openai-completions",
        "models": [
            {"id": "chat-1", "name": "Chat", "reasoning": true, "input": ["text"], "cost": cost, "contextWindow": 128000, "maxTokens": 4096},
            {"id": "image-1", "name": "Image", "type": "image", "api": "openrouter-images", "input": ["text"], "cost": cost, "output": ["image", "text"]},
            {"id": "classifier-1", "name": "Classifier", "type": "classifier", "api": "llama-cpp-classify", "input": ["text"], "cost": cost, "contextWindow": 8192},
        ],
    }));
    ext.register_mcp_server("loaded", json!({"url": "https://mcp.invalid/loaded", "exposure": "direct", "description": "Loaded server", "auth": {"provider": "loaded-provider"}}));
    ext.register_virtual_model({
        let mut model = VirtualModel::new("conformance", "auto", "Auto", |ctx, request| {
            Ok(ModelRoute {
                model: json!({"provider": "conformance", "id": format!("phys-{}-{}-{}", request.reason, request.messages.len(), ctx.is_cancelled())}),
                thinking_level: request.thinking_level,
                state: Some(json!({"routed": request.state.map_or(0, |state| state["routed"].as_u64().unwrap_or(0)) + 1})),
            })
        });
        model.thinking_levels = Some(vec!["off".into(), "high".into()]);
        model.context_window = Some(64_000);
        model
    });
    // A router that returns the state it was given keeps the session's state (agent-session.ts:788).
    ext.register_virtual_model(VirtualModel::new("conformance", "identity", "Identity", |_, request| {
        Ok(ModelRoute { model: json!({"provider": "conformance", "id": "phys"}), thinking_level: request.thinking_level, state: request.state })
    }));
    // A virtual model an earlier-loaded extension queued is removed before the runner binds (loader.ts:228-232).
    ext.unregister_virtual_model("conformance", "victim");
    ext
}

//! The Pi 0.99.1 extension API additions (`.upstream/v0.99.1/packages/coding-agent/src/core/extensions/types.ts`,
//! `runner.ts`, `loader.ts`, `core/virtual-models.ts`) as the Rust SDK puts them on the wire, against a fake host.
//! The frame shapes are `protocol_extension_api.go` and `protocol.go` in the host's subprocess package.
#![cfg(unix)]

use pig_sdk::{
    AgentToolResult, ExecuteToolOptions, Extension, ModelRoute, ProviderSignal, ToolAnnotations,
    ToolDefinition, ToolExposure, ToolLoadoutChanges, ToolNamespace, ToolResult, VirtualModel,
    empty_schema,
};
use serde_json::{Value, json};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex, mpsc};
use std::time::Duration;

static NEXT: AtomicUsize = AtomicUsize::new(0);

fn read(stream: &mut UnixStream) -> Value {
    let mut prefix = [0; 4];
    stream.read_exact(&mut prefix).expect("frame prefix");
    let mut data = vec![0; u32::from_be_bytes(prefix) as usize];
    stream.read_exact(&mut data).expect("frame body");
    serde_json::from_slice(&data).unwrap()
}

fn send(stream: &mut UnixStream, frame: &Value) {
    let raw = frame.to_string();
    stream.write_all(&(raw.len() as u32).to_be_bytes()).unwrap();
    stream.write_all(raw.as_bytes()).unwrap();
}

/// A fake host: the accepted socket of one running extension.
struct Host {
    stream: UnixStream,
    thread: Option<std::thread::JoinHandle<std::io::Result<()>>>,
    root: std::path::PathBuf,
    /// The `register` frame the extension sent.
    register: Value,
}

impl Host {
    fn start(ext: Extension, state: Value) -> Host {
        let root = std::env::temp_dir().join(format!(
            "pig-extapi-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        std::fs::create_dir(&root).unwrap();
        let socket = root.join("sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let thread = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
        let (mut stream, _) = listener.accept().unwrap();
        stream.set_read_timeout(Some(Duration::from_secs(10))).unwrap();
        let register = read(&mut stream);
        assert_eq!(register["type"], "register");
        send(&mut stream, &json!({"type":"ready","ready":{"state":state,"width":80,"mode":"tui"}}));
        Host { stream, thread: Some(thread), root, register }
    }

    /// The next frame that is not a request_state.
    fn next(&mut self) -> Value {
        loop {
            let frame = read(&mut self.stream);
            if frame["type"] != "request_state" {
                return frame;
            }
        }
    }

    fn expect_call(&mut self, method: &str) -> Value {
        let frame = self.next();
        assert_eq!(frame["type"], "call", "frame: {frame}");
        assert_eq!(frame["call"]["method"], method, "frame: {frame}");
        frame
    }

    fn reply(&mut self, call: &Value, result: Value) {
        send(&mut self.stream, &json!({"type":"call_result","id":call["id"],"call_result":{"result":result}}));
    }

    fn reply_error(&mut self, call: &Value, message: &str) {
        send(
            &mut self.stream,
            &json!({"type":"call_result","id":call["id"],"call_result":{"error":{"message":message}}}),
        );
    }

    fn request(&mut self, id: &str, request: Value) {
        send(&mut self.stream, &json!({"type":"request","id":id,"request":request}));
    }

    fn tool_call(&mut self, id: &str, tool: &str, tool_call_id: &str, args: Value) {
        self.request(id, json!({"method":"tool_call","tool":tool,"tool_call_id":tool_call_id,"args":args}));
    }

    fn notify(&mut self, method: &str, args: Value) {
        send(&mut self.stream, &json!({"type":"notify","notify":{"method":method,"args":args}}));
    }

    fn response(&mut self, id: &str) -> Value {
        let frame = self.next();
        assert_eq!(frame["type"], "response", "frame: {frame}");
        assert_eq!(frame["id"], id);
        frame["response"].clone()
    }

    fn finish(mut self) {
        send(&mut self.stream, &json!({"type":"shutdown","shutdown":{"reason":"done"}}));
        self.thread.take().unwrap().join().unwrap().unwrap();
    }
}

impl Drop for Host {
    fn drop(&mut self) {
        let _ = self.stream.shutdown(std::net::Shutdown::Both);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
        let _ = std::fs::remove_dir_all(&self.root);
    }
}

fn tool(name: &str) -> Value {
    json!({"name":name,"label":name,"description":name,"parameters":{"type":"object"}})
}

// types.ts:582-607 (ToolDefinition outputSchema, exposure, namespace, annotations, defaultActive, prepareLoadout), 509-527
// (ToolExposure, ToolAnnotations, ToolNamespace): every field of a tool definition reaches the host in the register frame.
// loader.ts:456-478 and mcp-servers.ts: a server registered while loading is queued; a repeated name replaces in place (Map.set).
#[test]
fn register_frame_carries_the_tool_definition_fields_and_load_time_registrations() {
    let mut ext = Extension::new("api");
    let mut lookup = ToolDefinition::new("lookup", "Lookup", "Look a thing up", empty_schema(), |_, _| {
        ToolResult::text("ok")
    });
    lookup.output_schema = Some(json!({"type":"object","properties":{"hits":{"type":"number"}}}));
    lookup.exposure = Some(ToolExposure::Codemode);
    lookup.namespace = Some(ToolNamespace { name: "mcp__docs".into(), description: Some("Documentation tools".into()), instructions: Some("Search before reading.".into()) });
    lookup.annotations = Some(ToolAnnotations { read_only_hint: Some(true), open_world_hint: Some(false), ..Default::default() });
    lookup.default_active = Some(false);
    lookup.prepare_loadout = Some(Box::new(|_| None));
    ext.register_tool(lookup);
    ext.register_tool(ToolDefinition::new("plain", "Plain", "Plain", empty_schema(), |_, _| ToolResult::text("ok")));
    ext.register_mcp_server("docs", json!({"url":"https://mcp.example.com/docs"}));
    ext.register_mcp_server("local", json!({"command":"mcp-local","args":["--stdio"]}));
    ext.register_mcp_server("docs", json!({"url":"https://mcp.example.com/docs-v2","exposure":"direct"}));
    ext.register_virtual_model({
        let mut model = VirtualModel::new("router", "auto", "Auto", |_, _| Err("unused".into()));
        model.thinking_levels = Some(vec!["off".into(), "high".into()]);
        model.context_window = Some(200_000);
        model.max_tokens = Some(8192);
        model.input = Some(vec!["text".into()]);
        model
    });
    let host = Host::start(ext, json!({}));
    let register = &host.register["register"];
    let tools = register["tools"].as_array().unwrap();
    let lookup = tools.iter().find(|t| t["name"] == "lookup").unwrap();
    assert_eq!(lookup["output_schema"], json!({"type":"object","properties":{"hits":{"type":"number"}}}));
    assert_eq!(lookup["exposure"], "codemode");
    assert_eq!(lookup["namespace"], json!({"name":"mcp__docs","description":"Documentation tools","instructions":"Search before reading."}));
    assert_eq!(lookup["annotations"], json!({"readOnlyHint":true,"openWorldHint":false}));
    assert_eq!(lookup["default_active"], false);
    assert_eq!(lookup["prepares_loadout"], true);
    let plain = tools.iter().find(|t| t["name"] == "plain").unwrap();
    for absent in ["output_schema", "exposure", "namespace", "annotations", "default_active", "prepares_loadout"] {
        assert!(plain.get(absent).is_none(), "{absent} must be omitted: {plain}");
    }
    assert_eq!(
        register["mcp_servers"],
        json!([
            {"name":"docs","config":{"url":"https://mcp.example.com/docs-v2","exposure":"direct"}},
            {"name":"local","config":{"command":"mcp-local","args":["--stdio"]}},
        ])
    );
    assert_eq!(
        register["virtual_models"],
        json!([{"provider":"router","id":"auto","name":"Auto","thinkingLevels":["off","high"],"contextWindow":200000,"maxTokens":8192,"input":["text"]}])
    );
    host.finish();
}

// types.ts:1711 (getSettings), 1839 (getMcpServers): the getters are synchronous, so the SDK answers them from the state the host
// replicates: the ready state first, then each state_update. getSettings returns a copy. ExtensionToolContext.tools (types.ts:385)
// is a live getter (runner.ts:958-961): the SDK asks the host each time it is read.
#[test]
fn getters_answer_from_the_replicated_state() {
    let mut ext = Extension::new("api");
    ext.tool("probe", "Probe", empty_schema(), |ctx, _| {
        let mut scratch = ctx.get_settings().unwrap();
        scratch["defaultTools"] = json!(["mutated"]);
        ToolResult::json(json!({
            "content": "probe",
            "details": {"settings": ctx.get_settings().unwrap(), "servers": ctx.get_mcp_servers(), "tools": ctx.tools().unwrap()}
        }))
    });
    let mut host = Host::start(
        ext,
        json!({
            "settings": {"defaultTools": ["+read"], "fullscreenWheelScrollLines": 7},
            "mcpServers": [{"name":"docs","config":{"url":"https://mcp.example.com"},"extensionPath":"/ext/a"}],
        }),
    );
    host.tool_call("r1", "probe", "call-1", json!({}));
    let call = host.expect_call("getCallableTools");
    host.reply(&call, json!({"tools": [tool("read")]}));
    let response = host.response("r1");
    let details = &response["result"]["details"];
    assert_eq!(details["settings"], json!({"defaultTools":["+read"],"fullscreenWheelScrollLines":7}));
    assert_eq!(details["servers"], json!([{"name":"docs","config":{"url":"https://mcp.example.com"},"extensionPath":"/ext/a"}]));
    assert_eq!(details["tools"], json!([tool("read")]));

    host.notify(
        "state_update",
        json!({"state":{
            "settings": {"defaultTools": ["-bash"]},
            "mcpServers": [],
        }}),
    );
    host.tool_call("r2", "probe", "call-2", json!({}));
    let call = host.expect_call("getCallableTools");
    host.reply(&call, json!({"tools": [{"name":"grep","label":"Grep","description":"Search","parameters":{"type":"object"},"outputSchema":{"type":"object"}}]}));
    let response = host.response("r2");
    let details = &response["result"]["details"];
    assert_eq!(details["settings"], json!({"defaultTools":["-bash"]}));
    assert_eq!(details["servers"], json!([]));
    assert_eq!(details["tools"][0]["name"], "grep");
    assert_eq!(details["tools"][0]["outputSchema"], json!({"type":"object"}));
    host.finish();
}

// loader.ts:157-177 (notInitialized) and 411-414 (getSettings): before the host sends settings the getter fails, as upstream's throws
// before the runtime is bound, rather than answering an empty object. runner.ts:952-965: `tools` exists only on a tool's context.
#[test]
fn get_settings_without_host_settings_and_tools_outside_a_tool_fail() {
    let mut ext = Extension::new("api");
    ext.tool("probe", "Probe", empty_schema(), |ctx, _| match ctx.get_settings() {
        Ok(settings) => ToolResult::json(json!({"content": "probe", "details": settings})),
        Err(err) => ToolResult::Error(err.to_string()),
    });
    ext.command("list", "List", |ctx, _| match ctx.tools() {
        Ok(tools) => pig_sdk::CommandResult::Error(format!("tools outside a tool call: {tools:?}")),
        Err(err) => pig_sdk::CommandResult::Error(err.to_string()),
    });
    let mut host = Host::start(ext, json!({"callableTools": [tool("read")]}));
    host.tool_call("r1", "probe", "call-1", json!({}));
    let response = host.response("r1");
    assert_eq!(response["error"]["message"], "the host sent no settings", "{response}");
    host.request("c1", json!({"method":"command","tool":"list","args":""}));
    let response = host.response("c1");
    assert_eq!(response["error"]["message"], "tools is only available while a tool runs", "{response}");
    host.notify("state_update", json!({"state":{"settings": {"theme": "dark"}}}));
    host.tool_call("r2", "probe", "call-2", json!({}));
    assert_eq!(host.response("r2")["result"]["details"], json!({"theme": "dark"}));
    host.finish();
}

// types.ts:1833-1839 (registerMcpServer, unregisterMcpServer, getMcpServers), loader.ts:456-478: registration after load is a host
// call whose reply lists every registered server, so a getMcpServers that follows sees the change; the host's validation error is
// upstream's throw and reaches the extension.
#[test]
fn post_load_mcp_registration_is_a_host_call_and_refreshes_the_replica() {
    let mut ext = Extension::new("api");
    ext.tool("register", "Register", empty_schema(), |ctx, args| {
        match ctx.register_mcp_server(args["name"].as_str().unwrap(), args["config"].clone()) {
            Ok(()) => ToolResult::json(json!({"content":"ok","details":ctx.get_mcp_servers()})),
            Err(err) => ToolResult::Error(err.to_string()),
        }
    });
    ext.tool("unregister", "Unregister", empty_schema(), |ctx, args| {
        match ctx.unregister_mcp_server(args["name"].as_str().unwrap()) {
            Ok(()) => ToolResult::json(json!({"content":"ok","details":ctx.get_mcp_servers()})),
            Err(err) => ToolResult::Error(err.to_string()),
        }
    });
    let mut host = Host::start(ext, json!({"mcpServers": []}));

    host.tool_call("r1", "register", "c1", json!({"name":"jira","config":{"url":"https://mcp.example.com/jira"}}));
    let call = host.expect_call("registerMcpServer");
    assert_eq!(call["call"]["args"], json!({"name":"jira","config":{"url":"https://mcp.example.com/jira"}}));
    let servers = json!({"servers":[
        {"name":"other","config":{"command":"x"},"extensionPath":"/ext/b"},
        {"name":"jira","config":{"url":"https://mcp.example.com/jira"},"extensionPath":"/ext/a"},
    ]});
    host.reply(&call, servers.clone());
    assert_eq!(host.response("r1")["result"]["details"], servers["servers"]);

    host.tool_call("r2", "unregister", "c2", json!({"name":"jira"}));
    let call = host.expect_call("unregisterMcpServer");
    assert_eq!(call["call"]["args"], json!({"name":"jira"}));
    host.reply(&call, json!({"servers":[{"name":"other","config":{"command":"x"},"extensionPath":"/ext/b"}]}));
    assert_eq!(
        host.response("r2")["result"]["details"],
        json!([{"name":"other","config":{"command":"x"},"extensionPath":"/ext/b"}])
    );

    host.tool_call("r3", "register", "c3", json!({"name":"bad name","config":{}}));
    let call = host.expect_call("registerMcpServer");
    host.reply_error(&call, r#"Invalid MCP server registered by extension "/ext/a": invalid server name "bad name" (use letters, digits, "_" and "-")"#);
    let response = host.response("r3");
    assert!(
        response["error"]["message"].as_str().unwrap().contains(r#"invalid server name "bad name""#),
        "the host's error must reach the extension: {response}"
    );
    host.finish();
}

// types.ts:1852-1855, virtual-models.ts:87-101, loader.ts:480-497: a virtual model registers with every field but its route; the
// host calls the route with each request; the router's error is the request's error; an unknown virtual model does not route.
#[test]
fn virtual_model_route_request_runs_the_extensions_router() {
    let seen: Arc<Mutex<Vec<(String, bool)>>> = Arc::new(Mutex::new(Vec::new()));
    let recorded = seen.clone();
    let mut ext = Extension::new("api");
    ext.register_virtual_model(VirtualModel::new("router", "auto", "Auto", move |ctx, request| {
        recorded.lock().unwrap().push((request.reason.clone(), ctx.is_cancelled()));
        if request.reason == "direct" {
            return Err("no direct routing".into());
        }
        let count = request.state.as_ref().and_then(|s| s["count"].as_u64()).unwrap_or(0) + 1;
        Ok(ModelRoute {
            model: json!({"provider":"anthropic","id":format!("claude-{}", request.messages.len())}),
            thinking_level: request.thinking_level.clone(),
            state: Some(json!({"count":count})),
        })
    }));
    let mut host = Host::start(ext, json!({}));
    host.request(
        "route-1",
        json!({"method":"virtual_model_route","args":{
            "provider":"router","id":"auto",
            "request":{
                "model":{"provider":"router","id":"auto","api":"pi-virtual"},
                "thinkingLevel":"high","reason":"user",
                "previous":{"model":{"provider":"openai","id":"gpt"},"thinkingLevel":"low"},
                "state":{"count":2},
                "messages":[{"role":"user","content":"hi"},{"role":"user","content":"again"}],
            }
        }}),
    );
    assert_eq!(
        host.response("route-1")["result"],
        json!({"model":{"provider":"anthropic","id":"claude-2"},"thinkingLevel":"high","state":{"count":3}})
    );

    host.request("route-2", json!({"method":"virtual_model_route","args":{"provider":"router","id":"missing","request":{"reason":"user","messages":[]}}}));
    let response = host.response("route-2");
    assert!(response["error"]["message"].as_str().unwrap().contains("router/missing"), "{response}");

    host.request("route-3", json!({"method":"virtual_model_route","args":{"provider":"router","id":"auto","request":{"reason":"direct","messages":[]}}}));
    let response = host.response("route-3");
    assert_eq!(response["error"]["message"], "no direct routing");
    assert_eq!(*seen.lock().unwrap(), vec![("user".to_string(), false), ("direct".to_string(), false)]);
    host.finish();
}

// types.ts:1852-1855 and loader.ts:480-497: registerVirtualModel and unregisterVirtualModel after load are host calls, and an
// unregistered virtual model no longer routes.
#[test]
fn post_load_virtual_model_registration_and_unregistration() {
    let mut ext = Extension::new("api");
    ext.tool("add", "Add", empty_schema(), |ctx, _| {
        let mut model = VirtualModel::new("router", "late", "Late", |_, _| {
            Ok(ModelRoute { model: json!({"provider":"p","id":"m"}), thinking_level: "off".into(), state: None })
        });
        model.thinking_levels = Some(vec!["off".into()]);
        match ctx.register_virtual_model(model) {
            Ok(()) => ToolResult::text("added"),
            Err(err) => ToolResult::Error(err.to_string()),
        }
    });
    ext.tool("remove", "Remove", empty_schema(), |ctx, _| match ctx.unregister_virtual_model("router", "late") {
        Ok(()) => ToolResult::text("removed"),
        Err(err) => ToolResult::Error(err.to_string()),
    });
    let mut host = Host::start(ext, json!({}));
    host.tool_call("a", "add", "c", json!({}));
    let call = host.expect_call("registerVirtualModel");
    assert_eq!(call["call"]["args"], json!({"provider":"router","id":"late","name":"Late","thinkingLevels":["off"]}));
    host.reply(&call, json!({}));
    assert_eq!(host.response("a")["result"]["content"], "added");

    let route = json!({"method":"virtual_model_route","args":{"provider":"router","id":"late","request":{"reason":"user","messages":[]}}});
    host.request("route-a", route.clone());
    assert_eq!(host.response("route-a")["result"], json!({"model":{"provider":"p","id":"m"},"thinkingLevel":"off"}));

    host.tool_call("b", "remove", "c", json!({}));
    let call = host.expect_call("unregisterVirtualModel");
    assert_eq!(call["call"]["args"], json!({"provider":"router","id":"late"}));
    host.reply(&call, json!({}));
    assert_eq!(host.response("b")["result"]["content"], "removed");

    host.request("route-b", route);
    assert!(host.response("route-b")["error"]["message"].as_str().unwrap().contains("router/late"));
    host.finish();
}

// agent-session.ts:1498-1531 (_applyToolLoadout) and types.ts:541-563: prepareLoadout sees the declared, callable and registered
// tools with getExposure (default direct, agent-session.ts:1481) and getNamespace, and returns changes, or undefined (null).
#[test]
fn prepare_loadout_request_runs_the_tools_hook() {
    let mut ext = Extension::new("api");
    let mut orchestrate = ToolDefinition::new("orchestrate", "Orchestrate", "Runs tools", empty_schema(), |_, _| ToolResult::text("x"));
    orchestrate.prepare_loadout = Some(Box::new(|loadout| {
        let names: Vec<_> = loadout.callable.iter().map(|t| t.name.clone()).collect();
        if names.is_empty() {
            return None;
        }
        Some(ToolLoadoutChanges {
            descriptions: Some(
                [(
                    "orchestrate".to_string(),
                    format!(
                        "Runs {} ({}, {}, {:?})",
                        names.join(","),
                        loadout.get_exposure("mcp_search").as_str(),
                        loadout.get_exposure("unknown").as_str(),
                        loadout.get_namespace("mcp_search").map(|n| n.name.clone()),
                    ),
                )]
                .into(),
            ),
            hidden_declarations: Some(loadout.declared.iter().skip(1).map(|t| t.name.clone()).collect()),
        })
    }));
    ext.register_tool(orchestrate);
    let mut host = Host::start(ext, json!({}));
    let payload = json!({
        "declared":[tool("orchestrate"), tool("read")],
        "callable":[tool("read")],
        "registered":[tool("orchestrate"), tool("read")],
        "exposures":{"orchestrate":"direct","read":"direct","mcp_search":"deferred"},
        "namespaces":{"mcp_search":{"name":"mcp__docs"}},
    });
    host.request("l1", json!({"method":"tool_prepare_loadout","tool":"orchestrate","args":payload}));
    assert_eq!(
        host.response("l1")["result"],
        json!({"descriptions":{"orchestrate":"Runs read (deferred, direct, Some(\"mcp__docs\"))"},"hiddenDeclarations":["read"]})
    );

    let empty = json!({"declared":[],"callable":[],"registered":[],"exposures":{}});
    host.request("l2", json!({"method":"tool_prepare_loadout","tool":"orchestrate","args":empty}));
    let response = host.response("l2");
    assert!(response.get("result").is_none_or(Value::is_null), "no changes is null: {response}");
    assert!(response.get("error").is_none(), "{response}");
    host.finish();
}

// types.ts:582-607 through pi.registerTool after load: the declaration the running session receives carries the same fields, and a
// tool registered that way runs its prepareLoadout hook (agent-session.ts:1508).
#[test]
fn post_load_register_tool_carries_the_new_fields_and_its_loadout_hook() {
    let mut ext = Extension::new("api");
    ext.tool("late", "Late", empty_schema(), |ctx, _| {
        let mut late = ToolDefinition::new("added", "Added", "Added later", empty_schema(), |_, _| ToolResult::text("x"));
        late.output_schema = Some(json!({"type":"string"}));
        late.exposure = Some(ToolExposure::Deferred);
        late.namespace = Some(ToolNamespace { name: "mcp__late".into(), description: None, instructions: None });
        late.annotations = Some(ToolAnnotations { destructive_hint: Some(true), ..Default::default() });
        late.default_active = Some(true);
        late.prepare_loadout = Some(Box::new(|loadout| {
            Some(ToolLoadoutChanges { hidden_declarations: Some(vec![format!("saw-{}", loadout.declared.len())]), ..Default::default() })
        }));
        match ctx.register_tool(late) {
            Ok(()) => ToolResult::text("registered"),
            Err(err) => ToolResult::Error(err.to_string()),
        }
    });
    let mut host = Host::start(ext, json!({}));
    host.tool_call("r1", "late", "c", json!({}));
    let call = host.expect_call("registerTool");
    let declaration = &call["call"]["args"];
    assert_eq!(declaration["output_schema"], json!({"type":"string"}));
    assert_eq!(declaration["exposure"], "deferred");
    assert_eq!(declaration["namespace"], json!({"name":"mcp__late"}));
    assert_eq!(declaration["annotations"], json!({"destructiveHint":true}));
    assert_eq!(declaration["default_active"], true);
    assert_eq!(declaration["prepares_loadout"], true);
    host.reply(&call, json!({}));
    assert_eq!(host.response("r1")["result"]["content"], "registered");
    host.request("l1", json!({"method":"tool_prepare_loadout","tool":"added","args":{"declared":[tool("a"), tool("b")],"callable":[],"registered":[],"exposures":{}}}));
    assert_eq!(host.response("l1")["result"], json!({"hiddenDeclarations":["saw-2"]}));
    host.finish();
}

// runner.ts:966-983 (createToolContext executeTool) and types.ts:367-395: executeTool is a host call named by the calling tool
// call's id, cancelled with the calling request; partial results reach onUpdate in order before the outcome.
#[test]
fn execute_tool_runs_a_nested_call_and_delivers_updates_in_order() {
    let updates: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
    let recorded = updates.clone();
    let mut ext = Extension::new("api");
    ext.tool("caller", "Caller", empty_schema(), move |ctx, _| {
        let recorded = recorded.clone();
        let outcome = ctx
            .execute_tool(
                "target",
                json!({"q":"x"}),
                ExecuteToolOptions {
                    signal: None,
                    on_update: Some(Arc::new(move |partial: AgentToolResult| {
                        // A slow callback: an implementation that returns before the updates are delivered loses the later ones.
                        std::thread::sleep(Duration::from_millis(30));
                        recorded.lock().unwrap().push(partial.content[0]["text"].as_str().unwrap().to_string());
                    })),
                },
            )
            .unwrap();
        ToolResult::json(json!({"content":"done","details":outcome}))
    });
    let mut host = Host::start(ext, json!({}));
    host.tool_call("r1", "caller", "call-7", json!({}));
    let call = host.expect_call("executeTool");
    assert_eq!(call["call"]["parent_request_id"], "r1", "the calling request cancels the nested call by default: {call}");
    let args = &call["call"]["args"];
    assert_eq!(args["callerId"], "call-7");
    assert_eq!(args["name"], "target");
    assert_eq!(args["args"], json!({"q":"x"}));
    assert_eq!(args["wantsUpdates"], true);
    let execute_id = args["executeId"].as_str().unwrap().to_string();
    assert!(!execute_id.is_empty());
    // The host sends each partial result as a request and waits for its answer (nested-tool-calls.ts:219-231); the answer is null when the callback returned.
    for (n, text) in ["one", "two", "three"].into_iter().enumerate() {
        let id = format!("u{n}");
        host.request(&id, json!({"method":"execute_tool_update","args":{"executeId":execute_id,"result":{"content":[{"type":"text","text":text}],"details":{}}}}));
        let answer = host.response(&id);
        assert!(answer["error"].is_null(), "update {text} answer: {answer}");
    }
    host.request("u-stray", json!({"method":"execute_tool_update","args":{"executeId":"other","result":{"content":[{"type":"text","text":"stray"}]}}}));
    assert!(host.response("u-stray")["error"].is_null());
    host.reply(
        &call,
        json!({
            "toolCall":{"type":"toolCall","id":"call-7/1","name":"target","arguments":{"q":"x"}},
            "result":{"content":[{"type":"text","text":"found"}],"details":{"n":1},"structuredContent":{"hits":3},"isError":false},
            "isError":false,
        }),
    );
    let response = host.response("r1");
    let details = &response["result"]["details"];
    assert_eq!(details["toolCall"]["id"], "call-7/1");
    assert_eq!(details["result"]["structuredContent"], json!({"hits":3}));
    assert_eq!(details["isError"], false);
    assert_eq!(
        *updates.lock().unwrap(),
        vec!["one", "two", "three"],
        "every partial result is delivered, in order, before executeTool returns"
    );
    host.finish();
}

// runner.ts:979-981 (`{ ...options, signal: options.signal ?? signal }`): a nested tool runs with the signal its caller passed, and with the calling tool's otherwise, so its `signal` parameter differs only in the first case. The call tells the host which case it is.
#[test]
fn execute_tool_marks_an_explicit_signal() {
    let mut ext = Extension::new("api");
    ext.tool("caller", "Caller", empty_schema(), |ctx, _| {
        let own = ProviderSignal::new();
        ctx.execute_tool("inner", json!({}), ExecuteToolOptions::default()).unwrap();
        ctx.execute_tool("inner", json!({}), ExecuteToolOptions { signal: Some(own), on_update: None }).unwrap();
        ToolResult::json(json!({"content":"done"}))
    });
    let mut host = Host::start(ext, json!({}));
    host.tool_call("r1", "caller", "call-8", json!({}));
    let outcome = json!({
        "toolCall":{"type":"toolCall","id":"call-8/1","name":"inner","arguments":{}},
        "result":{"content":[{"type":"text","text":"ok"}],"details":{}},
        "isError":false,
    });
    let first = host.expect_call("executeTool");
    assert!(first["call"]["args"].get("ownSignal").is_none(), "no signal option, no ownSignal: {first}");
    host.reply(&first, outcome.clone());
    let second = host.expect_call("executeTool");
    assert_eq!(second["call"]["args"]["ownSignal"], true, "an explicit signal is marked: {second}");
    host.reply(&second, outcome);
    host.response("r1");
    host.finish();
}

// runner.ts:966-983 and types.ts:389-393: "Never rejects for tool failures: unknown tools, validation errors, blocked calls, and
// thrown errors come back as isError: true." Without onUpdate the call does not ask for updates.
#[test]
fn execute_tool_failure_is_an_outcome_not_an_error() {
    let mut ext = Extension::new("api");
    ext.tool("caller", "Caller", empty_schema(), |ctx, _| {
        match ctx.execute_tool("missing", json!({}), ExecuteToolOptions::default()) {
            Ok(outcome) => ToolResult::json(json!({"content":"ran","details":outcome})),
            Err(err) => ToolResult::Error(format!("rejected: {err}")),
        }
    });
    let mut host = Host::start(ext, json!({}));
    host.tool_call("r1", "caller", "call-1", json!({}));
    let call = host.expect_call("executeTool");
    assert!(call["call"]["args"].get("wantsUpdates").is_none(), "{call}");
    host.reply(
        &call,
        json!({
            "toolCall":{"type":"toolCall","id":"call-1/1","name":"missing","arguments":{}},
            "result":{"content":[{"type":"text","text":"Tool missing not found"}],"details":{}},
            "isError":true,
        }),
    );
    let response = host.response("r1");
    assert_eq!(response["result"]["content"], "ran");
    assert_eq!(response["result"]["details"]["isError"], true);
    assert_eq!(response["result"]["details"]["result"]["content"][0]["text"], "Tool missing not found");
    host.finish();
}

// types.ts:367-372 (ExecuteToolOptions.signal): the caller's signal aborts the nested call; the host is told with executeTool.cancel
// naming the call, after the executeTool call itself.
#[test]
fn execute_tool_signal_cancels_the_nested_call() {
    let signal = ProviderSignal::new();
    let tool_signal = signal.clone();
    let (started_tx, started_rx) = mpsc::channel::<()>();
    let started_tx = Mutex::new(started_tx);
    let mut ext = Extension::new("api");
    ext.tool("caller", "Caller", empty_schema(), move |ctx, _| {
        started_tx.lock().unwrap().send(()).unwrap();
        let outcome = ctx
            .execute_tool("slow", json!({}), ExecuteToolOptions { signal: Some(tool_signal.clone()), on_update: None })
            .unwrap();
        ToolResult::json(json!({"content":"returned","details":outcome}))
    });
    let mut host = Host::start(ext, json!({}));
    host.tool_call("r1", "caller", "call-1", json!({}));
    started_rx.recv_timeout(Duration::from_secs(10)).unwrap();
    let call = host.expect_call("executeTool");
    let execute_id = call["call"]["args"]["executeId"].as_str().unwrap().to_string();
    signal.cancel();
    let cancel = host.expect_call("executeTool.cancel");
    assert_eq!(cancel["call"]["args"], json!({"executeId":execute_id}));
    host.reply(&cancel, json!({}));
    host.reply(
        &call,
        json!({
            "toolCall":{"type":"toolCall","id":"call-1/1","name":"slow","arguments":{}},
            "result":{"content":[{"type":"text","text":"aborted"}],"details":{}},
            "isError":true,
        }),
    );
    assert_eq!(host.response("r1")["result"]["details"]["isError"], true);
    host.finish();
}

// runner.ts:952: the context with executeTool exists for a tool call, whose id names the nested calls.
#[test]
fn execute_tool_outside_a_tool_call_is_an_error() {
    let mut ext = Extension::new("api");
    ext.command("nested", "Nested", |ctx, _| match ctx.execute_tool("read", json!({}), ExecuteToolOptions::default()) {
        Ok(_) => pig_sdk::CommandResult::Ok,
        Err(err) => pig_sdk::CommandResult::Error(err.to_string()),
    });
    let mut host = Host::start(ext, json!({}));
    host.request("c1", json!({"method":"command","tool":"nested","args":""}));
    let response = host.response("c1");
    assert!(response["error"]["message"].as_str().unwrap().contains("only available while a tool runs"), "{response}");
    host.finish();
}

// agent types.ts:433-440 (structuredContent, isError): a result carries structured content, and isError reports a failure without
// throwing while the details and structured content stay.
#[test]
fn tool_result_carries_structured_content_and_is_error() {
    let mut ext = Extension::new("api");
    ext.tool("structured", "Structured", empty_schema(), |_, _| {
        ToolResult::text("3 hits").with_structured_content(json!({"hits":3}))
    });
    ext.tool("failing", "Failing", empty_schema(), |_, _| {
        ToolResult::text("upstream said no")
            .with_details(json!({"status":503}))
            .with_structured_content(json!({"ok":false}))
            .with_is_error(true)
    });
    ext.tool("json", "Json", empty_schema(), |_, _| {
        ToolResult::json(json!({"content":"j"})).with_structured_content(json!([1, 2]))
    });
    let mut host = Host::start(ext, json!({}));
    host.tool_call("s", "structured", "c", json!({}));
    assert_eq!(host.response("s")["result"], json!({"content":"3 hits","structured_content":{"hits":3}}));
    host.tool_call("f", "failing", "c", json!({}));
    assert_eq!(
        host.response("f")["result"],
        json!({"content":"upstream said no","details":{"status":503},"structured_content":{"ok":false},"is_error":true})
    );
    host.tool_call("j", "json", "c", json!({}));
    assert_eq!(host.response("j")["result"], json!({"content":"j","structured_content":[1, 2]}));
    host.finish();
}

// types.ts:2063 (ToolInfo.exposure, namespace, annotations): getAllTools reports how the model reaches each tool.
#[test]
fn get_all_tools_reports_exposure_namespace_and_annotations() {
    let mut ext = Extension::new("api");
    ext.tool("list", "List", empty_schema(), |ctx, _| {
        let tools = ctx.get_all_tools().unwrap();
        ToolResult::json(json!({"content":"x","details":tools.iter().map(|t| json!({
            "name": t.name,
            "exposure": t.exposure.as_str(),
            "namespace": t.namespace,
            "annotations": t.annotations,
        })).collect::<Vec<_>>()}))
    });
    let mut host = Host::start(ext, json!({}));
    host.tool_call("r1", "list", "c", json!({}));
    let call = host.expect_call("getAllTools");
    host.reply(
        &call,
        json!({"tools":[
            {"name":"search","description":"d","parameters":{},"sourceInfo":{"path":"p","source":"s","scope":"user","origin":"top-level"},
             "exposure":"deferred","namespace":{"name":"mcp__docs","description":"Docs"},"annotations":{"readOnlyHint":true}},
            {"name":"read","description":"d","parameters":{},"sourceInfo":{"path":"p","source":"builtin","scope":"user","origin":"top-level"},"exposure":"direct"},
        ]}),
    );
    let details = host.response("r1")["result"]["details"].clone();
    assert_eq!(details[0], json!({"name":"search","exposure":"deferred","namespace":{"name":"mcp__docs","description":"Docs"},"annotations":{"readOnlyHint":true}}));
    assert_eq!(details[1], json!({"name":"read","exposure":"direct","namespace":null,"annotations":null}));
    host.finish();
}

// types.ts:699 (McpServersChangeEvent) and 884 (ProviderStreamEvent): both events reach a handler with the host's payload.
#[test]
fn mcp_servers_change_and_provider_stream_event_reach_handlers() {
    let seen: Arc<Mutex<Vec<Value>>> = Arc::new(Mutex::new(Vec::new()));
    let mut ext = Extension::new("api");
    let recorded = seen.clone();
    ext.on_event(pig_sdk::EVENT_MCP_SERVERS_CHANGE, false, move |ctx, data| {
        // loader.ts:473-476: getMcpServers answers the live registry, which the host replicates before it delivers the event, not the
        // event's snapshot (runner.ts:458 emits without awaiting, so a later change can precede the handler).
        recorded.lock().unwrap().push(json!({"event": data.clone(), "getMcpServers": ctx.get_mcp_servers()}));
        None
    });
    let recorded = seen.clone();
    ext.on_event(pig_sdk::EVENT_PROVIDER_STREAM_EVENT, false, move |_, data| {
        recorded.lock().unwrap().push(data.clone());
        None
    });
    let mut host = Host::start(ext, json!({}));
    let handlers = host.register["register"]["handlers"].clone();
    assert_eq!(handlers[0]["event"], "mcp_servers_change");
    assert_eq!(handlers[1]["event"], "provider_stream_event");
    host.notify(
        "state_update",
        json!({"state":{"mcpServers":[
            {"name":"docs","config":{"url":"https://x"},"extensionPath":"/a"},
            {"name":"later","config":{"command":"y"},"extensionPath":"/b"},
        ]}}),
    );
    host.request("e1", json!({"method":"event","event":"mcp_servers_change","handler_id":handlers[0]["handler_id"],
        "args":{"type":"mcp_servers_change","servers":[{"name":"docs","config":{"url":"https://x"},"extensionPath":"/a"}]}}));
    host.response("e1");
    host.request("e2", json!({"method":"event","event":"provider_stream_event","handler_id":handlers[1]["handler_id"],
        "args":{"type":"provider_stream_event","provider":"openai","api":"openai-completions","model":"gpt","data":{"choices":[]}}}));
    host.response("e2");
    let seen = seen.lock().unwrap();
    assert_eq!(seen[0]["event"]["servers"][0]["name"], "docs");
    assert_eq!(
        seen[0]["getMcpServers"],
        json!([
            {"name":"docs","config":{"url":"https://x"},"extensionPath":"/a"},
            {"name":"later","config":{"command":"y"},"extensionPath":"/b"},
        ])
    );
    assert_eq!(seen[1]["data"], json!({"choices":[]}));
    drop(seen);
    host.finish();
}

//! Which wire call each `AgentSession` member of the Rust SDK sends, against a fake host. Pi declares eleven members `async` (core/agent-session.ts); they go over
//! `childSession.opAsync`, so a call made from another thread (an abort) never waits behind them in the host's lane. Every other member goes over `childSession.op`. A member on
//! the wrong lane is answered with "<op> is on the wrong lane", which fails the member.
#![cfg(unix)]

use pig_sdk::{
    CreateAgentSessionOptions, CustomMessage, ExecuteBashOptions, Extension, ModelMutationOptions,
    SendMessageOptions, SendUserMessageOptions, ToolDefinition, ToolResult,
};
use serde_json::{Value, json};
use std::collections::BTreeSet;
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::time::Duration;

/// Pi's `async` members of AgentSession.
const ASYNC: [&str; 11] = [
    "sendUserMessage",
    "waitForIdle",
    "setModel",
    "cycleModel",
    "compact",
    "reload",
    "executeBash",
    "navigateTree",
    "exportToHtml",
    "summarizeForBugReport",
    "sendCustomMessage",
];

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

/// A value of the shape Pi's member returns, so the typed decode of the SDK accepts it.
fn answer(op: &str) -> Value {
    match op {
        "isStreaming"
        | "isIdle"
        | "isCompacting"
        | "isRetrying"
        | "isBashRunning"
        | "hasPendingBashMessages"
        | "autoCompactionEnabled"
        | "autoRetryEnabled"
        | "supportsThinking"
        | "hasExtensionHandlers" => json!(true),
        "retryAttempt" | "pendingMessageCount" => json!(1),
        "sessionId"
        | "systemPrompt"
        | "thinkingLevel"
        | "steeringMode"
        | "followUpMode"
        | "sessionFile"
        | "sessionName"
        | "getLastAssistantText"
        | "cycleThinkingLevel"
        | "exportToHtml"
        | "exportToJsonl"
        | "summarizeForBugReport" => json!("x"),
        "model" | "routedModel" | "cacheWarmingStatus" | "cycleModel" => json!({"id": "m"}),
        "getToolDefinition" => json!({"name": "t"}),
        "scopedModels"
        | "getActiveToolNames"
        | "getCallableToolNames"
        | "getSteeringMessages"
        | "getFollowUpMessages"
        | "getAvailableThinkingLevels"
        | "getAllTools"
        | "getUserMessagesForForking"
        | "promptTemplates" => json!([]),
        "getContextUsage" => json!({"tokens": 1, "contextWindow": 2, "percent": 0.5}),
        "clearQueue" => json!({"steering": [], "followUp": []}),
        "getSessionStats" | "compact" | "navigateTree" | "executeBash" => json!({}),
        _ => Value::Null,
    }
}

type Member = (
    &'static str,
    Box<dyn Fn(&pig_sdk::AgentSession) -> std::io::Result<()> + Send + Sync>,
);

fn members() -> Vec<Member> {
    let none = ModelMutationOptions::default();
    let model = json!({"provider": "p", "id": "m"});
    let mut list: Vec<Member> = Vec::new();
    macro_rules! member {
        ($op:literal, |$s:ident| $call:expr) => {
            list.push(($op, Box::new(move |$s| $call.map(|_| ()))));
        };
    }
    member!("isStreaming", |s| s.is_streaming());
    member!("isIdle", |s| s.is_idle());
    member!("isCompacting", |s| s.is_compacting());
    member!("isRetrying", |s| s.is_retrying());
    member!("isBashRunning", |s| s.is_bash_running());
    member!("hasPendingBashMessages", |s| s.has_pending_bash_messages());
    member!("retryAttempt", |s| s.retry_attempt());
    member!("pendingMessageCount", |s| s.pending_message_count());
    member!("sessionId", |s| s.current_session_id());
    member!("sessionFile", |s| s.current_session_file());
    member!("sessionName", |s| s.session_name());
    member!("systemPrompt", |s| s.current_system_prompt());
    member!("thinkingLevel", |s| s.current_thinking_level());
    member!("model", |s| s.current_model());
    member!("routedModel", |s| s.routed_model());
    member!("scopedModels", |s| s.scoped_models());
    member!("steeringMode", |s| s.steering_mode());
    member!("followUpMode", |s| s.follow_up_mode());
    member!("autoCompactionEnabled", |s| s.auto_compaction_enabled());
    member!("autoRetryEnabled", |s| s.auto_retry_enabled());
    member!("supportsThinking", |s| s.supports_thinking());
    member!("getAvailableThinkingLevels", |s| s
        .get_available_thinking_levels());
    member!("getLastAssistantText", |s| s.get_last_assistant_text());
    member!("getActiveToolNames", |s| s.get_active_tool_names());
    member!("getCallableToolNames", |s| s.get_callable_tool_names());
    member!("getSteeringMessages", |s| s.get_steering_messages());
    member!("getFollowUpMessages", |s| s.get_follow_up_messages());
    member!("getAllTools", |s| s.get_all_tools());
    member!("getToolDefinition", |s| s.get_tool_definition("t"));
    member!("getContextUsage", |s| s.get_context_usage());
    member!("getSessionStats", |s| s.get_session_stats());
    member!("getUserMessagesForForking", |s| s
        .get_user_messages_for_forking());
    member!("promptTemplates", |s| s.prompt_templates());
    member!("cacheWarmingStatus", |s| s.cache_warming_status());
    member!("hasExtensionHandlers", |s| s.has_extension_handlers("e"));
    member!("setModel", |s| s.set_model(&model, none));
    member!("cycleModel", |s| s.cycle_model("forward", none));
    member!("setThinkingLevel", |s| s.set_thinking_level("off", none));
    member!("cycleThinkingLevel", |s| s.cycle_thinking_level(none));
    member!("setScopedModels", |s| s.set_scoped_models(&[]));
    member!("setActiveToolsByName", |s| s
        .set_active_tools_by_name(&["t"]));
    member!("setSteeringMode", |s| s.set_steering_mode("all"));
    member!("setFollowUpMode", |s| s.set_follow_up_mode("all"));
    member!("setAutoCompactionEnabled", |s| s
        .set_auto_compaction_enabled(true));
    member!("setAutoRetryEnabled", |s| s.set_auto_retry_enabled(true));
    member!("setCacheWarmingMode", |s| s.set_cache_warming_mode("off"));
    member!("setSessionName", |s| s.set_session_name("n"));
    member!("clearQueue", |s| s.clear_queue());
    member!("abortBash", |s| s.abort_bash());
    member!("abortBranchSummary", |s| s.abort_branch_summary());
    member!("abortCompaction", |s| s.abort_compaction());
    member!("abortRetry", |s| s.abort_retry());
    member!("waitForIdle", |s| s.wait_for_idle());
    member!("reload", |s| s.reload());
    member!("compact", |s| s.compact(None));
    member!("navigateTree", |s| s.navigate_tree("e", Value::Null));
    member!("executeBash", |s| s
        .execute_bash("c", ExecuteBashOptions::default()));
    member!("recordBashResult", |s| s.record_bash_result(
        "c",
        &json!({}),
        ExecuteBashOptions::default()
    ));
    member!("exportToHtml", |s| s.export_to_html(None, None));
    member!("exportToJsonl", |s| s.export_to_jsonl(None));
    member!("summarizeForBugReport", |s| s
        .summarize_for_bug_report(None));
    member!("sendCustomMessage", |s| s.send_custom_message(
        &CustomMessage {
            custom_type: "t".into(),
            content: json!("c"),
            display: true,
            details: None
        },
        &SendMessageOptions::default()
    ));
    member!("sendUserMessage", |s| s.send_user_message(
        json!("c"),
        &SendUserMessageOptions::default()
    ));
    list
}

#[test]
fn every_member_goes_over_the_lane_pi_gives_it() {
    let mut ext = Extension::new("members");
    ext.register_tool(ToolDefinition::new(
        "probe",
        "probe",
        "probe",
        json!({"type": "object"}),
        |ctx, _args: Value| {
            let session = match ctx.create_agent_session(CreateAgentSessionOptions::default()) {
                Ok(session) => session,
                Err(error) => return ToolResult::Error(error.to_string()),
            };
            let mut failures = Vec::new();
            for (op, call) in members() {
                if let Err(error) = call(&session) {
                    failures.push(format!("{op}: {error}"));
                }
            }
            session.dispose();
            ToolResult::text(json!(failures).to_string())
        },
    ));

    let root = std::env::temp_dir().join(format!("pig-members-{}", std::process::id()));
    std::fs::create_dir(&root).unwrap();
    let socket = root.join("sock");
    let listener = UnixListener::bind(&socket).unwrap();
    let runner = std::thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
    let (mut stream, _) = listener.accept().unwrap();
    stream
        .set_read_timeout(Some(Duration::from_secs(20)))
        .unwrap();
    assert_eq!(read(&mut stream)["type"], "register");
    send(
        &mut stream,
        &json!({"type":"ready","ready":{"state":{},"width":80,"mode":"tui"}}),
    );
    send(
        &mut stream,
        &json!({"type":"request","id":"p1","request":{"method":"tool_call","tool":"probe","tool_call_id":"tc1","args":{}}}),
    );

    let mut seen = BTreeSet::new();
    let mut id = Value::Null;
    let response = loop {
        let frame = read(&mut stream);
        match frame["type"].as_str() {
            Some("call") => {
                let call = &frame["call"];
                let method = call["method"].as_str().unwrap_or("");
                let reply = match method {
                    "childSession.create" => {
                        id = call["args"]["id"].clone();
                        json!({"sessionId": id})
                    }
                    "childSession.dispose" => Value::Null,
                    "childSession.op" | "childSession.opAsync" => {
                        let op = call["args"]["op"].as_str().unwrap().to_string();
                        assert_eq!(call["args"]["id"], id, "{op}");
                        let on_async = method == "childSession.opAsync";
                        if on_async != ASYNC.contains(&op.as_str()) {
                            send(
                                &mut stream,
                                &json!({"type":"call_result","id":frame["id"],"call_result":{"error":{"message":format!("{op} is on the wrong lane")}}}),
                            );
                            continue;
                        }
                        seen.insert(op.clone());
                        answer(&op)
                    }
                    other => panic!("unexpected call {other}"),
                };
                send(
                    &mut stream,
                    &json!({"type":"call_result","id":frame["id"],"call_result":{"result":reply}}),
                );
            }
            Some("response") => break frame["response"].clone(),
            _ => {}
        }
    };
    send(
        &mut stream,
        &json!({"type":"shutdown","shutdown":{"reason":"done"}}),
    );
    runner.join().unwrap().unwrap();
    let _ = std::fs::remove_dir_all(&root);

    let text = response["result"]["content"]
        .as_str()
        .unwrap_or_else(|| panic!("response: {response}"));
    assert_eq!(text, "[]", "members that failed");
    let expected: BTreeSet<String> = members()
        .into_iter()
        .map(|(op, _)| op.to_string())
        .collect();
    assert_eq!(seen, expected, "ops the host saw");
    assert_eq!(
        expected.len(),
        63,
        "the members over childSession.op and childSession.opAsync"
    );
    for op in ASYNC {
        assert!(seen.contains(op), "{op} is async in Pi and was not called");
    }
}

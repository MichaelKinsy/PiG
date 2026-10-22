//! Every pure pi-ai helper returns what Pi's own pi-ai returned at the pin for each case of `test/parity/sdk-vectors/pi-ai/*.json` (written by `automation/gen/generate-sdk-vectors.mjs`).

use crate::pi_ai_helpers::*;
use crate::provider::{APIKeyAuthInput, AuthContext, AuthInteraction, ProviderSignal};
use serde_json::{Value, json};
use std::sync::Arc;

const VECTORS: &str = concat!(env!("CARGO_MANIFEST_DIR"), "/../../test/parity/sdk-vectors");

fn is_undefined(value: &Value) -> bool {
    value == &json!({"$": "undefined"})
}

fn read_vectors(module: &str, export: &str) -> Vec<Value> {
    let path = format!("{VECTORS}/{module}/{export}.json");
    let file: Value = serde_json::from_str(&std::fs::read_to_string(&path).unwrap_or_else(|e| panic!("{path}: {e}"))).unwrap();
    let cases = file["cases"].as_array().unwrap().clone();
    assert!(!cases.is_empty(), "{path} has no cases");
    cases
}

/// The pi-ai vectors as the tests below consume them: an argument or answer that is undefined has no value.
fn golden() -> Value {
    let mut cases = Vec::new();
    let mut exports: Vec<String> = std::fs::read_dir(format!("{VECTORS}/pi-ai")).unwrap().map(|entry| entry.unwrap().path()).filter(|path| path.extension().is_some_and(|ext| ext == "json")).map(|path| path.file_stem().unwrap().to_string_lossy().into_owned()).collect();
    exports.sort();
    for export in exports.iter().filter(|export| !matches!(export.as_str(), "envApiKeyAuth" | "createAssistantMessageEventStream")) {
        for case in read_vectors("pi-ai", export) {
            let args: Vec<Value> = case["args"].as_array().unwrap().iter().map(|arg| if is_undefined(arg) { Value::Null } else { arg.clone() }).collect();
            let mut entry = json!({ "fn": export, "args": args });
            if !is_undefined(&case["result"]) {
                entry["result"] = case["result"].clone();
            }
            cases.push(entry);
        }
    }
    let (mut auth_cases, mut login) = (Vec::new(), Value::Null);
    for case in read_vectors("pi-ai", "envApiKeyAuth") {
        let args = case["args"].as_array().unwrap();
        if args[2] == json!("$login") {
            login = json!({ "prompts": case["result"]["prompts"], "result": case["result"]["result"] });
            continue;
        }
        let mut entry = json!({ "name": args[0], "envVars": args[1], "env": args[3], "seen": case["result"]["seen"] });
        if !is_undefined(&args[2]) {
            entry["credential"] = args[2].clone();
        }
        if !case["result"]["result"].is_null() {
            entry["result"] = case["result"]["result"].clone();
        }
        auth_cases.push(entry);
    }
    let timing: Vec<Value> = read_vectors("pi-ai", "createAssistantMessageEventStream")
        .into_iter()
        .map(|case| {
            let (scenario, answer) = (&case["args"][0], &case["result"]);
            let kept = if answer["kept"] == json!(true) { scenario["preset"].clone() } else { Value::Null };
            json!({ "kind": scenario["kind"], "timestamp": scenario["timestamp"], "preset": scenario["preset"], "present": answer["present"], "value": kept, "laterTimed": answer["laterTimed"] })
        })
        .collect();
    json!({ "cases": cases, "auth": { "cases": auth_cases, "login": login }, "timing": timing })
}

/// Same value, with the keys of every object in the order `Object.keys` gives them (which is what `JSON.stringify` writes) and a NaN or infinity as the null `JSON.stringify` makes of it.
fn same(got: &Value, want: &Value, path: &str) -> Result<(), String> {
    match (got, want) {
        (Value::Object(got), Value::Object(want)) => {
            let (got_keys, want_keys) = (ordered_keys(got), ordered_keys(want));
            if got_keys != want_keys {
                return Err(format!("{path}: keys {got_keys:?}, want {want_keys:?}"));
            }
            got_keys.into_iter().try_for_each(|key| same(&got[key], &want[key], &format!("{path}.{key}")))
        }
        (Value::Array(got), Value::Array(want)) if got.len() == want.len() => got.iter().zip(want).enumerate().try_for_each(|(i, (got, want))| same(got, want, &format!("{path}[{i}]"))),
        (Value::Number(got), Value::Number(want)) if got.as_f64() == want.as_f64() => Ok(()),
        (got, want) if got == want => Ok(()),
        (got, want) => Err(format!("{path}: {got}, want {want}")),
    }
}

fn finite(value: f64) -> Value {
    serde_json::Number::from_f64(value).map_or(Value::Null, Value::Number)
}

fn run(fn_name: &str, args: &[Value]) -> Option<Value> {
    let a = |i: usize| &args[i];
    let messages = |i: usize| a(i).as_array().unwrap().clone();
    let flag = |i: usize| a(i).as_bool().unwrap();
    let float = |i: usize| a(i).as_f64().unwrap();
    Some(match fn_name {
        "StringEnum" => {
            let values: Vec<&str> = a(0).as_array().unwrap().iter().map(|v| v.as_str().unwrap()).collect();
            let options = args.get(1).map(|o| StringEnumOptions { description: o.get("description").and_then(Value::as_str).map(String::from), default: o.get("default").and_then(Value::as_str).map(String::from) });
            string_enum(&values, options.as_ref())
        }
        "getSupportedThinkingLevels" => json!(get_supported_thinking_levels(a(0))),
        "clampThinkingLevel" => json!(clamp_thinking_level(a(0), a(1).as_str().unwrap())),
        "calculateCost" => {
            let mut usage = a(1).clone();
            let cost = calculate_cost(a(0), &mut usage).unwrap();
            assert_eq!(usage["cost"], cost, "the cost is stored in usage");
            Value::Object(cost.as_object().unwrap().iter().map(|(key, value)| (key.clone(), finite(value.as_f64().unwrap()))).collect())
        }
        "contentText" => json!(content_text(Some(a(0)), args.get(1).map_or("\n", |s| s.as_str().unwrap())).unwrap()),
        "getCurrentTools" => json!(get_current_tools(&messages(0))),
        "getCurrentSystemMessage" => return get_current_system_message(&messages(0)).unwrap(),
        "getCurrentSystemPrompt" => json!(get_current_system_prompt(&messages(0)).unwrap()),
        "getInitialSystemMessage" => return get_initial_system_message(&messages(0)).cloned(),
        "getDeclaredTools" => json!(get_declared_tools(&messages(0))),
        "hasNonAdditiveToolChanges" => json!(has_non_additive_tool_changes(&messages(0))),
        "resolveTranscriptTools" => {
            let support = match a(1).as_str().unwrap() {
                "none" => TranscriptToolSupport::None,
                "additions" => TranscriptToolSupport::Additions,
                "changes" => TranscriptToolSupport::Changes,
                other => panic!("unknown transcript tool support {other:?}"),
            };
            let resolved = resolve_transcript_tools(&messages(0), support);
            json!({ "requestTools": resolved.request_tools, "foldedSystemMessages": resolved.folded_system_messages })
        }
        "collapseSystemMessages" => collapse_system_messages(a(0)).unwrap(),
        "resolveTranscript" => resolve_transcript(a(0), flag(1)).unwrap(),
        "withoutInitialSystemMessage" => json!(without_initial_system_message(&messages(0))),
        "normalizeContext" => normalize_context(a(0)),
        "createInitialSystemMessage" => {
            let tools = args.get(1).filter(|tools| !tools.is_null()).map(|tools| tools.as_array().unwrap().clone());
            return create_initial_system_message(a(0).as_str(), tools.as_deref());
        }
        "getSystemMessageText" => json!(get_system_message_text(a(0)).unwrap()),
        "renderSystemMessageUpdate" => json!(render_system_message_update(a(0)).unwrap()),
        "getModelType" => json!(get_model_type(a(0))),
        "isModelType" => json!(is_model_type(a(0), a(1).as_str().unwrap())),
        "hasApi" => json!(has_api(a(0), a(1).as_str().unwrap())),
        "modelsAreEqual" => json!(models_are_equal(Some(a(0)).filter(|first| !first.is_null()), Some(a(1)).filter(|other| !other.is_null()))),
        "isRetryableAssistantError" => json!(is_retryable_assistant_error(a(0))),
        "isContextOverflow" => json!(is_context_overflow(a(0), args.get(1).map(|w| w.as_f64().unwrap()))),
        "isRecoverableLength" => json!(is_recoverable_length(a(0), float(1))),
        "retryDelayMs" => {
            let policy = RetryPolicy { base_delay_ms: a(0)["baseDelayMs"].as_f64().unwrap(), max_agent_delay_ms: a(0).get("maxAgentDelayMs").and_then(Value::as_f64) };
            finite(retry_delay_ms(&policy, a(1).as_i64().unwrap()))
        }
        other => panic!("no Rust counterpart for {other}"),
    })
}

#[test]
fn every_helper_matches_the_pinned_package() {
    let golden = golden();
    let (mut failures, mut counted) = (Vec::new(), std::collections::BTreeSet::new());
    for case in golden["cases"].as_array().unwrap() {
        let fn_name = case["fn"].as_str().unwrap();
        counted.insert(fn_name.to_string());
        let got = run(fn_name, case["args"].as_array().unwrap());
        let outcome = match (&got, case.get("result")) {
            (None, None) => Ok(()),
            (Some(got), Some(want)) => same(got, want, fn_name),
            (got, want) => Err(format!("{fn_name}: {got:?}, want {want:?}")),
        };
        if let Err(message) = outcome {
            failures.push(format!("{} {}: {message}", fn_name, case["args"]));
        }
    }
    assert!(failures.is_empty(), "{} failures, first: {:?}", failures.len(), &failures[..failures.len().min(3)]);
    for fn_name in ["StringEnum", "calculateCost", "clampThinkingLevel", "contentText", "getCurrentSystemMessage", "isContextOverflow", "isRetryableAssistantError", "normalizeContext", "retryDelayMs"] {
        assert!(counted.contains(fn_name), "the golden file has no {fn_name} call");
    }
}

#[test]
fn env_api_key_auth_matches_the_pinned_package() {
    let golden = golden();
    for case in golden["auth"]["cases"].as_array().unwrap() {
        let vars: Vec<&str> = case["envVars"].as_array().unwrap().iter().map(|v| v.as_str().unwrap()).collect();
        let auth = env_api_key_auth(case["name"].as_str().unwrap(), &vars);
        let env = case["env"].clone();
        let seen = Arc::new(std::sync::Mutex::new(Vec::new()));
        let seen_in = seen.clone();
        let input = APIKeyAuthInput {
            ctx: AuthContext {
                env: Arc::new(move |name| {
                    seen_in.lock().unwrap().push(name.clone());
                    Ok(env.get(&name).and_then(Value::as_str).map(String::from))
                }),
                file_exists: Arc::new(|_| Ok(false)),
            },
            credential: case.get("credential").filter(|c| !c.is_null()).cloned(),
            signal: ProviderSignal::default(),
        };
        let result = (auth.resolve)(input).unwrap();
        let got = result.map(|result| serde_json::to_value(result).unwrap());
        let want = case.get("result");
        match (&got, want) {
            (None, None) => {}
            (Some(got), Some(want)) => same(got, want, "result").unwrap(),
            (got, want) => panic!("{case}: {got:?}, want {want:?}"),
        }
        assert_eq!(*seen.lock().unwrap(), case["seen"].as_array().unwrap().iter().map(|v| v.as_str().unwrap().to_string()).collect::<Vec<_>>(), "{case}");
    }
    let auth = env_api_key_auth("Name", &["FIRST", "SECOND"]);
    let prompts = Arc::new(std::sync::Mutex::new(Vec::new()));
    let prompts_in = prompts.clone();
    let credential = (auth.login.unwrap())(AuthInteraction {
        signal: ProviderSignal::default(),
        prompt: Arc::new(move |request| {
            prompts_in.lock().unwrap().push(request);
            Ok("typed-key".to_string())
        }),
        notify: Arc::new(|_| Ok(())),
    })
    .unwrap();
    assert_eq!(Value::Array(prompts.lock().unwrap().clone()), golden["auth"]["login"]["prompts"]);
    same(&credential, &golden["auth"]["login"]["result"], "login").unwrap();
    assert!(auth.check.is_none());
}

#[test]
fn a_cancelled_signal_stops_the_auth() {
    let auth = env_api_key_auth("Name", &["FIRST"]);
    let signal = ProviderSignal::default();
    signal.cancel();
    let input = APIKeyAuthInput { ctx: AuthContext { env: Arc::new(|_| Ok(Some("x".into()))), file_exists: Arc::new(|_| Ok(false)) }, credential: None, signal };
    assert!((auth.resolve)(input).is_err());
}

/// The golden file's "timing" scenarios: how the final message reaches a stream made by `create_assistant_message_event_stream` and what it carries.
#[test]
fn the_event_stream_times_its_final_message_as_pi_does() {
    let golden = golden();
    let scenarios = golden["timing"].as_array().unwrap();
    assert!(!scenarios.is_empty());
    let now = || std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).unwrap().as_millis() as u64;
    for case in scenarios {
        let stream = crate::create_assistant_message_event_stream();
        let kind = case["kind"].as_str().unwrap();
        let message = |timestamp: Option<u64>| {
            let mut message = json!({ "role": "assistant", "content": [], "stopReason": if kind == "error" { "error" } else { "stop" } });
            match (timestamp, case["timestamp"].as_str().unwrap()) {
                (Some(ts), _) => message["timestamp"] = json!(ts),
                (None, "now") => message["timestamp"] = json!(now()),
                (None, "old") => message["timestamp"] = json!(1),
                _ => {}
            }
            if !case["preset"].is_null() && timestamp.is_none() {
                message["durationMs"] = case["preset"].clone();
            }
            message
        };
        let (final_message, later) = (message(None), message(Some(now())));
        let result = |stream: &crate::ModelEventStream| stream.next();
        let delivered = match kind {
            "done" => {
                stream.push(json!({ "type": "done", "reason": "stop", "message": final_message }));
                result(&stream).unwrap()["message"].clone()
            }
            "error" => {
                stream.push(json!({ "type": "error", "reason": "error", "error": final_message }));
                result(&stream).unwrap()["error"].clone()
            }
            "end" => {
                stream.end(final_message);
                stream.result().unwrap()
            }
            _ => {
                stream.push(json!({ "type": "done", "reason": "stop", "message": final_message }));
                stream.end(later.clone());
                result(&stream).unwrap()["message"].clone()
            }
        };
        let value = delivered.get("durationMs");
        assert_eq!(value.is_some(), case["present"].as_bool().unwrap(), "{case}");
        let kept = value.filter(|value| !case["preset"].is_null() && **value == case["preset"]).cloned().unwrap_or(Value::Null);
        assert_eq!(kept, case["value"], "{case}");
        if let Some(value) = value {
            assert!(value.is_u64(), "{case}: durationMs {value}");
        }
        if kind == "doneThenEnd" {
            // The end after the done event changes nothing: the result is the done message, and the later message is never timed.
            assert_eq!(stream.result().unwrap(), delivered, "{case}");
            assert!(!case["laterTimed"].as_bool().unwrap(), "{case}");
        }
    }
}

#[test]
fn a_stream_the_sdk_makes_for_a_host_operation_is_not_timed() {
    let stream = crate::ModelEventStream::new();
    stream.push(json!({ "type": "done", "reason": "stop", "message": { "role": "assistant", "timestamp": 1u64 << 50 } }));
    assert!(stream.next().unwrap()["message"].get("durationMs").is_none());
}

#[test]
fn the_default_retry_delay_cap_is_pis() {
    assert_eq!(DEFAULT_MAX_AGENT_RETRY_DELAY_MS, 60_000.0);
}

/// `read_stored_credential` reads the credential an auth.json holds, in the key order of the file, as Pi's does (vectors: `pi-coding-agent/readStoredCredential.json`).
#[test]
fn read_stored_credential_matches_the_pinned_package() {
    let dir = std::env::temp_dir().join(format!("pig-sdk-vectors-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    for (i, case) in read_vectors("pi-coding-agent", "readStoredCredential").iter().enumerate() {
        let args = case["args"].as_array().unwrap();
        let path = dir.join(format!("auth-{i}.json"));
        if !is_undefined(&args[1]) {
            std::fs::write(&path, args[1].as_str().unwrap()).unwrap();
        }
        let got = crate::read_stored_credential(args[0].as_str().unwrap(), path.to_str()).unwrap().unwrap_or(Value::Null);
        same(&got, &case["result"]["credential"], case["name"].as_str().unwrap()).unwrap();
    }
    let _ = std::fs::remove_dir_all(&dir);
}

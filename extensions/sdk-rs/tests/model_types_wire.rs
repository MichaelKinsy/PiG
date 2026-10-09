//! The typed model operations of Pi 0.99.1 (`.upstream/v0.99.1/packages/coding-agent/src/core/model-registry.ts:71-79,135-177`,
//! `core/extensions/types.ts:1896-1898`) as the Rust SDK puts them on the wire, against a fake host.
//! The frame shapes are `protocol_extension_api.go` and `protocol.go` in the host's subprocess package.
#![cfg(unix)]

use pig_sdk::{
    APIKeyAuth, AuthResult, Extension, ModelRoute, Provider, ProviderAuth, ProviderOperations,
    ToolResult, VirtualModel, empty_schema,
};
use serde_json::{Value, json};
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::sync::atomic::{AtomicUsize, Ordering};
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
    /// The payload of the `register` frame the extension sent.
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
        let frame = read(&mut stream);
        assert_eq!(frame["type"], "register");
        let register = frame["register"].clone();
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

impl Host {
    /// Runs the tool `name` and answers every call it makes with `answer` until it responds; returns the calls in order and the tool's text.
    fn run_tool(&mut self, name: &str, answer: impl Fn(&Value) -> Result<Value, String>) -> (Vec<Value>, Value) {
        self.tool_call(name, name, "call-1", json!({}));
        let mut calls = Vec::new();
        loop {
            let frame = self.next();
            if frame["type"] == "call" {
                calls.push(frame["call"].clone());
                match answer(&frame["call"]) {
                    Ok(result) => self.reply(&frame, result),
                    Err(message) => self.reply_error(&frame, &message),
                }
            } else {
                assert_eq!(frame["type"], "response", "frame: {frame}");
                assert_eq!(frame["id"], name);
                return (calls, frame["response"].clone());
            }
        }
    }
}

fn text(response: &Value) -> Value {
    serde_json::from_str(response["result"]["content"].as_str().unwrap_or_else(|| panic!("no text: {response}"))).unwrap()
}

const STATE: &str = r#"{"models":[{"id":"gpt","provider":"openai","api":"openai-completions"},{"id":"opus","provider":"anthropic","api":"anthropic-messages"}],
"typedModels":[{"type":"image","id":"flux","provider":"openrouter","api":"openrouter-images","output":["image"]},
{"type":"classifier","id":"jev-latest","provider":"typesafe","api":"typesafe-system-one","contextWindow":64000},
{"type":"classifier","id":"~typesafe/jev-latest","provider":"openrouter","api":"typesafe-system-one","contextWindow":64000}],
"providers":{},"registered":[]}"#;

fn ids(models: &Value) -> Vec<String> {
    models.as_array().unwrap().iter().map(|m| format!("{}/{}", m["provider"].as_str().unwrap(), m["id"].as_str().unwrap())).collect()
}

// model-registry.ts:145-161: the typed reads answer from the host's registry state, chat models from `models` and the others from
// `typedModels`, in the host's order and optionally for one provider.
#[test]
fn typed_reads_answer_from_the_registry_state() {
    let mut ext = Extension::new("typed");
    ext.tool("read", "Read", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        let report = (|| -> std::io::Result<Value> {
            Ok(json!({
                "classifiers": registry.get_models_of_type("classifier", None)?,
                "chat": registry.get_models_of_type("chat", None)?,
                "images": registry.get_models_of_type("image", Some("openrouter"))?,
                "none": registry.get_models_of_type("audio", None)?,
                "inProvider": registry.get_models_of_type("classifier", Some("typesafe"))?,
                "found": registry.find_of_type("classifier", "typesafe", "jev-latest")?,
                "missing": registry.find_of_type("classifier", "typesafe", "missing")?,
                "viaGet": registry.get_model_of_type("image", "openrouter", "flux")?,
                "chatModel": registry.get_model_of_type("chat", "openai", "gpt")?,
            }))
        })();
        match report {
            Ok(report) => ToolResult::text(report.to_string()),
            Err(error) => ToolResult::Error(error.to_string()),
        }
    });
    let mut host = Host::start(ext, json!({}));
    let (calls, response) = host.run_tool("read", |_| Ok(serde_json::from_str(STATE).unwrap()));
    assert!(calls.iter().all(|call| call["method"] == "getModelRegistryState"), "a typed read must answer from the registry state: {calls:?}");
    let report = text(&response);
    assert_eq!(ids(&report["classifiers"]), ["typesafe/jev-latest", "openrouter/~typesafe/jev-latest"]);
    assert_eq!(ids(&report["chat"]), ["openai/gpt", "anthropic/opus"]);
    assert_eq!(ids(&report["images"]), ["openrouter/flux"]);
    assert_eq!(report["none"], json!([]));
    assert_eq!(ids(&report["inProvider"]), ["typesafe/jev-latest"]);
    assert_eq!(report["found"]["contextWindow"], 64000);
    assert_eq!(report["found"]["type"], "classifier");
    assert_eq!(report["missing"], Value::Null);
    assert_eq!(report["viaGet"]["api"], "openrouter-images");
    assert_eq!(report["chatModel"]["id"], "gpt");
    host.finish();
}

// model-registry.ts:135-143: getAvailableOfType awaits the host; the type and provider are what the host filters on, and its error is the caller's.
#[test]
fn get_available_of_type_calls_the_host() {
    let mut ext = Extension::new("typed");
    ext.tool("read", "Read", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        let classifiers = registry.get_available_of_type("classifier", Some("typesafe")).map_err(|e| e.to_string());
        let images = registry.get_available_of_type("image", None).map_err(|e| e.to_string());
        let audio = registry.get_available_of_type("audio", None).map_err(|e| e.to_string());
        ToolResult::text(json!({"classifiers":classifiers,"images":images,"audio":audio}).to_string())
    });
    let mut host = Host::start(ext, json!({}));
    let (calls, response) = host.run_tool("read", |call| match call["args"]["type"].as_str() {
        Some("classifier") => Ok(json!([{"type":"classifier","id":"jev-latest","provider":"typesafe"}])),
        Some("image") => Ok(json!([])),
        _ => Err("Unknown model type: audio".to_string()),
    });
    assert_eq!(calls.len(), 3, "{calls:?}");
    assert_eq!(calls[0]["method"], "getAvailableOfType");
    assert_eq!(calls[0]["args"], json!({"type":"classifier","provider":"typesafe"}));
    assert_eq!(calls[1]["args"], json!({"type":"image"}));
    let report = text(&response);
    assert_eq!(report["classifiers"]["Ok"][0]["id"], "jev-latest");
    assert_eq!(report["images"]["Ok"], json!([]));
    assert!(report["audio"]["Err"].as_str().unwrap().contains("Unknown model type: audio"), "{report}");
    host.finish();
}

fn approval_context() -> Value {
    json!({
        "state": {"text": "Looks good"},
        "questions": {
            "tone": {"type":"choice","instructions":"Which tone?","criteria":{"warm":"Warm","cold":"Cold"}},
            "approved": {"type":"bool","instructions":"Does this express approval?","criteria":{"true":"Approval","false":"No approval"}},
        }
    })
}

fn jev() -> Value {
    json!({"type":"classifier","id":"jev-latest","provider":"typesafe","api":"typesafe-system-one","contextWindow":64000})
}

// model-registry.ts:170-177 and model-runtime.ts:800-815: classify sends the model, the state and the questions in their order; the answers
// come back in the service's order; it never rejects.
#[test]
fn classify_round_trips_ordered_questions_and_answers() {
    let mut ext = Extension::new("typed");
    ext.tool("classify", "Classify", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        let ok = registry.classify(&jev(), approval_context(), Some(json!({"apiKey":"sk-explicit","headers":{"X-Trace":null},"maxRetries":2,"temperature":0.5})));
        let failed = registry.classify(&jev(), approval_context(), None);
        ToolResult::text(json!({"ok":ok,"failed":failed}).to_string())
    });
    let mut host = Host::start(ext, json!({}));
    let (calls, response) = host.run_tool("classify", |call| {
        if call["args"]["options"]["apiKey"] == "sk-explicit" {
            Ok(serde_json::from_str(r#"{"api":"typesafe-system-one","provider":"typesafe","model":"jev-latest","answers":{"approved":{"type":"bool","probability":0.8},"tone":{"type":"choice","choice":"warm","probabilities":{"warm":0.7,"cold":0.3},"confidence":0.4}},"usage":{"input":12,"output":3,"cacheRead":0,"cacheWrite":0,"totalTokens":15,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":7}"#).unwrap())
        } else {
            Err("host went away".into())
        }
    });
    assert_eq!(calls.len(), 2, "{calls:?}");
    assert_eq!(calls[0]["method"], "classify");
    assert_eq!(calls[0]["args"]["model"], jev());
    assert_eq!(calls[0]["args"]["context"], approval_context());
    let questions: Vec<&String> = calls[0]["args"]["context"]["questions"].as_object().unwrap().keys().collect();
    assert_eq!(questions, ["tone", "approved"], "the questions lost their order");
    assert_eq!(calls[0]["args"]["options"], json!({"apiKey":"sk-explicit","headers":{"X-Trace":null},"maxRetries":2,"temperature":0.5}));
    assert!(calls[1]["args"].get("options").is_none(), "omitted options were sent: {}", calls[1]["args"]);

    let report = text(&response);
    let ok = &report["ok"];
    assert_eq!(ok["stopReason"], "stop");
    assert_eq!(ok["usage"]["totalTokens"], 15);
    let answers: Vec<&String> = ok["answers"].as_object().unwrap().keys().collect();
    assert_eq!(answers, ["approved", "tone"], "the answers must keep the service's order");
    let failed = &report["failed"];
    assert_eq!(failed["stopReason"], "error");
    assert!(failed["errorMessage"].as_str().unwrap().contains("host went away"), "{failed}");
    assert_eq!((failed["provider"].as_str(), failed["model"].as_str(), failed["api"].as_str()), (Some("typesafe"), Some("jev-latest"), Some("typesafe-system-one")));
    assert!(failed["timestamp"].as_u64().unwrap() > 0);
    host.finish();
}

// model-runtime.ts:800-815: an aborted classification is a result with stopReason "aborted", not an error; the request lifetime is the signal.
#[test]
fn classify_reports_a_cancelled_request_as_an_aborted_result() {
    let mut ext = Extension::new("typed");
    ext.tool("classify", "Classify", empty_schema(), |ctx, _| {
        ToolResult::text(ctx.model_registry().classify(&jev(), approval_context(), None).to_string())
    });
    let mut host = Host::start(ext, json!({}));
    host.tool_call("classify", "classify", "call-1", json!({}));
    let call = host.next();
    assert_eq!(call["type"], "call", "frame: {call}");
    assert_eq!(call["call"]["method"], "classify");
    send(&mut host.stream, &json!({"type":"cancel","id":"classify","cancel":{"request_id":"classify","reason":"test"}}));
    let response = host.response("classify");
    let result = text(&response);
    assert_eq!(result["stopReason"], "aborted", "{result}");
    assert_eq!(result["answers"], json!({}));
    assert_eq!((result["provider"].as_str(), result["model"].as_str(), result["api"].as_str()), (Some("typesafe"), Some("jev-latest"), Some("typesafe-system-one")));
    host.finish();
}

// model-registry.ts:161-168: registerVirtualModel and unregisterVirtualModel of the facade are the runtime's (loader.ts:480-495).
#[test]
fn registry_virtual_models_reach_the_host_like_the_context_ones() {
    let mut ext = Extension::new("router");
    ext.tool("go", "Go", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        let route = || VirtualModel::new("router", "late", "Late", |_, _| Ok(ModelRoute { model: json!({"provider":"p","id":"m"}), thinking_level: "off".into(), state: None }));
        let mut late = route();
        late.context_window = Some(1000);
        let first = registry.register_virtual_model(late).map_err(|e| e.to_string());
        let refused = registry.register_virtual_model(VirtualModel::new("router", "claimed", "Claimed", |_, _| Err("unused".into()))).map_err(|e| e.to_string());
        let removed = registry.unregister_virtual_model("router", "late").map_err(|e| e.to_string());
        ToolResult::text(json!({"first":first,"refused":refused,"removed":removed}).to_string())
    });
    let mut host = Host::start(ext, json!({}));
    let (calls, response) = host.run_tool("go", |call| {
        if call["args"]["id"] == "claimed" { Err("virtual model router/claimed is the id of a physical model".into()) } else { Ok(json!({})) }
    });
    assert_eq!(calls.len(), 3, "{calls:?}");
    assert_eq!(calls[0]["method"], "registerVirtualModel");
    assert_eq!(calls[0]["args"], json!({"provider":"router","id":"late","name":"Late","contextWindow":1000}));
    assert_eq!(calls[2]["method"], "unregisterVirtualModel");
    assert_eq!(calls[2]["args"], json!({"provider":"router","id":"late"}));
    let report = text(&response);
    assert_eq!(report["first"], json!({"Ok":null}));
    assert!(report["refused"]["Err"].as_str().unwrap().contains("is the id of a physical model"), "{report}");
    assert_eq!(report["removed"], json!({"Ok":null}));
    host.finish();
}

// types.ts:1896-1898: a provider config's images and classifiers are implementations keyed by API. The callbacks stay in the extension; the
// register frame names the APIs and the host runs each through a provider_operation request.
#[test]
fn provider_config_implementations_are_declared_and_run_in_the_extension() {
    let mut ext = Extension::new("ops");
    ext.register_provider_operations(
        "ops",
        json!({"baseUrl":"https://ops.test/v1","apiKey":"key","models":[
            {"id":"flux","name":"Flux","type":"image","api":"test-images","input":["text"],"output":["image"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}},
            {"id":"cls","name":"Cls","type":"classifier","api":"test-classifier","input":["text"],"contextWindow":1000,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}),
        ProviderOperations::new()
            .images("test-images", |model, request, options| {
                Ok(json!({"api":"test-images","provider":"ops","model":model["id"],"n":request["input"].as_array().unwrap().len(),"key":options.values["apiKey"],"cancelled":options.signal.is_cancelled(),"stopReason":"stop","timestamp":1}))
            })
            .classifier("test-classifier", |_, _, _| Err("an earlier implementation of the API ran".into()))
            .classifier("test-classifier", |model, request, _| {
                if request["state"]["text"] == "fail" {
                    return Err("classifier failed".into());
                }
                let order: Vec<&String> = request["questions"].as_object().unwrap().keys().collect();
                Ok(json!({"api":"test-classifier","provider":"ops","model":model["id"],"order":order,"answers":{"b":{"type":"bool","probability":0.5},"a":{"type":"bool","probability":0.25}},"stopReason":"stop","timestamp":2}))
            }),
    );
    let mut host = Host::start(ext, json!({}));
    let provider = &host.register["providers"][0];
    assert_eq!(provider["image_apis"], json!(["test-images"]));
    assert_eq!(provider["classifier_apis"], json!(["test-classifier"]));
    assert!(provider["config"].get("images").is_none() && provider["config"].get("classifiers").is_none(), "{provider}");
    assert_eq!(provider["config"]["models"][0]["type"], "image");
    assert_eq!(provider["config"]["models"][1]["contextWindow"], 1000);

    host.request("img-1", json!({"method":"provider_operation","tool":"ops","args":{"kind":"images","api":"test-images","model":{"type":"image","id":"flux","provider":"ops","api":"test-images"},"context":{"input":[{"type":"text","text":"a red circle"}]},"options":{"apiKey":"sk-ops"}}}));
    let response = host.response("img-1");
    assert_eq!(response["result"]["model"], "flux");
    assert_eq!(response["result"]["n"], 1);
    assert_eq!(response["result"]["key"], "sk-ops");
    assert_eq!(response["result"]["cancelled"], false);

    host.request("cls-1", json!({"method":"provider_operation","tool":"ops","args":{"kind":"classifiers","api":"test-classifier","model":{"type":"classifier","id":"cls","provider":"ops"},"context":{"state":{"text":"ok"},"questions":{"z":{"type":"bool"},"y":{"type":"bool"}}}}}));
    let response = host.response("cls-1");
    assert_eq!(response["result"]["order"], json!(["z", "y"]), "the callback lost the question order");
    let answers: Vec<&String> = response["result"]["answers"].as_object().unwrap().keys().collect();
    assert_eq!(answers, ["b", "a"]);

    host.request("cls-2", json!({"method":"provider_operation","tool":"ops","args":{"kind":"classifiers","api":"test-classifier","model":{"id":"cls"},"context":{"state":{"text":"fail"},"questions":{}}}}));
    assert_eq!(host.response("cls-2")["error"]["message"], "classifier failed");
    host.request("cls-3", json!({"method":"provider_operation","tool":"ops","args":{"kind":"classifiers","api":"other","model":{"id":"x"},"context":{"state":{},"questions":{}}}}));
    // upstream: provider-composer.ts composeModelProvider, `Provider ${providerId} has no classifier implementation for "${model.api}"`.
    assert_eq!(host.response("cls-3")["error"]["message"], r#"Provider ops has no classifier implementation for "other""#);
    host.finish();
}

// model-runtime.ts registerProvider merges a re-registration's defined values, so a second registration without images keeps the first's; the host keeps the APIs it wired.
#[test]
fn provider_re_registration_keeps_implementations_it_does_not_redefine() {
    let mut ext = Extension::new("ops");
    ext.register_provider_operations(
        "ops",
        json!({"baseUrl":"https://ops.test/v1"}),
        ProviderOperations::new()
            .images("test-images", |_, _, _| Ok(json!({"ran":"first images"})))
            .classifier("test-classifier", |_, _, _| Ok(json!({"ran":"first classifier"}))),
    );
    ext.register_provider_operations(
        "ops",
        json!({"baseUrl":"https://ops.test/v2"}),
        ProviderOperations::new().classifier("test-classifier", |_, _, _| Ok(json!({"ran":"second classifier"}))),
    );
    let mut host = Host::start(ext, json!({}));
    host.request("img-1", json!({"method":"provider_operation","tool":"ops","args":{"kind":"images","api":"test-images","model":{"id":"flux"},"context":{"input":[]}}}));
    assert_eq!(host.response("img-1")["result"]["ran"], "first images");
    host.request("cls-1", json!({"method":"provider_operation","tool":"ops","args":{"kind":"classifiers","api":"test-classifier","model":{"id":"cls"},"context":{"state":{},"questions":{}}}}));
    assert_eq!(host.response("cls-1")["result"]["ran"], "second classifier");
    host.finish();
}

// types.ts:1896-1898 for a Provider object: generateImages and classify are members of the object, declared with its other methods and run through provider_call.
#[test]
fn provider_object_declares_and_runs_generate_images_and_classify() {
    let stream: pig_sdk::ProviderStreamFn = std::sync::Arc::new(|_, _, _| Err("unused".into()));
    let bare = |generate: bool| Provider {
        id: "pixels".into(),
        name: "Pixels".into(),
        base_url: None,
        headers: None,
        auth: ProviderAuth {
            api_key: Some(APIKeyAuth { name: "Pixels key".into(), check: None, resolve: std::sync::Arc::new(|_| Ok(Some(AuthResult { auth: json!({}), env: None, source: None }))), login: None }),
            oauth: None,
        },
        get_models: std::sync::Arc::new(|| Ok(vec![std::sync::Arc::new(json!({"id":"flux","name":"Flux","type":"image","api":"test-images"}))])),
        filter_models: None,
        get_all_models: None,
        filter_all_models: None,
        refresh_models: None,
        stream: stream.clone(),
        stream_simple: stream.clone(),
        fetch_deferred: None,
        cancel_deferred: None,
        generate_images: generate.then(|| -> pig_sdk::ProviderOperationFn {
            std::sync::Arc::new(|model, request, options| Ok(json!({"model":model["id"],"n":request["input"].as_array().unwrap().len(),"key":options.values["apiKey"],"stopReason":"stop"})))
        }),
        classify: generate.then(|| -> pig_sdk::ProviderOperationFn {
            std::sync::Arc::new(|model, request, _| Ok(json!({"model":model["id"],"order":request["questions"].as_object().unwrap().keys().collect::<Vec<_>>(),"answers":{"q":{"type":"bool","probability":0.5}},"stopReason":"stop"})))
        }),
    };
    let mut ext = Extension::new("objects");
    ext.register_native_provider(std::sync::Arc::new(bare(true))).unwrap();
    let mut host = Host::start(ext, json!({}));
    let native = host.register["providers"][0]["native"].clone();
    let methods: Vec<&str> = native["methods"].as_array().unwrap().iter().map(|m| m.as_str().unwrap()).collect();
    assert!(methods.contains(&"generateImages") && methods.contains(&"classify"), "{methods:?}");
    let key = native["key"].as_str().unwrap().to_string();
    host.request("obj-1", json!({"method":"provider_call","tool":key,"args":{"method":"generateImages","params":{"model":{"id":"flux"},"context":{"input":[{"type":"text","text":"x"}]},"options":{"apiKey":"sk"}}}}));
    let response = host.response("obj-1");
    assert_eq!(response["result"], json!({"model":"flux","n":1,"key":"sk","stopReason":"stop"}), "{response}");
    host.request("obj-2", json!({"method":"provider_call","tool":key,"args":{"method":"classify","params":{"model":{"id":"cls"},"context":{"state":{},"questions":{"q2":{"type":"bool"},"q1":{"type":"bool"}}},"options":{}}}}));
    let response = host.response("obj-2");
    assert_eq!(response["result"]["order"], json!(["q2", "q1"]), "{response}");
    assert_eq!(response["result"]["model"], "cls");
    host.finish();

    // A provider without the members does not declare them, as Pi's Provider leaves them undefined.
    let mut ext = Extension::new("objects");
    ext.register_native_provider(std::sync::Arc::new(bare(false))).unwrap();
    let host = Host::start(ext, json!({}));
    let methods: Vec<&str> = host.register["providers"][0]["native"]["methods"].as_array().unwrap().iter().map(|m| m.as_str().unwrap()).collect();
    assert!(!methods.contains(&"generateImages") && !methods.contains(&"classify"), "{methods:?}");
    host.finish();
}

// A provider registered after the factory finished carries the same images, classifiers and streamSimple a factory registration does. Pi's
// `pi.registerProvider` takes the whole ProviderConfig, callbacks included, at any time (`.upstream/v0.99.2/packages/coding-agent/src/core/extensions/types.ts:1766-1803,1875-1903`,
// `loader.ts:449-457`, `runner.ts:517-523`). The callbacks stay in the extension; the host call names them as the register payload does and the host
// runs each through a provider_operation or provider_stream_simple request.
fn late_operations() -> ProviderOperations {
    ProviderOperations::new()
        .images("late-images", |model, _, options| Ok(json!({"api":"late-images","provider":"late","model":model["id"],"key":options.values["apiKey"],"output":[],"stopReason":"stop","timestamp":1})))
        .classifier("late-classifier", |model, _, _| Ok(json!({"api":"late-classifier","provider":"late","model":model["id"],"answers":{},"stopReason":"stop","timestamp":2})))
        .stream_simple(|_, model, _, options| {
            let stream = std::sync::Arc::new(pig_sdk::ModelEventStream::new());
            let message = json!({"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[{"type":"text","text":format!("late:{}:{}", model["id"].as_str().unwrap(), options["apiKey"].as_str().unwrap())}],"stopReason":"stop","timestamp":1});
            stream.push(json!({"type":"done","reason":"stop","message":message}));
            Ok(stream)
        })
}

fn late_config() -> Value {
    json!({"api":"late-chat-api","baseUrl":"https://late.test/v1","apiKey":"late-key","models":[
        {"id":"chat","name":"Chat","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100},
        {"id":"flux","name":"Flux","type":"image","api":"late-images","input":["text"],"output":["image"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]})
}

fn late_image() -> Value {
    json!({"method":"provider_operation","tool":"late","args":{"kind":"images","api":"late-images","model":{"type":"image","id":"flux","provider":"late","api":"late-images"},"context":{"input":[]},"options":{"apiKey":"sk-late"}}})
}

#[test]
fn registry_register_provider_keeps_operations_after_the_factory() {
    let mut ext = Extension::new("late");
    ext.tool("go", "Go", empty_schema(), |ctx, _| {
        let registered = ctx.model_registry().register_provider_operations("late", late_config(), late_operations()).map_err(|e| e.to_string());
        ToolResult::text(json!({"registered":registered}).to_string())
    });
    let mut host = Host::start(ext, json!({}));
    assert!(host.register["providers"].as_array().is_none_or(|providers| providers.is_empty()), "the factory registered {}", host.register["providers"]);
    let (calls, response) = host.run_tool("go", |_| Ok(Value::Null));
    assert_eq!(text(&response)["registered"], json!({"Ok":null}));
    assert_eq!(calls.len(), 1, "{calls:?}");
    assert_eq!(calls[0]["method"], "registerProvider");
    let args = &calls[0]["args"];
    assert_eq!((&args["name"], &args["stream_simple"], &args["image_apis"], &args["classifier_apis"]), (&json!("late"), &json!(true), &json!(["late-images"]), &json!(["late-classifier"])), "{args}");
    assert!(args["config"].get("images").is_none() && args["config"].get("streamSimple").is_none(), "the callbacks crossed the wire: {args}");
    assert_eq!(args["config"]["baseUrl"], "https://late.test/v1");
    assert_eq!(args["config"]["models"].as_array().unwrap().len(), 2);

    host.request("img-1", late_image());
    let response = host.response("img-1");
    assert_eq!((&response["result"]["model"], &response["result"]["key"]), (&json!("flux"), &json!("sk-late")), "{response}");
    host.request("cls-1", json!({"method":"provider_operation","tool":"late","args":{"kind":"classifiers","api":"late-classifier","model":{"id":"cls"},"context":{"state":{},"questions":{}}}}));
    assert_eq!(host.response("cls-1")["result"]["model"], "cls");
    host.request("stream-1", json!({"method":"provider_stream_simple","tool":"late","args":{"model":{"id":"chat","provider":"late","api":"late-chat-api"},"context":{"messages":[]},"options":{"apiKey":"sk-stream"}}}));
    // The events are notifications that precede the response, in order.
    let notification = host.next();
    assert_eq!((&notification["type"], &notification["notify"]["method"], &notification["notify"]["args"]["request_id"]), (&json!("notify"), &json!("provider_stream_event"), &json!("stream-1")), "{notification}");
    let response = host.response("stream-1");
    assert_eq!(response["result"]["content"][0]["text"], "late:chat:sk-stream", "{response}");
    host.finish();
}

// model-runtime.ts:753-766: a late registration that defines no operation keeps the implementations of the one before.
#[test]
fn registry_register_provider_merges_operations_like_the_factory() {
    let mut ext = Extension::new("late");
    ext.tool("go", "Go", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        registry.register_provider_operations("late", late_config(), late_operations()).unwrap();
        registry.register_provider_operations("late", json!({"baseUrl":"https://late.test/v2"}), ProviderOperations::new()).unwrap();
        ToolResult::text("{}".to_string())
    });
    let mut host = Host::start(ext, json!({}));
    let (calls, _) = host.run_tool("go", |_| Ok(Value::Null));
    assert_eq!(calls.len(), 2, "{calls:?}");
    let second = &calls[1]["args"];
    assert!(second.get("image_apis").is_none() && second.get("classifier_apis").is_none() && second["stream_simple"] != json!(true), "a registration without operations declared some: {second}");
    host.request("img-1", late_image());
    assert_eq!(host.response("img-1")["result"]["model"], "flux");
    host.finish();
}

// types.ts:1805-1819: after unregisterProvider no request finds a callback the extension held for the provider.
#[test]
fn registry_unregister_provider_drops_its_operations() {
    let mut ext = Extension::new("late");
    ext.tool("go", "Go", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        registry.register_provider_operations("late", late_config(), late_operations()).unwrap();
        registry.unregister_provider("late").unwrap();
        ToolResult::text("{}".to_string())
    });
    let mut host = Host::start(ext, json!({}));
    let (calls, _) = host.run_tool("go", |_| Ok(Value::Null));
    assert_eq!(calls.iter().map(|call| call["method"].as_str().unwrap()).collect::<Vec<_>>(), ["registerProvider", "unregisterProvider"]);
    host.request("img-1", late_image());
    assert!(host.response("img-1")["error"]["message"].as_str().unwrap().contains("no image implementation"));
    host.request("stream-1", json!({"method":"provider_stream_simple","tool":"late","args":{"model":{"id":"chat"},"context":{"messages":[]},"options":{}}}));
    assert_eq!(host.response("stream-1")["error"]["message"], "unknown provider stream");
    host.finish();
}

// model-registry.test.ts:1320-1343 "failed registerProvider does not remove existing provider models": a registration the host refuses leaves the provider as it was, so the implementations of the refused config do not replace the held ones.
#[test]
fn registry_refused_registration_keeps_the_held_operations() {
    let mut ext = Extension::new("late");
    ext.tool("go", "Go", empty_schema(), |ctx, _| {
        let registry = ctx.model_registry();
        registry.register_provider_operations("late", late_config(), late_operations()).unwrap();
        let refused = registry
            .register_provider_operations("late", late_config(), ProviderOperations::new().images("late-images", |_, _, _| Ok(json!({"ran":"refused images"}))))
            .map_err(|e| e.to_string());
        ToolResult::text(json!({"refused":refused}).to_string())
    });
    let mut host = Host::start(ext, json!({}));
    let seen = std::sync::atomic::AtomicUsize::new(0);
    let (calls, response) = host.run_tool("go", |_| if seen.fetch_add(1, Ordering::SeqCst) == 1 { Err("invalid model definitions".into()) } else { Ok(Value::Null) });
    assert_eq!(calls.len(), 2, "{calls:?}");
    assert!(text(&response)["refused"]["Err"].as_str().unwrap().contains("invalid model definitions"), "{response}");
    host.request("img-1", late_image());
    assert_eq!(host.response("img-1")["result"]["model"], "flux", "the held implementation answers, not the refused one");
    host.finish();
}

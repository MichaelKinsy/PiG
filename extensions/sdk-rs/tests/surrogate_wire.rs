// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//! Public SDK calls over length-prefixed JSON, without the SDK's private wire helpers.
//! The host listener uses std's Unix sockets, as do the SDK's existing socket tests.
//! upstream: packages/tui/src/stdin-buffer.ts:251 emits individual UTF-16 units.
//! upstream: packages/tui/src/tui.ts:1015-1030 preserves listener data and rewrites.
//! upstream: packages/coding-agent/src/core/extensions/types.ts:219-225 uses JS strings for editor access.

#![cfg(unix)]

use pig_sdk::{CommandResult, Extension, JsString, ToolResult, empty_schema};
use serde::Deserialize;
use serde_json::{Value, json, value::RawValue};
use std::collections::HashSet;
use std::io::{Read, Write};
use std::net::Shutdown;
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::PathBuf;
use std::process::Command;
use std::sync::atomic::{AtomicU64, Ordering};
use std::thread::{self, JoinHandle};

struct TempDir(PathBuf);

impl TempDir {
    fn new() -> Self {
        static NEXT: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "pig-wire-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        std::fs::create_dir(&path).unwrap();
        Self(path)
    }
}

impl Drop for TempDir {
    fn drop(&mut self) {
        std::fs::remove_dir_all(&self.0).unwrap();
    }
}

// Re-exec only this test: changing HOME in the parallel Rust test runner would race other tests.
fn isolated(name: &str, test: impl FnOnce()) {
    const CHILD: &str = "SURROGATE_WIRE_TEST";
    if std::env::var(CHILD).as_deref() == Ok(name) {
        let home = PathBuf::from(std::env::var_os("HOME").unwrap());
        assert!(home.is_dir());
        for key in ["PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"] {
            assert_eq!(std::env::var_os(key).unwrap(), home.as_os_str());
        }
        test();
        return;
    }
    let home = TempDir::new();
    let mut child = Command::new(std::env::current_exe().unwrap());
    child.args(["--exact", name, "--nocapture"]);
    for (key, _) in std::env::vars_os() {
        let name = key.to_string_lossy();
        if name.starts_with("PIG_") || name.starts_with("PI_") || name.starts_with("GOPI_") {
            child.env_remove(key);
        }
    }
    for key in [
        "HOME",
        "PIG_HOME",
        "PIG_CODING_AGENT_DIR",
        "PI_CODING_AGENT_DIR",
    ] {
        child.env(key, &home.0);
    }
    let output = child.env(CHILD, name).output().unwrap();
    print!("{}", String::from_utf8(output.stdout).unwrap());
    eprint!("{}", String::from_utf8(output.stderr).unwrap());
    assert!(
        output.status.success(),
        "isolated {name}: {}",
        output.status
    );
}

struct Host {
    stream: UnixStream,
    runner: Option<JoinHandle<std::io::Result<()>>>,
    calls: HashSet<String>,
    _dir: TempDir,
}

impl Host {
    fn start(ext: Extension) -> Self {
        let dir = TempDir::new();
        let socket = dir.0.join("host.sock");
        let listener = UnixListener::bind(&socket).unwrap();
        let runner = thread::spawn(move || ext.run_with_socket(socket.to_str().unwrap()));
        let (stream, _) = listener.accept().unwrap();
        Self {
            stream,
            runner: Some(runner),
            calls: HashSet::new(),
            _dir: dir,
        }
    }

    fn read(&mut self) -> String {
        let mut prefix = [0; 4];
        self.stream
            .read_exact(&mut prefix)
            .expect("SDK frame prefix");
        let size = u32::from_be_bytes(prefix) as usize;
        // These tests exchange only small frames; reject bogus lengths before allocation.
        assert!(size <= 64 * 1024, "unexpected SDK frame length: {size}");
        let mut bytes = vec![0; size];
        self.stream.read_exact(&mut bytes).expect("SDK frame body");
        let raw = String::from_utf8(bytes).expect("wire must be valid UTF-8");
        println!("SDK -> host prefix={prefix:02x?} JSON={raw}");
        serde_json::from_str::<Box<RawValue>>(&raw).expect("wire must be valid JSON");
        raw
    }

    fn send(&mut self, raw: &str) {
        serde_json::from_str::<Box<RawValue>>(raw).expect("host fixture must be valid JSON");
        let prefix = u32::try_from(raw.len()).unwrap().to_be_bytes();
        println!("host -> SDK prefix={prefix:02x?} JSON={raw}");
        self.stream.write_all(&prefix).unwrap();
        self.stream.write_all(raw.as_bytes()).unwrap();
    }

    fn expect(&mut self, expected: Value) {
        let raw = self.read();
        let actual: Value = serde_json::from_str(&raw).unwrap();
        assert_eq!(actual, expected, "raw frame: {raw}");
    }

    fn state(&mut self, id: &str, state: &str, reason: Option<&str>) {
        let mut body = json!({"request_id": id, "state": state});
        if let Some(reason) = reason {
            body["reason"] = json!(reason);
        }
        self.expect(json!({"type": "request_state", "request_state": body}));
    }

    fn ping(&mut self, nonce: &str) {
        self.send(&json!({"type": "ping", "ping": {"nonce": nonce}}).to_string());
        self.expect(json!({"type": "pong", "pong": {"nonce": nonce}}));
    }

    fn request(&mut self, id: &str, method: &str, tool: &str) {
        self.send(
            &json!({"type": "request", "id": id, "request": {
                "method": method, "tool": tool, "args": {}
            }})
            .to_string(),
        );
        self.state(id, "started", None);
    }

    fn call(&mut self, parent: &str, method: &str, text: Option<(&str, &[u16])>) -> String {
        self.state(parent, "blocked", Some("host_call"));
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct CallFrame {
            r#type: String,
            id: String,
            call: Call,
        }
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Call {
            method: String,
            parent_request_id: String,
            args: Option<Box<RawValue>>,
        }
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Text {
            text: JsString,
        }
        let raw = self.read();
        let frame: CallFrame = serde_json::from_str(&raw).unwrap();
        assert_eq!(frame.r#type, "call");
        assert!(!frame.id.is_empty());
        assert!(self.calls.insert(frame.id.clone()), "reused call ID");
        assert_eq!(frame.call.method, method);
        assert_eq!(frame.call.parent_request_id, parent);
        if let Some((literal, units)) = text {
            let args = frame.call.args.expect("text arguments");
            // Exact literal guards against a mutually lossy encoder/decoder, double escaping, and WTF-8 bytes.
            assert_eq!(args.get(), format!(r#"{{"text":{literal}}}"#));
            let decoded: Text = serde_json::from_str(args.get()).unwrap();
            assert_eq!(decoded.text.as_units(), units, "{method} host-bound units");
        } else {
            assert!(frame.call.args.is_none(), "getter must omit args");
        }
        frame.id
    }

    fn reply(&mut self, parent: &str, call: &str, result: Option<&str>) {
        let body = result.map_or_else(String::new, |r| format!(r#""result":{r}"#));
        self.send(&format!(
            r#"{{"type":"call_result","id":{},"call_result":{{{body}}}}}"#,
            serde_json::to_string(call).unwrap()
        ));
        self.state(parent, "progress", None);
    }

    fn response(&mut self, id: &str, body: Value) {
        self.state(id, "completed", None);
        self.expect(json!({"type": "response", "id": id, "response": body}));
    }

    fn shutdown(mut self) {
        self.send(r#"{"type":"shutdown","shutdown":{"reason":"test complete"}}"#);
        self.runner.take().unwrap().join().unwrap().unwrap();
        let mut byte = [0];
        assert_eq!(
            self.stream.read(&mut byte).unwrap(),
            0,
            "SDK socket stays open"
        );
    }
}

impl Drop for Host {
    fn drop(&mut self) {
        if let Some(runner) = self.runner.take() {
            // Also release pending typed calls and join handlers when a host assertion fails.
            let _ = self.stream.shutdown(Shutdown::Both);
            let outcome = runner.join();
            if !thread::panicking() {
                outcome.unwrap().unwrap();
            }
        }
    }
}

#[test]
fn editor_text_typed_calls_preserve_utf16_on_the_wire() {
    isolated("editor_text_typed_calls_preserve_utf16_on_the_wire", || {
        let mut ext = Extension::new("editor-wire");
        ext.tool(
            "roundtrip",
            "Read, set, and paste editor text",
            empty_schema(),
            |ctx, _| {
                let text = ctx.get_editor_text().unwrap();
                ctx.set_editor_text(text.clone());
                ctx.paste_to_editor(text.clone());
                ToolResult::json(json!({"units": text.as_units()}))
            },
        );
        let mut host = Host::start(ext);
        let register: Value = serde_json::from_str(&host.read()).unwrap();
        assert_eq!(register["type"], "register");
        assert_eq!(register["register"]["name"], "editor-wire");
        assert_eq!(register["register"]["tools"][0]["name"], "roundtrip");
        host.send(r#"{"type":"ready","ready":{"mode":"tui","state":{"hasUI":true}}}"#);

        // Literals and units are independent of JsString's serializer/deserializer.
        for (name, input, output, units) in [
            ("empty", r#""""#, r#""""#, &[][..]),
            (
                "ascii",
                r#""plain""#,
                r#""plain""#,
                &[112, 108, 97, 105, 110][..],
            ),
            ("highD83D", r#""\ud83d""#, r#""\ud83d""#, &[0xd83d][..]),
            ("lowDE00", r#""\ude00""#, r#""\ude00""#, &[0xde00][..]),
            ("emoji", r#""😀""#, r#""😀""#, &[0xd83d, 0xde00][..]),
            (
                "escaped-pair",
                r#""\ud83d\ude00""#,
                r#""😀""#,
                &[0xd83d, 0xde00][..],
            ),
            (
                "separated",
                r#""A\ud83dZ\ude00""#,
                r#""A\ud83dZ\ude00""#,
                &[65, 0xd83d, 90, 0xde00][..],
            ),
            (
                "literal-escape",
                r#""\\ud83d""#,
                r#""\\ud83d""#,
                &[92, 117, 100, 56, 51, 100][..],
            ),
        ] {
            host.request(name, "tool_call", "roundtrip");
            let call = host.call(name, "ui.getEditorText", None);
            // A pong must arrive while the handler is still waiting for this exact call_result.
            host.ping(name);
            host.reply(name, &call, Some(&format!(r#"{{"text":{input}}}"#)));
            for method in ["ui.setEditorText", "ui.pasteToEditor"] {
                let call = host.call(name, method, Some((output, units)));
                host.reply(name, &call, None);
            }
            host.response(name, json!({"result": {"units": units}}));
        }
        host.shutdown();
    });
}

#[test]
fn surrogate_ready_and_state_updates_keep_calls_and_heartbeat_live() {
    isolated(
        "surrogate_ready_and_state_updates_keep_calls_and_heartbeat_live",
        || {
            for (name, ready, updated, units) in [
                ("high-to-low", r#""\ud83d""#, r#""\ude00""#, &[0xde00][..]),
                ("low-to-high", r#""\ude00""#, r#""\ud83d""#, &[0xd83d][..]),
            ] {
                let mut ext = Extension::new(name);
                ext.tool("read", "Read current UI state", empty_schema(), |ctx, _| {
                    ToolResult::json(
                        json!({"hasUI": ctx.has_ui(), "units": ctx.get_editor_text().unwrap().as_units()}),
                    )
                });
                let mut host = Host::start(ext);
                let register: Value = serde_json::from_str(&host.read()).unwrap();
                assert_eq!(register["type"], "register");
                assert_eq!(register["register"]["name"], name);
                host.send(&r#"{"type":"ready","ready":{"mode":"tui","state":{"hasUI":true,"editorText":TEXT}}}"#.replace("TEXT", ready));
                host.ping("after-ready");
                host.request("ready-read", "tool_call", "read");
                let call = host.call("ready-read", "ui.getEditorText", None);
                // The authoritative getter result differs from the ready replica.
                host.reply(
                    "ready-read",
                    &call,
                    Some(&format!(r#"{{"text":{updated}}}"#)),
                );
                host.response(
                    "ready-read",
                    json!({"result": {"hasUI": true, "units": units}}),
                );

                host.send(&r#"{"type":"notify","notify":{"method":"state_update","args":{"state":{"hasUI":false,"editorText":TEXT}}}}"#.replace("TEXT", updated));
                // Socket order is the barrier: the dispatcher processes the state update before the ping/request.
                host.ping("after-state-update");
                host.request("updated-read", "tool_call", "read");
                let call = host.call("updated-read", "ui.getEditorText", None);
                host.ping("while-typed-call-pending");
                host.reply("updated-read", &call, Some(r#"{"text":"😀"}"#));
                host.response(
                    "updated-read",
                    json!({"result": {"hasUI": false, "units": [0xd83d, 0xde00]}}),
                );
                host.shutdown();
            }
        },
    );
}

#[test]
fn no_result_response_omits_result_after_typed_editor_calls() {
    isolated(
        "no_result_response_omits_result_after_typed_editor_calls",
        || {
            let mut ext = Extension::new("no-result-wire");
            ext.command("paste", "Paste without a command result", |ctx, _| {
                ctx.set_editor_text(JsString::from_units(vec![0xd83d]));
                ctx.paste_to_editor(JsString::from_units(vec![0xde00]));
                CommandResult::Ok
            });
            let mut host = Host::start(ext);
            let register: Value = serde_json::from_str(&host.read()).unwrap();
            assert_eq!(register["type"], "register");
            assert_eq!(register["register"]["commands"][0]["name"], "paste");
            host.send(r#"{"type":"ready","ready":{"state":{"hasUI":true}}}"#);
            host.request("no-result", "command", "paste");
            for (method, literal, units) in [
                ("ui.setEditorText", r#""\ud83d""#, &[0xd83d][..]),
                ("ui.pasteToEditor", r#""\ude00""#, &[0xde00][..]),
            ] {
                let call = host.call("no-result", method, Some((literal, units)));
                host.reply("no-result", &call, None);
            }
            // Structural equality distinguishes {} from {"result":null}, not Option<Value> deserialization.
            host.response("no-result", json!({}));
            host.ping("after-no-result-response");
            host.shutdown();
        },
    );
}

#[test]
fn run_with_socket_closes_its_connection_while_a_context_is_retained() {
    // A retained runtime process outlives this generation, and the host's Retire waits for the SDK to close its socket; a Context kept in module state must not hold it open (Go's RunWithConn closes on return).
    isolated("run_with_socket_closes_its_connection_while_a_context_is_retained", || {
        static KEPT: std::sync::Mutex<Vec<pig_sdk::Context>> = std::sync::Mutex::new(Vec::new());
        let mut ext = Extension::new("retained-context");
        ext.tool("keep", "Keep the context", empty_schema(), |ctx, _| {
            KEPT.lock().unwrap().push(ctx.clone());
            ToolResult::json(json!({"kept": true}))
        });
        let mut host = Host::start(ext);
        let register: Value = serde_json::from_str(&host.read()).unwrap();
        assert_eq!(register["type"], "register");
        host.send(r#"{"type":"ready","ready":{"mode":"tui","state":{"hasUI":true}}}"#);
        host.request("keep", "tool_call", "keep");
        host.response("keep", json!({"result": {"kept": true}}));
        // A socket left open fails the read with a timeout instead of hanging the test.
        host.stream.set_read_timeout(Some(std::time::Duration::from_secs(5))).unwrap();
        host.shutdown();
        assert_eq!(KEPT.lock().unwrap().len(), 1);
    });
}

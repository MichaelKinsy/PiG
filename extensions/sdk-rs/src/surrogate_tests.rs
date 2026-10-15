use super::*;

// Pi stdin-buffer.ts:251 and tui.ts:1015-1030 preserve these exact units before
// and after the ordered listener pass. Value/String cannot decode this frame.
#[test]
fn terminal_request_and_state_accept_lone_surrogates() {
    let frame = r#"{"type":"request","id":"input","request":{"method":"terminal_input","args":{"data":"\ud83d","editorText":"A\ude00","toolsExpanded":true}}}"#;
    let envelope: Envelope<Box<serde_json::value::RawValue>> = serde_json::from_str(frame).unwrap();
    let args = envelope.request.unwrap().terminal_input.unwrap();
    assert_eq!(args.data.as_units(), [0xd83d]);
    assert_eq!(args.editor_text.as_units(), [65, 0xde00]);
    assert!(args.tools_expanded);
    for frame in [
        r#"{"type":"ready","ready":{"state":{"editorText":"\ud83d","hasUI":true}}}"#,
        r#"{"type":"notify","notify":{"method":"state_update","args":{"state":{"editorText":"\ude00","hasUI":true}}}}"#,
    ] {
        let envelope: Envelope = serde_json::from_str(frame).unwrap();
        let state = match envelope.ready {
            Some(ready) => ready.state.unwrap(),
            None => envelope.notify.unwrap().args.unwrap()["state"].clone(),
        };
        assert_eq!(state["hasUI"], true);
    }
}

#[test]
fn custom_input_accepts_each_utf16_half() {
    for unit in ["d83d", "de00"] {
        let frame = format!(
            r#"{{"type":"notify","notify":{{"method":"ui.custom.input","args":{{"key":"overlay","data":"\u{unit}"}}}}}}"#
        );
        let envelope = serde_json::from_str::<Envelope>(&frame);
        assert!(envelope.is_ok(), "custom input disconnected: {envelope:?}");
    }
}

#[test]
fn raw_call_result_reaches_lossless_typed_decoder() {
    let frame = r#"{"type":"call_result","id":"get","call_result":{"result":{"text":"\ud83d"}}}"#;
    let envelope: Envelope<Box<serde_json::value::RawValue>> = serde_json::from_str(frame).unwrap();
    #[derive(Deserialize)]
    struct Text {
        text: JsString,
    }
    let text: Text =
        serde_json::from_str(envelope.call_result.unwrap().result.unwrap().get()).unwrap();
    assert_eq!(text.text.as_units(), [0xd83d]);
    assert_eq!(
        serde_json::to_string(&crate::TerminalInputResult {
            consume: false,
            data: Some(text.text)
        })
        .unwrap(),
        r#"{"consume":false,"data":"\ud83d"}"#
    );
}

// A frame whose Value or String position holds an unpaired surrogate is decoded with U+FFFD for it, not refused: refusing ended the
// connection, so one stray UTF-16 unit in a tool argument or event payload killed the extension.
#[test]
fn a_tool_request_with_an_unpaired_surrogate_in_its_arguments_still_decodes() {
    for (args, text) in [
        (r#"{"text":"x\ud800y"}"#, "x\u{fffd}y"),
        (r#"{"text":"\ude00"}"#, "\u{fffd}"),
        (r#"{"text":"\ud83d\ude00"}"#, "\u{1f600}"),
        (r#"{"text":"\ud83d\ud83d\ude00"}"#, "\u{fffd}\u{1f600}"),
        (r#"{"text":"\\ud800"}"#, "\\ud800"),
        (r#"{"text":"\\dc00\ud800"}"#, "\\dc00\u{fffd}"),
        (r#"{"text":"\\\ud800"}"#, "\\\u{fffd}"),
        (r#"{"text":"\ud800\u0041"}"#, "\u{fffd}A"),
        (r#"{"text":"\uD800"}"#, "\u{fffd}"),
    ] {
        let frame = format!(r#"{{"type":"request","id":"t","request":{{"method":"tool_call","args":{{"name":"echo","params":{args}}}}}}}"#);
        let envelope = decode_frame(frame.as_bytes()).unwrap_or_else(|err| panic!("{args}: {err}"));
        let request = envelope.request.unwrap();
        let args_value = request.args.unwrap();
        assert_eq!(args_value["params"]["text"], text, "{args}");
    }
    // An object key holding a lone unit is repaired the same way.
    let frame = br#"{"type":"notify","notify":{"method":"x","args":{"k\ud800":1}}}"#;
    let envelope: Envelope = serde_json::from_value(serde_json::to_value(decode_frame(frame).unwrap()).unwrap()).unwrap();
    assert_eq!(envelope.notify.unwrap().args.unwrap()["k\u{fffd}"], 1);
}

#[test]
fn a_frame_without_unpaired_surrogates_is_decoded_untouched() {
    let frame = r#"{"type":"request","id":"input","request":{"method":"terminal_input","args":{"data":"\ud83d","editorText":"A\ude00","toolsExpanded":true}}}"#;
    // The typed UTF-16 fields still keep the unit: the repair runs only when the first decode fails.
    let envelope = decode_frame(frame.as_bytes()).unwrap();
    assert_eq!(envelope.request.unwrap().terminal_input.unwrap().data.as_units(), [0xd83d]);
    assert!(replace_unpaired_surrogate_escapes(br#"{"a":"\ud83d\ude00 \\ud800 \u0041"}"#).is_none());
    assert!(decode_frame(b"{\"type\":").is_err());
}

fn nested_request(depth: usize, text: &str) -> String {
    let deep = format!("{}1{}", "[".repeat(depth), "]".repeat(depth));
    format!(r#"{{"type":"request","id":"t","request":{{"method":"tool_call","args":{{"name":"echo","params":{{"v":{deep},"text":"{text}"}}}}}}}}"#)
}

// serde_json refuses nesting past 128 levels; every other runtime takes far more, and a refused frame ended the connection.
#[test]
fn a_tool_request_nested_past_serde_jsons_limit_still_decodes() {
    for depth in [10, 120, 130, 1000, MAX_NESTING_DEPTH - 10] {
        let envelope = decode_frame(nested_request(depth, "ok").as_bytes()).unwrap_or_else(|err| panic!("depth {depth}: {err}"));
        let params = envelope.request.unwrap().args.unwrap();
        assert_eq!(params["params"]["text"], "ok", "depth {depth}");
        let mut level = &params["params"]["v"];
        for _ in 0..depth {
            level = &level[0];
        }
        assert_eq!(level, 1, "depth {depth}");
    }
}

#[test]
fn nesting_past_the_bound_is_refused_and_brackets_in_strings_do_not_count() {
    assert!(decode_frame(nested_request(MAX_NESTING_DEPTH + 10, "ok").as_bytes()).is_err());
    let brackets = "[".repeat(MAX_NESTING_DEPTH * 2);
    let envelope = decode_frame(nested_request(130, &brackets).as_bytes()).unwrap();
    assert_eq!(envelope.request.unwrap().args.unwrap()["params"]["text"], brackets.as_str());
    assert_eq!(nesting_depth(br#"{"a":["\"[[",{"b":[]}]}"#), 4);
    assert_eq!(nesting_depth(br#"["\\",[[]]]"#), 3);
}

#[test]
fn a_deep_frame_with_an_unpaired_surrogate_is_repaired_and_decoded() {
    let envelope = decode_frame(nested_request(300, r"x\ud800y").as_bytes()).unwrap();
    assert_eq!(envelope.request.unwrap().args.unwrap()["params"]["text"], "x\u{fffd}y");
}


#[test]
fn nesting_depth_counts_levels_not_brackets() {
    assert_eq!(nesting_depth(b"[[],[],[]]"), 2);
    assert_eq!(nesting_depth(br#"{"a":{"b":1},"c":{"d":2}}"#), 2);
    assert_eq!(nesting_depth(b"1"), 0);
}


// Pi writes a session entry with JSON.stringify, which spells an unpaired surrogate as an escape. The mirror's read of the session file refused the whole file
// for one such line and returned no entries at all.
#[test]
fn session_file_with_an_unpaired_surrogate_keeps_every_entry() {
    let dir = std::env::temp_dir().join(format!("pig-sdk-rs-surrogate-{}", std::process::id()));
    std::fs::create_dir_all(&dir).unwrap();
    let path = dir.join("session.jsonl");
    std::fs::write(
        &path,
        concat!(
            "{\"type\":\"session\",\"id\":\"s\"}\n",
            "{\"type\":\"message\",\"id\":\"a\",\"parentId\":null,\"message\":{\"role\":\"user\",\"content\":\"ok\"}}\n",
            "{\"type\":\"message\",\"id\":\"b\",\"parentId\":\"a\",\"message\":{\"role\":\"user\",\"content\":\"x\\ud83dy\"}}\n",
            "{\"type\":\"message\",\"id\":\"c\",\"parentId\":\"b\",\"message\":{\"role\":\"user\",\"content\":\"z\"}}\n",
        ),
    )
    .unwrap();
    let entries = crate::Context::read_session_entries(path.to_str().unwrap());
    let _ = std::fs::remove_dir_all(&dir);
    let ids: Vec<_> = entries.iter().map(|entry| entry["id"].as_str().unwrap().to_string()).collect();
    assert_eq!(ids, ["a", "b", "c"]);
    assert_eq!(entries[1]["message"]["content"], "x\u{fffd}y");
}

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
        let frame = format!(
            r#"{{"type":"request","id":"t","request":{{"method":"tool_call","args":{{"name":"echo","params":{args}}}}}}}"#
        );
        let envelope = decode_frame(frame.as_bytes()).unwrap_or_else(|err| panic!("{args}: {err}"));
        let request = envelope.request.unwrap();
        let args_value = request.args.unwrap();
        assert_eq!(args_value["params"]["text"], text, "{args}");
    }
    // An object key holding a lone unit is repaired the same way.
    let frame = br#"{"type":"notify","notify":{"method":"x","args":{"k\ud800":1}}}"#;
    let envelope: Envelope =
        serde_json::from_value(serde_json::to_value(decode_frame(frame).unwrap()).unwrap())
            .unwrap();
    assert_eq!(envelope.notify.unwrap().args.unwrap()["k\u{fffd}"], 1);
}

#[test]
fn a_frame_without_unpaired_surrogates_is_decoded_untouched() {
    let frame = r#"{"type":"request","id":"input","request":{"method":"terminal_input","args":{"data":"\ud83d","editorText":"A\ude00","toolsExpanded":true}}}"#;
    // The typed UTF-16 fields still keep the unit: the repair runs only when the first decode fails.
    let envelope = decode_frame(frame.as_bytes()).unwrap();
    assert_eq!(
        envelope
            .request
            .unwrap()
            .terminal_input
            .unwrap()
            .data
            .as_units(),
        [0xd83d]
    );
    assert!(
        replace_unpaired_surrogate_escapes(br#"{"a":"\ud83d\ude00 \\ud800 \u0041"}"#).is_none()
    );
    assert!(decode_frame(b"{\"type\":").is_err());
}

fn nested_request(depth: usize, text: &str) -> String {
    let deep = format!("{}1{}", "[".repeat(depth), "]".repeat(depth));
    format!(
        r#"{{"type":"request","id":"t","request":{{"method":"tool_call","args":{{"name":"echo","params":{{"v":{deep},"text":"{text}"}}}}}}}}"#
    )
}

// serde_json refuses nesting past 128 levels; every other runtime takes far more, and a refused frame ended the connection.
#[test]
fn a_tool_request_nested_past_serde_jsons_limit_still_decodes() {
    // The deep values drop on a stack made for them, as a request's thread does (a test thread has 2 MiB).
    on_deep_stack(tool_request_nested_past_serde_jsons_limit_still_decodes).unwrap();
}

fn tool_request_nested_past_serde_jsons_limit_still_decodes() {
    for depth in [10, 120, 130, 1000, MAX_NESTING_DEPTH - 10] {
        let envelope = decode_frame(nested_request(depth, "ok").as_bytes())
            .unwrap_or_else(|err| panic!("depth {depth}: {err}"));
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
    on_deep_stack(nesting_past_the_bound_is_refused).unwrap();
}

fn nesting_past_the_bound_is_refused() {
    // Past the bound the frame still decodes (a refused frame ended the connection): the request is answered with Pi's RangeError.
    let envelope = decode_frame(nested_request(MAX_NESTING_DEPTH + 10, "ok").as_bytes()).unwrap();
    let request = envelope.request.unwrap();
    assert_eq!(request.args_error.as_deref(), Some(TOO_DEEP_MESSAGE));
    assert!(request.args.is_none());
    let brackets = "[".repeat(MAX_NESTING_DEPTH * 2);
    let envelope = decode_frame(nested_request(130, &brackets).as_bytes()).unwrap();
    assert_eq!(
        envelope.request.unwrap().args.unwrap()["params"]["text"],
        brackets.as_str()
    );
    assert_eq!(nesting_depth(br#"{"a":["\"[[",{"b":[]}]}"#), 4);
    assert_eq!(nesting_depth(br#"["\\",[[]]]"#), 3);
}

#[test]
fn a_deep_frame_with_an_unpaired_surrogate_is_repaired_and_decoded() {
    let envelope = decode_frame(nested_request(300, r"x\ud800y").as_bytes()).unwrap();
    assert_eq!(
        envelope.request.unwrap().args.unwrap()["params"]["text"],
        "x\u{fffd}y"
    );
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
    let ids: Vec<_> = entries
        .iter()
        .map(|entry| entry["id"].as_str().unwrap().to_string())
        .collect();
    assert_eq!(ids, ["a", "b", "c"]);
    assert_eq!(entries[1]["message"]["content"], "x\u{fffd}y");
}

fn nested_notify(method: &str, depth: usize) -> String {
    let deep = format!("{}1{}", "[".repeat(depth), "]".repeat(depth));
    let args = if method == "ui.custom.input" {
        format!(r#"{{"key":"k","data":"x","state":{deep}}}"#)
    } else if method == "state_update" {
        format!(r#"{{"state":{{"footerData":{deep}}}}}"#)
    } else {
        format!(r#"{{"event":{deep}}}"#)
    };
    format!(r#"{{"type":"notify","notify":{{"method":"{method}","args":{args}}}}}"#)
}

// A notification nests as a request does (model_stream_event carries the parsed tool-call arguments of a partial message): up to the bound it decodes, past it the
// notification is marked, not refused, so the connection keeps serving and the loop drops it with a diagnostic.
#[test]
fn a_notification_nested_up_to_the_bound_decodes_and_past_it_is_marked() {
    on_deep_stack(notification_nested_up_to_the_bound_decodes).unwrap();
}

fn notification_nested_up_to_the_bound_decodes() {
    for method in ["model_stream_event", "ui.custom.input", "state_update"] {
        let envelope = decode_frame(nested_notify(method, 9990).as_bytes()).unwrap();
        let notify = envelope.notify.unwrap();
        assert!(notify.args_error.is_none(), "{method}");
        assert!(
            notify.args.is_some() || notify.custom_input.is_some() || method == "state_update",
            "{method}"
        );
        let envelope =
            decode_frame(nested_notify(method, MAX_NESTING_DEPTH + 10).as_bytes()).unwrap();
        let notify = envelope.notify.unwrap();
        assert_eq!(
            notify.args_error.as_deref(),
            Some(TOO_DEEP_MESSAGE),
            "{method}"
        );
        assert!(
            notify.args.is_none() && notify.custom_input.is_none(),
            "{method}"
        );
    }
}

// Only a request whose arguments needed the deep decode (nesting past serde_json's 128 levels) asks for the deep stack, which a thread stack committed under
// vm.overcommit_memory=2 or ulimit -v would charge per concurrent request; every other request runs on 8 MiB, enough to drop a deep value that reached it in a notification or a host call result.
#[test]
fn only_a_request_with_deep_arguments_runs_on_the_deep_stack() {
    let size_of = |depth: usize| {
        on_deep_stack(|| {
            let envelope = decode_frame(nested_request(depth, "ok").as_bytes()).unwrap();
            crate::extension::request_stack_size(&envelope.request.unwrap())
        })
        .unwrap()
    };
    for depth in [0, 10, 100] {
        assert_eq!(size_of(depth), 8 << 20, "depth {depth}");
    }
    for depth in [130, 1000, 9000] {
        assert_eq!(size_of(depth), 64 << 20, "depth {depth}");
    }
    // Past the bound the request is answered with the RangeError without running its handler.
    assert_eq!(size_of(MAX_NESTING_DEPTH + 10), 8 << 20);
}

fn nested_delta(depth: usize) -> String {
    let deep = format!("{}1{}", "[".repeat(depth), "]".repeat(depth));
    format!(
        r#"{{"type":"request","id":"t","request":{{"method":"event","event":"message_update","stream":{{"seq":2,"base":1,"ops":[{{"op":"set","path":["content",1],"value":{{"zeta":1,"meta":{{"d":{deep}}}}}}}]}}}}}}"#
    )
}

// The host puts a whole new subtree into a delta's `set` operation (a tool call of a partial message, with its arguments), so the operations nest as the arguments do. They are
// read like them: past serde_json's 128 levels up to the bound, and a value past the bound is answered with the RangeError instead of ending the connection.
#[test]
fn a_delta_whose_operations_nest_past_serde_jsons_limit_still_decodes() {
    on_deep_stack(delta_operations_nested_past_serde_jsons_limit_still_decode).unwrap();
}

fn delta_operations_nested_past_serde_jsons_limit_still_decode() {
    for depth in [10, 100, 130, 200, 9000, MAX_NESTING_DEPTH - 10] {
        let envelope = decode_frame(nested_delta(depth).as_bytes())
            .unwrap_or_else(|err| panic!("depth {depth}: {err}"));
        let req = envelope.request.unwrap();
        assert!(req.args_error.is_none(), "depth {depth}");
        assert_eq!(req.deep_args, depth >= 120, "depth {depth}");
        let ops = req.stream.as_ref().unwrap().ops.as_ref().unwrap();
        let mut level = &ops[0]["value"]["meta"]["d"];
        for _ in 0..depth {
            level = &level[0];
        }
        assert_eq!(level, 1, "depth {depth}");
        assert_eq!(
            crate::extension::request_stack_size(&req),
            if depth >= 120 { 64 << 20 } else { 8 << 20 },
            "depth {depth}"
        );
    }
    let envelope = decode_frame(nested_delta(MAX_NESTING_DEPTH + 10).as_bytes()).unwrap();
    let req = envelope.request.unwrap();
    assert_eq!(req.args_error.as_deref(), Some(TOO_DEEP_MESSAGE));
    assert!(req.stream.unwrap().ops.is_none());
}

// A host call's result (the highlighted lines of `highlight_code`) is cut out of its frame as a raw value, and the host writes a lone surrogate of the
// code as an escape. A `Value` or `String` cannot hold the unit, so the value is read with U+FFFD in its place, not refused.
#[test]
fn a_value_cut_out_of_a_frame_reads_an_unpaired_surrogate_escape_as_u_fffd() {
    let value: serde_json::Value =
        from_json_str(r#"{"lines":["a\ud83db","\ude00","\ud83d\ude00"]}"#).unwrap();
    assert_eq!(
        value["lines"],
        serde_json::json!(["a\u{fffd}b", "\u{fffd}", "😀"])
    );
    let lines: Vec<String> = from_json_str(r#"["x\ud800y"]"#).unwrap();
    assert_eq!(lines, ["x\u{fffd}y"]);
    assert!(from_json_str::<serde_json::Value>(r#"{"lines":["\ud83d"#).is_err());
}

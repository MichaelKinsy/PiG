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

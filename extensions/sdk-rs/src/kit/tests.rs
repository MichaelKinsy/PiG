use super::*;
use serde_json::json;

fn sha(data: &[u8]) -> String {
    digest::sha256_hex(data)
}

fn wire_view(ledger: &ViewLedger, view: &View) -> Value {
    ledger.wire(&ledger.encode(view).unwrap())
}

#[test]
fn every_kind_encodes_its_upstream_fields() {
    let tree = Container::new()
        .child(DynamicBorder::new("accent"))
        .child(Box::new(2, 0).bg("selectedBg").child(Text::new("in box", 1, 1)))
        .child(Text::new("Pick", 2, 0).bg("customMessageBg"))
        .child(TruncatedText::new("cut", 0, 0))
        .child(
            Markdown::new("- **a**", 1, 0)
                .default_text_style(TextStyle {
                    color: Some("text".into()),
                    bg_color: Some("customMessageBg".into()),
                    italic: true,
                    ..TextStyle::default()
                })
                .render_latex(false),
        )
        .child(Spacer::new(2))
        .child(
            SelectList::new(
                "tracks",
                vec![SelectItem::new("t1", "One").description("first"), SelectItem::new("t2", "Two")],
                3,
            )
            .layout(SelectLayout { min_primary_column_width: Some(4), max_primary_column_width: None })
            .selected_index(1)
            .filter("o"),
        )
        .child(
            SettingsList::new(
                "prefs",
                vec![
                    SettingItem::new("mode", "Mode", "fast").values(vec!["fast".into(), "slow".into()]),
                    SettingItem::new("theme", "Theme", "dark")
                        .description("colors")
                        .submenu(SelectList::new("themes", vec![SelectItem::new("dark", "Dark")], 5)),
                ],
                4,
            )
            .enable_search(true)
            .selected_index(1)
            .filter("th"),
        )
        .child(
            Loader::new("Working")
                .spinner_color("accent")
                .message_color("muted")
                .indicator(LoaderIndicator { frames: Some(vec!["a".into(), "b".into()]), interval_ms: Some(120) })
                .frame(1),
        )
        .child(
            HStack::new()
                .child(TruncatedText::new("left", 0, 0))
                .child_with(
                    TruncatedText::new("right", 0, 0),
                    StackEntry { grow: Some(1), basis: Some(3), ..StackEntry::default() },
                )
                .child_with(TruncatedText::new("plain", 0, 0), StackEntry::default())
                .gap(1)
                .align(Align::Center),
        )
        .child(VStack::new().child(Spacer::new(1)))
        .child(Lines::new(vec!["raw".into()]));
    let view = View::new(tree).focus("tracks").theme("accent", "#d75f00");
    let encoded = view.encode(false).unwrap();
    assert_eq!(
        encoded.body,
        json!({
            "root": {"kind": "container", "children": [
                {"kind": "dynamic-border", "color": "accent"},
                {"kind": "box", "children": [{"kind": "text", "text": "in box", "paddingX": 1, "paddingY": 1}],
                 "paddingX": 2, "paddingY": 0, "bg": "selectedBg"},
                {"kind": "text", "text": "Pick", "paddingX": 2, "paddingY": 0, "bg": "customMessageBg"},
                {"kind": "truncated-text", "text": "cut", "paddingX": 0, "paddingY": 0},
                {"kind": "markdown", "text": "- **a**", "paddingX": 1, "paddingY": 0,
                 "defaultTextStyle": {"color": "text", "bgColor": "customMessageBg", "italic": true},
                 "renderLatex": false},
                {"kind": "spacer", "lines": 2},
                {"kind": "select-list", "id": "tracks",
                 "items": [{"value": "t1", "label": "One", "description": "first"}, {"value": "t2", "label": "Two"}],
                 "maxVisible": 3, "layout": {"minPrimaryColumnWidth": 4}, "selectedIndex": 1, "filter": "o"},
                {"kind": "settings-list", "id": "prefs", "items": [
                    {"id": "mode", "label": "Mode", "currentValue": "fast", "values": ["fast", "slow"]},
                    {"id": "theme", "label": "Theme", "description": "colors", "currentValue": "dark",
                     "submenu": {"kind": "select-list", "id": "themes",
                                 "items": [{"value": "dark", "label": "Dark"}], "maxVisible": 5}}],
                 "maxVisible": 4, "enableSearch": true, "selectedIndex": 1, "filter": "th"},
                {"kind": "loader", "message": "Working", "spinnerColor": "accent", "messageColor": "muted",
                 "indicator": {"frames": ["a", "b"], "intervalMs": 120}, "frame": 1},
                {"kind": "hstack", "children": [
                    {"kind": "truncated-text", "text": "left", "paddingX": 0, "paddingY": 0},
                    {"kind": "truncated-text", "text": "right", "paddingX": 0, "paddingY": 0,
                     "stack": {"basis": 3, "grow": 1}},
                    {"kind": "truncated-text", "text": "plain", "paddingX": 0, "paddingY": 0}],
                 "gap": 1, "align": "center"},
                {"kind": "vstack", "children": [{"kind": "spacer", "lines": 1}]},
                {"kind": "lines", "content": ["raw"]}
            ]},
            "focus": "tracks",
            "theme": {"accent": "#d75f00"}
        })
    );
    assert!(encoded.images.is_empty());
}

#[test]
fn defaults_match_upstream_constructors() {
    let encoded = View::new(
        Container::new()
            .child(Box::default())
            .child(Spacer::default())
            .child(DynamicBorder::default())
            .child(Loader::default()),
    )
    .encode(false)
    .unwrap();
    assert_eq!(
        encoded.body["root"]["children"],
        json!([
            {"kind": "box", "paddingX": 1, "paddingY": 1},
            {"kind": "spacer", "lines": 1},
            {"kind": "dynamic-border", "color": "border"},
            {"kind": "loader", "message": "Loading..."}
        ])
    );
}

#[test]
fn theme_override_replaces_an_earlier_value_for_the_token() {
    let body = View::new(Spacer::new(1))
        .theme("accent", "#000000")
        .theme("muted", "#111111")
        .theme("accent", "#d75f00")
        .encode(false)
        .unwrap()
        .body;
    assert_eq!(body["theme"], json!({"accent": "#d75f00", "muted": "#111111"}));
    assert_eq!(serde_json::to_string(&body["theme"]).unwrap(), r##"{"accent":"#d75f00","muted":"#111111"}"##);
}

#[test]
fn image_ref_is_the_sha256_of_its_bytes() {
    let image = Image::new(b"png-bytes".to_vec(), "image/png")
        .max_width_cells(20)
        .max_height_cells(10)
        .filename("cover.png")
        .fallback_color("dim");
    assert_eq!(image.reference(), sha(b"png-bytes"));
    let encoded = View::new(image).encode(false).unwrap();
    assert_eq!(
        encoded.body["root"],
        json!({"kind": "image", "ref": sha(b"png-bytes"), "mimeType": "image/png", "maxWidthCells": 20,
               "maxHeightCells": 10, "filename": "cover.png", "fallbackColor": "dim"})
    );
    assert_eq!(encoded.images.len(), 1);
}

#[test]
fn lines_annotations_travel_only_with_a_frontend() {
    let view = View::new(
        Lines::new(vec!["▀▀".into()]).image(b"cover".to_vec(), "image/jpeg").progress(30.0, 120.0),
    );
    let without = view.encode(false).unwrap();
    assert_eq!(without.body["root"], json!({"kind": "lines", "content": ["▀▀"]}));
    assert!(without.images.is_empty(), "no image bytes without a frontend");

    let with = view.encode(true).unwrap();
    assert_eq!(
        with.body["root"],
        json!({"kind": "lines", "content": ["▀▀"], "image": {"ref": sha(b"cover")},
               "progress": {"value": 30.0, "max": 120.0}})
    );
    assert_eq!(with.images.len(), 1);
}

#[test]
fn list_annotation_travels_only_with_a_frontend() {
    let list = List {
        items: vec![
            ListItem { label: "Blue in Green".into(), detail: Some("Miles Davis".into()), columns: Some(vec!["5:37".into()]) },
            ListItem { label: "So What".into(), ..ListItem::default() },
        ],
        selected: Some(1),
    };
    let view = View::new(Lines::new(vec!["a".into(), "b".into()]).list(list));
    assert_eq!(view.encode(false).unwrap().body["root"], json!({"kind": "lines", "content": ["a", "b"]}));
    assert_eq!(
        view.encode(true).unwrap().body["root"]["list"],
        json!({"items": [{"label": "Blue in Green", "detail": "Miles Davis", "columns": ["5:37"]}, {"label": "So What"}],
               "selectedIndex": 1})
    );
    let none = View::new(Lines::new(vec!["a".into()]).list(List { items: vec![ListItem::default()], selected: None }));
    assert_eq!(none.encode(true).unwrap().body["root"]["list"]["selectedIndex"], -1, "no selection is -1");
}

#[test]
fn ledger_sends_image_bytes_once_per_connection_until_evicted() {
    let ledger = ViewLedger::default();
    let view = View::new(
        Container::new()
            .child(Image::new(b"abc".to_vec(), "image/png"))
            .child(Image::new(b"abc".to_vec(), "image/png")),
    );
    let first = wire_view(&ledger, &view);
    assert_eq!(
        first["images"],
        json!([{"ref": sha(b"abc"), "mimeType": "image/png", "data": "YWJj"}]),
        "one entry per ref, base64 of the bytes"
    );
    let encoded = ledger.encode(&view).unwrap();
    assert!(!ledger.has_unsent(&encoded));
    assert!(ledger.wire(&encoded).get("images").is_none(), "the second frame names the ref only");

    let refs: HashSet<String> = [sha(b"abc")].into();
    assert!(encoded.references(&refs));
    assert_eq!(encoded.refs(), vec![sha(b"abc")]);
    ledger.forget(&refs);
    assert!(ledger.has_unsent(&encoded));
    assert_eq!(wire_view(&ledger, &view)["images"][0]["data"], "YWJj", "evicted bytes are sent again");
}

#[test]
fn a_view_deeper_than_the_host_bound_is_an_error() {
    let mut node = Node::from(Spacer::new(1));
    for _ in 0..63 {
        node = Container::new().child(node).into();
    }
    assert!(View::new(node.clone()).encode(false).is_ok(), "64 levels are within the bound");
    let deeper = View::new(Container::new().child(node));
    assert_eq!(deeper.encode(false).unwrap_err(), "kit: view deeper than 64 nodes");
    let rendered = Rendered::View(deeper).into_result(&ViewLedger::default());
    assert_eq!(rendered.unwrap_err(), "kit: view deeper than 64 nodes");
}

#[test]
fn ledger_follows_the_frontend_flag_of_each_snapshot() {
    let ledger = ViewLedger::default();
    let view = View::new(Lines::new(vec![]).progress(1.0, 2.0));
    assert!(wire_view(&ledger, &view)["root"].get("progress").is_none());
    ledger.apply_state(json!({"frontend": true}).as_object().unwrap());
    assert_eq!(wire_view(&ledger, &view)["root"]["progress"], json!({"value": 1.0, "max": 2.0}));
    // state_update snapshots omit a false flag.
    ledger.apply_state(json!({"hasUI": true}).as_object().unwrap());
    assert!(wire_view(&ledger, &view)["root"].get("progress").is_none());
}

#[test]
fn events_decode_each_upstream_callback() {
    let (key, select) = Event::from_wire(&json!({
        "key": "custom-1", "node": "tracks", "type": "select", "index": 2,
        "item": {"value": "k2", "label": "Two", "description": "d"}
    }))
    .unwrap();
    assert_eq!(key, "custom-1");
    assert_eq!(
        select,
        Event {
            node: "tracks".into(),
            kind: EventKind::Select,
            index: 2,
            item: Some(SelectItem::new("k2", "Two").description("d")),
            id: String::new(),
            value: String::new(),
        }
    );
    let (_, change) =
        Event::from_wire(&json!({"key": "k", "node": "prefs", "type": "change", "index": 0, "id": "mode", "value": "slow"}))
            .unwrap();
    assert_eq!((change.kind, change.id.as_str(), change.value.as_str(), change.item), (EventKind::Change, "mode", "slow", None));
    for (wire, kind) in [("cancel", EventKind::Cancel), ("selectionChange", EventKind::SelectionChange)] {
        assert_eq!(Event::from_wire(&json!({"key": "k", "node": "n", "type": wire})).unwrap().1.kind, kind);
    }
    assert!(Event::from_wire(&json!({"key": "k", "node": "n", "type": "hover"})).is_none());
}

#[test]
fn rendered_results_carry_lines_or_a_view_never_both() {
    let ledger = ViewLedger::default();
    assert_eq!(Rendered::Lines(vec!["a".into()]).into_result(&ledger).unwrap(), json!({"lines": ["a"]}));
    let view = Rendered::View(View::new(Spacer::new(1))).into_result(&ledger).unwrap();
    assert_eq!(view, json!({"view": {"root": {"kind": "spacer", "lines": 1}}}));
}

#[test]
fn conversation_kinds_encode_as_the_go_sdk() {
    let image = b"png bytes".to_vec();
    let reference = sha(&image);
    let mut assistant = AssistantMessage::new(None).id("a1");
    assistant.update_content(
        Message {
            content: vec![ContentBlock::thinking("plan"), ContentBlock::text("done"), ContentBlock::tool_call()],
            stop_reason: "toolUse".into(),
            ..Message::default()
        },
        true,
    );
    let mut hidden = AssistantMessage::new(Some(Message {
        content: vec![ContentBlock::text("x")],
        stop_reason: "error".into(),
        error_message: "boom".into(),
    }));
    hidden.set_hide_thinking_block(true);
    hidden.set_hidden_thinking_label("Pondering...");
    hidden.set_output_pad(0);
    let mut tool = ToolExecution::new("read", "call-1", json!({"path": "a.txt"}), "/work").id("call-1");
    tool.set_args_complete();
    tool.mark_execution_started();
    tool.update_result(
        ToolResult {
            content: vec![ToolResultContent::text("hi"), ToolResultContent::image(image.clone(), "image/png")],
            is_error: true,
            details: Some(json!({"n": 1})),
        },
        false,
    );
    tool.set_expanded(true);
    let mut empty = ToolExecution::new("kit_tool", "", Value::Null, "").tool_definition(ToolDefinition::Empty);
    empty.set_show_images(false);
    empty.set_image_width_cells(30);
    let mut bash = BashExecution::new("ls", true).id("b");
    bash.append_output("a\n");
    bash.append_output("b");
    bash.set_complete(Some(2), false, true, "/tmp/out");
    bash.set_expanded(true);
    let diff = Diff::new(" 1 a\n-2 b\n+2 c").file_path("x.go");
    let tree = Container::new()
        .child(UserMessage::new("hello"))
        .child(assistant)
        .child(hidden)
        .child(tool)
        .child(empty)
        .child(bash)
        .child(BashExecution::new("sleep", false))
        .child(diff);
    let encoded = View::new(tree).encode(false).unwrap();
    let want = String::from(r#"{"root":{"kind":"container","children":["#)
        + r#"{"kind":"user-message","text":"hello","outputPad":1},"#
        + r#"{"kind":"assistant-message","id":"a1","message":{"content":[{"type":"thinking","thinking":"plan"},{"type":"text","text":"done"},{"type":"toolCall"}],"stopReason":"toolUse"},"outputPad":1,"isStreaming":true},"#
        + r#"{"kind":"assistant-message","message":{"content":[{"type":"text","text":"x"}],"stopReason":"error","errorMessage":"boom"},"outputPad":0,"hideThinkingBlock":true,"hiddenThinkingLabel":"Pondering..."},"#
        + r#"{"kind":"tool-execution","id":"call-1","toolName":"read","toolCallId":"call-1","args":{"path":"a.txt"},"cwd":"/work","executionStarted":true,"argsComplete":true,"expanded":true,"result":{"content":[{"type":"text","text":"hi"},{"type":"image","ref":""#
        + &reference
        + r#"","mimeType":"image/png"}],"isError":true,"details":{"n":1}},"isPartial":false},"#
        + r#"{"kind":"tool-execution","toolName":"kit_tool","args":{},"toolDefinition":"empty","showImages":false,"imageWidthCells":30},"#
        + r#"{"kind":"bash-execution","id":"b","expanded":true,"command":"ls","excludeFromContext":true,"output":"a\nb","complete":{"exitCode":2,"truncated":true,"fullOutputPath":"/tmp/out"}},"#
        + r#"{"kind":"bash-execution","command":"sleep"},"#
        + r#"{"kind":"diff","paddingX":0,"paddingY":0,"diff":" 1 a\n-2 b\n+2 c","filePath":"x.go"}]}}"#;
    assert_eq!(serde_json::to_string(&encoded.body).unwrap(), want);
    assert_eq!(encoded.refs(), vec![reference]);
}

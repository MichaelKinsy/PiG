//! pi-tui helpers against the vectors generated from Pi's own functions (`test/parity/sdk-vectors/gen.mjs`). Every SDK reproduces the
//! same vectors, so a helper that returns a constant or ports a different rule fails here.

use std::path::PathBuf;

use serde_json::Value;

use super::*;

fn load(name: &str) -> Value {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../test/parity/sdk-vectors").join(name);
    serde_json::from_slice(&std::fs::read(&path).unwrap_or_else(|e| panic!("{}: {e}", path.display()))).unwrap()
}

fn s(v: &Value) -> &str {
    v.as_str().unwrap()
}

fn i(v: &Value) -> i64 {
    v.as_i64().unwrap()
}

#[test]
fn text_vectors_match_pi() {
    let v = load("tui-text.json");
    assert_eq!(s(&v["cursorMarker"]), CURSOR_MARKER);
    assert!(v["visibleWidth"].as_array().unwrap().len() > 1000 && v["truncate"].as_array().unwrap().len() > 1000);
    for c in v["visibleWidth"].as_array().unwrap() {
        assert_eq!(visible_width(s(&c[0])) as i64, i(&c[1]), "visible_width({:?})", c[0]);
    }
    for c in v["strip"].as_array().unwrap() {
        assert_eq!(strip_terminal_sequences(s(&c[0])), s(&c[1]), "strip({:?})", c[0]);
    }
    for c in v["bg"].as_array().unwrap() {
        assert_eq!(get_active_background_ansi(s(&c[0])), s(&c[1]), "bg({:?})", c[0]);
    }
    for c in v["truncate"].as_array().unwrap() {
        let (ellipsis, pad) = if c[2].is_null() { ("...", false) } else { (s(&c[2]), c[3].as_bool().unwrap()) };
        assert_eq!(truncate_to_width(s(&c[0]), i(&c[1]), ellipsis, pad), s(&c[4]), "truncate({:?}, {:?}, {:?}, {:?})", c[0], c[1], c[2], c[3]);
    }
    for c in v["wrap"].as_array().unwrap() {
        let want: Vec<&str> = c[2].as_array().unwrap().iter().map(s).collect();
        assert_eq!(wrap_text_with_ansi(s(&c[0]), i(&c[1])), want, "wrap({:?}, {:?})", c[0], c[1]);
    }
    for c in v["slice"].as_array().unwrap() {
        let (line, start, n, strict) = (s(&c[0]), i(&c[1]), i(&c[2]), c[3].as_bool().unwrap());
        assert_eq!(slice_by_column(line, start, n, strict), s(&c[4]), "slice_by_column({line:?}, {start}, {n}, {strict})");
        let w = &c[5];
        assert_eq!(slice_with_width(line, start, n, strict), SliceResult { text: s(&w["text"]).to_string(), width: i(&w["width"]) });
    }
    for c in v["segments"].as_array().unwrap() {
        let r = &c[5];
        assert_eq!(
            extract_segments(s(&c[0]), i(&c[1]), i(&c[2]), i(&c[3]), c[4].as_bool().unwrap()),
            Segments { before: s(&r["before"]).into(), before_width: i(&r["beforeWidth"]), after: s(&r["after"]).into(), after_width: i(&r["afterWidth"]) },
            "extract_segments({:?})",
            c
        );
    }
    for c in v["link"].as_array().unwrap() {
        let want = c[2].as_str().map(str::to_string);
        assert_eq!(get_osc8_link_at_column(s(&c[0]), i(&c[1])), want, "link({:?}, {:?})", c[0], c[1]);
    }
}

fn snake(name: &str) -> String {
    let mut out = String::new();
    for ch in name.chars() {
        if ch.is_ascii_uppercase() {
            out.push('_');
            out.push(ch.to_ascii_lowercase());
        } else {
            out.push(ch);
        }
    }
    out
}

fn key_helper(name: &str) -> Option<&'static str> {
    Some(match name {
        "escape" => Key::ESCAPE, "esc" => Key::ESC, "enter" => Key::ENTER, "return" => Key::RETURN, "tab" => Key::TAB, "space" => Key::SPACE,
        "backspace" => Key::BACKSPACE, "delete" => Key::DELETE, "insert" => Key::INSERT, "clear" => Key::CLEAR, "home" => Key::HOME, "end" => Key::END,
        "pageUp" => Key::PAGE_UP, "pageDown" => Key::PAGE_DOWN, "up" => Key::UP, "down" => Key::DOWN, "left" => Key::LEFT, "right" => Key::RIGHT,
        "f1" => Key::F1, "f2" => Key::F2, "f3" => Key::F3, "f4" => Key::F4, "f5" => Key::F5, "f6" => Key::F6, "f7" => Key::F7, "f8" => Key::F8,
        "f9" => Key::F9, "f10" => Key::F10, "f11" => Key::F11, "f12" => Key::F12, "backtick" => Key::BACKTICK, "hyphen" => Key::HYPHEN,
        "equals" => Key::EQUALS, "leftbracket" => Key::LEFTBRACKET, "rightbracket" => Key::RIGHTBRACKET, "backslash" => Key::BACKSLASH,
        "semicolon" => Key::SEMICOLON, "quote" => Key::QUOTE, "comma" => Key::COMMA, "period" => Key::PERIOD, "slash" => Key::SLASH,
        "exclamation" => Key::EXCLAMATION, "at" => Key::AT, "hash" => Key::HASH, "dollar" => Key::DOLLAR, "percent" => Key::PERCENT,
        "caret" => Key::CARET, "ampersand" => Key::AMPERSAND, "asterisk" => Key::ASTERISK, "leftparen" => Key::LEFTPAREN,
        "rightparen" => Key::RIGHTPAREN, "underscore" => Key::UNDERSCORE, "plus" => Key::PLUS, "pipe" => Key::PIPE, "tilde" => Key::TILDE,
        "leftbrace" => Key::LEFTBRACE, "rightbrace" => Key::RIGHTBRACE, "colon" => Key::COLON, "lessthan" => Key::LESSTHAN,
        "greaterthan" => Key::GREATERTHAN, "question" => Key::QUESTION,
        _ => return None,
    })
}

fn key_builder(name: &str, key: &str) -> String {
    match snake(name).as_str() {
        "ctrl" => Key::ctrl(key), "shift" => Key::shift(key), "alt" => Key::alt(key), "super" => Key::super_(key),
        "ctrl_shift" => Key::ctrl_shift(key), "shift_ctrl" => Key::shift_ctrl(key), "ctrl_alt" => Key::ctrl_alt(key), "alt_ctrl" => Key::alt_ctrl(key),
        "shift_alt" => Key::shift_alt(key), "alt_shift" => Key::alt_shift(key), "ctrl_super" => Key::ctrl_super(key), "super_ctrl" => Key::super_ctrl(key),
        "shift_super" => Key::shift_super(key), "super_shift" => Key::super_shift(key), "alt_super" => Key::alt_super(key), "super_alt" => Key::super_alt(key),
        "ctrl_shift_alt" => Key::ctrl_shift_alt(key), "ctrl_shift_super" => Key::ctrl_shift_super(key),
        other => panic!("Key.{other} is missing"),
    }
}

#[test]
fn key_vectors_match_pi() {
    let v = load("tui-keys.json");
    for (name, want) in v["keyHelper"].as_object().unwrap() {
        match want {
            Value::String(text) => assert_eq!(key_helper(name), Some(text.as_str()), "Key.{name}"),
            Value::Array(list) => {
                let got: Vec<String> = ["a", "Z", "enter", "f5", "[", "space"].iter().map(|k| key_builder(name, k)).collect();
                let want: Vec<&str> = list.iter().map(s).collect();
                assert_eq!(got, want, "Key.{name}");
            }
            _ => unreachable!(),
        }
    }
    let universe: Vec<&str> = v["universe"].as_array().unwrap().iter().map(s).collect();
    let data: Vec<&str> = v["data"].as_array().unwrap().iter().map(s).collect();
    for mode in v["modes"].as_array().unwrap() {
        // Tests in this crate share one process; no other test reads these variables.
        for name in ["WT_SESSION", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"] {
            unsafe { std::env::remove_var(name) };
        }
        for (name, value) in mode["env"].as_object().unwrap() {
            unsafe { std::env::set_var(name, s(value)) };
        }
        set_kitty_protocol_active(mode["kitty"].as_bool().unwrap());
        for (index, row) in mode["rows"].as_array().unwrap().iter().enumerate() {
            let d = data[index];
            let ctx = format!("kitty={} env={} {:?}", mode["kitty"], mode["env"], d);
            assert_eq!(parse_key(d).as_deref(), row[0].as_str(), "parse_key {ctx}");
            assert_eq!(is_key_release(d), row[1].as_bool().unwrap(), "release {ctx}");
            assert_eq!(is_key_repeat(d), row[2].as_bool().unwrap(), "repeat {ctx}");
            assert_eq!(decode_kitty_printable(d).as_deref(), row[3].as_str(), "decode_kitty_printable {ctx}");
            assert_eq!(decode_printable_key(d).as_deref(), row[4].as_str(), "decode_printable_key {ctx}");
            let matched: std::collections::HashSet<usize> = row[5].as_array().unwrap().iter().map(|x| x.as_u64().unwrap() as usize).collect();
            // Every identifier Pi matches must match, and every 11th of the rest (an offset that moves with the row) must not: the full
            // universe is 5 million calls per mode, which the Go SDK runs in full.
            for &j in &matched {
                assert!(matches_key(d, universe[j]), "matches_key({:?}) {ctx}", universe[j]);
            }
            for j in (index % 11..universe.len()).step_by(11) {
                assert!(matched.contains(&j) || !matches_key(d, universe[j]), "matches_key({:?}) must be false {ctx}", universe[j]);
            }
        }
    }
    set_kitty_protocol_active(false);
    for name in ["WT_SESSION", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"] {
        unsafe { std::env::remove_var(name) };
    }
}

#[test]
fn fuzzy_vectors_match_pi() {
    let v = load("tui-fuzzy.json");
    for c in v["match"].as_array().unwrap() {
        let got = fuzzy_match(s(&c[0]), s(&c[1]));
        let matches = c[2].as_bool().unwrap();
        assert!(got.matches == matches && (!matches || got.score == c[3].as_f64().unwrap()), "fuzzy_match({:?}, {:?}) = {got:?}, Pi {:?} {:?}", c[0], c[1], c[2], c[3]);
    }
    for c in v["filter"].as_array().unwrap() {
        let items: Vec<String> = c[0].as_array().unwrap().iter().map(|x| s(x).to_string()).collect();
        let got: Vec<&String> = fuzzy_filter(&items, s(&c[1]), |t| t.clone());
        let want: Vec<&str> = c[2].as_array().unwrap().iter().map(s).collect();
        assert_eq!(got.iter().map(|x| x.as_str()).collect::<Vec<_>>(), want, "fuzzy_filter({:?}, {:?})", c[0], c[1]);
    }
}

#[test]
fn js_lower_matches_every_code_point() {
    let changed: std::collections::HashMap<u32, String> = load("tui-lower.json")["pairs"]
        .as_array()
        .unwrap()
        .iter()
        .map(|p| (p[0].as_u64().unwrap() as u32, p[1].as_array().unwrap().iter().map(|c| char::from_u32(c.as_u64().unwrap() as u32).unwrap()).collect()))
        .collect();
    for cp in 0..=0x10FFFFu32 {
        let Some(c) = char::from_u32(cp) else { continue };
        if cp == 0x3A3 {
            continue;
        }
        let want = changed.get(&cp).cloned().unwrap_or_else(|| c.to_string());
        assert_eq!(fuzzy::js_lower(&c.to_string()), want, "{cp:#x}");
    }
}

fn color(o: &Value) -> Color {
    match s(&o["kind"]) {
        "indexed" => Color::Indexed(i(&o["index"]) as u8),
        "rgb" => Color::Rgb { r: o["r"].as_f64().unwrap(), g: o["g"].as_f64().unwrap(), b: o["b"].as_f64().unwrap() },
        _ => Color::Oklch { l: o["l"].as_f64().unwrap(), c: o["c"].as_f64().unwrap(), h: o["h"].as_f64().unwrap() },
    }
}

fn color_json(c: Color) -> Value {
    match c {
        Color::Indexed(i) => serde_json::json!({"kind": "indexed", "index": i}),
        Color::Rgb { r, g, b } => serde_json::json!({"kind": "rgb", "r": r, "g": g, "b": b}),
        Color::Oklch { l, c, h } => serde_json::json!({"kind": "oklch", "l": l, "c": c, "h": h}),
    }
}

/// Exact for integers and strings, 1e-9 for the transcendental channels (V8 and the platform libm round sin, cos, atan2 and cbrt
/// differently in the last bits), and no hue for a neutral color.
fn near(got: &Value, want: &Value) -> bool {
    match (got, want) {
        (Value::Number(g), Value::Number(w)) => {
            let (g, w) = (g.as_f64().unwrap(), w.as_f64().unwrap());
            g == w || (g - w).abs() <= 1e-9 * w.abs().max(1.0)
        }
        (Value::Null, Value::Number(_)) | (Value::Number(_), Value::Null) => true,
        (Value::Object(g), Value::Object(w)) => {
            if g.len() != w.len() {
                return false;
            }
            let neutral = ["c", "s"].iter().any(|k| w.get(*k).and_then(Value::as_f64).is_some_and(|x| x < 1e-9));
            w.iter().all(|(k, x)| (k == "h" && neutral) || g.get(k).is_some_and(|y| near(y, x)))
        }
        _ => got == want,
    }
}

fn mode(v: &Value) -> TerminalColorMode {
    if s(v) == "truecolor" { TerminalColorMode::TrueColor } else { TerminalColorMode::Color256 }
}

fn attributes(o: &Value) -> TextAttributes {
    let flag = |k: &str| o.get(k).and_then(Value::as_bool).unwrap_or(false);
    TextAttributes { bold: flag("bold"), dim: flag("dim"), italic: flag("italic"), underline: flag("underline"), inverse: flag("inverse"), strikethrough: flag("strikethrough") }
}

#[test]
fn color_vectors_match_pi() {
    let v = load("tui-colors.json");
    let ops = v["ops"].as_array().unwrap();
    assert!(ops.len() > 1000);
    for op in ops {
        let (name, args, want) = (s(&op[0]), op[1].as_array().unwrap(), &op[2]);
        let num = |k: usize| args[k].as_f64().unwrap_or(f64::NAN);
        let result: Result<Value, ColorError> = match name {
            "parseColor" => match &args[0] {
                Value::String(t) if t == "NaN" => parse_color(f64::NAN).map(color_json),
                Value::String(t) => parse_color(t.as_str()).map(color_json),
                n => parse_color(n.as_f64().unwrap()).map(color_json),
            },
            "colorToRgb" => {
                let r = color_to_rgb(color(&args[0]));
                Ok(serde_json::json!({"r": r.r, "g": r.g, "b": r.b}))
            }
            "colorToHex" => Ok(Value::String(color_to_hex(color(&args[0])))),
            "colorToOklch" => {
                let o = color_to_oklch(color(&args[0]));
                Ok(serde_json::json!({"l": o.l, "c": o.c, "h": o.h}))
            }
            "colorToOkhsl" => {
                let o = color_to_okhsl(color(&args[0]));
                Ok(serde_json::json!({"h": o.h, "s": o.s, "l": o.l}))
            }
            "foregroundAnsi" => Ok(Value::String(foreground_ansi(color(&args[0]), mode(&args[1])))),
            "backgroundAnsi" => Ok(Value::String(background_ansi(color(&args[0]), mode(&args[1])))),
            "rgbColor" => rgb_color(num(0), num(1), num(2)).map(color_json),
            "oklchColor" => oklch_color(num(0), num(1), num(2)).map(color_json),
            "okhslColor" => okhsl_color(num(0), num(1), num(2)).map(color_json),
            "indexedColor" => indexed_color(num(0)).map(color_json),
            "mixColors" => {
                let space = if args[3].as_str() == Some("srgb") { ColorMixSpace::Srgb } else { ColorMixSpace::Oklch };
                mix_colors(color(&args[0]), color(&args[1]), num(2), space).map(color_json)
            }
            "styleText" => {
                let st = &args[1];
                let style = TextStyle { attributes: attributes(st), fg: st.get("fg").map(color), bg: st.get("bg").map(color) };
                Ok(Value::String(style_text(s(&args[0]), style, mode(&args[2]))))
            }
            "styleTextWithAnsi" => Ok(Value::String(style_text_with_ansi(s(&args[0]), args[1].as_str(), args[2].as_str(), attributes(&args[3])))),
            other => panic!("unknown op {other}"),
        };
        match (result, want.get("error")) {
            (Err(e), Some(msg)) => assert_eq!(e.0, s(msg), "{name}{args:?}"),
            (Ok(got), None) => assert!(near(&got, &want["ok"]), "{name}{args:?} = {got}, Pi {}", want["ok"]),
            (got, _) => panic!("{name}{args:?} = {got:?}, Pi {want}"),
        }
    }
}

#[test]
fn hyperlink_and_capabilities_match_pi() {
    let v = load("tui-terminal.json");
    for c in v["hyperlink"].as_array().unwrap() {
        assert_eq!(hyperlink(s(&c[0]), s(&c[1])), s(&c[2]));
    }
    let mut probed = 0;
    for c in v["capabilities"].as_array().unwrap() {
        let env = c["env"].as_object().unwrap();
        let lookup = |k: &str| env.get(k).map(|x| s(x).to_string());
        let calls = std::cell::Cell::new(0);
        let tmux = c["tmux"].as_bool().unwrap();
        let probe = || {
            calls.set(calls.get() + 1);
            tmux
        };
        let got = terminal::detect_in(&lookup, s(&c["platform"]) == "win32", Some(&probe));
        let d = &c["detected"];
        let want = TerminalCapabilities {
            images: match d["images"].as_str() {
                Some("kitty") => Some(ImageProtocol::Kitty),
                Some("iterm2") => Some(ImageProtocol::ITerm2),
                _ => None,
            },
            true_color: d["trueColor"].as_bool().unwrap(),
            hyperlinks: d["hyperlinks"].as_bool().unwrap(),
        };
        assert_eq!(got, want, "{c}");
        assert_eq!(calls.get() > 0, c["tmuxProbed"].as_bool().unwrap(), "{c}");
        probed += usize::from(calls.get() > 0);
    }
    assert!(probed > 0);
}

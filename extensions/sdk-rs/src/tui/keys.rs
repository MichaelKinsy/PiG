//! pi-tui's `keys.ts`: raw terminal input against key identifiers.
//!
//! A port of the pinned Pi release's `keys.ts` (legacy sequences, Kitty CSI-u with alternate keys and event types, xterm modifyOtherKeys).
//! JavaScript number semantics are kept where they decide a result: `parseInt` of an over-long digit run is a float, bitwise operators
//! truncate to int32, and `String.fromCharCode` keeps the low 16 bits.
//!
//! A code point that is a lone surrogate (`\ud800`..`\udfff`) has no `String` in Rust; [`decode_kitty_printable`] and
//! [`decode_printable_key`] return `None` for it where Pi returns the lone surrogate.

use std::sync::atomic::{AtomicBool, Ordering};

static KITTY_PROTOCOL_ACTIVE: AtomicBool = AtomicBool::new(false);

/// pi-tui's `setKittyProtocolActive`: set the global Kitty keyboard protocol state.
pub fn set_kitty_protocol_active(active: bool) {
    KITTY_PROTOCOL_ACTIVE.store(active, Ordering::SeqCst);
}

/// pi-tui's `isKittyProtocolActive`.
pub fn is_kitty_protocol_active() -> bool {
    KITTY_PROTOCOL_ACTIVE.load(Ordering::SeqCst)
}

/// pi-tui's `Key` object: named key identifiers and modifier builders.
pub struct Key;

#[allow(missing_docs)]
impl Key {
    pub const ESCAPE: &'static str = "escape";
    pub const ESC: &'static str = "esc";
    pub const ENTER: &'static str = "enter";
    pub const RETURN: &'static str = "return";
    pub const TAB: &'static str = "tab";
    pub const SPACE: &'static str = "space";
    pub const BACKSPACE: &'static str = "backspace";
    pub const DELETE: &'static str = "delete";
    pub const INSERT: &'static str = "insert";
    pub const CLEAR: &'static str = "clear";
    pub const HOME: &'static str = "home";
    pub const END: &'static str = "end";
    pub const PAGE_UP: &'static str = "pageUp";
    pub const PAGE_DOWN: &'static str = "pageDown";
    pub const UP: &'static str = "up";
    pub const DOWN: &'static str = "down";
    pub const LEFT: &'static str = "left";
    pub const RIGHT: &'static str = "right";
    pub const F1: &'static str = "f1";
    pub const F2: &'static str = "f2";
    pub const F3: &'static str = "f3";
    pub const F4: &'static str = "f4";
    pub const F5: &'static str = "f5";
    pub const F6: &'static str = "f6";
    pub const F7: &'static str = "f7";
    pub const F8: &'static str = "f8";
    pub const F9: &'static str = "f9";
    pub const F10: &'static str = "f10";
    pub const F11: &'static str = "f11";
    pub const F12: &'static str = "f12";
    pub const BACKTICK: &'static str = "`";
    pub const HYPHEN: &'static str = "-";
    pub const EQUALS: &'static str = "=";
    pub const LEFTBRACKET: &'static str = "[";
    pub const RIGHTBRACKET: &'static str = "]";
    pub const BACKSLASH: &'static str = "\\";
    pub const SEMICOLON: &'static str = ";";
    pub const QUOTE: &'static str = "'";
    pub const COMMA: &'static str = ",";
    pub const PERIOD: &'static str = ".";
    pub const SLASH: &'static str = "/";
    pub const EXCLAMATION: &'static str = "!";
    pub const AT: &'static str = "@";
    pub const HASH: &'static str = "#";
    pub const DOLLAR: &'static str = "$";
    pub const PERCENT: &'static str = "%";
    pub const CARET: &'static str = "^";
    pub const AMPERSAND: &'static str = "&";
    pub const ASTERISK: &'static str = "*";
    pub const LEFTPAREN: &'static str = "(";
    pub const RIGHTPAREN: &'static str = ")";
    pub const UNDERSCORE: &'static str = "_";
    pub const PLUS: &'static str = "+";
    pub const PIPE: &'static str = "|";
    pub const TILDE: &'static str = "~";
    pub const LEFTBRACE: &'static str = "{";
    pub const RIGHTBRACE: &'static str = "}";
    pub const COLON: &'static str = ":";
    pub const LESSTHAN: &'static str = "<";
    pub const GREATERTHAN: &'static str = ">";
    pub const QUESTION: &'static str = "?";

    pub fn ctrl(key: &str) -> String { format!("ctrl+{key}") }
    pub fn shift(key: &str) -> String { format!("shift+{key}") }
    pub fn alt(key: &str) -> String { format!("alt+{key}") }
    pub fn super_(key: &str) -> String { format!("super+{key}") }
    pub fn ctrl_shift(key: &str) -> String { format!("ctrl+shift+{key}") }
    pub fn shift_ctrl(key: &str) -> String { format!("shift+ctrl+{key}") }
    pub fn ctrl_alt(key: &str) -> String { format!("ctrl+alt+{key}") }
    pub fn alt_ctrl(key: &str) -> String { format!("alt+ctrl+{key}") }
    pub fn shift_alt(key: &str) -> String { format!("shift+alt+{key}") }
    pub fn alt_shift(key: &str) -> String { format!("alt+shift+{key}") }
    pub fn ctrl_super(key: &str) -> String { format!("ctrl+super+{key}") }
    pub fn super_ctrl(key: &str) -> String { format!("super+ctrl+{key}") }
    pub fn shift_super(key: &str) -> String { format!("shift+super+{key}") }
    pub fn super_shift(key: &str) -> String { format!("super+shift+{key}") }
    pub fn alt_super(key: &str) -> String { format!("alt+super+{key}") }
    pub fn super_alt(key: &str) -> String { format!("super+alt+{key}") }
    pub fn ctrl_shift_alt(key: &str) -> String { format!("ctrl+shift+alt+{key}") }
    pub fn ctrl_shift_super(key: &str) -> String { format!("ctrl+shift+super+{key}") }
}

const SYMBOL_KEYS: &str = "`-=[]\\;',./!@#$%^&*()_+|~{}:<>?";
const SHIFTED_SYMBOL_KEYS: &str = "~!@#$%^&*()_+|{}:<>?";
const MODIFIER_NAMES: [&str; 4] = ["shift", "alt", "ctrl", "super"];
const SHIFT: i32 = 1;
const ALT: i32 = 2;
const CTRL: i32 = 4;
const SUPER: i32 = 8;
const LOCK_MASK: i32 = 64 + 128;

const ESCAPE: f64 = 27.0;
const TAB: f64 = 9.0;
const ENTER: f64 = 13.0;
const SPACE: f64 = 32.0;
const BACKSPACE: f64 = 127.0;
const KP_ENTER: f64 = 57414.0;
const UP: f64 = -1.0;
const DOWN: f64 = -2.0;
const RIGHT: f64 = -3.0;
const LEFT: f64 = -4.0;
const DELETE: f64 = -10.0;
const INSERT: f64 = -11.0;
const PAGE_UP: f64 = -12.0;
const PAGE_DOWN: f64 = -13.0;
const HOME: f64 = -14.0;
const END: f64 = -15.0;

fn functional_equivalent(codepoint: f64) -> f64 {
    let mapped = match codepoint as i64 {
        _ if codepoint.fract() != 0.0 || !codepoint.is_finite() => return codepoint,
        57399..=57408 => codepoint - 57399.0 + 48.0,
        57409 => 46.0,
        57410 => 47.0,
        57411 => 42.0,
        57412 => 45.0,
        57413 => 43.0,
        57415 => 61.0,
        57416 => 44.0,
        57417 => LEFT,
        57418 => RIGHT,
        57419 => UP,
        57420 => DOWN,
        57421 => PAGE_UP,
        57422 => PAGE_DOWN,
        57423 => HOME,
        57424 => END,
        57425 => INSERT,
        57426 => DELETE,
        _ => codepoint,
    };
    mapped
}

fn legacy_key_sequences(name: &str) -> &'static [&'static str] {
    match name {
        "up" => &["\x1b[A", "\x1bOA"],
        "down" => &["\x1b[B", "\x1bOB"],
        "right" => &["\x1b[C", "\x1bOC"],
        "left" => &["\x1b[D", "\x1bOD"],
        "home" => &["\x1b[H", "\x1bOH", "\x1b[1~", "\x1b[7~"],
        "end" => &["\x1b[F", "\x1bOF", "\x1b[4~", "\x1b[8~"],
        "insert" => &["\x1b[2~"],
        "delete" => &["\x1b[3~"],
        "pageUp" => &["\x1b[5~", "\x1b[[5~"],
        "pageDown" => &["\x1b[6~", "\x1b[[6~"],
        "clear" => &["\x1b[E", "\x1bOE"],
        "f1" => &["\x1bOP", "\x1b[11~", "\x1b[[A"],
        "f2" => &["\x1bOQ", "\x1b[12~", "\x1b[[B"],
        "f3" => &["\x1bOR", "\x1b[13~", "\x1b[[C"],
        "f4" => &["\x1bOS", "\x1b[14~", "\x1b[[D"],
        "f5" => &["\x1b[15~", "\x1b[[E"],
        "f6" => &["\x1b[17~"],
        "f7" => &["\x1b[18~"],
        "f8" => &["\x1b[19~"],
        "f9" => &["\x1b[20~"],
        "f10" => &["\x1b[21~"],
        "f11" => &["\x1b[23~"],
        "f12" => &["\x1b[24~"],
        _ => &[],
    }
}

fn legacy_shift_sequences(name: &str) -> &'static [&'static str] {
    match name {
        "up" => &["\x1b[a"],
        "down" => &["\x1b[b"],
        "right" => &["\x1b[c"],
        "left" => &["\x1b[d"],
        "clear" => &["\x1b[e"],
        "insert" => &["\x1b[2$"],
        "delete" => &["\x1b[3$"],
        "pageUp" => &["\x1b[5$"],
        "pageDown" => &["\x1b[6$"],
        "home" => &["\x1b[7$"],
        "end" => &["\x1b[8$"],
        _ => &[],
    }
}

fn legacy_ctrl_sequences(name: &str) -> &'static [&'static str] {
    match name {
        "up" => &["\x1bOa"],
        "down" => &["\x1bOb"],
        "right" => &["\x1bOc"],
        "left" => &["\x1bOd"],
        "clear" => &["\x1bOe"],
        "insert" => &["\x1b[2^"],
        "delete" => &["\x1b[3^"],
        "pageUp" => &["\x1b[5^"],
        "pageDown" => &["\x1b[6^"],
        "home" => &["\x1b[7^"],
        "end" => &["\x1b[8^"],
        _ => &[],
    }
}

fn legacy_sequence_key_id(data: &str) -> Option<&'static str> {
    Some(match data {
        "\x1bOA" => "up",
        "\x1bOB" => "down",
        "\x1bOC" => "right",
        "\x1bOD" => "left",
        "\x1bOH" => "home",
        "\x1bOF" => "end",
        "\x1b[E" | "\x1bOE" => "clear",
        "\x1bOe" => "ctrl+clear",
        "\x1b[e" => "shift+clear",
        "\x1b[2~" => "insert",
        "\x1b[2$" => "shift+insert",
        "\x1b[2^" => "ctrl+insert",
        "\x1b[3$" => "shift+delete",
        "\x1b[3^" => "ctrl+delete",
        "\x1b[[5~" => "pageUp",
        "\x1b[[6~" => "pageDown",
        "\x1b[a" => "shift+up",
        "\x1b[b" => "shift+down",
        "\x1b[c" => "shift+right",
        "\x1b[d" => "shift+left",
        "\x1bOa" => "ctrl+up",
        "\x1bOb" => "ctrl+down",
        "\x1bOc" => "ctrl+right",
        "\x1bOd" => "ctrl+left",
        "\x1b[5$" => "shift+pageUp",
        "\x1b[6$" => "shift+pageDown",
        "\x1b[7$" => "shift+home",
        "\x1b[8$" => "shift+end",
        "\x1b[5^" => "ctrl+pageUp",
        "\x1b[6^" => "ctrl+pageDown",
        "\x1b[7^" => "ctrl+home",
        "\x1b[8^" => "ctrl+end",
        "\x1bOP" | "\x1b[11~" | "\x1b[[A" => "f1",
        "\x1bOQ" | "\x1b[12~" | "\x1b[[B" => "f2",
        "\x1bOR" | "\x1b[13~" | "\x1b[[C" => "f3",
        "\x1bOS" | "\x1b[14~" | "\x1b[[D" => "f4",
        "\x1b[[E" | "\x1b[15~" => "f5",
        "\x1b[17~" => "f6",
        "\x1b[18~" => "f7",
        "\x1b[19~" => "f8",
        "\x1b[20~" => "f9",
        "\x1b[21~" => "f10",
        "\x1b[23~" => "f11",
        "\x1b[24~" => "f12",
        "\x1bb" => "alt+left",
        "\x1bf" => "alt+right",
        "\x1bp" => "alt+up",
        "\x1bn" => "alt+down",
        _ => return None,
    })
}

/// JavaScript `ToInt32`.
fn i32_of(value: f64) -> i32 {
    if !value.is_finite() {
        return 0;
    }
    (value.trunc().rem_euclid(4294967296.0) as u64 as u32) as i32
}

fn from_char_code(value: f64) -> char {
    char::from_u32((i32_of(value) as u32) & 0xFFFF).unwrap_or('\u{fffd}')
}

fn is_symbol(value: f64) -> bool {
    let c = (i32_of(value) as u32) & 0xFFFF;
    char::from_u32(c).is_some_and(|c| SYMBOL_KEYS.contains(c))
}

fn normalize_functional(codepoint: f64) -> f64 {
    functional_equivalent(codepoint)
}

fn normalize_shifted_letter(codepoint: f64, modifier: f64) -> f64 {
    if i32_of(modifier) & !LOCK_MASK & SHIFT != 0 && (65.0..=90.0).contains(&codepoint) { codepoint + 32.0 } else { codepoint }
}

/// `getLogicalShiftedSymbolCandidate`: `(codepoint, modifier)`.
fn logical_shifted_symbol(codepoint: Option<f64>, modifier: f64, shifted_only: bool) -> Option<(f64, i32)> {
    let codepoint = codepoint?;
    if i32_of(modifier) & SHIFT == 0 {
        return None;
    }
    let normalized = normalize_functional(codepoint);
    let c = from_char_code(normalized);
    let allowed = if shifted_only { SHIFTED_SYMBOL_KEYS } else { SYMBOL_KEYS };
    if !allowed.contains(c) {
        return None;
    }
    Some((normalized, i32_of(modifier) & !SHIFT))
}

/// `Number.parseInt(digits, 10)` over a run of ASCII digits: a float, infinite when it overflows.
fn num(digits: &str) -> f64 {
    digits.parse::<f64>().unwrap_or(f64::INFINITY)
}

fn digits_at(s: &[u8], i: &mut usize) -> Option<(usize, usize)> {
    let start = *i;
    while *i < s.len() && s[*i].is_ascii_digit() {
        *i += 1;
    }
    (*i > start).then_some((start, *i))
}

struct Kitty {
    codepoint: f64,
    shifted: Option<f64>,
    base: Option<f64>,
    modifier: f64,
}

/// `/^\x1b\[(\d+)(?::(\d*))?(?::(\d+))?(?:;(\d+))?(?::(\d+))?u$/`: groups 1-4 as text (`""` is an absent or empty group).
fn parse_csi_u(data: &str) -> Option<[&str; 4]> {
    let b = data.as_bytes();
    if !data.starts_with("\x1b[") || !data.ends_with('u') {
        return None;
    }
    let body_end = b.len() - 1;
    let mut i = 2;
    let (s1, e1) = digits_at(b, &mut i)?;
    let mut groups = [&data[s1..e1], "", "", ""];
    // The regex backtracks over its optional groups; the sequence is short, so try each combination in order.
    let rest = &data[i..body_end];
    for with_g2 in [true, false] {
        for with_g3 in [true, false] {
            for with_g4 in [true, false] {
                for with_g5 in [true, false] {
                    let mut p = 0usize;
                    let r = rest.as_bytes();
                    let mut g = ["", "", "", ""];
                    let mut ok = true;
                    if with_g2 {
                        if r.get(p) == Some(&b':') {
                            p += 1;
                            let s = p;
                            while p < r.len() && r[p].is_ascii_digit() { p += 1; }
                            g[1] = &rest[s..p];
                        } else {
                            ok = false;
                        }
                    }
                    if ok && with_g3 {
                        if r.get(p) == Some(&b':') {
                            p += 1;
                            let s = p;
                            while p < r.len() && r[p].is_ascii_digit() { p += 1; }
                            if p == s { ok = false } else { g[2] = &rest[s..p]; }
                        } else {
                            ok = false;
                        }
                    }
                    if ok && with_g4 {
                        if r.get(p) == Some(&b';') {
                            p += 1;
                            let s = p;
                            while p < r.len() && r[p].is_ascii_digit() { p += 1; }
                            if p == s { ok = false } else { g[3] = &rest[s..p]; }
                        } else {
                            ok = false;
                        }
                    }
                    if ok && with_g5 {
                        if r.get(p) == Some(&b':') {
                            p += 1;
                            let s = p;
                            while p < r.len() && r[p].is_ascii_digit() { p += 1; }
                            if p == s { ok = false }
                        } else {
                            ok = false;
                        }
                    }
                    if ok && p == r.len() {
                        groups[1] = g[1];
                        groups[2] = g[2];
                        groups[3] = g[3];
                        return Some(groups);
                    }
                }
            }
        }
    }
    None
}

fn parse_kitty(data: &str) -> Option<Kitty> {
    if let Some(g) = parse_csi_u(data) {
        return Some(Kitty {
            codepoint: num(g[0]),
            shifted: (!g[1].is_empty()).then(|| num(g[1])),
            base: (!g[2].is_empty()).then(|| num(g[2])),
            modifier: if g[3].is_empty() { 1.0 } else { num(g[3]) } - 1.0,
        });
    }
    // Arrow, functional and home/end forms: \x1b[1;<mod>[:<event>]<A-D|H|F> and \x1b[<n>[;<mod>][:<event>]~
    let b = data.as_bytes();
    if !data.starts_with("\x1b[") || b.len() < 4 {
        return None;
    }
    let last = *b.last().unwrap();
    let body = &data[2..data.len() - 1];
    let mut it = body.splitn(2, ';');
    let first = it.next().unwrap_or("");
    let tail = it.next();
    let all_digits = |s: &str| !s.is_empty() && s.bytes().all(|c| c.is_ascii_digit());
    let mod_event = |s: &str| -> Option<f64> {
        let (m, e) = match s.split_once(':') {
            Some((m, e)) => (m, Some(e)),
            None => (s, None),
        };
        if !all_digits(m) || e.is_some_and(|e| !all_digits(e)) {
            return None;
        }
        Some(num(m))
    };
    match last {
        b'A' | b'B' | b'C' | b'D' | b'H' | b'F' if first == "1" => {
            let m = mod_event(tail?)?;
            let codepoint = match last {
                b'A' => UP,
                b'B' => DOWN,
                b'C' => RIGHT,
                b'D' => LEFT,
                b'H' => HOME,
                _ => END,
            };
            Some(Kitty { codepoint, shifted: None, base: None, modifier: m - 1.0 })
        }
        b'~' if all_digits(first) => {
            // \x1b[<num>~ | \x1b[<num>;<mod>~ | \x1b[<num>;<mod>:<event>~ | \x1b[<num>:<event>~
            let (n_text, direct_event) = (first, None::<&str>);
            let _ = direct_event;
            let (n_str, mod_value) = match tail {
                Some(t) => (n_text, Some(mod_event(t)?)),
                None => {
                    // The regex allows `\x1b[5:3~`: digits, no modifier, then ":<event>".
                    (n_text, None)
                }
            };
            let codepoint = match num(n_str) as i64 {
                2 => INSERT,
                3 => DELETE,
                5 => PAGE_UP,
                6 => PAGE_DOWN,
                7 => HOME,
                8 => END,
                _ => return None,
            };
            Some(Kitty { codepoint, shifted: None, base: None, modifier: mod_value.unwrap_or(1.0) - 1.0 })
        }
        b'~' => {
            // `\x1b[5:3~`: the number then an event with no modifier group.
            let (n, e) = first.split_once(':')?;
            if !all_digits(n) || !all_digits(e) || tail.is_some() {
                return None;
            }
            let codepoint = match num(n) as i64 {
                2 => INSERT,
                3 => DELETE,
                5 => PAGE_UP,
                6 => PAGE_DOWN,
                7 => HOME,
                8 => END,
                _ => return None,
            };
            Some(Kitty { codepoint, shifted: None, base: None, modifier: 0.0 })
        }
        _ => None,
    }
}

fn matches_kitty(data: &str, expected_codepoint: f64, expected_modifier: i32) -> bool {
    let Some(parsed) = parse_kitty(data) else { return false };
    let actual_mod = i32_of(parsed.modifier) & !LOCK_MASK;
    let expected_mod = expected_modifier & !LOCK_MASK;
    let normalized = normalize_shifted_letter(normalize_functional(parsed.codepoint), parsed.modifier);
    let normalized_expected = normalize_shifted_letter(normalize_functional(expected_codepoint), expected_modifier as f64);
    if actual_mod == expected_mod && normalized == normalized_expected {
        return true;
    }
    if let Some((cp, m)) = logical_shifted_symbol(parsed.shifted, actual_mod as f64, false) {
        if (m & !LOCK_MASK) == expected_mod && cp == normalize_functional(expected_codepoint) {
            return true;
        }
    }
    if actual_mod != expected_mod {
        return false;
    }
    if let Some(base) = parsed.base {
        if base == expected_codepoint {
            let is_latin_letter = (97.0..=122.0).contains(&normalized);
            if !is_latin_letter && !is_symbol(normalized) {
                return true;
            }
        }
    }
    false
}

fn parse_modify_other(data: &str) -> Option<(f64, f64)> {
    let rest = data.strip_prefix("\x1b[27;")?.strip_suffix('~')?;
    let (m, c) = rest.split_once(';')?;
    let digits = |s: &str| !s.is_empty() && s.bytes().all(|b| b.is_ascii_digit());
    if !digits(m) || !digits(c) {
        return None;
    }
    Some((num(c), num(m) - 1.0))
}

fn matches_modify_other(data: &str, expected_keycode: f64, expected_modifier: i32) -> bool {
    parse_modify_other(data).is_some_and(|(cp, m)| cp == expected_keycode && m == expected_modifier as f64)
}

fn matches_printable_modify_other(data: &str, expected_keycode: f64, expected_modifier: i32) -> bool {
    let Some((codepoint, modifier)) = parse_modify_other(data) else { return false };
    if modifier == expected_modifier as f64
        && normalize_shifted_letter(codepoint, modifier) == normalize_shifted_letter(expected_keycode, expected_modifier as f64)
    {
        return true;
    }
    logical_shifted_symbol(Some(codepoint), modifier, true).is_some_and(|(cp, m)| m == expected_modifier && cp == expected_keycode)
}

fn is_windows_terminal_session() -> bool {
    let set = |name: &str| std::env::var(name).is_ok_and(|v| !v.is_empty());
    set("WT_SESSION") && !set("SSH_CONNECTION") && !set("SSH_CLIENT") && !set("SSH_TTY")
}

fn matches_raw_backspace(data: &str, expected_modifier: i32) -> bool {
    if data == "\x7f" {
        return expected_modifier == 0;
    }
    if data != "\x08" {
        return false;
    }
    if is_windows_terminal_session() { expected_modifier == CTRL } else { expected_modifier == 0 }
}

fn raw_ctrl_char(key: char) -> Option<char> {
    let lower = key.to_lowercase().next().unwrap_or(key);
    let code = lower as u32;
    if (97..=122).contains(&code) || matches!(lower, '[' | '\\' | ']' | '_') {
        return char::from_u32(code & 0x1F);
    }
    if lower == '-' { char::from_u32(31) } else { None }
}

fn legacy_modifier(data: &str, key: &str, modifier: i32) -> bool {
    if modifier == SHIFT {
        return legacy_shift_sequences(key).contains(&data);
    }
    if modifier == CTRL {
        return legacy_ctrl_sequences(key).contains(&data);
    }
    false
}

fn parse_key_id(key_id: &str) -> Option<(String, [bool; 4])> {
    let mut key = key_id.to_lowercase();
    let mut present = [false; 4];
    loop {
        let found = MODIFIER_NAMES.iter().position(|name| key.starts_with(&format!("{name}+")));
        let Some(index) = found else { break };
        if present[index] {
            return None;
        }
        present[index] = true;
        key = key[MODIFIER_NAMES[index].len() + 1..].to_string();
    }
    if key.is_empty() || (key.contains('+') && key != "+") {
        return None;
    }
    Some((key, present))
}

/// pi-tui's `matchesKey`: whether raw terminal `data` is the key `key_id`.
pub fn matches_key(data: &str, key_id: &str) -> bool {
    let Some((key, present)) = parse_key_id(key_id) else { return false };
    let mut modifier = 0;
    if present[0] {
        modifier |= SHIFT;
    }
    if present[1] {
        modifier |= ALT;
    }
    if present[2] {
        modifier |= CTRL;
    }
    if present[3] {
        modifier |= SUPER;
    }
    let kitty_active = is_kitty_protocol_active();
    let key = key.as_str();
    match key {
        "escape" | "esc" => {
            if modifier != 0 {
                return false;
            }
            return data == "\x1b" || matches_kitty(data, ESCAPE, 0) || matches_modify_other(data, ESCAPE, 0);
        }
        "space" => {
            if !kitty_active {
                if modifier == CTRL && data == "\x00" {
                    return true;
                }
                if modifier == ALT && data == "\x1b " {
                    return true;
                }
            }
            if modifier == 0 {
                return data == " " || matches_kitty(data, SPACE, 0) || matches_modify_other(data, SPACE, 0);
            }
            return matches_kitty(data, SPACE, modifier) || matches_modify_other(data, SPACE, modifier);
        }
        "tab" => {
            if modifier == SHIFT {
                return data == "\x1b[Z" || matches_kitty(data, TAB, SHIFT) || matches_modify_other(data, TAB, SHIFT);
            }
            if modifier == 0 {
                return data == "\t" || matches_kitty(data, TAB, 0);
            }
            return matches_kitty(data, TAB, modifier) || matches_modify_other(data, TAB, modifier);
        }
        "enter" | "return" => {
            if modifier == SHIFT {
                if matches_kitty(data, ENTER, SHIFT) || matches_kitty(data, KP_ENTER, SHIFT) || matches_modify_other(data, ENTER, SHIFT) {
                    return true;
                }
                return kitty_active && (data == "\x1b\r" || data == "\n");
            }
            if modifier == ALT {
                if matches_kitty(data, ENTER, ALT) || matches_kitty(data, KP_ENTER, ALT) || matches_modify_other(data, ENTER, ALT) {
                    return true;
                }
                return !kitty_active && data == "\x1b\r";
            }
            if modifier == 0 {
                return data == "\r"
                    || (!kitty_active && data == "\n")
                    || data == "\x1bOM"
                    || matches_kitty(data, ENTER, 0)
                    || matches_kitty(data, KP_ENTER, 0);
            }
            return matches_kitty(data, ENTER, modifier) || matches_kitty(data, KP_ENTER, modifier) || matches_modify_other(data, ENTER, modifier);
        }
        "backspace" => {
            if modifier == ALT {
                if data == "\x1b\x7f" || data == "\x1b\x08" {
                    return true;
                }
                return matches_kitty(data, BACKSPACE, ALT) || matches_modify_other(data, BACKSPACE, ALT);
            }
            if modifier == CTRL {
                if matches_raw_backspace(data, CTRL) {
                    return true;
                }
                return matches_kitty(data, BACKSPACE, CTRL) || matches_modify_other(data, BACKSPACE, CTRL);
            }
            if modifier == 0 {
                return matches_raw_backspace(data, 0) || matches_kitty(data, BACKSPACE, 0) || matches_modify_other(data, BACKSPACE, 0);
            }
            return matches_kitty(data, BACKSPACE, modifier) || matches_modify_other(data, BACKSPACE, modifier);
        }
        "insert" | "delete" | "home" | "end" | "pageup" | "pagedown" => {
            let (name, code) = match key {
                "insert" => ("insert", INSERT),
                "delete" => ("delete", DELETE),
                "home" => ("home", HOME),
                "end" => ("end", END),
                "pageup" => ("pageUp", PAGE_UP),
                _ => ("pageDown", PAGE_DOWN),
            };
            if modifier == 0 {
                return legacy_key_sequences(name).contains(&data) || matches_kitty(data, code, 0);
            }
            if legacy_modifier(data, name, modifier) {
                return true;
            }
            return matches_kitty(data, code, modifier);
        }
        "clear" => {
            if modifier == 0 {
                return legacy_key_sequences("clear").contains(&data);
            }
            return legacy_modifier(data, "clear", modifier);
        }
        "up" | "down" | "left" | "right" => {
            let code = match key {
                "up" => UP,
                "down" => DOWN,
                "left" => LEFT,
                _ => RIGHT,
            };
            if modifier == ALT {
                return match key {
                    "up" => data == "\x1bp" || matches_kitty(data, code, ALT),
                    "down" => data == "\x1bn" || matches_kitty(data, code, ALT),
                    "left" => data == "\x1b[1;3D" || (!kitty_active && data == "\x1bB") || data == "\x1bb" || matches_kitty(data, code, ALT),
                    _ => data == "\x1b[1;3C" || (!kitty_active && data == "\x1bF") || data == "\x1bf" || matches_kitty(data, code, ALT),
                };
            }
            if modifier == CTRL && (key == "left" || key == "right") {
                let csi = if key == "left" { "\x1b[1;5D" } else { "\x1b[1;5C" };
                return data == csi || legacy_modifier(data, key, CTRL) || matches_kitty(data, code, CTRL);
            }
            if modifier == 0 {
                return legacy_key_sequences(key).contains(&data) || matches_kitty(data, code, 0);
            }
            if legacy_modifier(data, key, modifier) {
                return true;
            }
            return matches_kitty(data, code, modifier);
        }
        "f1" | "f2" | "f3" | "f4" | "f5" | "f6" | "f7" | "f8" | "f9" | "f10" | "f11" | "f12" => {
            if modifier != 0 {
                return false;
            }
            return legacy_key_sequences(key).contains(&data);
        }
        _ => {}
    }
    let mut chars = key.chars();
    let (Some(ch), None) = (chars.next(), chars.next()) else { return false };
    if !(ch.is_ascii_lowercase() || ch.is_ascii_digit() || SYMBOL_KEYS.contains(ch)) {
        return false;
    }
    let codepoint = ch as u32 as f64;
    let raw_ctrl = raw_ctrl_char(ch);
    let is_letter = ch.is_ascii_lowercase();
    let is_digit = ch.is_ascii_digit();
    if modifier == CTRL + ALT && !kitty_active {
        if let Some(rc) = raw_ctrl {
            if data == format!("\x1b{rc}") {
                return true;
            }
        }
    }
    if modifier == ALT && !kitty_active && (is_letter || is_digit || SYMBOL_KEYS.contains(ch)) && data == format!("\x1b{ch}") {
        return true;
    }
    if modifier == CTRL {
        if let Some(rc) = raw_ctrl {
            if data == rc.to_string() {
                return true;
            }
        }
        return matches_kitty(data, codepoint, CTRL) || matches_printable_modify_other(data, codepoint, CTRL);
    }
    if modifier == SHIFT + CTRL {
        return matches_kitty(data, codepoint, SHIFT + CTRL) || matches_printable_modify_other(data, codepoint, SHIFT + CTRL);
    }
    if modifier == SHIFT {
        if is_letter && data == ch.to_ascii_uppercase().to_string() {
            return true;
        }
        return matches_kitty(data, codepoint, SHIFT) || matches_printable_modify_other(data, codepoint, SHIFT);
    }
    if modifier != 0 {
        return matches_kitty(data, codepoint, modifier) || matches_printable_modify_other(data, codepoint, modifier);
    }
    data == key || matches_kitty(data, codepoint, 0) || matches_printable_modify_other(data, codepoint, 0)
}

/// pi-tui's `isKeyRelease`: whether `data` is a Kitty key-release event.
pub fn is_key_release(data: &str) -> bool {
    if data.contains("\x1b[200~") {
        return false;
    }
    [":3u", ":3~", ":3A", ":3B", ":3C", ":3D", ":3H", ":3F"].iter().any(|m| data.contains(m))
}

/// pi-tui's `isKeyRepeat`: whether `data` is a Kitty key-repeat event.
pub fn is_key_repeat(data: &str) -> bool {
    if data.contains("\x1b[200~") {
        return false;
    }
    [":2u", ":2~", ":2A", ":2B", ":2C", ":2D", ":2H", ":2F"].iter().any(|m| data.contains(m))
}

fn format_key_name(key_name: &str, modifier: f64) -> Option<String> {
    let effective = i32_of(modifier) & !LOCK_MASK;
    if effective & !(SHIFT | CTRL | ALT | SUPER) != 0 {
        return None;
    }
    let mut mods: Vec<&str> = Vec::new();
    if effective & SHIFT != 0 {
        mods.push("shift");
    }
    if effective & CTRL != 0 {
        mods.push("ctrl");
    }
    if effective & ALT != 0 {
        mods.push("alt");
    }
    if effective & SUPER != 0 {
        mods.push("super");
    }
    Some(if mods.is_empty() { key_name.to_string() } else { format!("{}+{key_name}", mods.join("+")) })
}

fn format_parsed_key(codepoint: f64, modifier: f64, base_layout_key: Option<f64>) -> Option<String> {
    let normalized = normalize_functional(codepoint);
    let identity = normalize_shifted_letter(normalized, modifier);
    let is_latin_letter = (97.0..=122.0).contains(&identity);
    let is_digit = (48.0..=57.0).contains(&identity);
    let effective = if is_latin_letter || is_digit || is_symbol(identity) { identity } else { base_layout_key.unwrap_or(identity) };
    let named = match effective {
        x if x == ESCAPE => Some("escape"),
        x if x == TAB => Some("tab"),
        x if x == ENTER || x == KP_ENTER => Some("enter"),
        x if x == SPACE => Some("space"),
        x if x == BACKSPACE => Some("backspace"),
        x if x == DELETE => Some("delete"),
        x if x == INSERT => Some("insert"),
        x if x == HOME => Some("home"),
        x if x == END => Some("end"),
        x if x == PAGE_UP => Some("pageUp"),
        x if x == PAGE_DOWN => Some("pageDown"),
        x if x == UP => Some("up"),
        x if x == DOWN => Some("down"),
        x if x == LEFT => Some("left"),
        x if x == RIGHT => Some("right"),
        _ => None,
    };
    let key_name = match named {
        Some(name) => name.to_string(),
        None => {
            if (48.0..=57.0).contains(&effective) || (97.0..=122.0).contains(&effective) || is_symbol(effective) {
                from_char_code(effective).to_string()
            } else {
                return None;
            }
        }
    };
    format_key_name(&key_name, modifier)
}

/// pi-tui's `parseKey`: the key identifier raw terminal `data` stands for, or `None`.
pub fn parse_key(data: &str) -> Option<String> {
    if let Some(k) = parse_kitty(data) {
        let logical = logical_shifted_symbol(k.shifted, k.modifier, false);
        return format_parsed_key(logical.map_or(k.codepoint, |l| l.0), logical.map_or(k.modifier, |l| l.1 as f64), k.base);
    }
    if let Some((codepoint, modifier)) = parse_modify_other(data) {
        let logical = logical_shifted_symbol(Some(codepoint), modifier, true);
        return format_parsed_key(logical.map_or(codepoint, |l| l.0), logical.map_or(modifier, |l| l.1 as f64), None);
    }
    let active = is_kitty_protocol_active();
    if active && (data == "\x1b\r" || data == "\n") {
        return Some("shift+enter".into());
    }
    if let Some(id) = legacy_sequence_key_id(data) {
        return Some(id.into());
    }
    let simple = match data {
        "\x1b" => Some("escape"),
        "\x1c" => Some("ctrl+\\"),
        "\x1d" => Some("ctrl+]"),
        "\x1f" => Some("ctrl+-"),
        "\x1b\x1b" => Some("ctrl+alt+["),
        "\x1b\x1c" => Some("ctrl+alt+\\"),
        "\x1b\x1d" => Some("ctrl+alt+]"),
        "\x1b\x1f" => Some("ctrl+alt+-"),
        "\t" => Some("tab"),
        "\r" | "\x1bOM" => Some("enter"),
        "\x00" => Some("ctrl+space"),
        " " => Some("space"),
        "\x7f" => Some("backspace"),
        "\x1b[Z" => Some("shift+tab"),
        "\x1b\x7f" | "\x1b\x08" => Some("alt+backspace"),
        _ => None,
    };
    if let Some(s) = simple {
        return Some(s.into());
    }
    if !active && data == "\n" {
        return Some("enter".into());
    }
    if data == "\x08" {
        return Some(if is_windows_terminal_session() { "ctrl+backspace" } else { "backspace" }.into());
    }
    if !active && data == "\x1b\r" {
        return Some("alt+enter".into());
    }
    if !active && data == "\x1b " {
        return Some("alt+space".into());
    }
    if !active && data == "\x1bB" {
        return Some("alt+left".into());
    }
    if !active && data == "\x1bF" {
        return Some("alt+right".into());
    }
    let chars: Vec<char> = data.chars().collect();
    if !active && data.encode_utf16().count() == 2 && chars.len() == 2 && chars[0] == '\x1b' {
        let code = chars[1] as u32;
        if (1..=26).contains(&code) {
            return Some(format!("ctrl+alt+{}", char::from_u32(code + 96).unwrap()));
        }
        let key = chars[1];
        if (97..=122).contains(&code) || (48..=57).contains(&code) || SYMBOL_KEYS.contains(key) {
            return Some(format!("alt+{key}"));
        }
    }
    let rest = match data {
        "\x1b[A" => Some("up"),
        "\x1b[B" => Some("down"),
        "\x1b[C" => Some("right"),
        "\x1b[D" => Some("left"),
        "\x1b[H" | "\x1bOH" => Some("home"),
        "\x1b[F" | "\x1bOF" => Some("end"),
        "\x1b[3~" => Some("delete"),
        "\x1b[5~" => Some("pageUp"),
        "\x1b[6~" => Some("pageDown"),
        _ => None,
    };
    if let Some(s) = rest {
        return Some(s.into());
    }
    if chars.len() == 1 && data.len() == 1 {
        let code = chars[0] as u32;
        if (1..=26).contains(&code) {
            return Some(format!("ctrl+{}", char::from_u32(code + 96).unwrap()));
        }
        if (32..=126).contains(&code) {
            return Some(data.to_string());
        }
    }
    None
}

fn from_code_point(value: f64) -> Option<String> {
    if !value.is_finite() || value.fract() != 0.0 || !(0.0..=1114111.0).contains(&value) {
        return None;
    }
    char::from_u32(value as u32).map(|c| c.to_string())
}

/// pi-tui's `decodeKittyPrintable`: the character of a plain or shifted Kitty CSI-u key event.
pub fn decode_kitty_printable(data: &str) -> Option<String> {
    let g = parse_csi_u(data)?;
    let codepoint = num(g[0]);
    let shifted = (!g[1].is_empty()).then(|| num(g[1]));
    let mod_value = if g[3].is_empty() { 1.0 } else { num(g[3]) };
    let modifier = if mod_value.is_finite() { i32_of(mod_value - 1.0) } else { 0 };
    if modifier & !(SHIFT | LOCK_MASK) != 0 || modifier & (ALT | CTRL) != 0 {
        return None;
    }
    let mut effective = codepoint;
    if modifier & SHIFT != 0 {
        if let Some(s) = shifted {
            effective = s;
        }
    }
    effective = normalize_functional(effective);
    if !effective.is_finite() || effective < 32.0 {
        return None;
    }
    from_code_point(effective)
}

fn decode_modify_other_printable(data: &str) -> Option<String> {
    let (codepoint, modifier) = parse_modify_other(data)?;
    if i32_of(modifier) & !LOCK_MASK & !SHIFT != 0 {
        return None;
    }
    if !codepoint.is_finite() || codepoint < 32.0 {
        return None;
    }
    from_code_point(codepoint)
}

/// pi-tui's `decodePrintableKey`: [`decode_kitty_printable`], or the character of a modifyOtherKeys event.
pub fn decode_printable_key(data: &str) -> Option<String> {
    decode_kitty_printable(data).or_else(|| decode_modify_other_printable(data))
}

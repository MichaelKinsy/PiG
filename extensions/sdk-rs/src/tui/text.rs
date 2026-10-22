//! pi-tui's `utils.ts`: terminal width, grapheme, ANSI, wrap, truncate and slice helpers.
//!
//! A port of the pinned Pi release's `utils.ts`. The Unicode data comes from `tables` (generated from the sources Pi's runtime reads:
//! get-east-asian-width, ICU property escapes, `Intl.Segmenter` grapheme rules). `test/parity/sdk-bulk` holds the results of Pi's own
//! functions, and `tests/tui_vectors.rs` holds this module to them.

use super::tables::{PROP_RANGES, RGI_SEQUENCES};

/// pi-tui's `CURSOR_MARKER`: a zero-width APC sequence a focused component emits at its cursor position.
pub const CURSOR_MARKER: &str = "\x1b_pi:c\x07";

const W2: u32 = 1 << 0;
const ZW: u32 = 1 << 1;
const NP: u32 = 1 << 2;
const MARK: u32 = 1 << 3;
const TSM: u32 = 1 << 4;
const RGI1: u32 = 1 << 5;
const EXT_PICT: u32 = 1 << 6;
const CJK: u32 = 1 << 7;
const JS_SPACE: u32 = 1 << 14;

const GB_CR: u32 = 1;
const GB_LF: u32 = 2;
const GB_CONTROL: u32 = 3;
const GB_EXTEND: u32 = 4;
const GB_ZWJ: u32 = 5;
const GB_RI: u32 = 6;
const GB_PREPEND: u32 = 7;
const GB_SPACING_MARK: u32 = 8;
const GB_L: u32 = 9;
const GB_V: u32 = 10;
const GB_T: u32 = 11;
const GB_LV: u32 = 12;
const GB_LVT: u32 = 13;
const INCB_CONSONANT: u32 = 1;
const INCB_EXTEND: u32 = 2;
const INCB_LINKER: u32 = 3;

fn props(c: char) -> u32 {
    let cp = c as u32;
    let i = PROP_RANGES.partition_point(|r| r.1 < cp);
    match PROP_RANGES.get(i) {
        Some(r) if r.0 <= cp => r.2,
        _ => 0,
    }
}

fn gcb(p: u32) -> u32 {
    (p >> 8) & 0xF
}

fn incb(p: u32) -> u32 {
    (p >> 12) & 0x3
}

fn east_asian_width(c: char) -> i64 {
    if props(c) & W2 != 0 { 2 } else { 1 }
}

pub(crate) fn is_js_space(c: char) -> bool {
    props(c) & JS_SPACE != 0
}

fn utf16_len(s: &[char]) -> usize {
    s.iter().map(|c| c.len_utf16()).sum()
}

fn trim_end_string(s: &str) -> String {
    s.trim_end_matches(is_js_space).to_string()
}

fn chars_to_string(s: &[char]) -> String {
    s.iter().collect()
}

/// Extended grapheme clusters as ICU's `Intl.Segmenter` yields them (UAX #29 with GB9c), as ranges into `text`.
fn graphemes(text: &[char]) -> Vec<&[char]> {
    let n = text.len();
    let mut out = Vec::new();
    let mut i = 0;
    while i < n {
        let start = i;
        let p = props(text[i]);
        let mut prev = gcb(p);
        let mut incb_state = u32::from(incb(p) == INCB_CONSONANT);
        let mut emoji_state = u32::from(p & EXT_PICT != 0);
        let mut ri_count = u32::from(prev == GB_RI);
        i += 1;
        while i < n {
            let q = props(text[i]);
            let cur = gcb(q);
            let brk = if prev == GB_CR && cur == GB_LF {
                false
            } else if prev == GB_CONTROL || prev == GB_CR || prev == GB_LF {
                true
            } else if cur == GB_CONTROL || cur == GB_CR || cur == GB_LF {
                true
            } else if prev == GB_L && matches!(cur, GB_L | GB_V | GB_LV | GB_LVT) {
                false
            } else if matches!(prev, GB_LV | GB_V) && matches!(cur, GB_V | GB_T) {
                false
            } else if matches!(prev, GB_LVT | GB_T) && cur == GB_T {
                false
            } else if cur == GB_EXTEND || cur == GB_ZWJ || cur == GB_SPACING_MARK || prev == GB_PREPEND {
                false
            } else if incb_state == 2 && incb(q) == INCB_CONSONANT {
                false
            } else if emoji_state == 2 && q & EXT_PICT != 0 {
                false
            } else {
                !(prev == GB_RI && cur == GB_RI && ri_count % 2 == 1)
            };
            if brk {
                break;
            }
            match incb(q) {
                INCB_CONSONANT => incb_state = 1,
                INCB_LINKER => {
                    if incb_state >= 1 {
                        incb_state = 2;
                    }
                }
                INCB_EXTEND => {}
                _ => incb_state = 0,
            }
            if q & EXT_PICT != 0 {
                emoji_state = 1;
            } else if cur == GB_EXTEND && emoji_state == 1 {
            } else if cur == GB_ZWJ && emoji_state == 1 {
                emoji_state = 2;
            } else {
                emoji_state = 0;
            }
            ri_count = if cur == GB_RI { ri_count + 1 } else { 0 };
            prev = cur;
            i += 1;
        }
        out.push(&text[start..i]);
    }
    out
}

fn could_be_emoji(segment: &[char]) -> bool {
    let cp = segment[0] as u32;
    (0x1F000..=0x1FBFF).contains(&cp)
        || (0x2300..=0x23FF).contains(&cp)
        || (0x2600..=0x27BF).contains(&cp)
        || (0x2B50..=0x2B55).contains(&cp)
        || segment.contains(&'\u{fe0f}')
        || utf16_len(segment) > 2
}

fn is_rgi_emoji(segment: &[char]) -> bool {
    if segment.len() == 1 {
        return props(segment[0]) & RGI1 != 0;
    }
    let text = chars_to_string(segment);
    RGI_SEQUENCES.binary_search(&text.as_str()).is_ok()
}

fn grapheme_width(segment: &[char]) -> i64 {
    if segment.len() == 1 {
        let code = segment[0] as u32;
        if (0x20..=0x7E).contains(&code) {
            return 1;
        }
        if code == 0x09 {
            return 3;
        }
    }
    let flags: Vec<u32> = segment.iter().map(|&c| props(c)).collect();
    if flags.iter().all(|f| f & TSM != 0) {
        return segment.len() as i64;
    }
    if flags.iter().all(|f| f & ZW != 0) {
        return 0;
    }
    if could_be_emoji(segment) && is_rgi_emoji(segment) {
        return 2;
    }
    let Some(start) = flags.iter().position(|f| f & NP == 0) else {
        return 0;
    };
    let cp = segment[start] as u32;
    if (0x1F1E6..=0x1F1FF).contains(&cp) {
        return 2;
    }
    let mut width = east_asian_width(segment[start]);
    let mut follows_mark = false;
    for k in start + 1..segment.len() {
        let c = segment[k];
        let f = flags[k];
        if f & TSM != 0 {
            width += 1;
            follows_mark = false;
        } else if f & MARK != 0 {
            follows_mark = true;
        } else if f & NP == 0 {
            if follows_mark || (0xFF00..=0xFFEF).contains(&(c as u32)) {
                width += east_asian_width(c);
            } else if c == '\u{0e33}' || c == '\u{0eb3}' {
                width += 1;
            }
            follows_mark = false;
        }
    }
    width
}

fn is_printable_ascii(s: &[char]) -> bool {
    s.iter().all(|&c| ('\x20'..='\x7e').contains(&c))
}

/// Length of the ANSI/OSC/APC escape sequence starting at `pos`, or 0 when there is none.
fn ansi_code_length(s: &[char], pos: usize) -> usize {
    if pos >= s.len() || s[pos] != '\x1b' {
        return 0;
    }
    match s.get(pos + 1) {
        Some('[') => {
            for j in pos + 2..s.len() {
                if matches!(s[j], 'm' | 'G' | 'K' | 'H' | 'J') {
                    return j + 1 - pos;
                }
            }
            0
        }
        Some(']') | Some('_') => {
            for j in pos + 2..s.len() {
                if s[j] == '\x07' {
                    return j + 1 - pos;
                }
                if s[j] == '\x1b' && s.get(j + 1) == Some(&'\\') {
                    return j + 2 - pos;
                }
            }
            0
        }
        _ => 0,
    }
}

fn ascii_visible_width(s: &[char]) -> Option<i64> {
    let mut width = 0;
    let mut i = 0;
    while i < s.len() {
        let code = s[i] as u32;
        if (0x20..=0x7E).contains(&code) {
            width += 1;
            i += 1;
        } else if code == 0x09 {
            width += 3;
            i += 1;
        } else if code == 0x1B {
            let length = ansi_code_length(s, i);
            if length == 0 {
                return None;
            }
            i += length;
        } else {
            return None;
        }
    }
    Some(width)
}

fn visible_width_chars(text: &[char]) -> i64 {
    if text.is_empty() {
        return 0;
    }
    if let Some(w) = ascii_visible_width(text) {
        return w;
    }
    let mut clean: Vec<char> = Vec::with_capacity(text.len());
    let mut i = 0;
    while i < text.len() {
        if text[i] == '\t' {
            clean.extend([' ', ' ', ' ']);
            i += 1;
            continue;
        }
        clean.push(text[i]);
        i += 1;
    }
    let mut stripped: Vec<char> = Vec::with_capacity(clean.len());
    let mut i = 0;
    while i < clean.len() {
        let length = if clean[i] == '\x1b' { ansi_code_length(&clean, i) } else { 0 };
        if length > 0 {
            i += length;
        } else {
            stripped.push(clean[i]);
            i += 1;
        }
    }
    graphemes(&stripped).iter().map(|g| grapheme_width(g)).sum()
}

/// The terminal columns of `text`, ignoring ANSI/OSC/APC sequences (pi-tui's `visibleWidth`).
pub fn visible_width(text: &str) -> usize {
    let chars: Vec<char> = text.chars().collect();
    visible_width_chars(&chars) as usize
}

/// `text` without its ANSI, OSC and APC control sequences (pi-tui's `stripTerminalSequences`).
pub fn strip_terminal_sequences(text: &str) -> String {
    if !text.contains('\x1b') {
        return text.to_string();
    }
    let chars: Vec<char> = text.chars().collect();
    let mut out = String::with_capacity(text.len());
    let mut i = 0;
    while i < chars.len() {
        let length = ansi_code_length(&chars, i);
        if length > 0 {
            i += length;
        } else {
            out.push(chars[i]);
            i += 1;
        }
    }
    out
}

fn text_end(line: &[char], i: usize) -> usize {
    let mut end = i;
    while end < line.len() && ansi_code_length(line, end) == 0 {
        end += 1;
    }
    end
}

/// The OSC 8 hyperlink target covering the visible `column` of `line` (pi-tui's `getOsc8LinkAtColumn`).
pub fn get_osc8_link_at_column(line: &str, column: i64) -> Option<String> {
    let line: Vec<char> = line.chars().collect();
    let mut active: Option<String> = None;
    let mut current: i64 = 0;
    let mut i = 0;
    while i < line.len() {
        let length = ansi_code_length(&line, i);
        if length > 0 {
            if let Some(url) = osc8_link_url(&line[i..i + length]) {
                active = if url.is_empty() { None } else { Some(url) };
            }
            i += length;
            continue;
        }
        let end = text_end(&line, i);
        for seg in graphemes(&line[i..end]) {
            let width = if seg == ['\t'] { 3 } else { grapheme_width(seg) };
            if column >= current && column < current + width {
                return active;
            }
            current += width;
        }
        i = end;
    }
    None
}

/// `/^\x1b\]8;[^;]*;([^\x07\x1b]*)(?:\x07|\x1b\\)$/` over one escape code: the captured target, or `None` when the code is not that shape.
fn osc8_link_url(code: &[char]) -> Option<String> {
    let rest = code.strip_prefix(&['\x1b', ']', '8', ';'])?;
    let sep = rest.iter().position(|&c| c == ';')?;
    if rest[..sep].contains(&';') {
        return None;
    }
    let tail = &rest[sep + 1..];
    let url_len = tail.iter().position(|&c| c == '\x07' || c == '\x1b').unwrap_or(tail.len());
    let terminator = &tail[url_len..];
    if terminator == ['\x07'] || terminator == ['\x1b', '\\'] {
        Some(chars_to_string(&tail[..url_len]))
    } else {
        None
    }
}

#[derive(Clone)]
struct Hyperlink {
    params: String,
    url: String,
    terminator: &'static str,
}

enum Osc8 {
    Open(Hyperlink),
    Close,
}

/// `None` is JavaScript's `undefined` (not an OSC 8 sequence); `Some(Osc8::Close)` is its `null` (a close).
fn parse_osc8(code: &[char]) -> Option<Osc8> {
    let code = chars_to_string(code);
    let rest = code.strip_prefix("\x1b]8;")?;
    let (terminator, body) = if let Some(b) = rest.strip_suffix('\x07') { ("\x07", b) } else { ("\x1b\\", rest.get(..rest.len().saturating_sub(2)).unwrap_or("")) };
    let sep = body.find(';')?;
    let (params, url) = (&body[..sep], &body[sep + 1..]);
    if url.is_empty() {
        return Some(Osc8::Close);
    }
    Some(Osc8::Open(Hyperlink { params: params.to_string(), url: url.to_string(), terminator }))
}

fn active_osc8_close(prefix: &[char]) -> String {
    let text = chars_to_string(prefix);
    if !text.contains("\x1b]8;") {
        return String::new();
    }
    let mut active: Option<Hyperlink> = None;
    let mut i = 0;
    while i < prefix.len() {
        let length = ansi_code_length(prefix, i);
        if length > 0 {
            match parse_osc8(&prefix[i..i + length]) {
                Some(Osc8::Open(link)) => active = Some(link),
                Some(Osc8::Close) => active = None,
                None => {}
            }
            i += length;
        } else {
            i += 1;
        }
    }
    active.map_or(String::new(), |a| format!("\x1b]8;;{}", a.terminator))
}

#[derive(Default)]
struct AnsiCodeTracker {
    bold: bool,
    dim: bool,
    italic: bool,
    underline: bool,
    blink: bool,
    inverse: bool,
    hidden: bool,
    strikethrough: bool,
    fg: Option<String>,
    bg: Option<String>,
    hyperlink: Option<Hyperlink>,
}

/// `Number.parseInt(text, 10)` of an SGR parameter (digits only here); `None` is NaN.
fn js_int(text: &str) -> Option<i64> {
    if text.is_empty() || !text.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    Some(text.parse::<i64>().unwrap_or(i64::MAX))
}

impl AnsiCodeTracker {
    fn reset(&mut self) {
        self.bold = false;
        self.dim = false;
        self.italic = false;
        self.underline = false;
        self.blink = false;
        self.inverse = false;
        self.hidden = false;
        self.strikethrough = false;
        self.fg = None;
        self.bg = None;
    }

    fn clear(&mut self) {
        self.reset();
        self.hyperlink = None;
    }

    fn process(&mut self, code: &[char]) {
        match parse_osc8(code) {
            Some(Osc8::Open(link)) => {
                self.hyperlink = Some(link);
                return;
            }
            Some(Osc8::Close) => {
                self.hyperlink = None;
                return;
            }
            None => {}
        }
        if code.last() != Some(&'m') {
            return;
        }
        // /\x1b\[([\d;]*)m/ anywhere in the code.
        let mut params: Option<String> = None;
        let mut s = 0;
        while s + 1 < code.len() {
            if code[s] == '\x1b' && code[s + 1] == '[' {
                let mut e = s + 2;
                while e < code.len() && (code[e].is_ascii_digit() || code[e] == ';') {
                    e += 1;
                }
                if e < code.len() && code[e] == 'm' {
                    params = Some(chars_to_string(&code[s + 2..e]));
                    break;
                }
            }
            s += 1;
        }
        let Some(params) = params else { return };
        if params.is_empty() || params == "0" {
            self.reset();
            return;
        }
        let parts: Vec<&str> = params.split(';').collect();
        let mut i = 0;
        while i < parts.len() {
            let code = js_int(parts[i]);
            if code == Some(38) || code == Some(48) {
                if parts.get(i + 1) == Some(&"5") && i + 2 < parts.len() {
                    let color = format!("{};{};{}", parts[i], parts[i + 1], parts[i + 2]);
                    if code == Some(38) { self.fg = Some(color) } else { self.bg = Some(color) }
                    i += 3;
                    continue;
                }
                if parts.get(i + 1) == Some(&"2") && i + 4 < parts.len() {
                    let color = parts[i..i + 5].join(";");
                    if code == Some(38) { self.fg = Some(color) } else { self.bg = Some(color) }
                    i += 5;
                    continue;
                }
            }
            match code {
                Some(0) => self.reset(),
                Some(1) => self.bold = true,
                Some(2) => self.dim = true,
                Some(3) => self.italic = true,
                Some(4) => self.underline = true,
                Some(5) => self.blink = true,
                Some(7) => self.inverse = true,
                Some(8) => self.hidden = true,
                Some(9) => self.strikethrough = true,
                Some(21) => self.bold = false,
                Some(22) => {
                    self.bold = false;
                    self.dim = false;
                }
                Some(23) => self.italic = false,
                Some(24) => self.underline = false,
                Some(25) => self.blink = false,
                Some(27) => self.inverse = false,
                Some(28) => self.hidden = false,
                Some(29) => self.strikethrough = false,
                Some(39) => self.fg = None,
                Some(49) => self.bg = None,
                Some(c) => {
                    if (30..=37).contains(&c) || (90..=97).contains(&c) {
                        self.fg = Some(c.to_string());
                    } else if (40..=47).contains(&c) || (100..=107).contains(&c) {
                        self.bg = Some(c.to_string());
                    }
                }
                None => {}
            }
            i += 1;
        }
    }

    fn active_codes(&self) -> String {
        let mut codes: Vec<&str> = Vec::new();
        if self.bold { codes.push("1") }
        if self.dim { codes.push("2") }
        if self.italic { codes.push("3") }
        if self.underline { codes.push("4") }
        if self.blink { codes.push("5") }
        if self.inverse { codes.push("7") }
        if self.hidden { codes.push("8") }
        if self.strikethrough { codes.push("9") }
        if let Some(fg) = self.fg.as_deref().filter(|s| !s.is_empty()) { codes.push(fg) }
        if let Some(bg) = self.bg.as_deref().filter(|s| !s.is_empty()) { codes.push(bg) }
        let mut result = if codes.is_empty() { String::new() } else { format!("\x1b[{}m", codes.join(";")) };
        if let Some(h) = &self.hyperlink {
            result.push_str(&format!("\x1b]8;{};{}{}", h.params, h.url, h.terminator));
        }
        result
    }

    fn active_background_code(&self) -> String {
        match self.bg.as_deref() {
            Some(bg) if !bg.is_empty() => format!("\x1b[{bg}m"),
            _ => String::new(),
        }
    }

    fn line_end_reset(&self) -> String {
        let mut result = String::new();
        if self.underline {
            result.push_str("\x1b[24m");
        }
        if let Some(h) = &self.hyperlink {
            result.push_str(&format!("\x1b]8;;{}", h.terminator));
        }
        result
    }
}

fn update_tracker(text: &[char], tracker: &mut AnsiCodeTracker) {
    let mut i = 0;
    while i < text.len() {
        if text[i] == '\x1b' {
            let length = ansi_code_length(text, i);
            if length > 0 {
                tracker.process(&text[i..i + length]);
                i += length;
                continue;
            }
        }
        i += 1;
    }
}

/// The background SGR sequence active at the end of `text` (pi-tui's `getActiveBackgroundAnsi`).
pub fn get_active_background_ansi(text: &str) -> String {
    let chars: Vec<char> = text.chars().collect();
    let mut tracker = AnsiCodeTracker::default();
    update_tracker(&chars, &mut tracker);
    tracker.active_background_code()
}

fn is_cjk_break(segment: &[char]) -> bool {
    segment.iter().any(|&c| props(c) & CJK != 0)
}

#[allow(unused_assignments)]
fn split_into_tokens(text: &[char]) -> Vec<String> {
    let mut tokens: Vec<String> = Vec::new();
    let mut current = String::new();
    let mut pending = String::new();
    let mut kind: Option<bool> = None; // Some(true) = space
    let mut i = 0;
    while i < text.len() {
        let length = ansi_code_length(text, i);
        if length > 0 {
            pending.push_str(&chars_to_string(&text[i..i + length]));
            i += length;
            continue;
        }
        let end = (i + 1..text.len()).find(|&j| text[j] == '\x1b' && ansi_code_length(text, j) > 0).unwrap_or(text.len());
        let chunk = &text[i..end];
        let ascii_chunk = is_printable_ascii(chunk);
        let segments: Vec<&[char]> = if ascii_chunk { (0..chunk.len()).map(|k| &chunk[k..k + 1]).collect() } else { graphemes(chunk) };
        for segment in segments {
            let is_space = segment == [' '];
            if !ascii_chunk && !is_space && is_cjk_break(segment) {
                if !current.is_empty() {
                    tokens.push(std::mem::take(&mut current));
                    kind = None;
                }
                tokens.push(format!("{}{}", pending, chars_to_string(segment)));
                pending.clear();
                continue;
            }
            if !current.is_empty() && kind != Some(is_space) {
                tokens.push(std::mem::take(&mut current));
                kind = None;
            }
            if !pending.is_empty() {
                current.push_str(&pending);
                pending.clear();
            }
            kind = Some(is_space);
            current.push_str(&chars_to_string(segment));
        }
        i = end;
    }
    if !pending.is_empty() {
        if !current.is_empty() {
            current.push_str(&pending);
        } else if let Some(last) = tokens.last_mut() {
            last.push_str(&pending);
        } else {
            current = pending;
        }
    }
    if !current.is_empty() {
        tokens.push(current);
    }
    tokens
}

/// `text` word-wrapped to `width` columns, carrying SGR styling and OSC 8 links across lines (pi-tui's `wrapTextWithAnsi`).
pub fn wrap_text_with_ansi(text: &str, width: i64) -> Vec<String> {
    if text.is_empty() {
        return vec![String::new()];
    }
    let mut result: Vec<String> = Vec::new();
    let mut tracker = AnsiCodeTracker::default();
    let chars: Vec<char> = text.chars().collect();
    let mut lines: Vec<&[char]> = Vec::new();
    let mut start = 0;
    let mut i = 0;
    while i < chars.len() {
        if chars[i] == '\r' || chars[i] == '\n' {
            lines.push(&chars[start..i]);
            if chars[i] == '\r' && chars.get(i + 1) == Some(&'\n') {
                i += 1;
            }
            start = i + 1;
        }
        i += 1;
    }
    lines.push(&chars[start..]);
    for input_line in lines {
        let prefix = if result.is_empty() { String::new() } else { tracker.active_codes() };
        let mut line: Vec<char> = prefix.chars().collect();
        line.extend_from_slice(input_line);
        result.extend(wrap_single_line(&line, width));
        update_tracker(input_line, &mut tracker);
    }
    if result.is_empty() { vec![String::new()] } else { result }
}

#[allow(unused_assignments)]
fn wrap_single_line(line: &[char], width: i64) -> Vec<String> {
    if line.is_empty() {
        return vec![String::new()];
    }
    if visible_width_chars(line) <= width {
        return vec![chars_to_string(line)];
    }
    let mut wrapped: Vec<String> = Vec::new();
    let mut tracker = AnsiCodeTracker::default();
    let mut current = String::new();
    let mut current_len: i64 = 0;
    for token in split_into_tokens(line) {
        let token_chars: Vec<char> = token.chars().collect();
        let token_len = visible_width_chars(&token_chars);
        let is_whitespace = token.trim_matches(is_js_space).is_empty();
        if token_len > width && !is_whitespace {
            if !current.is_empty() {
                current.push_str(&tracker.line_end_reset());
                wrapped.push(std::mem::take(&mut current));
                current_len = 0;
            }
            let mut broken = break_long_word(&token_chars, width, &mut tracker);
            let last = broken.pop().unwrap_or_default();
            wrapped.extend(broken);
            current_len = visible_width_chars(&last.chars().collect::<Vec<_>>());
            current = last;
            continue;
        }
        if current_len + token_len > width && current_len > 0 {
            let mut to_wrap = trim_end_string(&current);
            to_wrap.push_str(&tracker.line_end_reset());
            wrapped.push(to_wrap);
            if is_whitespace {
                current = tracker.active_codes();
                current_len = 0;
            } else {
                current = tracker.active_codes() + &token;
                current_len = token_len;
            }
        } else {
            current.push_str(&token);
            current_len += token_len;
        }
        update_tracker(&token_chars, &mut tracker);
    }
    if !current.is_empty() {
        wrapped.push(current);
    }
    if wrapped.is_empty() { vec![String::new()] } else { wrapped.iter().map(|l| trim_end_string(l)).collect() }
}

fn break_long_word(word: &[char], width: i64, tracker: &mut AnsiCodeTracker) -> Vec<String> {
    let mut lines: Vec<String> = Vec::new();
    let mut current = tracker.active_codes();
    let mut current_width: i64 = 0;
    let mut segments: Vec<(bool, &[char])> = Vec::new();
    let mut i = 0;
    while i < word.len() {
        let length = ansi_code_length(word, i);
        if length > 0 {
            segments.push((true, &word[i..i + length]));
            i += length;
        } else {
            let end = text_end(word, i);
            for g in graphemes(&word[i..end]) {
                segments.push((false, g));
            }
            i = end;
        }
    }
    for (is_ansi, value) in segments {
        if is_ansi {
            current.push_str(&chars_to_string(value));
            tracker.process(value);
            continue;
        }
        if value.is_empty() {
            continue;
        }
        let w = visible_width_chars(value);
        if current_width + w > width {
            current.push_str(&tracker.line_end_reset());
            lines.push(std::mem::take(&mut current));
            current = tracker.active_codes();
            current_width = 0;
        }
        current.push_str(&chars_to_string(value));
        current_width += w;
    }
    if !current.is_empty() {
        lines.push(current);
    }
    if lines.is_empty() { vec![String::new()] } else { lines }
}

fn truncate_fragment(text: &[char], max_width: i64) -> (String, i64) {
    if max_width <= 0 || text.is_empty() {
        return (String::new(), 0);
    }
    if is_printable_ascii(text) {
        let clipped = &text[..text.len().min(max_width as usize)];
        return (chars_to_string(clipped), clipped.len() as i64);
    }
    let has_ansi = text.contains(&'\x1b');
    let has_tabs = text.contains(&'\t');
    let mut result = String::new();
    let mut width: i64 = 0;
    if !has_ansi && !has_tabs {
        for seg in graphemes(text) {
            let w = grapheme_width(seg);
            if width + w > max_width {
                break;
            }
            result.push_str(&chars_to_string(seg));
            width += w;
        }
        return (result, width);
    }
    let mut i = 0;
    let mut pending = String::new();
    while i < text.len() {
        let length = ansi_code_length(text, i);
        if length > 0 {
            pending.push_str(&chars_to_string(&text[i..i + length]));
            i += length;
            continue;
        }
        if text[i] == '\t' {
            if width + 3 > max_width {
                break;
            }
            result.push_str(&pending);
            pending.clear();
            result.push('\t');
            width += 3;
            i += 1;
            continue;
        }
        let mut end = i;
        while end < text.len() && text[end] != '\t' && ansi_code_length(text, end) == 0 {
            end += 1;
        }
        for seg in graphemes(&text[i..end]) {
            let w = grapheme_width(seg);
            if width + w > max_width {
                return (result, width);
            }
            result.push_str(&pending);
            pending.clear();
            result.push_str(&chars_to_string(seg));
            width += w;
        }
        i = end;
    }
    (result, width)
}

fn finalize_truncated(prefix: &str, prefix_width: i64, ellipsis: &str, ellipsis_width: i64, max_width: i64, pad: bool) -> String {
    let reset = "\x1b[0m";
    let prefix_chars: Vec<char> = prefix.chars().collect();
    let close = active_osc8_close(&prefix_chars);
    let visible = prefix_width + ellipsis_width;
    let mut result = if ellipsis.is_empty() { format!("{prefix}{close}{reset}") } else { format!("{prefix}{close}{reset}{ellipsis}{reset}") };
    if pad {
        result.push_str(&" ".repeat((max_width - visible).max(0) as usize));
    }
    result
}

/// `text` cut to at most `max_width` columns with `ellipsis` appended when it was cut (pi-tui's `truncateToWidth`). Pass `"..."` for
/// upstream's default ellipsis and `false` for its default `pad`.
pub fn truncate_to_width(text: &str, max_width: i64, ellipsis: &str, pad: bool) -> String {
    if max_width <= 0 {
        return String::new();
    }
    if text.is_empty() {
        return if pad { " ".repeat(max_width as usize) } else { String::new() };
    }
    let text_chars: Vec<char> = text.chars().collect();
    let ellipsis_chars: Vec<char> = ellipsis.chars().collect();
    let ellipsis_width = visible_width_chars(&ellipsis_chars);
    if ellipsis_width >= max_width {
        let text_width = visible_width_chars(&text_chars);
        if text_width <= max_width {
            return if pad { format!("{text}{}", " ".repeat((max_width - text_width) as usize)) } else { text.to_string() };
        }
        let (clipped, clipped_width) = truncate_fragment(&ellipsis_chars, max_width);
        if clipped_width == 0 {
            return if pad { " ".repeat(max_width as usize) } else { String::new() };
        }
        return finalize_truncated("", 0, &clipped, clipped_width, max_width, pad);
    }
    if is_printable_ascii(&text_chars) {
        if text_chars.len() as i64 <= max_width {
            return if pad { format!("{text}{}", " ".repeat((max_width - text_chars.len() as i64) as usize)) } else { text.to_string() };
        }
        let target = max_width - ellipsis_width;
        return finalize_truncated(&text[..target as usize], target, ellipsis, ellipsis_width, max_width, pad);
    }
    let target = max_width - ellipsis_width;
    let mut result = String::new();
    let mut pending = String::new();
    let mut visible_so_far: i64 = 0;
    let mut kept: i64 = 0;
    let mut contiguous = true;
    let mut overflowed = false;
    let exhausted;
    let has_ansi = text_chars.contains(&'\x1b');
    let has_tabs = text_chars.contains(&'\t');
    if !has_ansi && !has_tabs {
        for seg in graphemes(&text_chars) {
            let w = grapheme_width(seg);
            if contiguous && kept + w <= target {
                result.push_str(&chars_to_string(seg));
                kept += w;
            } else {
                contiguous = false;
            }
            visible_so_far += w;
            if visible_so_far > max_width {
                overflowed = true;
                break;
            }
        }
        exhausted = !overflowed;
    } else {
        let mut i = 0;
        'outer: while i < text_chars.len() {
            let length = ansi_code_length(&text_chars, i);
            if length > 0 {
                pending.push_str(&chars_to_string(&text_chars[i..i + length]));
                i += length;
                continue;
            }
            if text_chars[i] == '\t' {
                if contiguous && kept + 3 <= target {
                    result.push_str(&pending);
                    pending.clear();
                    result.push('\t');
                    kept += 3;
                } else {
                    contiguous = false;
                    pending.clear();
                }
                visible_so_far += 3;
                if visible_so_far > max_width {
                    overflowed = true;
                    break;
                }
                i += 1;
                continue;
            }
            let mut end = i;
            while end < text_chars.len() && text_chars[end] != '\t' && ansi_code_length(&text_chars, end) == 0 {
                end += 1;
            }
            for seg in graphemes(&text_chars[i..end]) {
                let w = grapheme_width(seg);
                if contiguous && kept + w <= target {
                    result.push_str(&pending);
                    pending.clear();
                    result.push_str(&chars_to_string(seg));
                    kept += w;
                } else {
                    contiguous = false;
                    pending.clear();
                }
                visible_so_far += w;
                if visible_so_far > max_width {
                    overflowed = true;
                    break 'outer;
                }
            }
            i = end;
        }
        exhausted = i >= text_chars.len();
    }
    if !overflowed && exhausted {
        return if pad { format!("{text}{}", " ".repeat((max_width - visible_so_far).max(0) as usize)) } else { text.to_string() };
    }
    finalize_truncated(&result, kept, ellipsis, ellipsis_width, max_width, pad)
}

/// The value of [`slice_with_width`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SliceResult {
    pub text: String,
    pub width: i64,
}

/// The visible columns `[start_col, start_col + length)` of `line` with their width (pi-tui's `sliceWithWidth`).
pub fn slice_with_width(line: &str, start_col: i64, length: i64, strict: bool) -> SliceResult {
    if length <= 0 {
        return SliceResult { text: String::new(), width: 0 };
    }
    let line: Vec<char> = line.chars().collect();
    let end_col = start_col + length;
    let mut result = String::new();
    let mut result_width: i64 = 0;
    let mut current: i64 = 0;
    let mut i = 0;
    let mut pending = String::new();
    while i < line.len() {
        let code_len = ansi_code_length(&line, i);
        if code_len > 0 {
            let code = chars_to_string(&line[i..i + code_len]);
            if current >= start_col && current < end_col {
                result.push_str(&pending);
                result.push_str(&code);
                pending.clear();
            } else if current < start_col {
                pending.push_str(&code);
            }
            i += code_len;
            continue;
        }
        let end = text_end(&line, i);
        for seg in graphemes(&line[i..end]) {
            let w = grapheme_width(seg);
            let in_range = current >= start_col && current < end_col;
            let fits = !strict || current + w <= end_col;
            if in_range && fits {
                result.push_str(&pending);
                pending.clear();
                result.push_str(&chars_to_string(seg));
                result_width += w;
            }
            current += w;
            if current >= end_col {
                break;
            }
        }
        i = end;
        if current >= end_col {
            break;
        }
    }
    SliceResult { text: result, width: result_width }
}

/// The visible columns `[start_col, start_col + length)` of `line` with ANSI codes preserved (pi-tui's `sliceByColumn`).
pub fn slice_by_column(line: &str, start_col: i64, length: i64, strict: bool) -> String {
    slice_with_width(line, start_col, length, strict).text
}

/// The value of [`extract_segments`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Segments {
    pub before: String,
    pub before_width: i64,
    pub after: String,
    pub after_width: i64,
}

/// The columns before `before_end` and the `after_len` columns from `after_start` in one pass (pi-tui's `extractSegments`).
pub fn extract_segments(line: &str, before_end: i64, after_start: i64, after_len: i64, strict_after: bool) -> Segments {
    let line: Vec<char> = line.chars().collect();
    let mut before = String::new();
    let mut before_width: i64 = 0;
    let mut after = String::new();
    let mut after_width: i64 = 0;
    let mut current: i64 = 0;
    let mut i = 0;
    let mut pending_before = String::new();
    let mut after_started = false;
    let after_end = after_start + after_len;
    let mut tracker = AnsiCodeTracker::default();
    tracker.clear();
    let done = |current: i64| if after_len <= 0 { current >= before_end } else { current >= after_end };
    while i < line.len() {
        let code_len = ansi_code_length(&line, i);
        if code_len > 0 {
            let code = &line[i..i + code_len];
            tracker.process(code);
            if current < before_end {
                pending_before.push_str(&chars_to_string(code));
            } else if current >= after_start && current < after_end && (after_started || code.starts_with(&['\x1b', '_'])) {
                after.push_str(&chars_to_string(code));
            }
            i += code_len;
            continue;
        }
        let end = text_end(&line, i);
        for seg in graphemes(&line[i..end]) {
            let w = grapheme_width(seg);
            if current < before_end && current + w <= before_end {
                before.push_str(&pending_before);
                pending_before.clear();
                before.push_str(&chars_to_string(seg));
                before_width += w;
            } else if current >= after_start && current < after_end {
                let fits = !strict_after || current + w <= after_end;
                if fits {
                    if !after_started {
                        after.push_str(&tracker.active_codes());
                        after_started = true;
                    }
                    after.push_str(&chars_to_string(seg));
                    after_width += w;
                }
            }
            current += w;
            if done(current) {
                break;
            }
        }
        i = end;
        if done(current) {
            break;
        }
    }
    Segments { before, before_width, after, after_width }
}

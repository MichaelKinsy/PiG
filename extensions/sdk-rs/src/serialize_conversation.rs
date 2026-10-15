//! Pi's `serializeConversation` (`core/compaction/utils.ts:120-155`, exported from `index.ts`): the text a summarization request carries for a conversation.
//!
//! Pi reads JavaScript strings, which hold UTF-16 units including an unpaired surrogate, and `serde_json::Value` cannot. The work therefore runs on [`Json`], a
//! value whose strings are unit vectors, and [`serialize_conversation_json`] reads such a string from JSON text without losing it.

use serde_json::Value;

use crate::compaction_estimate::js_number_string;
use crate::js_string::JsString;
use crate::llm_convert::js_number_to_string;

/// Pi's `TOOL_RESULT_MAX_CHARS`.
const TOOL_RESULT_MAX_CHARS: usize = 2000;

/// A decoded JSON value whose strings are UTF-16 units and whose objects keep the order of their first occurrence, a repeated key taking the last value, as
/// `JSON.parse` builds them.
#[derive(Debug, Clone, PartialEq)]
enum Json {
    Null,
    Bool(bool),
    Number(f64),
    Str(Vec<u16>),
    Array(Vec<Json>),
    Object(Vec<(Vec<u16>, Json)>),
}

impl Json {
    fn from_value(value: &Value) -> Json {
        match value {
            Value::Null => Json::Null,
            Value::Bool(flag) => Json::Bool(*flag),
            Value::Number(number) => Json::Number(number.as_f64().unwrap_or(f64::NAN)),
            Value::String(text) => Json::Str(text.encode_utf16().collect()),
            Value::Array(elements) => Json::Array(elements.iter().map(Json::from_value).collect()),
            Value::Object(members) => Json::Object(members.iter().map(|(key, member)| (key.encode_utf16().collect(), Json::from_value(member))).collect()),
        }
    }

    fn get(&self, key: &str) -> Option<&Json> {
        match self {
            Json::Object(members) => members.iter().find(|(name, _)| name.iter().copied().eq(key.encode_utf16())).map(|(_, member)| member),
            _ => None,
        }
    }

    fn is_null(&self) -> bool {
        matches!(self, Json::Null)
    }

    /// The string of a `Json::Str` that is exactly `text`.
    fn is_str(&self, text: &str) -> bool {
        matches!(self, Json::Str(units) if units.iter().copied().eq(text.encode_utf16()))
    }
}

fn units(text: &str) -> Vec<u16> {
    text.encode_utf16().collect()
}

/// Pi's `serializeConversation`: `messages` are LLM messages as JSON objects (the result of [`crate::convert_to_llm`]). A tool call prints its arguments in
/// property order, which `serde_json`'s order-keeping maps give. A message with another role than `user`, `assistant` or `toolResult` adds nothing, and
/// thinking blocks are omitted (Pi 1.2.0). Pi throws a `TypeError` when a message it reads has content that is not a string or an array, and this returns
/// that as `Err`.
pub fn serialize_conversation(messages: &[Value]) -> Result<String, String> {
    let messages: Vec<Json> = messages.iter().map(Json::from_value).collect();
    serialize(&messages).map(|units| String::from_utf16_lossy(&units))
}

/// [`serialize_conversation`] over the JSON text of the messages (an array), for a conversation whose strings hold an unpaired surrogate, which
/// `serde_json::Value` refuses. The text is a JavaScript string: the result keeps each unpaired unit, as Pi does.
pub fn serialize_conversation_json(messages_json: &str) -> Result<JsString, String> {
    let messages = crate::protocol::on_deep_stack(|| parse_json(messages_json)).map_err(|err| err.to_string())??;
    let Json::Array(messages) = messages else {
        return Err("TypeError: messages is not iterable".to_string());
    };
    serialize(&messages).map(JsString::from_units)
}

fn serialize(messages: &[Json]) -> Result<Vec<u16>, String> {
    let mut parts: Vec<Vec<u16>> = Vec::new();
    for message in messages {
        if message.is_null() {
            return Err("TypeError: Cannot read properties of null (reading 'role')".to_string());
        }
        let role = message.get("role");
        if role.is_some_and(|role| role.is_str("user")) {
            let content = content_text(message.get("content"), &[])?;
            if !content.is_empty() {
                parts.push([units("[User]: "), content].concat());
            }
        } else if role.is_some_and(|role| role.is_str("assistant")) {
            serialize_assistant(message, &mut parts)?;
        } else if role.is_some_and(|role| role.is_str("toolResult")) {
            let content = content_text(message.get("content"), &[])?;
            if !content.is_empty() {
                parts.push([units("[Tool result]: "), truncate_for_summary(&content, TOOL_RESULT_MAX_CHARS)].concat());
            }
        }
    }
    Ok(join(&parts, &units("\n\n")))
}

fn join(parts: &[Vec<u16>], separator: &[u16]) -> Vec<u16> {
    let mut out = Vec::new();
    for (i, part) in parts.iter().enumerate() {
        if i > 0 {
            out.extend_from_slice(separator);
        }
        out.extend_from_slice(part);
    }
    out
}

fn serialize_assistant(message: &Json, parts: &mut Vec<Vec<u16>>) -> Result<(), String> {
    let Some(Json::Array(blocks)) = message.get("content") else {
        return Err("TypeError: msg.content is not iterable".to_string());
    };
    let (mut tool_calls, mut has_text) = (Vec::new(), false);
    for block in blocks {
        if block.is_null() {
            return Err("TypeError: Cannot read properties of null (reading 'type')".to_string());
        }
        let kind = block.get("type");
        if kind.is_some_and(|kind| kind.is_str("toolCall")) {
            tool_calls.push([js_to_string(block.get("name")), units("("), entries_string(block.get("arguments"))?, units(")")].concat());
        } else if kind.is_some_and(|kind| kind.is_str("text")) {
            has_text = true;
        }
    }
    if has_text {
        parts.push([units("[Assistant]: "), content_text(message.get("content"), &units("\n"))?].concat());
    }
    if !tool_calls.is_empty() {
        parts.push([units("[Assistant tool calls]: "), join(&tool_calls, &units("; "))].concat());
    }
    Ok(())
}

/// The string a template literal or `+` makes of a decoded value: JavaScript's ToString, or `undefined` for an absent key.
fn js_to_string(value: Option<&Json>) -> Vec<u16> {
    match value {
        None => units("undefined"),
        Some(Json::Null) => units("null"),
        Some(Json::Str(text)) => text.clone(),
        Some(Json::Bool(flag)) => units(if *flag { "true" } else { "false" }),
        Some(Json::Number(number)) => units(&js_number_to_string(*number)),
        Some(Json::Array(elements)) => {
            let parts: Vec<Vec<u16>> = elements.iter().map(|element| if element.is_null() { Vec::new() } else { js_to_string(Some(element)) }).collect();
            join(&parts, &units(","))
        }
        Some(Json::Object(_)) => units("[object Object]"),
    }
}

/// What `Array.prototype.join` makes of a member: absent and `null` are the empty string, any other value its `ToString`.
fn string_or_empty(value: Option<&Json>) -> Vec<u16> {
    match value {
        None | Some(Json::Null) => Vec::new(),
        value => js_to_string(value),
    }
}

/// `Object.entries(args).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(", ")`. `Object.entries` throws for `null` and `undefined`, reads the indexes of a
/// string or array as keys, and yields nothing for a number or boolean.
fn entries_string(arguments: Option<&Json>) -> Result<Vec<u16>, String> {
    let entries: Vec<Vec<u16>> = match arguments {
        None | Some(Json::Null) => return Err("TypeError: Cannot convert undefined or null to object".to_string()),
        Some(Json::Object(members)) => ordered_members(members).into_iter().map(|(key, member)| [key.clone(), units("="), json_stringify(member)].concat()).collect(),
        Some(Json::Array(elements)) => elements.iter().enumerate().map(|(i, element)| [units(&format!("{i}=")), json_stringify(element)].concat()).collect(),
        Some(Json::Str(text)) => text.iter().enumerate().map(|(i, unit)| [units(&format!("{i}=")), unit_json(*unit)].concat()).collect(),
        Some(_) => Vec::new(),
    };
    Ok(join(&entries, &units(", ")))
}

/// pi-ai's `contentText` (`utils/text.ts:6-12`): a string is its own text and an array of blocks is the text of its text blocks joined with `separator`.
fn content_text(content: Option<&Json>, separator: &[u16]) -> Result<Vec<u16>, String> {
    match content {
        Some(Json::Str(text)) => Ok(text.clone()),
        Some(Json::Array(blocks)) => {
            let mut texts = Vec::new();
            for block in blocks {
                if block.is_null() {
                    return Err("TypeError: Cannot read properties of null (reading 'type')".to_string());
                }
                if block.get("type").is_some_and(|kind| kind.is_str("text")) {
                    texts.push(string_or_empty(block.get("text")));
                }
            }
            Ok(join(&texts, separator))
        }
        _ => Err("TypeError: content.filter is not a function".to_string()),
    }
}

/// Pi's `truncateForSummary`: `text.length` counts UTF-16 units, and the cut keeps the first `max_chars` of them, a cut through a pair leaving its high surrogate.
fn truncate_for_summary(text: &[u16], max_chars: usize) -> Vec<u16> {
    if text.len() <= max_chars {
        return text.to_vec();
    }
    [text[..max_chars].to_vec(), units(&format!("\n\n[... {} more characters truncated]", text.len() - max_chars))].concat()
}

/// The members of an object in the order `Object.keys` and `JSON.stringify` use: the array-index keys ascending, then every other key in insertion order.
fn ordered_members(members: &[(Vec<u16>, Json)]) -> Vec<(&Vec<u16>, &Json)> {
    let (mut indexes, others): (Vec<_>, Vec<_>) = members.iter().map(|(key, member)| (key, member)).partition(|(key, _)| is_array_index(key));
    indexes.sort_by(|a, b| a.0.len().cmp(&b.0.len()).then_with(|| a.0.cmp(b.0)));
    indexes.into_iter().chain(others).collect()
}

fn is_array_index(key: &[u16]) -> bool {
    let digits = key.iter().all(|unit| (u16::from(b'0')..=u16::from(b'9')).contains(unit));
    if key == [u16::from(b'0')] {
        return true;
    }
    digits && !key.is_empty() && key.len() <= 10 && key[0] != u16::from(b'0') && String::from_utf16_lossy(key).parse::<u64>().is_ok_and(|n| n <= u64::from(u32::MAX) - 1)
}

/// `JSON.stringify` for a decoded JSON value.
fn json_stringify(value: &Json) -> Vec<u16> {
    let mut out = Vec::new();
    write_json(&mut out, value);
    out
}

fn push(out: &mut Vec<u16>, text: &str) {
    out.extend(text.encode_utf16());
}

fn write_json(out: &mut Vec<u16>, value: &Json) {
    match value {
        Json::Null => push(out, "null"),
        Json::Bool(flag) => push(out, if *flag { "true" } else { "false" }),
        Json::Number(number) => push(out, &js_number_string(*number)),
        Json::Str(text) => write_json_string(out, text),
        Json::Array(elements) => {
            out.push(u16::from(b'['));
            for (i, element) in elements.iter().enumerate() {
                if i > 0 {
                    out.push(u16::from(b','));
                }
                write_json(out, element);
            }
            out.push(u16::from(b']'));
        }
        Json::Object(members) => {
            out.push(u16::from(b'{'));
            for (i, (key, member)) in ordered_members(members).into_iter().enumerate() {
                if i > 0 {
                    out.push(u16::from(b','));
                }
                write_json_string(out, key);
                out.push(u16::from(b':'));
                write_json(out, member);
            }
            out.push(u16::from(b'}'));
        }
    }
}

/// `QuoteJSONString`: a quote, a backslash and the control characters are escaped (`\b \f \n \r \t` short, the rest as lowercase `\u00xx`), an unpaired surrogate
/// is written as its lowercase `\uXXXX` escape and every other character is written as it is.
fn write_json_string(out: &mut Vec<u16>, text: &[u16]) {
    out.push(u16::from(b'"'));
    for c in char::decode_utf16(text.iter().copied()) {
        match c {
            Ok('"') => push(out, "\\\""),
            Ok('\\') => push(out, "\\\\"),
            Ok('\u{8}') => push(out, "\\b"),
            Ok('\u{c}') => push(out, "\\f"),
            Ok('\n') => push(out, "\\n"),
            Ok('\r') => push(out, "\\r"),
            Ok('\t') => push(out, "\\t"),
            Ok(c) if (c as u32) < 0x20 => push(out, &format!("\\u{:04x}", c as u32)),
            Ok(c) => {
                let mut buf = [0u16; 2];
                out.extend_from_slice(c.encode_utf16(&mut buf));
            }
            Err(unpaired) => push(out, &format!("\\u{:04x}", unpaired.unpaired_surrogate())),
        }
    }
    out.push(u16::from(b'"'));
}

/// `JSON.stringify` of a one-unit string.
fn unit_json(unit: u16) -> Vec<u16> {
    let mut out = Vec::new();
    write_json_string(&mut out, &[unit]);
    out
}

/// `JSON.parse` of a text, with every string kept as UTF-16 units.
fn parse_json(text: &str) -> Result<Json, String> {
    let mut parser = Parser { bytes: text.as_bytes(), at: 0 };
    let value = parser.value()?;
    parser.skip_space();
    if parser.at != parser.bytes.len() {
        return Err(parser.error("Unexpected non-whitespace character after JSON"));
    }
    Ok(value)
}

struct Parser<'a> {
    bytes: &'a [u8],
    at: usize,
}

impl Parser<'_> {
    fn error(&self, message: &str) -> String {
        format!("SyntaxError: {message} at position {}", self.at)
    }

    fn skip_space(&mut self) {
        while matches!(self.bytes.get(self.at), Some(b' ' | b'\t' | b'\n' | b'\r')) {
            self.at += 1;
        }
    }

    fn expect(&mut self, literal: &str, value: Json) -> Result<Json, String> {
        if self.bytes[self.at..].starts_with(literal.as_bytes()) {
            self.at += literal.len();
            Ok(value)
        } else {
            Err(self.error("Unexpected token"))
        }
    }

    fn value(&mut self) -> Result<Json, String> {
        self.skip_space();
        match self.bytes.get(self.at) {
            None => Err(self.error("Unexpected end of JSON input")),
            Some(b'n') => self.expect("null", Json::Null),
            Some(b't') => self.expect("true", Json::Bool(true)),
            Some(b'f') => self.expect("false", Json::Bool(false)),
            Some(b'"') => self.string().map(Json::Str),
            Some(b'[') => {
                self.at += 1;
                let mut elements = Vec::new();
                self.skip_space();
                if self.bytes.get(self.at) == Some(&b']') {
                    self.at += 1;
                    return Ok(Json::Array(elements));
                }
                loop {
                    elements.push(self.value()?);
                    self.skip_space();
                    match self.bytes.get(self.at) {
                        Some(b',') => self.at += 1,
                        Some(b']') => {
                            self.at += 1;
                            return Ok(Json::Array(elements));
                        }
                        _ => return Err(self.error("Expected ',' or ']'")),
                    }
                }
            }
            Some(b'{') => {
                self.at += 1;
                let mut members: Vec<(Vec<u16>, Json)> = Vec::new();
                self.skip_space();
                if self.bytes.get(self.at) == Some(&b'}') {
                    self.at += 1;
                    return Ok(Json::Object(members));
                }
                loop {
                    self.skip_space();
                    if self.bytes.get(self.at) != Some(&b'"') {
                        return Err(self.error("Expected a property name"));
                    }
                    let key = self.string()?;
                    self.skip_space();
                    if self.bytes.get(self.at) != Some(&b':') {
                        return Err(self.error("Expected ':'"));
                    }
                    self.at += 1;
                    let member = self.value()?;
                    match members.iter_mut().find(|(name, _)| *name == key) {
                        Some(existing) => existing.1 = member,
                        None => members.push((key, member)),
                    }
                    self.skip_space();
                    match self.bytes.get(self.at) {
                        Some(b',') => self.at += 1,
                        Some(b'}') => {
                            self.at += 1;
                            return Ok(Json::Object(members));
                        }
                        _ => return Err(self.error("Expected ',' or '}'")),
                    }
                }
            }
            Some(b'-' | b'0'..=b'9') => self.number(),
            Some(_) => Err(self.error("Unexpected token")),
        }
    }

    fn number(&mut self) -> Result<Json, String> {
        let start = self.at;
        if self.bytes.get(self.at) == Some(&b'-') {
            self.at += 1;
        }
        match self.bytes.get(self.at) {
            Some(b'0') => self.at += 1,
            Some(b'1'..=b'9') => self.digits(),
            _ => return Err(self.error("No number after minus sign")),
        }
        if self.bytes.get(self.at) == Some(&b'.') {
            self.at += 1;
            if !self.bytes.get(self.at).is_some_and(u8::is_ascii_digit) {
                return Err(self.error("Unterminated fractional number"));
            }
            self.digits();
        }
        if matches!(self.bytes.get(self.at), Some(b'e' | b'E')) {
            self.at += 1;
            if matches!(self.bytes.get(self.at), Some(b'+' | b'-')) {
                self.at += 1;
            }
            if !self.bytes.get(self.at).is_some_and(u8::is_ascii_digit) {
                return Err(self.error("Exponent part is missing a number"));
            }
            self.digits();
        }
        // The slice is ASCII digits, a sign, a point and an exponent: it parses, to an infinity when out of range, as JSON.parse reads it.
        Ok(Json::Number(std::str::from_utf8(&self.bytes[start..self.at]).ok().and_then(|text| text.parse().ok()).unwrap_or(f64::NAN)))
    }

    fn digits(&mut self) {
        while self.bytes.get(self.at).is_some_and(u8::is_ascii_digit) {
            self.at += 1;
        }
    }

    /// A string from its opening quote: each `\uXXXX` is one unit, kept whether or not it is half of a pair.
    fn string(&mut self) -> Result<Vec<u16>, String> {
        self.at += 1;
        let mut out: Vec<u16> = Vec::new();
        loop {
            let Some(&byte) = self.bytes.get(self.at) else {
                return Err(self.error("Unterminated string in JSON"));
            };
            match byte {
                b'"' => {
                    self.at += 1;
                    return Ok(out);
                }
                b'\\' => {
                    let escape = self.bytes.get(self.at + 1).copied();
                    self.at += 2;
                    match escape {
                        Some(b'"') => out.push(u16::from(b'"')),
                        Some(b'\\') => out.push(u16::from(b'\\')),
                        Some(b'/') => out.push(u16::from(b'/')),
                        Some(b'b') => out.push(8),
                        Some(b'f') => out.push(12),
                        Some(b'n') => out.push(10),
                        Some(b'r') => out.push(13),
                        Some(b't') => out.push(9),
                        Some(b'u') => {
                            let digits = self.bytes.get(self.at..self.at + 4).and_then(|digits| std::str::from_utf8(digits).ok());
                            let Some(unit) = digits.filter(|digits| digits.bytes().all(|b| b.is_ascii_hexdigit())).and_then(|digits| u16::from_str_radix(digits, 16).ok()) else {
                                return Err(self.error("Bad Unicode escape in JSON"));
                            };
                            out.push(unit);
                            self.at += 4;
                        }
                        _ => return Err(self.error("Bad escaped character in JSON")),
                    }
                }
                0..=0x1f => return Err(self.error("Bad control character in string literal in JSON")),
                _ => {
                    // A run of literal characters: the source is a &str, so the run is valid UTF-8.
                    let start = self.at;
                    while self.bytes.get(self.at).is_some_and(|b| *b != b'"' && *b != b'\\' && *b >= 0x20) {
                        self.at += 1;
                    }
                    out.extend(String::from_utf8_lossy(&self.bytes[start..self.at]).encode_utf16());
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn units_of(text: &JsString) -> Vec<u16> {
        text.as_units().to_vec()
    }

    // Pi's JavaScript strings keep an unpaired surrogate; JSON.stringify writes one as its lowercase escape and a slice may cut a pair.
    #[test]
    fn an_unpaired_surrogate_in_a_message_keeps_its_unit() {
        let text = serialize_conversation_json(r#"[{"role":"user","content":"a\ud800b\udc00c"}]"#).unwrap();
        let mut want = units("[User]: a");
        want.push(0xD800);
        want.extend(units("b"));
        want.push(0xDC00);
        want.extend(units("c"));
        assert_eq!(units_of(&text), want);
    }

    #[test]
    fn a_reversed_pair_is_two_unpaired_units() {
        let text = serialize_conversation_json(r#"[{"role":"user","content":"\ude00\ud83d"}]"#).unwrap();
        assert_eq!(units_of(&text)[8..], [0xDE00, 0xD83D]);
    }

    #[test]
    fn a_string_argument_prints_one_escaped_entry_per_unit() {
        let text = serialize_conversation_json(r#"[{"role":"assistant","content":[{"type":"toolCall","name":"n\udc00","arguments":"x\ud83d"}]}]"#).unwrap();
        let mut want = units("[Assistant tool calls]: n");
        want.push(0xDC00);
        want.extend(units(r#"(0="x", 1="\ud83d")"#));
        assert_eq!(units_of(&text), want);
    }

    #[test]
    fn the_cut_of_a_pair_leaves_its_high_surrogate() {
        let json = format!(r#"[{{"role":"toolResult","content":"{}😀😀"}}]"#, "a".repeat(1999));
        let units = units_of(&serialize_conversation_json(&json).unwrap());
        let body = &units["[Tool result]: ".len()..];
        assert_eq!(body[1999], 0xD83D);
        assert_eq!(&body[2000..], super::units("\n\n[... 3 more characters truncated]").as_slice());
    }

    #[test]
    fn the_value_path_and_the_text_path_agree() {
        let json = r#"[{"role":"assistant","content":[{"type":"text","text":"hi"},{"type":"toolCall","name":"t","arguments":{"b":1,"2":[1.5,null],"1":"x\n","a":{"9":3,"10":2,"z":1}}}]},{"role":"toolResult","content":[{"type":"text","text":"ok"}]}]"#;
        let values: Vec<Value> = serde_json::from_str(json).unwrap();
        assert_eq!(serialize_conversation(&values).unwrap(), serialize_conversation_json(json).unwrap().to_string_lossy());
        assert!(serialize_conversation(&values).unwrap().contains(r#"t(1="x\n", 2=[1.5,null], b=1, a={"9":3,"10":2,"z":1})"#));
    }

    #[test]
    fn text_that_is_not_json_or_not_an_array_is_an_error() {
        assert!(serialize_conversation_json("[").is_err());
        assert!(serialize_conversation_json(r#"{"role":"user"}"#).is_err());
        assert!(serialize_conversation_json(r#"["\ud800"#).is_err());
        assert!(serialize_conversation_json(r#"[{"role":"user","content":5}]"#).is_err());
    }
}

//! Pi's `estimateTokens` (`core/compaction/compaction.ts`): a chars/4 heuristic over one agent message as the wire carries it, rounded up. Lengths are
//! JavaScript string lengths, UTF-16 code units, and a tool call's arguments count as the length of their `JSON.stringify` text.

use serde_json::Value;

const ESTIMATED_IMAGE_CHARS: usize = 4800;

fn utf16_length(text: &str) -> usize {
    text.encode_utf16().count()
}

fn text_of<'a>(value: &'a Value, key: &str) -> &'a str {
    value.get(key).and_then(Value::as_str).unwrap_or("")
}

fn content_chars(content: &Value) -> usize {
    match content {
        Value::String(text) => utf16_length(text),
        Value::Array(blocks) => blocks
            .iter()
            .map(|block| match block.get("type").and_then(Value::as_str) {
                Some("text") => utf16_length(text_of(block, "text")),
                Some("image") => ESTIMATED_IMAGE_CHARS,
                _ => 0,
            })
            .sum(),
        _ => 0,
    }
}

pub(crate) fn js_truthy(value: &Value) -> bool {
    match value {
        Value::Null => false,
        Value::Bool(flag) => *flag,
        Value::String(text) => !text.is_empty(),
        Value::Number(number) => number.as_f64().is_some_and(|number| number != 0.0 && !number.is_nan()),
        _ => true,
    }
}

/// Pi's `estimateTokens` for one agent message (a JSON object with a `role`). A role Pi does not size is 0.
pub fn estimate_tokens(message: &Value) -> i64 {
    estimate_tokens_with(message, &[])
}

/// [`estimate_tokens`] for a message read from text that held unpaired surrogates ([`crate::lossless_json`]): each character of `stand_ins` stands for one, which
/// is one unit in a string and the six of `\ud800` in the `JSON.stringify` text of a tool call's arguments.
pub(crate) fn estimate_tokens_with(message: &Value, stand_ins: &[char]) -> i64 {
    let chars = match message.get("role").and_then(Value::as_str).unwrap_or("") {
        "system" => {
            let mut chars = content_chars(&message["content"]);
            if let Some(sections) = message.get("sections").and_then(Value::as_object) {
                chars += sections.values().filter_map(Value::as_str).map(utf16_length).sum::<usize>();
            }
            let added = &message["toolsAdded"];
            if js_truthy(added) {
                chars += json_stringify_length(added, stand_ins);
            }
            chars
        }
        "user" | "custom" | "toolResult" => content_chars(&message["content"]),
        "assistant" => message["content"]
            .as_array()
            .map(|blocks| {
                blocks
                    .iter()
                    .map(|block| match block.get("type").and_then(Value::as_str) {
                        Some("text") => utf16_length(text_of(block, "text")),
                        Some("thinking") => utf16_length(text_of(block, "thinking")),
                        Some("toolCall") => utf16_length(text_of(block, "name")) + json_stringify_length(&block["arguments"], stand_ins),
                        _ => 0,
                    })
                    .sum()
            })
            .unwrap_or(0),
        "bashExecution" => utf16_length(text_of(message, "command")) + utf16_length(text_of(message, "output")),
        "branchSummary" | "compactionSummary" => utf16_length(text_of(message, "summary")),
        _ => return 0,
    };
    chars.div_ceil(4) as i64
}

/// The UTF-16 length of `JSON.stringify(value)` for a decoded JSON value, without building the text. A key order does not change a length.
pub(crate) fn json_stringify_length(value: &Value, stand_ins: &[char]) -> usize {
    match value {
        Value::Null => 4,
        Value::Bool(true) => 4,
        Value::Bool(false) => 5,
        Value::Number(number) => js_number_string(number.as_f64().unwrap_or(f64::NAN)).len(),
        Value::String(text) => json_quoted_length(text, stand_ins),
        Value::Array(elements) => 2 + elements.iter().map(|element| json_stringify_length(element, stand_ins)).sum::<usize>() + elements.len().saturating_sub(1),
        Value::Object(members) => 2 + members.iter().map(|(key, element)| json_quoted_length(key, stand_ins) + 1 + json_stringify_length(element, stand_ins)).sum::<usize>() + members.len().saturating_sub(1),
    }
}

/// The UTF-16 length of `JSON.stringify(text)`: quotes, the short escapes, `\u00XX` for the other control characters, and every other character as it is.
fn json_quoted_length(text: &str, stand_ins: &[char]) -> usize {
    2 + text
        .chars()
        .map(|c| match c {
            '"' | '\\' | '\u{8}' | '\u{c}' | '\n' | '\r' | '\t' => 2,
            c if (c as u32) < 0x20 => 6,
            c if stand_ins.contains(&c) => 6,
            c => c.len_utf16(),
        })
        .sum::<usize>()
}

/// JavaScript's `Number::toString` for the number `JSON.stringify` writes: the shortest digits that round-trip, positional for exponents from -7 to 20
/// and exponential beyond; `-0` is `0` and a non-finite number is `null`.
pub(crate) fn js_number_string(value: f64) -> String {
    if !value.is_finite() {
        return "null".to_string();
    }
    if value == 0.0 {
        return "0".to_string();
    }
    let sign = if value < 0.0 { "-" } else { "" };
    let exponential = format!("{:e}", value.abs());
    let (mantissa, exponent_text) = exponential.split_once('e').unwrap_or((&exponential, "0"));
    let digits = mantissa.replacen('.', "", 1);
    let exponent: i32 = exponent_text.parse().unwrap_or(0);
    let (n, k) = (exponent + 1, digits.len() as i32);
    if k <= n && n <= 21 {
        return format!("{sign}{digits}{}", "0".repeat((n - k) as usize));
    }
    if 0 < n && n <= 21 {
        return format!("{sign}{}.{}", &digits[..n as usize], &digits[n as usize..]);
    }
    if -6 < n && n <= 0 {
        return format!("{sign}0.{}{digits}", "0".repeat((-n) as usize));
    }
    let exponent_sign = if n - 1 < 0 { '-' } else { '+' };
    let magnitude = (n - 1).abs();
    if k == 1 {
        return format!("{sign}{digits}e{exponent_sign}{magnitude}");
    }
    format!("{sign}{}.{}e{exponent_sign}{magnitude}", &digits[..1], &digits[1..])
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    // The cases ECMA-262 7.1.12.1 and V8 print.
    #[test]
    fn number_to_string_matches_javascript() {
        for (value, want) in [
            (0.0, "0"), (1.0, "1"), (-1.0, "-1"), (1.5, "1.5"), (0.1, "0.1"), (100.0, "100"), (123456789012345680000.0, "123456789012345680000"), (1e21, "1e+21"), (1.5e21, "1.5e+21"),
            (1e-6, "0.000001"), (1e-7, "1e-7"), (1.5e-7, "1.5e-7"), (123e-20, "1.23e-18"), (0.000001234, "0.000001234"), (5e-324, "5e-324"), (1.7976931348623157e308, "1.7976931348623157e+308"),
            (-0.5, "-0.5"), (4294967296.5, "4294967296.5"), (1e20, "100000000000000000000"), (12345.678, "12345.678"), (-0.0, "0"),
        ] {
            assert_eq!(js_number_string(value), want, "{value:?}");
        }
    }

    #[test]
    fn estimates_count_utf16_units_and_stringify_length() {
        for (name, message, want) in [
            ("user string", json!({"role":"user","content":"abcde"}), 2),
            ("user astral", json!({"role":"user","content":"😀😀"}), 1),
            ("user image and text", json!({"role":"user","content":[{"type":"image"},{"type":"text","text":"abcd"}]}), 1201),
            ("assistant tool call", json!({"role":"assistant","content":[{"type":"toolCall","name":"read","arguments":{"path":"a\"b"}}]}), 5),
            ("unknown role", json!({"role":"x","content":"abc"}), 0),
        ] {
            assert_eq!(estimate_tokens(&message), want, "{name}");
        }
    }
}

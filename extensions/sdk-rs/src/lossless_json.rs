//! JSON text that holds an unpaired surrogate (`"\ud83d"`, which `JSON.stringify` writes for a JavaScript string that holds one) cannot become a `serde_json::Value`: a
//! Rust `String` is UTF-8. [`Protected`] carries such text through code written over `Value` without losing a unit: each unpaired surrogate escape becomes a private-use
//! character that occurs nowhere in the text (one unit, as the escape is one unit, and a different character for a different unit), and [`Protected::restore`] writes
//! the escapes back into the JSON the code produced.

use std::collections::HashSet;

use crate::protocol::rewrite_unpaired_surrogate_escapes;

/// The unpaired surrogate units of one text and the private-use character that stands for each.
pub(crate) struct Protected {
    stands_for: Vec<(char, u16)>,
}

/// `text` with each unpaired surrogate escape replaced by its private-use character. Text with none is returned as it is. There are 6,400 private-use characters in
/// the Basic Multilingual Plane and 2,048 surrogate units, so a text that holds some of the characters still has one left for each unit it needs.
pub(crate) fn protect(text: &str) -> (String, Protected) {
    // A private-use character the text holds, written out or as an escape (an over-count is harmless), is one the text's own value must keep.
    let mut used: HashSet<char> = text.chars().filter(|c| ('\u{e000}'..='\u{f8ff}').contains(c)).collect();
    for (at, _) in text.match_indices("\\u") {
        if let Some(c) = text.get(at + 2..at + 6).and_then(|hex| u32::from_str_radix(hex, 16).ok()).and_then(char::from_u32) {
            used.insert(c);
        }
    }
    let mut free = ('\u{e000}'..='\u{f8ff}').filter(|c| !used.contains(c));
    let mut stands_for: Vec<(char, u16)> = Vec::new();
    let rewritten = rewrite_unpaired_surrogate_escapes(text.as_bytes(), |unit| {
        let found = stands_for.iter().find(|(_, known)| *known == unit).map(|(c, _)| *c);
        let c = found.unwrap_or_else(|| {
            let c = free.next().expect("6,400 private-use characters are more than the 2,048 surrogate units");
            stands_for.push((c, unit));
            c
        });
        c.to_string().into_bytes()
    });
    let text = match rewritten {
        Some(bytes) => String::from_utf8(bytes).expect("the rewrite replaces ASCII escapes with whole characters"),
        None => text.to_string(),
    };
    (text, Protected { stands_for })
}

impl Protected {
    /// The characters that stand for an unpaired surrogate.
    pub(crate) fn stand_in_chars(&self) -> Vec<char> {
        self.stands_for.iter().map(|(c, _)| *c).collect()
    }

    /// `json` with each stand-in character written as the escape of its unit.
    pub(crate) fn restore(&self, json: &str) -> String {
        if self.stands_for.is_empty() {
            return json.to_string();
        }
        let mut out = String::with_capacity(json.len());
        for c in json.chars() {
            match self.stands_for.iter().find(|(stand_in, _)| *stand_in == c) {
                Some((_, unit)) => out.push_str(&format!("\\u{unit:04x}")),
                None => out.push(c),
            }
        }
        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn units_round_trip_through_a_value() {
        let text = r#"{"a":"x\ud83dy","\udc00":["\ud83d","\ud83d\ude00","\\ud83d"],"p":"\ue000"}"#;
        let (protected, stands) = protect(text);
        let value: serde_json::Value = serde_json::from_str(&protected).unwrap();
        let back = stands.restore(&serde_json::to_string(&value).unwrap());
        assert_eq!(back, format!(r#"{{"a":"x\ud83dy","\udc00":["\ud83d","😀","\\ud83d"],"p":"{}"}}"#, '\u{e000}'));
    }

    #[test]
    fn a_private_use_escape_of_the_text_keeps_its_character() {
        let (protected, stands) = protect(r#"["\ud83d","\ue000","\ue001"]"#);
        let value: serde_json::Value = serde_json::from_str(&protected).unwrap();
        assert_eq!(value[1], "\u{e000}");
        assert_eq!(value[2], "\u{e001}");
        assert_eq!(stands.restore(&serde_json::to_string(&value).unwrap()), format!(r#"["\ud83d","{}","{}"]"#, '\u{e000}', '\u{e001}'));
    }

    #[test]
    fn text_without_an_unpaired_surrogate_is_unchanged() {
        let (text, stands) = protect(r#"{"a":"\ud83d\ude00"}"#);
        assert_eq!(text, r#"{"a":"\ud83d\ude00"}"#);
        assert_eq!(stands.restore("x"), "x");
    }

    #[test]
    fn a_stand_in_never_collides_with_a_character_of_the_text() {
        let text = format!(r#"["{}","\ud83d","\udc00"]"#, '\u{e000}');
        let (protected, stands) = protect(&text);
        assert_eq!(stands.stands_for.len(), 2);
        assert!(stands.stands_for.iter().all(|(c, _)| *c != '\u{e000}'));
        let value: serde_json::Value = serde_json::from_str(&protected).unwrap();
        assert_eq!(value[0], "\u{e000}");
        assert_eq!(stands.restore(&serde_json::to_string(&value).unwrap()), format!(r#"["{}","\ud83d","\udc00"]"#, '\u{e000}'));
    }
}

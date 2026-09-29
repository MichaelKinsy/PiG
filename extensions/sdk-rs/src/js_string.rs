//! JavaScript strings retain UTF-16 code units, including unpaired surrogates.
use serde::{Deserialize, Deserializer, Serialize, Serializer};
use serde_json::value::RawValue;
use std::fmt::Write;

/// A JavaScript string. Use `as_units` for lossless access and `to_string_lossy`
/// only when a Unicode-scalar Rust string is required for display.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct JsString(Vec<u16>);

impl JsString {
    pub fn from_units(units: Vec<u16>) -> Self {
        Self(units)
    }
    pub fn as_units(&self) -> &[u16] {
        &self.0
    }
    pub fn to_string_lossy(&self) -> String {
        String::from_utf16_lossy(&self.0)
    }
    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }
    pub fn to_string(&self) -> Result<String, std::string::FromUtf16Error> {
        String::from_utf16(&self.0)
    }
}
impl From<&str> for JsString {
    fn from(text: &str) -> Self {
        Self(text.encode_utf16().collect())
    }
}
impl From<String> for JsString {
    fn from(text: String) -> Self {
        Self::from(text.as_str())
    }
}
impl From<&String> for JsString {
    fn from(text: &String) -> Self {
        Self::from(text.as_str())
    }
}
impl From<&JsString> for JsString {
    fn from(text: &JsString) -> Self {
        text.clone()
    }
}
impl PartialEq<str> for JsString {
    fn eq(&self, other: &str) -> bool {
        self.0.iter().copied().eq(other.encode_utf16())
    }
}
impl PartialEq<&str> for JsString {
    fn eq(&self, other: &&str) -> bool {
        self == *other
    }
}

// RawValue keeps serde_json from converting through String or Value, neither of
// which can represent a lone surrogate. The output is still a JSON string.
impl Serialize for JsString {
    fn serialize<S: Serializer>(&self, serializer: S) -> Result<S::Ok, S::Error> {
        let mut json = String::from("\"");
        let mut scalar_text = String::new();
        for ch in char::decode_utf16(self.0.iter().copied()) {
            match ch {
                Ok(ch) => scalar_text.push(ch),
                Err(err) => {
                    append_scalar_json(&mut json, &scalar_text);
                    scalar_text.clear();
                    write!(&mut json, "\\u{:04x}", err.unpaired_surrogate()).unwrap();
                }
            }
        }
        append_scalar_json(&mut json, &scalar_text);
        json.push('"');
        RawValue::from_string(json)
            .map_err(serde::ser::Error::custom)?
            .serialize(serializer)
    }
}
fn append_scalar_json(out: &mut String, text: &str) {
    let encoded = serde_json::to_string(text).expect("str is JSON");
    out.push_str(&encoded[1..encoded.len() - 1]);
}

impl<'de> Deserialize<'de> for JsString {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        let raw = Box::<RawValue>::deserialize(deserializer)?;
        let text = raw.get();
        let Some(text) = text.strip_prefix('"').and_then(|s| s.strip_suffix('"')) else {
            return Err(serde::de::Error::custom("expected a JSON string"));
        };
        let mut units = Vec::new();
        let mut chars = text.chars();
        while let Some(ch) = chars.next() {
            let ch = if ch == '\\' {
                match chars.next() {
                    Some('u') => {
                        let mut unit = 0u16;
                        for _ in 0..4 {
                            let digit =
                                chars.next().and_then(|c| c.to_digit(16)).ok_or_else(|| {
                                    serde::de::Error::custom("invalid Unicode escape")
                                })?;
                            unit = (unit << 4) | digit as u16;
                        }
                        units.push(unit);
                        continue;
                    }
                    Some('"') => '"',
                    Some('\\') => '\\',
                    Some('/') => '/',
                    Some('b') => '\u{8}',
                    Some('f') => '\u{c}',
                    Some('n') => '\n',
                    Some('r') => '\r',
                    Some('t') => '\t',
                    _ => return Err(serde::de::Error::custom("invalid JSON escape")),
                }
            } else {
                ch
            };
            units.extend_from_slice(ch.encode_utf16(&mut [0; 2]));
        }
        Ok(Self(units))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn pi_utf16_strings_round_trip() {
        for (json, units) in [
            (r#""""#, vec![]),
            (r#""A""#, vec![65]),
            (r#""\ud83d""#, vec![0xd83d]),
            (r#""\ude00""#, vec![0xde00]),
            (r#""😀""#, vec![0xd83d, 0xde00]),
            (r#""\ud83d\ude00""#, vec![0xd83d, 0xde00]),
            (r#""A\ud83dZ\ude00""#, vec![65, 0xd83d, 90, 0xde00]),
            (r#""\\ud83d""#, "\\ud83d".encode_utf16().collect()),
        ] {
            let value: JsString = serde_json::from_str(json).unwrap();
            assert_eq!(value.as_units(), units);
            let encoded = serde_json::to_string(&value).unwrap();
            assert_eq!(serde_json::from_str::<JsString>(&encoded).unwrap(), value);
        }
        assert_eq!(
            JsString::from(&String::from("ordinary")),
            JsString::from("ordinary")
        );
        let high = JsString::from_units(vec![0xd83d]);
        assert_eq!(JsString::from(&high), high);
        assert!(JsString::from_units(vec![0xd83d]).to_string().is_err());
        assert_eq!(JsString::from_units(vec![0xd83d]).to_string_lossy(), "�");
        assert!(serde_json::from_str::<JsString>("null").is_err());
    }
}

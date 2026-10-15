use super::*;

// JavaScript's JSON.parse reads each number as the nearest double. serde_json's default parser can land one unit in the last place away, which changes the
// value an extension reads from a frame and every digit it prints (a Rust extension reported 123456789012345670000 where Node reports 123456789012345680000).
#[test]
fn frame_numbers_decode_to_the_nearest_double() {
    for text in [
        "123456789012345680000",
        "1.2345678901234568e+20",
        "0.1",
        "0.30000000000000004",
        "5e-324",
        "1.7976931348623157e308",
        "2.2250738585072011e-308",
        "9007199254740993",
        "1e23",
        "8.41e21",
        "4.35",
        "0.000001234567890123456789",
        "123456789.12345678912345",
    ] {
        let value: serde_json::Value = from_json_str(text).unwrap();
        let want: f64 = text.parse().unwrap();
        assert_eq!(value.as_f64().map(f64::to_bits), Some(want.to_bits()), "{text}");
    }
}

// JSON.parse reads 1e400 as Infinity, which the frames Pi writes spell null. serde_json refused the number, and a refused frame ended the connection: a
// model's tool arguments of {"v":1e400} stopped the extension for every later call. The number is null, a string that spells one is untouched, and a
// number that underflows is 0.
#[test]
fn a_number_outside_the_double_range_is_null_not_a_refused_frame() {
    let value: serde_json::Value = from_json_str(r#"{"v":1e400,"w":[-1e999,1e-400,"1e400",2.5,{"k":1E+309}]}"#).unwrap();
    assert_eq!(value, serde_json::json!({"v": null, "w": [null, 0.0, "1e400", 2.5, {"k": null}]}));
    assert!(from_json_str::<serde_json::Value>(r#"{"v":1e400"#).is_err());
}

// JSON.parse takes 1e400 at any depth. A frame nested past serde_json's 128 levels is decoded again on the deep path, and an out-of-range number met there
// was the deep decode's error, so the frame was refused and the extension ended.
#[test]
fn a_number_outside_the_double_range_is_null_past_the_recursion_limit() {
    let deep = format!("{}1e400{}", "[".repeat(200), "]".repeat(200));
    let value: serde_json::Value = from_json_str(&deep).unwrap();
    let mut inner = &value;
    for _ in 0..199 {
        inner = &inner[0];
    }
    assert_eq!(inner, &serde_json::json!([null]));
    let shallow_first = format!(r#"{{"v":1e400,"w":{}0{}}}"#, "[".repeat(200), "]".repeat(200));
    assert!(from_json_str::<serde_json::Value>(&shallow_first).is_ok());
}

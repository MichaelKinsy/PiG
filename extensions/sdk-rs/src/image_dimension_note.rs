//! Pi's `formatDimensionNote` (`utils/image-resize.ts:115-123`, exported from `index.ts`).

use crate::llm_convert::js_number_to_string;

/// The dimensions of Pi's `ResizedImage` (`utils/image-resize-core.ts`) that [`format_dimension_note`] reads. Dimensions are JavaScript numbers.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct ResizedImage {
    pub original_width: f64,
    pub original_height: f64,
    pub width: f64,
    pub height: f64,
    pub was_resized: bool,
}

/// Pi's `formatDimensionNote`: the note that tells the model how a resized image's coordinates map to the original, and `None` when the image was not resized.
pub fn format_dimension_note(result: &ResizedImage) -> Option<String> {
    if !result.was_resized {
        return None;
    }
    let scale = result.original_width / result.width;
    Some(format!(
        "[Image: original {}x{}, displayed at {}x{}. Multiply coordinates by {} to map to original image.]",
        js_number_to_string(result.original_width),
        js_number_to_string(result.original_height),
        js_number_to_string(result.width),
        js_number_to_string(result.height),
        js_to_fixed_2(scale)
    ))
}

/// `Number.prototype.toFixed(2)`: the exact decimal value of the double rounded half up in magnitude (a tie goes to the larger n, which Rust's half-even
/// formatting does not do for 0.125), `-` for any x < 0 even when it rounds to zero, and `Number::toString` from 1e21 on.
fn js_to_fixed_2(value: f64) -> String {
    if value.is_nan() {
        return "NaN".to_string();
    }
    if value.abs() >= 1e21 {
        return js_number_to_string(value);
    }
    let sign = if value < 0.0 { "-" } else { "" };
    let exact = format!("{:.1100}", value.abs());
    let (whole, fraction) = exact.split_once('.').unwrap_or((&exact, ""));
    let mut digits: Vec<u8> = format!("{whole}{}", &fraction[..2]).into_bytes();
    if fraction.as_bytes()[2] >= b'5' {
        let mut index = digits.len();
        loop {
            if index == 0 {
                digits.insert(0, b'1');
                break;
            }
            index -= 1;
            if digits[index] != b'9' {
                digits[index] += 1;
                break;
            }
            digits[index] = b'0';
        }
    }
    let text = String::from_utf8(digits).unwrap_or_default();
    let split = text.len() - 2;
    format!("{sign}{}.{}", &text[..split], &text[split..])
}

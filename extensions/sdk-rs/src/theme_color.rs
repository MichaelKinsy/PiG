//! The part of pi-tui's color model that renders a color as an SGR sequence.
//!
//! Ports `.upstream/v0.99.2/packages/tui/src/colors.ts` (`foregroundAnsi`, `backgroundAnsi`, `styleTextWithAnsi`) and `oklab.ts` (`oklabToLinearSrgb`). Oklab is Björn Ottosson's color space (https://bottosson.github.io/posts/colorpicker/, MIT).

use crate::theme::{Color, TextAttributes};

type Rgb = (f64, f64, f64);

// Ottosson's reference constants, kept at the digits oklab.ts carries so the conversion is bit-identical to pi-tui's.
#[allow(clippy::excessive_precision)]
const LAB_TO_LMS: [[f64; 3]; 3] = [
    [1.0, 0.3963377773761749, 0.2158037573099136],
    [1.0, -0.1055613458156586, -0.0638541728258133],
    [1.0, -0.0894841775298119, -1.2914855480194092],
];
#[allow(clippy::excessive_precision)]
const LMS_TO_LINEAR_SRGB: [[f64; 3]; 3] = [
    [4.0767416360759583, -3.3077115392580629, 0.2309699031821043],
    [-1.2684379732850315, 2.6097573492876882, -0.341319376002657],
    [-0.0041960761386756, -0.7034186179359362, 1.7076146940746117],
];
const BASIC_COLORS: [Rgb; 16] = [
    (0.0, 0.0, 0.0),
    (128.0, 0.0, 0.0),
    (0.0, 128.0, 0.0),
    (128.0, 128.0, 0.0),
    (0.0, 0.0, 128.0),
    (128.0, 0.0, 128.0),
    (0.0, 128.0, 128.0),
    (192.0, 192.0, 192.0),
    (128.0, 128.0, 128.0),
    (255.0, 0.0, 0.0),
    (0.0, 255.0, 0.0),
    (255.0, 255.0, 0.0),
    (0.0, 0.0, 255.0),
    (255.0, 0.0, 255.0),
    (0.0, 255.0, 255.0),
    (255.0, 255.0, 255.0),
];
const CUBE_VALUES: [f64; 6] = [0.0, 95.0, 135.0, 175.0, 215.0, 255.0];

/// JavaScript's `Math.round`: halves round toward positive infinity.
fn js_round(value: f64) -> f64 {
    (value + 0.5).floor()
}

fn linear_to_srgb(value: f64) -> f64 {
    if value > 0.0031308 {
        1.055 * value.powf(1.0 / 2.4) - 0.055
    } else {
        12.92 * value
    }
}

fn oklab_to_linear_srgb(l: f64, a: f64, b: f64) -> [f64; 3] {
    let cubed = LAB_TO_LMS.map(|row| {
        let lms = row[0] * l + row[1] * a + row[2] * b;
        lms * lms * lms
    });
    LMS_TO_LINEAR_SRGB.map(|row| row[0] * cubed[0] + row[1] * cubed[1] + row[2] * cubed[2])
}

fn in_gamut(linear: [f64; 3]) -> bool {
    const EPSILON: f64 = 1e-7;
    linear
        .iter()
        .all(|channel| *channel >= -EPSILON && *channel <= 1.0 + EPSILON)
}

fn linear_to_rgb(linear: [f64; 3]) -> Rgb {
    let channel = |value: f64| js_round(linear_to_srgb(value).clamp(0.0, 1.0) * 255.0);
    (channel(linear[0]), channel(linear[1]), channel(linear[2]))
}

/// Maps an OKLCH color into sRGB, keeping its hue and reducing chroma until it fits the gamut; the achromatic color is the fallback.
fn oklch_to_rgb(l: f64, c: f64, h: f64) -> Rgb {
    let radians = (h * std::f64::consts::PI) / 180.0;
    let (sin, cos) = radians.sin_cos();
    let at_chroma = |chroma: f64| oklab_to_linear_srgb(l, chroma * cos, chroma * sin);

    let direct = at_chroma(c);
    if in_gamut(direct) {
        return linear_to_rgb(direct);
    }
    let mut linear = at_chroma(0.0);
    let (mut low, mut high) = (0.0, c);
    for _ in 0..20 {
        let chroma = (low + high) / 2.0;
        let candidate = at_chroma(chroma);
        if in_gamut(candidate) {
            low = chroma;
            linear = candidate;
        } else {
            high = chroma;
        }
    }
    linear_to_rgb(linear)
}

fn color_to_rgb(color: Color) -> Rgb {
    match color {
        Color::Indexed { index } => {
            let index = usize::from(index);
            if index < 16 {
                BASIC_COLORS[index]
            } else if index < 232 {
                let cube = index - 16;
                (
                    CUBE_VALUES[cube / 36],
                    CUBE_VALUES[(cube % 36) / 6],
                    CUBE_VALUES[cube % 6],
                )
            } else {
                let gray = (8 + (index - 232) * 10) as f64;
                (gray, gray, gray)
            }
        }
        Color::Rgb { r, g, b } => (r, g, b),
        Color::Oklch { l, c, h } => oklch_to_rgb(l, c, h),
    }
}

fn find_closest(values: &[f64], target: f64) -> usize {
    let (mut closest, mut distance) = (0, f64::INFINITY);
    for (index, value) in values.iter().enumerate() {
        let d = (target - value).abs();
        if d < distance {
            closest = index;
            distance = d;
        }
    }
    closest
}

fn color_distance(first: Rgb, second: Rgb) -> f64 {
    let (dr, dg, db) = (first.0 - second.0, first.1 - second.1, first.2 - second.2);
    dr * dr * 0.299 + dg * dg * 0.587 + db * db * 0.114
}

fn rgb_to_ansi256(color: Rgb) -> usize {
    let r_index = find_closest(&CUBE_VALUES, color.0);
    let g_index = find_closest(&CUBE_VALUES, color.1);
    let b_index = find_closest(&CUBE_VALUES, color.2);
    let cube_color = (
        CUBE_VALUES[r_index],
        CUBE_VALUES[g_index],
        CUBE_VALUES[b_index],
    );
    let cube_index = 16 + 36 * r_index + 6 * g_index + b_index;

    let grays: Vec<f64> = (0..24).map(|i| (8 + i * 10) as f64).collect();
    let gray = js_round(0.299 * color.0 + 0.587 * color.1 + 0.114 * color.2);
    let gray_offset = find_closest(&grays, gray);
    let gray_value = grays[gray_offset];
    let spread = color.0.max(color.1).max(color.2) - color.0.min(color.1).min(color.2);
    if spread < 10.0
        && color_distance(color, (gray_value, gray_value, gray_value))
            < color_distance(color, cube_color)
    {
        return 232 + gray_offset;
    }
    cube_index
}

/// The SGR sequence selecting `color` as the foreground or background in `mode`.
pub(crate) fn color_ansi(color: Color, mode: &str, background: bool) -> String {
    let layer = if background { 48 } else { 38 };
    if let Color::Indexed { index } = color {
        return format!("\x1b[{layer};5;{index}m");
    }
    let rgb = color_to_rgb(color);
    if mode == "truecolor" {
        return format!(
            "\x1b[{layer};2;{};{};{}m",
            js_round(rgb.0),
            js_round(rgb.1),
            js_round(rgb.2)
        );
    }
    format!("\x1b[{layer};5;{}m", rgb_to_ansi256(rgb))
}

/// Wraps `text` in the color sequences and attributes, closing them in reverse order of opening. An empty sequence is unset.
pub(crate) fn style_text_with_ansi(
    text: &str,
    fg_ansi: &str,
    bg_ansi: &str,
    attributes: TextAttributes,
) -> String {
    let mut prefix = String::new();
    let mut suffix = String::new();
    if !fg_ansi.is_empty() {
        prefix.push_str(fg_ansi);
        suffix = "\x1b[39m".to_string();
    }
    if !bg_ansi.is_empty() {
        prefix.push_str(bg_ansi);
        suffix = format!("\x1b[49m{suffix}");
    }
    if attributes.bold {
        prefix.push_str("\x1b[1m");
    }
    if attributes.dim {
        prefix.push_str("\x1b[2m");
    }
    if attributes.bold || attributes.dim {
        suffix = format!("\x1b[22m{suffix}");
    }
    for (on, open, close) in [
        (attributes.italic, 3, 23),
        (attributes.underline, 4, 24),
        (attributes.inverse, 7, 27),
        (attributes.strikethrough, 9, 29),
    ] {
        if on {
            prefix.push_str(&format!("\x1b[{open}m"));
            suffix = format!("\x1b[{close}m{suffix}");
        }
    }
    format!("{prefix}{text}{suffix}")
}

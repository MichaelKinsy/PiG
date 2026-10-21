//! pi-tui's `colors.ts` and `oklab.ts`: colors, OKLCH/OKHSL conversion, mixing and ANSI styling.
//!
//! A port of the pinned Pi release. JavaScript semantics the numbers depend on are kept explicitly: `Math.max` and `Math.min`
//! propagate NaN, `Math.round` rounds halves toward +Infinity, and a number prints in JavaScript's shortest form.

use std::f64::consts::PI;
use std::fmt;

/// The error of a color constructor or parser; its text is the message Pi's `Error` carries.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ColorError(pub String);

impl fmt::Display for ColorError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for ColorError {}

fn err<T>(message: String) -> Result<T, ColorError> {
    Err(ColorError(message))
}

/// `Number.prototype.toString()`.
pub fn js_number_string(value: f64) -> String {
    if value.is_nan() {
        return "NaN".into();
    }
    if value.is_infinite() {
        return if value > 0.0 { "Infinity" } else { "-Infinity" }.into();
    }
    if value == 0.0 {
        return "0".into();
    }
    let sign = if value < 0.0 { "-" } else { "" };
    let sci = format!("{:e}", value.abs());
    let (mantissa, exp) = sci.split_once('e').unwrap_or((&sci, "0"));
    let digits: String = mantissa.chars().filter(|c| *c != '.').collect();
    let n: i32 = exp.parse::<i32>().unwrap_or(0) + 1; // value = 0.DIGITS x 10**n
    let k = digits.len() as i32;
    if k <= n && n <= 21 {
        return format!("{sign}{digits}{}", "0".repeat((n - k) as usize));
    }
    if 0 < n && n <= 21 {
        return format!("{sign}{}.{}", &digits[..n as usize], &digits[n as usize..]);
    }
    if -6 < n && n <= 0 {
        return format!("{sign}0.{}{digits}", "0".repeat((-n) as usize));
    }
    let e = n - 1;
    let mantissa = if k > 1 { format!("{}.{}", &digits[..1], &digits[1..]) } else { digits.clone() };
    format!("{sign}{mantissa}e{}{}", if e < 0 { '-' } else { '+' }, e.abs())
}

fn js_round(value: f64) -> f64 {
    if value.is_nan() || value.is_infinite() || value == 0.0 {
        return value;
    }
    if value > 0.0 && value < 0.5 {
        return 0.0;
    }
    if (-0.5..0.0).contains(&value) {
        return -0.0;
    }
    let rounded = value.floor();
    if value - rounded >= 0.5 { rounded + 1.0 } else { rounded }
}

fn jmax(a: f64, b: f64) -> f64 {
    if a.is_nan() || b.is_nan() { f64::NAN } else if a > b { a } else { b }
}

fn jmin(a: f64, b: f64) -> f64 {
    if a.is_nan() || b.is_nan() { f64::NAN } else if a < b { a } else { b }
}

/// pi-tui's `Color`: an ANSI 256-color palette index, an sRGB color (channels 0-255) or an OKLCH color.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Color {
    Indexed(u8),
    Rgb { r: f64, g: f64, b: f64 },
    Oklch { l: f64, c: f64, h: f64 },
}

/// pi-tui's `RgbColor`: the channels [`color_to_rgb`] returns.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct RgbColor {
    pub r: f64,
    pub g: f64,
    pub b: f64,
}

/// pi-tui's `OklchChannels`.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct OklchChannels {
    pub l: f64,
    pub c: f64,
    pub h: f64,
}

/// pi-tui's `OkhslChannels`.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct OkhslChannels {
    pub h: f64,
    pub s: f64,
    pub l: f64,
}

/// pi-tui's `TerminalColorMode`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TerminalColorMode {
    Color256,
    TrueColor,
}

/// pi-tui's `ColorMixSpace`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ColorMixSpace {
    Oklch,
    Srgb,
}

/// pi-tui's `TextAttributes`.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct TextAttributes {
    pub bold: bool,
    pub dim: bool,
    pub italic: bool,
    pub underline: bool,
    pub inverse: bool,
    pub strikethrough: bool,
}

/// pi-tui's `TextStyle`: [`TextAttributes`] plus an optional foreground and background color.
#[derive(Debug, Clone, Copy, Default, PartialEq)]
pub struct TextStyle {
    pub attributes: TextAttributes,
    pub fg: Option<Color>,
    pub bg: Option<Color>,
}

fn require_finite(value: f64, name: &str) -> Result<(), ColorError> {
    if value.is_finite() { Ok(()) } else { err(format!("{name} must be finite")) }
}

/// pi-tui's `indexedColor`.
pub fn indexed_color(index: f64) -> Result<Color, ColorError> {
    if !index.is_finite() || index.fract() != 0.0 || !(0.0..=255.0).contains(&index) {
        return err(format!("ANSI color index must be an integer from 0 to 255: {}", js_number_string(index)));
    }
    Ok(Color::Indexed(index as u8))
}

/// pi-tui's `rgbColor`.
pub fn rgb_color(r: f64, g: f64, b: f64) -> Result<Color, ColorError> {
    for (name, value) in [("r", r), ("g", g), ("b", b)] {
        require_finite(value, name)?;
        if !(0.0..=255.0).contains(&value) {
            return err(format!("{name} must be between 0 and 255: {}", js_number_string(value)));
        }
    }
    Ok(Color::Rgb { r, g, b })
}

/// pi-tui's `oklchColor`.
pub fn oklch_color(l: f64, c: f64, h: f64) -> Result<Color, ColorError> {
    require_finite(l, "l")?;
    require_finite(c, "c")?;
    require_finite(h, "h")?;
    if !(0.0..=1.0).contains(&l) {
        return err(format!("l must be between 0 and 1: {}", js_number_string(l)));
    }
    if c < 0.0 {
        return err(format!("c must not be negative: {}", js_number_string(c)));
    }
    Ok(Color::Oklch { l, c, h: ((h % 360.0) + 360.0) % 360.0 })
}

type V3 = [f64; 3];
type M3 = [V3; 3];

const LINEAR_SRGB_TO_LMS: M3 = [
    [0.4122214694707629, 0.5363325372617349, 0.0514459932675022],
    [0.2119034958178251, 0.6806995506452344, 0.1073969535369405],
    [0.0883024591900564, 0.2817188391361215, 0.6299787016738222],
];
const LMS_TO_LAB: M3 = [
    [0.210454268309314, 0.793617774702305, -0.0040720430116193],
    [1.9779985324311684, -2.42859224204858, 0.450593709617411],
    [0.0259040424655478, 0.7827717124575296, -0.8086757549230774],
];
const LAB_TO_LMS: M3 = [
    [1.0, 0.3963377773761749, 0.2158037573099136],
    [1.0, -0.1055613458156586, -0.0638541728258133],
    [1.0, -0.0894841775298119, -1.2914855480194092],
];
const LMS_TO_LINEAR_SRGB: M3 = [
    [4.0767416360759583, -3.3077115392580629, 0.2309699031821043],
    [-1.2684379732850315, 2.6097573492876882, -0.341319376002657],
    [-0.0041960761386756, -0.7034186179359362, 1.7076146940746117],
];
const SATURATION_FIT: [([f64; 2], [f64; 5]); 3] = [
    ([-1.8817031, -0.80936501], [1.19086277, 1.76576728, 0.59662641, 0.75515197, 0.56771245]),
    ([1.8144408, -1.19445267], [0.73956515, -0.45954404, 0.08285427, 0.12541073, -0.14503204]),
    ([0.13110758, 1.81333971], [1.35733652, -0.00915799, -1.1513021, -0.50559606, 0.00692167]),
];
const K1: f64 = 0.206;
const K2: f64 = 0.03;

fn k3() -> f64 {
    (1.0 + K1) / (1.0 + K2)
}

fn multiply(m: &M3, v: V3) -> V3 {
    let [x, y, z] = v;
    [m[0][0] * x + m[0][1] * y + m[0][2] * z, m[1][0] * x + m[1][1] * y + m[1][2] * z, m[2][0] * x + m[2][1] * y + m[2][2] * z]
}

fn cube(v: f64) -> f64 {
    v.powf(3.0)
}

fn oklab_to_okhsl_lightness(x: f64) -> f64 {
    0.5 * (k3() * x - K1 + ((k3() * x - K1).powf(2.0) + 4.0 * K2 * k3() * x).sqrt())
}

fn okhsl_to_oklab_lightness(x: f64) -> f64 {
    (x * x + K1 * x) / (k3() * (x + K2))
}

fn linear_to_srgb(value: f64) -> f64 {
    if value > 0.0031308 { 1.055 * value.powf(1.0 / 2.4) - 0.055 } else { 12.92 * value }
}

fn srgb_to_linear(value: f64) -> f64 {
    if value <= 0.04045 { value / 12.92 } else { ((value + 0.055) / 1.055).powf(2.4) }
}

fn oklab_to_linear_srgb(lab: V3) -> V3 {
    let lms = multiply(&LAB_TO_LMS, lab);
    multiply(&LMS_TO_LINEAR_SRGB, [cube(lms[0]), cube(lms[1]), cube(lms[2])])
}

fn rgb_to_oklab(rgb: RgbColor) -> V3 {
    let linear = [srgb_to_linear(rgb.r / 255.0), srgb_to_linear(rgb.g / 255.0), srgb_to_linear(rgb.b / 255.0)];
    let lms = multiply(&LINEAR_SRGB_TO_LMS, linear);
    multiply(&LMS_TO_LAB, [lms[0].cbrt(), lms[1].cbrt(), lms[2].cbrt()])
}

fn linear_srgb_to_rgb(linear: V3) -> RgbColor {
    let ch = |v: f64| js_round(jmin(1.0, jmax(0.0, linear_to_srgb(v))) * 255.0);
    RgbColor { r: ch(linear[0]), g: ch(linear[1]), b: ch(linear[2]) }
}

fn lms_slopes(a: f64, b: f64) -> V3 {
    [LAB_TO_LMS[0][1] * a + LAB_TO_LMS[0][2] * b, LAB_TO_LMS[1][1] * a + LAB_TO_LMS[1][2] * b, LAB_TO_LMS[2][1] * a + LAB_TO_LMS[2][2] * b]
}

fn max_saturation(a: f64, b: f64) -> f64 {
    let mut channel = 2;
    for (index, (xy, _)) in SATURATION_FIT.iter().enumerate() {
        if index == 2 || xy[0] * a + xy[1] * b > 1.0 {
            channel = index;
            break;
        }
    }
    let [k0, k1, k2, k3, k4] = SATURATION_FIT[channel].1;
    let weights = LMS_TO_LINEAR_SRGB[channel];
    let saturation = k0 + k1 * a + k2 * b + k3 * a * a + k4 * a * b;
    let slopes = lms_slopes(a, b);
    let base = [1.0 + saturation * slopes[0], 1.0 + saturation * slopes[1], 1.0 + saturation * slopes[2]];
    let dot = |values: [f64; 3]| {
        let mut total = 0.0;
        for (index, value) in values.iter().enumerate() {
            total += weights[index] * value;
        }
        total
    };
    let f = dot([cube(base[0]), cube(base[1]), cube(base[2])]);
    let f1 = dot([3.0 * slopes[0] * base[0].powf(2.0), 3.0 * slopes[1] * base[1].powf(2.0), 3.0 * slopes[2] * base[2].powf(2.0)]);
    let f2 = dot([6.0 * slopes[0].powf(2.0) * base[0], 6.0 * slopes[1].powf(2.0) * base[1], 6.0 * slopes[2].powf(2.0) * base[2]]);
    saturation - (f * f1) / (f1 * f1 - 0.5 * f * f2)
}

fn cusp(a: f64, b: f64) -> (f64, f64) {
    let saturation = max_saturation(a, b);
    let lms = oklab_to_linear_srgb([1.0, saturation * a, saturation * b]);
    let peak = jmax(jmax(lms[0], lms[1]), lms[2]);
    let lightness = (1.0 / peak).cbrt();
    (lightness, lightness * saturation)
}

fn max_chroma(a: f64, b: f64, lightness: f64, cusp: (f64, f64)) -> f64 {
    let (cusp_l, cusp_c) = cusp;
    if lightness <= cusp_l {
        return (cusp_c * lightness) / cusp_l;
    }
    let t = (cusp_c * (lightness - 1.0)) / (cusp_l - 1.0);
    let slopes = lms_slopes(a, b);
    let lms = [lightness + t * slopes[0], lightness + t * slopes[1], lightness + t * slopes[2]];
    let cubes = [cube(lms[0]), cube(lms[1]), cube(lms[2])];
    let first = [3.0 * slopes[0] * lms[0].powf(2.0), 3.0 * slopes[1] * lms[1].powf(2.0), 3.0 * slopes[2] * lms[2].powf(2.0)];
    let second = [6.0 * slopes[0].powf(2.0) * lms[0], 6.0 * slopes[1].powf(2.0) * lms[1], 6.0 * slopes[2].powf(2.0) * lms[2]];
    let dot = |row: V3, v: [f64; 3]| row[0] * v[0] + row[1] * v[1] + row[2] * v[2];
    let mut smallest = f64::INFINITY;
    for (n, row) in LMS_TO_LINEAR_SRGB.iter().enumerate() {
        let f = dot(*row, cubes) - 1.0;
        let f1 = dot(*row, first);
        let f2 = dot(*row, second);
        let u = f1 / (f1 * f1 - 0.5 * f * f2);
        let step = if u >= 0.0 { -f * u } else { f64::MAX };
        smallest = if n == 0 { step } else { jmin(smallest, step) };
    }
    t + smallest
}

fn chroma_stops(lightness: f64, a: f64, b: f64) -> (f64, f64, f64) {
    let peak = cusp(a, b);
    let c_max = max_chroma(a, b, lightness, peak);
    let k = c_max / jmin(lightness * (peak.1 / peak.0), (1.0 - lightness) * (peak.1 / (1.0 - peak.0)));
    let mid_s = 0.11516993
        + 1.0
            / (7.4477897
                + 4.1590124 * b
                + a * (-2.19557347 + 1.75198401 * b + a * (-2.13704948 - 10.02301043 * b + a * (-4.24894561 + 5.38770819 * b + 4.69891013 * a))));
    let mid_t = 0.11239642
        + 1.0
            / (1.6132032 - 0.68124379 * b
                + a * (0.40370612 + 0.90148123 * b + a * (-0.27087943 + 0.6122399 * b + a * (0.00299215 - 0.45399568 * b - 0.14661872 * a))));
    let c_mid = 0.9 * k * (1.0 / (1.0 / (lightness * mid_s).powf(4.0) + 1.0 / ((1.0 - lightness) * mid_t).powf(4.0))).sqrt().sqrt();
    let c0 = (1.0 / (1.0 / (lightness * 0.4).powf(2.0) + 1.0 / ((1.0 - lightness) * 0.8).powf(2.0))).sqrt();
    (c0, c_mid, c_max)
}

fn okhsl_to_rgb(hue: f64, saturation: f64, lightness: f64) -> RgbColor {
    let big_l = okhsl_to_oklab_lightness(lightness);
    let mut lab = [big_l, 0.0, 0.0];
    if big_l > 0.0 && big_l < 1.0 && saturation > 0.0 {
        let angle = (2.0 * PI * (((hue % 360.0) + 360.0) % 360.0)) / 360.0;
        let a = angle.cos();
        let b = angle.sin();
        let (c0, c_mid, c_max) = chroma_stops(big_l, a, b);
        let chroma = if saturation < 0.8 {
            let t = 1.25 * saturation;
            let k1 = 0.8 * c0;
            (t * k1) / (1.0 - (1.0 - k1 / c_mid) * t)
        } else {
            let t = 5.0 * (saturation - 0.8);
            let k1 = (0.2 * c_mid.powf(2.0) * 1.25f64.powf(2.0)) / c0;
            c_mid + (t * k1) / (1.0 - (1.0 - k1 / (c_max - c_mid)) * t)
        };
        lab = [big_l, chroma * a, chroma * b];
    }
    linear_srgb_to_rgb(oklab_to_linear_srgb(lab))
}

fn rgb_to_okhsl(rgb: RgbColor) -> OkhslChannels {
    let [big_l, lab_a, lab_b] = rgb_to_oklab(rgb);
    let chroma = lab_a.hypot(lab_b);
    let lightness = oklab_to_okhsl_lightness(big_l);
    if chroma < 1e-9 || lightness <= 0.0 || lightness >= 1.0 {
        return OkhslChannels { h: 0.0, s: 0.0, l: lightness };
    }
    let hue = ((lab_b.atan2(lab_a) * 180.0) / PI + 360.0) % 360.0;
    let (c0, c_mid, c_max) = chroma_stops(big_l, lab_a / chroma, lab_b / chroma);
    let saturation = if chroma < c_mid {
        let k1 = 0.8 * c0;
        0.8 * (chroma / (k1 + (1.0 - k1 / c_mid) * chroma))
    } else {
        let k1 = (0.2 * c_mid.powf(2.0) * 1.25f64.powf(2.0)) / c0;
        let offset = chroma - c_mid;
        0.8 + 0.2 * (offset / (k1 + (1.0 - k1 / (c_max - c_mid)) * offset))
    };
    OkhslChannels { h: hue, s: jmin(1.0, jmax(0.0, saturation)), l: lightness }
}

/// pi-tui's `okhslColor`: an OKHSL color converted to sRGB.
pub fn okhsl_color(h: f64, s: f64, l: f64) -> Result<Color, ColorError> {
    require_finite(h, "h")?;
    require_finite(s, "s")?;
    require_finite(l, "l")?;
    if !(0.0..=1.0).contains(&s) {
        return err(format!("s must be between 0 and 1: {}", js_number_string(s)));
    }
    if !(0.0..=1.0).contains(&l) {
        return err(format!("l must be between 0 and 1: {}", js_number_string(l)));
    }
    let rgb = okhsl_to_rgb(h, s, l);
    rgb_color(rgb.r, rgb.g, rgb.b)
}

/// pi-tui's `colorToOkhsl`.
pub fn color_to_okhsl(color: Color) -> OkhslChannels {
    rgb_to_okhsl(color_to_rgb(color))
}

/// The argument of [`parse_color`]: pi-tui's `string | number`.
#[derive(Debug, Clone, Copy)]
pub enum ColorValue<'a> {
    Text(&'a str),
    Number(f64),
}

impl<'a> From<&'a str> for ColorValue<'a> {
    fn from(value: &'a str) -> Self {
        ColorValue::Text(value)
    }
}
impl<'a> From<&'a String> for ColorValue<'a> {
    fn from(value: &'a String) -> Self {
        ColorValue::Text(value)
    }
}
impl From<f64> for ColorValue<'_> {
    fn from(value: f64) -> Self {
        ColorValue::Number(value)
    }
}
impl From<i32> for ColorValue<'_> {
    fn from(value: i32) -> Self {
        ColorValue::Number(f64::from(value))
    }
}
impl From<u8> for ColorValue<'_> {
    fn from(value: u8) -> Self {
        ColorValue::Number(f64::from(value))
    }
}

struct Scanner<'a> {
    s: &'a [u8],
    i: usize,
}

impl Scanner<'_> {
    fn eat_ci(&mut self, word: &str) -> bool {
        let w = word.as_bytes();
        if self.s.len() - self.i >= w.len() && self.s[self.i..self.i + w.len()].eq_ignore_ascii_case(w) {
            self.i += w.len();
            true
        } else {
            false
        }
    }

    fn eat(&mut self, byte: u8) -> bool {
        if self.s.get(self.i) == Some(&byte) {
            self.i += 1;
            true
        } else {
            false
        }
    }

    /// JavaScript `\s` over UTF-8 text, as many characters as match.
    fn spaces(&mut self) -> usize {
        let rest = std::str::from_utf8(&self.s[self.i..]).unwrap_or("");
        let mut n = 0;
        for c in rest.chars() {
            if !super::text::is_js_space(c) {
                break;
            }
            n += c.len_utf8();
        }
        self.i += n;
        rest.chars().take_while(|c| super::text::is_js_space(*c)).count()
    }

    /// `[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:e[+-]?\d+)?`
    fn number(&mut self) -> Option<f64> {
        let start = self.i;
        let mut j = self.i;
        let b = self.s;
        if matches!(b.get(j), Some(b'+') | Some(b'-')) {
            j += 1;
        }
        let int_start = j;
        while b.get(j).is_some_and(u8::is_ascii_digit) {
            j += 1;
        }
        if j > int_start {
            if b.get(j) == Some(&b'.') {
                j += 1;
                while b.get(j).is_some_and(u8::is_ascii_digit) {
                    j += 1;
                }
            }
        } else if b.get(j) == Some(&b'.') && b.get(j + 1).is_some_and(u8::is_ascii_digit) {
            j += 1;
            while b.get(j).is_some_and(u8::is_ascii_digit) {
                j += 1;
            }
        } else {
            return None;
        }
        if matches!(b.get(j), Some(b'e') | Some(b'E')) {
            let mut k = j + 1;
            if matches!(b.get(k), Some(b'+') | Some(b'-')) {
                k += 1;
            }
            let digits = k;
            while b.get(k).is_some_and(u8::is_ascii_digit) {
                k += 1;
            }
            if k > digits {
                j = k;
            }
        }
        self.i = j;
        std::str::from_utf8(&b[start..j]).ok()?.parse::<f64>().ok()
    }

    fn done(&self) -> bool {
        self.i == self.s.len()
    }
}

fn parse_oklch(value: &str) -> Option<(f64, f64, f64)> {
    let mut sc = Scanner { s: value.as_bytes(), i: 0 };
    if !sc.eat_ci("oklch(") {
        return None;
    }
    sc.spaces();
    let lightness = sc.number()?;
    let percent = sc.eat(b'%');
    if sc.spaces() == 0 {
        return None;
    }
    let chroma = sc.number()?;
    if sc.spaces() == 0 {
        return None;
    }
    let hue = sc.number()?;
    sc.eat_ci("deg");
    sc.spaces();
    if !sc.eat(b')') || !sc.done() {
        return None;
    }
    Some((lightness / if percent { 100.0 } else { 1.0 }, chroma, hue))
}

fn parse_okhsl(value: &str) -> Option<(f64, f64, f64)> {
    let mut sc = Scanner { s: value.as_bytes(), i: 0 };
    if !sc.eat_ci("okhsl(") {
        return None;
    }
    sc.spaces();
    let hue = sc.number()?;
    sc.eat_ci("deg");
    if sc.spaces() == 0 {
        return None;
    }
    let saturation = sc.number()?;
    let s_percent = sc.eat(b'%');
    if sc.spaces() == 0 {
        return None;
    }
    let lightness = sc.number()?;
    let l_percent = sc.eat(b'%');
    sc.spaces();
    if !sc.eat(b')') || !sc.done() {
        return None;
    }
    Some((hue, saturation / if s_percent { 100.0 } else { 1.0 }, lightness / if l_percent { 100.0 } else { 1.0 }))
}

/// pi-tui's `parseColor`: a color from `"#rgb"`, `"#rrggbb"`, `"oklch(...)"`, `"okhsl(...)"` or a palette index.
pub fn parse_color<'a>(value: impl Into<ColorValue<'a>>) -> Result<Color, ColorError> {
    let value = match value.into() {
        ColorValue::Number(n) => return indexed_color(n),
        ColorValue::Text(t) => t,
    };
    if let Some(hex) = value.strip_prefix('#') {
        if (hex.len() == 3 || hex.len() == 6) && hex.bytes().all(|b| b.is_ascii_hexdigit()) {
            let digits: String = if hex.len() == 3 { hex.chars().flat_map(|c| [c, c]).collect() } else { hex.to_string() };
            let channel = |i: usize| f64::from(u8::from_str_radix(&digits[i..i + 2], 16).unwrap_or(0));
            return rgb_color(channel(0), channel(2), channel(4));
        }
    }
    if let Some((l, c, h)) = parse_oklch(value) {
        return oklch_color(l, c, h);
    }
    if let Some((h, s, l)) = parse_okhsl(value) {
        return okhsl_color(h, s, l);
    }
    err(format!("Invalid color value: {value}"))
}

const BASIC_COLORS: [(f64, f64, f64); 16] = [
    (0.0, 0.0, 0.0), (128.0, 0.0, 0.0), (0.0, 128.0, 0.0), (128.0, 128.0, 0.0), (0.0, 0.0, 128.0), (128.0, 0.0, 128.0), (0.0, 128.0, 128.0),
    (192.0, 192.0, 192.0), (128.0, 128.0, 128.0), (255.0, 0.0, 0.0), (0.0, 255.0, 0.0), (255.0, 255.0, 0.0), (0.0, 0.0, 255.0),
    (255.0, 0.0, 255.0), (0.0, 255.0, 255.0), (255.0, 255.0, 255.0),
];
const CUBE_VALUES: [f64; 6] = [0.0, 95.0, 135.0, 175.0, 215.0, 255.0];

fn indexed_to_rgb(index: u8) -> RgbColor {
    let index = usize::from(index);
    if index < 16 {
        let (r, g, b) = BASIC_COLORS[index];
        return RgbColor { r, g, b };
    }
    if index < 232 {
        let cube = index - 16;
        return RgbColor { r: CUBE_VALUES[cube / 36], g: CUBE_VALUES[(cube % 36) / 6], b: CUBE_VALUES[cube % 6] };
    }
    let gray = (8 + (index - 232) * 10) as f64;
    RgbColor { r: gray, g: gray, b: gray }
}

fn in_gamut(linear: V3) -> bool {
    let epsilon = 1e-7;
    linear.iter().all(|c| *c >= -epsilon && *c <= 1.0 + epsilon)
}

fn oklch_to_rgb(l: f64, c: f64, h: f64) -> RgbColor {
    let radians = (h * PI) / 180.0;
    let cos = radians.cos();
    let sin = radians.sin();
    let at_chroma = |chroma: f64| oklab_to_linear_srgb([l, chroma * cos, chroma * sin]);
    let direct = at_chroma(c);
    if in_gamut(direct) {
        return linear_srgb_to_rgb(direct);
    }
    let mut linear = at_chroma(0.0);
    let mut low = 0.0;
    let mut high = c;
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
    linear_srgb_to_rgb(linear)
}

/// pi-tui's `colorToRgb`.
pub fn color_to_rgb(color: Color) -> RgbColor {
    match color {
        Color::Indexed(i) => indexed_to_rgb(i),
        Color::Rgb { r, g, b } => RgbColor { r, g, b },
        Color::Oklch { l, c, h } => oklch_to_rgb(l, c, h),
    }
}

/// pi-tui's `colorToOklch`.
pub fn color_to_oklch(color: Color) -> OklchChannels {
    if let Color::Oklch { l, c, h } = color {
        return OklchChannels { l, c, h };
    }
    let [l, a, b] = rgb_to_oklab(color_to_rgb(color));
    OklchChannels { l, c: a.hypot(b), h: ((b.atan2(a) * 180.0) / PI + 360.0) % 360.0 }
}

/// pi-tui's `colorToHex`.
pub fn color_to_hex(color: Color) -> String {
    let rgb = color_to_rgb(color);
    let channel = |v: f64| format!("{:02x}", js_round(v) as i64);
    format!("#{}{}{}", channel(rgb.r), channel(rgb.g), channel(rgb.b))
}

/// pi-tui's `mixColors`: `first` blended toward `second` by `amount` in `space`.
pub fn mix_colors(first: Color, second: Color, amount: f64, space: ColorMixSpace) -> Result<Color, ColorError> {
    require_finite(amount, "amount")?;
    if !(0.0..=1.0).contains(&amount) {
        return err(format!("amount must be between 0 and 1: {}", js_number_string(amount)));
    }
    if space == ColorMixSpace::Srgb {
        let a = color_to_rgb(first);
        let b = color_to_rgb(second);
        return rgb_color(a.r + (b.r - a.r) * amount, a.g + (b.g - a.g) * amount, a.b + (b.b - a.b) * amount);
    }
    let a = color_to_oklch(first);
    let b = color_to_oklch(second);
    let first_hue = if a.c < 1e-7 { b.h } else { a.h };
    let second_hue = if b.c < 1e-7 { first_hue } else { b.h };
    let hue_delta = ((second_hue - first_hue + 540.0) % 360.0) - 180.0;
    oklch_color(a.l + (b.l - a.l) * amount, a.c + (b.c - a.c) * amount, first_hue + hue_delta * amount)
}

fn find_closest(values: &[f64], target: f64) -> usize {
    let mut closest_index = 0;
    let mut closest_distance = f64::INFINITY;
    for (index, value) in values.iter().enumerate() {
        let distance = (target - value).abs();
        if distance < closest_distance {
            closest_index = index;
            closest_distance = distance;
        }
    }
    closest_index
}

fn color_distance(first: RgbColor, second: RgbColor) -> f64 {
    let dr = first.r - second.r;
    let dg = first.g - second.g;
    let db = first.b - second.b;
    dr * dr * 0.299 + dg * dg * 0.587 + db * db * 0.114
}

fn rgb_to_ansi256(color: RgbColor) -> usize {
    let gray_values: Vec<f64> = (0..24).map(|i| (8 + i * 10) as f64).collect();
    let r_index = find_closest(&CUBE_VALUES, color.r);
    let g_index = find_closest(&CUBE_VALUES, color.g);
    let b_index = find_closest(&CUBE_VALUES, color.b);
    let cube_color = RgbColor { r: CUBE_VALUES[r_index], g: CUBE_VALUES[g_index], b: CUBE_VALUES[b_index] };
    let cube_index = 16 + 36 * r_index + 6 * g_index + b_index;
    let gray = js_round(0.299 * color.r + 0.587 * color.g + 0.114 * color.b);
    let gray_offset = find_closest(&gray_values, gray);
    let gray_value = gray_values[gray_offset];
    let spread = color.r.max(color.g).max(color.b) - color.r.min(color.g).min(color.b);
    if spread < 10.0 && color_distance(color, RgbColor { r: gray_value, g: gray_value, b: gray_value }) < color_distance(color, cube_color) {
        return 232 + gray_offset;
    }
    cube_index
}

fn color_ansi(color: Color, mode: TerminalColorMode, background: bool) -> String {
    let layer = if background { 48 } else { 38 };
    if let Color::Indexed(i) = color {
        return format!("\x1b[{layer};5;{i}m");
    }
    let rgb = color_to_rgb(color);
    if mode == TerminalColorMode::TrueColor {
        return format!(
            "\x1b[{layer};2;{};{};{}m",
            js_number_string(js_round(rgb.r)),
            js_number_string(js_round(rgb.g)),
            js_number_string(js_round(rgb.b))
        );
    }
    format!("\x1b[{layer};5;{}m", rgb_to_ansi256(rgb))
}

/// pi-tui's `foregroundAnsi`.
pub fn foreground_ansi(color: Color, mode: TerminalColorMode) -> String {
    color_ansi(color, mode, false)
}

/// pi-tui's `backgroundAnsi`.
pub fn background_ansi(color: Color, mode: TerminalColorMode) -> String {
    color_ansi(color, mode, true)
}

/// pi-tui's `styleText`: `text` wrapped in the SGR sequences of `options`.
pub fn style_text(text: &str, options: TextStyle, mode: TerminalColorMode) -> String {
    let fg = options.fg.map(|c| foreground_ansi(c, mode));
    let bg = options.bg.map(|c| background_ansi(c, mode));
    style_text_with_ansi(text, fg.as_deref(), bg.as_deref(), options.attributes)
}

/// pi-tui's `styleTextWithAnsi`: like [`style_text`] with precomputed color sequences, e.g. cached theme colors.
pub fn style_text_with_ansi(text: &str, fg_ansi: Option<&str>, bg_ansi: Option<&str>, options: TextAttributes) -> String {
    let mut prefix = String::new();
    let mut suffix = String::new();
    if let Some(fg) = fg_ansi.filter(|s| !s.is_empty()) {
        prefix.push_str(fg);
        suffix = "\x1b[39m".into();
    }
    if let Some(bg) = bg_ansi.filter(|s| !s.is_empty()) {
        prefix.push_str(bg);
        suffix = format!("\x1b[49m{suffix}");
    }
    if options.bold {
        prefix.push_str("\x1b[1m");
    }
    if options.dim {
        prefix.push_str("\x1b[2m");
    }
    if options.bold || options.dim {
        suffix = format!("\x1b[22m{suffix}");
    }
    if options.italic {
        prefix.push_str("\x1b[3m");
        suffix = format!("\x1b[23m{suffix}");
    }
    if options.underline {
        prefix.push_str("\x1b[4m");
        suffix = format!("\x1b[24m{suffix}");
    }
    if options.inverse {
        prefix.push_str("\x1b[7m");
        suffix = format!("\x1b[27m{suffix}");
    }
    if options.strikethrough {
        prefix.push_str("\x1b[9m");
        suffix = format!("\x1b[29m{suffix}");
    }
    format!("{prefix}{text}{suffix}")
}

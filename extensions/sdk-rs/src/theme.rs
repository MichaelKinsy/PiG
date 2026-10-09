//! Upstream `ctx.ui.theme`: the host's active theme palette.
//!
//! The host replicates its theme into the extension as a palette of escape
//! sequences by token (the ready state's `theme`, then `state_update` and
//! `theme_change` notifies). The methods below apply that palette exactly as
//! the Node runtime's `ThemeShim` does, so every SDK colors text alike.

use crate::theme_color::{color_ansi, style_text_with_ansi};
use serde_json::Value;
use std::collections::HashMap;

/// Upstream `Theme` as a subprocess extension sees it: the host's active
/// theme name and source path, and styling over the host's palette.
#[derive(Debug, Clone, PartialEq)]
pub struct Theme {
    /// The theme's name, or empty when the host reported none.
    pub name: String,
    /// The file the theme was loaded from, `None` for a built-in theme.
    pub source_path: Option<String>,
    foregrounds: HashMap<String, String>,
    backgrounds: HashMap<String, String>,
    modifiers: bool,
    mode: String,
    appearance: Option<ThemeAppearance>,
    colors: HashMap<String, Color>,
}

/// Ends the opening of a faint foreground token in the host's palette.
const FAINT_SGR: &str = "\x1b[2m";

/// The background a theme is designed for (upstream `ThemeAppearance`).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ThemeAppearance {
    Light,
    Dark,
}

/// A concrete color (upstream pi-tui `Color`): an ANSI palette index, an sRGB color with channels 0-255, or an OKLCH color.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum Color {
    Indexed { index: u8 },
    Rgb { r: f64, g: f64, b: f64 },
    Oklch { l: f64, c: f64, h: f64 },
}

/// The text attributes `Theme::style` applies (upstream pi-tui `TextAttributes`).
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct TextAttributes {
    pub bold: bool,
    pub dim: bool,
    pub italic: bool,
    pub underline: bool,
    pub inverse: bool,
    pub strikethrough: bool,
}

/// One slot of a `ThemeStyle`: a theme token or a concrete color (upstream's `fg?: ThemeColor | Color`).
#[derive(Debug, Clone, PartialEq)]
pub enum ThemeSlot {
    Token(String),
    Color(Color),
}

/// The style `Theme::style` applies (upstream `ThemeStyle`): a foreground and a background, each a token accepted only in its own slot or a concrete color, and text attributes.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct ThemeStyle {
    pub attributes: TextAttributes,
    pub fg: Option<ThemeSlot>,
    pub bg: Option<ThemeSlot>,
}

/// A text styling function returned by [`Theme::get_thinking_border_color`]
/// and [`Theme::get_bash_mode_border_color`].
pub type ThemeColorFn = Box<dyn Fn(&str) -> String + Send + Sync>;

impl Default for Theme {
    /// The palette before the host has sent one: no colors, modifiers drawn,
    /// truecolor mode (the Node runtime's `ThemeShim` initial state).
    fn default() -> Self {
        Self {
            name: String::new(),
            source_path: None,
            foregrounds: HashMap::new(),
            backgrounds: HashMap::new(),
            modifiers: true,
            mode: "truecolor".to_string(),
            appearance: None,
            colors: HashMap::new(),
        }
    }
}

/// A well-formed pi-tui `Color` from its wire object, `None` for anything else.
fn color_from_value(value: &Value) -> Option<Color> {
    let number = |key: &str| {
        value
            .get(key)
            .and_then(Value::as_f64)
            .filter(|n| n.is_finite())
    };
    match value.get("kind").and_then(Value::as_str)? {
        "indexed" => {
            let index = number("index")?;
            (index.fract() == 0.0 && (0.0..=255.0).contains(&index))
                .then_some(Color::Indexed { index: index as u8 })
        }
        "rgb" => Some(Color::Rgb {
            r: number("r")?,
            g: number("g")?,
            b: number("b")?,
        }),
        "oklch" => Some(Color::Oklch {
            l: number("l")?,
            c: number("c")?,
            h: number("h")?,
        }),
        _ => None,
    }
}

fn string_map(value: Option<&Value>) -> HashMap<String, String> {
    value
        .and_then(Value::as_object)
        .map(|map| {
            map.iter()
                .filter_map(|(token, ansi)| ansi.as_str().map(|a| (token.clone(), a.to_string())))
                .collect()
        })
        .unwrap_or_default()
}

impl Theme {
    /// Builds a theme from the host's palette
    /// `{"name","sourcePath","foregrounds","backgrounds","modifiers","mode","appearance","colors"}`,
    /// as `ThemeShim.setPalette` does.
    pub(crate) fn from_palette(palette: &Value) -> Self {
        Self {
            name: palette
                .get("name")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_string(),
            source_path: palette
                .get("sourcePath")
                .and_then(Value::as_str)
                .filter(|path| !path.is_empty())
                .map(str::to_string),
            foregrounds: string_map(palette.get("foregrounds")),
            backgrounds: string_map(palette.get("backgrounds")),
            modifiers: palette.get("modifiers") != Some(&Value::Bool(false)),
            mode: if palette.get("mode").and_then(Value::as_str) == Some("256color") {
                "256color".to_string()
            } else {
                "truecolor".to_string()
            },
            appearance: match palette.get("appearance").and_then(Value::as_str) {
                Some("light") => Some(ThemeAppearance::Light),
                Some("dark") => Some(ThemeAppearance::Dark),
                _ => None,
            },
            // A color of an unknown kind is not a color; it is dropped, as a token without an escape sequence is.
            colors: palette
                .get("colors")
                .and_then(Value::as_object)
                .map(|map| {
                    map.iter()
                        .filter_map(|(token, value)| {
                            color_from_value(value).map(|c| (token.clone(), c))
                        })
                        .collect()
                })
                .unwrap_or_default(),
        }
    }

    fn modifier(&self, open: &str, close: &str, text: &str) -> String {
        if self.modifiers {
            format!("{open}{text}{close}")
        } else {
            text.to_string()
        }
    }

    /// Colors `text` with the foreground of `token`. A token the palette lacks
    /// panics with upstream's `Unknown theme color: <token>` error once the host
    /// has sent a palette; before that the text stays unstyled.
    pub fn fg(&self, token: &str, text: &str) -> String {
        match self.foregrounds.get(token).filter(|open| !open.is_empty()) {
            Some(open) => {
                // The host opens a faint token with SGR 2 (theme.ts:399-402); Pi's fg closes it with SGR 22;39 (theme.ts:363).
                let close = if open.ends_with("\x1b[2m") {
                    "\x1b[22;39m"
                } else {
                    "\x1b[39m"
                };
                format!("{open}{text}{close}")
            }
            None => self.unknown_or_unstyled(token, text),
        }
    }

    /// Colors `text` with the background of `token`. A token the palette lacks
    /// panics with upstream's `Unknown theme color: <token>` error once the host
    /// has sent a palette; before that the text stays unstyled.
    pub fn bg(&self, token: &str, text: &str) -> String {
        match self.backgrounds.get(token).filter(|open| !open.is_empty()) {
            Some(open) => format!("{open}{text}\x1b[49m"),
            None => self.unknown_or_unstyled(token, text),
        }
    }

    /// A token the palette lacks is upstream's `Unknown theme color` throw (theme.ts:374); a theme the host has not sent a palette to has no tokens to lack.
    fn unknown_or_unstyled(&self, token: &str, text: &str) -> String {
        if self.foregrounds.is_empty() && self.backgrounds.is_empty() {
            return text.to_string();
        }
        panic!("Unknown theme color: {token}");
    }

    pub fn bold(&self, text: &str) -> String {
        self.modifier("\x1b[1m", "\x1b[22m", text)
    }

    pub fn italic(&self, text: &str) -> String {
        self.modifier("\x1b[3m", "\x1b[23m", text)
    }

    pub fn underline(&self, text: &str) -> String {
        self.modifier("\x1b[4m", "\x1b[24m", text)
    }

    pub fn inverse(&self, text: &str) -> String {
        self.modifier("\x1b[7m", "\x1b[27m", text)
    }

    pub fn strikethrough(&self, text: &str) -> String {
        self.modifier("\x1b[9m", "\x1b[29m", text)
    }

    /// The foreground escape sequence of `color`, or upstream's
    /// `Unknown theme color: <color>` error.
    pub fn get_fg_ansi(&self, color: &str) -> Result<String, String> {
        self.foregrounds
            .get(color)
            .filter(|ansi| !ansi.is_empty())
            .cloned()
            .ok_or_else(|| format!("Unknown theme color: {color}"))
    }

    /// The background escape sequence of `color`, or upstream's
    /// `Unknown theme color: <color>` error.
    pub fn get_bg_ansi(&self, color: &str) -> Result<String, String> {
        self.backgrounds
            .get(color)
            .filter(|ansi| !ansi.is_empty())
            .cloned()
            .ok_or_else(|| format!("Unknown theme color: {color}"))
    }

    /// The background the theme is designed for, or `None` before the host has sent a palette. The host resolves it with the palette (upstream `Theme.appearance`).
    pub fn appearance(&self) -> Option<ThemeAppearance> {
        self.appearance
    }

    /// A concrete color for every theme token (upstream `Theme.colors`), resolved by the host with the palette. The map is a copy.
    pub fn colors(&self) -> HashMap<String, Color> {
        self.colors.clone()
    }

    /// Renders `text` in `style` (upstream `Theme.style`). An unknown token, or a token in the wrong slot, is upstream's `Unknown theme color: <token>` error. The attributes are drawn whatever the host's chalk level is.
    pub fn style(&self, text: &str, style: &ThemeStyle) -> Result<String, String> {
        let mut attributes = style.attributes;
        let mut fg_ansi = String::new();
        match &style.fg {
            Some(ThemeSlot::Token(token)) => {
                let ansi = self
                    .foregrounds
                    .get(token)
                    .filter(|ansi| !ansi.is_empty())
                    .ok_or_else(|| format!("Unknown theme color: {token}"))?;
                // The palette's foreground appends SGR 2 to a faint token's color; a color never ends in it.
                match ansi.strip_suffix(FAINT_SGR) {
                    Some(open) => {
                        fg_ansi = open.to_string();
                        attributes.dim = true;
                    }
                    None => fg_ansi = ansi.clone(),
                }
            }
            Some(ThemeSlot::Color(color)) => fg_ansi = color_ansi(*color, &self.mode, false),
            None => {}
        }
        let bg_ansi = match &style.bg {
            Some(ThemeSlot::Token(token)) => self
                .backgrounds
                .get(token)
                .filter(|ansi| !ansi.is_empty())
                .ok_or_else(|| format!("Unknown theme color: {token}"))?
                .clone(),
            Some(ThemeSlot::Color(color)) => color_ansi(*color, &self.mode, true),
            None => String::new(),
        };
        Ok(style_text_with_ansi(text, &fg_ansi, &bg_ansi, attributes))
    }

    /// `"truecolor"` or `"256color"`.
    pub fn get_color_mode(&self) -> String {
        self.mode.clone()
    }

    /// The border color for a thinking level (`off`, `minimal`, `low`,
    /// `medium`, `high`, `xhigh`, `max`); an unknown level uses `off`'s.
    pub fn get_thinking_border_color(&self, level: &str) -> ThemeColorFn {
        let token = match level {
            "minimal" => "thinkingMinimal",
            "low" => "thinkingLow",
            "medium" => "thinkingMedium",
            "high" => "thinkingHigh",
            "xhigh" => "thinkingXhigh",
            "max" => "thinkingMax",
            _ => "thinkingOff",
        };
        let theme = self.clone();
        Box::new(move |text| theme.fg(token, text))
    }

    /// The border color of the editor in bash mode.
    pub fn get_bash_mode_border_color(&self) -> ThemeColorFn {
        let theme = self.clone();
        Box::new(move |text| theme.fg("bashMode", text))
    }
}

/// Replicated UI state: upstream `ctx.hasUI` and `ctx.ui.theme`.
#[derive(Debug, Clone)]
pub(crate) struct UiState {
    pub(crate) has_ui: bool,
    pub(crate) theme: Theme,
}

impl Default for UiState {
    /// An unbound runner has no UI until a host snapshot binds one.
    fn default() -> Self {
        Self {
            has_ui: false,
            theme: Theme::default(),
        }
    }
}

impl UiState {
    /// Applies a state snapshot (the ready state or a `state_update`): its
    /// `hasUI` when a boolean and its `theme` when an object.
    pub(crate) fn apply_state(&mut self, state: &serde_json::Map<String, Value>) {
        if let Some(has_ui) = state.get("hasUI").and_then(Value::as_bool) {
            self.has_ui = has_ui;
        }
        if let Some(theme) = state.get("theme").filter(|theme| theme.is_object()) {
            self.theme = Theme::from_palette(theme);
        }
    }

    /// Applies a `theme_change` notify's palette. A palette sent as a JSON
    /// string is decoded, and one that is not an object clears the palette,
    /// as the Node runtime does.
    pub(crate) fn apply_theme_change(&mut self, args: Option<&Value>) {
        let decoded;
        let palette = match args {
            Some(Value::String(raw)) => {
                decoded = serde_json::from_str::<Value>(raw).unwrap_or(Value::Null);
                &decoded
            }
            Some(value) => value,
            None => &Value::Null,
        };
        self.theme = Theme::from_palette(palette);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn palette() -> Value {
        json!({
            "name": "dark",
            "sourcePath": "/themes/dark.json",
            "foregrounds": {"accent": "\x1b[38;5;1m", "thinkingHigh": "<h>", "bashMode": "<b>"},
            "backgrounds": {"selectedBg": "\x1b[48;5;2m"},
            "modifiers": true,
            "mode": "256color"
        })
    }

    #[test]
    fn applies_the_palette_like_the_node_theme_shim() {
        let theme = Theme::from_palette(&palette());
        assert_eq!(theme.name, "dark");
        assert_eq!(theme.source_path.as_deref(), Some("/themes/dark.json"));
        assert_eq!(theme.fg("accent", "x"), "\x1b[38;5;1mx\x1b[39m");
        assert_eq!(theme.bg("selectedBg", "x"), "\x1b[48;5;2mx\x1b[49m");
        assert_eq!(theme.bold("x"), "\x1b[1mx\x1b[22m");
        assert_eq!(theme.strikethrough("x"), "\x1b[9mx\x1b[29m");
        assert_eq!(theme.get_fg_ansi("accent").unwrap(), "\x1b[38;5;1m");
        assert_eq!(
            theme.get_fg_ansi("nope").unwrap_err(),
            "Unknown theme color: nope"
        );
        assert_eq!(
            theme.get_bg_ansi("nope").unwrap_err(),
            "Unknown theme color: nope"
        );
        assert_eq!(theme.get_color_mode(), "256color");
        assert_eq!(theme.get_thinking_border_color("high")("t"), "<h>t\x1b[39m");
        assert_eq!(theme.get_bash_mode_border_color()("t"), "<b>t\x1b[39m");

        let plain = Theme::from_palette(&json!({"modifiers": false, "sourcePath": ""}));
        assert_eq!(plain.italic("x"), "x");
        assert_eq!(plain.source_path, None);
        assert_eq!(plain.get_color_mode(), "truecolor");
    }

    // Theme.appearance, Theme.colors and Theme.style over the host's palette (.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/theme/theme.ts:311-367). The vectors are the escape sequences the host's own tui.ForegroundAnsi and BackgroundAnsi produce for each color in each mode.
    fn style_palette(mode: &str) -> Value {
        json!({
            "name": "dark",
            "appearance": "light",
            "foregrounds": {"success": "\x1b[38;2;1;2;3m", "dimmed": "\x1b[38;2;9;9;9m\x1b[2m", "accent": "\x1b[38;5;4m"},
            "backgrounds": {"toolSuccessBg": "\x1b[48;2;4;5;6m", "userMessageBg": "\x1b[49m"},
            "colors": {
                "success": {"kind": "rgb", "r": 1, "g": 2, "b": 3},
                "accent": {"kind": "oklch", "l": 0.62, "c": 0.1, "h": 200},
                "toolSuccessBg": {"kind": "indexed", "index": 5}
            },
            "modifiers": true,
            "mode": mode
        })
    }

    fn token(name: &str) -> Option<ThemeSlot> {
        Some(ThemeSlot::Token(name.to_string()))
    }

    fn color(color: Color) -> Option<ThemeSlot> {
        Some(ThemeSlot::Color(color))
    }

    #[test]
    fn appearance_and_colors_come_from_the_palette() {
        let theme = Theme::from_palette(&style_palette("truecolor"));
        assert_eq!(theme.appearance(), Some(ThemeAppearance::Light));
        let mut want = HashMap::new();
        want.insert(
            "success".to_string(),
            Color::Rgb {
                r: 1.0,
                g: 2.0,
                b: 3.0,
            },
        );
        want.insert(
            "accent".to_string(),
            Color::Oklch {
                l: 0.62,
                c: 0.1,
                h: 200.0,
            },
        );
        want.insert("toolSuccessBg".to_string(), Color::Indexed { index: 5 });
        assert_eq!(theme.colors(), want);
        assert_eq!(Theme::default().appearance(), None);
        assert!(Theme::default().colors().is_empty());

        // A color of an unknown kind is dropped, as a token without an escape sequence is.
        let sparse = Theme::from_palette(
            &json!({"appearance": "dark", "colors": {"b": {"kind": "rgb", "r": 1, "g": 2, "b": 3}, "bad": {"kind": "nope"}, "worse": "x"}}),
        );
        assert_eq!(sparse.appearance(), Some(ThemeAppearance::Dark));
        assert_eq!(sparse.colors().len(), 1);
        assert_eq!(
            sparse.colors()["b"],
            Color::Rgb {
                r: 1.0,
                g: 2.0,
                b: 3.0
            }
        );
    }

    #[test]
    fn style_renders_tokens_attributes_and_colors() {
        let theme = Theme::from_palette(&style_palette("truecolor"));
        let all = TextAttributes {
            bold: true,
            dim: true,
            italic: true,
            underline: true,
            inverse: true,
            strikethrough: true,
        };
        let cases: Vec<(&str, ThemeStyle, String)> = vec![
            ("tokens and bold", ThemeStyle { attributes: TextAttributes { bold: true, ..Default::default() }, fg: token("success"), bg: token("toolSuccessBg") },
                "\x1b[38;2;1;2;3m\x1b[48;2;4;5;6m\x1b[1mx\x1b[22m\x1b[49m\x1b[39m".into()),
            ("no style", ThemeStyle::default(), "x".into()),
            ("every attribute", ThemeStyle { attributes: all, ..Default::default() },
                "\x1b[1m\x1b[2m\x1b[3m\x1b[4m\x1b[7m\x1b[9mx\x1b[29m\x1b[27m\x1b[24m\x1b[23m\x1b[22m".into()),
            ("faint token adds dim and closes with 22", ThemeStyle { fg: token("dimmed"), ..Default::default() }, "\x1b[38;2;9;9;9m\x1b[2mx\x1b[22m\x1b[39m".into()),
            ("rgb in truecolor", ThemeStyle { fg: color(Color::Rgb { r: 10.0, g: 20.0, b: 30.0 }), ..Default::default() }, "\x1b[38;2;10;20;30mx\x1b[39m".into()),
            ("fractional rgb rounds half up", ThemeStyle { fg: color(Color::Rgb { r: 10.5, g: 20.4, b: 29.6 }), ..Default::default() }, "\x1b[38;2;11;20;30mx\x1b[39m".into()),
            ("oklch is mapped into sRGB", ThemeStyle { fg: color(Color::Oklch { l: 0.62, c: 0.1, h: 200.0 }), ..Default::default() }, "\x1b[38;2;28;152;158mx\x1b[39m".into()),
            ("an out-of-gamut oklch keeps its hue by losing chroma", ThemeStyle { bg: color(Color::Oklch { l: 1.0, c: 0.3, h: 150.0 }), ..Default::default() }, "\x1b[48;2;255;255;255mx\x1b[49m".into()),
            ("indexed", ThemeStyle { fg: color(Color::Indexed { index: 5 }), ..Default::default() }, "\x1b[38;5;5mx\x1b[39m".into()),
        ];
        for (name, style, want) in cases {
            assert_eq!(theme.style("x", &style), Ok(want), "{name}");
        }
        // A color in 256-color mode is the nearest palette entry (theme.ts:342-367 foregroundAnsi).
        let palette = Theme::from_palette(&style_palette("256color"));
        for (c, want) in [
            (
                Color::Rgb {
                    r: 10.0,
                    g: 20.0,
                    b: 30.0,
                },
                "\x1b[38;5;16mx\x1b[39m",
            ),
            (
                Color::Rgb {
                    r: 128.0,
                    g: 128.0,
                    b: 130.0,
                },
                "\x1b[38;5;244mx\x1b[39m",
            ),
            (
                Color::Rgb {
                    r: 250.0,
                    g: 100.0,
                    b: 50.0,
                },
                "\x1b[38;5;203mx\x1b[39m",
            ),
            (
                Color::Oklch {
                    l: 0.62,
                    c: 0.1,
                    h: 200.0,
                },
                "\x1b[38;5;31mx\x1b[39m",
            ),
            (Color::Indexed { index: 5 }, "\x1b[38;5;5mx\x1b[39m"),
        ] {
            assert_eq!(
                palette.style(
                    "x",
                    &ThemeStyle {
                        fg: color(c),
                        ..Default::default()
                    }
                ),
                Ok(want.to_string()),
                "{c:?}"
            );
        }
    }

    #[test]
    fn style_rejects_unknown_tokens_and_tokens_in_the_wrong_slot() {
        // theme-style.test.ts:43-48.
        let theme = Theme::from_palette(&style_palette("truecolor"));
        for (style, want) in [
            (
                ThemeStyle {
                    fg: token("notAToken"),
                    ..Default::default()
                },
                "Unknown theme color: notAToken",
            ),
            (
                ThemeStyle {
                    fg: token("toolSuccessBg"),
                    ..Default::default()
                },
                "Unknown theme color: toolSuccessBg",
            ),
            (
                ThemeStyle {
                    bg: token("success"),
                    ..Default::default()
                },
                "Unknown theme color: success",
            ),
        ] {
            assert_eq!(theme.style("x", &style), Err(want.to_string()), "{style:?}");
        }
    }

    // theme.ts:361-376: fg and bg throw `Unknown theme color: <token>` for a token the palette lacks, one of the other slot included. Before any palette arrives the text stays unstyled.
    #[test]
    fn fg_and_bg_panic_for_an_unknown_token() {
        let theme = Theme::from_palette(&json!({
            "foregrounds": {"success": "\x1b[38;5;2m"},
            "backgrounds": {"toolSuccessBg": "\x1b[48;5;4m"},
            "modifiers": true,
            "mode": "256color"
        }));
        for (slot, token) in [("fg", "notAToken"), ("fg", "toolSuccessBg"), ("bg", "notAToken"), ("bg", "success")] {
            let caught = std::panic::catch_unwind(|| {
                if slot == "fg" { theme.fg(token, "x") } else { theme.bg(token, "x") }
            });
            let message = caught.expect_err("an unknown token panics").downcast::<String>().expect("a formatted message");
            assert_eq!(*message, format!("Unknown theme color: {token}"));
        }
        assert_eq!(theme.get_bg_ansi("notAToken"), Err("Unknown theme color: notAToken".to_string()));
        assert_eq!(Theme::default().fg("accent", "x"), "x");
    }

    // Pi 0.99.2 theme.ts:363 closes a faint token's foreground with SGR 22;39; the host opens it with SGR 2
    // after the color (theme.ts:399-402), so the faint run ends at the text.
    #[test]
    fn fg_closes_a_faint_token_with_sgr_22_39() {
        let theme = Theme::from_palette(&json!({
            "foregrounds": {
                "accent": "\x1b[38;5;5m",
                "muted": "\x1b[39m\x1b[2m",
                "thinkingXhigh": "\x1b[38;5;13m\x1b[2m",
                "bashMode": "\x1b[38;5;2m"
            },
            "backgrounds": {},
            "modifiers": true,
            "mode": "256color"
        }));
        assert_eq!(theme.fg("muted", "x"), "\x1b[39m\x1b[2mx\x1b[22;39m");
        assert_eq!(theme.fg("accent", "x"), "\x1b[38;5;5mx\x1b[39m");
        assert_eq!(
            theme.get_thinking_border_color("xhigh")("x"),
            "\x1b[38;5;13m\x1b[2mx\x1b[22;39m"
        );
        assert_eq!(
            theme.get_bash_mode_border_color()("x"),
            "\x1b[38;5;2mx\x1b[39m"
        );
    }
}

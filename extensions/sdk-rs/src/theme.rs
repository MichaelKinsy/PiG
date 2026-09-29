//! Upstream `ctx.ui.theme`: the host's active theme palette.
//!
//! The host replicates its theme into the extension as a palette of escape
//! sequences by token (the ready state's `theme`, then `state_update` and
//! `theme_change` notifies). The methods below apply that palette exactly as
//! the Node runtime's `ThemeShim` does, so every SDK colors text alike.

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
        }
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
    /// `{"name","sourcePath","foregrounds","backgrounds","modifiers","mode"}`,
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
        }
    }

    fn style(&self, open: &str, close: &str, text: &str) -> String {
        if self.modifiers {
            format!("{open}{text}{close}")
        } else {
            text.to_string()
        }
    }

    /// Colors `text` with the foreground of `token`. An unknown token leaves
    /// the text unstyled.
    pub fn fg(&self, token: &str, text: &str) -> String {
        match self.foregrounds.get(token).filter(|open| !open.is_empty()) {
            Some(open) => format!("{open}{text}\x1b[39m"),
            None => text.to_string(),
        }
    }

    /// Colors `text` with the background of `token`. An unknown token leaves
    /// the text unstyled.
    pub fn bg(&self, token: &str, text: &str) -> String {
        match self.backgrounds.get(token).filter(|open| !open.is_empty()) {
            Some(open) => format!("{open}{text}\x1b[49m"),
            None => text.to_string(),
        }
    }

    pub fn bold(&self, text: &str) -> String {
        self.style("\x1b[1m", "\x1b[22m", text)
    }

    pub fn italic(&self, text: &str) -> String {
        self.style("\x1b[3m", "\x1b[23m", text)
    }

    pub fn underline(&self, text: &str) -> String {
        self.style("\x1b[4m", "\x1b[24m", text)
    }

    pub fn inverse(&self, text: &str) -> String {
        self.style("\x1b[7m", "\x1b[27m", text)
    }

    pub fn strikethrough(&self, text: &str) -> String {
        self.style("\x1b[9m", "\x1b[29m", text)
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
    /// `Unknown theme background color: <color>` error.
    pub fn get_bg_ansi(&self, color: &str) -> Result<String, String> {
        self.backgrounds
            .get(color)
            .filter(|ansi| !ansi.is_empty())
            .cloned()
            .ok_or_else(|| format!("Unknown theme background color: {color}"))
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
        assert_eq!(theme.fg("missing", "x"), "x");
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
            "Unknown theme background color: nope"
        );
        assert_eq!(theme.get_color_mode(), "256color");
        assert_eq!(theme.get_thinking_border_color("high")("t"), "<h>t\x1b[39m");
        assert_eq!(theme.get_thinking_border_color("bogus")("t"), "t");
        assert_eq!(theme.get_bash_mode_border_color()("t"), "<b>t\x1b[39m");

        let plain = Theme::from_palette(&json!({"modifiers": false, "sourcePath": ""}));
        assert_eq!(plain.italic("x"), "x");
        assert_eq!(plain.source_path, None);
        assert_eq!(plain.get_color_mode(), "truecolor");
    }
}

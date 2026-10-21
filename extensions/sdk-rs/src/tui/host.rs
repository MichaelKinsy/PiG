//! The host's replicated pi-tui state: its keybinding table and terminal capabilities.

use std::sync::Mutex;

use serde_json::{Map, Value};

use crate::theme::Theme;

use super::keybindings::{KeybindingsManager, set_keybindings};
use super::terminal::{ImageProtocol, TerminalCapabilities, set_capabilities};

static INSTALLED_TABLE: Mutex<Option<Value>> = Mutex::new(None);

/// Install the snapshot's keybinding table as pi-tui's global keybindings, as Pi installs its manager with `setKeybindings` at startup,
/// and its resolved terminal capabilities as pi-tui's capability cache (the Node runtime's `applyKeybindings` and
/// `applyTerminalCapabilities`).
pub(crate) fn apply_host_state(state: &Map<String, Value>) {
    if let Some(palette) = state.get("theme").filter(|theme| theme.is_object()) {
        set_host_theme(palette);
    }
    if let Some(table) = state.get("keybindings").filter(|t| t.get("definitions").is_some_and(Value::is_object)) {
        let mut installed = INSTALLED_TABLE.lock().unwrap_or_else(|e| e.into_inner());
        if installed.as_ref() != Some(table) {
            *installed = Some(table.clone());
            set_keybindings(KeybindingsManager::from_wire(table));
        }
    }
    if let Some(caps) = state.get("terminalCapabilities").filter(|c| c.is_object()) {
        let images = match caps.get("images").and_then(Value::as_str) {
            Some("kitty") => Some(ImageProtocol::Kitty),
            Some("iterm2") => Some(ImageProtocol::ITerm2),
            _ => None,
        };
        set_capabilities(TerminalCapabilities {
            images,
            true_color: caps.get("trueColor").and_then(Value::as_bool) == Some(true),
            hyperlinks: caps.get("hyperlinks").and_then(Value::as_bool) == Some(true),
        });
    }
}

/// A global theme and the palette it was built from, which a host helper that draws with the theme (`highlightCode`) is given when the theme is not the host's active one.
struct GlobalTheme {
    theme: Theme,
    raw: Value,
}

/// The host's active theme as Pi's own process-global theme is: the pi-coding-agent helpers (`key_hint`, `get_markdown_theme`, ...) color with it. The host has
/// one theme, so extensions sharing a process share it.
static HOST_THEME: Mutex<Option<GlobalTheme>> = Mutex::new(None);
/// The theme `init_theme` installed, which stands for the global theme until the host's theme changes, as Pi's own theme change replaces it.
static INSTALLED_THEME: Mutex<Option<GlobalTheme>> = Mutex::new(None);

fn locked<T>(mutex: &Mutex<T>) -> std::sync::MutexGuard<'_, T> {
    mutex.lock().unwrap_or_else(|e| e.into_inner())
}

/// Record the palette the host published. A palette that differs from the last one the host published ends what `init_theme` installed; the same palette, which
/// every state snapshot repeats, leaves it. A value that is not an object is the empty palette (the Node runtime's `ThemeShim.setPalette`).
pub(crate) fn set_host_theme(palette: &Value) {
    let raw = if palette.is_object() { palette.clone() } else { Value::Null };
    let mut host = locked(&HOST_THEME);
    let changed = host.as_ref().is_some_and(|previous| previous.raw != raw);
    *host = Some(GlobalTheme { theme: Theme::from_palette(&raw), raw });
    if changed {
        *locked(&INSTALLED_THEME) = None;
    }
}

/// The global theme: the one `init_theme` installed, else the theme the host last published, or the empty palette (modifiers only) before one arrives.
pub fn current_host_theme() -> Theme {
    if let Some(installed) = locked(&INSTALLED_THEME).as_ref() {
        return installed.theme.clone();
    }
    locked(&HOST_THEME).as_ref().map(|host| host.theme.clone()).unwrap_or_default()
}

/// The palette of the theme `init_theme` installed, `None` while the global theme is the host's.
pub(crate) fn installed_theme_palette() -> Option<Value> {
    locked(&INSTALLED_THEME).as_ref().map(|installed| installed.raw.clone())
}

/// Make the palette the global theme until the host's theme changes.
pub(crate) fn install_theme(palette: Value) {
    *locked(&INSTALLED_THEME) = Some(GlobalTheme { theme: Theme::from_palette(&palette), raw: palette });
}

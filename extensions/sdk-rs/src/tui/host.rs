//! The host's replicated pi-tui state: its keybinding table and terminal capabilities.

use std::sync::Mutex;

use serde_json::{Map, Value};

use super::keybindings::{KeybindingsManager, set_keybindings};
use super::terminal::{ImageProtocol, TerminalCapabilities, set_capabilities};

static INSTALLED_TABLE: Mutex<Option<Value>> = Mutex::new(None);

/// Install the snapshot's keybinding table as pi-tui's global keybindings, as Pi installs its manager with `setKeybindings` at startup,
/// and its resolved terminal capabilities as pi-tui's capability cache (the Node runtime's `applyKeybindings` and
/// `applyTerminalCapabilities`).
pub(crate) fn apply_host_state(state: &Map<String, Value>) {
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

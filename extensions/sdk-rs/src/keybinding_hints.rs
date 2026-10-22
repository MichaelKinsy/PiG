//! pi-coding-agent's keybinding hints (`modes/interactive/components/keybinding-hints.ts`): a key identifier as the interface shows it, and the dim key plus muted
//! description of a hint such as "esc to interrupt". The keys come from the global keybindings the host published and the colors from the global theme.

use crate::tui::{current_host_theme, get_keybindings};

/// `KeyTextFormatOptions`.
#[derive(Debug, Clone, Copy, Default)]
pub struct KeyTextFormatOptions {
    /// Upper-case the first letter of each part ("Ctrl+Enter").
    pub capitalize: bool,
}

#[cfg(not(test))]
fn is_darwin() -> bool {
    cfg!(target_os = "macos")
}

#[cfg(test)]
thread_local! {
    /// The platform a test writes key hints for (Pi reads `process.platform`).
    pub(crate) static DARWIN: std::cell::Cell<bool> = const { std::cell::Cell::new(cfg!(target_os = "macos")) };
}

#[cfg(test)]
fn is_darwin() -> bool {
    DARWIN.with(|darwin| darwin.get())
}

fn format_key_part(darwin: bool, part: &str, capitalize: bool) -> String {
    // `part.toLowerCase() === "alt"` holds only for the ASCII case variants of "alt".
    let display = if darwin && part.eq_ignore_ascii_case("alt") { "option" } else { part };
    if !capitalize {
        return display.to_string();
    }
    // `charAt(0).toUpperCase()` upper-cases one UTF-16 unit: a character beyond the Basic Multilingual Plane is half a surrogate pair and stays as it is.
    let mut chars = display.chars();
    match chars.next() {
        Some(first) if (first as u32) <= 0xFFFF => first.to_uppercase().chain(chars).collect(),
        _ => display.to_string(),
    }
}

fn format_key_text_for(darwin: bool, key: &str, capitalize: bool) -> String {
    key.split('/')
        .map(|alternative| alternative.split('+').map(|part| format_key_part(darwin, part, capitalize)).collect::<Vec<_>>().join("+"))
        .collect::<Vec<_>>()
        .join("/")
}

fn format_keys(keys: &[String], capitalize: bool) -> String {
    if keys.is_empty() {
        return String::new();
    }
    format_key_text_for(is_darwin(), &keys.join("/"), capitalize)
}

/// pi-coding-agent's `formatKeyText`: a key identifier as the interface shows it, "alt" as "option" on macOS.
pub fn format_key_text(key: &str, options: KeyTextFormatOptions) -> String {
    format_key_text_for(is_darwin(), key, options.capitalize)
}

/// pi-coding-agent's `keyText`: the keys of a keybinding as the interface shows them.
pub fn key_text(keybinding: &str) -> String {
    format_keys(&get_keybindings().get_keys(keybinding), false)
}

/// pi-coding-agent's `keyDisplayText`: [`key_text`] with each part capitalized.
pub fn key_display_text(keybinding: &str) -> String {
    format_keys(&get_keybindings().get_keys(keybinding), true)
}

/// pi-coding-agent's `keyHint`: the keys of a keybinding in the dim color and `description` in the muted color of the host's theme.
pub fn key_hint(keybinding: &str, description: &str) -> String {
    let theme = current_host_theme();
    theme.fg("dim", &key_text(keybinding)) + &theme.fg("muted", &format!(" {description}"))
}

/// pi-coding-agent's `rawKeyHint`: a hint for a key identifier that is not a keybinding.
pub fn raw_key_hint(key: &str, description: &str) -> String {
    let theme = current_host_theme();
    theme.fg("dim", &format_key_text(key, KeyTextFormatOptions::default())) + &theme.fg("muted", &format!(" {description}"))
}

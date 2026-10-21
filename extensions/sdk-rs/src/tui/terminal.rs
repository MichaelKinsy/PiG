//! pi-tui's `terminal-image.ts` capability detection and `hyperlink`.

use std::sync::Mutex;

use super::colors::TerminalColorMode;
use super::fuzzy::js_lower;

/// pi-tui's `ImageProtocol` (`null` is `None` where it is optional).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ImageProtocol {
    Kitty,
    ITerm2,
}

/// pi-tui's `TerminalCapabilities`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct TerminalCapabilities {
    pub images: Option<ImageProtocol>,
    pub true_color: bool,
    pub hyperlinks: bool,
}

/// pi-tui's `Partial<TerminalCapabilities>` for `setCapabilityOverrides`. `None` is absent; `images: Some(None)` is upstream's `null`.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct CapabilityOverrides {
    pub images: Option<Option<ImageProtocol>>,
    pub true_color: Option<bool>,
    pub hyperlinks: Option<bool>,
}

/// pi-tui's `hyperlink`: `text` wrapped in an OSC 8 link to `url`.
pub fn hyperlink(text: &str, url: &str) -> String {
    format!("\x1b]8;;{url}\x1b\\{text}\x1b]8;;\x1b\\")
}

fn probe_tmux_hyperlinks() -> bool {
    use std::process::{Command, Stdio};
    use std::time::{Duration, Instant};
    let Ok(mut child) = Command::new("tmux")
        .args(["display-message", "-p", "#{client_termfeatures}"])
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
    else {
        return false;
    };
    let deadline = Instant::now() + Duration::from_millis(250);
    loop {
        match child.try_wait() {
            Ok(Some(status)) if status.success() => break,
            Ok(Some(_)) | Err(_) => return false,
            Ok(None) if Instant::now() >= deadline => {
                let _ = child.kill();
                let _ = child.wait();
                return false;
            }
            Ok(None) => std::thread::sleep(Duration::from_millis(5)),
        }
    }
    let Ok(out) = child.wait_with_output() else { return false };
    String::from_utf8_lossy(&out.stdout).split(',').any(|f| f.trim() == "hyperlinks")
}

fn detect_from_environment(env: &dyn Fn(&str) -> Option<String>, windows: bool, tmux_forwards_hyperlink: &dyn Fn() -> bool) -> TerminalCapabilities {
    let get = |key: &str| env(key).unwrap_or_default();
    let term_program = js_lower(&get("TERM_PROGRAM"));
    let terminal_emulator = js_lower(&get("TERMINAL_EMULATOR"));
    let term = js_lower(&get("TERM"));
    let color_term = js_lower(&get("COLORTERM"));
    let true_color_hint = color_term == "truecolor" || color_term == "24bit" || term.ends_with("-direct");
    let caps = |images, true_color, hyperlinks| TerminalCapabilities { images, true_color, hyperlinks };
    if !get("TMUX").is_empty() || term.starts_with("tmux") {
        return caps(None, true_color_hint, tmux_forwards_hyperlink());
    }
    if term.starts_with("screen") {
        return caps(None, true_color_hint, false);
    }
    if !get("KITTY_WINDOW_ID").is_empty() || term_program == "kitty" {
        return caps(Some(ImageProtocol::Kitty), true, true);
    }
    if term_program == "ghostty" || term.contains("ghostty") || !get("GHOSTTY_RESOURCES_DIR").is_empty() {
        return caps(Some(ImageProtocol::Kitty), true, true);
    }
    if !get("WEZTERM_PANE").is_empty() || term_program == "wezterm" {
        return caps(Some(ImageProtocol::Kitty), true, true);
    }
    if term_program == "warpterminal" || !get("WARP_SESSION_ID").is_empty() || !get("WARP_TERMINAL_SESSION_UUID").is_empty() {
        return caps(Some(ImageProtocol::Kitty), true, true);
    }
    if !get("ITERM_SESSION_ID").is_empty() || term_program == "iterm.app" {
        return caps(Some(ImageProtocol::ITerm2), true, true);
    }
    if !get("WT_SESSION").is_empty() {
        return caps(None, true, true);
    }
    if matches!(term_program.as_str(), "alacritty" | "vscode" | "zed") {
        return caps(None, true, true);
    }
    if terminal_emulator == "jetbrains-jediterm" {
        return caps(None, true, false);
    }
    if windows {
        return caps(None, true, false);
    }
    caps(None, true_color_hint, false)
}

fn boolean_override(value: Option<String>) -> Option<bool> {
    match value.as_deref() {
        Some("1") => Some(true),
        Some("0") => Some(false),
        _ => None,
    }
}

pub(crate) fn detect_in(env: &dyn Fn(&str) -> Option<String>, windows: bool, tmux_forwards_hyperlink: Option<&dyn Fn() -> bool>) -> TerminalCapabilities {
    let hyperlinks = boolean_override(env("PI_HYPERLINKS"));
    let default_probe = probe_tmux_hyperlinks;
    let probe: &dyn Fn() -> bool = tmux_forwards_hyperlink.unwrap_or(&default_probe);
    let forced = move || hyperlinks.unwrap_or(false);
    let mut detected = match hyperlinks {
        Some(_) => detect_from_environment(env, windows, &forced),
        None => detect_from_environment(env, windows, probe),
    };
    match env("PI_IMAGE_PROTOCOL").map(|v| js_lower(&v)).as_deref() {
        Some("kitty") => detected.images = Some(ImageProtocol::Kitty),
        Some("iterm2") => detected.images = Some(ImageProtocol::ITerm2),
        Some("none") | Some("0") => detected.images = None,
        _ => {}
    }
    if let Some(tc) = boolean_override(env("PI_TRUE_COLOR")) {
        detected.true_color = tc;
    }
    if let Some(h) = hyperlinks {
        detected.hyperlinks = h;
    }
    detected
}

/// pi-tui's `detectCapabilities`: the terminal's capabilities from the environment. `PI_HYPERLINKS`, `PI_IMAGE_PROTOCOL` and
/// `PI_TRUE_COLOR` override detection; without `tmux_forwards_hyperlink` a tmux session is probed.
pub fn detect_capabilities(tmux_forwards_hyperlink: Option<&dyn Fn() -> bool>) -> TerminalCapabilities {
    detect_in(&|key| std::env::var(key).ok(), cfg!(windows), tmux_forwards_hyperlink)
}

struct State {
    cached: Option<TerminalCapabilities>,
    overrides: CapabilityOverrides,
}

static STATE: Mutex<State> = Mutex::new(State { cached: None, overrides: CapabilityOverrides { images: None, true_color: None, hyperlinks: None } });

fn apply_overrides(mut caps: TerminalCapabilities, o: CapabilityOverrides) -> TerminalCapabilities {
    if let Some(images) = o.images {
        caps.images = images;
    }
    if let Some(tc) = o.true_color {
        caps.true_color = tc;
    }
    if let Some(h) = o.hyperlinks {
        caps.hyperlinks = h;
    }
    caps
}

/// pi-tui's `getCapabilities`: the detected capabilities with the overrides on top, cached.
pub fn get_capabilities() -> TerminalCapabilities {
    let overrides = {
        let state = STATE.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(cached) = state.cached {
            return cached;
        }
        state.overrides
    };
    let forced = overrides.hyperlinks;
    let probe = move || forced.unwrap_or(false);
    let detected = detect_capabilities(forced.map(|_| &probe as &dyn Fn() -> bool));
    let caps = apply_overrides(detected, overrides);
    STATE.lock().unwrap_or_else(|e| e.into_inner()).cached = Some(caps);
    caps
}

/// pi-tui's `getTerminalColorMode`: truecolor when the capabilities report it, else 256 colors. `None` takes `getCapabilities()`.
pub fn get_terminal_color_mode(capabilities: Option<TerminalCapabilities>) -> TerminalColorMode {
    let caps = capabilities.unwrap_or_else(get_capabilities);
    if caps.true_color { TerminalColorMode::TrueColor } else { TerminalColorMode::Color256 }
}

/// pi-tui's `resetCapabilitiesCache`.
pub fn reset_capabilities_cache() {
    STATE.lock().unwrap_or_else(|e| e.into_inner()).cached = None;
}

/// pi-tui's `setCapabilityOverrides`: replace the overrides, dropping the cache only when one changed.
pub fn set_capability_overrides(overrides: CapabilityOverrides) {
    let mut state = STATE.lock().unwrap_or_else(|e| e.into_inner());
    if state.overrides == overrides {
        return;
    }
    state.overrides = overrides;
    state.cached = None;
}

/// pi-tui's `setCapabilities`: replace the cached capabilities.
pub fn set_capabilities(capabilities: TerminalCapabilities) {
    STATE.lock().unwrap_or_else(|e| e.into_inner()).cached = Some(capabilities);
}

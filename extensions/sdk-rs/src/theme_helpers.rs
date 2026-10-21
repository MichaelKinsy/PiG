//! Pi's coding-agent theme helpers (`modes/interactive/theme/theme.ts`): the component themes of pi-tui built from the process-global theme, `highlightCode` and
//! `initTheme`. Pi's theme is global, so these read the global theme each time a function runs: the theme the host last published, or the one [`init_theme`] installed
//! since. The host has the highlight.js port, so [`highlight_code`] asks it (the `highlight.code` host call) and a failed call takes the same lines Pi returns for a
//! failed highlight.

use std::io;

use crate::host_helpers::{highlight_code_lines, load_theme};
use crate::tui::{current_host_theme, install_theme, installed_theme_palette};

/// A text style: pi-tui's `(text: string) => string`.
pub type StyleFn = Box<dyn Fn(&str) -> String + Send + Sync>;
/// pi-tui's `(text: string, selected: boolean) => string`.
pub type SelectedStyleFn = Box<dyn Fn(&str, bool) -> String + Send + Sync>;
/// `getMarkdownTheme().highlightCode`: `(code, lang?) => string[]`.
pub type HighlightFn = Box<dyn Fn(&str, Option<&str>) -> Vec<String> + Send + Sync>;

/// pi-tui's `MarkdownTheme` as `getMarkdownTheme` returns it: every style is a function of the global theme.
pub struct MarkdownTheme {
    pub heading: StyleFn,
    pub link: StyleFn,
    pub link_url: StyleFn,
    pub code: StyleFn,
    pub code_block: StyleFn,
    pub code_block_border: StyleFn,
    pub quote: StyleFn,
    pub quote_border: StyleFn,
    pub hr: StyleFn,
    pub list_bullet: StyleFn,
    pub bold: StyleFn,
    pub italic: StyleFn,
    pub underline: StyleFn,
    pub strikethrough: StyleFn,
    /// `getMarkdownTheme().highlightCode`: [`highlight_code`], with the `mdCodeBlock` color for a highlight that fails.
    pub highlight_code: HighlightFn,
}

/// pi-tui's `SelectListTheme` as `getSelectListTheme` returns it.
pub struct SelectListTheme {
    pub selected_prefix: StyleFn,
    pub selected_text: StyleFn,
    pub description: StyleFn,
    pub scroll_info: StyleFn,
    pub no_match: StyleFn,
}

/// pi-tui's `SettingsListTheme` as `getSettingsListTheme` returns it. `cursor` is drawn when the theme is built.
pub struct SettingsListTheme {
    pub label: SelectedStyleFn,
    pub value: SelectedStyleFn,
    pub description: StyleFn,
    pub cursor: String,
    pub hint: StyleFn,
}

/// pi-tui's `EditorTheme` as `getEditorTheme` returns it.
pub struct EditorTheme {
    pub border_color: StyleFn,
    pub select_list: SelectListTheme,
}

fn foreground_of(token: &'static str) -> StyleFn {
    Box::new(move |text| current_host_theme().fg(token, text))
}

/// pi-coding-agent's `getMarkdownTheme`.
pub fn get_markdown_theme() -> MarkdownTheme {
    MarkdownTheme {
        heading: foreground_of("mdHeading"),
        link: foreground_of("mdLink"),
        link_url: foreground_of("mdLinkUrl"),
        code: foreground_of("mdCode"),
        code_block: foreground_of("mdCodeBlock"),
        code_block_border: foreground_of("mdCodeBlockBorder"),
        quote: foreground_of("mdQuote"),
        quote_border: foreground_of("mdQuoteBorder"),
        hr: foreground_of("mdHr"),
        list_bullet: foreground_of("mdListBullet"),
        bold: Box::new(|text| current_host_theme().bold(text)),
        italic: Box::new(|text| current_host_theme().italic(text)),
        underline: Box::new(|text| current_host_theme().underline(text)),
        strikethrough: Box::new(|text| current_host_theme().strikethrough(text)),
        highlight_code: Box::new(|code, lang| highlight(code, lang, true)),
    }
}

/// pi-coding-agent's `getSelectListTheme`.
pub fn get_select_list_theme() -> SelectListTheme {
    SelectListTheme {
        selected_prefix: foreground_of("accent"),
        selected_text: foreground_of("accent"),
        description: foreground_of("muted"),
        scroll_info: foreground_of("muted"),
        no_match: foreground_of("muted"),
    }
}

/// `theme.ts` `getEditorTheme`, which the interactive mode builds its editor with.
pub fn get_editor_theme() -> EditorTheme {
    EditorTheme { border_color: foreground_of("borderMuted"), select_list: get_select_list_theme() }
}

/// pi-coding-agent's `getSettingsListTheme`.
pub fn get_settings_list_theme() -> SettingsListTheme {
    SettingsListTheme {
        label: Box::new(|text, selected| if selected { current_host_theme().fg("accent", text) } else { text.to_string() }),
        value: Box::new(|text, selected| {
            let theme = current_host_theme();
            if selected { theme.fg("accent", text) } else { theme.fg("muted", text) }
        }),
        description: foreground_of("dim"),
        cursor: current_host_theme().fg("accent", "→ "),
        hint: foreground_of("dim"),
    }
}

/// pi-coding-agent's `highlightCode`: `code` highlighted for `lang` with the global theme, one string per line. Code in a language the host's highlight.js does not support,
/// or with no language, takes the `mdCodeBlock` color; code highlight.js fails on stays unstyled. The host's highlighter runs it; a host that cannot be reached leaves the
/// code unstyled, as a failed highlight does.
pub fn highlight_code(code: &str, lang: Option<&str>) -> Vec<String> {
    highlight(code, lang, false)
}

fn highlight(code: &str, lang: Option<&str>, markdown: bool) -> Vec<String> {
    match lang.filter(|lang| !lang.is_empty()) {
        // No language: Pi highlights nothing, whatever the host would answer.
        None => code_block_lines(code),
        Some(lang) => match highlight_code_lines(code, lang, markdown, installed_theme_palette()) {
            Ok(lines) => lines,
            Err(_) if markdown => code_block_lines(code),
            Err(_) => code.split('\n').map(str::to_string).collect(),
        },
    }
}

/// The code's lines in the `mdCodeBlock` color, which Pi gives code it does not highlight.
fn code_block_lines(code: &str) -> Vec<String> {
    let theme = current_host_theme();
    code.split('\n').map(|line| theme.fg("mdCodeBlock", line)).collect()
}

/// pi-coding-agent's `initTheme`: it makes the host's theme of that name, or the system theme for `None`, an empty name or one the host has no theme of, the global theme,
/// which every helper above reads, until the host's own theme changes. The host answers over the `theme.load` call; an error means it could not be reached and the global
/// theme is unchanged. Pi's `enableWatcher` has no counterpart: the host owns its theme files.
pub fn init_theme(name: Option<&str>) -> io::Result<()> {
    let palette = load_theme(name.filter(|name| !name.is_empty()))?;
    install_theme(palette);
    Ok(())
}

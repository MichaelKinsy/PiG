//! pi-coding-agent's `truncateToVisualLines` (`modes/interactive/components/visual-truncate.ts`).

use crate::tui::{visible_width, wrap_text_with_ansi};

/// pi-coding-agent's `VisualTruncateResult`: the visual lines kept and how many were dropped.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct VisualTruncateResult {
    pub visual_lines: Vec<String>,
    pub skipped_count: usize,
}

/// Which visual lines `truncate_to_visual_lines` keeps.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum VisualKeep {
    /// The last lines (Pi's default).
    #[default]
    End,
    /// The first lines.
    Start,
}

/// pi-coding-agent's `truncateToVisualLines`: `text` wrapped at `width` as a `Text` with horizontal padding `padding_x` renders it (Pi's default is 0, right for a result
/// that is placed in a `Box`; use 1 for a plain `Container`), cut to at most `max_visual_lines` visual lines, from the end unless `keep` is [`VisualKeep::Start`].
pub fn truncate_to_visual_lines(text: &str, max_visual_lines: i64, width: i64, padding_x: i64, keep: VisualKeep) -> VisualTruncateResult {
    if text.is_empty() {
        return VisualTruncateResult { visual_lines: Vec::new(), skipped_count: 0 };
    }
    let all = render_plain_text(text, padding_x, width);
    let len = all.len() as i64;
    if len <= max_visual_lines {
        return VisualTruncateResult { visual_lines: all, skipped_count: 0 };
    }
    let kept = match keep {
        VisualKeep::Start => slice(&all, 0, Some(max_visual_lines)),
        VisualKeep::End => slice(&all, -max_visual_lines, None),
    };
    // `allVisualLines.length - maxVisualLines` is positive here: a negative maximum is below the length too, and the count is never negative.
    VisualTruncateResult { visual_lines: kept, skipped_count: (len - max_visual_lines) as usize }
}

/// pi-tui's `Text(text, paddingX, 0).render(width)`: tabs as three spaces, the text wrapped in the width less the padding on both sides (the padding shrinks to fit a
/// narrow width), every line padded to the width.
fn render_plain_text(text: &str, padding_x: i64, width: i64) -> Vec<String> {
    if js_trimmed_empty(text) {
        return Vec::new();
    }
    let normalized = text.replace('\t', "   ");
    let padding_x = padding_x.min((width - 1).div_euclid(2).max(0));
    let content_width = (width - padding_x * 2).max(1);
    let margin = " ".repeat(padding_x.max(0) as usize);
    let mut lines: Vec<String> = wrap_text_with_ansi(&normalized, content_width)
        .into_iter()
        .map(|line| {
            let with_margins = format!("{margin}{line}{margin}");
            let pad = (width - visible_width(&with_margins) as i64).max(0) as usize;
            format!("{with_margins}{}", " ".repeat(pad))
        })
        .collect();
    if lines.is_empty() {
        lines.push(String::new());
    }
    lines
}

/// `text.trim() === ""`: only ECMAScript white space and line terminators.
fn js_trimmed_empty(text: &str) -> bool {
    text.chars().all(|c| {
        matches!(c, '\t' | '\n' | '\u{b}' | '\u{c}' | '\r' | ' ' | '\u{a0}' | '\u{1680}' | '\u{2000}'..='\u{200a}' | '\u{2028}' | '\u{2029}' | '\u{202f}' | '\u{205f}' | '\u{3000}' | '\u{feff}')
    })
}

/// `Array.prototype.slice(start, end)`: a negative index counts from the end.
fn slice(lines: &[String], start: i64, end: Option<i64>) -> Vec<String> {
    let n = lines.len() as i64;
    let resolve = |index: i64| if index < 0 { (n + index).max(0) } else { index.min(n) };
    let start = resolve(start);
    let end = end.map_or(n, resolve);
    if start >= end { Vec::new() } else { lines[start as usize..end as usize].to_vec() }
}

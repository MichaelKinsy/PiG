//! Output truncation helpers for tool results, mirroring Pi's exports of `core/tools/truncate.ts`
//! (`formatSize`, `truncateHead`, `truncateTail`, `truncateLine` and the default limits).
//!
//! Truncation uses two independent limits, a line count and a UTF-8 byte count; the one hit first wins.

/// Pi's `DEFAULT_MAX_LINES`.
pub const DEFAULT_MAX_LINES: usize = 2000;
/// Pi's `DEFAULT_MAX_BYTES` (50KB).
pub const DEFAULT_MAX_BYTES: usize = 50 * 1024;
/// Pi's `GREP_MAX_LINE_LENGTH`: the characters a grep match line keeps before [`truncate_line`] cuts it.
pub const GREP_MAX_LINE_LENGTH: usize = 500;

/// The limits [`truncate_head`] and [`truncate_tail`] apply. A `None` limit takes its default, like an omitted Pi
/// option; a limit of 0 is used as given.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct TruncationOptions {
    pub max_lines: Option<usize>,
    pub max_bytes: Option<usize>,
}

/// The limit a truncation hit: Pi's `"lines" | "bytes"`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TruncatedBy {
    Lines,
    Bytes,
}

impl TruncatedBy {
    /// Pi's string for the limit.
    pub fn as_str(self) -> &'static str {
        match self {
            TruncatedBy::Lines => "lines",
            TruncatedBy::Bytes => "bytes",
        }
    }
}

/// Pi's `TruncationResult`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TruncationResult {
    /// The truncated content.
    pub content: String,
    /// Whether truncation occurred.
    pub truncated: bool,
    /// The limit that was hit; `None` (Pi's `null`) when nothing was truncated.
    pub truncated_by: Option<TruncatedBy>,
    pub total_lines: usize,
    pub total_bytes: usize,
    pub output_lines: usize,
    pub output_bytes: usize,
    /// The last line was cut: the tail edge case where the final line alone exceeds the byte limit.
    pub last_line_partial: bool,
    /// The first line alone exceeded the byte limit (head truncation).
    pub first_line_exceeds_limit: bool,
    pub max_lines: usize,
    pub max_bytes: usize,
}

impl TruncationOptions {
    fn limits(self) -> (usize, usize) {
        (self.max_lines.unwrap_or(DEFAULT_MAX_LINES), self.max_bytes.unwrap_or(DEFAULT_MAX_BYTES))
    }
}

/// Pi's `splitLinesForCounting`: a trailing newline terminates the last line instead of starting another.
fn split_lines_for_counting(content: &str) -> Vec<&str> {
    if content.is_empty() {
        return Vec::new();
    }
    let mut lines: Vec<&str> = content.split('\n').collect();
    if content.ends_with('\n') {
        lines.pop();
    }
    lines
}

/// Pi's `formatSize`: `512B`, `1.5KB` or `2.0MB`, rounding the one decimal half up like JavaScript's `toFixed`.
pub fn format_size(bytes: usize) -> String {
    if bytes < 1024 {
        format!("{bytes}B")
    } else if bytes < 1024 * 1024 {
        format!("{}KB", tenths(bytes, 1024))
    } else {
        format!("{}MB", tenths(bytes, 1024 * 1024))
    }
}

/// `unit` is a power of two, so the quotient is exact in a double and `toFixed(1)` rounds an exact tie up.
fn tenths(bytes: usize, unit: usize) -> String {
    let scaled = (bytes as u128 * 10 + unit as u128 / 2) / unit as u128;
    format!("{}.{}", scaled / 10, scaled % 10)
}

fn unchanged(content: &str, total_lines: usize, total_bytes: usize, max_lines: usize, max_bytes: usize) -> TruncationResult {
    TruncationResult {
        content: content.to_string(),
        truncated: false,
        truncated_by: None,
        total_lines,
        total_bytes,
        output_lines: total_lines,
        output_bytes: total_bytes,
        last_line_partial: false,
        first_line_exceeds_limit: false,
        max_lines,
        max_bytes,
    }
}

/// Keep the first lines and bytes of `content` (file reads). It never returns a partial line: when the first line
/// alone exceeds the byte limit the content is empty and `first_line_exceeds_limit` is set.
pub fn truncate_head(content: &str, options: TruncationOptions) -> TruncationResult {
    let (max_lines, max_bytes) = options.limits();
    let total_bytes = content.len();
    let lines = split_lines_for_counting(content);
    let total_lines = lines.len();
    if total_lines <= max_lines && total_bytes <= max_bytes {
        return unchanged(content, total_lines, total_bytes, max_lines, max_bytes);
    }
    if lines[0].len() > max_bytes {
        return TruncationResult {
            content: String::new(),
            truncated: true,
            truncated_by: Some(TruncatedBy::Bytes),
            total_lines,
            total_bytes,
            output_lines: 0,
            output_bytes: 0,
            last_line_partial: false,
            first_line_exceeds_limit: true,
            max_lines,
            max_bytes,
        };
    }
    let mut kept = 0;
    let mut output_bytes = 0;
    let mut truncated_by = TruncatedBy::Lines;
    for (i, line) in lines.iter().enumerate().take(max_lines) {
        let line_bytes = line.len() + usize::from(i > 0);
        if output_bytes + line_bytes > max_bytes {
            truncated_by = TruncatedBy::Bytes;
            break;
        }
        kept += 1;
        output_bytes += line_bytes;
    }
    if kept >= max_lines && output_bytes <= max_bytes {
        truncated_by = TruncatedBy::Lines;
    }
    let output = lines[..kept].join("\n");
    TruncationResult {
        output_bytes: output.len(),
        content: output,
        truncated: true,
        truncated_by: Some(truncated_by),
        total_lines,
        total_bytes,
        output_lines: kept,
        last_line_partial: false,
        first_line_exceeds_limit: false,
        max_lines,
        max_bytes,
    }
}

/// Keep the last lines and bytes of `content` (command output, where errors and final results come last). When the
/// last line alone exceeds the byte limit it keeps the end of that line and sets `last_line_partial`.
pub fn truncate_tail(content: &str, options: TruncationOptions) -> TruncationResult {
    let (max_lines, max_bytes) = options.limits();
    let total_bytes = content.len();
    let lines = split_lines_for_counting(content);
    let total_lines = lines.len();
    if total_lines <= max_lines && total_bytes <= max_bytes {
        return unchanged(content, total_lines, total_bytes, max_lines, max_bytes);
    }
    let mut kept: Vec<&str> = Vec::new();
    let mut output_bytes = 0;
    let mut truncated_by = TruncatedBy::Lines;
    let mut last_line_partial = false;
    for line in lines.iter().rev() {
        if kept.len() >= max_lines {
            break;
        }
        let line_bytes = line.len() + usize::from(!kept.is_empty());
        if output_bytes + line_bytes > max_bytes {
            truncated_by = TruncatedBy::Bytes;
            if kept.is_empty() {
                let partial = truncate_str_to_bytes_from_end(line, max_bytes);
                output_bytes = partial.len();
                kept.push(partial);
                last_line_partial = true;
            }
            break;
        }
        kept.push(line);
        output_bytes += line_bytes;
    }
    if kept.len() >= max_lines && output_bytes <= max_bytes {
        truncated_by = TruncatedBy::Lines;
    }
    kept.reverse();
    let output = kept.join("\n");
    TruncationResult {
        output_bytes: output.len(),
        content: output,
        truncated: true,
        truncated_by: Some(truncated_by),
        total_lines,
        total_bytes,
        output_lines: kept.len(),
        last_line_partial,
        first_line_exceeds_limit: false,
        max_lines,
        max_bytes,
    }
}

/// The last `max_bytes` bytes of `s`, with the cut moved forward to the start of a UTF-8 character.
fn truncate_str_to_bytes_from_end(s: &str, max_bytes: usize) -> &str {
    if s.len() <= max_bytes {
        return s;
    }
    let mut start = s.len() - max_bytes;
    while !s.is_char_boundary(start) {
        start += 1;
    }
    &s[start..]
}

/// Cut a line longer than `max_chars` UTF-16 code units (a JavaScript string's length) to that many units plus
/// `... [truncated]`. Returns the text and whether it was cut. Pass [`GREP_MAX_LINE_LENGTH`] for Pi's default. A cut
/// inside a surrogate pair leaves U+FFFD where JavaScript keeps a lone surrogate.
pub fn truncate_line(line: &str, max_chars: usize) -> (String, bool) {
    if line.chars().map(char::len_utf16).sum::<usize>() <= max_chars {
        return (line.to_string(), false);
    }
    let mut out = String::new();
    let mut used = 0;
    for c in line.chars() {
        let n = c.len_utf16();
        if used + n > max_chars {
            if used < max_chars {
                out.push('\u{FFFD}');
            }
            break;
        }
        out.push(c);
        used += n;
    }
    out.push_str("... [truncated]");
    (out, true)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn format_size_rounds_half_up_like_to_fixed() {
        let cases = [(0, "0B"), (1023, "1023B"), (1024, "1.0KB"), (1280, "1.3KB"), (1536, "1.5KB"), (1048575, "1024.0KB"), (1048576, "1.0MB"), (5505024, "5.3MB")];
        for (bytes, want) in cases {
            assert_eq!(format_size(bytes), want, "format_size({bytes})");
        }
    }

    #[test]
    fn tail_keeps_the_end_of_an_oversized_last_line() {
        let r = truncate_tail("日本語のテキスト", TruncationOptions { max_bytes: Some(7), ..Default::default() });
        assert_eq!((r.content.as_str(), r.last_line_partial, r.truncated_by, r.output_bytes), ("スト", true, Some(TruncatedBy::Bytes), 6));
    }

    #[test]
    fn head_never_returns_a_partial_line() {
        let r = truncate_head("no newline at all", TruncationOptions { max_bytes: Some(4), ..Default::default() });
        assert_eq!((r.content.as_str(), r.first_line_exceeds_limit, r.truncated_by), ("", true, Some(TruncatedBy::Bytes)));
        let r = truncate_head("a\nb\nc\n", TruncationOptions { max_lines: Some(2), ..Default::default() });
        assert_eq!((r.content.as_str(), r.truncated_by, r.total_lines, r.output_lines), ("a\nb", Some(TruncatedBy::Lines), 3, 2));
    }

    #[test]
    fn line_counts_utf16_units() {
        assert_eq!(truncate_line("😀😀😀😀", 6), ("😀😀😀... [truncated]".to_string(), true));
        assert_eq!(truncate_line("😀😀😀", 6), ("😀😀😀".to_string(), false));
    }
}

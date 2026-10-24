//! pi-tui's `fuzzy.ts`: ordered-subsequence matching with Pi's scores (lower is better).
//!
//! Matching runs over UTF-16 code units, as JavaScript strings do, so the indexes in the score agree with Pi's for astral characters.

use super::lower::JS_LOWER;
use super::text::is_js_space;

/// pi-tui's `FuzzyMatch`: whether every query character appears in order, and the score (lower is better).
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct FuzzyMatch {
    pub matches: bool,
    pub score: f64,
}

fn lower_of(c: char) -> Option<&'static str> {
    let cp = c as u32;
    JS_LOWER.binary_search_by_key(&cp, |e| e.0).ok().map(|i| JS_LOWER[i].1)
}

/// `String.prototype.toLowerCase`: the Unicode of Node's ICU for every code point, Final_Sigma by the standard library's own rule.
pub fn js_lower(text: &str) -> String {
    if text.is_ascii() {
        return text.to_ascii_lowercase();
    }
    let sigma = '\u{3a3}';
    let sigma_lower: Vec<char> = if text.contains(sigma) {
        // Final_Sigma needs the cased/case-ignorable context; the standard library implements it, so lowercase a copy whose other
        // characters are 1:1.
        text.chars().map(|c| if c != sigma && lower_of(c).is_some() { 'A' } else { c }).collect::<String>().to_lowercase().chars().collect()
    } else {
        Vec::new()
    };
    let mut out = String::with_capacity(text.len());
    for (i, c) in text.chars().enumerate() {
        if c == sigma {
            out.push(sigma_lower.get(i).copied().unwrap_or('\u{3c3}'));
        } else if let Some(l) = lower_of(c) {
            out.push_str(l);
        } else {
            out.push(c);
        }
    }
    out
}

fn is_boundary(unit: u16) -> bool {
    if matches!(unit, 0x2d | 0x5f | 0x2e | 0x2f | 0x3a) {
        return true;
    }
    char::from_u32(unit as u32).is_some_and(is_js_space)
}

/// Pi's `scorePositions` accumulated one matched text index at a time, so the greedy alignment and each contiguous run share the
/// arithmetic.
struct Alignment {
    score: f64,
    last: i64,
    consecutive: i64,
}

impl Alignment {
    fn new() -> Self {
        Alignment { score: 0.0, last: -1, consecutive: 0 }
    }

    fn add(&mut self, text: &[u16], i: usize) {
        let i = i as i64;
        // Reward consecutive matches; penalize gaps.
        if self.last == i - 1 {
            self.consecutive += 1;
            self.score -= (self.consecutive * 5) as f64;
        } else {
            self.consecutive = 0;
            if self.last >= 0 {
                self.score += ((i - self.last - 1) * 2) as f64;
            }
        }
        // Reward word boundary matches.
        if i == 0 || is_boundary(text[(i - 1) as usize]) {
            self.score -= 10.0;
        }
        // Slight penalty for later matches.
        self.score += i as f64 * 0.1;
        self.last = i;
    }
}

/// `String.prototype.indexOf` of a UTF-16 query from unit index `from`.
fn index_units(text: &[u16], query: &[u16], from: usize) -> Option<usize> {
    if from + query.len() > text.len() {
        return None;
    }
    text[from..].windows(query.len()).position(|w| w == query).map(|p| from + p)
}

fn match_query(query: &[u16], text: &[u16]) -> FuzzyMatch {
    if query.is_empty() {
        return FuzzyMatch { matches: true, score: 0.0 };
    }
    if query.len() > text.len() {
        return FuzzyMatch { matches: false, score: 0.0 };
    }
    // Greedy alignment: each query character at its first occurrence after the previous one.
    let mut greedy = Alignment::new();
    let mut from = 0;
    for &unit in query {
        let Some(found) = text[from..].iter().position(|&u| u == unit) else {
            return FuzzyMatch { matches: false, score: 0.0 };
        };
        let i = from + found;
        greedy.add(text, i);
        from = i + 1;
    }
    let mut score = greedy.score;
    // The greedy alignment can scatter the query over an earlier, unrelated part of the text, e.g. "opus" in
    // "openrouter openrouter/anthropic/claude-opus" takes o, p, u from "openrouter". Also score every contiguous occurrence and
    // keep the best alignment.
    let mut start = index_units(text, query, 0);
    while let Some(s) = start {
        let mut run = Alignment::new();
        for k in 0..query.len() {
            run.add(text, s + k);
        }
        score = score.min(run.score);
        start = index_units(text, query, s + 1);
    }
    if query == text {
        score -= 100.0;
    }
    FuzzyMatch { matches: true, score }
}

fn units(s: &str) -> Vec<u16> {
    s.encode_utf16().collect()
}

/// Whether every character of `query` appears in `text` in order, and Pi's score (pi-tui's `fuzzyMatch`).
pub fn fuzzy_match(query: &str, text: &str) -> FuzzyMatch {
    let query_lower = js_lower(query);
    let text_units = units(&js_lower(text));
    let primary = match_query(&units(&query_lower), &text_units);
    if primary.matches {
        return primary;
    }
    let b = query_lower.as_bytes();
    let letters = b.iter().take_while(|c| c.is_ascii_lowercase()).count();
    let digits_after = b[letters..].iter().take_while(|c| c.is_ascii_digit()).count();
    let digits = b.iter().take_while(|c| c.is_ascii_digit()).count();
    let letters_after = b[digits..].iter().take_while(|c| c.is_ascii_lowercase()).count();
    let swapped = if letters > 0 && digits_after > 0 && letters + digits_after == b.len() {
        format!("{}{}", &query_lower[letters..], &query_lower[..letters])
    } else if digits > 0 && letters_after > 0 && digits + letters_after == b.len() {
        format!("{}{}", &query_lower[digits..], &query_lower[..digits])
    } else {
        return primary;
    };
    let swapped_match = match_query(&units(&swapped), &text_units);
    if !swapped_match.matches {
        return primary;
    }
    FuzzyMatch { matches: true, score: swapped_match.score + 5.0 }
}

/// The items whose text matches every whitespace- or slash-separated token of `query`, best first (pi-tui's `fuzzyFilter`). A blank
/// query returns the items unchanged.
pub fn fuzzy_filter<'a, T>(items: &'a [T], query: &str, get_text: impl Fn(&T) -> String) -> Vec<&'a T> {
    let trimmed = query.trim_matches(is_js_space);
    if trimmed.is_empty() {
        return items.iter().collect();
    }
    let tokens: Vec<&str> = trimmed.split(|c: char| c == '/' || is_js_space(c)).filter(|t| !t.is_empty()).collect();
    if tokens.is_empty() {
        return items.iter().collect();
    }
    let mut results: Vec<(f64, &T)> = Vec::new();
    for item in items {
        let text = get_text(item);
        let mut total = 0.0;
        let mut all_match = true;
        for token in &tokens {
            let m = fuzzy_match(token, &text);
            if m.matches {
                total += m.score;
            } else {
                all_match = false;
                break;
            }
        }
        if all_match {
            results.push((total, item));
        }
    }
    results.sort_by(|a, b| a.0.partial_cmp(&b.0).unwrap_or(std::cmp::Ordering::Equal));
    results.into_iter().map(|r| r.1).collect()
}

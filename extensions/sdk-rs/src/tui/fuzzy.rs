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

fn match_query(query: &[u16], text: &[u16]) -> FuzzyMatch {
    if query.is_empty() {
        return FuzzyMatch { matches: true, score: 0.0 };
    }
    if query.len() > text.len() {
        return FuzzyMatch { matches: false, score: 0.0 };
    }
    let mut query_index = 0;
    let mut score = 0.0f64;
    let mut last: i64 = -1;
    let mut consecutive: i64 = 0;
    while query_index < query.len() {
        let from = (last + 1) as usize;
        let Some(found) = text.get(from..).and_then(|t| t.iter().position(|&u| u == query[query_index])) else { break };
        let i = (from + found) as i64;
        let boundary = i == 0 || is_boundary(text[(i - 1) as usize]);
        if last == i - 1 {
            consecutive += 1;
            score -= (consecutive * 5) as f64;
        } else {
            consecutive = 0;
            if last >= 0 {
                score += ((i - last - 1) * 2) as f64;
            }
        }
        if boundary {
            score -= 10.0;
        }
        score += i as f64 * 0.1;
        last = i;
        query_index += 1;
    }
    if query_index < query.len() {
        return FuzzyMatch { matches: false, score: 0.0 };
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

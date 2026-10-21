//! Compaction usage helpers, mirroring Pi's exports of `core/compaction/compaction.ts` (`CompactionSettings`, `DEFAULT_COMPACTION_SETTINGS`,
//! `calculateContextTokens`, `shouldCompact`, `getLastAssistantUsage`). They are pure: they read values the caller already holds.

use crate::context::UsageInfo;
use serde_json::Value;
use std::borrow::Borrow;

/// Pi's `CompactionSettings`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct CompactionSettings {
    pub enabled: bool,
    pub reserve_tokens: i64,
    pub keep_recent_tokens: i64,
}

/// Pi's `DEFAULT_COMPACTION_SETTINGS`.
pub const DEFAULT_COMPACTION_SETTINGS: CompactionSettings = CompactionSettings { enabled: true, reserve_tokens: 16384, keep_recent_tokens: 20000 };

/// Pi's `calculateContextTokens`: the native total when it is non-zero, else the sum of the components.
pub fn calculate_context_tokens(usage: &UsageInfo) -> i64 {
    if usage.total_tokens != 0 {
        return i64::from(usage.total_tokens);
    }
    i64::from(usage.input) + i64::from(usage.output) + i64::from(usage.cache_read) + i64::from(usage.cache_write)
}

/// Pi's `shouldCompact`: compaction triggers when the context exceeds the window less the reserve.
pub fn should_compact(context_tokens: i64, context_window: i64, settings: &CompactionSettings) -> bool {
    settings.enabled && context_tokens > context_window - settings.reserve_tokens
}

fn js_number(value: Option<&Value>) -> f64 {
    value.and_then(Value::as_f64).unwrap_or(f64::NAN)
}

/// A token count as JavaScript's `+` reads it: null is 0, and a missing (undefined) or non-numeric count is NaN.
fn usage_addend(value: Option<&Value>) -> f64 {
    if value.is_some_and(Value::is_null) { 0.0 } else { js_number(value) }
}

fn token_count(value: Option<&Value>) -> i32 {
    let number = js_number(value);
    if number.is_finite() { number as i32 } else { 0 }
}

/// Pi's `getLastAssistantUsage` over the session entries Pi hands an extension (for example [`crate::ReadonlySessionManager::get_entries`]): the usage of the last
/// assistant message that did not abort or fail and whose usage is not all zero. As in JavaScript's arithmetic, a null token count is 0 and a missing or
/// non-numeric one is NaN, so it never makes a usage valid.
pub fn get_last_assistant_usage<E: Borrow<Value>>(entries: &[E]) -> Option<UsageInfo> {
    for entry in entries.iter().rev() {
        let entry: &Value = entry.borrow();
        if entry.get("type").and_then(Value::as_str) != Some("message") {
            continue;
        }
        let message = &entry["message"];
        if message.get("role").and_then(Value::as_str) != Some("assistant") {
            continue;
        }
        if matches!(message.get("stopReason").and_then(Value::as_str), Some("aborted" | "error")) {
            continue;
        }
        let Some(usage) = message.get("usage").filter(|usage| usage.is_object()) else { continue };
        let mut total = js_number(usage.get("totalTokens"));
        if total == 0.0 || total.is_nan() {
            total = ["input", "output", "cacheRead", "cacheWrite"].iter().map(|key| usage_addend(usage.get(*key))).sum();
        }
        if !(total > 0.0) {
            continue;
        }
        return Some(UsageInfo {
            input: token_count(usage.get("input")),
            output: token_count(usage.get("output")),
            cache_read: token_count(usage.get("cacheRead")),
            cache_write: token_count(usage.get("cacheWrite")),
            total_tokens: token_count(usage.get("totalTokens")),
        });
    }
    None
}

//! pi-ai's pure helpers: `StringEnum` (`utils/typebox-helpers.ts`), `contentText`, `getSystemMessageText` and `renderSystemMessageUpdate` (`utils/text.ts`), the transcript
//! helpers (`utils/transcript.ts`), `calculateCost`, `getSupportedThinkingLevels` and `clampThinkingLevel` (`models.ts`), `isContextOverflow` and `isRecoverableLength`
//! (`utils/overflow.ts`), `isRetryableAssistantError` and `retryDelayMs` (`utils/retry.ts`), and `envApiKeyAuth` (`auth/helpers.ts`).
//!
//! A model, a message, a context and a tool are the JSON objects the wire carries. A value Pi's types rule out but Pi tolerates (a missing optional field) is read as Pi reads
//! it; one Pi throws a `TypeError` on (content that is neither a string nor an array) is an `Err`. The order of a `sections` object is `Object.entries` order: the array-index
//! keys first, then the rest in insertion order. A string holding an unpaired surrogate is not representable in a `serde_json::Value`.

use crate::provider::{APIKeyAuth, APIKeyAuthInput, AuthInteraction, AuthResult, ProviderResult, ProviderSignal};
use serde_json::{Map, Value, json};
use std::sync::Arc;

/// pi-ai's `DEFAULT_MAX_AGENT_RETRY_DELAY_MS`: the cap on one retry delay when a policy names none.
pub const DEFAULT_MAX_AGENT_RETRY_DELAY_MS: f64 = 60_000.0;

fn truthy(value: Option<&Value>) -> bool {
    match value {
        None | Some(Value::Null) => false,
        Some(Value::Bool(flag)) => *flag,
        Some(Value::Number(number)) => number.as_f64().is_some_and(|n| n != 0.0 && !n.is_nan()),
        Some(Value::String(text)) => !text.is_empty(),
        Some(_) => true,
    }
}

/// A JSON number as JavaScript's arithmetic reads it: `null` is 0, and an absent or non-numeric value is NaN.
fn number(value: Option<&Value>) -> f64 {
    match value {
        Some(Value::Null) => 0.0,
        Some(Value::Number(number)) => number.as_f64().unwrap_or(f64::NAN),
        _ => f64::NAN,
    }
}

fn is_array_index(key: &str) -> bool {
    key == "0" || (!key.is_empty() && key.len() <= 10 && key.bytes().all(|b| b.is_ascii_digit()) && !key.starts_with('0') && key.parse::<u64>().is_ok_and(|n| n <= 4_294_967_294))
}

/// The order of an object's own string keys that `Object.keys` and `JSON.stringify` use: the array-index keys ascending, then every other key in insertion order.
pub(crate) fn ordered_keys(object: &Map<String, Value>) -> Vec<&String> {
    let mut indexes: Vec<&String> = object.keys().filter(|key| is_array_index(key)).collect();
    indexes.sort_by_key(|key| (key.len(), key.as_str()));
    indexes.into_iter().chain(object.keys().filter(|key| !is_array_index(key))).collect()
}

/// The options of [`string_enum`].
#[derive(Debug, Clone, Default)]
pub struct StringEnumOptions {
    /// Kept when it is not empty.
    pub description: Option<String>,
    /// Kept when it is not empty.
    pub default: Option<String>,
}

/// pi-ai's `StringEnum`: a JSON Schema string enum that Google's API and other providers that reject `anyOf`/`const` accept. The keys are `type`, `enum`, then `description` and
/// `default` when `options` carry a non-empty one.
pub fn string_enum(values: &[&str], options: Option<&StringEnumOptions>) -> Value {
    let mut schema = Map::new();
    schema.insert("type".into(), json!("string"));
    schema.insert("enum".into(), json!(values));
    if let Some(description) = options.and_then(|o| o.description.as_deref()).filter(|text| !text.is_empty()) {
        schema.insert("description".into(), json!(description));
    }
    if let Some(default) = options.and_then(|o| o.default.as_deref()).filter(|text| !text.is_empty()) {
        schema.insert("default".into(), json!(default));
    }
    Value::Object(schema)
}

const EXTENDED_THINKING_LEVELS: [&str; 7] = ["off", "minimal", "low", "medium", "high", "xhigh", "max"];

/// pi-ai's `getSupportedThinkingLevels`: the thinking levels the model accepts, in Pi's order (off, minimal, low, medium, high, xhigh, max). A model without reasoning accepts
/// only `"off"`. A level the model's `thinkingLevelMap` maps to null is not accepted, and `xhigh` and `max` are accepted only when the map names them.
pub fn get_supported_thinking_levels(model: &Value) -> Vec<&'static str> {
    if !truthy(model.get("reasoning")) {
        return vec!["off"];
    }
    let map = model.get("thinkingLevelMap").and_then(Value::as_object);
    EXTENDED_THINKING_LEVELS
        .into_iter()
        .filter(|level| {
            let mapped = map.and_then(|map| map.get(*level));
            if matches!(mapped, Some(Value::Null)) {
                return false;
            }
            !(matches!(*level, "xhigh" | "max") && mapped.is_none())
        })
        .collect()
}

/// pi-ai's `clampThinkingLevel`: `level` when the model accepts it, else the nearest accepted level at or above it, else the nearest below it, else the first accepted level, else
/// `"off"`. A level that is not one of Pi's is the first accepted level.
pub fn clamp_thinking_level(model: &Value, level: &str) -> String {
    let available = get_supported_thinking_levels(model);
    if available.contains(&level) {
        return level.to_string();
    }
    let first = available.first().copied().unwrap_or("off");
    let Some(requested) = EXTENDED_THINKING_LEVELS.iter().position(|candidate| *candidate == level) else {
        return first.to_string();
    };
    let found = EXTENDED_THINKING_LEVELS[requested..].iter().find(|c| available.contains(c)).or_else(|| EXTENDED_THINKING_LEVELS[..requested].iter().rev().find(|c| available.contains(c)));
    found.copied().unwrap_or(first).to_string()
}

/// pi-ai's `calculateCost`: it prices `usage` at the model's per-million-token rates and stores the result in `usage["cost"]` (an object the caller supplies, as in Pi), which it
/// also returns. The rates are the model's cost, or those of the tier with the highest `inputTokensAbove` that the request's input (input, cacheRead and cacheWrite tokens)
/// exceeds. Cache writes that `usage["cacheWrite1h"]` reports are charged at twice the input rate. `usage["cost"]` that is not an object is a `TypeError` in Pi and an `Err` here.
pub fn calculate_cost(model: &Value, usage: &mut Value) -> Result<Value, String> {
    let get = |key: &str| usage.get(key);
    let input_tokens = number(get("input")) + number(get("cacheRead")) + number(get("cacheWrite"));
    let (input, output, cache_read, cache_write) = (number(get("input")), number(get("output")), number(get("cacheRead")), number(get("cacheWrite")));
    let long_write = match get("cacheWrite1h") {
        None | Some(Value::Null) => 0.0,
        value => number(value),
    };
    let model_cost = model.get("cost");
    let mut rates = model_cost;
    let mut matched = -1.0;
    for tier in model_cost.and_then(|cost| cost.get("tiers")).and_then(Value::as_array).into_iter().flatten() {
        let above = number(tier.get("inputTokensAbove"));
        if input_tokens > above && above > matched {
            rates = Some(tier);
            matched = above;
        }
    }
    let rate = |key: &str| number(rates.and_then(|rates| rates.get(key)));
    let short_write = cache_write - long_write;
    let cost_input = (rate("input") / 1000000.0) * input;
    let cost_output = (rate("output") / 1000000.0) * output;
    let cost_cache_read = (rate("cacheRead") / 1000000.0) * cache_read;
    let cost_cache_write = (rate("cacheWrite") * short_write + rate("input") * 2.0 * long_write) / 1000000.0;
    let total = cost_input + cost_output + cost_cache_read + cost_cache_write;
    let cost = usage.get_mut("cost").and_then(Value::as_object_mut).ok_or("TypeError: usage.cost is not an object")?;
    for (key, value) in [("input", cost_input), ("output", cost_output), ("cacheRead", cost_cache_read), ("cacheWrite", cost_cache_write), ("total", total)] {
        cost.insert(key.into(), serde_json::Number::from_f64(value).map_or(Value::Null, Value::Number));
    }
    Ok(Value::Object(cost.clone()))
}

fn join_element(value: Option<&Value>) -> String {
    match value {
        None | Some(Value::Null) => String::new(),
        value => crate::llm_convert::js_to_string(value),
    }
}

/// pi-ai's `contentText`: a string is its own text, and an array of content blocks is the text of its text blocks joined with `separator` (pi-ai's default is `"\n"`). Any other
/// content makes Pi throw a `TypeError`, which is an `Err` here.
pub fn content_text(content: Option<&Value>, separator: &str) -> Result<String, String> {
    match content {
        Some(Value::String(text)) => Ok(text.clone()),
        Some(Value::Array(blocks)) => {
            let mut texts = Vec::new();
            for block in blocks {
                if block.is_null() {
                    return Err("TypeError: Cannot read properties of null (reading 'type')".to_string());
                }
                if block.get("type").and_then(Value::as_str) == Some("text") {
                    texts.push(join_element(block.get("text")));
                }
            }
            Ok(texts.join(separator))
        }
        _ => Err("TypeError: content.filter is not a function".to_string()),
    }
}

fn is_system(message: &Value) -> bool {
    message.get("role").and_then(Value::as_str) == Some("system")
}

fn list<'a>(value: Option<&'a Value>) -> &'a [Value] {
    value.and_then(Value::as_array).map_or(&[], Vec::as_slice)
}

fn tool_name(tool: &Value) -> &str {
    tool.get("name").and_then(Value::as_str).unwrap_or_default()
}

/// A JavaScript `Map` keyed by string: setting an existing key keeps its position, and deleting a key then setting it again moves it to the end.
#[derive(Default)]
struct OrderedMap(Vec<(String, Value)>);

impl OrderedMap {
    fn set(&mut self, key: &str, value: Value) {
        match self.0.iter_mut().find(|(existing, _)| existing == key) {
            Some(entry) => entry.1 = value,
            None => self.0.push((key.to_string(), value)),
        }
    }
    fn delete(&mut self, key: &str) {
        self.0.retain(|(existing, _)| existing != key);
    }
    fn into_values(self) -> Vec<Value> {
        self.0.into_iter().map(|(_, value)| value).collect()
    }
}

fn current_tools<'a>(messages: impl IntoIterator<Item = &'a Value>) -> Vec<Value> {
    let mut tools = OrderedMap::default();
    for message in messages.into_iter().filter(|message| is_system(message)) {
        for tool in list(message.get("toolsRemoved")) {
            tools.delete(tool_name(tool));
        }
        for tool in list(message.get("toolsAdded")) {
            tools.set(tool_name(tool), tool.clone());
        }
    }
    tools.into_values()
}

/// pi-ai's `getCurrentTools`: the tools available after every system message's `toolsRemoved` and `toolsAdded` are applied in order, in the order they were first added (a tool
/// removed and added again moves to the end).
pub fn get_current_tools(messages: &[Value]) -> Vec<Value> {
    current_tools(messages)
}

/// pi-ai's `getCurrentSystemMessage`: every system message replayed into one holding the current prompt and tools. Later content is appended to the base prompt with a blank line
/// between, sections are patched by name (null removes one) and tools are resolved as [`get_current_tools`] does. It is `Ok(None)` when the transcript has no system message and
/// no tool.
pub fn get_current_system_message(messages: &[Value]) -> Result<Option<Value>, String> {
    enum Timestamp {
        Undefined,
        Null,
        Value(Value),
    }
    let mut content = Vec::new();
    let mut sections = OrderedMap::default();
    let mut timestamp = Timestamp::Undefined;
    for message in messages.iter().filter(|message| is_system(message)) {
        if matches!(timestamp, Timestamp::Undefined | Timestamp::Null) {
            timestamp = match message.get("timestamp") {
                None => Timestamp::Undefined,
                Some(Value::Null) => Timestamp::Null,
                Some(value) => Timestamp::Value(value.clone()),
            };
        }
        let text = content_text(message.get("content"), "\n")?;
        if !text.is_empty() {
            content.push(text);
        }
        if let Some(declared) = message.get("sections").and_then(Value::as_object) {
            for name in ordered_keys(declared) {
                match &declared[name] {
                    Value::Null => sections.delete(name),
                    value => sections.set(name, value.clone()),
                }
            }
        }
    }
    let tools = current_tools(messages);
    if matches!(timestamp, Timestamp::Undefined) && tools.is_empty() {
        return Ok(None);
    }
    let mut result = Map::new();
    result.insert("role".into(), json!("system"));
    result.insert("content".into(), json!(content.join("\n\n")));
    if !sections.0.is_empty() {
        let merged: Map<String, Value> = sections.0.into_iter().collect();
        let ordered: Map<String, Value> = ordered_keys(&merged).into_iter().map(|key| (key.clone(), merged[key].clone())).collect();
        result.insert("sections".into(), Value::Object(ordered));
    }
    if !tools.is_empty() {
        result.insert("toolsAdded".into(), Value::Array(tools));
    }
    result.insert("timestamp".into(), match timestamp {
        Timestamp::Value(value) => value,
        _ => json!(0),
    });
    Ok(Some(Value::Object(result)))
}

fn system_message_text(message: &Value) -> Result<String, String> {
    let mut parts = vec![content_text(message.get("content"), "\n")?];
    if let Some(sections) = message.get("sections").and_then(Value::as_object) {
        for name in ordered_keys(sections) {
            if !sections[name].is_null() {
                parts.push(join_element(sections.get(name)));
            }
        }
    }
    parts.retain(|part| !part.is_empty());
    Ok(parts.join("\n\n"))
}

/// pi-ai's `getSystemMessageText`: a system message rendered as a complete prompt, its content followed by its non-null sections, separated by blank lines.
pub fn get_system_message_text(message: &Value) -> Result<String, String> {
    system_message_text(message)
}

/// pi-ai's `getCurrentSystemPrompt`: the text of [`get_current_system_message`], or `""` when there is none.
pub fn get_current_system_prompt(messages: &[Value]) -> Result<String, String> {
    match get_current_system_message(messages)? {
        Some(message) => system_message_text(&message),
        None => Ok(String::new()),
    }
}

/// pi-ai's `renderSystemMessageUpdate`: a later system message rendered for APIs that accept system messages mid-conversation. Section changes are framed by name. Pi marks the
/// framing request-time only and subject to change between versions.
pub fn render_system_message_update(message: &Value) -> Result<String, String> {
    let mut parts = Vec::new();
    let text = content_text(message.get("content"), "\n")?;
    if !text.is_empty() {
        parts.push(text);
    }
    if let Some(sections) = message.get("sections").and_then(Value::as_object) {
        for name in ordered_keys(sections) {
            parts.push(match &sections[name] {
                Value::Null => format!("Removed system prompt section \"{name}\"."),
                value => format!("Updated system prompt section \"{name}\":\n\n{}", join_element(Some(value))),
            });
        }
    }
    Ok(parts.join("\n\n"))
}

fn initial_system_message(system_prompt: Option<&Value>, tools: Option<&Value>) -> Option<Value> {
    let prompt = system_prompt.and_then(Value::as_str);
    let tool_list = list(tools);
    if prompt.is_none_or(str::is_empty) && tool_list.is_empty() {
        return None;
    }
    let mut message = Map::new();
    message.insert("role".into(), json!("system"));
    message.insert("content".into(), json!(prompt.unwrap_or_default()));
    if !tool_list.is_empty() {
        message.insert("toolsAdded".into(), Value::Array(tool_list.to_vec()));
    }
    message.insert("timestamp".into(), json!(0));
    Some(Value::Object(message))
}

/// pi-ai's `createInitialSystemMessage`: the leading system message for a prompt and tool set, or `None` when both are empty.
pub fn create_initial_system_message(system_prompt: Option<&str>, tools: Option<&[Value]>) -> Option<Value> {
    let tools = tools.map(|tools| Value::Array(tools.to_vec()));
    initial_system_message(system_prompt.map(|prompt| json!(prompt)).as_ref(), tools.as_ref())
}

/// pi-ai's `normalizeContext`: the context's `systemPrompt` and `tools` folded into a leading system message, as the `{"messages": [...]}` transcript every provider-facing
/// function expects. A context with neither keeps its messages as they are.
pub fn normalize_context(context: &Value) -> Value {
    let mut messages = list(context.get("messages")).to_vec();
    if let Some(initial) = initial_system_message(context.get("systemPrompt"), context.get("tools")) {
        messages.insert(0, initial);
    }
    json!({ "messages": messages })
}

/// pi-ai's `getInitialSystemMessage`: the leading message when it is a system message, else `None`.
pub fn get_initial_system_message(messages: &[Value]) -> Option<&Value> {
    messages.first().filter(|message| is_system(message))
}

/// pi-ai's `withoutInitialSystemMessage`: the messages without a leading system message, for APIs that carry the prompt outside the message list.
pub fn without_initial_system_message(messages: &[Value]) -> Vec<Value> {
    let skip = usize::from(get_initial_system_message(messages).is_some());
    messages[skip..].to_vec()
}

/// pi-ai's `collapseSystemMessages`: the transcript for APIs without mid-conversation system messages. The replayed system message leads and every later system message is
/// dropped.
pub fn collapse_system_messages(context: &Value) -> Result<Value, String> {
    let messages = list(context.get("messages"));
    let mut collapsed: Vec<Value> = get_current_system_message(messages)?.into_iter().collect();
    collapsed.extend(messages.iter().filter(|message| message.get("role").and_then(Value::as_str) != Some("system")).cloned());
    Ok(json!({ "messages": collapsed }))
}

/// pi-ai's `resolveTranscript`: the context itself when the model accepts later system messages, else [`collapse_system_messages`] of it.
pub fn resolve_transcript(context: &Value, supports_mid_convo_system_messages: bool) -> Result<Value, String> {
    if supports_mid_convo_system_messages { Ok(context.clone()) } else { collapse_system_messages(context) }
}

/// pi-ai's `getDeclaredTools`: every definition the transcript's `toolsAdded` names, one per name, in first-declaration order with the latest definition of each.
pub fn get_declared_tools(messages: &[Value]) -> Vec<Value> {
    let mut declared = OrderedMap::default();
    for message in messages.iter().filter(|message| is_system(message)) {
        for tool in list(message.get("toolsAdded")) {
            declared.set(tool_name(tool), tool.clone());
        }
    }
    declared.into_values()
}

/// The index, among `system_messages`, of the last one with a removal or a same-name redeclaration. A name declared before counts as a redeclaration even after its removal: Kimi K3
/// ignores a removed tool that a later tool message declares again.
fn find_last_non_additive_tool_change(system_messages: &[&Value]) -> Option<usize> {
    let mut declared = std::collections::HashSet::new();
    let mut last = None;
    for (index, message) in system_messages.iter().enumerate() {
        if !list(message.get("toolsRemoved")).is_empty() {
            last = Some(index);
        }
        for tool in list(message.get("toolsAdded")) {
            if !declared.insert(tool_name(tool).to_string()) {
                last = Some(index);
            }
        }
    }
    last
}

/// pi-ai's `hasNonAdditiveToolChanges`: whether the tool history holds a removal or a same-name redeclaration that an addition-only transport cannot replay.
pub fn has_non_additive_tool_changes(messages: &[Value]) -> bool {
    let system: Vec<&Value> = messages.iter().filter(|message| is_system(message)).collect();
    find_last_non_additive_tool_change(&system).is_some()
}

/// pi-ai's `TranscriptToolSupport`: how a transport can express tool changes after the first request.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum TranscriptToolSupport {
    /// `"none"`: only the top-level tool list exists; it always holds the current tools.
    None,
    /// `"additions"`: tools can be added at a later message, but not removed or redefined.
    Additions,
    /// `"changes"`: every tool, including the initial ones, is declared in place, and a later declaration under the same name replaces the earlier one. Removals are not expressible
    /// on the wire, so the transport announces them as text and the removed tools stay declared.
    Changes,
}

/// pi-ai's `TranscriptTools`.
#[derive(Debug, Clone, PartialEq)]
pub struct TranscriptTools {
    /// The tools sent in the top-level request field.
    pub request_tools: Vec<Value>,
    /// The number of leading system messages whose tool changes `request_tools` already holds. Every later system message carries its own tool changes in place.
    pub folded_system_messages: usize,
}

/// pi-ai's `resolveTranscriptTools`: the tool declarations split between the top-level request field and in-place changes.
///
/// With [`TranscriptToolSupport::Changes`], the top-level list stays empty and every system message, including the leading one, declares its tools in place: an in-place declaration
/// can replace an earlier in-place one, but not a top-level one. With [`TranscriptToolSupport::Additions`], a removal or redefinition cannot be expressed in place, so the top-level
/// list becomes the tool state after the last such change; later additions stay anchored, and the cached prefix is invalidated once per removal instead of on every later addition.
/// With [`TranscriptToolSupport::None`], the top-level list is the current tool set.
pub fn resolve_transcript_tools(messages: &[Value], support: TranscriptToolSupport) -> TranscriptTools {
    let system: Vec<&Value> = messages.iter().filter(|message| is_system(message)).collect();
    match support {
        TranscriptToolSupport::None => TranscriptTools { request_tools: current_tools(system.iter().copied()), folded_system_messages: system.len() },
        TranscriptToolSupport::Changes => TranscriptTools { request_tools: Vec::new(), folded_system_messages: 0 },
        TranscriptToolSupport::Additions => {
            let folded = usize::from(get_initial_system_message(messages).is_some()).max(find_last_non_additive_tool_change(&system).map_or(0, |last| last + 1));
            TranscriptTools { request_tools: current_tools(system[..folded].iter().copied()), folded_system_messages: folded }
        }
    }
}

/// The part of pi-ai's `RetryPolicy` that [`retry_delay_ms`] reads.
#[derive(Debug, Clone, Copy)]
pub struct RetryPolicy {
    /// The delay before the first retry; attempt `n` waits `base_delay_ms * 2^(n-1)`.
    pub base_delay_ms: f64,
    /// Caps each delay; `None` is [`DEFAULT_MAX_AGENT_RETRY_DELAY_MS`].
    pub max_agent_delay_ms: Option<f64>,
}

/// pi-ai's `retryDelayMs`: the delay before retry `attempt` (1-indexed): the policy's base delay doubled for each earlier attempt, a result that is not a safe integer counting as
/// the largest safe integer, capped by `max_agent_delay_ms`.
pub fn retry_delay_ms(policy: &RetryPolicy, attempt: i64) -> f64 {
    const MAX_SAFE_INTEGER: f64 = 9007199254740991.0;
    let delay = policy.base_delay_ms * 2f64.powf(0.max(attempt - 1) as f64);
    let safe = if delay.is_finite() && delay == delay.trunc() && delay.abs() <= MAX_SAFE_INTEGER { delay } else { MAX_SAFE_INTEGER };
    let limit = policy.max_agent_delay_ms.unwrap_or(DEFAULT_MAX_AGENT_RETRY_DELAY_MS);
    if safe.is_nan() || limit.is_nan() { f64::NAN } else { safe.min(limit) }
}

/// The subset of JavaScript regular-expression syntax pi-ai's patterns use, matched the way V8 does for the `i` flag without `u`: over UTF-16 code units, a letter matching its
/// own two ASCII cases only (in a class too, so `[a-z0-9]` holds A-Z), `.` any unit but a line terminator, `\s` and `\S` JavaScript's white space, `\d` an ASCII digit, `\w` an
/// ASCII word character, `$` the end of the text, and `(?<!...)` and `(?!...)` negative lookaround.
mod js_regex {
    #[derive(Debug)]
    enum Node {
        Unit(u16),
        Any,
        Class { ranges: Vec<(u16, u16)>, negated: bool },
        Start,
        End,
        /// `(?<!inner)` with the fixed width of `inner`.
        NotBehind(Box<Node>, usize),
        /// `(?!inner)`.
        NotAhead(Box<Node>),
        Seq(Vec<Node>),
        Alt(Vec<Node>),
        Repeat(Box<Node>, usize, usize),
    }

    const SPACE: [(u16, u16); 10] = [(9, 13), (32, 32), (0xA0, 0xA0), (0x1680, 0x1680), (0x2000, 0x200A), (0x2028, 0x2029), (0x202F, 0x202F), (0x205F, 0x205F), (0x3000, 0x3000), (0xFEFF, 0xFEFF)];

    pub struct Pattern(Node);

    struct Parser<'a> {
        source: &'a [u8],
        at: usize,
    }

    impl Parser<'_> {
        fn peek(&self) -> Option<u8> {
            self.source.get(self.at).copied()
        }
        fn alternation(&mut self) -> Node {
            let mut branches = vec![self.sequence()];
            while self.peek() == Some(b'|') {
                self.at += 1;
                branches.push(self.sequence());
            }
            if branches.len() == 1 { branches.remove(0) } else { Node::Alt(branches) }
        }
        fn sequence(&mut self) -> Node {
            let mut items = Vec::new();
            while let Some(c) = self.peek() {
                if c == b'|' || c == b')' {
                    break;
                }
                let atom = self.atom();
                items.push(self.quantified(atom));
            }
            Node::Seq(items)
        }
        fn quantified(&mut self, atom: Node) -> Node {
            let (min, max) = match self.peek() {
                Some(b'?') => (0, 1),
                Some(b'*') => (0, usize::MAX),
                Some(b'+') => (1, usize::MAX),
                _ => return atom,
            };
            self.at += 1;
            Node::Repeat(Box::new(atom), min, max)
        }
        fn escape_ranges(letter: u8) -> Option<(Vec<(u16, u16)>, bool)> {
            match letter {
                b'd' => Some((vec![(0x30, 0x39)], false)),
                b'w' => Some((vec![(0x30, 0x39), (0x41, 0x5A), (0x5F, 0x5F), (0x61, 0x7A)], false)),
                b's' => Some((SPACE.to_vec(), false)),
                b'S' => Some((SPACE.to_vec(), true)),
                _ => None,
            }
        }
        fn atom(&mut self) -> Node {
            let c = self.source[self.at];
            self.at += 1;
            match c {
                b'(' => {
                    let rest = &self.source[self.at..];
                    let (not_behind, not_ahead) = (rest.starts_with(b"?<!"), rest.starts_with(b"?!"));
                    if not_behind {
                        self.at += 3;
                    } else if not_ahead {
                        self.at += 2;
                    } else if rest.starts_with(b"?:") {
                        self.at += 2;
                    }
                    let inner = self.alternation();
                    assert_eq!(self.peek(), Some(b')'), "unbalanced group");
                    self.at += 1;
                    if not_behind {
                        let width = width(&inner);
                        Node::NotBehind(Box::new(inner), width)
                    } else if not_ahead {
                        Node::NotAhead(Box::new(inner))
                    } else {
                        inner
                    }
                }
                b'.' => Node::Any,
                b'^' => Node::Start,
                b'$' => Node::End,
                b'[' => {
                    let negated = self.peek() == Some(b'^');
                    if negated {
                        self.at += 1;
                    }
                    let mut ranges = Vec::new();
                    while self.peek() != Some(b']') {
                        let mut low = u16::from(self.source[self.at]);
                        self.at += 1;
                        if low == u16::from(b'\\') {
                            let letter = self.source[self.at];
                            self.at += 1;
                            if let Some((class, _)) = Self::escape_ranges(letter) {
                                ranges.extend(class);
                                continue;
                            }
                            low = u16::from(letter);
                        }
                        match (self.peek(), self.source.get(self.at + 1)) {
                            (Some(b'-'), Some(&high)) if high != b']' => {
                                self.at += 2;
                                ranges.push((low, u16::from(high)));
                            }
                            _ => ranges.push((low, low)),
                        }
                    }
                    self.at += 1;
                    Node::Class { ranges, negated }
                }
                b'\\' => {
                    let letter = self.source[self.at];
                    self.at += 1;
                    match Self::escape_ranges(letter) {
                        Some((ranges, negated)) => Node::Class { ranges, negated },
                        None => Node::Unit(u16::from(letter)),
                    }
                }
                other => Node::Unit(u16::from(other)),
            }
        }
    }

    impl Pattern {
        /// Compile the source of a pattern. It panics on syntax outside the supported subset, which is a defect in this crate's pattern list, not in a caller's input.
        pub fn new(source: &str) -> Self {
            let mut parser = Parser { source: source.as_bytes(), at: 0 };
            let node = parser.alternation();
            assert_eq!(parser.at, source.len(), "unparsed pattern text in {source}");
            Pattern(node)
        }

        /// Whether the pattern matches anywhere in `text`.
        pub fn is_match(&self, text: &[u16]) -> bool {
            (0..=text.len()).any(|start| matches(&self.0, text, start, &mut |_| true))
        }
    }

    fn unit_matches(pattern: u16, unit: u16) -> bool {
        if pattern < 128 && (pattern as u8).is_ascii_alphabetic() {
            unit < 128 && (unit as u8).eq_ignore_ascii_case(&(pattern as u8))
        } else {
            pattern == unit
        }
    }

    fn matches(node: &Node, text: &[u16], at: usize, next: &mut dyn FnMut(usize) -> bool) -> bool {
        match node {
            Node::Unit(unit) => at < text.len() && unit_matches(*unit, text[at]) && next(at + 1),
            Node::Any => at < text.len() && !matches!(text[at], 0x0A | 0x0D | 0x2028 | 0x2029) && next(at + 1),
            Node::Class { ranges, negated } => at < text.len() && (class_has(ranges, text[at]) != *negated) && next(at + 1),
            Node::Start => at == 0 && next(at),
            Node::End => at == text.len() && next(at),
            Node::NotBehind(inner, width) => (at < *width || !matches(inner, text, at - *width, &mut |end| end == at)) && next(at),
            Node::NotAhead(inner) => !matches(inner, text, at, &mut |_| true) && next(at),
            Node::Alt(branches) => branches.iter().any(|branch| matches(branch, text, at, next)),
            Node::Seq(items) => sequence(items, text, at, next),
            Node::Repeat(inner, min, max) => repeat(inner, *min, *max, 0, text, at, next),
        }
    }

    fn sequence(items: &[Node], text: &[u16], at: usize, next: &mut dyn FnMut(usize) -> bool) -> bool {
        match items.split_first() {
            None => next(at),
            Some((first, rest)) => matches(first, text, at, &mut |after| sequence(rest, text, after, next)),
        }
    }

    fn repeat(inner: &Node, min: usize, max: usize, count: usize, text: &[u16], at: usize, next: &mut dyn FnMut(usize) -> bool) -> bool {
        if count < max && matches(inner, text, at, &mut |after| (after != at || count < min) && repeat(inner, min, max, count + 1, text, after, next)) {
            return true;
        }
        count >= min && next(at)
    }

    /// Whether a class holds `unit`, an ASCII letter counting as itself and as its other case.
    fn class_has(ranges: &[(u16, u16)], unit: u16) -> bool {
        let holds = |unit: u16| ranges.iter().any(|(low, high)| (*low..=*high).contains(&unit));
        holds(unit) || (unit < 128 && (unit as u8).is_ascii_alphabetic() && holds(u16::from(unit as u8 ^ 0x20)))
    }

    /// The number of units `node` always matches, which a lookbehind needs to know where to start; it panics on a node of varying width.
    fn width(node: &Node) -> usize {
        match node {
            Node::Unit(_) | Node::Any | Node::Class { .. } => 1,
            Node::Start | Node::End | Node::NotBehind(..) | Node::NotAhead(_) => 0,
            Node::Seq(items) => items.iter().map(width).sum(),
            Node::Alt(branches) => {
                let first = width(&branches[0]);
                assert!(branches.iter().all(|branch| width(branch) == first), "lookbehind of varying width");
                first
            }
            Node::Repeat(inner, min, max) if min == max => width(inner) * min,
            Node::Repeat(..) => panic!("lookbehind of varying width"),
        }
    }
}

use js_regex::Pattern;
use std::sync::LazyLock;

fn compile_each(sources: &[&str]) -> Vec<Pattern> {
    sources.iter().map(|source| Pattern::new(source)).collect()
}

fn compile_alternation(sources: &[&str]) -> Pattern {
    Pattern::new(&sources.join("|"))
}

static OVERFLOW_PATTERNS: LazyLock<Vec<Pattern>> = LazyLock::new(|| {
    compile_each(&[
        r"prompt (?:is )?too long",
        r"prompt exceeds max length",
        r"request_too_large",
        r"input is too long for requested model",
        r"exceeds the context window",
        r"exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))",
        r"input token count.*exceeds the maximum",
        r"maximum prompt length is \d+",
        r"reduce the length of the messages",
        r"maximum context length is \d+ tokens",
        r"exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?",
        r"input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)",
        r"exceeds the limit of \d+",
        r"exceeds the available context size",
        r"greater than the context length",
        r"context window exceeds limit",
        r"exceeded model token limit",
        r"too large for model with \d+ maximum context length",
        r"prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?",
        r"model_context_window_exceeded",
        r"prompt too long; exceeded (?:max )?context length",
        r"range of input length should be",
        r"context[_ ]length[_ ]exceeded",
        r"too many tokens",
        r"token limit exceeded",
        // HTTP 413 means request-size overflow regardless of provider or body text. Match raw SDK errors and formatProviderError's prefix, not a status mentioned in the body.
        r"^(?:[\w -]+ API error \(413\):|413(?:\s|:|$))",
    ])
});
static NON_OVERFLOW_PATTERNS: LazyLock<Vec<Pattern>> = LazyLock::new(|| compile_each(&[r"^(Throttling error|Service unavailable):", r"rate limit", r"too many requests"]));
static CEREBRAS_BODYLESS_OVERFLOW_PATTERN: LazyLock<Pattern> = LazyLock::new(|| Pattern::new(r"^4(?:00|13)\s*(?:status code)?\s*\(no body\)"));
static NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN: LazyLock<Pattern> = LazyLock::new(|| {
    compile_alternation(&[
        "GoUsageLimitError", "FreeUsageLimitError", "Monthly usage limit reached", "available balance", "insufficient_quota", "out of budget", "quota exceeded", "billing",
        "subscription_sharing_usage_limit_exceeded",
        // Sign in with ChatGPT on a ChatGPT Business plan: a usage limit set by the workspace admin. Only the admin can raise it.
        "subscription_sharing_workplace_individual_usage_limit_exceeded",
    ])
});
static RETRYABLE_PROVIDER_ERROR_PATTERN: LazyLock<Pattern> = LazyLock::new(|| {
    compile_alternation(&[
        "overloaded", "server_busy", "servers are currently busy", "currently experiencing high demand", "model is at capacity", "rate.?limit", "too many requests",
        // Status codes must not be part of a longer word or number, e.g. a request id or byte count (#11057).
        "(?<![a-z0-9])(?:429|500|502|503|504|520|524)(?![a-z0-9])",
        "service.?(?:is.?)?(?:temporarily.?)?unavailable", "server.?error", "internal.?error", "provider.?returned.?(?:an.?)?error",
        "exceeded request buffer limit while retrying upstream", "unable to forward upstream", "upstream.?request.?failed", "upstream_stream_read_error", "stream was interrupted",
        "http/?2 stream failed", "network.?error", "connection.?error", "connection.?refused", "connection.?lost", "other side closed", "fetch failed",
        r"request to \S+ failed, reason:", "getaddrinfo", "ENOTFOUND", "EAI_AGAIN", "upstream.?connect", "reset before headers", "socket hang up", "socket connection was closed",
        "timed? out", "timeout", "terminated", "websocket.?closed", "websocket.?error", "ended without", "stream ended before message_stop",
        "stream ended before a terminal response event", "http2 request did not get a response",
        // Codex CLI mid-stream disconnect, relayed by proxies that wrap the Codex CLI, e.g. "stream disconnected before completion: stream closed before response.completed" (#11057).
        "stream disconnected before completion",
        "pending stream has been canceled", "retry delay", "you can retry your request",
        "try your request again", "please retry your request", "ResourceExhausted", "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable",
    ])
});

fn assistant_error(message: &Value) -> (Option<&str>, &str) {
    (message.get("stopReason").and_then(Value::as_str), message.get("errorMessage").and_then(Value::as_str).unwrap_or_default())
}

/// pi-ai's `isRetryableAssistantError`: whether a failed assistant message looks like a transient provider or transport error, so the caller can restart the turn. It is a
/// classifier, not a retry policy: a quota, billing or subscription-limit message is not retryable. Handle [`is_context_overflow`] first.
pub fn is_retryable_assistant_error(message: &Value) -> bool {
    let (stop_reason, error) = assistant_error(message);
    if stop_reason != Some("error") || error.is_empty() {
        return false;
    }
    let text: Vec<u16> = error.encode_utf16().collect();
    !NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN.is_match(&text) && RETRYABLE_PROVIDER_ERROR_PATTERN.is_match(&text)
}

/// pi-ai's `isContextOverflow`: whether an assistant message is a context overflow. It is one when it failed with an overflow error message that is not a rate limit or
/// throttling message, or when it failed with a Cerebras body-less 400 or 413. With a truthy `context_window` it is also one when it stopped normally with more input (input
/// and cacheRead tokens) than the window, or stopped at its length limit with no output and input that fills 99% of the window.
pub fn is_context_overflow(message: &Value, context_window: Option<f64>) -> bool {
    let (stop_reason, error) = assistant_error(message);
    if stop_reason == Some("error") && !error.is_empty() {
        let text: Vec<u16> = error.encode_utf16().collect();
        if !NON_OVERFLOW_PATTERNS.iter().any(|pattern| pattern.is_match(&text)) {
            if OVERFLOW_PATTERNS.iter().any(|pattern| pattern.is_match(&text)) {
                return true;
            }
            if message.get("provider").and_then(Value::as_str) == Some("cerebras") && CEREBRAS_BODYLESS_OVERFLOW_PATTERN.is_match(&text) {
                return true;
            }
        }
    }
    if let Some(window) = context_window.filter(|window| *window != 0.0 && !window.is_nan()) {
        let usage = message.get("usage");
        let field = |key: &str| number(usage.and_then(|usage| usage.get(key)));
        let input_tokens = field("input") + field("cacheRead");
        if stop_reason == Some("stop") && input_tokens > window {
            return true;
        }
        if stop_reason == Some("length") && field("output") == 0.0 && input_tokens >= window * 0.99 {
            return true;
        }
    }
    false
}

/// pi-ai's `isRecoverableLength`: whether a length stop ended below the output limit the caller or model intended (`desired_max_output`, before any context-based clamping), so
/// one compact-and-retry attempt may help.
pub fn is_recoverable_length(message: &Value, desired_max_output: f64) -> bool {
    let output = number(message.get("usage").and_then(|usage| usage.get("output")));
    message.get("stopReason").and_then(Value::as_str) == Some("length") && desired_max_output > 0.0 && output < desired_max_output
}

fn throw_if_aborted(signal: &ProviderSignal) -> ProviderResult<()> {
    if signal.is_cancelled() { Err("This operation was aborted".to_string()) } else { Ok(()) }
}

/// pi-ai's `envApiKeyAuth`: the standard API-key auth of a provider whose key comes from a stored credential or an environment variable. A stored credential's key wins (its env
/// travels with it); otherwise the first of `env_vars` that the auth context finds non-empty resolves, and none resolving is `None`. Its login prompts for the key with a secret
/// prompt and returns an `api_key` credential. The auth has no check, as in Pi.
pub fn env_api_key_auth(name: &str, env_vars: &[&str]) -> APIKeyAuth {
    let prompt_name = name.to_string();
    let env_vars: Vec<String> = env_vars.iter().map(|var| var.to_string()).collect();
    APIKeyAuth {
        name: name.to_string(),
        check: None,
        resolve: Arc::new(move |input: APIKeyAuthInput| {
            throw_if_aborted(&input.signal)?;
            if let Some(credential) = input.credential.as_ref().filter(|credential| truthy(credential.get("key"))) {
                return Ok(Some(AuthResult { auth: json!({ "apiKey": credential["key"] }), env: credential.get("env").filter(|env| env.is_object()).cloned(), source: Some("stored credential".to_string()) }));
            }
            for var in &env_vars {
                let value = (input.ctx.env)(var.clone())?;
                throw_if_aborted(&input.signal)?;
                if let Some(value) = value.filter(|value| !value.is_empty()) {
                    return Ok(Some(AuthResult { auth: json!({ "apiKey": value }), env: None, source: Some(var.clone()) }));
                }
            }
            Ok(None)
        }),
        login: Some(Arc::new(move |interaction: AuthInteraction| {
            throw_if_aborted(&interaction.signal)?;
            let key = (interaction.prompt)(json!({ "type": "secret", "message": format!("Enter {prompt_name}") }))?;
            throw_if_aborted(&interaction.signal)?;
            Ok(json!({ "type": "api_key", "key": key }))
        })),
    }
}

/// pi-ai's `getModelType`: the type of a model, `"chat"`, `"image"` or `"classifier"`. A model without a type is a chat model.
pub fn get_model_type(model: &Value) -> &str {
    model.get("type").and_then(Value::as_str).unwrap_or("chat")
}

/// pi-ai's `isModelType`: whether the model is of the type, a model without a type counting as a chat model.
pub fn is_model_type(model: &Value, model_type: &str) -> bool {
    get_model_type(model) == model_type
}

/// pi-ai's `hasApi`: whether the model is a chat model of the API. A model of another type never matches, even when its `api` names the same API.
pub fn has_api(model: &Value, api: &str) -> bool {
    is_model_type(model, "chat") && model.get("api").and_then(Value::as_str) == Some(api)
}

/// pi-ai's `modelsAreEqual`: whether two models have the same type, id and provider. It is false when either is `None`.
pub fn models_are_equal(a: Option<&Value>, b: Option<&Value>) -> bool {
    let (Some(a), Some(b)) = (a, b) else { return false };
    get_model_type(a) == get_model_type(b) && a.get("id") == b.get("id") && a.get("provider") == b.get("provider")
}

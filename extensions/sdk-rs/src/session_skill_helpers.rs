//! Pi library helpers over session entries and skills: `CURRENT_SESSION_VERSION` (`core/session-manager.ts:41`), `VIRTUAL_MODEL_STATE_ENTRY`
//! (`core/virtual-models.ts:32`), `getLatestCompactionEntry` (`core/session-manager.ts:372`), `parseSkillBlock` (`core/agent-session.ts:177`) and
//! `formatSkillsForPrompt` (`core/skills.ts:358`).

use serde_json::Value;
use std::borrow::Borrow;

/// Pi's `CURRENT_SESSION_VERSION`: the version a session file's header carries.
pub const CURRENT_SESSION_VERSION: u32 = 3;
/// Pi's `VIRTUAL_MODEL_STATE_ENTRY`: the custom entry type that persists the virtual model state.
pub const VIRTUAL_MODEL_STATE_ENTRY: &str = "pi.virtual-model-state";

/// Pi's `getLatestCompactionEntry`: the last `compaction` entry of the session entries, or `None`.
pub fn get_latest_compaction_entry<E: Borrow<Value>>(entries: &[E]) -> Option<&Value> {
    entries.iter().rev().map(Borrow::borrow).find(|entry| entry.get("type").and_then(Value::as_str) == Some("compaction"))
}

/// Pi's `ParsedSkillBlock`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ParsedSkillBlock {
    pub name: String,
    pub location: String,
    pub content: String,
    /// The trimmed text after the block, `None` where Pi's is `undefined` (no text, or only white space).
    pub user_message: Option<String>,
}

/// JavaScript's `String.prototype.trim`: it removes the ECMAScript white space and line terminators, which include U+FEFF and not U+0085.
fn js_trim(text: &str) -> &str {
    text.trim_matches(|c: char| matches!(c, '\t' | '\n' | '\u{b}' | '\u{c}' | '\r' | ' ' | '\u{a0}' | '\u{1680}' | '\u{2028}' | '\u{2029}' | '\u{202f}' | '\u{205f}' | '\u{3000}' | '\u{feff}') || ('\u{2000}'..='\u{200a}').contains(&c))
}

/// Pi's `parseSkillBlock`: it reads `<skill name="N" location="L">\n CONTENT \n</skill>` optionally followed by `\n\n` and the user's message, where the block
/// must span the whole text, or returns `None`. The content ends at the first `\n</skill>` that leaves either nothing or `\n\n` and at least one more character,
/// as Pi's lazy pattern does.
pub fn parse_skill_block(text: &str) -> Option<ParsedSkillBlock> {
    let rest = text.strip_prefix("<skill name=\"")?;
    let (name, rest) = rest.split_once('"')?;
    let rest = rest.strip_prefix(" location=\"")?;
    let (location, rest) = rest.split_once('"')?;
    let rest = rest.strip_prefix(">\n")?;
    if name.is_empty() || location.is_empty() {
        return None;
    }
    const CLOSING: &str = "\n</skill>";
    let mut offset = 0;
    while let Some(found) = rest[offset..].find(CLOSING) {
        let at = offset + found;
        let after = &rest[at + CLOSING.len()..];
        let block = |user_message| ParsedSkillBlock { name: name.to_string(), location: location.to_string(), content: rest[..at].to_string(), user_message };
        if after.is_empty() {
            return Some(block(None));
        }
        if let Some(user) = after.strip_prefix("\n\n").filter(|user| !user.is_empty()) {
            return Some(block(Some(js_trim(user).to_string()).filter(|trimmed| !trimmed.is_empty())));
        }
        offset = at + 1;
    }
    None
}

fn js_truthy(value: Option<&Value>) -> bool {
    match value {
        None | Some(Value::Null) => false,
        Some(Value::Bool(flag)) => *flag,
        Some(Value::String(text)) => !text.is_empty(),
        Some(Value::Number(number)) => number.as_f64().is_some_and(|number| number != 0.0 && !number.is_nan()),
        Some(_) => true,
    }
}

fn escape_xml(text: &str) -> String {
    let mut escaped = String::with_capacity(text.len());
    for c in text.chars() {
        match c {
            '&' => escaped.push_str("&amp;"),
            '<' => escaped.push_str("&lt;"),
            '>' => escaped.push_str("&gt;"),
            '"' => escaped.push_str("&quot;"),
            '\'' => escaped.push_str("&apos;"),
            c => escaped.push(c),
        }
    }
    escaped
}

/// Pi's `formatSkillsForPrompt` over skill objects (the `skills` of the system prompt options): the XML block that lists the skills a model may load, without
/// those that disable model invocation. `file_read_tool` names the tool that loads a skill's file: `"read"` (Pi's default), `"bash"`, or any other value for a
/// reader reachable only through another tool (`"indirect"`).
pub fn format_skills_for_prompt<S: Borrow<Value>>(skills: &[S], file_read_tool: &str) -> String {
    let visible: Vec<&Value> = skills.iter().map(Borrow::borrow).filter(|skill: &&Value| !js_truthy(skill.get("disableModelInvocation"))).collect();
    if visible.is_empty() {
        return String::new();
    }
    let load = match file_read_tool {
        "read" => "Use the read tool to load a skill's file when the task matches its description.",
        "bash" => "Use bash to load a skill's file when the task matches its description.",
        _ => "Load a skill's file when the task matches its description.",
    };
    let mut lines = vec![
        "\n\nThe following skills provide specialized instructions for specific tasks.".to_string(),
        load.to_string(),
        "When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.".to_string(),
        String::new(),
        "<available_skills>".to_string(),
    ];
    let text = |skill: &Value, key: &str| escape_xml(skill.get(key).and_then(Value::as_str).unwrap_or(""));
    for skill in visible {
        lines.push("  <skill>".to_string());
        lines.push(format!("    <name>{}</name>", text(skill, "name")));
        lines.push(format!("    <description>{}</description>", text(skill, "description")));
        lines.push(format!("    <location>{}</location>", text(skill, "filePath")));
        lines.push("  </skill>".to_string());
    }
    lines.push("</available_skills>".to_string());
    lines.join("\n")
}

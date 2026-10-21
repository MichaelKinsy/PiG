//! The widget kinds of the component kit (D107,
//! docs/plan/extension-component-kit.md §2.2): pi-tui's `ScrollView`,
//! `MouseRegion`, `Input`, `Editor` and `CancellableLoader` and
//! coding-agent's `BorderedLoader` and message components. Each is a
//! [`Node`](super::Node) through [`Widget`].

use super::{put, put_opt, put_nonempty, Encoder, Node};
use serde_json::{Map, Value};

/// A widget node of the kit.
#[derive(Debug, Clone, PartialEq)]
pub enum Widget {
    BranchSummary(BranchSummary),
    CompactionSummary(CompactionSummary),
    SkillInvocation(SkillInvocation),
    // widget-variants
}

macro_rules! widget_from {
    ($($kind:ident),*) => {$(
        impl From<$kind> for Node {
            fn from(node: $kind) -> Self {
                Node::Widget(Widget::$kind(node))
            }
        }
    )*};
}
widget_from!(BranchSummary , CompactionSummary , SkillInvocation /* widget-from */);

impl Widget {
    pub(super) fn kind(&self) -> &'static str {
        match self {
            Widget::BranchSummary(_) => "branch-summary",
            Widget::CompactionSummary(_) => "compaction-summary",
            Widget::SkillInvocation(_) => "skill-invocation",
            // widget-kinds
        }
    }

    pub(super) fn encode(&self, enc: &mut Encoder, map: &mut Map<String, Value>, depth: usize) -> Result<(), String> {
        let _ = (&enc, depth);
        match self {
            Widget::BranchSummary(n) => {
                put_opt(map, "id", &n.id);
                put_pad(map, n.output_pad);
                if n.expanded {
                    put(map, "expanded", true);
                }
                put_nonempty(map, "summary", &n.summary);
            }
            Widget::CompactionSummary(n) => {
                put_opt(map, "id", &n.id);
                put_pad(map, n.output_pad);
                if n.expanded {
                    put(map, "expanded", true);
                }
                put_nonempty(map, "summary", &n.summary);
                put(map, "tokensBefore", n.tokens_before);
            }
            Widget::SkillInvocation(n) => {
                put_opt(map, "id", &n.id);
                put_pad(map, n.output_pad);
                if n.expanded {
                    put(map, "expanded", true);
                }
                let mut skill = Map::new();
                put_nonempty(&mut skill, "name", &n.name);
                put_nonempty(&mut skill, "content", &n.content);
                put(map, "skill", Value::Object(skill));
            }
            // widget-encode
        }
        Ok(())
    }
}

/// `outputPad` on the wire: absent for upstream's default 1.
fn put_pad(map: &mut Map<String, Value>, output_pad: u32) {
    if output_pad != 1 {
        put(map, "outputPad", output_pad);
    }
}

/// An input's or an editor's cursor: a logical line and a UTF-16 column (line
/// 0 for an input).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Cursor {
    pub line: usize,
    pub col: usize,
}

impl Cursor {
    pub(super) fn from_wire(value: &Value) -> Option<Cursor> {
        let field = |key: &str| value.get(key).and_then(Value::as_u64).map(|n| n as usize);
        Some(Cursor { line: field("line")?, col: field("col")? })
    }
}

/// pi-tui's `TuiMouseEvent` as a mouse region took it: `x` and `y` are local
/// to the region's first cell, `screen_x` and `screen_y` on the terminal.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Mouse {
    pub kind: String,
    pub button: String,
    pub x: i64,
    pub y: i64,
    pub screen_x: i64,
    pub screen_y: i64,
    pub width: i64,
    pub height: i64,
    pub shift: bool,
    pub alt: bool,
    pub ctrl: bool,
    pub wheel_delta: i64,
    pub click_count: i64,
}

impl Mouse {
    pub(super) fn from_wire(value: &Value) -> Option<Mouse> {
        let int = |key: &str| value.get(key).and_then(Value::as_i64).unwrap_or(0);
        let flag = |key: &str| value.get(key).and_then(Value::as_bool).unwrap_or(false);
        let text = |key: &str| value.get(key).and_then(Value::as_str).unwrap_or("").to_string();
        value.get("type")?;
        Some(Mouse {
            kind: text("type"),
            button: text("button"),
            x: int("x"),
            y: int("y"),
            screen_x: int("screenX"),
            screen_y: int("screenY"),
            width: int("width"),
            height: int("height"),
            shift: flag("shift"),
            alt: flag("alt"),
            ctrl: flag("ctrl"),
            wheel_delta: int("wheelDelta"),
            click_count: int("clickCount"),
        })
    }
}

/// Coding-agent's `BranchSummaryMessageComponent(message, getMarkdownTheme(),
/// outputPad)`: the marker of a summarized branch, with the summary's
/// markdown behind a click or [`BranchSummary::set_expanded`]. It draws with
/// the active theme, as Pi's conversation components do. A node with an id
/// keeps its host component across frames, so a click's toggle lives as long
/// as the node.
#[derive(Debug, Clone, PartialEq)]
pub struct BranchSummary {
    id: Option<String>,
    summary: String,
    output_pad: u32,
    expanded: bool,
}

impl BranchSummary {
    /// A collapsed summary with upstream's outputPad 1.
    pub fn new(summary: impl Into<String>) -> Self {
        Self { id: None, summary: summary.into(), output_pad: 1, expanded: false }
    }

    /// Keeps the host component across frames.
    pub fn id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }

    /// Upstream `setExpanded`.
    pub fn set_expanded(&mut self, expanded: bool) {
        self.expanded = expanded;
    }

    /// Upstream `setOutputPad`.
    pub fn set_output_pad(&mut self, output_pad: u32) {
        self.output_pad = output_pad;
    }
}

/// Coding-agent's `CompactionSummaryMessageComponent(message,
/// getMarkdownTheme(), outputPad)`: the marker of a compaction ("Compacted
/// from 12,345 tokens"), with the summary's markdown behind a click or
/// [`CompactionSummary::set_expanded`]. It draws with the active theme, and a
/// node with an id keeps its host component across frames, as
/// [`BranchSummary`] does.
#[derive(Debug, Clone, PartialEq)]
pub struct CompactionSummary {
    id: Option<String>,
    summary: String,
    tokens_before: u64,
    output_pad: u32,
    expanded: bool,
}

impl CompactionSummary {
    /// A collapsed summary of a context of `tokens_before` tokens with
    /// upstream's outputPad 1.
    pub fn new(summary: impl Into<String>, tokens_before: u64) -> Self {
        Self { id: None, summary: summary.into(), tokens_before, output_pad: 1, expanded: false }
    }

    /// Keeps the host component across frames.
    pub fn id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }

    /// Upstream `setExpanded`.
    pub fn set_expanded(&mut self, expanded: bool) {
        self.expanded = expanded;
    }

    /// Upstream `setOutputPad`.
    pub fn set_output_pad(&mut self, output_pad: u32) {
        self.output_pad = output_pad;
    }
}

/// Coding-agent's `SkillInvocationMessageComponent(skillBlock,
/// getMarkdownTheme(), outputPad)`: a skill's invocation, one line collapsed
/// and the skill's content behind a click or
/// [`SkillInvocation::set_expanded`]. It draws with the active theme, and a
/// node with an id keeps its host component across frames, as
/// [`BranchSummary`] does.
#[derive(Debug, Clone, PartialEq)]
pub struct SkillInvocation {
    id: Option<String>,
    name: String,
    content: String,
    output_pad: u32,
    expanded: bool,
}

impl SkillInvocation {
    /// A collapsed invocation of the skill `name` with `content`, with
    /// upstream's outputPad 1.
    pub fn new(name: impl Into<String>, content: impl Into<String>) -> Self {
        Self { id: None, name: name.into(), content: content.into(), output_pad: 1, expanded: false }
    }

    /// Keeps the host component across frames.
    pub fn id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }

    /// Upstream `setExpanded`.
    pub fn set_expanded(&mut self, expanded: bool) {
        self.expanded = expanded;
    }

    /// Upstream `setOutputPad`.
    pub fn set_output_pad(&mut self, output_pad: u32) {
        self.output_pad = output_pad;
    }
}

// widget-structs

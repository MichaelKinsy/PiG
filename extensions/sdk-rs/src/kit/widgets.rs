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
    CustomMessage(CustomMessage),
    CancellableLoader(CancellableLoader),
    BorderedLoader(BorderedLoader),
    Input(Input),
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
widget_from!(BranchSummary , CompactionSummary , SkillInvocation , CustomMessage , CancellableLoader , BorderedLoader , Input /* widget-from */);

impl Widget {
    pub(super) fn kind(&self) -> &'static str {
        match self {
            Widget::BranchSummary(_) => "branch-summary",
            Widget::CompactionSummary(_) => "compaction-summary",
            Widget::SkillInvocation(_) => "skill-invocation",
            Widget::CustomMessage(_) => "custom-message",
            Widget::CancellableLoader(_) => "cancellable-loader",
            Widget::BorderedLoader(_) => "bordered-loader",
            Widget::Input(_) => "input",
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
            Widget::CustomMessage(n) => {
                put_opt(map, "id", &n.id);
                put_pad(map, n.output_pad);
                if n.expanded {
                    put(map, "expanded", true);
                }
                put_nonempty(map, "customType", &n.custom_type);
                put_nonempty(map, "text", &n.text);
            }
            Widget::CancellableLoader(n) => {
                put(map, "id", n.id.as_str());
                put_loader(map, &n.loader);
            }
            Widget::BorderedLoader(n) => {
                put(map, "id", n.id.as_str());
                put(map, "message", n.message.as_str());
                if !n.cancellable {
                    put(map, "cancellable", false);
                }
                put_opt(map, "frame", &n.frame);
            }
            Widget::Input(n) => {
                put(map, "id", n.id.as_str());
                put_nonempty(map, "value", &n.value);
                put_opt(map, "prompt", &n.prompt);
                put_nonempty(map, "placeholder", &n.placeholder);
                put_opt(map, "placeholderColor", &n.placeholder_color);
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

/// Coding-agent's `CustomMessageComponent(message, undefined,
/// getMarkdownTheme(), outputPad)`: the default rendering of an extension's
/// message, "[customType]" over the content's text in a customMessageBg box. A
/// message's custom renderer is a closure upstream and has no kind here; the
/// message-renderer surfaces draw it. It draws with the active theme, and a
/// node with an id keeps its host component across frames.
#[derive(Debug, Clone, PartialEq)]
pub struct CustomMessage {
    id: Option<String>,
    custom_type: String,
    text: String,
    output_pad: u32,
    expanded: bool,
}

impl CustomMessage {
    /// A message of `custom_type` with `text` (a content array's text blocks
    /// joined with newlines) and upstream's outputPad 1.
    pub fn new(custom_type: impl Into<String>, text: impl Into<String>) -> Self {
        Self { id: None, custom_type: custom_type.into(), text: text.into(), output_pad: 1, expanded: false }
    }

    /// Keeps the host component across frames.
    pub fn id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }

    /// Upstream `setExpanded`; the default rendering does not draw it.
    pub fn set_expanded(&mut self, expanded: bool) {
        self.expanded = expanded;
    }

    /// Upstream `setOutputPad`.
    pub fn set_output_pad(&mut self, output_pad: u32) {
        self.output_pad = output_pad;
    }
}

/// Pi-tui's `CancellableLoader(ui, spinnerColorFn, messageColorFn, message,
/// indicator)`: a [`Loader`](super::Loader) that the focused
/// `tui.select.cancel` key aborts. The host owns its abort state and
/// animation; the abort reaches the extension as an [`super::EventKind::Abort`]
/// event of the node, which the view's focus must name to receive the key.
/// Any other key goes to the extension, as the loader ignores it.
#[derive(Debug, Clone, PartialEq)]
pub struct CancellableLoader {
    id: String,
    loader: super::Loader,
}

impl CancellableLoader {
    /// A cancellable loader named `id` (required, unique within the view) with
    /// the loader's arguments.
    pub fn new(id: impl Into<String>, loader: super::Loader) -> Self {
        Self { id: id.into(), loader }
    }
}

/// A loader's wire fields.
fn put_loader(map: &mut Map<String, Value>, n: &super::Loader) {
    put(map, "message", n.message.as_str());
    put_opt(map, "spinnerColor", &n.spinner_color);
    put_opt(map, "messageColor", &n.message_color);
    if let Some(indicator) = &n.indicator {
        let mut i = Map::new();
        put_opt(&mut i, "frames", &indicator.frames);
        put_opt(&mut i, "intervalMs", &indicator.interval_ms);
        put(map, "indicator", Value::Object(i));
    }
    put_opt(map, "frame", &n.frame);
}


/// Coding-agent's `BorderedLoader(tui, theme, message, {cancellable})`: a
/// loader between two borders with, when cancellable (the default), a cancel
/// key hint that the focused `tui.select.cancel` key aborts
/// ([`super::EventKind::Abort`]). It draws with the view's theme, the one Pi hands
/// the factory that builds it. The host owns its abort state and animation.
#[derive(Debug, Clone, PartialEq)]
pub struct BorderedLoader {
    id: String,
    message: String,
    cancellable: bool,
    frame: Option<u32>,
}

impl BorderedLoader {
    /// A cancellable loader named `id` (required, unique within the view)
    /// showing `message`.
    pub fn new(id: impl Into<String>, message: impl Into<String>) -> Self {
        Self { id: id.into(), message: message.into(), cancellable: true, frame: None }
    }

    /// Upstream's `{cancellable}` option.
    pub fn cancellable(mut self, cancellable: bool) -> Self {
        self.cancellable = cancellable;
        self
    }

    /// The spinner's upstream `currentFrame`; absent starts at 0.
    pub fn frame(mut self, frame: u32) -> Self {
        self.frame = Some(frame);
        self
    }
}

/// Pi-tui's `Input({prompt, placeholder, placeholderStyle})`: a one-line text
/// field. The host owns its value and cursor and runs Input's own key and
/// mouse handling once the view's focus names it, as the focused component of
/// Pi's TUI takes every key. The extension receives Input's callbacks as
/// [`super::EventKind::Submit`] (`onSubmit`) and [`super::EventKind::Escape`]
/// (`onEscape`) events of the node, and a [`super::EventKind::Change`] with the
/// value and cursor after each key or press that changed either.
#[derive(Debug, Clone, PartialEq)]
pub struct Input {
    id: String,
    value: String,
    prompt: Option<String>,
    placeholder: String,
    placeholder_color: Option<String>,
}

impl Input {
    /// An empty input named `id` (required, unique within the view) with
    /// upstream's defaults.
    pub fn new(id: impl Into<String>) -> Self {
        Self { id: id.into(), value: String::new(), prompt: None, placeholder: String::new(), placeholder_color: None }
    }

    /// `setValue`'s argument, applied when it differs from the value the
    /// previous frame sent; the cursor stays where the input left it.
    pub fn value(mut self, value: impl Into<String>) -> Self {
        self.value = value.into();
        self
    }

    /// The options' prompt; absent takes upstream's `"> "`.
    pub fn prompt(mut self, prompt: impl Into<String>) -> Self {
        self.prompt = Some(prompt.into());
        self
    }

    /// The text drawn while the value is empty.
    pub fn placeholder(mut self, placeholder: impl Into<String>) -> Self {
        self.placeholder = placeholder.into();
        self
    }

    /// A theme foreground token for `placeholderStyle`.
    pub fn placeholder_color(mut self, token: impl Into<String>) -> Self {
        self.placeholder_color = Some(token.into());
        self
    }
}

// widget-structs

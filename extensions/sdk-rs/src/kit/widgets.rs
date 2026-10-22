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
    Editor(Editor),
    ScrollView(ScrollView),
    MouseRegion(MouseRegion),
    // widget-variants
}

macro_rules! widget_from {
    ($($kind:ident),* $(,)?) => {$(
        impl From<$kind> for Node {
            fn from(node: $kind) -> Self {
                Node::Widget(Widget::$kind(node))
            }
        }
    )*};
}
widget_from!(
    BranchSummary,
    CompactionSummary,
    SkillInvocation,
    CustomMessage,
    CancellableLoader,
    BorderedLoader,
    Input,
    Editor,
    ScrollView,
    MouseRegion,
    /* widget-from */
);

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
            Widget::Editor(_) => "editor",
            Widget::ScrollView(_) => "scroll-view",
            Widget::MouseRegion(_) => "mouse-region",
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
            Widget::Editor(n) => {
                put(map, "id", n.id.as_str());
                put_nonempty(map, "value", &n.value);
                if n.padding_x != 0 {
                    put(map, "paddingX", n.padding_x);
                }
                if n.autocomplete_max_visible != 0 {
                    put(map, "autocompleteMaxVisible", n.autocomplete_max_visible);
                }
                put_opt(map, "borderColor", &n.border_color);
                if n.disable_submit {
                    put(map, "disableSubmit", true);
                }
                if !n.history.is_empty() {
                    put(map, "history", Value::Array(n.history.iter().map(|h| Value::String(h.clone())).collect()));
                }
                if let Some(a) = &n.autocomplete {
                    put(map, "autocomplete", a.wire());
                }
            }
            Widget::ScrollView(n) => {
                put(map, "id", n.id.as_str());
                put(map, "children", enc.children(std::slice::from_ref(&*n.child), depth)?);
                put_nonempty(map, "follow", &n.follow);
                if n.primary {
                    put(map, "primary", true);
                }
                put_nonempty(map, "overscroll", &n.overscroll);
                put_nonempty(map, "scrollbar", &n.scrollbar);
                put_opt(map, "scrollbarTrackColor", &n.scrollbar_track_color);
                put_opt(map, "scrollbarThumbColor", &n.scrollbar_thumb_color);
                put_opt(map, "scrollbarHideDelayMs", &n.scrollbar_hide_delay_ms);
                put_opt(map, "scrollTop", &n.scroll_top);
            }
            Widget::MouseRegion(n) => {
                put(map, "id", n.id.as_str());
                put(map, "children", enc.children(std::slice::from_ref(&*n.child), depth)?);
                let mut region = Map::new();
                if !n.types.is_empty() {
                    put(&mut region, "types", Value::Array(n.types.iter().map(|t| Value::String(t.clone())).collect()));
                }
                put_opt(&mut region, "handled", &n.handled);
                if n.capture {
                    put(&mut region, "capture", true);
                }
                if n.focus {
                    put(&mut region, "focus", true);
                }
                put_opt(&mut region, "render", &n.render);
                put(map, "region", Value::Object(region));
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

/// Pi-tui's `Editor(tui, theme, {paddingX, autocompleteMaxVisible})` with the
/// setters an author calls on it: a multi-line text editor between two
/// borders. The host owns the text and the cursor and runs Editor's own key and
/// mouse handling once the view's focus names it. The extension receives
/// `onSubmit` as a [`super::EventKind::Submit`] event of the node (the text in
/// the event's value) and a [`super::EventKind::Change`] with the text and
/// cursor after each key or press that changed either. The editor shows
/// `max(5, floor(terminal rows * 0.3))` lines, the terminal's rows being the
/// host's.
#[derive(Debug, Clone, PartialEq)]
pub struct Editor {
    id: String,
    value: String,
    padding_x: u32,
    autocomplete_max_visible: u32,
    border_color: Option<String>,
    disable_submit: bool,
    history: Vec<String>,
    autocomplete: Option<Autocomplete>,
}

impl Editor {
    /// An empty editor named `id` (required, unique within the view) with
    /// upstream's defaults.
    pub fn new(id: impl Into<String>) -> Self {
        Self {
            id: id.into(),
            value: String::new(),
            padding_x: 0,
            autocomplete_max_visible: 0,
            border_color: None,
            disable_submit: false,
            history: Vec::new(),
            autocomplete: None,
        }
    }

    /// `setText`'s argument, applied when it differs from the value the
    /// previous frame sent.
    pub fn value(mut self, value: impl Into<String>) -> Self {
        self.value = value.into();
        self
    }

    /// The options' `paddingX`.
    pub fn padding_x(mut self, padding_x: u32) -> Self {
        self.padding_x = padding_x;
        self
    }

    /// The options' `autocompleteMaxVisible`, clamped to 3..20 by the host; 0
    /// takes upstream's 5.
    pub fn autocomplete_max_visible(mut self, max_visible: u32) -> Self {
        self.autocomplete_max_visible = max_visible;
        self
    }

    /// A theme foreground token for `borderColor`; absent takes upstream's
    /// `"borderMuted"`.
    pub fn border_color(mut self, token: impl Into<String>) -> Self {
        self.border_color = Some(token.into());
        self
    }

    /// Upstream's `disableSubmit` field.
    pub fn disable_submit(mut self, disable_submit: bool) -> Self {
        self.disable_submit = disable_submit;
        self
    }

    /// The `addToHistory` calls, oldest first. A frame that extends the history
    /// the previous frame sent adds the new entries; another history is another
    /// editor.
    pub fn history(mut self, history: impl IntoIterator<Item = impl Into<String>>) -> Self {
        self.history = history.into_iter().map(Into::into).collect();
        self
    }

    /// The provider `setAutocompleteProvider` received.
    pub fn autocomplete(mut self, autocomplete: Autocomplete) -> Self {
        self.autocomplete = Some(autocomplete);
        self
    }
}

/// Pi-tui's `SlashCommand` without `getArgumentCompletions`, which is a closure
/// and cannot cross.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct SlashCommand {
    pub name: String,
    pub description: String,
    pub argument_hint: String,
}

impl SlashCommand {
    pub fn new(name: impl Into<String>) -> Self {
        Self { name: name.into(), description: String::new(), argument_hint: String::new() }
    }

    pub fn description(mut self, description: impl Into<String>) -> Self {
        self.description = description.into();
        self
    }

    pub fn argument_hint(mut self, argument_hint: impl Into<String>) -> Self {
        self.argument_hint = argument_hint.into();
        self
    }
}

/// Pi-tui's `CombinedAutocompleteProvider(commands, basePath)`: the slash
/// commands and the directory its path completion starts from (the host's
/// working directory when empty). The host runs its `getSuggestions`,
/// `applyCompletion` and `shouldTriggerFileCompletion` as the editor asks.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Autocomplete {
    commands: Vec<SlashCommand>,
    base_path: String,
}

impl Autocomplete {
    pub fn new(commands: impl IntoIterator<Item = SlashCommand>) -> Self {
        Self { commands: commands.into_iter().collect(), base_path: String::new() }
    }

    /// The directory path completion starts from.
    pub fn base_path(mut self, base_path: impl Into<String>) -> Self {
        self.base_path = base_path.into();
        self
    }

    /// The provider as the wire carries it: an editor node's `autocomplete` field and `setAutocompleteProvider`'s argument.
    pub(crate) fn wire(&self) -> Value {
        let mut auto = Map::new();
        if !self.commands.is_empty() {
            let commands = self
                .commands
                .iter()
                .map(|c| {
                    let mut command = Map::new();
                    put(&mut command, "name", c.name.as_str());
                    put_nonempty(&mut command, "description", &c.description);
                    put_nonempty(&mut command, "argumentHint", &c.argument_hint);
                    Value::Object(command)
                })
                .collect();
            put(&mut auto, "commands", Value::Array(commands));
        }
        put_nonempty(&mut auto, "basePath", &self.base_path);
        Value::Object(auto)
    }
}

/// Pi-tui's `ScrollView(child, {follow, primary, overscroll, scrollbar,
/// scrollbarTrackStyle, scrollbarThumbStyle, scrollbarHideDelayMs})`: one child
/// that scrolls. A view is laid out without a viewport height, as Pi's layout
/// engine does for a scroll node given none: the host draws the child's rows,
/// padded by a column when the scrollbar is `"always"`, and a frontend scrolls
/// from the state the component reports. The host keeps its component (and
/// its scroll state) across frames.
#[derive(Debug, Clone, PartialEq)]
pub struct ScrollView {
    id: String,
    child: Box<Node>,
    follow: String,
    primary: bool,
    overscroll: String,
    scrollbar: String,
    scrollbar_track_color: Option<String>,
    scrollbar_thumb_color: Option<String>,
    scrollbar_hide_delay_ms: Option<u32>,
    scroll_top: Option<u32>,
}

impl ScrollView {
    /// A scroll view named `id` over `child`.
    pub fn new(id: impl Into<String>, child: impl Into<Node>) -> Self {
        Self {
            id: id.into(),
            child: Box::new(child.into()),
            follow: String::new(),
            primary: false,
            overscroll: String::new(),
            scrollbar: String::new(),
            scrollbar_track_color: None,
            scrollbar_thumb_color: None,
            scrollbar_hide_delay_ms: None,
            scroll_top: None,
        }
    }

    /// `"none"` (the default) or `"end"`.
    pub fn follow(mut self, follow: impl Into<String>) -> Self {
        self.follow = follow.into();
        self
    }

    /// Makes it the scroll view the wheel scrolls first.
    pub fn primary(mut self, primary: bool) -> Self {
        self.primary = primary;
        self
    }

    /// `"chain"` (the default) or `"contain"`.
    pub fn overscroll(mut self, overscroll: impl Into<String>) -> Self {
        self.overscroll = overscroll.into();
        self
    }

    /// `"hidden"` (the default), `"auto"` or `"always"`.
    pub fn scrollbar(mut self, scrollbar: impl Into<String>) -> Self {
        self.scrollbar = scrollbar.into();
        self
    }

    /// A theme foreground token for `scrollbarTrackStyle`.
    pub fn scrollbar_track_color(mut self, token: impl Into<String>) -> Self {
        self.scrollbar_track_color = Some(token.into());
        self
    }

    /// A theme foreground token for `scrollbarThumbStyle`.
    pub fn scrollbar_thumb_color(mut self, token: impl Into<String>) -> Self {
        self.scrollbar_thumb_color = Some(token.into());
        self
    }

    /// The options' `scrollbarHideDelayMs`; absent takes upstream's 1000.
    pub fn scrollbar_hide_delay_ms(mut self, delay_ms: u32) -> Self {
        self.scrollbar_hide_delay_ms = Some(delay_ms);
        self
    }

    /// `scrollTo`'s argument, applied when it differs from the one the previous
    /// frame sent.
    pub fn scroll_top(mut self, scroll_top: u32) -> Self {
        self.scroll_top = Some(scroll_top);
        self
    }
}

/// Pi-tui's `MouseRegion(child, onMouse)`: mouse handling added to a child
/// without changing how it draws. The child gets each event first and the
/// region's handler only the events it leaves. The handler is a closure upstream
/// and crosses as a description: for each event whose type is in `types` (every
/// type when none is named) it returns the result below, and for the others no
/// result. The host answers the terminal at once and reports each event the
/// handler ran for as a [`super::EventKind::Mouse`] event of the node, with
/// coordinates local to the region.
#[derive(Debug, Clone, PartialEq)]
pub struct MouseRegion {
    id: String,
    child: Box<Node>,
    types: Vec<String>,
    handled: Option<bool>,
    capture: bool,
    focus: bool,
    render: Option<bool>,
}

impl MouseRegion {
    /// A region named `id` (required, unique within the view) over `child`
    /// that handles every event.
    pub fn new(id: impl Into<String>, child: impl Into<Node>) -> Self {
        Self { id: id.into(), child: Box::new(child.into()), types: Vec::new(), handled: None, capture: false, focus: false, render: None }
    }

    /// The event types the handler answers: `"press"`, `"release"`, `"click"`,
    /// `"drag"`, `"move"` or `"wheel"`.
    pub fn types(mut self, types: impl IntoIterator<Item = impl Into<String>>) -> Self {
        self.types = types.into_iter().map(Into::into).collect();
        self
    }

    /// The result's `handled`; absent takes true.
    pub fn handled(mut self, handled: bool) -> Self {
        self.handled = Some(handled);
        self
    }

    /// The result's `capture`.
    pub fn capture(mut self, capture: bool) -> Self {
        self.capture = capture;
        self
    }

    /// The result's `focus`.
    pub fn focus(mut self, focus: bool) -> Self {
        self.focus = focus;
        self
    }

    /// The result's `render`; absent takes upstream's per-type default.
    pub fn render(mut self, render: bool) -> Self {
        self.render = Some(render);
        self
    }
}

// widget-structs

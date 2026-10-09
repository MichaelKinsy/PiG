//! The extension component kit (D107): Pi's tui components described as a
//! view the host renders with its tui ports.
//!
//! Build a tree from the node types here, wrap it in a [`View`], and hand it
//! to a view surface: [`crate::ViewComponent`] for `ui.custom`,
//! [`crate::Context::set_widget_view`],
//! [`crate::Context::set_header_view`]/[`crate::Context::set_footer_view`],
//! the `*_view` tool renderers, or
//! [`crate::Extension::message_view_renderer`]/[`crate::Extension::entry_view_renderer`].
//! The view is authoritative: the host renders it at the width it lays the
//! surface out at, so the terminal shows what Pi's components draw.
//!
//! Constructors take upstream's positional arguments; upstream's option bags
//! are builder methods. Every style closure upstream takes is a theme token
//! name here (`"accent"`, `"customMessageBg"`, ...); the host validates
//! tokens, colors and ids, and keeps the previous frame for an invalid view.
//! A component with a `render` of its own is a [`Lines`] range.

mod digest;

use serde_json::{Map, Value};
use std::collections::{HashMap, HashSet};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};

/// One component of a view.
#[derive(Debug, Clone, PartialEq)]
pub enum Node {
    Container(Container),
    Box(Box),
    Text(Text),
    TruncatedText(TruncatedText),
    Markdown(Markdown),
    Spacer(Spacer),
    DynamicBorder(DynamicBorder),
    SelectList(SelectList),
    SettingsList(SettingsList),
    Image(Image),
    Loader(Loader),
    HStack(HStack),
    VStack(VStack),
    Lines(Lines),
    UserMessage(UserMessage),
    AssistantMessage(AssistantMessage),
    ToolExecution(ToolExecution),
    BashExecution(BashExecution),
    Diff(Diff),
}

macro_rules! node_from {
    ($($kind:ident),*) => {$(
        impl From<$kind> for Node {
            fn from(node: $kind) -> Self {
                Node::$kind(node)
            }
        }
    )*};
}
node_from!(
    Container, Box, Text, TruncatedText, Markdown, Spacer, DynamicBorder, SelectList, SettingsList,
    Image, Loader, HStack, VStack, Lines, UserMessage, AssistantMessage, ToolExecution, BashExecution, Diff
);

/// Upstream `Container`: its children one after another.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct Container {
    children: Vec<Node>,
}

impl Container {
    pub fn new() -> Self {
        Self::default()
    }

    /// Appends a child (builder form of [`Self::add_child`]).
    pub fn child(mut self, child: impl Into<Node>) -> Self {
        self.children.push(child.into());
        self
    }

    /// Upstream `addChild`.
    pub fn add_child(&mut self, child: impl Into<Node>) {
        self.children.push(child.into());
    }
}

/// Upstream `Box(paddingX = 1, paddingY = 1, bgFn?)`: its children inside
/// padding, on an optional background.
#[derive(Debug, Clone, PartialEq)]
pub struct Box {
    padding_x: u32,
    padding_y: u32,
    bg: Option<String>,
    children: Vec<Node>,
}

impl Default for Box {
    fn default() -> Self {
        Self::new(1, 1)
    }
}

impl Box {
    pub fn new(padding_x: u32, padding_y: u32) -> Self {
        Self { padding_x, padding_y, bg: None, children: Vec::new() }
    }

    /// The background token (upstream `bgFn`, `theme.bg(token, ...)`).
    pub fn bg(mut self, token: impl Into<String>) -> Self {
        self.bg = Some(token.into());
        self
    }

    /// Appends a child (builder form of [`Self::add_child`]).
    pub fn child(mut self, child: impl Into<Node>) -> Self {
        self.children.push(child.into());
        self
    }

    /// Upstream `addChild`.
    pub fn add_child(&mut self, child: impl Into<Node>) {
        self.children.push(child.into());
    }
}

/// Upstream `Text(text = "", paddingX = 1, paddingY = 1, customBgFn?)`.
#[derive(Debug, Clone, PartialEq)]
pub struct Text {
    text: String,
    padding_x: u32,
    padding_y: u32,
    bg: Option<String>,
}

impl Text {
    pub fn new(text: impl Into<String>, padding_x: u32, padding_y: u32) -> Self {
        Self { text: text.into(), padding_x, padding_y, bg: None }
    }

    /// The background token (upstream `customBgFn`).
    pub fn bg(mut self, token: impl Into<String>) -> Self {
        self.bg = Some(token.into());
        self
    }
}

/// Upstream `TruncatedText(text, paddingX = 0, paddingY = 0)`.
#[derive(Debug, Clone, PartialEq)]
pub struct TruncatedText {
    text: String,
    padding_x: u32,
    padding_y: u32,
}

impl TruncatedText {
    pub fn new(text: impl Into<String>, padding_x: u32, padding_y: u32) -> Self {
        Self { text: text.into(), padding_x, padding_y }
    }
}

/// Upstream Markdown `DefaultTextStyle`, with theme tokens for its color
/// functions.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct TextStyle {
    /// Foreground token.
    pub color: Option<String>,
    /// Background token.
    pub bg_color: Option<String>,
    pub bold: bool,
    pub italic: bool,
    pub strikethrough: bool,
    pub underline: bool,
}

/// Upstream `Markdown(text, paddingX, paddingY, theme, defaultTextStyle?,
/// options?)`, themed with coding-agent's `getMarkdownTheme()`.
#[derive(Debug, Clone, PartialEq)]
pub struct Markdown {
    text: String,
    padding_x: u32,
    padding_y: u32,
    default_text_style: Option<TextStyle>,
    render_latex: Option<bool>,
}

impl Markdown {
    pub fn new(text: impl Into<String>, padding_x: u32, padding_y: u32) -> Self {
        Self {
            text: text.into(),
            padding_x,
            padding_y,
            default_text_style: None,
            render_latex: None,
        }
    }

    pub fn default_text_style(mut self, style: TextStyle) -> Self {
        self.default_text_style = Some(style);
        self
    }

    /// Upstream `MarkdownOptions.renderLatex` (default true).
    pub fn render_latex(mut self, render: bool) -> Self {
        self.render_latex = Some(render);
        self
    }
}

/// Upstream `Spacer(lines = 1)`.
#[derive(Debug, Clone, PartialEq)]
pub struct Spacer {
    lines: u32,
}

impl Default for Spacer {
    fn default() -> Self {
        Self::new(1)
    }
}

impl Spacer {
    pub fn new(lines: u32) -> Self {
        Self { lines }
    }
}

/// Coding-agent `DynamicBorder(color)`: a full-width rule in a foreground
/// token (default `"border"`).
#[derive(Debug, Clone, PartialEq)]
pub struct DynamicBorder {
    color: String,
}

impl Default for DynamicBorder {
    fn default() -> Self {
        Self::new("border")
    }
}

impl DynamicBorder {
    pub fn new(color: impl Into<String>) -> Self {
        Self { color: color.into() }
    }
}

/// Upstream `SelectItem`.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct SelectItem {
    pub value: String,
    pub label: String,
    pub description: Option<String>,
}

impl SelectItem {
    pub fn new(value: impl Into<String>, label: impl Into<String>) -> Self {
        Self { value: value.into(), label: label.into(), description: None }
    }

    pub fn description(mut self, description: impl Into<String>) -> Self {
        self.description = Some(description.into());
        self
    }
}

/// Upstream `SelectListLayoutOptions` without `truncatePrimary`, a closure.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct SelectLayout {
    pub min_primary_column_width: Option<u32>,
    pub max_primary_column_width: Option<u32>,
}

/// Upstream `SelectList(items, maxVisible, theme, layout?)`, themed with
/// coding-agent's `getSelectListTheme()`. The host owns its selection and
/// filter and reports its callbacks as [`Event`]s.
#[derive(Debug, Clone, PartialEq)]
pub struct SelectList {
    id: String,
    items: Vec<SelectItem>,
    max_visible: u32,
    layout: Option<SelectLayout>,
    selected_index: Option<usize>,
    filter: Option<String>,
}

impl SelectList {
    /// `id` names the list in [`Event::node`] and [`View::focus`]; it is
    /// required and unique within the view.
    pub fn new(id: impl Into<String>, items: Vec<SelectItem>, max_visible: u32) -> Self {
        Self {
            id: id.into(),
            items,
            max_visible,
            layout: None,
            selected_index: None,
            filter: None,
        }
    }

    pub fn layout(mut self, layout: SelectLayout) -> Self {
        self.layout = Some(layout);
        self
    }

    /// Upstream `setSelectedIndex`: applied when it differs from the index
    /// this view sent last, so repeating it never undoes the user's moves.
    pub fn selected_index(mut self, index: usize) -> Self {
        self.selected_index = Some(index);
        self
    }

    /// Upstream `setFilter`, applied as [`Self::selected_index`] is.
    pub fn filter(mut self, filter: impl Into<String>) -> Self {
        self.filter = Some(filter.into());
        self
    }
}

/// Upstream `SettingItem`; a submenu is a node the open setting shows in the
/// list's place.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct SettingItem {
    pub id: String,
    pub label: String,
    pub description: Option<String>,
    pub current_value: String,
    pub values: Option<Vec<String>>,
    pub submenu: Option<std::boxed::Box<Node>>,
}

impl SettingItem {
    pub fn new(
        id: impl Into<String>,
        label: impl Into<String>,
        current_value: impl Into<String>,
    ) -> Self {
        Self {
            id: id.into(),
            label: label.into(),
            current_value: current_value.into(),
            ..Self::default()
        }
    }

    pub fn description(mut self, description: impl Into<String>) -> Self {
        self.description = Some(description.into());
        self
    }

    /// The values the confirm key and space cycle through.
    pub fn values(mut self, values: Vec<String>) -> Self {
        self.values = Some(values);
        self
    }

    pub fn submenu(mut self, submenu: impl Into<Node>) -> Self {
        self.submenu = Some(std::boxed::Box::new(submenu.into()));
        self
    }
}

/// Upstream `SettingsList(items, maxVisible, theme, onChange, onCancel,
/// options?)`, themed with coding-agent's `getSettingsListTheme()`. The host
/// owns its state and reports `onChange`/`onCancel` as [`Event`]s.
#[derive(Debug, Clone, PartialEq)]
pub struct SettingsList {
    id: String,
    items: Vec<SettingItem>,
    max_visible: u32,
    enable_search: bool,
    selected_index: Option<usize>,
    filter: Option<String>,
}

impl SettingsList {
    pub fn new(id: impl Into<String>, items: Vec<SettingItem>, max_visible: u32) -> Self {
        Self {
            id: id.into(),
            items,
            max_visible,
            enable_search: false,
            selected_index: None,
            filter: None,
        }
    }

    /// Upstream `SettingsListOptions.enableSearch`.
    pub fn enable_search(mut self, enable: bool) -> Self {
        self.enable_search = enable;
        self
    }

    /// The selected index among the shown settings, applied when it differs
    /// from the index this view sent last.
    pub fn selected_index(mut self, index: usize) -> Self {
        self.selected_index = Some(index);
        self
    }

    /// The search input's text, applied as [`Self::selected_index`] is.
    pub fn filter(mut self, filter: impl Into<String>) -> Self {
        self.filter = Some(filter.into());
        self
    }
}

/// Image bytes and their ref, the lowercase hex SHA-256 of the bytes. A
/// connection sends the bytes once; later frames name the ref.
#[derive(Debug, Clone, PartialEq)]
struct ImageData {
    reference: String,
    mime_type: String,
    data: Arc<[u8]>,
}

impl ImageData {
    fn new(data: impl Into<Vec<u8>>, mime_type: impl Into<String>) -> Self {
        let data: Arc<[u8]> = data.into().into();
        Self { reference: digest::sha256_hex(&data), mime_type: mime_type.into(), data }
    }
}

/// Upstream `Image(base64Data, mimeType, theme, options?)` given the decoded
/// bytes.
#[derive(Debug, Clone, PartialEq)]
pub struct Image {
    image: ImageData,
    max_width_cells: Option<u32>,
    max_height_cells: Option<u32>,
    filename: Option<String>,
    fallback_color: Option<String>,
}

impl Image {
    pub fn new(data: impl Into<Vec<u8>>, mime_type: impl Into<String>) -> Self {
        Self {
            image: ImageData::new(data, mime_type),
            max_width_cells: None,
            max_height_cells: None,
            filename: None,
            fallback_color: None,
        }
    }

    /// The image's ref: the lowercase hex SHA-256 of its bytes.
    pub fn reference(&self) -> &str {
        &self.image.reference
    }

    pub fn max_width_cells(mut self, cells: u32) -> Self {
        self.max_width_cells = Some(cells);
        self
    }

    pub fn max_height_cells(mut self, cells: u32) -> Self {
        self.max_height_cells = Some(cells);
        self
    }

    pub fn filename(mut self, filename: impl Into<String>) -> Self {
        self.filename = Some(filename.into());
        self
    }

    /// The foreground token of the text fallback (upstream
    /// `ImageTheme.fallbackColor`).
    pub fn fallback_color(mut self, token: impl Into<String>) -> Self {
        self.fallback_color = Some(token.into());
        self
    }
}

/// Upstream `LoaderIndicatorOptions`. `frames: Some(vec![])` hides the
/// indicator.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct LoaderIndicator {
    pub frames: Option<Vec<String>>,
    pub interval_ms: Option<u32>,
}

/// Upstream `Loader(ui, spinnerColorFn, messageColorFn, message =
/// "Loading...", indicator?)`. The host animates it while the surface is
/// mounted.
#[derive(Debug, Clone, PartialEq)]
pub struct Loader {
    message: String,
    spinner_color: Option<String>,
    message_color: Option<String>,
    indicator: Option<LoaderIndicator>,
    frame: Option<u32>,
}

impl Default for Loader {
    fn default() -> Self {
        Self::new("Loading...")
    }
}

impl Loader {
    pub fn new(message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
            spinner_color: None,
            message_color: None,
            indicator: None,
            frame: None,
        }
    }

    /// The spinner's foreground token (upstream `spinnerColorFn`).
    pub fn spinner_color(mut self, token: impl Into<String>) -> Self {
        self.spinner_color = Some(token.into());
        self
    }

    /// The message's foreground token (upstream `messageColorFn`).
    pub fn message_color(mut self, token: impl Into<String>) -> Self {
        self.message_color = Some(token.into());
        self
    }

    pub fn indicator(mut self, indicator: LoaderIndicator) -> Self {
        self.indicator = Some(indicator);
        self
    }

    /// The frame the host's animation starts from (upstream `currentFrame`).
    pub fn frame(mut self, frame: u32) -> Self {
        self.frame = Some(frame);
        self
    }
}

/// Upstream `StackEntryOptions` without `visible`, a closure. `basis: None`
/// is `"auto"`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct StackEntry {
    pub basis: Option<u32>,
    pub grow: Option<u32>,
    pub shrink: Option<u32>,
    pub min_size: Option<u32>,
    pub max_size: Option<u32>,
}

/// Upstream `StackOptions.align`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum Align {
    #[default]
    Stretch,
    Start,
    Center,
    End,
}

impl Align {
    fn as_str(self) -> &'static str {
        match self {
            Align::Stretch => "stretch",
            Align::Start => "start",
            Align::Center => "center",
            Align::End => "end",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Default)]
struct StackFields {
    children: Vec<(Node, Option<StackEntry>)>,
    gap: Option<u32>,
    align: Option<Align>,
}

/// Upstream `HStack(children?, options?)`: children side by side.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct HStack {
    stack: StackFields,
}

impl HStack {
    pub fn new() -> Self {
        Self::default()
    }

    /// Appends a child with upstream's default entry options.
    pub fn child(mut self, child: impl Into<Node>) -> Self {
        self.stack.children.push((child.into(), None));
        self
    }

    /// Appends a child with entry options (upstream `StackEntry`).
    pub fn child_with(mut self, child: impl Into<Node>, entry: StackEntry) -> Self {
        self.stack.children.push((child.into(), Some(entry)));
        self
    }

    /// Upstream `addChild(component, options?)`.
    pub fn add_child(&mut self, child: impl Into<Node>, entry: Option<StackEntry>) {
        self.stack.children.push((child.into(), entry));
    }

    /// Upstream `StackOptions.gap` (default 0).
    pub fn gap(mut self, gap: u32) -> Self {
        self.stack.gap = Some(gap);
        self
    }

    /// Upstream `StackOptions.align` (default stretch).
    pub fn align(mut self, align: Align) -> Self {
        self.stack.align = Some(align);
        self
    }
}

/// Upstream `VStack(children?, options?)`: children one under another.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct VStack {
    stack: StackFields,
}

impl VStack {
    pub fn new() -> Self {
        Self::default()
    }

    /// Appends a child with upstream's default entry options.
    pub fn child(mut self, child: impl Into<Node>) -> Self {
        self.stack.children.push((child.into(), None));
        self
    }

    /// Appends a child with entry options (upstream `StackEntry`).
    pub fn child_with(mut self, child: impl Into<Node>, entry: StackEntry) -> Self {
        self.stack.children.push((child.into(), Some(entry)));
        self
    }

    /// Upstream `addChild(component, options?)`.
    pub fn add_child(&mut self, child: impl Into<Node>, entry: Option<StackEntry>) {
        self.stack.children.push((child.into(), entry));
    }

    /// Upstream `StackOptions.gap` (default 0).
    pub fn gap(mut self, gap: u32) -> Self {
        self.stack.gap = Some(gap);
        self
    }

    /// Upstream `StackOptions.align` (default stretch).
    pub fn align(mut self, align: Align) -> Self {
        self.stack.align = Some(align);
        self
    }
}

/// Rows a component of the extension's own drew, shown verbatim. The image
/// and progress annotations tell a frontend what the rows depict; the SDK
/// sends them only while a frontend is attached.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct Lines {
    content: Vec<String>,
    image: Option<ImageData>,
    progress: Option<(f64, f64)>,
    list: Option<List>,
}

/// One row of a [`List`] annotation.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct ListItem {
    pub label: String,
    pub detail: Option<String>,
    pub columns: Option<Vec<String>>,
}

/// Frontend-only: the rows of a [`Lines`] node are a list, one item per row,
/// which a frontend may draw as a native list. `selected: None` is no
/// selection.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct List {
    pub items: Vec<ListItem>,
    pub selected: Option<usize>,
}

impl Lines {
    pub fn new(content: Vec<String>) -> Self {
        Self { content, ..Self::default() }
    }

    /// Frontend-only: the rows depict this image (for example a half-block
    /// cover), which a frontend may draw in their place.
    pub fn image(mut self, data: impl Into<Vec<u8>>, mime_type: impl Into<String>) -> Self {
        self.image = Some(ImageData::new(data, mime_type));
        self
    }

    /// Frontend-only: the rows show `value` in `0..=max` (for example a play
    /// bar), which a frontend may draw as a native bar.
    pub fn progress(mut self, value: f64, max: f64) -> Self {
        self.progress = Some((value, max));
        self
    }

    /// Frontend-only: the rows are this list, one item per row (for example
    /// a track table that keeps its own terminal look). The host rejects a
    /// list whose item count differs from the rows.
    pub fn list(mut self, list: List) -> Self {
        self.list = Some(list);
        self
    }
}

// The conversation kinds are Pi's conversation components, which Pi exports
// to extensions: a user message, an assistant message with its thinking, a
// tool call's card, a `!` command and a colored diff. The host draws each with
// the port PiG's main transcript uses, and a frontend draws them as it draws
// the transcript. A node with an id keeps its host component across frames
// while its constructor arguments stay the same, as a Pi author keeps a
// component and calls its update methods; then a field that differs from the
// value sent last applies as that method. See
// docs/plan/extension-component-kit.md §2.1.

/// Upstream `UserMessageComponent(text, getMarkdownTheme(), outputPad)`: the
/// user's Markdown on the user message background.
#[derive(Debug, Clone, PartialEq)]
pub struct UserMessage {
    text: String,
    output_pad: u32,
}

impl UserMessage {
    /// A user message with upstream's outputPad 1.
    pub fn new(text: impl Into<String>) -> Self {
        Self { text: text.into(), output_pad: 1 }
    }

    /// Upstream `setOutputPad` (0 or 1, the values of Pi's outputPad setting).
    pub fn set_output_pad(&mut self, padding: u32) {
        self.output_pad = padding;
    }
}

/// A content block of an assistant message.
#[derive(Debug, Clone, PartialEq)]
pub enum ContentBlock {
    Text(String),
    Thinking(String),
    /// A tool call: the component draws none; it separates thinking runs and
    /// leaves abort and error lines to the tool cards.
    ToolCall,
}

impl ContentBlock {
    pub fn text(text: impl Into<String>) -> Self {
        ContentBlock::Text(text.into())
    }

    pub fn thinking(thinking: impl Into<String>) -> Self {
        ContentBlock::Thinking(thinking.into())
    }

    pub fn tool_call() -> Self {
        ContentBlock::ToolCall
    }
}

/// The part of upstream `AssistantMessage` the component draws.
/// `stop_reason` is `"stop"` (also `""`), `"length"`, `"toolUse"`, `"error"`
/// or `"aborted"`.
#[derive(Debug, Clone, PartialEq, Default)]
pub struct Message {
    pub content: Vec<ContentBlock>,
    pub stop_reason: String,
    pub error_message: String,
}

/// Upstream `AssistantMessageComponent(message, hideThinkingBlock,
/// getMarkdownTheme(), hiddenThinkingLabel, outputPad)`: text blocks as
/// Markdown, thinking runs in the thinking color (or the hidden label), and
/// a length, abort or error line. A click on a thinking run toggles it on the
/// host.
#[derive(Debug, Clone, PartialEq)]
pub struct AssistantMessage {
    id: Option<String>,
    message: Option<Message>,
    hide_thinking_block: bool,
    hidden_thinking_label: String,
    output_pad: u32,
    is_streaming: bool,
}

impl AssistantMessage {
    /// An assistant message with upstream's defaults: thinking shown, the
    /// label "Thinking..." and outputPad 1. `None` draws nothing.
    pub fn new(message: Option<Message>) -> Self {
        Self {
            id: None,
            message,
            hide_thinking_block: false,
            hidden_thinking_label: "Thinking...".into(),
            output_pad: 1,
            is_streaming: false,
        }
    }

    /// Keeps the host component across frames.
    pub fn id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }

    /// Upstream `updateContent(message, isStreaming)`.
    pub fn update_content(&mut self, message: Message, is_streaming: bool) {
        self.message = Some(message);
        self.is_streaming = is_streaming;
    }

    /// Upstream `setHideThinkingBlock`; on the host it also clears the runs a
    /// click toggled.
    pub fn set_hide_thinking_block(&mut self, hide: bool) {
        self.hide_thinking_block = hide;
    }

    /// Upstream `setHiddenThinkingLabel`.
    pub fn set_hidden_thinking_label(&mut self, label: impl Into<String>) {
        self.hidden_thinking_label = label.into();
    }

    /// Upstream `setOutputPad` (0 or 1).
    pub fn set_output_pad(&mut self, padding: u32) {
        self.output_pad = padding;
    }
}

/// The renderers of a [`ToolExecution`], since a tool definition is closures.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum ToolDefinition {
    /// The built-in tool's definition when the tool name has one (read,
    /// bash, powershell, edit, write, grep, find, ls), and a definition
    /// without renderers otherwise: the card the main transcript draws for a
    /// tool without renderers of its own.
    #[default]
    Builtin,
    /// A definition without renderers (`{}`): Pi's call and result fallbacks
    /// for any name.
    Empty,
}

/// A content block of a tool result: [`Self::text`] or [`Self::image`].
#[derive(Debug, Clone, PartialEq)]
pub struct ToolResultContent {
    text: String,
    image: Option<ImageData>,
}

impl ToolResultContent {
    pub fn text(text: impl Into<String>) -> Self {
        Self { text: text.into(), image: None }
    }

    /// An image block given the decoded bytes. The SDK sends them as other
    /// kit images, once per connection.
    pub fn image(data: impl Into<Vec<u8>>, mime_type: impl Into<String>) -> Self {
        Self { text: String::new(), image: Some(ImageData::new(data, mime_type)) }
    }
}

/// The result upstream `updateResult` receives. `details` is any JSON value
/// the built-in renderers read (an edit's diff, a read's truncation).
#[derive(Debug, Clone, PartialEq, Default)]
pub struct ToolResult {
    pub content: Vec<ToolResultContent>,
    pub is_error: bool,
    pub details: Option<Value>,
}

/// Upstream `ToolExecutionComponent(toolName, toolCallId, args, {showImages,
/// imageWidthCells}, toolDefinition, ui, cwd)` and its state: the tool card
/// the main transcript draws. A click on a result toggles its expansion on
/// the host.
#[derive(Debug, Clone, PartialEq)]
pub struct ToolExecution {
    id: Option<String>,
    tool_name: String,
    tool_call_id: String,
    args: Value,
    tool_definition: ToolDefinition,
    cwd: String,
    show_images: bool,
    image_width_cells: u32,
    execution_started: bool,
    args_complete: bool,
    expanded: bool,
    result: Option<ToolResult>,
    is_partial: bool,
}

impl ToolExecution {
    /// A tool card with upstream's defaults: the built-in definition, images
    /// shown 60 cells wide, and no result yet. `Value::Null` args are `{}`.
    pub fn new(
        tool_name: impl Into<String>,
        tool_call_id: impl Into<String>,
        args: Value,
        cwd: impl Into<String>,
    ) -> Self {
        Self {
            id: None,
            tool_name: tool_name.into(),
            tool_call_id: tool_call_id.into(),
            args,
            tool_definition: ToolDefinition::Builtin,
            cwd: cwd.into(),
            show_images: true,
            image_width_cells: 60,
            execution_started: false,
            args_complete: false,
            expanded: false,
            result: None,
            is_partial: true,
        }
    }

    /// Keeps the host component across frames, and with it the card's
    /// renderer state, such as a shell command's elapsed time.
    pub fn id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }

    /// The card's renderers (default [`ToolDefinition::Builtin`]).
    pub fn tool_definition(mut self, definition: ToolDefinition) -> Self {
        self.tool_definition = definition;
        self
    }

    /// Upstream `updateArgs`.
    pub fn update_args(&mut self, args: Value) {
        self.args = args;
    }

    /// Upstream `markExecutionStarted`.
    pub fn mark_execution_started(&mut self) {
        self.execution_started = true;
    }

    /// Upstream `setArgsComplete`.
    pub fn set_args_complete(&mut self) {
        self.args_complete = true;
    }

    /// Upstream `updateResult(result, isPartial)`.
    pub fn update_result(&mut self, result: ToolResult, is_partial: bool) {
        self.result = Some(result);
        self.is_partial = is_partial;
    }

    /// Upstream `setExpanded`.
    pub fn set_expanded(&mut self, expanded: bool) {
        self.expanded = expanded;
    }

    /// Upstream `setShowImages`.
    pub fn set_show_images(&mut self, show: bool) {
        self.show_images = show;
    }

    /// Upstream `setImageWidthCells`: at least 1.
    pub fn set_image_width_cells(&mut self, width: u32) {
        self.image_width_cells = width.max(1);
    }
}

/// Upstream `setComplete`'s arguments.
#[derive(Debug, Clone, PartialEq)]
struct BashComplete {
    exit_code: Option<i64>,
    cancelled: bool,
    truncated: bool,
    full_output_path: String,
}

/// Upstream `BashExecutionComponent(command, ui, excludeFromContext)`: a `!`
/// command between two borders, with its spinner while it runs (the host
/// animates it) and its status after.
#[derive(Debug, Clone, PartialEq)]
pub struct BashExecution {
    id: Option<String>,
    command: String,
    exclude_from_context: bool,
    output: String,
    complete: Option<BashComplete>,
    expanded: bool,
}

impl BashExecution {
    /// A running command.
    pub fn new(command: impl Into<String>, exclude_from_context: bool) -> Self {
        Self {
            id: None,
            command: command.into(),
            exclude_from_context,
            output: String::new(),
            complete: None,
            expanded: false,
        }
    }

    /// Keeps the host component across frames.
    pub fn id(mut self, id: impl Into<String>) -> Self {
        self.id = Some(id.into());
        self
    }

    /// Upstream `appendOutput`. The host cleans the output as upstream does:
    /// no ANSI escapes, and "\r\n" and "\r" as "\n".
    pub fn append_output(&mut self, chunk: &str) {
        self.output.push_str(chunk);
    }

    /// Upstream `setComplete(exitCode, cancelled, truncated ? {truncated} :
    /// undefined, fullOutputPath)`: the exit code (`None` when unknown) and
    /// where the full output is (`""` for nowhere).
    pub fn set_complete(
        &mut self,
        exit_code: Option<i64>,
        cancelled: bool,
        truncated: bool,
        full_output_path: impl Into<String>,
    ) {
        self.complete = Some(BashComplete { exit_code, cancelled, truncated, full_output_path: full_output_path.into() });
    }

    /// Upstream `setExpanded`.
    pub fn set_expanded(&mut self, expanded: bool) {
        self.expanded = expanded;
    }
}

/// Upstream `renderDiff(diff, {filePath})` drawn in a `Text(…, paddingX,
/// paddingY)`, as Pi's edit renderer draws it: context, removed and added
/// lines in the diff colors, with a changed word inverted.
#[derive(Debug, Clone, PartialEq)]
pub struct Diff {
    diff: String,
    file_path: String,
    padding_x: u32,
    padding_y: u32,
}

impl Diff {
    /// A diff without padding.
    pub fn new(diff: impl Into<String>) -> Self {
        Self { diff: diff.into(), file_path: String::new(), padding_x: 0, padding_y: 0 }
    }

    /// Upstream `filePath`.
    pub fn file_path(mut self, path: impl Into<String>) -> Self {
        self.file_path = path.into();
        self
    }

    /// The enclosing `Text`'s padding.
    pub fn padding(mut self, padding_x: u32, padding_y: u32) -> Self {
        self.padding_x = padding_x;
        self.padding_y = padding_y;
        self
    }
}

/// A component tree with the surface's focus and theme overrides.
#[derive(Debug, Clone, PartialEq)]
pub struct View {
    root: Node,
    focus: Option<String>,
    theme: Vec<(String, String)>,
}

impl View {
    pub fn new(root: impl Into<Node>) -> Self {
        Self { root: root.into(), focus: None, theme: Vec::new() }
    }

    /// The id of the select-list or settings-list that receives the keys it
    /// binds (`ui.custom` only).
    pub fn focus(mut self, id: impl Into<String>) -> Self {
        self.focus = Some(id.into());
        self
    }

    /// Overrides a theme token for this surface only: foreground `accent`,
    /// `muted`, `dim`, `text`, `border`, `borderAccent`, `borderMuted`,
    /// `success`, `warning`, `error`; background `selectedBg`,
    /// `customMessageBg`. `color` is `#rrggbb`.
    pub fn theme(mut self, token: impl Into<String>, color: impl Into<String>) -> Self {
        let token = token.into();
        let color = color.into();
        match self.theme.iter_mut().find(|(name, _)| *name == token) {
            Some(slot) => slot.1 = color,
            None => self.theme.push((token, color)),
        }
        self
    }

    /// Encodes the view as the wire's `ViewPayload` without `images`, and
    /// collects the images its frame references. `frontend` keeps the
    /// frontend-only `lines` annotations. A tree deeper than the host's bound
    /// is an error, as in the Go SDK.
    pub(crate) fn encode(&self, frontend: bool) -> Result<Encoded, String> {
        let mut encoder = Encoder { frontend, images: Vec::new() };
        let mut view = Map::new();
        view.insert("root".into(), encoder.node(&self.root, 0)?);
        if let Some(focus) = &self.focus {
            view.insert("focus".into(), Value::from(focus.as_str()));
        }
        if !self.theme.is_empty() {
            let theme: Map<String, Value> = self
                .theme
                .iter()
                .map(|(token, color)| (token.clone(), Value::from(color.as_str())))
                .collect();
            view.insert("theme".into(), Value::Object(theme));
        }
        Ok(Encoded { body: Value::Object(view), images: encoder.images })
    }
}

/// The host's depth bound: deeper trees are invalid, and encoding stops there.
const MAX_DEPTH: usize = 64;

/// A view's wire body and the images it references, one per ref.
#[derive(Debug, Clone)]
pub(crate) struct Encoded {
    pub(crate) body: Value,
    images: Vec<ImageData>,
}

impl Encoded {
    pub(crate) fn references(&self, refs: &HashSet<String>) -> bool {
        self.images.iter().any(|image| refs.contains(&image.reference))
    }

    pub(crate) fn refs(&self) -> Vec<String> {
        self.images.iter().map(|image| image.reference.clone()).collect()
    }
}

fn put(map: &mut Map<String, Value>, key: &str, value: impl Into<Value>) {
    map.insert(key.into(), value.into());
}

fn put_opt<T: Into<Value> + Clone>(map: &mut Map<String, Value>, key: &str, value: &Option<T>) {
    if let Some(value) = value {
        map.insert(key.into(), value.clone().into());
    }
}

fn put_nonempty(map: &mut Map<String, Value>, key: &str, value: &str) {
    if !value.is_empty() {
        map.insert(key.into(), value.into());
    }
}

struct Encoder {
    frontend: bool,
    images: Vec<ImageData>,
}

impl Encoder {
    fn add_image(&mut self, image: &ImageData) {
        if !self.images.iter().any(|known| known.reference == image.reference) {
            self.images.push(image.clone());
        }
    }

    fn children(&mut self, children: &[Node], depth: usize) -> Result<Value, String> {
        children.iter().map(|child| self.node(child, depth + 1)).collect::<Result<Vec<_>, _>>().map(Value::Array)
    }

    fn node(&mut self, node: &Node, depth: usize) -> Result<Value, String> {
        if depth >= MAX_DEPTH {
            return Err(format!("kit: view deeper than {MAX_DEPTH} nodes"));
        }
        let mut map = Map::new();
        let kind = match node {
            Node::Container(_) => "container",
            Node::Box(_) => "box",
            Node::Text(_) => "text",
            Node::TruncatedText(_) => "truncated-text",
            Node::Markdown(_) => "markdown",
            Node::Spacer(_) => "spacer",
            Node::DynamicBorder(_) => "dynamic-border",
            Node::SelectList(_) => "select-list",
            Node::SettingsList(_) => "settings-list",
            Node::Image(_) => "image",
            Node::Loader(_) => "loader",
            Node::HStack(_) => "hstack",
            Node::VStack(_) => "vstack",
            Node::Lines(_) => "lines",
            Node::UserMessage(_) => "user-message",
            Node::AssistantMessage(_) => "assistant-message",
            Node::ToolExecution(_) => "tool-execution",
            Node::BashExecution(_) => "bash-execution",
            Node::Diff(_) => "diff",
        };
        put(&mut map, "kind", kind);
        match node {
            Node::Container(n) => {
                if !n.children.is_empty() {
                    put(&mut map, "children", self.children(&n.children, depth)?);
                }
            }
            Node::Box(n) => {
                if !n.children.is_empty() {
                    put(&mut map, "children", self.children(&n.children, depth)?);
                }
                put(&mut map, "paddingX", n.padding_x);
                put(&mut map, "paddingY", n.padding_y);
                put_opt(&mut map, "bg", &n.bg);
            }
            Node::Text(n) => {
                put(&mut map, "text", n.text.as_str());
                put(&mut map, "paddingX", n.padding_x);
                put(&mut map, "paddingY", n.padding_y);
                put_opt(&mut map, "bg", &n.bg);
            }
            Node::TruncatedText(n) => {
                put(&mut map, "text", n.text.as_str());
                put(&mut map, "paddingX", n.padding_x);
                put(&mut map, "paddingY", n.padding_y);
            }
            Node::Markdown(n) => {
                put(&mut map, "text", n.text.as_str());
                put(&mut map, "paddingX", n.padding_x);
                put(&mut map, "paddingY", n.padding_y);
                if let Some(style) = &n.default_text_style {
                    let mut s = Map::new();
                    put_opt(&mut s, "color", &style.color);
                    put_opt(&mut s, "bgColor", &style.bg_color);
                    for (key, on) in [
                        ("bold", style.bold),
                        ("italic", style.italic),
                        ("strikethrough", style.strikethrough),
                        ("underline", style.underline),
                    ] {
                        if on {
                            put(&mut s, key, true);
                        }
                    }
                    put(&mut map, "defaultTextStyle", Value::Object(s));
                }
                put_opt(&mut map, "renderLatex", &n.render_latex);
            }
            Node::Spacer(n) => put(&mut map, "lines", n.lines),
            Node::DynamicBorder(n) => put(&mut map, "color", n.color.as_str()),
            Node::SelectList(n) => {
                put(&mut map, "id", n.id.as_str());
                let items = n
                    .items
                    .iter()
                    .map(|item| {
                        let mut i = Map::new();
                        put(&mut i, "value", item.value.as_str());
                        put(&mut i, "label", item.label.as_str());
                        put_opt(&mut i, "description", &item.description);
                        Value::Object(i)
                    })
                    .collect();
                put(&mut map, "items", Value::Array(items));
                put(&mut map, "maxVisible", n.max_visible);
                if let Some(layout) = &n.layout {
                    let mut l = Map::new();
                    put_opt(&mut l, "minPrimaryColumnWidth", &layout.min_primary_column_width);
                    put_opt(&mut l, "maxPrimaryColumnWidth", &layout.max_primary_column_width);
                    put(&mut map, "layout", Value::Object(l));
                }
                put_opt(&mut map, "selectedIndex", &n.selected_index.map(|i| i as u64));
                put_opt(&mut map, "filter", &n.filter);
            }
            Node::SettingsList(n) => {
                put(&mut map, "id", n.id.as_str());
                let mut items = Vec::with_capacity(n.items.len());
                for item in &n.items {
                    let mut i = Map::new();
                    put(&mut i, "id", item.id.as_str());
                    put(&mut i, "label", item.label.as_str());
                    put_opt(&mut i, "description", &item.description);
                    put(&mut i, "currentValue", item.current_value.as_str());
                    put_opt(&mut i, "values", &item.values);
                    if let Some(submenu) = &item.submenu {
                        put(&mut i, "submenu", self.node(submenu, depth + 1)?);
                    }
                    items.push(Value::Object(i));
                }
                put(&mut map, "items", Value::Array(items));
                put(&mut map, "maxVisible", n.max_visible);
                if n.enable_search {
                    put(&mut map, "enableSearch", true);
                }
                put_opt(&mut map, "selectedIndex", &n.selected_index.map(|i| i as u64));
                put_opt(&mut map, "filter", &n.filter);
            }
            Node::Image(n) => {
                put(&mut map, "ref", n.image.reference.as_str());
                put(&mut map, "mimeType", n.image.mime_type.as_str());
                put_opt(&mut map, "maxWidthCells", &n.max_width_cells);
                put_opt(&mut map, "maxHeightCells", &n.max_height_cells);
                put_opt(&mut map, "filename", &n.filename);
                put_opt(&mut map, "fallbackColor", &n.fallback_color);
                self.add_image(&n.image);
            }
            Node::Loader(n) => {
                put(&mut map, "message", n.message.as_str());
                put_opt(&mut map, "spinnerColor", &n.spinner_color);
                put_opt(&mut map, "messageColor", &n.message_color);
                if let Some(indicator) = &n.indicator {
                    let mut i = Map::new();
                    put_opt(&mut i, "frames", &indicator.frames);
                    put_opt(&mut i, "intervalMs", &indicator.interval_ms);
                    put(&mut map, "indicator", Value::Object(i));
                }
                put_opt(&mut map, "frame", &n.frame);
            }
            Node::HStack(HStack { stack }) | Node::VStack(VStack { stack }) => {
                if !stack.children.is_empty() {
                    let mut encoded = Vec::with_capacity(stack.children.len());
                    for (child, entry) in &stack.children {
                        let mut child = self.node(child, depth + 1)?;
                        // Default entry options travel as no options, as upstream's addChild records none.
                        if let (Some(entry), Value::Object(child)) = (entry.filter(|entry| *entry != StackEntry::default()), &mut child) {
                            let mut e = Map::new();
                            put_opt(&mut e, "basis", &entry.basis);
                            put_opt(&mut e, "grow", &entry.grow);
                            put_opt(&mut e, "shrink", &entry.shrink);
                            put_opt(&mut e, "minSize", &entry.min_size);
                            put_opt(&mut e, "maxSize", &entry.max_size);
                            put(child, "stack", Value::Object(e));
                        }
                        encoded.push(child);
                    }
                    put(&mut map, "children", Value::Array(encoded));
                }
                put_opt(&mut map, "gap", &stack.gap);
                put_opt(&mut map, "align", &stack.align.map(Align::as_str));
            }
            Node::Lines(n) => {
                put(&mut map, "content", n.content.clone());
                if self.frontend {
                    if let Some(image) = &n.image {
                        put(&mut map, "image", serde_json::json!({"ref": image.reference}));
                        self.add_image(image);
                    }
                    if let Some((value, max)) = n.progress {
                        put(&mut map, "progress", serde_json::json!({"value": value, "max": max}));
                    }
                    if let Some(list) = &n.list {
                        let items: Vec<Value> = list
                            .items
                            .iter()
                            .map(|item| {
                                let mut i = Map::new();
                                put(&mut i, "label", item.label.as_str());
                                put_opt(&mut i, "detail", &item.detail);
                                put_opt(&mut i, "columns", &item.columns);
                                Value::Object(i)
                            })
                            .collect();
                        let selected = list.selected.map_or(-1, |index| index as i64);
                        put(&mut map, "list", serde_json::json!({"items": items, "selectedIndex": selected}));
                    }
                }
            }
            Node::UserMessage(n) => {
                put(&mut map, "text", n.text.as_str());
                put(&mut map, "outputPad", n.output_pad);
            }
            Node::AssistantMessage(n) => {
                put_opt(&mut map, "id", &n.id);
                if let Some(message) = &n.message {
                    let content: Vec<Value> = message
                        .content
                        .iter()
                        .map(|block| {
                            let mut b = Map::new();
                            match block {
                                ContentBlock::Text(text) => {
                                    put(&mut b, "type", "text");
                                    put_nonempty(&mut b, "text", text);
                                }
                                ContentBlock::Thinking(thinking) => {
                                    put(&mut b, "type", "thinking");
                                    put_nonempty(&mut b, "thinking", thinking);
                                }
                                ContentBlock::ToolCall => put(&mut b, "type", "toolCall"),
                            }
                            Value::Object(b)
                        })
                        .collect();
                    let mut m = Map::new();
                    put(&mut m, "content", content);
                    put_nonempty(&mut m, "stopReason", &message.stop_reason);
                    put_nonempty(&mut m, "errorMessage", &message.error_message);
                    put(&mut map, "message", Value::Object(m));
                }
                put(&mut map, "outputPad", n.output_pad);
                if n.hide_thinking_block {
                    put(&mut map, "hideThinkingBlock", true);
                }
                // Upstream's default label travels as none.
                if n.hidden_thinking_label != "Thinking..." {
                    put(&mut map, "hiddenThinkingLabel", n.hidden_thinking_label.as_str());
                }
                if n.is_streaming {
                    put(&mut map, "isStreaming", true);
                }
            }
            Node::ToolExecution(n) => self.tool_execution(n, &mut map),
            Node::BashExecution(n) => {
                put_opt(&mut map, "id", &n.id);
                if n.expanded {
                    put(&mut map, "expanded", true);
                }
                put(&mut map, "command", n.command.as_str());
                if n.exclude_from_context {
                    put(&mut map, "excludeFromContext", true);
                }
                put_nonempty(&mut map, "output", &n.output);
                if let Some(c) = &n.complete {
                    let mut m = Map::new();
                    put_opt(&mut m, "exitCode", &c.exit_code);
                    if c.cancelled {
                        put(&mut m, "cancelled", true);
                    }
                    if c.truncated {
                        put(&mut m, "truncated", true);
                    }
                    put_nonempty(&mut m, "fullOutputPath", &c.full_output_path);
                    put(&mut map, "complete", Value::Object(m));
                }
            }
            Node::Diff(n) => {
                put(&mut map, "paddingX", n.padding_x);
                put(&mut map, "paddingY", n.padding_y);
                put(&mut map, "diff", n.diff.as_str());
                put_nonempty(&mut map, "filePath", &n.file_path);
            }
        }
        Ok(Value::Object(map))
    }

    /// Encodes a tool card in the Go SDK's field order: its constructor
    /// arguments always, its options when they differ from upstream's
    /// defaults, and isPartial with a result.
    fn tool_execution(&mut self, n: &ToolExecution, map: &mut Map<String, Value>) {
        put_opt(map, "id", &n.id);
        put_nonempty(map, "toolName", &n.tool_name);
        put_nonempty(map, "toolCallId", &n.tool_call_id);
        let args = if n.args.is_null() { Value::Object(Map::new()) } else { n.args.clone() };
        put(map, "args", args);
        if n.tool_definition == ToolDefinition::Empty {
            put(map, "toolDefinition", "empty");
        }
        put_nonempty(map, "cwd", &n.cwd);
        if !n.show_images {
            put(map, "showImages", false);
        }
        if n.image_width_cells != 60 {
            put(map, "imageWidthCells", n.image_width_cells);
        }
        for (key, on) in [
            ("executionStarted", n.execution_started),
            ("argsComplete", n.args_complete),
            ("expanded", n.expanded),
        ] {
            if on {
                put(map, key, true);
            }
        }
        if let Some(result) = &n.result {
            let mut content = Vec::with_capacity(result.content.len());
            for block in &result.content {
                let mut b = Map::new();
                match &block.image {
                    Some(image) => {
                        put(&mut b, "type", "image");
                        put(&mut b, "ref", image.reference.as_str());
                        put(&mut b, "mimeType", image.mime_type.as_str());
                        self.add_image(image);
                    }
                    None => {
                        put(&mut b, "type", "text");
                        put_nonempty(&mut b, "text", &block.text);
                    }
                }
                content.push(Value::Object(b));
            }
            let mut r = Map::new();
            put(&mut r, "content", content);
            if result.is_error {
                put(&mut r, "isError", true);
            }
            put_opt(&mut r, "details", &result.details);
            put(map, "result", Value::Object(r));
            put(map, "isPartial", n.is_partial);
        }
    }
}

/// Upstream callback a view event reports.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EventKind {
    /// SelectList `onSelect(item)`.
    Select,
    /// SelectList or SettingsList `onCancel()`.
    Cancel,
    /// SelectList `onSelectionChange(item)`.
    SelectionChange,
    /// SettingsList `onChange(id, newValue)`.
    Change,
}

/// A callback of an interactive node of a `ui.custom` view, delivered to
/// [`crate::ViewComponent::handle_view_event`] (wire `ui.view.event`).
#[derive(Debug, Clone, PartialEq)]
pub struct Event {
    /// The list's id.
    pub node: String,
    /// The wire `type`.
    pub kind: EventKind,
    /// The item's index among the shown (filtered) items for select and
    /// selection changes; 0 otherwise.
    pub index: usize,
    /// The select-list item of select and selection changes.
    pub item: Option<SelectItem>,
    /// The changed setting's id (change).
    pub id: String,
    /// The changed setting's new value (change).
    pub value: String,
}

impl Event {
    /// Decodes a `ui.view.event` argument into its overlay key and event.
    /// An unknown type is not an event this SDK can deliver.
    pub(crate) fn from_wire(args: &Value) -> Option<(String, Event)> {
        let text = |key: &str| args.get(key).and_then(Value::as_str).unwrap_or("").to_string();
        let kind = match args.get("type").and_then(Value::as_str)? {
            "select" => EventKind::Select,
            "cancel" => EventKind::Cancel,
            "selectionChange" => EventKind::SelectionChange,
            "change" => EventKind::Change,
            _ => return None,
        };
        let item = args.get("item").filter(|item| item.is_object()).map(|item| {
            let field = |key: &str| item.get(key).and_then(Value::as_str).unwrap_or("").to_string();
            SelectItem {
                value: field("value"),
                label: field("label"),
                description: item.get("description").and_then(Value::as_str).map(str::to_string),
            }
        });
        let event = Event {
            node: text("node"),
            kind,
            index: args.get("index").and_then(Value::as_u64).unwrap_or(0) as usize,
            item,
            id: text("id"),
            value: text("value"),
        };
        Some((text("key"), event))
    }
}

/// A connection's view state: the frontend flag, the image refs the host
/// holds, and the latest view of each widget, header and footer, which an
/// eviction sends again.
#[derive(Default)]
pub(crate) struct ViewLedger {
    frontend: AtomicBool,
    sent: Mutex<HashSet<String>>,
    /// Each widget's latest encoded view and its options.
    pub(crate) widgets: Mutex<HashMap<String, (Encoded, Option<Value>)>>,
    /// The header's and the footer's latest encoded view, by host method.
    pub(crate) surfaces: Mutex<HashMap<&'static str, Encoded>>,
}

impl ViewLedger {
    /// Applies a state snapshot (ready or `state_update`, both whole
    /// snapshots): `frontend` is true only while a frontend draws.
    pub(crate) fn apply_state(&self, state: &Map<String, Value>) {
        let frontend = state.get("frontend").and_then(Value::as_bool).unwrap_or(false);
        self.frontend.store(frontend, Ordering::Release);
    }

    pub(crate) fn encode(&self, view: &View) -> Result<Encoded, String> {
        view.encode(self.frontend.load(Ordering::Acquire))
    }

    /// Whether the frame references an image the host may lack.
    pub(crate) fn has_unsent(&self, encoded: &Encoded) -> bool {
        let sent = self.sent.lock().unwrap();
        encoded.images.iter().any(|image| !sent.contains(&image.reference))
    }

    /// The wire view: the body with the data of every image not sent on
    /// this connection yet, which counts as sent from here on.
    pub(crate) fn wire(&self, encoded: &Encoded) -> Value {
        let mut body = encoded.body.clone();
        let mut sent = self.sent.lock().unwrap();
        let unsent: Vec<Value> = encoded
            .images
            .iter()
            .filter(|image| sent.insert(image.reference.clone()))
            .map(|image| {
                serde_json::json!({
                    "ref": image.reference,
                    "mimeType": image.mime_type,
                    "data": digest::base64(&image.data),
                })
            })
            .collect();
        if !unsent.is_empty() {
            if let Value::Object(body) = &mut body {
                body.insert("images".into(), Value::Array(unsent));
            }
        }
        body
    }

    /// Forgets refs the host dropped (`ui.view.evicted`).
    pub(crate) fn forget(&self, refs: &HashSet<String>) {
        self.sent.lock().unwrap().retain(|reference| !refs.contains(reference));
    }
}

/// A renderer's output: lines, or an authoritative view the host renders.
pub(crate) enum Rendered {
    Lines(Vec<String>),
    View(View),
}

impl Rendered {
    /// The wire `RenderResult`: `{lines}`, or `{view}` with lines absent. A
    /// view that cannot be encoded is the renderer's error.
    pub(crate) fn into_result(self, ledger: &ViewLedger) -> Result<Value, String> {
        match self {
            Rendered::Lines(lines) => Ok(serde_json::json!({ "lines": lines })),
            Rendered::View(view) => Ok(serde_json::json!({ "view": ledger.wire(&ledger.encode(&view)?) })),
        }
    }
}

#[cfg(test)]
mod tests;

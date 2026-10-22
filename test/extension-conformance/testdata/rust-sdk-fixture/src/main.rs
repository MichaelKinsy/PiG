use pig_sdk::kit::{
    Box as KitBox, Container, DynamicBorder, EventKind, HStack, Image, Lines, ListItem, Loader,
    LoaderIndicator, Markdown, SelectItem, SelectList, SettingItem, SettingsList, Spacer, StackEntry, Text,
    TruncatedText, VStack,
};
use pig_sdk::kit;
use pig_sdk::{
    AutocompleteItem, CommandResult, ConstrainedSampling, Extension, LoginDefinition, MouseEvent,
    OAuthCredentialStatus,
    OAuthCredentialStore, OAuthCredentials, OAuthDeviceCodeInfo, OAuthPrompt, OAuthProvider,
    ProjectTrustDecision, ProjectTrustResult, RemoteComponent, RemoteComponentInvalidate,
    RemoteComponentResult, SpriteDefinition, TerminalInputResult, TerminalInputSubscription, ToolDefinition,
    ToolRenderShell, ToolResult, ViewComponent,
};
use pig_sdk::{EditorBase, EditorComponent, JsString};
use serde_json::{Value, json};
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicBool, Ordering},
};
use std::{thread, time::Duration};

struct FocusedList {
    items: [&'static str; 3],
    selected: usize,
    disposed: Arc<AtomicBool>,
}

impl RemoteComponent for FocusedList {
    fn render(&self, width: u32) -> Vec<String> {
        let mut lines = vec![format!("focused width={width}")];
        for (index, item) in self.items.iter().enumerate() {
            let prefix = if index == self.selected { "> " } else { "  " };
            lines.push(format!("{prefix}{item}"));
        }
        lines
    }

    fn handle_input(&mut self, data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
        match data.to_string().as_deref() {
            Ok("\u{1b}[A") => self.selected = (self.selected + self.items.len() - 1) % self.items.len(),
            Ok("\u{1b}[B") => self.selected = (self.selected + 1) % self.items.len(),
            Ok("\u{1b}[6~") => self.selected = (self.selected + 2).min(self.items.len() - 1),
            Ok("\r" | "\n") => {
                return Ok(RemoteComponentResult::done(Some(json!(
                    self.items[self.selected]
                ))));
            }
            Ok("\u{1b}") => return Ok(RemoteComponentResult::done(None)),
            _ => {}
        }
        Ok(RemoteComponentResult::pending())
    }

    fn dispose(&mut self) {
        self.disposed.store(true, Ordering::Relaxed);
    }
}

struct TimerFocused {
    frame: Arc<Mutex<u64>>,
    invalidate: Arc<Mutex<Option<RemoteComponentInvalidate>>>,
    stop: Arc<AtomicBool>,
    disposed: Arc<AtomicBool>,
    detached: Arc<AtomicBool>,
    worker: Option<thread::JoinHandle<()>>,
}

impl TimerFocused {
    fn new() -> (Self, Arc<AtomicBool>, Arc<AtomicBool>) {
        let frame = Arc::new(Mutex::new(0));
        let invalidate = Arc::new(Mutex::new(None::<RemoteComponentInvalidate>));
        let stop = Arc::new(AtomicBool::new(false));
        let disposed = Arc::new(AtomicBool::new(false));
        let detached = Arc::new(AtomicBool::new(false));
        let worker = {
            let frame = frame.clone();
            let invalidate = invalidate.clone();
            let stop = stop.clone();
            thread::spawn(move || {
                while !stop.load(Ordering::Acquire) {
                    thread::sleep(Duration::from_millis(20));
                    if stop.load(Ordering::Acquire) {
                        return;
                    }
                    *frame.lock().unwrap() += 1;
                    let callback = invalidate.lock().unwrap().clone();
                    if let Some(callback) = callback {
                        callback();
                    }
                }
            })
        };
        (
            Self {
                frame,
                invalidate,
                stop,
                disposed: disposed.clone(),
                detached: detached.clone(),
                worker: Some(worker),
            },
            disposed,
            detached,
        )
    }
}

impl RemoteComponent for TimerFocused {
    fn render(&self, width: u32) -> Vec<String> {
        vec![format!(
            "timer frame={} width={width}",
            *self.frame.lock().unwrap()
        )]
    }

    fn handle_input(&mut self, data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
        if data == "\r" {
            return Ok(RemoteComponentResult::done(Some(json!(
                *self.frame.lock().unwrap()
            ))));
        }
        Ok(RemoteComponentResult::pending())
    }

    fn set_invalidate(&mut self, invalidate: Option<RemoteComponentInvalidate>) {
        self.detached.store(invalidate.is_none(), Ordering::Release);
        *self.invalidate.lock().unwrap() = invalidate;
    }

    fn dispose(&mut self) {
        self.stop.store(true, Ordering::Release);
        if let Some(worker) = self.worker.take() {
            let _ = worker.join();
        }
        self.disposed.store(true, Ordering::Release);
    }
}

/// Subscribes `handler` to the event `name`. A before_agent_start handler registers with `on_before_agent_start` and receives the event as Pi's object (`BeforeAgentStartEvent::snapshot`).
fn subscribe(
    subscriber: &pig_sdk::EventSubscriber,
    name: &str,
    handler: impl Fn(&pig_sdk::Context, &mut Value) -> Result<Option<Value>, String> + Send + Sync + 'static,
) -> pig_sdk::EventSubscription {
    if name == pig_sdk::EVENT_BEFORE_AGENT_START {
        return subscriber.on_before_agent_start(move |ctx, event| {
            let mut data = event.snapshot().map_err(|err| err.to_string())?;
            handler(ctx, &mut data)
        });
    }
    subscriber.on_event(name, handler)
}

fn conformance_login_definition() -> LoginDefinition {
    LoginDefinition {
        brand: vec!["A".repeat(41); 5],
        hero: vec!["A".repeat(32); 14],
        mascot: vec!["A".repeat(16); 14],
        palette: [("A".to_string(), "#123ABC".to_string())]
            .into_iter()
            .collect(),
        name: "Conformance Pig".to_string(),
        description: "Cross-language login fixture".to_string(),
        tagline: "One canonical definition across every SDK".to_string(),
    }
}

fn conformance_sprite_definition() -> SpriteDefinition {
    SpriteDefinition {
        id: "conformance-pig".to_string(),
        name: "Conformance Pig".to_string(),
        tagline: "One canonical sprite across every SDK".to_string(),
        mascot: vec!["A".repeat(16); 14],
        palette: [("A".to_string(), "#123ABC".to_string())]
            .into_iter()
            .collect(),
    }
}

/// The component kit's conformance view (D107,
/// docs/plan/extension-component-kit.md §10). It logs every input and view
/// event and closes on select with the log joined by ",".
struct KitProbe {
    log: Vec<String>,
}

fn kit_probe_view() -> kit::View {
    let items = (0..5)
        .map(|i| SelectItem::new(format!("k{i}"), format!("Track {i}")).description(format!("Artist {i}")))
        .collect();
    let grow = StackEntry { grow: Some(1), ..StackEntry::default() };
    kit::View::new(
        Container::new()
            .child(DynamicBorder::new("accent"))
            .child(Text::new("Kit probe", 2, 0).bg("customMessageBg"))
            .child(Markdown::new("- one\n- **two**", 1, 0))
            .child(
                HStack::new()
                    .child_with(TruncatedText::new("left side", 0, 0), grow)
                    .child_with(TruncatedText::new("right", 0, 0), grow)
                    .gap(1),
            )
            .child(Spacer::new(1))
            .child(SelectList::new("kit-tracks", items, 3).selected_index(2)),
    )
    .focus("kit-tracks")
    .theme("accent", "#d75f00")
}

impl ViewComponent for KitProbe {
    fn view(&self, _width: u32) -> kit::View {
        kit_probe_view()
    }

    fn handle_input(&mut self, data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
        self.log.push(format!("input:{}", data.to_string_lossy()));
        Ok(RemoteComponentResult::pending())
    }

    fn handle_view_event(&mut self, event: kit::Event) -> Result<RemoteComponentResult, String> {
        let kind = match event.kind {
            EventKind::Select => "select",
            EventKind::Cancel => "cancel",
            EventKind::SelectionChange => "selectionChange",
            EventKind::Change => "change",
            other => kit_event_name(other),
        };
        let value = event.item.map(|item| item.value).unwrap_or_default();
        self.log.push(format!("{kind}:{}:{value}", event.index));
        if event.kind == EventKind::Select {
            return Ok(RemoteComponentResult::done(Some(Value::from(self.log.join(",")))));
        }
        Ok(RemoteComponentResult::pending())
    }
}

/// The extension mouse row's component: it logs every mouse event it
/// receives, all of its fields, and closes on a click with the log joined
/// by ",".
struct MouseProbe {
    log: Vec<String>,
}

impl RemoteComponent for MouseProbe {
    fn render(&self, _width: u32) -> Vec<String> {
        ["mouse probe", "row 1", "row 2", "row 3"].map(str::to_string).to_vec()
    }

    fn handle_input(&mut self, _data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
        Ok(RemoteComponentResult::pending())
    }

    fn handles_mouse(&self) -> bool {
        true
    }

    fn handle_mouse(&mut self, event: &MouseEvent) -> Result<RemoteComponentResult, String> {
        let mods: String = [(event.shift, "S"), (event.alt, "A"), (event.ctrl, "C")]
            .into_iter()
            .filter_map(|(on, name)| on.then_some(name))
            .collect();
        self.log.push(format!(
            "{}/{}/{},{}/{},{}/{}x{}/w{}/c{}/{mods}",
            event.kind, event.button, event.x, event.y, event.screen_x, event.screen_y, event.width, event.height,
            event.wheel_delta, event.click_count
        ));
        if event.kind == "click" {
            return Ok(RemoteComponentResult::done(Some(Value::from(self.log.join(",")))));
        }
        Ok(RemoteComponentResult::pending())
    }
}

/// Image n of kit-images: a 1×1 RGBA PNG whose pixel encodes n, so each n has
/// its own bytes and ref.
fn kit_image_png(n: u32) -> Vec<u8> {
    fn crc32(bytes: &[u8]) -> u32 {
        let mut crc = !0u32;
        for &byte in bytes {
            crc ^= u32::from(byte);
            for _ in 0..8 {
                crc = if crc & 1 == 1 { (crc >> 1) ^ 0xedb8_8320 } else { crc >> 1 };
            }
        }
        !crc
    }
    fn chunk(png: &mut Vec<u8>, kind: &[u8; 4], data: &[u8]) {
        png.extend_from_slice(&(data.len() as u32).to_be_bytes());
        let start = png.len();
        png.extend_from_slice(kind);
        png.extend_from_slice(data);
        let crc = crc32(&png[start..]);
        png.extend_from_slice(&crc.to_be_bytes());
    }
    // One filter byte (none) and the pixel, in a zlib stream of one stored block.
    let raw = [0, n as u8, (n >> 8) as u8, 0x5f, 0xff];
    let (mut a, mut b) = (1u32, 0u32);
    for &byte in &raw {
        a = (a + u32::from(byte)) % 65521;
        b = (b + a) % 65521;
    }
    let mut idat = vec![0x78, 0x01, 0x01, raw.len() as u8, 0, !(raw.len() as u8), 0xff];
    idat.extend_from_slice(&raw);
    idat.extend_from_slice(&((b << 16) | a).to_be_bytes());
    let mut png = b"\x89PNG\r\n\x1a\n".to_vec();
    chunk(&mut png, b"IHDR", &[0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0]);
    chunk(&mut png, b"IDAT", &idat);
    chunk(&mut png, b"IEND", &[]);
    png
}

/// kit-kinds' image, a 1×1 PNG every SDK fixture embeds byte for byte.
const KIT_KINDS_PNG: &str = "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c48900000010494441547801010500faff002a005fff026a01892888e8cd0000000049454e44ae426082";

const KIT_BRANCH_SUMMARY: &str = "The branch explored **two** options:\n\n- keep the cache\n- drop the cache\n\nIt chose `drop`.";

fn kit_branch_view(set: impl FnOnce(&mut kit::BranchSummary)) -> kit::View {
    let mut node = kit::BranchSummary::new(KIT_BRANCH_SUMMARY).id("b");
    set(&mut node);
    kit::View::new(Container::new().child(node))
}


const KIT_COMPACTION_SUMMARY: &str = "Compacted the first **forty** turns:\n\n1. the build setup\n2. the failing test\n\nThe fix is pending.";

fn kit_compaction_view(tokens: u64, set: impl FnOnce(&mut kit::CompactionSummary)) -> kit::View {
    let mut node = kit::CompactionSummary::new(KIT_COMPACTION_SUMMARY, tokens).id("c");
    set(&mut node);
    kit::View::new(Container::new().child(node))
}


const KIT_SKILL_CONTENT: &str = "Review the diff for **bugs** first.\n\n- correctness\n- naming\n\nReport findings as a list.";

fn kit_skill_view(set: impl FnOnce(&mut kit::SkillInvocation)) -> kit::View {
    let mut node = kit::SkillInvocation::new("code-review", KIT_SKILL_CONTENT).id("s");
    set(&mut node);
    kit::View::new(Container::new().child(node))
}


fn kit_custom_message_view(text: &str, set: impl FnOnce(&mut kit::CustomMessage)) -> kit::View {
    let mut node = kit::CustomMessage::new("ci-status", text).id("m");
    set(&mut node);
    kit::View::new(Container::new().child(node))
}


const KIT_WIDE_MESSAGE: &str = "A very long message that wraps around the narrow widths of the table";

fn kit_cancellable_view(loader: kit::Loader) -> kit::View {
    kit::View::new(Container::new().child(kit::CancellableLoader::new("c", loader))).focus("c")
}

fn kit_bordered_view(message: &str, set: impl FnOnce(kit::BorderedLoader) -> kit::BorderedLoader) -> kit::View {
    kit::View::new(Container::new().child(set(kit::BorderedLoader::new("b", message)))).focus("b")
}


fn kit_input_view(focus: bool, set: impl FnOnce(kit::Input) -> kit::Input) -> kit::View {
    let view = kit::View::new(Container::new().child(set(kit::Input::new("i"))));
    if focus { view.focus("i") } else { view }
}


fn kit_editor_view(focus: bool, set: impl FnOnce(kit::Editor) -> kit::Editor) -> kit::View {
    let view = kit::View::new(Container::new().child(set(kit::Editor::new("e"))));
    if focus { view.focus("e") } else { view }
}

fn kit_editor_commands() -> kit::Autocomplete {
    kit::Autocomplete::new(vec![
        kit::SlashCommand::new("help").description("Show help"),
        kit::SlashCommand::new("model").description("Switch the model").argument_hint("<name>"),
        kit::SlashCommand::new("quit").description("Exit"),
        kit::SlashCommand::new("session").description("Session info"),
        kit::SlashCommand::new("settings").description("Open settings"),
        kit::SlashCommand::new("share"),
        kit::SlashCommand::new("skills").description("List skills"),
        kit::SlashCommand::new("compact").description("Compact the context"),
    ])
}


fn kit_scroll_child(kind: &str) -> kit::Node {
    match kind {
        "md" => Markdown::new("# Title\n\n- one\n- two with a long line that wraps at narrow widths\n\n`code`", 1, 0).into(),
        "region" => Text::new("Region content\nsecond row", 1, 0).into(),
        "empty" => Container::new().into(),
        "branch" => kit::BranchSummary::new("The branch explored **two** options:\n\n- keep the cache\n- drop the cache\n\nIt chose `drop`.").id("b").into(),
        _ => Text::new("Alpha line of the log\nBeta line of the log\nGamma line of the log that is long enough to wrap at the narrow widths\nDelta", 1, 0).into(),
    }
}


/// The widget kinds' Pi vector cases (D107, docs/plan/extension-component-kit.md
/// §10), built with the SDK's own constructors; the key is
/// "<module>/<export>/<case name>" and the case's wire node is its vector's
/// `root`.
fn kit_widget_case(key: &str) -> Option<kit::View> {
    Some(match key {
        "pi-coding-agent/BranchSummaryMessageComponent/collapsed"
        | "pi-coding-agent/BranchSummaryMessageComponent/click toggles"
        | "pi-coding-agent/BranchSummaryMessageComponent/right click ignored" => kit_branch_view(|_| {}),
        "pi-coding-agent/BranchSummaryMessageComponent/expanded" => kit_branch_view(|n| n.set_expanded(true)),
        "pi-coding-agent/BranchSummaryMessageComponent/outputPad 0 collapsed" => kit_branch_view(|n| n.set_output_pad(0)),
        "pi-coding-agent/BranchSummaryMessageComponent/outputPad 0 expanded" => kit_branch_view(|n| {
            n.set_output_pad(0);
            n.set_expanded(true);
        }),
        "pi-coding-agent/BranchSummaryMessageComponent/empty summary expanded" => {
            let mut node = kit::BranchSummary::new("");
            node.set_expanded(true);
            kit::View::new(node)
        }
        "pi-coding-agent/CompactionSummaryMessageComponent/collapsed"
        | "pi-coding-agent/CompactionSummaryMessageComponent/click toggles"
        | "pi-coding-agent/CompactionSummaryMessageComponent/right click ignored" => kit_compaction_view(123456, |_| {}),
        "pi-coding-agent/CompactionSummaryMessageComponent/expanded" => kit_compaction_view(123456, |n| n.set_expanded(true)),
        "pi-coding-agent/CompactionSummaryMessageComponent/outputPad 0 collapsed" => kit_compaction_view(123456, |n| n.set_output_pad(0)),
        "pi-coding-agent/CompactionSummaryMessageComponent/outputPad 0 expanded" => kit_compaction_view(123456, |n| {
            n.set_output_pad(0);
            n.set_expanded(true);
        }),
        "pi-coding-agent/CompactionSummaryMessageComponent/tokens 999" => kit_compaction_view(999, |_| {}),
        "pi-coding-agent/CompactionSummaryMessageComponent/tokens 1234567 expanded" => kit_compaction_view(1234567, |n| n.set_expanded(true)),
        "pi-coding-agent/CompactionSummaryMessageComponent/empty summary tokens 0 expanded" => {
            let mut node = kit::CompactionSummary::new("", 0);
            node.set_expanded(true);
            kit::View::new(node)
        }
        "pi-coding-agent/SkillInvocationMessageComponent/collapsed"
        | "pi-coding-agent/SkillInvocationMessageComponent/click toggles"
        | "pi-coding-agent/SkillInvocationMessageComponent/right click ignored" => kit_skill_view(|_| {}),
        "pi-coding-agent/SkillInvocationMessageComponent/expanded" => kit_skill_view(|n| n.set_expanded(true)),
        "pi-coding-agent/SkillInvocationMessageComponent/outputPad 0 collapsed" => kit_skill_view(|n| n.set_output_pad(0)),
        "pi-coding-agent/SkillInvocationMessageComponent/outputPad 0 expanded" => kit_skill_view(|n| {
            n.set_output_pad(0);
            n.set_expanded(true);
        }),
        "pi-coding-agent/SkillInvocationMessageComponent/empty skill expanded" => {
            let mut node = kit::SkillInvocation::new("", "");
            node.set_expanded(true);
            kit::View::new(node)
        }
        "pi-coding-agent/CustomMessageComponent/default" => {
            kit_custom_message_view("Build **passed** in 12s\n\n- 214 tests\n- 0 failures", |_| {})
        }
        "pi-coding-agent/CustomMessageComponent/outputPad 0" => {
            kit_custom_message_view("Build **passed** in 12s\n\n- 214 tests\n- 0 failures", |n| n.set_output_pad(0))
        }
        "pi-coding-agent/CustomMessageComponent/expanded changes nothing" => {
            kit_custom_message_view("Build **passed** in 12s\n\n- 214 tests\n- 0 failures", |n| n.set_expanded(true))
        }
        "pi-coding-agent/CustomMessageComponent/empty text" => kit_custom_message_view("", |_| {}),
        "pi-coding-agent/CustomMessageComponent/no id" => kit::View::new(kit::CustomMessage::new("note", "plain")),
        "pi-tui/CancellableLoader/default"
        | "pi-tui/CancellableLoader/escape aborts"
        | "pi-tui/CancellableLoader/escape twice aborts twice"
        | "pi-tui/CancellableLoader/other keys are ignored" => kit_cancellable_view(Loader::new("Working")),
        "pi-tui/CancellableLoader/default message" => kit_cancellable_view(Loader::new("Loading...")),
        "pi-tui/CancellableLoader/colors" => kit_cancellable_view(Loader::new("Working").spinner_color("accent").message_color("muted")),
        "pi-tui/CancellableLoader/frame 3" => kit_cancellable_view(Loader::new("Working").frame(3)),
        "pi-tui/CancellableLoader/indicator frames" => kit_cancellable_view(
            Loader::new("Working")
                .indicator(LoaderIndicator { frames: Some(vec!["*".into(), "+".into(), "x".into()]), interval_ms: Some(50) })
                .frame(1),
        ),
        "pi-tui/CancellableLoader/hidden indicator" => {
            kit_cancellable_view(Loader::new("Working").indicator(LoaderIndicator { frames: Some(vec![]), interval_ms: None }))
        }
        "pi-tui/CancellableLoader/wide message" => kit_cancellable_view(Loader::new(KIT_WIDE_MESSAGE).spinner_color("accent")),
        "pi-coding-agent/BorderedLoader/cancellable"
        | "pi-coding-agent/BorderedLoader/escape aborts"
        | "pi-coding-agent/BorderedLoader/escape twice aborts twice"
        | "pi-coding-agent/BorderedLoader/other keys are ignored" => kit_bordered_view("Fetching models", |l| l),
        "pi-coding-agent/BorderedLoader/not cancellable" | "pi-coding-agent/BorderedLoader/escape ignored when not cancellable" => {
            kit_bordered_view("Fetching models", |l| l.cancellable(false))
        }
        "pi-coding-agent/BorderedLoader/default message" => kit_bordered_view("Loading...", |l| l),
        "pi-coding-agent/BorderedLoader/frame 4" => kit_bordered_view("Fetching models", |l| l.frame(4)),
        "pi-coding-agent/BorderedLoader/wide message" => {
            kit_bordered_view("Fetching the models of every configured provider, one after another", |l| l)
        }
        "pi-tui/Input/empty" => kit_input_view(false, |n| n),
        "pi-tui/Input/empty focused"
        | "pi-tui/Input/typing"
        | "pi-tui/Input/left then insert"
        | "pi-tui/Input/backspace and delete"
        | "pi-tui/Input/home and end"
        | "pi-tui/Input/kill word and yank"
        | "pi-tui/Input/kill to end"
        | "pi-tui/Input/paste scrolls"
        | "pi-tui/Input/submit" => kit_input_view(true, |n| n),
        "pi-tui/Input/placeholder" => kit_input_view(false, |n| n.placeholder("type here")),
        "pi-tui/Input/placeholder focused colored" => kit_input_view(true, |n| n.placeholder("type here").placeholder_color("dim")),
        "pi-tui/Input/custom prompt" => kit_input_view(true, |n| n.prompt("$ ").value("ls -la")),
        "pi-tui/Input/value" => kit_input_view(false, |n| n.value("hello world")),
        "pi-tui/Input/value focused" | "pi-tui/Input/click moves the cursor" | "pi-tui/Input/right click ignored" => {
            kit_input_view(true, |n| n.value("hello world"))
        }
        "pi-tui/Input/wide characters" => kit_input_view(true, |n| n.value("漢字 and 😀 text")),
        "pi-tui/Input/submit newline key" => kit_input_view(true, |n| n.value("preset")),
        "pi-tui/Input/escape" => kit_input_view(true, |n| n.value("keep")),
        "pi-tui/Editor/empty" => kit_editor_view(false, |n| n),
        "pi-tui/Editor/empty focused" => kit_editor_view(true, |n| n),
        "pi-tui/Editor/value" => kit_editor_view(false, |n| n.value("hello world")),
        "pi-tui/Editor/multiline value focused" => kit_editor_view(true, |n| n.value("one\ntwo\nthree")),
        "pi-tui/Editor/wrapped line" => kit_editor_view(true, |n| n.value("The quick brown fox jumps over the lazy dog and keeps running through the field")),
        "pi-tui/Editor/wide characters" => kit_editor_view(true, |n| n.value("漢字 and 😀 text")),
        "pi-tui/Editor/typing" => kit_editor_view(true, |n| n),
        "pi-tui/Editor/newline" => kit_editor_view(true, |n| n),
        "pi-tui/Editor/backspace and delete" => kit_editor_view(true, |n| n),
        "pi-tui/Editor/kill word and yank" => kit_editor_view(true, |n| n),
        "pi-tui/Editor/undo" => kit_editor_view(true, |n| n),
        "pi-tui/Editor/submit" => kit_editor_view(true, |n| n),
        "pi-tui/Editor/paste" => kit_editor_view(true, |n| n),
        "pi-tui/Editor/paste large" => kit_editor_view(true, |n| n),
        "pi-tui/Editor/cursor movement" => kit_editor_view(true, |n| n.value("one\ntwo\nthree")),
        "pi-tui/Editor/submit multiline" => kit_editor_view(true, |n| n.value("one\ntwo")),
        "pi-tui/Editor/submit disabled" => kit_editor_view(true, |n| n.value("keep").disable_submit(true)),
        "pi-tui/Editor/padding" => kit_editor_view(true, |n| n.value("padded text").padding_x(2)),
        "pi-tui/Editor/padding wrapped" => kit_editor_view(true, |n| n.value("The quick brown fox jumps over the lazy dog and keeps running").padding_x(3)),
        "pi-tui/Editor/border color" => kit_editor_view(true, |n| n.value("colored").border_color("accent")),
        "pi-tui/Editor/history" => kit_editor_view(true, |n| n.history(["first", "second", "third"])),
        "pi-tui/Editor/history then edit" => kit_editor_view(true, |n| n.history(["first", "second"])),
        "pi-tui/Editor/scrolls past the visible lines" => kit_editor_view(true, |n| n.value("line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12")),
        "pi-tui/Editor/scroll indicator below" => kit_editor_view(true, |n| n.value("line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12")),
        "pi-tui/Editor/more rows show more lines" => kit_editor_view(true, |n| n.value("line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12")),
        "pi-tui/Editor/few rows show five lines" => kit_editor_view(true, |n| n.value("line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12")),
        "pi-tui/Editor/page up and down" => kit_editor_view(true, |n| n.value("line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12\nline 13\nline 14\nline 15\nline 16\nline 17\nline 18\nline 19\nline 20\nline 21\nline 22\nline 23\nline 24\nline 25\nline 26\nline 27\nline 28\nline 29\nline 30")),
        "pi-tui/Editor/click moves the cursor" => kit_editor_view(true, |n| n.value("hello\nworld")),
        "pi-tui/Editor/click on the border" => kit_editor_view(true, |n| n.value("hello\nworld")),
        "pi-tui/Editor/right click ignored" => kit_editor_view(true, |n| n.value("hello")),
        "pi-tui/Editor/slash opens the menu" => kit_editor_view(true, |n| n.autocomplete(kit_editor_commands())),
        "pi-tui/Editor/slash narrows" => kit_editor_view(true, |n| n.autocomplete(kit_editor_commands())),
        "pi-tui/Editor/slash no match" => kit_editor_view(true, |n| n.autocomplete(kit_editor_commands())),
        "pi-tui/Editor/slash tab accepts" => kit_editor_view(true, |n| n.autocomplete(kit_editor_commands())),
        "pi-tui/Editor/slash arrow then tab" => kit_editor_view(true, |n| n.autocomplete(kit_editor_commands())),
        "pi-tui/Editor/slash enter accepts" => kit_editor_view(true, |n| n.autocomplete(kit_editor_commands())),
        "pi-tui/Editor/slash escape closes" => kit_editor_view(true, |n| n.autocomplete(kit_editor_commands())),
        "pi-tui/Editor/slash click selects" => kit_editor_view(true, |n| n.autocomplete(kit_editor_commands())),
        "pi-tui/Editor/text after the slash opens nothing" => kit_editor_view(true, |n| n.autocomplete(kit_editor_commands())),
        "pi-tui/Editor/slash max visible 3" => kit_editor_view(true, |n| n.autocomplete_max_visible(3).autocomplete(kit_editor_commands())),
        "pi-tui/Editor/slash with padding" => kit_editor_view(true, |n| n.padding_x(2).autocomplete(kit_editor_commands())),
        "pi-tui/Editor/autocomplete menu does not follow disabled submit" => kit_editor_view(true, |n| n.disable_submit(true).autocomplete(kit_editor_commands())),
        "pi-tui/ScrollView/default" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("text")))),
        "pi-tui/ScrollView/scrollbar always" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("text")).scrollbar("always"))),
        "pi-tui/ScrollView/scrollbar auto" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("text")).scrollbar("auto"))),
        "pi-tui/ScrollView/scrollbar colors" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("text")).scrollbar("always").scrollbar_track_color("dim").scrollbar_thumb_color("accent"))),
        "pi-tui/ScrollView/follow end" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("text")).follow("end"))),
        "pi-tui/ScrollView/primary and contain" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("text")).primary(true).overscroll("contain"))),
        "pi-tui/ScrollView/hide delay" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("text")).scrollbar("auto").scrollbar_hide_delay_ms(50))),
        "pi-tui/ScrollView/scroll top clamps" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("text")).scroll_top(3))),
        "pi-tui/ScrollView/markdown child" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("md")).scrollbar("always"))),
        "pi-tui/ScrollView/after other rows" => kit::View::new(Container::new().child(Text::new("Header", 1, 0)).child(kit::ScrollView::new("s", kit_scroll_child("text")).scrollbar("always")).child(Text::new("Footer", 1, 0))),
        "pi-tui/ScrollView/empty child" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("empty")).scrollbar("always"))),
        "pi-tui/ScrollView/wheel and click pass" => kit::View::new(Container::new().child(kit::ScrollView::new("s", kit_scroll_child("text"))).child(Text::new("after", 1, 0))),
        "pi-tui/MouseRegion/renders its child" => kit::View::new(Container::new().child(kit::MouseRegion::new("m", kit_scroll_child("region")))),
        "pi-tui/MouseRegion/every type by default" => kit::View::new(Container::new().child(kit::MouseRegion::new("m", kit_scroll_child("region")))),
        "pi-tui/MouseRegion/types filter" => kit::View::new(Container::new().child(kit::MouseRegion::new("m", kit_scroll_child("region")).types(["click"]))),
        "pi-tui/MouseRegion/not handled" => kit::View::new(Container::new().child(kit::MouseRegion::new("m", kit_scroll_child("region")).handled(false))),
        "pi-tui/MouseRegion/capture" => kit::View::new(Container::new().child(kit::MouseRegion::new("m", kit_scroll_child("region")).capture(true))),
        "pi-tui/MouseRegion/focus and render" => kit::View::new(Container::new().child(kit::MouseRegion::new("m", kit_scroll_child("region")).focus(true).render(false))),
        "pi-tui/MouseRegion/local coordinates" => kit::View::new(Container::new().child(Text::new("Above the region", 1, 0)).child(kit::MouseRegion::new("m", kit_scroll_child("region"))).child(Text::new("Below", 1, 0))),
        "pi-tui/MouseRegion/the child takes the event first" => kit::View::new(Container::new().child(kit::MouseRegion::new("m", kit_scroll_child("branch")))),
        "pi-tui/MouseRegion/right button" => kit::View::new(Container::new().child(kit::MouseRegion::new("m", kit_scroll_child("region")))),
        // kit-widget-cases
        _ => return None,
    })
}

/// kit-widget's component: it takes the keys and mouse events the view leaves
/// to the extension, and notifies each callback its nodes fire as
/// "kit-widget-event=<type>:<value>" (a change report is not one). It never
/// closes itself: the harness delivers the case's steps and then reads the
/// callbacks, and a probe that closed after the callbacks Pi fired would drop
/// the steps still to come.
struct KitWidgetProbe {
    ctx: pig_sdk::Context,
    view: kit::View,
}

impl ViewComponent for KitWidgetProbe {
    fn view(&self, _width: u32) -> kit::View {
        self.view.clone()
    }

    fn handle_input(&mut self, _data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
        Ok(RemoteComponentResult::pending())
    }

    fn handles_mouse(&self) -> bool {
        true
    }

    fn handle_mouse(&mut self, _event: &MouseEvent) -> Result<RemoteComponentResult, String> {
        Ok(RemoteComponentResult::pending())
    }

    fn handle_view_event(&mut self, event: kit::Event) -> Result<RemoteComponentResult, String> {
        let entry = match event.kind {
            EventKind::Change => return Ok(RemoteComponentResult::pending()),
            EventKind::Mouse => match &event.mouse {
                Some(m) => format!("mouse:{}/{}/{}/{}", m.kind, m.button, m.x, m.y),
                None => "mouse:".to_string(),
            },
            kind => format!("{}:{}", kit_event_name(kind), event.value),
        };
        self.ctx.notify(&format!("kit-widget-event={entry}"), "info");
        Ok(RemoteComponentResult::pending())
    }
}

fn kit_event_name(kind: EventKind) -> &'static str {
    match kind {
        EventKind::Select => "select",
        EventKind::Cancel => "cancel",
        EventKind::SelectionChange => "selectionChange",
        EventKind::Change => "change",
        EventKind::Submit => "submit",
        EventKind::Escape => "escape",
        EventKind::Abort => "abort",
        EventKind::Mouse => "mouse",
    }
}

fn main() {
    let mut ext = Extension::new("rust-sdk-fixture");
    // factory_agent_dir is get_agent_dir() while the factory runs, where Pi's extensions call it (TestConformance_HostConfigurationAgreesAcrossSDKs).
    let factory_agent_dir = pig_sdk::get_agent_dir().unwrap_or_else(|err| format!("error: {err}"));
    let schema_rejected = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
        ext.tool("schema-invalid", "Must not register", Value::Null, |_, _| ToolResult::text("bad"));
    })).is_err();
    ext.command("language_probe", "Report the highlight language of a path", |ctx, args| {
        let case: serde_json::Value = match serde_json::from_str(args) { Ok(case) => case, Err(err) => return CommandResult::Error(err.to_string()) };
        let language = pig_sdk::get_language_from_path(case["path"].as_str().unwrap_or(""));
        ctx.notify(&format!("language:{}:end", json!({"lang": language})), "info");
        CommandResult::Ok
    });
    ext.command("convert_probe", "Run convertToLlm or formatDimensionNote on a JSON case and report the result", |ctx, args| {
        let case: serde_json::Value = match serde_json::from_str(args) { Ok(case) => case, Err(err) => return CommandResult::Error(err.to_string()) };
        let result = if case["fn"] == "convert" {
            json!(pig_sdk::convert_to_llm(case["messages"].as_array().map(Vec::as_slice).unwrap_or(&[])))
        } else {
            let i = &case["image"];
            let image = pig_sdk::ResizedImage { original_width: i["originalWidth"].as_f64().unwrap_or(0.0), original_height: i["originalHeight"].as_f64().unwrap_or(0.0), width: i["width"].as_f64().unwrap_or(0.0), height: i["height"].as_f64().unwrap_or(0.0), was_resized: i["wasResized"].as_bool().unwrap_or(false), ..Default::default() };
            json!(pig_sdk::format_dimension_note(&image))
        };
        ctx.notify(&format!("convert:{}:end", json!({"result": result})), "info");
        CommandResult::Ok
    });
    // The helpers run here, while the factory runs, before the connection to the host is ready.
    fn key_lists(value: &serde_json::Value, out: &mut Vec<String>) {
        match value {
            serde_json::Value::Array(items) => items.iter().for_each(|item| key_lists(item, out)),
            serde_json::Value::Object(map) => {
                out.push(map.keys().cloned().collect::<Vec<_>>().join(","));
                map.values().for_each(|item| key_lists(item, out));
            }
            _ => {}
        }
    }
    fn frontmatter_report(parsed: std::io::Result<pig_sdk::ParsedFrontmatter>) -> serde_json::Value {
        match parsed {
            Ok(parsed) => {
                let mut keys = Vec::new();
                key_lists(&parsed.frontmatter, &mut keys);
                json!({"frontmatter": parsed.frontmatter, "body": parsed.body, "keys": keys})
            }
            Err(_) => json!({"error": true}),
        }
    }
    fn date_report(parsed: std::io::Result<Option<f64>>) -> serde_json::Value {
        match parsed {
            Ok(ms) => json!({"ms": ms}),
            Err(_) => json!({"error": true}),
        }
    }
    let factory_helpers = json!({
        "frontmatter": frontmatter_report(pig_sdk::parse_frontmatter("---\nz: 1\n10: a\n2: b\nnested:\n  y: 1\n  3: x\n  \"q r\": [1, 2]\n---\nbody text\n")),
        "error": frontmatter_report(pig_sdk::parse_frontmatter("---\na: [\n---\nx")),
        "date": date_report(pig_sdk::parse_date("2020-01-02T03:04:05.678Z")),
        "strip": match pig_sdk::strip_frontmatter("---\nz: 1\n---\nbody text\n") { Ok(body) => json!({"body": body}), Err(_) => json!({"error": true}) },
    });
    ext.command("helper_probe", "Run parseFrontmatter, stripFrontmatter or Date.parse on its argument", |ctx, args| {
        let case: serde_json::Value = match serde_json::from_str(args) { Ok(case) => case, Err(err) => return CommandResult::Error(err.to_string()) };
        let input = case["input"].as_str().unwrap_or("");
        let out = match case["fn"].as_str().unwrap_or("") {
            "date" => date_report(ctx.parse_date(input)),
            "strip" => match ctx.strip_frontmatter(input) { Ok(body) => json!({"body": body}), Err(_) => json!({"error": true}) },
            _ => frontmatter_report(ctx.parse_frontmatter(input)),
        };
        ctx.notify(&format!("helper:{out}:end"), "info");
        CommandResult::Ok
    });
    ext.command("helper_factory_probe", "Report the helpers' results from the factory", move |ctx, _args| {
        ctx.notify(&format!("helper-factory:{factory_helpers}:end"), "info");
        CommandResult::Ok
    });
    ext.command("serialize_probe", "Run serializeConversation on JSON messages and report the text", |ctx, args| {
        // The messages stay JSON text, so an unpaired surrogate escape reaches serialize_conversation_json as the unit it is.
        let case: std::collections::HashMap<String, Box<serde_json::value::RawValue>> = match serde_json::from_str(args) { Ok(case) => case, Err(err) => return CommandResult::Error(err.to_string()) };
        let messages = case.get("messages").map(|messages| messages.get()).unwrap_or("[]");
        // A conversation a serde_json::Value can hold runs through the typed serialize_conversation; one with an unpaired surrogate, which a Value refuses, through the JSON-text form. Both answer Pi's text.
        let exact = pig_sdk::serialize_conversation_json(messages);
        // A conversation a serde_json::Value can hold also runs through the typed serialize_conversation: it must fail where the JSON-text form fails and, where both succeed, give the same text with each unpaired unit written as U+FFFD (a String cannot hold one).
        let out = match (&exact, serde_json::from_str::<Vec<serde_json::Value>>(messages)) {
            (Ok(text), Ok(typed)) => match pig_sdk::serialize_conversation(&typed) {
                Ok(lossy) if lossy == text.to_string_lossy() => None,
                _ => Some("{\"error\":\"typed serialize_conversation differs from serialize_conversation_json\"}".to_string()),
            },
            (Err(_), Ok(typed)) => match pig_sdk::serialize_conversation(&typed) {
                Err(_) => None,
                Ok(_) => Some("{\"error\":\"typed serialize_conversation succeeded where serialize_conversation_json failed\"}".to_string()),
            },
            _ => None,
        };
        let out = out.unwrap_or_else(|| match exact {
            // A JsString serializes as a JSON string that keeps an unpaired surrogate as its escape; a serde_json::Value could not hold it.
            Ok(text) => format!("{{\"result\":{}}}", serde_json::to_string(&text).unwrap_or_default()),
            Err(_) => "{\"error\":true}".to_string(),
        });
        ctx.notify(&format!("serialize:{out}:end"), "info");
        CommandResult::Ok
    });
    ext.command("branch_probe", "Run a branch summarization helper on a JSON case and report the result", |ctx, args| {
        struct EntrySession(std::collections::HashMap<String, serde_json::Value>);
        impl pig_sdk::SessionReader for EntrySession {
            fn get_entry(&self, id: &str) -> std::io::Result<serde_json::Value> { Ok(self.0.get(id).cloned().unwrap_or(serde_json::Value::Null)) }
            fn get_branch(&self, from_id: Option<&str>) -> std::io::Result<serde_json::Value> {
                let mut path = Vec::new();
                let mut current = from_id.and_then(|id| self.0.get(id));
                while let Some(entry) = current {
                    path.push(entry.clone());
                    current = entry["parentId"].as_str().filter(|id| !id.is_empty()).and_then(|id| self.0.get(id));
                }
                path.reverse();
                Ok(serde_json::Value::Array(path))
            }
        }
        // The entries are read as JSON text: a JavaScript string may hold an unpaired surrogate, which a Value cannot, and only the text path keeps it.
        let raw: std::collections::HashMap<String, Box<serde_json::value::RawValue>> = match serde_json::from_str(args) { Ok(raw) => raw, Err(err) => return CommandResult::Error(err.to_string()) };
        // Every member but the entries holds no surrogate; the entries are parsed as Values only where a Value can hold them.
        let member = |name: &str| raw.get(name).and_then(|member| serde_json::from_str::<serde_json::Value>(member.get()).ok()).unwrap_or(serde_json::Value::Null);
        let mut case = serde_json::Map::new();
        for name in ["fn", "lone", "budget", "old", "target"] {
            case.insert(name.to_string(), member(name));
        }
        let case = serde_json::Value::Object(case);
        let entries: Vec<serde_json::Value> = if case["lone"] == true { Vec::new() } else { member("entries").as_array().cloned().unwrap_or_default() };
        let report: String = if case["fn"] == "prepare" {
            let budget = case["budget"].as_i64().unwrap_or(0);
            let entries_json = raw.get("entries").map_or("[]", |entries| entries.get());
            match pig_sdk::prepare_branch_entries_json(entries_json, budget) {
                Err(_) => json!({"error": true}).to_string(),
                Ok(text) if case["lone"] == true => text,
                // Where a Value can hold the input, the Value path must answer what the text path answers.
                Ok(text) => {
                    let plain = pig_sdk::prepare_branch_entries(&entries, budget).map(|r| json!({"messages": r.messages, "fileOps": {"read": r.file_ops.read, "written": r.file_ops.written, "edited": r.file_ops.edited}, "totalTokens": r.total_tokens}));
                    if plain.is_ok_and(|plain| serde_json::from_str::<serde_json::Value>(&text).ok() == Some(plain)) { text } else { json!({"error": "the Value path and the text path differ"}).to_string() }
                }
            }
        } else {
            let session = EntrySession(entries.iter().filter_map(|entry| Some((entry["id"].as_str()?.to_string(), entry.clone()))).collect());
            match pig_sdk::collect_entries_for_branch_summary(&session, case["old"].as_str(), case["target"].as_str().unwrap_or("")) {
                Ok(r) => json!({"entries": r.entries, "commonAncestorId": r.common_ancestor_id}),
                Err(_) => json!({"error": true}),
            }
            .to_string()
        };
        ctx.notify(&format!("branch:{report}:end"), "info");
        CommandResult::Ok
    });
    ext.command("compact_usage_probe", "Run a compaction usage helper on a JSON case and report the result", |ctx, args| {
        let case: serde_json::Value = match serde_json::from_str(args) { Ok(case) => case, Err(err) => return CommandResult::Error(err.to_string()) };
        let result = match case["fn"].as_str().unwrap_or("") {
            "calc" => {
                let usage: pig_sdk::UsageInfo = match serde_json::from_value(case["usage"].clone()) { Ok(usage) => usage, Err(err) => return CommandResult::Error(err.to_string()) };
                json!({"tokens": pig_sdk::calculate_context_tokens(&usage)})
            }
            "should" => {
                let s = &case["settings"];
                let settings = pig_sdk::CompactionSettings { enabled: s["enabled"].as_bool().unwrap_or(false), reserve_tokens: s["reserveTokens"].as_i64().unwrap_or(0), keep_recent_tokens: s["keepRecentTokens"].as_i64().unwrap_or(0) };
                json!({"result": pig_sdk::should_compact(case["tokens"].as_i64().unwrap_or(0), case["window"].as_i64().unwrap_or(0), &settings)})
            }
            "last" => {
                let entries: Vec<serde_json::Value> = case["entries"].as_array().cloned().unwrap_or_default();
                match pig_sdk::get_last_assistant_usage(&entries) {
                    Some(u) => json!({"usage": {"input": u.input, "output": u.output, "cacheRead": u.cache_read, "cacheWrite": u.cache_write, "totalTokens": u.total_tokens}}),
                    None => json!({"usage": null}),
                }
            }
            "cut" => {
                let entries: Vec<serde_json::Value> = case["entries"].as_array().cloned().unwrap_or_default();
                let cut = pig_sdk::find_cut_point(&entries, case["start"].as_u64().unwrap_or(0) as usize, case["end"].as_u64().unwrap_or(0) as usize, case["keep"].as_i64().unwrap_or(0));
                json!({"firstKeptEntryIndex": cut.first_kept_entry_index, "turnStartIndex": cut.turn_start_index.map_or(-1, |i| i as i64), "isSplitTurn": cut.is_split_turn})
            }
            "turn" => {
                let entries: Vec<serde_json::Value> = case["entries"].as_array().cloned().unwrap_or_default();
                let found = pig_sdk::find_turn_start_index(&entries, case["index"].as_u64().unwrap_or(0) as usize, case["start"].as_u64().unwrap_or(0) as usize);
                json!({"index": found.map_or(-1, |i| i as i64)})
            }
            "estimate" => json!({"tokens": pig_sdk::estimate_tokens(&case["message"])}),
            _ => {
                let d = pig_sdk::DEFAULT_COMPACTION_SETTINGS;
                json!({"enabled": d.enabled, "reserveTokens": d.reserve_tokens, "keepRecentTokens": d.keep_recent_tokens})
            }
        };
        ctx.notify(&format!("compact:{result}:end"), "info");
        CommandResult::Ok
    });
    ext.command("skills_probe", "Run a session or skill helper on a JSON case and report the result", |ctx, args| {
        let case: serde_json::Value = match serde_json::from_str(args) { Ok(case) => case, Err(err) => return CommandResult::Error(err.to_string()) };
        let result = match case["fn"].as_str().unwrap_or("") {
            "latest" => {
                let entries: Vec<serde_json::Value> = case["entries"].as_array().cloned().unwrap_or_default();
                json!({"entry": pig_sdk::get_latest_compaction_entry(&entries)})
            }
            "parse" => {
                let block = pig_sdk::parse_skill_block(case["text"].as_str().unwrap_or(""));
                json!({"block": block.map(|b| json!({"name": b.name, "location": b.location, "content": b.content, "userMessage": b.user_message}))})
            }
            "format" => {
                let skills: Vec<serde_json::Value> = case["skills"].as_array().cloned().unwrap_or_default();
                json!({"text": pig_sdk::format_skills_for_prompt(&skills, case["tool"].as_str().unwrap_or("read"))})
            }
            _ => json!({"version": pig_sdk::CURRENT_SESSION_VERSION, "virtual": pig_sdk::VIRTUAL_MODEL_STATE_ENTRY}),
        };
        ctx.notify(&format!("skills:{result}:end"), "info");
        CommandResult::Ok
    });
    ext.command("schema-probe", "Report schema rejection", move |ctx, _args| {
        ctx.notify(&format!("schema-rejected:{schema_rejected}"), "info");
        CommandResult::Ok
    });
    // The factory get_editor_component answered right after this extension's install: the editor-get-probe reports "installed" only for
    // that same Arc, so a getter that builds another factory fails TestConformance_EditorComponentGetterAgreesAcrossSDKs.
    let installed_factory: Arc<Mutex<Option<pig_sdk::EditorFactory>>> = Arc::new(Mutex::new(None));
    let installed = installed_factory.clone();
    ext.command("editor-install", "Install a custom editor component", move |ctx, _args| {
        match ctx.set_editor_component(|base| Box::new(ConformanceEditor { base })) {
            Ok(()) => {
                *installed.lock().unwrap() = ctx.get_editor_component();
                CommandResult::Ok
            }
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    // editor-get-probe and editor-clear back TestConformance_EditorComponentGetterAgreesAcrossSDKs; editor-install is above.
    let installed = installed_factory.clone();
    ext.command("editor-get-probe", "Report the factory get_editor_component returns", move |ctx, _args| {
        let state = match (ctx.get_editor_component(), installed.lock().unwrap().as_ref()) {
            (None, _) => "absent",
            (Some(got), Some(want)) if Arc::ptr_eq(&got, want) => "installed",
            (Some(_), _) => "other",
        };
        ctx.notify(&format!("editor_get:{state}:end"), "info");
        CommandResult::Ok
    });
    // truncate_probe runs one SDK truncation helper on the JSON case in its argument and reports the result in Pi's shape (TestConformance_TruncationHelpersAgreeAcrossSDKs).
    ext.command("truncate_probe", "Run a truncation helper on a JSON case and report the result", |ctx, args| {
        let case: Value = match serde_json::from_str(&args) {
            Ok(case) => case,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let limit = |key: &str| case.get(key).and_then(Value::as_u64).map(|n| n as usize);
        let options = pig_sdk::TruncationOptions { max_lines: limit("maxLines"), max_bytes: limit("maxBytes") };
        let content = case["content"].as_str().unwrap_or("");
        let result = match case["fn"].as_str().unwrap_or("") {
            fn_name @ ("head" | "tail") => {
                let r = if fn_name == "head" { pig_sdk::truncate_head(content, options) } else { pig_sdk::truncate_tail(content, options) };
                json!({"content": r.content, "truncated": r.truncated, "truncatedBy": r.truncated_by.map(|by| by.as_str()), "totalLines": r.total_lines,
                    "totalBytes": r.total_bytes, "outputLines": r.output_lines, "outputBytes": r.output_bytes, "lastLinePartial": r.last_line_partial,
                    "firstLineExceedsLimit": r.first_line_exceeds_limit, "maxLines": r.max_lines, "maxBytes": r.max_bytes})
            }
            "line" => {
                let (text, was) = pig_sdk::truncate_line(content, limit("maxChars").unwrap_or(pig_sdk::GREP_MAX_LINE_LENGTH));
                json!({"text": text, "wasTruncated": was})
            }
            "limits" => json!({"maxLines": pig_sdk::DEFAULT_MAX_LINES, "maxBytes": pig_sdk::DEFAULT_MAX_BYTES}),
            _ => json!({"text": pig_sdk::format_size(limit("bytes").unwrap_or(0))}),
        };
        ctx.notify(&format!("truncate:{result}:end"), "info");
        CommandResult::Ok
    });
    // builtin_probe: see the Go fixture (testfixture/builtin_tool.go).
    ext.command("builtin_probe", "Delegate to a built-in tool through the SDK and report what it returned", |ctx, args| {
        let case: Value = match serde_json::from_str(&args) {
            Ok(case) => case,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        match builtin_probe(ctx, &case) {
            Ok(report) => {
                ctx.notify(&format!("builtin:{report}:end"), "info");
                CommandResult::Ok
            }
            Err(err) => CommandResult::Error(err),
        }
    });
    // message_probe reads the event payload in its JSON argument with the SDK's message_role and message_text helpers (TestConformance_MessageHelpersAgreeAcrossSDKs).
    ext.command("message_probe", "Report message_role and message_text of a JSON event payload", |ctx, args| {
        let data: Value = match serde_json::from_str(&args) {
            Ok(data) => data,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let result = json!({"role": pig_sdk::message_role(&data).unwrap_or_default(), "text": pig_sdk::message_text(&data)});
        ctx.notify(&format!("message:{result}:end"), "info");
        CommandResult::Ok
    });
    // file_queue_probe runs the tasks in its JSON argument concurrently through the SDK's file mutation queue and reports the start/end log and the tasks that failed (TestConformance_FileMutationQueueAgreesAcrossSDKs).
    // Tasks are ordered by waiting on each other's progress, never by sleeping through a race: waitStarted/waitCalled delay the call into the queue, holdEnded/holdCalled keep a task inside it.
    // After a "called" signal a fixed settle time lets a task that wrongly skipped the queue show itself.
    ext.command("file_queue_probe", "Run tasks through the file mutation queue and report the log", |ctx, args| {
        use std::collections::HashSet;
        use std::time::{Duration, Instant};
        let case: Value = match serde_json::from_str(&args) {
            Ok(case) => case,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let settle_ms = case["settleMs"].as_u64().unwrap_or(0);
        let log = Arc::new(Mutex::new(Vec::<String>::new()));
        let failed = Arc::new(Mutex::new(Vec::<u64>::new()));
        let progress = Arc::new(Mutex::new(HashSet::<(String, u64)>::new()));
        let ids_of = |task: &Value, key: &str| -> Vec<u64> { task[key].as_array().map(|ids| ids.iter().filter_map(Value::as_u64).collect()).unwrap_or_default() };
        let handles: Vec<_> = case["tasks"]
            .as_array()
            .cloned()
            .unwrap_or_default()
            .into_iter()
            .map(|task| {
                let (log, failed, progress) = (log.clone(), failed.clone(), progress.clone());
                let (wait_started, wait_called) = (ids_of(&task, "waitStarted"), ids_of(&task, "waitCalled"));
                let (hold_ended, hold_called) = (ids_of(&task, "holdEnded"), ids_of(&task, "holdCalled"));
                std::thread::spawn(move || {
                    let id = task["id"].as_u64().unwrap_or(0);
                    let fail = task["fail"].as_bool().unwrap_or(false);
                    let mark = |kind: &str, who: u64| {
                        progress.lock().unwrap().insert((kind.to_string(), who));
                    };
                    let await_stage = |kind: &str, ids: &[u64]| {
                        for other in ids {
                            let deadline = Instant::now() + Duration::from_secs(20);
                            while !progress.lock().unwrap().contains(&(kind.to_string(), *other)) {
                                if Instant::now() > deadline {
                                    log.lock().unwrap().push(format!("timeout:{id}:{kind}:{other}"));
                                    return;
                                }
                                std::thread::sleep(Duration::from_millis(1));
                            }
                        }
                    };
                    let settle = |ids: &[u64]| {
                        if !ids.is_empty() {
                            std::thread::sleep(Duration::from_millis(settle_ms));
                        }
                    };
                    await_stage("started", &wait_started);
                    await_stage("called", &wait_called);
                    settle(&wait_called);
                    mark("called", id);
                    let outcome = pig_sdk::with_file_mutation_queue(task["path"].as_str().unwrap_or(""), || {
                        log.lock().unwrap().push(format!("start:{id}"));
                        mark("started", id);
                        await_stage("ended", &hold_ended);
                        await_stage("called", &hold_called);
                        settle(&hold_called);
                        log.lock().unwrap().push(format!("end:{id}"));
                        mark("ended", id);
                        !fail
                    });
                    if !matches!(outcome, Ok(true)) {
                        mark("ended", id);
                        failed.lock().unwrap().push(id);
                    }
                })
            })
            .collect();
        for handle in handles {
            let _ = handle.join();
        }
        let mut failed = failed.lock().unwrap().clone();
        failed.sort_unstable();
        ctx.notify(&format!("file_queue:{}:end", json!({"log": *log.lock().unwrap(), "errors": failed})), "info");
        CommandResult::Ok
    });
    // host_paths_probe reports getPackageDir() and getShellConfig() as this SDK answers them, for the default shell and for the existing and the missing custom shell path
    // in args ("<existing>|<missing>") (TestConformance_HostPathsAgreeAcrossSDKs).
    ext.command("host_paths_probe", "Report getPackageDir and getShellConfig", |ctx, args| {
        let (existing, missing) = args.split_once('|').unwrap_or((args, ""));
        let shell = |custom: Option<&str>| match pig_sdk::get_shell_config(custom) {
            Ok(config) => {
                let mut report = json!({"shell": config.shell, "args": config.args});
                if let Some(transport) = config.command_transport {
                    report["commandTransport"] = json!(transport);
                }
                report
            }
            Err(err) => json!({"error": err.to_string()}),
        };
        match pig_sdk::get_package_dir() {
            Ok(package_dir) => {
                ctx.notify(&format!("host_paths:{}:end", json!({"packageDir": package_dir, "default": shell(None), "custom": shell(Some(existing)), "missing": shell(Some(missing))})), "info");
                CommandResult::Ok
            }
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    // host_helpers_probe runs the host-served pi helpers over the golden file's inputs (TestConformance_HostHelpersAgreeAcrossSDKs). args is
    // "<golden>|<auth.json path>|<text to copy>".
    ext.command("host_helpers_probe", "Run the host-served pi helpers over the golden inputs", |ctx, args| match host_helpers_probe(&ctx, &args) {
        Ok(report) => {
            ctx.notify(&format!("host_helpers:{report}:end"), "info");
            CommandResult::Ok
        }
        Err(err) => CommandResult::Error(err),
    });
    // pi_ai_helpers_probe runs every call of the vector calls (test/parity/sdk-vectors, Pi's own answers at the pin, listed in one file by the test) through this SDK's pi-ai
    // helpers and reports each answer as JSON (TestConformance_PiAiHelpersAgreeAcrossSDKs).
    ext.command("pi_ai_helpers_probe", "Run the golden pi-ai helper calls", |ctx, args| match pi_ai_helpers_probe(&args) {
        Ok(report) => {
            ctx.notify(&format!("pi_ai_helpers:{report}:end"), "info");
            CommandResult::Ok
        }
        Err(err) => CommandResult::Error(err),
    });
    // host_info_probe reports Pi's getAgentDir(), CONFIG_DIR_NAME and VERSION as this SDK answers them (TestConformance_HostConfigurationAgreesAcrossSDKs).
    ext.command("host_info_probe", "Report getAgentDir, CONFIG_DIR_NAME and VERSION", move |ctx, _args| {
        let report = (|| -> std::io::Result<Value> {
            Ok(json!({"agentDir": pig_sdk::get_agent_dir()?, "configDirName": pig_sdk::get_config_dir_name()?, "version": pig_sdk::get_version()?, "factoryAgentDir": factory_agent_dir}))
        })();
        match report {
            Ok(report) => {
                ctx.notify(&format!("host_info:{report}:end"), "info");
                CommandResult::Ok
            }
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    // ui_dialog_values_probe reports the value each dialog resolves with (TestConformance_UIDialogValuesAgreeAcrossSDKs): `<undefined>` for Pi's undefined, else the JSON of the value.
    ext.command("ui_dialog_values_probe", "Report the typed result of each dialog", |ctx, _args| {
        let report = |name: &str, value: Value, ok: bool| {
            let text = if ok { value.to_string() } else { "<undefined>".to_string() };
            ctx.notify(&format!("{name}={text}"), "info");
        };
        let result = (|| -> std::io::Result<()> {
            for title in ["S1", "S2", "S3", "S4"] {
                let (value, ok) = ctx.select(title, &["a", "b"])?;
                report(title, value, ok);
            }
            for title in ["C1", "C2", "C3", "C4"] {
                report(title, ctx.confirm(title, "m")?, true);
            }
            for title in ["I1", "I2"] {
                let (value, ok) = ctx.input(title, "p")?;
                report(title, value, ok);
            }
            for title in ["E1", "E2"] {
                let (value, ok) = ctx.editor(title, "p")?;
                report(title, value, ok);
            }
            Ok(())
        })();
        match result { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err.to_string()) }
    });
    // ui_forward_probe calls the UI members whose host effect a conformance row records (TestConformance_UIForwardingAgreesAcrossSDKs).
    ext.command("ui_forward_probe", "Call the working, paste, editor-text and tools-expanded UI members", |ctx, _args| {
        ctx.set_working_message("Thinking hard");
        ctx.set_working_message("");
        ctx.clear_working_message();
        let _ = ctx.set_hidden_thinking_label("Pondering");
        let _ = ctx.set_hidden_thinking_label("");
        let _ = ctx.clear_hidden_thinking_label();
        ctx.set_working_visible(false);
        if let Err(err) = ctx.set_working_indicator(json!({"frames": ["-", "+"], "intervalMs": 120})) {
            return CommandResult::Error(err.to_string());
        }
        ctx.paste_to_editor("pasted \u{1F600}");
        ctx.set_editor_text("draft");
        ctx.set_tools_expanded(true);
        CommandResult::Ok
    });
    // dialog-stack-probe opens its dialogs at the same time, each from its own thread, and reports each one's answer as it settles (TestConformance_ConcurrentDialogsAgreeAcrossSDKs). "pair" is a select A and a confirm B; "mix" is a select S, a confirm C, an input I and an editor E.
    ext.command("dialog-stack-probe", "Open concurrent dialogs from one command", |ctx, scenario| {
        let names: &[&str] = if scenario == "mix" { &["S", "C", "I", "E"] } else { &["A", "B"] };
        let workers: Vec<_> = names
            .iter()
            .map(|name| {
                let ctx = ctx.clone();
                let name = name.to_string();
                thread::spawn(move || -> std::io::Result<()> {
                    let chosen = |(value, ok): (serde_json::Value, bool)| if ok { value.as_str().map_or_else(|| value.to_string(), str::to_string) } else { "cancelled".to_string() };
                    let value = match name.as_str() {
                        "A" => chosen(ctx.select("A", &["a1", "a2"])?),
                        "B" => ctx.confirm("B", "message")?.to_string(),
                        "S" => chosen(ctx.select("S", &["s1", "s2"])?),
                        "C" => ctx.confirm("C", "message")?.to_string(),
                        "I" => chosen(ctx.input("I", "placeholder")?),
                        _ => chosen(ctx.editor("E", "prefill")?),
                    };
                    ctx.notify(&format!("stack settled {name}={value}"), "info");
                    Ok(())
                })
            })
            .collect();
        for worker in workers {
            match worker.join() {
                Ok(Ok(())) => {}
                Ok(Err(err)) => return CommandResult::Error(err.to_string()),
                Err(_) => return CommandResult::Error("a dialog thread panicked".to_string()),
            }
        }
        ctx.notify("stack done", "info");
        CommandResult::Ok
    });
    // ui_wire_empties_probe calls the UI members Pi's RPC mode writes with empty strings and lists, and the optional members it omits, in this SDK's own spelling of each (TestConformance_UIEmptyMembersAgreeAcrossSDKs).
    ext.command("ui_wire_empties_probe", "Call the UI members with empty and unset arguments", |ctx, _args| {
        ctx.set_status("k", "");
        ctx.clear_status("k");
        ctx.notify("", "");
        ctx.notify("typed", "warning");
        ctx.set_title("");
        ctx.set_editor_text("");
        let _ = ctx.select("T", &[]);
        let _ = ctx.confirm("", "");
        let _ = ctx.input("T1", "");
        let _ = ctx.input("T3", "ph");
        let _ = ctx.editor("E1", "");
        let _ = ctx.editor("E3", "pre");
        CommandResult::Ok
    });
    let installed = installed_factory;
    ext.command("editor-clear", "Restore the host's editor", move |ctx, _args| {
        *installed.lock().unwrap() = None;
        match ctx.clear_editor_component() {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("editor-install-eager", "Install a custom editor that calls super in its factory", |ctx, _args| {
        let notify = ctx.clone();
        match ctx.set_editor_component(move |base| {
            let text = base.set_text(&JsString::from("eager")).and_then(|()| base.get_text());
            // The stock editor's other queries and setters, at the values it already holds.
            let _ = (base.get_cursor(), base.get_expanded_text(), base.is_showing_autocomplete());
            let _ = (base.set_padding_x(0), base.set_autocomplete_max_visible(5));
            match text {
                Ok(text) => notify.notify(&format!("eager:{}", text.to_string_lossy()), "info"),
                Err(err) => notify.notify(&format!("eager-error:{err}"), "info"),
            }
            Box::new(ConformanceEditor { base })
        }) {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("editor-install-embed", "Install a custom editor that embeds the working status", |ctx, _args| {
        match ctx.set_editor_component(|base| Box::new(EmbeddingEditor(ConformanceEditor { base }))) {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("editor-install-hooks", "Install a custom editor that sets CustomEditor's handlers", |ctx, _args| {
        match ctx.set_editor_component(|base| Box::new(HooksEditor::new(base))) {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    // headless-ui-probe hands the SDK footer, header and custom component closures; its argument is a file every closure appends a marker to when it runs (TestConformance_HeadlessModesNeverRunUIClosures).
    ext.command("headless-ui-probe", "Hand the SDK footer, header and custom component closures", |ctx, path| {
        fn mark(path: &str, line: &str) {
            use std::io::Write;
            if let Ok(mut file) = std::fs::OpenOptions::new().append(true).create(true).open(path) {
                let _ = writeln!(file, "{line}");
            }
        }
        struct HeadlessComponent(String);
        impl RemoteComponent for HeadlessComponent {
            fn render(&self, _width: u32) -> Vec<String> {
                mark(&self.0, "rust:custom");
                vec!["rust:custom".to_string()]
            }
            fn handle_input(&mut self, _data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
                mark(&self.0, "rust:custom");
                Ok(RemoteComponentResult::done(Some(json!("closed"))))
            }
        }
        let footer_path = path.to_string();
        if let Err(err) = ctx.set_footer_renderer(Some(move |_width: u32| {
            mark(&footer_path, "rust:footer");
            vec!["rust:footer".to_string()]
        })) {
            return CommandResult::Error(err.to_string());
        }
        let header_path = path.to_string();
        if let Err(err) = ctx.set_header_renderer(Some(move |_width: u32| {
            mark(&header_path, "rust:header");
            vec!["rust:header".to_string()]
        })) {
            return CommandResult::Error(err.to_string());
        }
        if let Err(err) = ctx.custom_component(HeadlessComponent(path.to_string()), json!({})) {
            return CommandResult::Error(err.to_string());
        }
        // Options only, no component: Pi's RPC and no-op UIs resolve undefined, the TUI SDK reports it unsupported.
        match ctx.custom(json!({})) {
            Ok(None) => mark(path, "rust:custom-opaque:undefined"),
            Ok(Some(_)) => mark(path, "rust:custom-opaque:value"),
            Err(_) => mark(path, "rust:custom-opaque:error"),
        }
        CommandResult::Ok
    });
    ext.flag("flag-true", pig_sdk::FlagOptions::boolean("", true));
    ext.flag("flag-false", pig_sdk::FlagOptions::boolean("", false));
    ext.flag("flag-string", pig_sdk::FlagOptions::string("", "default"));
    ext.flag("flag-empty", pig_sdk::FlagOptions::string("", ""));
    ext.flag("flag-unset", pig_sdk::FlagOptions { description: String::new(), flag_type: pig_sdk::FlagType::String, default: None });
    ext.command("flag-probe", "Report registered flag values", |ctx, _args| {
        let values: Vec<_> = ["flag-true", "flag-false", "flag-string", "flag-empty", "flag-unset", "unregistered"].iter().map(|name| ctx.get_flag(name).unwrap()).collect();
        ctx.notify(&serde_json::to_string(&values).unwrap(), "info");
        CommandResult::Ok
    });
    ext.command("timeout-probe", "Send JavaScript-number timeouts", |ctx, _| {
        for timeout in [0.5, 4294967296.5, 1e21] {
            if let Err(err) = ctx.exec_with_options("timeout-command", &[], &pig_sdk::ExecOptions { timeout: Some(timeout), ..Default::default() }) {
                return CommandResult::Error(err);
            }
        }
        match ctx.select_with_options("timeout", &["a"], &pig_sdk::DialogOptions { timeout: Some(1500.5) }) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("exec-reject-probe", "Report exec outcomes", |ctx, args| {
        let commands: Vec<String> = match serde_json::from_str(args) {
            Ok(commands) => commands,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        for command in commands {
            match ctx.exec(&command, &[]) {
                Ok(result) => ctx.notify(&format!("resolved:{}", result.exit_code), "info"),
                Err(err) => ctx.notify(&format!("rejected:{err}"), "info"),
            }
        }
        CommandResult::Ok
    });
    ext.command("exec-abort-probe", "Abort a running exec through its signal", |ctx, args| {
        let probe: Value = match serde_json::from_str(args) {
            Ok(probe) => probe,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let command = probe["command"].as_str().unwrap_or_default().to_string();
        let argv: Vec<String> = probe["args"].as_array().into_iter().flatten().filter_map(|arg| arg.as_str().map(str::to_string)).collect();
        let signal = pig_sdk::ProviderSignal::new();
        if probe["pre"].as_bool() == Some(true) {
            signal.cancel();
        } else {
            let signal = signal.clone();
            let pid_file = probe["pidFile"].as_str().unwrap_or_default().to_string();
            thread::spawn(move || {
                for _ in 0..1500 {
                    if std::path::Path::new(&pid_file).exists() {
                        break;
                    }
                    thread::sleep(Duration::from_millis(10));
                }
                signal.cancel();
            });
        }
        let argv: Vec<&str> = argv.iter().map(String::as_str).collect();
        match ctx.exec_with_options(&command, &argv, &pig_sdk::ExecOptions { signal: Some(signal), ..Default::default() }) {
            Ok(result) => ctx.notify(&format!("exec-abort:killed={};code={};stdout={}", result.killed, result.exit_code, result.stdout.trim()), "info"),
            Err(err) => ctx.notify(&format!("rejected:{err}"), "info"),
        }
        CommandResult::Ok
    });
    ext.command("thinking-model", "Read the thinking level and switch the model", |ctx, _| {
        let level = match ctx.get_thinking_level() {
            Ok(level) => level,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        ctx.set_thinking_level("high");
        let (ok, _) = ctx.set_model("probe/model");
        ctx.notify(&json!([level, ok]).to_string(), "info");
        CommandResult::Ok
    });
    ext.command("replicated-accessors", "Read the replicated accessors after each write", |ctx, _| {
        let run = || -> std::io::Result<()> {
            let last_type = || -> std::io::Result<String> { Ok(ctx.get_entries()?.last().and_then(|entry| entry["type"].as_str().map(str::to_string)).unwrap_or_default()) };
            let mut out = serde_json::Map::new();
            out.insert("name0".into(), json!(ctx.session_name()));
            out.insert("model0".into(), json!(ctx.model()));
            out.insert("provider0".into(), json!(ctx.model_provider()));
            ctx.set_session_name("renamed")?;
            out.insert("name1".into(), json!(ctx.session_name()));
            ctx.set_label("one", "lab")?;
            out.insert("entryAfterLabel".into(), json!(last_type()?));
            ctx.send_message("probe", "hi", true, None, None)?;
            out.insert("entryAfterMessage".into(), json!(last_type()?));
            ctx.set_model("prov-after/id-after");
            out.insert("model1".into(), json!(ctx.model()));
            out.insert("provider1".into(), json!(ctx.model_provider()));
            out.insert("qualified1".into(), json!(ctx.model_qualified()));
            ctx.notify(&serde_json::Value::Object(out).to_string(), "info");
            Ok(())
        };
        match run() { Ok(()) => CommandResult::Ok, Err(error) => CommandResult::Error(error.to_string()) }
    });
    ext.command("active_tools_set", "Set the active tools to the comma-separated names", |ctx, args| {
        let names: Vec<&str> = args.trim().split(',').collect();
        ctx.set_active_tools(&names);
        CommandResult::Ok
    });
    ext.command("session_actions_unbound", "Report what unbound session actions answer", |ctx, _| {
        let cancelled = |name: &str, result: std::io::Result<Option<serde_json::Value>>| match result {
            Ok(value) => format!("{name}={}", value.and_then(|v| v.get("cancelled").and_then(|c| c.as_bool())).map_or("none".to_string(), |c| c.to_string())),
            Err(err) => format!("{name}=error:{err}"),
        };
        let parts = vec![
            cancelled("new", ctx.new_session(serde_json::json!({}))),
            cancelled("fork", ctx.fork("entry", serde_json::json!({}))),
            cancelled("navigate", ctx.navigate_tree("entry", serde_json::json!({}))),
            cancelled("switch", ctx.switch_session("/s.jsonl", serde_json::json!({}))),
            match ctx.reload() {
                Ok(()) => "reload=ok".to_string(),
                Err(err) => format!("reload=error:{err}"),
            },
        ];
        ctx.notify(&format!("unbound:{}", parts.join(",")), "info");
        CommandResult::Ok
    });
    ext.command("active_tools_get", "Report the active tools", |ctx, _| match ctx.get_active_tools() {
        Ok(names) => {
            ctx.notify(&format!("active_tools:{}", names.join(",")), "info");
            CommandResult::Ok
        }
        Err(err) => CommandResult::Error(err.to_string()),
    });
    ext.command("provider_probe_register", "Register a provider with one model", |ctx, _| {
        let config: Value = serde_json::from_str(r#"{"baseUrl":"https://probe.invalid/v1","api":"openai-completions","apiKey":"probe-key","models":[{"id":"probe-model","name":"Probe Model","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}]}"#).unwrap();
        match ctx.model_registry().register_provider("conformance-probe", config) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("provider_probe_unregister", "Unregister the provider", |ctx, _| match ctx.model_registry().unregister_provider("conformance-probe") {
        Ok(_) => CommandResult::Ok,
        Err(err) => CommandResult::Error(err.to_string()),
    });
    ext.command("commands-probe", "Read the host's slash commands", |ctx, _| match ctx.get_commands() {
        Ok(commands) => {
            let listed: Vec<Value> = commands.iter().filter(|c| c.name == "conformance-listed").map(|c| json!([c.name, c.source, c.description])).collect();
            ctx.notify(&Value::Array(listed).to_string(), "info");
            CommandResult::Ok
        }
        Err(err) => CommandResult::Error(err.to_string()),
    });
    ext.command("session-identity", "Read context identity accessors", |ctx, _| {
        let identity = (|| -> std::io::Result<Value> { Ok(json!([ctx.get_session_id()?, ctx.get_session_file()?, ctx.get_leaf_id()?, ctx.get_session_name()?])) })();
        match identity {
            Ok(identity) => { ctx.notify(&identity.to_string(), "info"); CommandResult::Ok }
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("registry-session", "Read registry and session facades", |ctx, _| {
        let run = || -> std::io::Result<()> {
            let s = ctx.session_manager();
            let r = ctx.model_registry();
            let out = json!({
                "cwd":s.get_cwd()?, "dir":s.get_session_dir()?, "id":s.get_session_id()?, "name":s.get_session_name()?, "leaf":s.get_leaf_id()?,
                "entry":s.get_entry("one")?, "missing":s.get_entry("missing")?, "label":s.get_label("one")?,
                "entries":s.get_entries()?, "branch":s.get_branch(Some("one"))?, "tree":s.get_tree()?,
                "contextEntries":s.build_context_entries()?, "projection":s.build_session_projection()?,
                "persisted":s.is_persisted()?, "usesDefaultDir":s.uses_default_session_dir()?, "children":s.get_children(Some("one"))?, "sessionContext":s.build_session_context()?,
                "models":r.get_all()?, "available":r.get_available()?, "status":r.get_provider_auth_status("registry-probe")?,
                "display":r.get_provider_display_name("registry-probe")?, "error":r.get_error()?,
                "config":r.get_registered_provider_config("registry-probe")?, "ids":r.get_registered_provider_ids()?,
                "auth":r.get_provider_auth("registry-probe")?, "apiKey":r.get_api_key_for_provider("registry-probe"),
                "missingKey":r.get_api_key_for_provider("missing"), "refresh":r.refresh(json!({"allowNetwork":false}))?
            });
            ctx.notify(&out.to_string(), "info");
            Ok(())
        };
        match run() { Ok(()) => CommandResult::Ok, Err(error) => CommandResult::Error(error.to_string()) }
    });
    ext.command("registry-edges", "Read the registry answers that depend on the provider state", |ctx, _| {
        let run = || -> std::io::Result<()> {
            let r = ctx.model_registry();
            let mut models = r.get_all()?.as_array().cloned().unwrap_or_default();
            models.push(json!({}));
            let has_auth = models.iter().map(|model| r.has_configured_auth(model)).collect::<std::io::Result<Vec<_>>>()?;
            let oauth = models.iter().map(|model| r.is_using_oauth(model)).collect::<std::io::Result<Vec<_>>>()?;
            let out = json!({
                "available": r.get_available()?, "hasAuth": has_auth, "oauth": oauth,
                "found": r.find("registry-probe", "a")?, "foundOff": r.find("registry-off", "d")?,
                "missing": r.find("registry-probe", "zzz")?, "mismatch": r.find("registry-probe", "c")?
            });
            ctx.notify(&out.to_string(), "info");
            Ok(())
        };
        match run() { Ok(()) => CommandResult::Ok, Err(error) => CommandResult::Error(error.to_string()) }
    });
    ext.command("registry-unbound-reads", "Report find and a session read against a host that cannot answer", |ctx, _| {
        let report = |read: std::io::Result<Value>| match read {
            Ok(value) => format!("ok:{value}"),
            Err(error) => format!("error:{error}"),
        };
        let find = report(ctx.model_registry().find("registry-probe", "a").map(|model| model.unwrap_or(Value::Null)));
        let entries = report(ctx.session_manager().get_entries());
        ctx.notify(&json!({"find": find, "entries": entries}).to_string(), "info");
        CommandResult::Ok
    });
    ext.command("session-order", "Read the session as Pi returns it", |ctx, _| {
        let run = || -> std::io::Result<()> {
            let s = ctx.session_manager();
            let mut out = serde_json::Map::new();
            out.insert("getEntries".into(), s.get_entries()?);
            out.insert("getEntry".into(), s.get_entry("a4")?);
            out.insert("getLeafEntry".into(), s.get_leaf_entry()?);
            out.insert("getBranch".into(), s.get_branch(Some("a4"))?);
            out.insert("getChildren".into(), s.get_children(Some("a1"))?);
            out.insert("getTree".into(), s.get_tree()?);
            out.insert("buildContextEntries".into(), s.build_context_entries()?);
            out.insert("buildSessionProjection".into(), s.build_session_projection()?);
            out.insert("buildSessionContext".into(), s.build_session_context()?);
            ctx.notify(&Value::Object(out).to_string(), "info");
            Ok(())
        };
        match run() { Ok(()) => CommandResult::Ok, Err(error) => CommandResult::Error(error.to_string()) }
    });
    ext.message_renderer("conformance-message", |_ctx, message, options, width| {
        let content = message.get("content").and_then(Value::as_str).unwrap_or("");
        if content == "padding-options" {
            return Ok(vec![serde_json::to_string(&options).map_err(|e| e.to_string())?]);
        }
        Ok(vec![format!(
            "renderer:{content}:expanded={}:width={width}",
            options.expanded
        )])
    });
    ext.message_view_renderer("kit-message", |_ctx, _message, _options, _width| Ok(kit_probe_view()));
    ext.entry_view_renderer("kit-entry", |_ctx, _entry, _options, _width| Ok(kit_probe_view()));
    ext.markdown_transformer(|markdown, context| {
        // "trace:<dir>:<name>" records the body's entry in <dir>/trace; the body named "first" returns only once <dir>/release exists.
        if let Some(rest) = markdown.strip_prefix("trace:") {
            // The name is the last field: a Windows <dir> contains a drive colon.
            if let Some((dir, name)) = rest.rsplit_once(':') {
                let append = |line: &str| {
                    use std::io::Write;
                    let mut file = std::fs::OpenOptions::new().create(true).append(true).open(format!("{dir}/trace")).unwrap();
                    writeln!(file, "{line}").unwrap();
                };
                append(&format!("entered {name}"));
                if name == "first" {
                    while !std::path::Path::new(&format!("{dir}/release")).exists() {
                        thread::sleep(Duration::from_millis(10));
                    }
                    append("returned first");
                }
            }
        }
        Some(format!(
            "md:{markdown}:{}:streaming={}:width={}",
            context.message_type, context.is_streaming, context.available_width
        ))
    });
    fn resolved_line(prefix: &'static str, tool: &str) -> pig_sdk::ToolRendererSet {
        let tool = tool.to_string();
        pig_sdk::ToolRendererSet::with_call(move |_ctx, args, _render, _width| {
            let q = args.get("q").and_then(Value::as_str).unwrap_or("");
            Ok(vec![format!("{prefix}:{tool}:{q}")])
        })
    }
    ext.tool_renderer(|tool, next| match tool {
        "conformance_tool_renderer" => Some(resolved_line("resolved", tool)),
        "kit_resolved_tool" => {
            // The resolver draws a call and its result as the kit probe (D107): the view forms of ToolRendererSet.
            let mut set = pig_sdk::ToolRendererSet::default();
            set.call_view = Some(Arc::new(|_ctx, _args, _render, _width| Ok(kit_probe_view())));
            set.result_view = Some(Arc::new(|_ctx, _result, _options, _render, _width| Ok(kit_probe_view())));
            Some(set)
        }
        "conformance_no_renderer" => None,
        "conformance_fill" => next().or_else(|| Some(resolved_line("filled", tool))),
        "conformance_wrap" => {
            let mut wrapped = next()?;
            wrapped.render_call = resolved_line("wrapped", tool).render_call;
            Some(wrapped)
        }
        _ => next(),
    });
    ext.command("late_tool_renderer", "Register a tool renderer resolver after loading", |ctx, _args| {
        match ctx.register_tool_renderer(|tool, next| if tool == "conformance_late" { Some(resolved_line("late", tool)) } else { next() }) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.entry_renderer("conformance-entry", |_ctx, entry, options, width| {
        let data = entry.get("data").and_then(Value::as_str).unwrap_or("");
        Ok(vec![format!(
            "entryrenderer:{data}:expanded={}:width={width}",
            options.expanded
        )])
    });
    ext.tool(
        "render_probe",
        "Render its own tool card",
        json!({"type": "object", "properties": {}}),
        |_ctx, _params| ToolResult::text("render ok"),
    );
    ext.tool_render_shell("render_probe", ToolRenderShell::SelfShell);
    ext.render_tool_call("render_probe", |_ctx, args, render, width| {
        let calls = render.state.get("calls").and_then(Value::as_u64).unwrap_or(0) + 1;
        render.state.insert("calls".to_string(), json!(calls));
        let topic = args.get("topic").and_then(Value::as_str).unwrap_or("");
        Ok(vec![format!(
            "toolrender:call:{topic}:partial={}:calls={calls}:width={width}",
            render.is_partial
        )])
    });
    ext.render_tool_result("render_probe", |_ctx, result, options, render, width| {
        let text = result.content[0].get("text").and_then(Value::as_str).unwrap_or("");
        let key = result.details.get("k").and_then(Value::as_str).unwrap_or("");
        let calls = render.state.get("calls").cloned().unwrap_or(Value::Null);
        let duration = render.duration_ms.map_or("none".to_string(), |ms| ms.to_string());
        Ok(vec![format!(
            "toolrender:result:{text}:{key}:expanded={}:calls={calls}:width={width}:duration={duration}",
            options.expanded
        )])
    });
    ext.tool(
        "update_tool",
        "Stream two partial results",
        json!({"type": "object", "properties": {}}),
        |ctx, _params: Value| {
            let _ = ctx.on_update(ToolResult::Json(json!({"content": [{"type": "text", "text": "step 1"}]})));
            let _ = ctx.on_update(ToolResult::Json(json!({"content": [{"type": "text", "text": "step 2"}]})));
            ToolResult::Json(json!({"content": [{"type": "text", "text": "done"}]}))
        },
    );
    ext.tool(
        "big_out",
        "Return text of the size in the argument",
        json!({"type": "object", "properties": {}}),
        |_ctx, params: Value| ToolResult::Json(json!({"content": [{"type": "text", "text": "a".repeat(params["n"].as_u64().unwrap_or(0) as usize)}]})),
    );
    ext.tool(
        "echo_params",
        "Return the arguments as the result details",
        json!({"type": "object", "properties": {}}),
        |_ctx, params: Value| ToolResult::Json(json!({"content": [{"type": "text", "text": "echoed"}], "details": params})),
    );
    ext.tool(
        "nan_out",
        "Return details holding NaN and Infinity",
        json!({"type": "object", "properties": {}}),
        |_ctx, _params: Value| ToolResult::Json(json!({"content": [{"type": "text", "text": "nan"}], "details": {"n": f64::NAN, "i": f64::NEG_INFINITY, "l": [f64::NAN, 2, {"d": f64::INFINITY}], "ok": 1}})),
    );
    ext.tool(
        "ordered_details",
        "Return details whose members are not in alphabetical order",
        json!({"type": "object", "properties": {}}),
        |ctx, _params: Value| {
            let _ = ctx.on_update(ToolResult::Json(json!({"content": [{"type": "text", "text": "partial"}], "details": {"zeta": 1.0, "alpha": {"yy": 2, "bb": 3}, "mid": [{"qq": 1, "aa": 2}]}})));
            ToolResult::Json(json!({"content": [{"type": "text", "text": "done"}], "details": {"zeta": 1.0, "alpha": {"yy": 2, "bb": 3}, "mid": [{"qq": 1, "aa": 2}]}}))
        },
    );
    ext.tool(
        "ordered_result",
        "Return a result whose members are not in the declared order",
        json!({"type": "object", "properties": {}}),
        |ctx, _params: Value| {
            let _ = ctx.on_update(ToolResult::Json(json!({"details": {"k": 1}, "content": [{"type": "text", "text": "partial"}]})));
            ToolResult::Json(json!({"details": {"k": 1}, "is_error": true, "content": [{"type": "text", "text": "done"}]}))
        },
    );
    let abort_observed = Arc::new(AtomicBool::new(false));
    let abort_set = abort_observed.clone();
    let abort_reason: Arc<std::sync::Mutex<Option<String>>> = Arc::default();
    let abort_reason_set = abort_reason.clone();
    let abort_reason_read = abort_reason;
    ext.tool(
        "abort_tool",
        "Wait for the abort signal",
        json!({"type": "object", "properties": {}}),
        move |ctx, _params: Value| {
            let _ = ctx.on_update(ToolResult::Json(json!({"content": [{"type": "text", "text": "waiting"}]})));
            while !ctx.is_cancelled() {
                thread::sleep(Duration::from_millis(10));
            }
            *abort_reason_set.lock().unwrap() = ctx.cancellation_reason();
            abort_set.store(true, Ordering::SeqCst);
            ToolResult::text("aborted")
        },
    );
    // hang_tool ignores its abort signal for longer than the host's abort grace period (D111).
    ext.tool(
        "hang_tool",
        "Ignore the abort signal",
        json!({"type": "object", "properties": {}}),
        |ctx, _params: Value| {
            let _ = ctx.on_update(ToolResult::Json(json!({"content": [{"type": "text", "text": "waiting"}]})));
            thread::sleep(Duration::from_secs(8));
            ToolResult::text("late")
        },
    );
    ext.command(
        "abort_probe",
        "Report whether abort_tool saw its abort signal",
        move |ctx, _args| {
            ctx.notify(&format!("abort:{}", abort_observed.load(Ordering::SeqCst)), "info");
            CommandResult::Ok
        },
    );
    ext.command("abort_reason", "Report the cancellation reason abort_tool saw", move |ctx, _args| {
        ctx.notify(&format!("reason:{}", abort_reason_read.lock().unwrap().clone().unwrap_or_default()), "info");
        CommandResult::Ok
    });
    ext.tool(
        "rich_tool",
        "Return text, image, and terminate",
        json!({"type": "object", "properties": {}}),
        |_ctx, _params: Value| {
            ToolResult::Json(json!({
                "content": [
                    {"type": "text", "text": "  padded  "},
                    {"type": "image", "data": "aW1n", "mimeType": "image/png"},
                    {"type": "text", "text": "tail\n"}
                ],
                "terminate": true
            }))
        },
    );
    ext.tool(
        "echo",
        "Echo back the input",
        json!({
            "type": "object",
            "required": ["text"],
            "properties": {"text": {"type": "string", "description": "Text to echo"}, "offset": {"type": "number"}}
        }),
        |_ctx, params: Value| {
            let text = params.get("text").and_then(Value::as_str).unwrap_or("");
            ToolResult::text(format!("echo: {text}"))
        },
    );
    ext.tool_with_prepare_arguments(
        "prepared_tool",
        "Transform legacy arguments before execution",
        json!({"type": "object", "required": ["text"], "properties": {"text": {"type": "string"}}}),
        |params| Ok(json!({"text": params.get("legacy").cloned().unwrap_or(Value::Null)})),
        |_ctx, params| {
            let text = params.get("text").and_then(Value::as_str).unwrap_or("");
            ToolResult::text(format!("prepared:{text}"))
        },
    );
    // Reports the order in which calls start: the number of calls that started before it, plus its own argument.
    static STARTED_CALLS: std::sync::atomic::AtomicUsize = std::sync::atomic::AtomicUsize::new(0);
    ext.tool(
        "start_order",
        "Report the order in which calls start",
        json!({"type": "object", "properties": {"n": {"type": "number"}}}),
        |_ctx, params: Value| {
            let number = STARTED_CALLS.fetch_add(1, std::sync::atomic::Ordering::SeqCst) + 1;
            ToolResult::text(format!("start#{number} n={}", params.get("n").map(Value::to_string).unwrap_or_default()))
        },
    );
    // A tool that notifies inline and, from a thread timer_release releases, after its handler returned (D189).
    static TIMER_GATE: (std::sync::Mutex<usize>, std::sync::Condvar) = (std::sync::Mutex::new(0), std::sync::Condvar::new());
    static STALE_CTX: std::sync::Mutex<Option<pig_sdk::Context>> = std::sync::Mutex::new(None);
    ext.tool(
        "timer_defer",
        "Notify inline and from deferred work",
        json!({"type": "object"}),
        |ctx, _params: Value| {
            ctx.notify("inline:sync", "info");
            *STALE_CTX.lock().unwrap() = Some(ctx.clone());
            let ctx = ctx.clone();
            std::thread::spawn(move || {
                let (released, signal) = &TIMER_GATE;
                let mut count = released.lock().unwrap();
                while *count == 0 {
                    count = signal.wait(count).unwrap();
                }
                *count -= 1;
                drop(count);
                ctx.notify("deferred:thread", "info");
            });
            ToolResult::text("queued")
        },
    );
    // timer_stale awaits a host call it makes through the context of the earlier timer_defer request, which Pi-port extensions keep (an activeCtx), and returns once it was applied. The call is not detached work: the handler waits for it, so the host must not hold it for a loop turn that waits for the handler.
    ext.tool(
        "timer_stale",
        "Await a host call made through an earlier request's context",
        json!({"type": "object"}),
        |_ctx, _params: Value| {
            let earlier = STALE_CTX.lock().unwrap().clone().expect("timer_defer has not run");
            earlier.notify("stale:awaited", "info");
            ToolResult::text("applied")
        },
    );
    ext.tool(
        "timer_release",
        "Release the deferred work",
        json!({"type": "object"}),
        |_ctx, _params: Value| {
            let (released, signal) = &TIMER_GATE;
            *released.lock().unwrap() += 1;
            signal.notify_one();
            ToolResult::text("released")
        },
    );
    // order_probe makes the host calls and updates whose order the ordering rows pin (ordering_test.go): its first acts are two synchronous host calls, then one update, and the tool_result handler below reports each result as its first act.
    ext.tool(
        "order_probe",
        "Make a host call and an update in the first tick",
        json!({"type": "object", "properties": {"n": {"type": "number"}}}),
        |ctx, params: Value| {
            let n = params.get("n").map(Value::to_string).unwrap_or_default();
            ctx.notify(&format!("order-call={n}"), "info");
            ctx.notify(&format!("order-then={n}"), "info");
            let _ = ctx.on_update(ToolResult::Json(json!({"content": [{"type": "text", "text": format!("order-update={n}")}]})));
            ToolResult::text(format!("order-done n={n}"))
        },
    );
    // mutation_order_probe takes the file mutation queue in its first statement and appends its tag to the file (TestFileMutationOrderAcrossSDKs).
    ext.tool(
        "mutation_order_probe",
        "Take the file mutation queue and append a tag to the file",
        json!({"type": "object", "properties": {"file": {"type": "string"}, "tag": {"type": "string"}}}),
        |_, params: Value| {
            let file = params["file"].as_str().unwrap_or_default().to_string();
            let tag = params["tag"].as_str().unwrap_or_default().to_string();
            let result = pig_sdk::with_file_mutation_queue(&file, || {
                let before = std::fs::read_to_string(&file).unwrap_or_default();
                std::fs::write(&file, format!("{before}{tag}\n"))
            });
            match result {
                Ok(Ok(())) => ToolResult::text(format!("mutated {tag}")),
                Ok(Err(err)) | Err(err) => ToolResult::Error(err.to_string()),
            }
        },
    );
    ext.tool(
        "dialog_order",
        "Open a select dialog titled by the call and report the answer",
        json!({"type": "object", "properties": {"title": {"type": "string"}, "exec": {"type": "boolean"}}}),
        |ctx, params: Value| {
            let title = params["title"].as_str().unwrap_or("").to_string();
            if params["exec"].as_bool() == Some(true) {
                let _ = ctx.exec("dialog-order-exec", &[]);
            }
            match ctx.select(&title, &["a", "b"]) {
                Ok((selected, _)) => ToolResult::text(format!("dialog:{title}={selected}")),
                Err(error) => ToolResult::Error(error.to_string()),
            }
        },
    );
    ext.tool(
        "throw_probe",
        "Throw a value of the requested kind",
        json!({"type": "object", "properties": {"kind": {"type": "string"}}}),
        |_ctx, params: Value| match params["kind"].as_str().unwrap_or("") {
            "unicode" => ToolResult::Error("é😀 \n x".to_string()),
            "empty" => ToolResult::Error(String::new()),
            "long" => ToolResult::Error("x".repeat(100000)),
            _ => ToolResult::Text("ok".to_string()),
        },
    );
    ext.tool(
        "tool_error",
        "Return a thrown tool error",
        json!({"type": "object"}),
        |_ctx, _params: Value| ToolResult::Error("tool exploded".to_string()),
    );
    ext.tool(
        "tool_is_error",
        "Return a structured tool error result",
        json!({"type": "object"}),
        |_ctx, _params: Value| {
            ToolResult::Json(json!({"content": "soft tool error", "is_error": true}))
        },
    );
    ext.tool_with_guidelines(
        "guided_tool",
        "Tool with prompt guidelines",
        json!({"type": "object"}),
        vec!["Use guided_tool when the user asks for guided behavior.".to_string()],
        |_ctx, _params: Value| ToolResult::Json(json!({"content": "guided"})),
    );
    ext.tool_prompt_snippet("guided_tool", " \u{feff}Guided\r\n tool\t summary ");
    ext.tool_with_source(
        "sourced_tool",
        "Tool with explicit source",
        json!({"type": "object"}),
        "mcp:test-server",
        vec!["Use sourced_tool to test per-tool source attribution.".to_string()],
        |_ctx, _params: Value| ToolResult::Json(json!({"content": "sourced"})),
    );
    let mut disabled = pig_sdk::ToolDefinition::new("sampling_disabled", "sampling_disabled", "Disable constrained sampling", json!({"type":"object"}), |_, _| ToolResult::text("disabled"));
    disabled.constrained_sampling = Some(pig_sdk::ToolConstrainedSampling::Disabled);
    ext.register_tool(disabled);
    ext.tool_with_constrained_sampling(
        "grammar_tool",
        "Tool with a grammar constrained sampling request",
        json!({"type": "object"}),
        ConstrainedSampling {
            kind: "grammar".to_string(),
            strict: None,
            variants: Some(
                [("openai_lark".to_string(), "start: NUMBER".to_string())]
                    .into_iter()
                    .collect(),
            ),
        },
        |_ctx, _params: Value| ToolResult::Json(json!({"content": "grammar"})),
    );
    ext.command("complete_probe", "Complete its arguments", |_ctx, _args| CommandResult::Ok);
    ext.command_argument_completions("complete_probe", |prefix| {
        let items: Vec<AutocompleteItem> = [
            AutocompleteItem { value: "alpha".into(), label: Some("alpha — first".into()), description: None },
            AutocompleteItem { value: "apple".into(), label: None, description: Some("fruit".into()) },
            AutocompleteItem { value: "beta".into(), label: None, description: None },
        ]
        .into_iter()
        .filter(|item| item.value.starts_with(prefix.trim()))
        .collect();
        (!items.is_empty()).then_some(items)
    });
    ext.command("ping", "Respond with pong", |ctx, _args| {
        ctx.notify("pong", "info");
        CommandResult::Ok
    });
    ext.command("model-stream-probe", "Exercise model streaming", |ctx, _args| {
        let registry = ctx.model_registry();
        let find = |provider: &str, id: &str| registry.find(provider, id).map_err(|error| CommandResult::Error(format!("find {id}: {error}")));
        let current = match find("conformance", "current") { Ok(Some(model)) => model, Ok(None) => return CommandResult::Error("find current returned no model".into()), Err(error) => return error };
        if current["id"] != "current" { return CommandResult::Error(format!("find current = {current}")); }
        let found = match find("conformance", "declared") { Ok(Some(model)) => model, Ok(None) => return CommandResult::Error("find declared returned no model".into()), Err(error) => return error };
        if found["id"] != "declared" || found["provider"] != "conformance" { return CommandResult::Error(format!("find declared = {found}")); }
        let slash = match find("conformance", "org/model/name") { Ok(Some(model)) => model, Ok(None) => return CommandResult::Error("find slash returned no model".into()), Err(error) => return error };
        if slash["id"] != "org/model/name" { return CommandResult::Error(format!("find slash = {slash}")); }
        let expected_limits = json!({"maxRequestBytes":12345,"images":{"maxPerMessage":7,"maxPerRequest":11,"resize":{"maxWidth":321,"maxHeight":123,"maxBytes":45678,"jpegQuality":67}}});
        if slash["inputLimits"] != expected_limits || ctx.get_model_info().unwrap().and_then(|model| model.input_limits) != Some(expected_limits) { return CommandResult::Error(format!("model inputLimits = {slash}")); }
        for field in ["baseUrl", "input", "cost", "thinkingLevelMap", "promptCache", "contextWindow", "maxTokens", "samplingParams", "headers", "compat"] {
            if slash.get(field).is_none() { return CommandResult::Error(format!("find slash missing {field}: {slash}")); }
        }
        if slash["input"].as_array().map(Vec::len) != Some(0) || slash["cost"]["input"] != 0 || slash["cost"]["tiers"].as_array().map(Vec::len) != Some(1) || slash["compat"]["supportsStrictMode"] != false { return CommandResult::Error(format!("find slash shape = {slash}")); }
        for missing in ["missing", "override-only"] {
            match find("conformance", missing) {
                Ok(None) => {}
                Ok(Some(_)) => return CommandResult::Error(format!("find {missing} returned a model")),
                Err(error) => return error,
            }
        }
        let auth = match registry.get_api_key_and_headers(&found) {
            Ok(value) => value,
            Err(error) => return CommandResult::Error(format!("get auth: {error}")),
        };
        let expected_auth = json!({"ok":true,"apiKey":"conformance-key","headers":{"X-Conformance-Auth":"yes"},"baseUrl":"https://models.invalid/v1","env":{"CONFORMANCE_AUTH":"yes"}});
        if auth != expected_auth { return CommandResult::Error(format!("auth = {auth}")); }
        let model = json!({"provider":"conformance", "modelId":"declared", "api":"openai-responses"});
        let request = json!({
            "systemPrompt":"conformance-system",
            "messages":[
                {"role":"system", "content":[{"type":"text", "text":"signed system", "textSignature":"system-signature"}], "sections":{"zeta":"last-first","alpha":null,"middle":"middle"}, "timestamp":41},
                {"role":"user", "content":"hello", "timestamp":42},
                {"role":"assistant", "content":[{"type":"text", "text":"prior", "textSignature":"signed"}], "api":"openai-responses", "provider":"prior-provider", "model":"prior-model", "usage":{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"totalTokens":10,"cost":{"input":0.1,"output":0.2,"cacheRead":0.3,"cacheWrite":0.4,"total":1.0}}, "stopReason":"stop", "timestamp":43}
            ],
            "tools":[{"name":"lookup", "description":"lookup", "parameters":{"type":"object"}, "constrainedSampling":{"type":"grammar","variants":{"openai_lark":"start: NUMBER"}}}]
        });
        let options = json!({
            "timeoutMs":0,"websocketConnectTimeoutMs":1234,"maxRetries":2,"maxRetryDelayMs":3000,
            "maxTokens":321, "temperature":0.65, "samplingParams":{"topP":0.8},
            "thinkingBudgets":{"minimal":11,"low":22,"medium":33,"high":44}, "reasoning":"high", "isReasoning":true,
            "env":{"WIRE_ENV":"request-value","SECOND_ENV":"distinct-value"}, "headers":{"X-Wire":"yes","X-Remove":null}, "sessionId":"conformance-session", "transport":"sse"
        });
        let stream = ctx.model_registry().stream(model.clone(), request.clone(), options.clone());
        let mut types = Vec::new();
        while let Some(event) = stream.next() { types.push(event["type"].as_str().unwrap_or_default().to_string()); }
        if types.join(",") != "start,text_start,text_delta,text_end,done" { return CommandResult::Error(format!("stream events = {types:?}")); }
        let result = stream.result().unwrap_or_default();
        if result["content"][0]["text"] != "streamed" { return CommandResult::Error(format!("stream result = {result}")); }
        let simple = ctx.model_registry().stream_simple(model.clone(), request.clone(), options.clone());
        types.clear();
        while let Some(event) = simple.next() { types.push(event["type"].as_str().unwrap_or_default().to_string()); }
        if types.join(",") != "start,text_start,text_delta,text_end,done" { return CommandResult::Error(format!("simple events = {types:?}")); }
        if simple.result().unwrap_or_default()["stopReason"] != "stop" { return CommandResult::Error("simple did not stop".into()); }
        if ctx.model_registry().complete(model.clone(), request.clone(), options.clone()).unwrap_or_default()["stopReason"] != "stop" { return CommandResult::Error("complete did not stop".into()); }
        if ctx.model_registry().stream(model, request.clone(), options.clone()).result().unwrap_or_default()["stopReason"] != "stop" { return CommandResult::Error("result without iteration did not stop".into()); }
        let unknown = ctx.model_registry().complete(json!({"provider":"conformance", "modelId":"unknown", "api":"openai-responses"}), request.clone(), options.clone()).unwrap_or_default();
        if unknown["stopReason"] != "error" || !unknown["errorMessage"].as_str().unwrap_or_default().contains("unknown model") { return CommandResult::Error(format!("unknown result = {unknown}")); }
        let mut transport_error = ctx.model_registry().complete(json!({"provider":"conformance", "modelId":"protocol-error", "api":"openai-responses"}), request, options).unwrap_or_default();
        let timestamp = transport_error.get("timestamp").and_then(Value::as_u64).unwrap_or_default();
        if timestamp == 0 { return CommandResult::Error(format!("transport timestamp = {}", transport_error["timestamp"])); }
        transport_error.as_object_mut().map(|value| value.remove("timestamp"));
        let expected_transport = json!({
            "role":"assistant", "content":[], "api":"openai-responses", "provider":"conformance", "model":"protocol-error",
            "usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},
            "stopReason":"error", "errorMessage":"transport boom"
        });
        if transport_error != expected_transport { return CommandResult::Error(format!("transport result = {transport_error}")); }
        ctx.notify("model-stream=ok", "info");
        CommandResult::Ok
    });
    ext.command("overlay-handle-probe", "Exercise the overlay handle custom hands to on_handle", |ctx, _args| {
        struct HandleProbe;
        impl RemoteComponent for HandleProbe {
            fn render(&self, _width: u32) -> Vec<String> {
                vec!["overlay".to_string()]
            }
            fn handle_input(&mut self, data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
                if data == "q" {
                    return Ok(RemoteComponentResult::done(Some(json!("closed"))));
                }
                Ok(RemoteComponentResult::pending())
            }
        }
        let failures: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
        let recorded = failures.clone();
        let on_handle = move |handle: pig_sdk::OverlayHandle| {
            let mut expect = |label: &str, ok: bool, detail: String| {
                if !ok {
                    recorded.lock().unwrap().push(format!("{label}: {detail}"));
                }
            };
            expect("initial focused", handle.is_focused(), format!("{}", handle.is_focused()));
            expect("initial hidden", !handle.is_hidden(), format!("{}", handle.is_hidden()));
            expect("initial bounds", handle.bounds() == Some(pig_sdk::OverlayBounds { row: 3, col: 4, width: 20, height: 5 }), format!("{:?}", handle.bounds()));
            let _ = handle.focus();
            expect("focus", handle.is_focused(), format!("{}", handle.is_focused()));
            let _ = handle.set_hidden(true);
            expect("hidden", handle.is_hidden() && !handle.is_focused() && handle.bounds().is_none(), format!("{} {} {:?}", handle.is_hidden(), handle.is_focused(), handle.bounds()));
            let _ = handle.set_hidden(false);
            expect("shown", !handle.is_hidden() && !handle.is_focused(), format!("{} {}", handle.is_hidden(), handle.is_focused()));
            let _ = handle.focus();
            expect("refocus", handle.is_focused(), format!("{}", handle.is_focused()));
            let _ = handle.unfocus();
            expect("unfocus", !handle.is_focused(), format!("{}", handle.is_focused()));
            let _ = handle.unfocus_target(pig_sdk::UnfocusTarget::Nothing);
        };
        if let Err(err) = ctx.custom_component_with_handle(HandleProbe, json!({"overlay": true}), on_handle) {
            return CommandResult::Error(err.to_string());
        }
        let failures = failures.lock().unwrap();
        if !failures.is_empty() {
            return CommandResult::Error(failures.join("; "));
        }
        ctx.notify("overlay-handle=ok", "info");
        CommandResult::Ok
    });
    ext.command("overlay-focus-probe", "Render the focus the host publishes to a mounted overlay", |ctx, _args| {
        struct FocusProbe(Arc<Mutex<Option<pig_sdk::OverlayHandle>>>);
        impl RemoteComponent for FocusProbe {
            fn render(&self, _width: u32) -> Vec<String> {
                match self.0.lock().unwrap().as_ref() {
                    Some(handle) => vec![format!("focused={}", handle.is_focused())],
                    None => vec!["focused=pending".to_string()],
                }
            }
            fn handle_input(&mut self, data: &pig_sdk::JsString) -> Result<RemoteComponentResult, String> {
                if data == "q" {
                    return Ok(RemoteComponentResult::done(Some(json!("closed"))));
                }
                Ok(RemoteComponentResult::pending())
            }
        }
        let handle: Arc<Mutex<Option<pig_sdk::OverlayHandle>>> = Arc::new(Mutex::new(None));
        let mounted = handle.clone();
        let on_handle = move |overlay: pig_sdk::OverlayHandle| {
            *mounted.lock().unwrap() = Some(overlay);
        };
        if let Err(err) = ctx.custom_component_with_handle(FocusProbe(handle), json!({"overlay": true}), on_handle) {
            return CommandResult::Error(err.to_string());
        }
        ctx.notify("overlay-focus=ok", "info");
        CommandResult::Ok
    });
    ext.command("model-stream-fetch-probe", "Exercise the fetch option of the model registry stream", |ctx, _args| {
        let registry = ctx.model_registry();
        let model = json!({"provider":"conformance","id":"declared","modelId":"declared","api":"openai-responses"});
        let request = json!({"systemPrompt":"fetch","messages":[{"role":"user","content":"hello","timestamp":1}]});
        let seen: Arc<Mutex<Option<(String, String, Option<String>, String)>>> = Arc::new(Mutex::new(None));
        let recorded = seen.clone();
        let callbacks = pig_sdk::ModelStreamCallbacks::new().fetch(move |call| {
            let host = call.headers.iter().find(|(name, _)| name == "X-Host").map(|(_, value)| value.clone());
            *recorded.lock().unwrap() = Some((call.url, call.method, host, String::from_utf8_lossy(&call.body.unwrap_or_default()).to_string()));
            let body: Vec<u8> = (0..70000u32).map(|index| (index % 251) as u8).collect();
            Ok(pig_sdk::ModelFetchResponse {
                status: 207,
                status_text: "Answered".to_string(),
                headers: vec![("x-sdk-fetch".to_string(), "answered".to_string())],
                body: Some(Box::new(std::io::Cursor::new(body))),
            })
        });
        let result = registry.stream_with_callbacks(model.clone(), request.clone(), json!({}), callbacks).result();
        if result.as_ref().and_then(|value| value["stopReason"].as_str()) != Some("stop") { return CommandResult::Error(format!("fetch stream = {result:?}")); }
        let saw = seen.lock().unwrap().clone();
        let expected = Some(("https://fetch.invalid/v1/chat?x=1".to_string(), "POST".to_string(), Some("1".to_string()), "ping-body".to_string()));
        if saw != expected { return CommandResult::Error(format!("fetch saw {saw:?}")); }
        let plain = registry.stream(model, request, json!({})).result();
        if plain.as_ref().and_then(|value| value["stopReason"].as_str()) != Some("stop") { return CommandResult::Error(format!("plain stream = {plain:?}")); }
        ctx.notify("model-fetch=ok", "info");
        CommandResult::Ok
    });
    ext.command("model-stream-callback-probe", "Exercise the provider request callbacks of the model registry stream", |ctx, _args| {
        let registry = ctx.model_registry();
        let model = json!({"provider":"conformance","id":"declared","modelId":"declared","api":"openai-responses"});
        let request = json!({"systemPrompt":"callbacks","messages":[{"role":"user","content":"hello","timestamp":1}]});
        let seen: Arc<Mutex<(Value, String, Value)>> = Arc::new(Mutex::new((Value::Null, String::new(), Value::Null)));
        let (payload_seen, response_seen) = (seen.clone(), seen.clone());
        let callbacks = pig_sdk::ModelStreamCallbacks::new()
            .on_payload(move |payload, callback_model| {
                let mut marked = payload.clone();
                marked["mark"] = json!("on-payload");
                let mut seen = payload_seen.lock().unwrap();
                seen.0 = payload;
                seen.1 = callback_model["id"].as_str().unwrap_or_default().to_string();
                Ok(Some(marked))
            })
            .on_response(move |response, _model| {
                response_seen.lock().unwrap().2 = response;
                Ok(())
            })
            .transform_headers(|mut headers, _model| {
                headers["x-transformed"] = json!("yes");
                Ok(headers)
            });
        let result = registry.stream_with_callbacks(model.clone(), request.clone(), json!({}), callbacks).result();
        if result.as_ref().and_then(|value| value["stopReason"].as_str()) != Some("stop") { return CommandResult::Error(format!("callback stream = {result:?}")); }
        let (payload, callback_model, response) = seen.lock().unwrap().clone();
        if payload["original"] != true || callback_model != "declared" { return CommandResult::Error(format!("onPayload saw {payload} for {callback_model}")); }
        if response["status"] != 201 || response["headers"]["x-upstream"] != "seen" { return CommandResult::Error(format!("onResponse saw {response}")); }
        let plain = registry.stream(model, request, json!({})).result();
        if plain.as_ref().and_then(|value| value["stopReason"].as_str()) != Some("stop") { return CommandResult::Error(format!("plain stream = {plain:?}")); }
        ctx.notify("model-callbacks=ok", "info");
        CommandResult::Ok
    });
    ext.command(
        "liveness_host_call",
        "Exercise an awaited host call",
        |ctx, _args| match ctx.wait_for_idle() {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "liveness_user_call",
        "Exercise an interactive host call",
        |ctx, _args| match ctx.input("Question", "Answer") {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "liveness_fire_call",
        "Exercise a no-result UI host call",
        |ctx, _args| {
            ctx.set_title("Conformance title");
            CommandResult::Ok
        },
    );
    ext.command("command_error", "Return a command error", |_ctx, _args| {
        CommandResult::Error("command exploded".to_string())
    });
    ext.command(
        "command_awaited_error",
        "Return an error after awaited work",
        |_ctx, _args| {
            thread::sleep(Duration::from_millis(150));
            CommandResult::Error("awaited command exploded".to_string())
        },
    );
    ext.command("surface_footer", "Install a footer renderer", |ctx, _args| {
        match ctx.set_footer_renderer(Some(|width: u32| vec![format!("footer@{width}")])) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("surface_header", "Install a header renderer", |ctx, _args| {
        match ctx.set_header_renderer(Some(|width: u32| vec![format!("header@{width}")])) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("surface_static_footer", "Push static footer rows", |ctx, _args| {
        match ctx.set_footer(vec![format!("static@{}", ctx.width())]) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("surface_static_header", "Push static header rows", |ctx, _args| {
        match ctx.set_header(vec![format!("static@{}", ctx.width())]) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("surface_widget_value", "Set the string list widget through the JSON content form", |ctx, _args| {
        match ctx.set_widget_value("wide", serde_json::json!([format!("{} tail", "A".repeat(60)), "short"]), None) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("surface_widget", "Set a string list widget wider than the pane", |ctx, _args| {
        match ctx.set_widget("wide", vec![format!("{} tail", "A".repeat(60)), "short".to_string()]) {
            Ok(()) => CommandResult::Ok,
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    // pi.on called after the extension connected returns an unsubscribe (conformance TestConformance_EventUnsubscribe): the
    // handler reports the event's own message entry id, which no SDK fallback produces.
    let event_probe: Arc<Mutex<Option<pig_sdk::EventSubscription>>> = Arc::new(Mutex::new(None));
    let event_probe_for_subscribe = event_probe.clone();
    let event_subscriber = ext.event_subscriber();
    ext.command(
        "event_probe_subscribe",
        "Subscribe to turn_end after connecting",
        move |_ctx, _args| {
            let subscription = event_subscriber.on_event("turn_end", |ctx, data| {
                ctx.notify(&format!("event_probe:{}", data["messageEntryId"].as_str().unwrap_or("")), "info");
                Ok(None)
            });
            *event_probe_for_subscribe.lock().unwrap() = Some(subscription);
            CommandResult::Ok
        },
    );
    ext.command(
        "event_probe_unsubscribe",
        "Remove the turn_end handler registered after connecting",
        move |_ctx, _args| {
            if let Some(subscription) = event_probe.lock().unwrap().take() {
                subscription.unsubscribe();
            }
            CommandResult::Ok
        },
    );
    ext.command(
        "report_geometry",
        "Report observed terminal geometry",
        |ctx, _args| {
            ctx.notify(
                &format!("geometry:{}x{}", ctx.width(), ctx.height()),
                "info",
            );
            CommandResult::Ok
        },
    );
    // A width handler makes a host call the way any other handler does; the host's reply must reach it (conformance TestConformance_WidthHandlerHostCall).
    let width_probe = std::sync::Mutex::new(None);
    ext.command("arm_width_probe", "Notify from a width handler", move |ctx, _args| {
        let mut armed = width_probe.lock().unwrap();
        if armed.is_none() {
            let retained = ctx.clone();
            *armed = Some(ctx.on_width_change(move |width| {
                retained.notify(&format!("width-probe:{width}"), "info");
                retained.notify(&format!("width-probe-returned:{width}"), "info");
            }));
        }
        CommandResult::Ok
    });
    ext.command("status", "Set a status entry", |ctx, _args| {
        ctx.set_status("conformance", "ok");
        CommandResult::Ok
    });
    ext.command(
        "status_burst",
        "Set one status repeatedly without awaiting",
        |ctx, _args| {
            for i in 0..200 {
                ctx.set_status("burst", &i.to_string());
            }
            CommandResult::Ok
        },
    );
    let term_sub: Arc<Mutex<Option<TerminalInputSubscription>>> = Arc::new(Mutex::new(None));
    let sub_for_subscribe = term_sub.clone();
    ext.command(
        "term_subscribe",
        "Subscribe to raw terminal input",
        move |ctx, _args| {
            let input_ctx = ctx.clone();
            match ctx.on_terminal_input(move |data: &pig_sdk::JsString| {
                if matches!(data.as_units(), [0xd83d] | [0xde00] | [0xd83d, 0xde00]) {
                    let mut units: Vec<u16> = "seen:".encode_utf16().collect();
                    units.extend_from_slice(data.as_units());
                    return TerminalInputResult { consume: false, data: Some(pig_sdk::JsString::from_units(units)) };
                }
                match data.to_string().as_deref() {
                Ok("\x1b[96~") => TerminalInputResult {
                    consume: false,
                    data: Some(
                        serde_json::to_string(&(input_ctx.get_editor_text().unwrap(), input_ctx.get_tools_expanded().unwrap())).unwrap().into(),
                    ),
                },
                Ok("\x1b[98~") => TerminalInputResult {
                    consume: false,
                    data: Some("rewritten".into()),
                },
                Ok("\x1b[97~") => {
                    thread::sleep(Duration::from_millis(200));
                    TerminalInputResult {
                        consume: true,
                        data: None,
                    }
                }
                _ => TerminalInputResult {
                    consume: data == "\x1b[99~",
                    data: None,
                },
            }}) {
                Ok(sub) => {
                    *sub_for_subscribe.lock().unwrap() = Some(sub);
                    CommandResult::Ok
                }
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    let sub_for_release = term_sub.clone();
    ext.command(
        "term_unsubscribe",
        "Release the raw input subscription",
        move |_ctx, _args| {
            if let Some(sub) = sub_for_release.lock().unwrap().take() {
                sub.unsubscribe();
            }
            CommandResult::Ok
        },
    );
    ext.command(
        "send_message",
        "Send a custom message",
        |ctx, _args| match ctx.send_message("notice", "hello-custom", true, Some(true), Some("steer")) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "send_message_default",
        "Send a custom message with default options",
        |ctx, _args| match ctx.send_message("notice", "default", true, None, None) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "send_message_no_turn",
        "Send a custom message that never starts a turn",
        |ctx, _args| match ctx.send_message("notice", "no-turn", true, Some(false), None) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "send_user_message",
        "Send a user message",
        |ctx, args| {
            let content = if args.is_empty() { serde_json::Value::String("hello-user".into()) } else {
                match serde_json::from_str::<serde_json::Value>(&args) { Ok(value) => value, Err(err) => return CommandResult::Error(err.to_string()) }
            };
            match ctx.send_user_message(content, "followUp") {
                Ok(_) => CommandResult::Ok,
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    ext.command("settings_probe", "Report the effective settings", |ctx, _args| match ctx.get_settings() {
        Ok(settings) => {
            ctx.notify(&format!("settings_probe:{}", settings), "info");
            CommandResult::Ok
        }
        Err(err) => CommandResult::Error(err.to_string()),
    });
    ext.command("model_set", "Switch the model", |ctx, args| {
        let (ok, _error) = ctx.set_model(args.trim());
        ctx.notify(&format!("model_set:{}", ok), "info");
        CommandResult::Ok
    });
    ext.command("thinking_set", "Set the thinking level", |ctx, args| {
        ctx.set_thinking_level(args.trim());
        CommandResult::Ok
    });
    ext.command("thinking_get", "Report the thinking level", |ctx, _args| match ctx.get_thinking_level() {
        Ok(level) => {
            ctx.notify(&format!("thinking_get:{}", level), "info");
            CommandResult::Ok
        }
        Err(err) => CommandResult::Error(err.to_string()),
    });
    ext.command(
        "set_session_name",
        "Set the session name",
        |ctx, _args| match ctx.set_session_name("conformance-session") {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    let event_field_subscriber = ext.event_subscriber();
    let event_field_subscriptions: Arc<Mutex<Vec<pig_sdk::EventSubscription>>> = Arc::new(Mutex::new(Vec::new()));
    ext.command(
        "event_field_probe",
        "Report a field of the events named by the argument `<event> <field>`",
        move |_ctx, args| {
            let mut words = args.split_whitespace();
            let (name, field) = (words.next().unwrap_or("").to_string(), words.next().unwrap_or("").to_string());
            let reported = name.clone();
            let subscription = subscribe(&event_field_subscriber, &name, move |ctx, data| {
                let value = data.get(field.as_str()).map_or_else(|| "absent".to_string(), |value| value.to_string());
                ctx.notify(&format!("event_field:{}.{}={}", reported, field, value), "info");
                Ok(None)
            });
            event_field_subscriptions.lock().unwrap().push(subscription);
            CommandResult::Ok
        },
    );
    ext.command(
        "host_state_probe",
        "Report the thinking level and the session commands",
        |ctx, _args| {
            let level = match ctx.get_thinking_level() {
                Ok(level) => level,
                Err(err) => return CommandResult::Error(err.to_string()),
            };
            let commands = match ctx.get_commands() {
                Ok(commands) => commands,
                Err(err) => return CommandResult::Error(err.to_string()),
            };
            let parts: Vec<String> = commands
                .iter()
                .map(|c| {
                    [
                        c.name.as_str(),
                        c.description.as_str(),
                        c.source.as_str(),
                        c.source_info.path.as_str(),
                        c.source_info.scope.as_str(),
                    ]
                    .join("|")
                })
                .collect();
            ctx.notify(
                &format!("getThinkingLevel={level};getCommands={}", parts.join(",")),
                "info",
            );
            CommandResult::Ok
        },
    );
    ext.command(
        "rw_prompt_state",
        "Read the system prompt state after registering and activating tools",
        |ctx, _args| {
            let tool = |name: &str| {
                let mut tool = ToolDefinition::new(name, name, name, json!({"type":"object"}), |_, _| ToolResult::text("ok"));
                tool.prompt_snippet = Some(format!("{name}-snippet"));
                tool
            };
            if let Err(err) = ctx.register_tool(tool("rw-alpha")).and_then(|_| ctx.register_tool(tool("rw-beta"))) {
                return CommandResult::Error(err.to_string());
            }
            let registered = match ctx.get_system_prompt_options() {
                Ok(options) => options,
                Err(err) => return CommandResult::Error(err.to_string()),
            };
            ctx.set_active_tools(&["rw-beta"]);
            let options = match ctx.get_system_prompt_options() {
                Ok(options) => options,
                Err(err) => return CommandResult::Error(err.to_string()),
            };
            let prompt = match ctx.get_system_prompt() {
                Ok(prompt) => prompt,
                Err(err) => return CommandResult::Error(err.to_string()),
            };
            let selected: Vec<&str> = options["selectedTools"].as_array().map(|tools| tools.iter().filter_map(Value::as_str).collect()).unwrap_or_default();
            ctx.notify(
                &format!(
                    "rw_prompt_state=registered:{};selected:{};beta:{};alpha:{}",
                    registered["toolSnippets"]["rw-alpha"].as_str().unwrap_or(""),
                    selected.join(","),
                    prompt.contains("rw-beta-snippet"),
                    prompt.contains("rw-alpha-snippet"),
                ),
                "info",
            );
            CommandResult::Ok
        },
    );
    ext.command(
        "rw_idle_after_trigger",
        "Read isIdle after a message that starts a run",
        |ctx, _args| {
            if let Err(err) = ctx.send_message("rw-trigger", "rw-trigger", true, Some(true), None) {
                return CommandResult::Error(err.to_string());
            }
            match ctx.is_idle() {
                Ok(idle) => ctx.notify(&format!("rw_idle_after_trigger={idle}"), "info"),
                Err(err) => return CommandResult::Error(err.to_string()),
            }
            CommandResult::Ok
        },
    );
    let rw_settled_subscriber = ext.event_subscriber();
    let rw_settled_subscriptions: Arc<Mutex<Vec<pig_sdk::EventSubscription>>> = Arc::new(Mutex::new(Vec::new()));
    let rw_settled_once = Arc::new(AtomicBool::new(false));
    ext.command(
        "rw_idle_in_settled",
        "Read isIdle after a message that starts a run, sent from an agent_settled handler",
        move |_ctx, _args| {
            let once = rw_settled_once.clone();
            let subscription = rw_settled_subscriber.on_event("agent_settled", move |ctx, _data| {
                if once.swap(true, Ordering::SeqCst) {
                    return Ok(None);
                }
                ctx.send_message("rw-trigger", "rw-trigger", true, Some(true), None).map_err(|err| err.to_string())?;
                let idle = ctx.is_idle().map_err(|err| err.to_string())?;
                ctx.notify(&format!("rw_idle_in_settled={idle}"), "info");
                Ok(None)
            });
            rw_settled_subscriptions.lock().unwrap().push(subscription);
            CommandResult::Ok
        },
    );
    ext.command(
        "set_model",
        "Switch to the model in the arguments",
        |ctx, args| {
            let (ok, error) = ctx.set_model(args);
            if !error.is_empty() {
                return CommandResult::Error(error);
            }
            ctx.notify(&format!("setModel={ok}"), "info");
            CommandResult::Ok
        },
    );
    let event_probe_on_subscriber = ext.event_subscriber();
    let event_probe_on_subscriptions: Arc<Mutex<Vec<(String, pig_sdk::EventSubscription)>>> = Arc::new(Mutex::new(Vec::new()));
    let event_probe_off_subscriptions = event_probe_on_subscriptions.clone();
    let event_probe_results: Arc<Mutex<std::collections::HashMap<String, Vec<Value>>>> = Arc::new(Mutex::new(std::collections::HashMap::new()));
    let event_probe_result_setter = event_probe_results.clone();
    ext.command(
        "event_probe_result",
        "Queue the JSON result the next event_probe_on handler call of an event returns",
        move |_ctx, args| {
            let (name, raw) = args.trim().split_once(' ').unwrap_or((args.trim(), "null"));
            match serde_json::from_str::<Value>(raw) {
                Ok(value) => {
                    event_probe_result_setter.lock().unwrap().entry(name.to_string()).or_default().push(value);
                    CommandResult::Ok
                }
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    ext.command(
        "event_probe_on",
        "Subscribe to the event named by the argument",
        move |_ctx, args| {
            let name = args.trim().to_string();
            let reported = name.clone();
            let results = event_probe_results.clone();
            let subscription = subscribe(&event_probe_on_subscriber, &name, move |ctx, data| {
                ctx.notify(&format!("event_probe_on:{}", reported), "info");
                // The event as the host sent it, for the tests that compare a payload field with the Go reference's.
                ctx.notify(&format!("event_probe_data:{}:{}", reported, serde_json::to_string(&*data).unwrap()), "info");
                // mcp_servers_change carries every registered server (types.ts:699-709): report their names, which only the host's registry knows.
                if let Some(servers) = data.get("servers").and_then(|servers| servers.as_array()) {
                    let names: Vec<String> = servers.iter().map(|server| server["name"].as_str().unwrap_or("").to_string()).collect();
                    ctx.notify(&format!("event_probe_servers:{}", names.join(",")), "info");
                }
                ctx.notify(&format!("event_payload:{}:{}", reported, serde_json::to_string(&*data).unwrap()), "info");
                let mut queued = results.lock().unwrap();
                Ok(queued.get_mut(&reported).filter(|values| !values.is_empty()).map(|values| values.remove(0)))
            });
            event_probe_on_subscriptions.lock().unwrap().push((name, subscription));
            CommandResult::Ok
        },
    );
    ext.command(
        "event_probe_off",
        "Unsubscribe the event_probe_on handlers of the event named by the argument",
        move |_ctx, args| {
            let name = args.trim();
            let removed: Vec<(String, pig_sdk::EventSubscription)> = {
                let mut subscriptions = event_probe_off_subscriptions.lock().unwrap();
                let (removed, kept) = subscriptions.drain(..).partition(|(event, _)| event == name);
                *subscriptions = kept;
                removed
            };
            for (_, subscription) in removed {
                subscription.unsubscribe();
            }
            CommandResult::Ok
        },
    );
    let event_probe_result_subscriber = ext.event_subscriber();
    let event_probe_result_subscriptions: Arc<Mutex<Vec<pig_sdk::EventSubscription>>> = Arc::new(Mutex::new(Vec::new()));
    ext.command(
        "event_result_probe",
        "Subscribe to an event with a handler that returns the given JSON",
        move |_ctx, args| {
            let trimmed = args.trim();
            let (name, raw) = trimmed.split_once(' ').unwrap_or((trimmed, "null"));
            let result: serde_json::Value = match serde_json::from_str(raw) {
                Ok(value) => value,
                Err(err) => return CommandResult::Error(err.to_string()),
            };
            let subscription = subscribe(&event_probe_result_subscriber, name, move |_ctx, _data| Ok(Some(result.clone())));
            event_probe_result_subscriptions.lock().unwrap().push(subscription);
            CommandResult::Ok
        },
    );
    ext.command(
        "label_probe",
        "Label the entry named first",
        |ctx, args| {
            let trimmed = args.trim();
            let (id, label) = trimmed.split_once(' ').unwrap_or((trimmed, ""));
            match ctx.set_label(id, label) {
                Ok(_) => CommandResult::Ok,
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    let tool_result_guards_subscriber = ext.event_subscriber();
    let tool_result_guards_subscriptions: Arc<Mutex<Vec<pig_sdk::EventSubscription>>> = Arc::new(Mutex::new(Vec::new()));
    ext.command(
        "tool_result_guards_probe",
        "Report the tool_result guards that accept each tool_result event",
        move |_ctx, _args| {
            let subscription = tool_result_guards_subscriber.on_event("tool_result", move |ctx, data| {
                let guards: [(&str, fn(&Value) -> bool); 8] = [
                    ("bash", pig_sdk::is_bash_tool_result),
                    ("edit", pig_sdk::is_edit_tool_result),
                    ("find", pig_sdk::is_find_tool_result),
                    ("grep", pig_sdk::is_grep_tool_result),
                    ("ls", pig_sdk::is_ls_tool_result),
                    ("powershell", pig_sdk::is_powershell_tool_result),
                    ("read", pig_sdk::is_read_tool_result),
                    ("write", pig_sdk::is_write_tool_result),
                ];
                let matched: Vec<&str> = guards.iter().filter(|(_, guard)| guard(&*data)).map(|(name, _)| *name).collect();
                ctx.notify(&format!("tool_result_guards:{}:end", matched.join(",")), "info");
                Ok(None)
            });
            tool_result_guards_subscriptions.lock().unwrap().push(subscription);
            CommandResult::Ok
        },
    );
    // tool_call_guard_probe reports which tool names isToolCallEventType accepts for each tool_call event it receives (TestConformance_ToolCallGuardAgreesAcrossSDKs).
    let tool_call_guard_subscriber = ext.event_subscriber();
    let tool_call_guard_subscriptions: Arc<Mutex<Vec<pig_sdk::EventSubscription>>> = Arc::new(Mutex::new(Vec::new()));
    ext.command("tool_call_guard_probe", "Report the tool names the tool_call guard accepts", move |_ctx, _args| {
        let subscription = tool_call_guard_subscriber.on_event("tool_call", move |ctx, data| {
            let accepted: Vec<&str> = ["bash", "read", "probe", "other"].into_iter().filter(|name| pig_sdk::is_tool_call_event_type(name, &*data)).collect();
            ctx.notify(&format!("tool_call_guard:{}:end", accepted.join(",")), "info");
            Ok(None)
        });
        tool_call_guard_subscriptions.lock().unwrap().push(subscription);
        CommandResult::Ok
    });
    let event_payload_subscriber = ext.event_subscriber();
    let event_payload_subscriptions: Arc<Mutex<Vec<pig_sdk::EventSubscription>>> = Arc::new(Mutex::new(Vec::new()));
    ext.command(
        "event_payload_probe",
        "Subscribe and report the payload of the event named by the argument",
        move |_ctx, args| {
            let name = args.trim().to_string();
            let reported = name.clone();
            let subscription = subscribe(&event_payload_subscriber, &name, move |ctx, data| {
                ctx.notify(&format!("event_payload:{}:{}", reported, serde_json::to_string(&*data).unwrap()), "info");
                Ok(None)
            });
            event_payload_subscriptions.lock().unwrap().push(subscription);
            CommandResult::Ok
        },
    );
    ext.command(
        "set_label",
        "Set an entry label",
        |ctx, _args| match ctx.set_label("label-entry", "conformance-label") {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.shortcut("ctrl+alt+y", "Conformance shortcut", |_ctx| CommandResult::Ok);
    ext.command(
        "append_entry",
        "Append a custom entry",
        |ctx, _args| match ctx.append_entry("conformance-entry", json!("hello-entry")) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        },
    );
    ext.command(
        "append-entries-probe",
        "Append a custom entry for each comma-separated type",
        |ctx, args| {
            for custom_type in args.split(',') {
                // Pi's appendEntry returns nothing and never fails, so a refusal the SDK reports is dropped here; the row reads the session afterwards.
                let _ = ctx.append_entry(custom_type, json!(format!("data-of-{custom_type}")));
            }
            CommandResult::Ok
        },
    );
    ext.command(
        "sprite-probe",
        "Exercise sprite registration and host errors",
        |ctx, _args| {
            let mut definition = conformance_sprite_definition();
            if let Err(err) = ctx.register_sprite(&definition) {
                return CommandResult::Error(err.to_string());
            }
            definition.mascot[0].pop();
            match ctx.register_sprite(&definition) {
                Ok(()) => CommandResult::Error("invalid sprite definition was accepted".to_string()),
                Err(err) => {
                    ctx.notify(&err.to_string(), "error");
                    CommandResult::Ok
                }
            }
        },
    );
    ext.command(
        "login-probe",
        "Exercise semantic login submission and host errors",
        |ctx, _args| {
            let mut definition = conformance_login_definition();
            if let Err(err) = ctx.set_login(&definition) {
                return CommandResult::Error(err.to_string());
            }
            definition.brand[0].pop();
            match ctx.set_login(&definition) {
                Ok(()) => CommandResult::Error("invalid login definition was accepted".to_string()),
                Err(err) => {
                    ctx.notify(&err.to_string(), "error");
                    CommandResult::Ok
                }
            }
        },
    );
    ext.command("scoped-models-probe", "Report the model scope", |ctx, _args| {
        match ctx.scoped_models() {
            Ok(models) => { ctx.notify(&serde_json::to_string(&models).unwrap(), "info"); CommandResult::Ok }
            Err(error) => CommandResult::Error(error.to_string()),
        }
    });
    ext.command("ui-availability", "Probe bound UI and headless defaults", |ctx, _args| {
        let result = (|| -> std::io::Result<()> {
            let (selected, _) = ctx.select("Pick", &["first", "second"])?;
            let selected = dialog_text(&selected);
            ctx.notify("availability-notify", "info");
            if !ctx.has_ui() {
                assert_eq!(ctx.input("Input", "placeholder")?, (serde_json::Value::Null, false));
                assert_eq!(ctx.editor("Editor", "prefill")?, (serde_json::Value::Null, false));
                assert_eq!(ctx.confirm("Confirm", "message")?, json!(false));
                struct NoUIComponent;
                impl pig_sdk::RemoteComponent for NoUIComponent {
                    fn render(&self, _: u32) -> Vec<String> { panic!("headless render") }
                    fn handle_input(&mut self, _: &pig_sdk::JsString) -> Result<pig_sdk::RemoteComponentResult, String> { panic!("headless input") }
                    fn dispose(&mut self) { panic!("headless disposal") }
                }
                assert!(ctx.custom_component(NoUIComponent, json!({}))?.is_none());
                let _off = ctx.on_terminal_input(|_| panic!("headless terminal"))?;
                ctx.add_autocomplete_provider(std::sync::Arc::new(|_, _| panic!("headless autocomplete factory")))?;
                ctx.set_editor_text("ignored");
                ctx.set_tools_expanded(true);
                assert_eq!(ctx.get_editor_text()?, "");
                assert!(!ctx.get_tools_expanded()?);
                assert!(ctx.get_all_themes()?.is_empty());
                assert!(ctx.get_theme("dark")?.is_none());
                assert_eq!(ctx.set_theme("dark"), (false, "UI not available".to_string()));
            }
            ctx.append_entry("ui-availability", json!(format!("hasUI={} selected={selected}", ctx.has_ui())))?;
            Ok(())
        })();
        match result { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err.to_string()) }
    });
    ext.command("ui-state-barrier", "Read UI state after a dialog", |ctx, _args| {
        let result = (|| -> std::io::Result<()> {
            let (selected, _) = ctx.select("expand", &["chosen"])?;
            let selected = dialog_text(&selected);
            let named = ctx.get_theme("light")?.unwrap();
            let missing = ctx.get_theme("missing")?;
            let (success, message) = ctx.set_theme("missing");
            ctx.notify(&format!("selected={selected} expanded={} named={} missing={} success={success} error={message}", ctx.get_tools_expanded()?, named["name"].as_str().unwrap(), missing.is_none()), "info");
            Ok(())
        })();
        match result { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err.to_string()) }
    });
    ext.command("autocomplete-register", "Register retained provider wrappers", |ctx, _| {
        let result = (|| -> std::io::Result<()> {
            for tag in ["A", "B"] {
                ctx.add_autocomplete_provider(std::sync::Arc::new(move |ctx, current| {
                    ctx.notify(&format!("factory:{tag}"), "info");
                    let calls = std::sync::atomic::AtomicUsize::new(0);
                    let get = current.clone();
                    let apply = current.clone();
                    Ok(std::sync::Arc::new(pig_sdk::AutocompleteProvider {
                        trigger_characters: if tag == "A" { vec!["$".into()] } else { vec!["#".into(), "$".into()] },
                        get_suggestions: std::sync::Arc::new(move |ctx, lines, line, col, force| {
                            let count = calls.fetch_add(1, std::sync::atomic::Ordering::SeqCst) + 1;
                            let Some(mut result) = (get.get_suggestions)(ctx, lines, line, col, force)? else { return Ok(None); };
                            if tag == "A" {
                                result.items.retain(|item| item.value != "drop");
                                for item in &mut result.items { item.label = Some(format!("{}:{count}", item.value)); }
                            } else { result.items.push(pig_sdk::AutocompleteItem { value: "tail".into(), label: Some(format!("tail:{count}")), description: None }); }
                            Ok(Some(result))
                        }),
                        apply_completion: std::sync::Arc::new(move |ctx, lines, line, col, item, prefix| {
                            let mut result = (apply.apply_completion)(ctx, lines, line, col, item, prefix)?;
                            result.lines[result.cursor_line].push_str(&format!("-{tag}"));
                            result.cursor_col += 2;
                            Ok(result)
                        }),
                        should_trigger_file_completion: Some(std::sync::Arc::new(move |ctx, lines, line, col| current.should_trigger_file_completion.as_ref().unwrap()(ctx, lines, line, col))),
                    }))
                }))?;
                ctx.notify(&format!("registered:{tag}"), "info");
            }
            Ok(())
        })();
        match result { Ok(()) => CommandResult::Ok, Err(err) => CommandResult::Error(err.to_string()) }
    });
    // Upstream ctx.cwd, ctx.mode, ctx.hasUI and ctx.model throw the stale message after invalidation (runner.ts:571-600).
    ext.command("stale-probe", "Report cwd, mode, hasUI and model", |ctx, _args| {
        ctx.notify(&format!("stale:{}|{}|{}|{}", ctx.cwd(), ctx.mode(), ctx.has_ui(), ctx.model()), "info");
        CommandResult::Ok
    });
    ext.command("signal-probe", "Report ctx.signal", |ctx, _args| {
        let state = match ctx.signal() {
            None => "none",
            Some(signal) if signal.is_cancelled() => "aborted",
            Some(_) => "live",
        };
        ctx.notify(&format!("signal:{state}"), "info");
        CommandResult::Ok
    });
    ext.command("signal-wait", "Wait for ctx.signal to abort", |ctx, _args| {
        let Some(signal) = ctx.signal() else {
            ctx.notify("wait:none", "info");
            return CommandResult::Ok;
        };
        ctx.notify("wait:start", "info");
        let deadline = std::time::Instant::now() + Duration::from_secs(10);
        while !signal.is_cancelled() && std::time::Instant::now() < deadline {
            thread::sleep(Duration::from_millis(5));
        }
        ctx.notify(if signal.is_cancelled() { "wait:aborted" } else { "wait:timeout" }, "info");
        CommandResult::Ok
    });
    ext.command("signal-poll", "Poll ctx.signal until it is live or none", |ctx, args| {
        ctx.notify("poll:start", "info");
        let deadline = std::time::Instant::now() + Duration::from_secs(10);
        while std::time::Instant::now() < deadline {
            if ctx.signal().is_some() == (args == "live") {
                ctx.notify(&format!("poll:{args}"), "info");
                return CommandResult::Ok;
            }
            thread::sleep(Duration::from_millis(5));
        }
        ctx.notify("poll:timeout", "info");
        CommandResult::Ok
    });
    ext.command("usage-probe", "Report context usage", |ctx, _args| {
        let usage = ctx.get_context_usage().unwrap().map(|u| json!({"tokens": u.tokens, "contextWindow": u.context_window, "percent": u.percent}));
        ctx.notify(&serde_json::to_string(&usage).unwrap(), "info");
        CommandResult::Ok
    });
    ext.command(
        "context-probe",
        "Report ctx.mode + ctx.getSystemPromptOptions()",
        |ctx, _args| {
            let opts = ctx.get_system_prompt_options().unwrap();
            let prompt = opts
                .get("customPrompt")
                .and_then(Value::as_str)
                .unwrap_or("");
            let cwd = opts.get("cwd").and_then(Value::as_str).unwrap_or("");
            let tools = opts
                .get("selectedTools")
                .and_then(Value::as_array)
                .map(|a| {
                    a.iter()
                        .filter_map(Value::as_str)
                        .collect::<Vec<_>>()
                        .join(",")
                })
                .unwrap_or_default();
            let shape = |value: Option<&Value>| match value {
                None | Some(Value::Null) => "absent".to_string(),
                Some(Value::Array(items)) => format!("array:{}", items.len()),
                Some(Value::Object(fields)) => format!("object:{}", fields.len()),
                Some(Value::String(text)) => format!("string:{}", text.encode_utf16().count()),
                Some(other) => format!("other:{other}"),
            };
            let shapes = ["selectedTools", "toolSnippets", "toolGuidelines", "promptGuidelines", "appendSystemPrompt", "sections", "contextFiles", "skills"]
                .iter()
                .map(|key| format!("{key}:{}", shape(opts.get(*key))))
                .collect::<Vec<_>>()
                .join(",");
            ctx.notify(
                &format!(
                    "mode={} trusted={} spo_prompt={} spo_cwd={} spo_tools={} spo_shape={} spo_guidelines={} spo_skill_scope={} spo_force_empty={} spo_custom_present={}",
                    ctx.mode(),
                    ctx.is_project_trusted().unwrap(),
                    prompt,
                    cwd,
                    tools,
                    shapes,
                    opts["toolGuidelines"]["read"].as_array().map(|v| v.iter().filter_map(Value::as_str).collect::<Vec<_>>().join(",")).unwrap_or_default(),
                    opts["skills"][0]["sourceInfo"]["scope"].as_str().unwrap_or(""),
                    opts["forceSystemPrompt"].as_str() == Some(""),
                    opts.get("customPrompt").is_some()
                ),
                "info",
            );
            CommandResult::Ok
        },
    );
    // model-info-probe: see the Go fixture.
    ext.command("model-info-probe", "Report the typed active model", |ctx, _args| {
        match ctx.get_model_info() {
            Err(err) => return CommandResult::Error(err.to_string()),
            Ok(None) => ctx.notify("modelinfo:none:end", "info"),
            Ok(Some(m)) => ctx.notify(
                &format!(
                    "modelinfo:{}/{}/{}/{}/{}/{}/{}/{}/{}/{}:end",
                    m.id, m.name, m.provider, m.context_window, m.max_output_tokens, m.reasoning, m.input_cost_per_1m, m.output_cost_per_1m, m.cache_read_cost_per_1m, m.cache_write_cost_per_1m
                ),
                "info",
            ),
        }
        CommandResult::Ok
    });
    // branch-typed-probe: see the Go fixture.
    ext.command("branch-typed-probe", "Report the typed branch entries", |ctx, _args| {
        let branch = match ctx.get_branch() {
            Ok(branch) => branch,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let entries: Vec<String> = branch
            .iter()
            .map(|entry| {
                let calls: Vec<String> = entry.tool_calls.iter().map(|call| format!("{}={}={}", call.name, call.id, call.args)).collect();
                let usage = entry
                    .usage
                    .as_ref()
                    .map(|u| format!("{}/{}/{}/{}/{}", u.input, u.output, u.cache_read, u.cache_write, u.total_tokens))
                    .unwrap_or_else(|| "-".to_string());
                [entry.id.as_str(), &entry.role, &entry.content, &entry.thinking, &entry.provider, &entry.model, &calls.join(","), &usage].join("~")
            })
            .collect();
        ctx.notify(&format!("branchtyped:{}:end", entries.join(" | ")), "info");
        CommandResult::Ok
    });
    ext.command(
        "session-log-probe",
        "Read a paged session log",
        |ctx, _args| {
            ctx.notify(
                &format!(
                    "session entries={} branch={}",
                    ctx.get_entries().unwrap().len(),
                    ctx.get_branch().unwrap().len()
                ),
                "info",
            );
            CommandResult::Ok
        },
    );
    ext.command(
        "session-branch-probe",
        "Read the branch, an entry and its label",
        |ctx, args| {
            let manager = ctx.session_manager();
            let id = |value: serde_json::Value| value.get("id").and_then(|id| id.as_str()).unwrap_or("none").to_string();
            let ids: Vec<String> = ctx.get_branch().unwrap().iter().map(|entry| entry.id.clone()).collect();
            let label = match manager.get_label(args).unwrap() {
                serde_json::Value::String(text) => text,
                _ => "none".to_string(),
            };
            ctx.notify(
                &format!(
                    "branch={} leaf={} entry={} label={}",
                    ids.join(">"),
                    id(manager.get_leaf_entry().unwrap()),
                    id(manager.get_entry(args).unwrap()),
                    label
                ),
                "info",
            );
            CommandResult::Ok
        },
    );
    ext.command(
        "dialog-probe",
        "Exercise interactive dialog responses",
        |ctx, _args| {
            let result = (|| -> std::io::Result<String> {
                let selected = dialog_text(&ctx.select("Pick", &["first", "second"])?.0);
                let input = dialog_text(&ctx.input("Input", "placeholder")?.0);
                let edited = dialog_text(&ctx.editor("Editor", "prefill")?.0);
                let confirmed = ctx.confirm("Confirm", "message")? == json!(true);
                Ok(format!(
                    "select={selected} input={input} editor={edited} confirm={confirmed}"
                ))
            })();
            match result {
                Ok(summary) => {
                    ctx.notify(&summary, "info");
                    CommandResult::Ok
                }
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    ext.command(
        "focused-probe",
        "Exercise focused subprocess UI",
        |ctx, _args| {
            let disposed = Arc::new(AtomicBool::new(false));
            let component = FocusedList {
                items: ["alpha", "beta", "gamma"],
                selected: 0,
                disposed: disposed.clone(),
            };
            match ctx.custom_component(
                component,
                json!({"title": "Focused", "widthFraction": 0.5, "heightFraction": 0.5}),
            ) {
                Ok(value) => {
                    let selected = value
                        .and_then(|v| v.as_str().map(str::to_string))
                        .unwrap_or_default();
                    ctx.notify(
                        &format!(
                            "focused={selected} disposed={}",
                            disposed.load(Ordering::Relaxed)
                        ),
                        "info",
                    );
                    CommandResult::Ok
                }
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    // kit-widget "<case key>" opens a widget kind's Pi vector case in an
    // overlay; the probe notifies its callbacks as they fire.
    ext.command("kit-widget", "Show a widget kind's Pi vector case (D107)", |ctx, key| {
        let Some(view) = kit_widget_case(key) else {
            return CommandResult::Error(format!("kit-widget: unknown case {key:?}"));
        };
        match ctx.custom_view(KitWidgetProbe { ctx: ctx.clone(), view }, json!({"title": "Kit"})) {
            Ok(_) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command("kit-probe", "Exercise the component kit (D107)", |ctx, _args| {
        match ctx.custom_view(KitProbe { log: Vec::new() }, json!({"title": "Kit"})) {
            Ok(result) => {
                let text = result.map(|value| value.as_str().map_or_else(|| value.to_string(), str::to_string)).unwrap_or_default();
                ctx.notify(&format!("kit={text}"), "info");
                CommandResult::Ok
            }
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    // mouse-probe opens the mouse row's component: the host hands it
    // fullscreen mouse events, and it closes on a click with what it got.
    ext.command("mouse-probe", "Exercise extension mouse input", |ctx, _args| {
        match ctx.custom_component(MouseProbe { log: Vec::new() }, json!({"overlay": true})) {
            Ok(result) => {
                let text = result.map(|value| value.as_str().map_or_else(|| value.to_string(), str::to_string)).unwrap_or_default();
                ctx.notify(&format!("mouse={text}"), "info");
                CommandResult::Ok
            }
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    // kit-surfaces shows the kit probe's tree on every other view surface
    // (D107, spec §10): a pushed widget, the header, the footer and a tool
    // result. The message renderer "kit-message" draws it too.
    ext.command("kit-surfaces", "Show the kit probe on every view surface (D107)", |ctx, _args| {
        let steps = || -> std::io::Result<()> {
            ctx.set_widget_view("kit-probe", kit_probe_view(), None)?;
            ctx.set_header_view(kit_probe_view())?;
            ctx.set_footer_view(kit_probe_view())?;
            let mut tool = ToolDefinition::new(
                "kit_view_tool",
                "kit_view_tool",
                "Render its result as the kit probe",
                json!({"type": "object"}),
                |_ctx, _params| ToolResult::text("kit"),
            );
            tool.render_result_view = Some(Box::new(|_, _, _, _, _| Ok(kit_probe_view())));
            ctx.register_tool(tool)
        };
        match steps() {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    // kit-images drives the image transport (D107, spec §7): step "a<k>"
    // shows image 0 and step "b<n>" image n (1 ≤ n ≤ 64), each with its step
    // as the text, as one ui.setWidget call on the "kit-img" widget.
    ext.command("kit-images", "Exercise the component kit's image transport (D107)", |ctx, args| {
        let image = match (args.get(..1), args.get(1..).and_then(|n| n.parse::<u32>().ok())) {
            (Some("a"), Some(1..)) => 0,
            (Some("b"), Some(n @ 1..=64)) => n,
            _ => return CommandResult::Error(format!("kit-images: unknown step {args:?}")),
        };
        let view = kit::View::new(
            Container::new().child(Image::new(kit_image_png(image), "image/png")).child(Text::new(args, 0, 0)),
        );
        match ctx.set_widget_view("kit-img", view, Some(json!({}))) {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    // kit-kinds sets the "kit-kinds" widget to the kinds kit-probe does not
    // draw (D107, spec §10): a box, a settings list, a loader, an image and a
    // lines node whose list annotation goes out only while a frontend draws.
    ext.command("kit-kinds", "Show every other component kit kind (D107)", |ctx, _args| {
        let png: Vec<u8> = (0..KIT_KINDS_PNG.len())
            .step_by(2)
            .map(|i| u8::from_str_radix(&KIT_KINDS_PNG[i..i + 2], 16).unwrap())
            .collect();
        let settings = SettingsList::new(
            "kit-settings",
            vec![
                SettingItem::new("theme", "Theme", "dark").description("Color theme").values(vec!["dark".into(), "light".into()]),
                SettingItem::new("wrap", "Wrap", "on").description("Wrap lines").values(vec!["on".into(), "off".into()]),
            ],
            3,
        );
        let loader = Loader::new("Working")
            .spinner_color("accent")
            .message_color("muted")
            .indicator(LoaderIndicator { frames: Some(vec!["*".into()]), interval_ms: None });
        let item = |label: &str, detail: &str| ListItem { label: label.into(), detail: Some(detail.into()), columns: None };
        let lines = Lines::new(vec!["track one".into(), "track two".into()])
            .list(kit::List { items: vec![item("track one", "A"), item("track two", "B")], selected: Some(1) });
        let stack = VStack::new()
            .child(KitBox::new(1, 0).bg("customMessageBg").child(Text::new("boxed", 0, 0)))
            .child(settings)
            .child(loader)
            .child(Image::new(png, "image/png"))
            .child(lines)
            .gap(1);
        match ctx.set_widget_view("kit-kinds", kit::View::new(stack).theme("accent", "#d75f00"), Some(json!({}))) {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    // kit-conversation sets the "kit-conversation" widget to every
    // conversation kind (D107, spec §2.1, §10); "next" updates the nodes
    // with an id as a Pi author updates kept components.
    ext.command("kit-conversation", "Show Pi's conversation components (D107)", |ctx, args| {
        use pig_sdk::kit::{
            AssistantMessage, BashExecution, ContentBlock, Diff, Message, ToolDefinition, ToolExecution, ToolResult,
            ToolResultContent, UserMessage,
        };
        let next = args == "next";
        let text_result = |text: &str, is_error: bool| ToolResult {
            content: vec![ToolResultContent::text(text)],
            is_error,
            details: None,
            duration_ms: None,
        };
        let user = UserMessage::new("Fix **the** kit build\n\n- one\n- two");
        let mut streaming = AssistantMessage::new(None).id("kit-a1");
        let mut reply = String::from("Done. **Bold** reply\n\n1. a\n2. b");
        if next {
            reply.push_str("\n\nThen more kit.");
        }
        streaming.update_content(
            Message {
                content: vec![ContentBlock::thinking("Reading the *kit* file"), ContentBlock::text(reply)],
                ..Message::default()
            },
            !next,
        );
        let mut failed = AssistantMessage::new(Some(Message {
            content: vec![ContentBlock::thinking("secret"), ContentBlock::text("Visible kit")],
            stop_reason: "error".into(),
            error_message: "kit-boom-7".into(),
        }));
        failed.set_hide_thinking_block(true);
        failed.set_hidden_thinking_label("Pondering kit...");
        failed.set_output_pad(0);
        let card = |id: &str, name: &str, call_id: &str, args: Value| {
            let mut tool = ToolExecution::new(name, call_id, args, "/work/kit");
            if !id.is_empty() {
                tool = tool.id(id);
            }
            tool.set_args_complete();
            tool.mark_execution_started();
            tool
        };
        let mut ls = card("kit-t1", "ls", "call-1", json!({"path": "src"}));
        ls.update_result(text_result("a.go\nb.go", false), false);
        let mut grep = card("kit-t2", "grep", "call-2", json!({"pattern": "TODO"}));
        if next {
            grep.update_result(text_result("x.go:1: TODO kit\ny.go:2: TODO kit", false), false);
        } else {
            grep.update_result(text_result("x.go:1: TODO kit", false), true);
        }
        let mut read = card("", "read", "call-3", json!({"path": "missing.txt"}));
        read.update_result(text_result("ENOENT: kit", true), false);
        read.set_expanded(true);
        let mut custom = card("", "kit_tool", "call-4", json!({"q": "x"})).tool_definition(ToolDefinition::Empty);
        custom.update_result(text_result("answer 42", false), false);
        let mut exited = BashExecution::new("ls -la", false).id("kit-b1");
        exited.append_output("a.txt\n");
        exited.append_output("b.txt");
        exited.set_complete(Some(2), false, false, "");
        exited.set_expanded(next);
        let mut seq = BashExecution::new("seq 25", true);
        seq.append_output(&(1..=25).map(|i| i.to_string()).collect::<Vec<_>>().join("\n"));
        seq.set_complete(Some(0), false, false, "");
        let diff = Diff::new(" 1 keep\n-2 old kit line\n+2 new kit line\n 3 tail").file_path("kit.go");
        let root = Container::new()
            .child(user)
            .child(streaming)
            .child(failed)
            .child(ls)
            .child(grep)
            .child(read)
            .child(custom)
            .child(exited)
            .child(seq)
            .child(diff);
        match ctx.set_widget_view("kit-conversation", kit::View::new(root), Some(json!({}))) {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    // kit-output-pad sets the "kit-output-pad" widget to a bash tool card and
    // a bash command with Pi 1.1.0's outputPad 3 and a final result's
    // durationMs 1234 (F7); "next" calls setOutputPad(2) on both and gives
    // the result durationMs 61500.
    ext.command("kit-output-pad", "Show Pi's outputPad and durationMs (D107)", |ctx, args| {
        use pig_sdk::kit::{BashExecution, ToolExecution, ToolResult, ToolResultContent};
        let next = args == "next";
        let mut tool = ToolExecution::new("bash", "call-p1", json!({"command": "echo kit"}), "/work/kit").id("kit-p1");
        tool.set_output_pad(3);
        tool.set_args_complete();
        tool.mark_execution_started();
        tool.update_result(
            ToolResult {
                content: vec![ToolResultContent::text("kit out")],
                duration_ms: Some(if next { 61500 } else { 1234 }),
                ..ToolResult::default()
            },
            false,
        );
        let mut command = BashExecution::new("echo pad", false).id("kit-p2");
        command.set_output_pad(3);
        command.append_output("padded");
        command.set_complete(Some(0), false, false, "");
        if next {
            tool.set_output_pad(2);
            command.set_output_pad(2);
        }
        match ctx.set_widget_view("kit-output-pad", kit::View::new(Container::new().child(tool).child(command)), Some(json!({}))) {
            Ok(()) => CommandResult::Ok,
            Err(err) => CommandResult::Error(err.to_string()),
        }
    });
    ext.command(
        "overlay-width-probe",
        "Report the width overlay components render at",
        |ctx, _args| {
            let mut widths = Vec::new();
            for options in [json!({"overlay": true}), json!({"overlay": true, "overlayOptions": {"width": "50%"}})] {
                match ctx.custom_component(OverlayWidthProbe::default(), options) {
                    Ok(value) => widths.push(value.and_then(|v| v.as_u64()).unwrap_or(0)),
                    Err(err) => return CommandResult::Error(err.to_string()),
                }
            }
            ctx.notify(&format!("overlay-width default={} percent={}", widths[0], widths[1]), "info");
            CommandResult::Ok
        },
    );
    ext.command(
        "timer-focused-probe",
        "Exercise timer-driven focused UI",
        |ctx, _args| {
            let (component, disposed, detached) = TimerFocused::new();
            match ctx.custom_component(component, json!({"title": "Timer"})) {
                Ok(value) => {
                    let frame = value.and_then(|v| v.as_u64()).unwrap_or(0);
                    ctx.notify(
                        &format!(
                            "timer={frame} disposed={} detached={}",
                            disposed.load(Ordering::Acquire),
                            detached.load(Ordering::Acquire),
                        ),
                        "info",
                    );
                    CommandResult::Ok
                }
                Err(err) => CommandResult::Error(err.to_string()),
            }
        },
    );
    // Typed tool events (upstream PowerShellToolCallEvent/BashToolCallEvent and
    // their result variants): record the input and details, block the
    // conformance sentinel command, and replace a result's content.
    ext.on_event("user_bash", |ctx, data| {
        let mut value =
            json!({"result":{"output":"handled","exitCode":7,"cancelled":false,"truncated":false}});
        match data["command"].as_str().unwrap_or("") {
            "valid" => {}
            "undefined" => value["result"]["exitCode"] = Value::Null,
            "undefined-path" => value["result"]["fullOutputPath"] = Value::Null,
            "missing" => {
                value["result"].as_object_mut().unwrap().remove("exitCode");
            }
            "invalid" => value["result"]["exitCode"] = json!("invalid"),
            "null-operations" => value["operations"] = Value::Null,
            "operations" => return Some(ctx.bash_operations(conformance_bash_operations())),
            _ => return None,
        }
        Some(value)
    });

    ext.on_event("tool_call", |ctx, data| {
        let name = data["toolName"].as_str().unwrap_or_default().to_string();
        let name = name.as_str();
        // Pi's handler mutates event.input in place and the runner reads it back (runner.ts emitToolCall), so the rewrite is the handler's own edit to the event.
        // Moving a member to the end is an edit in Pi, and two added members follow in the order the handler added them. With serde_json's preserve_order, `shift_remove` is JavaScript's `delete`; `remove` swaps the last member into the gap.
        if name == "rewrite_order_probe" {
            let order = data["input"].as_object_mut().unwrap();
            if order.get("command") == Some(&json!("reorder")) {
                let timeout = order.shift_remove("timeout").unwrap();
                order.insert("timeout".to_string(), timeout);
            } else {
                order.insert("zeta".to_string(), json!(1));
                order.insert("alpha".to_string(), json!(2));
            }
            return None;
        }
        if name == "rewrite_probe" || name == "rewrite_block_probe" {
            if data["input"]["command"] == "git status" || name == "rewrite_block_probe" {
                data["input"]["command"] = json!("git status --short");
                data["input"].as_object_mut().unwrap().shift_remove("drop");
                data["input"]["added"] = json!(true);
                data["input"]["nested"] = json!({"depth": 2});
            }
            if name == "rewrite_block_probe" {
                return Some(json!({"block": true, "reason": "blocked after rewrite"}));
            }
            return None;
        }
        if name != "powershell" && name != "bash" {
            return None;
        }
        let command = data["input"]["command"].as_str().unwrap_or_default();
        ctx.notify(
            &format!(
                "tool-call={}:{}:{}",
                name,
                command,
                data["input"]["timeout"].as_u64().unwrap_or_default()
            ),
            "info",
        );
        if command == "blocked-command" {
            return Some(json!({"block": true, "reason": format!("blocked {name}")}));
        }
        None
    });
    ext.on_event("tool_result", |ctx, data| {
        if data["toolName"] == "order_probe" {
            ctx.notify(&format!("order-result={}", data["toolCallId"].as_str().unwrap_or_default()), "info");
            return None;
        }
        let held: Vec<&str> = [
            ("bash", pig_sdk::is_bash_tool_result as fn(&Value) -> bool),
            ("powershell", pig_sdk::is_powershell_tool_result),
            ("read", pig_sdk::is_read_tool_result),
            ("edit", pig_sdk::is_edit_tool_result),
            ("write", pig_sdk::is_write_tool_result),
            ("grep", pig_sdk::is_grep_tool_result),
            ("find", pig_sdk::is_find_tool_result),
            ("ls", pig_sdk::is_ls_tool_result),
        ]
        .into_iter()
        .filter(|(_, is)| is(data))
        .map(|(name, _)| name)
        .collect();
        ctx.notify(&format!("tool-result-guards={}", held.join(",")), "info");
        let name = data["toolName"].as_str().unwrap_or_default();
        if name != "powershell" && name != "bash" {
            return None;
        }
        ctx.notify(
            &format!(
                "tool-result={}:{}:{}:{}",
                name,
                data["details"]["fullOutputPath"]
                    .as_str()
                    .unwrap_or_default(),
                data["details"]["truncation"]["totalLines"]
                    .as_u64()
                    .unwrap_or_default(),
                data["content"][0]["text"].as_str().unwrap_or_default()
            ),
            "info",
        );
        Some(json!({"content": [{"type": "text", "text": format!("{name} redacted")}]}))
    });
    ext.on_event("after_provider_response", |ctx, data| {
        ctx.notify(&format!("provider-response={}:{}:{}", data["type"].as_str().unwrap_or_default(), data["status"], data["headers"]["x-probe"].as_str().unwrap_or_default()), "info");
        Some(json!({"cancel":true}))
    });
    ext.on_event("after_provider_response", |ctx, _| { ctx.notify("provider-response=second", "info"); None });
    ext.on_event("message_update", |ctx, data| {
        let assistant = &data["assistantMessageEvent"];
        ctx.notify(
            &format!(
                "message-update={}:{}:{}:{}",
                assistant["type"].as_str().unwrap_or_default(),
                assistant["contentIndex"].as_u64().unwrap_or_default(),
                assistant["delta"].as_str().unwrap_or_default(),
                assistant.get("assistantMessageEvent").is_some()
            ),
            "info",
        );
        None
    });
    ext.on_event("tool_execution_update", |ctx, data| {
        if data["toolName"] == "production_tool" {
            ctx.notify(
                &format!(
                    "tool-update={}:{}:{}:{}:{}",
                    data["toolName"].as_str().unwrap_or_default(),
                    data["args"]["path"].as_str().unwrap_or_default(),
                    data["args"]["nested"]["depth"].as_u64().unwrap_or_default(),
                    data["partialResult"]["content"][0]["text"]
                        .as_str()
                        .unwrap_or_default(),
                    data["partialResult"]["details"]["progress"]
                        .as_u64()
                        .unwrap_or_default()
                ),
                "info",
            );
            return None;
        }
        ctx.notify(
            &format!(
                "tool-update={}:{}:{}:{}",
                data["toolName"].as_str().unwrap_or_default(),
                data["args"],
                data["partialResult"]["content"][0]["text"]
                    .as_str()
                    .unwrap_or_default(),
                data["partialResult"]["details"]["progress"]
                    .as_u64()
                    .unwrap_or_default()
            ),
            "info",
        );
        None
    });
    ext.on_event("tool_execution_end", |ctx, data| {
        if data["toolName"] == "production_tool" {
            ctx.notify(
                &format!(
                    "tool-end={}:{}:{}:{}:{}:{}:{}",
                    data["toolName"].as_str().unwrap_or_default(),
                    data["result"]["content"][0]["text"]
                        .as_str()
                        .unwrap_or_default(),
                    data["result"]["content"]
                        .as_array()
                        .map(Vec::len)
                        .unwrap_or_default(),
                    data["result"]["content"][1]["data"]
                        .as_str()
                        .unwrap_or_default(),
                    data["result"]["content"][1]["mimeType"]
                        .as_str()
                        .unwrap_or_default(),
                    data["result"]["details"]["nested"]["value"]
                        .as_str()
                        .unwrap_or_default(),
                    data["isError"].as_bool().unwrap_or_default()
                ),
                "info",
            );
            return None;
        }
        ctx.notify(
            &format!(
                "tool-end={}:{}:{}:{}:{}",
                data["toolName"].as_str().unwrap_or_default(),
                data["result"]["content"]
                    .as_array()
                    .map(Vec::len)
                    .unwrap_or_default(),
                data["result"]["content"][1]["data"]
                    .as_str()
                    .unwrap_or_default(),
                data["result"]["details"]["nested"]["value"]
                    .as_str()
                    .unwrap_or_default(),
                data["isError"].as_bool().unwrap_or_default()
            ),
            "info",
        );
        None
    });
    ext.on_project_trust(|_, _| Err("trust-boom".to_string()));
    ext.on_project_trust(|_, _| {
        Ok(ProjectTrustResult {
            trusted: ProjectTrustDecision::Undecided,
            remember: None,
        })
    });
    ext.on_project_trust(|_, data| {
        if data["cwd"] == json!("/probe") {
            return Ok(ProjectTrustResult {
                trusted: ProjectTrustDecision::Undecided,
                remember: None,
            });
        }
        Ok(ProjectTrustResult {
            trusted: ProjectTrustDecision::Yes,
            remember: Some(true),
        })
    });
    ext.on_event("cache_warming_decision", |_, data| {
        if data["warmCost"] != json!(0.05) || data["missCost"] != json!(0.5) || data["continuationProbability"] != json!(0.15) || data["action"] != json!("warm") {
            return Some(json!({"error": "unexpected cache decision"}));
        }
        Some(json!({"action": "stop"}))
    });
    ext.on_event("agent_before_settle", |ctx, mut data| {
        let outcome = data.get("outcome").and_then(Value::as_str).unwrap_or("");
        let entries = data.get("entries").and_then(Value::as_array).map_or(0, Vec::len);
        let continuation = data.get("continue").and_then(Value::as_bool).unwrap_or(false);
        let preview = data.get("context").and_then(Value::as_object);
        let context_entries = preview
            .and_then(|value| value.get("contextEntries"))
            .and_then(Value::as_array)
            .map_or(0, Vec::len);
        let can_continue = preview
            .and_then(|value| value.get("canContinue"))
            .and_then(Value::as_bool)
            .unwrap_or(false);
        ctx.notify(
            &format!("agent_before_settle:{outcome}:{entries}:{continuation}:{context_entries}:{can_continue}"),
            "info",
        );
        data["entries"].as_array_mut().unwrap().push(json!({"type": "custom", "customType": "kept"}));
        None
    });
    ext.on_event("agent_before_settle", |_, mut data| {
        data["entries"].as_array_mut().unwrap().push(json!({"type": "custom", "customType": "before-error"}));
        panic!("boundary failed");
    });
    ext.on_event("agent_before_settle", |_, data| {
        assert_eq!(data["entries"].as_array().unwrap().len(), 2);
        assert_eq!(data["entries"][0]["customType"], "kept");
        assert_eq!(data["entries"][1]["customType"], "before-error");
        assert_eq!(data["context"]["contextEntries"].as_array().unwrap().len(), 2);
        Some(json!({
            "entries": [{"type": "custom", "customType": "conformance-boundary"}],
            "continue": true
        }))
    });
    ext.on_event("session_start", |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        ctx.notify(&format!("session_start:{reason}"), "info");
        if let Some(path) = data.get("previousSessionFile").and_then(Value::as_str).filter(|s| !s.is_empty()) {
            ctx.notify(&format!("previous:{path}"), "info");
        }
        None
    });
    ext.on_event("session_shutdown", |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        ctx.notify(&format!("session_shutdown:{reason}"), "info");
        if let Some(path) = data.get("targetSessionFile").and_then(Value::as_str).filter(|s| !s.is_empty()) {
            ctx.notify(&format!("target:{path}"), "info");
        }
        None
    });
    ext.on_event("session_info_changed", |ctx, data| {
        let name = data.get("name").and_then(Value::as_str).unwrap_or("");
        ctx.notify(&format!("session_info_changed:{name}"), "info");
        None
    });
    ext.on_event("session_before_compact", |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        let will_retry = data
            .get("willRetry")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        ctx.notify(
            &format!("session_before_compact:{reason}:{will_retry}"),
            "info",
        );
        None
    });
    ext.on_event("session_compact", |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        let will_retry = data
            .get("willRetry")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        let from_extension = data
            .get("fromExtension")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        ctx.notify(
            &format!("session_compact:{reason}:{will_retry}:{from_extension}"),
            "info",
        );
        None
    });
    ext.on_event("session_compact_failed", |ctx, data| {
        let reason = data.get("reason").and_then(Value::as_str).unwrap_or("");
        let error = data
            .get("errorMessage")
            .and_then(Value::as_str)
            .unwrap_or("");
        let aborted = data
            .get("aborted")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        let will_retry = data
            .get("willRetry")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        let from_extension = data
            .get("fromExtension")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        ctx.notify(
            &format!("session_compact_failed:{reason}:{error}:{aborted}:{will_retry}:{from_extension}"),
            "info",
        );
        None
    });
    ext.on_event("turn_end", |_, data| {
        if data["messageEntryId"] != "boundary-assistant" { return None; }
        data["entries"].as_array_mut().unwrap().push(json!({"type":"custom", "customType":"mutated"}));
        panic!("turn-boundary-failure");
    });
    ext.on_event("turn_end", |_, data| {
        if data["messageEntryId"] != "boundary-assistant" { return None; }
        Some(json!({"entries":[{"type":"custom", "customType":"turn-boundary", "data": data}], "continue":true}))
    });
    ext.on_event("turn_end", |ctx, data| {
        let message_id = data.get("messageEntryId").and_then(Value::as_str).unwrap_or("");
        let tool_id = data
            .get("toolResultEntryIds")
            .and_then(Value::as_array)
            .and_then(|ids| ids.first())
            .and_then(Value::as_str)
            .unwrap_or("");
        ctx.notify(&format!("turn_end:{message_id}:{tool_id}"), "info");
        None
    });
    let prompt_sequence = Arc::new(Mutex::new(0));
    for name in ["ui_prompt_start", "ui_prompt_end"] {
        let prompt_sequence = prompt_sequence.clone();
        ext.on_event(name, move |ctx, data| {
            let sequence = { let mut counter = prompt_sequence.lock().unwrap(); let n = *counter; *counter += 1; n };
            let field = |key: &str| data.get(key).and_then(Value::as_str).unwrap_or("").to_string();
            let title = data.get("title").and_then(Value::as_str).unwrap_or("(none)");
            if title.starts_with("fifo:") {
                ctx.notify(&format!("fifo:{sequence}:{}:{title}", field("type")), "info");
                return None;
            }
            ctx.notify(
                &format!("ui_prompt:{}:{}:{}:{title}", field("type"), field("reason"), field("kind")),
                "info",
            );
            None
        });
    }
    register_conformance_oauth(&mut ext);
    register_conformance_oauth_object(&mut ext);
    register_conformance_oauth_large(&mut ext);
    register_state_push_probe(&mut ext);
    register_signal_abort(&mut ext);
    ext.run().unwrap();
}

/// How long a state-push-probe reader waits for the value it armed on to change before it reports the value it still holds.
const STATE_PUSH_WINDOW: Duration = Duration::from_secs(5);

/// Registers the state-push-probe command (TestHostStateChangeReachesABackgroundReaderAcrossSDKs). The command arms a thread that reads one replicated accessor from outside every request, as an extension's timer does, and returns: the host then changes its own state with no call and no event to this extension, and the thread reports the first value that differs from the one it armed on, or the value it still holds after STATE_PUSH_WINDOW, to a file in the directory the arguments name.
/// The arguments are {"kind": "model" | "session-name" | "theme" | "settings:<key>" | "mcp", "dir": <directory>}; the report is "<armed>=><read>".
fn register_state_push_probe(ext: &mut Extension) {
    ext.command("state-push-probe", "Read one replicated accessor from a thread between requests", |ctx, args| {
        let request: Value = match serde_json::from_str(args) {
            Ok(request) => request,
            Err(err) => return CommandResult::Error(err.to_string()),
        };
        let kind = request["kind"].as_str().unwrap_or("").to_string();
        let dir = std::path::PathBuf::from(request["dir"].as_str().unwrap_or(""));
        let read = match state_push_reader(ctx, &kind) {
            Ok(read) => read,
            Err(err) => return CommandResult::Error(err),
        };
        let armed = read();
        thread::spawn(move || {
            let mut current = armed.clone();
            let deadline = std::time::Instant::now() + STATE_PUSH_WINDOW;
            while current == armed && std::time::Instant::now() < deadline {
                thread::sleep(Duration::from_millis(2));
                current = read();
            }
            let name = format!("state-push-{}", kind.replace(':', "_"));
            let temp = dir.join(format!("{name}.tmp"));
            if std::fs::write(&temp, format!("{armed}=>{current}")).is_ok() {
                let _ = std::fs::rename(&temp, dir.join(name));
            }
        });
        CommandResult::Ok
    });
}

/// The read of one replicated accessor as a string, or an error for a kind the Rust SDK does not replicate: its thinking level is a host call, which is live.
fn state_push_reader(ctx: &pig_sdk::Context, kind: &str) -> Result<Box<dyn Fn() -> String + Send>, String> {
    let ctx = ctx.clone();
    if kind == "model" {
        return Ok(Box::new(move || ctx.model()));
    }
    if kind == "session-name" {
        return Ok(Box::new(move || ctx.session_name()));
    }
    if kind == "theme" {
        return Ok(Box::new(move || ctx.theme().name));
    }
    if let Some(key) = kind.strip_prefix("settings:") {
        let key = key.to_string();
        return Ok(Box::new(move || match ctx.get_settings() {
            Ok(settings) => settings.get(&key).cloned().unwrap_or(Value::Null).to_string(),
            Err(err) => format!("error:{err}"),
        }));
    }
    if kind == "mcp" {
        return Ok(Box::new(move || ctx.get_mcp_servers().iter().map(|server| server.name.clone()).collect::<Vec<_>>().join(",")));
    }
    Err(format!("state-push-probe: unsupported kind {kind:?}"))
}

/// Owns credential persistence for the conformance OAuth provider. Its returned
/// values must match the Go and Python fixtures so the recordings compare equal.
struct ConformanceStore;

impl OAuthCredentialStore for ConformanceStore {
    fn credential_status(&self) -> OAuthCredentialStatus {
        OAuthCredentialStatus {
            present: true,
            auth_type: "oauth".to_string(),
            source: "conformance".to_string(),
        }
    }
    fn store_credentials(&self, creds: OAuthCredentials) -> Result<String, String> {
        if creds.account_id != "account-store" || creds.scope != "scope-store" {
            return Err("credential metadata lost".to_string());
        }
        Ok("/conf/creds.json".to_string())
    }
    fn delete_credentials(&self) -> Result<bool, String> {
        Ok(true)
    }
}

/// Contributes a provider whose callbacks treat credentials as Pi's complete token object: login returns
/// a fractional expiry and provider-owned keys, and refresh spreads its input as `{ ...creds, access,
/// expires }` does. Behavior must match the Go, Python and Node fixtures.
fn register_conformance_oauth_object(ext: &mut Extension) {
    ext.register_oauth_provider(
        "conformance-oauth-object",
        json!({"name": "Conformance OAuth Object"}),
        OAuthProvider {
            name: "Conformance OAuth Object".to_string(),
            login: Box::new(|_cb| {
                let mut creds = OAuthCredentials {
                    access: "object-access".to_string(),
                    refresh: "object-refresh".to_string(),
                    ..Default::default()
                };
                creds.extra.insert("meta".into(), json!({"k": [1, null, ""]}));
                creds.extra.insert("projectId".into(), json!(""));
                creds.set_expires_millis(1_700_000_000_000.25);
                Ok(creds)
            }),
            refresh_token: Some(Box::new(|creds: OAuthCredentials| {
                let mut next = creds.clone();
                next.access = format!("refreshed-{}", creds.refresh);
                next.set_expires_millis(creds.expires_millis().unwrap_or(f64::NAN) + 0.5);
                Ok(next)
            })),
            get_api_key: Some(Box::new(|creds: OAuthCredentials| {
                let typed = if creds.extra.get("type") == Some(&json!("oauth")) { "typed" } else { "untyped" };
                let meta = if creds.extra.contains_key("meta") { "meta" } else { "nometa" };
                format!("key:{typed}:{meta}")
            })),
            is_subscription: false,
            credential_store: None,
        },
    );
}

/// Contributes a provider whose expiry is the double 2**60: JSON.parse reads the wire digits 1152921504606847000 as that double, so get_api_key reports a distance of 0 from 2**60 for the number and for its integer projection. Behavior must match the Go, Python and Node fixtures.
fn register_conformance_oauth_large(ext: &mut Extension) {
    const TWO_60: f64 = 1_152_921_504_606_846_976.0;
    ext.register_oauth_provider(
        "conformance-oauth-large",
        json!({"name": "Conformance OAuth Large"}),
        OAuthProvider {
            name: "Conformance OAuth Large".to_string(),
            login: Box::new(|_cb| {
                let mut creds = OAuthCredentials { access: "large-access".to_string(), refresh: "large-refresh".to_string(), ..Default::default() };
                creds.set_expires_millis(TWO_60);
                Ok(creds)
            }),
            refresh_token: None,
            get_api_key: Some(Box::new(|creds: OAuthCredentials| format!("key:{}:{}", (creds.expires_millis().unwrap_or(f64::NAN) - TWO_60) as i64, creds.expires - (1_i64 << 60)))),
            is_subscription: false,
            credential_store: None,
        },
    );
}

/// Contributes the canonical OAuth provider the cross-transport conformance
/// suite drives. Behavior must match the Go and Python fixtures byte-for-byte.
fn register_conformance_oauth(ext: &mut Extension) {
    ext.register_oauth_provider(
        "conformance-oauth",
        json!({"name": "Conformance OAuth"}),
        OAuthProvider {
            name: "Conformance OAuth".to_string(),
            is_subscription: true,
            login: Box::new(|cb| {
                cb.on_device_code(OAuthDeviceCodeInfo {
                    user_code: "CONF-USER-CODE".to_string(),
                    verification_uri: "https://conf.example/verify".to_string(),
                    ..Default::default()
                });
                cb.on_progress("waiting");
                let value = cb.on_prompt(OAuthPrompt {
                    message: "paste the code".to_string(),
                    ..Default::default()
                })?;
                Ok(OAuthCredentials {
                    access: format!("access-{value}"),
                    refresh: "refresh-tok".to_string(),
                    expires: 4242,
                    account_id: "account-login".to_string(),
                    scope: "scope-login".to_string(),
                    ..Default::default()
                })
            }),
            refresh_token: Some(Box::new(|creds: OAuthCredentials| {
                Ok(OAuthCredentials {
                    access: format!("refreshed-{}", creds.refresh),
                    refresh: creds.refresh,
                    expires: 9999,
                    account_id: creds.account_id,
                    scope: creds.scope,
                    ..Default::default()
                })
            })),
            get_api_key: Some(Box::new(|creds: OAuthCredentials| {
                if creds.access == "boom" {
                    panic!("getApiKey exploded");
                }
                format!("key:{}", creds.access)
            })),
            credential_store: Some(Box::new(ConformanceStore)),
        },
    );
}

/// Swallows "q", rewrites "a" to "A", upper-cases the host's setText, and frames super.render.
struct ConformanceEditor {
    base: EditorBase,
}

impl EditorComponent for ConformanceEditor {
    fn base(&self) -> &EditorBase {
        &self.base
    }
    fn handle_input(&mut self, data: &JsString) -> Result<(), String> {
        if data == "q" {
            return Ok(());
        }
        if data == "L" {
            let lines = self.base.get_lines().map_err(|e| e.to_string())?;
            let mut units: Vec<u16> = "raw:lines=".encode_utf16().collect();
            for (i, line) in lines.iter().enumerate() {
                if i > 0 {
                    units.push(u16::from(b'|'));
                }
                units.extend_from_slice(line.as_units());
            }
            return self.base.set_text(&JsString::from_units(units)).map_err(|e| e.to_string());
        }
        let data = if data == "a" { JsString::from("A") } else { data.clone() };
        self.base.handle_input(&data).map_err(|e| e.to_string())
    }
    fn set_text(&mut self, text: &JsString) -> Result<(), String> {
        if text.to_string_lossy().starts_with("raw:") {
            return self.base.set_text(text).map_err(|e| e.to_string());
        }
        self.base.set_text(&JsString::from(text.to_string_lossy().to_uppercase())).map_err(|e| e.to_string())
    }
    // insert_text_at_cursor wraps the host's text in angle brackets and add_to_history prefixes it, unless it starts with "raw:" (TestEditorComponentOverridesReachSuper).
    fn insert_text_at_cursor(&mut self, text: &JsString) -> Result<(), String> {
        if text.to_string_lossy().starts_with("raw:") {
            return self.base.insert_text_at_cursor(text).map_err(|e| e.to_string());
        }
        self.base.insert_text_at_cursor(&JsString::from(format!("<{}>", text.to_string_lossy()))).map_err(|e| e.to_string())
    }
    fn add_to_history(&mut self, text: &JsString) -> Result<(), String> {
        if text.to_string_lossy().starts_with("raw:") {
            return self.base.add_to_history(text).map_err(|e| e.to_string());
        }
        self.base.add_to_history(&JsString::from(format!("history:{}", text.to_string_lossy()))).map_err(|e| e.to_string())
    }
    fn render(&mut self, width: u32) -> Result<Vec<JsString>, String> {
        let mut rows = vec![JsString::from("[custom editor]")];
        rows.extend(self.base.render(width).map_err(|e| e.to_string())?);
        Ok(rows)
    }
}

/// Opts into the working status in its border.
struct EmbeddingEditor(ConformanceEditor);

impl EditorComponent for EmbeddingEditor {
    fn base(&self) -> &EditorBase {
        self.0.base()
    }
    fn embed_working_status(&self) -> bool {
        true
    }
    fn handle_input(&mut self, data: &JsString) -> Result<(), String> {
        self.0.handle_input(data)
    }
    fn set_text(&mut self, text: &JsString) -> Result<(), String> {
        self.0.set_text(text)
    }
    fn render(&mut self, width: u32) -> Result<Vec<JsString>, String> {
        self.0.render(width)
    }
}

/// A CustomEditor subclass that sets the handlers Pi's CustomEditor keeps as public fields (on_escape, on_ctrl_d, on_paste_image,
/// action_handlers through on_action, on_extension_shortcut), reads and sets the members it inherits from Pi's Editor on its base ("M"),
/// and hears the host's working status and invalidate. Each handler writes what ran into the host's text
/// (TestEditorComponentAppHandlersRunInTheExtension, TestEditorComponentInheritedMembersReachTheHost,
/// TestEditorComponentHearsTheWorkingStatusAndInvalidate).
struct HooksEditor(ConformanceEditor);

fn io_message(err: std::io::Error) -> String {
    err.to_string()
}

impl HooksEditor {
    fn new(base: EditorBase) -> Self {
        let say = |what: &'static str| -> pig_sdk::EditorHook {
            let base = base.clone();
            Arc::new(move || base.set_text(&JsString::from(format!("raw:hook={what}"))).map_err(io_message))
        };
        base.set_on_escape(Some(say("escape")));
        base.set_on_ctrl_d(Some(say("ctrl-d")));
        base.set_on_paste_image(Some(say("paste-image")));
        base.on_action("app.clear", say("clear"));
        // action_handlers is the live map: an entry removed from it is no longer the component's.
        base.on_action("app.unused", say("unused"));
        let _ = base.action_handlers().remove("app.unused");
        let shortcut_base = base.clone();
        base.set_on_extension_shortcut(Some(Arc::new(move |data: &JsString| {
            if data != "S" {
                return Ok(false);
            }
            shortcut_base.set_text(&JsString::from("raw:hook=shortcut")).map_err(io_message)?;
            Ok(true)
        })));
        HooksEditor(ConformanceEditor { base })
    }
}

impl EditorComponent for HooksEditor {
    fn base(&self) -> &EditorBase {
        self.0.base()
    }
    fn handle_input(&mut self, data: &JsString) -> Result<(), String> {
        if data != "M" {
            return self.0.handle_input(data);
        }
        let base = &self.0.base;
        let padding = base.get_padding_x().map_err(io_message)?;
        let visible = base.get_autocomplete_max_visible().map_err(io_message)?;
        let border = base.border_color(&JsString::from("x")).map_err(io_message)?;
        base.set_disable_submit(true).map_err(io_message)?;
        let disabled = base.disable_submit().map_err(io_message)?;
        base.set_focused(true).map_err(io_message)?;
        let focused = base.focused().map_err(io_message)?;
        let provider = pig_sdk::kit::Autocomplete::new([pig_sdk::kit::SlashCommand::new("help").description("Show help")]).base_path("/work");
        base.set_autocomplete_provider(Some(&provider)).map_err(io_message)?;
        base.on_submit(&JsString::from("sub")).map_err(io_message)?;
        base.on_change(&JsString::from("chg")).map_err(io_message)?;
        let text = format!("raw:members=padding={padding},max={visible},border={},disable={disabled},focused={focused}", border.to_string_lossy());
        base.set_text(&JsString::from(text)).map_err(io_message)
    }
    fn set_text(&mut self, text: &JsString) -> Result<(), String> {
        self.0.set_text(text)
    }
    fn render(&mut self, width: u32) -> Result<Vec<JsString>, String> {
        self.0.render(width)
    }
    fn set_working_status_indicator(&mut self, status: Option<&pig_sdk::EditorStatus>) -> Result<(), String> {
        let text = match status {
            None => "raw:status=none".to_string(),
            Some(status) => format!("raw:status={}/{}/{}", status.kind, status.message, status.frame),
        };
        self.0.base.set_text(&JsString::from(text)).map_err(io_message)
    }
    fn invalidate(&mut self) -> Result<(), String> {
        self.0.base.set_text(&JsString::from("raw:invalidated")).map_err(io_message)
    }
}

/// Renders the width the SDK draws it at and returns that width on Enter.
#[derive(Default)]
struct OverlayWidthProbe {
    width: std::sync::atomic::AtomicU32,
}

impl pig_sdk::RemoteComponent for OverlayWidthProbe {
    fn render(&self, width: u32) -> Vec<String> {
        self.width.store(width, std::sync::atomic::Ordering::Release);
        vec![format!("overlay width={width}")]
    }

    fn handle_input(&mut self, data: &pig_sdk::JsString) -> Result<pig_sdk::RemoteComponentResult, String> {
        if *data == "\r" {
            return Ok(pig_sdk::RemoteComponentResult::done(Some(json!(self.width.load(std::sync::atomic::Ordering::Acquire)))));
        }
        Ok(pig_sdk::RemoteComponentResult::pending())
    }
}

/// The BashOperations every SDK fixture returns for the user_bash command "operations" (TestConformance_UserBashOperationsRunInTheExtension).
fn conformance_bash_operations() -> pig_sdk::BashOperations {
    pig_sdk::BashOperations {
        exec: std::sync::Arc::new(|command, cwd, options| match command {
            "echo" => {
                (options.on_data)(format!("cmd:{command}\n").as_bytes());
                (options.on_data)(format!("cwd:{cwd}\n").as_bytes());
                if options.env.as_ref().is_some_and(|env| env.is_empty()) {
                    (options.on_data)(b"env-empty\n");
                }
                let mut names: Vec<&String> = options.env.iter().flat_map(|env| env.keys()).collect();
                names.sort();
                for name in names {
                    (options.on_data)(format!("env:{name}={}\n", options.env.as_ref().unwrap()[name]).as_bytes());
                }
                if let Some(timeout) = options.timeout {
                    (options.on_data)(format!("timeout:{timeout}\n").as_bytes());
                }
                Ok(Some(3))
            }
            "chunks" => {
                for chunk in ["a", "b", "c"] {
                    (options.on_data)(chunk.as_bytes());
                }
                Ok(None)
            }
            "binary" => {
                (options.on_data)(&[0xff, 0x00, 0x80]);
                Ok(Some(0))
            }
            "wait" => {
                (options.on_data)(b"waiting");
                options.signal.wait();
                (options.on_data)(b"stopped");
                Err("aborted".to_string())
            }
            other => Err(format!("exec failed: {other}")),
        }),
    }
}

fn pi_ai_helpers_call(name: &str, args: &[Value]) -> Result<Option<Value>, String> {
    let a = |i: usize| &args[i];
    let messages = |i: usize| a(i).as_array().cloned().unwrap_or_default();
    let flag = |i: usize| a(i).as_bool().unwrap_or_default();
    Ok(Some(match name {
        "StringEnum" => {
            let values: Vec<&str> = a(0).as_array().unwrap().iter().filter_map(Value::as_str).collect();
            let options = args.get(1).map(|o| pig_sdk::StringEnumOptions { description: o.get("description").and_then(Value::as_str).map(String::from), default: o.get("default").and_then(Value::as_str).map(String::from) });
            pig_sdk::string_enum(&values, options.as_ref())
        }
        "getSupportedThinkingLevels" => json!(pig_sdk::get_supported_thinking_levels(a(0))),
        "clampThinkingLevel" => json!(pig_sdk::clamp_thinking_level(a(0), a(1).as_str().unwrap_or_default())),
        "calculateCost" => pig_sdk::calculate_cost(a(0), &mut a(1).clone())?,
        "contentText" => json!(pig_sdk::content_text(Some(a(0)), args.get(1).and_then(Value::as_str).unwrap_or("\n"))?),
        "getCurrentTools" => json!(pig_sdk::get_current_tools(&messages(0))),
        "getCurrentSystemMessage" => return pig_sdk::get_current_system_message(&messages(0)),
        "getCurrentSystemPrompt" => json!(pig_sdk::get_current_system_prompt(&messages(0))?),
        "getInitialSystemMessage" => return Ok(pig_sdk::get_initial_system_message(&messages(0)).cloned()),
        "getDeclaredTools" => json!(pig_sdk::get_declared_tools(&messages(0))),
        "hasNonAdditiveToolChanges" => json!(pig_sdk::has_non_additive_tool_changes(&messages(0))),
        "resolveTranscriptTools" => {
            let support = match a(1).as_str().unwrap_or_default() {
                "none" => pig_sdk::TranscriptToolSupport::None,
                "additions" => pig_sdk::TranscriptToolSupport::Additions,
                "changes" => pig_sdk::TranscriptToolSupport::Changes,
                other => return Err(format!("unknown transcript tool support {other:?}")),
            };
            let resolved = pig_sdk::resolve_transcript_tools(&messages(0), support);
            json!({ "requestTools": resolved.request_tools, "foldedSystemMessages": resolved.folded_system_messages })
        }
        "collapseSystemMessages" => pig_sdk::collapse_system_messages(a(0))?,
        "resolveTranscript" => pig_sdk::resolve_transcript(a(0), flag(1))?,
        "withoutInitialSystemMessage" => json!(pig_sdk::without_initial_system_message(&messages(0))),
        "normalizeContext" => pig_sdk::normalize_context(a(0)),
        "createInitialSystemMessage" => {
            let tools = args.get(1).filter(|tools| !tools.is_null()).map(|tools| tools.as_array().cloned().unwrap_or_default());
            return Ok(pig_sdk::create_initial_system_message(a(0).as_str(), tools.as_deref()));
        }
        "getSystemMessageText" => json!(pig_sdk::get_system_message_text(a(0))?),
        "renderSystemMessageUpdate" => json!(pig_sdk::render_system_message_update(a(0))?),
        "getModelType" => json!(pig_sdk::get_model_type(a(0))),
        "isModelType" => json!(pig_sdk::is_model_type(a(0), a(1).as_str().unwrap_or_default())),
        "hasApi" => json!(pig_sdk::has_api(a(0), a(1).as_str().unwrap_or_default())),
        "modelsAreEqual" => json!(pig_sdk::models_are_equal(Some(a(0)).filter(|first| !first.is_null()), Some(a(1)).filter(|other| !other.is_null()))),
        "isRetryableAssistantError" => json!(pig_sdk::is_retryable_assistant_error(a(0))),
        "isContextOverflow" => json!(pig_sdk::is_context_overflow(a(0), args.get(1).and_then(Value::as_f64))),
        "isRecoverableLength" => json!(pig_sdk::is_recoverable_length(a(0), a(1).as_f64().unwrap_or_default())),
        "retryDelayMs" => {
            let policy = pig_sdk::RetryPolicy { base_delay_ms: a(0)["baseDelayMs"].as_f64().unwrap_or_default(), max_agent_delay_ms: a(0).get("maxAgentDelayMs").and_then(Value::as_f64) };
            json!(pig_sdk::retry_delay_ms(&policy, a(1).as_i64().unwrap_or_default()))
        }
        "DEFAULT_MAX_AGENT_RETRY_DELAY_MS" => json!(pig_sdk::DEFAULT_MAX_AGENT_RETRY_DELAY_MS),
        other => return Err(format!("no Rust counterpart for {other}")),
    }))
}

fn pi_ai_helpers_probe(path: &str) -> Result<Value, String> {
    let golden: Value = serde_json::from_str(&std::fs::read_to_string(path).map_err(|e| e.to_string())?).map_err(|e| e.to_string())?;
    let mut cases = Vec::new();
    for case in golden["cases"].as_array().ok_or("no cases")? {
        let name = case["fn"].as_str().unwrap_or_default();
        let value = pi_ai_helpers_call(name, case["args"].as_array().unwrap_or(&Vec::new())).map_err(|e| format!("{name}: {e}"))?;
        cases.push(Value::Array(value.into_iter().collect()));
    }
    let mut auth = Vec::new();
    for case in golden["auth"]["cases"].as_array().ok_or("no auth cases")? {
        let vars: Vec<&str> = case["envVars"].as_array().unwrap().iter().filter_map(Value::as_str).collect();
        let env = case["env"].clone();
        let seen = Arc::new(Mutex::new(Vec::new()));
        let seen_in = seen.clone();
        let input = pig_sdk::APIKeyAuthInput {
            ctx: pig_sdk::AuthContext {
                env: Arc::new(move |name| {
                    seen_in.lock().unwrap().push(name.clone());
                    Ok(env.get(&name).and_then(Value::as_str).map(String::from))
                }),
                file_exists: Arc::new(|_| Ok(false)),
            },
            credential: case.get("credential").filter(|c| !c.is_null()).cloned(),
            signal: pig_sdk::ProviderSignal::default(),
        };
        let result = (pig_sdk::env_api_key_auth(case["name"].as_str().unwrap_or_default(), &vars).resolve)(input)?;
        auth.push(json!({ "result": result.map(|r| serde_json::to_value(r).unwrap()), "seen": *seen.lock().unwrap() }));
    }
    let prompts = Arc::new(Mutex::new(Vec::new()));
    let prompts_in = prompts.clone();
    let credential = (pig_sdk::env_api_key_auth("Name", &["FIRST", "SECOND"]).login.unwrap())(pig_sdk::AuthInteraction {
        signal: pig_sdk::ProviderSignal::default(),
        prompt: Arc::new(move |request| {
            prompts_in.lock().unwrap().push(request);
            Ok("typed-key".to_string())
        }),
        notify: Arc::new(|_| Ok(())),
    })?;
    let now = || std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map_or(0, |elapsed| elapsed.as_millis() as u64);
    let mut timing = Vec::new();
    for case in golden["timing"].as_array().ok_or("no timing")? {
        let stream = pig_sdk::create_assistant_message_event_stream();
        let kind = case["kind"].as_str().unwrap_or_default();
        let message = |is_later: bool| {
            let mut message = json!({ "role": "assistant", "content": [], "stopReason": if kind == "error" { "error" } else { "stop" } });
            match case["timestamp"].as_str().unwrap_or_default() {
                _ if is_later => message["timestamp"] = json!(now()),
                "now" => message["timestamp"] = json!(now()),
                "old" => message["timestamp"] = json!(1),
                _ => {}
            }
            if !case["preset"].is_null() && !is_later {
                message["durationMs"] = case["preset"].clone();
            }
            message
        };
        let (final_message, later) = (message(false), message(true));
        let delivered = match kind {
            "done" => {
                stream.push(json!({ "type": "done", "reason": "stop", "message": final_message }));
                stream.next().unwrap()["message"].clone()
            }
            "error" => {
                stream.push(json!({ "type": "error", "reason": "error", "error": final_message }));
                stream.next().unwrap()["error"].clone()
            }
            "end" => {
                stream.end(final_message);
                stream.result().unwrap()
            }
            _ => {
                stream.push(json!({ "type": "done", "reason": "stop", "message": final_message }));
                stream.end(later);
                stream.next().unwrap()["message"].clone()
            }
        };
        let value = delivered.get("durationMs");
        let kept = value.filter(|value| !case["preset"].is_null() && **value == case["preset"]).cloned().unwrap_or(Value::Null);
        timing.push(json!({ "present": value.is_some(), "value": kept, "laterTimed": false }));
    }
    Ok(json!({ "cases": cases, "timing": timing, "auth": auth, "login": { "prompts": *prompts.lock().unwrap(), "result": credential } }))
}

fn host_helpers_probe(ctx: &pig_sdk::Context, args: &str) -> Result<Value, String> {
    let mut parts = args.splitn(3, '|');
    let (golden_path, auth_path, text) = (parts.next().unwrap_or_default(), parts.next().unwrap_or_default(), parts.next().unwrap_or_default());
    let golden: Value = serde_json::from_str(&std::fs::read_to_string(golden_path).map_err(|e| e.to_string())?).map_err(|e| e.to_string())?;
    let io = |e: std::io::Error| e.to_string();
    let mut diffs = Vec::new();
    for case in golden["diffs"].as_array().ok_or("no diffs")? {
        let lines = case.get("contextLines").and_then(Value::as_i64);
        let (old, new) = (case["oldContent"].as_str().unwrap_or_default(), case["newContent"].as_str().unwrap_or_default());
        let mut report = json!({"patch": ctx.generate_unified_patch(case.get("path").and_then(Value::as_str).unwrap_or("src/a.ts"), old, new, lines).map_err(io)?});
        if !case.get("patchOnly").and_then(Value::as_bool).unwrap_or(false) {
            let diff = ctx.generate_diff_string(old, new, lines).map_err(io)?;
            report["diff"] = json!(diff.diff);
            report["firstChangedLine"] = json!(diff.first_changed_line);
        }
        diffs.push(report);
    }
    let mut entries = Vec::new();
    for case in golden["entries"].as_array().ok_or("no entries")? {
        entries.push(json!(pig_sdk::session_entry_to_context_messages(&case["entry"]).map_err(io)?));
    }
    let mut images = Vec::new();
    for case in golden["images"].as_array().ok_or("no images")? {
        let (data, mime) = (case["data"].as_str().unwrap_or_default(), case["mimeType"].as_str().unwrap_or_default());
        if case["op"] == "convert" {
            images.push(match ctx.convert_to_png(data, mime).map_err(io)? {
                Some(converted) => json!({"mimeType": converted.mime_type, "decodes": !converted.data.is_empty()}),
                None => Value::Null,
            });
            continue;
        }
        let options = case.get("options").filter(|o| o.is_object()).map(|o| pig_sdk::ImageResizeOptions {
            max_width: o.get("maxWidth").and_then(Value::as_u64).map(|v| v as u32),
            max_height: o.get("maxHeight").and_then(Value::as_u64).map(|v| v as u32),
            max_bytes: o.get("maxBytes").and_then(Value::as_u64),
            jpeg_quality: o.get("jpegQuality").and_then(Value::as_u64).map(|v| v as u32),
        });
        let input = decode_base64(data);
        images.push(match ctx.resize_image(&input, mime, options.as_ref()).map_err(io)? {
            Some(resized) => json!({"mimeType": resized.mime_type, "originalWidth": resized.original_width, "originalHeight": resized.original_height, "width": resized.width, "height": resized.height, "wasResized": resized.was_resized, "decodes": !resized.data.is_empty()}),
            None => Value::Null,
        });
    }
    let credential = |provider: &str, path: Option<&str>| pig_sdk::read_stored_credential(provider, path).ok().flatten().unwrap_or(Value::Null);
    let creds = json!({
        "explicit": credential("anthropic", Some(auth_path)), "default": credential("anthropic", None), "absentProvider": credential("nobody", Some(auth_path)),
        "missingFile": credential("anthropic", Some(&format!("{auth_path}.missing"))), "notJSON": credential("anthropic", Some(&format!("{auth_path}.bad"))),
    });
    ctx.copy_to_clipboard(text).map_err(io)?;
    Ok(json!({"diffs": diffs, "entries": entries, "images": images, "creds": creds}))
}

fn decode_base64(text: &str) -> Vec<u8> {
    let value = |c: u8| match c {
        b'A'..=b'Z' => Some(c - b'A'),
        b'a'..=b'z' => Some(c - b'a' + 26),
        b'0'..=b'9' => Some(c - b'0' + 52),
        b'+' => Some(62),
        b'/' => Some(63),
        _ => None,
    };
    let (mut out, mut buffer, mut bits) = (Vec::new(), 0u32, 0);
    for byte in text.bytes().filter_map(value) {
        buffer = (buffer << 6) | u32::from(byte);
        bits += 6;
        if bits >= 8 {
            bits -= 8;
            out.push((buffer >> bits) as u8);
            buffer &= (1 << bits) - 1;
        }
    }
    out
}

// ── builtin_probe (TestConformance_BuiltinToolDelegationAcrossSDKs) ──────────────────────────────────────────────────────────────────────────────────────────

type CallLog = Arc<Mutex<Vec<String>>>;

fn log_call(log: &CallLog, entry: String) {
    log.lock().unwrap().push(entry);
}

fn builtin_report(result: &pig_sdk::AgentToolResult) -> Value {
    let blocks: Vec<Value> = result
        .content
        .iter()
        .map(|block| match block["type"].as_str() {
            Some("text") => json!({"type": "text", "text": block["text"].as_str().unwrap_or("")}),
            other => json!({"type": other, "mimeType": block["mimeType"]}),
        })
        .collect();
    let mut report = json!({"content": blocks, "isError": result.is_error});
    if !result.details.is_null() {
        report["details"] = result.details.clone();
        report["detailsJSON"] = json!(serde_json::to_string(&result.details).unwrap_or_default());
    }
    if let Some(structured) = result.structured_content.as_ref().and_then(Value::as_object) {
        let keys: Vec<&String> = structured.keys().collect();
        report["structuredKeys"] = json!(keys);
        report["exitCode"] = structured.get("exit_code").cloned().unwrap_or(Value::Null);
    }
    report
}

fn path_op<R: 'static>(log: &CallLog, label: &'static str, result: impl Fn(&str) -> Result<R, String> + Send + Sync + 'static) -> pig_sdk::PathOperation<R> {
    let log = log.clone();
    Arc::new(move |path| {
        log_call(&log, format!("{label}:{path}"));
        result(path)
    })
}

fn write_op(log: &CallLog) -> pig_sdk::WriteOperation {
    let log = log.clone();
    Arc::new(move |path, content| {
        log_call(&log, format!("writeFile:{path}:{content}"));
        Ok(())
    })
}

/// The options of the case for `tool`, with callbacks that log into `log`; one value per tool kind so the factories take the right type.
struct ProbeOptions {
    read: pig_sdk::ReadToolOptions,
    write: pig_sdk::WriteToolOptions,
    edit: pig_sdk::EditToolOptions,
    ls: pig_sdk::LsToolOptions,
    grep: pig_sdk::GrepToolOptions,
    find: pig_sdk::FindToolOptions,
    bash: pig_sdk::BashToolOptions,
    powershell: pig_sdk::PowerShellToolOptions,
}

fn builtin_options(case: &Value, log: &CallLog) -> ProbeOptions {
    let custom = case["ops"].as_str() == Some("custom");
    let expose = case["exposeSessionEnvironment"].as_bool();
    let mut options = ProbeOptions {
        read: pig_sdk::ReadToolOptions { auto_resize_images: case["autoResizeImages"].as_bool(), ..Default::default() },
        write: Default::default(),
        edit: Default::default(),
        ls: Default::default(),
        grep: Default::default(),
        find: Default::default(),
        bash: pig_sdk::BashToolOptions {
            command_prefix: case["commandPrefix"].as_str().filter(|prefix| !prefix.is_empty()).map(str::to_string),
            expose_session_environment: expose,
            ..Default::default()
        },
        powershell: pig_sdk::PowerShellToolOptions { expose_session_environment: expose, ..Default::default() },
    };
    if case["hook"].as_bool() == Some(true) {
        let hook_log = log.clone();
        options.bash.spawn_hook = Some(Arc::new(move |spawn: pig_sdk::BashSpawnContext| {
            log_call(&hook_log, format!("spawnHook:{}", spawn.command));
            let mut env = spawn.env.clone();
            env.insert("HOOK_VAR".to_string(), "hook-env".to_string());
            Ok(pig_sdk::BashSpawnContext { command: "printf '%s|%s' \"$HOOK_VAR\" \"$PI_SESSION_ID\"".to_string(), cwd: spawn.cwd, env })
        }));
    }
    if !custom {
        return options;
    }
    options.read.operations = Some(pig_sdk::ReadOperations {
        read_file: path_op(log, "readFile", |path| Ok(format!("OPS:{path}").into_bytes())),
        access: path_op(log, "access", |_| Ok(())),
        detect_image_mime_type: None,
    });
    options.write.operations = Some(pig_sdk::WriteOperations { write_file: write_op(log), mkdir: path_op(log, "mkdir", |_| Ok(())) });
    options.edit.operations = Some(pig_sdk::EditOperations {
        read_file: path_op(log, "readFile", |_| Ok(b"hello world\n".to_vec())),
        write_file: write_op(log),
        access: path_op(log, "access", |_| Ok(())),
    });
    options.ls.operations = Some(pig_sdk::LsOperations {
        exists: path_op(log, "exists", |_| Ok(true)),
        stat: path_op(log, "stat", |path| Ok(pig_sdk::LsStat { is_directory: !path.contains('.') })),
        readdir: path_op(log, "readdir", |_| Ok(vec!["b.txt".to_string(), "a.txt".to_string(), "sub".to_string()])),
    });
    options.grep.operations = Some(pig_sdk::GrepOperations {
        is_directory: path_op(log, "isDirectory", |_| Err("no such path for the probe".to_string())),
        read_file: None,
    });
    let glob_log = log.clone();
    options.find.operations = Some(pig_sdk::FindOperations {
        exists: path_op(log, "exists", |_| Ok(false)),
        glob: Arc::new(move |pattern, _cwd, _options| {
            log_call(&glob_log, format!("glob:{pattern}"));
            Ok(Vec::new())
        }),
    });
    let exec_log = log.clone();
    options.bash.operations = Some(pig_sdk::BashOperations {
        exec: Arc::new(move |command, cwd, exec_options| {
            log_call(&exec_log, format!("exec:{command}|cwd={cwd}"));
            (exec_options.on_data)(format!("custom:{command}").as_bytes());
            Ok(Some(0))
        }),
    });
    options
}

fn builtin_probe(ctx: &pig_sdk::Context, case: &Value) -> Result<Value, String> {
    let kind = case["kind"].as_str().unwrap_or("");
    let factory_cwd = case["factoryCwd"].as_str().unwrap_or("").to_string();
    match kind {
        "cwd" => return Ok(json!({"cwd": ctx.cwd()})),
        "register" => return builtin_register(ctx, &factory_cwd),
        "local" => return Ok(builtin_local(case)),
        "sets" => {
            let names = |tools: Vec<pig_sdk::BuiltinTool>| tools.into_iter().map(|tool| tool.name).collect::<Vec<_>>();
            let coding = names(pig_sdk::create_coding_tools(&factory_cwd, None).map_err(|e| e.to_string())?);
            let read_only = names(pig_sdk::create_read_only_tools(&factory_cwd, None).map_err(|e| e.to_string())?);
            return Ok(json!({"coding": coding, "readOnly": read_only}));
        }
        _ => {}
    }
    let log: CallLog = Arc::new(Mutex::new(Vec::new()));
    let options = builtin_options(case, &log);
    let tool = case["tool"].as_str().unwrap_or("bash");
    macro_rules! create {
        ($definition:ident, $tool:ident, $options:expr) => {
            if kind == "definition" { Created::Definition(pig_sdk::$definition(&factory_cwd, Some($options)).map_err(|e| e.to_string())?) } else if case["noContext"].as_bool() == Some(true) { Created::Tool(pig_sdk::$tool(&factory_cwd, Some($options)).map_err(|e| e.to_string())?) } else { Created::Executor(pig_sdk::$definition(&factory_cwd, Some($options)).map_err(|e| e.to_string())?) }
        };
    }
    let created = match tool {
        "read" => create!(create_read_tool_definition, create_read_tool, options.read),
        "write" => create!(create_write_tool_definition, create_write_tool, options.write),
        "edit" => create!(create_edit_tool_definition, create_edit_tool, options.edit),
        "ls" => create!(create_ls_tool_definition, create_ls_tool, options.ls),
        "grep" => create!(create_grep_tool_definition, create_grep_tool, options.grep),
        "find" => create!(create_find_tool_definition, create_find_tool, options.find),
        "powershell" => create!(create_powershell_tool_definition, create_powershell_tool, options.powershell),
        _ => create!(create_bash_tool_definition, create_bash_tool, options.bash),
    };
    if kind == "definition" {
        let Created::Definition(built) = created else { unreachable!() };
        let d = &built.definition;
        let sampling = d.constrained_sampling.as_ref().map(|sampling| match sampling {
            pig_sdk::ToolConstrainedSampling::Config(config) => serde_json::to_value(config).unwrap_or(Value::Null),
            pig_sdk::ToolConstrainedSampling::Disabled => json!(false),
        });
        return Ok(json!({"definition": {
            "name": d.name, "label": d.label, "description": d.description, "promptSnippet": d.prompt_snippet.clone().unwrap_or_default(),
            "promptGuidelines": if d.prompt_guidelines.is_empty() { Value::Null } else { json!(d.prompt_guidelines) }, "parameters": d.parameters,
            "outputSchema": d.output_schema.clone().unwrap_or(Value::Null), "constrainedSampling": sampling.unwrap_or(Value::Null),
            "executionMode": d.execution_mode.clone().unwrap_or_default(), "renderShell": if d.render_shell == ToolRenderShell::SelfShell { "self" } else { "" },
        }}));
    }
    let signal = pig_sdk::ProviderSignal::new();
    if case["preAborted"].as_bool() == Some(true) {
        signal.cancel();
    }
    let updates: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
    let abort_on_output = case["abortOnOutput"].as_bool() == Some(true);
    let on_update: Option<pig_sdk::ExecuteToolUpdate> = (case["updates"].as_bool() == Some(true) || abort_on_output).then(|| {
        let updates = updates.clone();
        let signal = signal.clone();
        Arc::new(move |partial: pig_sdk::AgentToolResult| {
            let text = partial.text();
            if abort_on_output && !text.is_empty() {
                signal.cancel();
            }
            updates.lock().unwrap().push(text);
        }) as pig_sdk::ExecuteToolUpdate
    });
    let params = case.get("params").cloned().unwrap_or_else(|| json!({}));
    let outcome = match created {
        Created::Tool(tool) => tool.execute("probe-call", params, Some(&signal), on_update),
        Created::Executor(built) => built.executor().execute_with(ctx, "probe-call", params, Some(&signal), on_update),
        Created::Definition(_) => unreachable!(),
    };
    let mut report = json!({});
    match outcome {
        Ok(result) => report["result"] = builtin_report(&result),
        Err(error) => report["error"] = json!(error.to_string()),
    }
    if case["updates"].as_bool() == Some(true) {
        report["updates"] = json!(updates.lock().unwrap().clone());
    }
    report["calls"] = json!(log.lock().unwrap().clone());
    Ok(report)
}

enum Created {
    Definition(pig_sdk::BuiltinToolDefinition),
    Executor(pig_sdk::BuiltinToolDefinition),
    Tool(pig_sdk::BuiltinTool),
}

fn wrap_first_text(result: &mut pig_sdk::AgentToolResult, wrap: impl Fn(&str) -> String) {
    if let Some(block) = result.content.first_mut() {
        let text = block["text"].as_str().unwrap_or("").to_string();
        block["text"] = json!(wrap(&text));
    }
}

/// Overrides the read and bash tools with wrapped built-ins, as Pi's `pi.registerTool({ ...definition, execute })` does: the wrapper runs the built-in through
/// the SDK and post-processes its result, so only a tool that really ran on the host can produce the sentinel text.
fn builtin_register(ctx: &pig_sdk::Context, factory_cwd: &str) -> Result<Value, String> {
    let mut read = pig_sdk::create_read_tool_definition(factory_cwd, None).map_err(|e| e.to_string())?;
    let run_read = read.executor();
    read.definition.execute = Box::new(move |ctx, params| match run_read.execute(ctx, params) {
        Ok(mut result) => {
            wrap_first_text(&mut result, |text| format!("«delegated:{text}»"));
            result.details = json!({"delegated": true});
            result.into_tool_result()
        }
        Err(error) => ToolResult::error(error.to_string()),
    });
    let read_name = read.definition.name.clone();
    ctx.register_tool(read.definition).map_err(|e| e.to_string())?;
    let mut bash = pig_sdk::create_bash_tool_definition(factory_cwd, None).map_err(|e| e.to_string())?;
    let run_bash = bash.executor();
    bash.definition.execute = Box::new(move |ctx, params| match run_bash.execute(ctx, params) {
        Ok(mut result) => {
            wrap_first_text(&mut result, |text| format!("«{text}»"));
            result.into_tool_result()
        }
        Err(error) => ToolResult::error(error.to_string()),
    });
    let bash_name = bash.definition.name.clone();
    ctx.register_tool(bash.definition).map_err(|e| e.to_string())?;
    Ok(json!({"registered": [read_name, bash_name]}))
}

fn builtin_local(case: &Value) -> Value {
    let operations = if case["shell"].as_str() == Some("powershell") { pig_sdk::create_local_powershell_operations() } else { pig_sdk::create_local_bash_operations(None) };
    let signal = pig_sdk::ProviderSignal::new();
    let chunks: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(Vec::new()));
    let sink = chunks.clone();
    let abort_signal = signal.clone();
    let abort_on_output = case["abortOnOutput"].as_bool() == Some(true);
    let env = case["env"].as_object().map(|object| object.iter().map(|(name, value)| (name.clone(), value.as_str().unwrap_or("").to_string())).collect());
    let options = pig_sdk::BashExecOptions {
        on_data: Arc::new(move |data| {
            sink.lock().unwrap().push(String::from_utf8_lossy(data).into_owned());
            if abort_on_output {
                abort_signal.cancel();
            }
        }),
        signal,
        timeout: case["timeout"].as_f64(),
        env,
    };
    let mut report = json!({});
    match (operations.exec)(case["command"].as_str().unwrap_or(""), case["factoryCwd"].as_str().unwrap_or(""), options) {
        Ok(code) => report["exitCode"] = json!(code),
        Err(message) => report["error"] = json!(message),
    }
    report["output"] = json!(chunks.lock().unwrap().join(""));
    report
}

/// Contributes the signal-abort row every SDK fixture carries. Pi's ctx.abort() aborts the run's signal before it returns (agent.ts:340-342), so a handler that holds
/// the signal and reads it again right after the abort sees it aborted, in the same turn.
fn register_signal_abort(ext: &mut Extension) {
    ext.command("signal-abort", "Abort the run and report ctx.signal", |ctx, _args| {
        let state = |cancelled: Option<bool>| match cancelled {
            None => "none",
            Some(true) => "aborted",
            Some(false) => "live",
        };
        let held = ctx.signal();
        ctx.abort();
        let report = format!(
            "abort:{}:{}",
            state(held.map(|signal| signal.is_cancelled())),
            state(ctx.signal().map(|signal| signal.is_cancelled()))
        );
        ctx.notify(&report, "info");
        CommandResult::Ok
    });
}

/// A dialog's string answer: a select, input or editor result is whatever JSON the client sent (Pi's RPC mode does not check its type), and undefined is null.
fn dialog_text(value: &serde_json::Value) -> String {
    value.as_str().unwrap_or("").to_string()
}

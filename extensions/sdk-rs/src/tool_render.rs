//! Tool renderers: upstream ToolDefinition.renderShell, renderCall and
//! renderResult for renderers that return terminal lines or a kit view.

use crate::context::Context;
use crate::kit::{Rendered, View};
use crate::protocol::Connection;
use serde_json::{Map, Value};
use std::collections::HashMap;
use std::sync::{Arc, Mutex};

/// Upstream ToolDefinition.renderShell.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum ToolRenderShell {
    /// The renderers draw inside the standard tool card.
    #[default]
    Default,
    /// The renderers draw their own framing (upstream "self").
    SelfShell,
}

/// Upstream ToolRenderContext for a renderer that returns lines. `state` is
/// the tool card's renderer state: it starts empty and is shared by the call
/// and result renderers of one card.
pub struct ToolRenderContext {
    pub args: Value,
    pub tool_call_id: String,
    pub cwd: String,
    pub execution_started: bool,
    pub args_complete: bool,
    pub is_partial: bool,
    pub expanded: bool,
    pub show_images: bool,
    pub is_error: bool,
    /// Horizontal padding configured by the outputPad setting. Renderers with `renderShell: "self"` apply it themselves.
    pub output_pad: u32,
    /// Recorded execution time of a final result in milliseconds. Absent while the result is partial and for results stored without one. upstream: ToolRenderContext.durationMs
    pub duration_ms: Option<i64>,
    pub state: Map<String, Value>,
    invalidate: Arc<dyn Fn() + Send + Sync>,
}

impl ToolRenderContext {
    /// Asks the host to run both renderers of the card again, as upstream
    /// context.invalidate() does.
    pub fn invalidate(&self) {
        (self.invalidate)();
    }
}

/// The result upstream renderResult receives: text and image content blocks
/// and the tool's details.
#[derive(Debug, Clone, Default, serde::Deserialize)]
pub struct ToolRenderResult {
    #[serde(default)]
    pub content: Vec<Value>,
    #[serde(default)]
    pub details: Value,
}

/// Upstream ToolRenderResultOptions.
#[derive(Debug, Clone, Copy, Default, serde::Deserialize)]
pub struct ToolRenderResultOptions {
    #[serde(default)]
    pub expanded: bool,
    #[serde(default, rename = "isPartial")]
    pub is_partial: bool,
}

/// Renders a tool call into terminal lines at a width.
pub type ToolRenderCallHandler = Box<
    dyn Fn(&Context, Value, &mut ToolRenderContext, u32) -> Result<Vec<String>, String>
        + Send
        + Sync,
>;

/// Renders a tool result into terminal lines at a width.
pub type ToolRenderResultHandler = Box<
    dyn Fn(
            &Context,
            ToolRenderResult,
            ToolRenderResultOptions,
            &mut ToolRenderContext,
            u32,
        ) -> Result<Vec<String>, String>
        + Send
        + Sync,
>;

/// Renders a tool call into terminal lines at a width; shared by the renderers a resolver returns.
pub type SharedToolRenderCall = Arc<
    dyn Fn(&Context, Value, &mut ToolRenderContext, u32) -> Result<Vec<String>, String> + Send + Sync,
>;

/// Renders a tool result into terminal lines at a width; shared by the renderers a resolver returns.
pub type SharedToolRenderResult = Arc<
    dyn Fn(
            &Context,
            ToolRenderResult,
            ToolRenderResultOptions,
            &mut ToolRenderContext,
            u32,
        ) -> Result<Vec<String>, String>
        + Send
        + Sync,
>;

/// Renders a tool call into a kit view (D107), which the host renders at the
/// card's width.
pub type ToolRenderCallViewHandler =
    Box<dyn Fn(&Context, Value, &mut ToolRenderContext, u32) -> Result<View, String> + Send + Sync>;

/// Renders a tool result into a kit view (D107).
pub type ToolRenderResultViewHandler = Box<
    dyn Fn(&Context, ToolRenderResult, ToolRenderResultOptions, &mut ToolRenderContext, u32) -> Result<View, String>
        + Send
        + Sync,
>;

/// [`ToolRenderCallViewHandler`] shared by the renderers a resolver returns.
pub type SharedToolRenderCallView =
    Arc<dyn Fn(&Context, Value, &mut ToolRenderContext, u32) -> Result<View, String> + Send + Sync>;

/// [`ToolRenderResultViewHandler`] shared by the renderers a resolver returns.
pub type SharedToolRenderResultView = Arc<
    dyn Fn(&Context, ToolRenderResult, ToolRenderResultOptions, &mut ToolRenderContext, u32) -> Result<View, String>
        + Send
        + Sync,
>;

/// The renderers a tool renderer resolver returns (upstream ToolRenderers).
///
/// `call_view` and `result_view` are the view forms (D107): each declares its
/// phase rendered and wins over the line form set for the same phase.
///
/// pig divergence (D89): `next()` returns renderers the host draws as a marker. Returning it keeps them; its
/// renderers are `None`, so a resolver cannot wrap them. A marker given a renderer is the resolver's own renderers.
#[derive(Clone, Default)]
pub struct ToolRendererSet {
    pub render_shell: ToolRenderShell,
    pub render_call: Option<SharedToolRenderCall>,
    pub render_result: Option<SharedToolRenderResult>,
    pub call_view: Option<SharedToolRenderCallView>,
    pub result_view: Option<SharedToolRenderResultView>,
    next_marker: bool,
}

impl ToolRendererSet {
    /// Renderers that draw calls with `render_call`.
    pub fn with_call(
        render_call: impl Fn(&Context, Value, &mut ToolRenderContext, u32) -> Result<Vec<String>, String>
        + Send
        + Sync
        + 'static,
    ) -> Self {
        Self { render_call: Some(Arc::new(render_call)), ..Self::default() }
    }

    /// Renderers that draw calls with the view `call_view`.
    pub fn with_call_view(
        call_view: impl Fn(&Context, Value, &mut ToolRenderContext, u32) -> Result<View, String> + Send + Sync + 'static,
    ) -> Self {
        Self { call_view: Some(Arc::new(call_view)), ..Self::default() }
    }

    fn renders_call(&self) -> bool {
        self.render_call.is_some() || self.call_view.is_some()
    }

    fn renders_result(&self) -> bool {
        self.render_result.is_some() || self.result_view.is_some()
    }
}

/// The function of a tool renderer resolver.
pub(crate) type ToolRendererResolverFn =
    dyn Fn(&str, &dyn Fn() -> Option<ToolRendererSet>) -> Option<ToolRendererSet> + Send + Sync;

/// A tool renderer resolver (upstream ToolRendererResolver): the tool's name and `next`, which returns the renderers
/// the remaining resolvers, then the registered tool, would use.
pub type ToolRendererResolver = Box<ToolRendererResolverFn>;

/// The resolvers an extension registers after loading ([`crate::Context::register_tool_renderer`]), after the ones it
/// registered while loading.
#[derive(Default)]
pub(crate) struct LateToolRendererResolvers {
    /// How many resolvers the extension registered while loading.
    pub(crate) loaded: std::sync::atomic::AtomicUsize,
    pub(crate) resolvers: Mutex<Vec<Arc<ToolRendererResolverFn>>>,
}

/// Each tool card's renderer state, by card.
type CardStates = HashMap<String, Arc<Mutex<Map<String, Value>>>>;

#[derive(Default)]
pub(crate) struct ToolRenderers {
    pub(crate) call: HashMap<String, ToolRenderCallHandler>,
    pub(crate) result: HashMap<String, ToolRenderResultHandler>,
    pub(crate) call_view: HashMap<String, ToolRenderCallViewHandler>,
    pub(crate) result_view: HashMap<String, ToolRenderResultViewHandler>,
    pub(crate) resolvers: Vec<ToolRendererResolver>,
    resolved: Mutex<HashMap<String, ToolRendererSet>>,
    cards: Mutex<CardStates>,
}

#[derive(serde::Deserialize, Default)]
#[serde(rename_all = "camelCase")]
struct RenderContextWire {
    #[serde(default)]
    tool_call_id: String,
    #[serde(default)]
    cwd: String,
    #[serde(default)]
    execution_started: bool,
    #[serde(default)]
    args_complete: bool,
    #[serde(default)]
    is_partial: bool,
    #[serde(default)]
    expanded: bool,
    #[serde(default)]
    show_images: bool,
    #[serde(default)]
    is_error: bool,
    #[serde(default)]
    output_pad: u32,
    #[serde(default)]
    duration_ms: Option<i64>,
}

#[derive(serde::Deserialize, Default)]
struct RenderToolWire {
    #[serde(default)]
    card: String,
    #[serde(default)]
    phase: String,
    #[serde(default)]
    renderers: String,
    #[serde(default)]
    args: Value,
    #[serde(default)]
    result: Option<ToolRenderResult>,
    #[serde(default)]
    options: ToolRenderResultOptions,
    #[serde(default)]
    context: RenderContextWire,
    #[serde(default)]
    width: u32,
}

impl ToolRenderers {
    /// Answers a render_tool request with the renderer's lines or view. A
    /// view form wins over the line form of the same phase. Renders of one
    /// card run one at a time.
    pub(crate) fn render(
        &self,
        ctx: &Context,
        conn: &Arc<Connection>,
        tool: &str,
        args: Option<&Value>,
    ) -> Result<Rendered, String> {
        let request: RenderToolWire = args
            .cloned()
            .map(|value| serde_json::from_value(value).map_err(|err| err.to_string()))
            .transpose()?
            .unwrap_or_default();
        let card = self
            .cards
            .lock()
            .unwrap()
            .entry(request.card.clone())
            .or_default()
            .clone();
        let mut state = card.lock().unwrap();
        let notify_conn = conn.clone();
        let notify_card = request.card.clone();
        let mut render = ToolRenderContext {
            args: request.args.clone(),
            tool_call_id: request.context.tool_call_id,
            cwd: request.context.cwd,
            execution_started: request.context.execution_started,
            args_complete: request.context.args_complete,
            is_partial: request.context.is_partial,
            expanded: request.context.expanded,
            show_images: request.context.show_images,
            is_error: request.context.is_error,
            output_pad: request.context.output_pad,
            duration_ms: request.context.duration_ms,
            state: std::mem::take(&mut *state),
            invalidate: Arc::new(move || {
                let _ = notify_conn.notify(
                    "tool_render_invalidate",
                    Some(serde_json::json!({ "card": notify_card })),
                );
            }),
        };
        if !request.renderers.is_empty() {
            let set = self.resolved.lock().unwrap().get(&request.renderers).cloned();
            let rendered = match (set, request.phase.as_str()) {
                (Some(set), "result") => match (set.result_view, set.render_result) {
                    (Some(view), _) => view(ctx, request.result.unwrap_or_default(), request.options, &mut render, request.width).map(Rendered::View),
                    (None, Some(handler)) => handler(ctx, request.result.unwrap_or_default(), request.options, &mut render, request.width).map(Rendered::Lines),
                    (None, None) => Err(format!("tool {tool} has no result renderer")),
                },
                (Some(set), _) => match (set.call_view, set.render_call) {
                    (Some(view), _) => view(ctx, request.args, &mut render, request.width).map(Rendered::View),
                    (None, Some(handler)) => handler(ctx, request.args, &mut render, request.width).map(Rendered::Lines),
                    (None, None) => Err(format!("tool {tool} has no call renderer")),
                },
                (None, _) => Err(format!("unknown tool renderers {}", request.renderers)),
            };
            *state = render.state;
            return rendered;
        }
        let registered = conn.registered_tools.lock().unwrap().get(tool).cloned();
        let rendered = if request.phase == "result" {
            let (view, lines) = match &registered {
                Some(definition) => (definition.render_result_view.as_ref(), definition.render_result.as_ref()),
                None => (self.result_view.get(tool), self.result.get(tool)),
            };
            match (view, lines) {
                (Some(view), _) => view(ctx, request.result.unwrap_or_default(), request.options, &mut render, request.width).map(Rendered::View),
                (None, Some(handler)) => handler(ctx, request.result.unwrap_or_default(), request.options, &mut render, request.width).map(Rendered::Lines),
                (None, None) => Err(format!("tool {tool} has no result renderer")),
            }
        } else {
            let (view, lines) = match &registered {
                Some(definition) => (definition.render_call_view.as_ref(), definition.render_call.as_ref()),
                None => (self.call_view.get(tool), self.call.get(tool)),
            };
            match (view, lines) {
                (Some(view), _) => view(ctx, request.args, &mut render, request.width).map(Rendered::View),
                (None, Some(handler)) => handler(ctx, request.args, &mut render, request.width).map(Rendered::Lines),
                (None, None) => Err(format!("tool {tool} has no call renderer")),
            }
        };
        *state = render.state;
        rendered
    }

    /// Answers a resolve_tool_renderers request: the resolvers run in registration order with the host's next()
    /// renderers last.
    pub(crate) fn resolve(&self, conn: &Connection, args: Option<&Value>) -> Value {
        let tool = args.and_then(|args| args.get("tool")).and_then(Value::as_str).unwrap_or("").to_string();
        let marker = args.and_then(|args| args.get("next")).filter(|next| next.is_object()).map(|next| ToolRendererSet {
            render_shell: if next.get("render_shell").and_then(Value::as_str) == Some("self") { ToolRenderShell::SelfShell } else { ToolRenderShell::Default },
            next_marker: true,
            ..ToolRendererSet::default()
        });
        fn chain(resolvers: &[&ToolRendererResolverFn], tool: &str, marker: &Option<ToolRendererSet>) -> Option<ToolRendererSet> {
            match resolvers.split_first() {
                Some((first, rest)) => first(tool, &|| chain(rest, tool, marker)),
                None => marker.clone(),
            }
        }
        let late = conn.late_tool_renderers.resolvers.lock().unwrap().clone();
        let resolvers: Vec<&ToolRendererResolverFn> =
            self.resolvers.iter().map(|resolver| resolver.as_ref()).chain(late.iter().map(|resolver| resolver.as_ref())).collect();
        match chain(&resolvers, &tool, &marker) {
            None => serde_json::json!({ "use": "none" }),
            // A marker given render functions (`let mut set = next()?; set.render_call = ...`) is the resolver's own
            // renderers.
            Some(set) if set.next_marker && !set.renders_call() && !set.renders_result() => {
                serde_json::json!({ "use": "next" })
            }
            Some(set) => {
                let mut resolved = self.resolved.lock().unwrap();
                let id = format!("r{}", resolved.len() + 1);
                let mut answer = serde_json::json!({ "use": "own", "renderers": id });
                if set.render_shell == ToolRenderShell::SelfShell {
                    answer["render_shell"] = Value::from("self");
                }
                if set.renders_call() {
                    answer["renders_call"] = Value::from(true);
                }
                if set.renders_result() {
                    answer["renders_result"] = Value::from(true);
                }
                resolved.insert(id, set);
                answer
            }
        }
    }

    /// Drops the state of a tool card the host no longer shows.
    pub(crate) fn release(&self, args: Option<&Value>) {
        if let Some(card) = args
            .and_then(|args| args.get("card"))
            .and_then(|card| card.as_str())
        {
            self.cards.lock().unwrap().remove(card);
        }
    }
}

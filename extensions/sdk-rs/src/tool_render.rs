//! Tool renderers: upstream ToolDefinition.renderShell, renderCall and
//! renderResult for renderers that return terminal lines.

use crate::context::Context;
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

/// Each tool card's renderer state, by card.
type CardStates = HashMap<String, Arc<Mutex<Map<String, Value>>>>;

#[derive(Default)]
pub(crate) struct ToolRenderers {
    pub(crate) call: HashMap<String, ToolRenderCallHandler>,
    pub(crate) result: HashMap<String, ToolRenderResultHandler>,
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
}

#[derive(serde::Deserialize, Default)]
struct RenderToolWire {
    #[serde(default)]
    card: String,
    #[serde(default)]
    phase: String,
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
    /// Answers a render_tool request with the renderer's lines. Renders of
    /// one card run one at a time.
    pub(crate) fn render(
        &self,
        ctx: &Context,
        conn: &Arc<Connection>,
        tool: &str,
        args: Option<&Value>,
    ) -> Result<Vec<String>, String> {
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
            state: std::mem::take(&mut *state),
            invalidate: Arc::new(move || {
                let _ = notify_conn.notify(
                    "tool_render_invalidate",
                    Some(serde_json::json!({ "card": notify_card })),
                );
            }),
        };
        let lines = if request.phase == "result" {
            match self.result.get(tool) {
                Some(handler) => handler(
                    ctx,
                    request.result.unwrap_or_default(),
                    request.options,
                    &mut render,
                    request.width,
                ),
                None => Err(format!("tool {tool} has no result renderer")),
            }
        } else {
            match self.call.get(tool) {
                Some(handler) => handler(ctx, request.args, &mut render, request.width),
                None => Err(format!("tool {tool} has no call renderer")),
            }
        };
        *state = render.state;
        lines
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

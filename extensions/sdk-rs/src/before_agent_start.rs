//! Pi's `before_agent_start` event for a Rust handler: the run's prompt and the shared `systemPromptOptions` every handler of the
//! emission edits (runner.ts:1411-1462).

use crate::context::{Context, SystemPromptState};
use serde_json::Value;
use std::io;
use std::sync::Arc;

/// What a `before_agent_start` handler receives (Pi's `BeforeAgentStartEvent`).
///
/// Pi's `event.systemPromptOptions` is one object every handler of the emission edits, and `event.systemPrompt` and
/// `ctx.getSystemPrompt()` render it when read. A Rust handler borrows its event exclusively, so the options live in a cell the
/// event and the [`Context`] share: [`BeforeAgentStartEvent::set_system_prompt_options`] and
/// [`BeforeAgentStartEvent::edit_system_prompt_options`] write it, and [`BeforeAgentStartEvent::system_prompt`] and
/// [`Context::get_system_prompt`] read the prompt the current options render, so a read after an edit sees the edit, as it does
/// in Pi and in the Go, Python and Node SDKs. Register the handler with [`crate::Extension::on_before_agent_start`].
pub struct BeforeAgentStartEvent {
    ctx: Context,
    payload: Value,
    state: Arc<SystemPromptState>,
}

impl BeforeAgentStartEvent {
    pub(crate) fn new(ctx: &Context, mut payload: Value, state: Arc<SystemPromptState>) -> Self {
        if let Some(object) = payload.as_object_mut() {
            object.remove("systemPrompt");
            object.remove("systemPromptOptions");
        }
        Self { ctx: ctx.clone(), payload, state }
    }

    /// The event as the host sent it, without `systemPrompt` and `systemPromptOptions`, which change as handlers edit the options:
    /// `messageType`, `prompt` and `images` for a user run, `message` for a custom-message run.
    pub fn payload(&self) -> &Value {
        &self.payload
    }

    /// The whole event as Pi's event object: [`BeforeAgentStartEvent::payload`] with `systemPrompt` and `systemPromptOptions` as
    /// they are now, for a handler that reports or forwards the event.
    pub fn snapshot(&self) -> io::Result<Value> {
        let mut event = self.payload.clone();
        event["systemPrompt"] = Value::String(self.system_prompt()?);
        event["systemPromptOptions"] = self.system_prompt_options();
        Ok(event)
    }

    /// Pi's `event.systemPrompt`: the prompt the emission's shared options render now, including every edit made so far. It is the
    /// same value [`Context::get_system_prompt`] returns in this handler.
    pub fn system_prompt(&self) -> io::Result<String> {
        self.ctx.get_system_prompt()
    }

    /// Pi's `event.systemPromptOptions`: a copy of the emission's shared options as they are now. A later handler receives
    /// every edit made here.
    pub fn system_prompt_options(&self) -> Value {
        self.state.options()
    }

    /// Replaces the emission's shared `systemPromptOptions` with `options`, which keeps every collection Pi's normalized options
    /// carry (`sections`, `selectedTools`, `hiddenTools`, `promptGuidelines`, `contextFiles`, `skills`, `toolSnippets`,
    /// `toolGuidelines`). The next [`BeforeAgentStartEvent::system_prompt`] or [`Context::get_system_prompt`] renders it.
    pub fn set_system_prompt_options(&self, options: Value) {
        self.state.set_options(options);
    }

    /// Edits the emission's shared `systemPromptOptions` in place with `edit` and returns its result. `edit` runs on a copy that
    /// replaces the options when it returns, so it may read the prompt through the context, and a read in it sees the options
    /// from before the edit.
    pub fn edit_system_prompt_options<R>(&self, edit: impl FnOnce(&mut Value) -> R) -> R {
        self.state.edit_options(edit)
    }
}

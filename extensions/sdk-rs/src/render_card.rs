//! The renderer state of displayed messages and entries. The host names each displayed message or entry with a key; Pi hands a custom
//! message renderer the same message object on every rebuild, so a renderer keeps per-message state in a WeakMap keyed by the message. The
//! SDK mirrors it: a card per key holds the renderer state and the message as the first request for the key decoded it.

use crate::kit::Event;
use serde_json::{Map, Value};
use std::collections::HashMap;
use std::sync::{Arc, Mutex, MutexGuard};

/// The renderer state of one displayed message or entry ([`crate::MessageRenderOptions::state`],
/// [`crate::EntryRenderOptions::state`]): it starts empty and every request for the message or entry shares it. Clones of the handle are the same
/// state.
pub type RenderState = Arc<Mutex<Map<String, Value>>>;

/// One displayed message or entry. `subject` is the message or entry as the first request for the key decoded it, and its lock runs the requests
/// for one card one at a time, as Pi draws a message on one thread.
#[derive(Default)]
struct RenderCard {
    state: RenderState,
    subject: Mutex<Option<Value>>,
}

/// The cards of the messages and entries the host shows, by key.
#[derive(Default)]
pub(crate) struct RenderCards {
    cards: Mutex<HashMap<String, Arc<RenderCard>>>,
}

fn locked<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    mutex
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

impl RenderCards {
    /// The card of `key`, created on first use. An empty key gets a card of its own that nothing keeps.
    fn card(&self, key: &str) -> Arc<RenderCard> {
        if key.is_empty() {
            return Arc::new(RenderCard::default());
        }
        locked(&self.cards)
            .entry(key.to_string())
            .or_default()
            .clone()
    }

    /// Answers a `render_message` or `render_entry` request: the events the host's view took go to `handle` in order, then `render` draws the
    /// subject. `subject_key` is the member of `args` that holds the message or entry. A request without a key is not for a displayed message: it
    /// keeps nothing. A failing handler does not stop the events after it or the render; its error is the answer unless the render failed.
    pub(crate) fn run<T>(
        &self,
        args: &Value,
        subject_key: &str,
        handle: impl Fn(Value, &RenderState, Event) -> Result<(), String>,
        render: impl FnOnce(Value, &RenderState) -> Result<T, String>,
    ) -> Result<T, String> {
        let key = args.get("key").and_then(Value::as_str).unwrap_or("");
        let card = self.card(key);
        let mut subject = locked(&card.subject);
        let subject = subject.get_or_insert_with(|| {
            args.get(subject_key)
                .cloned()
                .unwrap_or_else(|| Value::Object(Map::new()))
        });
        let mut failure = None;
        let events = args.get("events").and_then(Value::as_array);
        for (_, event) in events.into_iter().flatten().filter_map(Event::from_wire) {
            if let Err(message) = handle(subject.clone(), &card.state, event) {
                failure.get_or_insert(message);
            }
        }
        let rendered = render(subject.clone(), &card.state)?;
        match failure {
            Some(message) => Err(message),
            None => Ok(rendered),
        }
    }

    /// Drops the state of a message or entry the host no longer shows.
    pub(crate) fn release(&self, args: Option<&Value>) {
        if let Some(key) = args
            .and_then(|args| args.get("key"))
            .and_then(Value::as_str)
        {
            locked(&self.cards).remove(key);
        }
    }
}

//! Upstream's `pi.events` (packages/coding-agent/src/core/event-bus.ts), shared with every other realm of the session.
//!
//! Payloads cross by value as JSON. Listeners run in registration order across all extensions, each listener's work finishes before [`EventBus::emit`] returns, a listener's error is reported and reaches neither the emitter nor the later listeners, and a listener that never returns blocks its emitter, as in upstream. A payload arrives as the emitter's JSON value: the emitter's `JSON.stringify` view for a node emitter (`undefined` arrives as `null`), the encoded value for a native one.
//!
//! Call [`Context::events`] from a handler so a nested emit is ordered with that request; [`Extension::events`](crate::Extension::events) makes calls that are not tied to a request.

use std::collections::HashMap;
use std::io;
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};

use serde_json::{Value, json};

use crate::context::Context;
use crate::protocol::Connection;

/// The handler of `pi.events.on(channel, handler)`: it runs as one unit of work, and an `Err` (or a panic) is reported without reaching the emitter or the listeners after it.
pub type EventBusHandler = Arc<dyn Fn(&Context, &Value) -> Result<(), String> + Send + Sync>;

// Process-wide, like the Host's realm: the Host keys a listener by process and ID.
static HANDLER_SEQ: AtomicU64 = AtomicU64::new(1);

struct Sub {
    id: String,
    channel: String,
    handler: EventBusHandler,
    // `sent` is set once the events.on call is made; `off` once the listener is unsubscribed.
    state: Mutex<SubState>,
}

#[derive(Default)]
struct SubState {
    sent: bool,
    off: bool,
}

#[derive(Default)]
struct BusState {
    live: bool,
    conn: Option<Arc<Connection>>,
    // A dispatch can arrive before events.on returns, and after events.off for a listener the Host had already snapshotted, so a listener stays until the Host releases it.
    subs: HashMap<String, Arc<Sub>>,
    pending: Vec<Arc<Sub>>,
}

/// An extension's listeners.
#[derive(Default)]
pub(crate) struct BusRegistry {
    state: Mutex<BusState>,
}

impl BusRegistry {
    /// Makes later subscriptions register at once and returns the listeners subscribed before the extension ran, in subscription order.
    pub(crate) fn go_live(&self, conn: &Arc<Connection>) -> Vec<(String, String)> {
        let mut state = self.state.lock().unwrap();
        state.live = true;
        state.conn = Some(conn.clone());
        let pending = std::mem::take(&mut state.pending);
        let mut out = Vec::new();
        for sub in pending {
            let mut sub_state = sub.state.lock().unwrap();
            if sub_state.off {
                continue;
            }
            sub_state.sent = true;
            out.push((sub.id.clone(), sub.channel.clone()));
        }
        out
    }

    /// Forgets a finished run's listeners: the Host dropped them when the connection closed.
    pub(crate) fn reset(&self) {
        let mut state = self.state.lock().unwrap();
        *state = BusState::default();
    }

    pub(crate) fn release(&self, args: Option<&Value>) {
        let Some(id) = args.and_then(|args| args["handlerId"].as_str()) else { return };
        let mut state = self.state.lock().unwrap();
        if state.subs.get(id).is_some_and(|sub| sub.state.lock().unwrap().off) {
            state.subs.remove(id);
        }
    }

    /// Runs one listener for an events.dispatch request.
    pub(crate) fn dispatch(&self, ctx: &Context, args: &Value) -> Result<(), String> {
        let id = args["handlerId"].as_str().unwrap_or("");
        let sub = self
            .state
            .lock()
            .unwrap()
            .subs
            .get(id)
            .cloned()
            .filter(|sub| args["channel"].as_str() == Some(sub.channel.as_str()))
            .ok_or_else(|| format!("Unknown event bus handler: {id}"))?;
        let data = args.get("json").cloned().unwrap_or(Value::Null);
        match catch_unwind(AssertUnwindSafe(|| (sub.handler)(ctx, &data))) {
            Ok(result) => result,
            Err(payload) => Err(payload
                .downcast_ref::<String>()
                .cloned()
                .or_else(|| payload.downcast_ref::<&str>().map(|message| (*message).to_string()))
                .unwrap_or_else(|| "extension handler panicked".to_string())),
        }
    }
}

/// Upstream's `pi.events`.
#[derive(Clone)]
pub struct EventBus {
    registry: Arc<BusRegistry>,
    ctx: Option<Context>,
}

/// A listener's handle: [`Subscription::unsubscribe`] removes it and is idempotent.
#[derive(Clone)]
pub struct Subscription {
    registry: Arc<BusRegistry>,
    sub: Arc<Sub>,
    done: Arc<AtomicBool>,
}

impl EventBus {
    pub(crate) fn new(registry: Arc<BusRegistry>, ctx: Option<Context>) -> Self {
        Self { registry, ctx }
    }

    fn call(&self, method: &str, args: Value) -> io::Result<Option<Value>> {
        let result = match &self.ctx {
            Some(ctx) => ctx.call_wire(method, Some(args))?,
            None => {
                let conn = self.registry.state.lock().unwrap().conn.clone().ok_or_else(|| {
                    io::Error::new(io::ErrorKind::NotConnected, "pi.events: the extension is not connected to the host yet")
                })?;
                conn.call(method, Some(args))?
            }
        };
        if let Some(err) = result.error {
            return Err(io::Error::other(crate::context::host_error_message(err)));
        }
        Ok(result.result)
    }

    /// Subscribes `handler` to `channel`. A listener subscribed before the extension runs is registered with the Host before the extension registers, as a node factory's `pi.events.on` is. A failure to register with the Host is returned, and nothing stays subscribed.
    pub fn on(
        &self,
        channel: impl Into<String>,
        handler: impl Fn(&Context, &Value) -> Result<(), String> + Send + Sync + 'static,
    ) -> io::Result<Subscription> {
        let channel = channel.into();
        let sub = Arc::new(Sub {
            id: format!("rs-{}", HANDLER_SEQ.fetch_add(1, Ordering::Relaxed)),
            channel: channel.clone(),
            handler: Arc::new(handler),
            state: Mutex::new(SubState::default()),
        });
        let subscription = Subscription { registry: self.registry.clone(), sub: sub.clone(), done: Arc::new(AtomicBool::new(false)) };
        {
            let mut state = self.registry.state.lock().unwrap();
            state.subs.insert(sub.id.clone(), sub.clone());
            if !state.live {
                state.pending.push(sub);
                return Ok(subscription);
            }
            sub.state.lock().unwrap().sent = true;
        }
        if let Err(err) = self.call("events.on", json!({"channel": channel, "handlerId": subscription.sub.id, "value": true})) {
            self.registry.state.lock().unwrap().subs.remove(&subscription.sub.id);
            return Err(err);
        }
        Ok(subscription)
    }

    /// Delivers `data` to every listener of `channel`, in registration order, and returns when each has finished. Like EventEmitter, an emit on `"error"` with no listener fails.
    pub fn emit(&self, channel: &str, data: Value) -> io::Result<()> {
        let outcome = self.call("events.emit", json!({"channel": channel, "value": true, "json": data}))?;
        if outcome.is_some_and(|outcome| outcome["unhandledError"] == Value::Bool(true)) {
            return Err(io::Error::other(format!("pi.events.emit {channel:?}: unhandled error event: {data}")));
        }
        Ok(())
    }
}

impl Subscription {
    /// Removes the listener. Calling it again does nothing.
    pub fn unsubscribe(&self) {
        if self.done.swap(true, Ordering::AcqRel) {
            return;
        }
        let sent = {
            let mut state = self.registry.state.lock().unwrap();
            let mut sub_state = self.sub.state.lock().unwrap();
            sub_state.off = true;
            if !sub_state.sent {
                state.subs.remove(&self.sub.id);
            }
            sub_state.sent
        };
        if sent {
            let bus = EventBus::new(self.registry.clone(), None);
            if let Err(err) = bus.call("events.off", json!({"handlerId": self.sub.id})) {
                eprintln!("extension: pi.events unsubscribe {:?}: {err}", self.sub.channel);
            }
        }
    }
}

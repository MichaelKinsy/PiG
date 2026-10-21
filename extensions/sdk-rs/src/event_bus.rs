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
    // The listeners subscribed where there is no Host to register them with (no PIG_EXT_SOCKET, as in a unit test), or while the extension connects, in subscription order: `go_live` registers them with the Host before the extension registers.
    pending: Vec<Arc<Sub>>,
}

/// A listener the extension registers with the Host when it connects.
pub(crate) struct LiveOp {
    pub(crate) handler_id: String,
    pub(crate) channel: String,
}

/// An extension's listeners.
#[derive(Default)]
pub(crate) struct BusRegistry {
    state: Mutex<BusState>,
}

impl BusRegistry {
    /// Makes later subscriptions register at once and returns the listeners subscribed before the extension ran, in subscription order.
    pub(crate) fn go_live(&self, conn: &Arc<Connection>) -> Vec<LiveOp> {
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
            out.push(LiveOp { handler_id: sub.id.clone(), channel: sub.channel.clone() });
        }
        out
    }

    /// Records the connection `run` took, so a subscription made while the extension connects waits for [`go_live`](Self::go_live) instead of dialling a second connection.
    pub(crate) fn adopt(&self, conn: &Arc<Connection>) {
        self.state.lock().unwrap().conn = Some(conn.clone());
    }

    /// What serves the host's frames on the factory's connection while one of its calls waits: the dispatch of a listener the factory subscribed, which runs on this thread with a [`Context`] that has no session state, and the release of an unsubscribed one. Anything else the extension cannot serve before it registers is refused, so the Host does not wait for it.
    fn loading_server(self: &Arc<Self>, conn: &Arc<Connection>) -> crate::protocol::LoadingServer {
        let registry = self.clone();
        let conn = Arc::downgrade(conn);
        Arc::new(move |env| {
            let Some(conn) = conn.upgrade() else { return };
            match env.msg_type.as_str() {
                "request" => {
                    let Some(id) = env.id.as_deref() else { return };
                    let request = env.request.as_ref();
                    let error = if request.is_some_and(|request| request.method == "events.dispatch") {
                        let ctx = Context::for_factory(conn.clone(), registry.clone());
                        registry.dispatch(&ctx, request.and_then(|request| request.args.as_ref()).unwrap_or(&Value::Null)).err()
                    } else {
                        Some(format!("{}: the extension has not registered with the host yet", request.map_or("", |request| request.method.as_str())))
                    };
                    let _ = conn.respond(id, None, error.map(|message| crate::protocol::ErrorInfo { code: None, message }));
                }
                "notify" if env.notify.as_ref().is_some_and(|notify| notify.method == "events.release") => registry.release(env.notify.as_ref().and_then(|notify| notify.args.as_ref())),
                _ => {}
            }
        })
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

    /// The connection a bus call made outside a request rides: the running extension's, or, while the factory runs, the one its first call dials (host_helpers.rs `dial_loading`), which `run` takes over. A call on that one answers the Host's dispatches while it waits, so a listener the factory subscribed hears an emit another extension makes then, even one made in reply to the factory's own emit.
    fn connection(&self) -> io::Result<Arc<Connection>> {
        if let Some(conn) = self.registry.state.lock().unwrap().conn.clone() {
            return Ok(conn);
        }
        let conn = crate::host_helpers::dial_loading()?;
        conn.set_loading_server(self.registry.loading_server(&conn));
        Ok(conn)
    }

    fn call(&self, method: &str, args: Value) -> io::Result<Option<Value>> {
        let result = match &self.ctx {
            Some(ctx) => ctx.call_wire(method, Some(args))?,
            None => self.connection()?.call(method, Some(args))?
        };
        if let Some(err) = result.error {
            return Err(io::Error::other(crate::context::host_error_message(err)));
        }
        Ok(result.result)
    }

    /// Subscribes `handler` to `channel`. A listener subscribed while the factory runs is registered with the Host at once, over the connection the factory opened, as Pi's shared emitter holds it from that moment: it hears an emit another extension makes while the factory runs, while the factory waits for a host call of its own. With no Host to reach (no `PIG_EXT_SOCKET`, as in a unit test) it is queued and registered when the extension runs. A failure to register with the Host is returned, and nothing stays subscribed.
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
            if !state.live && state.conn.is_some() {
                // `run` has taken the connection and registers the listener in `go_live`.
                state.pending.push(sub);
                return Ok(subscription);
            }
        }
        if !self.registry.state.lock().unwrap().live && self.ctx.is_none() {
            match self.connection() {
                Ok(_) => {}
                Err(err) if err.kind() == io::ErrorKind::NotConnected => {
                    let mut state = self.registry.state.lock().unwrap();
                    if !state.live {
                        state.pending.push(sub);
                        return Ok(subscription);
                    }
                }
                Err(err) => {
                    self.registry.state.lock().unwrap().subs.remove(&sub.id);
                    return Err(err);
                }
            }
        }
        sub.state.lock().unwrap().sent = true;
        if let Err(err) = self.call("events.on", json!({"channel": channel, "handlerId": subscription.sub.id, "value": true})) {
            self.registry.state.lock().unwrap().subs.remove(&subscription.sub.id);
            return Err(err);
        }
        Ok(subscription)
    }

    /// Delivers `data` to every listener of `channel`, in registration order, and returns when each has finished. Like EventEmitter, an emit on `"error"` with no listener fails.
    ///
    /// Called through [`Extension::events`](crate::Extension::events) while the factory runs (before the extension runs), it does what Pi's synchronous emit does in a factory: the Host delivers it to every listener it holds, in registration order, the listeners of the extensions loaded before this one and then this extension's own, which [`on`](Self::on) registered with it at once; each finishes before `emit` returns, and an emit on `"error"` that nobody hears fails. The first factory-time bus call connects to the Host (as `parse_frontmatter` does) and `run` reuses the connection: a factory that runs with no Host (no `PIG_EXT_SOCKET`, as in a unit test) gets an error. A listener the factory's emit runs has a [`Context`] with no session state yet (its `cwd` and `mode` are empty), and until the extension registers the only host calls it can make are `pi.events` ones and the parse helpers (`parse_frontmatter`, `parse_date`). Use [`Extension::on_loaded`](crate::Extension::on_loaded) for work that needs the live session.
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

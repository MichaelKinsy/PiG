//! The function options of Pi's `createMcpExtension` (extensions/mcp/index.ts `McpExtensionOptions`): `loadConfig`, `createTransport`, `credentials`, `openUrl` and `updateConfig`.
//!
//! They stay in this process. The reference [`crate::create_mcp_extension`] returns names them to the host as session graph callbacks when [`crate::DefaultResourceLoader`] is built, and the host runs them with JSON arguments over the connection (the wire of the loader's override options; no request kind is added). Entries, configurations, tokens, states and messages are Pi's JSON shapes as `serde_json::Value`.

use crate::context::Context;
use crate::graph::{add_callback, remove_callback, GraphCallback};
use serde_json::{json, Map, Value};
use std::collections::{BTreeMap, HashMap};
use std::io;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::mpsc;
use std::sync::{Arc, Condvar, Mutex};

/// The part of Pi's `ExtensionContext` that a `load_config` function reads: the session's working directory and whether the project is trusted (`ctx.cwd`, `ctx.isProjectTrusted()`). The rest of the context (its UI and session objects) lives in the host.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct McpLoadConfigContext {
    pub cwd: String,
    pub is_project_trusted: bool,
}

/// Pi's `AuthProvider` of an HTTP server, held by the host for a transport: it supplies bearer tokens and refreshes them after a rejection. Its methods call the host.
#[derive(Clone)]
pub struct McpAuthProvider {
    context: Context,
    handle: String,
}

impl McpAuthProvider {
    /// `token()`: the current access token, `None` for none.
    pub fn token(&self) -> io::Result<Option<String>> {
        let value = self.context.call_host("sessionGraph.call", Some(json!({"handle": self.handle, "method": "token", "args": []})))?;
        Ok(value.as_ref().and_then(Value::as_str).map(str::to_string))
    }

    /// `onUnauthorized(context)`: the host refreshes the credentials after the response (its `status` and ordered `headers`; Pi's `fetch` and the body stay in the host) a request of the transport got; the transport retries the request once with what the provider holds then.
    pub fn on_unauthorized(&self, status: u16, headers: &[(String, String)], server_url: &str, token: Option<&str>) -> io::Result<()> {
        let mut wire = json!({"status": status, "headers": headers.iter().map(|(name, value)| json!([name, value])).collect::<Vec<_>>(), "serverUrl": server_url});
        if let Some(token) = token.filter(|token| !token.is_empty()) {
            wire["token"] = json!(token);
        }
        self.context.call_host("sessionGraph.call", Some(json!({"handle": self.handle, "method": "onUnauthorized", "args": [wire]})))?;
        Ok(())
    }
}

/// Pi's `McpTransportSendOptions.signal`: it is aborted once the sender no longer waits for a reply.
#[derive(Clone, Default)]
pub struct McpSendSignal(Arc<AtomicBool>);

impl McpSendSignal {
    pub fn is_aborted(&self) -> bool {
        self.0.load(Ordering::SeqCst)
    }
}

type Listener<T> = Arc<dyn Fn(T) + Send + Sync>;

#[derive(Default)]
struct EventsInner {
    next: u64,
    messages: BTreeMap<u64, Listener<Value>>,
    errors: BTreeMap<u64, Listener<String>>,
    closes: BTreeMap<u64, Listener<()>>,
    close_emitted: bool,
}

/// Pi's `TransportEvents`: the listener bookkeeping a transport holds. `emit_*` call the listeners in registration order on the caller's thread; `emit_close` fires at most once.
#[derive(Clone, Default)]
pub struct McpTransportEvents(Arc<Mutex<EventsInner>>);

impl McpTransportEvents {
    pub fn on_message(&self, listener: Box<dyn Fn(Value) + Send + Sync>) -> Box<dyn FnOnce() + Send> {
        let mut inner = self.0.lock().unwrap();
        inner.next += 1;
        let id = inner.next;
        inner.messages.insert(id, Arc::from(listener));
        let shared = self.0.clone();
        Box::new(move || {
            shared.lock().unwrap().messages.remove(&id);
        })
    }

    pub fn on_error(&self, listener: Box<dyn Fn(String) + Send + Sync>) -> Box<dyn FnOnce() + Send> {
        let mut inner = self.0.lock().unwrap();
        inner.next += 1;
        let id = inner.next;
        inner.errors.insert(id, Arc::from(listener));
        let shared = self.0.clone();
        Box::new(move || {
            shared.lock().unwrap().errors.remove(&id);
        })
    }

    pub fn on_close(&self, listener: Box<dyn Fn() + Send + Sync>) -> Box<dyn FnOnce() + Send> {
        let mut inner = self.0.lock().unwrap();
        inner.next += 1;
        let id = inner.next;
        let listener: Arc<dyn Fn() + Send + Sync> = Arc::from(listener);
        inner.closes.insert(id, Arc::new(move |()| listener()));
        let shared = self.0.clone();
        Box::new(move || {
            shared.lock().unwrap().closes.remove(&id);
        })
    }

    pub fn emit_message(&self, message: Value) {
        let listeners: Vec<_> = self.0.lock().unwrap().messages.values().cloned().collect();
        for listener in listeners {
            listener(message.clone());
        }
    }

    pub fn emit_error(&self, error: &str) {
        let listeners: Vec<_> = self.0.lock().unwrap().errors.values().cloned().collect();
        for listener in listeners {
            listener(error.to_string());
        }
    }

    pub fn emit_close(&self) {
        let listeners: Vec<_> = {
            let mut inner = self.0.lock().unwrap();
            if inner.close_emitted {
                return;
            }
            inner.close_emitted = true;
            inner.closes.values().cloned().collect()
        };
        for listener in listeners {
            listener(());
        }
    }
}

/// Pi's `McpTransport` (packages/mcp/src/transports/transport.ts): the duplex message stream of one MCP connection. The host's client calls `start`, `send` and `close` from the host; the SDK delivers them to the transport in this process, registers its listeners, and delivers what the transport emits back, in order.
///
/// `send` writes one JSON-RPC message. It may wait for the peer's HTTP reply, so calls run concurrently; `signal` is aborted once the sender no longer waits for a reply. A transport that writes in the order the writes took their place, as a stdio pipe does, overrides `send_ordered`: it calls `placed` once every later send is written after `message`; the default calls `placed` just before `send`, as the in-tree HTTP transport does. A transport with Pi's optional `setProtocolVersion` overrides `sets_protocol_version` and `set_protocol_version`.
pub trait McpTransport: Send + Sync {
    fn start(&self) -> Result<(), String>;
    fn send(&self, message: Value, signal: &McpSendSignal) -> Result<(), String>;
    fn send_ordered(&self, message: Value, signal: &McpSendSignal, placed: &dyn Fn()) -> Result<(), String> {
        placed();
        self.send(message, signal)
    }
    fn close(&self) -> Result<(), String>;
    /// Registers a listener for each JSON-RPC message the transport receives; the returned function removes it.
    fn on_message(&self, listener: Box<dyn Fn(Value) + Send + Sync>) -> Box<dyn FnOnce() + Send>;
    /// Registers a listener for errors that do not close the transport.
    fn on_error(&self, listener: Box<dyn Fn(String) + Send + Sync>) -> Box<dyn FnOnce() + Send>;
    /// Registers a listener for the close, which the transport reports once.
    fn on_close(&self, listener: Box<dyn Fn() + Send + Sync>) -> Box<dyn FnOnce() + Send>;
    fn sets_protocol_version(&self) -> bool {
        false
    }
    fn set_protocol_version(&self, _version: &str) {}
}

/// Pi's `McpOAuthStateStore` plus the refresh lock of one server.
pub trait McpOAuthServerStore: Send + Sync {
    /// The stored state, `None` when there is none.
    fn load(&self) -> Result<Option<Value>, String>;
    fn save(&self, state: Value) -> Result<(), String>;
    /// Runs `f` while no other process refreshes the server's tokens.
    fn with_refresh_lock(&self, f: &mut dyn FnMut() -> Result<(), String>) -> Result<(), String>;
}

/// What the MCP extension asks of its `credentials` option (Pi's `McpOAuthCredentialStore`: per-server OAuth state in `mcp-auth.json` by default).
pub trait McpOAuthCredentialStore: Send + Sync {
    /// The store of the server with the given name and URL.
    fn for_server(&self, name: &str, server_url: &str) -> Result<Arc<dyn McpOAuthServerStore>, String>;
    /// The stored tokens of the server (an `OAuthTokens` object), `None` when there are none.
    fn tokens(&self, name: &str, server_url: &str) -> Result<Option<Value>, String>;
    /// Deletes the stored state of the server and returns whether credentials were stored for it.
    fn remove(&self, name: &str, server_url: &str) -> Result<bool, String>;
}

/// `loadConfig`: runs when a session starts; an error is the throw of Pi's `loadConfig`. Returns Pi's `LoadedMcpConfig` (`{servers, autoEnableCodemode?, errors, projectConfig?}`).
pub type McpLoadConfig = Arc<dyn Fn(McpLoadConfigContext) -> Result<Value, String> + Send + Sync>;
/// `createTransport`: creates the transport of a server entry (Pi's `McpServerEntry` as JSON). The auth provider is `None` for a server without OAuth and lives until the transport closes.
pub type McpTransportFactory = Arc<dyn Fn(Value, &str, Option<McpAuthProvider>) -> Result<Arc<dyn McpTransport>, String> + Send + Sync>;
/// `openUrl`: opens the OAuth authorization URL.
pub type McpOpenUrl = Arc<dyn Fn(&str) + Send + Sync>;
/// `updateConfig`: saves `/mcp` changes (`{enabled?, exposure?}`) to the server's config file; an error is the throw of Pi's `updateConfig`.
pub type McpUpdateConfig = Arc<dyn Fn(Value, Value) -> Result<(), String> + Send + Sync>;

/// The function options of `McpExtensionOptions`.
#[derive(Clone, Default)]
pub struct McpFunctionOptions {
    pub load_config: Option<McpLoadConfig>,
    pub create_transport: Option<McpTransportFactory>,
    pub credentials: Option<Arc<dyn McpOAuthCredentialStore>>,
    pub open_url: Option<McpOpenUrl>,
    pub update_config: Option<McpUpdateConfig>,
}

impl McpFunctionOptions {
    pub(crate) fn is_empty(&self) -> bool {
        self.load_config.is_none() && self.create_transport.is_none() && self.credentials.is_none() && self.open_url.is_none() && self.update_config.is_none()
    }

    /// Names each function to the host in `wire`, the option object of the reference.
    pub(crate) fn register(&self, context: &Context, wire: &mut Map<String, Value>) {
        if let Some(load_config) = self.load_config.clone() {
            let callback: GraphCallback = Arc::new(move |args| {
                let ctx = args.first().cloned().unwrap_or(Value::Null);
                load_config(McpLoadConfigContext { cwd: ctx["cwd"].as_str().unwrap_or_default().to_string(), is_project_trusted: ctx["isProjectTrusted"].as_bool().unwrap_or(false) })
            });
            wire.insert("loadConfig".into(), json!(add_callback(context, callback)));
        }
        if let Some(open_url) = self.open_url.clone() {
            let callback: GraphCallback = Arc::new(move |args| {
                open_url(args.first().and_then(Value::as_str).unwrap_or_default());
                Ok(Value::Null)
            });
            wire.insert("openUrl".into(), json!(add_callback(context, callback)));
        }
        if let Some(update_config) = self.update_config.clone() {
            let callback: GraphCallback = Arc::new(move |args| {
                update_config(args.first().cloned().unwrap_or(Value::Null), args.get(1).cloned().unwrap_or(Value::Null))?;
                Ok(Value::Null)
            });
            wire.insert("updateConfig".into(), json!(add_callback(context, callback)));
        }
        if let Some(create_transport) = self.create_transport.clone() {
            let owner = context.clone();
            let callback: GraphCallback = Arc::new(move |args| {
                let entry = args.first().cloned().unwrap_or(Value::Null);
                let cwd = args.get(1).and_then(Value::as_str).unwrap_or_default().to_string();
                let handle = args.get(2).and_then(Value::as_str).unwrap_or_default().to_string();
                let provider = (!handle.is_empty()).then(|| McpAuthProvider { context: owner.clone(), handle });
                let transport = create_transport(entry, &cwd, provider)?;
                let sets_version = transport.sets_protocol_version();
                let shim = TransportShim::new(&owner, transport);
                Ok(json!({"transport": shim.id.lock().unwrap().clone(), "setsProtocolVersion": sets_version}))
            });
            wire.insert("createTransport".into(), json!(add_callback(context, callback)));
        }
        if let Some(credentials) = self.credentials.clone() {
            let shim = Arc::new(CredentialsShim { store: credentials, state: Mutex::new(CredentialState::default()), next: AtomicU64::new(0) });
            let callback: GraphCallback = Arc::new(move |args| shim.serve(&args));
            wire.insert("credentials".into(), json!(add_callback(context, callback)));
        }
    }
}

#[derive(Default)]
struct ShimState {
    events: Vec<Value>,
    closed: bool,
    aborts: HashMap<u64, McpSendSignal>,
}

/// Serves one transport of the caller to the host: the host's ops arrive as one session graph callback, and what the transport produces is queued for the host's long poll (`receive`).
struct TransportShim {
    id: Mutex<String>,
    context: Context,
    transport: Arc<dyn McpTransport>,
    state: Mutex<ShimState>,
    changed: Condvar,
}

impl TransportShim {
    fn new(context: &Context, transport: Arc<dyn McpTransport>) -> Arc<TransportShim> {
        let shim = Arc::new(TransportShim { id: Mutex::new(String::new()), context: context.clone(), transport: transport.clone(), state: Mutex::new(ShimState::default()), changed: Condvar::new() });
        let (on_message, on_error, on_close) = (shim.clone(), shim.clone(), shim.clone());
        let _ = transport.on_message(Box::new(move |message| on_message.push(json!({"type": "message", "message": message}))));
        let _ = transport.on_error(Box::new(move |error| on_error.push(json!({"type": "error", "error": error}))));
        let _ = transport.on_close(Box::new(move || on_close.push_close()));
        let served = shim.clone();
        let callback: GraphCallback = Arc::new(move |args| served.serve(&args));
        *shim.id.lock().unwrap() = add_callback(context, callback);
        shim
    }

    fn push(&self, event: Value) {
        let mut state = self.state.lock().unwrap();
        if !state.closed {
            state.events.push(event);
        }
        self.changed.notify_all();
    }

    /// Queues the close once; nothing is queued after it.
    fn push_close(&self) {
        let mut state = self.state.lock().unwrap();
        if !state.closed {
            state.events.push(json!({"type": "close"}));
            state.closed = true;
        }
        self.changed.notify_all();
    }

    fn serve(&self, args: &[Value]) -> Result<Value, String> {
        match args.first().and_then(Value::as_str).unwrap_or_default() {
            "start" => self.transport.start().map(|()| Value::Null),
            "setProtocolVersion" => {
                if self.transport.sets_protocol_version() {
                    self.transport.set_protocol_version(args.get(1).and_then(Value::as_str).unwrap_or_default());
                }
                Ok(Value::Null)
            }
            "receive" => Ok(self.receive()),
            "send" => self.send(args).map(|()| Value::Null),
            "abort" => {
                let seq = args.get(1).and_then(Value::as_u64).unwrap_or_default();
                if let Some(signal) = self.state.lock().unwrap().aborts.get(&seq) {
                    signal.0.store(true, Ordering::SeqCst);
                }
                Ok(Value::Null)
            }
            "close" => {
                let closed = self.transport.close();
                self.push_close();
                // The host closed the transport and waits for its poll to see the close; the id is not used again.
                remove_callback(&self.context, &self.id.lock().unwrap());
                closed.map(|()| Value::Null)
            }
            op => Err(format!("an MCP transport has no operation {op}")),
        }
    }

    /// Answers the host's poll: the events since the last poll, or the next ones when there are none.
    fn receive(&self) -> Value {
        let mut state = self.state.lock().unwrap();
        loop {
            if !state.events.is_empty() {
                let events = std::mem::take(&mut state.events);
                return json!({"events": events});
            }
            if state.closed {
                return json!({"events": [{"type": "close"}]});
            }
            state = self.changed.wait(state).unwrap();
        }
    }

    fn send(&self, args: &[Value]) -> Result<(), String> {
        let seq = args.get(1).and_then(Value::as_u64).ok_or("send takes a sequence number, a message and the ordered flag")?;
        let message = args.get(2).cloned().ok_or("send takes a sequence number, a message and the ordered flag")?;
        let ordered = args.get(3).and_then(Value::as_bool).unwrap_or(false);
        let signal = McpSendSignal::default();
        self.state.lock().unwrap().aborts.insert(seq, signal.clone());
        let placed_once = AtomicBool::new(false);
        let placed = || {
            if !placed_once.swap(true, Ordering::SeqCst) && ordered {
                self.push(json!({"type": "placed", "seq": seq}));
            }
        };
        let result = self.transport.send_ordered(message, &signal, &placed);
        // A transport that never calls placed must not hold up the sends after this one.
        placed();
        self.state.lock().unwrap().aborts.remove(&seq);
        signal.0.store(true, Ordering::SeqCst);
        result
    }
}

#[derive(Default)]
struct CredentialState {
    stores: HashMap<String, Arc<dyn McpOAuthServerStore>>,
    locks: HashMap<String, (mpsc::Sender<Option<String>>, mpsc::Receiver<Result<(), String>>)>,
}

/// Serves the caller's credential store: the host's ops arrive as one callback.
struct CredentialsShim {
    store: Arc<dyn McpOAuthCredentialStore>,
    state: Mutex<CredentialState>,
    next: AtomicU64,
}

impl CredentialsShim {
    fn serve(&self, args: &[Value]) -> Result<Value, String> {
        let text = |index: usize| args.get(index).and_then(Value::as_str).unwrap_or_default().to_string();
        let op = text(0);
        match op.as_str() {
            "forServer" => {
                let store = self.store.for_server(&text(1), &text(2))?;
                let id = format!("s{}", self.next.fetch_add(1, Ordering::Relaxed) + 1);
                self.state.lock().unwrap().stores.insert(id.clone(), store);
                return Ok(json!({"store": id}));
            }
            "tokens" => return self.store.tokens(&text(1), &text(2)).map(|tokens| tokens.unwrap_or(Value::Null)),
            "remove" => return self.store.remove(&text(1), &text(2)).map(Value::Bool),
            _ => {}
        }
        let id = text(1);
        if op == "release" {
            self.state.lock().unwrap().stores.remove(&id);
            return Ok(Value::Null);
        }
        if op == "unlock" {
            return self.unlock(&text(2), args.get(3).and_then(Value::as_str).unwrap_or_default(), args.get(4).and_then(Value::as_bool).unwrap_or(false));
        }
        let store = self.state.lock().unwrap().stores.get(&id).cloned().ok_or_else(|| format!("unknown credential store {id}"))?;
        match op.as_str() {
            "load" => store.load().map(|state| state.unwrap_or(Value::Null)),
            "save" => store.save(args.get(2).cloned().unwrap_or(Value::Null)).map(|()| Value::Null),
            "lock" => self.lock(&id, store),
            other => Err(format!("a credential store has no operation {other}")),
        }
    }

    /// Runs the store's `with_refresh_lock` with a function that waits for the host's unlock, and answers when the function entered.
    fn lock(&self, store_id: &str, store: Arc<dyn McpOAuthServerStore>) -> Result<Value, String> {
        let (entered_tx, entered_rx) = mpsc::channel::<()>();
        let (unlock_tx, unlock_rx) = mpsc::channel::<Option<String>>();
        let (done_tx, done_rx) = mpsc::channel::<Result<(), String>>();
        std::thread::spawn(move || {
            let result = store.with_refresh_lock(&mut || {
                let _ = entered_tx.send(());
                match unlock_rx.recv() {
                    Ok(None) => Ok(()),
                    Ok(Some(failure)) => Err(failure),
                    Err(_) => Err("the host went away while it held the refresh lock".to_string()),
                }
            });
            let _ = done_tx.send(result);
        });
        if entered_rx.recv().is_err() {
            // The store's with_refresh_lock returned without running the function: it could not lock.
            return match done_rx.recv() {
                Ok(Err(error)) => Err(error),
                _ => Err("with_refresh_lock returned without running its function".to_string()),
            };
        }
        let lock_id = format!("{store_id}.{}", self.next.fetch_add(1, Ordering::Relaxed) + 1);
        self.state.lock().unwrap().locks.insert(lock_id.clone(), (unlock_tx, done_rx));
        Ok(json!({"lock": lock_id}))
    }

    fn unlock(&self, lock_id: &str, failure: &str, failed: bool) -> Result<Value, String> {
        let (unlock, done) = self.state.lock().unwrap().locks.remove(lock_id).ok_or_else(|| format!("unknown refresh lock {lock_id}"))?;
        let _ = unlock.send(failed.then(|| failure.to_string()));
        done.recv().map_err(|_| "the refresh lock's function vanished".to_string())?.map(|()| Value::Null)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn transport_events_deliver_in_registration_order_and_close_once() {
        let events = McpTransportEvents::default();
        let seen = Arc::new(Mutex::new(Vec::<String>::new()));
        let first = seen.clone();
        let second = seen.clone();
        let closes = seen.clone();
        let dispose = events.on_message(Box::new(move |message| first.lock().unwrap().push(format!("a{message}"))));
        let _ = events.on_message(Box::new(move |message| second.lock().unwrap().push(format!("b{message}"))));
        let _ = events.on_close(Box::new(move || closes.lock().unwrap().push("close".into())));
        events.emit_message(json!(1));
        dispose();
        events.emit_message(json!(2));
        events.emit_close();
        events.emit_close();
        assert_eq!(*seen.lock().unwrap(), vec!["a1", "b1", "b2", "close"]);
    }

    struct Store {
        held: Arc<AtomicBool>,
    }

    impl McpOAuthServerStore for Store {
        fn load(&self) -> Result<Option<Value>, String> {
            Ok(None)
        }
        fn save(&self, _state: Value) -> Result<(), String> {
            Ok(())
        }
        fn with_refresh_lock(&self, f: &mut dyn FnMut() -> Result<(), String>) -> Result<(), String> {
            self.held.store(true, Ordering::SeqCst);
            let result = f();
            self.held.store(false, Ordering::SeqCst);
            result
        }
    }

    struct Credentials {
        held: Arc<AtomicBool>,
    }

    impl McpOAuthCredentialStore for Credentials {
        fn for_server(&self, _name: &str, _server_url: &str) -> Result<Arc<dyn McpOAuthServerStore>, String> {
            Ok(Arc::new(Store { held: self.held.clone() }))
        }
        fn tokens(&self, _name: &str, _server_url: &str) -> Result<Option<Value>, String> {
            Ok(None)
        }
        fn remove(&self, _name: &str, _server_url: &str) -> Result<bool, String> {
            Ok(false)
        }
    }

    /// The refresh lock is held by the caller's store for as long as the host holds it: `lock` answers once inside, `unlock` returns the function's outcome.
    #[test]
    fn refresh_lock_is_held_until_the_host_unlocks() {
        let held = Arc::new(AtomicBool::new(false));
        let shim = CredentialsShim { store: Arc::new(Credentials { held: held.clone() }), state: Mutex::new(CredentialState::default()), next: AtomicU64::new(0) };
        let made = shim.serve(&[json!("forServer"), json!("demo"), json!("https://x.example")]).unwrap();
        let store = made["store"].as_str().unwrap().to_string();
        let locked = shim.serve(&[json!("lock"), json!(store)]).unwrap();
        assert!(held.load(Ordering::SeqCst), "lock answered outside the store's with_refresh_lock");
        let lock = locked["lock"].as_str().unwrap().to_string();
        let failure = shim.serve(&[json!("unlock"), json!(store), json!(lock), json!("refresh failed"), json!(true)]);
        assert_eq!(failure, Err("refresh failed".to_string()));
        assert!(!held.load(Ordering::SeqCst), "the store still holds the lock after unlock");
        assert!(shim.serve(&[json!("unlock"), json!(store), json!(lock), json!(""), json!(false)]).is_err(), "a second unlock of the same lock succeeded");
    }
}

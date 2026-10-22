//! Subscriptions to host state the SDK replicates (theme, tool expansion), the tool-call accessors a handler reads
//! (`tool_call_index`, `tool_input`, `tool_params`), `bell`, `extension_source_info` and `on_loaded`.

use crate::context::{Context, SourceInfo};
use crate::extension::RequestThreads;
use crate::theme::Theme;
use serde_json::{Map, Value};
use std::collections::VecDeque;
use std::io;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;

type Handler<T> = Arc<dyn Fn(T) + Send + Sync>;

struct FeedState<T> {
    subs: Vec<(u64, Handler<T>)>,
    queue: VecDeque<(T, Vec<Handler<T>>)>,
    running: bool,
    stopped: bool,
}

/// Delivers each change of a value the host replicates to the handlers subscribed. Upstream Pi calls a widget component's `invalidate()` on such a change and the component reads the new value at its next render; a subprocess extension has no component to invalidate, so it subscribes.
///
/// Handlers run on a worker thread, one delivery at a time in the order the changes arrived, never on the thread that reads the host's replies, so a handler can make a blocking host call. A handler that panics does not stop the others. An unsubscribe changes only later deliveries: a delivery already queued still reaches the handlers subscribed when the change arrived.
pub(crate) struct Feed<T: Clone + Send + 'static> {
    state: Mutex<FeedState<T>>,
    next_id: AtomicU64,
    what: &'static str,
}

impl<T: Clone + Send + 'static> Feed<T> {
    fn new(what: &'static str) -> Self {
        Self {
            state: Mutex::new(FeedState { subs: Vec::new(), queue: VecDeque::new(), running: false, stopped: false }),
            next_id: AtomicU64::new(1),
            what,
        }
    }

    fn subscribe(self: &Arc<Self>, handler: Handler<T>) -> HostSubscription
    where
        T: 'static,
    {
        let id = self.next_id.fetch_add(1, Ordering::SeqCst);
        self.state.lock().unwrap().subs.push((id, handler));
        let feed = self.clone();
        HostSubscription { release: Some(Box::new(move || feed.state.lock().unwrap().subs.retain(|(sub, _)| *sub != id))) }
    }

    /// Queue `value` for the handlers subscribed now.
    pub(crate) fn publish(self: &Arc<Self>, value: T, threads: &Arc<RequestThreads>) {
        {
            let mut state = self.state.lock().unwrap();
            if state.stopped || state.subs.is_empty() {
                return;
            }
            let handlers = state.subs.iter().map(|(_, handler)| handler.clone()).collect();
            state.queue.push_back((value, handlers));
            if state.running {
                return;
            }
            state.running = true;
        }
        threads.start();
        let (feed, finished) = (self.clone(), threads.clone());
        let spawned = thread::Builder::new().name(format!("pig-{}-change", self.what)).spawn(move || {
            feed.run();
            finished.finish();
        });
        if spawned.is_err() {
            let mut state = self.state.lock().unwrap();
            state.running = false;
            state.queue.clear();
            drop(state);
            threads.finish();
        }
    }

    fn run(&self) {
        loop {
            let (value, handlers) = {
                let mut state = self.state.lock().unwrap();
                match state.queue.pop_front() {
                    Some(delivery) if !state.stopped => delivery,
                    _ => {
                        state.queue.clear();
                        state.running = false;
                        return;
                    }
                }
            };
            for handler in handlers {
                let value = value.clone();
                let _ = catch_unwind(AssertUnwindSafe(|| handler(value)));
            }
        }
    }

    fn stop(&self) {
        let mut state = self.state.lock().unwrap();
        state.stopped = true;
        state.queue.clear();
    }
}

/// Unsubscribes the handler it was returned for when dropped.
pub struct HostSubscription {
    release: Option<Box<dyn FnOnce() + Send>>,
}

impl HostSubscription {
    /// Unsubscribe now. Idempotent; dropping the guard afterwards does nothing.
    pub fn unsubscribe(mut self) {
        if let Some(release) = self.release.take() {
            release();
        }
    }
}

impl Drop for HostSubscription {
    fn drop(&mut self) {
        if let Some(release) = self.release.take() {
            release();
        }
    }
}

/// An `on_loaded` handler.
pub(crate) type LoadedHandler = Box<dyn Fn(&Context) + Send + Sync>;

/// Holds the extension's requests back until its `on_loaded` handlers returned.
pub(crate) struct LoadedGate {
    done: Mutex<bool>,
    changed: std::sync::Condvar,
}

impl LoadedGate {
    pub(crate) fn new(done: bool) -> Self {
        Self { done: Mutex::new(done), changed: std::sync::Condvar::new() }
    }

    pub(crate) fn wait(&self) {
        let mut done = self.done.lock().unwrap();
        while !*done {
            done = self.changed.wait(done).unwrap();
        }
    }

    pub(crate) fn open(&self) {
        *self.done.lock().unwrap() = true;
        self.changed.notify_all();
    }
}

/// The extension-wide state behind the subscriptions of this module, shared by every context.
pub(crate) struct HostSubscriptions {
    pub(crate) theme: Arc<Feed<Theme>>,
    pub(crate) tools_expanded: Arc<Feed<bool>>,
    pub(crate) threads: Arc<RequestThreads>,
    /// The provenance the host stamped on this extension, from the ready payload.
    pub(crate) source_info: Mutex<Option<SourceInfo>>,
}

impl HostSubscriptions {
    pub(crate) fn new(threads: Arc<RequestThreads>) -> Self {
        Self {
            theme: Arc::new(Feed::new("theme")),
            tools_expanded: Arc::new(Feed::new("tools expanded")),
            threads,
            source_info: Mutex::new(None),
        }
    }

    pub(crate) fn stop(&self) {
        self.theme.stop();
        self.tools_expanded.stop();
    }

    /// Deliver what a state snapshot or a `theme_change` changed.
    pub(crate) fn publish(&self, change: UiChange) {
        if let Some(theme) = change.theme {
            self.theme.publish(theme, &self.threads);
        }
        if let Some(expanded) = change.tools_expanded {
            self.tools_expanded.publish(expanded, &self.threads);
        }
    }
}

/// What applying a snapshot changed in the replicated UI state: the new theme and the new tool-expansion state, each only when it differs from the one before.
#[derive(Default)]
pub(crate) struct UiChange {
    pub(crate) theme: Option<Theme>,
    pub(crate) tools_expanded: Option<bool>,
}

impl Context {
    /// Subscribe to changes of the host's theme: the user switching theme, a custom theme's file reloading, the terminal reporting new colors. `handler` receives the new theme after [`Context::theme`] was updated.
    ///
    /// Pi calls a widget component's `invalidate()` on a theme change, and the component reads `ctx.ui.theme` at its next render, so it never paints the colors of an old theme. A subprocess extension pushes rows that were colored when it built them, so it re-renders from this subscription. [`Self::set_widget_renderer`] colors through the live theme at every render and needs no handler for it.
    ///
    /// Handlers run on a worker thread, one at a time in the order the changes arrived, never on the thread that reads the host's replies, so a handler can make a blocking host call. A handler that panics does not stop the others. The returned guard unsubscribes when dropped; a delivery already queued still reaches the handlers subscribed when the change arrived. A palette equal to the one before is not a change.
    pub fn on_theme_change<F>(&self, handler: F) -> HostSubscription
    where
        F: Fn(Theme) + Send + Sync + 'static,
    {
        self.host_subs.theme.subscribe(Arc::new(handler))
    }

    /// Subscribe to the host toggling tool-output expansion (Pi's expand key, or `set_tools_expanded` from any extension). `handler` receives the new state after it changed.
    ///
    /// The original of a widget reads `ui.getToolsExpanded()` at every frame and Pi redraws on its expand key; a subprocess extension pushes rows built for one state and re-renders from this subscription. It follows the contract of [`Self::on_theme_change`].
    pub fn on_tools_expanded_change<F>(&self, handler: F) -> HostSubscription
    where
        F: Fn(bool) + Send + Sync + 'static,
    {
        self.host_subs.tools_expanded.subscribe(Arc::new(handler))
    }

    /// Ring the terminal bell (BEL) of the interactive terminal between frames, the call a Pi extension makes by writing `"\x07"` to `process.stdout`. It does nothing where there is no terminal UI: print, JSON and RPC modes. The error reports a failure to reach the host only.
    pub fn bell(&self) -> io::Result<()> {
        crate::context::call_result_to_io(self.call_wire("ui.bell", None)?)
    }

    /// The provenance the loader stamped on this extension: upstream's `extension.sourceInfo` (`types.ts` `SourceInfo`), where `ExtensionRunner.getExtensionPaths` reads the extension's path from. `path` is the entry file and `base_dir` the package root when the extension came from a Package; a Pi extension reads both from `import.meta.url`, which a subprocess extension has no equivalent of. `None` when the host holds no source for this extension.
    pub fn extension_source_info(&self) -> Option<SourceInfo> {
        self.host_subs.source_info.lock().unwrap().clone()
    }

    /// The running tool call's position among the tool calls of the assistant message that issued it, the batch agent-loop.ts executes (the `toolCall` content blocks of that message, counted from 0). `None` outside a tool handler and for a call no assistant message issued, such as a nested [`Self::execute_tool`] call. The host sends it with the call, so a batch is ordered without reading the session's leaf entry.
    pub fn tool_call_index(&self) -> Option<usize> {
        self.tool_call_index.map(|index| index as usize)
    }

    /// The running tool call's arguments as the JSON text `JSON.stringify` gives for the object Pi hands `execute`: members in the order the model wrote them (array-index keys first, ascending, as JavaScript orders them), numbers as JavaScript prints them. The arguments are the ones the host sent the handler, after the tool's `prepare_arguments`. `None` outside a tool handler.
    pub fn tool_input(&self) -> Option<String> {
        crate::serialize_conversation::js_stringify_text(self.tool_input.as_deref()?)
    }

    /// The running tool call's arguments with every object, at any depth, in the member order JavaScript's `Object.keys` gives (array-index keys ascending, then the others in insertion order), which Pi's `execute` params keep and an extension that writes the arguments back out (`JSON.stringify(params.metadata)`) relies on. `None` outside a tool handler or when the arguments are not an object.
    pub fn tool_params(&self) -> Option<Map<String, Value>> {
        let Value::Object(object) = js_ordered(serde_json::from_str(self.tool_input.as_deref()?).ok()?) else {
            return None;
        };
        Some(object)
    }
}

/// `value` with every object's members in the order JavaScript's `Object.keys` gives.
fn js_ordered(value: Value) -> Value {
    match value {
        Value::Object(object) => {
            let (mut indexes, others): (Vec<_>, Vec<_>) = object.into_iter().partition(|(key, _)| is_array_index(key));
            indexes.sort_by_key(|(key, _)| key.parse::<u64>().unwrap_or(0));
            Value::Object(indexes.into_iter().chain(others).map(|(key, value)| (key, js_ordered(value))).collect())
        }
        Value::Array(items) => Value::Array(items.into_iter().map(js_ordered).collect()),
        other => other,
    }
}

fn is_array_index(key: &str) -> bool {
    key == "0" || (!key.is_empty() && key.len() <= 10 && !key.starts_with('0') && key.bytes().all(|b| b.is_ascii_digit()) && key.parse::<u64>().is_ok_and(|n| n <= u64::from(u32::MAX) - 1))
}

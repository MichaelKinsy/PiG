//! Pi's `ReadonlyFooterDataProvider` (footer-data-provider.ts:103-163,387): the footer data Pi hands to a
//! `ui.setFooter` factory as its third argument. A Rust footer renderer reads it from the context it was
//! installed with; [`crate::Context::set_footer_renderer`] renders again when the host's footer data changes.
//! The values replicate the host's state updates, so reads never call the host.

use std::collections::{BTreeMap, HashMap};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};

use serde_json::{Map, Value};

type BranchCallback = Arc<dyn Fn() + Send + Sync>;

#[derive(Default, Clone, PartialEq)]
struct Snapshot {
    branch: Option<String>,
    statuses: BTreeMap<String, String>,
    providers: u32,
}

/// The replicated footer data and the `onBranchChange` subscribers of one connection.
#[derive(Default)]
pub(crate) struct FooterState {
    snapshot: Mutex<Snapshot>,
    subscribers: Mutex<HashMap<u64, BranchCallback>>,
    next: AtomicU64,
}

impl FooterState {
    /// Install the footer data of a state update and report whether it changed and whether the branch did. A state without footer data keeps the last one.
    pub(crate) fn apply(&self, state: &Map<String, Value>) -> (bool, bool) {
        let Some(data) = state.get("footerData").and_then(Value::as_object) else {
            return (false, false);
        };
        let next = Snapshot {
            branch: data.get("gitBranch").and_then(Value::as_str).filter(|branch| !branch.is_empty()).map(str::to_string),
            statuses: data
                .get("extensionStatuses")
                .and_then(Value::as_object)
                .map(|statuses| statuses.iter().filter_map(|(key, text)| text.as_str().map(|text| (key.clone(), text.to_string()))).collect())
                .unwrap_or_default(),
            providers: data.get("availableProviderCount").and_then(Value::as_u64).unwrap_or(0) as u32,
        };
        let mut current = self.snapshot.lock().unwrap();
        let branch_changed = next.branch != current.branch;
        let changed = next != *current;
        *current = next;
        (changed, branch_changed)
    }

    /// Run the branch subscribers in subscription order. A panic in one is reported through `report`, as the Node runtime reports a failing subscriber, and does not stop the others.
    pub(crate) fn notify_branch(&self, report: impl Fn(String)) {
        let mut callbacks: Vec<(u64, BranchCallback)> = self.subscribers.lock().unwrap().iter().map(|(id, callback)| (*id, callback.clone())).collect();
        callbacks.sort_by_key(|(id, _)| *id);
        for (_, callback) in callbacks {
            if let Err(panic) = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| callback())) {
                let message = panic.downcast_ref::<&str>().map(|text| text.to_string()).or_else(|| panic.downcast_ref::<String>().cloned()).unwrap_or_default();
                report(format!("footer branch subscriber failed: {message}"));
            }
        }
    }
}

/// Pi's `ReadonlyFooterDataProvider`.
#[derive(Clone)]
pub struct FooterData {
    pub(crate) state: Arc<crate::protocol::Connection>,
}

impl FooterData {
    /// The current Git branch, or `None` when there is none (Pi's null).
    pub fn git_branch(&self) -> Option<String> {
        self.state.footer.snapshot.lock().unwrap().branch.clone()
    }

    /// Every extension's status text by key, in key order (the host reports them sorted).
    pub fn extension_statuses(&self) -> BTreeMap<String, String> {
        self.state.footer.snapshot.lock().unwrap().statuses.clone()
    }

    /// The number of providers with configured auth.
    pub fn available_provider_count(&self) -> u32 {
        self.state.footer.snapshot.lock().unwrap().providers
    }

    /// Run `callback` when the Git branch changes. The guard unsubscribes when dropped, as [`crate::Context::on_width_change`]'s does.
    pub fn on_branch_change<F>(&self, callback: F) -> BranchChangeSubscription
    where
        F: Fn() + Send + Sync + 'static,
    {
        let id = self.state.footer.next.fetch_add(1, Ordering::SeqCst);
        self.state.footer.subscribers.lock().unwrap().insert(id, Arc::new(callback));
        BranchChangeSubscription { id, state: self.state.clone(), released: false }
    }
}

/// Unsubscribes a branch-change callback when dropped.
pub struct BranchChangeSubscription {
    id: u64,
    state: Arc<crate::protocol::Connection>,
    released: bool,
}

impl BranchChangeSubscription {
    /// Unsubscribe now. Idempotent; dropping the guard afterwards does nothing.
    pub fn unsubscribe(mut self) {
        self.release();
    }

    fn release(&mut self) {
        if !self.released {
            self.released = true;
            self.state.footer.subscribers.lock().unwrap().remove(&self.id);
        }
    }
}

impl Drop for BranchChangeSubscription {
    fn drop(&mut self) {
        self.release();
    }
}

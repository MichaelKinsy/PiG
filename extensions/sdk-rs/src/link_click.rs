//! The OSC 8 link claims of an extension (pig additive D193): the handler of each scheme, and the clicks waiting
//! for their worker thread. A handler may block on a host call, whose reply only the main loop reads, so handlers
//! never run on the main loop: one worker runs the queued clicks in the order the host sent them and exits when
//! the queue is empty.

use std::collections::{HashMap, VecDeque};
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;

use crate::extension::RequestThreads;

pub(crate) type LinkClickHandler = Box<dyn Fn(&str) + Send + Sync>;

#[derive(Default)]
pub(crate) struct LinkClicks {
    seq: AtomicU64,
    state: Mutex<LinkClickState>,
}

#[derive(Default)]
struct LinkClickState {
    claims: HashMap<String, (u64, Arc<LinkClickHandler>)>,
    queue: VecDeque<(Arc<LinkClickHandler>, String)>,
    running: bool,
    stopped: bool,
}

impl LinkClicks {
    /// Claims `scheme` for `handler` and returns the claim's id and the claim it replaced.
    pub(crate) fn claim(&self, scheme: &str, handler: LinkClickHandler) -> (u64, Option<(u64, Arc<LinkClickHandler>)>) {
        let id = self.seq.fetch_add(1, Ordering::SeqCst) + 1;
        let previous = self.state.lock().unwrap().claims.insert(scheme.to_string(), (id, Arc::new(handler)));
        (id, previous)
    }

    /// Takes a failed claim back: the claim it replaced returns, if nothing newer holds the scheme.
    pub(crate) fn restore(&self, scheme: &str, id: u64, previous: Option<(u64, Arc<LinkClickHandler>)>) {
        let mut state = self.state.lock().unwrap();
        if state.claims.get(scheme).map(|(held, _)| *held) != Some(id) {
            return;
        }
        match previous {
            Some(claim) => {
                state.claims.insert(scheme.to_string(), claim);
            }
            None => {
                state.claims.remove(scheme);
            }
        }
    }

    /// Gives `scheme` back when claim `id` still holds it, and reports whether it did.
    pub(crate) fn release(&self, scheme: &str, id: u64) -> bool {
        let mut state = self.state.lock().unwrap();
        if state.claims.get(scheme).map(|(held, _)| *held) != Some(id) {
            return false;
        }
        state.claims.remove(scheme);
        true
    }

    /// Queues a click for the scheme's handler.
    pub(crate) fn submit(self: &Arc<Self>, scheme: &str, url: String, threads: &Arc<RequestThreads>) {
        {
            let mut state = self.state.lock().unwrap();
            if state.stopped {
                return;
            }
            let Some((_, handler)) = state.claims.get(scheme).cloned() else {
                return;
            };
            state.queue.push_back((handler, url));
            if state.running {
                return;
            }
            state.running = true;
        }
        threads.start();
        let (clicks, finished) = (self.clone(), threads.clone());
        let spawned = thread::Builder::new().name("pig-link-click".to_string()).spawn(move || {
            clicks.run();
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
            let (handler, url) = {
                let mut state = self.state.lock().unwrap();
                match state.queue.pop_front() {
                    Some(click) if !state.stopped => click,
                    _ => {
                        state.queue.clear();
                        state.running = false;
                        return;
                    }
                }
            };
            // One click's panic must not stop the next.
            let _ = catch_unwind(AssertUnwindSafe(|| handler(&url)));
        }
    }

    pub(crate) fn stop(&self) {
        let mut state = self.state.lock().unwrap();
        state.stopped = true;
        state.queue.clear();
    }
}

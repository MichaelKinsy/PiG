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

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::mpsc;
    use std::time::Duration;

    fn recording(tx: mpsc::Sender<String>, label: &'static str) -> LinkClickHandler {
        Box::new(move |url| {
            let _ = tx.send(format!("{label}:{url}"));
        })
    }

    #[test]
    fn clicks_reach_the_claimed_scheme_in_order_and_other_schemes_reach_nobody() {
        let clicks = Arc::new(LinkClicks::default());
        let threads = Arc::new(RequestThreads::default());
        let (tx, rx) = mpsc::channel();
        clicks.claim("probe", recording(tx, "h"));
        for url in ["probe:1", "probe:2", "probe:3"] {
            clicks.submit("probe", url.to_string(), &threads);
        }
        clicks.submit("other", "other:1".to_string(), &threads);
        let got: Vec<String> = (0..3).map(|_| rx.recv_timeout(Duration::from_secs(5)).unwrap()).collect();
        assert_eq!(got, ["h:probe:1", "h:probe:2", "h:probe:3"]);
        assert!(rx.recv_timeout(Duration::from_millis(50)).is_err(), "a click on another scheme reached a handler");
    }

    #[test]
    fn a_handler_that_panics_does_not_stop_the_next_click() {
        let clicks = Arc::new(LinkClicks::default());
        let threads = Arc::new(RequestThreads::default());
        let (tx, rx) = mpsc::channel();
        clicks.claim(
            "probe",
            Box::new(move |url| {
                if url == "probe:boom" {
                    panic!("handler exploded");
                }
                let _ = tx.send(url.to_string());
            }),
        );
        clicks.submit("probe", "probe:boom".to_string(), &threads);
        clicks.submit("probe", "probe:after".to_string(), &threads);
        assert_eq!(rx.recv_timeout(Duration::from_secs(5)).unwrap(), "probe:after");
    }

    #[test]
    fn release_gives_the_scheme_back_once_and_only_to_the_claim_that_holds_it() {
        let clicks = Arc::new(LinkClicks::default());
        let (tx, _rx) = mpsc::channel();
        let (first, _) = clicks.claim("probe", recording(tx.clone(), "first"));
        let (second, replaced) = clicks.claim("probe", recording(tx, "second"));
        assert_eq!(replaced.map(|(id, _)| id), Some(first));
        assert!(!clicks.release("probe", first), "a replaced claim released the scheme of the claim that replaced it");
        assert!(clicks.release("probe", second));
        assert!(!clicks.release("probe", second), "the second release took something back");
    }

    #[test]
    fn a_refused_claim_restores_the_earlier_one_unless_a_newer_claim_holds_the_scheme() {
        let clicks = Arc::new(LinkClicks::default());
        let threads = Arc::new(RequestThreads::default());
        let (tx, rx) = mpsc::channel();
        clicks.claim("probe", recording(tx.clone(), "first"));
        let (refused, previous) = clicks.claim("probe", recording(tx.clone(), "refused"));
        clicks.restore("probe", refused, previous);
        clicks.submit("probe", "probe:1".to_string(), &threads);
        assert_eq!(rx.recv_timeout(Duration::from_secs(5)).unwrap(), "first:probe:1");

        let (stale, previous) = clicks.claim("probe", recording(tx.clone(), "stale"));
        let (newer, _) = clicks.claim("probe", recording(tx, "newer"));
        clicks.restore("probe", stale, previous);
        assert!(clicks.release("probe", newer), "restoring a claim that was already replaced took the newer claim's scheme");
    }

    #[test]
    fn stop_drops_the_queued_clicks() {
        let clicks = Arc::new(LinkClicks::default());
        let threads = Arc::new(RequestThreads::default());
        let (tx, rx) = mpsc::channel();
        clicks.claim("probe", recording(tx, "h"));
        clicks.stop();
        clicks.submit("probe", "probe:1".to_string(), &threads);
        assert!(rx.recv_timeout(Duration::from_millis(50)).is_err(), "a click after stop reached the handler");
    }
}

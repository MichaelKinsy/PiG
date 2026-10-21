//! Starts the handlers of the requests that run extension code in the order the requests arrive: `tool_call`, `event` and `command` (`starts_in_arrival_order`).
//!
//! Pi starts every call of a parallel batch through `Promise.all(calls.map(...))` and each reaches `tool.execute` synchronously, so
//! the handlers start in source order (`agent-loop.ts:619-647, 820-837`); concurrent emits, such as the `tool_result` hooks of that
//! batch, start their handlers in call order too (`runner.ts:1096-1125`). The host writes the requests in that order, but each request
//! runs on its own thread, and the scheduler is free to run a later thread first.
//!
//! A handler is Rust code on a preemptible thread, so the order is the order the handlers are invoked: a request's handler is invoked
//! only after the handler of every earlier request was invoked or its request ended without one. Pi's single thread also orders the
//! first statements of the handlers; a thread that the operating system preempts between its hand-off and its handler's first
//! statement can still be overtaken, and no Rust construct closes that window.

use std::sync::{Arc, Condvar, Mutex};

#[derive(Default)]
struct Latch {
    done: Mutex<bool>,
    changed: Condvar,
}

impl Latch {
    fn set(&self) {
        *self.done.lock().unwrap() = true;
        self.changed.notify_all();
    }

    fn wait(&self) {
        let mut done = self.done.lock().unwrap();
        #[cfg(test)]
        if !*done {
            parked::note();
        }
        while !*done {
            done = self.changed.wait(done).unwrap();
        }
    }
}

/// The names of the threads that parked behind an earlier request's place, so a wire test can wait for a request's thread to take its place instead of
/// sleeping. Request threads are named `pig-request-<id>`, and a test uses ids no other test uses.
#[cfg(test)]
pub(crate) mod parked {
    use std::collections::HashSet;
    use std::sync::{Mutex, OnceLock, PoisonError};

    fn names() -> &'static Mutex<HashSet<String>> {
        static NAMES: OnceLock<Mutex<HashSet<String>>> = OnceLock::new();
        NAMES.get_or_init(Default::default)
    }

    pub(super) fn note() {
        if let Some(name) = std::thread::current().name() {
            names().lock().unwrap_or_else(PoisonError::into_inner).insert(name.to_string());
        }
    }

    /// Whether the thread called `thread_name` parked behind an earlier place.
    pub(crate) fn contains(thread_name: &str) -> bool {
        names().lock().unwrap_or_else(PoisonError::into_inner).contains(thread_name)
    }
}

/// The order's tail: the place the next request takes.
#[derive(Default)]
pub(crate) struct RequestStartOrder {
    tail: Mutex<Option<Arc<Latch>>>,
}

impl RequestStartOrder {
    /// Take the next place. The read loop calls this in arrival order, before it starts the request's thread.
    pub(crate) fn reserve(&self) -> Arc<RequestStart> {
        let done = Arc::new(Latch::default());
        let previous = self.tail.lock().unwrap().replace(done.clone());
        Arc::new(RequestStart { previous, done })
    }
}

/// Whether a request runs a handler whose start Pi orders by call order. Other requests (renders, provider streams, completions) hold no place, so a long one never holds a handler back.
pub(crate) fn starts_in_arrival_order(method: &str) -> bool {
    matches!(method, "tool_call" | "event" | "command")
}

/// One request's place in the order.
pub(crate) struct RequestStart {
    previous: Option<Arc<Latch>>,
    done: Arc<Latch>,
}

impl RequestStart {
    /// Returns when every earlier request began its handler or ended without one, then hands the place on.
    pub(crate) fn begin(&self) {
        if let Some(previous) = &self.previous {
            previous.wait();
        }
        self.done.set();
    }

    /// Hands the place on when the request's thread ends, also for a request that failed before it reached its handler. Like `begin`
    /// it waits for the earlier requests first, so a request without a handler cannot let a later handler start before an earlier one.
    /// It is idempotent.
    pub(crate) fn end(&self) {
        self.begin();
    }

    /// Hands the place on at once, for a request whose thread never started. The read loop calls it, so it must not wait for the
    /// earlier requests. It is idempotent.
    pub(crate) fn release(&self) {
        self.done.set();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::mpsc;
    use std::time::Duration;

    fn begun(start: Arc<RequestStart>) -> mpsc::Receiver<()> {
        let (tx, rx) = mpsc::channel();
        std::thread::spawn(move || {
            start.begin();
            let _ = tx.send(());
        });
        rx
    }

    fn held(name: &str, began: &mpsc::Receiver<()>) {
        assert!(began.recv_timeout(Duration::from_millis(50)).is_err(), "{name} began before the requests ahead of it");
    }

    // agent-loop.ts:619-647: a request's handler waits for the earlier requests' handlers to be invoked, whatever order the threads run in.
    #[test]
    fn a_later_request_waits_for_every_earlier_one_to_begin() {
        let order = RequestStartOrder::default();
        let (first, second, third, fourth) = (order.reserve(), order.reserve(), order.reserve(), order.reserve());
        let fourth_began = begun(fourth);
        let third_began = begun(third);
        held("fourth", &fourth_began);
        held("third", &third_began);
        first.begin();
        // The first request began, but the second did not: the third still waits for it.
        held("third", &third_began);
        second.begin();
        third_began.recv_timeout(Duration::from_secs(5)).expect("third began");
        fourth_began.recv_timeout(Duration::from_secs(5)).expect("fourth began");
    }

    #[test]
    fn a_request_that_ended_without_a_handler_hands_its_place_on_once() {
        let order = RequestStartOrder::default();
        let (lost, next, last) = (order.reserve(), order.reserve(), order.reserve());
        let next_began = begun(next);
        let last_began = begun(last);
        held("next", &next_began);
        lost.release();
        lost.release();
        next_began.recv_timeout(Duration::from_secs(5)).expect("next began");
        last_began.recv_timeout(Duration::from_secs(5)).expect("last began");
    }

    // A request whose thread ends without its handler still keeps the order of the requests ahead of it: the next handler waits for them.
    #[test]
    fn a_request_that_ended_without_a_handler_keeps_the_earlier_ones_ahead() {
        let order = RequestStartOrder::default();
        let (ahead, skipped, after) = (order.reserve(), order.reserve(), order.reserve());
        let (ended_tx, skipped_ended) = mpsc::channel();
        std::thread::spawn(move || {
            skipped.end();
            skipped.end();
            let _ = ended_tx.send(());
        });
        let after_began = begun(after);
        held("after", &after_began);
        held("skipped", &skipped_ended);
        ahead.begin();
        skipped_ended.recv_timeout(Duration::from_secs(5)).expect("skipped ended");
        after_began.recv_timeout(Duration::from_secs(5)).expect("after began");
    }

    // runner.ts:1096-1125 and agent-loop.ts:619-647: the requests that run extension code take a place; renders, provider streams and completions do not.
    #[test]
    fn the_requests_that_run_extension_code_take_a_place() {
        for method in ["tool_call", "event", "command"] {
            assert!(starts_in_arrival_order(method), "{method}");
        }
        for method in ["provider_stream", "render_tool", "tool_prepare_arguments", "autocomplete.suggest", "user_bash_exec"] {
            assert!(!starts_in_arrival_order(method), "{method}");
        }
    }
}

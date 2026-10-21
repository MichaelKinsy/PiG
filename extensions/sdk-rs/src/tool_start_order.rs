//! Orders the calls of one parallel tool batch the way Pi's single thread does.
//!
//! Pi starts every call of a parallel batch through `Promise.all(calls.map(...))` and each reaches `tool.execute` synchronously, so
//! the handlers start in source order (`agent-loop.ts:619-647, 820-837`). The host writes a batch's `tool_call` requests in source
//! order and names the batch in each request's `batch` member, but each request runs on its own thread, and the scheduler is free to
//! run a later thread first. Three orders follow from Pi's one thread, and each is kept here, per batch:
//!
//! - Start: a request's handler is invoked only after the handler of every earlier request of its batch was invoked or its request
//!   ended without one. A handler is Rust code on a preemptible thread, so a thread that the operating system preempts between its
//!   hand-off and its handler's first statement can still be overtaken, and no Rust construct closes that window.
//! - Dialog: a handler's first host call, of any kind, is written only after the first host call of every earlier request of its
//!   batch was written or its request ended, and a request passes its dialog place on once that first call is written, so the first
//!   dialogs of a batch open in the order the calls started, and a call that awaits an exec before its dialog is overtaken by the
//!   later calls' dialogs, as in Pi. A handler that waits on something the SDK does not see (its own fetch, timer or child process)
//!   before its first host call holds the later calls' first host calls of its batch, which Pi's single thread lets overtake it;
//!   that is the price of an order the handlers' own code cannot be asked for.
//! - Continuation: when two dialogs are answered back to back, Pi runs the two handlers' continuations in the order of the answers,
//!   and each continuation runs to its next await or its return before the next one starts. The reader settles a handler's slot when
//!   it reads the answer to the handler's dialog, and the handler's response is written only after the response of every handler of
//!   its batch that settled before it, or after that handler's next host call (the await its continuation reached).
//!
//! Only dialog answers settle a slot, and a handler that opens no dialog has no slot, so a batch without dialogs is never gated.
//! A request without a batch id (a sequential batch, a nested call) takes part in no order, and the requests of two batches never
//! gate each other. `ToolBatches` keeps one `ToolStartOrder` for each batch in flight and drops it when its last request ended.

use std::collections::{HashMap, VecDeque};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Condvar, Mutex, OnceLock};

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
        while !*done {
            done = self.changed.wait(done).unwrap();
        }
    }
}

/// A request's place in the dialog queue. `waiting` is made only when the request has to wait for its first host call.
struct DialogPlace {
    id: u64,
    waiting: Option<Arc<Latch>>,
}

/// What the order's lock guards.
#[derive(Default)]
struct Places {
    /// The place the next request takes in the start order.
    tail: Option<Arc<Latch>>,
    next_id: u64,
    /// The requests whose dialog place has not passed, in arrival order. The first is the one whose first host call may be written; a
    /// request passes its place once that call is written, whatever the call is.
    dialogs: VecDeque<DialogPlace>,
}

#[derive(Default)]
struct Shared {
    places: Mutex<Places>,
    gate: SettleGate,
    /// When set, runs each time a caller starts to wait for an earlier one, so a test knows it is held without timing.
    #[cfg(test)]
    parked: Option<Arc<dyn Fn() + Send + Sync>>,
}

impl Shared {
    #[cfg(test)]
    fn park(&self) {
        if let Some(parked) = &self.parked {
            parked();
        }
    }

    #[cfg(not(test))]
    fn park(&self) {}
}

/// The order of one parallel batch's tool requests.
#[derive(Default)]
pub(crate) struct ToolStartOrder {
    shared: Arc<Shared>,
}

impl ToolStartOrder {
    #[cfg(test)]
    fn with_parked(parked: impl Fn() + Send + Sync + 'static) -> Self {
        Self { shared: Arc::new(Shared { parked: Some(Arc::new(parked)), ..Default::default() }) }
    }

    /// Take the next place in this order, held by no registry. The gate tests use it; the read loop goes through `ToolBatches`.
    #[cfg(test)]
    pub(crate) fn reserve(&self) -> Arc<ToolStart> {
        self.reserve_leased(None)
    }

    /// `reserve` for a request that holds a place in the batch registry until it ends.
    fn reserve_leased(&self, lease: Option<Lease>) -> Arc<ToolStart> {
        let done = Arc::new(Latch::default());
        let mut places = self.shared.places.lock().unwrap();
        let previous = places.tail.replace(done.clone());
        let id = places.next_id;
        places.next_id += 1;
        places.dialogs.push_back(DialogPlace { id, waiting: None });
        drop(places);
        Arc::new(ToolStart {
            previous,
            done,
            id,
            order: self.shared.clone(),
            dialog_opened: AtomicBool::new(false),
            ended: AtomicBool::new(false),
            slot: OnceLock::new(),
            lease,
        })
    }
}

/// One batch's order and the number of its requests that have not ended.
#[derive(Default)]
struct Batch {
    order: ToolStartOrder,
    live: usize,
}

type Registry = Mutex<HashMap<String, Batch>>;

/// The orders of the parallel batches in flight on one connection, one for each batch id. A batch's order is dropped when its last
/// request ended, so a request that arrives after every earlier request of its batch ended gets a fresh order.
#[derive(Default)]
pub(crate) struct ToolBatches {
    batches: Arc<Registry>,
}

impl ToolBatches {
    /// Takes the next place in the order of `batch`. The read loop calls this in arrival order, before it starts the request's thread.
    /// A request with no batch id takes part in no order: it gets no place.
    pub(crate) fn reserve(&self, batch: Option<&str>) -> Option<Arc<ToolStart>> {
        let batch = batch.filter(|batch| !batch.is_empty())?;
        let mut batches = self.batches.lock().unwrap();
        let entry = batches.entry(batch.to_string()).or_default();
        entry.live += 1;
        Some(entry.order.reserve_leased(Some(Lease { batches: self.batches.clone(), batch: batch.to_string() })))
    }

    /// The number of batches that have a request that did not end.
    #[cfg(test)]
    fn in_flight(&self) -> usize {
        self.batches.lock().unwrap().len()
    }
}

/// A request's hold on its batch's order in the registry.
struct Lease {
    batches: Arc<Registry>,
    batch: String,
}

impl Lease {
    fn release(&self) {
        let mut batches = self.batches.lock().unwrap();
        if let Some(entry) = batches.get_mut(&self.batch) {
            entry.live -= 1;
            if entry.live == 0 {
                batches.remove(&self.batch);
            }
        }
    }
}

/// One request's place in the orders.
pub(crate) struct ToolStart {
    previous: Option<Arc<Latch>>,
    done: Arc<Latch>,
    id: u64,
    order: Arc<Shared>,
    dialog_opened: AtomicBool,
    /// Whether the request ended; guarded by the order's lock.
    ended: AtomicBool,
    /// The request's place in the response order, made when the request opens a dialog: a handler that opens none is never gated.
    slot: OnceLock<Arc<SettleSlot>>,
    /// The request's hold on its batch's order in the registry, released when the request ends.
    lease: Option<Lease>,
}

impl ToolStart {
    /// Returns when every earlier request began its handler or ended without one, then hands the place on.
    pub(crate) fn begin(&self) {
        if let Some(previous) = &self.previous {
            previous.wait();
        }
        self.done.set();
    }

    /// Hands the places on when the request's thread ends, also for a request that failed before it reached its handler. Like `begin`
    /// it waits for the earlier requests first, so a request without a handler cannot let a later handler start before an earlier one.
    /// It is idempotent.
    pub(crate) fn end(&self) {
        self.begin();
        self.finish();
    }

    /// Hands the places on at once, for a request whose thread never started. The read loop calls it, so it must not wait for the
    /// earlier requests. It is idempotent.
    pub(crate) fn release(&self) {
        self.done.set();
        self.finish();
    }

    /// Passes the dialog place on, closes the response slot, which stops it holding up the slots behind it, and lets go of the batch.
    fn finish(&self) {
        self.pass_dialog();
        let (first, slot) = {
            let _places = self.order.places.lock().unwrap();
            (!self.ended.swap(true, Ordering::AcqRel), self.slot.get().cloned())
        };
        if let Some(slot) = slot {
            slot.close();
        }
        if first {
            if let Some(lease) = &self.lease {
                lease.release();
            }
        }
    }

    /// The request's place in the response order, made on first use. A request that ended has none: a dialog a retained context opens
    /// after the handler returned must not leave a place that nothing closes.
    pub(crate) fn response_slot(&self) -> Option<Arc<SettleSlot>> {
        let _places = self.order.places.lock().unwrap();
        if self.ended.load(Ordering::Acquire) {
            return None;
        }
        Some(self.slot.get_or_init(|| Arc::new(SettleSlot::new(self.order.clone(), self.id))).clone())
    }

    /// The handler reached an await: a handler the reader settled gives its place in the response order up.
    pub(crate) fn yield_turn(&self) {
        if let Some(slot) = self.slot.get() {
            slot.yield_turn();
        }
    }

    /// Runs before the handler writes a host call, of any kind. The call is an await of the handler's continuation, so a settled
    /// handler yields its place in the response order. A handler's first host call waits for the first host calls of the earlier
    /// requests, and `after_call` then passes its dialog place on, as Pi's single thread lets a later call run once an earlier call
    /// reached its first await.
    pub(crate) fn before_call(&self) {
        self.yield_turn();
        if self.dialog_opened.swap(true, Ordering::AcqRel) {
            return;
        }
        // pig divergence (D187): the first host call of a call waits for the first host calls of the earlier calls of its batch, also while an earlier call is blocked in work the SDK cannot see (its own fetch, timer or child process); Pi's single thread lets a later call's dialog overtake an earlier call that awaits real I/O.
        let waiting = {
            let mut places = self.order.places.lock().unwrap();
            // A request that is not queued passed its place already; the first one in the queue is the one whose turn it is.
            let Some(at) = places.dialogs.iter().position(|place| place.id == self.id) else { return };
            if at == 0 {
                return;
            }
            places.dialogs[at].waiting.insert(Arc::new(Latch::default())).clone()
        };
        self.order.park();
        waiting.wait();
    }

    /// Runs once the host call's frame is written, or failed to be. The first host call of any kind gives the dialog place up: Pi's
    /// handler that awaits an exec has yielded the thread, so a later call's dialog may open before the handler's own.
    pub(crate) fn after_call(&self) {
        self.pass_dialog();
    }

    /// Gives up the request's dialog place: the first request that still holds one may write its first host call. It never waits, so a
    /// request that ends while an earlier one still runs is not held by it. It is idempotent.
    fn pass_dialog(&self) {
        let mut places = self.order.places.lock().unwrap();
        let Some(at) = places.dialogs.iter().position(|place| place.id == self.id) else { return };
        // A second thread of this request may wait for the place it gives up.
        if let Some(waiting) = places.dialogs.remove(at).and_then(|own| own.waiting) {
            waiting.set();
        }
        if let Some(waiting) = places.dialogs.front_mut().and_then(|head| head.waiting.take()) {
            waiting.set();
        }
    }

    /// Writes the request's response after the response of every handler whose dialog answer was read before this handler's.
    pub(crate) fn respond(&self, send: impl FnOnce()) {
        match self.slot.get() {
            // pig divergence (D187): the response waits for the handlers whose dialog answers were read earlier, until each returns or makes its next host call; Pi's continuations run to their next await, never to the end of blocking work.
            Some(slot) => slot.run(send),
            None => send(),
        }
    }
}

/// Whether a host call opens a dialog the user answers.
pub(crate) fn is_dialog_method(method: &str) -> bool {
    matches!(method, "ui.select" | "ui.confirm" | "ui.input" | "ui.editor" | "ui.custom")
}

/// Orders actions by the order their slots settled: a slot settles when the reader reads the answer its handler waits for, and its
/// action runs after every slot that settled before it finished. A slot that was never settled runs at once.
#[derive(Default)]
struct SettleGate {
    /// The slots that settled and have not yet finished or yielded, in settle order.
    settled: Mutex<VecDeque<u64>>,
    turn: Condvar,
}

/// One request's place in the response order. The flags are read and written with the gate's lock held.
pub(crate) struct SettleSlot {
    order: Arc<Shared>,
    id: u64,
    settled: AtomicBool,
    finished: AtomicBool,
}

impl SettleSlot {
    fn new(order: Arc<Shared>, id: u64) -> Self {
        Self { order, id, settled: AtomicBool::new(false), finished: AtomicBool::new(false) }
    }

    /// Gives the slot its place after every slot that settled before. The reader calls it before it hands the answer to the waiting
    /// handler. A slot that already holds a place keeps it.
    pub(crate) fn settle(&self) {
        let mut settled = self.order.gate.settled.lock().unwrap();
        if self.settled.load(Ordering::Relaxed) || self.finished.load(Ordering::Relaxed) {
            return;
        }
        self.settled.store(true, Ordering::Relaxed);
        settled.push_back(self.id);
    }

    /// Gives the place up: the handler's continuation reached its next await, so the handlers that settled after it go on. The next
    /// answer settles the slot again.
    pub(crate) fn yield_turn(&self) {
        let mut settled = self.order.gate.settled.lock().unwrap();
        self.leave(&mut settled);
    }

    fn leave(&self, settled: &mut VecDeque<u64>) {
        if !self.settled.swap(false, Ordering::Relaxed) {
            return;
        }
        if let Some(at) = settled.iter().position(|id| *id == self.id) {
            settled.remove(at);
        }
        self.order.gate.turn.notify_all();
    }

    /// Runs `action` after every slot that settled before this one left its place. A slot that holds no place runs it at once.
    pub(crate) fn run<R>(&self, action: impl FnOnce() -> R) -> R {
        {
            let gate = &self.order.gate;
            let mut settled = gate.settled.lock().unwrap();
            while self.settled.load(Ordering::Relaxed) && !self.finished.load(Ordering::Relaxed) && settled.front() != Some(&self.id) {
                self.order.park();
                settled = gate.turn.wait(settled).unwrap();
            }
        }
        struct Close<'a>(&'a SettleSlot);
        impl Drop for Close<'_> {
            fn drop(&mut self) {
                self.0.close();
            }
        }
        let _close = Close(self);
        action()
    }

    /// Ends the slot: it stops holding up the slots behind it. It is idempotent.
    pub(crate) fn close(&self) {
        let mut settled = self.order.gate.settled.lock().unwrap();
        self.leave(&mut settled);
        self.finished.store(true, Ordering::Relaxed);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::mpsc;
    use std::thread;
    use std::time::Duration;

    fn begun(start: Arc<ToolStart>) -> mpsc::Receiver<()> {
        let (tx, rx) = mpsc::channel();
        thread::spawn(move || {
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
        let order = ToolStartOrder::default();
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
        let order = ToolStartOrder::default();
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
        let order = ToolStartOrder::default();
        let (ahead, skipped, after) = (order.reserve(), order.reserve(), order.reserve());
        let (ended_tx, skipped_ended) = mpsc::channel();
        thread::spawn(move || {
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

    // The gate tests below use no timing: the order's `parked` hook tells a test when a caller started to wait for an earlier one.

    /// The names of the steps in the order they ran.
    #[derive(Clone, Default)]
    struct Recorder(Arc<Mutex<Vec<String>>>);

    impl Recorder {
        fn add(&self, step: &str) {
            self.0.lock().unwrap().push(step.to_string());
        }

        fn list(&self) -> Vec<String> {
            self.0.lock().unwrap().clone()
        }
    }

    /// A hook that tells a test each time a caller starts to wait.
    fn parked_counter() -> (impl Fn() + Send + Sync + 'static, mpsc::Receiver<()>) {
        let (tx, parked) = mpsc::sync_channel(16);
        let tx = Mutex::new(tx);
        (
            move || {
                let _ = tx.lock().unwrap().try_send(());
            },
            parked,
        )
    }

    fn wait_parked(parked: &mpsc::Receiver<()>) {
        parked.recv_timeout(Duration::from_secs(5)).expect("a caller started to wait");
    }

    fn join(threads: Vec<thread::JoinHandle<()>>) {
        for thread in threads {
            thread.join().unwrap();
        }
    }

    // Pi starts every call of a batch in source order and each reaches its first await, usually a dialog, before the next call starts, so the dialogs of a
    // batch open in call order. The first dialog of each request is written only after the first dialog of every earlier request was written.
    #[test]
    fn dialogs_open_in_call_order() {
        let (hook, parked) = parked_counter();
        let order = ToolStartOrder::with_parked(hook);
        let starts = [order.reserve(), order.reserve(), order.reserve()];
        let rec = Recorder::default();
        let mut threads = Vec::new();
        let mut open = |i: usize| {
            let (start, rec) = (starts[i].clone(), rec.clone());
            threads.push(thread::spawn(move || {
                start.before_call();
                rec.add(&["a", "b", "c"][i]);
                start.after_call();
                // A second dialog of the same request does not wait again.
                start.before_call();
                start.after_call();
                start.end();
            }));
        };
        // The later requests reach their dialogs first: each is held until the one before it opened its own.
        open(2);
        wait_parked(&parked);
        open(1);
        wait_parked(&parked);
        assert!(rec.list().is_empty(), "dialogs {:?} opened before the first request's", rec.list());
        open(0);
        join(threads);
        assert_eq!(rec.list(), ["a", "b", "c"], "dialogs opened out of call order");
    }

    // A request that ends without a dialog hands its place on, also one that failed before its handler ran.
    #[test]
    fn an_ended_request_passes_its_dialog_place_on() {
        let (hook, parked) = parked_counter();
        let order = ToolStartOrder::with_parked(hook);
        let (first, second, third) = (order.reserve(), order.reserve(), order.reserve());
        let rec = Recorder::default();
        let dialog = |name: &'static str, start: &Arc<ToolStart>| {
            let (start, rec) = (start.clone(), rec.clone());
            thread::spawn(move || {
                start.before_call();
                rec.add(name);
                start.after_call();
            })
        };
        let mut threads = vec![dialog("third", &third)];
        wait_parked(&parked);
        threads.push(dialog("second", &second));
        wait_parked(&parked);
        // The first request ends with no handler and no dialog.
        first.end();
        join(threads);
        assert_eq!(rec.list(), ["second", "third"]);
    }

    // Pi's handler that awaits an exec before its first dialog has yielded the thread, so the later calls' dialogs open while its exec runs: the first
    // host call, whatever it is, passes the dialog place on.
    #[test]
    fn the_first_host_call_of_any_kind_passes_the_dialog_place() {
        let (hook, parked) = parked_counter();
        let order = ToolStartOrder::with_parked(hook);
        let (first, second, third) = (order.reserve(), order.reserve(), order.reserve());
        // `first` writes an exec and goes on without ending.
        first.before_call();
        first.after_call();
        // The later calls' first host calls, dialogs, do not wait for it.
        let (done_tx, done) = mpsc::channel();
        let later = thread::spawn(move || {
            second.before_call();
            second.after_call();
            third.before_call();
            third.after_call();
            done_tx.send(()).unwrap();
        });
        done.recv_timeout(Duration::from_secs(5)).expect("a later call waited for the dialog place of a request whose exec is in flight");
        later.join().unwrap();
        assert!(parked.try_recv().is_err(), "a later call parked behind a request whose exec is in flight");
        // `first`'s own dialog, after its exec, does not wait again.
        first.before_call();
        first.after_call();
    }

    // The reader hands the places of a request whose thread never started on at once: it does not wait for the earlier requests, which still hold the
    // dialog place, and the place reaches the next request when they pass it.
    #[test]
    fn a_request_released_by_the_reader_passes_its_dialog_place_without_waiting() {
        let (hook, parked) = parked_counter();
        let order = ToolStartOrder::with_parked(hook);
        let (head, lost, last) = (order.reserve(), order.reserve(), order.reserve());
        let rec = Recorder::default();
        let dialog = {
            let (last, rec) = (last.clone(), rec.clone());
            thread::spawn(move || {
                last.before_call();
                rec.add("last");
                last.after_call();
            })
        };
        wait_parked(&parked);
        // `head` neither began nor ended, and `lost` is released at once, twice.
        lost.release();
        lost.release();
        assert!(rec.list().is_empty(), "the dialog opened before the head request passed its place");
        head.end();
        dialog.join().unwrap();
        assert_eq!(rec.list(), ["last"]);
    }

    // Dialogs answered back to back resume their handlers in the order of the answers: the response of a handler whose answer the reader read first is
    // written first, though the handler that was answered second reaches its response first. A handler that continues to another host call has reached
    // its next await and no longer holds the later ones.
    #[test]
    fn responses_follow_the_order_the_answers_were_read() {
        let (hook, parked) = parked_counter();
        let order = ToolStartOrder::with_parked(hook);
        let (first, second, third) = (order.reserve(), order.reserve(), order.reserve());
        // The reader reads the answers of second, third and first, in that order.
        for start in [&second, &third, &first] {
            start.response_slot().unwrap().settle();
        }
        let rec = Recorder::default();
        let respond = |name: &'static str, start: &Arc<ToolStart>| {
            let (start, rec) = (start.clone(), rec.clone());
            thread::spawn(move || start.respond(|| rec.add(name)))
        };
        let mut threads = vec![respond("first", &first)];
        wait_parked(&parked);
        threads.push(respond("third", &third));
        wait_parked(&parked);
        assert!(rec.list().is_empty(), "responses {:?} were written before the handler answered first", rec.list());
        threads.push(respond("second", &second));
        join(threads);
        assert_eq!(rec.list(), ["second", "third", "first"], "responses did not follow the order of the answers");
    }

    // A handler that went on to another host call lets the ones behind it respond.
    #[test]
    fn a_handler_that_reached_its_next_host_call_lets_the_ones_behind_it_respond() {
        let (hook, parked) = parked_counter();
        let order = ToolStartOrder::with_parked(hook);
        let (a, b) = (order.reserve(), order.reserve());
        a.response_slot().unwrap().settle();
        b.response_slot().unwrap().settle();
        let rec = Recorder::default();
        let responder = {
            let (b, rec) = (b.clone(), rec.clone());
            thread::spawn(move || b.respond(|| rec.add("b")))
        };
        wait_parked(&parked);
        assert!(rec.list().is_empty());
        // Not a dialog: any host call is the await the continuation reached.
        a.before_call();
        responder.join().unwrap();
        assert_eq!(rec.list(), ["b"]);
        // The next answer settles `a` again, and it holds its place until its response is written.
        a.response_slot().unwrap().settle();
        let c = order.reserve();
        c.response_slot().unwrap().settle();
        let responder = {
            let (c, rec) = (c.clone(), rec.clone());
            thread::spawn(move || c.respond(|| rec.add("c")))
        };
        wait_parked(&parked);
        a.respond(|| rec.add("a"));
        responder.join().unwrap();
        assert_eq!(rec.list(), ["b", "a", "c"]);
    }

    // A handler that settled no slot responds at once, whether it has no slot or an unsettled one.
    #[test]
    fn an_unsettled_handler_responds_at_once() {
        let order = ToolStartOrder::default();
        let (settled, none, unsettled) = (order.reserve(), order.reserve(), order.reserve());
        settled.response_slot().unwrap().settle();
        unsettled.response_slot().unwrap();
        let rec = Recorder::default();
        none.respond(|| rec.add("none"));
        unsettled.respond(|| rec.add("unsettled"));
        assert_eq!(rec.list(), ["none", "unsettled"]);
    }

    // A response that panics still ends its slot, so the handlers behind it are not held for good.
    #[test]
    fn a_response_that_panics_closes_its_slot() {
        let order = ToolStartOrder::default();
        let (first, second) = (order.reserve(), order.reserve());
        first.response_slot().unwrap().settle();
        second.response_slot().unwrap().settle();
        let failed = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| first.respond(|| panic!("response failed"))));
        assert!(failed.is_err());
        let rec = Recorder::default();
        second.respond(|| rec.add("second"));
        assert_eq!(rec.list(), ["second"]);
    }

    // A request that ended closed its slot with its handler, so a slot it held does not hold the handlers behind it.
    #[test]
    fn a_request_that_ended_closes_its_slot() {
        let order = ToolStartOrder::default();
        let (first, second) = (order.reserve(), order.reserve());
        first.response_slot().unwrap().settle();
        second.response_slot().unwrap().settle();
        first.end();
        let rec = Recorder::default();
        second.respond(|| rec.add("second"));
        assert_eq!(rec.list(), ["second"]);
    }

    // A dialog a retained context opens after its request ended gets no response slot: a slot nothing closes would hold every handler that settles after
    // it. Its first dialog does not wait for a place it passed, whatever the requests ahead of it still hold.
    #[test]
    fn a_dialog_a_retained_context_opens_after_the_request_ended_leaves_no_slot() {
        let order = ToolStartOrder::default();
        let (_head, ended, later) = (order.reserve(), order.reserve(), order.reserve());
        ended.release();
        assert!(ended.response_slot().is_none(), "a request that ended made a response slot");
        let (done, finished) = mpsc::channel();
        let retained = ended.clone();
        thread::spawn(move || {
            retained.before_call();
            retained.after_call();
            let _ = done.send(());
        });
        finished.recv_timeout(Duration::from_secs(5)).expect("the dialog of an ended request waited for a place it passed");
        later.response_slot().unwrap().settle();
        let rec = Recorder::default();
        later.respond(|| rec.add("later"));
        assert_eq!(rec.list(), ["later"]);
    }

    /// Runs `step` on a thread of its own and reports whether it returned, so a gate that wrongly held it fails a test instead of hanging it.
    fn returns(step: impl FnOnce() + Send + 'static) -> bool {
        let (done, finished) = mpsc::channel();
        thread::spawn(move || {
            step();
            let _ = done.send(());
        });
        finished.recv_timeout(Duration::from_secs(5)).is_ok()
    }

    // The orders belong to one batch: a long request of batch A that opens no dialog and does not end holds neither the handlers' start nor the first
    // dialogs of batch B, and a request of A still waits for the requests of its own batch ahead of it.
    #[test]
    fn the_requests_of_two_batches_do_not_gate_each_other() {
        let batches = ToolBatches::default();
        let long = batches.reserve(Some("A")).unwrap();
        let (first, second) = (batches.reserve(Some("B")).unwrap(), batches.reserve(Some("B")).unwrap());
        let later = batches.reserve(Some("A")).unwrap();
        assert_eq!(batches.in_flight(), 2);
        let (b_first, b_second) = (first.clone(), second.clone());
        assert!(
            returns(move || {
                for start in [&b_first, &b_second] {
                    start.begin();
                    start.before_call();
                    start.after_call();
                }
            }),
            "a request of batch B waited for a request of batch A"
        );
        let began = begun(later);
        held("a later request of batch A", &began);
        long.begin();
        began.recv_timeout(Duration::from_secs(5)).expect("the later request of batch A began");
    }

    // A request without a batch id (a sequential batch, a nested call) takes part in no order: it has no place to wait in or to hold.
    #[test]
    fn a_request_without_a_batch_id_takes_part_in_no_order() {
        let batches = ToolBatches::default();
        assert!(batches.reserve(None).is_none());
        assert!(batches.reserve(Some("")).is_none());
        assert_eq!(batches.in_flight(), 0);
    }

    // A batch's order is dropped when its last request ended, and a request that arrives after that gets a fresh one.
    #[test]
    fn a_batchs_order_is_dropped_when_its_last_request_ended() {
        let batches = ToolBatches::default();
        let (first, second) = (batches.reserve(Some("A")).unwrap(), batches.reserve(Some("A")).unwrap());
        let other = batches.reserve(Some("B")).unwrap();
        assert_eq!(batches.in_flight(), 2);
        // A request that ends twice counts once.
        first.end();
        first.end();
        assert_eq!(batches.in_flight(), 2);
        second.release();
        assert_eq!(batches.in_flight(), 1);
        other.end();
        assert_eq!(batches.in_flight(), 0);
        let fresh = batches.reserve(Some("A")).unwrap();
        assert_eq!(batches.in_flight(), 1);
        assert!(returns({
            let fresh = fresh.clone();
            move || fresh.begin()
        }));
        fresh.end();
        assert_eq!(batches.in_flight(), 0);
    }
}

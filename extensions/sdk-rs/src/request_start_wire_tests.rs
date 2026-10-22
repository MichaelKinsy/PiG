//! The handlers of concurrent requests start in the order the requests arrive, whichever order the operating system runs their threads in
//! (runner.ts:1096-1125 starts concurrent emits' handlers in call order; agent-loop.ts:619-647 starts a batch's tools in call order).
//!
//! Each request runs on a thread of its own. The test makes the first request's thread late without a hook in the dispatch path: that request is
//! an event whose handler lookup waits for the registry's lock, which the test holds, so its thread cannot begin until the test lets it. The
//! later requests' threads run meanwhile. With the arrival-order places each of them parks in `RequestStart::begin` (the parked recorder of
//! `request_start_order` says so), and no handler starts while the first request's thread has not begun. Without a place for the request's kind, the handler starts
//! at once and the test fails on the spot.
use super::handler_dispatch_tests::Host;
use super::{CommandResult, Extension};
use crate::protocol::{Envelope, RequestMsg};
use crate::{ToolResult, empty_schema};
use serde_json::json;
use std::sync::{Arc, Mutex, MutexGuard, PoisonError};
use std::time::{Duration, Instant};

const WAIT: Duration = Duration::from_secs(60);

/// The guard of `lock`, whether or not a panicking test thread poisoned it: this test only reads what the handlers recorded.
fn locked<T>(lock: &Mutex<T>) -> MutexGuard<'_, T> {
    lock.lock().unwrap_or_else(PoisonError::into_inner)
}

fn request(id: &str, method: &str, tool: Option<&str>, event: Option<&str>, handler_id: u32) -> Envelope {
    Envelope {
        msg_type: "request".into(),
        id: Some(id.into()),
        request: Some(RequestMsg {
            child_session: None,
            terminal_input: None,
            stream: None,
            stream_error: None,
            tool_call_index: None,
            args_raw: None,
            method: method.into(),
            tool: tool.map(Into::into),
            event: event.map(Into::into),
            handler_id,
            tool_call_id: Some(format!("call-{id}")),
            batch: None,
            args: Some(json!({})),
        }),
        ..Default::default()
    }
}

/// An extension whose tool `probe`, command `probe` and `tool_result` handler record which of them started. It returns the handler id of the event
/// handler, the log, and the registry handle.
fn recording_extension(name: &str) -> (Extension, u32, Arc<Mutex<Vec<&'static str>>>, super::EventSubscriber) {
    let started: Arc<Mutex<Vec<&'static str>>> = Arc::default();
    let mut ext = Extension::new(name);
    let log = started.clone();
    ext.tool("probe", "Record the start order", empty_schema(), move |_, _| {
        locked(&log).push("tool_call");
        ToolResult::text("ok")
    });
    let log = started.clone();
    ext.command("probe", "Record the start order", move |_, _| {
        locked(&log).push("command");
        CommandResult::Ok
    });
    let log = started.clone();
    let subscription = ext.on_event("tool_result", move |_, _| {
        locked(&log).push("event");
        None
    });
    let handler_id = subscription.handler_id;
    let registry = ext.event_subscriber();
    (ext, handler_id, started, registry)
}

// A request that runs extension code takes its place when its frame is read, so a handler that is invoked later waits for the earlier ones however late
// the earlier request's thread runs. The first request is an event; the other two are a command and a tool call, in each order.
#[test]
fn a_late_request_thread_does_not_let_a_later_handler_start_first() {
    for (round, kinds) in [["command", "tool_call"], ["tool_call", "command"], ["command", "tool_call"], ["tool_call", "command"]].into_iter().enumerate() {
        let (ext, handler_id, started, registry) = recording_extension(&format!("late-thread-{round}"));
        let mut host = Host::connect(ext);
        let late = format!("late{round}-0");
        let later: Vec<String> = (1..=2).map(|n| format!("late{round}-{n}")).collect();
        let hold = locked(&registry.state);
        host.send(request(&late, "event", None, Some("tool_result"), handler_id));
        for (id, kind) in later.iter().zip(kinds) {
            host.send(match kind {
                "command" => request(id, "command", Some("probe"), None, 0),
                _ => request(id, "tool_call", Some("probe"), None, 0),
            });
        }
        // Both later threads run while the first is held. A thread that parks in begin() has taken its place behind the first; a handler that started
        // instead overtook it.
        let deadline = Instant::now() + WAIT;
        while !later.iter().all(|id| crate::request_start_order::parked::contains(&format!("pig-request-{id}"))) {
            let overtaken = locked(&started).clone();
            assert!(overtaken.is_empty(), "round {round}: {overtaken:?} started before the first request's handler, whose thread had not begun");
            assert!(Instant::now() < deadline, "round {round}: the later requests' threads never parked behind the first");
            std::thread::sleep(Duration::from_millis(2));
        }
        assert!(locked(&started).is_empty(), "round {round}: a handler started before the first request's");
        drop(hold);
        for _ in 0..3 {
            let response = host.read_until("response").response.unwrap();
            assert!(response.error.is_none(), "round {round}: {:?}", response.error);
        }
        // The handlers all ran. Their order after the first request began is not asserted: a thread is handed on by begin() and runs its handler a moment later, and a thread
        // the operating system preempts in between can be overtaken (D187), which no test can pin. What the places guarantee, and the assertions above pin, is that no
        // handler started while the first request's thread had not begun.
        let mut ran = locked(&started).clone();
        ran.sort_unstable();
        assert_eq!(ran, ["command", "event", "tool_call"], "round {round}: every handler ran");
        host.stop();
    }
}

use super::*;
use std::thread;

#[test]
fn parent_selection_races_response_publication() {
    for _ in 0..100 {
        let (client, server) = UnixStream::pair().unwrap();
        let connection = Arc::new(Connection::new(client));
        let host = Connection::new(server);
        let parent = connection.arm_parent("origin");
        let barrier = Arc::new(std::sync::Barrier::new(3));
        let caller = connection.clone();
        let captured = parent.clone();
        let start_call = barrier.clone();
        let query = thread::spawn(move || {
            start_call.wait();
            caller.call_for_scope(Some(&captured), Some("origin"), "ui.getEditorText", None)
        });
        let writer = connection.clone();
        let start_response = barrier.clone();
        let response = thread::spawn(move || {
            start_response.wait();
            writer.respond("origin", None, None).unwrap();
        });
        barrier.wait();
        let (mut response_seen, mut call_seen) = (false, false);
        while !(response_seen && call_seen) {
            let frame = host.read_envelope().unwrap();
            if frame.msg_type == "response" {
                response_seen = true;
                assert!(parent.completed());
            } else if frame.msg_type == "call" {
                let owner = frame.call.unwrap().parent_request_id;
                assert!(
                    !(response_seen && owner.is_some()),
                    "completed parent followed its response"
                );
                host.write_envelope(&Envelope {
                    msg_type: "call_result".into(),
                    id: frame.id,
                    call_result: Some(CallResultMsg {
                        result: Some(serde_json::json!({"text":"current"})),
                        error: None,
                    }),
                    ..Default::default()
                })
                .unwrap();
                connection.complete_call(&connection.read_envelope().unwrap());
                call_seen = true;
            }
        }
        let _ = query.join().unwrap();
        response.join().unwrap();
        assert!(connection.pending_calls.lock().unwrap().is_empty());
        assert!(connection.request_parents.lock().unwrap().is_empty());
    }
}

#[test]
fn retained_parent_uses_live_connection_after_response() {
    let (client, server) = UnixStream::pair().unwrap();
    let connection = Arc::new(Connection::new(client));
    let host = Connection::new(server);
    let parent = connection.arm_parent("origin");
    let writer = connection.clone();
    let response = thread::spawn(move || writer.respond("origin", None, None).unwrap());
    assert_eq!(host.read_envelope().unwrap().msg_type, "request_state");
    assert_eq!(host.read_envelope().unwrap().msg_type, "response");
    assert!(
        parent.completed(),
        "completion was not published before the response"
    );
    response.join().unwrap();
    for text in ["nondefault editor", "", "replacement editor"] {
        let caller = connection.clone();
        let captured = parent.clone();
        let query = thread::spawn(move || {
            caller.call_for_scope(Some(&captured), Some("origin"), "ui.getEditorText", None)
        });
        let call = host.read_envelope().unwrap();
        let observed_parent = call.call.unwrap().parent_request_id;
        host.write_envelope(&Envelope {
            msg_type: "call_result".into(),
            id: call.id,
            call_result: Some(CallResultMsg {
                result: Some(serde_json::json!({"text": text})),
                error: None,
            }),
            ..Default::default()
        })
        .unwrap();
        let result = connection.read_envelope().unwrap();
        assert!(connection.complete_call(&result));
        assert_eq!(
            query.join().unwrap().unwrap().result,
            Some(serde_json::json!({"text": text}))
        );
        assert_eq!(observed_parent, None);
    }
    assert!(connection.request_parents.lock().unwrap().is_empty());
    assert!(connection.pending_calls.lock().unwrap().is_empty());
    connection.cancel_pending_calls();
    assert!(connection
        .call_for_scope(Some(&parent), Some("origin"), "ui.getEditorText", None)
        .is_err());
}

#[test]
fn cancelled_parent_never_becomes_runtime_parent() {
    let (client, server) = UnixStream::pair().unwrap();
    let connection = Arc::new(Connection::new(client));
    let host = Connection::new(server);
    let parent = connection.arm_parent("cancelled");
    let caller = connection.clone();
    let captured = parent.clone();
    let query = thread::spawn(move || {
        caller.call_for_scope(Some(&captured), Some("cancelled"), "ui.getEditorText", None)
    });
    let call = host.read_envelope().unwrap();
    assert_eq!(
        call.call.unwrap().parent_request_id.as_deref(),
        Some("cancelled")
    );
    connection.cancel_pending_calls_for("cancelled");
    assert!(query.join().unwrap().is_err());
    let writer = connection.clone();
    let response = thread::spawn(move || writer.respond("cancelled", None, None).unwrap());
    host.read_envelope().unwrap();
    host.read_envelope().unwrap();
    response.join().unwrap();
    assert!(parent.cancelled());
    assert!(!parent.completed());
    assert!(connection
        .call_for_scope(Some(&parent), Some("cancelled"), "ui.getEditorText", None)
        .is_err());
}

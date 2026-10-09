use super::*;
use std::os::unix::net::UnixStream;
use std::sync::mpsc;
use std::time::Duration;

#[test]
fn provider_transport_preserves_events_errors_and_releases_subscription() {
    for failure in [false, true] {
        let signal = ProviderSignal::new();
        let stream = Arc::new(ModelEventStream::new());
        let expected = (0..1024)
            .map(|index| json!({"type":"text_delta","index":index}))
            .collect::<Vec<_>>();
        for event in &expected {
            stream.push(event.clone());
        }
        stream.end(json!({"complete":true}));
        let mut events = Vec::new();
        let result = stream.forward(&signal, |event| {
            if failure {
                return Err("write failed".into());
            }
            events.push(event);
            Ok(())
        });
        if failure {
            assert_eq!(result, Err("write failed".into()));
        } else {
            assert_eq!(result, Ok(()));
            assert_eq!(events, expected);
        }
        assert!(signal.inner.0.lock().unwrap().listeners.is_empty());
        signal.cancel();
        assert_eq!(stream.result(), Some(json!({"complete":true})));
    }
}

#[test]
fn provider_transport_shutdown_does_not_settle_owned_stream() {
    // Pi 0.87.1 packages/ai/src/utils/event-stream.ts:43-89: transport shutdown owns neither iteration closure nor the local result.
    for method in ["stream", "streamSimple", "fetchDeferred"] {
        for preclosed in [false, true] {
            let (client, server) = UnixStream::pair().unwrap();
            server
                .set_read_timeout(Some(Duration::from_secs(5)))
                .unwrap();
            let conn = Arc::new(Connection::new(client));
            let host = Connection::new(server);
            let objects = Arc::new(ProviderObjects::default());
            let stream = Arc::new(ModelEventStream::new());
            let captured = stream.clone();
            let produce: ProviderStreamFn = Arc::new(move |_, _, _| Ok(captured.clone()));
            objects.native.lock().unwrap().insert(
                "owner".into(),
                Arc::new(Provider {
                    id: "unsettled".into(),
                    name: "Unsettled".into(),
                    base_url: None,
                    headers: None,
                    auth: ProviderAuth::default(),
                    get_models: Arc::new(|| Ok(vec![])),
                    filter_models: None,
                    get_all_models: None,
                    filter_all_models: None,
                    refresh_models: None,
                    stream: produce.clone(),
                    stream_simple: produce.clone(),
                    fetch_deferred: Some(produce),
                    cancel_deferred: None,
                    generate_images: None,
                    classify: None,
                }),
            );
            let request: RequestMsg = serde_json::from_value(json!({"method":"provider_stream", "tool":"owner", "args":{"method":method,"params":{"model":{},"context":{},"handle":{}}}})).unwrap();
            if preclosed {
                conn.cancel_pending_calls();
            }
            let (tx, rx) = mpsc::channel();
            let producer_conn = conn.clone();
            let worker = std::thread::spawn(move || {
                tx.send(objects.dispatch(
                    &producer_conn,
                    "request",
                    &request,
                    ProviderSignal::new(),
                ))
                .unwrap();
            });
            assert_eq!(
                host.read_envelope().unwrap().notify.unwrap().args.unwrap()["result"]["type"],
                "provider_started"
            );
            conn.cancel_pending_calls();
            let outcome = rx.recv_timeout(Duration::from_secs(2));
            // Drain the pre-fix blocked worker even when the assertion fails. Its result must still belong to the caller.
            stream.end(json!({"late":true}));
            worker.join().unwrap();
            assert!(
                outcome.is_ok(),
                "{method} preclosed={preclosed}: SDK-owned waiter survived connection shutdown"
            );
            assert!(outcome.unwrap().unwrap_err().contains("closed"));
            assert_eq!(stream.result(), Some(json!({"late":true})));
        }
    }
}

use super::handler_dispatch_tests::Host;
use super::Extension;
use crate::protocol::{Envelope, NotifyMsg, RequestMsg};
use serde_json::json;

// Pi keeps one effective registration per provider and merges a later registration's defined values over it, whichever extension makes it (model-runtime.ts:921-940). After another extension's partial re-registration, the host's merged registration still sends this extension provider_stream_simple for the provider, so provider_superseded must not drop the stream (TestLateProviderSupersededByAnotherExtensionKeepsOperationsAcrossSDKs runs the cross-process path).
#[test]
fn provider_superseded_keeps_the_stream_the_host_still_calls() {
    let mut ext = Extension::new("superseded-probe");
    ext.register_provider_stream("gone", json!({}), |_, _, _, _| Err("the superseded author's stream ran".into()));
    let mut host = Host::connect(ext);
    host.send(Envelope {
        msg_type: "notify".into(),
        notify: Some(NotifyMsg { custom_input: None, method: "provider_superseded".into(), args: Some(json!({"name": "gone"})) }),
        ..Default::default()
    });
    host.send(Envelope {
        msg_type: "request".into(),
        id: Some("stream".into()),
        request: Some(RequestMsg {
            terminal_input: None,
            method: "provider_stream_simple".into(),
            tool: Some("gone".into()),
            event: None,
            handler_id: 0,
            tool_call_id: None,
            args: Some(json!({"model": {}, "context": {}, "options": {}})),
        }),
        ..Default::default()
    });
    let response = host.read_until("response");
    let error = response.response.and_then(|response| response.error).map(|error| error.message);
    assert_eq!(error.as_deref(), Some("the superseded author's stream ran"), "provider_superseded dropped a stream the host still calls");
    host.stop();
}

import pytest

import pig_sdk


@pytest.mark.parametrize("phase", ["normal", "before", "during", "after"])
def test_provider_dispatch_uses_armed_context_cancellation(phase):
    ext = pig_sdk.Extension("provider-context")
    responses, notifications, seen, ended = [], [], [], []
    request = {
        "id": "provider-request",
        "request": {"method": "provider_stream_simple", "tool": "provider", "args": {
            "model": {"id": "test-model"}, "context": {"messages": []}, "options": {"apiKey": "sentinel"},
        }},
    }
    ctx = ext._arm_request(request)
    signal = ctx._cancelled
    result = {"sentinel": "provider-result"}

    def cancel():
        # What the read loop does with an ordinary cancel frame, which ends the request: it marks the request's parent cancelled under the state lock and sets the request's
        # signal. (A signal-only cancel sets the signal alone and leaves the stream to its author: tests/test_provider_stream_signal_only.py.)
        with ext._state_lock:
            ctx._parent.state = "cancelled"
        signal.set()

    class Stream(pig_sdk.ModelEventStream):
        def events(self):
            if phase == "during":
                cancel()
                yield {"type": "text_delta", "delta": "must not publish"}
            if phase == "after":
                cancel()

        def end(self, value):
            ended.append(value)
            super().end(value)

    stream = Stream()
    stream.end(result)
    ended.clear()

    def provider(actual, model, context, options):
        seen.append(actual._cancelled)
        assert actual is ctx
        assert model is request["request"]["args"]["model"]
        assert context is request["request"]["args"]["context"]
        assert options["apiKey"] == "sentinel"
        return stream

    ext._provider_streams["provider"] = provider
    ext._respond = lambda request_id, value, error: responses.append((request_id, value, error))
    ext._notify = lambda method, payload: notifications.append((method, payload))
    if phase == "before":
        cancel()
    ext._handle_request(request, ctx)
    assert seen == [signal]
    assert notifications == []
    if phase == "normal":
        assert responses == [("provider-request", result, None)]
    else:
        assert responses == [("provider-request", None, {"message": "provider stream aborted"})]
    assert ended == ([None] if phase == "before" else [])
    assert ext._provider_active == {}
    assert ext._active == {}
    assert ext._request_parents == {}

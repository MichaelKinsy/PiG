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

    class Stream(pig_sdk.ModelEventStream):
        def events(self):
            if phase == "during":
                signal.set()
                yield {"type": "text_delta", "delta": "must not publish"}
            if phase == "after":
                signal.set()

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
        signal.set()
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

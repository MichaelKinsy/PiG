from pig_sdk import Context, Extension, ModelEventStream
from pig_sdk import _model_stream_error_event


def test_model_event_stream_preserves_order_and_result():
    stream = ModelEventStream()
    stream.push({"type": "start"})
    stream.push({"type": "text_delta", "delta": "ok"})
    terminal = {"role": "assistant", "stopReason": "stop"}
    stream.push({"type": "done", "message": terminal})
    assert [event["type"] for event in stream.events()] == ["start", "text_delta", "done"]
    assert stream.result() is terminal


def test_model_event_stream_competing_iterators_share_one_fifo():
    stream = ModelEventStream()
    stream.push({"type": "start"})
    stream.push({"type": "text_delta", "delta": "ok"})
    terminal = {"role": "assistant", "stopReason": "stop"}
    stream.push({"type": "done", "message": terminal})
    stream.push({"type": "text_delta", "delta": "ignored"})

    first = stream.events()
    second = stream.events()
    assert next(first)["type"] == "start"
    assert next(second)["type"] == "text_delta"
    assert next(first)["type"] == "done"
    assert list(second) == []
    assert stream.result() is terminal


def test_model_event_stream_long_competing_consumers_release_queue():
    stream = ModelEventStream()
    for sequence in range(1000):
        stream.push({"type": "text_delta", "sequence": sequence})
    stream.push({"type": "done", "message": {"stopReason": "stop"}})
    first = stream.events()
    second = stream.events()
    seen = []
    while True:
        advanced = False
        for consumer in (first, second):
            try:
                event = next(consumer)
                if "sequence" in event:
                    seen.append(event["sequence"])
                advanced = True
            except StopIteration:
                pass
        if not advanced:
            break
    assert sorted(seen) == list(range(1000))
    assert len(stream._events) == 0


def test_model_stream_transport_error_shape_is_complete():
    event = _model_stream_error_event(RuntimeError("transport boom"), {"api": "openai-responses", "provider": "conformance", "modelId": "transport-error"})
    error = event["error"]
    assert error["role"] == "assistant"
    assert error["api"] == "openai-responses"
    assert error["provider"] == "conformance"
    assert error["model"] == "transport-error"
    assert error["stopReason"] == "error"
    assert error["errorMessage"] == "transport boom"
    assert error["timestamp"] > 0
    assert error["usage"]["totalTokens"] == 0
    assert error["usage"]["cost"]["total"] == 0


def test_extension_routes_model_stream_notify():
    extension = Extension("test")
    stream = ModelEventStream()
    extension._model_streams["stream-1"] = stream
    extension._handle_notify({"notify": {"method": "model_stream_event", "args": {"streamId": "stream-1", "event": {"type": "done", "message": {"stopReason": "stop"}}}}})
    assert stream.result()["stopReason"] == "stop"
    assert callable(Context(extension).model_registry.complete)

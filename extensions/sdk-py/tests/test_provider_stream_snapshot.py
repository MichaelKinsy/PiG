import threading

import pytest

import pig_sdk as sdk


def message_with_block():
    block = {"type": "text", "text": ""}
    return {"role": "assistant", "content": [block], "stopReason": "stop"}, block


def shown_text(event):
    return (event.get("partial") or event["message"])["content"][0]["text"]


def drive(provider):
    """Run a provider_stream_simple request through the extension; returns what the host is sent: (events, responses)."""
    ext = sdk.Extension("snapshot")
    request = {"id": "rid", "request": {"method": "provider_stream_simple", "tool": "provider", "args": {"model": {}, "context": {}, "options": {}}}}
    ctx = ext._arm_request(request)
    events, responses = [], []
    ext._provider_streams["provider"] = lambda *_: provider()
    ext._notify = lambda method, payload: events.append(payload["result"])
    ext._respond = lambda request_id, value, error: responses.append((value, error))
    handling = threading.Thread(target=ext._handle_request, args=(request, ctx), daemon=True)
    handling.start()
    handling.join(10)
    assert not handling.is_alive(), "the request did not finish: it waits for a result the stream never gets"
    return events, responses


def test_the_host_receives_each_event_as_it_was_pushed():
    # The forwarder thread reads the queue after the author went on.
    objects = {}

    def provider():
        stream = sdk.ModelEventStream()
        message, block = message_with_block()
        objects["message"] = message
        stream.push({"type": "start", "partial": message})
        for _ in range(3):
            block["text"] += "x"
            stream.push({"type": "text_delta", "delta": "x", "partial": message})
        block["text"] += "-AFTER"
        stream.push({"type": "done", "reason": "stop", "message": message})
        block["text"] = "changed after done"
        return stream

    events, responses = drive(provider)
    assert [shown_text(event) for event in events] == ["", "x", "xx", "xxx", "xxx-AFTER"]
    assert responses[0][0]["content"][0]["text"] == "xxx-AFTER"
    assert objects["message"]["content"][0]["text"] == "changed after done"


def test_iteration_in_process_yields_the_pushed_objects():
    stream = sdk.ModelEventStream()
    message, _ = message_with_block()
    start = {"type": "start", "partial": message}
    done = {"type": "done", "reason": "stop", "message": message}
    stream.push(start)
    stream.push(done)
    first, last = list(stream.events())
    assert first is start and last is done
    assert last["message"] is stream.result()


def test_end_hands_the_host_the_result_as_it_is():
    objects = {}

    def provider():
        stream = sdk.ModelEventStream()
        message, block = message_with_block()
        objects["message"], objects["stream"] = message, stream
        block["text"] = "ended"
        stream.end(message)
        block["text"] = "changed after end"
        return stream

    events, responses = drive(provider)
    assert events == []
    assert responses[0][0]["content"][0]["text"] == "ended"
    assert objects["stream"].result() is objects["message"]


def test_events_the_sdk_builds_are_queued_as_they_are():
    stream = sdk.ModelEventStream()
    event = {"type": "start", "partial": {"content": []}}
    stream._enqueue(event, False)
    stream.end(None)
    assert next(iter(stream.events())) is event
    assert event is not None


def test_an_overridden_result_answers_the_request():
    # Pi and Node answer with `await stream.result()`; the snapshot must not bypass an override, even for a stream that never ends.
    class Stream(sdk.ModelEventStream):
        def events(self):
            yield {"type": "start", "overridden": True}

        def result(self):
            return {"override": True}

    events, responses = drive(Stream)
    assert events == [{"type": "start", "overridden": True}]
    assert responses == [({"override": True}, None)]


def uncopyable(name):
    lock = threading.Lock()
    cycle = {}
    cycle["self"] = cycle
    return {"lock": lock, "cycle": cycle}[name]


@pytest.mark.parametrize("name", ["lock", "cycle"])
def test_a_value_that_cannot_be_copied_fails_the_request_and_not_push(name):
    def provider():
        stream = sdk.ModelEventStream()
        message, _ = message_with_block()
        stream.push({"type": "start", "partial": message})
        stream.push({"type": "text_delta", "delta": uncopyable(name)})
        stream.push({"type": "done", "reason": "stop", "message": message})
        return stream

    events, responses = drive(provider)
    assert [event["type"] for event in events] == ["start"]
    assert responses[0][0] is None and responses[0][1]["message"]


@pytest.mark.parametrize("name", ["lock", "cycle"])
def test_a_producer_thread_survives_a_value_that_cannot_be_copied(name):
    failures = []

    def provider():
        stream = sdk.ModelEventStream()

        def produce():
            try:
                message, _ = message_with_block()
                stream.push({"type": "start", "partial": message})
                message["uncopyable"] = uncopyable(name)
                stream.push({"type": "done", "reason": "stop", "message": message})
            except BaseException as exc:  # noqa: BLE001
                failures.append(exc)

        threading.Thread(target=produce).start()
        return stream

    events, responses = drive(provider)
    assert failures == []
    assert [event["type"] for event in events] == ["start"]
    assert responses[0][0] is None and responses[0][1]["message"]


def test_a_result_that_cannot_be_copied_ends_the_stream_and_fails_the_request():
    objects = {}

    def provider():
        stream = sdk.ModelEventStream()
        objects["result"] = {"lock": threading.Lock()}
        stream.end(objects["result"])
        return stream

    events, responses = drive(provider)
    assert events == []
    assert responses[0][0] is None and responses[0][1]["message"]

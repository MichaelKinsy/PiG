import threading

import pytest

import pig_sdk as sdk
from test_request_lifetime import connected_extension


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


@pytest.mark.parametrize("signal_is_late", [False, True])
def test_a_cancel_during_a_push_burst_forwards_the_pushed_events_and_fails_the_request(signal_is_late):
    # The host cancels the request in the middle of a burst of pushes, through the SDK's own read loop. As in the Go SDK
    # (TestProviderStreamCancelledDuringABurstForwardsThePushedEventsAndFailsTheRequest): the host has read the events pushed before the cancel, each as it was at its push and in
    # push order, it receives none of the events pushed after it, and the request ends with an error and no result. The author's later pushes return and change nothing.
    # The read loop ends the stream of the cancelled request before it sets the request's signal. With signal_is_late the signal is held back until the request is answered, so
    # the forwarder runs between the two: the stream it sees ended was ended by the cancel, and the answer is still the abort, never a result.
    ext = sdk.Extension("snapshot-cancel")
    cancelled, finished, answered = threading.Event(), threading.Event(), threading.Event()
    streams, failures = [], []

    def provider(ctx, model, context, options):
        if signal_is_late:
            set_signal = ctx._cancelled.set

            def set_after_the_answer():
                assert answered.wait(10)
                set_signal()

            ctx._cancelled.set = set_after_the_answer
        stream = sdk.create_assistant_message_event_stream()
        streams.append(stream)
        message, block = message_with_block()

        def produce():
            try:
                stream.push({"type": "start", "partial": message})
                for index in range(6):
                    if index == 3:
                        assert cancelled.wait(10)
                    block["text"] += "x"
                    stream.push({"type": "text_delta", "contentIndex": 0, "delta": "x", "partial": message})
                block["text"] += "-AFTER"
                stream.push({"type": "done", "reason": "stop", "message": message})
            except BaseException as exc:  # noqa: BLE001 - the test reports it
                failures.append(exc)
            finally:
                finished.set()

        threading.Thread(target=produce, daemon=True).start()
        return stream

    ext._provider_streams["provider"] = provider
    with connected_extension(ext) as (send, receive):
        shown, responses, pongs = [], [], []

        def read_until(done):
            while not done():
                frame = receive(None)
                if frame["type"] == "notify" and frame["notify"]["method"] == "provider_stream_event":
                    shown.append(shown_text(frame["notify"]["args"]["result"]))
                elif frame["type"] == "response":
                    responses.append((frame["id"], frame["response"]["result"], frame["response"]["error"]))
                    answered.set()
                elif frame["type"] == "pong":
                    pongs.append(frame["pong"]["nonce"])

        send({"type": "request", "id": "rid", "request": {"method": "provider_stream_simple", "tool": "provider", "args": {"model": {}, "context": {}, "options": {}}}})
        read_until(lambda: len(shown) == 4)
        send({"type": "cancel", "cancel": {"request_id": "rid", "reason": "user"}})
        send({"type": "ping", "ping": {"nonce": "cancel-read"}})
        read_until(lambda: "cancel-read" in pongs)
        cancelled.set()
        assert finished.wait(10)
        read_until(lambda: responses)
        send({"type": "ping", "ping": {"nonce": "after-the-burst"}})
        read_until(lambda: "after-the-burst" in pongs)
        assert shown == ["", "x", "xx", "xxx"]
        assert responses == [("rid", None, {"message": "provider stream aborted"})]
    assert failures == []
    assert streams[0].result() is None
    assert ext._provider_active == {}

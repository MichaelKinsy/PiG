import threading
import time

import pig_sdk

from test_request_lifetime import connected_extension


def _stream_request(send):
    send({"type": "request", "id": "stream", "request": {"method": "provider_stream_simple", "tool": "fixture", "args": {"model": {}, "context": {}, "options": {}}}})


def _next_stream_frame(receive):
    """The next stream event of the request, or its response: (event, response), one of them None."""
    while True:
        frame = receive(None)
        if frame["type"] == "notify" and frame["notify"]["method"] == "provider_stream_event":
            return frame["notify"]["args"]["result"], None
        if frame["type"] == "response":
            return None, frame["response"]


def _author(release=None):
    """A streamSimple whose author pushes start, waits until its request's signal aborts, then for release when given, and goes on: a text_delta, and the end of its stream
    with a result."""
    final = {"role": "assistant", "content": [{"type": "text", "text": "the author's result"}], "stopReason": "stop"}

    def produce(ctx, model, transcript, options):
        stream = pig_sdk.ModelEventStream()

        def author():
            stream.push({"type": "start", "partial": {"role": "assistant", "content": []}})
            deadline = time.monotonic() + 5
            while not ctx.is_cancelled() and time.monotonic() < deadline:
                time.sleep(0.002)
            if release is not None:
                release.wait(5)
            stream.push({"type": "text_delta", "contentIndex": 0, "delta": "after the abort", "partial": {"role": "assistant", "content": [{"type": "text", "text": "after the abort"}]}})
            stream.end(final)

        threading.Thread(target=author, daemon=True).start()
        return stream

    return produce


def test_signal_only_cancel_leaves_the_provider_stream_to_its_author():
    # Pi's consumer of a response has no check of the abort signal (packages/agent/src/agent-loop.ts:424-478): after the abort of a turn, a signal-only cancel, the author's
    # signal is set, the events its stream gives are forwarded and its result is the answer.
    ext = pig_sdk.Extension("stream-signal-only")
    ext.register_provider("fixture", {"streamSimple": _author()})
    with connected_extension(ext) as (send, receive):
        _stream_request(send)
        event, _ = _next_stream_frame(receive)
        assert event["type"] == "start"
        send({"type": "cancel", "cancel": {"request_id": "stream", "reason": "context canceled", "signal_only": True}})
        event, response = _next_stream_frame(receive)
        assert response is None, f"the request was answered with {response} before the event its author pushed after the abort"
        assert event["type"] == "text_delta"
        event, response = _next_stream_frame(receive)
        assert event is None
        assert response.get("error") is None
        assert response["result"]["content"][0]["text"] == "the author's result"


def test_ordinary_cancel_after_signal_only_cancel_ends_the_provider_stream_request():
    # An ordinary cancel ends the request, also after a signal-only cancel: the author's stream is ended, nothing more is forwarded and the answer is the abort.
    ext = pig_sdk.Extension("stream-ordinary-after")
    release = threading.Event()
    ext.register_provider("fixture", {"streamSimple": _author(release)})
    with connected_extension(ext) as (send, receive):
        _stream_request(send)
        event, _ = _next_stream_frame(receive)
        assert event["type"] == "start"
        try:
            send({"type": "cancel", "cancel": {"request_id": "stream", "reason": "context canceled", "signal_only": True}})
            send({"type": "cancel", "cancel": {"request_id": "stream", "reason": "the host ends the request"}})
            event, response = _next_stream_frame(receive)
        finally:
            release.set()
        assert event is None, "no event is forwarded after the ordinary cancel"
        assert response == {"result": None, "error": {"message": "provider stream aborted"}}

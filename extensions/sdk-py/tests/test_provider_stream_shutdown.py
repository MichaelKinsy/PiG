import threading
from types import SimpleNamespace

import pig_sdk as sdk
import pytest
from pig_sdk.provider import dispatch_provider


def provider_request(stream, method):
    ext = sdk.Extension("unsettled")
    def produce(*_):
        return stream
    ext._native_providers["owner"] = sdk.Provider(
        "unsettled", "Unsettled", sdk.ProviderAuth(), lambda: [],
        produce, produce, fetch_deferred=produce,
    )
    ctx = SimpleNamespace(request_id="request", _cancelled=sdk.ProviderSignal())
    request = {"tool": "owner", "args": {"method": method, "params": {"model": {}, "context": {}, "handle": {}}}}
    return ext, ctx, request


@pytest.mark.parametrize("method", ["stream", "streamSimple", "fetchDeferred"])
@pytest.mark.parametrize("preclosed", [False, True])
def test_provider_transport_shutdown_does_not_settle_owned_stream(method, preclosed):
    # Pi 0.87.1 packages/ai/src/utils/event-stream.ts:43-89: only the producer settles its stream, not request abort or transport shutdown.
    stream = sdk.ModelEventStream()
    ext, ctx, request = provider_request(stream, method)
    waiting = threading.Event()
    original_wait = stream._condition.wait

    def wait(*args):
        waiting.set()
        return original_wait(*args)

    stream._condition.wait = wait
    ext._notify = lambda *_: None
    if preclosed:
        ext._stop_runtime()
    outcome = []

    def run():
        try:
            outcome.append(dispatch_provider(ext, ctx, request))
        except RuntimeError as error:
            outcome.append(str(error))
        finally:
            with ext._request_threads_lock:
                ext._request_threads.discard(threading.current_thread())

    worker = threading.Thread(target=run, daemon=True)
    ext._request_threads.add(worker)
    worker.start()
    try:
        if not preclosed:
            assert waiting.wait(5), "provider never waited for an event"
            ctx._cancelled.set()
            assert not stream._terminal, "request abort fabricated a local result"
        ext._stop_runtime()
        assert not worker.is_alive(), "transport worker survived shutdown"
        assert len(outcome) == 1 and "closed" in outcome[0]
        assert not stream._terminal, "transport fabricated a local result"
    finally:
        stream.end({"late": True})
        worker.join(5)
    assert stream.result() == {"late": True}


@pytest.mark.parametrize("derived", [False, True])
def test_provider_transport_preserves_stream_overrides(derived):
    # Pi consumes the returned object's iterator. Do not bypass overrides or require a private SDK method on duck-typed streams.
    class Stream(sdk.ModelEventStream if derived else object):
        def events(self):
            yield {"type": "start", "overridden": True}

        def result(self):
            raise AssertionError("stable event-only dispatcher must not add a result wait")

    stream = Stream()
    if derived:
        stream.end({"base": True})
    ext, ctx, request = provider_request(stream, "stream")
    frames = []
    ext._notify = lambda method, value: frames.append(value["result"])
    assert dispatch_provider(ext, ctx, request) is None
    assert frames == [{"type": "provider_started"}, {"type": "start", "overridden": True}]


def test_provider_transport_unsubscribes_and_preserves_local_waits():
    stream = sdk.ModelEventStream()
    ext, ctx, request = provider_request(stream, "stream")
    stream.push({"type": "start"})

    def notify(method, value):
        if value["result"]["type"] == "start":
            raise RuntimeError("write failed")

    ext._notify = notify
    with pytest.raises(RuntimeError, match="write failed"):
        dispatch_provider(ext, ctx, request)
    ext._stop_runtime()
    assert not stream._terminal
    stream.push({"type": "done", "message": {"local": True}})
    assert list(stream.events()) == [{"type": "done", "message": {"local": True}}]
    assert stream.result() == {"local": True}
    assert not ext._shutdown._listeners

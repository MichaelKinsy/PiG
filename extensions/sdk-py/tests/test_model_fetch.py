"""The extension's ``fetch`` option of ModelRegistry.stream (Pi's ProviderRequestOptions.fetch, packages/ai/src/types.ts).

The reference is runtime-node/model-fetch.mjs: the fetch's init.signal aborts when the host cancels the fetch callback or a fetchRead,
when the host closes the response, and when the stream ends; a response that arrives after the stream ended is cancelled and the fetch fails.
"""

import base64
import threading

import pytest

from pig_sdk import _ModelFetchTransport
from pig_sdk.provider import ProviderSignal

CALL = {"id": "1", "url": "https://fetch.invalid/v1", "method": "POST", "headers": {"X-Host": ["1"]}, "body": base64.b64encode(b"ping").decode()}


class _Body:
    def __init__(self, data: bytes = b"", signal: ProviderSignal | None = None) -> None:
        self.data = data
        self.signal = signal
        self.closed = False

    def read(self, size: int) -> bytes:
        if self.signal is not None:
            assert self.signal.wait(10), "the read was not cancelled"
            raise RuntimeError("read cancelled")
        chunk, self.data = self.data[:size], self.data[size:]
        return chunk

    def close(self) -> None:
        self.closed = True


def test_fetch_gets_the_request_and_streams_the_body():
    seen = {}

    def fetch(request):
        seen.update({key: request[key] for key in ("url", "method", "headers", "body")})
        return {"status": 207, "statusText": "Answered", "headers": {"x-sdk-fetch": "answered"}, "body": b"abc"}

    transport = _ModelFetchTransport(fetch)
    assert transport.serve("fetch", CALL, None) == {"status": 207, "statusText": "Answered", "headers": [["x-sdk-fetch", "answered"]], "body": True}
    assert seen == {"url": "https://fetch.invalid/v1", "method": "POST", "headers": {"X-Host": ["1"]}, "body": b"ping"}
    assert transport.serve("fetchRead", {"id": "1", "size": 2}, None) == {"data": base64.b64encode(b"ab").decode(), "done": False}
    assert transport.serve("fetchRead", {"id": "1", "size": 2}, None) == {"data": base64.b64encode(b"c").decode(), "done": False}
    assert transport.serve("fetchRead", {"id": "1", "size": 2}, None) == {"data": "", "done": True}


@pytest.mark.parametrize("size", [0, 64 * 1024 + 1, True, "2"])
def test_fetch_read_size_is_bounded(size):
    transport = _ModelFetchTransport(lambda request: {"status": 200, "body": b"abc"})
    transport.serve("fetch", CALL, None)
    with pytest.raises(RuntimeError, match="invalid model fetch read size"):
        transport.serve("fetchRead", {"id": "1", "size": size}, None)


def test_host_cancellation_of_the_fetch_sets_its_signal():
    cancelled = ProviderSignal()

    def fetch(request):
        cancelled.set()  # the host cancels the fetch callback while the fetch runs
        assert request["signal"].wait(10)
        raise RuntimeError("aborted")

    transport = _ModelFetchTransport(fetch)
    with pytest.raises(RuntimeError, match="aborted"):
        transport.serve("fetch", CALL, cancelled)
    assert transport._entries == {}


def test_host_cancellation_of_a_read_sets_the_fetch_signal():
    body = {}

    def fetch(request):
        body["body"] = _Body(signal=request["signal"])
        return {"status": 200, "body": body["body"]}

    transport = _ModelFetchTransport(fetch)
    transport.serve("fetch", CALL, None)
    cancelled = ProviderSignal()
    cancelled.set()
    with pytest.raises(RuntimeError, match="read cancelled"):
        transport.serve("fetchRead", {"id": "1", "size": 4}, cancelled)


def test_fetch_close_sets_the_signal_and_closes_the_body():
    seen = {}
    body = _Body(b"abc")

    def fetch(request):
        seen["signal"] = request["signal"]
        return {"status": 200, "body": body}

    transport = _ModelFetchTransport(fetch)
    transport.serve("fetch", CALL, None)
    assert not seen["signal"].is_set()
    assert transport.serve("fetchClose", {"id": "1"}, None) is None
    assert seen["signal"].is_set() and body.closed
    with pytest.raises(RuntimeError, match="unknown model fetch response 1"):
        transport.serve("fetchRead", {"id": "1", "size": 4}, None)
    assert transport.serve("fetchClose", {"id": "1"}, None) is None


def test_stream_end_cancels_a_fetch_in_flight_and_closes_its_late_body():
    started = threading.Event()
    release = threading.Event()
    seen = {}
    body = _Body(b"late")

    def fetch(request):
        seen["signal"] = request["signal"]
        started.set()
        assert release.wait(10)  # a fetch that ignores its signal and answers after the stream ended
        return {"status": 200, "body": body}

    transport = _ModelFetchTransport(fetch)
    outcome = {}

    def run():
        try:
            outcome["result"] = transport.serve("fetch", CALL, None)
        except RuntimeError as exc:
            outcome["error"] = str(exc)

    worker = threading.Thread(target=run)
    worker.start()
    assert started.wait(10)
    transport.dispose()
    assert seen["signal"].is_set()
    release.set()
    worker.join(10)
    assert outcome == {"error": "model fetch transport is closed"}
    assert body.closed
    assert transport._entries == {}
    with pytest.raises(RuntimeError, match="model fetch transport is closed"):
        transport.serve("fetch", CALL, None)

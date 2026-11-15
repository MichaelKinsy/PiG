"""A session graph object the extension no longer refers to is released, and the host then refuses every call that names its handle.

``_GraphObject.__del__`` releases the host's object when the Python value is collected, but a handle is a string on the
wire: whatever only names it (a ``LoadedExtension`` that says which loader it was read from, a model stream thread that
runs on a runtime) must keep the value itself, or a collection between the last use of the value and the host's use of
the name releases the handle first ("Unknown or released session graph object"). A weak reference answers the question
without a wait: after ``gc.collect()`` it is dead exactly when the object was collected, and ``__del__`` released it then.
"""

from __future__ import annotations

import gc
import threading
import weakref
from typing import Any

import pig_sdk
from pig_sdk.extension_runner import ExtensionRunner
from pig_sdk.model_runtime import ModelRuntime
from pig_sdk.resource_loader import DefaultResourceLoader


class _Host:
    """A context whose host issues a handle for each object, records every call, and holds a ``modelStream`` call until the test lets it go."""

    def __init__(self) -> None:
        self.extension = pig_sdk.Extension("graph-lifetime")
        self.calls: list[tuple[str, Any]] = []
        self._issued = 0
        self.stream_started = threading.Event()
        self.finish_stream = threading.Event()

    def _call(self, method: str, params: Any = None) -> dict[str, Any]:
        self.calls.append((method, params))
        if method == "sessionGraph.new":
            self._issued += 1
            return {"result": {"handle": f"h{self._issued}"}}
        if method == "sessionGraph.call" and params.get("method") == "getExtensions":
            extension = {"path": "/w/a.mjs", "resolvedPath": "/w/a.mjs", "sourceInfo": {"path": "/w/a.mjs", "source": "local", "scope": "temporary", "origin": "top-level"}, "tools": []}
            return {"result": {"extensions": [extension], "errors": [], "warnings": []}}
        if method == "modelStream":
            self.stream_started.set()
            assert self.finish_stream.wait(10), "the test never let the stream's call go"
        return {"result": None}

    def news(self, kind: str) -> list[Any]:
        return [params for method, params in self.calls if method == "sessionGraph.new" and params["kind"] == kind]


def _load(host: _Host) -> tuple[weakref.ref, Any]:
    """The loader is local to this function, as it is in a helper that returns only what the loader loaded."""
    loader = DefaultResourceLoader(host, cwd="/w", agent_dir="/a")
    loaded = loader.get_extensions()
    return weakref.ref(loader), loaded


def test_loaded_extensions_keep_the_loader_they_name() -> None:
    host = _Host()
    loader, loaded = _load(host)
    gc.collect()
    assert loader() is not None, "the loader was collected, so its handle was released, while the extensions that name it are still held"
    runner = ExtensionRunner(host, loaded.extensions, loaded.runtime, "/w")
    assert host.news("ExtensionRunner")[0]["args"][0]["extensions"] == [{"loader": "h1", "resolvedPath": "/w/a.mjs"}]
    assert loader() is not None
    # Once nothing names the loader it is released, as Pi frees an object that is unreachable.
    del runner, loaded
    gc.collect()
    assert loader() is None, "the loader outlived everything that names it"


def test_a_model_stream_keeps_the_runtime_it_runs_on() -> None:
    host = _Host()
    runtime = ModelRuntime.create(host)
    ref = weakref.ref(runtime)
    runtime.stream({"provider": "p", "id": "m"}, {})
    del runtime
    try:
        assert host.stream_started.wait(10), "the stream never reached the host"
        gc.collect()
        assert ref() is not None, "the runtime was collected, so its handle was released, while the host ran a stream on it"
        assert [params for method, params in host.calls if method == "modelStream"][0]["runtime"] == "h1"
    finally:
        host.finish_stream.set()

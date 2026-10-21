"""Delivery of a host-replicated value's changes to the handlers an extension subscribed.

Upstream Pi calls a widget component's ``invalidate()`` when the theme or the tool-expansion state changes, and the component reads the new value at its next render. A subprocess extension has no component to invalidate, so it subscribes (``on_theme_change``, ``on_tools_expanded_change``).

Handlers run on a worker thread, one delivery at a time in the order the changes arrived, never on the thread that reads the host's replies, so a handler can make a blocking host call. A handler that raises is reported on stderr and does not stop the others. An unsubscribe changes only later deliveries: a delivery already queued still reaches the handlers subscribed when the change arrived.
"""

from __future__ import annotations

import collections
import itertools
import sys
import threading
import traceback
from collections.abc import Callable
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from . import Extension


class StateFeed:
    def __init__(self, extension: Extension, what: str) -> None:
        self._extension = extension
        self._what = what
        self._lock = threading.Lock()
        self._ids = itertools.count(1)
        self._subs: list[tuple[int, Callable[[Any], Any]]] = []
        self._queue: collections.deque[tuple[Any, list[Callable[[Any], Any]]]] = collections.deque()
        self._running = False

    def subscribe(self, handler: Callable[[Any], Any]) -> Callable[[], None]:
        with self._lock:
            sub_id = next(self._ids)
            self._subs.append((sub_id, handler))
        removed = threading.Lock()

        def unsubscribe() -> None:
            if not removed.acquire(blocking=False):
                return
            with self._lock:
                self._subs = [sub for sub in self._subs if sub[0] != sub_id]

        return unsubscribe

    def publish(self, value: Any) -> None:
        """Queue ``value`` for the handlers subscribed now."""
        extension = self._extension
        with self._lock:
            handlers = [handler for _, handler in self._subs]
            if not handlers or extension._shutdown.is_set():
                return
            self._queue.append((value, handlers))
            if self._running:
                return
            self._running = True
            worker = threading.Thread(target=self._run, name=f"pig-{self._what}-change", daemon=True)
            with extension._request_threads_lock:
                extension._request_threads.add(worker)
        try:
            worker.start()
        except Exception as exc:  # noqa: BLE001 - a worker that cannot start drops the queue and the reader keeps serving
            with extension._request_threads_lock:
                extension._request_threads.discard(worker)
            with self._lock:
                self._queue.clear()
                self._running = False
            print(f"pig: start {self._what} change handlers: {exc}", file=sys.stderr)

    def _run(self) -> None:
        extension = self._extension
        try:
            while True:
                with self._lock:
                    if not self._queue or extension._shutdown.is_set():
                        self._queue.clear()
                        self._running = False
                        return
                    value, handlers = self._queue.popleft()
                for handler in handlers:
                    try:
                        handler(value)
                    except Exception:  # noqa: BLE001 - one handler's failure must not stop the others
                        if not extension._shutdown.is_set():
                            print(f"pig: {self._what} change handler failed:\n{traceback.format_exc()}", file=sys.stderr, end="")
        except BaseException:
            with self._lock:
                self._queue.clear()
                self._running = False
            raise
        finally:
            with extension._request_threads_lock:
                extension._request_threads.discard(threading.current_thread())

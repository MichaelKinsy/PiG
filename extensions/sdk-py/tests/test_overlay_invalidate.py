"""The host's ui.custom.invalidate reaches a mounted component on its own worker.

Pi's TUI.invalidate calls invalidate() on every mounted root and every overlay's component (tui.ts:951-954). The host tells a
remote component with ui.custom.invalidate {key, count}; the SDK queues it behind the component's input and the worker calls the
component's optional invalidate() count times and renders once.
"""

from __future__ import annotations

import pig_sdk


class Probe:
    def __init__(self) -> None:
        self.invalidations = 0
        self.renders = 0

    def render(self, width: int) -> list[str]:
        self.renders += 1
        return ["row"]

    def handle_input(self, data: str) -> pig_sdk.RemoteComponentResult:
        return pig_sdk.RemoteComponentResult(done=False, value=None)

    def invalidate(self) -> None:
        self.invalidations += 1


def test_an_invalidate_queued_for_a_closed_component_is_dropped() -> None:
    ext = pig_sdk.Extension("py-invalidate")
    sent: list[tuple[str, object]] = []
    ext._notify = lambda method, args=None: sent.append((method, args))  # type: ignore[method-assign]
    component = Probe()
    overlay = pig_sdk._RemoteOverlayState(component)
    assert overlay.enqueue_invalidate(2), "queued while the component is mounted"
    overlay.stop()  # the component closed before its worker took the event
    ext._remote_component_worker("custom-1", overlay)
    assert (component.invalidations, component.renders, sent) == (0, 0, []), "no invalidate, no render and no error after the close"
    queued = overlay.events.qsize()
    assert overlay.enqueue_invalidate(1), "an invalidate for a closed component is not an error"
    assert overlay.events.qsize() == queued, "and it is not queued"

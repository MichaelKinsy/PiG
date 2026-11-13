"""``Extension.on_view_abilities`` delivers each change of the replicated client abilities (D614).

The handler runs after the replica took the change, in the order the changes arrived, on a worker thread. The state an extension starts in is not a change, an
equal snapshot is not one, a session without a client runs no handler, and "no client" (``None``) differs from a client that draws no slot. The same behavior is
held across every SDK by test/extension-conformance TestConformance_UIViewAbilitiesEvent.
"""

from __future__ import annotations

import queue
import threading
import unittest

import pig_sdk


def describe(abilities: pig_sdk.ViewAbilities | None) -> str:
    if abilities is None:
        return "none"
    return f"slots={'+'.join(abilities.slots)},mounts={abilities.limits.mounts}"


class ViewAbilitiesEvent(unittest.TestCase):
    def setUp(self) -> None:
        self.ext = pig_sdk.Extension("abilities")
        self.addCleanup(self.ext._shutdown.set)
        self.got: queue.Queue[str] = queue.Queue()

    def apply(self, state: dict) -> None:
        with self.ext._state_lock:
            self.ext._apply_ui_state(state)

    def next(self, want: str) -> None:
        self.assertEqual(self.got.get(timeout=30), want)

    def test_each_change_is_delivered_in_order_after_the_replica(self) -> None:
        self.ext.on_view_abilities(lambda abilities, ctx: self.got.put(f"{describe(abilities)}|{describe(ctx.view_abilities())}"))
        self.ext.on_view_abilities(lambda abilities, ctx: self.got.put(f"second:{describe(abilities)}"))
        client = {"uiAbilities": {"slots": ["panel", "status"], "nodes": ["text"], "limits": {"mounts": 7}}}

        def quiet() -> None:
            # A change that follows proves nothing was queued before it: deliveries are in order.
            self.apply({"uiAbilities": {"slots": ["status"], "limits": {"mounts": 99}}})
            self.next("slots=status,mounts=99|slots=status,mounts=99")
            self.next("second:slots=status,mounts=99")

        self.apply(client)  # the first state is where the extension starts
        quiet()
        self.apply(client)
        self.next("slots=panel+status,mounts=7|slots=panel+status,mounts=7")
        self.next("second:slots=panel+status,mounts=7")
        self.apply(dict(client))  # an equal snapshot is not a change
        quiet()
        self.apply({"uiAbilities": {"slots": [], "limits": {"mounts": 7}}})
        self.next("slots=,mounts=7|slots=,mounts=7")
        self.next("second:slots=,mounts=7")
        self.apply({"hasUI": True})
        self.next("none|none")
        self.next("second:none")

    def test_a_session_without_a_client_runs_no_handler(self) -> None:
        self.ext.on_view_abilities(lambda abilities, ctx: self.got.put("fired"))
        for state in ({"hasUI": True}, {"toolsExpanded": True}, {"hasUI": False}, {"uiAbilities": None}):
            self.apply(state)
        feed = self.ext._view_abilities_feed
        with feed._lock:
            self.assertEqual((len(feed._queue), feed._running), (0, False))
        self.assertTrue(self.got.empty())

    def test_a_blocked_handler_does_not_hold_the_reader(self) -> None:
        entered, gate = threading.Event(), threading.Event()
        self.addCleanup(gate.set)

        def block(abilities: pig_sdk.ViewAbilities | None, ctx: pig_sdk.Context) -> None:
            entered.set()
            gate.wait()

        self.ext.on_view_abilities(block)
        self.apply({})
        self.apply({"uiAbilities": {"slots": ["panel"], "limits": {"mounts": 3}}})
        self.assertTrue(entered.wait(30), "the handler ran")
        self.apply({"uiAbilities": {"slots": ["panel"], "limits": {"mounts": 4}}})
        self.assertEqual(pig_sdk.Context(self.ext).view_abilities().limits.mounts, 4, "the state was applied while the handler blocked")

    def test_a_handler_must_be_callable(self) -> None:
        with self.assertRaises(TypeError):
            self.ext.on_view_abilities(None)  # type: ignore[arg-type]


if __name__ == "__main__":
    unittest.main()

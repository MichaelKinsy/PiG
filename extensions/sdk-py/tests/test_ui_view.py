"""D614 plugin UI mounts as the Python SDK sends and reads them. The behavior against the host is the shared conformance row
TestConformance_UIViewMounts; these cases pin the wire records and the replicated-state decoding."""

from __future__ import annotations

import unittest

import pig_sdk
from pig_sdk import ui_view


class UIViewWireTests(unittest.TestCase):
    def test_intent_wire_names(self) -> None:
        obj = pig_sdk.ViewShape
        intent = pig_sdk.ViewIntent(
            "x.go",
            obj("object", properties={"n": obj("integer")}, required=("n",)),
            node_key="btn",
            answer=obj("array", items=obj("string"), max_items=3),
            cancellable=True,
            timeout_ms=500,
        )
        self.assertEqual(
            intent._wire(),
            {
                "name": "x.go",
                "arg": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]},
                "nodeKey": "btn",
                "answer": {"type": "array", "items": {"type": "string"}, "maxItems": 3},
                "cancellable": True,
                "timeoutMs": 500,
            },
        )

    def test_target_sets_one_member(self) -> None:
        self.assertEqual(pig_sdk.ViewTarget(tool_call_id="c1")._wire(), {"toolCallId": "c1"})

    def test_abilities_decode_and_absence_clears(self) -> None:
        state = ui_view.ViewState()
        state.apply_state({"uiAbilities": {"slots": ["panel"], "nodes": ["text"], "markdown": True, "imageFormats": ["png"], "limits": {"mounts": 7, "intentArgBytes": 123}, "input": {"keyboard": True}}})
        ab = state.abilities()
        assert ab is not None
        self.assertEqual((ab.slots, ab.limits.mounts, ab.limits.intent_arg_bytes, ab.input.keyboard, ab.input.pointer, ab.markdown), (("panel",), 7, 123, True, False, True))
        state.apply_state({})
        self.assertIsNone(state.abilities())

    def test_status_decode_and_malformed_reply(self) -> None:
        st = ui_view.decode_status({"mount": {"key": "k", "epoch": 3, "rev": 2}, "state": "broken", "reason": "owner_gone"})
        self.assertEqual((st.mount, st.state, st.reason), (pig_sdk.ViewRef("k", 3, 2), "broken", "owner_gone"))
        with self.assertRaises(RuntimeError):
            ui_view.decode_status({"state": "live"})

    def test_intent_without_handler_is_refused(self) -> None:
        self.assertEqual(ui_view.handle_intent(ui_view.ViewState(), None, {"intent": "x.go"})["outcome"], "refused")

    def test_handler_result_defaults_to_done(self) -> None:
        state = ui_view.ViewState()
        seen = []
        state.handler = lambda inv: (seen.append((inv.mount, inv.rev, inv.ticket, inv.arg)), pig_sdk.ViewIntentResult(answer={"ok": 1}))[1]
        out = ui_view.handle_intent(state, None, {"mount": {"key": "k", "epoch": 2}, "rev": 5, "intent": "x.go", "arg": {"n": 1}, "ticket": "t7"})
        self.assertEqual(out, {"outcome": "done", "answer": {"ok": 1}})
        self.assertEqual(seen, [(pig_sdk.ViewRef("k", 2, 0), 5, "t7", {"n": 1})])


if __name__ == "__main__":
    unittest.main()

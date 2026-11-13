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


class AmendmentTest(unittest.TestCase):
    """The D614 amendment: claims, the status result, notices and the content members."""

    def test_abilities_carry_claims_slot_claims_and_drawing(self):
        ab = ui_view.decode_abilities(
            {
                "slots": ["panel"],
                "claims": [{"name": "a.flag", "value": True}, {"name": "a.count", "value": 4242}, {"name": "a.list", "value": ["x", "y"]}],
                "slotClaims": [{"slot": "panel", "claims": [{"name": "a.count", "value": 77}]}],
                "drawing": {"profiles": ["scene"], "shapes": 5, "pixels": 16},
            }
        )
        self.assertEqual([(c.name, c.value) for c in ab.claims], [("a.flag", True), ("a.count", 4242), ("a.list", ("x", "y"))])
        self.assertEqual((ab.slot_claims[0].slot, ab.slot_claims[0].claims[0].value), ("panel", 77))
        self.assertEqual((ab.drawing.profiles, ab.drawing.shapes, ab.drawing.pixels), (("scene",), 5, 16))
        plain = ui_view.decode_abilities({"slots": ["panel"]})
        self.assertEqual((plain.claims, plain.slot_claims, plain.drawing), ((), (), None))

    def test_status_carries_the_finish_result(self):
        st = ui_view.decode_status({"mount": {"key": "k", "epoch": 2, "rev": 1}, "state": "finished", "result": {"picked": 77}})
        self.assertEqual(st.result, {"picked": 77})
        self.assertIsNone(ui_view.decode_status({"mount": {"key": "k", "epoch": 2, "rev": 1}, "state": "live"}).result)

    def test_notices_decode_and_a_malformed_reply_raises(self):
        notices = ui_view.decode_notices({"notices": [{"code": "limit", "severity": "error", "path": "update", "measured": 6, "limit": 5, "epoch": 2, "rev": 1, "count": 3}]})
        n = notices[0]
        self.assertEqual((n.code, n.severity, n.path, n.measured, n.limit, n.epoch, n.rev, n.count, n.message), ("limit", "error", "update", 6, 5, 2, 1, 3, ""))
        self.assertEqual(ui_view.decode_notices({"notices": []}), [])
        with self.assertRaises(RuntimeError):
            ui_view.decode_notices({})

    def test_content_carries_the_members_only_when_given(self):
        self.assertEqual(ui_view.encode_content({"root": {}}, (), (), None), {"view": {"root": {}}})
        drawing = {"profile": "scene", "shapes": [{"kind": "rect", "width": 1, "height": 1}]}
        alt = {"when": {"any": [{"all": [{"claim": "a.count", "op": "atLeast", "value": 2}]}]}, "view": {"root": {}}}
        out = ui_view.encode_content({"root": {}}, (), (), None, [alt], drawing, [{"node": "n"}], [{"node": "n", "request": "mount"}])
        self.assertEqual(out, {"view": {"root": {}}, "alternatives": [alt], "drawing": drawing, "a11y": [{"node": "n"}], "focus": [{"node": "n", "request": "mount"}]})
        binding = {"key": "shift+left", "intent": "x.page", "arg": {}}
        self.assertEqual(ui_view.encode_content({"root": {}}, (), (), None, bindings=[binding]), {"view": {"root": {}}, "bindings": [binding]})

    def test_title_and_the_intent_a_hidden_mount_takes(self):
        out = ui_view.encode_content({"root": {}}, [pig_sdk.ViewIntent("x.open", pig_sdk.ViewShape("object"), when_hidden=True)], (), None, focus=[{"node": "n", "request": "update"}], title="Mail")
        self.assertEqual(out, {"view": {"root": {}}, "title": "Mail", "intents": [{"name": "x.open", "arg": {"type": "object"}, "whenHidden": True}], "focus": [{"node": "n", "request": "update"}]})
        self.assertNotIn("title", ui_view.encode_content({"root": {}}, (), (), None, title=""))

    def test_the_intent_the_users_dismissal_raises(self):
        close = pig_sdk.ViewIntent("x.close", pig_sdk.ViewShape("object"), dismiss=True)
        self.assertEqual(close._wire(), {"name": "x.close", "arg": {"type": "object"}, "dismiss": True})
        self.assertNotIn("dismiss", pig_sdk.ViewIntent("x.tap", pig_sdk.ViewShape("object"))._wire())


if __name__ == "__main__":
    unittest.main()

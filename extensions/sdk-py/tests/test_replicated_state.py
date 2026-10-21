"""A state snapshot replicates Pi's ctx.model.id, ctx.model.provider and ctx.sessionManager.getSessionName().

The id is the model's id (its name only when it has no id), an explicit null clears the model, an absent member keeps it, and every session snapshot carries the name
with "" for no name. The Go and Rust SDKs read the same members (test/extension-conformance/replicated_accessors_test.go).
"""

from __future__ import annotations

import unittest

import pig_sdk


class ReplicatedIdentity(unittest.TestCase):
    def test_state_snapshots_replicate_the_model_identity_and_the_session_name(self) -> None:
        ext = pig_sdk.Extension("replicated")
        ctx = pig_sdk.Context(ext)

        def apply(state: dict) -> None:
            with ext._state_lock:
                ext._apply_ui_state(state)

        def read() -> tuple[str, str, str, str]:
            return ctx.model, ctx.model_provider, ctx.model_qualified, ctx.session_name

        self.assertEqual(read(), ("", "", "", ""))
        apply({"model": {"id": "id-sentinel", "name": "Name Sentinel", "provider": {"id": "prov-sentinel"}}, "session": {"sessionName": "s-sentinel"}})
        self.assertEqual(read(), ("id-sentinel", "prov-sentinel", "prov-sentinel/id-sentinel", "s-sentinel"))
        apply({"isIdle": True})
        self.assertEqual(read(), ("id-sentinel", "prov-sentinel", "prov-sentinel/id-sentinel", "s-sentinel"))
        apply({"model": {"id": "", "name": "Only Name", "provider": "flat-provider"}, "session": {"sessionName": ""}})
        self.assertEqual(read(), ("Only Name", "flat-provider", "flat-provider/Only Name", ""))
        apply({"model": None})
        self.assertEqual(read(), ("", "", "", ""))


if __name__ == "__main__":
    unittest.main()

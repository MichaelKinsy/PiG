"""A focused overlay component renders at the width Pi's TUI.resolveOverlayLayout resolves (tui.ts:1212-1233).

The table matches the Node runtime's (coding/extension/host/subprocess/runtime_node_overlay_sizing_test.go). An inline
component and the legacy titled modal render at the terminal width.
"""

from __future__ import annotations

import pig_sdk


def test_overlay_render_width_follows_upstream_layout() -> None:
    cases = [
        ("default", {"overlay": True}, 80),
        ("empty layout", {"overlay": True, "overlayOptions": {}}, 80),
        ("percent width", {"overlay": True, "overlayOptions": {"width": "75%", "maxHeight": "95%", "margin": {"top": 1}}}, 90),
        ("minWidth clamped after margins", {"overlay": True, "overlayOptions": {"width": 20, "minWidth": 200, "margin": 2}}, 116),
        ("percentage before margin", {"overlay": True, "overlayOptions": {"width": "50%", "margin": {"left": 10, "right": 10}}}, 60),
        ("zero width", {"overlay": True, "overlayOptions": {"width": 0}}, 1),
        ("invalid width", {"overlay": True, "overlayOptions": {"width": "oops"}}, 80),
        ("legacy titled modal", {"overlay": True, "title": "Picker"}, 120),
        ("titled overlay with layout", {"overlay": True, "title": "Picker", "overlayOptions": {"width": 30}}, 30),
        ("inline", {}, 120),
    ]
    for name, options, want in cases:
        assert pig_sdk._overlay_render_width(options)(120) == want, name


if __name__ == "__main__":
    test_overlay_render_width_follows_upstream_layout()

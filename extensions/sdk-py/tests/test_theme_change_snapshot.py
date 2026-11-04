"""A theme-change handler receives the theme of its own change.

Handlers run on a worker thread, so one can run after the host applied the next palette. The first handler below holds the delivery of "second" while the host moves on to "third";
the handler behind it must still get "second" (the Go SDK passes a UITheme by value, the Rust SDK a clone), and ctx.theme stays the live theme. The same behavior is held across
every SDK by test/extension-conformance TestOnThemeChangeDeliversEachChangesOwnThemeAcrossSDKs.
"""

from __future__ import annotations

import threading
import unittest

import pig_sdk


def palette(name: str, rgb: int) -> dict:
    return {"name": name, "foregrounds": {"success": f"\x1b[38;2;{rgb};{rgb};{rgb}m"}, "backgrounds": {}}


class ThemeChangeSnapshot(unittest.TestCase):
    def test_a_handler_behind_a_held_delivery_gets_the_theme_of_its_own_change(self) -> None:
        ext = pig_sdk.Extension("feeds")
        ctx = pig_sdk.Context(ext)
        entered, gate, logged = threading.Event(), threading.Event(), threading.Event()
        seen: list[tuple[str, str]] = []

        def hold(theme: pig_sdk.Theme) -> None:
            entered.set()
            gate.wait()

        def log(theme: pig_sdk.Theme) -> None:
            seen.append((theme.name, theme.fg("success", "x")))
            if len(seen) == 2:
                logged.set()

        def apply(value: dict) -> None:
            with ext._state_lock:
                ext._apply_ui_state({"theme": value})

        ctx.on_theme_change(hold)
        ctx.on_theme_change(log)
        try:
            apply(palette("first", 1))  # the first palette is not a change
            apply(palette("second", 2))
            self.assertTrue(entered.wait(30), "the first handler took the delivery of the second theme")
            apply(palette("third", 3))
            self.assertEqual(ctx.theme.name, "third", "the held theme follows the host")
            self.assertEqual(seen, [], "the second handler waits behind the first")
        finally:
            gate.set()
        self.assertTrue(logged.wait(30), "both deliveries reached the second handler")
        self.assertEqual([name for name, _ in seen], ["second", "third"])
        self.assertIn("38;2;2;2;2m", seen[0][1])
        self.assertIn("38;2;3;3;3m", seen[1][1])


if __name__ == "__main__":
    unittest.main()

"""``ctx.theme`` closes a faint token like Pi's Theme (.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/theme/theme.ts).

The host sends each foreground as Pi's getFgAnsi opening, which ends in SGR 2 for a faint token (theme.ts:399-402).
Pi's ``fg`` closes such a token with SGR 22;39 (theme.ts:363), and ``getThinkingBorderColor`` draws through ``fg``,
so the faint run ends at the text instead of leaking into the cells a Text component pads after it.
"""

from __future__ import annotations

import pig_sdk

_PALETTE = {
    "name": "faint",
    "foregrounds": {
        "accent": "\x1b[38;5;5m",
        "muted": "\x1b[39m\x1b[2m",
        "thinkingXhigh": "\x1b[38;5;13m\x1b[2m",
        "bashMode": "\x1b[38;5;2m",
    },
    "backgrounds": {},
    "modifiers": True,
    "mode": "256color",
}


def test_fg_closes_a_faint_token_with_sgr_22_39() -> None:
    theme = pig_sdk.Theme()
    theme._set_palette(_PALETTE)
    assert theme.fg("muted", "x") == "\x1b[39m\x1b[2mx\x1b[22;39m"
    assert theme.fg("accent", "x") == "\x1b[38;5;5mx\x1b[39m"
    assert theme.get_thinking_border_color("xhigh")("x") == "\x1b[38;5;13m\x1b[2mx\x1b[22;39m"
    assert theme.get_bash_mode_border_color()("x") == "\x1b[38;5;2mx\x1b[39m"

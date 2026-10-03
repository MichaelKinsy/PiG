"""Theme.appearance, Theme.colors and Theme.style over the host's palette.

.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/theme/theme.ts:311-367. The vectors are the
escape sequences the host's own tui.ForegroundAnsi and BackgroundAnsi produce for each color in each mode;
test/extension-conformance compares every SDK with the host for the same colors.
"""

from __future__ import annotations

from typing import Any

import pytest

import pig_sdk

ESC = "\x1b["


def _palette(mode: str) -> dict[str, Any]:
    return {
        "name": "dark",
        "appearance": "light",
        "foregrounds": {"success": ESC + "38;2;1;2;3m", "dimmed": ESC + "38;2;9;9;9m" + ESC + "2m", "accent": ESC + "38;5;4m"},
        "backgrounds": {"toolSuccessBg": ESC + "48;2;4;5;6m", "userMessageBg": ESC + "49m"},
        "colors": {
            "success": {"kind": "rgb", "r": 1, "g": 2, "b": 3},
            "accent": {"kind": "oklch", "l": 0.62, "c": 0.1, "h": 200},
            "toolSuccessBg": {"kind": "indexed", "index": 5},
        },
        "modifiers": True,
        "mode": mode,
    }


def _theme(mode: str = "truecolor", palette: dict[str, Any] | None = None) -> pig_sdk.Theme:
    theme = pig_sdk.Theme()
    theme._set_palette(_palette(mode) if palette is None else palette)
    return theme


def test_appearance_and_colors_come_from_the_palette() -> None:
    theme = _theme()
    assert theme.appearance == "light"
    assert theme.colors == {
        "success": {"kind": "rgb", "r": 1, "g": 2, "b": 3},
        "accent": {"kind": "oklch", "l": 0.62, "c": 0.1, "h": 200},
        "toolSuccessBg": {"kind": "indexed", "index": 5},
    }
    # The dict is a copy: a caller cannot change the replicated palette.
    theme.colors["success"]["r"] = 99
    theme.colors["extra"] = {"kind": "indexed", "index": 1}
    assert theme.colors["success"] == {"kind": "rgb", "r": 1, "g": 2, "b": 3}
    assert "extra" not in theme.colors
    # Before any palette there is no appearance and no color.
    assert pig_sdk.Theme().appearance is None
    assert pig_sdk.Theme().colors == {}
    # A color of an unknown kind is dropped, as a token without an escape sequence is.
    sparse = _theme(palette={"appearance": "dark", "colors": {"b": {"kind": "rgb", "r": 1, "g": 2, "b": 3}, "bad": {"kind": "nope"}, "worse": "x"}})
    assert sparse.appearance == "dark"
    assert sparse.colors == {"b": {"kind": "rgb", "r": 1, "g": 2, "b": 3}}


@pytest.mark.parametrize(
    ("name", "options", "want"),
    [
        ("tokens and bold", {"fg": "success", "bg": "toolSuccessBg", "bold": True}, ESC + "38;2;1;2;3m" + ESC + "48;2;4;5;6m" + ESC + "1mx" + ESC + "22m" + ESC + "49m" + ESC + "39m"),
        ("no style", {}, "x"),
        (
            "every attribute",
            {"bold": True, "dim": True, "italic": True, "underline": True, "inverse": True, "strikethrough": True},
            ESC + "1m" + ESC + "2m" + ESC + "3m" + ESC + "4m" + ESC + "7m" + ESC + "9mx" + ESC + "29m" + ESC + "27m" + ESC + "24m" + ESC + "23m" + ESC + "22m",
        ),
        ("faint token adds dim and closes with 22", {"fg": "dimmed"}, ESC + "38;2;9;9;9m" + ESC + "2mx" + ESC + "22m" + ESC + "39m"),
        ("rgb in truecolor", {"fg": {"kind": "rgb", "r": 10, "g": 20, "b": 30}}, ESC + "38;2;10;20;30mx" + ESC + "39m"),
        ("fractional rgb rounds half up", {"fg": {"kind": "rgb", "r": 10.5, "g": 20.4, "b": 29.6}}, ESC + "38;2;11;20;30mx" + ESC + "39m"),
        ("oklch is mapped into sRGB", {"fg": {"kind": "oklch", "l": 0.62, "c": 0.1, "h": 200}}, ESC + "38;2;28;152;158mx" + ESC + "39m"),
        ("an out-of-gamut oklch keeps its hue by losing chroma", {"bg": {"kind": "oklch", "l": 1, "c": 0.3, "h": 150}}, ESC + "48;2;255;255;255mx" + ESC + "49m"),
        ("indexed", {"fg": {"kind": "indexed", "index": 5}}, ESC + "38;5;5mx" + ESC + "39m"),
    ],
)
def test_style_renders_tokens_attributes_and_colors(name: str, options: dict[str, Any], want: str) -> None:
    assert _theme().style("x", options) == want, name


@pytest.mark.parametrize(
    ("color", "want"),
    [
        ({"kind": "rgb", "r": 10, "g": 20, "b": 30}, ESC + "38;5;16mx" + ESC + "39m"),
        ({"kind": "rgb", "r": 128, "g": 128, "b": 130}, ESC + "38;5;244mx" + ESC + "39m"),
        ({"kind": "rgb", "r": 250, "g": 100, "b": 50}, ESC + "38;5;203mx" + ESC + "39m"),
        ({"kind": "oklch", "l": 0.62, "c": 0.1, "h": 200}, ESC + "38;5;31mx" + ESC + "39m"),
        ({"kind": "indexed", "index": 5}, ESC + "38;5;5mx" + ESC + "39m"),
    ],
)
def test_style_maps_a_color_to_the_nearest_palette_entry_in_256_color_mode(color: dict[str, Any], want: str) -> None:
    # theme.ts:342-367 foregroundAnsi.
    assert _theme("256color").style("x", {"fg": color}) == want


@pytest.mark.parametrize(
    ("options", "message"),
    [
        ({"fg": "notAToken"}, "Unknown theme color: notAToken"),
        ({"fg": "toolSuccessBg"}, "Unknown theme color: toolSuccessBg"),
        ({"bg": "success"}, "Unknown theme color: success"),
    ],
)
def test_style_rejects_unknown_tokens_and_tokens_in_the_wrong_slot(options: dict[str, Any], message: str) -> None:
    # theme-style.test.ts:43-48.
    with pytest.raises(ValueError) as error:
        _theme().style("x", options)
    assert str(error.value) == message


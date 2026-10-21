"""pi-tui helpers against the vectors generated from Pi's own functions (test/parity/sdk-bulk/gen.mjs).

Every SDK reproduces the same vectors, so a helper that returns a constant or ports a different rule fails here.
"""

from __future__ import annotations

import json
import math
import os
import re
from pathlib import Path

import pytest

from pig_sdk import tui
from pig_sdk.tui import _colors, _terminal

VECTORS = Path(__file__).resolve().parents[3] / "test" / "parity" / "sdk-bulk"


def load(name: str):
    return json.loads((VECTORS / name).read_text(encoding="utf-8"))


def test_text_vectors_match_pi():
    v = load("tui-text.json")
    assert v["cursorMarker"] == tui.CURSOR_MARKER
    assert len(v["visibleWidth"]) > 1000 and len(v["truncate"]) > 1000 and len(v["wrap"]) > 1000
    for s, w in v["visibleWidth"]:
        assert tui.visible_width(s) == w, s
    for s, out in v["strip"]:
        assert tui.strip_terminal_sequences(s) == out, s
    for s, out in v["bg"]:
        assert tui.get_active_background_ansi(s) == out, s
    for s, w, ellipsis, pad, out in v["truncate"]:
        got = tui.truncate_to_width(s, w) if ellipsis is None else tui.truncate_to_width(s, w, ellipsis, pad)
        assert got == out, (s, w, ellipsis, pad)
    for s, w, out in v["wrap"]:
        assert tui.wrap_text_with_ansi(s, w) == out, (s, w)
    for s, start, n, strict, out, with_width in v["slice"]:
        assert tui.slice_by_column(s, start, n, strict) == out, (s, start, n, strict)
        assert tui.slice_with_width(s, start, n, strict) == (with_width["text"], with_width["width"]), (s, start, n, strict)
    for s, be, a_start, a_len, strict, out in v["segments"]:
        assert tui.extract_segments(s, be, a_start, a_len, strict) == (out["before"], out["beforeWidth"], out["after"], out["afterWidth"]), s
    for s, col, out in v["link"]:
        assert tui.get_osc8_link_at_column(s, col) == out, (s, col)


def _snake(name: str) -> str:
    snake = re.sub(r"([A-Z])", lambda m: "_" + m.group(1).lower(), name)
    return snake + "_" if snake == "return" else snake


def test_key_helper_matches_pi():
    v = load("tui-keys.json")["keyHelper"]
    assert len(v) > 70
    for name, expected in v.items():
        member = getattr(tui.Key, _snake(name))
        if isinstance(expected, list):
            assert [member(k) for k in ["a", "Z", "enter", "f5", "[", "space"]] == expected, name
        else:
            assert member == expected, name


@pytest.mark.parametrize("mode_index", range(4))
def test_key_vectors_match_pi(mode_index, monkeypatch):
    v = load("tui-keys.json")
    mode = v["modes"][mode_index]
    for name in ("WT_SESSION", "SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"):
        monkeypatch.delenv(name, raising=False)
    for name, value in mode["env"].items():
        monkeypatch.setenv(name, value)
    tui.set_kitty_protocol_active(mode["kitty"])
    try:
        universe = v["universe"]
        for index, (data, row) in enumerate(zip(v["data"], mode["rows"])):
            parsed, release, repeat, kitty_printable, printable, matched = row
            assert tui.parse_key(data) == parsed, data
            assert tui.is_key_release(data) == release, data
            assert tui.is_key_repeat(data) == repeat, data
            assert tui.decode_kitty_printable(data) == kitty_printable, data
            assert tui.decode_printable_key(data) == printable, data
            matched_set = set(matched)
            # Every identifier Pi matches must match, and every 11th of the rest (an offset that moves with the row) must not: the full
            # universe is 5 million calls per mode, which Go and Rust run in full.
            for j in matched:
                assert tui.matches_key(data, universe[j]), (data, universe[j])
            for j in range(index % 11, len(universe), 11):
                if j not in matched_set:
                    assert not tui.matches_key(data, universe[j]), (data, universe[j])
    finally:
        tui.set_kitty_protocol_active(False)


def test_fuzzy_vectors_match_pi():
    v = load("tui-fuzzy.json")
    for query, text, matches, score in v["match"]:
        got = tui.fuzzy_match(query, text)
        assert got.matches == matches and (not matches or got.score == score), (query, text)
    for items, query, out in v["filter"]:
        assert tui.fuzzy_filter(items, query, lambda s: s) == out, (items, query)


def test_js_lower_matches_every_code_point():
    from pig_sdk.tui._fuzzy import js_lower

    changed = {cp: "".join(map(chr, to)) for cp, to in load("tui-lower.json")["pairs"]}
    for cp in range(0x110000):
        if 0xD800 <= cp <= 0xDFFF or cp == 0x3A3:
            continue
        assert js_lower(chr(cp)) == changed.get(cp, chr(cp)), hex(cp)


def _color(o):
    if o["kind"] == "indexed":
        return tui.IndexedColor(int(o["index"]))
    if o["kind"] == "rgb":
        return tui.RgbColorValue(o["r"], o["g"], o["b"])
    return tui.OklchColorValue(o["l"], o["c"], o["h"])


def _json(x):
    if isinstance(x, tui.IndexedColor):
        return {"kind": "indexed", "index": float(x.index)}
    if isinstance(x, tui.RgbColorValue):
        return {"kind": "rgb", "r": float(x.r), "g": float(x.g), "b": float(x.b)}
    if isinstance(x, tui.OklchColorValue):
        return {"kind": "oklch", "l": x.l, "c": x.c, "h": x.h}
    return x


def _near(got, want):
    if isinstance(want, (int, float)) and not isinstance(want, bool):
        return isinstance(got, (int, float)) and (got == want or abs(got - want) <= 1e-9 * max(1, abs(want)) or (got != got and want != want))
    if isinstance(want, dict):
        if set(got) != set(want):
            return False
        # A neutral color has no hue; atan2 of rounding noise differs between engines.
        neutral = any(isinstance(want.get(k), (int, float)) and want[k] < 1e-9 for k in ("c", "s"))
        return all(_near(got[k], x) for k, x in want.items() if not (k == "h" and neutral))
    return got == want


def test_color_vectors_match_pi():
    v = load("tui-colors.json")
    assert len(v["ops"]) > 1000
    attrs = ("bold", "dim", "italic", "underline", "inverse", "strikethrough")
    for op, args, want in v["ops"]:
        args = [math.nan if a is None and op not in ("mixColors", "styleTextWithAnsi") else a for a in args]
        try:
            if op == "parseColor":
                got = _json(tui.parse_color(math.nan if args[0] == "NaN" else args[0]))
            elif op == "colorToRgb":
                r = tui.color_to_rgb(_color(args[0]))
                got = {"r": r.r, "g": r.g, "b": r.b}
            elif op == "colorToHex":
                got = tui.color_to_hex(_color(args[0]))
            elif op == "colorToOklch":
                o = tui.color_to_oklch(_color(args[0]))
                got = {"l": o.l, "c": o.c, "h": o.h}
            elif op == "colorToOkhsl":
                o = tui.color_to_okhsl(_color(args[0]))
                got = {"h": o.h, "s": o.s, "l": o.l}
            elif op == "foregroundAnsi":
                got = tui.foreground_ansi(_color(args[0]), args[1])
            elif op == "backgroundAnsi":
                got = tui.background_ansi(_color(args[0]), args[1])
            elif op == "rgbColor":
                got = _json(tui.rgb_color(*args))
            elif op == "oklchColor":
                got = _json(tui.oklch_color(*args))
            elif op == "okhslColor":
                got = _json(tui.okhsl_color(*args))
            elif op == "indexedColor":
                got = _json(tui.indexed_color(args[0]))
            elif op == "mixColors":
                got = _json(tui.mix_colors(_color(args[0]), _color(args[1]), args[2], args[3] or "oklch"))
            elif op == "styleText":
                s = args[1]
                style = tui.TextStyle(**{k: s.get(k, False) for k in attrs}, fg=_color(s["fg"]) if "fg" in s else None, bg=_color(s["bg"]) if "bg" in s else None)
                got = tui.style_text(args[0], style, args[2])
            else:
                got = tui.style_text_with_ansi(args[0], args[1], args[2], tui.TextAttributes(**{k: args[3].get(k, False) for k in attrs}))
            result = {"ok": got}
        except ValueError as e:
            result = {"error": str(e)}
        if "error" in want:
            assert result.get("error") == want["error"], (op, args)
        else:
            assert "ok" in result and _near(result["ok"], want["ok"]), (op, args, result, want)


def test_hyperlink_and_capabilities_match_pi():
    v = load("tui-terminal.json")
    for text, url, out in v["hyperlink"]:
        assert tui.hyperlink(text, url) == out
    probed = 0
    for case in v["capabilities"]:
        probes = []

        def probe(case=case, probes=probes):
            probes.append(1)
            return case["tmux"]

        got = _terminal._detect(case["env"], case["platform"], probe)
        want = case["detected"]
        assert (got.images, got.true_color, got.hyperlinks) == (want["images"], want["trueColor"], want["hyperlinks"]), case
        assert bool(probes) == case["tmuxProbed"], case
        probed += bool(probes)
    assert probed > 0


def test_keybindings_vectors_match_pi():
    v = load("tui-keybindings.json")

    def keys(value):
        return [value] if isinstance(value, str) else list(value)

    for id, want in v["tuiKeybindings"].items():
        got = tui.TUI_KEYBINDINGS[id]
        assert keys(got.default_keys) == keys(want["defaultKeys"]) and got.description == want.get("description"), id
    assert len(tui.TUI_KEYBINDINGS) == len(v["tuiKeybindings"])
    assert {i: keys(k) for i, k in tui.get_keybindings().get_resolved_bindings().items()} == {i: keys(k) for i, k in v["defaultResolved"].items()}
    for n, case in enumerate(v["cases"]):
        definitions = v["merged"] if case["definitions"] == "merged" else v["tuiKeybindings"]
        manager = tui.KeybindingsManager(definitions, case["user"])
        for id, want in case["keys"].items():
            assert manager.get_keys(id) == want, (n, id)
            definition = manager.get_definition(id)
            wanted = case["definition"][id]
            assert (definition is None) == (wanted is None), (n, id)
            if definition is not None:
                assert keys(definition.default_keys) == keys(wanted["defaultKeys"]), (n, id)
            for i, data in enumerate(v["sample"]):
                assert manager.matches(data, id) == case["matches"][id][i], (n, id, data)
        assert manager.get_resolved_bindings() == case["resolved"], n
        assert [(c.key, c.keybindings) for c in manager.get_conflicts()] == [(c["key"], c["keybindings"]) for c in case["conflicts"]], n
        assert {i: keys(k) for i, k in manager.get_user_bindings().items()} == {i: keys(k) for i, k in case["userBindings"].items()}, n
        manager.set_user_bindings({"tui.editor.cursorLeft": "ctrl+g"})
        assert manager.get_keys("tui.editor.cursorLeft") == case["afterSet"]["keys"], n
        assert manager.get_resolved_bindings() == case["afterSet"]["resolved"], n
        assert [(c.key, c.keybindings) for c in manager.get_conflicts()] == [(c["key"], c["keybindings"]) for c in case["afterSet"]["conflicts"]], n

package tui

import "testing"

// Character jump (editor.ts handleInput jump mode and jumpToChar): ctrl+] / ctrl+alt+] wait for a character, then move to the next / previous occurrence of it, searching from just after / before the cursor on its line and the whole line on other lines, in UTF-16 units; the hotkey again cancels, a control character cancels and is handled normally, and a data string of several characters is searched whole.
// Pi source: packages/tui/src/components/editor.ts
func TestEditorCharacterJumpMatchesPi(t *testing.T) {
	const forward, backward = "\x1d", "\x1b\x1d"
	texts := []string{"hello world\nfoo bar\nbaz hello", "日本語 text 日本\nsecond 日本 line", "a😀b😀c\n😀 d\nx😀", "e\u0301e\u0301\nabab\nbaba", "", "a", "aaa\n\naaa", "tab\tx\nx y"}
	targets := []string{"o", "l", "a", "b", "x", "日", "😀", "e", "\u0301", " ", "z", "\t", "ab", "日本", "A", "é"}
	var probes []editorBindingsProbe
	for _, text := range texts {
		// Every cursor position of every line: setText puts the cursor at the end, so reach each one by moving from the document start (four ups and ctrl+a).
		lines := 1
		for _, r := range text {
			if r == '\n' {
				lines++
			}
		}
		for _, hotkey := range []string{forward, backward} {
			for _, target := range targets {
				for line := range lines {
					for col := range 14 {
						ops := editorJumpStart(text)
						for range line {
							ops = append(ops, editorBindingsOp{Keys: new("\x1b[B")}, editorBindingsOp{Keys: new("\x01")})
						}
						for range col {
							ops = append(ops, editorBindingsOp{Keys: new("\x1b[C")})
						}
						ops = append(ops, editorBindingsOp{Keys: new(hotkey)}, editorBindingsOp{Keys: new(target)})
						probes = append(probes, editorBindingsProbe{Ops: ops})
					}
				}
			}
		}
	}
	// Excluded: CSI-u shift+letter (\x1b[101;2u): PiG decodes the uppercase letter, Pi the lowercase one (handoffs/owner-signoff.md, Shift+letter uppercase fallback).
	// Repeated jumps, cancel by the hotkey (either), a control character, an escape sequence and a printable key that is not a character.
	text := "one two three\nfour five six\nseven"
	for _, script := range [][]string{
		{forward, "e", forward, "e", forward, "e"}, {backward, "e", backward, "e", backward, "e"}, {forward, forward, "e"}, {forward, backward, "e"}, {backward, forward, "e"},
		{forward, "\x01", "e"}, {forward, "\x1b[C", "e"}, {forward, "\x1b[A", "x"}, {backward, "\x7f", "e"}, {forward, "\r", "e"}, {forward, "\x1b"}, {forward, "\x1b[27;5;101~"}, {forward, "\x1b[101;1u", "e"},
		{backward, "\x1b[101u"}, {forward, "e", "\x1f"}, {forward, " "}, {forward, "\x7f"}, {forward, "\x03"},
	} {
		ops := editorJumpStart(text)
		for _, keys := range script {
			ops = append(ops, editorBindingsOp{Keys: new(keys)})
		}
		probes = append(probes, editorBindingsProbe{Ops: ops})
	}
	compareEditorProbesWithPi(t, probes)
}

// editorJumpStart sets the text and moves the cursor to the document start, one key per op.
func editorJumpStart(text string) []editorBindingsOp {
	ops := []editorBindingsOp{{SetText: &text}}
	for _, keys := range []string{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x01"} {
		ops = append(ops, editorBindingsOp{Keys: new(keys)})
	}
	return ops
}

package tui

import (
	"strings"
	"testing"
)

// user-message styled-box rendering.

func TestUserMessageBlock_AppliesBackgroundToEveryLine(t *testing.T) {
	// Multi-line user input must have bg paint on every rendered
	// row (incl. the padding rows). Pre-fix bug analog: blockquote
	// rendering dropped the `│` bar on continuation lines.
	b := NewUserMessageBlock("hello world\nsecond line\nthird line")
	out := b.Render(60)
	if len(out) < 5 {
		t.Fatalf("expected ≥5 rows (top pad + 3 content + bottom pad), got %d:\n%v", len(out), out)
	}
	for i, line := range out {
		if !strings.HasPrefix(line, UserMessageBgOpen()) {
			t.Errorf("row %d missing bg-open prefix: %q", i, line)
		}
		if !strings.HasSuffix(line, BgClose()) {
			t.Errorf("row %d missing bg-close suffix: %q", i, line)
		}
	}
}

func TestUserMessageBlock_PadsToFullWidth(t *testing.T) {
	// The bg paint must extend to the full width so the user sees
	// a continuous coloured rectangle, not a ragged-right box.
	b := NewUserMessageBlock("hi")
	out := b.Render(40)
	for i, line := range out {
		visWidth := lineDisplayWidth(line)
		if visWidth != 40 {
			t.Errorf("row %d visible width=%d want=40 (line=%q)", i, visWidth, line)
		}
	}
}

func TestUserMessageBlock_LLMOutputCannotMimic(t *testing.T) {
	// Regression test for the live-use bug that motivated 2.19b:
	// an LLM responding with text containing literal `> ` lines
	// (e.g. when reciting a system prompt that quotes things) must
	// NOT visually match a user-message block. A user block uses
	// raw ANSI bg paint; an LLM markdown response can only emit
	// markdown, which cannot produce raw bg ANSI by construction.
	//
	// We assert by structural inspection: the user-block opens with
	// the bg-open ANSI escape (\x1b[48;5;...m). Any markdown
	// rendering: including blockquote text: passes through the
	// Markdown renderer which never emits that escape.
	user := NewUserMessageBlock("say hi").Render(20)
	if len(user) == 0 || !strings.HasPrefix(user[0], UserMessageBgOpen()) {
		t.Fatalf("user block first row should start with bg-open escape, got: %q", user[0])
	}

	// Sanity check: the markdown renderer used inside the block
	// produces text WITHOUT a leading bg-open escape on its own.
	// (UserMessageBlock applies the bg as a wrapper.)
	plain := NewMarkdown("> blockquote text\n> more").Render(20)
	for i, line := range plain {
		if strings.HasPrefix(line, UserMessageBgOpen()) {
			t.Errorf("plain markdown row %d unexpectedly has user-block bg: %q", i, line)
		}
	}
}

func TestUserMessageBlock_PreservesInlineMarkdown(t *testing.T) {
	// Bold/italic/code from the user's input should still render -
	// the bg paint wraps but doesn't strip the inline ANSI.
	b := NewUserMessageBlock("this has **bold** and `code`")
	out := b.Render(50)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "\x1b[1m") {
		t.Errorf("expected bold ANSI inside user block: %q", joined)
	}
	// Inline code uses theme color (not always \x1b[7m).
	if !strings.Contains(joined, "code") {
		t.Errorf("expected code content inside user block: %q", joined)
	}
}

func TestUserMessageBlock_NarrowWidthClamp(t *testing.T) {
	// Width below the minimum still produces output without panicking.
	b := NewUserMessageBlock("xyz")
	out := b.Render(2)
	if len(out) == 0 {
		t.Fatal("expected output even at narrow width")
	}
}

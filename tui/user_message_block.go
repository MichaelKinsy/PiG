package tui

import "strings"

// UserMessageBlock renders a user message in a styled box that the LLM
// cannot accidentally mimic. Replaces the prior
// markdown-blockquote rendering (`> **You:** ...`) which was
// ambiguous: any LLM output containing literal `> ` lines (e.g. when
// asked to recite documentation that quotes things) rendered with the
// same visual treatment as the user's own messages.
//
// Why ANSI bg paint and not blockquote: the LLM emits markdown text;
// pig's chat renders that markdown via the standard markdown
// renderer. Markdown CANNOT produce raw ANSI escape sequences in its
// text output, so a user-message visual treatment that depends on raw
// ANSI bg is by construction non-mimickable.
//
// Visual: each rendered line is left-padded by 1, padded to full
// width, and wrapped with a dark-grey background ANSI escape
// (`\x1b[48;5;236m...\x1b[49m`). Mirrors upstream
// `modes/interactive/components/user-message.ts::UserMessageComponent`
// (v0.69.0) which uses `theme.bg("userMessageBg", content)` with
// dark-theme `userMsgBg = #343541`. xterm-256 color 236 (~`#303030`)
// is the closest widely-supported approximation; truecolor would
// match exactly but a 256-color escape is universally rendered the
// same way across modern terminals.
//
// The inner content is rendered through the existing Markdown
// component, so user messages still get bold/italic/code-fence
// formatting. The bg is painted AFTER markdown rendering so ANSI
// foreground sequences from inline code etc. compose correctly.
type UserMessageBlock struct {
	invalidatable
	content   string
	inner     *Markdown
	outputPad int
}

// NewUserMessageBlock returns a component that renders text as a
// styled user-message block. The text is interpreted as markdown.
func NewUserMessageBlock(text string) *UserMessageBlock {
	return &UserMessageBlock{
		content:   text,
		inner:     NewMarkdown(text),
		outputPad: 1,
	}
}

// Note: bg open/close constants moved to bg_paint.go (truecolor RGB,
// shared with tool-execution lifecycle paint, row 2.9a).
//
// Promoted from xterm-256 `[48;5;236m` (`#303030`, neutral grey) to
// 24-bit `[48;2;52;53;65m` (`#343541`, exact match upstream
// `userMessageBg` in `theme/dark.json:9`). The 256-floor lost the
// blue tint; truecolor is universally honored by the same terminals
// pig already targets.

// SetOutputPad changes the horizontal content padding.
func (u *UserMessageBlock) SetOutputPad(padding int) {
	u.outputPad = max(0, min(1, padding))
	u.Invalidate()
}

func (u *UserMessageBlock) Render(width int) []string {
	if width < 3 {
		width = 3
	}
	// Upstream user-message.ts renders the body through Markdown with
	// color: theme.fg("userMessageText", ...), resolved at render time so a
	// theme switch is honored. SetDefaultColor is a no-op when unchanged.
	u.inner.SetDefaultColor(ActiveTheme().UserMessageText)
	// Render the markdown into the inner content area. Reserve 1
	// column on each side for padding so text doesn't sit flush
	// against the bg edge.
	contentWidth := max(width-u.outputPad*2, 1)
	inner := u.inner.Render(contentWidth)
	leftPad := strings.Repeat(" ", u.outputPad)

	out := make([]string, 0, capHint(len(inner), 2))
	// Top padding row.
	out = append(out, paintBgWith(UserMessageBgOpen(), "", width))
	for _, line := range inner {
		out = append(out, paintBgWith(UserMessageBgOpen(), leftPad+line, width))
	}
	// Bottom padding row.
	out = append(out, paintBgWith(UserMessageBgOpen(), "", width))
	return out
}

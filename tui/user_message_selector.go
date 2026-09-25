package tui

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// UserMessageSelector renders the upstream /fork user-message picker in the
// editor slot. It mirrors user-message-selector.ts: a title, descriptive copy,
// dynamic borders, and a chronological user-message list with the newest
// message selected by default.
type UserMessageSelector struct {
	invalidatable
	messages      []string
	selectedIndex int
	maxVisible    int
	done          bool
	cancelled     bool
}

// NewUserMessageSelector creates a selector for chronological user-message
// texts (oldest to newest). The newest message is selected initially.
func NewUserMessageSelector(messages []string) *UserMessageSelector {
	sel := &UserMessageSelector{
		messages:      append([]string(nil), messages...),
		selectedIndex: max(0, len(messages)-1),
		maxVisible:    10,
	}
	if len(messages) == 0 {
		sel.selectedIndex = -1
	}
	return sel
}

func (s *UserMessageSelector) Done() bool      { return s.done }
func (s *UserMessageSelector) Cancelled() bool { return s.cancelled }

// SelectedIndex returns the selected chronological message index, or -1 when
// no message was selected.
func (s *UserMessageSelector) SelectedIndex() int {
	if s.selectedIndex < 0 || s.selectedIndex >= len(s.messages) {
		return -1
	}
	return s.selectedIndex
}

func (s *UserMessageSelector) Render(width int) []string {
	th := ActiveTheme()
	accent := th.Accent
	if accent == "" {
		accent = "\x1b[38;2;138;190;183m"
	}
	muted := th.Muted
	if muted == "" {
		muted = "\x1b[2m"
	}
	border := NewDynamicBorder("")
	// pig divergence (D66): Bound every user-message list row after adding
	// cursors and metadata. Upstream leaves these rows over-wide in narrow panes.
	fit := func(line string) string { return widthx.TruncateToWidth(line, width, "", false) }

	// Header: Spacer(1) + Text(bold title, 1, 0) + Text(muted description, 1, 0)
	// + Spacer(1) + DynamicBorder + Spacer(1), as in upstream.
	lines := []string{""}
	lines = append(lines, NewPaddedText("\x1b[1mFork from Message\x1b[22m", 1, 0, nil).Render(width)...)
	description := "Select a user message to copy the active path up to that point into a new session"
	lines = append(lines, NewPaddedText(muted+description+"\x1b[0m", 1, 0, nil).Render(width)...)
	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)
	lines = append(lines, "")

	if len(s.messages) == 0 {
		lines = append(lines, fit(muted+"  No user messages found"+"\x1b[0m"))
		lines = append(lines, "")
		lines = append(lines, border.Render(width)...)
		return lines
	}

	start := max(0, min(s.selectedIndex-s.maxVisible/2, len(s.messages)-s.maxVisible))
	end := min(start+s.maxVisible, len(s.messages))
	for i := start; i < end; i++ {
		msg := widthx.TruncateToWidth(normalizeToSingleLine(s.messages[i]), width-2, "...", false)
		if i == s.selectedIndex {
			lines = append(lines, fit(accent+"› "+"\x1b[39m"+"\x1b[1m"+msg+"\x1b[22m"))
		} else {
			lines = append(lines, fit("  "+msg))
		}
		meta := fmt.Sprintf("  Message %d of %d", i+1, len(s.messages))
		lines = append(lines, fit(muted+meta+"\x1b[0m"))
		lines = append(lines, "")
	}
	if start > 0 || end < len(s.messages) {
		scroll := fmt.Sprintf("  (%d/%d)", s.selectedIndex+1, len(s.messages))
		lines = append(lines, fit(muted+scroll+"\x1b[0m"))
	}
	lines = append(lines, "")
	lines = append(lines, border.Render(width)...)
	return lines
}

func (s *UserMessageSelector) HandleInput(data string) {
	kb := GetTUIKeybindings()
	switch {
	case kb.Matches(data, KBSelectCancel):
		s.cancelled = true
		s.done = true
	case kb.Matches(data, KBSelectConfirm):
		if len(s.messages) > 0 {
			s.done = true
		}
	case kb.Matches(data, KBSelectUp):
		if len(s.messages) > 0 {
			if s.selectedIndex <= 0 {
				s.selectedIndex = len(s.messages) - 1
			} else {
				s.selectedIndex--
			}
		}
	case kb.Matches(data, KBSelectDown):
		if len(s.messages) > 0 {
			if s.selectedIndex >= len(s.messages)-1 {
				s.selectedIndex = 0
			} else {
				s.selectedIndex++
			}
		}
	}
	s.Invalidate()
}

package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// UserMessageItem is one selectable user message (user-message-selector.ts UserMessageItem).
type UserMessageItem struct {
	// ID is the entry ID in the session.
	ID string
	// Text is the message text.
	Text string
	// Timestamp is the message's optional timestamp.
	Timestamp string
}

// UserMessageSelectorComponent renders the upstream /fork user-message picker in the
// editor slot. It mirrors user-message-selector.ts: it is a Container of a
// spacer, the title and descriptive copy, a border, the user-message list and a
// closing border, with the newest message selected by default.
type UserMessageSelectorComponent struct {
	Container
	list *UserMessageList
}

// UserMessageList is upstream's UserMessageList: the chronological user-message rows.
type UserMessageList struct {
	invalidatable
	messages      []UserMessageItem
	selectedIndex int
	maxVisible    int
	// OnSelect runs with the entry ID of the selected message on confirm.
	OnSelect func(entryID string)
	// OnCancel runs on the cancel key.
	OnCancel func()
}

// userMessageSelectorEmptyCancelDelay is the setTimeout delay of the empty-list auto-cancel (user-message-selector.ts:146).
const userMessageSelectorEmptyCancelDelay = 100 * time.Millisecond

// NewUserMessageSelectorComponent creates a selector for chronological user messages (oldest to newest). The message with
// initialSelectedID is selected initially, else the newest; an empty initialSelectedID stands for an omitted argument. With
// no messages, onCancel runs once after 100ms on a timer goroutine (user-message-selector.ts:146-148), so it must be safe to
// call from there.
func NewUserMessageSelectorComponent(messages []UserMessageItem, onSelect func(entryID string), onCancel func(), initialSelectedID string) *UserMessageSelectorComponent {
	list := &UserMessageList{messages: append([]UserMessageItem(nil), messages...), maxVisible: 10, OnSelect: onSelect, OnCancel: onCancel}
	list.selectedIndex = max(0, len(messages)-1)
	if initialSelectedID != "" {
		if index := slices.IndexFunc(messages, func(message UserMessageItem) bool { return message.ID == initialSelectedID }); index >= 0 {
			list.selectedIndex = index
		}
	}
	sel := &UserMessageSelectorComponent{list: list}
	th := ActiveTheme()
	sel.Add(NewSpacer(1))
	sel.Add(NewPaddedText(th.Bold("Fork from Message"), 1, 0, nil))
	sel.Add(NewPaddedText(th.Fg("muted", "Select a user message to copy the active path up to that point into a new session"), 1, 0, nil))
	sel.Add(NewSpacer(1))
	sel.Add(NewDynamicBorder())
	sel.Add(NewSpacer(1))
	sel.Add(list)
	sel.Add(NewSpacer(1))
	sel.Add(NewDynamicBorder())
	if len(messages) == 0 && onCancel != nil {
		time.AfterFunc(userMessageSelectorEmptyCancelDelay, onCancel)
	}
	return sel
}

// GetMessageList returns the message list (user-message-selector.ts getMessageList).
func (s *UserMessageSelectorComponent) GetMessageList() *UserMessageList { return s.list }

// HandleInput forwards to the message list, as the host drives upstream's getMessageList(). A parent Container reuses this component's lines until it is invalidated, so a list change must invalidate it.
func (s *UserMessageSelectorComponent) HandleInput(data string) {
	s.list.HandleInput(data)
	s.Invalidate()
}

func (s *UserMessageList) Render(width int) []string {
	th := ActiveTheme()
	// pig divergence (D66): Bound every user-message list row after adding
	// cursors and metadata. Upstream leaves these rows over-wide in narrow panes.
	fit := func(line string) string { return widthx.TruncateToWidth(line, width, "", false) }

	var lines []string
	if len(s.messages) == 0 {
		return []string{fit(th.Fg("muted", "  No user messages found"))}
	}

	start := max(0, min(s.selectedIndex-s.maxVisible/2, len(s.messages)-s.maxVisible))
	end := min(start+s.maxVisible, len(s.messages))
	for i := start; i < end; i++ {
		msg := widthx.TruncateToWidth(strings.TrimFunc(strings.ReplaceAll(s.messages[i].Text, "\n", " "), isJSWhitespace), width-2, "...", false)
		if i == s.selectedIndex {
			lines = append(lines, fit(th.Fg("accent", "› ")+th.Bold(msg)))
		} else {
			lines = append(lines, fit("  "+msg))
		}
		meta := fmt.Sprintf("  Message %d of %d", i+1, len(s.messages))
		lines = append(lines, fit(th.Fg("muted", meta)))
		lines = append(lines, "")
	}
	if start > 0 || end < len(s.messages) {
		scroll := fmt.Sprintf("  (%d/%d)", s.selectedIndex+1, len(s.messages))
		lines = append(lines, fit(th.Fg("muted", scroll)))
	}
	return lines
}

// HandleInput checks up, down, confirm and cancel in user-message-selector.ts order, so a key bound to two of them keeps the earlier action.
func (s *UserMessageList) HandleInput(data string) {
	kb := GetTUIKeybindings()
	switch {
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
	case kb.Matches(data, KBSelectConfirm):
		if s.selectedIndex >= 0 && s.selectedIndex < len(s.messages) && s.OnSelect != nil {
			s.OnSelect(s.messages[s.selectedIndex].ID)
		}
	case kb.Matches(data, KBSelectCancel):
		if s.OnCancel != nil {
			s.OnCancel()
		}
	}
	s.Invalidate()
}

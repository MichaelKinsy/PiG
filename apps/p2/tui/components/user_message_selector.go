package components

// Copied from coding-agent interactive/components/user-message-selector.ts (`/fork`, p2-tui-port.md section 10.8). Changes: the instance theme
// and keymap (section 5.4) instead of the globals; message text is server text (`LineText`); the selector is the focused overlay component
// itself (`HandleInput`), and "No user messages found" is the caller's status line instead of an auto-cancel timer.
//
// pi: apps/p2/src/tui/components/user-message-selector.ts

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// UserMessageItem is a user message of the conversation.
type UserMessageItem struct {
	// ID is the entry ID in the conversation.
	ID   int
	Text string
}

// userMessageList is the custom user message list component with selection.
type userMessageList struct {
	messages      []UserMessageItem
	selectedIndex int
	onSelect      func(entryID int)
	onCancel      func()
	maxVisible    int
	ctx           SelectorContext
}

func newUserMessageList(ctx SelectorContext, messages []UserMessageItem, initialSelectedID *int) *userMessageList {
	l := &userMessageList{ctx: ctx, messages: messages, maxVisible: 10}
	initialIndex := -1
	if initialSelectedID != nil {
		for i, message := range messages {
			if message.ID == *initialSelectedID {
				initialIndex = i
				break
			}
		}
	}
	// Start with the selected message if provided, else the most recent.
	if initialIndex >= 0 {
		l.selectedIndex = initialIndex
	} else {
		l.selectedIndex = max(0, len(messages)-1)
	}
	return l
}

func (l *userMessageList) Invalidate() {}

func (l *userMessageList) Render(width int) []string {
	theme := l.ctx.Theme().Current()
	if len(l.messages) == 0 {
		return []string{theme.Fg("muted", "  No user messages found")}
	}
	var lines []string
	startIndex := max(0, min(l.selectedIndex-l.maxVisible/2, len(l.messages)-l.maxVisible))
	endIndex := min(startIndex+l.maxVisible, len(l.messages))
	// Two lines per message and a blank line.
	for i := startIndex; i < endIndex; i++ {
		isSelected := i == l.selectedIndex
		normalized := strings.TrimFunc(render.LineText(l.messages[i].Text), widthx.IsJSSpace)
		cursor := "  "
		if isSelected {
			cursor = theme.Fg("accent", "› ")
		}
		truncated := widthx.TruncateToWidth(normalized, width-2, "…", false)
		if isSelected {
			truncated = theme.Bold(truncated)
		}
		lines = append(lines, cursor+truncated, theme.Fg("muted", "  Message "+strconv.Itoa(i+1)+" of "+strconv.Itoa(len(l.messages))), "")
	}
	if startIndex > 0 || endIndex < len(l.messages) {
		lines = append(lines, theme.Fg("muted", "  ("+strconv.Itoa(l.selectedIndex+1)+"/"+strconv.Itoa(len(l.messages))+")"))
	}
	return lines
}

func (l *userMessageList) HandleInput(data string) {
	keys := l.ctx.Keys()
	switch {
	// Up: the previous (older) message, wrapping to the bottom.
	case keys.Matches(data, "tui.select.up"):
		if l.selectedIndex == 0 {
			l.selectedIndex = len(l.messages) - 1
		} else {
			l.selectedIndex--
		}
	// Down: the next (newer) message, wrapping to the top.
	case keys.Matches(data, "tui.select.down"):
		if l.selectedIndex == len(l.messages)-1 {
			l.selectedIndex = 0
		} else {
			l.selectedIndex++
		}
	// Enter selects the message and branches.
	case keys.Matches(data, "tui.select.confirm"):
		if l.selectedIndex >= 0 && l.selectedIndex < len(l.messages) && l.onSelect != nil {
			l.onSelect(l.messages[l.selectedIndex].ID)
		}
	case keys.Matches(data, "tui.select.cancel"):
		if l.onCancel != nil {
			l.onCancel()
		}
	}
	l.ctx.RequestRender()
}

// UserMessageSelectorComponent renders a user message selector for branching.
type UserMessageSelectorComponent struct {
	*tui.Container
	focused     bool
	messageList *userMessageList
}

// NewUserMessageSelector is the UserMessageSelectorComponent constructor; initialSelectedID is nil for the most recent.
func NewUserMessageSelector(ctx SelectorContext, messages []UserMessageItem, onSelect func(entryID int), onCancel func(), initialSelectedID *int) *UserMessageSelectorComponent {
	c := &UserMessageSelectorComponent{Container: tui.NewContainer()}
	theme := ctx.Theme().Current()
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(tui.NewPaddedText(theme.Bold("Fork from Message"), 1, 0, nil))
	c.AddChild(tui.NewPaddedText(theme.Fg("muted", "Select a user message to copy the active path up to that point into a new session"), 1, 0, nil))
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder(ctx, ""))
	c.AddChild(tui.NewSpacer(1))
	c.messageList = newUserMessageList(ctx, messages, initialSelectedID)
	c.messageList.onSelect = onSelect
	c.messageList.onCancel = onCancel
	c.AddChild(c.messageList)
	c.AddChild(tui.NewSpacer(1))
	c.AddChild(NewDynamicBorder(ctx, ""))
	return c
}

// SetFocused is the TypeScript's `focused` field.
func (c *UserMessageSelectorComponent) SetFocused(focused bool) { c.focused = focused }

// HandleInput forwards to the list.
func (c *UserMessageSelectorComponent) HandleInput(data string) { c.messageList.HandleInput(data) }

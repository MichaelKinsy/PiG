package tui

import (
	"fmt"
	"strconv"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// NativeNode reports the picker as a selector whose item ids are the
// messages' chronological indexes. pig additive (D91): each row's "Message i
// of n" line is its detail, and an empty list reports the empty message as
// the status.
func (s *UserMessageSelectorComponent) NativeNode() (frontend.Node, bool) {
	list := s.list
	node := frontend.Selector{
		Title:       "Fork from Message",
		Description: "Select a user message to copy the active path up to that point into a new session",
		Items:       make([]frontend.SelectorItem, len(list.messages)),
	}
	if len(list.messages) == 0 {
		node.Status = "No user messages found"
	}
	for i, message := range list.messages {
		node.Items[i] = frontend.SelectorItem{
			ID:     strconv.Itoa(i),
			Label:  plainText(normalizeToSingleLine(message.Text)),
			Detail: fmt.Sprintf("Message %d of %d", i+1, len(list.messages)),
		}
	}
	if list.selectedIndex >= 0 && list.selectedIndex < len(list.messages) {
		node.Selected = strconv.Itoa(list.selectedIndex)
	}
	return node, true
}

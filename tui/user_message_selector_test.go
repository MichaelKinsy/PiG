package tui

// pi: packages/coding-agent/src/modes/interactive/components/user-message-selector.ts

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUserMessageSelectorRenderMatchesUpstreamLayout(t *testing.T) {
	sel := newUserMessageSelectorForTest([]string{
		"reply with exactly: alpha",
		"reply with exactly: beta",
	})

	got := stripUserMessageSelectorANSILines(sel.Render(80))
	want := []string{
		"",
		" Fork from Message",
		" Select a user message to copy the active path up to that point into a new",
		" session",
		"",
		strings.Repeat("─", 80),
		"",
		"  reply with exactly: alpha",
		"  Message 1 of 2",
		"",
		"› reply with exactly: beta",
		"  Message 2 of 2",
		"",
		"",
		strings.Repeat("─", 80),
	}
	if len(got) != len(want) {
		t.Fatalf("line count = %d, want %d\n%q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestUserMessageSelectorHandleInputMovesSelectionAndConfirms(t *testing.T) {
	var selected []string
	cancels := 0
	sel := NewUserMessageSelectorComponent(userMessageItems([]string{"alpha", "beta"}), func(id string) { selected = append(selected, id) }, func() { cancels++ }, "")
	sel.HandleInput("\x1b[A") // Up
	got := stripUserMessageSelectorANSILines(sel.Render(40))
	if got[8] != "› alpha" {
		t.Fatalf("selected row after Up = %q, want %q", got[8], "› alpha")
	}
	if got[11] != "  beta" {
		t.Fatalf("second row after Up = %q, want %q", got[11], "  beta")
	}
	sel.HandleInput("\r")
	if !slices.Equal(selected, []string{"0"}) || cancels != 0 {
		t.Fatalf("callbacks after Up, Enter = select %v cancel %d, want select [0] cancel 0 (user-message-selector.ts:98-104)", selected, cancels)
	}
}

func TestUserMessageSelectorEmptyState(t *testing.T) {
	sel := newUserMessageSelectorForTest(nil)
	got := stripUserMessageSelectorANSILines(sel.Render(40))
	// Upstream: the list renders only the empty-state row, then Spacer(1) and
	// the bottom DynamicBorder.
	tail := []string{"  No user messages found", "", strings.Repeat("─", 40)}
	if len(got) < len(tail) || !slices.Equal(got[len(got)-len(tail):], tail) {
		t.Fatalf("empty state tail = %q, want %q", got, tail)
	}
}

func stripUserMessageSelectorANSILines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		// Upstream Text rows pad to the render width; compare the content.
		out[i] = strings.TrimRight(stripANSI(line), " ")
	}
	return out
}

// UserMessageSelectorComponent extends Container (user-message-selector.ts:92): spacer, title, description, spacer, border,
// spacer, message list, spacer, border; a key on the selector moves the list's selection.
func TestUserMessageSelectorComponentChildrenFollowUpstream(t *testing.T) {
	s := newUserMessageSelectorForTest([]string{"one", "two"})
	if got := len(s.Children()); got != 9 {
		t.Fatalf("children = %d, want 9", got)
	}
	before := strings.Join(s.Render(40), "\n")
	s.HandleInput("\x1b[A")
	if strings.Join(s.Render(40), "\n") == before {
		t.Fatal("up did not change the render")
	}
}

// userMessageItems numbers texts as session entries "0", "1", ...
func userMessageItems(texts []string) []UserMessageItem {
	items := make([]UserMessageItem, len(texts))
	for i, text := range texts {
		items[i] = UserMessageItem{ID: strconv.Itoa(i), Text: text}
	}
	return items
}

func newUserMessageSelectorForTest(texts []string) *UserMessageSelectorComponent {
	return NewUserMessageSelectorComponent(userMessageItems(texts), nil, nil, "")
}

// packages/coding-agent/src/modes/interactive/components/user-message-selector.ts:25-27,146-148,152 getMessageList() is the list the
// selector shows; initialSelectedId selects that message (unknown or empty selects the newest); an empty selector cancels itself once
// after 100ms; confirm and cancel report the entry id and the cancel.
func TestUserMessageSelectorInitialSelectionAndAutoCancel(t *testing.T) {
	items := userMessageItems([]string{"a", "b", "c"})
	var selected []string
	for id, want := range map[string]string{"": "2", "1": "1", "0": "0", "missing": "2"} {
		selected = nil
		sel := NewUserMessageSelectorComponent(items, func(id string) { selected = append(selected, id) }, nil, id)
		if sel.GetMessageList() == nil || sel.GetMessageList() != sel.Children()[6] {
			t.Fatal("GetMessageList is not the list child")
		}
		sel.HandleInput("\r")
		if !slices.Equal(selected, []string{want}) {
			t.Fatalf("initialSelectedID %q selected %v, want [%s]", id, selected, want)
		}
	}
	cancelled := make(chan struct{}, 2)
	NewUserMessageSelectorComponent(nil, nil, func() { cancelled <- struct{}{} }, "")
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("an empty selector did not cancel itself")
	}
	cancels := 0
	nonEmpty := NewUserMessageSelectorComponent(items, nil, func() { cancels++ }, "")
	nonEmpty.HandleInput("\x1b")
	if cancels != 1 {
		t.Fatalf("cancel key ran onCancel %d times, want 1", cancels)
	}
}

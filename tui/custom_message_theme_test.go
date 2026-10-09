package tui

import (
	"slices"
	"testing"
)

// custom-message.ts:55-58 rebuilds the children on invalidate(), so a theme change recolors the label of a message already in the chat. The production theme switch invalidates the whole tree (presentation_theme.go).
func TestCustomMessageLabelFollowsAThemeChange(t *testing.T) {
	original := ActiveTheme().Name
	t.Cleanup(func() { SetThemeByName(original) })
	SetTheme("dark")
	message := NewCustomMessageComponent(&CustomMessage{CustomType: "note", Content: "body"}, nil, nil, 1)
	_ = message.Render(40)

	SetTheme("light")
	message.Invalidate()
	got := message.Render(40)
	want := NewCustomMessageComponent(&CustomMessage{CustomType: "note", Content: "body"}, nil, nil, 1).Render(40)
	if !slices.Equal(got, want) {
		t.Fatalf("after a theme change\n got %q\nwant %q", got, want)
	}
}

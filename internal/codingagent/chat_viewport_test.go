package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TestCreateChatViewportDefaultsScrollbarToAuto mirrors upstream
// test/chat-viewport.test.ts "defaults the transcript scrollbar to auto and
// accepts overrides".
func TestCreateChatViewportDefaultsScrollbarToAuto(t *testing.T) {
	options := func(scrollbar string) ChatViewportOptions {
		return ChatViewportOptions{
			Document:        tui.NewContainer(),
			PendingMessages: tui.NewContainer(),
			Status:          tui.NewContainer(),
			Editor:          tui.NewContainer(),
			Footer:          tui.NewContainer(),
			Scrollbar:       scrollbar,
		}
	}
	automatic := CreateChatViewport(options(""))
	hidden := CreateChatViewport(options("hidden"))
	if got := automatic.Transcript.Scrollbar(); got != "auto" {
		t.Fatalf("default transcript scrollbar = %q, want auto", got)
	}
	if got := hidden.Transcript.Scrollbar(); got != "hidden" {
		t.Fatalf("overridden transcript scrollbar = %q, want hidden", got)
	}
}

// TestCreateChatViewportPinsDockUnderTranscript checks the layout shape: the
// transcript fills the rows above the dock, and the optional widget slots sit
// around the editor in upstream order.
func TestCreateChatViewportPinsDockUnderTranscript(t *testing.T) {
	text := func(s string) tui.Component { return tui.NewText(s) }
	viewport := CreateChatViewport(ChatViewportOptions{
		Document:        text("doc1\ndoc2\ndoc3\ndoc4\ndoc5\ndoc6"),
		PendingMessages: text("pending"),
		Status:          text("status"),
		WidgetsAbove:    text("above"),
		Editor:          text("editor"),
		WidgetsBelow:    text("below"),
		Footer:          text("footer"),
	})
	// The editor slot keeps upstream's three-row minimum.
	frame := tui.RenderLayoutFrame(viewport.Root, 20, 12, nil)
	var rows []string
	for _, line := range frame.Lines {
		rows = append(rows, strings.TrimSpace(widthx.StripTerminalSequences(line)))
	}
	want := []string{"doc3", "doc4", "doc5", "doc6", "pending", "status", "above", "editor", "", "", "below", "footer"}
	for i := range want {
		if len(rows) != len(want) || rows[i] != want[i] {
			t.Fatalf("rows = %q, want %q", rows, want)
		}
	}
	if frame.PrimaryScrollView != viewport.Transcript {
		t.Fatal("the transcript must be the primary scroll view")
	}
}

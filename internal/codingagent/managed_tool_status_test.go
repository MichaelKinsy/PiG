package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// The paired probe compares raw managed-tool rows, including ANSI resets and wrapping.
func TestManagedToolRenderProbe(t *testing.T) {
	original := tui.ActiveTheme().Name
	t.Cleanup(func() { tui.SetTheme(original) })
	type trace struct {
		Theme string   `json:"theme"`
		Width int      `json:"width"`
		Rows  []string `json:"rows"`
	}
	var traces []trace
	for _, theme := range []string{"dark", "light"} {
		tui.SetTheme(theme)
		for _, width := range []int{24, 80, 120} {
			m := &InteractiveMode{chatContainer: tui.NewContainer()}
			m.showManagedToolStatus(tools.ToolStatus{Type: "info", Message: "fd not found. Downloading..."})
			m.showManagedToolStatus(tools.ToolStatus{Type: "warning", Message: "Failed to download fd: fetch failed: connect ETIMEDOUT"})
			traces = append(traces, trace{theme, width, m.chatContainer.Render(width)})
		}
	}
	encoded, err := json.Marshal(traces)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("MANAGED_TOOL_RENDER %s\n", encoded)
}

// Mirrors upstream showManagedToolStatus: one spacer before the first report, warnings prefixed "Warning: ", and a later status line starts fresh instead of coalescing into a report.
func TestShowManagedToolStatus(t *testing.T) {
	m := &InteractiveMode{chatContainer: tui.NewContainer()}
	m.showManagedToolStatus(tools.ToolStatus{Type: "info", Message: "fd not found. Downloading..."})
	m.showManagedToolStatus(tools.ToolStatus{Type: "warning", Message: "Failed to download fd: boom"})
	if got := m.chatContainer.ChildCount(); got != 3 {
		t.Fatalf("child count = %d, want spacer + 2 reports", got)
	}
	_, last := m.chatContainer.LastTwoChildren()
	text, ok := last.(*tui.Text)
	if !ok || !strings.Contains(text.Content, "Warning: Failed to download fd: boom") {
		t.Fatalf("last child = %#v", last)
	}
	m.showStatus("next")
	if got := m.chatContainer.ChildCount(); got != 5 {
		t.Fatalf("status after reports: child count = %d, want 5", got)
	}
}

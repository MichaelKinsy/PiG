package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

func upstreamStatusMode(t *testing.T) *InteractiveMode {
	t.Helper()
	old := tui.ActiveTheme().Name
	tui.SetTheme("dark")
	t.Cleanup(func() { tui.SetTheme(old) })
	return &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 120, 24)}
}

func TestInteractiveStatusCoalescingUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-status.test.ts:80
	t.Run("coalesces immediately-sequential status messages", func(t *testing.T) {
		mode := upstreamStatusMode(t)
		mode.showStatus("STATUS_ONE")
		if mode.chatContainer.ChildCount() != 2 {
			t.Fatal("first status must add spacer and text")
		}
		_, last := mode.chatContainer.LastTwoChildren()
		if !strings.Contains(strings.Join(last.Render(120), "\n"), "STATUS_ONE") {
			t.Fatal("first status missing")
		}
		mode.showStatus("STATUS_TWO")
		if mode.chatContainer.ChildCount() != 2 {
			t.Fatal("sequential status appended instead of coalescing")
		}
		_, last = mode.chatContainer.LastTwoChildren()
		text := strings.Join(last.Render(120), "\n")
		if !strings.Contains(text, "STATUS_TWO") || strings.Contains(text, "STATUS_ONE") {
			t.Fatalf("last status = %q", text)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-status.test.ts:99
	t.Run("appends a new status line if something else was added in between", func(t *testing.T) {
		mode := upstreamStatusMode(t)
		mode.showStatus("STATUS_ONE")
		if mode.chatContainer.ChildCount() != 2 {
			t.Fatal("first status must add spacer and text")
		}
		mode.chatContainer.Add(tui.NewText("OTHER"))
		if mode.chatContainer.ChildCount() != 3 {
			t.Fatal("intervening content missing")
		}
		mode.showStatus("STATUS_TWO")
		if mode.chatContainer.ChildCount() != 5 {
			t.Fatal("new status must append a spacer and text after intervening content")
		}
		_, last := mode.chatContainer.LastTwoChildren()
		if !strings.Contains(strings.Join(last.Render(120), "\n"), "STATUS_TWO") {
			t.Fatal("second status missing")
		}
	})
}

// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-status.test.ts:124
func TestInteractiveManagedToolStatusContiguousGroupUpstream(t *testing.T) {
	mode := upstreamStatusMode(t)
	mode.showManagedToolStatus(tools.ToolStatus{Type: "info", Message: "fd downloading"})
	mode.showManagedToolStatus(tools.ToolStatus{Type: "info", Message: "rg downloading"})
	mode.showManagedToolStatus(tools.ToolStatus{Type: "warning", Message: "rg failed"})
	if mode.chatContainer.ChildCount() != 4 {
		t.Fatal("expected one spacer and three reports")
	}
	rows := mode.chatContainer.Render(220)
	for i, row := range rows {
		rows[i] = strings.TrimRight(stripANSITest(row), " ")
	}
	got := strings.TrimSpace(strings.Join(rows, "\n"))
	if got != "fd downloading\n rg downloading\n Warning: rg failed" {
		t.Fatalf("group=%q", got)
	}
}

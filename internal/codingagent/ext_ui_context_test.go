package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestExtUIContextSetToolsExpandedUpdatesVisibleToolCards(t *testing.T) {
	component := newToolCardForTest("ask_user", `question:"Which option?"`)
	mode := &InteractiveMode{
		toolOrder:     []*tui.ToolExecutionComponent{component},
		chatContainer: tui.NewContainer(component),
	}
	ui := &ExtUIContext{m: mode}

	ui.SetToolsExpanded(true)
	if component.Collapsed {
		t.Fatal("ui.setToolsExpanded(true) left the active ask_user tool card collapsed")
	}
	if !ui.GetToolsExpanded() {
		t.Fatal("ui.getToolsExpanded() did not report the applied expanded state")
	}

	ui.SetToolsExpanded(false)
	if !component.Collapsed {
		t.Fatal("ui.setToolsExpanded(false) left the active ask_user tool card expanded")
	}
	if ui.GetToolsExpanded() {
		t.Fatal("ui.getToolsExpanded() did not report the applied collapsed state")
	}
}

func TestExtUIContextSetToolsExpandedMarshalsToOwnerLoop(t *testing.T) {
	component := newToolCardForTest("ask_user", `question:"Which option?"`)
	mode := &InteractiveMode{
		runCtx:        context.Background(),
		uiTaskCh:      make(chan func(), 1),
		toolOrder:     []*tui.ToolExecutionComponent{component},
		chatContainer: tui.NewContainer(component),
	}
	ui := &ExtUIContext{m: mode}

	ui.SetToolsExpanded(true)
	if !component.Collapsed {
		t.Fatal("ui.setToolsExpanded mutated the tool card before the owner loop applied its task")
	}

	select {
	case apply := <-mode.uiTaskCh:
		apply()
	default:
		t.Fatal("ui.setToolsExpanded did not queue an owner-loop update")
	}
	if component.Collapsed {
		t.Fatal("owner-loop update left the active ask_user tool card collapsed")
	}
}

// Upstream setExtensionFooter replaces the built-in footer with the
// extension's component whatever it renders: a custom footer with no lines
// shows nothing (pi-powerline-footer's secondary row is empty at startup). Only
// clearing the footer brings the built-in one back.
// SetLinesAt can synchronously paint through its invalidation callback. Pi swaps footer ownership before painting the replacement, not after that callback returns.
func TestFooterOwnershipChangesBeforeFrameInvalidation(t *testing.T) {
	mode := &InteractiveMode{statusLine: NewFooterComponent(nil, "", nil)}
	var visible []bool
	mode.extFooter = newSpecialLinesComponent(func() { visible = append(visible, len(mode.statusLine.Render(80)) != 0) })
	ui := &ExtUIContext{m: mode}
	ui.SetFooter(extension.FrameFooter([]string{"custom"}, 0))
	ui.SetFooter(nil)
	if len(visible) != 2 || visible[0] || !visible[1] {
		t.Fatalf("built-in footer visibility at paint = %v, want suppressed then restored", visible)
	}
}

func TestExtUIContextEmptyCustomFooterReplacesTheBuiltInFooter(t *testing.T) {
	mode := &InteractiveMode{
		extFooter:  newSpecialLinesComponent(func() {}),
		statusLine: NewFooterComponent(nil, "", nil),
	}
	ui := &ExtUIContext{m: mode}

	ui.SetFooter(extension.FrameFooter(nil, 80))
	if lines := mode.statusLine.Render(80); len(lines) != 0 {
		t.Fatalf("built-in footer still renders %q under an empty custom footer", lines)
	}

	ui.SetFooter(nil)
	if lines := mode.statusLine.Render(80); len(lines) == 0 {
		t.Fatal("clearing the custom footer did not restore the built-in footer")
	}
}

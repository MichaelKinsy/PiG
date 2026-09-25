package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestExtUIContextSetToolsExpandedUpdatesVisibleToolCards(t *testing.T) {
	component := tui.NewToolExecutionComponent("ask_user", `question:"Which option?"`)
	mode := &InteractiveMode{
		toolOrder: []*tui.ToolExecutionComponent{component},
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
	component := tui.NewToolExecutionComponent("ask_user", `question:"Which option?"`)
	mode := &InteractiveMode{
		runCtx:    context.Background(),
		uiTaskCh:  make(chan func(), 1),
		toolOrder: []*tui.ToolExecutionComponent{component},
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

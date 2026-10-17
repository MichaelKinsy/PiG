package cli

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts:195-206 setWidget sends the widget request only for `undefined` and a string
// array; a component factory is ignored. Go splits the overloads into SetWidget and SetWidgetFactory, so a nil factory is `undefined` (remove
// the widget) and a non-nil factory sends nothing.
func TestRPCUIContextSetWidgetFactoryRemovesOnNilAndIgnoresAFactory(t *testing.T) {
	var sent []rpcUIRequest
	ui := newRPCUIContext(func(value any) { sent = append(sent, value.(rpcUIRequest)) })

	ui.SetWidgetFactory("w", func(tui.TUI, *tui.Theme) extension.DisposableComponent { return nil }, nil)
	if len(sent) != 0 {
		t.Fatalf("a component factory sent %+v, want nothing (rpc-mode.ts ignores factories)", sent)
	}

	ui.SetWidgetFactory("w", nil, &extension.ExtensionWidgetOptions{Placement: extension.WidgetPlacementBelowEditor})
	if len(sent) != 1 {
		t.Fatalf("a nil factory sent %d requests, want 1 setWidget removal", len(sent))
	}
	got := sent[0]
	if got.Method != RPCUIMethodSetWidget || got.WidgetKey != "w" || got.WidgetLines != nil || got.WidgetPlacement != string(extension.WidgetPlacementBelowEditor) {
		t.Fatalf("nil factory request = %+v, want setWidget w without widgetLines, placement belowEditor", got)
	}
}

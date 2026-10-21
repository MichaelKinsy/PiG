package subprocess

// view_kit_bordered_loader.go: the bordered-loader kind, coding-agent's
// BorderedLoader(tui, theme, message, {cancellable}) (D107 §2.2;
// bordered-loader.ts): a loader between two borders with, when cancellable
// (the default), a cancel key hint, which the tui.select.cancel key aborts
// ("abort" view event). It draws with the surface's theme, the one Pi hands
// the factory that builds it.

import (
	"fmt"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

func init() {
	registerViewWidget(ViewKindBorderedLoader, &viewWidget{
		check: func(c *viewChecker, n *ViewNode) {
			if n.ID == "" {
				c.fail("bordered-loader needs an id")
			}
		},
		build:     buildBorderedLoader,
		focusable: true,
	})
}

// viewBordered is the widget state of a bordered loader.
type viewBordered struct {
	bl    *tui.BorderedLoader
	theme string
}

func (v *viewBordered) handleInput(data string) bool {
	if v.bl.CancellableContext() == nil || !tui.GetTUIKeybindings().Matches(data, tui.KBSelectCancel) {
		return false
	}
	v.bl.HandleInput(data)
	return true
}
func (*viewBordered) setFocused(bool) {}
func (v *viewBordered) refresh(node *frontend.ViewNode) {
	if cl := v.bl.CancellableContext(); cl != nil {
		node.Aborted = cl.Aborted()
	}
}

func buildBorderedLoader(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	message := "Loading..."
	if n.Message != nil {
		message = *n.Message
	}
	cancellable := n.Cancellable == nil || *n.Cancellable
	themeKey := fmt.Sprintf("%p|%v", tui.ActiveTheme(), b.f.payload.Theme)
	// The theme is part of the constructor: a theme switch builds a new frame, and a new component.
	inst, fresh := b.widgetInstance(n, widgetCtorKey(n, func(c *ViewNode) { c.Frame = nil }))
	inst.node = out
	if fresh || inst.widget.(*viewBordered).theme != themeKey {
		inst.widget = &viewBordered{bl: tui.NewBorderedLoader(nil, b.theme, message, tui.BorderedLoaderOptions{Cancellable: &cancellable}), theme: themeKey}
		bordered := inst.widget.(*viewBordered)
		bordered.bl.OnAbort = func() { b.queue(inst, ViewEventAbort, nil) }
		fresh = true
	}
	bl := inst.widget.(*viewBordered).bl
	loader := bl.Loader()
	if n.Frame != nil && len(loader.Frames) > 0 && (fresh || inst.sent.Frame == nil || *inst.sent.Frame != *n.Frame) {
		loader.Frame = *n.Frame % len(loader.Frames)
	}
	inst.remember(n)
	// upstream: packages/tui/src/components/loader.ts DEFAULT_INTERVAL_MS
	out.Text, out.Interval = message, 80*time.Millisecond
	out.Frames, out.Frame = loader.Frames, loader.Frame
	b.f.loaders = append(b.f.loaders, &viewLoader{loader: loader, node: out, interval: out.Interval})
	if cl := bl.CancellableContext(); cl != nil {
		out.Aborted = cl.Aborted()
	}
	return bl, nil
}

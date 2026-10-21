package subprocess

// view_kit_cancellable_loader.go: the cancellable-loader kind, pi-tui's
// CancellableLoader(ui, spinnerColorFn, messageColorFn, message, indicator)
// (D107 §2.2; cancellable-loader.ts): a Loader whose focused handleInput
// aborts on the tui.select.cancel key and calls onAbort, which the extension
// receives as an "abort" view event. The host animates it like a loader, and
// keeps the component (with its abort state) across frames while the node's
// id and constructor fields stay the same.

import (
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

func init() {
	registerViewWidget(ViewKindCancellableLoader, &viewWidget{
		check: func(c *viewChecker, n *ViewNode) {
			if n.ID == "" {
				c.fail("cancellable-loader needs an id")
			}
			c.fg(n.SpinnerColor)
			c.fg(n.MessageColor)
		},
		build:     buildCancellableLoader,
		focusable: true,
	})
}

// loaderSetup is a loader node's constructor arguments.
type loaderSetup struct {
	message   string
	spinner   func(string) string
	messageFn func(string) string
	indicator *tui.LoaderIndicatorOptions
	interval  time.Duration
}

// loaderSetupFor reads the constructor arguments of a loader-like node: upstream's
// message default and DEFAULT_INTERVAL_MS (loader.ts) apply to what it omits.
func (b *viewBuilder) loaderSetupFor(n *ViewNode) loaderSetup {
	// upstream: packages/tui/src/components/loader.ts DEFAULT_INTERVAL_MS
	s := loaderSetup{message: "Loading...", spinner: b.fgFn(n.SpinnerColor), messageFn: b.fgFn(n.MessageColor), interval: 80 * time.Millisecond}
	if n.Message != nil {
		s.message = *n.Message
	}
	if ind := n.Indicator; ind != nil {
		var frames []string
		if ind.Frames != nil {
			frames = append([]string{}, (*ind.Frames)...)
		}
		s.indicator = &tui.LoaderIndicatorOptions{Frames: frames}
		if ind.IntervalMs > 0 {
			s.interval = time.Duration(ind.IntervalMs) * time.Millisecond
		}
	}
	return s
}

// describeLoader fills a loader node's frontend fields and registers its animation.
func (b *viewBuilder) describeLoader(n *ViewNode, out *frontend.ViewNode, l *tui.Loader, setup loaderSetup) {
	out.Text, out.Color, out.MessageColor = setup.message, n.SpinnerColor, n.MessageColor
	out.Frames = slices.Clone(l.Frames)
	if len(l.Frames) == 0 {
		out.Frames = nil
	}
	out.Frame = l.Frame
	out.Interval = setup.interval
	b.f.loaders = append(b.f.loaders, &viewLoader{loader: l, node: out, interval: setup.interval})
}

// viewCancellable is the widget state of a cancellable loader.
type viewCancellable struct {
	cl *tui.CancellableLoader
}

func (c *viewCancellable) handleInput(data string) bool {
	if !tui.GetTUIKeybindings().Matches(data, tui.KBSelectCancel) {
		return false
	}
	c.cl.HandleInput(data)
	return true
}
func (*viewCancellable) setFocused(bool) {}
func (c *viewCancellable) refresh(node *frontend.ViewNode) {
	node.Aborted = c.cl.Aborted()
}

func buildCancellableLoader(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	setup := b.loaderSetupFor(n)
	inst, fresh := b.widgetInstance(n, widgetCtorKey(n, func(c *ViewNode) { c.Frame = nil }))
	inst.node = out
	if fresh {
		cl := tui.NewCancellableLoader(nil, setup.spinner, setup.messageFn, setup.message, setup.indicator)
		cl.OnAbort = func() { b.queue(inst, ViewEventAbort, nil) }
		inst.widget = &viewCancellable{cl: cl}
	}
	state := inst.widget.(*viewCancellable)
	cl := state.cl
	if n.Frame != nil && len(cl.Frames) > 0 && (fresh || inst.sent.Frame == nil || *inst.sent.Frame != *n.Frame) {
		cl.Frame = *n.Frame % len(cl.Frames)
	}
	inst.remember(n)
	b.describeLoader(n, out, &cl.Loader, setup)
	out.Aborted = cl.Aborted()
	return cl, nil
}

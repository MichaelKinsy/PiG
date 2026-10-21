package subprocess

// view_kit_scroll_view.go: the scroll-view kind, pi-tui's ScrollView(child,
// {follow, primary, overscroll, scrollbar, scrollbarTrackStyle,
// scrollbarThumbStyle, scrollbarHideDelayMs}) (D107 §2.2; scroll-view.ts). It
// draws its child, padded by a column when the scrollbar is "always", as
// ScrollView.render does. A view surface lays its tree out without a
// viewport height, as Pi's layout engine does for a scroll node given none:
// the viewport is the content's height, and the state a frontend scrolls from
// is the component's own (layoutScroll, layout.go).

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

func init() {
	registerViewWidget(ViewKindScrollView, &viewWidget{
		check: func(c *viewChecker, n *ViewNode) {
			if len(n.Children) != 1 {
				c.fail("scroll-view takes exactly one child")
			}
			if n.Follow != "" && n.Follow != "none" && n.Follow != "end" {
				c.fail("unknown scroll-view follow %q", n.Follow)
			}
			if n.Overscroll != "" && n.Overscroll != "chain" && n.Overscroll != "contain" {
				c.fail("unknown scroll-view overscroll %q", n.Overscroll)
			}
			if n.Scrollbar != "" && n.Scrollbar != "hidden" && n.Scrollbar != "auto" && n.Scrollbar != "always" {
				c.fail("unknown scroll-view scrollbar %q", n.Scrollbar)
			}
			c.fg(n.ScrollbarTrackColor)
			c.fg(n.ScrollbarThumbColor)
			if n.ScrollbarHideDelayMs != nil && *n.ScrollbarHideDelayMs < 0 {
				c.fail("negative scrollbarHideDelayMs")
			}
		},
		build:    buildScrollView,
		children: 1,
	})
}

// viewScrollComponent is the ScrollView the tree holds: after it draws, it
// takes the layout Pi's engine gives a scroll node without a height.
type viewScrollComponent struct {
	*tui.ScrollView
	repaint func()
}

func (c *viewScrollComponent) Render(width int) []string {
	rows := c.ScrollView.Render(width)
	c.UpdateLayout(len(rows), len(rows), c.repaint)
	return rows
}

// viewScroll is the widget state of a scroll view.
type viewScroll struct {
	comp  *viewScrollComponent
	child *viewChildProxy
}

// viewChildProxy is the scroll view's child: the component the current frame built for it, so the scroll view lives
// across frames that rebuild their tree.
type viewChildProxy struct{ cur tui.Component }

func (p *viewChildProxy) Render(width int) []string { return p.cur.Render(width) }
func (p *viewChildProxy) Invalidate()               { p.cur.Invalidate() }
func (p *viewChildProxy) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	return tui.DispatchMouseEvent(p.cur, event)
}

func (*viewScroll) handleInput(string) bool { return false }
func (*viewScroll) setFocused(bool)         {}
func (v *viewScroll) refresh(node *frontend.ViewNode) {
	node.Scroll = &frontend.ViewScroll{Top: v.comp.ScrollTop(), ViewportHeight: v.comp.ViewportHeight(), FollowingEnd: v.comp.IsFollowingEnd(), ScrollbarVisible: v.comp.IsScrollbarVisible()}
}

func buildScrollView(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	children, err := b.buildChildren(n, out)
	if err != nil {
		return nil, err
	}
	inst, fresh := b.widgetInstance(n, widgetCtorKey(n, func(c *ViewNode) { c.ScrollTop, c.Scrollbar = nil, "" }))
	inst.node = out
	if fresh {
		options := tui.ScrollViewOptions{Follow: n.Follow, Primary: n.Primary, Overscroll: n.Overscroll, Scrollbar: tui.ScrollViewScrollbar(n.Scrollbar), ScrollbarHideDelayMs: n.ScrollbarHideDelayMs}
		if style := b.fgFn(n.ScrollbarTrackColor); style != nil {
			options.ScrollbarTrackStyle = style
		}
		if style := b.fgFn(n.ScrollbarThumbColor); style != nil {
			options.ScrollbarThumbStyle = style
		}
		proxy := &viewChildProxy{}
		view := tui.NewScrollView(proxy, options)
		inst.widget = &viewScroll{comp: &viewScrollComponent{ScrollView: view}, child: proxy}
		inst.dispose = view.Dispose
	}
	state := inst.widget.(*viewScroll)
	// The frame's child is new unless it is a kept instance's; the scroll view keeps the state.
	state.child.cur = children[0]
	state.comp.repaint = b.s.requestCardRender
	scrollbar := tui.ScrollViewScrollbar(n.Scrollbar)
	if scrollbar == "" {
		scrollbar = tui.ScrollbarHidden
	}
	if !fresh {
		state.comp.SetScrollbar(scrollbar)
	}
	if n.ScrollTop != nil && (fresh || inst.sent.ScrollTop == nil || *inst.sent.ScrollTop != *n.ScrollTop) {
		state.comp.ScrollTo(*n.ScrollTop)
	}
	inst.remember(n)
	return state.comp, nil
}

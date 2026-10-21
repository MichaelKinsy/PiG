package subprocess

// view_kit_mouse_region.go: the mouse-region kind, pi-tui's MouseRegion(child,
// onMouse) (D107 §2.2; mouse-region.ts). The handler is a closure upstream and
// crosses as a description: onMouse returns the node's result for each event
// whose type it names (every type when it names none) and no result for the
// others. As upstream, the child gets the event first and the region's handler
// only the events it leaves; the host answers the terminal at once with the
// result and reports each event the handler ran for to the extension as a
// "mouse" view event, with the coordinates local to the region.

import (
	"slices"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

func init() {
	registerViewWidget(ViewKindMouseRegion, &viewWidget{
		check: func(c *viewChecker, n *ViewNode) {
			if n.ID == "" {
				c.fail("mouse-region needs an id")
			}
			if len(n.Children) != 1 {
				c.fail("mouse-region takes exactly one child")
			}
			if n.Region == nil {
				c.fail("mouse-region needs a region")
				return
			}
			for _, t := range n.Region.Types {
				if !slices.Contains([]string{"press", "release", "click", "drag", "move", "wheel"}, t) {
					c.fail("unknown mouse event type %q", t)
				}
			}
		},
		build:    buildMouseRegion,
		children: 1,
	})
}

// viewMouseRegion is the widget state of a mouse region: it takes no keys and has no state to report.
type viewMouseRegion struct {
	region    *viewChildProxy
	component *tui.MouseRegion
	cur       *ViewMouseRegion
	builder   *viewBuilder
}

func (*viewMouseRegion) handleInput(string) bool         { return false }
func (*viewMouseRegion) setFocused(bool)                 {}
func (*viewMouseRegion) refresh(node *frontend.ViewNode) {}

func buildMouseRegion(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	children, err := b.buildChildren(n, out)
	if err != nil {
		return nil, err
	}
	inst, fresh := b.widgetInstance(n, widgetCtorKey(n, func(c *ViewNode) { c.Region = nil }))
	inst.node = out
	if fresh {
		state := &viewMouseRegion{}
		proxy := &viewChildProxy{}
		region := tui.NewMouseRegion(proxy, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
			r := state.cur
			if len(r.Types) > 0 && !slices.Contains(r.Types, string(event.Type)) {
				return nil
			}
			b := state.builder
			b.queue(inst, ViewEventMouse, func(ev *ViewEventPayload) { ev.Mouse = remoteMouse(event) })
			result := &tui.TuiMouseEventResult{Handled: r.Handled == nil || *r.Handled, Capture: r.Capture, Focus: r.Focus}
			if r.Render != nil {
				render := *r.Render
				result.Render = &render
			}
			return result
		})
		state.region = proxy
		state.component = region
		inst.widget = state
	}
	state := inst.widget.(*viewMouseRegion)
	state.builder = b
	state.cur = n.Region
	state.region.cur = children[0]
	inst.remember(n)
	return state.component, nil
}

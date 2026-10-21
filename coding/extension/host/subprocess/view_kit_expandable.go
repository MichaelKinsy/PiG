package subprocess

// view_kit_expandable.go: the shared host side of the message kinds Pi draws
// as a Box with a click-to-expand mouse region (branch-summary,
// compaction-summary, skill-invocation) and of custom-message, whose
// default rendering is the same (D107 §2.2). The host keeps the component
// of a node with an id across frames, so a click's toggle lives as long as
// the node and a repeated value never undoes it.

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// expandableComponent is what the four message components share.
type expandableComponent interface {
	tui.Component
	SetExpanded(bool)
	SetOutputPad(int)
	Expanded() bool
}

// viewExpandable is the widget state of an expandable message.
type viewExpandable struct{ comp expandableComponent }

func (*viewExpandable) handleInput(string) bool { return false }
func (*viewExpandable) setFocused(bool)         {}
func (e *viewExpandable) refresh(node *frontend.ViewNode) {
	node.Expanded = e.comp.Expanded()
}

// expandableMessage builds (or keeps) the component of an expandable
// message node. ctor holds the constructor arguments: a change makes a new
// component, as a new Pi component would. make builds the component.
func (b *viewBuilder) expandableMessage(n *ViewNode, out *frontend.ViewNode, ctor any, make func(pad int) expandableComponent) tui.Component {
	inst, fresh := b.widgetInstance(n, ctor)
	inst.node = out
	pad := intOr(n.OutputPad, 1)
	if fresh {
		inst.widget = &viewExpandable{comp: make(pad)}
	}
	comp, sent := inst.widget.(*viewExpandable).comp, inst.sent
	if !fresh {
		if pad != intOr(sent.OutputPad, 1) {
			comp.SetOutputPad(pad)
		}
	}
	if fresh && n.Expanded || !fresh && n.Expanded != sent.Expanded {
		comp.SetExpanded(n.Expanded)
	}
	inst.remember(n)
	out.Expanded = comp.Expanded()
	return comp
}

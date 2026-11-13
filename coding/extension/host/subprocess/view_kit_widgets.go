package subprocess

// view_kit_widgets.go: the registry of the component kit's widget kinds
// (D107, docs/plan/extension-component-kit.md §2.2): pi-tui's ScrollView,
// MouseRegion, Input, Editor and CancellableLoader, and coding-agent's
// BorderedLoader and message components. Each kind registers a validator
// in its own file; its builder enters the table of builders when the
// program enables it (EnableViewWidgets, EnableEditorViews). The registry
// gives the generic code in view_kit.go one place to ask.

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// viewWidget is one registered widget kind.
type viewWidget struct {
	// check validates the node's own fields (the generic checks already ran).
	check func(c *viewChecker, n *ViewNode)
	// children is how many children the kind takes.
	children int
	// focusable kinds can be a view's focus and then take every key.
	focusable bool
}

var viewWidgets = map[string]*viewWidget{}

func registerViewWidget(kind string, w *viewWidget) { viewWidgets[kind] = w }

func viewWidgetFocusable(kind string) bool {
	w := viewWidgets[kind]
	return w != nil && w.focusable
}

func viewWidgetChildLimit(kind string) int {
	if w := viewWidgets[kind]; w != nil {
		return w.children
	}
	return 0
}

// viewWidgetState is a widget's host state beyond its component: it takes
// the keys of a focused node and reports its live state to a frontend.
type viewWidgetState interface {
	// handleInput gives the focused widget a key and reports whether it took
	// it; a focused widget takes every key, as Pi's focused component does.
	handleInput(data string) bool
	// refresh copies the widget's live state into its frontend node.
	refresh(node *frontend.ViewNode)
	// setFocused tells the widget whether the view's focus names it.
	setFocused(focused bool)
}

func (b *viewBuilder) buildChildren(n *ViewNode, out *frontend.ViewNode) ([]tui.Component, error) {
	out.Children = make([]frontend.ViewNode, len(n.Children))
	comps := make([]tui.Component, len(n.Children))
	for i := range n.Children {
		c, err := b.build(&n.Children[i], &out.Children[i])
		if err != nil {
			return nil, err
		}
		comps[i] = c
	}
	return comps, nil
}

// widgetBuildFunc makes a node's component and fills its frontend node; it builds the node's children itself, with
// buildChildren.
type widgetBuildFunc func(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error)

// viewWidgetBuilders holds the builders of the kinds this program enabled, by kind. A registered kind reaches its builder
// only through this table, so a program that enables none links no builder and none of the components behind them: the
// linker keeps a function only when something refers to it.
var viewWidgetBuilders atomic.Pointer[map[string]widgetBuildFunc]

// enableViewWidgetBuilders adds builders to the table. A table is never written once it is stored, so a build that loaded
// one reads it while another goroutine enables more.
func enableViewWidgetBuilders(builders map[string]widgetBuildFunc) {
	for {
		enabled := viewWidgetBuilders.Load()
		next := maps.Clone(builders)
		if enabled != nil {
			maps.Copy(next, *enabled)
			if len(next) == len(*enabled) {
				return
			}
		}
		if viewWidgetBuilders.CompareAndSwap(enabled, &next) {
			return
		}
	}
}

// EnableViewWidgets lets the view kit build every widget kind but `editor`, which EnableEditorViews adds: `grid`, `input`,
// `scroll-view`, `mouse-region`, the two loaders and the four message components. The `pig` command calls it before it
// runs a command, so every mode builds them. An embedder that builds no extension view leaves it out and does not link
// the builders; one that mounts widgets, for a terminal or for a frontend of its own, calls it once before the first view
// is built. Until then a view that holds one of them fails with a message that names the call; validation needs no
// builder, so a malformed node is rejected the same way. It is safe to call more than once, in either order with
// EnableEditorViews, and from any goroutine.
func EnableViewWidgets() {
	enableViewWidgetBuilders(map[string]widgetBuildFunc{
		ViewKindBorderedLoader:    buildBorderedLoader,
		ViewKindBranchSummary:     buildBranchSummary,
		ViewKindCancellableLoader: buildCancellableLoader,
		ViewKindCompactionSummary: buildCompactionSummary,
		ViewKindCustomMessage:     buildCustomMessage,
		ViewKindGrid:              buildGrid,
		ViewKindInput:             buildInput,
		ViewKindMouseRegion:       buildMouseRegion,
		ViewKindScrollView:        buildScrollView,
		ViewKindSkillInvocation:   buildSkillInvocation,
	})
}

// buildWidget builds a registered kind with the builder the program enabled. It reports an unknown kind as unknown and a
// kind whose builder is not enabled with a message that names the call.
func (b *viewBuilder) buildWidget(n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	if viewWidgets[n.Kind] == nil {
		return nil, fmt.Errorf("unknown view kind %q", n.Kind)
	}
	// pig additive (D107): a kind's builder is linked only into a program that enabled it; Pi links every component.
	if enabled := viewWidgetBuilders.Load(); enabled != nil {
		if build := (*enabled)[n.Kind]; build != nil {
			return build(b, n, out)
		}
	}
	call, article := "EnableViewWidgets", "a"
	if n.Kind == ViewKindEditor {
		call = "EnableEditorViews"
	}
	if strings.ContainsRune("aeiou", rune(n.Kind[0])) {
		article = "an"
	}
	return nil, fmt.Errorf("%s views are not enabled in this program: call subprocess.%s before building a view that holds %s %s", n.Kind, call, article, n.Kind)
}

// checkWidget validates a registered kind, or fails for an unknown one.
func (c *viewChecker) checkWidget(n *ViewNode) {
	w := viewWidgets[n.Kind]
	if w == nil {
		c.fail("unknown view kind %q", n.Kind)
		return
	}
	if len(n.Children) != w.children {
		c.fail("%s takes %d children, not %d", n.Kind, w.children, len(n.Children))
		return
	}
	if w.check != nil {
		w.check(c, n)
	}
}

// widgetInstance returns the kept instance of an id'd widget node whose
// constructor key is ctor, or a fresh one; a node without an id is fresh
// every frame, as `new …` is in Pi.
func (b *viewBuilder) widgetInstance(n *ViewNode, ctor any) (inst *viewInstance, fresh bool) {
	key, _ := json.Marshal(ctor)
	return b.conversationInstance(n, key, nil)
}

// widgetCtorKey is the node without the setter state: the fields that make a
// new component when they change.
func widgetCtorKey(n *ViewNode, drop ...func(*ViewNode)) any {
	ctor := *n
	ctor.Stack, ctor.Children = nil, nil
	for _, d := range drop {
		d(&ctor)
	}
	return ctor
}

// handleWidgetInput gives data to the focused widget inst with s.mu held, and
// releases it, as HandleViewInput does for a list.
func (s *viewSurface) handleWidgetInput(inst *viewInstance, data string) bool {
	took := inst.widget.handleInput(data)
	events := inst.pending
	inst.pending = nil
	if took {
		s.rev++
	}
	send, repaint := s.sendEvent, s.repaint
	s.mu.Unlock()
	if send != nil {
		for _, ev := range events {
			send(ev)
		}
	}
	if took && repaint != nil {
		repaint()
	}
	return took
}

// queue records an event of the widget node inst for the extension.
func (b *viewBuilder) queue(inst *viewInstance, typ string, fill func(*ViewEventPayload)) {
	ev := ViewEventPayload{Key: b.s.key, Node: inst.id, Type: typ}
	if fill != nil {
		fill(&ev)
	}
	inst.pending = append(inst.pending, ev)
}

func remoteMouse(ev tui.TuiMouseEvent) *extension.RemoteMouseEvent {
	return &extension.RemoteMouseEvent{
		Type: string(ev.Type), Button: string(ev.Button), X: ev.X, Y: ev.Y, ScreenX: ev.ScreenX, ScreenY: ev.ScreenY,
		Width: ev.Width, Height: ev.Height, Shift: ev.Shift, Alt: ev.Alt, Ctrl: ev.Ctrl, WheelDelta: ev.WheelDelta, ClickCount: ev.ClickCount,
	}
}

// viewLoaderAnimation is whether the host advances the frames of the loaders
// it draws (§4.4); it is on unless SetViewLoaderAnimation turns it off.
var viewLoaderAnimation atomic.Bool

func init() { viewLoaderAnimation.Store(true) }

// SetViewLoaderAnimation turns the host-owned loader animation on or off for
// the views drawn after the call. A test freezes it to compare a loader's
// frames with Pi's byte for byte; the frames a view names stay as it named
// them.
func SetViewLoaderAnimation(enabled bool) { viewLoaderAnimation.Store(enabled) }

package subprocess

// view_kit_input.go: the input kind, pi-tui's Input({prompt, placeholder,
// placeholderStyle}) (D107 §2.2; input.ts). The host owns the value and the
// cursor and runs Input's own key and mouse handling: the focused node takes
// every key, as Pi's focused component does. The extension receives Input's
// callbacks, "submit" (onSubmit) and "escape" (onEscape), and a "change" with
// the value and cursor after each key or press that changed either, since
// Pi's author reads them with getValue.

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

func init() {
	registerViewWidget(ViewKindInput, &viewWidget{
		check: func(c *viewChecker, n *ViewNode) {
			if n.ID == "" {
				c.fail("input needs an id")
			}
			c.fg(n.PlaceholderColor)
			if cur := n.Cursor; cur != nil && (cur.Line != 0 || cur.Col < 0) {
				c.fail("input cursor must be {line: 0, col >= 0}")
			}
		},
		build:     buildInput,
		focusable: true,
	})
}

// viewInputComponent is the Input the tree holds; it reports what a mouse press changed.
type viewInputComponent struct {
	*tui.TextInput
	changed func()
}

func (c *viewInputComponent) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	result := c.TextInput.HandleMouse(event)
	c.changed()
	return result
}

// viewInput is the widget state of an input.
type viewInput struct {
	comp   *viewInputComponent
	value  string
	cursor int
}

// report queues a "change" when the value or the cursor moved since the last report.
func (v *viewInput) report(b *viewBuilder, inst *viewInstance) {
	value, cursor := v.comp.GetValue(), v.comp.Cursor()
	if value == v.value && cursor == v.cursor {
		return
	}
	v.value, v.cursor = value, cursor
	b.queue(inst, ViewEventChange, func(ev *ViewEventPayload) { ev.Value, ev.Cursor = value, &ViewCursor{Col: cursor} })
}

// inputHandler is the widget state's key path, with the builder it reports through.
type viewInputState struct {
	*viewInput
	b    *viewBuilder
	inst *viewInstance
}

func (v *viewInputState) handleInput(data string) bool {
	v.comp.HandleInput(data)
	v.report(v.b, v.inst)
	return true
}
func (v *viewInputState) setFocused(focused bool) { v.comp.SetFocused(focused) }
func (v *viewInputState) refresh(node *frontend.ViewNode) {
	node.Value, node.Cursor = v.comp.GetValue(), frontend.ViewCursor{Col: v.comp.Cursor()}
}

func buildInput(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	inst, fresh := b.widgetInstance(n, widgetCtorKey(n, func(c *ViewNode) { c.Value, c.Cursor, c.Focused = "", nil, false }))
	inst.node = out
	if fresh {
		options := tui.InputOptions{Prompt: n.Prompt, Placeholder: n.Placeholder}
		if style := b.fgFn(n.PlaceholderColor); style != nil {
			options.PlaceholderStyle = style
		}
		state := &viewInputState{viewInput: &viewInput{}, b: b, inst: inst}
		in := tui.NewInput(options)
		state.comp = &viewInputComponent{TextInput: in, changed: func() { state.report(b, inst) }}
		in.OnSubmit = func(value string) {
			b.queue(inst, ViewEventSubmit, func(ev *ViewEventPayload) { ev.Value, ev.Cursor = value, &ViewCursor{Col: in.Cursor()} })
		}
		in.OnEscape = func() {
			b.queue(inst, ViewEventEscape, func(ev *ViewEventPayload) { ev.Value, ev.Cursor = in.GetValue(), &ViewCursor{Col: in.Cursor()} })
		}
		inst.widget = state
	}
	state := inst.widget.(*viewInputState)
	// The state reports through the builder of the frame that made it; a later frame's callbacks queue on the same instance.
	state.b = b
	if n.Value != "" && fresh || !fresh && n.Value != inst.sent.Value {
		state.comp.SetValue(n.Value)
	}
	// A sender that owns the cursor (Node's) names it with every frame; the host applies it when it moved.
	if n.Cursor != nil && (fresh || inst.sent.Cursor == nil || *inst.sent.Cursor != *n.Cursor) {
		state.comp.SetCursor(n.Cursor.Col)
	}
	if fresh {
		state.value, state.cursor = state.comp.GetValue(), state.comp.Cursor()
	}
	inst.remember(n)
	prompt := "> "
	if n.Prompt != nil {
		prompt = *n.Prompt
	}
	out.Value, out.Cursor, out.Prompt, out.Placeholder = state.comp.GetValue(), frontend.ViewCursor{Col: state.comp.Cursor()}, prompt, n.Placeholder
	return state.comp, nil
}

package testfixture

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// tuiAltTerminal is the recording terminal of tui-alt-screen-probe: 20 columns by 6 rows, no Kitty protocol.
type tuiAltTerminal struct {
	log     []string
	started bool
	onInput func(string)
}

func (t *tuiAltTerminal) Start(onInput func(string), _ func()) {
	t.onInput, t.started = onInput, true
	t.log = append(t.log, "start")
}
func (t *tuiAltTerminal) Stop()                              { t.started = false; t.log = append(t.log, "stop") }
func (t *tuiAltTerminal) DrainInput(int, int)                {}
func (t *tuiAltTerminal) Write(data string)                  { t.log = append(t.log, data) }
func (t *tuiAltTerminal) Columns() int                       { return 20 }
func (t *tuiAltTerminal) Rows() int                          { return 6 }
func (t *tuiAltTerminal) KittyProtocolActive() bool          { return false }
func (t *tuiAltTerminal) MoveBy(int)                         {}
func (t *tuiAltTerminal) HideCursor()                        {}
func (t *tuiAltTerminal) ShowCursor()                        {}
func (t *tuiAltTerminal) ClearLine()                         {}
func (t *tuiAltTerminal) ClearFromCursor()                   {}
func (t *tuiAltTerminal) ClearScreen()                       {}
func (t *tuiAltTerminal) SetTitle(string)                    {}
func (t *tuiAltTerminal) SetProgress(bool)                   {}
func (t *tuiAltTerminal) SetProgramStatus(sdk.ProgramStatus) {}

type tuiAltLines struct{ render func(width int) []string }

func (c *tuiAltLines) Render(width int) []string { return c.render(width) }
func (c *tuiAltLines) Invalidate()               {}

type tuiAltFocusable struct {
	tuiAltLines
	focused bool
	inputs  []string
}

func (c *tuiAltFocusable) SetFocused(focused bool) { c.focused = focused }
func (c *tuiAltFocusable) HandleInput(data string) { c.inputs = append(c.inputs, data) }

var tuiAltANSI = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]|\x1b\\]8;;[^\x07\x1b]*(?:\x07|\x1b\\\\)?")

// tuiAltProbe drives a TuiAltScreen the way the Node fixture drives Pi's own class and reports the same text.
func tuiAltProbe(ctx sdk.Context, _ string) error {
	var failure error
	must := func(err error) {
		if err != nil && failure == nil {
			failure = err
		}
	}
	val := func(v any, err error) any { must(err); return v }
	term := &tuiAltTerminal{}
	hardware := false
	mouse, copyOnSelect := false, true
	screen, err := sdk.NewTuiAltScreen(ctx, term, &hardware, "", sdk.TuiAltScreenOptions{Mouse: &mouse, WheelScrollLines: 3, CopyOnSelect: &copyOnSelect})
	if err != nil {
		return err
	}
	vis := func(s string) string { return strings.TrimRight(tuiAltANSI.ReplaceAllString(s, ""), " \t\r\n") }
	sl := func() []string {
		lines, err := screen.GetScreenLines()
		must(err)
		out := make([]string, len(lines))
		for i, l := range lines {
			out[i] = vis(l)
		}
		return out
	}
	doc := &tuiAltLines{render: func(w int) []string {
		lines := make([]string, 12)
		for i := range lines {
			lines[i] = fmt.Sprintf("row %d w=%d", i+1, w)
		}
		return lines
	}}
	must(screen.AddChild(doc))
	var f []string
	add := func(format string, args ...any) { f = append(f, fmt.Sprintf(format, args...)) }
	add("mode=%s", screen.Mode())
	add("children=%d", len(screen.Children()))
	add("terminal=%t", screen.Terminal() == sdk.Terminal(term))
	must(screen.Start())
	must(screen.RenderNow(false))
	vp := func() string {
		top := val(screen.ViewportTop()).(int)
		following := val(screen.IsFollowingOutput()).(bool)
		return fmt.Sprintf("%d/%t", top, following)
	}
	add("viewport=%s", vp())
	add("first=%s", sl()[0])
	add("last=%s", sl()[5])
	add("redraws=%t", val(screen.FullRedraws()).(int) > 0)
	entered := false
	for _, l := range term.log {
		entered = entered || strings.Contains(l, "\x1b[?1049h")
	}
	add("entered=%t", entered)
	must(screen.ScrollBy(-2))
	must(screen.RenderNow(false))
	add("up=%s", vp())
	must(screen.ScrollToTop())
	must(screen.RenderNow(false))
	add("top=%s", vp())
	add("topfirst=%s", sl()[0])
	must(screen.ScrollToBottom())
	must(screen.RenderNow(false))
	add("bottom=%s", vp())
	must(screen.Flash("hello", 500))
	must(screen.RenderNow(false))
	flash := false
	for _, l := range sl() {
		flash = flash || strings.Contains(l, "hello")
	}
	add("flash=%t", flash)
	ov := &tuiAltLines{render: func(int) []string { return []string{"OVER"} }}
	h, err := screen.ShowOverlay(ov, &sdk.TuiOverlayOptions{OverlayOptions: sdk.OverlayOptions{Width: sdk.OverlayCells(8), Anchor: "center"}})
	must(err)
	must(screen.RenderNow(false))
	bounds, err := h.GetBounds()
	must(err)
	width := "undefined"
	if bounds != nil {
		width = fmt.Sprint(bounds.Width)
	}
	add("overlay=%t/%t/%t/%s", val(screen.HasOverlay()), val(screen.HasOverlayEntries()), val(h.IsFocused()), width)
	must(h.SetHidden(true))
	add("hidden=%t", val(h.IsHidden()))
	must(h.SetHidden(false))
	must(screen.HideOverlay())
	add("afterHide=%t/%t", val(screen.HasOverlay()), val(screen.HasOverlayEntries()))
	foc := &tuiAltFocusable{tuiAltLines: tuiAltLines{render: func(int) []string { return []string{"foc"} }}}
	must(screen.SetFocus(foc))
	focused, err := screen.GetFocusedComponent()
	must(err)
	add("focus=%t/%t", focused == sdk.Component(foc), foc.focused)
	term.onInput("k")
	listener := func(data string) *sdk.TuiInputResult {
		if data == "c" {
			return &sdk.TuiInputResult{Consume: true}
		}
		return nil
	}
	remove, err := screen.AddInputListener(listener)
	must(err)
	term.onInput("c")
	remove()
	term.onInput("c")
	_, err = screen.AddInputListener(listener)
	must(err)
	must(screen.RemoveInputListener(listener))
	add("inputs=%s", strings.Join(foc.inputs, ","))
	debug := 0
	must(screen.SetOnDebug(func() { debug++ }))
	term.onInput("\x1b[100;6u")
	add("debug=%d", debug)
	must(screen.SetShowHardwareCursor(true))
	must(screen.SetClearOnShrink(true))
	add("flags=%t/%t", val(screen.GetShowHardwareCursor()), val(screen.GetClearOnShrink()))
	must(screen.SetWheelScrollLines(2, false))
	must(screen.SetCopyOnSelect(false))
	add("copy=%t/%t/%t", val(screen.GetCopyOnSelect()), val(screen.HasActiveSelection()), val(screen.CopyActiveSelectionToClipboard()))
	must(screen.ResetTextSelection())
	var schemes []string
	screen.OnTerminalColorSchemeChange(func(scheme string) { schemes = append(schemes, scheme) })
	must(screen.SetTerminalColorSchemeNotifications(true))
	term.onInput("\x1b[?997;1n")
	term.onInput("\x1b[?997;2n")
	add("schemes=%s", strings.Join(schemes, ","))
	colors, err := screen.QueryTerminalColors(20, nil)
	must(err)
	encoded, _ := json.Marshal(colors)
	add("colors=%s", encoded)
	result, err := screen.HandleMouse(sdk.MouseEvent{Type: "wheel", Button: "none", Width: 20, Height: 6, WheelDelta: 1})
	must(err)
	encoded, _ = json.Marshal(result)
	add("mouse=%s", encoded)
	rendered, err := screen.Render(10)
	must(err)
	add("render=%s", strings.Join(rendered[:2], "|"))
	must(screen.Invalidate())
	must(screen.RequestRender(false))
	root := &tuiAltLines{render: func(int) []string { return []string{"ROOT"} }}
	must(screen.SetLayoutRoot(root))
	must(screen.RenderNow(false))
	add("root=%t", strings.HasPrefix(sl()[0], "ROOT"))
	must(screen.SetLayoutRoot(nil))
	must(screen.RemoveChild(doc))
	add("removed=%d", len(screen.Children()))
	must(screen.AddChild(doc))
	must(screen.Clear())
	add("cleared=%d", len(screen.Children()))
	must(screen.Stop(false))
	add("stopped=%t", !term.started)
	add("viewportTui=%t", screen.ViewportTui())
	if failure != nil {
		return failure
	}
	ctx.Notify("tui-alt: "+strings.Join(f[:len(f)-1], "; "), "info")
	return nil
}

// tuiAltFrameProbe reads the screen from a component's render, shows overlays with visible callbacks and invalidates, as the Node fixture does.
func tuiAltFrameProbe(ctx sdk.Context, _ string) error {
	var failure error
	must := func(err error) {
		if err != nil && failure == nil {
			failure = err
		}
	}
	val := func(v any, err error) any { must(err); return v }
	term := &tuiAltTerminal{}
	hardware, mouse := false, false
	screen, err := sdk.NewTuiAltScreen(ctx, term, &hardware, "", sdk.TuiAltScreenOptions{Mouse: &mouse})
	if err != nil {
		return err
	}
	var seen []string
	docInv, ovInv := 0, 0
	doc := &tuiAltCounting{invalidated: &docInv, render: func(int) []string {
		seen = append(seen, fmt.Sprintf("%d/%t/%d/%t/%t/%t", val(screen.ViewportTop()), val(screen.IsFollowingOutput()), len(val(screen.GetScreenLines()).([]string)),
			val(screen.HasActiveSelection()), val(screen.HasOverlay()), val(screen.GetShowHardwareCursor())))
		lines := make([]string, 12)
		for i := range lines {
			lines[i] = fmt.Sprintf("row %d", i+1)
		}
		return lines
	}}
	must(screen.AddChild(doc))
	must(screen.Start())
	must(screen.RenderNow(false))
	must(screen.RenderNow(false))
	f := []string{"frame1=" + seen[0], "frame2=" + seen[len(seen)-1]}
	var widths []string
	ov := &tuiAltCounting{invalidated: &ovInv, render: func(int) []string { return []string{"OVER"} }}
	hOn, err := screen.ShowOverlay(ov, &sdk.TuiOverlayOptions{OverlayOptions: sdk.OverlayOptions{Width: sdk.OverlayCells(8)}, Visible: func(w, h int) bool {
		widths = append(widths, fmt.Sprintf("%dx%d", w, h))
		return w >= 20
	}})
	must(err)
	must(screen.RenderNow(false))
	has20 := false
	for _, w := range widths {
		has20 = has20 || w == "20x6"
	}
	f = append(f, fmt.Sprintf("visibleOn=%t/%t/%t", val(screen.HasOverlay()), val(screen.HasOverlayEntries()), has20))
	must(hOn.Hide())
	off := &tuiAltCounting{invalidated: new(int), render: func(int) []string { return []string{"X"} }}
	hOff, err := screen.ShowOverlay(off, &sdk.TuiOverlayOptions{Visible: func(int, int) bool { return false }})
	must(err)
	f = append(f, fmt.Sprintf("visibleOff=%t/%t", val(screen.HasOverlay()), val(screen.HasOverlayEntries())))
	must(hOff.Hide())
	ov2, err := screen.ShowOverlay(ov, &sdk.TuiOverlayOptions{OverlayOptions: sdk.OverlayOptions{Width: sdk.OverlayCells(8)}})
	must(err)
	docInv, ovInv = 0, 0
	must(screen.Invalidate())
	f = append(f, fmt.Sprintf("invalidate=%t/%t", docInv > 0, ovInv > 0))
	must(ov2.Hide())
	var mutLog []string
	armed, mutInv := false, 0
	extra := &tuiAltCounting{invalidated: new(int), render: func(int) []string { return []string{"EXTRA"} }}
	target := &tuiAltCounting{invalidated: new(int), render: func(int) []string { return []string{"TARGET"} }}
	popup := &tuiAltCounting{invalidated: new(int), render: func(int) []string { return []string{"POP"} }}
	mut := &tuiAltCounting{invalidated: &mutInv}
	mut.render = func(int) []string {
		if !armed {
			return []string{"MUT"}
		}
		armed = false
		flags := func() string {
			return fmt.Sprintf("%t/%t/%t", val(screen.GetShowHardwareCursor()), val(screen.GetClearOnShrink()), val(screen.GetCopyOnSelect()))
		}
		r := []string{"flags=" + flags()}
		must(screen.SetShowHardwareCursor(true))
		must(screen.SetClearOnShrink(true))
		must(screen.SetCopyOnSelect(false))
		r = append(r, "set="+flags())
		must(screen.AddChild(extra))
		r = append(r, fmt.Sprintf("add=%d", len(screen.Children())))
		must(screen.RemoveChild(extra))
		r = append(r, fmt.Sprintf("remove=%d", len(screen.Children())))
		must(screen.SetFocus(target))
		r = append(r, fmt.Sprintf("focus=%t", val(screen.GetFocusedComponent()) == sdk.Component(target)))
		var first string
		h, err := screen.ShowOverlay(popup, &sdk.TuiOverlayOptions{OverlayOptions: sdk.OverlayOptions{Width: sdk.OverlayCells(8)}, Visible: func(int, int) bool {
			if first == "" {
				first = fmt.Sprintf("%t", val(screen.GetShowHardwareCursor()))
			}
			return true
		}})
		must(err)
		r = append(r, fmt.Sprintf("overlay=%t/%t/%t/%s", val(screen.HasOverlay()), val(screen.HasOverlayEntries()), val(h.IsFocused()), first))
		must(h.Hide())
		r = append(r, fmt.Sprintf("hidden=%t", val(screen.HasOverlayEntries())))
		must(screen.ScrollBy(-2))
		r = append(r, fmt.Sprintf("scroll=%d/%t", val(screen.ViewportTop()), val(screen.IsFollowingOutput())))
		mutInv = 0
		must(screen.Invalidate())
		r = append(r, fmt.Sprintf("invalidate=%t", mutInv > 0))
		r = append(r, fmt.Sprintf("lines=%d", len(val(screen.GetScreenLines()).([]string))))
		mutLog = append(mutLog, strings.Join(r, ","))
		return []string{"MUT"}
	}
	must(screen.AddChild(mut))
	must(screen.RenderNow(false))
	armed = true
	must(screen.RenderNow(false))
	f = append(f, "mutate="+strings.Join(mutLog, "|"),
		fmt.Sprintf("after=%t/%t/%t/%d", val(screen.GetShowHardwareCursor()), val(screen.GetClearOnShrink()), val(screen.GetCopyOnSelect()), len(screen.Children())))
	must(screen.Stop(false))
	if failure != nil {
		return failure
	}
	ctx.Notify("tui-alt-frame: "+strings.Join(f, "; "), "info")
	return nil
}

// tuiAltChildrenProbe reads children after each change, with three distinct components of equal content.
func tuiAltChildrenProbe(ctx sdk.Context, _ string) error {
	var failure error
	must := func(err error) {
		if err != nil && failure == nil {
			failure = err
		}
	}
	term := &tuiAltTerminal{}
	hardware, mouse := false, false
	screen, err := sdk.NewTuiAltScreen(ctx, term, &hardware, "", sdk.TuiAltScreenOptions{Mouse: &mouse})
	if err != nil {
		return err
	}
	a, b, c := &tuiAltSame{}, &tuiAltSame{}, &tuiAltSame{}
	name := func(k sdk.Component) string {
		switch k {
		case sdk.Component(a):
			return "a"
		case sdk.Component(b):
			return "b"
		case sdk.Component(c):
			return "c"
		}
		return "?"
	}
	order := func() string {
		var s string
		for _, k := range screen.Children() {
			s += name(k)
		}
		if s == "" {
			return "-"
		}
		return s
	}
	f := []string{"empty=" + order()}
	must(screen.AddChild(a))
	must(screen.AddChild(b))
	must(screen.AddChild(c))
	f = append(f, "added="+order())
	must(screen.RemoveChild(b))
	f = append(f, "removeB="+order())
	must(screen.RemoveChild(b))
	f = append(f, "removeAbsent="+order())
	must(screen.AddChild(a))
	f = append(f, "duplicate="+order())
	must(screen.RemoveChild(a))
	f = append(f, "removeFirst="+order())
	must(screen.AddChild(b))
	f = append(f, "readd="+order())
	lines, err := screen.Render(10)
	must(err)
	f = append(f, "render="+strings.Join(lines, "|"))
	must(screen.SetFocus(b))
	focused, err := screen.GetFocusedComponent()
	must(err)
	f = append(f, "focus="+name(focused))
	must(screen.Clear())
	f = append(f, "cleared="+order())
	if failure != nil {
		return failure
	}
	ctx.Notify("tui-alt-children: "+strings.Join(f, "; "), "info")
	return nil
}

// tuiAltSame is a component whose every instance has equal content.
type tuiAltSame struct{ _ byte }

func (c *tuiAltSame) Render(int) []string { return []string{"same"} }
func (c *tuiAltSame) Invalidate()         {}

// tuiAltReentryProbe calls the frame's own members from a component's render and reports that they fail.
func tuiAltReentryProbe(ctx sdk.Context, _ string) error {
	var failure error
	must := func(err error) {
		if err != nil && failure == nil {
			failure = err
		}
	}
	term := &tuiAltTerminal{}
	hardware, mouse := false, false
	screen, err := sdk.NewTuiAltScreen(ctx, term, &hardware, "", sdk.TuiAltScreenOptions{Mouse: &mouse})
	if err != nil {
		return err
	}
	armed := false
	var facts []string
	fact := func(name string, err error) {
		switch {
		case err == nil:
			facts = append(facts, name+"=ok")
		case strings.Contains(err.Error(), "cannot run while a component renders"):
			facts = append(facts, name+"=err")
		default:
			facts = append(facts, name+"=other")
		}
	}
	doc := &tuiAltCounting{invalidated: new(int)}
	doc.render = func(int) []string {
		if armed {
			armed = false
			_, err := screen.Render(10)
			fact("render", err)
			fact("renderNow", screen.RenderNow(false))
			fact("start", screen.Start())
			fact("stop", screen.Stop(false))
			_, err = screen.CopyActiveSelectionToClipboard()
			fact("copy", err)
			_, err = screen.QueryTerminalColors(20, nil)
			fact("colors", err)
			must(screen.SetShowHardwareCursor(true))
			on, err := screen.GetShowHardwareCursor()
			must(err)
			facts = append(facts, fmt.Sprintf("after=%t", on))
		}
		return []string{"one", "two", "three"}
	}
	must(screen.AddChild(doc))
	must(screen.Start())
	must(screen.RenderNow(false))
	armed = true
	must(screen.RenderNow(false))
	must(screen.Stop(false))
	if failure != nil {
		return failure
	}
	ctx.Notify("tui-alt-reentry: "+strings.Join(facts, "; "), "info")
	return nil
}

// tuiAltValueLine has value receivers: the SDK must reject it.
type tuiAltValueLine struct{ text string }

func (c tuiAltValueLine) Render(int) []string { return []string{c.text} }
func (c tuiAltValueLine) Invalidate()         {}

// tuiAltPtrLine has pointer receivers.
type tuiAltPtrLine struct{ text string }

func (c *tuiAltPtrLine) Render(int) []string { return []string{c.text} }
func (c *tuiAltPtrLine) Invalidate()         {}

// tuiAltGoIdentityProbe passes component values and equal-content pointers to a TuiAltScreen.
func tuiAltGoIdentityProbe(ctx sdk.Context, _ string) error {
	var failure error
	must := func(err error) {
		if err != nil && failure == nil {
			failure = err
		}
	}
	term := &tuiAltTerminal{}
	hardware, mouse := false, false
	screen, err := sdk.NewTuiAltScreen(ctx, term, &hardware, "", sdk.TuiAltScreenOptions{Mouse: &mouse})
	if err != nil {
		return err
	}
	verdict := func(err error) string {
		if err != nil && strings.Contains(err.Error(), "pointer") {
			return "rejected"
		}
		return "accepted"
	}
	value := tuiAltValueLine{"same"}
	f := []string{"add=" + verdict(screen.AddChild(value))}
	f = append(f, "remove="+verdict(screen.RemoveChild(value)))
	f = append(f, "focus="+verdict(screen.SetFocus(value)))
	f = append(f, "root="+verdict(screen.SetLayoutRoot(value)))
	_, err = screen.ShowOverlay(value, nil)
	f = append(f, "overlay="+verdict(err))
	handle, err := screen.ShowOverlay(&tuiAltPtrLine{"ov"}, nil)
	must(err)
	f = append(f, "unfocus="+verdict(handle.Unfocus(sdk.UnfocusOptions{Target: value})))
	f = append(f, fmt.Sprintf("children=%d", len(screen.Children())))
	a, b := &tuiAltPtrLine{"same"}, &tuiAltPtrLine{"same"}
	must(screen.AddChild(a))
	must(screen.AddChild(b))
	must(screen.RemoveChild(b))
	kids := screen.Children()
	which := "?"
	if len(kids) == 1 && kids[0] == sdk.Component(a) {
		which = "a"
	}
	f = append(f, fmt.Sprintf("distinct=%s/%d", which, len(kids)))
	if failure != nil {
		return failure
	}
	ctx.Notify("tui-alt-go-identity: "+strings.Join(f, "; "), "info")
	return nil
}

// tuiAltCounting counts the invalidations it receives.
type tuiAltCounting struct {
	invalidated *int
	render      func(width int) []string
}

func (c *tuiAltCounting) Render(width int) []string { return c.render(width) }
func (c *tuiAltCounting) Invalidate()               { *c.invalidated++ }

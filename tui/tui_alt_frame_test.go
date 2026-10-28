package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// framePart is a component whose Render is a function, so a test can call the screen from inside it as a subprocess extension's component does.
type framePart struct{ render func(width int) []string }

// newFrameScreenForTest is a screen whose renderer records the goroutine that runs a frame, as the extension host asks.
func newFrameScreenForTest(out *bytes.Buffer, width, height int) *TuiAltScreen {
	screen := newAltScreenForTest(out, width, height, TuiAltScreenOptions{})
	screen.EnableFrameAccess()
	return screen
}
func (p *framePart) Render(width int) []string { return p.render(width) }
func (p *framePart) Invalidate()               {}

// screenRows returns the first n rows of the last frame with their escape sequences and padding removed.
func screenRows(screen *TuiAltScreen, n int) []string {
	lines := screen.GetScreenLines()
	rows := make([]string, 0, n)
	for _, line := range lines[:min(n, len(lines))] {
		rows = append(rows, strings.TrimSpace(stripANSI(line)))
	}
	return rows
}

// Pi's render runs on the one thread that draws the frame, so a component's render may call the screen and the call runs inside the frame.
// Frame is how the goroutine that runs the frame says so: no other goroutine, including one a render starts and waits for, is the frame.
func TestAltScreenFrameBelongsToTheRenderingGoroutine(t *testing.T) {
	var out bytes.Buffer
	screen := newFrameScreenForTest(&out, 20, 4)
	if _, ok := screen.Frame(); ok {
		t.Fatal("Frame reports a frame outside one")
	}
	var onFrame, onOther bool
	screen.Add(&framePart{render: func(int) []string {
		_, onFrame = screen.Frame()
		other := make(chan bool)
		go func() {
			_, ok := screen.Frame()
			other <- ok
		}()
		onOther = <-other
		return []string{"x"}
	}})
	screen.Start()
	defer screen.StopWithOptions(StopOptions{PreserveScreen: true})
	if !onFrame {
		t.Error("the goroutine that runs the frame is not told it is the frame")
	}
	if onOther {
		t.Error("a goroutine the render started is told it is the frame")
	}
	if _, ok := screen.Frame(); ok {
		t.Error("Frame reports a frame after the frame ended")
	}
}

// The exit dump of Stop renders the components as a frame does, under the same lock.
func TestAltScreenFrameCoversTheExitDump(t *testing.T) {
	var out bytes.Buffer
	screen := newFrameScreenForTest(&out, 20, 4)
	stopping, inDump := false, false
	screen.Add(&framePart{render: func(int) []string {
		if stopping {
			_, inDump = screen.Frame()
		}
		return []string{"x"}
	}})
	screen.Start()
	stopping = true
	screen.StopWithOptions(StopOptions{})
	if !inDump {
		t.Fatal("the exit dump's render is not a frame")
	}
}

// A render that changes the screen reads its own change back: the members of the frame apply at once.
func TestAltScreenFrameMembersApplyAtOnce(t *testing.T) {
	var out bytes.Buffer
	screen := newFrameScreenForTest(&out, 20, 4)
	focus := &framePart{render: func(int) []string { return nil }}
	var before, after, lines1 string
	var focused bool
	armed := false
	screen.Add(&framePart{render: func(int) []string {
		if !armed {
			return []string{"doc"}
		}
		armed = false
		f, ok := screen.Frame()
		if !ok {
			t.Error("render is not the frame")
			return []string{"doc"}
		}
		flags := func() string {
			return strings.Join([]string{boolText(f.GetShowHardwareCursor()), boolText(f.GetClearOnShrink()), boolText(f.GetCopyOnSelect())}, "/")
		}
		before = flags()
		f.SetShowHardwareCursor(true)
		f.SetClearOnShrink(true)
		f.SetCopyOnSelect(false)
		after = flags()
		f.SetFocus(focus)
		focused = screen.GetFocusedComponent() == Component(focus)
		lines1 = strings.Join(f.GetScreenLines(), "|")
		return []string{"doc"}
	}})
	screen.Start()
	defer screen.StopWithOptions(StopOptions{PreserveScreen: true})
	armed = true
	screen.Render()
	if before != "false/false/true" || after != "true/true/false" {
		t.Errorf("flags read in the render: before=%q after=%q, want false/false/true then true/true/false", before, after)
	}
	if !focused {
		t.Error("the focus set in the render is not the focused component in the same render")
	}
	if lines1 == "" || strings.Count(lines1, "|") != 3 {
		t.Errorf("getScreenLines in the render = %q, want the 4 rows of the frame before", lines1)
	}
	if got := boolText(screen.GetShowHardwareCursor()) + "/" + boolText(screen.GetClearOnShrink()) + "/" + boolText(screen.GetCopyOnSelect()); got != "true/true/false" {
		t.Errorf("flags after the frame = %q, want true/true/false", got)
	}
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Pi's Container.render reads its children live: a child that adds a child is followed by it in the same frame, and one that removes the next child
// is followed by the one after. The window the container keeps of what it rendered must agree with the children afterwards, so the next frame, which
// reuses it, draws the same rows.
func TestAltScreenFrameChangesTheChildrenDuringTheirRender(t *testing.T) {
	var out bytes.Buffer
	screen := newFrameScreenForTest(&out, 20, 6)
	line := func(text string) *framePart { return &framePart{render: func(int) []string { return []string{text} }} }
	tail, middle := line("C"), line("B")
	var adds, removes, clears int
	head := &framePart{}
	head.render = func(int) []string {
		f, _ := screen.Frame()
		switch {
		case adds > 0:
			adds--
			f.AddChild(tail)
		case removes > 0:
			removes--
			f.RemoveChild(middle)
		case clears > 0:
			clears--
			f.Clear()
		}
		return []string{"A"}
	}
	screen.Add(head)
	screen.Add(middle)
	screen.Start()
	defer screen.StopWithOptions(StopOptions{PreserveScreen: true})
	rows := func() string { return strings.Join(screenRows(screen, 4), "") }
	if got := rows(); got != "AB" {
		t.Fatalf("rows = %q, want AB", got)
	}

	adds = 1
	screen.Render()
	if got := rows(); got != "ABC" {
		t.Fatalf("rows after a child added a child = %q, want ABC (the added child renders in the same frame)", got)
	}
	screen.Render()
	if got := rows(); got != "ABC" {
		t.Fatalf("rows of the next frame = %q, want ABC (the window agrees with the children)", got)
	}

	removes = 1
	screen.Render()
	if got := rows(); got != "AC" {
		t.Fatalf("rows after a child removed the next child = %q, want AC", got)
	}
	screen.Render()
	if got := rows(); got != "AC" {
		t.Fatalf("rows of the next frame = %q, want AC", got)
	}
	if children := screen.Children(); len(children) != 2 || children[0] != Component(head) || children[1] != Component(tail) {
		t.Fatalf("children = %v, want head and tail", children)
	}

	clears = 1
	screen.Render()
	if got := rows(); got != "A" {
		t.Fatalf("rows after a child cleared the children = %q, want A (the frame that cleared them drew the child that did)", got)
	}
	screen.Render()
	if got := rows(); got != "" {
		t.Fatalf("rows of the next frame = %q, want none", got)
	}
	screen.Add(line("D"))
	screen.Render()
	if got := rows(); got != "D" {
		t.Fatalf("rows after adding to a cleared container = %q, want D", got)
	}
}

// With a layout root the base container is not rendering, so the frame takes its lock like any caller.
func TestAltScreenFrameChangesTheChildrenFromALayoutRoot(t *testing.T) {
	var out bytes.Buffer
	screen := newFrameScreenForTest(&out, 20, 4)
	child := &framePart{render: func(int) []string { return []string{"child"} }}
	var children int
	armed := true
	screen.SetLayoutRoot(&framePart{render: func(int) []string {
		if armed {
			armed = false
			f, _ := screen.Frame()
			f.AddChild(child)
			children = len(f.Children())
		}
		return []string{"root"}
	}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		screen.Start()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a layout root's render could not change the children: the frame waits for a lock it holds")
	}
	defer screen.StopWithOptions(StopOptions{PreserveScreen: true})
	if children != 1 || len(screen.Children()) != 1 {
		t.Fatalf("children in the render = %d, after = %d, want 1", children, len(screen.Children()))
	}
}

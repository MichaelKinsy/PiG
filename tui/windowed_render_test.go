package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// windowScript drives one main-screen renderer through a random sequence of the edits a session makes: messages appended,
// streamed, changed, inserted, replaced and removed in the transcript and in a nested container, editor keystrokes with
// the cursor marker, a status line, overlays, Kitty image lines, width and height changes, forced redraws, clearOnShrink,
// and renders of the tree outside a frame. It returns the bytes of every frame.
type windowScript struct {
	rng     *rand.Rand
	out     *bytes.Buffer
	ui      *TuiMainScreen
	chat    *Container
	nested  *Container
	editor  *Editor
	status  *Text
	overlay *OverlayHandle
	marker  *windowRows
	timers  []*windowTimer
	tail    *AssistantMessageComponent
	tailLen int
	serial  int
	width   int
	height  int
}

func newWindowScript(seed uint64) *windowScript {
	s := &windowScript{rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), out: &bytes.Buffer{}, width: 60, height: 12}
	s.ui = NewWithOutput(s.out, s.width, s.height)
	// The script owns the clock: a render a change requests runs when the script fires the timer, not when the scheduler
	// does, so both runs of a seed render the same frames.
	s.ui.afterFunc = func(_ time.Duration, fn func()) stoppableTimer {
		timer := &windowTimer{fn: fn}
		s.timers = append(s.timers, timer)
		return timer
	}
	s.chat = NewContainer()
	s.nested = NewContainer()
	s.editor = NewEditor()
	s.status = NewText("status 0")
	s.ui.Add(s.chat)
	s.ui.Add(s.editor)
	s.ui.Add(s.status)
	for range 1 + s.rng.IntN(30) {
		s.chat.Add(s.message())
	}
	s.chat.Add(s.nested)
	s.nested.Add(NewText("nested 0"))
	return s
}

func (s *windowScript) text(words int) string {
	var b strings.Builder
	for i := range words {
		if i > 0 {
			if s.rng.IntN(9) == 0 {
				b.WriteString("\n\n")
			} else {
				b.WriteByte(' ')
			}
		}
		s.serial++
		switch s.rng.IntN(6) {
		case 0:
			fmt.Fprintf(&b, "**w%d**", s.serial)
		case 1:
			fmt.Fprintf(&b, "`c%d`", s.serial)
		default:
			fmt.Fprintf(&b, "w%d", s.serial)
		}
	}
	return b.String()
}

func (s *windowScript) message() Component {
	s.serial++
	switch s.rng.IntN(7) {
	case 0:
		return NewUserMessageComponent(s.text(1+s.rng.IntN(20)), nil, 1, nil)
	case 1:
		block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
		block.SetContent([]AssistantSegment{{Thinking: true, Text: s.text(3)}, {Text: s.text(1 + s.rng.IntN(40))}})
		return block
	case 2:
		card := NewToolExecutionComponent("read", fmt.Sprintf("call_%d", s.serial), nil, ToolExecutionOptions{}, nil, nil, "")
		card.UpdateArgs(json.RawMessage(fmt.Sprintf(`{"path":"src/f%d.go"}`, s.serial)))
		card.MarkExecutionStarted()
		card.SetResult(s.text(1+s.rng.IntN(12)), s.rng.IntN(4) == 0, 20*time.Millisecond)
		return card
	case 3:
		return NewSpacer(s.rng.IntN(3))
	case 4:
		lines := make([]string, s.rng.IntN(4))
		for i := range lines {
			s.serial++
			lines[i] = fmt.Sprintf("row %d", s.serial)
		}
		if s.rng.IntN(8) == 0 {
			// A component without a dirty flag renders every frame.
			return &recordingComponent{lines: lines}
		}
		return &windowRows{lines: lines}
	case 5:
		// Kitty image rows: an image header reserving rows, then its blank rows.
		rows := 1 + s.rng.IntN(3)
		lines := []string{fmt.Sprintf("\x1b_Ga=T,r=%d,i=%d;AAAA\x1b\\", rows, s.serial)}
		for range rows - 1 {
			lines = append(lines, "")
		}
		return &windowRows{lines: lines}
	default:
		return NewText(s.text(1 + s.rng.IntN(15)))
	}
}

// windowRows is a component with fixed lines and a dirty flag.
type windowRows struct {
	invalidatable
	lines []string
}

func (w *windowRows) Render(int) []string { return w.lines }

// pick returns a random child of c, or nil.
func (s *windowScript) pick(c *Container) Component {
	children := c.Children()
	if len(children) == 0 {
		return nil
	}
	return children[s.rng.IntN(len(children))]
}

// step makes one to three edits, as a frame may follow several.
func (s *windowScript) step() {
	if s.overlay != nil && s.rng.IntN(3) == 0 {
		s.overlay.Hide()
		s.overlay = nil
	}
	for range 1 + s.rng.IntN(3) {
		s.edit()
		if s.rng.IntN(4) == 0 {
			s.fireTimers()
		}
	}
}

// fireTimers runs the renders requested so far, as the scheduler would between two edits.
func (s *windowScript) fireTimers() {
	timers := s.timers
	s.timers = nil
	for _, timer := range timers {
		if !timer.stopped {
			timer.stopped = true
			timer.fn()
		}
	}
}

// windowTimer is a render timer the script fires.
type windowTimer struct {
	fn      func()
	stopped bool
}

func (w *windowTimer) Stop() bool {
	was := !w.stopped
	w.stopped = true
	return was
}

func (s *windowScript) edit() {
	switch op := s.rng.IntN(108); {
	case op >= 100:
		s.rareEdit(op - 100)
	case op < 22: // stream into the tail message
		if s.tail == nil {
			s.tail = NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
			s.tailLen = 0
			s.chat.Add(s.tail)
		}
		s.tailLen += 1 + s.rng.IntN(5)
		s.tail.SetContent([]AssistantSegment{{Text: s.text(s.tailLen)}})
	case op < 30: // a message ends and the next mounts
		s.tail = nil
		s.chat.Add(s.message())
	case op < 38: // a keystroke
		keys := []string{"a", "b", " ", "\x7f", "\x1b[D", "\x1b[C", "x"}
		s.editor.HandleInput(keys[s.rng.IntN(len(keys))])
	case op < 42:
		s.serial++
		s.status.SetText(fmt.Sprintf("status %d", s.serial))
	case op < 47: // change a message anywhere in the transcript
		switch c := s.pick(s.chat).(type) {
		case *AssistantMessageComponent:
			c.SetContent([]AssistantSegment{{Text: s.text(1 + s.rng.IntN(30))}})
		case *Text:
			c.SetText(s.text(1 + s.rng.IntN(10)))
		case *Spacer:
			c.SetLines(s.rng.IntN(3))
		case *recordingComponent:
			s.serial++
			c.lines = append(c.lines, fmt.Sprintf("row %d", s.serial))
		case *windowRows:
			s.serial++
			c.lines = append(slices.Clone(c.lines), fmt.Sprintf("row %d", s.serial))
			c.Invalidate()
		case *ToolExecutionComponent:
			c.SetResult(s.text(1+s.rng.IntN(8)), false, 30*time.Millisecond)
		}
	case op < 51:
		if child := s.pick(s.chat); child != nil && child != s.nested {
			if child == s.tail {
				s.tail = nil
			}
			s.chat.Remove(child)
		}
	case op < 55:
		if child := s.pick(s.chat); child != nil {
			s.chat.InsertBefore(child, s.message())
		}
	case op < 58:
		if child := s.pick(s.chat); child != nil && child != s.nested {
			if child == s.tail {
				s.tail = nil
			}
			s.chat.Replace(child, s.message())
		}
	case op < 63: // the nested container
		switch s.rng.IntN(3) {
		case 0:
			s.nested.Add(NewText(s.text(1 + s.rng.IntN(8))))
		case 1:
			if child := s.pick(s.nested); child != nil {
				s.nested.Remove(child)
			}
		default:
			if child, ok := s.pick(s.nested).(*Text); ok {
				child.SetText(s.text(1 + s.rng.IntN(8)))
			}
		}
	case op < 66:
		s.width = 20 + s.rng.IntN(80)
		s.ui.SetFixedSize(s.width, s.height)
	case op < 68:
		s.height = 4 + s.rng.IntN(20)
		s.ui.SetFixedSize(s.width, s.height)
	case op < 72:
		if s.overlay == nil {
			s.overlay = s.ui.ShowOverlay(NewText(s.text(1+s.rng.IntN(10))), OverlayOptions{})
		}
	case op < 75:
		if s.rng.IntN(2) == 0 {
			s.ui.SetFocus(s.editor)
		} else {
			s.ui.SetFocus(nil)
		}
	case op < 77:
		s.ui.ForceFullRender()
	case op < 78:
		s.ui.SetClearOnShrink(!s.ui.GetClearOnShrink())
	case op < 79:
		s.ui.SetShowHardwareCursor(!s.ui.GetShowHardwareCursor())
	case op < 80:
		s.tail = nil
		s.chat.Clear()
		s.chat.Add(s.nested)
	case op < 84: // a render of the tree outside the frame, at this width or another
		width := s.width
		if s.rng.IntN(2) == 0 {
			width = 20 + s.rng.IntN(80)
		}
		_ = s.ui.RenderSnapshot(width)
	case op < 87:
		_ = s.chat.Render(s.width)
	default: // a frame with no change
	}
}

func (s *windowScript) rareEdit(op int) {
	switch op {
	case 0, 1: // a status of many lines pushes the editor's cursor row out of the viewport
		var b strings.Builder
		for i := range s.rng.IntN(2 * s.height) {
			fmt.Fprintf(&b, "status line %d\n", i)
		}
		s.status.SetText(b.String())
	case 2, 3: // a second cursor marker below the editor, or none
		lines := []string{}
		if s.rng.IntN(2) == 0 {
			lines = append(lines, "marker "+widthx.CursorMarker+"here")
		}
		if s.marker == nil {
			s.marker = &windowRows{lines: lines}
			s.ui.Add(s.marker)
		} else {
			s.marker.lines = lines
			s.marker.Invalidate()
		}
	case 4: // a renderer swap restores the screen state it captured
		state := s.ui.CaptureRenderState()
		s.ui.RestoreRenderState(state)
	default: // many renders of the tree, each after a change, between two frames
		for range 2 + s.rng.IntN(12) {
			s.serial++
			s.status.SetText(fmt.Sprintf("status %d", s.serial))
			_ = s.ui.RenderSnapshot(s.width)
		}
	}
}

// runWindowScript returns the bytes of every frame of seed's script and the
// number of frames that kept lines of the frame before.
func runWindowScript(t *testing.T, seed uint64, steps int) ([]string, int) {
	t.Helper()
	s := newWindowScript(seed)
	frames := make([]string, 0, steps+1)
	s.ui.Render()
	frames = append(frames, s.out.String())
	windowed := 0
	for range steps {
		s.out.Reset()
		s.step()
		s.ui.Render()
		frames = append(frames, s.out.String())
		if s.ui.resetAgree > 0 {
			windowed++
		}
	}
	return frames, windowed
}

// TestWindowedFramesEqualWholeFrames drives the same random session twice: once with windowed frames, which keep the lines
// before the first change, and once with whole frames, which rebuild and compare every line as Pi's TUI.render does. Every
// frame must write the same bytes.
func TestWindowedFramesEqualWholeFrames(t *testing.T) {
	const seeds, steps = 120, 150
	windowedFrames := 0
	for seed := range uint64(seeds) {
		windowed, kept := runWindowScript(t, seed, steps)
		windowedFrames += kept
		wholeFrames.Store(true)
		whole, wholeKept := runWindowScript(t, seed, steps)
		wholeFrames.Store(false)
		if wholeKept != 0 {
			t.Fatalf("seed %d: whole frames kept lines in %d frames", seed, wholeKept)
		}
		for i := range whole {
			if windowed[i] != whole[i] {
				t.Fatalf("seed %d frame %d differs:\nwindowed %q\nwhole    %q", seed, i, windowed[i], whole[i])
			}
		}
	}
	// The property is vacuous unless most frames take the windowed path.
	if windowedFrames < seeds*steps/2 {
		t.Fatalf("only %d of %d frames kept lines of the frame before", windowedFrames, seeds*steps)
	}
	t.Logf("%d of %d frames kept lines of the frame before", windowedFrames, seeds*steps)
}

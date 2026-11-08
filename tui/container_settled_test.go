package tui

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// settledTwin is one child in the two transcripts a fuzz drives alike: a in the one rendered as usual, b in the one rendered as
// whole frames, which asks every child on every frame.
type settledTwin struct{ a, b Component }

// settledFuzz applies the same edits to two containers: the first renders the way the program does, the second asks every child
// whether it is dirty on every frame (wholeFrames), as the render did before a container read the invalidations of its children.
type settledFuzz struct {
	rng          *rand.Rand
	chatA, chatB *Container
	twins        map[Component]settledTwin
	width        int
	serial       int
}

func newSettledFuzz(seed uint64, initial int) *settledFuzz {
	f := &settledFuzz{rng: rand.New(rand.NewPCG(seed, seed^0xa5a5a5a55a5a5a5a)), chatA: NewContainer(), chatB: NewContainer(), twins: map[Component]settledTwin{}, width: 60}
	for range initial {
		f.add(f.newKid())
	}
	return f
}

func (f *settledFuzz) text(words int) string {
	var b strings.Builder
	for range words {
		f.serial++
		fmt.Fprintf(&b, "w%d ", f.serial)
	}
	return b.String()
}

// newKid makes the same child for both containers: one of each type a transcript holds, a nested container, a custom component that
// keeps a dirty flag of its own, a component that renders every frame, and tool cards that have and have not finished.
func (f *settledFuzz) newKid() settledTwin {
	f.serial++
	both := func(make func() Component) settledTwin {
		tw := settledTwin{a: make(), b: make()}
		f.twins[tw.a] = tw
		return tw
	}
	text, lines := f.text(1+f.rng.IntN(12)), f.rng.IntN(3)
	switch f.rng.IntN(9) {
	case 0:
		return both(func() Component { return NewText(text) })
	case 1:
		return both(func() Component { return NewSpacer(lines) })
	case 2:
		return both(func() Component { return NewUserMessageComponent(text, nil, 1, nil, "", "") })
	case 3:
		return both(func() Component {
			block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
			block.SetContent([]AssistantSegment{{Thinking: true, Text: "think " + text}, {Text: text}})
			return block
		})
	case 4, 5:
		finished, failed := f.rng.IntN(3) != 0, f.rng.IntN(4) == 0
		return both(func() Component {
			card := NewToolExecutionComponent("read", fmt.Sprintf("call_%d", f.serial), nil, ToolExecutionOptions{}, nil, nil, "")
			card.UpdateArgs(json.RawMessage(fmt.Sprintf(`{"path":"src/f%d.go"}`, f.serial)))
			if finished {
				card.MarkExecutionStarted()
				card.SetResult(text, failed, 20*time.Millisecond)
			}
			return card
		})
	case 6:
		return both(func() Component {
			nested := NewContainer()
			nested.Add(NewText(text))
			return nested
		})
	case 7:
		return both(func() Component { return &windowRows{lines: []string{"row " + text}} })
	default:
		return both(func() Component { return &recordingComponent{lines: []string{"live " + text}} })
	}
}

func (f *settledFuzz) add(tw settledTwin) {
	f.chatA.Add(tw.a)
	f.chatB.Add(tw.b)
}

// pick returns a random child of the first container with its twin, or false when it has none.
func (f *settledFuzz) pick() (settledTwin, bool) {
	children := f.chatA.Children()
	if len(children) == 0 {
		return settledTwin{}, false
	}
	return f.twins[children[f.rng.IntN(len(children))]], true
}

// mutate changes a child in both containers as the program does: through the methods that invalidate it.
func (f *settledFuzz) mutate(tw settledTwin) {
	text, lines := f.text(1+f.rng.IntN(10)), f.rng.IntN(3)
	each := func(do func(Component)) {
		do(tw.a)
		do(tw.b)
	}
	switch tw.a.(type) {
	case *Text:
		each(func(c Component) { c.(*Text).SetText(text) })
	case *Spacer:
		each(func(c Component) { c.(*Spacer).SetLines(lines) })
	case *UserMessageComponent:
		each(func(c Component) { c.(*UserMessageComponent).Invalidate() })
	case *AssistantMessageComponent:
		each(func(c Component) { c.(*AssistantMessageComponent).SetContent([]AssistantSegment{{Text: text}}) })
	case *ToolExecutionComponent:
		// A card that has not finished finishes, one that has changes its result, and a started one starts again.
		switch f.rng.IntN(3) {
		case 0:
			each(func(c Component) { c.(*ToolExecutionComponent).SetResult(text, false, 30*time.Millisecond) })
		case 1:
			each(func(c Component) { c.(*ToolExecutionComponent).SetRunning("args " + text) })
		default:
			each(func(c Component) { c.(*ToolExecutionComponent).Invalidate() })
		}
	case *Container:
		each(func(c Component) { c.(*Container).Add(NewText(text)) })
	case *windowRows:
		each(func(c Component) {
			rows := c.(*windowRows)
			rows.lines = append(slices.Clone(rows.lines), "row "+text)
			rows.Invalidate()
		})
	case *recordingComponent:
		each(func(c Component) {
			rows := c.(*recordingComponent)
			rows.lines = append(slices.Clone(rows.lines), "live "+text)
		})
	}
}

func (f *settledFuzz) edit() {
	switch op := f.rng.IntN(100); {
	case op < 26:
		f.add(f.newKid())
	case op < 34:
		if tw, ok := f.pick(); ok {
			f.chatA.InsertBefore(tw.a, f.newKidIn(f.chatA, tw))
		}
	case op < 41:
		if tw, ok := f.pick(); ok {
			f.chatA.Remove(tw.a)
			f.chatB.Remove(tw.b)
		}
	case op < 46:
		if tw, ok := f.pick(); ok {
			next := f.newKid()
			f.chatA.Replace(tw.a, next.a)
			f.chatB.Replace(tw.b, next.b)
		}
	case op < 49:
		// The children become a mix of the ones they were, some listed twice, and new ones.
		var as, bs []Component
		for _, child := range f.chatA.Children() {
			if f.rng.IntN(4) != 0 {
				tw := f.twins[child]
				as, bs = append(as, tw.a), append(bs, tw.b)
			}
			if f.rng.IntN(12) == 0 {
				tw := f.newKid()
				as, bs = append(as, tw.a), append(bs, tw.b)
			}
		}
		f.chatA.SetChildren(as...)
		f.chatB.SetChildren(bs...)
	case op < 50:
		f.chatA.Clear()
		f.chatB.Clear()
	case op < 53:
		if tw, ok := f.pick(); ok {
			f.add(tw) // the same child twice
		}
	case op < 75:
		if tw, ok := f.pick(); ok {
			f.mutate(tw)
		}
	case op < 82:
		if tw, ok := f.pick(); ok {
			tw.a.Invalidate()
			tw.b.Invalidate()
		}
	case op < 86:
		f.width = 20 + f.rng.IntN(60)
	default: // a frame with no change
	}
}

// newKidIn makes a child for InsertBefore, which the fuzz applies to the second container alike: it returns the first one's and inserts the second's.
func (f *settledFuzz) newKidIn(_ *Container, before settledTwin) Component {
	next := f.newKid()
	f.chatB.InsertBefore(before.b, next.b)
	return next.a
}

// render draws both containers and returns their lines.
func (f *settledFuzz) render() (windowed, whole []string) {
	windowed = f.chatA.Render(f.width)
	wholeFrames.Store(true)
	whole = f.chatB.Render(f.width)
	wholeFrames.Store(false)
	return windowed, whole
}

// A container that reads the invalidations of its children draws the lines of one that asks every child on every frame, through
// appends, inserts, removals, replacements, whole replacements of the children (some listed twice), tool cards that finish, children
// that poll, nested containers and width changes, for transcripts of tens to hundreds of children.
func TestSettledScanEqualsAskingEveryChild(t *testing.T) {
	const seeds, steps = 160, 250
	for seed := range uint64(seeds) {
		f := newSettledFuzz(seed, int(seed%7)*40)
		for step := range steps {
			f.edit()
			windowed, whole := f.render()
			if !slices.Equal(windowed, whole) {
				t.Fatalf("seed %d step %d: the lines differ (%d lines, want %d)\nwindowed %q\nwhole    %q", seed, step, len(windowed), len(whole), windowed, whole)
			}
		}
	}
}

// Once a container has found a long transcript clean it asks only the children that need asking: those that poll, and the ones an
// invalidation named.
func TestSettledScanAsksOnlyThePolledChildren(t *testing.T) {
	chat := NewContainer()
	for i := range 300 {
		chat.Add(NewText(fmt.Sprintf("message %d", i)))
	}
	live := &countingDirty{}
	chat.Add(live)
	for i := range 300 {
		chat.Add(NewText(fmt.Sprintf("late message %d", i)))
	}
	chat.Render(80)
	chat.Render(80)
	if chat.settled != 601 || !slices.Equal(chat.polled, []int{300}) {
		t.Fatalf("settled %d, polled %v after two frames of a clean transcript; want 601 and [300]", chat.settled, chat.polled)
	}
	live.asked = 0
	chat.Render(80)
	if live.asked != 1 {
		t.Fatalf("the polled child was asked %d times in one frame, want 1", live.asked)
	}
	// An invalidation of an early child makes the next frame draw from it, and the frame after finds the transcript clean again.
	first := chat.Children()[10].(*Text)
	first.SetText("changed")
	chat.Render(80)
	if chat.settled != 10 {
		t.Fatalf("settled %d after child 10 changed, want 10", chat.settled)
	}
	chat.Render(80)
	if chat.settled != 601 {
		t.Fatalf("settled %d after the change drew, want 601", chat.settled)
	}
	if got := chat.Render(80)[10]; !strings.Contains(got, "changed") {
		t.Fatalf("line 10 is %q, want the changed text", got)
	}
}

// countingDirty is a child that is dirty only when it says so and counts the times it is asked, as a component with a flag of its own does.
type countingDirty struct {
	invalidatable
	asked int
}

func (c *countingDirty) Render(int) []string { return []string{"counting"} }
func (c *countingDirty) IsDirty() bool       { c.asked++; return false }

// Invalidate from other goroutines, which the owner loop's render does not wait for, never loses a change: after the goroutines stop,
// the container draws what a container that asks every child draws.
func TestSettledScanKeepsInvalidationsFromOtherGoroutines(t *testing.T) {
	a, b := NewContainer(), NewContainer()
	var spacersA, spacersB []*Spacer
	for range 200 {
		sa, sb := NewSpacer(1), NewSpacer(1)
		a.Add(sa)
		b.Add(sb)
		spacersA, spacersB = append(spacersA, sa), append(spacersB, sb)
	}
	a.Render(40)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := range 4 {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(g), 7))
			for {
				select {
				case <-stop:
					return
				default:
					spacersA[rng.IntN(len(spacersA))].Invalidate()
				}
			}
		})
	}
	for range 300 {
		a.Render(40)
	}
	close(stop)
	wg.Wait()
	// The goroutines only invalidated: the last frame still agrees with the whole-frame render, and a change made after them is drawn.
	spacersA[150].SetLines(3)
	spacersB[150].SetLines(3)
	wholeFrames.Store(true)
	want := b.Render(40)
	wholeFrames.Store(false)
	if got := a.Render(40); !slices.Equal(got, want) {
		t.Fatalf("lines differ after concurrent invalidations: %d lines, want %d", len(got), len(want))
	}
}

// A child that left the container does not make it draw again from its old place when it invalidates, and one that moved to another
// container is read there.
func TestSettledScanIgnoresAChildThatLeft(t *testing.T) {
	first, second := NewContainer(), NewContainer()
	moved := NewText("moved")
	first.Add(NewText("before"))
	first.Add(moved)
	first.Add(NewText("after"))
	second.Add(NewText("elsewhere"))
	first.Render(40)
	first.Render(40)
	first.Remove(moved)
	second.Add(moved)
	second.Render(40)
	second.Render(40)
	first.Render(40)
	first.Render(40)
	before := first.settled
	moved.SetText("moved again")
	first.Render(40)
	if first.settled != before {
		t.Fatalf("settled %d after a child that left invalidated, want %d", first.settled, before)
	}
	if got := second.Render(40); !strings.Contains(strings.Join(got, "\n"), "moved again") {
		t.Fatalf("the container that holds the child now does not draw its change: %q", got)
	}
}

// An Invalidate that lands after the scan read a child clean and before the container claims it reaches no owner and queues
// nothing; the container then draws the child's change on the next frame all the same.
func TestSettledScanKeepsAnInvalidationBetweenTheCheckAndTheClaim(t *testing.T) {
	a, b := NewContainer(), NewContainer()
	sa, sb := NewSpacer(1), NewSpacer(1)
	a.Add(sa)
	b.Add(sb)
	fired := false
	trustKidHook = func(ch Component) {
		if ch == Component(sa) && !fired {
			fired = true
			sa.SetLines(3)
		}
	}
	defer func() { trustKidHook = nil }()
	a.Render(40) // builds the window; the second frame is the first scan that finds the child settled
	a.Render(40)
	if !fired {
		t.Fatal("the hook did not run: the scan did not claim the child")
	}
	sb.SetLines(3)
	wholeFrames.Store(true)
	want := b.Render(40)
	wholeFrames.Store(false)
	if got := a.Render(40); !slices.Equal(got, want) {
		t.Fatalf("lines after the invalidation = %d, want %d", len(got), len(want))
	}
}

// selfDirtyReporter is a renderer component of an extension: it dirties itself, not through its card.
type selfDirtyReporter struct {
	text  string
	dirty bool
}

func (r *selfDirtyReporter) Render(int) []string { r.dirty = false; return []string{r.text} }
func (r *selfDirtyReporter) IsDirty() bool       { return r.dirty }
func (r *selfDirtyReporter) Invalidate()         { r.dirty = true }

// A finished card whose renderer component is not a Text and reports its own changes is not read from its invalidations: the
// component's later change is drawn.
func TestSettledScanRedrawsAFinishedCardWhoseRendererReportsItself(t *testing.T) {
	reporter := &selfDirtyReporter{text: "before"}
	card := newToolCardForTest("probe", "")
	card.SetDefinition(&ToolDefinitionRenderers{Call: func(ToolRenderInput) (Component, bool) { return reporter, true }}, json.RawMessage(`{}`))
	card.SetResult("done", false, time.Second)
	chat := NewContainer()
	chat.Add(NewSpacer(1))
	chat.Add(card)
	chat.Add(NewSpacer(1))
	chat.Render(40)
	chat.Render(40)
	reporter.text, reporter.dirty = "after", true
	if got := strings.Join(plainLines(chat.Render(40)), "\n"); !strings.Contains(got, "after") {
		t.Fatalf("card drew %q after its renderer changed, want it to contain after", got)
	}
}

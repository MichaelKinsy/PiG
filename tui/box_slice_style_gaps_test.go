package tui

import (
	"math"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type gapChild struct {
	lines       []string
	invalidated int
	renderedAt  []int
}

func (c *gapChild) Render(width int) []string {
	c.renderedAt = append(c.renderedAt, width)
	return c.lines
}
func (c *gapChild) Invalidate() { c.invalidated++ }

// box.ts Box: children render at width - 2*paddingX, every line is left-padded and padded to the full width, padding rows surround the content, bgFn wraps each line, and an empty box renders nothing.
// Pi: packages/tui/src/components/box.ts.
func TestBoxRendersPaddedChildrenWithBackground(t *testing.T) {
	box := NewBox()
	if box.PaddingX != 1 || box.PaddingY != 1 {
		t.Fatalf("NewBox padding = %d,%d, want 1,1", box.PaddingX, box.PaddingY)
	}
	if got := box.Render(10); got == nil || len(got) != 0 {
		t.Fatalf("empty box rendered %#v, want an empty non-nil list", got)
	}
	if got := NewBoxWithEmptyChild().Render(10); got == nil || len(got) != 0 {
		t.Fatalf("box with a child that renders nothing rendered %#v, want an empty non-nil list", got)
	}
	child := &gapChild{lines: []string{"ab", "cd"}}
	box.AddChild(child)
	got := box.Render(6)
	if want := []string{"      ", " ab   ", " cd   ", "      "}; !slices.Equal(got, want) {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	if child.renderedAt[0] != 4 {
		t.Fatalf("child rendered at width %d, want 4", child.renderedAt[0])
	}
	if narrow := box.Render(1); len(narrow) != 4 || child.renderedAt[len(child.renderedAt)-1] != 1 {
		t.Fatalf("narrow render = %q, child width %v", narrow, child.renderedAt)
	}

	padded := NewPaddedBox(2, 0, func(s string) string { return "<" + s + ">" })
	padded.AddChild(&gapChild{lines: []string{"x"}})
	if got := padded.Render(5); !slices.Equal(got, []string{"<  x  >"}) {
		t.Fatalf("padded Render = %q", got)
	}
	padded.SetBgFn(func(s string) string { return "[" + s + "]" })
	if got := padded.Render(5); !slices.Equal(got, []string{"[  x  ]"}) {
		t.Fatalf("Render after SetBgFn = %q (a changed background must not hit the cache)", got)
	}
	padded.SetBgFn(nil)
	padded.SetPaddingX(0)
	if got := padded.Render(5); !slices.Equal(got, []string{"x    "}) {
		t.Fatalf("Render after SetPaddingX = %q", got)
	}

	// A child that grows its lines is picked up: the cache only holds while the child lines match.
	grow := &gapChild{lines: []string{"a"}}
	cached := NewPaddedBox(0, 0, nil)
	cached.AddChild(grow)
	if got := cached.Render(3); !slices.Equal(got, []string{"a  "}) {
		t.Fatalf("first Render = %q", got)
	}
	if again := cached.Render(3); !slices.Equal(again, []string{"a  "}) {
		t.Fatalf("second Render = %q", again)
	}
	cached.PaddingX = 1 // set directly: the cached lines must not be served
	if got := cached.Render(3); !slices.Equal(got, []string{" a "}) {
		t.Fatalf("Render after PaddingX changed = %q", got)
	}
	cached.PaddingX = 0
	grow.lines = []string{"a", "b"}
	if got := cached.Render(3); !slices.Equal(got, []string{"a  ", "b  "}) {
		t.Fatalf("Render after the child grew = %q", got)
	}
	grow.lines = []string{"a", "c"}
	if got := cached.Render(3); !slices.Equal(got, []string{"a  ", "c  "}) {
		t.Fatalf("Render after the child changed = %q", got)
	}
	if got := cached.Render(4); !slices.Equal(got, []string{"a   ", "c   "}) {
		t.Fatalf("Render at a new width = %q", got)
	}
	// Only the number of child lines changes: the remaining lines match the cache, so the line count alone must invalidate it.
	grow.lines = []string{"a"}
	if got := cached.Render(4); !slices.Equal(got, []string{"a   "}) {
		t.Fatalf("Render after the child shrank = %q", got)
	}
	grow.lines = []string{"a", "c"}
	if got := cached.Render(4); !slices.Equal(got, []string{"a   ", "c   "}) {
		t.Fatalf("Render after the child grew back = %q", got)
	}
	cached.PaddingY = 1
	if got := cached.Render(4); len(got) != 4 {
		t.Fatalf("Render after PaddingY = %q", got)
	}
}

func NewBoxWithEmptyChild() *Box {
	box := NewBox()
	box.AddChild(&gapChild{})
	return box
}

// box.ts addChild/removeChild/clear/invalidate: structure changes drop the box's own cache and do not invalidate the children, removal is by identity, and invalidate reaches every child.
func TestBoxAddRemoveClearAndInvalidateReachChildren(t *testing.T) {
	box := NewPaddedBox(0, 0, nil)
	a, b := &gapChild{lines: []string{"a"}}, &gapChild{lines: []string{"b"}}
	box.AddChild(a)
	a.invalidated = 0
	box.AddChild(b)
	if a.invalidated != 0 {
		t.Fatalf("AddChild invalidated an existing child %d times, want 0 (box.ts addChild calls only invalidateCache)", a.invalidated)
	}
	if got := box.Render(2); !slices.Equal(got, []string{"a ", "b "}) {
		t.Fatalf("Render = %q", got)
	}
	a.invalidated, b.invalidated = 0, 0
	box.Invalidate()
	if a.invalidated != 1 || b.invalidated != 1 {
		t.Fatalf("Invalidate reached children %d and %d times, want 1 each", a.invalidated, b.invalidated)
	}
	box.RemoveChild(&gapChild{lines: []string{"a"}}) // a different child: nothing is removed
	if got := box.Render(2); len(got) != 2 {
		t.Fatalf("removing an unknown child changed the box: %q", got)
	}
	box.RemoveChild(a)
	if b.invalidated != 1 {
		t.Fatalf("RemoveChild invalidated the remaining child %d times, want none beyond Invalidate's one (box.ts removeChild calls only invalidateCache)", b.invalidated-1)
	}
	if got := box.Render(2); !slices.Equal(got, []string{"b "}) {
		t.Fatalf("Render after RemoveChild = %q", got)
	}
	box.NeedsRedraw()
	box.Clear()
	if !box.IsDirty() {
		t.Fatal("Clear did not mark the box for redraw")
	}
	if got := box.Render(2); len(got) != 0 {
		t.Fatalf("Render after Clear = %q", got)
	}
}

// cancellable-loader.ts: the signal is the loader's context, cancel aborts it, and dispose stops the animation without aborting.
// Pi: packages/tui/src/components/cancellable-loader.ts.
func TestCancellableLoaderSignalAndDispose(t *testing.T) {
	// The animation timer runs on synctest's fake clock, so the frame checks do not depend on host load.
	synctest.Test(t, func(t *testing.T) {
		loader := NewCancellableLoader(nil, nil, nil, "working", nil)
		signal := loader.Signal()
		if signal != loader.Context() || signal.Err() != nil || loader.Aborted() {
			t.Fatalf("fresh loader: signal %v aborted %v", signal.Err(), loader.Aborted())
		}
		loader.Start()
		frame := func() string { return strings.Join(loader.Render(40), "\n") }
		first := frame()
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if frame() == first {
			t.Fatal("the loader did not animate")
		}
		loader.Dispose()
		stopped := frame()
		time.Sleep(250 * time.Millisecond)
		synctest.Wait()
		if frame() != stopped {
			t.Fatal("Dispose did not stop the animation")
		}
		if loader.Aborted() || signal.Err() != nil {
			t.Fatal("Dispose aborted the loader")
		}
		loader.HandleInput("\x1b")
		if !loader.Aborted() || signal.Err() == nil {
			t.Fatal("Escape did not abort the signal")
		}
	})
}

// OKHSL lightness is the inverse of the Oklab-lightness conversion (color-space toe), fixed at 0 and 1, and monotonic.
// Pi: packages/tui/src/oklab.ts oklabToOkhslLightness.
func TestOklabToOkhslLightnessInvertsTheToe(t *testing.T) {
	if got := OklabToOkhslLightness(0); got != 0 {
		t.Fatalf("lightness of 0 = %v", got)
	}
	if got := OklabToOkhslLightness(1); math.Abs(got-1) > 1e-12 {
		t.Fatalf("lightness of 1 = %v", got)
	}
	// 0.5 -> a known toe value: toe(0.5) with k1=0.206, k2=0.03, k3=(1+k1)/(1+k2).
	k1, k2 := 0.206, 0.03
	k3 := (1 + k1) / (1 + k2)
	t0 := k3*0.5 - k1
	want := 0.5 * (t0 + math.Sqrt(t0*t0+4*k2*k3*0.5))
	if got := OklabToOkhslLightness(0.5); math.Abs(got-want) > 1e-12 || math.Abs(got-0.42114) > 1e-5 {
		t.Fatalf("lightness of 0.5 = %v, want %v", got, want)
	}
	previous := -1.0
	for i := 0; i <= 100; i++ {
		x := float64(i) / 100
		got := OklabToOkhslLightness(x)
		if got <= previous && i > 0 {
			t.Fatalf("not increasing at %v", x)
		}
		previous = got
		if back := okhslToOklabLightness(got); math.Abs(back-x) > 1e-9 {
			t.Fatalf("okhslToOklabLightness(OklabToOkhslLightness(%v)) = %v", x, back)
		}
	}
}

// slice.ts sliceByColumn: columns are terminal cells; ANSI codes before the range stay in order ahead of the first visible cell; strict mode drops a wide character that would cross the end.
// Pi: packages/tui/src/utils.ts sliceByColumn.
func TestSliceByColumnRespectsCellsAnsiAndStrict(t *testing.T) {
	cases := []struct {
		name          string
		line          string
		start, length int
		strict        bool
		want          string
	}{
		{"plain middle", "abcdef", 2, 3, true, "cde"},
		{"zero length", "abcdef", 1, 0, true, ""},
		{"negative length", "abcdef", 1, -2, true, ""},
		{"past the end", "abc", 5, 3, true, ""},
		{"keeps leading codes", "\x1b[31mabcdef\x1b[0m", 2, 2, true, "\x1b[31mcd"},
		{"wide kept when it fits", "a世b", 1, 2, true, "世"},
		{"wide dropped when strict", "ab世", 1, 2, true, "b"},
		{"wide kept when not strict", "ab世", 1, 2, false, "b世"},
		{"wide starting before the range", "世ab", 1, 2, false, "a"},
		{"codes before the range precede the code at its start", "\x1b[31mab\x1b[1mcd", 2, 2, true, "\x1b[31m\x1b[1mcd"},
	}
	for _, tc := range cases {
		if got := widthx.SliceByColumn(tc.line, tc.start, tc.length, tc.strict); got != tc.want {
			t.Errorf("%s: SliceByColumn(%q, %d, %d, %v) = %q, want %q", tc.name, tc.line, tc.start, tc.length, tc.strict, got, tc.want)
		}
	}
	res := widthx.SliceWithWidth("ab世c", 1, 3, true)
	if res.Text != "b世" || res.Width != 3 {
		t.Fatalf("SliceWithWidth = %+v", res)
	}
}

// colors.ts styleTextWithAnsi wraps text in precomputed color codes and attributes, closing in reverse order.
// Pi: packages/tui/src/utils.ts styleTextWithAnsi.
func TestStyleTextWithAnsiOpensAndClosesInReverse(t *testing.T) {
	cases := []struct {
		name   string
		fg, bg string
		attrs  TextAttributes
		want   string
	}{
		{"nothing", "", "", TextAttributes{}, "x"},
		{"fg", "\x1b[31m", "", TextAttributes{}, "\x1b[31mx\x1b[39m"},
		{"bg", "", "\x1b[41m", TextAttributes{}, "\x1b[41mx\x1b[49m"},
		{"fg and bg", "\x1b[31m", "\x1b[41m", TextAttributes{}, "\x1b[31m\x1b[41mx\x1b[49m\x1b[39m"},
		{"bold", "", "", TextAttributes{Bold: true}, "\x1b[1mx\x1b[22m"},
		{"dim", "", "", TextAttributes{Dim: true}, "\x1b[2mx\x1b[22m"},
		{"bold and dim", "", "", TextAttributes{Bold: true, Dim: true}, "\x1b[1m\x1b[2mx\x1b[22m"},
		{"italic", "", "", TextAttributes{Italic: true}, "\x1b[3mx\x1b[23m"},
		{"underline", "", "", TextAttributes{Underline: true}, "\x1b[4mx\x1b[24m"},
		{"inverse", "", "", TextAttributes{Inverse: true}, "\x1b[7mx\x1b[27m"},
		{"strikethrough", "", "", TextAttributes{Strikethrough: true}, "\x1b[9mx\x1b[29m"},
		{"all", "\x1b[31m", "\x1b[41m", TextAttributes{Bold: true, Italic: true, Underline: true, Inverse: true, Strikethrough: true},
			"\x1b[31m\x1b[41m\x1b[1m\x1b[3m\x1b[4m\x1b[7m\x1b[9mx\x1b[29m\x1b[27m\x1b[24m\x1b[23m\x1b[22m\x1b[49m\x1b[39m"},
	}
	for _, tc := range cases {
		if got := StyleTextWithAnsi("x", tc.fg, tc.bg, tc.attrs); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

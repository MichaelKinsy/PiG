package tui

import (
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type widthAwareCacheProbe struct {
	invalidatable
	text        string
	renderCount int
}

func (c *widthAwareCacheProbe) Render(width int) []string {
	c.renderCount++
	return []string{fmt.Sprintf("%d:%s", width, c.text)}
}

func TestContainerRenderReturnsIndependentSlice(t *testing.T) {
	child := &cacheProbeComponent{lines: []string{"one", "two"}}
	container := NewContainer(child)

	first := container.Render(80)
	first[0] = "caller mutation"
	first = append(first, "caller append")
	if len(first) != 3 {
		t.Fatalf("mutated caller slice length=%d, want 3", len(first))
	}

	second := container.Render(80)
	if !slices.Equal(second, []string{"one", "two"}) {
		t.Fatalf("later render was corrupted through returned slice: %q", second)
	}
}

func TestMainScreenCursorExtractionDoesNotMutateBorrowedContainerLines(t *testing.T) {
	child := &cacheProbeComponent{lines: []string{"prompt " + widthx.CursorMarker + "here"}}
	container := NewContainer(child)
	renderer := &TUI{}

	borrowed := container.renderBorrowed(80)
	position, row, cursorFreeLine, ok := findCursorPosition(borrowed, 24)
	if !ok || position.Row != 0 || position.Col != 7 {
		t.Fatalf("cursor position = %+v, ok=%v", position, ok)
	}
	painted := renderer.applyLineResetsCachedWithCursor(borrowed, row, cursorFreeLine)
	if slices.ContainsFunc(painted, func(line string) bool { return strings.Contains(line, widthx.CursorMarker) }) {
		t.Fatalf("painted lines retain cursor marker: %q", painted)
	}
	if got := container.renderBorrowed(80); !strings.Contains(got[0], widthx.CursorMarker) {
		t.Fatalf("cursor extraction corrupted cached component output: %q", got)
	}
}

func TestMainScreenCursorLineCrossesViewportBoundary(t *testing.T) {
	fixture := func() *fixedLinesComponent {
		return &fixedLinesComponent{lines: []string{
			"top",
			"prompt " + widthx.CursorMarker + "x",
			"r2", "r3", "r4", "r5",
		}}
	}

	t.Run("leaves viewport", func(t *testing.T) {
		var out strings.Builder
		ui := NewWithOutput(&out, 80, 10)
		ui.Add(fixture())
		ui.doRender()
		out.Reset()
		ui.height = 2
		ui.doRender()
		if got := out.String(); !strings.Contains(got, widthx.CursorMarker) {
			t.Fatalf("marker above viewport was lost through reset-cache reuse: %q", got)
		}
	})

	t.Run("enters viewport", func(t *testing.T) {
		var out strings.Builder
		ui := NewWithOutput(&out, 80, 2)
		ui.Add(fixture())
		ui.doRender()
		out.Reset()
		ui.height = 10
		ui.doRender()
		if got := out.String(); strings.Contains(got, widthx.CursorMarker) {
			t.Fatalf("cursor marker reached terminal through reset-cache reuse: %q", got)
		}
	})
}

func TestMainScreenChangedFrameAfterIdleRenderStillPaints(t *testing.T) {
	for _, idleFrames := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("idle-frames-%d", idleFrames), func(t *testing.T) {
			var out strings.Builder
			ui := NewWithOutput(&out, 80, 24)
			child := &cacheProbeComponent{lines: []string{"old text"}}
			ui.Add(child)
			ui.doRender()
			for range idleFrames {
				ui.doRender()
			}

			out.Reset()
			child.lines = []string{"NEW VISIBLE TEXT"}
			child.Invalidate()
			ui.doRender()
			if got := out.String(); !strings.Contains(got, "NEW VISIBLE TEXT") {
				t.Fatalf("changed frame after %d idle renders did not paint: %q", idleFrames, got)
			}
		})
	}
}

func TestContainerBorrowedCacheInvalidation(t *testing.T) {
	first := &widthAwareCacheProbe{text: "first"}
	second := &widthAwareCacheProbe{text: "second"}
	container := NewContainer(first)

	assertLines := func(want []string, width int) {
		t.Helper()
		if got := container.renderBorrowed(width); !slices.Equal(got, want) {
			t.Fatalf("renderBorrowed(%d) = %q, want %q", width, got, want)
		}
	}

	assertLines([]string{"80:first"}, 80)
	assertLines([]string{"80:first"}, 80)
	if first.renderCount != 1 {
		t.Fatalf("settled child rendered %d times, want 1", first.renderCount)
	}

	first.text = "changed"
	first.Invalidate()
	assertLines([]string{"80:changed"}, 80)

	container.Add(second)
	assertLines([]string{"80:changed", "80:second"}, 80)
	if first.renderCount != 2 {
		t.Fatalf("adding a child rerendered settled history; count=%d, want 2", first.renderCount)
	}

	container.Remove(first)
	assertLines([]string{"80:second"}, 80)

	assertLines([]string{"100:second"}, 100)
	if second.renderCount != 2 {
		t.Fatalf("width change render count=%d, want 2", second.renderCount)
	}
}

func TestNestedContainerMutationAndOutOfBandRenderStayVisible(t *testing.T) {
	grandchild := &widthAwareCacheProbe{text: "before"}
	inner := NewContainer(grandchild)
	outer := NewContainer(NewContainer(inner))
	if got := outer.Render(80); !slices.Equal(got, []string{"80:before"}) {
		t.Fatalf("initial nested render = %q", got)
	}
	grandchild.text = "after"
	grandchild.Invalidate()
	if got := outer.Render(80); !slices.Equal(got, []string{"80:after"}) {
		t.Fatalf("nested mutation = %q", got)
	}

	type containerEmbedder struct{ *Container }
	embedded := &containerEmbedder{Container: NewContainer()}
	parent := NewContainer(embedded)
	_ = parent.Render(80)
	embedded.Add(&widthAwareCacheProbe{text: "added"})
	_ = embedded.Render(80)
	if got := parent.Render(80); !slices.Contains(got, "80:added") {
		t.Fatalf("out-of-band embedder render hid structural change: %q", got)
	}
}

func TestContainerThemeChangeInvalidatesBorrowedCache(t *testing.T) {
	original := ActiveTheme().Name
	t.Cleanup(func() { SetThemeByName(original) })
	SetTheme("dark")

	child := &cacheProbeComponent{lines: []string{ActiveTheme().Accent + "text"}}
	child.alwaysDirty = false
	markdown := NewMarkdown("**heading** and `code`")
	tool := NewToolExecutionComponent("read", "file.go")
	container := NewContainer(child, markdown, tool)
	gotDark := slices.Clone(container.renderBorrowed(80))

	SetTheme("light")
	child.lines = []string{ActiveTheme().Accent + "text"}
	gotLight := container.renderBorrowed(80)
	if slices.Equal(gotDark, gotLight) {
		t.Fatalf("theme switch retained cached lines %q", gotLight)
	}
	if child.renderCount != 2 {
		t.Fatalf("theme switch render count=%d, want 2", child.renderCount)
	}
}

func FuzzContainerBorrowedMatchesFreshConcatenation(f *testing.F) {
	f.Add([]byte("alpha\x00beta\x01gamma"))
	f.Add([]byte{80, 1, 2, 3, 4, 5, 6})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		width := int(data[0]%120) + 1
		leaf := NewContainer()
		children := make([]*widthAwareCacheProbe, 0, 16)
		container := NewContainer(NewContainer(leaf))
		check := func() {
			t.Helper()
			want := make([]string, len(children))
			for i, child := range children {
				want[i] = fmt.Sprintf("%d:%s", width, child.text)
			}
			if got := container.renderBorrowed(width); !slices.Equal(got, want) {
				t.Fatalf("renderBorrowed(%d) = %q, want %q", width, got, want)
			}
		}

		for i, value := range data[1:] {
			switch value % 4 {
			case 0:
				if len(children) < 16 {
					child := &widthAwareCacheProbe{text: fmt.Sprintf("%d/%d", i, value)}
					children = append(children, child)
					leaf.Add(child)
				}
			case 1:
				if len(children) > 0 {
					index := int(value) % len(children)
					children[index].text += "!"
					children[index].Invalidate()
				}
			case 2:
				if len(children) > 0 {
					index := int(value) % len(children)
					leaf.Remove(children[index])
					children = append(children[:index], children[index+1:]...)
				}
			case 3:
				width = int(value%120) + 1
			}
			check()
		}
	})
}

var containerAllocationLines []string

func fixedAllocatedBytesPerCall(iterations int, call func() []string) uint64 {
	for range 4 {
		containerAllocationLines = call()
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range iterations {
		containerAllocationLines = call()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / uint64(iterations)
}

func productionMainScreenBytesPerFrame(messages int) uint64 {
	ui := NewWithOutput(io.Discard, 100, 24)
	chat := NewContainer()
	for i := range messages {
		chat.Add(&cacheProbeComponent{lines: []string{fmt.Sprintf("line %d", i)}})
	}
	layout := NewContainer(NewText("header"), chat, NewContainer(NewText("editor")), NewText("footer"))
	ui.Add(layout)
	return fixedAllocatedBytesPerCall(24, func() []string {
		ui.doRender()
		return ui.prevLines
	})
}

func productionFullscreenDocumentBytesPerFrame(messages int) uint64 {
	chat := NewContainer()
	for i := range messages {
		chat.Add(&cacheProbeComponent{lines: []string{fmt.Sprintf("line %d", i)}})
	}
	document := NewContainer(NewText("header"), chat)
	return fixedAllocatedBytesPerCall(64, func() []string { return document.renderBorrowed(100) })
}

func TestContainerRenderAllocationDoesNotScaleWithProductionHistory(t *testing.T) {
	mainSmall := productionMainScreenBytesPerFrame(16)
	mainLarge := productionMainScreenBytesPerFrame(2048)
	fullSmall := productionFullscreenDocumentBytesPerFrame(16)
	fullLarge := productionFullscreenDocumentBytesPerFrame(2048)
	t.Logf("main 16=%d B/frame 2048=%d B/frame; fullscreen 16=%d B/frame 2048=%d B/frame", mainSmall, mainLarge, fullSmall, fullLarge)
	if delta := int64(mainLarge) - int64(mainSmall); delta > 4096 {
		t.Fatalf("main-screen steady allocation grew with history by %d B/frame", delta)
	}
	if delta := int64(fullLarge) - int64(fullSmall); delta > 2048 {
		t.Fatalf("fullscreen steady allocation grew with history by %d B/frame", delta)
	}
}

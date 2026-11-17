//go:build pigfm50red

// Parked red proof (FollowMain50ScrollFrames): fails on the current code by design, so it is built only with -tags pigfm50red. The change that makes it pass (P2 for the alt screen test, P3 for the container test) removes the constraint line in the same commit.

package tui

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"
)

// Ports packages/tui/test/tui-alt-screen.test.ts of Pi b7bb7ca905b8, 'scrolls without re-rendering components until content changes'
// (#9549: re-rendering a long transcript on every wheel event saturated a core). The component counts its Render calls, as Pi's
// does; a wheel event and ScrollBy move the viewport only, so no component renders until something requests a render for a change.

type countedComponent struct {
	text    *Text
	renders *int
}

func (c countedComponent) Render(width int) []string {
	*c.renders++
	return c.text.Render(width)
}
func (c countedComponent) Invalidate() { c.text.Invalidate() }

func TestAltScreenScrollsWithoutRenderingComponentsUntilContentChanges(t *testing.T) {
	rows := make([]string, 10)
	for i := range rows {
		rows[i] = fmt.Sprintf("line %d", i+1)
	}
	text := NewPaddedText(strings.Join(rows, "\n"), 0, 0, nil)
	renders := 0
	ui := newAltScreenForTest(nil, 20, 4, TuiAltScreenOptions{})
	ui.out = io.Discard
	ui.afterFunc = func(time.Duration, func()) stoppableTimer { return stoppedOracleTimer{} }
	scroll := NewScrollView(countedComponent{text, &renders}, ScrollViewOptions{Follow: "end", Primary: true})
	t.Cleanup(scroll.Dispose)
	ui.SetLayoutRoot(scroll)
	ui.Start()
	ui.doRender()
	initial := renders

	ui.HandleViewportInput("\x1b[<64;1;1M")
	ui.doRender()
	ui.ScrollBy(-1)
	ui.doRender()
	if renders != initial {
		t.Fatalf("scrolling rendered the component %d more times", renders-initial)
	}
	want := []string{"line 5", "line 6", "line 7", "line 8"}
	if got := trimmedRows(shownRows(ui.GetScreenLines(), 4)); !slices.Equal(got, want) {
		t.Fatalf("viewport = %q, want %q", got, want)
	}

	text.SetText("changed")
	ui.RequestRender()
	ui.doRender()
	if renders <= initial {
		t.Fatalf("a changed component was not rendered again")
	}
	if got := strings.TrimRight(shownRows(ui.GetScreenLines(), 4)[0], " "); got != "changed" {
		t.Fatalf("first row = %q, want %q", got, "changed")
	}
}

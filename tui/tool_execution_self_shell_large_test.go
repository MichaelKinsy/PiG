package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi 5b5be2c316 (#12145): ToolExecutionComponent.render appended the rows of a tool that draws itself (renderShell "self") with
// lines.push(...contentLines), and a few hundred thousand rows exceed V8's argument limit (RangeError: Maximum call stack size exceeded).
// renderSelfShell appends a slice, which has no such limit, so the Go card was never broken. This pins what the Go append can lose: every row
// arrives, in order, after the one blank row, and drawing the unchanged card again costs the copy of the row references, nothing per row.
func TestSelfShellCardDrawsHundredsOfThousandsOfRowsPi5b5be2c31(t *testing.T) {
	const perRenderer = 150_000
	rows := func(prefix string) string {
		var b strings.Builder
		for i := range perRenderer {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(prefix)
			b.WriteString(strconv.Itoa(i))
		}
		return b.String()
	}
	card := newToolCardForTest("probe", "")
	card.SetDefinition(&ToolDefinitionRenderers{
		Self:   true,
		Call:   func(ToolRenderInput) (Component, bool) { return NewPaddedText(rows("call "), 0, 0, nil), true },
		Result: func(ToolRenderInput) (Component, bool) { return NewPaddedText(rows("result "), 0, 0, nil), true },
	}, json.RawMessage(`{}`))
	card.SetResult("done", false, time.Second)

	// tool-execution.ts render, renderShell "self": one blank row, then the rows of the call component and of the result component.
	const want = 1 + 2*perRenderer
	lines := card.Render(30)
	if len(lines) != want {
		t.Fatalf("self-rendered card = %d rows, want %d", len(lines), want)
	}
	for _, row := range []struct {
		index int
		want  string
	}{
		{0, ""},
		{1, "call 0"},
		{perRenderer, "call " + strconv.Itoa(perRenderer-1)},
		{perRenderer + 1, "result 0"},
		{want - 1, "result " + strconv.Itoa(perRenderer-1)},
	} {
		if got := strings.TrimSpace(widthx.StripAnsi(lines[row.index])); got != row.want {
			t.Errorf("row %d = %q, want %q", row.index, got, row.want)
		}
	}

	allocs := testing.AllocsPerRun(5, func() { lines = card.Render(30) })
	if len(lines) != want {
		t.Fatalf("second draw = %d rows, want %d", len(lines), want)
	}
	if allocs > 256 {
		t.Fatalf("drawing the unchanged card allocated %.0f objects for %d rows, want a count that does not grow with the rows", allocs, want)
	}
}

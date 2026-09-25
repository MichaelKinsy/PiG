package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestWordWrapLine_ParityCases(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		width int
		want  []string
	}{
		{"wraps word to next line when it ends exactly at width", "hello world test", 11, []string{"hello ", "world test"}},
		{"keeps whitespace at boundary", "hello world test", 12, []string{"hello world ", "test"}},
		{"unbreakable word filling width exactly followed by space", "aaaaaaaaaaaa aaaa", 12, []string{"aaaaaaaaaaaa", " aaaa"}},
		{"word fits width but not remaining space", "      aaaaaaaaaaaa", 12, []string{"      ", "aaaaaaaaaaaa"}},
		{"multi-space plus word together when they fit", "Lorem ipsum dolor sit amet,    consectetur", 30, []string{"Lorem ipsum dolor sit ", "amet,    consectetur"}},
		{"force-break after backtracking still overflows", " " + repeat("a", 186) + "你", 187, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			chunks := wordWrapLine(tc.line, tc.width, nil)
			for _, chunk := range chunks {
				if widthx.VisibleWidth(chunk.text) > tc.width {
					t.Fatalf("chunk %q width=%d > %d", chunk.text, widthx.VisibleWidth(chunk.text), tc.width)
				}
			}
			if tc.want != nil {
				got := make([]string, len(chunks))
				for i, chunk := range chunks {
					got[i] = chunk.text
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("chunks = %v, want %v", got, tc.want)
				}
			}
			var reconstructed strings.Builder
			for _, chunk := range chunks {
				reconstructed.WriteString(tc.line[chunk.startIndex:chunk.endIndex])
			}
			if reconstructed.String() != tc.line {
				t.Fatalf("reconstructed = %q, want %q", reconstructed.String(), tc.line)
			}
		})
	}
}

func TestEditor_EnterSubmitsAndClears(t *testing.T) {
	e := NewEditor()
	var changes []string
	var submitted string
	e.OnChange = func(text string) { changes = append(changes, text) }
	e.OnSubmit = func(text string) { submitted = text }

	e.HandleInput("hello")
	e.HandleInput("\r")

	if submitted != "hello" {
		t.Fatalf("submitted = %q, want hello", submitted)
	}
	if got := e.Text(); got != "" {
		t.Fatalf("editor text after submit = %q, want empty", got)
	}
	if len(changes) < 2 {
		t.Fatalf("expected onChange calls for typing + clear, got %v", changes)
	}
	if changes[0] != "hello" {
		t.Fatalf("first onChange = %q, want hello", changes[0])
	}
	if changes[len(changes)-1] != "" {
		t.Fatalf("final onChange = %q, want empty", changes[len(changes)-1])
	}
}

func TestEditor_DisableSubmitBlocksEnter(t *testing.T) {
	e := NewEditor()
	e.DisableSubmit = true
	called := false
	e.OnSubmit = func(string) { called = true }

	e.HandleInput("hello")
	e.HandleInput("\r")

	if called {
		t.Fatal("OnSubmit called with DisableSubmit=true")
	}
	if got := e.Text(); got != "hello" {
		t.Fatalf("editor text = %q, want hello", got)
	}
}

func TestEditor_PreferredVisualColumnRestoresAfterShortLine(t *testing.T) {
	e := NewEditor()
	e.SetText("abcde\nab\nabcde")
	e.cursor = [2]int{0, 4}
	_ = e.Render(6)

	e.HandleInput("\x1b[B")
	if got := e.cursor; got != [2]int{1, 2} {
		t.Fatalf("after first down cursor = %v, want [1 2]", got)
	}
	if e.preferredVisualCol == nil || *e.preferredVisualCol != 4 {
		t.Fatalf("preferredVisualCol = %v, want 4", e.preferredVisualCol)
	}

	e.HandleInput("\x1b[B")
	if got := e.cursor; got != [2]int{2, 4} {
		t.Fatalf("after second down cursor = %v, want [2 4]", got)
	}
	if e.preferredVisualCol != nil {
		t.Fatalf("preferredVisualCol should clear after restore, got %v", *e.preferredVisualCol)
	}
}

func TestEditor_UpOnWrappedHistoryEntryMovesWithinVisualLines(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("older")
	e.AddToHistory("abcdefghijklm")
	e.navigateHistory(-1)
	e.cursor = [2]int{0, 7}
	_ = e.Render(6)

	e.HandleInput("\x1b[A")
	if got := e.Text(); got != "abcdefghijklm" {
		t.Fatalf("up replaced wrapped history entry with %q", got)
	}
	if got := e.cursor; got != [2]int{0, 2} {
		t.Fatalf("wrapped up cursor = %v, want [0 2]", got)
	}

	e.HandleInput("\x1b[B")
	if got := e.Text(); got != "abcdefghijklm" {
		t.Fatalf("down left history before the last visual line: %q", got)
	}
	if got := e.cursor; got != [2]int{0, 7} {
		t.Fatalf("wrapped down cursor = %v, want [0 7]", got)
	}
}

func TestEditor_HistoryDirectionControlsCursorPlacement(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("older")
	e.AddToHistory("latest")

	e.HandleInput("\x1b[A")
	if got := e.Text(); got != "latest" || e.cursor != [2]int{0, 0} {
		t.Fatalf("latest history state = (%q, %v), want cursor at start", got, e.cursor)
	}
	e.HandleInput("\x1b[A")
	if got := e.Text(); got != "older" || e.cursor != [2]int{0, 0} {
		t.Fatalf("older history state = (%q, %v), want cursor at start", got, e.cursor)
	}
	e.HandleInput("\x1b[B")
	if got := e.Text(); got != "latest" || e.cursor != [2]int{0, len("latest")} {
		t.Fatalf("newer history state = (%q, %v), want cursor at end", got, e.cursor)
	}
}

func TestEditor_LeavingHistoryRestoresDraftCursor(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("older")
	e.SetText("first\nsecond")
	e.cursor = [2]int{0, 3}

	e.navigateHistory(-1)
	e.navigateHistory(1)

	if got := e.Text(); got != "first\nsecond" {
		t.Fatalf("restored draft = %q", got)
	}
	if got := e.cursor; got != [2]int{0, 3} {
		t.Fatalf("restored draft cursor = %v, want [0 3]", got)
	}
}

func TestEditor_UpOnFirstVisualLineMovesToStartBeforeHistory(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("older")
	e.SetText("draft")
	e.cursor = [2]int{0, 2}
	_ = e.Render(20)

	e.HandleInput("\x1b[A")
	if got := e.Text(); got != "draft" {
		t.Fatalf("first up entered history from a nonzero column: %q", got)
	}
	if got := e.cursor; got != [2]int{0, 0} {
		t.Fatalf("first up cursor = %v, want line start", got)
	}

	e.HandleInput("\x1b[A")
	if got := e.Text(); got != "older" {
		t.Fatalf("second up text = %q, want history entry", got)
	}
	if got := e.cursor; got != [2]int{0, 0} {
		t.Fatalf("history cursor = %v, want start", got)
	}
}

func TestEditor_JumpForwardAndBackward(t *testing.T) {
	e := NewEditor()
	e.SetText("alpha\nbeta\ngamma")
	e.cursor = [2]int{0, 0}

	e.HandleInput("\x1d")
	e.HandleInput("m")
	if got := e.cursor; got != [2]int{2, 2} {
		t.Fatalf("after jump forward cursor = %v, want [2 2]", got)
	}

	e.HandleInput("\x1b\x1d")
	e.HandleInput("b")
	if got := e.cursor; got != [2]int{1, 0} {
		t.Fatalf("after jump backward cursor = %v, want [1 0]", got)
	}
}

func TestEditor_PageDownUsesVisibleWindowSize(t *testing.T) {
	e := NewEditor()
	e.SetMaxVisibleLines(3)
	e.SetText("l1\nl2\nl3\nl4\nl5\nl6\nl7")
	e.cursor = [2]int{0, 0}
	_ = e.Render(10)

	e.HandleInput("\x1b[6~")
	if got := e.cursor; got != [2]int{5, 0} {
		t.Fatalf("after page down cursor = %v, want [5 0]", got)
	}

	e.HandleInput("\x1b[5~")
	if got := e.cursor; got != [2]int{0, 0} {
		t.Fatalf("after page up cursor = %v, want [0 0]", got)
	}
}

func TestEditor_SetTextCallsOnChange(t *testing.T) {
	e := NewEditor()
	var got []string
	e.OnChange = func(text string) { got = append(got, text) }

	e.SetText("hello")
	e.Clear()

	want := []string{"hello", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("onChange calls = %v, want %v", got, want)
	}
}

func TestEditor_Render_WordWrapParity(t *testing.T) {
	e := NewEditor()
	e.SetText("hello world test")
	e.cursor = [2]int{0, 1}
	rows := editorContentRows(e.Render(11))
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want 2 content rows", rows)
	}
	if got := stripANSI(rows[0]); got != "hello " {
		t.Fatalf("row 0 = %q, want %q", got, "hello ")
	}
	if got := stripANSI(rows[1]); got != "world test" {
		t.Fatalf("row 1 = %q, want %q", got, "world test")
	}
}

func TestEditor_Render_ReservesRightmostColumnForCursor(t *testing.T) {
	e := NewEditor()
	e.SetText(strings.Repeat("x", 80))
	rows := editorContentRows(e.Render(80))
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if got := widthx.VisibleWidth(rows[0]); got != 79 {
		t.Fatalf("first row width = %d, want 79", got)
	}
	if got := stripANSI(rows[1]); got != "x " {
		t.Fatalf("second row = %q, want cursor-decorated trailing x", got)
	}
}

func repeat(s string, n int) string {
	var out strings.Builder
	for range n {
		out.WriteString(s)
	}
	return out.String()
}

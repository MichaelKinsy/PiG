package tui

// tests for the BashExecutionBlock TUI component.

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestBashExecutionBlock_RunningHeader(t *testing.T) {
	b := NewBashExecutionBlock("echo hi", false)
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "$ echo hi") {
		t.Errorf("expected `$ echo hi` header, got:\n%s", joined)
	}
	if !strings.Contains(joined, "Running") {
		t.Errorf("expected `Running` in loader, got:\n%s", joined)
	}
}

func TestBashExecutionBlock_AppendsOutputLines(t *testing.T) {
	b := NewBashExecutionBlock("seq 3", false)
	b.AppendOutput("1\n2\n3\n")
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	for _, want := range []string{"1", "2", "3"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected output %q in render, got:\n%s", want, joined)
		}
	}
}

// TestBashExecutionBlock_PreservesTrailingNewlineRow locks the parity
// behavior fixed in the interactive-rendering loop. Upstream renders
// `echo hi\n` as output producing TWO logical lines (content + blank)
// because `"hi\n".split("\n")` returns ["hi", ""]. Previously pig
// `TrimRight("\n")`-stripped the trailing newline, losing the blank.
func TestBashExecutionBlock_PreservesTrailingNewlineRow(t *testing.T) {
	b := NewBashExecutionBlock("echo hi", false)
	b.AppendOutput("hi\n")
	zero := 0
	b.startedAt = time.Now().Add(-10 * time.Millisecond)
	b.SetComplete(&zero, false, false)
	rows := b.Render(80)
	// Expected box body (between borders): blank, " hi", blank.
	// Locate top border row by hyphen and count rows until bottom border.
	var topIdx, botIdx = -1, -1
	for i, r := range rows {
		if strings.Contains(r, "\u2500") {
			if topIdx < 0 {
				topIdx = i
			} else {
				botIdx = i
				break
			}
		}
	}
	if topIdx < 0 || botIdx < 0 {
		t.Fatalf("missing borders in render: %#v", rows)
	}
	inside := rows[topIdx+1 : botIdx]
	if len(inside) != 4 {
		t.Errorf("expected 4 inside rows (header, blank, output, blank), got %d: %#v", len(inside), inside)
	}
	if !strings.Contains(inside[0], "$ echo hi") {
		t.Errorf("row 0 should be header `$ echo hi`, got %q", inside[0])
	}
	if inside[1] != "" {
		t.Errorf("row 1 should be blank (upstream `\\n` prefix), got %q", inside[1])
	}
	if !strings.Contains(inside[2], "hi") {
		t.Errorf("row 2 should contain `hi`, got %q", inside[2])
	}
	if inside[3] != "" {
		t.Errorf("row 3 should be blank (trailing newline preserved), got %q", inside[3])
	}
}

func TestBashExecutionBlock_CompleteSuccessHidesStatus(t *testing.T) {
	// Upstream `bash-execution.ts:184-188`: status `(exit N)` is
	// rendered ONLY for non-zero exit codes (the "error" branch).
	// On success, no status row appears. This test locks parity.
	b := NewBashExecutionBlock("true", false)
	b.startedAt = time.Now().Add(-200 * time.Millisecond)
	zero := 0
	b.SetComplete(&zero, false, false)
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "exit 0") {
		t.Errorf("successful exit must NOT render `exit 0` (upstream parity), got:\n%s", joined)
	}
}

func TestBashExecutionBlock_NonZeroIsRed(t *testing.T) {
	b := NewBashExecutionBlock("exit 7", false)
	b.startedAt = time.Now().Add(-100 * time.Millisecond)
	seven := 7
	b.SetComplete(&seven, false, false)
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "(exit 7)") {
		t.Errorf("expected `(exit 7)`, got:\n%s", joined)
	}
	if !strings.Contains(joined, "\033[31m") {
		t.Errorf("expected red ANSI for non-zero exit, got:\n%s", joined)
	}
	// Upstream renders `(exit N)` only: no duration suffix.
	if strings.Contains(joined, "·") {
		t.Errorf("non-zero exit must NOT include duration `· Xms` (upstream parity), got:\n%s", joined)
	}
}

func TestBashExecutionBlock_Cancelled(t *testing.T) {
	b := NewBashExecutionBlock("sleep 5", false)
	b.startedAt = time.Now().Add(-100 * time.Millisecond)
	b.SetComplete(nil, true, false)
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "(cancelled)") {
		t.Errorf("expected `(cancelled)`, got:\n%s", joined)
	}
	// Upstream renders bare `(cancelled)` in warning color: no duration.
	if strings.Contains(joined, "·") {
		t.Errorf("cancelled status must NOT include duration (upstream parity), got:\n%s", joined)
	}
}

func TestBashExecutionBlock_TruncatedRibbon(t *testing.T) {
	b := NewBashExecutionBlock("yes", false)
	b.startedAt = time.Now().Add(-50 * time.Millisecond)
	zero := 0
	b.SetCompleteWithOutput(&zero, false, true, "tail", "/tmp/full-output.log")
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "Output truncated. Full output: /tmp/full-output.log") {
		t.Errorf("expected truncation warning with full-output path, got:\n%s", joined)
	}
}

func TestBashExecutionBlockDurableTruncationIsIndependentOfPreviewCollapse(t *testing.T) {
	preview := NewBashExecutionBlock("seq 30", false)
	for range 30 {
		preview.AppendOutput("line\n")
	}
	zero := 0
	preview.SetComplete(&zero, false, false)
	previewText := strings.Join(preview.Render(80), "\n")
	if !strings.Contains(previewText, "more lines") || strings.Contains(previewText, "Output truncated") {
		t.Fatalf("preview collapse was reported as durable truncation:\n%s", previewText)
	}

	durable := NewBashExecutionBlock("large output", false)
	durable.AppendOutput("discarded\ntail")
	durable.SetCompleteWithOutput(&zero, false, true, "tail", "/tmp/full-output.log")
	durableText := strings.Join(durable.Render(80), "\n")
	if strings.Contains(durableText, "more lines") || strings.Contains(durableText, "discarded") || !strings.Contains(durableText, "tail") || !strings.Contains(durableText, "Output truncated. Full output: /tmp/full-output.log") {
		t.Fatalf("durable truncation was coupled to preview collapse:\n%s", durableText)
	}
}

func TestBashExecutionBlock_ExcludeFromContextDimColor(t *testing.T) {
	// `!!cmd` variant: header should use dim color, not bashHeaderColor.
	b := NewBashExecutionBlock("echo secret", true)
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, bashHeaderColor()) {
		t.Errorf("excludeFromContext block should NOT use bash color, got:\n%s", joined)
	}
}

// Row 2.8a: bordered-block visual parity: top + bottom horizontal
// rule lines in bashMode (or dim for !!), full terminal width.
func TestBashExecutionBlock_HasTopAndBottomBorders(t *testing.T) {
	b := NewBashExecutionBlock("echo hi", false)
	rows := b.Render(40)
	// rows[0] is leading spacer (empty); rows[1] should be top border.
	if rows[0] != "" {
		t.Errorf("row 0 should be leading spacer, got %q", rows[0])
	}
	if !strings.Contains(rows[1], "\u2500") || !strings.Contains(rows[1], bashHeaderColor()) {
		t.Errorf("row 1 should be bashMode top border, got %q", rows[1])
	}
	// Last is trailing spacer; second-to-last is bottom border.
	if rows[len(rows)-1] != "" {
		t.Errorf("last row should be trailing spacer, got %q", rows[len(rows)-1])
	}
	bottom := rows[len(rows)-2]
	if !strings.Contains(bottom, "\u2500") || !strings.Contains(bottom, bashHeaderColor()) {
		t.Errorf("bottom border missing/wrong: %q", bottom)
	}
}

func TestBashExecutionBlock_ExcludedUsesDimBorder(t *testing.T) {
	b := NewBashExecutionBlock("echo s", true)
	rows := b.Render(40)
	if !strings.Contains(rows[1], bashDimColor()) {
		t.Errorf("!! variant should use dim color border, got %q", rows[1])
	}
	if strings.Contains(rows[1], bashHeaderColor()) {
		t.Errorf("!! variant border must NOT use bashMode color, got %q", rows[1])
	}
}

func TestBashExecutionBlock_CollapsePreviewLimit(t *testing.T) {
	b := NewBashExecutionBlock("yes", false)
	// 30 chunks of `line\n` → 31 logical output lines (30 "line" + 1 trailing empty)
	// after "X\n".split("\n") semantics. Preview=20, hidden=11.
	for range 30 {
		b.AppendOutput("line\n")
	}
	zero := 0
	b.startedAt = time.Now().Add(-50 * time.Millisecond)
	b.SetComplete(&zero, false, false)

	// Collapsed (default).
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "... 11 more lines") {
		t.Errorf("expected collapse hint `... 11 more lines`, got:\n%s", joined)
	}
	if !strings.Contains(joined, "ctrl+o to expand") {
		t.Errorf("expected expand-key hint, got:\n%s", joined)
	}

	// Expanded.
	b.SetExpanded(true)
	rowsExp := b.Render(80)
	joinedExp := strings.Join(rowsExp, "\n")
	if strings.Contains(joinedExp, "more lines") {
		t.Errorf("expanded should not show `more lines`, got:\n%s", joinedExp)
	}
	if !strings.Contains(joinedExp, "ctrl+o to collapse") {
		t.Errorf("expanded output with hidden logical lines should show collapse hint, got:\n%s", joinedExp)
	}
	if len(rowsExp) <= len(rows) {
		t.Errorf("expanded should be taller than collapsed: exp=%d coll=%d", len(rowsExp), len(rows))
	}
}

func TestBashExecutionBlockWrapsLongOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		want   []string
	}{
		{name: "ASCII", output: "abcdefghijklmnopqrstuvwxyz", want: []string{"abcdefghij", "klmnopqrst", "uvwxyz"}},
		{name: "ANSI", output: "\x1b[31mabcdefghijklmnopqrstuv\x1b[0m", want: []string{"abcdefghij", "klmnopqrst", "uv"}},
		{name: "wide Unicode", output: "你好世界五六七八九十", want: []string{"你好世界五", "六七八九十"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			block := NewBashExecutionBlock("printf", false)
			block.AppendOutput(test.output)
			zero := 0
			block.SetComplete(&zero, false, false)
			plain := widthx.StripAnsi(strings.Join(block.Render(12), "\n"))
			for _, want := range test.want {
				if !strings.Contains(plain, want) {
					t.Fatalf("wrapped output missing %q:\n%s", want, plain)
				}
			}
		})
	}
}

func TestBashExecutionBlockRewrapsAfterResize(t *testing.T) {
	block := NewBashExecutionBlock("printf", false)
	block.AppendOutput("abcdefghijklmnopqrstuvwxyz")
	zero := 0
	block.SetComplete(&zero, false, false)

	narrow := block.Render(12)
	wide := block.Render(22)
	if got := countRowsContaining(narrow, bashMutedColor()); got != 3 {
		t.Fatalf("narrow wrapped rows = %d, want 3: %#v", got, narrow)
	}
	if got := countRowsContaining(wide, bashMutedColor()); got != 2 {
		t.Fatalf("wide wrapped rows = %d, want 2: %#v", got, wide)
	}
}

func TestBashExecutionBlockCollapsedLimitUsesVisualRows(t *testing.T) {
	block := NewBashExecutionBlock("printf", false)
	block.AppendOutput(strings.Repeat("a", 50) + strings.Repeat("b", 200))
	zero := 0
	block.SetComplete(&zero, false, false)

	collapsed := block.Render(12)
	if got := countRowsContaining(collapsed, bashMutedColor()); got != previewLines {
		t.Fatalf("collapsed output rows = %d, want %d", got, previewLines)
	}
	if strings.Contains(widthx.StripAnsi(strings.Join(collapsed, "\n")), "a") {
		t.Fatalf("collapsed output retained rows older than the final %d visual rows", previewLines)
	}

	block.SetExpanded(true)
	expanded := block.Render(12)
	if got := countRowsContaining(expanded, bashMutedColor()); got != 25 {
		t.Fatalf("expanded output rows = %d, want 25", got)
	}
	if !strings.Contains(widthx.StripAnsi(strings.Join(expanded, "\n")), "aaaaa") {
		t.Fatal("expanded output lost the beginning of the long logical line")
	}
}

func TestBashExecutionBlockExcludeFromContextDoesNotChangeBody(t *testing.T) {
	render := func(exclude bool) []string {
		block := NewBashExecutionBlock("printf", exclude)
		block.AppendOutput("abcdefghijklmnopqrstuvwxyz")
		zero := 0
		block.SetComplete(&zero, false, false)
		rows := block.Render(12)
		plain := make([]string, len(rows))
		for i, row := range rows {
			plain[i] = widthx.StripAnsi(row)
		}
		return plain
	}
	if included, excluded := render(false), render(true); !reflect.DeepEqual(included, excluded) {
		t.Fatalf("! and !! visible text differ:\n!  %#v\n!! %#v", included, excluded)
	}
}

func countRowsContaining(rows []string, marker string) int {
	count := 0
	for _, row := range rows {
		if strings.Contains(row, marker) {
			count++
		}
	}
	return count
}

func TestEditor_BashModeSwitchesBorderColor(t *testing.T) {
	e := NewEditor()
	if e.IsBashMode() {
		t.Error("empty editor should not be in bash mode")
	}
	e.HandleInput("!")
	if !e.IsBashMode() {
		t.Error("editor should enter bash mode on `!`")
	}
	rows := e.Render(80)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, bashHeaderColor()) {
		t.Errorf("editor border should use bashHeaderColor() in bash mode, got:\n%s", joined)
	}
}

func TestEditor_BashModeSuppressesAutocomplete(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	// Slash mode → popup opens.
	e.HandleInput("/")
	if !e.AutocompleteOpen() {
		t.Fatal("popup should open on `/`")
	}
	// Switch buffer to `!`.
	e.SetText("!")
	if e.AutocompleteOpen() {
		t.Error("popup should be suppressed in bash mode")
	}
}

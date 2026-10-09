package tui

// pi: packages/coding-agent/src/modes/interactive/components/bash-execution.ts

// tests for the BashExecutionComponent TUI component.

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestBashExecutionBlock_RunningHeader(t *testing.T) {
	b := NewBashExecutionComponent("echo hi", nil, false, 1)
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
	b := NewBashExecutionComponent("seq 3", nil, false, 1)
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
	b := NewBashExecutionComponent("echo hi", nil, false, 1)
	b.AppendOutput("hi\n")
	zero := 0
	b.startedAt = time.Now().Add(-10 * time.Millisecond)
	b.SetComplete(&zero, false, nil, "")
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
	if strings.TrimSpace(inside[1]) != "" {
		t.Errorf("row 1 should be blank (upstream `\\n` prefix), got %q", inside[1])
	}
	if !strings.Contains(inside[2], "hi") {
		t.Errorf("row 2 should contain `hi`, got %q", inside[2])
	}
	if widthx.VisibleWidth(strings.TrimSpace(widthx.StripAnsi(inside[3]))) != 0 {
		t.Errorf("row 3 should be blank (trailing newline preserved), got %q", inside[3])
	}
}

func TestBashExecutionBlock_CompleteSuccessHidesStatus(t *testing.T) {
	// Upstream `bash-execution.ts:184-188`: status `(exit N)` is
	// rendered ONLY for non-zero exit codes (the "error" branch).
	// On success, no status row appears. This test locks parity.
	b := NewBashExecutionComponent("true", nil, false, 1)
	b.startedAt = time.Now().Add(-200 * time.Millisecond)
	zero := 0
	b.SetComplete(&zero, false, nil, "")
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "exit 0") {
		t.Errorf("successful exit must NOT render `exit 0` (upstream parity), got:\n%s", joined)
	}
}

func TestBashExecutionBlock_NonZeroIsRed(t *testing.T) {
	b := NewBashExecutionComponent("exit 7", nil, false, 1)
	b.startedAt = time.Now().Add(-100 * time.Millisecond)
	seven := 7
	b.SetComplete(&seven, false, nil, "")
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "(exit 7)") {
		t.Errorf("expected `(exit 7)`, got:\n%s", joined)
	}
	if want := ActiveTheme().Fg("error", "(exit 7)"); !strings.Contains(joined, want) {
		t.Errorf("expected the theme's error color around the exit status (bash-execution.ts:179), want %q in:\n%s", want, joined)
	}
	// Upstream renders `(exit N)` only: no duration suffix.
	if strings.Contains(joined, "·") {
		t.Errorf("non-zero exit must NOT include duration `· Xms` (upstream parity), got:\n%s", joined)
	}
}

func TestBashExecutionBlock_Cancelled(t *testing.T) {
	b := NewBashExecutionComponent("sleep 5", nil, false, 1)
	b.startedAt = time.Now().Add(-100 * time.Millisecond)
	b.SetComplete(nil, true, nil, "")
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
	b := NewBashExecutionComponent("yes", nil, false, 1)
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
	preview := NewBashExecutionComponent("seq 30", nil, false, 1)
	for range 30 {
		preview.AppendOutput("line\n")
	}
	zero := 0
	preview.SetComplete(&zero, false, nil, "")
	previewText := strings.Join(preview.Render(80), "\n")
	if !strings.Contains(previewText, "more lines") || strings.Contains(previewText, "Output truncated") {
		t.Fatalf("preview collapse was reported as durable truncation:\n%s", previewText)
	}

	durable := NewBashExecutionComponent("large output", nil, false, 1)
	durable.AppendOutput("discarded\ntail")
	durable.SetCompleteWithOutput(&zero, false, true, "tail", "/tmp/full-output.log")
	durableText := strings.Join(durable.Render(80), "\n")
	if strings.Contains(durableText, "more lines") || strings.Contains(durableText, "discarded") || !strings.Contains(durableText, "tail") || !strings.Contains(durableText, "Output truncated. Full output: /tmp/full-output.log") {
		t.Fatalf("durable truncation was coupled to preview collapse:\n%s", durableText)
	}
}

func TestBashExecutionBlock_ExcludeFromContextDimColor(t *testing.T) {
	// `!!cmd` variant: header should use dim color, not bashHeaderColor.
	b := NewBashExecutionComponent("echo secret", nil, true, 1)
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, bashHeaderColor()) {
		t.Errorf("excludeFromContext block should NOT use bash color, got:\n%s", joined)
	}
}

// Row 2.8a: bordered-block visual parity: top + bottom horizontal
// rule lines in bashMode (or dim for !!), full terminal width.
func TestBashExecutionBlock_HasTopAndBottomBorders(t *testing.T) {
	b := NewBashExecutionComponent("echo hi", nil, false, 1)
	rows := b.Render(40)
	// rows[0] is leading spacer (empty); rows[1] should be top border.
	if rows[0] != "" {
		t.Errorf("row 0 should be leading spacer, got %q", rows[0])
	}
	if !strings.Contains(rows[1], "\u2500") || !strings.Contains(rows[1], bashHeaderColor()) {
		t.Errorf("row 1 should be bashMode top border, got %q", rows[1])
	}
	// bash-execution.ts constructor: the bottom border is the last child; the next transcript component brings its own spacer.
	bottom := rows[len(rows)-1]
	if !strings.Contains(bottom, "\u2500") || !strings.Contains(bottom, bashHeaderColor()) {
		t.Errorf("bottom border missing/wrong: %q", bottom)
	}
}

func TestBashExecutionBlock_ExcludedUsesDimBorder(t *testing.T) {
	b := NewBashExecutionComponent("echo s", nil, true, 1)
	rows := b.Render(40)
	if !strings.Contains(rows[1], bashDimColor()) {
		t.Errorf("!! variant should use dim color border, got %q", rows[1])
	}
	if strings.Contains(rows[1], bashHeaderColor()) {
		t.Errorf("!! variant border must NOT use bashMode color, got %q", rows[1])
	}
}

// Pi: packages/coding-agent/src/modes/interactive/components/bash-execution.ts:70 (BashExecutionComponent.setExpanded).
func TestBashExecutionBlock_CollapsePreviewLimit(t *testing.T) {
	b := NewBashExecutionComponent("yes", nil, false, 1)
	// 30 chunks of `line\n` → 31 logical output lines (30 "line" + 1 trailing empty)
	// after "X\n".split("\n") semantics. Preview=20, hidden=11.
	for range 30 {
		b.AppendOutput("line\n")
	}
	zero := 0
	b.startedAt = time.Now().Add(-50 * time.Millisecond)
	b.SetComplete(&zero, false, nil, "")

	// Collapsed (default).
	rows := b.Render(80)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "... 11 more lines") {
		t.Errorf("expected collapse hint `... 11 more lines`, got:\n%s", joined)
	}
	if !strings.Contains(stripANSI(joined), "ctrl+o to expand") {
		t.Errorf("expected expand-key hint, got:\n%s", joined)
	}

	// Expanded.
	b.SetExpanded(true)
	rowsExp := b.Render(80)
	joinedExp := strings.Join(rowsExp, "\n")
	if strings.Contains(joinedExp, "more lines") {
		t.Errorf("expanded should not show `more lines`, got:\n%s", joinedExp)
	}
	if !strings.Contains(stripANSI(joinedExp), "(ctrl+o to collapse)") {
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
			block := NewBashExecutionComponent("printf", nil, false, 1)
			block.AppendOutput(test.output)
			zero := 0
			block.SetComplete(&zero, false, nil, "")
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
	block := NewBashExecutionComponent("printf", nil, false, 1)
	block.AppendOutput("abcdefghijklmnopqrstuvwxyz")
	zero := 0
	block.SetComplete(&zero, false, nil, "")

	narrow := block.Render(12)
	wide := block.Render(22)
	if got := countRowsContaining(narrow, bashMutedColor()); got != 3 {
		t.Fatalf("narrow wrapped rows = %d, want 3: %#v", got, narrow)
	}
	if got := countRowsContaining(wide, bashMutedColor()); got != 2 {
		t.Fatalf("wide wrapped rows = %d, want 2: %#v", got, wide)
	}
}

// Pi: packages/coding-agent/src/modes/interactive/components/bash-execution.ts:70 (BashExecutionComponent.setExpanded).
func TestBashExecutionBlockCollapsedLimitUsesVisualRows(t *testing.T) {
	block := NewBashExecutionComponent("printf", nil, false, 1)
	block.AppendOutput(strings.Repeat("a", 50) + strings.Repeat("b", 200))
	zero := 0
	block.SetComplete(&zero, false, nil, "")

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
		block := NewBashExecutionComponent("printf", nil, exclude, 1)
		block.AppendOutput("abcdefghijklmnopqrstuvwxyz")
		zero := 0
		block.SetComplete(&zero, false, nil, "")
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

// bash-execution.ts:25-110: BashExecutionComponent extends Container: spacer, border, content container, border; the content
// container holds the header, the output, then the loader while running or the status rows after completion.
func TestBashExecutionComponentIsAContainerWithAContentContainer(t *testing.T) {
	b := NewBashExecutionComponent("echo hi", nil, false, 1)
	if got := len(b.Children()); got != 4 {
		t.Fatalf("children = %d, want spacer, border, content, border", got)
	}
	content := b.Children()[2].(*Container)
	if got := len(content.Children()); got != 2 {
		t.Fatalf("running content children = %d, want header and loader", got)
	}
	if content.Children()[1] != Component(b.Loader()) {
		t.Fatal("the loader is not the second content child")
	}
	b.AppendOutput("out\n")
	b.Render(40)
	one := 1
	b.SetComplete(&one, false, nil, "")
	if got := len(content.Children()); got != 3 {
		t.Fatalf("complete content children = %d, want header, output and status", got)
	}
}

// bash-execution.ts:166-186: every status part is colored by the theme (muted hint text around keyHint, warning for cancelled and the truncation notice, error for the exit status), not by fixed ANSI codes.
func TestBashExecutionStatusPartsUseThemeColors(t *testing.T) {
	theme := ActiveTheme()
	expand := theme.Fg("dim", AppKeyText("app.tools.expand", "ctrl+o"))
	three := 3
	tests := []struct {
		name     string
		output   string
		expanded bool
		exit     *int
		cancel   bool
		trunc    bool
		path     string
		want     []string
	}{
		{"collapsed hint", strings.Repeat("l\n", 24), false, nil, false, false, "", []string{theme.Fg("muted", "... 5 more lines (") + expand + theme.Fg("muted", " to expand") + theme.Fg("muted", ")")}},
		{"expanded hint", strings.Repeat("l\n", 24), true, nil, false, false, "", []string{theme.Fg("muted", "(") + expand + theme.Fg("muted", " to collapse") + theme.Fg("muted", ")")}},
		{"cancelled", "x", false, nil, true, false, "", []string{theme.Fg("warning", "(cancelled)")}},
		{"error exit", "x", false, &three, false, false, "", []string{theme.Fg("error", "(exit 3)")}},
		{"truncation notice", "x", false, nil, false, true, "/tmp/f.log", []string{theme.Fg("warning", "Output truncated. Full output: /tmp/f.log")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBashExecutionComponent("cmd", nil, false, 1)
			b.SetExpanded(tc.expanded)
			b.SetCompleteWithOutput(tc.exit, tc.cancel, tc.trunc, tc.output, tc.path)
			joined := strings.Join(b.Render(80), "\n")
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Errorf("missing %q in:\n%q", want, joined)
				}
			}
		})
	}
}

// bash-execution.ts:appendOutput strips ANSI codes and turns CRLF and CR into LF before it joins a chunk to the last line; getOutput joins the lines with LF and getCommand returns the command.
// Pi: packages/coding-agent/src/modes/interactive/components/bash-execution.ts:217 (BashExecutionComponent.getCommand).
func TestBashExecutionAppendOutputCleansChunksAndExposesOutputAndCommand(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"empty", nil, ""},
		{"plain", []string{"a\nb"}, "a\nb"},
		{"ansi stripped", []string{"\x1b[31mred\x1b[0m"}, "red"},
		{"crlf", []string{"a\r\nb"}, "a\nb"},
		{"lone cr", []string{"a\rb"}, "a\nb"},
		{"incomplete line continues in the next chunk", []string{"ab", "cd\ne"}, "abcd\ne"},
		{"trailing newline keeps the empty last line", []string{"x\n"}, "x\n"},
		{"cr split from its lf across chunks", []string{"a\r", "\nb"}, "a\n\nb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBashExecutionComponent("echo hi", nil, false, 1)
			for _, c := range tc.chunks {
				b.AppendOutput(c)
			}
			if got := b.GetOutput(); got != tc.want {
				t.Errorf("GetOutput() = %q, want %q", got, tc.want)
			}
			if got := b.GetCommand(); got != "echo hi" {
				t.Errorf("GetCommand() = %q", got)
			}
		})
	}
}

// bash-execution.ts setComplete(exitCode, cancelled, truncationResult, fullOutputPath): the fourth argument is the file with the untruncated
// output. It keeps the streamed output (unlike SetCompleteWithOutput) and shows the path in the truncation row only when the output was truncated.
func TestBashExecutionSetCompleteRecordsFullOutputPath(t *testing.T) {
	zero := 0
	render := func(truncated bool, path string) string {
		b := NewBashExecutionComponent("yes", nil, false, 1)
		b.AppendOutput("streamed tail\n")
		var result *TruncationResult
		if truncated {
			result = &TruncationResult{Truncated: true}
		}
		b.SetComplete(&zero, false, result, path)
		return strings.Join(plainLines(b.Render(80)), "\n")
	}
	got := render(true, "/tmp/full-output.log")
	if !strings.Contains(got, "streamed tail") || !strings.Contains(got, "Output truncated. Full output: /tmp/full-output.log") {
		t.Fatalf("truncated block with a path = %q", got)
	}
	for name, out := range map[string]string{"not truncated": render(false, "/tmp/full-output.log"), "no path": render(true, "")} {
		if strings.Contains(out, "Full output") {
			t.Fatalf("%s must not show the full-output row: %q", name, out)
		}
	}
}

// bash-execution.ts:199 wasTruncated = this.truncationResult?.truncated || contextTruncation.truncated: only a result that reports
// truncated adds the row; a nil result and a result with truncated false do not.
func TestBashExecutionSetCompleteReadsTheTruncatedFlagOfTheResult(t *testing.T) {
	zero := 0
	row := func(result *TruncationResult) bool {
		b := NewBashExecutionComponent("yes", nil, false, 1)
		b.AppendOutput("tail\n")
		b.SetComplete(&zero, false, result, "/tmp/full.log")
		return strings.Contains(strings.Join(plainLines(b.Render(80)), "\n"), "Output truncated. Full output: /tmp/full.log")
	}
	if !row(&TruncationResult{Truncated: true, TruncatedBy: "lines"}) {
		t.Fatal("a truncated result did not add the truncation row")
	}
	if row(nil) || row(&TruncationResult{Truncated: false, Content: "tail"}) {
		t.Fatal("a nil or untruncated result added the truncation row")
	}
}

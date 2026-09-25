package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Tool-execution Render emits top-pad + header + bottom-pad lines
// (matching upstream Box(paddingY=1)). Body lines added when expanded.
const (
	toolFrameMinLines = 4 // blank spacer + top-pad + header + bottom-pad
)

// toolHeader returns the painted header line (index 2: after leading spacer and top-pad).
func toolHeader(out []string) string {
	if len(out) < 3 {
		return ""
	}
	return out[2]
}

func TestToolExecutionReadErrorUsesToolOutputColor(t *testing.T) {
	// read.ts formatReadResult disables syntax highlighting on failure and
	// uses toolOutput, not the terminal default or the error foreground.
	c := NewToolExecutionComponent("read", "example.go")
	c.SetResult("Operation aborted", true, 0)
	want := ActiveTheme().ToolOutput + "Operation aborted" + SGRFgReset
	if got := c.renderResultBody(80); len(got) != 1 || got[0] != want {
		t.Fatalf("read error body = %q, want %q", got, want)
	}
}

func TestToolExecutionRunningHeader(t *testing.T) {
	c := NewToolExecutionComponent("read", HeaderForTool("read", json.RawMessage(`{"path":"README.md"}`), ""))
	out := c.Render(80)
	if len(out) != toolFrameMinLines {
		t.Fatalf("running with no output should be %d-line frame, got %d: %v", toolFrameMinLines, len(out), out)
	}
	h := toolHeader(out)
	// Upstream renders tool call headers without lifecycle markers.
	// The header carries the styled call (bold toolTitle name + accent path).
	if !strings.Contains(stripANSI(h), "read README.md") {
		t.Errorf("header missing call text: %q", stripANSI(h))
	}
	// The caller-supplied header styling (bold name) must be preserved.
	if !strings.Contains(h, "\033[1m") {
		t.Errorf("header should preserve bold styling: %q", h)
	}
	// Running state uses toolPendingBg.
	if !strings.HasPrefix(h, ToolPendingBgOpen()) {
		t.Errorf("running header should be painted toolPendingBg, got prefix %q", h[:min(len(h), 20)])
	}
}

// A BodyRenderer may return a single string carrying embedded newlines: the
// diff renderer's styleAndWrap joins wrapped visual lines with "\n". The
// framebuffer paints one string per terminal row, so each embedded row must be
// split into its own element and bg-painted, or the wrapped continuation
// renders at column 0 with no background (the visible gaps/fragments in a
// wrapped edit diff). Every Render() element must be exactly one visual row.
func TestToolExecutionBodyRendererFlattensEmbeddedNewlines(t *testing.T) {
	c := NewToolExecutionComponent("edit", "")
	c.SetResult("diff", false, 0)
	bgOpen := c.bgOpenSGR()
	// Mirror styleAndWrap output for a diff line wrapped into two visual rows.
	c.BodyRenderer = func(width int, expanded bool) []string {
		return []string{"\x1b[32mfirst visual row\x1b[0m\n\x1b[32mwrapped continuation\x1b[0m"}
	}
	out := c.Render(80)

	for i, line := range out {
		if strings.Contains(line, "\n") {
			t.Fatalf("Render()[%d] carries an embedded newline; body rows must be split before bg-paint: %q", i, line)
		}
	}
	var found bool
	for _, line := range out {
		if strings.Contains(line, "wrapped continuation") {
			found = true
			if bgOpen != "" && !strings.HasPrefix(line, bgOpen) {
				t.Fatalf("continuation row is not bg-painted (missing bg open %q): %q", bgOpen, line)
			}
		}
	}
	if !found {
		t.Fatalf("wrapped continuation row was dropped from Render output: %q", out)
	}
}

// A BodyRenderer that emits no lines (collapsed read card, or a
// renderShell:"self" extension renderer with empty output) must not leave a
// stray blank row inside the box: the header/body separator is skipped.
// Mirrors upstream #5299.
func TestToolExecutionEmptyBodyRendererSkipsSeparator(t *testing.T) {
	c := NewToolExecutionComponent("read", `path:"x"`)
	c.SetResult("some output", false, 0)
	c.BodyRenderer = func(width int, expanded bool) []string { return nil }
	c.Collapsed = true
	out := c.Render(80)
	if len(out) != toolFrameMinLines {
		t.Fatalf("empty body must render exactly %d frame lines (no separator), got %d: %q", toolFrameMinLines, len(out), out)
	}
	if !strings.Contains(toolHeader(out), `path:"x"`) {
		t.Errorf("header should still show the call: %q", toolHeader(out))
	}

	// Non-empty body keeps the separator (frame + separator + 1 body line).
	c.BodyRenderer = func(width int, expanded bool) []string { return []string{"body line"} }
	c.cachedLines = nil // bust render cache
	out = c.Render(80)
	if len(out) != toolFrameMinLines+2 {
		t.Fatalf("non-empty body should add separator + body line, got %d: %q", len(out), out)
	}
}

func TestToolExecutionDoneAutoCollapse(t *testing.T) {
	c := NewToolExecutionComponent("read", HeaderForTool("read", json.RawMessage(`{"path":"x"}`), ""))
	body := strings.Repeat("line\n", 20)
	c.SetResult(body, false, 1234*time.Millisecond)
	out := c.Render(80)
	// Registered fallback preview includes the leading lines and a more-lines hint.
	if len(out) <= toolFrameMinLines {
		t.Fatalf("collapsed should include preview, got only %d lines", len(out))
	}
	h := toolHeader(out)
	// Upstream headers: no ✓ marker, no line count, no elapsed, no ▸ hint.
	// Header carries the styled call text.
	if !strings.Contains(stripANSI(h), "read x") {
		t.Errorf("call text missing: %q", stripANSI(h))
	}
	if !strings.Contains(h, "\033[1m") {
		t.Errorf("header should preserve bold styling: %q", h)
	}
	// Done state uses toolSuccessBg.
	if !strings.HasPrefix(h, ToolSuccessBgOpen()) {
		t.Errorf("done header should be painted toolSuccessBg")
	}
	// Preview should include the expand hint.
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "more lines") {
		t.Errorf("collapsed preview should include truncation hint: %v", out)
	}
	if !strings.Contains(joined, "ctrl+o") {
		t.Errorf("collapsed preview should include ctrl+o hint: %v", out)
	}
}

func TestToolExecutionShortOutputStaysExpanded(t *testing.T) {
	c := NewToolExecutionComponent("read", "")
	c.SetResult("hi\nthere\n", false, 0)
	out := c.Render(80)
	// 2 body lines + 1 header = 3
	if len(out) < toolFrameMinLines+2 {
		t.Fatalf("short output should auto-expand, got %d lines: %v", len(out), out)
	}
}

func TestToolExecutionErrorAutoExpands(t *testing.T) {
	c := NewToolExecutionComponent("read", HeaderForTool("read", json.RawMessage(`{"path":"missing"}`), ""))
	long := strings.Repeat("err\n", 50)
	c.SetResult(long, true, 0)
	out := c.Render(80)
	if len(out) < toolFrameMinLines+2 {
		t.Fatalf("error should auto-expand long output, got %d lines", len(out))
	}
	h := toolHeader(out)
	// Upstream: no ✗ marker in header. State conveyed by bg color only.
	if !strings.Contains(stripANSI(h), "read missing") {
		t.Errorf("call text missing: %q", stripANSI(h))
	}
	// Error state uses toolErrorBg.
	if !strings.HasPrefix(h, ToolErrorBgOpen()) {
		t.Errorf("error header should be painted toolErrorBg")
	}
	// Caller-supplied header styling (bold name) is preserved.
	if !strings.Contains(h, "\033[1m") {
		t.Errorf("header should preserve bold styling: %q", h)
	}
}

func TestToolExecutionToggleSticky(t *testing.T) {
	c := NewToolExecutionComponent("bash", "")
	c.SetResult(strings.Repeat("a\n", 50), false, 0)
	if !c.Collapsed {
		t.Fatal("should auto-collapse")
	}
	c.Toggle()
	if c.Collapsed {
		t.Fatal("Toggle should expand")
	}
	c.SetResult(strings.Repeat("a\n", 50), false, 0)
	if c.Collapsed {
		t.Fatal("user toggle should be sticky across SetResult")
	}
}

func TestToolExecutionSetExpanded(t *testing.T) {
	c := NewToolExecutionComponent("bash", "")
	c.SetResult(strings.Repeat("a\n", 50), false, 0)
	if !c.Collapsed {
		t.Fatal("should auto-collapse")
	}
	c.SetExpanded(true)
	if c.Collapsed {
		t.Fatal("SetExpanded(true) should open")
	}
	c.SetExpanded(false)
	if !c.Collapsed {
		t.Fatal("SetExpanded(false) should close")
	}
	c.SetExpanded(true)
	c.SetResult(strings.Repeat("a\n", 50), false, 0)
	if c.Collapsed {
		t.Fatal("SetExpanded should be sticky across SetResult")
	}
}

func TestToolExecutionBodyTruncation(t *testing.T) {
	c := NewToolExecutionComponent("bash", "")
	c.BodyMaxLines = 5
	c.SetResult(strings.Repeat("x\n", 100), false, 0)
	c.Expand()
	out := c.Render(80)
	// frame(blank+top-pad+header=3) + separator(1) + 5 body + 1 truncation footer + bottom-pad(1) = 11
	if len(out) != 11 {
		t.Fatalf("expected 11 lines, got %d: %v", len(out), out)
	}
	// truncation footer is second-to-last (last is bottom padding).
	footer := out[len(out)-2]
	if !strings.Contains(footer, "95 earlier lines") {
		t.Errorf("truncation footer wrong: %q", footer)
	}
}

// TestToolExecutionBodyDefaultUnlimited verifies the default behavior
// (no BodyMaxLines set) emits every line of output: fixes the bug
// where Ctrl+O claimed to "expand" but the body still capped at 40.
func TestToolExecutionBodyDefaultUnlimited(t *testing.T) {
	c := NewToolExecutionComponent("bash", "")
	c.SetResult(strings.Repeat("line\n", 245), false, 0)
	c.Expand()
	out := c.Render(80)
	// frame(blank+top-pad+header=3) + separator(1) + 245 body + bottom-pad(1) = 250
	if len(out) != 250 {
		t.Fatalf("expected 250 lines (frame + separator + 245 body + pads), got %d", len(out))
	}
	for _, l := range out {
		if strings.Contains(l, "suppressed by hard cap") {
			t.Errorf("unexpected truncation footer: %q", l)
		}
	}
}

// TestToolExecutionBgPaintedEdgeToEdge confirms every rendered line is
// painted edge-to-edge in the lifecycle bg: closes collapsed-state
// requirement (header keeps frame even when body is hidden).
func TestToolExecutionBgPaintedEdgeToEdge(t *testing.T) {
	c := NewToolExecutionComponent("read", "")
	c.SetResult(strings.Repeat("ok\n", 20), false, 0) // auto-collapses
	out := c.Render(80)
	for i, line := range out {
		if line == "" {
			continue // blank spacer line (upstream Spacer(1)): no bg paint
		}
		if !strings.HasPrefix(line, ToolSuccessBgOpen()) {
			t.Errorf("line %d not painted: %q", i, line)
		}
		if !strings.HasSuffix(line, BgClose()) {
			t.Errorf("line %d not closed: %q", i, line)
		}
	}
}

func TestToolExecutionStreamingFallbackUsesLeadingPreview(t *testing.T) {
	for _, toolName := range []string{"bash", "extension_tool"} {
		t.Run(toolName, func(t *testing.T) {
			c := NewToolExecutionComponent(toolName, "long-running call")

			var output strings.Builder
			for i := range 100 {
				fmt.Fprintf(&output, "streamed row %d\n", i)
			}
			c.SetStreaming(output.String())

			if !c.Collapsed {
				t.Fatal("new tool output should start collapsed")
			}
			rendered := stripANSI(strings.Join(c.Render(80), "\n"))
			if !strings.Contains(rendered, "streamed row 0") || !strings.Contains(rendered, "streamed row 9") {
				t.Fatalf("fallback preview omitted leading output rows:\n%s", rendered)
			}
			if strings.Contains(rendered, "streamed row 99") {
				t.Fatalf("fallback preview exposed rows after its ten-line limit:\n%s", rendered)
			}
			if !strings.Contains(rendered, "90 more lines") || !strings.Contains(rendered, "ctrl+o to expand") {
				t.Fatalf("fallback preview omitted its truncation hint:\n%s", rendered)
			}
		})
	}
}

func TestToolExecutionStreamingExpansionStaysStickyThroughCompletion(t *testing.T) {
	for _, toolName := range []string{"bash", "extension_tool"} {
		t.Run(toolName, func(t *testing.T) {
			c := NewToolExecutionComponent(toolName, "long-running call")
			c.SetStreaming("first\nsecond\n")
			c.Toggle()
			if c.Collapsed {
				t.Fatal("Ctrl+O should expand streaming output")
			}

			c.SetStreaming("first\nsecond\nthird\n")
			c.SetResult("first\nsecond\nthird\n", false, time.Second)
			if c.Collapsed {
				t.Fatal("explicit streaming expansion should remain sticky through updates and completion")
			}
		})
	}
}

// TestToolExecutionTabsReplacedInBgPaint confirms that tab characters in
// tool output are replaced with spaces before bg-painting. Terminals don't
// reliably paint background color through tab stops, causing visible gaps
// in the tinted tool box (e.g. grep -n output with indented source code).
func TestToolExecutionTabsReplacedInBgPaint(t *testing.T) {
	c := NewToolExecutionComponent("bash", "grep -n foo bar.ts")
	c.MarkExecutionStarted()
	// Simulate grep -n output with literal tabs.
	c.SetStreaming("105-\tprivate getRenderShell(): \"default\" {\n106-\t\tif (!this.builtIn) {\n107:\t\t\treturn this.def;\n")

	out := c.Render(80)
	for i, line := range out {
		if line == "" {
			continue
		}
		if strings.Contains(line, "\t") {
			t.Errorf("line %d contains literal tab (causes bg paint gaps): %q", i, line)
		}
	}
}

func TestGenericExtensionToolCollapsedDetailsScaleWithWidth(t *testing.T) {
	args := json.RawMessage(`{"find":"alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima","replace":"one two three four five six seven eight nine ten","nested":{"enabled":true,"count":12}}`)
	c := NewToolExecutionComponent("edit_spec", "")
	c.SetStructuredArgs(args)
	c.SetResult("updated", false, 0)

	narrow := stripANSI(strings.Join(c.Render(52), "\n"))
	wide := stripANSI(strings.Join(c.Render(100), "\n"))
	if !strings.Contains(narrow, "ctrl+o to expand") || !strings.Contains(narrow, "…") {
		t.Fatalf("narrow collapsed details must mark hidden arguments as recoverable:\n%s", narrow)
	}
	if strings.Contains(narrow, "juliet kilo lima") {
		t.Fatalf("narrow collapsed details unexpectedly contain the full long argument:\n%s", narrow)
	}
	if !strings.Contains(wide, "foxtrot golf") {
		t.Fatalf("wide collapsed details should reveal more retained argument data:\n%s", wide)
	}
	if len(wide) <= len(narrow) {
		t.Fatalf("wide collapsed details did not use the larger terminal budget:\nnarrow: %s\nwide: %s", narrow, wide)
	}
}

func TestGenericExtensionToolExpandedDetailsShowCompleteInputAndOutput(t *testing.T) {
	args := json.RawMessage(`{"find":"ALPHA_BRAVO_CHARLIE_DELTA_ECHO_FOXTROT","replace":"ONE_TWO_THREE_FOUR_FIVE_SIX_SEVEN","nested":{"enabled":true,"count":12}}`)
	c := NewToolExecutionComponent("edit_spec", "")
	c.SetStructuredArgs(args)
	c.SetResult("RESULT_FIRST\nRESULT_LAST\n[source truncated: 2 records unavailable]", false, 0)
	c.SetExpanded(true)

	rows := c.Render(34)
	rendered := stripANSI(strings.Join(rows, "\n"))
	compactRendered := strings.NewReplacer("\n", "", " ", "").Replace(rendered)
	for _, want := range []string{
		"ALPHA_BRAVO_CHARLIE_DELTA_ECHO_FOXTROT",
		"ONE_TWO_THREE_FOUR_FIVE_SIX_SEVEN",
		`"enabled":true`,
		`"count":12`,
		"RESULT_FIRST",
		"RESULT_LAST",
		"[sourcetruncated:2recordsunavailable]",
	} {
		if !strings.Contains(compactRendered, want) {
			t.Fatalf("expanded tool details lost %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "ctrl+o to expand") {
		t.Fatalf("expanded details still claim content is hidden:\n%s", rendered)
	}
	firstExpansion := strings.Join(rows, "\n")
	c.SetExpanded(false)
	c.SetExpanded(true)
	if secondExpansion := strings.Join(c.Render(34), "\n"); secondExpansion != firstExpansion {
		t.Fatal("collapse and re-expansion changed retained tool details")
	}
}

func TestGenericExtensionToolStructuredArgsReflowAfterResize(t *testing.T) {
	args := json.RawMessage(`{"message":"RESIZE_ALPHA_BRAVO_CHARLIE_DELTA_ECHO_FOXTROT_GOLF"}`)
	c := NewToolExecutionComponent("extension_tool", "")
	c.SetStructuredArgs(args)
	c.SetExpanded(true)

	narrow := c.Render(24)
	wide := c.Render(72)
	if len(narrow) <= len(wide) {
		t.Fatalf("narrow expanded details should wrap to more rows: narrow=%d wide=%d", len(narrow), len(wide))
	}
	for _, rows := range [][]string{narrow, wide} {
		joined := strings.NewReplacer("\n", "", " ", "").Replace(stripANSI(strings.Join(rows, "\n")))
		if !strings.Contains(joined, "RESIZE_ALPHA_BRAVO_CHARLIE_DELTA_ECHO_FOXTROT_GOLF") {
			t.Fatalf("resize discarded retained argument data:\n%s", joined)
		}
	}
}

func TestFormatToolArgsObject(t *testing.T) {
	got := FormatToolArgs(json.RawMessage(`{"path":"README.md","limit":10}`))
	want := `limit:10, path:"README.md"`
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestFormatToolArgsTruncatesLongStrings(t *testing.T) {
	long := strings.Repeat("x", 100)
	raw := json.RawMessage(`{"command":"` + long + `"}`)
	got := FormatToolArgs(raw)
	if !strings.Contains(got, "…") {
		t.Errorf("should truncate, got %q", got)
	}
	if len(got) > 90 {
		t.Errorf("overall preview should be capped, got len=%d: %q", len(got), got)
	}
}

func TestFormatToolArgsEmpty(t *testing.T) {
	if got := FormatToolArgs(nil); got != "" {
		t.Errorf("nil → empty, got %q", got)
	}
	if got := FormatToolArgs(json.RawMessage(`{}`)); got != "" {
		t.Errorf("empty obj → empty, got %q", got)
	}
}

func TestFormatToolArgsArrayShape(t *testing.T) {
	got := FormatToolArgs(json.RawMessage(`{"items":[1,2,3]}`))
	if !strings.Contains(got, "[3]") {
		t.Errorf("array should render as [N], got %q", got)
	}
}

func TestStripControlEscapesPreservesSGR(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello", "hello"},
		{"sgr-kept", "\x1b[31mred\x1b[0m text", "\x1b[31mred\x1b[0m text"},
		{"clear-screen-stripped", "\x1b[2Jhi", "hi"},
		{"cursor-home-stripped", "\x1b[Hhi", "hi"},
		{"cursor-pos-stripped", "\x1b[5;10Hhi", "hi"},
		{"hide-cursor-stripped", "\x1b[?25lhi\x1b[?25h", "hi"},
		{"erase-line-stripped", "\x1b[Khi\x1b[2K", "hi"},
		{"osc-stripped", "\x1b]0;title\x07hi", "hi"},
		{"alt-screen-stripped", "\x1b[?1049h hi \x1b[?1049l", " hi "},
		{"bell-stripped", "hi\x07", "hi"},
		{"newlines-kept", "a\nb\tc\rd", "a\nb\tc\rd"},
		{"sgr-around-cursor-mix", "\x1b[31m\x1b[2Jboom\x1b[0m", "\x1b[31mboom\x1b[0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripControlEscapes(tc.in); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestRenderBodyStripsControlsBeforeRender simulates the rocket-script
// disaster: bash output contains a `\x1b[2J` (clear) and `\x1b[5;5H`
// (cursor jump). The rendered body must NOT contain either escape.
func TestRenderBodyStripsControlsBeforeRender(t *testing.T) {
	c := NewToolExecutionComponent("bash", "")
	c.SetResult("\x1b[2J\x1b[Hhello\nworld\n", false, 0)
	c.Expand()
	out := strings.Join(c.Render(80), "\n")
	if strings.Contains(out, "\x1b[2J") || strings.Contains(out, "\x1b[H") {
		t.Fatalf("control escapes leaked into rendered output: %q", out)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("text content lost during sanitization: %q", out)
	}
}

func TestToolExecutionImageBlocks(t *testing.T) {
	SetCapabilities(TerminalCapabilities{})
	defer ResetCapabilitiesCache()
	c := NewToolExecutionComponent("read", `path:"test.png"`)
	c.ImageBlocks = []ImageBlock{
		{Data: "iVBORw0KGgoAAAANSUhEUg==", MIMEType: "image/png"},
	}
	c.ShowImages = true
	c.SetResult("image file read", false, 0)

	lines := c.Render(80)
	// Should include image fallback text (no real image protocol in test)
	found := false
	for _, l := range lines {
		if strings.Contains(l, "image/png") || strings.Contains(l, "Image") || strings.Contains(l, "📷") {
			found = true
			break
		}
	}
	if !found {
		t.Log("Lines rendered:")
		for i, l := range lines {
			t.Logf("  [%d] %q", i, l)
		}
		t.Error("expected image fallback text in rendered output")
	}
}

func TestToolExecutionImageHiddenWhenShowImagesFalse(t *testing.T) {
	c := NewToolExecutionComponent("read", `path:"test.png"`)
	c.ImageBlocks = []ImageBlock{
		{Data: "iVBORw0KGgoAAAANSUhEUg==", MIMEType: "image/png"},
	}
	c.ShowImages = false
	c.SetResult("ok", false, 0)

	lines := c.Render(80)
	for _, l := range lines {
		if strings.Contains(l, "image/png") || strings.Contains(l, "📷") {
			t.Error("should not render images when ShowImages=false")
			break
		}
	}
}

func TestFormatReadHeader(t *testing.T) {
	got := FormatReadHeader(json.RawMessage(`{"path":"README.md"}`), "")
	if stripANSI(got) != "read README.md" {
		t.Errorf("got %q want %q", stripANSI(got), "read README.md")
	}
	if !strings.Contains(got, "\x1b") {
		t.Errorf("header should be ANSI-styled (toolTitle name + accent path): %q", got)
	}
}

func TestFormatReadHeaderWithOffset(t *testing.T) {
	got := stripANSI(FormatReadHeader(json.RawMessage(`{"path":"file.go","offset":10,"limit":20}`), ""))
	if got != "read file.go:10-29" {
		t.Errorf("got %q want %q", got, "read file.go:10-29")
	}
}

func TestFormatWriteHeader(t *testing.T) {
	got := stripANSI(FormatWriteHeader(json.RawMessage(`{"path":"output.txt","content":"hello"}`), ""))
	if got != "write output.txt" {
		t.Errorf("got %q want %q", got, "write output.txt")
	}
}

func TestFormatEditHeader(t *testing.T) {
	got := stripANSI(FormatEditHeader(json.RawMessage(`{"path":"file.txt","oldText":"old","newText":"new"}`), ""))
	if got != "edit file.txt" {
		t.Errorf("got %q want %q", got, "edit file.txt")
	}
}

func TestFormatGrepHeader(t *testing.T) {
	got := stripANSI(FormatGrepHeader(json.RawMessage(`{"pattern":"TODO","path":"src/"}`)))
	if got != "grep /TODO/ in src/" {
		t.Errorf("got %q want %q", got, "grep /TODO/ in src/")
	}
}

func TestFormatGrepHeaderDefaultPath(t *testing.T) {
	got := stripANSI(FormatGrepHeader(json.RawMessage(`{"pattern":"TODO"}`)))
	if got != "grep /TODO/ in ." {
		t.Errorf("got %q want %q", got, "grep /TODO/ in .")
	}
}

func TestFormatFindHeader(t *testing.T) {
	got := stripANSI(FormatFindHeader(json.RawMessage(`{"pattern":"*.go","path":"src/"}`)))
	if got != "find *.go in src/" {
		t.Errorf("got %q want %q", got, "find *.go in src/")
	}
}

func TestFormatLsHeader(t *testing.T) {
	got := stripANSI(FormatLsHeader(json.RawMessage(`{"path":"src/"}`), ""))
	if got != "ls src/" {
		t.Errorf("got %q want %q", got, "ls src/")
	}
}

func TestFormatLsHeaderDefaultPath(t *testing.T) {
	got := stripANSI(FormatLsHeader(json.RawMessage(`{}`), ""))
	if got != "ls ." {
		t.Errorf("got %q want %q", got, "ls .")
	}
}

// TestToolExecutionNamePreservedForFallback verifies that NewToolExecutionComponent
// stores the tool name in comp.Name so that resumed sessions can fall back to
// comp.Name when the JSONL tool_result entry has no ToolName field.
// (Bug: tool_result entries don't store ToolName; fix uses comp.Name as fallback.)
func TestToolExecutionNamePreservedForFallback(t *testing.T) {
	const toolName = "bash"
	c := NewToolExecutionComponent(toolName, "$ ls")
	if c.Name != toolName {
		t.Fatalf("Name = %q, want %q", c.Name, toolName)
	}
	// Simulate what the resume path does: look up comp by id, read comp.Name.
	// After SetResult the component should still carry the original name so
	// toolBodyRenderer can be called with it.
	c.SetResult("file.go\n", false, 0)
	if c.Name != toolName {
		t.Fatalf("Name changed after SetResult: got %q, want %q", c.Name, toolName)
	}
	// The bug manifested as BodyRenderer being nil when toolName was empty.
	// Assign a sentinel BodyRenderer to prove the slot is available.
	sentinelCalled := false
	c.BodyRenderer = func(width int, expanded bool) []string {
		sentinelCalled = true
		return []string{"rendered"}
	}
	out := c.Render(80)
	if !sentinelCalled {
		t.Fatal("BodyRenderer was not called during Render")
	}
	found := false
	for _, l := range out {
		if strings.Contains(l, "rendered") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("BodyRenderer output not present in rendered lines: %v", out)
	}
}

func TestFormatBuiltinToolHeader(t *testing.T) {
	cases := []struct {
		tool string
		args string
		want string
	}{
		{"bash", `{"command":"echo hello"}`, "$ echo hello"},
		{"read", `{"path":"file.txt"}`, "read file.txt"},
		{"write", `{"path":"out.txt"}`, "write out.txt"},
		{"edit", `{"path":"f.go"}`, "edit f.go"},
		{"grep", `{"pattern":"x","path":"./"}`, "grep /x/ in ./"},
		{"find", `{"pattern":"*.md"}`, "find *.md in ."},
		{"ls", `{"path":"/tmp"}`, "ls /tmp"},
		{"unknown_tool", `{"x":1}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			got := stripANSI(FormatBuiltinToolHeader(tc.tool, json.RawMessage(tc.args), ""))
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestToolExecution_BodyRendererDefaultsCollapsed(t *testing.T) {
	c := NewToolExecutionComponent("write", "test_args")
	c.BodyRenderer = func(width int, expanded bool) []string {
		if expanded {
			return []string{"line1", "line2", "line3", "line4", "line5"}
		}
		return []string{"line1", "... (4 more, ctrl+o to expand)"}
	}
	c.SetResult("File created (100 bytes)", false, 0)
	if !c.Collapsed {
		t.Fatal("expected Collapsed=true for tool with BodyRenderer")
	}
	lines := c.Render(80)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "ctrl+o") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ctrl+o hint in preview mode, got:\n%s", strings.Join(lines, "\n"))
	}
}

func TestToolExecution_UpdateArgs(t *testing.T) {
	c := NewToolExecutionComponent("edit", "")

	// Before any args: header shows tool name.
	lines := c.Render(80)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "edit") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected 'edit' in header before args")
	}

	// Partial JSON: incomplete, should not crash, header stays as tool name.
	c.UpdateArgs("edit", `{"path": "main`)
	lines = c.Render(80)
	// Should still render without panic.
	if len(lines) == 0 {
		t.Fatal("expected non-empty render after partial args")
	}

	// Complete JSON: header should update to "edit main.go".
	c.UpdateArgs("edit", `{"path": "main.go"}`)
	lines = c.Render(80)
	found = false
	for _, line := range lines {
		if strings.Contains(line, "main.go") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'main.go' in header after complete args, got:\n%s", strings.Join(lines, "\n"))
	}
}

func TestToolExecution_MarkExecutionStarted(t *testing.T) {
	c := NewToolExecutionComponent("bash", "$ echo hello")

	if c.State != ToolStateRunning {
		t.Fatal("expected Running state")
	}

	c.MarkExecutionStarted()

	// Should still render in running state.
	lines := c.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected non-empty render after MarkExecutionStarted")
	}
}

func TestToolExecution_IsPartialBgTransition(t *testing.T) {
	c := NewToolExecutionComponent("bash", "$ echo hello")

	// Initially IsPartial is true: should use pending bg.
	if !c.IsPartial {
		t.Fatal("expected IsPartial=true initially")
	}

	lines1 := c.Render(80)

	// After SetResult, IsPartial becomes false: bg should change.
	c.SetResult("hello", false, 0)

	if c.IsPartial {
		t.Fatal("expected IsPartial=false after SetResult")
	}

	lines2 := c.Render(80)

	// The bg should differ (pending vs success).
	if len(lines1) > 1 && len(lines2) > 1 && lines1[1] == lines2[1] {
		t.Error("expected different bg between partial and complete states")
	}
}

func TestFormatEditHeaderShowsPatchFiles(t *testing.T) {
	header := FormatEditHeader(json.RawMessage(`{"patch":"*** Begin Patch\n*** Update File: tui/a.go\n*** Add File: tui/b.go\n*** End Patch"}`), "/workspace")
	plain := stripANSI(header)
	if !strings.Contains(plain, "tui/a.go") || !strings.Contains(plain, "+1 file") {
		t.Fatalf("patch edit header does not identify affected files: %q", plain)
	}
}

func TestFormatEditHeaderShowsMultipleFiles(t *testing.T) {
	header := FormatEditHeader(json.RawMessage(`{"multi":[{"path":"parity/testdata/provider.ts"},{"path":"ai/test_faux.go"}]}`), "/workspace")
	plain := stripANSI(header)
	if !strings.Contains(plain, "parity/testdata/provider.ts") || !strings.Contains(plain, "+1 file") {
		t.Fatalf("multi-file edit header does not identify affected files: %q", plain)
	}
}

func TestToolExecution_SetArgsComplete(t *testing.T) {
	c := NewToolExecutionComponent("edit", "edit main.go")

	c.SetArgsComplete()

	// Should still render normally.
	lines := c.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected non-empty render after SetArgsComplete")
	}
}

// TestToolExecutionCollapsedPreview verifies that a registered tool whose
// definition has no result renderer shows upstream's first ten lines and a
// "more lines" hint.
func TestToolExecutionCollapsedPreview(t *testing.T) {
	c := NewToolExecutionComponent("read_session", "mode:toc")
	c.SetStructuredArgs(json.RawMessage(`{"mode":"toc"}`))
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("output line %d", i+1)
	}
	c.SetResult(strings.Join(lines, "\n"), false, 0)

	if !c.Collapsed {
		t.Fatal("registered generic tool should start collapsed")
	}

	out := c.Render(80)
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "20 more lines") || !strings.Contains(joined, "ctrl+o to expand") {
		t.Errorf("expected upstream fallback hint, got:\n%s", joined)
	}
	for i := 1; i <= 10; i++ {
		want := fmt.Sprintf("output line %d", i)
		if !strings.Contains(joined, want) {
			t.Errorf("missing leading preview line %q", want)
		}
	}
	for _, i := range []int{11, 26, 30} {
		notWant := fmt.Sprintf("output line %d", i)
		if strings.Contains(joined, notWant) {
			t.Errorf("collapsed preview unexpectedly contains %q", notWant)
		}
	}

	for i, line := range out {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, ToolSuccessBgOpen()) {
			t.Errorf("preview line %d not bg-painted: %q", i, line)
		}
	}
}

func TestToolExecutionWithoutDefinitionShowsFullResult(t *testing.T) {
	c := NewToolExecutionComponent("unknown_tool", "")
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("output line %d", i+1)
	}
	c.SetResult(strings.Join(lines, "\n"), false, 0)
	if c.Collapsed {
		t.Fatal("tool without a definition must use the full fallback")
	}
	joined := strings.Join(c.Render(80), "\n")
	for _, want := range []string{"output line 1", "output line 30"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("full fallback missing %q:\n%s", want, joined)
		}
	}
}

// TestToolExecutionCollapsedPreviewShort verifies that output within the
// ten-line fallback limit has no truncation hint.
func TestToolExecutionCollapsedPreviewShort(t *testing.T) {
	c := NewToolExecutionComponent("custom_tool", "args")
	// 3 lines: below threshold for collapsing but force it.
	c.SetResult("a\nb\nc\n", false, 0)
	// Force collapse (would normally auto-expand for < 8 lines).
	c.Collapse()

	out := c.Render(80)
	joined := strings.Join(out, "\n")

	if strings.Contains(joined, "earlier line") {
		t.Errorf("short output should not show truncation hint: %s", joined)
	}
	// Should contain all 3 lines.
	for _, want := range []string{"a", "b", "c"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in preview", want)
		}
	}
}

// D-G: while a bash tool runs, the component must show a live "Elapsed X.Xs"
// footer (mirrors upstream bash.ts renderResult). Non-bash tools and completed
// tools must not show it. The line cache must not freeze the value.
func TestToolExecutionComponent_LiveBashElapsed(t *testing.T) {
	c := NewToolExecutionComponent("bash", "$ sleep 5")
	c.MarkExecutionStarted()
	c.StartedAt = time.Now().Add(-3200 * time.Millisecond)

	out := strings.Join(c.Render(80), "\n")
	if !strings.Contains(out, "Elapsed 3.2s") {
		t.Fatalf("running bash should show live Elapsed; got:\n%s", out)
	}

	// Cache must not freeze the elapsed: advancing the clock changes it.
	c.StartedAt = time.Now().Add(-9900 * time.Millisecond)
	out2 := strings.Join(c.Render(80), "\n")
	if !strings.Contains(out2, "Elapsed 9.9s") {
		t.Fatalf("elapsed must advance (cache bypass) ; got:\n%s", out2)
	}

	// Non-bash running tool: no Elapsed footer.
	r := NewToolExecutionComponent("read", "read x.go")
	r.MarkExecutionStarted()
	r.StartedAt = time.Now().Add(-2 * time.Second)
	if got := strings.Join(r.Render(80), "\n"); strings.Contains(got, "Elapsed") {
		t.Fatalf("non-bash tool must not show Elapsed; got:\n%s", got)
	}

	// Completed bash: no live Elapsed (the "Took" footer is the BodyRenderer's job).
	c.SetResult("done", false, 4*time.Second)
	if got := strings.Join(c.Render(80), "\n"); strings.Contains(got, "Elapsed") {
		t.Fatalf("completed bash must not show live Elapsed; got:\n%s", got)
	}
}

// TestToolExecutionComponent_FinalizeAbortedFreezesElapsed verifies that a
// bash tool aborted mid-run stops recomputing its live "Elapsed X.Xs" footer
// (the runaway timer that forced a repaint on every keystroke/agent chunk and
// broke scrollback), keeps its partial output, and is a no-op once terminal.
func TestToolExecutionComponent_FinalizeAbortedFreezesElapsed(t *testing.T) {
	c := NewToolExecutionComponent("bash", "echo hi")
	c.MarkExecutionStarted()
	c.StartedAt = time.Now().Add(-2 * time.Second) // backdate for a measurable elapsed
	c.SetStreaming("partial output")
	if c.State != ToolStateRunning {
		t.Fatal("precondition: bash tool should be Running")
	}
	if c.runningElapsedLine() == "" {
		t.Fatal("precondition: a running bash must show a live Elapsed line")
	}

	c.FinalizeAborted(2 * time.Second)
	if c.State == ToolStateRunning {
		t.Error("FinalizeAborted must transition the tool out of Running")
	}
	if c.runningElapsedLine() != "" {
		t.Error("after FinalizeAborted the live Elapsed line must stop recomputing")
	}
	if got := strings.Join(c.Render(80), "\n"); !strings.Contains(got, "partial output") {
		t.Errorf("partial streamed output should be preserved, got:\n%s", got)
	}

	// No-op once terminal: a later FinalizeAborted must not overwrite Elapsed.
	c.FinalizeAborted(99 * time.Second)
	if c.Elapsed != 2*time.Second {
		t.Errorf("FinalizeAborted on a terminal tool must be a no-op; elapsed=%v", c.Elapsed)
	}
}

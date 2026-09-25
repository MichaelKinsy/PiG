package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// Mirrors upstream formatGrepResult: the first 15 lines while collapsed with
// a "more lines" hint, every line when expanded, then the warning row built
// from matchLimitReached, the byte truncation, and linesTruncated.
func TestGrepBodyRenderer(t *testing.T) {
	th := tui.ActiveTheme()
	out := func(s string) string { return th.ToolOutput + s + tui.SGRFgReset }
	muted := func(s string) string { return th.Muted + s + tui.SGRFgReset }
	var content strings.Builder
	var lines []string
	for i := 1; i <= 17; i++ {
		fmt.Fprintf(&content, "f.go:%d: hit\n", i)
		lines = append(lines, out(fmt.Sprintf("f.go:%d: hit", i)))
	}
	details := &tools.GrepDetails{MatchLimitReached: 100, LinesTruncated: true,
		Truncation: &tools.TruncationResult{Truncated: true}}
	warning := th.Warning + "[Truncated: 100 matches limit, 50.0KB limit, some lines truncated]" + tui.SGRFgReset
	r := toolBodyRenderer("grep", agent.AgentToolResult{Content: content.String(), Details: details}, nil)

	// Upstream opens the muted hint before its newline, so the color code
	// trails the last preview line (wrapTextWithAnsi keeps it on that row).
	hint := muted("... (2 more lines,") + " " + th.Dim + "ctrl+o" + tui.SGRFgReset + muted(" to expand") + muted(")")
	preview := append([]string(nil), lines[:15]...)
	preview[14] += th.Muted
	collapsed := append(shellRows(90, preview...), shellRows(90, hint, warning)...)
	assertRows(t, r(90, false), collapsed)
	assertRows(t, r(90, true), append(shellRows(90, lines...), shellRows(90, warning)...))
}

// find and ls show 20 lines while collapsed; their warnings name the result
// and entry limits. Persisted results carry the upstream JSON shape.
func TestFindAndLsBodyRenderers(t *testing.T) {
	th := tui.ActiveTheme()
	out := func(s string) string { return th.ToolOutput + s + tui.SGRFgReset }
	muted := func(s string) string { return th.Muted + s + tui.SGRFgReset }
	var content strings.Builder
	var lines []string
	for i := 1; i <= 21; i++ {
		fmt.Fprintf(&content, "  entry%d\n", i)
		lines = append(lines, out(fmt.Sprintf("entry%d", i)))
	}
	// The JS trim strips the first line's indentation only.
	lines[0] = out("entry1")
	for i := 1; i < len(lines); i++ {
		lines[i] = out(fmt.Sprintf("  entry%d", i+1))
	}
	hint := muted("... (1 more lines,") + " " + th.Dim + "ctrl+o" + tui.SGRFgReset + muted(" to expand") + muted(")")

	find := toolBodyRenderer("find", agent.AgentToolResult{Content: content.String(), Details: &tools.FindDetails{ResultLimitReached: 1000}}, nil)
	preview := append([]string(nil), lines[:20]...)
	preview[19] += th.Muted
	assertRows(t, find(60, false), append(shellRows(60, preview...),
		shellRows(60, hint, th.Warning+"[Truncated: 1000 results limit]"+tui.SGRFgReset)...))

	persisted := map[string]any{"entryLimitReached": float64(500), "truncation": map[string]any{"truncated": true, "maxBytes": float64(1024)}}
	ls := toolBodyRenderer("ls", agent.AgentToolResult{Content: content.String(), Details: persisted}, nil)
	assertRows(t, ls(60, true), append(shellRows(60, lines...),
		shellRows(60, th.Warning+"[Truncated: 500 entries limit, 1.0KB limit]"+tui.SGRFgReset)...))

	// No output and no warnings renders no body at all.
	if got := toolBodyRenderer("ls", agent.AgentToolResult{Content: "  \n"}, nil)(60, false); len(got) != 0 {
		t.Fatalf("empty ls body = %q", got)
	}
}

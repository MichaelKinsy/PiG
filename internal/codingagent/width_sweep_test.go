package codingagent

// Width sweep for coding-agent rows: footer/status line and tool result
// bodies (success, error, diff) rendered with adversarial width content at
// every width 20..200 must never emit a row wider than the width, measured
// with widthx.VisibleWidth (upstream visibleWidth exactly).

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var sweepText = []string{
	"👨‍👩‍👧‍👦🏳️‍🌈🇺🇸1️⃣⚠️❤️‍🔥👍🏽 日本語のテキスト한국어 ｈａｌｆ ｶﾀｶﾅ",
	"Z̴̢̛̗a̷͚l̶̰g̵̝o क्ष স্ত্র กำ ລຳ \u200b\u200d\u00ad\ttab\there",
	"\x1b[1;31mred\x1b[0m \x1b]8;;https://example.com/a/very/long/url/path\x1b\\link\x1b]8;;\x1b\\ " + strings.Repeat("🚀", 30),
	"verylongunbrokenpath/日本/👨‍👩‍👧‍👦/" + strings.Repeat("x", 150),
}

func TestCodingAgentRowWidthSweep(t *testing.T) {
	all := strings.Join(sweepText, "\n")
	rows, violations := 0, 0
	// Renderers return strings that callers join and split on "\n" (upstream
	// renderDiff returns one joined string), so check physical rows.
	check := func(name string, w int, rendered []string) {
		lines := strings.Split(strings.Join(rendered, "\n"), "\n")
		for i, l := range lines {
			rows++
			if vw := widthx.VisibleWidth(l); vw > w {
				violations++
				if violations <= 30 {
					t.Errorf("%s width=%d row %d: visibleWidth %d > %d: %+q", name, w, i, vw, w, l)
				}
			}
		}
	}
	diff := "--- a/日本.go\n+++ b/日本.go\n@@ -1,3 +1,3 @@\n-" + sweepText[0] + "\n+" + sweepText[1] + "\n " + sweepText[2] + "\n+\t" + sweepText[3]
	fd := footerData{
		model:             &ai.Model{ID: "model-👨‍👩‍👧‍👦-日本語-" + strings.Repeat("m", 40), DisplayName: sweepText[0]},
		agentName:         sweepText[0],
		sessionName:       sweepText[1],
		cwd:               "/tmp/" + sweepText[3],
		gitBranch:         "feature/" + sweepText[0],
		contextTokens:     123456,
		thinkingLevel:     "high",
		providerCount:     2,
		extensionStatuses: map[string]string{"a": sweepText[0], "b": sweepText[2], "c": sweepText[1]},
	}
	tools := []string{"bash", "read", "write", "edit", "grep", "find", "ls", "custom_ext_tool_日本"}
	for w := 20; w <= 200; w++ {
		check("footer", w, renderFooter(fd, w))
		check("diff", w, renderDiffString(diff, w))
		for _, tool := range tools {
			for _, isErr := range []bool{false, true} {
				r := toolBodyRenderer(tool, agent.AgentToolResult{Content: all, IsError: isErr}, nil)
				if r == nil {
					continue
				}
				for _, exp := range []bool{false, true} {
					check("tool:"+tool, w, r(w, exp))
				}
			}
		}
	}
	t.Logf("coding-agent width sweep: footer+diff+%d tool bodies x error/ok x collapsed/expanded, widths 20..200, rows=%d violations=%d", len(tools), rows, violations)
}

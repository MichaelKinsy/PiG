package codingagent

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports the "self-rendered edit result" row of .upstream/v1.1.0/packages/coding-agent/test/output-pad.test.ts:62-77: an edit
// card that draws its own error result renders no line starting with a space at outputPad 0, and setOutputPad(1) shifts every
// line right by exactly one space. The other four rows are tui.TestOutputPadRendersAtZeroAndOne.
func TestOutputPadSelfRenderedEditResult(t *testing.T) {
	f, _, _, _ := editCardFixture(t, "id", "file.txt", []tools.EditReplacement{{OldText: "old", NewText: "new"}})
	f.card.SetOutputPad(0)
	f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Could not find old text"}}, IsError: true}, false)

	content := regexp.MustCompile(`[\w$(]`)
	lines := func() []string {
		var out []string
		for _, line := range f.card.Render(60) {
			plain := strings.TrimRight(widthx.StripAnsi(line), " \t")
			if content.MatchString(plain) {
				out = append(out, plain)
			}
		}
		return out
	}
	zero := lines()
	if len(zero) == 0 {
		t.Fatal("rendered no content lines")
	}
	for _, line := range zero {
		if line[0] == ' ' {
			t.Fatalf("outputPad 0 line starts with a space: %q", line)
		}
	}
	f.card.SetOutputPad(1)
	want := make([]string, len(zero))
	for i, line := range zero {
		want[i] = " " + line
	}
	if got := lines(); !slices.Equal(got, want) {
		t.Fatalf("outputPad 1 lines = %q, want %q", got, want)
	}
}

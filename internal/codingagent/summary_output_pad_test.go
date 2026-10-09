package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi 1.1.0 interactive-mode.ts (addMessageToChat / rebuildChatFromMessages) constructs CompactionSummaryMessageComponent with
// this.outputPad (compaction-summary-message.ts:15-16), so the label row indent is the outputPad setting at construction.
func TestCompactionSummaryUsesOutputPadProduction(t *testing.T) {
	for _, pad := range []int{0, 1} {
		m := &InteractiveMode{chatContainer: tui.NewContainer(), outputPad: pad}
		m.addCompactionSummary("kept the plan", 1200, nil)
		var indent = -1
		for _, line := range m.compactionOrder[0].Render(60) {
			plain := widthx.StripAnsi(line)
			if strings.Contains(plain, "[compaction]") {
				indent = len(plain) - len(strings.TrimLeft(plain, " "))
			}
		}
		if indent != pad {
			t.Fatalf("outputPad %d: label indent = %d", pad, indent)
		}
	}
}

// Pi 1.1.0 interactive-mode.ts:3537-3539 passes outputPad: this.outputPad in the options of every ToolExecutionComponent, so a
// tool card built with the setting at 0 has no horizontal padding.
func TestToolExecutionOptionsCarryOutputPadProduction(t *testing.T) {
	for _, pad := range []int{0, 1} {
		m := &InteractiveMode{outputPad: pad}
		opts := m.toolExecutionOptions()
		if opts.OutputPad == nil || *opts.OutputPad != pad {
			t.Fatalf("outputPad %d: options = %+v", pad, opts)
		}
		card := newToolCardForTest("grep", "pattern", opts)
		card.SetResult("hit", false, 0)
		indent := -1
		for _, line := range card.Render(60) {
			if plain := widthx.StripAnsi(line); strings.Contains(plain, "hit") {
				indent = len(plain) - len(strings.TrimLeft(plain, " "))
			}
		}
		if indent != pad {
			t.Fatalf("outputPad %d: card output indent = %d", pad, indent)
		}
	}
}

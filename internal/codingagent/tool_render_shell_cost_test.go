package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A shell card's result renderer runs once per update; the card then draws its component on every frame it is dirty. The
// component builds its rows once per update and width, as upstream's Text and VisualLinePreview children cache them, so a
// draw at an unchanged width costs the same for 1 KiB and for 256 KiB of output, collapsed and expanded.
func TestShellResultComponentDrawsWithoutRereadingOutput(t *testing.T) {
	cost := func(size int, expanded bool) float64 {
		output := strings.Repeat("line of command output\x1b[1m bold\x1b[0m\r\n", size/40)
		result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: output}}}
		context := extension.ToolRenderContext{State: map[string]any{}, Cwd: t.TempDir()}
		component := shellRenderResult(result, extension.ToolRenderResultOptions{Expanded: expanded}, nil, context)
		return renderBytesPerCall(50, func() { component.Render(100) })
	}
	for _, expanded := range []bool{false, true} {
		small, large := cost(1<<10, expanded), cost(256<<10, expanded)
		t.Logf("expanded=%v: %.0f B per draw at 1 KiB, %.0f B at 256 KiB", expanded, small, large)
		if large > 2*small+1024 {
			t.Errorf("expanded=%v: a draw re-reads the output: %.0f B at 1 KiB, %.0f B at 256 KiB", expanded, small, large)
		}
	}
}

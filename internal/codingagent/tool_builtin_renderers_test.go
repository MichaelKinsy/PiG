package codingagent

import (
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

var tookDuration = regexp.MustCompile(`Took [0-9.]+m?s`)

// An extension override of a built-in tool without renderers draws the
// built-in renderers upstream withBuiltInRenderers gives it in the default
// shell, which for every built-in tool but edit is the built-in card. Edit's
// built-in definition draws its own shell, so upstream nests its box in an
// override's default shell.
func TestBuiltInOverrideWithoutRenderersMatchesBuiltInCard(t *testing.T) {
	cwd := t.TempDir()
	cases := []struct {
		name, args string
		result     agent.AgentToolResult
	}{
		{"bash", `{"command":"echo hi"}`, agent.AgentToolResult{Content: "l1\nl2\nl3\nl4\nl5\nl6\nl7"}},
		{"bash", `{"command":"false"}`, agent.AgentToolResult{Content: "boom", IsError: true}},
		{"read", `{"path":"x.go"}`, agent.AgentToolResult{Content: "package x\n\tfunc\n", Details: &tools.ReadDetails{Path: "x.go"}}},
		{"read", `{"path":"x.go","offset":2,"limit":3}`, agent.AgentToolResult{Content: "nope", IsError: true, Details: &tools.ReadDetails{Path: "x.go"}}},
		{"write", `{"path":"x.txt","content":"a\nb"}`, agent.AgentToolResult{Content: "ok", Details: &tools.WriteDetails{Path: "x.txt", Content: "a\nb"}}},
		{"write", `{"path":"x.txt","content":"a\nb"}`, agent.AgentToolResult{Content: "denied", IsError: true, Details: &tools.WriteDetails{Path: "x.txt", Content: "a\nb"}}},
		{"grep", `{"pattern":"foo"}`, agent.AgentToolResult{Content: "a.go:1: foo\nb.go:2: foo"}},
		{"find", `{"pattern":"*.go"}`, agent.AgentToolResult{Content: "a.go\nb.go"}},
		{"ls", `{}`, agent.AgentToolResult{Content: "a\nb/"}},
	}
	for _, expanded := range []bool{false, true} {
		for _, c := range cases {
			render := func(exts []extension.Extension) string {
				m := &InteractiveMode{
					newRunner:     inproc.NewRunner(exts, ""),
					chatContainer: tui.NewContainer(),
					tuiInst:       tui.NewWithOutput(io.Discard, 80, 30),
					toolByID:      make(map[string]*tui.ToolExecutionComponent),
					toolStarts:    make(map[string]time.Time),
					opts:          InteractiveOptions{CWD: cwd},
					toolsExpanded: expanded,
				}
				m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "c", ToolName: c.name, Args: json.RawMessage(c.args)})
				card := m.toolByID["c"]
				m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "c", ToolName: c.name, Result: c.result})
				// The built-in card erases to the end of each row; the
				// definition shell pads it, which draws the same.
				// Durations depend on the run.
				return tookDuration.ReplaceAllString(strings.ReplaceAll(strings.Join(card.Render(80), "\n"), "\x1b[K", ""), "Took D")
			}
			builtin := render(nil)
			override := render([]extension.Extension{{Name: "o", Tools: map[string]extension.RegisteredTool{c.name: {Definition: extension.ToolDefinition{Name: c.name}}}}})
			if builtin != override {
				t.Errorf("%s %s expanded=%v: override card\n%q\nwant the built-in card\n%q", c.name, c.args, expanded, override, builtin)
			}
		}
	}
}

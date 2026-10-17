package tui

import (
	"encoding/json"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

var outputPadContentLine = regexp.MustCompile(`[\w$(]`)

// outputPadRenderLines is the text lines without ANSI codes or trailing fill; blank lines and full-width borders are skipped
// (output-pad.test.ts:23-29).
func outputPadRenderLines(component Component) []string {
	var out []string
	for _, line := range component.Render(60) {
		plain := trimRightSpace(widthx.StripAnsi(line))
		if outputPadContentLine.MatchString(plain) {
			out = append(out, plain)
		}
	}
	return out
}

func trimRightSpace(s string) string {
	end := len(s)
	for end > 0 && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[:end]
}

// Ports .upstream/v1.1.0/packages/coding-agent/test/output-pad.test.ts (#10607 siblings): at outputPad 0 no rendered line starts
// with a space, and setOutputPad(1) shifts every line right by exactly one space. The edit result rendered by the tool itself
// is the case in internal/codingagent/output_pad_edit_upstream_test.go.
func TestOutputPadRendersAtZeroAndOne(t *testing.T) {
	zero := 0
	finished := func(c *ToolExecutionComponent) *ToolExecutionComponent {
		c.SetResult("ok", false, time.Second)
		return c
	}
	for _, tc := range []struct {
		name   string
		create func() interface {
			Component
			SetOutputPad(int)
		}
	}{
		{"bash execution", func() interface {
			Component
			SetOutputPad(int)
		} {
			c := NewBashExecutionComponent("pwd", nil, false, 1)
			c.SetOutputPad(0)
			c.AppendOutput("/tmp")
			one := 1
			c.SetComplete(&one, false, nil, "")
			return c
		}},
		{"tool execution", func() interface {
			Component
			SetOutputPad(int)
		} {
			c := NewToolExecutionComponent("custom_tool", "", nil, ToolExecutionOptions{OutputPad: &zero}, nil, nil, "")
			c.SetDefinition(&ToolDefinitionRenderers{}, json.RawMessage(`{}`))
			return finished(c)
		}},
		{"tool execution without a definition", func() interface {
			Component
			SetOutputPad(int)
		} {
			return finished(NewToolExecutionComponent("custom_tool", "", nil, ToolExecutionOptions{OutputPad: &zero}, nil, nil, ""))
		}},
		{"compaction summary", func() interface {
			Component
			SetOutputPad(int)
		} {
			c := NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "summary", TokensBefore: 10}, nil, 0)
			return c
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			component := tc.create()
			lines := outputPadRenderLines(component)
			if len(lines) == 0 {
				t.Fatal("rendered no content lines")
			}
			for _, line := range lines {
				if line[0] == ' ' {
					t.Fatalf("outputPad 0 line starts with a space: %q", line)
				}
			}
			component.SetOutputPad(1)
			want := make([]string, len(lines))
			for i, line := range lines {
				want[i] = " " + line
			}
			if got := outputPadRenderLines(component); !slices.Equal(got, want) {
				t.Fatalf("outputPad 1 lines = %q, want %q", got, want)
			}
		})
	}
}

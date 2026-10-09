package codingagent

import (
	"encoding/json"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

type outputPaddedComponent interface {
	tui.Component
	SetOutputPad(outputPad int)
}

var outputPadTextRow = regexp.MustCompile(`[\w$(]`)

// outputPadLines is the rendered text without ANSI codes or trailing fill. Blank rows and full-width borders are skipped
// (pi 1.1.0 output-pad.test.ts renderLines).
func outputPadLines(component tui.Component) []string {
	var lines []string
	for _, row := range component.Render(60) {
		line := strings.TrimRight(stripANSITest(osc8Link.ReplaceAllString(row, "")), " ")
		if outputPadTextRow.MatchString(line) {
			lines = append(lines, line)
		}
	}
	return lines
}

// Ports pi 1.1.0 packages/coding-agent/test/output-pad.test.ts (#10557): every transcript block that carries the outputPad setting
// renders unpadded at outputPad 0, and setOutputPad(1) insets every text row by exactly one cell.
func TestOutputPad(t *testing.T) {
	m := &InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 80, 24), outputPad: 1}
	toolCard := func(outputPad int, definition *tui.ToolDefinitionRenderers) *tui.ToolExecutionComponent {
		card := newToolCardForTest("custom_tool", "", tui.ToolExecutionOptions{OutputPad: &outputPad})
		if definition != nil {
			card.SetDefinition(definition, json.RawMessage(`{}`))
		}
		card.SetResult("ok", false, 0)
		return card
	}
	for _, tc := range []struct {
		name   string
		create func(outputPad int) outputPaddedComponent
	}{
		{"bash execution", func(outputPad int) outputPaddedComponent {
			block := tui.NewBashExecutionComponent("pwd", nil, false, 1)
			block.SetOutputPad(outputPad)
			block.AppendOutput("/tmp")
			exitCode := 1
			block.SetComplete(&exitCode, false, nil, "")
			return block
		}},
		{"tool execution", func(outputPad int) outputPaddedComponent {
			return toolCard(outputPad, &tui.ToolDefinitionRenderers{})
		}},
		{"tool execution without a definition", func(outputPad int) outputPaddedComponent {
			return toolCard(outputPad, nil)
		}},
		{"self-rendered edit result", func(outputPad int) outputPaddedComponent {
			call, result := builtInToolRenderers("edit")
			// The card never sees the arguments complete, so the edit call renders no async diff preview and both renders show the same rows.
			card := newToolCardForTest("edit", "", tui.ToolExecutionOptions{OutputPad: &outputPad})
			card.SetDefinition(m.toolDefinitionRenderers(extension.ToolRenderers{RenderShell: extension.ToolRenderShellSelf, RenderCall: call, RenderResult: result}, func() *tui.ToolExecutionComponent { return card }, "id"), json.RawMessage(`{"path":"file.txt","edits":[{"oldText":"old","newText":"new"}]}`))
			card.SetResultValue(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Could not find old text"}}})
			card.SetResult("Could not find old text", true, 0)
			return card
		}},
		{"compaction summary", func(outputPad int) outputPaddedComponent {
			component := tui.NewCompactionSummaryMessageComponent(tui.CompactionSummaryMessage{Summary: "summary", TokensBefore: 10}, nil, 1)
			component.SetOutputPad(outputPad)
			return component
		}},
		{"branch summary", func(outputPad int) outputPaddedComponent {
			component := tui.NewBranchSummaryMessageComponent(tui.BranchSummaryMessage{Summary: "summary"}, nil, 1)
			component.SetOutputPad(outputPad)
			return component
		}},
		{"skill invocation", func(outputPad int) outputPaddedComponent {
			component := tui.NewSkillInvocationMessageComponent(tui.ParsedSkillBlock{Name: "skill", Content: "body"}, nil, 1)
			component.SetOutputPad(outputPad)
			return component
		}},
		{"custom entry", func(outputPad int) outputPaddedComponent {
			return NewCustomEntryComponent(CustomEntry{CustomType: "entry"}, func(extension.CustomEntry, extension.EntryRenderOptions, extension.Theme) extension.Component {
				panic("renderer failed")
			}, outputPad)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			component := tc.create(0)
			lines := outputPadLines(component)
			if len(lines) == 0 {
				t.Fatal("component rendered no text rows")
			}
			if padded := slices.DeleteFunc(slices.Clone(lines), func(line string) bool { return !strings.HasPrefix(line, " ") }); len(padded) != 0 {
				t.Fatalf("rows start with a space at outputPad 0: %q", padded)
			}
			component.SetOutputPad(1)
			want := make([]string, len(lines))
			for i, line := range lines {
				want[i] = " " + line
			}
			if got := outputPadLines(component); !slices.Equal(got, want) {
				t.Fatalf("rows at outputPad 1 = %q, want %q", got, want)
			}
		})
	}
}

// The expanded `!` output and a long collapsed preview take the same padding as the header and status rows (output-pad.test.ts covers
// the collapsed one-line case).
func TestOutputPadBashOutputRows(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		block := tui.NewBashExecutionComponent("seq", nil, false, 1)
		block.SetOutputPad(0)
		block.AppendOutput(strings.Repeat("line\n", 30))
		exitCode := 0
		block.SetComplete(&exitCode, false, nil, "")
		block.SetExpanded(expanded)
		lines := outputPadLines(block)
		if padded := slices.DeleteFunc(slices.Clone(lines), func(line string) bool { return !strings.HasPrefix(line, " ") }); len(padded) != 0 {
			t.Fatalf("expanded=%t: rows start with a space at outputPad 0: %q", expanded, padded)
		}
		block.SetOutputPad(1)
		for _, line := range outputPadLines(block) {
			if !strings.HasPrefix(line, " ") {
				t.Fatalf("expanded=%t: row %q is not inset at outputPad 1", expanded, line)
			}
		}
	}
}

// pi 1.1.0 interactive-mode.ts onOutputPadChange: changing the setting updates the transcript blocks, in the chat and in the pending-message
// area, in place instead of rebuilding the transcript, also while the agent streams.
func TestApplyOutputPadUpdatesBlocksInPlace(t *testing.T) {
	exitCode := 0
	bash := tui.NewBashExecutionComponent("pwd", nil, false, 1)
	bash.AppendOutput("/tmp")
	bash.SetComplete(&exitCode, false, nil, "")
	pendingBash := tui.NewBashExecutionComponent("ls", nil, false, 1)
	pendingBash.AppendOutput("file")
	pendingBash.SetComplete(&exitCode, false, nil, "")
	queuedBash := tui.NewBashExecutionComponent("date", nil, false, 1)
	queuedBash.AppendOutput("now")
	queuedBash.SetComplete(&exitCode, false, nil, "")
	card := newToolCardForTest("custom_tool", "")
	card.SetResult("ok", false, 0)
	summary := tui.NewCompactionSummaryMessageComponent(tui.CompactionSummaryMessage{Summary: "summary", TokensBefore: 10}, nil, 1)

	chat, pending := tui.NewContainer(), tui.NewContainer()
	for _, component := range []tui.Component{bash, card, summary} {
		chat.Add(component)
	}
	pending.Add(pendingBash)
	m := &InteractiveMode{chatContainer: chat, pendingMessagesContainer: pending, pendingBashBlocks: []*tui.BashExecutionComponent{queuedBash}, tuiInst: tui.NewWithOutput(io.Discard, 80, 24), outputPad: 0}
	before := chat.Children()

	m.applyOutputPad()

	if after := chat.Children(); !slices.Equal(after, before) {
		t.Fatal("the transcript was rebuilt")
	}
	for name, component := range map[string]tui.Component{"bash": bash, "tool card": card, "summary": summary, "pending bash": pendingBash, "queued bash": queuedBash} {
		lines := outputPadLines(component)
		if len(lines) == 0 || slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, " ") }) {
			t.Fatalf("%s rows at outputPad 0 = %q, want unpadded rows", name, lines)
		}
	}
	m.outputPad = 1
	m.applyOutputPad()
	for name, component := range map[string]tui.Component{"bash": bash, "tool card": card, "summary": summary, "pending bash": pendingBash, "queued bash": queuedBash} {
		if lines := outputPadLines(component); slices.ContainsFunc(lines, func(line string) bool { return !strings.HasPrefix(line, " ") }) {
			t.Fatalf("%s rows at outputPad 1 = %q, want every row inset", name, lines)
		}
	}
}

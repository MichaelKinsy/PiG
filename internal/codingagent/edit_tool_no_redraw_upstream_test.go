package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

func editFixtureFile(t *testing.T, name string, count int) (string, []string) {
	t.Helper()
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, lines
}
func largeFixtureEdits(lines []string) []tools.EditReplacement {
	var edits []tools.EditReplacement
	for _, line := range []int{50, 150, 250, 350, 450, 550, 650, 750, 850, 950} {
		if line+1 >= len(lines) {
			break
		}
		edits = append(edits, tools.EditReplacement{OldText: strings.Join(lines[line-1:line+2], "\n"), NewText: lines[line-1] + "\n" + lines[line] + " changed\n" + lines[line+1]})
	}
	return edits
}

type editComponentFixture struct {
	mode *InteractiveMode
	card *tui.ToolExecutionComponent
}

func (f editComponentFixture) update(result agent.AgentToolResult, partial bool) {
	f.card.SetResultValue(result)
	if partial {
		f.card.SetStreaming(result.Text())
	} else {
		f.card.SetResult(result.Text(), result.IsError, 0)
	}
}

func assertEditContains(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %q", want, text)
		}
	}
}

func editCardFixture(t *testing.T, id, path string, edits []tools.EditReplacement) (editComponentFixture, *tui.TUI, *bytes.Buffer, chan func()) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	definition := withBuiltInRenderers("edit", extension.ToolDefinition{Name: "edit", Label: "edit"})
	definition.RenderShell = extension.ToolRenderShellSelf
	raw, err := json.Marshal(map[string]any{"path": path, "edits": edits})
	if err != nil {
		t.Fatal(err)
	}
	mode := &InteractiveMode{opts: InteractiveOptions{CWD: cwd}, chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
	mode.newRunner = inproc.NewRunner([]extension.Extension{{Tools: map[string]extension.RegisteredTool{"edit": {Definition: definition}}}}, cwd)
	card := tui.NewToolExecutionComponent("edit", tui.HeaderForTool("edit", raw, cwd))
	card.Cwd = cwd
	card.SetHeaderArgs(raw)
	mode.applyToolPresentation(card, id, "edit", raw)
	mode.chatContainer.Add(card)
	fixture := editComponentFixture{mode: mode, card: card}
	output := &bytes.Buffer{}
	renderer := tui.NewWithOutput(output, 80, 24)
	fixture.mode.tuiInst = renderer
	renders := make(chan func(), 4)
	renderer.SetRenderDispatcher(func(render func()) { renders <- render })
	t.Cleanup(renderer.CancelPendingRender)
	renderer.Add(fixture.mode.chatContainer)
	return fixture, renderer, output, renders
}
func waitEditPreview(t *testing.T, card *tui.ToolExecutionComponent, renders <-chan func(), want string) {
	t.Helper()
	select {
	case render := <-renders:
		render()
	case <-time.After(5 * time.Second):
		t.Fatal("edit preview did not post its completed frame")
	}
	if got := plainRows(card.Render(80)); !strings.Contains(got, want) {
		t.Fatalf("preview missing %q: %q", want, got)
	}
}

func TestEditToolNoFullRedrawUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/edit-tool-no-full-redraw.test.ts:79
	t.Run("renders the large diff in the call preview and does not full-redraw when the result settles", func(t *testing.T) {
		path, lines := editFixtureFile(t, "large-edit.txt", 1000)
		edits := largeFixtureEdits(lines)
		diff := tools.ComputeEditsDiff(path, edits, "")
		if diff.Error != "" {
			t.Fatal(diff.Error)
		}
		f, ui, output, renders := editCardFixture(t, "tool-call-1", path, edits)
		f.mode.chatContainer.Clear()
		for i := range 200 {
			f.mode.chatContainer.Add(tui.NewText(fmt.Sprintf("history %d", i)))
		}
		f.mode.chatContainer.Add(f.card)
		ui.Render()
		f.card.SetArgsComplete()
		ui.Render()
		waitEditPreview(t, f.card, renders, "line 50 changed")
		assertEditContains(t, plainRows(f.card.Render(80)), "edit", "line 950 changed")
		clears := strings.Count(output.String(), "\x1b[2J\x1b[H\x1b[3J")
		replays := strings.Count(output.String(), "history 0")
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path)}}, Details: &tools.EditToolDetails{Diff: diff.Diff, FirstChangedLine: diff.FirstChangedLine}}, false)
		ui.Render()
		if got := strings.Count(output.String(), "\x1b[2J\x1b[H\x1b[3J"); got != clears {
			t.Fatalf("settling added full clears: %d -> %d", clears, got)
		}
		if got := strings.Count(output.String(), "history 0"); got != replays {
			t.Fatalf("settling replayed history: %d -> %d", replays, got)
		}
		text := plainRows(f.card.Render(80))
		assertEditContains(t, text, "line 50 changed", "line 950 changed")
		if strings.Contains(text, "Successfully replaced") {
			t.Fatalf("success message duplicated the diff: %q", text)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/edit-tool-no-full-redraw.test.ts:152
	t.Run("reconstructs the boxed preview from a settled result without argsComplete", func(t *testing.T) {
		path, lines := editFixtureFile(t, "replay-edit.txt", 200)
		edits := largeFixtureEdits(lines)
		diff := tools.ComputeEditsDiff(path, edits, "")
		if diff.Error != "" {
			t.Fatal(diff.Error)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		f, ui, _, _ := editCardFixture(t, "tool-call-replay", path, edits)
		ui.Render()
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(edits), path)}}, Details: &tools.EditToolDetails{Diff: diff.Diff, FirstChangedLine: diff.FirstChangedLine}}, false)
		ui.Render()
		assertEditContains(t, plainRows(f.card.Render(80)), "line 50 changed", "line 150 changed")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/edit-tool-no-full-redraw.test.ts:201
	t.Run("shows a preflight error without rendering a diff when the edits do not apply", func(t *testing.T) {
		path, _ := editFixtureFile(t, "missing-edit.txt", 2)
		f, ui, _, renders := editCardFixture(t, "tool-call-2", path, []tools.EditReplacement{{OldText: "does not exist", NewText: "replacement"}})
		ui.Render()
		f.card.SetArgsComplete()
		ui.Render()
		waitEditPreview(t, f.card, renders, "Could not find")
		text := plainRows(f.card.Render(80))
		if strings.Contains(text, "+1 ") || strings.Contains(text, "-1 ") {
			t.Fatalf("failed preflight rendered a diff: %q", text)
		}
	})
}

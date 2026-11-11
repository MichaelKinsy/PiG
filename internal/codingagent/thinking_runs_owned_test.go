package codingagent

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

var ownedRunsEscapes = regexp.MustCompile("\x1b(?:\\[[0-9;?]*[A-Za-z]|\\][^\x07]*\x07)")

// ownedRunsRows renders block twice around the mode's off-loop transforms, so the second frame holds their published results.
func ownedRunsRows(m *InteractiveMode, block *tui.AssistantMessageComponent) []string {
	block.Render(60)
	m.backgroundTasks.Wait()
	var rows []string
	for _, line := range block.Render(60) {
		rows = append(rows, strings.TrimRight(ownedRunsEscapes.ReplaceAllString(line, ""), " "))
	}
	return rows
}

// ownerRunner loads an extension that rewrites every thinking run to nothing, and owns thinking runs when owns is set; it returns the
// runner and the API the extension kept.
func ownerRunner(t *testing.T, owns bool) (*inproc.Runner, extension.API) {
	t.Helper()
	runtime := extension.CreateExtensionRuntime()
	var kept extension.API
	loaded, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		kept = pi
		pi.RegisterMarkdownTransformer(func(markdown string, ctx extension.MarkdownTransformContext) string {
			if ctx.MessageType == extension.MarkdownMessageAssistantThinking {
				return ""
			}
			return markdown
		})
		if owns {
			pi.OwnThinkingRuns(true)
		}
		return nil
	}, t.TempDir(), extension.CreateEventBus(), runtime, "compact")
	if err != nil {
		t.Fatal(err)
	}
	return inproc.NewRunner([]extension.Extension{loaded}, t.TempDir(), runtime), kept
}

// pig additive (D214): the interactive mode's assistant blocks leave no spacing around a thinking run an owning extension rewrote to
// nothing (issue #384, path 2 and the hidden path), follow a change made while the extension runs, and stop following a runner the
// mode replaced; a block created later starts in the current state.
func TestInteractiveModeFollowsOwnedThinkingRuns(t *testing.T) {
	m := autocompleteModeProbe(t)
	content := []tui.AssistantSegment{{Thinking: true, Text: "deep thoughts"}, {Text: "answer"}}
	newBlock := func() *tui.AssistantMessageComponent {
		block := m.newAssistantMessageBlock(messageMeta{})
		block.SetContent(content)
		m.assistantBlocks = append(m.assistantBlocks, block)
		return block
	}
	stock, owned := []string{"", "", " answer"}, []string{"", " answer"}

	runner, api := ownerRunner(t, true)
	m.newRunner = runner
	m.bindThinkingRuns()
	before := newBlock()
	if got := ownedRunsRows(m, before); !reflect.DeepEqual(got, owned) {
		t.Fatalf("a block of an owning extension: %q, want %q", got, owned)
	}
	api.OwnThinkingRuns(false)
	runPostedOwnerTasks(m)
	if got := ownedRunsRows(m, before); !reflect.DeepEqual(got, stock) {
		t.Fatalf("after the extension withdrew: %q, want %q", got, stock)
	}
	if got := ownedRunsRows(m, newBlock()); !reflect.DeepEqual(got, stock) {
		t.Fatalf("a block created after the withdrawal: %q, want %q", got, stock)
	}
	api.OwnThinkingRuns(true)
	runPostedOwnerTasks(m)
	for i, block := range m.assistantBlocks {
		if got := ownedRunsRows(m, block); !reflect.DeepEqual(got, owned) {
			t.Fatalf("block %d after the extension owned again: %q, want %q", i, got, owned)
		}
	}

	// A reload replaces the runner: ownership drops with the old extension and the old runner no longer reaches the blocks.
	plain, _ := ownerRunner(t, false)
	m.newRunner = plain
	m.bindThinkingRuns()
	for i, block := range m.assistantBlocks {
		if got := ownedRunsRows(m, block); !reflect.DeepEqual(got, stock) {
			t.Fatalf("block %d after the reload: %q, want %q", i, got, stock)
		}
	}
	api.OwnThinkingRuns(false)
	api.OwnThinkingRuns(true)
	runPostedOwnerTasks(m)
	if got := ownedRunsRows(m, before); !reflect.DeepEqual(got, stock) {
		t.Fatalf("the replaced runner still reaches the blocks: %q", got)
	}
}

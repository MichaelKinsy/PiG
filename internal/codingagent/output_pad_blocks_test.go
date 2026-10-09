package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/custom-entry.ts

import (
	"context"
	"io"

	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"

	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// resumedModeWithOutputPad persists a session through build, reloads it (the resume path) and returns a mode whose outputPad is pad.
func resumedModeWithOutputPad(t *testing.T, pad int, build func(sess *Session)) *InteractiveMode {
	t.Helper()
	dir := t.TempDir()
	sess, err := NewSessionManagerWithDir(dir, dir).Create("output-pad", "")
	if err != nil {
		t.Fatal(err)
	}
	build(sess)
	loaded, err := NewSessionManagerWithDir(dir, dir).Load(sess.Path())
	if err != nil {
		t.Fatalf("reload session: %v", err)
	}
	var terminal bytes.Buffer
	m := &InteractiveMode{
		opts:          InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: loaded}},
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(&terminal, 80, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
		outputPad:     pad,
	}
	m.tuiInst.Add(m.chatContainer)
	return m
}

func labelColumn(t *testing.T, m *InteractiveMode, label string) int {
	t.Helper()
	for _, line := range m.chatContainer.Render(60) {
		if plain := widthx.StripAnsi(line); strings.Contains(plain, label) {
			return len(plain) - len(strings.TrimLeft(plain, " "))
		}
	}
	t.Fatalf("no chat row contains %q", label)
	return -1
}

// Pi interactive-mode.ts:3916 (BranchSummaryMessageComponent with this.outputPad) and :3936 (SkillInvocationMessageComponent with
// this.outputPad): resuming a session renders the branch summary and the skill block with the outputPad setting, 0 or 1.
func TestResumedBranchSummaryAndSkillBlockUseOutputPadProduction(t *testing.T) {
	const skillText = "<skill name=\"pad-skill\" location=\"/tmp/pad-skill/SKILL.md\">\nskill body\n</skill>"
	for _, pad := range []int{0, 1} {
		branch := resumedModeWithOutputPad(t, pad, func(sess *Session) {
			id, err := sess.AppendMessage(userMsg("hello"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := sess.BranchWithSummary(&id, "abandoned work", nil, false, nil); err != nil {
				t.Fatal(err)
			}
		})
		branch.renderSessionEntries()
		if got := labelColumn(t, branch, "[branch]"); got != pad {
			t.Errorf("branch summary with outputPad %d starts after %d columns", pad, got)
		}
		skill := resumedModeWithOutputPad(t, pad, func(sess *Session) {
			if _, err := sess.AppendMessage(userMsg(skillText)); err != nil {
				t.Fatal(err)
			}
		})
		skill.renderSessionEntries()
		if got := labelColumn(t, skill, "[skill]"); got != pad {
			t.Errorf("skill invocation with outputPad %d starts after %d columns", pad, got)
		}
	}
}

// Pi interactive-mode.ts:3539, :3618, :4036 build every tool card with outputPad: this.outputPad, and :5079-5086 update it on a
// setting change: through the live event path a card has no horizontal padding at outputPad 0 and one column at 1.
func TestToolCardUsesOutputPadThroughTheEventPath(t *testing.T) {
	for _, pad := range []int{0, 1} {
		m := newFallbackTestMode()
		m.outputPad = pad
		args := json.RawMessage(`{"find":"PAD_MARKER"}`)
		m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "pad-1", ToolName: "pad_tool", Args: args})
		card := m.toolByID["pad-1"]
		if card == nil {
			t.Fatal("no tool card")
		}
		for _, line := range card.Render(80) {
			if plain := widthx.StripAnsi(line); strings.Contains(plain, "pad_tool") {
				if got := len(plain) - len(strings.TrimLeft(plain, " ")); got != pad {
					t.Errorf("outputPad %d: card header starts after %d columns", pad, got)
				}
			}
		}
		card.SetOutputPad(1 - pad)
		for _, line := range card.Render(80) {
			if plain := widthx.StripAnsi(line); strings.Contains(plain, "pad_tool") {
				if got := len(plain) - len(strings.TrimLeft(plain, " ")); got != 1-pad {
					t.Errorf("after SetOutputPad(%d): card header starts after %d columns", 1-pad, got)
				}
			}
		}
	}
}

// Pi interactive-mode.ts:7009 and :7037 build the user-bash block with `new BashExecutionComponent(command, this.ui, excludeFromContext,
// this.outputPad)`: through the live `!command` path the block's command row is padded by the outputPad setting.
func TestUserBashBlockUsesOutputPadProduction(t *testing.T) {
	for _, pad := range []int{0, 1} {
		m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), Model: &ai.Model{ID: "m"}})
		m.outputPad = pad
		m.chatContainer, m.pendingMessagesContainer = tui.NewContainer(), tui.NewContainer()
		m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
		ctx, cancel := context.WithCancel(t.Context())
		m.runCtx, m.backgroundCtx = ctx, ctx
		m.abortCtx, m.abortFn = context.WithCancel(ctx)
		result := &extension.UserBashEventResult{Result: map[string]any{"output": "out", "exitCode": 0, "cancelled": false, "truncated": false}}
		m.newRunner = inproc.NewRunner([]extension.Extension{{Path: "pad", Handlers: map[string][]extension.HandlerFn{
			"user_bash": {func(...any) (any, error) { return result, nil }},
		}}}, m.opts.CWD)
		loopDone := make(chan struct{})
		go m.drainLoop(ctx, loopDone)
		submitted := make(chan struct{})
		m.runOnMain(ctx, func() {
			m.handleBashCommand(ctx, "echo PAD_BASH", false)
			close(submitted)
		})
		<-submitted
		m.backgroundTasks.Wait()
		if len(m.bashOrder) != 1 {
			t.Fatalf("bash blocks = %d, want 1", len(m.bashOrder))
		}
		found := false
		for _, line := range m.bashOrder[0].Render(100) {
			if plain := widthx.StripAnsi(line); strings.Contains(plain, "echo PAD_BASH") {
				found = true
				if got := len(plain) - len(strings.TrimLeft(plain, " ")); got != pad {
					t.Errorf("outputPad %d: bash command row starts after %d columns", pad, got)
				}
			}
		}
		if !found {
			t.Error("bash command row not rendered")
		}
		cancel()
		<-loopDone
		m.backgroundTasks.Wait()
	}
}

// Pi custom-entry.ts:18,37,54-56 (constructor outputPad = 1, setOutputPad rebuilds, the renderer-failed Box uses outputPad) and
// interactive-mode.ts:3851 (new CustomEntryComponent(entry, renderer, this.outputPad)): a failing entry renderer's box is padded
// by the outputPad setting through addCustomEntryToChat, and follows a later setOutputPad.
func TestCustomEntryFailureBoxUsesOutputPadProduction(t *testing.T) {
	failing := extension.Extension{EntryRenderers: map[string]extension.EntryRenderer{
		"note": func(extension.CustomEntry, extension.EntryRenderOptions, extension.Theme) extension.Component {
			panic("boom")
		},
	}}
	start := func(component tui.Component) int {
		t.Helper()
		for _, line := range component.Render(60) {
			if plain := widthx.StripAnsi(line); strings.Contains(plain, "renderer failed: boom") {
				return len(plain) - len(strings.TrimLeft(plain, " "))
			}
		}
		t.Fatal("renderer failure not drawn")
		return -1
	}
	for _, pad := range []int{0, 1} {
		m := &InteractiveMode{
			newRunner:     inproc.NewRunner([]extension.Extension{failing}, t.TempDir()),
			chatContainer: tui.NewContainer(),
			outputPad:     pad,
		}
		m.addCustomEntryToChat(CustomEntry{CustomType: "note", Data: "x"})
		var entry *CustomEntryComponent
		for _, child := range m.chatContainer.Children() {
			if c, ok := child.(*CustomEntryComponent); ok {
				entry = c
			}
		}
		if entry == nil {
			t.Fatal("no custom entry component in the chat")
		}
		if got := start(entry); got != pad {
			t.Errorf("outputPad %d: failure box starts after %d columns", pad, got)
		}
		entry.SetOutputPad(1 - pad)
		if got := start(entry); got != 1-pad {
			t.Errorf("after SetOutputPad(%d): failure box starts after %d columns", 1-pad, got)
		}
	}
}

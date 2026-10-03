package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi's setupEditorSubmitHandler handles these exact strings before compaction
// and model dispatch; they never enter the command completion/help registry.
func TestHiddenEasterEggsSubmitDuringCompaction(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.chatContainer = tui.NewContainer()
	t.Cleanup(m.disposeArminComponents)
	m.slashRegistry = NewSlashRegistry()
	m.isCompacting = true
	m.setupEditorSubmitHandler(t.Context())
	for _, tc := range []struct{ command, text string }{
		{"/arminsayshi", "pigsayhi"},
		{"/pigsayhi", "pigsayhi"},
		{"/dementedelves", "pi has joined Earendil"},
	} {
		m.editor.SetText(tc.command)
		m.editor.OnSubmit(tc.command)
		got := widthx.StripAnsi(strings.Join(m.chatContainer.Render(80), "\n"))
		if !strings.Contains(got, tc.text) {
			t.Errorf("%s did not render %q: %s", tc.command, tc.text, got)
		}
		if m.editor.Text() != "" {
			t.Errorf("editor not cleared: %q", m.editor.Text())
		}
	}
	if len(m.compactionQueue) != 0 {
		t.Errorf("hidden commands were queued as model prompts: %+v", m.compactionQueue)
	}
	for _, cmd := range BuiltinSlashCommands() {
		if cmd.Name == "arminsayshi" || cmd.Name == "pigsayhi" || cmd.Name == "dementedelves" {
			t.Errorf("hidden inline command leaked into registry: %s", cmd.Name)
		}
	}
}

func TestHiddenEasterEggsStreamingInputAndExactMatch(t *testing.T) {
	m, ctx, _ := newStreamingRoutingMode(t)
	t.Cleanup(func() { onLoop(m, ctx, m.disposeArminComponents) })
	onLoop(m, ctx, func() {
		m.setupEditorSubmitHandler(ctx)
		for _, command := range []string{"/arminsayshi", "/pigsayhi", "/dementedelves", "/arminsayshi extra", "/pigsayhi extra", "/dementedelves extra", "/ARMINsayshi", "/PIGsayhi"} {
			m.editor.SetText(command)
			if err := m.dispatchKey(ctx, "\r"); err != nil {
				t.Error(err)
				return
			}
		}
		steering, followUp := m.agent.PendingMessages()
		want := []string{"/arminsayshi extra", "/pigsayhi extra", "/dementedelves extra", "/ARMINsayshi", "/PIGsayhi"}
		if len(steering) != len(want) || len(followUp) != 0 {
			t.Errorf("commands entered model queues: steering=%v followUp=%v", steering, followUp)
			return
		}
		for i, text := range want {
			if got := extractAgentMessageText(steering[i]); got != text {
				t.Errorf("steer[%d]=%q, want %q", i, got, text)
			}
		}
		chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(80), "\n"))
		for _, text := range []string{"pigsayhi", "pi has joined Earendil"} {
			if !strings.Contains(chat, text) {
				t.Errorf("missing inline output %q", text)
			}
		}
	})
}

func TestHiddenEasterEggsClearDisposesAnimation(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.chatContainer = tui.NewContainer()
	m.handleArminSaysHi(t.Context())
	a := m.arminComponents[0]
	m.buildSlashContext(t.Context()).Clear()
	if len(m.arminComponents) != 0 || len(m.chatContainer.Children()) != 0 {
		t.Fatal("clear retained hidden-command state")
	}
	select {
	case <-a.animationDone:
	default:
		t.Fatal("clear left timer worker running")
	}
}

type easterEggRenderer struct {
	tui.Renderer
	requests int
}

func (r *easterEggRenderer) RequestRender() { r.requests++ }

func TestArminAnimationFollowsRendererReplacement(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.chatContainer = tui.NewContainer()
	oldRenderer := &easterEggRenderer{Renderer: m.tuiInst}
	m.tuiInst = oldRenderer
	m.handleArminSaysHi(t.Context())
	t.Cleanup(m.disposeArminComponents)
	frame := <-m.uiTaskCh
	replacement := &easterEggRenderer{Renderer: oldRenderer.Renderer}
	m.tuiInst = replacement
	before := oldRenderer.requests
	frame()
	if oldRenderer.requests != before || replacement.requests != 1 {
		t.Fatalf("frame retained outgoing renderer: old=%d (was %d), replacement=%d", oldRenderer.requests, before, replacement.requests)
	}
}

// Pi handles both commands inside the editor's onSubmit and returns before any
// addToHistory or pendingUserInputs push (interactive-mode.ts:3203-3212), so
// neither exact command is recallable with Up and neither reaches the prompt loop.
func TestHiddenEasterEggsEditorSubmitSkipsHistoryAndPromptLoop(t *testing.T) {
	m := newPendingDisplayHarness(t)
	m.chatContainer = tui.NewContainer()
	m.slashRegistry = NewSlashRegistry()
	t.Cleanup(m.disposeArminComponents)
	m.setupEditorSubmitHandler(t.Context())
	for _, tc := range []struct{ command, text string }{
		{" /arminsayshi\t", "pigsayhi"},
		{"/pigsayhi", "pigsayhi"},
		{"/dementedelves ", "pi has joined Earendil"},
	} {
		m.editor.OnSubmit(tc.command)
		if len(m.pendingUserInputs) != 0 {
			t.Fatalf("%q entered the prompt loop: %q", tc.command, m.pendingUserInputs)
		}
		if got := widthx.StripAnsi(strings.Join(m.chatContainer.Render(80), "\n")); !strings.Contains(got, tc.text) {
			t.Fatalf("%q did not render %q synchronously", tc.command, tc.text)
		}
	}
	m.editor.HandleInput("\x1b[A")
	if got := m.editor.Text(); got != "" {
		t.Fatalf("hidden command entered editor history: Up recalled %q", got)
	}
}

// Only the editor's onSubmit recognizes the hidden commands. Pi sends an
// initial message through session.prompt (interactive-mode.ts:1164-1166) and
// replays compaction-queued text through session.prompt/steer/followUp
// (interactive-mode.ts:4616-4690), so on those paths the exact text is model
// input like any other unregistered slash text.
func TestHiddenEasterEggsNonEditorSubmitPathsReachModel(t *testing.T) {
	m, ctx, _ := newStreamingRoutingMode(t)
	t.Cleanup(func() { onLoop(m, ctx, m.disposeArminComponents) })
	onLoop(m, ctx, func() {
		m.handleSubmit(ctx, "/arminsayshi")
		m.handleSubmit(ctx, "/pigsayhi")
		m.handleSubmit(ctx, "/dementedelves")
		steering, _ := m.agent.PendingMessages()
		want := []string{"/arminsayshi", "/pigsayhi", "/dementedelves"}
		if len(steering) != len(want) {
			t.Errorf("steering=%v, want %q", steering, want)
			return
		}
		for i, text := range want {
			if got := extractAgentMessageText(steering[i]); got != text {
				t.Errorf("steer[%d]=%q, want %q", i, got, text)
			}
		}
		if len(m.arminComponents) != 0 {
			t.Error("non-editor path started the Armin animation")
		}
	})
}

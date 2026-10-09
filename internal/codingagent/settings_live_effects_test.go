package codingagent

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

type settingEffectsProvider struct {
	mu   sync.Mutex
	opts []ai.StreamOptions
}

func (*settingEffectsProvider) ID() string   { return "setting-effects" }
func (*settingEffectsProvider) Close() error { return nil }
func (p *settingEffectsProvider) Stream(_ context.Context, _ ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.opts = append(p.opts, opts)
	p.mu.Unlock()
	return completedTestStream(p.ID()), nil
}

func (p *settingEffectsProvider) lastOptions(t *testing.T) ai.StreamOptions {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.opts) == 0 {
		t.Fatal("provider was not called")
	}
	return p.opts[len(p.opts)-1]
}

func TestSettingsLiveEffectsClosureTrace(t *testing.T) {
	t.Run("skill autocomplete", TestOnSettingAppliedRebuildsSkillAutocomplete)
	t.Run("transport", TestOnSettingAppliedUpdatesAgentTransport)
	t.Run("cache misses", TestOnSettingAppliedRebuildsCacheMissNotices)
	t.Run("layout", TestOnSettingAppliedUpdatesEditorAndOutputLayout)
	t.Run("clear on shrink", TestOnSettingAppliedClearsIdleStatusAfterDisablingClearOnShrink)
	t.Run("hide thinking", TestOnSettingAppliedTogglesThinkingVisibility)
	writeSettingsClosureTrace(t,
		settingsClosureTraceEvent{kind: "update", subject: "autocomplete-max-visible", value: "15"},
		settingsClosureTraceEvent{kind: "update", subject: "cache-miss-notices", value: "visible"},
		settingsClosureTraceEvent{kind: "update", subject: "clear-on-shrink", value: "idle-cleared"},
		settingsClosureTraceEvent{kind: "update", subject: "editor-padding", value: "2"},
		settingsClosureTraceEvent{kind: "update", subject: "hide-thinking", value: "true"},
		settingsClosureTraceEvent{kind: "update", subject: "output-padding", value: "0"},
		settingsClosureTraceEvent{kind: "update", subject: "skill-commands", value: "enabled"},
		settingsClosureTraceEvent{kind: "update", subject: "transport", value: "websocket"},
	)
}

func TestOnSettingAppliedRebuildsSkillAutocomplete(t *testing.T) {
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	editor := tui.NewEditor()
	mode := &InteractiveMode{
		opts: InteractiveModeOptions{
			AgentDir:        dir,
			CWD:             dir,
			SettingsManager: sm,
			Skills:          []*SkillDef{{Name: "review", Description: "Review changes"}},
		},
		editor: editor,
	}
	editor.SetAutocomplete(mode.buildAutocompleteProvider())
	if err := sm.SetEnableSkillCommands(false); err != nil {
		t.Fatal(err)
	}
	mode.buildSlashContext(t.Context()).OnSettingApplied("skill-commands", "false")

	editor.SetText("/skill:")
	editor.RefreshAutocomplete()
	if got := strings.Join(editor.Render(80), "\n"); strings.Contains(got, "skill:review") {
		t.Fatalf("disabled skill command remained in autocomplete:\n%s", got)
	}

	if err := sm.SetEnableSkillCommands(true); err != nil {
		t.Fatal(err)
	}
	mode.buildSlashContext(t.Context()).OnSettingApplied("skill-commands", "true")
	editor.SetText("/skill:")
	editor.RefreshAutocomplete()
	if got := strings.Join(editor.Render(80), "\n"); !strings.Contains(got, "skill:review") {
		t.Fatalf("enabled skill command missing from autocomplete:\n%s", got)
	}
}

func TestOnSettingAppliedUpdatesAgentTransport(t *testing.T) {
	provider := &settingEffectsProvider{}
	model := &ai.Model{ID: "test", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 8_000}}
	agent := mustNewAgent(agent.AgentOptions{Model: model})
	mode := &InteractiveMode{agent: agent}

	mode.buildSlashContext(t.Context()).OnSettingApplied("transport", "websocket")
	if _, err := agent.Send(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	if got := provider.lastOptions(t).Transport; got != ai.TransportWebSocket {
		t.Fatalf("transport = %q, want %q", got, ai.TransportWebSocket)
	}
}

func TestOnSettingAppliedRebuildsCacheMissNotices(t *testing.T) {
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	if err := sm.SetShowCacheMissNotices(false); err != nil {
		t.Fatal(err)
	}
	session := NewSession("cache-miss", dir)
	first := &agent.AssistantMessage{
		Role: "assistant", Provider: "test", ModelID: "model", Timestamp: 1,
		Usage: &ai.Usage{Input: 1_000, CacheWrite: 49_000},
	}
	second := &agent.AssistantMessage{
		Role: "assistant", Provider: "test", ModelID: "model", Timestamp: 2,
		Usage: &ai.Usage{Input: 50_000},
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: first}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: second}); err != nil {
		t.Fatal(err)
	}
	handle := &recordingCompactHandle{inner: session}
	mode := &InteractiveMode{
		opts:          InteractiveModeOptions{SettingsManager: sm, SessionHandle: handle},
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 100, 30),
	}
	mode.rebuildChatFromSession()
	if got := strings.Join(mode.chatContainer.Render(100), "\n"); strings.Contains(got, "Cache miss") {
		t.Fatalf("cache miss notice rendered while disabled:\n%s", got)
	}

	if err := sm.SetShowCacheMissNotices(true); err != nil {
		t.Fatal(err)
	}
	mode.buildSlashContext(t.Context()).OnSettingApplied("cache-miss-notices", "true")
	if got := strings.Join(mode.chatContainer.Render(100), "\n"); !strings.Contains(got, "Cache miss: 50k tokens re-billed") {
		t.Fatalf("cache miss notice missing after live enable:\n%s", got)
	}
}

func TestOnSettingAppliedUpdatesEditorAndOutputLayout(t *testing.T) {
	editor := tui.NewEditor()
	mode := &InteractiveMode{
		agent:         mustNewAgent(agent.AgentOptions{}),
		editor:        editor,
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 100, 30),
		outputPad:     1,
	}
	apply := mode.buildSlashContext(t.Context()).OnSettingApplied
	apply("editor-padding", "2")
	apply("autocomplete-max-visible", "15")
	apply("output-padding", "0")

	if editor.PaddingX() != 2 || editor.AutocompleteMaxVisible() != 15 {
		t.Fatalf("editor settings = padding %d autocomplete %d", editor.PaddingX(), editor.AutocompleteMaxVisible())
	}
	if mode.outputPad != 0 {
		t.Fatalf("output padding = %d, want 0", mode.outputPad)
	}
	block := mode.newAssistantMessageBlock()
	block.SetTextDelta("hello")
	if got := block.Render(20)[1]; !strings.HasPrefix(got, "\x1b]133;B\x07\x1b]133;C\x07hello") {
		t.Fatalf("new assistant block retained old padding: %q", got)
	}
}

// TestOnSettingAppliedTogglesThinkingVisibility exercises OnSettingApplied's
// "hide-thinking" case: /settings persisting the row live-updates the mode's
// hideThinking flag and every visible assistant block, the same as Ctrl+T
// (toggleThinkingVisibility), and persists the preference. Mirrors upstream
// settings-selector.ts's hide-thinking callback (interactive-mode.ts:3340-3355).
func TestOnSettingAppliedTogglesThinkingVisibility(t *testing.T) {
	block := tui.NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	block.SetThinkingDelta("reasoning about the answer")
	mode := &InteractiveMode{
		tuiInst:         tui.NewWithOutput(io.Discard, 100, 30),
		assistantBlocks: []*tui.AssistantMessageComponent{block},
	}
	visible := strings.Join(block.Render(60), "\n")
	if !strings.Contains(visible, "reasoning about the answer") {
		t.Fatalf("thinking text not rendered before toggling: %q", visible)
	}

	mode.buildSlashContext(t.Context()).OnSettingApplied("hide-thinking", "true")
	if !mode.hideThinking {
		t.Fatal("hideThinking was not set")
	}
	if !mode.opts.Settings.HideThinkingBlock {
		t.Fatal("HideThinkingBlock preference was not persisted")
	}
	hidden := strings.Join(block.Render(60), "\n")
	if strings.Contains(hidden, "reasoning about the answer") {
		t.Fatalf("thinking text still rendered after hiding: %q", hidden)
	}

	mode.buildSlashContext(t.Context()).OnSettingApplied("hide-thinking", "false")
	if mode.hideThinking || mode.opts.Settings.HideThinkingBlock {
		t.Fatal("hide-thinking=false did not revert the persisted setting")
	}
	restored := strings.Join(block.Render(60), "\n")
	if !strings.Contains(restored, "reasoning about the answer") {
		t.Fatalf("thinking text not restored after un-hiding: %q", restored)
	}
}

func TestOnSettingAppliedClearsIdleStatusAfterDisablingClearOnShrink(t *testing.T) {
	status := tui.NewContainer()
	mode := &InteractiveMode{
		tuiInst:         tui.NewWithOutput(io.Discard, 100, 30),
		statusContainer: status,
	}
	mode.buildSlashContext(t.Context()).OnSettingApplied("clear-on-shrink", "false")
	if !status.IsDirty() {
		t.Fatal("idle status container was not invalidated after disabling clear-on-shrink")
	}
}

// interactive-mode.ts:5078-5088 onOutputPadChange: every child of the chat and pending-message containers that has setOutputPad takes
// the new padding in place, including the bash, summary and queued-message components, while the run is idle and the components keep
// their state (nothing is rebuilt from the session).
func TestOnSettingAppliedOutputPaddingReachesEveryChatAndPendingChild(t *testing.T) {
	bash := tui.NewBashExecutionComponent("make", nil, false, 1)
	bash.AppendOutput("live output\n")
	compaction := tui.NewCompactionSummaryMessageComponent(tui.CompactionSummaryMessage{Summary: "s", TokensBefore: 10}, nil, 1)
	pendingUser := tui.NewUserMessageComponent("queued", nil, 1, nil)
	chat, pending := tui.NewContainer(), tui.NewContainer()
	chat.Add(bash)
	chat.Add(compaction)
	pending.Add(pendingUser)
	mode := &InteractiveMode{
		agent:                    mustNewAgent(agent.AgentOptions{}),
		editor:                   tui.NewEditor(),
		chatContainer:            chat,
		pendingMessagesContainer: pending,
		tuiInst:                  tui.NewWithOutput(io.Discard, 100, 30),
		outputPad:                1,
	}
	firstRowWith := func(c tui.Component, text string) string {
		for _, line := range c.Render(40) {
			if plain := stripANSITest(line); strings.Contains(plain, text) {
				return plain
			}
		}
		t.Fatalf("no row with %q", text)
		return ""
	}
	if got := firstRowWith(bash, "live output"); !strings.HasPrefix(got, " live output") {
		t.Fatalf("bash output before = %q", got)
	}
	mode.buildSlashContext(t.Context()).OnSettingApplied("output-padding", "0")
	for _, check := range []struct {
		name      string
		component tui.Component
		text      string
	}{{"bash", bash, "live output"}, {"compaction", compaction, "Compacted from"}, {"pending user", pendingUser, "queued"}} {
		if got := firstRowWith(check.component, check.text); !strings.HasPrefix(got, check.text) {
			t.Errorf("%s row after output-padding 0 = %q, want no inset", check.name, got)
		}
	}
	if chat.Children()[0] != tui.Component(bash) || bash.GetOutput() != "live output\n" {
		t.Fatal("the chat was rebuilt instead of updated in place")
	}
}

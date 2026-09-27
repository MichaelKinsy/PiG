package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// ─── classifyKey (extended: base cases in input_split_test.go) ───────────────

func TestHasAssistantUsageAfterCompaction(t *testing.T) {
	before := mustEntry(t, map[string]any{
		"type": "message", "id": "a-before", "parentId": nil, "timestamp": "2026-01-01T00:00:00Z",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]any{{"type": "text", "text": "before"}},
			"usage":   map[string]any{"input": 900, "output": 100, "totalTokens": 1000},
		},
	})
	compact := mustEntry(t, map[string]any{
		"type": "compaction", "id": "c1", "parentId": "a-before", "timestamp": "2026-01-01T00:00:01Z",
		"summary": "summary", "firstKeptEntryId": "a-before", "tokensBefore": 1000,
	})
	if latest := latestCompactionIndex([]SessionEntry{before, compact}); latest != 1 {
		t.Fatalf("latestCompactionIndex = %d, want 1", latest)
	}
	if hasAssistantUsageAfter([]SessionEntry{before, compact}, 1) {
		t.Fatal("pre-compaction assistant usage was treated as post-compaction usage")
	}

	after := mustEntry(t, map[string]any{
		"type": "message", "id": "a-after", "parentId": "c1", "timestamp": "2026-01-01T00:00:02Z",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]any{{"type": "text", "text": "after"}},
			"usage":   map[string]any{"input": 200, "output": 50, "totalTokens": 250},
		},
	})
	if !hasAssistantUsageAfter([]SessionEntry{before, compact, after}, 1) {
		t.Fatal("post-compaction assistant usage was not detected")
	}
}

func TestClassifyKey_Extended(t *testing.T) {
	tui.SetKittyProtocolActive(false)
	t.Cleanup(func() { tui.SetKittyProtocolActive(false) })
	tests := []struct {
		name string
		data string
		want keyAction
	}{
		// Direct byte mappings not covered by input_split_test.go
		{"Ctrl+L model picker", "\x0c", actionModelPicker},
		{"Ctrl+T toggle thinking", "\x14", actionToggleThinking},
		// Ctrl+V (paste) and Ctrl+Z (suspend) diverge by platform; asserted
		// platform-correctly in TestPlatformDivergentControlKeys.
		{"Ctrl+P cycle model fwd", "\x10", actionCycleModelForward},

		// Mode-aware newline variants
		// Legacy ESC+CR follow-up is platform-dependent; see
		// TestPlatformDivergentControlKeys.
		{"ESC+LF insert", "\x1b\n", actionInsert},
		{"kitty Shift+Enter", "\x1b[13;2u", actionNewline},
		{"unrecognized CSI-tilde", "\x1b[13;2~", actionInsert},
		{"xterm modifyOtherKeys Shift+Enter", "\x1b[27;2;13~", actionNewline},

		// Alt+Enter follow-up
		{"kitty Alt+Enter", "\x1b[13;3u", actionFollowUp},
		{"xterm Alt+Enter", "\x1b[27;3;13~", actionFollowUp},

		// Alt+Up dequeue
		{"CSI-u Alt+Up", "\x1b[1;3A", actionDequeue},

		// Shift+Tab cycle thinking
		{"Shift+Tab", "\x1b[Z", actionCycleThinking},

		// Shift+Ctrl+P cycle model backward is platform-dependent; see
		// TestPlatformDivergentControlKeys.

		// Bracketed paste
		{"bracketed paste", "\x1b[200~hello world\x1b[201~", actionBracketedPaste},
	}
	km := otherColumnKeys()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyKeyWithBindings(tc.data, km)
			if got != tc.want {
				t.Errorf("classifyKey(%q) = %d, want %d", tc.data, got, tc.want)
			}
		})
	}
}

func TestInteractiveMode_CSIShiftEnterTildeReachesEditorNewline(t *testing.T) {
	m := &InteractiveMode{
		tuiInst:     tui.NewWithOutput(io.Discard, 120, 40),
		editor:      tui.NewEditor(),
		keybindings: DefaultKeybindingsManager(),
		isIdle:      true,
	}
	for _, input := range []string{"a", "\x1b[13;2~", "b"} {
		if err := m.dispatchKey(context.Background(), input); err != nil {
			t.Fatalf("dispatchKey(%q): %v", input, err)
		}
	}
	if got := m.editor.Text(); got != "a\nb" {
		t.Fatalf("editor text = %q, want %q", got, "a\nb")
	}
}

func TestPersistScopedModelIDsUpdatesSettings(t *testing.T) {
	agentDir := t.TempDir()
	m := NewInteractiveMode(InteractiveOptions{
		SettingsManager: NewSettingsManager(t.TempDir(), agentDir),
		Model:           &ai.Model{ID: "m", DisplayName: "m"},
	})
	m.statusLine = NewStatusLine(m.opts.Model, "", nil)

	m.persistScopedModelIDs([]string{"openai/gpt-4o", "anthropic/claude-sonnet"})

	got := m.opts.SettingsManager.GetEnabledModels()
	want := []string{"openai/gpt-4o", "anthropic/claude-sonnet"}
	if !slices.Equal(got, want) {
		t.Fatalf("enabled models = %v, want %v", got, want)
	}
}

// ─── resolveOutcome ───────────────────────────────────────────────────────────

func TestResolveOutcome(t *testing.T) {
	tests := []struct {
		name        string
		action      keyAction
		idle        bool
		editorEmpty bool
		want        dispatchOutcome
	}{
		// Exit (Ctrl+D) is dual-bound with tui.editor.deleteCharForward.
		// Upstream custom-editor.ts exits only when the editor is empty
		// ("Exit when editor is empty"); a non-empty editor falls through to
		// the Editor, which deletes the character forward. The guard is
		// editorEmpty, not idle.
		{"exit empty idle", actionExit, true, true, outcomeExit},
		{"exit empty working", actionExit, false, true, outcomeExit},
		{"ctrl-d nonempty idle deletes forward, not exit", actionExit, true, false, outcomeInsert},
		{"ctrl-d nonempty working deletes forward, not exit", actionExit, false, false, outcomeInsert},

		// Submit always reaches the submit handler. While working, it queues a steering message.
		{"submit idle", actionSubmit, true, false, outcomeSubmit},
		{"submit working steer", actionSubmit, false, false, outcomeSubmit},

		// Newline always
		{"newline idle", actionNewline, true, true, outcomeNewline},
		{"newline working", actionNewline, false, false, outcomeNewline},

		// FollowUp always
		{"followup idle", actionFollowUp, true, true, outcomeFollowUp},
		{"followup working", actionFollowUp, false, false, outcomeFollowUp},

		// Dequeue always
		{"dequeue idle", actionDequeue, true, true, outcomeDequeue},
		{"dequeue working", actionDequeue, false, false, outcomeDequeue},

		// Toggle tools always
		{"toggle tools idle", actionToggleTools, true, true, outcomeToggleTools},
		{"toggle tools working", actionToggleTools, false, false, outcomeToggleTools},

		// Interrupt (Esc): abort when working, nop when idle
		{"esc working abort", actionInterrupt, false, false, outcomeAbort},
		{"esc idle nop", actionInterrupt, true, true, outcomeNop},

		// ClearEditor (Ctrl+C): abort when working; clear when idle+nonempty; nop when idle+empty
		{"ctrl-c working clears (never aborts; abort is Esc)", actionClearEditor, false, false, outcomeClearEditor},
		{"ctrl-c idle nonempty clear", actionClearEditor, true, false, outcomeClearEditor},
		{"ctrl-c idle empty still routes to clear (arms exit timer)", actionClearEditor, true, true, outcomeClearEditor},

		// External editor: idle only
		{"ext editor idle", actionExternalEditor, true, true, outcomeExternalEditor},
		{"ext editor working", actionExternalEditor, false, false, outcomeExternalEditor},

		// Paste image: edits pending editor buffer in any state
		{"paste idle", actionPasteImage, true, true, outcomePasteImage},
		{"paste working", actionPasteImage, false, false, outcomePasteImage},

		// Model picker: idle only
		{"model picker idle", actionModelPicker, true, true, outcomeModelPicker},
		{"model picker working", actionModelPicker, false, false, outcomeModelPicker},

		// Suspend: always
		{"suspend idle", actionSuspend, true, true, outcomeSuspend},
		{"suspend working", actionSuspend, false, false, outcomeSuspend},

		// Thinking: state-invariant
		{"cycle thinking idle", actionCycleThinking, true, true, outcomeCycleThinking},
		{"cycle thinking working", actionCycleThinking, false, false, outcomeCycleThinking},
		{"toggle thinking idle", actionToggleThinking, true, true, outcomeToggleThinking},
		{"toggle thinking working", actionToggleThinking, false, false, outcomeToggleThinking},

		// Model cycling: state-invariant
		{"cycle model fwd idle", actionCycleModelForward, true, true, outcomeCycleModelForward},
		{"cycle model fwd working", actionCycleModelForward, false, false, outcomeCycleModelForward},
		{"cycle model bwd idle", actionCycleModelBackward, true, true, outcomeCycleModelBackward},
		{"cycle model bwd working", actionCycleModelBackward, false, false, outcomeCycleModelBackward},

		// Bracketed paste: edits pending editor buffer in any state
		{"paste bracket idle", actionBracketedPaste, true, true, outcomeBracketedPaste},
		{"paste bracket working", actionBracketedPaste, false, false, outcomeBracketedPaste},

		// Insert fallthrough
		{"insert", actionInsert, true, true, outcomeInsert},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveOutcome(tc.action, tc.idle, tc.editorEmpty)
			if got != tc.want {
				t.Errorf("resolveOutcome(%d, idle=%v, empty=%v) = %d, want %d",
					tc.action, tc.idle, tc.editorEmpty, got, tc.want)
			}
		})
	}
}

func TestShowStatus_Coalesces(t *testing.T) {
	m := &InteractiveMode{chatContainer: tui.NewContainer()}
	m.showStatus("first")
	m.showStatus("second")

	if got := m.chatContainer.ChildCount(); got != 2 {
		t.Fatalf("child count = %d, want 2", got)
	}
	if m.lastStatusText == nil {
		t.Fatal("lastStatusText is nil")
	}
	if got, want := m.lastStatusText.Content, tui.ActiveTheme().FgText("dim", "second"); got != want {
		t.Fatalf("status content = %q, want %q", got, want)
	}
}

func TestInteractiveMode_UsesChatContainerChildCountForUserSpacer(t *testing.T) {
	m := &InteractiveMode{chatContainer: tui.NewContainer()}
	if !m.chatContainer.IsEmpty() {
		t.Fatal("new chat container should be empty")
	}
	m.appendToChat(tui.NewUserMessageBlock("first"))
	if m.chatContainer.IsEmpty() {
		t.Fatal("chat container should not be empty after first message")
	}
}

func TestInteractiveMode_SubmitAtFileRendersAndSendsLiteralToken(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/attached.txt", []byte("INLINE_FILE_PAYLOAD\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	seen := make(chan capturedStreamRequest, 1)
	provider := captureStreamOptionsProvider{seen: seen}
	model := &ai.Model{
		ID:          "capture-1",
		DisplayName: "capture-1",
		Provider:    provider,
		Capabilities: ai.ModelCapabilities{
			ContextWindow: 8000,
		},
	}
	m := NewInteractiveMode(InteractiveOptions{CWD: dir, Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	events := make(chan agent.AgentEvent, 256)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model, EventCh: events})
	m.abortCtx = context.Background()
	m.abortFn = func() {}

	input := "@attached.txt reply with exactly: ok"
	m.handleSubmit(context.Background(), input)

	var opts capturedStreamRequest
	select {
	case opts = <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("provider was not called")
	}
	handleUserMessageStart(t, m, events)
	rendered := strings.Join(m.chatContainer.Render(100), "\n")
	if !strings.Contains(rendered, input) {
		t.Fatalf("rendered user message should keep @file token; got:\n%s", rendered)
	}
	if strings.Contains(rendered, "INLINE_FILE_PAYLOAD") || strings.Contains(rendered, "<file name=") {
		t.Fatalf("rendered user message expanded @file content; got:\n%s", rendered)
	}
	got := lastUserMessageText(t, opts.Messages)
	if got != input {
		t.Fatalf("provider prompt = %q, want literal input %q", got, input)
	}
	if strings.Contains(got, "INLINE_FILE_PAYLOAD") || strings.Contains(got, "<file name=") {
		t.Fatalf("provider prompt expanded @file content: %q", got)
	}
}

func TestInteractiveMode_WorkingMessagePersistsOnExtUIContext(t *testing.T) {
	m := &InteractiveMode{statusLine: NewStatusLine(nil, "", nil)}
	ui := &ExtUIContext{m: m}
	ui.SetWorkingMessage("loading assets")
	if got := m.workingMessage; got != "loading assets" {
		t.Fatalf("workingMessage = %q, want %q", got, "loading assets")
	}
	if got := m.statusLine.GetWorkingMessage(); got != "loading assets" {
		t.Fatalf("statusLine working message = %q, want %q", got, "loading assets")
	}
}

func TestInteractiveMode_WorkingVisiblePersistsOnExtUIContext(t *testing.T) {
	m := &InteractiveMode{statusLine: NewStatusLine(nil, "", nil)}
	ui := &ExtUIContext{m: m}
	if m.workingMessage != "" {
		t.Fatalf("initial workingMessage = %q, want empty", m.workingMessage)
	}
	ui.SetWorkingVisible(false)
	if got := m.workingVisible; got {
		t.Fatal("workingVisible should be false after SetWorkingVisible(false)")
	}
	ui.SetWorkingVisible(true)
	if !m.workingVisible {
		t.Fatal("workingVisible should be true after SetWorkingVisible(true)")
	}
}

// StdinBuffer base cases live in input_split_test.go.
// Extended cases for bracketed paste:

func TestStdinBuffer_BracketedPasteDispatch(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"bracketed paste", "\x1b[200~hello\nworld\x1b[201~", []string{"\x1b[200~hello\nworld\x1b[201~"}},
		{"text before paste", "x\x1b[200~p\x1b[201~", []string{"x", "\x1b[200~p\x1b[201~"}},
		{"paste no end marker stays pending", "\x1b[200~orphan", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b StdinBuffer
			got := b.ProcessString(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("StdinBuffer.ProcessString(%q) = %q (len %d), want %q (len %d)",
					tc.in, got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("chunk[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestEscapeSequenceCompleteness(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare ESC at end", "\x1b", "incomplete"},
		{"ESC+char", "\x1ba", "complete"},
		{"CSI arrow", "\x1b[A", "complete"},
		{"CSI params", "\x1b[13;2u", "complete"},
		{"SS3", "\x1bOP", "complete"},
		{"SS3 at end", "\x1bO", "incomplete"},
		{"CSI no final", "\x1b[", "incomplete"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCompleteSequence(tc.in); got != tc.want {
				t.Errorf("isCompleteSequence(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ─── filterAllowedTools ───────────────────────────────────────────────────────

// stubTool implements agent.AgentTool for testing.
type stubTool struct {
	name string
}

func (s *stubTool) Name() string                           { return s.name }
func (s *stubTool) Label() string                          { return "" }
func (s *stubTool) Schema() ai.ToolSchema                  { return ai.ToolSchema{Name: s.name} }
func (s *stubTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }
func (s *stubTool) Execute(_ context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{}, nil
}

// Verify stubTool satisfies AgentTool at compile time.
var _ agent.AgentTool = (*stubTool)(nil)

func TestFilterAllowedTools(t *testing.T) {
	tools := []agent.AgentTool{
		&stubTool{"bash"},
		&stubTool{"read"},
		&stubTool{"write"},
	}

	t.Run("nil allowlist returns empty", func(t *testing.T) {
		// nil map: no keys match
		got := filterAllowedTools(tools, nil)
		if len(got) != 0 {
			t.Errorf("expected 0 tools, got %d", len(got))
		}
	})

	t.Run("empty map returns empty", func(t *testing.T) {
		got := filterAllowedTools(tools, map[string]struct{}{})
		if len(got) != 0 {
			t.Errorf("expected 0 tools, got %d", len(got))
		}
	})

	t.Run("partial match", func(t *testing.T) {
		allow := map[string]struct{}{"bash": {}, "write": {}}
		got := filterAllowedTools(tools, allow)
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
		if got[0].Name() != "bash" || got[1].Name() != "write" {
			t.Errorf("got names %s, %s", got[0].Name(), got[1].Name())
		}
	})

	t.Run("all match", func(t *testing.T) {
		allow := map[string]struct{}{"bash": {}, "read": {}, "write": {}}
		got := filterAllowedTools(tools, allow)
		if len(got) != 3 {
			t.Errorf("expected 3, got %d", len(got))
		}
	})
}

// ─── levelsForModel / maxThinkingIndex ────────────────────────────────────────

func TestLevelsForModel(t *testing.T) {
	t.Run("nil model", func(t *testing.T) {
		levels := levelsForModel(nil)
		// nil model → GetSupportedThinkingLevels returns ["off"] only.
		if len(levels) != 1 || levels[0] != "off" {
			t.Errorf("expected [off], got %v", levels)
		}
	})

	t.Run("model without xhigh", func(t *testing.T) {
		// A reasoning model with no ThinkingLevelMap entries → all standard
		// levels are available (off, minimal, low, medium, high).
		m := &ai.Model{
			Capabilities:     ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh},
			ThinkingLevelMap: ai.ThinkingLevelMap{},
		}
		levels := levelsForModel(m)
		if len(levels) != 5 {
			t.Errorf("expected 5 levels, got %d: %v", len(levels), levels)
		}
	})

	t.Run("model with xhigh", func(t *testing.T) {
		xh := "xhigh"
		m := &ai.Model{
			Capabilities:     ai.ModelCapabilities{MaxThinking: ai.ThinkingXHigh},
			ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingXHigh: &xh},
		}
		levels := levelsForModel(m)
		if len(levels) != 6 {
			t.Errorf("expected 6 levels, got %d: %v", len(levels), levels)
		}
		if levels[5] != "xhigh" {
			t.Errorf("expected last level xhigh, got %s", levels[5])
		}
	})

	t.Run("model with off mapped to nil", func(t *testing.T) {
		// Mirrors gpt-5-mini: reasoning=true, thinkingLevelMap={"off": null}.
		// "off" is excluded because mapped=nil, so levels start at minimal.
		m := &ai.Model{
			Capabilities:     ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh},
			ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingOff: nil},
		}
		levels := levelsForModel(m)
		if len(levels) != 4 {
			t.Errorf("expected 4 levels (minimal..high), got %d: %v", len(levels), levels)
		}
		if levels[0] != "minimal" {
			t.Errorf("expected first level minimal, got %s", levels[0])
		}
	})
}

func TestMaxThinkingIndex(t *testing.T) {
	t.Run("nil model", func(t *testing.T) {
		if got := maxThinkingIndex(nil); got != 0 {
			t.Errorf("expected 0, got %d", got)
		}
	})

	t.Run("model with high", func(t *testing.T) {
		m := &ai.Model{
			Capabilities:     ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh},
			ThinkingLevelMap: ai.ThinkingLevelMap{},
		}
		if got := maxThinkingIndex(m); got != 4 {
			t.Errorf("expected 4, got %d", got)
		}
	})

	t.Run("model with xhigh", func(t *testing.T) {
		xh := "xhigh"
		m := &ai.Model{
			Capabilities:     ai.ModelCapabilities{MaxThinking: ai.ThinkingXHigh},
			ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingXHigh: &xh},
		}
		if got := maxThinkingIndex(m); got != 5 {
			t.Errorf("expected 5, got %d", got)
		}
	})

	t.Run("model with no thinking", func(t *testing.T) {
		m := &ai.Model{Capabilities: ai.ModelCapabilities{MaxThinking: ""}}
		if got := maxThinkingIndex(m); got != 0 {
			t.Errorf("expected 0, got %d", got)
		}
	})

	t.Run("model with low", func(t *testing.T) {
		m := &ai.Model{
			Capabilities:     ai.ModelCapabilities{MaxThinking: ai.ThinkingLow},
			ThinkingLevelMap: ai.ThinkingLevelMap{},
		}
		if got := maxThinkingIndex(m); got != 2 {
			t.Errorf("expected 2, got %d", got)
		}
	})
}

// ─── thinkingLevelToAI ───────────────────────────────────────────────────────

func TestThinkingLevelToAI(t *testing.T) {
	tests := []struct {
		in   string
		want ai.ThinkingLevel
	}{
		{"minimal", ai.ThinkingMinimal},
		{"low", ai.ThinkingLow},
		{"medium", ai.ThinkingMedium},
		{"high", ai.ThinkingHigh},
		{"xhigh", ai.ThinkingXHigh},
		{"off", ai.ThinkingNone},
		{"", ai.ThinkingNone},
		{"unknown", ai.ThinkingNone},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := thinkingLevelToAI(tc.in); got != tc.want {
				t.Errorf("thinkingLevelToAI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ─── shortenPath ──────────────────────────────────────────────────────────────

func TestShortenPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("cannot determine home dir")
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"home itself", home, "~"},
		{"subdir", home + "/projects/foo", "~/projects/foo"},
		{"not under home", "/tmp/bar", "/tmp/bar"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shortenPath(tc.in); got != tc.want {
				t.Errorf("shortenPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ─── jsonNoEscape ─────────────────────────────────────────────────────────────

func TestJsonNoEscape(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"ampersand", map[string]string{"cmd": "a && b"}, `{"cmd":"a && b"}`},
		{"angle brackets", map[string]string{"x": "<div>"}, `{"x":"<div>"}`},
		{"plain", map[string]string{"a": "b"}, `{"a":"b"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := jsonNoEscape(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("jsonNoEscape() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatProviderErrorForDisplayCopilotAuth(t *testing.T) {
	raw := "github-copilot: resolve base URL: github-copilot: refresh failed: run 'pig login' to re-authenticate: HTTP 401: {\n  \"message\": \"Bad credentials\",\n  \"status\": \"401\"\n}"
	status, chat := formatProviderErrorForDisplay("error", raw)
	if want := "GitHub Copilot authentication failed: credentials may have expired or network is unavailable; run pig login"; status != want {
		t.Fatalf("status = %q, want %q", status, want)
	}
	if !strings.Contains(chat, "github-copilot") || !strings.Contains(chat, "Bad credentials") {
		t.Fatalf("chat should preserve provider error details, got %q", chat)
	}
	if strings.Contains(chat, "\n") || strings.Contains(chat, "  ") {
		t.Fatalf("chat should be compact single-line provider text: %q", chat)
	}
}

func TestFormatProviderErrorForDisplayCompactsGenericMultiline(t *testing.T) {
	status, chat := formatProviderErrorForDisplay("error", "openai: HTTP 400:\n{\n  bad request\n}")
	if status != "Provider request failed" {
		t.Fatalf("status = %q", status)
	}
	if strings.Contains(chat, "\n") || strings.Contains(chat, "  ") {
		t.Fatalf("chat should be single-line compact text: %q", chat)
	}
	if !strings.Contains(chat, "openai: HTTP 400") {
		t.Fatalf("chat missing original error summary: %q", chat)
	}
	if strings.Contains(chat, "Provider request failed") {
		t.Fatalf("chat should be raw provider details, assistant block adds Error prefix: %q", chat)
	}
}

func TestInteractiveMode_WriteDebugLog(t *testing.T) {
	dir := t.TempDir()
	m := &InteractiveMode{
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 120, 40),
		agent:         agent.NewAgent(agent.AgentOptions{}),
		opts:          InteractiveOptions{AgentDir: dir},
	}
	m.agent.SetMessages([]agent.AgentMessage{{User: &agent.UserMessage{
		Role:    agent.RoleUser,
		Content: []ai.UserContentBlock{ai.TextContent{Text: "hello debug"}},
	}}})

	path, err := m.writeDebugLog()
	if err != nil {
		t.Fatalf("writeDebugLog: %v", err)
	}
	if filepath.Base(path) != AppName+"-debug.log" {
		t.Errorf("debug log name = %q, want %s-debug.log", filepath.Base(path), AppName)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		"Debug output at ",
		"Terminal: 120x40",
		"=== All rendered lines with visible widths ===",
		"=== Agent messages (JSONL) ===",
		"hello debug",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("debug log missing %q\n---\n%s", want, s)
		}
	}
}

// TestInteractiveMode_HasActiveAgentTurn_PreStreamWindow guards the
// concurrent-turn race. runPromptTurn commits a turn synchronously
// (turnActive=true) but starts the agent in a goroutine, so
// Agent.IsStreaming() stays false until the goroutine reaches runLoop (after
// the before_agent_start hook and any pre-prompt auto-compaction). The
// keystroke submit path used IsStreaming() to decide steer-vs-new-turn, so a
// submit in that window started a SECOND concurrent turn. Two turns then
// persisted back-to-back assistant messages, breaking tool_use/tool_result
// pairing ("tool_use_id ... has no corresponding tool_use") and wedging the
// session. hasActiveAgentTurn must report the committed turn as active (case A,
// steer) while still reporting a finished turn with a stale isIdle=false as
// inactive (case B, new turn: see
// TestPendingDisplay_EnterAfterAgentStoppedStartsNewTurn). Mirrors upstream
// isStreaming == _isAgentRunActive, set synchronously in prompt()
// (agent-session.ts:874).
func TestInteractiveMode_HasActiveAgentTurn_PreStreamWindow(t *testing.T) {
	m := &InteractiveMode{agent: agent.NewAgent(agent.AgentOptions{})}

	// Idle: no committed turn, not streaming.
	if m.hasActiveAgentTurn() {
		t.Fatal("idle session reported an active turn")
	}

	// Case A: turn committed by runPromptTurn (turnActive=true) but the agent
	// goroutine has not reached runLoop yet, so IsStreaming() is still false.
	// The submit must be treated as active so it steers.
	if m.agent.IsStreaming() {
		t.Fatal("precondition: fresh agent must not be streaming")
	}
	m.turnActive.Store(true)
	if !m.hasActiveAgentTurn() {
		t.Fatal("committed-but-not-yet-streaming turn read as inactive; a second submit would start a concurrent turn and corrupt the session")
	}

	// Case B: turn finished (goroutine cleared turnActive) but isIdle is still
	// stale-false because its runOnMain reset has not run. Enter must start a
	// new turn, not steer into a queue nothing drains.
	m.turnActive.Store(false)
	m.isIdle = false
	if m.hasActiveAgentTurn() {
		t.Fatal("finished turn with stale isIdle=false read as active; Enter would steer into a dead queue")
	}

	// Standalone auto-compaction (turnActive can be false while compacting).
	m.isCompacting = true
	if !m.hasActiveAgentTurn() {
		t.Fatal("compaction in progress read as inactive")
	}
}

func TestInteractiveMode_RendersSteeredUserMessageFromAgentEvent(t *testing.T) {
	chat := tui.NewContainer()
	m := &InteractiveMode{
		chatContainer:            chat,
		tuiInst:                  tui.NewWithOutput(io.Discard, 120, 40),
		pendingMessagesContainer: tui.NewContainer(),
		agent:                    agent.NewAgent(agent.AgentOptions{}),
	}
	msg := agent.AgentMessage{User: &agent.UserMessage{
		Role:    agent.RoleUser,
		Content: []ai.UserContentBlock{ai.TextContent{Text: "steer now"}},
	}}

	m.handleAgentEvent(agent.MessageStartEvent{Message: msg})

	rendered := strings.Join(chat.Render(120), "\n")
	if !strings.Contains(rendered, "steer now") {
		t.Fatalf("steered user message was not rendered inline; got:\n%s", rendered)
	}
}

func TestInteractiveMode_RendersProviderErrorInAssistantBlock(t *testing.T) {
	chat := tui.NewContainer()
	m := &InteractiveMode{
		chatContainer: chat,
		tuiInst:       tui.NewWithOutput(io.Discard, 120, 40),
		statusLine:    NewStatusLine(nil, "", nil),
		agent:         agent.NewAgent(agent.AgentOptions{}),
	}
	errMsg := `400 {"error":{"message":"output_config.effort \"high\" is not supported by model claude-opus-4.7; supported values: [medium]","code":"invalid_reasoning_effort"}}`
	msg := agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:         agent.RoleAssistant,
		StopReason:   "error",
		ErrorMessage: errMsg,
		Content:      []ai.AssistantContentBlock{ai.TextContent{Text: ""}},
	}}
	ch := make(chan agent.AgentEvent, 2)
	ch <- agent.MessageStartEvent{Message: msg}
	ch <- agent.MessageEndEvent{Message: msg}
	close(ch)

	for ev := range ch {
		m.handleAgentEvent(ev)
	}

	rendered := strings.Join(chat.Render(120), "\n")
	if !strings.Contains(rendered, "Error: 400") {
		t.Fatalf("assistant block did not render provider error; got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "invalid_reasoning_effort") {
		t.Fatalf("assistant block missing provider error details; got:\n%s", rendered)
	}
	if strings.Contains(rendered, "Provider request failed") {
		t.Fatalf("assistant block should show provider details, not generic status text; got:\n%s", rendered)
	}
}

func TestInteractiveMode_ResumedGenericToolDetailsToggle(t *testing.T) {
	sess := NewSession("resume-tools", t.TempDir())
	args := map[string]any{"find": "RESUME_ALPHA_BRAVO_CHARLIE_DELTA_ECHO_FOXTROT_GOLF_HOTEL_TAIL"}
	_, err := sess.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: agent.RoleAssistant,
		Content: []ai.AssistantContentBlock{ai.ToolCall{
			ID:        "resume-call",
			Name:      "generic_extension",
			Arguments: ai.JsonObject(args),
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sess.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role:       agent.RoleToolResult,
		ToolCallID: "resume-call",
		ToolName:   "generic_extension",
		Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: "resumed result"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "test-extension",
		Tools: map[string]extension.RegisteredTool{
			"generic_extension": {Definition: extension.ToolDefinition{Name: "generic_extension"}},
		},
	}}, "")
	var terminal bytes.Buffer
	m := &InteractiveMode{
		opts:          InteractiveOptions{SessionHandle: &recordingCompactHandle{inner: sess}},
		newRunner:     runner,
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(&terminal, 54, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
	}
	m.tuiInst.Add(m.chatContainer)

	m.renderSessionEntries()
	if len(m.toolOrder) != 1 {
		t.Fatalf("resumed tool order has %d components, want 1", len(m.toolOrder))
	}
	component := m.toolOrder[0]
	collapsed := stripANSITest(strings.Join(component.Render(54), "\n"))
	if !strings.Contains(collapsed, "ctrl+o to expand") || strings.Contains(collapsed, "Arguments:") {
		t.Fatalf("resumed generic card did not start collapsed:\n%s", collapsed)
	}

	m.toggleAllTools()
	expanded := strings.NewReplacer("\n", "", " ", "").Replace(stripANSITest(strings.Join(component.Render(54), "\n")))
	if !strings.Contains(expanded, "Arguments:") || !strings.Contains(expanded, "RESUME_ALPHA_BRAVO_CHARLIE_DELTA_ECHO_FOXTROT_GOLF_HOTEL_TAIL") {
		t.Fatalf("Ctrl+O did not expand resumed generic details: %s", expanded)
	}

	terminal.Reset()
	m.toggleAllTools()
	m.tuiInst.Render()
	collapsedAgain := stripANSITest(strings.Join(component.Render(54), "\n"))
	if !strings.Contains(collapsedAgain, "ctrl+o to expand") || strings.Contains(collapsedAgain, "Arguments:") {
		t.Fatalf("second Ctrl+O did not collapse resumed generic details:\n%s", collapsedAgain)
	}
	if !strings.Contains(terminal.String(), "\x1b[3J") {
		t.Fatalf("explicit collapse did not clear stale expanded rows from native scrollback: %q", terminal.String())
	}
}

func TestInteractiveMode_GenericExtensionToolDetailsRetainArguments(t *testing.T) {
	generic := extension.ToolDefinition{Name: "generic_extension"}
	custom := extension.ToolDefinition{
		Name: "custom_extension",
		RenderCall: func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
			return tui.NewText("custom call")
		},
	}
	runner := inproc.NewRunner([]extension.Extension{{
		Name: "test-extension",
		Tools: map[string]extension.RegisteredTool{
			generic.Name: {Definition: generic},
			custom.Name:  {Definition: custom},
		},
	}}, "")
	m := &InteractiveMode{
		newRunner:     runner,
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 80, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
	}
	args := json.RawMessage(`{"find":"PRODUCTION_PATH_ALPHA_BRAVO_CHARLIE_DELTA_ECHO_FOXTROT"}`)

	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "generic-1", ToolName: generic.Name, Args: args})
	genericComponent := m.toolByID["generic-1"]
	m.handleAgentEvent(agent.ToolExecutionEndEvent{
		ToolCallID: "generic-1",
		ToolName:   generic.Name,
		Result:     agent.AgentToolResult{Content: "updated"},
	})
	if genericComponent == nil {
		t.Fatal("generic extension tool did not create a tool card")
	}
	genericComponent.SetExpanded(true)
	rendered := strings.NewReplacer("\n", "", " ", "").Replace(stripANSITest(strings.Join(genericComponent.Render(32), "\n")))
	if !strings.Contains(rendered, "PRODUCTION_PATH_ALPHA_BRAVO_CHARLIE_DELTA_ECHO_FOXTROT") {
		t.Fatalf("production event path lost retained generic extension arguments: %s", rendered)
	}

	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "custom-1", ToolName: custom.Name, Args: args})
	customComponent := m.toolByID["custom-1"]
	customComponent.SetExpanded(true)
	if rendered := strings.Join(customComponent.Render(80), "\n"); strings.Contains(rendered, "Arguments:") {
		t.Fatalf("generic details renderer replaced a custom extension call renderer: %s", rendered)
	}
}

// TestInteractiveMode_AbortPushesErrorIntoPendingTools verifies that on abort
// with pending (streaming) tool calls, each tool component receives
// SetResult("Operation aborted", isError=true) instead of SetArgsComplete().
// Mirrors upstream interactive-mode.ts:2778-2787.
func TestInteractiveMode_AbortPushesErrorIntoPendingTools(t *testing.T) {
	chat := tui.NewContainer()
	m := &InteractiveMode{
		chatContainer: chat,
		tuiInst:       tui.NewWithOutput(io.Discard, 120, 40),
		statusLine:    NewStatusLine(nil, "", nil),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
		pendingArgs:   make(map[int]*pendingToolArg),
	}

	// Build a message with a tool call that was being streamed when abort hit.
	msg := agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:       agent.RoleAssistant,
		StopReason: "aborted",
		Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "Let me read that file"},
		},
	}}

	ch := make(chan agent.AgentEvent, 4)
	ch <- agent.MessageStartEvent{Message: msg}
	// Simulate a tool-call delta during streaming: this populates pendingArgs.
	toolPartial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{
		ai.ToolCall{ID: "tc_1", Name: "read", Arguments: ai.JsonObject{"path": "foo.go"}},
	}, StopReason: ai.StopReasonPending}
	ch <- agent.MessageUpdateEvent{
		Message: msg,
		AssistantMessageEvent: ai.ToolCallDeltaEvent{
			ContentIndex: 0, Delta: `{"path":"foo.go"}`, Partial: toolPartial,
		},
	}
	ch <- agent.MessageEndEvent{Message: msg}
	close(ch)

	var comp *tui.ToolExecutionComponent
	for ev := range ch {
		m.handleAgentEvent(ev)
		if pending := m.toolByID["tc_1"]; pending != nil {
			comp = pending
		}
	}

	// The retained transcript component should have received the error result.
	if comp == nil {
		t.Fatal("expected tool component tc_1 to be created during streaming")
	}
	if comp.State != tui.ToolStateError {
		t.Fatalf("tool state = %d, want ToolStateError(%d)", comp.State, tui.ToolStateError)
	}
	rendered := strings.Join(comp.Render(80), "\n")
	if !strings.Contains(rendered, "Operation aborted") {
		t.Fatalf("tool component should show 'Operation aborted', got:\n%s", rendered)
	}
}

// A tool still running when the user aborts (Esc) must be frozen immediately,
// not only at agent_end. A hung tool (ssh that ignores the cancelled context)
// does not emit ToolExecutionEnd promptly, so before this fix its component
// kept recomputing the live "Elapsed X.Xs" footer on every render; once the
// block had scrolled above the viewport each recompute forced a full clearing
// repaint (the flicker after Esc). finalizeRunningTools freezes it at abort
// time. Pre-fix (no finalize in the abort dispatch) this test's Render still
// shows "Elapsed" and State stays ToolStateRunning.
func TestInteractiveMode_FinalizeRunningTools_FreezesElapsed(t *testing.T) {
	running := tui.NewToolExecutionComponent("bash", "$ ssh host 'sleep 999'")
	running.MarkExecutionStarted()
	running.StartedAt = time.Now().Add(-437 * time.Second) // long-running, scrolled off
	running.SetStreaming("partial output line")
	if running.State != tui.ToolStateRunning ||
		!strings.Contains(strings.Join(running.Render(80), "\n"), "Elapsed") {
		t.Fatal("precondition: bash tool must be Running with a live Elapsed line")
	}

	// A read tool that already completed must be left untouched (no-op).
	done := tui.NewToolExecutionComponent("read", "read x.go")
	done.SetResult("contents", false, 2*time.Second)

	m := &InteractiveMode{
		toolByID:   map[string]*tui.ToolExecutionComponent{"tc_run": running, "tc_done": done},
		toolStarts: map[string]time.Time{"tc_run": time.Now().Add(-437 * time.Second)},
	}

	m.finalizeRunningTools()

	if running.State == tui.ToolStateRunning {
		t.Error("finalizeRunningTools must transition the running bash tool out of Running")
	}
	if got := strings.Join(running.Render(80), "\n"); strings.Contains(got, "Elapsed") {
		t.Errorf("frozen tool must not render a live Elapsed footer, got:\n%s", got)
	}
	if got := strings.Join(running.Render(80), "\n"); !strings.Contains(got, "partial output line") {
		t.Errorf("partial streamed output must be preserved, got:\n%s", got)
	}
	if done.State != tui.ToolStateDone {
		t.Errorf("already-terminal tool must be left untouched; state=%d", done.State)
	}
	if len(m.toolStarts) != 0 {
		t.Errorf("toolStarts must be drained after finalize; have %d", len(m.toolStarts))
	}
}

// End-to-end: pressing Esc while a bash tool runs routes through the real
// dispatchKey → outcomeAbort path and freezes the tool. Guards the production
// wiring, not just the helper.
func TestInteractiveMode_EscAbortFreezesRunningTool(t *testing.T) {
	ctx := context.Background()
	running := tui.NewToolExecutionComponent("bash", "$ ssh host 'sleep 999'")
	running.MarkExecutionStarted()
	running.StartedAt = time.Now().Add(-120 * time.Second)

	abortCtx, abortFn := context.WithCancel(ctx)
	m := &InteractiveMode{
		tuiInst:     tui.NewWithOutput(io.Discard, 120, 40),
		editor:      tui.NewEditor(),
		keybindings: DefaultKeybindingsManager(),
		isIdle:      false, // agent working → Esc means abort
		abortCtx:    abortCtx,
		abortFn:     abortFn,
		toolByID:    map[string]*tui.ToolExecutionComponent{"tc_run": running},
		toolStarts:  map[string]time.Time{"tc_run": time.Now().Add(-120 * time.Second)},
	}

	if err := m.dispatchKey(ctx, "\x1b"); err != nil {
		t.Fatalf("dispatchKey(Esc) returned error: %v", err)
	}

	if running.State == tui.ToolStateRunning {
		t.Error("Esc abort must freeze the running tool immediately")
	}
	if got := strings.Join(running.Render(80), "\n"); strings.Contains(got, "Elapsed") {
		t.Errorf("Esc abort must stop the live Elapsed footer, got:\n%s", got)
	}
	select {
	case <-abortCtx.Done():
	default:
		t.Error("Esc abort must cancel the in-flight abort context")
	}
}

func TestInteractiveMode_EscCancelsCompactionWhileAgentIsIdle(t *testing.T) {
	handle := &recordingCompactHandle{}
	m := &InteractiveMode{
		tuiInst:      tui.NewWithOutput(io.Discard, 120, 40),
		editor:       tui.NewEditor(),
		keybindings:  DefaultKeybindingsManager(),
		isIdle:       true,
		isCompacting: true,
		opts:         InteractiveOptions{SessionHandle: handle},
	}
	if err := m.dispatchKey(context.Background(), "\x1b"); err != nil {
		t.Fatal(err)
	}
	if handle.abortCompactionCount != 1 {
		t.Fatalf("AbortCompaction calls = %d, want 1", handle.abortCompactionCount)
	}
}

func TestInteractiveMode_ManualCompactionCancellationPersistsInConversation(t *testing.T) {
	chat := tui.NewContainer()
	m := &InteractiveMode{
		chatContainer:   chat,
		statusContainer: tui.NewContainer(),
		tuiInst:         tui.NewWithOutput(io.Discard, 120, 40),
		statusLine:      NewStatusLine(nil, "", nil),
		isCompacting:    true,
	}
	m.handleAgentEvent(agent.CompactionEndEvent{Reason: "manual", Aborted: true})
	if got := strings.Join(chat.Render(120), "\n"); !strings.Contains(got, "Error: Compaction cancelled") {
		t.Fatalf("manual cancellation was not retained in conversation: %q", got)
	}
}

// Local slash commands (/model, /session, /help, ...) must dispatch immediately
// during compaction, mirroring upstream where the builtin command if-chain runs
// before the isCompacting queue. Only model-bound input (plain prompts, unknown
// slashes forwarded to the LLM) is queued. Before the fix the gate queued every
// non-bash input, so "/session" showed up as a "Steering:" message and stalled
// until compaction finished.
// Upstream renders the prompt from the agent's message_start event, which
// prompt() reaches only after every before_agent_start handler completes.
func TestInteractiveMode_RendersPromptAfterBeforeAgentStartHook(t *testing.T) {
	dir := t.TempDir()
	seen := make(chan capturedStreamRequest, 1)
	provider := captureStreamOptionsProvider{seen: seen}
	model := &ai.Model{
		ID:           "capture-1",
		DisplayName:  "capture-1",
		Provider:     provider,
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	started := make(chan struct{})
	unblock := make(chan struct{})
	var closed atomic.Bool
	fresh := makeFreshWithHandler(t, EventBeforeAgentStart, func() {
		if closed.CompareAndSwap(false, true) {
			close(started)
		}
		<-unblock
	})
	m := NewInteractiveMode(InteractiveOptions{CWD: dir, Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.editor = tui.NewEditor()
	m.statusLine = NewStatusLine(model, "", nil)
	events := make(chan agent.AgentEvent, 256)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model, EventCh: events})
	m.newRunner = fresh
	m.runCtx = context.Background()
	m.abortCtx = context.Background()
	m.abortFn = func() {}

	m.handleSubmit(context.Background(), "render after hook")

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(unblock)
		t.Fatal("before_agent_start did not run")
	}
	if rendered := strings.Join(m.chatContainer.Render(100), "\n"); strings.Contains(rendered, "render after hook") {
		close(unblock)
		t.Fatalf("prompt rendered while before_agent_start was still running; got:\n%s", rendered)
	}
	select {
	case <-seen:
		close(unblock)
		t.Fatal("provider request started before before_agent_start completed")
	default:
	}
	close(unblock)
	select {
	case opts := <-seen:
		if got := lastUserMessageText(t, opts.Messages); got != "render after hook" {
			t.Fatalf("provider prompt = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider was not called after before_agent_start completed")
	}
	handleUserMessageStart(t, m, events)
	if rendered := strings.Join(m.chatContainer.Render(100), "\n"); !strings.Contains(rendered, "render after hook") {
		t.Fatalf("prompt did not render from message_start; got:\n%s", rendered)
	}
}

// handleUserMessageStart feeds the agent's first user message_start event to
// the mode, as the interactive event loop does.
func handleUserMessageStart(t *testing.T, m *InteractiveMode, events <-chan agent.AgentEvent) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if start, ok := event.(agent.MessageStartEvent); ok && start.Message.User != nil {
				m.handleAgentEvent(start)
				return
			}
		case <-deadline:
			t.Fatal("agent emitted no user message_start")
		}
	}
}

func TestInteractiveMode_PrePromptCompactionRunsBeforeBeforeAgentStart(t *testing.T) {
	dir := t.TempDir()
	seen := make(chan capturedStreamRequest, 1)
	provider := captureStreamOptionsProvider{seen: seen}
	model := &ai.Model{
		ID:           "capture-1",
		DisplayName:  "capture-1",
		Provider:     provider,
		Capabilities: ai.ModelCapabilities{ContextWindow: 20000},
	}
	handle := &recordingCompactHandle{}
	compactsSeenByHook := make(chan int, 1)
	unblock := make(chan struct{})
	fresh := makeFreshWithHandler(t, EventBeforeAgentStart, func() {
		compactsSeenByHook <- handle.compactCount
		<-unblock
	})
	m := NewInteractiveMode(InteractiveOptions{CWD: dir, Model: model, SessionHandle: handle})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.editor = tui.NewEditor()
	m.statusLine = NewStatusLine(model, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.agent.SetMessages([]agent.AgentMessage{
		mkUserMsg("previous"),
		{Assistant: &agent.AssistantMessage{
			Role:       agent.RoleAssistant,
			Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "large"}},
			StopReason: "stop",
			Usage:      &ai.Usage{Input: 20000, Output: 1},
		}},
	})
	handle.agent = m.agent
	m.newRunner = fresh
	m.runCtx = context.Background()
	m.abortCtx = context.Background()
	m.abortFn = func() {}

	m.handleSubmit(context.Background(), "next prompt")

	select {
	case got := <-compactsSeenByHook:
		if got != 1 {
			close(unblock)
			t.Fatalf("before_agent_start saw compactCount=%d, want 1", got)
		}
	case <-time.After(2 * time.Second):
		close(unblock)
		t.Fatal("before_agent_start did not run")
	}
	select {
	case <-seen:
		close(unblock)
		t.Fatal("provider request started before before_agent_start completed")
	default:
	}
	close(unblock)
	select {
	case <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("provider was not called after hook completed")
	}
}

func TestInteractiveMode_CompactionLetsLocalSlashCommandsThrough(t *testing.T) {
	ctx := context.Background()
	var ran bool
	reg := NewSlashRegistry()
	reg.Register(BuiltinSlashCommand{
		Name:        "pingtest",
		Description: "test-only local command",
		Handler:     func(_ *SlashContext) error { ran = true; return nil },
	})
	m := &InteractiveMode{
		isCompacting:             true,
		slashRegistry:            reg,
		chatContainer:            tui.NewContainer(),
		pendingMessagesContainer: tui.NewContainer(),
		tuiInst:                  tui.NewWithOutput(io.Discard, 120, 40),
		editor:                   tui.NewEditor(),
		statusLine:               NewStatusLine(nil, "", nil),
		agent:                    agent.NewAgent(agent.AgentOptions{}),
		keybindings:              DefaultKeybindingsManager(),
	}

	// A resolvable local command runs now and is not queued.
	m.handleSubmit(ctx, "/pingtest")
	if !ran {
		t.Error("a resolvable slash command must dispatch during compaction, not queue")
	}
	if len(m.compactionQueue) != 0 {
		t.Errorf("slash command must not be queued during compaction; queue=%v", m.compactionQueue)
	}

	// A plain message is still queued for after compaction.
	m.handleSubmit(ctx, "please refactor this")
	if got := m.compactionQueue; len(got) != 1 || got[0].text != "please refactor this" || got[0].mode != compactionQueueSteer {
		t.Errorf("plain message must queue during compaction; queue=%v", got)
	}

	// An unknown /slash (forwarded to the LLM) is queued too.
	m.handleSubmit(ctx, "/notacommand xyz")
	if got := m.compactionQueue; len(got) != 2 || got[1].text != "/notacommand xyz" || got[1].mode != compactionQueueSteer {
		t.Errorf("unknown slash must queue (forwarded to LLM after compaction); queue=%v", got)
	}
}

type capturedStreamRequest struct {
	ai.StreamOptions
	Messages []ai.Message
}

type captureStreamOptionsProvider struct {
	seen chan capturedStreamRequest
}

func (p captureStreamOptionsProvider) ID() string { return "capture" }

func (p captureStreamOptionsProvider) Stream(_ context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.seen <- capturedStreamRequest{StreamOptions: options, Messages: transcript.Messages()}
	partial := &ai.AssistantMessage{Provider: p.ID(), Model: "capture", StopReason: ai.StopReasonPending}
	final := &ai.AssistantMessage{Provider: p.ID(), Model: "capture", StopReason: ai.StopReasonStop}
	stream := ai.NewAssistantMessageEventStream()
	if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
		return nil, err
	}
	if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: final}); err != nil {
		return nil, err
	}
	return stream, nil
}

func (p captureStreamOptionsProvider) Close() error { return nil }

func lastUserMessageText(t *testing.T, messages []ai.Message) string {
	t.Helper()
	if len(messages) == 0 {
		t.Fatal("provider received no messages")
	}
	last, ok := messages[len(messages)-1].(ai.UserMessage)
	if !ok {
		t.Fatalf("last provider message = %T, want ai.UserMessage", messages[len(messages)-1])
	}
	switch content := last.Content.(type) {
	case ai.UserText:
		return string(content)
	case ai.UserContentBlocks:
		for _, block := range content {
			if text, ok := block.(ai.TextContent); ok {
				return text.Text
			}
		}
	}
	t.Fatal("last provider user message had no text block")
	return ""
}

func TestInteractiveMode_EnterDuringCompactionUsesCompactionQueue(t *testing.T) {
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir()})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(nil, "", nil)
	m.editor = tui.NewEditor()
	m.agent = agent.NewAgent(agent.AgentOptions{})
	m.keybindings = DefaultKeybindingsManager()
	m.isCompacting = true
	m.isIdle = true
	m.editor.SetText("send after compaction")

	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}

	if got := m.compactionQueue; len(got) != 1 || got[0].text != "send after compaction" || got[0].mode != compactionQueueSteer {
		t.Fatalf("Enter during compaction queued %v, want one compaction steering message", got)
	}
	steering, followUps := m.agent.PendingMessages()
	if len(steering) != 0 || len(followUps) != 0 {
		t.Fatalf("Enter during compaction leaked into agent queues: steering=%v followUps=%v", steering, followUps)
	}
}

func TestInteractiveMode_QueuedEnterSendsAfterCompactionEnd(t *testing.T) {
	seen := make(chan capturedStreamRequest, 1)
	model := &ai.Model{
		ID:           "capture-compaction-enter",
		DisplayName:  "capture-compaction-enter",
		Provider:     captureStreamOptionsProvider{seen: seen},
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	m.editor = tui.NewEditor()
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.keybindings = DefaultKeybindingsManager()
	m.runCtx = context.Background()
	m.abortCtx, m.abortFn = context.WithCancel(m.runCtx)
	m.isCompacting = true
	m.isIdle = true
	m.editor.SetText("send after compaction")

	if err := m.dispatchKey(context.Background(), "\r"); err != nil {
		t.Fatal(err)
	}
	m.handleAgentEvent(agent.CompactionEndEvent{Aborted: true, Reason: "manual"})

	select {
	case options := <-seen:
		if got := lastUserMessageText(t, options.Messages); got != "send after compaction" {
			t.Fatalf("post-compaction turn started with %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("message entered during compaction did not start automatically after compaction ended")
	}
}

// D-H: a message queued while compaction runs must be SENT when compaction
// ends. The agent is idle at compaction_end, so the old steer-only flush
// enqueued into a steering queue that no running loop drains: the message was
// silently dropped. The fix starts a turn with the first queued message.
// Before the fix this test times out (provider never called).
func TestInteractiveMode_CompactionQueueFlushStartsTurn(t *testing.T) {
	dir := t.TempDir()
	seen := make(chan capturedStreamRequest, 1)
	provider := captureStreamOptionsProvider{seen: seen}
	model := &ai.Model{
		ID:           "capture-1",
		DisplayName:  "capture-1",
		Provider:     provider,
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	m := NewInteractiveMode(InteractiveOptions{CWD: dir, Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.runCtx = context.Background()
	m.abortCtx = context.Background()
	m.abortFn = func() {}

	m.compactionQueue = []compactionQueuedMessage{{text: "queued during compaction", mode: compactionQueueSteer}}
	m.flushCompactionQueue(m.runCtx, true)

	select {
	case opts := <-seen:
		if got := lastUserMessageText(t, opts.Messages); got != "queued during compaction" {
			t.Fatalf("turn started with wrong prompt: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("flush did not start a turn: queued message was dropped")
	}
	if len(m.compactionQueue) != 0 {
		t.Fatalf("queue should be cleared after flush, got %v", m.compactionQueue)
	}
}

func TestAC49CompactionEndWillRetryQueuesMessageForRetryTurn(t *testing.T) {
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir()})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(nil, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{})
	m.keybindings = DefaultKeybindingsManager()
	m.runCtx = context.Background()
	m.isCompacting = true
	m.compactionQueue = []compactionQueuedMessage{
		{text: "steer after retry compaction", mode: compactionQueueSteer},
		{text: "follow up after retry compaction", mode: compactionQueueFollowUp},
	}

	m.handleAgentEvent(agent.CompactionEndEvent{Aborted: true, Reason: "auto", WillRetry: true})

	steering, followUps := m.agent.PendingMessages()
	if len(steering) != 1 || len(followUps) != 1 {
		t.Fatalf("retry queue = steering:%v followUps:%v", steering, followUps)
	}
	if got := extractAgentMessageText(steering[0]); got != "steer after retry compaction" {
		t.Fatalf("retry queued message = %q", got)
	}
	if got := extractAgentMessageText(followUps[0]); got != "follow up after retry compaction" {
		t.Fatalf("retry follow-up message = %q", got)
	}
	if len(m.compactionQueue) != 0 {
		t.Fatalf("compaction queue was not drained: %v", m.compactionQueue)
	}
}

// TestInteractiveMode_CompactionQueueFlushSkipsTurnWhenBusy verifies the
// isIdle guard: when a turn is already running (auto-compaction fired
// mid-turn), flushing must NOT start a second concurrent turn: it steers the
// queued messages instead. Starting a second turn would race the agent loop.
func TestInteractiveMode_CompactionQueueFirstFollowUpPreservesModeWhenTurnSettles(t *testing.T) {
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir()})
	m.chatContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(nil, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{})
	m.keybindings = DefaultKeybindingsManager()
	m.runCtx = context.Background()
	m.turnActive.Store(true)
	m.compactionQueue = []compactionQueuedMessage{{text: "follow after compact", mode: compactionQueueFollowUp}}

	m.flushCompactionQueue(m.runCtx, true)
	steering, followUps := m.agent.PendingMessages()
	if len(steering) != 0 || len(followUps) != 1 || extractAgentMessageText(followUps[0]) != "follow after compact" {
		t.Fatalf("first queued follow-up lost mode: steering=%v followUps=%v", steering, followUps)
	}
}

func TestInteractiveMode_CompactionQueueFlushSkipsTurnWhenBusy(t *testing.T) {
	dir := t.TempDir()
	seen := make(chan capturedStreamRequest, 1)
	provider := captureStreamOptionsProvider{seen: seen}
	model := &ai.Model{
		ID:           "capture-1",
		DisplayName:  "capture-1",
		Provider:     provider,
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	m := NewInteractiveMode(InteractiveOptions{CWD: dir, Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.keybindings = DefaultKeybindingsManager()
	m.runCtx = context.Background()
	m.abortCtx = context.Background()
	m.abortFn = func() {}

	// Simulate an in-flight turn. turnActive is what "a turn is running" means:
	// it marks exactly the interval in which a run goroutine exists to drain the
	// steering queue. Setting only isIdle described the state auto-compaction
	// leaves *after* a turn ends, where steering strands the message.
	m.keybindings = DefaultKeybindingsManager()
	m.isIdle = false
	m.turnActive.Store(true)
	m.compactionQueue = []compactionQueuedMessage{{text: "queued mid-turn", mode: compactionQueueSteer}}
	m.flushCompactionQueue(m.runCtx, true)

	select {
	case <-seen:
		t.Fatal("flush started a concurrent turn while a turn was running")
	case <-time.After(300 * time.Millisecond):
		// Expected: steered, no new Send.
	}
	if len(m.compactionQueue) != 0 {
		t.Fatalf("queue should be cleared after flush, got %v", m.compactionQueue)
	}
}

// recordingCompactHandle is a minimal InteractiveSessionHandle that records
// compaction requests, so the mode's calls into the Session can be asserted
// without a live model. Its run loop only continues from queued input: retry
// and compaction decisions belong to coding.Session and are tested there.
type recordingCompactHandle struct {
	agent                *agent.Agent
	inner                *Session
	compactCount         int
	abortCompactionCount int
	abortRetryCount      int
	cacheWarming         *CacheWarmingStatus
	cacheWarmingModes    []CacheWarmingMode
	agentSettledCount    int
}

func (h *recordingCompactHandle) CacheWarmingStatus() *CacheWarmingStatus { return h.cacheWarming }
func (h *recordingCompactHandle) SetCacheWarmingMode(mode CacheWarmingMode) error {
	h.cacheWarmingModes = append(h.cacheWarmingModes, mode)
	return nil
}
func (h *recordingCompactHandle) OnAgentSettled() { h.agentSettledCount++ }

func (h *recordingCompactHandle) Agent() *agent.Agent                               { return h.agent }
func (h *recordingCompactHandle) Inner() *Session                                   { return h.inner }
func (h *recordingCompactHandle) Events() <-chan agent.AgentEvent                   { return nil }
func (h *recordingCompactHandle) SetModel(*ai.Model, ...ModelMutationOptions) error { return nil }
func (h *recordingCompactHandle) SetThinkingLevel(level ai.ThinkingLevel, _ ...ModelMutationOptions) error {
	previous := h.agent.ThinkingLevel()
	effective := ai.ClampThinkingLevel(h.agent.Model(), level)
	h.agent.SetThinkingLevel(effective)
	if h.inner != nil && effective != previous {
		return h.inner.AppendThinkingLevelChange(string(effective))
	}
	return nil
}
func (h *recordingCompactHandle) StreamModel(_ context.Context, _ *ai.Model, _ ai.Context, _ ai.StreamOptions) *ai.AssistantMessageEventStream {
	return completedTestStream("recording")
}
func (h *recordingCompactHandle) AbortCompaction() { h.abortCompactionCount++ }
func (h *recordingCompactHandle) CheckPromptCompaction(context.Context) error {
	h.compactCount++
	return nil
}
func (h *recordingCompactHandle) RunAgentPrompt(ctx context.Context, start func(context.Context) ([]agent.AgentMessage, error)) ([]agent.AgentMessage, error) {
	messages, err := start(ctx)
	for err == nil && ctx.Err() == nil && h.agent.HasQueuedMessages() {
		messages, err = h.agent.Continue(ctx)
	}
	return messages, err
}
func (h *recordingCompactHandle) RunInputHandlers(ctx context.Context, text string, images []ai.ImageContent, source extension.InputSource, behavior string) (string, []ai.ImageContent, bool, error) {
	return text, images, false, nil
}
func (h *recordingCompactHandle) AbortRetry()             { h.abortRetryCount++ }
func (h *recordingCompactHandle) AbortBranchSummary()     {}
func (h *recordingCompactHandle) ReplaceInner(s *Session) { h.inner = s }
func (h *recordingCompactHandle) NavigateTreeHandle(_ context.Context, _ string, _ bool, _ string) (NavigateTreeResult, error) {
	return NavigateTreeResult{}, nil
}
func (h *recordingCompactHandle) Compact(_ context.Context, _ string) error {
	h.compactCount++
	return nil
}

// TestDynamicProviderInModelSurfaces: models from a registered dynamic provider
// (e.g. example-provider, radius) are resolvable and completable in the interactive
// model surfaces, matching upstream's getAvailable()-backed picker. Regression
// for the gap where pig's surfaces used only the static ai.ListModels catalog.
func TestDynamicProviderInModelSurfaces(t *testing.T) {
	dir := t.TempDir()
	reg := NewModelRegistry(dir)
	reg.RegisterProvider("example-provider", extension.ProviderConfig{
		BaseURL: "https://models.example/v1",
		APIKey:  "tok", // makes the provider count as authed in GetAvailable
		API:     "openai-completions",
		Models:  []extension.ProviderModelConfig{{ID: "test-model", Name: "Test Model"}},
	})
	m := &InteractiveMode{opts: InteractiveOptions{ModelRegistry: reg, AgentDir: dir}}

	if spec, ok := m.resolveAvailableModel("example-provider/test-model"); !ok || spec != "example-provider/test-model" {
		t.Fatalf("resolve(example-provider/test-model) = %q,%v want example-provider/test-model,true", spec, ok)
	}
	if spec, ok := m.resolveAvailableModel("test-model"); !ok || spec != "example-provider/test-model" {
		t.Fatalf("resolve(test-model) = %q,%v want example-provider/test-model,true", spec, ok)
	}

	found := false
	for _, c := range m.modelArgCompletions("test") {
		if c.Value == "example-provider/test-model" {
			found = true
		}
	}
	if !found {
		t.Fatal("modelArgCompletions omitted example-provider/test-model")
	}

	// Negative control: a provider with no configured auth is excluded.
	reg.RegisterProvider("secret-ai", extension.ProviderConfig{
		BaseURL: "https://secret.example/v1",
		Models:  []extension.ProviderModelConfig{{ID: "hidden", Name: "Hidden"}},
	})
	if _, ok := m.resolveAvailableModel("secret-ai/hidden"); ok {
		t.Fatal("unauthed dynamic provider must not resolve")
	}
}

// TestBuildChatViewportWiresLiveThemedScrollbar guards the fullscreen wiring:
// buildChatViewport must install the live-themed scrollbar track and thumb (not
// the ScrollView defaults), and a live theme change must repaint them.
func TestBuildChatViewportWiresLiveThemedScrollbar(t *testing.T) {
	prev := tui.ActiveTheme().Name
	t.Cleanup(func() { tui.SetTheme(prev) })

	m := &InteractiveMode{
		extHeader:                newSpecialLinesComponent(func() {}),
		chatContainer:            tui.NewContainer(),
		pendingMessagesContainer: tui.NewContainer(),
		statusContainer:          tui.NewContainer(),
		widgetContainer:          tui.NewContainer(),
		editorContainer:          tui.NewContainer(),
		extFooter:                newSpecialLinesComponent(func() {}),
		statusLine:               NewStatusLine(nil, "", nil),
	}

	tui.SetTheme("dark")
	sv := m.buildChatViewport().Transcript
	if got := sv.Scrollbar(); got != "auto" {
		t.Fatalf("transcript scrollbar = %q, want the auto default", got)
	}
	darkTrack := sv.ScrollbarTrackStyle()("X")
	if want := tui.ActiveTheme().FgText("scrollbarTrack", "X"); darkTrack != want || darkTrack == "X" {
		t.Fatalf("dark track = %q, want live-themed %q", darkTrack, want)
	}
	if got, want := sv.ScrollbarThumbStyle()("X"), tui.ActiveTheme().FgText("scrollbarThumb", "X"); got != want {
		t.Fatalf("dark thumb = %q, want live-themed %q", got, want)
	}

	tui.SetTheme("light")
	lightTrack := sv.ScrollbarTrackStyle()("X")
	if lightTrack != tui.ActiveTheme().FgText("scrollbarTrack", "X") {
		t.Fatalf("light track = %q did not follow the live theme", lightTrack)
	}
	if darkTrack == lightTrack {
		t.Fatal("scrollbar track did not repaint on a live theme change")
	}
}

// TestCreateInteractiveTuiRoutesOsc8ClickToInjectedOpener drives the production
// createInteractiveTui factory end to end: it constructs the fullscreen renderer
// (with a buffered output seam), renders an actual OSC 8 hyperlink, and drives a
// real press and release. It asserts the injected opener: the dependency the
// factory wires onto the renderer, defaulting to openBrowser: receives the URL.
// This proves the composition root supplies the opener AND that a click reaches
// it through the real press-side lookup, without a test-only getter or a mutable
// package global. Dropping the OpenURL wiring from the factory makes this red.
func TestCreateInteractiveTuiRoutesOsc8ClickToInjectedOpener(t *testing.T) {
	const url = "https://example.com/docs"
	var opened []string

	m := newRunOnMainProbe(t)
	m.opts.Settings.TuiMode = "fullscreen"
	m.openURL = func(u string) error { opened = append(opened, u); return nil }
	m.rendererOut = &bytes.Buffer{}

	m.createInteractiveTui(context.Background())
	defer m.teardownCurrentTui()

	m.altScreen.Add(tui.NewText(tui.Hyperlink("docs", url)))
	m.altScreen.Start()
	m.altScreen.Render()

	// The single hyperlink renders as "docs" at the top-left; a real primary press
	// there must let the renderer's press-side lookup discover the URL, and the
	// release must route it to the injected opener (SGR mouse is 1-based).
	m.altScreen.HandleViewportInput("\x1b[<0;1;1M")
	m.altScreen.HandleViewportInput("\x1b[<0;1;1m")

	if len(opened) != 1 || opened[0] != url {
		t.Fatalf("injected opener invocations = %v, want [%s]", opened, url)
	}
}

// newSwitchTuiProbe builds an InteractiveMode wired for driving switchTuiMode
// hermetically: a buffered renderer output, the shared component containers, and
// an initial regular renderer mounted as Run would.
func newSwitchTuiProbe(t *testing.T) *InteractiveMode {
	t.Helper()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model, Settings: Settings{TuiMode: "regular"}})
	m.runCtx = context.Background()
	m.rendererOut = &bytes.Buffer{}
	m.editor = tui.NewEditor()
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.widgetContainer = tui.NewContainer()
	m.editorContainer = tui.NewContainer()
	m.editorContainer.Add(m.editor)
	m.extHeader = newSpecialLinesComponent(func() { m.tuiInst.Render() })
	m.extFooter = newSpecialLinesComponent(func() { m.tuiInst.Render() })
	m.statusLine = NewStatusLine(model, "", nil)
	m.createInteractiveTui(m.runCtx)
	m.mountInteractiveTui()
	return m
}

// TestSwitchTuiModeRoundTrip proves a live regular->fullscreen->regular swap:
// each transition rebuilds the renderer for the target mode and manages the
// fullscreen transcript view's lifetime.
func TestSwitchTuiModeRoundTrip(t *testing.T) {
	m := newSwitchTuiProbe(t)
	if m.altScreen != nil {
		t.Fatal("probe did not start in regular mode")
	}

	if !m.switchTuiMode("fullscreen", false) {
		t.Fatal("switch to fullscreen returned false")
	}
	if m.altScreen == nil || m.tuiInst != tui.Renderer(m.altScreen) {
		t.Fatal("fullscreen switch did not install the alt-screen renderer")
	}
	if m.transcriptScrollView == nil {
		t.Fatal("fullscreen switch did not build the transcript scroll view")
	}

	if !m.switchTuiMode("regular", false) {
		t.Fatal("switch back to regular returned false")
	}
	if m.altScreen != nil {
		t.Fatal("regular switch left a stale alt-screen renderer")
	}
	if m.transcriptScrollView != nil {
		t.Fatal("regular switch did not dispose/clear the transcript scroll view")
	}
	if _, ok := m.tuiInst.(*tui.TUI); !ok {
		t.Fatalf("regular switch did not install the main-screen renderer: %T", m.tuiInst)
	}
}

// TestSwitchTuiModeNoOpSameMode proves switching to the current mode is a no-op
// that succeeds without rebuilding the renderer.
func TestSwitchTuiModeNoOpSameMode(t *testing.T) {
	m := newSwitchTuiProbe(t)
	before := m.tuiInst
	if !m.switchTuiMode("regular", false) {
		t.Fatal("no-op switch returned false")
	}
	if m.tuiInst != before {
		t.Fatal("no-op switch rebuilt the renderer")
	}
}

// TestSwitchTuiModeRefusedWhileOverlayActive proves the swap is refused while an
// overlay is open (upstream hasOverlayEntries guard), leaving the renderer intact.
func TestSwitchTuiModeRefusedWhileOverlayActive(t *testing.T) {
	m := newSwitchTuiProbe(t)
	before := m.tuiInst
	m.tuiInst.OpenOverlay(tui.NewText("overlay"), tui.OverlayOptions{})
	if m.switchTuiMode("fullscreen", false) {
		t.Fatal("switch proceeded while an overlay was active")
	}
	if m.tuiInst != before || m.altScreen != nil {
		t.Fatal("refused switch still mutated the renderer")
	}
}

// TestSwitchTuiModeDynamicUIContextFollowsSwap proves the extension UI context -
// a renderer reference captured at construction: resolves the CURRENT renderer
// after a swap, not the stopped one (pig's race-safe Proxy-equivalent). This is
// the silent-breakage the disposition sweep surfaced. It observes the renderer
// withRenderer actually targets, so a static implementation (using u.tui) fails.
func TestSwitchTuiModeDynamicUIContextFollowsSwap(t *testing.T) {
	m := newSwitchTuiProbe(t)
	uiCtx := NewTUIUIContext(m.tuiInst)
	uiCtx.interactiveMode = m
	regularRenderer := m.tuiInst

	var target tui.Renderer
	uiCtx.withRenderer(func(r tui.Renderer) { target = r })
	if target != regularRenderer {
		t.Fatal("UI context did not resolve the initial renderer")
	}

	m.switchTuiMode("fullscreen", false)
	uiCtx.withRenderer(func(r tui.Renderer) { target = r })
	if target != m.tuiInst {
		t.Fatal("UI context still points at the stopped renderer after the swap")
	}
	if target == regularRenderer {
		t.Fatal("UI context resolved the pre-swap renderer, not the current one")
	}
	if uiCtx.tui != regularRenderer {
		t.Fatal("test premise: the captured renderer field should still hold the stopped renderer")
	}
}

// TestSwitchTuiModeShutdownAfterEachTransition proves teardown is safe after each
// transition: stopInteractiveTui tears down whichever renderer is current and is
// idempotent.
func TestSwitchTuiModeShutdownAfterEachTransition(t *testing.T) {
	for _, target := range []string{"fullscreen", "regular"} {
		m := newSwitchTuiProbe(t)
		if target == "regular" {
			m.switchTuiMode("fullscreen", false)
		}
		m.switchTuiMode(target, false)
		m.teardownCurrentTui()
		m.stopInteractiveTui()
		m.stopInteractiveTui() // idempotent
	}
}

// TestSwitchTuiModePreservesHeaderAndTranscript proves a live switch remounts the
// shared component tree, including the extension header and transcript, into fullscreen.
func TestSwitchTuiModePreservesHeaderAndTranscript(t *testing.T) {
	m := newSwitchTuiProbe(t)
	m.opts.LoginVisible = true
	if err := (&ExtUIContext{m: m}).SetLogin(loginHeaderDefinition("HEADER_MARKER_UNIQUE")); err != nil {
		t.Fatal(err)
	}
	m.chatContainer.Add(tui.NewText("TRANSCRIPT_MARKER_UNIQUE"))
	buf := m.rendererOut.(*bytes.Buffer)

	m.switchTuiMode("fullscreen", false)
	frame := stripANSITest(buf.String())
	if !strings.Contains(frame, "HEADER_MARKER_UNIQUE") {
		t.Fatalf("fullscreen frame dropped the header:\n%s", frame)
	}
	if !strings.Contains(frame, "TRANSCRIPT_MARKER_UNIQUE") {
		t.Fatalf("fullscreen frame dropped the transcript:\n%s", frame)
	}

	buf.Reset()
	m.switchTuiMode("regular", false)
	frame = strings.Join(m.layout.Render(80), "\n")
	if !strings.Contains(frame, "HEADER_MARKER_UNIQUE") {
		t.Fatalf("regular layout after round trip dropped the header:\n%s", frame)
	}
}

// TestSwitchTuiModeRestoresMainScreenStateAcrossRoundTrip proves the main-screen
// render state is PERSISTED on m across two switch calls (not lost in a local),
// then RESTORED so the return-to-regular render is differential against the
// pre-switch screen and does not re-dump the transcript. Local-only state or a
// missing restore makes the marker re-appear (full redraw).
func TestSwitchTuiModeRestoresMainScreenStateAcrossRoundTrip(t *testing.T) {
	m := newSwitchTuiProbe(t)
	m.chatContainer.Add(tui.NewText("STATE_MARKER_UNIQUE"))
	m.tuiInst.Render()
	buf := m.rendererOut.(*bytes.Buffer)

	if !m.switchTuiMode("fullscreen", false) {
		t.Fatal("switch to fullscreen failed")
	}
	if m.mainScreenRenderState == nil {
		t.Fatal("main-screen render state was not persisted on leaving regular")
	}

	buf.Reset()
	if !m.switchTuiMode("regular", false) {
		t.Fatal("switch back to regular failed")
	}
	if got := stripANSITest(buf.String()); strings.Contains(got, "STATE_MARKER_UNIQUE") {
		t.Fatalf("return-to-regular re-dumped the transcript; main-screen state not restored:\n%s", got)
	}
}

// TestSwitchTuiModeRefusalRevertsPersistedSetting drives the real settings
// callback: the generic handler persists the new tui-mode BEFORE OnSettingApplied,
// so when an overlay refuses the live switch the callback must revert the
// persisted value to the mode still in effect. Otherwise settings would claim a
// mode the renderer never entered.
func TestSwitchTuiModeRefusalRevertsPersistedSetting(t *testing.T) {
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	m := newSwitchTuiProbe(t)
	m.opts.SettingsManager = sm

	// The generic handler already persisted "fullscreen"; simulate that.
	if err := sm.SetTuiMode("fullscreen"); err != nil {
		t.Fatal(err)
	}
	// An overlay is active, so the live switch must be refused.
	m.tuiInst.OpenOverlay(tui.NewText("overlay"), tui.OverlayOptions{})

	m.buildSlashContext(t.Context()).OnSettingApplied("tui-mode", "fullscreen")

	if m.altScreen != nil {
		t.Fatal("refused switch still entered fullscreen")
	}
	if got := sm.GetTuiMode(); got != "regular" {
		t.Fatalf("persisted tui-mode = %q after refusal, want reverted to %q", got, "regular")
	}
}

// TestOnSettingAppliedTuiModeEntersFullscreen drives the production /settings
// path with no overlay active: the generic handler persists tui-mode, then
// OnSettingApplied("tui-mode", "fullscreen") must call switchTuiMode and install
// the alt-screen renderer live. This is the reachable settings-driven swap the
// live-toggle behavior depends on; only the refusal case was covered before.
func TestOnSettingAppliedTuiModeEntersFullscreen(t *testing.T) {
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	m := newSwitchTuiProbe(t)
	m.opts.SettingsManager = sm

	// The generic settings handler persists the new value before the callback.
	if err := sm.SetTuiMode("fullscreen"); err != nil {
		t.Fatal(err)
	}

	m.buildSlashContext(t.Context()).OnSettingApplied("tui-mode", "fullscreen")

	if m.altScreen == nil || m.tuiInst != tui.Renderer(m.altScreen) {
		t.Fatal("settings-driven tui-mode change did not install the alt-screen renderer")
	}
	if got := sm.GetTuiMode(); got != "fullscreen" {
		t.Fatalf("persisted tui-mode = %q after swap, want fullscreen", got)
	}

	// The reverse change swaps back to the main-screen renderer.
	if err := sm.SetTuiMode("regular"); err != nil {
		t.Fatal(err)
	}
	m.buildSlashContext(t.Context()).OnSettingApplied("tui-mode", "regular")
	if m.altScreen != nil {
		t.Fatal("settings-driven change back to regular left a stale alt-screen renderer")
	}
	if _, ok := m.tuiInst.(*tui.TUI); !ok {
		t.Fatalf("regular swap did not install the main-screen renderer: %T", m.tuiInst)
	}
}

// TestSwitchTuiModeConcurrentInvalidationRaceSafe overlaps off-owner-loop
// invalidations (as subprocess/extension goroutines produce) with a live switch
// under -race, proving the dynamic renderer access is synchronized and no
// invalidation lands on a renderer after its preserve-screen stop.
func TestSwitchTuiModeConcurrentInvalidationRaceSafe(t *testing.T) {
	m := newSwitchTuiProbe(t)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				m.requestRender()
				m.renderNow()
			}
		}
	}()

	for i := range 20 {
		mode := "fullscreen"
		if i%2 == 1 {
			mode = "regular"
		}
		m.switchTuiMode(mode, false)
	}
	close(stop)
	<-done
}

// TestSwitchTuiModeMountDoesNotReenterRendererLock is the fast-failing guard for
// the rendererMu non-reentrancy invariant. It wires the extension header/footer/
// runner slots with the PRODUCTION invalidate callback (m.renderNow, which takes
// rendererMu.RLock) rather than the probe's plain closure, then runs a full
// regular<->fullscreen round trip. switchTuiMode holds the write lock across the
// swap; if any code under it (createInteractiveTui/mountInteractiveTui/re-apply)
// ever pushes lines via specialLinesComponent.SetLines, its m.renderNow callback
// would RLock the held write lock and self-deadlock. A plain deadlock would hang
// the suite until the global timeout, so this runs the swap on a goroutine with a
// short deadline and fails loudly instead.
func TestSwitchTuiModeMountDoesNotReenterRendererLock(t *testing.T) {
	m := newSwitchTuiProbe(t)
	// Production-faithful wiring: these callbacks take rendererMu.RLock.
	m.extHeader = newSpecialLinesComponent(m.renderNow)
	m.extFooter = newSpecialLinesComponent(m.renderNow)
	// Give them content so a would-be SetLines under the lock has a live callback.
	m.extHeader.SetLines([]string{"header"})
	m.extFooter.SetLines([]string{"footer"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.switchTuiMode("fullscreen", false)
		m.switchTuiMode("regular", false)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("switchTuiMode did not complete: a call under the rendererMu write lock " +
			"reentered RLock (renderNow/requestRender/invalidate/withRenderer) and deadlocked")
	}
}

// TestSwitchTuiModeAltScreenEnterLeaveOrdering ports the portable contract of
// upstream interactive-tui.test.ts "selects the alternate-screen renderer only
// when requested" and "replaces the renderer while preserving components",
// adapted to pig's live switch. Upstream asserts createInteractiveTui enters the
// alternate screen (\x1b[?1049h) only for fullscreen; here the same gating is
// proven across a LIVE switchTuiMode round trip: regular start emits no alt-enter,
// regular->fullscreen emits alt-enter, and fullscreen->regular emits alt-leave
// (\x1b[?1049l). This is the deterministic alternate-screen-ordering oracle (no
// tmux) for the runtime renderer swap.
//
// N/A vs upstream: the upstream test also asserts getFocusedComponent()/
// component.focused are preserved across the swap. pig has no renderer-level focus
// model: input is routed structurally (overlay stack, then the editor in the
// interactive loop), so there is no focus pointer to capture/restore. Component
// preservation across the swap is covered by
// TestSwitchTuiModePreservesBannerAndTranscript; input routing survives because
// the same editor tree is remounted. The focus assertions are therefore an
// upstream TS-mechanism with no Go equivalent, not a pig defect.
func TestSwitchTuiModeAltScreenEnterLeaveOrdering(t *testing.T) {
	const enterAlt = "\x1b[?1049h"
	const leaveAlt = "\x1b[?1049l"

	m := newSwitchTuiProbe(t)
	buf := m.rendererOut.(*bytes.Buffer)

	// Regular start: the main-screen renderer must not enter the alternate screen.
	if strings.Contains(buf.String(), enterAlt) {
		t.Fatalf("regular start entered the alternate screen (%q emitted)", enterAlt)
	}

	buf.Reset()
	if !m.switchTuiMode("fullscreen", false) {
		t.Fatal("switch to fullscreen failed")
	}
	if m.altScreen == nil {
		t.Fatal("switch to fullscreen did not install the alt-screen renderer")
	}
	if !strings.Contains(buf.String(), enterAlt) {
		t.Fatalf("live regular->fullscreen switch did not enter the alternate screen (%q)", enterAlt)
	}

	buf.Reset()
	if !m.switchTuiMode("regular", false) {
		t.Fatal("switch back to regular failed")
	}
	if m.altScreen != nil {
		t.Fatal("switch back to regular left the alt-screen renderer installed")
	}
	if !strings.Contains(buf.String(), leaveAlt) {
		t.Fatalf("live fullscreen->regular switch did not leave the alternate screen (%q)", leaveAlt)
	}
}

// A message queued during compaction must still be sent when compaction ends
// after a turn has just finished.
//
// isIdle is reset through a queued runOnMain, so it reads stale-false for a
// window after the run goroutine exits. Auto-compaction that runs at the end of
// a turn lands in exactly that window. flushCompactionQueue used isIdle to
// decide whether a turn was running, so it took the steer path, and steering
// only drains inside a running turn: with the goroutine already gone the
// message reached neither the UI nor the session, and a restart lost it.
//
// turnActive is the signal that means what this decision needs, marking exactly
// the interval in which a run goroutine exists to drain the queue.
func TestCompactionFlushStartsTurnWhenIsIdleIsStaleFalse(t *testing.T) {
	dir := t.TempDir()
	seen := make(chan capturedStreamRequest, 1)
	model := &ai.Model{
		ID:           "capture-stale",
		DisplayName:  "capture-stale",
		Provider:     captureStreamOptionsProvider{seen: seen},
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	m := NewInteractiveMode(InteractiveOptions{CWD: dir, Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.runCtx = context.Background()
	m.abortCtx = context.Background()
	m.abortFn = func() {}

	// The state auto-compaction leaves at the end of a turn: the run goroutine
	// has exited, so nothing will drain a steering queue, but the queued
	// runOnMain that restores isIdle has not run yet.
	m.isIdle = false
	m.turnActive.Store(false)

	m.compactionQueue = []compactionQueuedMessage{{text: "sent after compaction", mode: compactionQueueSteer}}
	m.flushCompactionQueue(m.runCtx, true)

	select {
	case opts := <-seen:
		if got := lastUserMessageText(t, opts.Messages); got != "sent after compaction" {
			t.Fatalf("turn started with wrong prompt: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no turn started: the queued message was stranded in a queue with no goroutine to drain it")
	}
	if len(m.compactionQueue) != 0 {
		t.Fatalf("queue should be cleared after flush, got %v", m.compactionQueue)
	}
}

// Extension-delivered user messages must reach a turn even when the previous
// turn has only just ended.
//
// The run goroutine clears turnActive and only then queues the cleanup that
// restores isIdle, so a UI task landing in that window sees isIdle false with
// no goroutine left to drain a steering queue. Deciding on isIdle sent the
// message to Steer/FollowUp and it was never delivered; deciding on turnActive
// starts a turn, which is what "no turn is running" has to mean.
func TestDeliverUserMessageStartsTurnWhenIsIdleIsStaleFalse(t *testing.T) {
	seen := make(chan capturedStreamRequest, 1)
	model := &ai.Model{
		ID:           "deliver-stale",
		DisplayName:  "deliver-stale",
		Provider:     captureStreamOptionsProvider{seen: seen},
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	m.keybindings = DefaultKeybindingsManager()
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.runCtx = context.Background()
	m.abortCtx = context.Background()
	m.abortFn = func() {}

	// The window after a turn's goroutine exits but before its cleanup runs.
	m.isIdle = false
	m.turnActive.Store(false)

	if err := m.deliverUserMessage("from an extension", extension.DeliverAsFollowUp); err != nil {
		t.Fatalf("deliverUserMessage: %v", err)
	}

	// Drain the UI task the delivery posted, as the main input loop would.
	select {
	case fn := <-m.uiTaskCh:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("no UI task was posted")
	}

	select {
	case opts := <-seen:
		if got := lastUserMessageText(t, opts.Messages); got != "from an extension" {
			t.Fatalf("turn started with wrong prompt: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no turn started: the message was handed to a queue with no goroutine to drain it")
	}
}

// A turn genuinely in flight must absorb the message instead of racing a second
// concurrent turn against the agent loop.
func TestDeliverUserMessageQueuesWhileATurnIsRunning(t *testing.T) {
	seen := make(chan capturedStreamRequest, 1)
	model := &ai.Model{
		ID:           "deliver-running",
		DisplayName:  "deliver-running",
		Provider:     captureStreamOptionsProvider{seen: seen},
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model})
	m.chatContainer = tui.NewContainer()
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 30)
	m.statusLine = NewStatusLine(model, "", nil)
	m.keybindings = DefaultKeybindingsManager()
	m.agent = agent.NewAgent(agent.AgentOptions{Model: model})
	m.runCtx = context.Background()
	m.abortCtx = context.Background()
	m.abortFn = func() {}

	m.isIdle = true // stale in the other direction
	m.turnActive.Store(true)

	if err := m.deliverUserMessage("while running", extension.DeliverAsFollowUp); err != nil {
		t.Fatalf("deliverUserMessage: %v", err)
	}
	select {
	case fn := <-m.uiTaskCh:
		fn()
	case <-time.After(2 * time.Second):
		t.Fatal("no UI task was posted")
	}

	select {
	case opts := <-seen:
		t.Fatalf("started a concurrent turn while one was running: %q",
			lastUserMessageText(t, opts.Messages))
	case <-time.After(200 * time.Millisecond):
	}
}

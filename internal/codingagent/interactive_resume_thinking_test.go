package codingagent

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// resumeThinkingMode persists messages to a session file, reloads the file
// from disk (the resume path), and returns a mode ready to render it.
func resumeThinkingMode(t *testing.T, hide bool, messages ...agent.AgentMessage) *InteractiveMode {
	t.Helper()
	dir := t.TempDir()
	sm := NewSessionManagerWithDir(dir, dir)
	sess, err := sm.Create("resume-thinking", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range messages {
		if _, err := sess.AppendMessage(msg); err != nil {
			t.Fatal(err)
		}
	}
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
		hideThinking:  hide,
		outputPad:     1,
	}
	m.tuiInst.Add(m.chatContainer)
	return m
}

func userMsg(text string) agent.AgentMessage {
	return agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
}

func assistantMsg(thinking string, content ...ai.AssistantContentBlock) agent.AgentMessage {
	return agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: agent.RoleAssistant, Content: content, Thinking: thinking, StopReason: ai.StopReasonStop,
	}}
}

func renderedChat(m *InteractiveMode) string {
	return stripANSITest(strings.Join(m.chatContainer.Render(80), "\n"))
}

// Ports packages/coding-agent/test/suite/regressions/8611-thinking-toggle-pending-bash-output.test.ts:26.
// Pi: packages/coding-agent/src/core/settings-manager.ts:1046 (SettingsManager.getHideThinkingBlock).
func TestInteractiveModeThinkingTogglePreservesPartialBashOutputUpstream(t *testing.T) {
	for _, path := range []string{"component", "agent events and Ctrl+T"} {
		t.Run(path, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, _ := newTickRenderProbe(t, "regular")
				agentDir := t.TempDir()
				m.opts.SettingsManager = NewSettingsManager(m.opts.CWD, agentDir)
				args := json.RawMessage(`{"command":"echo first; sleep 10"}`)
				var component *tui.ToolExecutionComponent
				if path == "component" {
					component = newToolCardForTest("bash", tui.HeaderForTool("bash", args, m.opts.CWD))
					component.ShowImages = false
					component.Cwd = m.opts.CWD
					component.SetHeaderArgs(args)
					component.MarkExecutionStarted()
					component.SetResultValue(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "first"}}, IsError: false})
					component.SetStreaming("first")
					m.chatContainer.Add(component)
				} else {
					m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "tool-8611", ToolName: "bash", Args: args})
					m.handleAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "tool-8611", ToolName: "bash", PartialResult: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "first"}}}})
					component = m.toolByID["tool-8611"]
				}
				before := m.chatContainer.Render(120)
				if chat := stripANSITest(strings.Join(before, "\n")); !strings.Contains(chat, "first") {
					t.Fatalf("partial output missing before toggle:\n%s", chat)
				}
				if path == "component" {
					m.toggleThinkingVisibility()
				} else if err := m.handleKey(t.Context(), "\x14"); err != nil {
					t.Fatal(err)
				}
				if !m.hideThinking || !m.opts.SettingsManager.GetHideThinkingBlock() {
					t.Fatal("toggle did not set hideThinkingBlock to true")
				}
				if !NewSettingsManager(m.opts.CWD, agentDir).GetHideThinkingBlock() {
					t.Fatal("hideThinkingBlock was not persisted")
				}
				if !slices.Contains(m.chatContainer.Children(), tui.Component(component)) {
					t.Fatal("toggle removed or replaced the running bash component")
				}
				// The command header also contains "first". Check retained output and the exact frame so the header cannot hide dropped partial output.
				if component == nil || component.Output != "first" || component.State != tui.ToolStateRunning || !component.IsPartial {
					t.Fatalf("toggle changed pending bash state: %+v", component)
				}
				if after := m.chatContainer.Render(120); !slices.Equal(after, before) {
					t.Fatalf("toggle changed bash rendering:\nbefore: %q\nafter: %q", before, after)
				}
			})
		})
	}
}

// Upstream renderInitialMessages/rebuildChatFromMessages render each assistant
// message through AssistantMessageComponent, which reads thinking from the
// persisted content blocks. Pig's runtime-only AssistantMessage.Thinking is
// not serialized, so a rebuild must not depend on it.
func TestInteractiveMode_ResumeRendersPersistedThinking(t *testing.T) {
	m := resumeThinkingMode(t, false,
		userMsg("question"),
		assistantMsg("PERSISTED_THOUGHT",
			ai.ThinkingContent{Thinking: "PERSISTED_THOUGHT"},
			ai.TextContent{Text: "FINAL_ANSWER"}),
	)
	m.renderSessionEntries()
	got := renderedChat(m)
	if !strings.Contains(got, "PERSISTED_THOUGHT") {
		t.Fatalf("resumed chat dropped persisted thinking:\n%s", got)
	}
	if strings.Index(got, "PERSISTED_THOUGHT") > strings.Index(got, "FINAL_ANSWER") {
		t.Fatalf("thinking must render before the answer:\n%s", got)
	}

	m.toggleThinkingVisibility()
	hidden := renderedChat(m)
	if strings.Contains(hidden, "PERSISTED_THOUGHT") || !strings.Contains(hidden, "Thinking...") {
		t.Fatalf("hide-thinking toggle did not collapse resumed thinking:\n%s", hidden)
	}
}

func TestInteractiveMode_ResumeHonorsHideThinkingSetting(t *testing.T) {
	m := resumeThinkingMode(t, true,
		userMsg("question"),
		assistantMsg("", ai.ThinkingContent{Thinking: "ONLY_THINKING"}),
	)
	m.renderSessionEntries()
	got := renderedChat(m)
	if strings.Contains(got, "ONLY_THINKING") || !strings.Contains(got, "Thinking...") {
		t.Fatalf("hidden thinking-only message should show the hidden label:\n%s", got)
	}
	m.toggleThinkingVisibility()
	if got := renderedChat(m); !strings.Contains(got, "ONLY_THINKING") {
		t.Fatalf("revealing thinking on a resumed thinking-only message showed nothing:\n%s", got)
	}
}

// AssistantMessageComponent.updateContent renders content in order, trims
// blocks, joins consecutive thinking blocks with a blank line, and adds a
// spacer after a thinking run only when visible content follows.
func TestInteractiveMode_ResumeThinkingFollowsContentOrder(t *testing.T) {
	m := resumeThinkingMode(t, false,
		userMsg("question"),
		assistantMsg("",
			ai.ThinkingContent{Thinking: "  THINK_A  "},
			ai.ThinkingContent{Thinking: "THINK_B\n"},
			ai.TextContent{Text: "TEXT_C"},
			ai.ThinkingContent{Thinking: "THINK_D"},
			ai.ThinkingContent{Thinking: "   "},
		),
	)
	m.renderSessionEntries()
	if len(m.assistantBlocks) != 1 {
		t.Fatalf("assistant blocks = %d, want 1", len(m.assistantBlocks))
	}
	lines := m.assistantBlocks[0].Render(80)
	for i := range lines {
		lines[i] = strings.TrimRight(stripANSITest(lines[i]), " ")
	}
	want := []string{"\x1b]133;A\x07", " THINK_A", "", " THINK_B", "", " TEXT_C", "\x1b]133;B\x07\x1b]133;C\x07 THINK_D"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rendered lines:\n%q\nwant:\n%q", lines, want)
	}
}

// Upstream addMessageToChat adds Spacer(1) before every user message once the
// chat has children, in addition to the user box's own vertical padding.
func TestInteractiveMode_ResumeSpacesUserMessageAfterAssistant(t *testing.T) {
	m := resumeThinkingMode(t, false,
		userMsg("FIRST_USER"),
		assistantMsg("", ai.TextContent{Text: "ANSWER"}, ai.ThinkingContent{Thinking: "TRAILING_THOUGHT"}),
		userMsg("SECOND_USER"),
	)
	m.renderSessionEntries()
	lines := m.chatContainer.Render(40)
	for i := range lines {
		lines[i] = strings.TrimSpace(stripANSITest(lines[i]))
	}
	want := []string{"\x1b]133;A\x07", "FIRST_USER", "\x1b]133;B\x07\x1b]133;C\x07", "\x1b]133;A\x07", "ANSWER", "\x1b]133;B\x07\x1b]133;C\x07 TRAILING_THOUGHT", "", "\x1b]133;A\x07", "SECOND_USER", "\x1b]133;B\x07\x1b]133;C\x07"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("resumed chat lines:\n%q\nwant:\n%q", lines, want)
	}
}

// interactive-mode.ts setHiddenThinkingLabel: the extension label replaces "Thinking..." in every assistant block already in the chat and in blocks created afterwards; an empty label restores the default.
func TestInteractiveModeSetHiddenThinkingLabelReachesExistingAndLaterBlocks(t *testing.T) {
	m := resumeThinkingMode(t, true,
		userMsg("question"),
		assistantMsg("", ai.ThinkingContent{Thinking: "FIRST_THOUGHT"}),
	)
	m.renderSessionEntries()
	if got := renderedChat(m); !strings.Contains(got, "Thinking...") {
		t.Fatalf("default label missing:\n%s", got)
	}

	(&ExtUIContext{m: m}).SetHiddenThinkingLabel("Pondering")
	got := renderedChat(m)
	if !strings.Contains(got, "Pondering") || strings.Contains(got, "Thinking...") {
		t.Fatalf("existing block kept the old label:\n%s", got)
	}

	later := m.newAssistantMessageBlock()
	later.SetContent([]tui.AssistantSegment{{Thinking: true, Text: "SECOND_THOUGHT"}})
	if rendered := stripANSITest(strings.Join(later.Render(80), "\n")); !strings.Contains(rendered, "Pondering") || strings.Contains(rendered, "SECOND_THOUGHT") {
		t.Fatalf("a block created after the label was set rendered:\n%s", rendered)
	}

	(&ExtUIContext{m: m}).SetHiddenThinkingLabel("")
	if got := renderedChat(m); !strings.Contains(got, "Thinking...") || strings.Contains(got, "Pondering") {
		t.Fatalf("an empty label did not restore the default:\n%s", got)
	}
}

// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
)

type suiteLargeTool struct {
	text      string
	terminate bool
}

func (t *suiteLargeTool) Name() string {
	if t.terminate {
		return "terminate_with_large_result"
	}
	return "large_result"
}
func (t *suiteLargeTool) Label() string {
	if t.terminate {
		return "Terminate with large result"
	}
	return "Large result"
}
func (t *suiteLargeTool) Schema() ai.ToolSchema {
	description := "Returns enough content to cross the compaction threshold"
	if t.terminate {
		description += ", then terminates"
	}
	return ai.ToolSchema{Name: t.Name(), Description: description, Parameters: ai.JsonObject{"type": "object", "properties": ai.JsonObject{}}}
}
func (*suiteLargeTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (t *suiteLargeTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: t.text}}, Details: map[string]any{}, Terminate: t.terminate}, nil
}

func TestAgentSessionCompactionSuiteActiveRunUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:476 (both table rows)
	for _, modelOverride := range []bool{false, true} {
		t.Run(fmt.Sprintf("compacts after oversized tool result in same run model override %v", modelOverride), func(t *testing.T) {
			settings := `{"compaction":{"enabled":true,"reserveTokens":400,"keepRecentTokens":1750}}`
			if modelOverride {
				settings = `{"compaction":{"enabled":true,"reserveTokens":0,"keepRecentTokens":20000,"modelOverrides":{"faux/faux-1":{"reserveTokens":400,"keepRecentTokens":1750}}}}`
			}
			tool := &suiteLargeTool{text: "large-tool-result:" + strings.Repeat("x", 8000)}
			var order []string
			var observed []compaction.CompactionSettings
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"session_before_compact": {func(args ...any) (any, error) {
				order = append(order, "compaction")
				prep := args[0].(extension.SessionBeforeCompactEvent).Preparation.(*compaction.CompactionPreparation)
				observed = append(observed, prep.Settings)
				return extension.SessionBeforeCompactResult{Compaction: map[string]any{"summary": "compacted history", "firstKeptEntryId": prep.FirstKeptEntryID, "tokensBefore": prep.TokensBefore, "details": map[string]any{}}}, nil
			}}}}
			resumed := ""
			h := newCompactionSuiteHarness(t, harnessOptions{contextWindow: 2600, maxTokens: 100, settings: settings, tools: []agent.AgentTool{tool}, extension: ext}, fauxReply("old-history:"+strings.Repeat("a", 800), ai.StopReasonStop, 0), fauxReply("recent-history:"+strings.Repeat("b", 800), ai.StopReasonStop, 0), fauxToolCall(tool.Name()), func(request []ai.Message) *ai.AssistantMessage {
				order = append(order, "provider")
				resumed = suiteJSON(t, request)
				return fauxReply("finished after compaction", ai.StopReasonStop, 0)(request)
			})
			starts := 0
			var lastStart agent.CompactionStartEvent
			unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
				switch event := event.(type) {
				case agent.AgentStartEvent:
					starts++
				case agent.CompactionStartEvent:
					lastStart = event
				}
			})
			defer unsubscribe()
			for _, prompt := range []string{"seed old history", "seed recent history"} {
				if _, err := h.session.Send(t.Context(), prompt); err != nil {
					t.Fatal(err)
				}
			}
			before := starts
			if _, err := h.session.Send(t.Context(), "run the large tool"); err != nil {
				t.Fatal(err)
			}
			if len(order) < 2 || !reflect.DeepEqual(order[:2], []string{"compaction", "provider"}) || len(observed) == 0 || observed[0] != (compaction.CompactionSettings{Enabled: true, ReserveTokens: 400, KeepRecentTokens: 1750}) || starts != before+1 || lastStart.Reason != "threshold" || !strings.Contains(resumed, "compacted history") || !strings.Contains(resumed, "large-tool-result") || suiteLastText(h.session) != "finished after compaction" {
				t.Fatalf("order=%v settings=%v starts=%d before=%d start=%+v resumed=%s", order, observed, starts, before, lastStart, resumed)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:550
	t.Run("includes steering queued during compaction in resumed assistant request", func(t *testing.T) {
		started, released := make(chan struct{}), make(chan struct{})
		tool := &suiteLargeTool{text: "large-tool-result:" + strings.Repeat("x", 6800)}
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"session_before_compact": {func(args ...any) (any, error) {
			close(started)
			<-released
			prep := args[0].(extension.SessionBeforeCompactEvent).Preparation.(*compaction.CompactionPreparation)
			return extension.SessionBeforeCompactResult{Compaction: map[string]any{"summary": "compacted history", "firstKeptEntryId": prep.FirstKeptEntryID, "tokensBefore": prep.TokensBefore, "details": map[string]any{}}}, nil
		}}}}
		resumed := ""
		h := newCompactionSuiteHarness(t, harnessOptions{contextWindow: 2600, maxTokens: 100, settings: `{"compaction":{"enabled":true,"reserveTokens":400,"keepRecentTokens":1750}}`, tools: []agent.AgentTool{tool}, extension: ext}, fauxReply("old-history:"+strings.Repeat("a", 800), ai.StopReasonStop, 0), fauxReply("recent-history:"+strings.Repeat("b", 800), ai.StopReasonStop, 0), fauxToolCall(tool.Name()), func(request []ai.Message) *ai.AssistantMessage {
			resumed = suiteJSON(t, request)
			return fauxReply("finished after compaction", ai.StopReasonStop, 0)(request)
		}, fauxReply("finished after delayed steering", ai.StopReasonStop, 0))
		for _, prompt := range []string{"seed old history", "seed recent history"} {
			if _, err := h.session.Send(t.Context(), prompt); err != nil {
				t.Fatal(err)
			}
		}
		done := make(chan error, 1)
		go func() { _, err := h.session.Send(t.Context(), "run the large tool"); done <- err }()
		select {
		case <-started:
		case err := <-done:
			t.Fatalf("run ended before compaction: %v", err)
		}
		if err := h.session.Steer(t.Context(), "change direction", nil, nil); err != nil {
			t.Fatal(err)
		}
		close(released)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(resumed, "change direction") || h.provider.callCount() != 4 {
			t.Fatalf("requests=%d resumed=%s", h.provider.callCount(), resumed)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:615
	t.Run("does not compact after terminating tool result", func(t *testing.T) {
		tool := &suiteLargeTool{text: "large-tool-result:" + strings.Repeat("x", 6800), terminate: true}
		h := newCompactionSuiteHarness(t, harnessOptions{contextWindow: 2600, maxTokens: 100, settings: `{"compaction":{"enabled":true,"reserveTokens":400,"keepRecentTokens":1750}}`, tools: []agent.AgentTool{tool}, extension: suiteSummaryExtension("unexpected compaction", nil)}, fauxReply("old-history:"+strings.Repeat("a", 800), ai.StopReasonStop, 0), fauxReply("recent-history:"+strings.Repeat("b", 800), ai.StopReasonStop, 0), fauxToolCall(tool.Name()))
		starts := 0
		unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.CompactionStartEvent); ok {
				starts++
			}
		})
		defer unsubscribe()
		for _, prompt := range []string{"seed old history", "seed recent history", "run the terminating tool"} {
			if _, err := h.session.Send(t.Context(), prompt); err != nil {
				t.Fatal(err)
			}
		}
		if starts != 0 || len(suiteCompactions(t, h.session)) != 0 || h.provider.callCount() != 3 {
			t.Fatalf("starts=%d requests=%d", starts, h.provider.callCount())
		}
	})
}

func TestAgentSessionCompactionSuiteAbortUpstream(t *testing.T) {
	for _, test := range []struct {
		name         string
		wholeSession bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:733
		{"cancels in-progress manual compaction when abortCompaction is called", false},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:746
		{"aborts manual compaction and waits until the session is idle", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{})
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"session_before_compact": {func(args ...any) (any, error) {
				event := args[0].(extension.SessionBeforeCompactEvent)
				close(started)
				<-event.Signal.Done()
				return extension.SessionBeforeCompactResult{Cancel: true}, nil
			}}}}
			h := newCompactionSuiteHarness(t, harnessOptions{settings: `{"compaction":{"keepRecentTokens":1}}`, extension: ext}, fauxReply("continued", ai.StopReasonStop, 0))
			suiteCompactionSeed(t, h.session)
			var end agent.CompactionEndEvent
			unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
				if value, ok := event.(agent.CompactionEndEvent); ok {
					end = value
				}
			})
			defer unsubscribe()
			done := make(chan error, 1)
			go func() { _, err := h.session.CompactResult(t.Context(), ""); done <- err }()
			<-started
			if test.wholeSession {
				if err := h.session.Abort(t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				h.session.AbortCompaction()
			}
			if err := <-done; err == nil || !strings.Contains(err.Error(), "Compaction cancelled") {
				t.Fatalf("error=%v", err)
			}
			if test.wholeSession {
				if end.Reason != "manual" || !end.Aborted || h.session.IsCompacting() || !h.session.IsIdle() {
					t.Fatalf("end=%+v compacting=%v idle=%v", end, h.session.IsCompacting(), h.session.IsIdle())
				}
				if _, err := h.session.Send(t.Context(), "next prompt"); err != nil {
					t.Fatal(err)
				}
				if suiteLastText(h.session) != "continued" {
					t.Fatal(suiteLastText(h.session))
				}
			}
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/agent-session-compaction.test.ts:189
func TestCompactionSuiteCheckpointsSystemPatches(t *testing.T) {
	h := newCompactionSuiteHarness(t, harnessOptions{tools: []agent.AgentTool{&fakeTool{name: "read"}, &fakeTool{name: "bash"}, &fakeTool{name: "edit"}, &fakeTool{name: "write"}}}, fauxReply("declared", ai.StopReasonStop, 0))
	if _, err := h.session.Send(t.Context(), "declare the prompt"); err != nil {
		t.Fatal(err)
	}
	declared := h.session.Messages()[0].System
	if declared == nil {
		t.Fatal("expected declared system message")
	}
	now := time.Now().UnixMilli()
	persistQueueMessage(t, h.session, agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("summarized instruction"), Sections: ai.OrderedSections{{Name: "early", Value: new("<early>1</early>")}}, ToolsRemoved: []ai.ToolReference{{Name: "bash"}}, Timestamp: now}})
	first := persistQueueMessage(t, h.session, queueUser("kept before patch", now))
	persistQueueMessage(t, h.session, agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText("retained instruction"), Sections: ai.OrderedSections{{Name: "extra", Value: new("<extra>late</extra>")}}, ToolsRemoved: []ai.ToolReference{{Name: "read"}}, Timestamp: now}})
	persistQueueMessage(t, h.session, queueUser("kept after patch", now))
	if _, err := h.session.inner.AppendCompaction("compacted", first, 100, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	messages := h.session.inner.BuildSessionProjection().Messages
	if len(messages) != 4 || messages[0].System == nil || messages[1].Custom["role"] != "compactionSummary" || messages[2].User == nil || messages[3].User == nil {
		t.Fatal(messages)
	}
	checkpoint := messages[0].System
	if checkpoint.Content != ai.SystemText("summarized instruction\n\nretained instruction") {
		t.Fatal(checkpoint.Content)
	}
	sections := append(ai.OrderedSections(nil), declared.Sections...)
	sections = append(sections, ai.PromptSection{Name: "early", Value: new("<early>1</early>")}, ai.PromptSection{Name: "extra", Value: new("<extra>late</extra>")})
	if !reflect.DeepEqual(checkpoint.Sections, sections) {
		t.Fatalf("sections=%+v want=%+v", checkpoint.Sections, sections)
	}
	var tools, want []string
	for _, tool := range checkpoint.ToolsAdded {
		tools = append(tools, tool.Name)
	}
	for _, name := range h.session.ActiveToolNames() {
		if name != "read" && name != "bash" {
			want = append(want, name)
		}
	}
	if !reflect.DeepEqual(tools, want) {
		t.Fatalf("tools=%v want=%v", tools, want)
	}
}

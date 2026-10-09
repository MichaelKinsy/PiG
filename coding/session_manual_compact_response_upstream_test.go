// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type compactionNoopTool struct{}

func (compactionNoopTool) Name() string                           { return "noop" }
func (compactionNoopTool) Label() string                          { return "No-op" }
func (compactionNoopTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (compactionNoopTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "noop", Description: "Return immediately", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}
}
func (compactionNoopTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{}}, nil
}

type manualDuringResponseProvider struct {
	calls    atomic.Int32
	started  chan context.Context
	released chan struct{}
}

func (*manualDuringResponseProvider) ID() string   { return "faux" }
func (*manualDuringResponseProvider) Close() error { return nil }
func (p *manualDuringResponseProvider) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	message := &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "noop-call", Name: "noop", Arguments: ai.JsonObject{}}}, Timestamp: time.Now().UnixMilli()}
	if p.calls.Add(1) > 1 {
		p.started <- ctx
		<-p.released
		message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "second response:" + strings.Repeat("x", 4000)}}
		message.StopReason = ai.StopReasonStop
		if ctx.Err() != nil {
			message.Content = []ai.AssistantContentBlock{}
			message.StopReason = ai.StopReasonAborted
			message.ErrorMessage = "Operation aborted"
		}
	}
	if message.StopReason == ai.StopReasonAborted {
		return newSessionTestStream(ai.StartEvent{Partial: message}, ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: message}), nil
	}
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: message.StopReason, Message: message}), nil
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7253-manual-compact-during-response.test.ts:26
// Pi: packages/coding-agent/src/core/settings-manager.ts:19 (CompactionSettingsJSON.reserveTokens); packages/coding-agent/src/core/settings-manager.ts:20 (CompactionSettingsJSON.keepRecentTokens); packages/coding-agent/src/core/settings-manager.ts:29 (CompactionSettingsJSON.enabled).
func TestManualCompactPersistsAbortedResponseBeforeCompaction(t *testing.T) {
	provider := &manualDuringResponseProvider{started: make(chan context.Context, 1), released: make(chan struct{})}
	model := fakeModelWithProvider(provider)
	model.ID = "faux-1"
	model.Capabilities.ContextWindow = 1000
	model.Capabilities.MaxOutputTokens = 1000
	services := newTestServicesWithSettings(t, icodingagent.Settings{Compaction: &icodingagent.CompactionSettingsJSON{Enabled: new(true), ReserveTokens: new(200.), KeepRecentTokens: new(2.)}})
	s, err := NewSession(services, SessionOptions{Model: model, SkipBuiltinTools: true, Tools: []agent.AgentTool{compactionNoopTool{}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	var recorded []recordedCompactionEvent
	withTreeHandlers(s, t, summaryOverrideHandlers("manual summary", &recorded))
	promptDone := make(chan error, 1)
	go func() { _, err := s.Send(t.Context(), "Run the tool, then continue responding."); promptDone <- err }()
	var responseContext context.Context
	select {
	case responseContext = <-provider.started:
	case err := <-promptDone:
		t.Fatalf("prompt ended before second response: %v", err)
	}
	type compactOutcome struct {
		result *CompactionResult
		err    error
	}
	compactDone := make(chan compactOutcome, 1)
	go func() { result, err := s.Compact(t.Context(), ""); compactDone <- compactOutcome{result, err} }()
	// Pi starts abort synchronously before compact() returns its promise. Wait for
	// that observable initiation before releasing the Go provider factory.
	<-responseContext.Done()
	close(provider.released)
	if err := <-promptDone; err != nil {
		t.Fatal(err)
	}
	out := <-compactDone
	if out.err != nil || out.result == nil || out.result.Summary != "manual summary" {
		t.Fatalf("result=%+v error=%v", out.result, out.err)
	}
	var starts, ends []string
	for _, event := range drainEvents(t, s) {
		switch event := event.(type) {
		case agent.CompactionStartEvent:
			starts = append(starts, event.Reason)
		case agent.CompactionEndEvent:
			ends = append(ends, event.Reason)
		}
	}
	if !reflect.DeepEqual(starts, []string{"manual"}) || !reflect.DeepEqual(ends, []string{"manual"}) {
		t.Fatalf("starts=%v ends=%v", starts, ends)
	}
	aborted, compacted, count := -1, -1, 0
	for i, entry := range s.inner.GetEntries() {
		if entry.Base().Type == "compaction" {
			compacted = i
			count++
		}
		if m, ok := entry.(icodingagent.MessageEntry); ok && m.Message.Assistant != nil && m.Message.Assistant.StopReason == ai.StopReasonAborted {
			aborted = i
		}
	}
	if aborted < 0 || compacted <= aborted || count != 1 {
		t.Fatalf("aborted=%d compaction=%d count=%d", aborted, compacted, count)
	}
}

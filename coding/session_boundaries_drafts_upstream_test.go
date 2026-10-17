package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi: packages/coding-agent/src/core/extensions/types.ts:348 (Context.isIdle); packages/coding-agent/src/core/extensions/types.ts:974 (BoundaryContextPreview.contextMessages); packages/coding-agent/src/core/extensions/types.ts:976 (BoundaryContextPreview.pendingMessages).
func TestUpstreamSessionBoundariesDrafts(t *testing.T) {
	for _, queueKind := range []string{"steering", "follow-up", "both"} {
		name := fmt.Sprintf("preserves %s queue scheduling around a turn_end handoff", queueKind)
		t.Run(name, func(t *testing.T) {
			handled := false
			requests := []string{}
			var h *recoveryHarness
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(...any) (any, error) {
				if handled {
					return nil, nil
				}
				handled = true
				if queueKind == "steering" || queueKind == "both" {
					if err := h.session.SendExtensionUserMessage("queued steering", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsSteer}); err != nil {
						t.Error(err)
					}
				}
				if queueKind == "follow-up" || queueKind == "both" {
					if err := h.session.SendExtensionUserMessage("queued follow-up", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp}); err != nil {
						t.Error(err)
					}
				}
				return boundaryDrafts(true, extension.SessionBoundaryDraft{Type: "compaction", Summary: "exact handoff", FirstKeptEntryID: nil}), nil
			}}}}
			h = newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryCapture(t, &requests, "second"), boundaryCapture(t, &requests, "third"))
			boundaryRecord(t, h, 71, name)
			boundaryPrompt(t, h, "start")
			if len(requests) == 0 {
				t.Fatal("missing continuation request")
			}
			boundaryContains(t, requests[0], "exact handoff", true)
			wantCalls := 2
			if queueKind == "both" {
				wantCalls = 3
			}
			if got := h.provider.callCount(); got != wantCalls {
				t.Errorf("callCount=%d, want %d", got, wantCalls)
			}
			switch queueKind {
			case "steering":
				boundaryContains(t, requests[0], "queued steering", true)
				boundaryContains(t, requests[0], "queued follow-up", false)
			case "follow-up":
				boundaryContains(t, requests[0], "queued follow-up", true)
			case "both":
				boundaryContains(t, requests[0], "queued steering", true)
				boundaryContains(t, requests[0], "queued follow-up", false)
				if len(requests) < 2 {
					t.Fatal("missing follow-up request")
				}
				boundaryContains(t, requests[1], "queued follow-up", true)
			}
		})
	}
	for _, replacement := range []bool{true, false} {
		site, name, instruction, prompt := 182, "keeps boundary input verbatim through threshold compaction when metadata follows it", strings.Repeat("EXACT-UNSENT-INSTRUCTION ", 100), strings.Repeat("old input ", 500)
		if replacement {
			site, name, instruction, prompt = 128, "keeps a boundary replacement verbatim through threshold compaction", strings.Repeat("EXACT-REPLACEMENT-INSTRUCTION ", 100), "original input"
		}
		t.Run(name, func(t *testing.T) {
			handled := false
			requests := []string{}
			var h *recoveryHarness
			ext := summaryFromPreparation("older history summary")
			ext.Handlers["turn_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
				if handled {
					return nil, nil
				}
				handled = true
				entries := []extension.SessionBoundaryDraft{{Type: "custom_message", CustomType: "next-work", Content: instruction, Display: false}}
				if replacement {
					event := args[0].(extension.TurnEndEvent)
					entries = []extension.SessionBoundaryDraft{{Type: "context_edit", TargetID: boundaryLastUser(t, h), Replacement: json.RawMessage(boundaryJSON(t, map[string]any{"content": instruction}))}, {Type: "context_edit", TargetID: event.MessageEntryID}}
				}
				entries = append(entries, extension.SessionBoundaryDraft{Type: "custom", CustomType: "bookkeeping", Data: map[string]any{"source": "test"}})
				return boundaryDrafts(true, entries...), nil
			}}
			first, second := "first", "second"
			if replacement {
				first, second = "answered original input", "answered replacement"
			}
			h = newBoundaryHarness(t, harnessOptions{contextWindow: 2000, maxTokens: 100, settings: `{"compaction":{"enabled":true,"keepRecentTokens":1,"reserveTokens":0}}`, extension: ext}, boundaryReply(first, ai.StopReasonStop, 0), boundaryCapture(t, &requests, second))
			boundaryRecord(t, h, site, name)
			if replacement {
				if _, err := h.session.Inner().AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: BuildUserContent("older input", nil), Timestamp: time.Now().UnixMilli() - 2}}); err != nil {
					t.Fatal(err)
				}
				boundaryAppendAssistant(t, h, "older answer", ai.StopReasonStop, time.Now().UnixMilli()-1, nil)
				h.session.RefreshContext()
			}
			boundaryPrompt(t, h, prompt)
			h.settle(t)
			if len(boundaryEvents[agent.CompactionStartEvent](h)) == 0 {
				t.Error("missing compaction_start")
			}
			if len(requests) != 1 {
				t.Fatalf("requests=%v", requests)
			}
			marker := "EXACT-UNSENT-INSTRUCTION"
			if replacement {
				marker = "EXACT-REPLACEMENT-INSTRUCTION"
			}
			boundaryContains(t, requests[0], marker, true)
		})
	}
	t.Run("refreshes canonical context before publishing boundary entry notifications", func(t *testing.T) {
		handled := false
		snapshots := []string{}
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(...any) (any, error) {
			if handled {
				return nil, nil
			}
			handled = true
			return boundaryDrafts(false, extension.SessionBoundaryDraft{Type: "custom", CustomType: "metadata", Data: true}, extension.SessionBoundaryDraft{Type: "custom_message", CustomType: "visible-context", Content: "committed context", Display: true}), nil
		}}}}
		h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0))
		boundaryRecord(t, h, 233, "refreshes canonical context before publishing boundary entry notifications")
		h.session.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.EntryAppendedEvent); ok {
				snapshots = append(snapshots, boundaryJSON(t, h.session.Messages()))
			}
		})
		boundaryPrompt(t, h, "start")
		if len(snapshots) != 2 {
			t.Errorf("snapshots=%v", snapshots)
		}
		for _, snapshot := range snapshots {
			boundaryContains(t, snapshot, "committed context", true)
		}
	})
	t.Run("continues from an agent_before_settle custom message before final settlement", func(t *testing.T) {
		requested := false
		requests := []string{}
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_before_settle": {func(...any) (any, error) {
			if requested {
				return nil, nil
			}
			requested = true
			return boundaryDrafts(true, extension.SessionBoundaryDraft{Type: "custom_message", CustomType: "test-continuation", Content: "continue now", Display: false}), nil
		}}}}
		h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryCapture(t, &requests, "second"))
		boundaryRecord(t, h, 269, "continues from an agent_before_settle custom message before final settlement")
		boundaryPrompt(t, h, "start")
		if len(requests) == 0 {
			t.Fatal("missing continuation request")
		}
		boundaryContains(t, requests[0], "continue now", true)
		if entry := boundaryEntry(t, h, "custom_message", "test-continuation"); entry["display"] != false {
			t.Errorf("entry=%v", entry)
		}
		h.settle(t)
		if n := len(boundaryEvents[agent.AgentStartEvent](h)); n != 2 {
			t.Errorf("agent_start=%d", n)
		}
		if n := len(boundaryEvents[agent.AgentSettledEvent](h)); n != 1 {
			t.Errorf("agent_settled=%d", n)
		}
	})
	t.Run("persists custom context queued by agent_end before pre-settlement continuation", func(t *testing.T) {
		firstRun, continued := true, false
		requests := []string{}
		var h *recoveryHarness
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"agent_end": {func(...any) (any, error) {
				if !firstRun {
					return nil, nil
				}
				firstRun = false
				return nil, h.session.SendMessage(extension.CustomMessageRef{CustomType: "agent-end-context", Content: "queued after agent end", Display: false}, &extension.SendMessageOptions{TriggerTurn: new(false)})
			}},
			"agent_before_settle": {func(args ...any) (any, error) {
				if continued {
					return nil, nil
				}
				continued = true
				event := args[0].(*extension.AgentBeforeSettleEvent)
				boundaryContains(t, boundaryJSON(t, event.Context.PendingMessages), "queued after agent end", true)
				boundaryContains(t, boundaryJSON(t, event.Context.ContextMessages), "queued after agent end", false)
				return extension.BoundaryResult{Continue: new(true)}, nil
			}},
		}}
		h = newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryCapture(t, &requests, "second"))
		boundaryRecord(t, h, 312, "persists custom context queued by agent_end before pre-settlement continuation")
		boundaryPrompt(t, h, "start")
		if len(requests) == 0 {
			t.Fatal("missing continuation request")
		}
		boundaryContains(t, requests[0], "queued after agent end", true)
		boundaryEntry(t, h, "custom_message", "agent-end-context")
	})
	t.Run("keeps a pre-settlement follow-up deferred until the explicit continuation would stop", func(t *testing.T) {
		handled := false
		requests := []string{}
		var h *recoveryHarness
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_before_settle": {func(...any) (any, error) {
			if handled {
				return nil, nil
			}
			handled = true
			if err := h.session.SendExtensionUserMessage("queued follow-up", &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAsFollowUp}); err != nil {
				t.Error(err)
			}
			return boundaryDrafts(true, extension.SessionBoundaryDraft{Type: "custom_message", CustomType: "boundary", Content: "boundary context", Display: false}), nil
		}}}}
		h = newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryCapture(t, &requests, "second"), boundaryCapture(t, &requests, "follow-up response"))
		boundaryRecord(t, h, 354, "keeps a pre-settlement follow-up deferred until the explicit continuation would stop")
		boundaryPrompt(t, h, "start")
		if n := h.provider.callCount(); n != 3 {
			t.Errorf("callCount=%d", n)
		}
		if len(requests) < 2 {
			t.Fatalf("requests=%v", requests)
		}
		boundaryContains(t, requests[0], "boundary context", true)
		boundaryContains(t, requests[0], "queued follow-up", false)
		boundaryContains(t, requests[1], "queued follow-up", true)
	})
	t.Run("defers runs started by agent_settled handlers until every settled handler completes", func(t *testing.T) {
		triggered := false
		lifecycle := []string{}
		var h *recoveryHarness
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"agent_start": {func(...any) (any, error) { lifecycle = append(lifecycle, "start"); return nil, nil }},
			"agent_settled": {
				func(args ...any) (any, error) {
					ctx := extension.FromContext(args[1].(context.Context))
					idle, err := ctx.IsIdle()
					if err != nil {
						t.Error(err)
					}
					lifecycle = append(lifecycle, fmt.Sprintf("settled-first:%v", idle))
					if triggered {
						return nil, nil
					}
					triggered = true
					return nil, h.session.SendMessage(extension.CustomMessageRef{CustomType: "settled-trigger", Content: "start later", Display: false}, &extension.SendMessageOptions{TriggerTurn: new(true)})
				},
				func(args ...any) (any, error) {
					ctx := extension.FromContext(args[1].(context.Context))
					idle, err := ctx.IsIdle()
					if err != nil {
						t.Error(err)
					}
					lifecycle = append(lifecycle, fmt.Sprintf("settled-second:%v", idle))
					return nil, nil
				},
			},
		}}
		h = newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("first", ai.StopReasonStop, 0), boundaryReply("second", ai.StopReasonStop, 0))
		boundaryRecord(t, h, 400, "defers runs started by agent_settled handlers until every settled handler completes")
		boundaryPrompt(t, h, "start")
		want := []string{"start", "settled-first:true", "settled-second:true", "start", "settled-first:true", "settled-second:true"}
		if !reflect.DeepEqual(lifecycle, want) {
			t.Errorf("lifecycle=%v, want %v", lifecycle, want)
		}
	})
	t.Run("does not let an invalid explicit continuation suppress natural tool continuation", func(t *testing.T) {
		var h *recoveryHarness
		requests, diagnostics := []string{}, []string{}
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(args ...any) (any, error) {
			event := args[0].(extension.TurnEndEvent)
			targets := append([]string{boundaryLastUser(t, h), event.MessageEntryID}, event.ToolResultEntryIds...)
			entries := make([]extension.SessionBoundaryDraft, len(targets))
			for i, target := range targets {
				entries[i] = extension.SessionBoundaryDraft{Type: "context_edit", TargetID: target}
			}
			return boundaryDrafts(true, entries...), nil
		}}}}
		h = newBoundaryHarness(t, harnessOptions{extension: ext, tools: []agent.AgentTool{boundaryTool{name: "noop", label: "Noop", description: "Noop", text: "done"}}}, boundaryToolReply("noop", ai.JsonObject{}, ai.StopReasonToolUse), boundaryCapture(t, &requests, "must not run"))
		h.session.currentRunner().AddErrorListener(func(err *extension.ExtensionError) { diagnostics = append(diagnostics, err.Error) })
		boundaryRecord(t, h, 439, "does not let an invalid explicit continuation suppress natural tool continuation")
		boundaryPrompt(t, h, "start")
		if n := h.provider.callCount(); n != 2 {
			t.Errorf("callCount=%d", n)
		}
		userID, toolID := "", ""
		assistantIDs := []string{}
		for _, entry := range h.entries("message") {
			message, ok := entry.(icodingagent.MessageEntry)
			if !ok {
				t.Fatal("message entry has no message")
			}
			switch {
			case message.Message.User != nil:
				userID = entry.Base().ID
			case message.Message.Assistant != nil:
				assistantIDs = append(assistantIDs, entry.Base().ID)
			case message.Message.ToolResult != nil:
				toolID = entry.Base().ID
			}
		}
		if userID == "" || toolID == "" || len(assistantIDs) != 2 {
			t.Fatalf("persisted turn IDs: user=%q tool=%q assistants=%v", userID, toolID, assistantIDs)
		}
		targets := []string{}
		for _, entry := range h.entries("context_edit") {
			var edit struct {
				TargetID    string          `json:"targetId"`
				Replacement json.RawMessage `json:"replacement"`
			}
			if err := json.Unmarshal(entry.Raw(), &edit); err != nil {
				t.Fatal(err)
			}
			if string(edit.Replacement) != "null" {
				t.Errorf("expected omission, got %s", edit.Replacement)
			}
			targets = append(targets, edit.TargetID)
		}
		if want := []string{userID, assistantIDs[0], toolID, userID, assistantIDs[1]}; !reflect.DeepEqual(targets, want) {
			t.Errorf("committed targets=%v, want %v", targets, want)
		}
		if len(requests) != 1 {
			t.Fatalf("continuation requests=%v", requests)
		}
		var context []struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal([]byte(requests[0]), &context); err != nil {
			t.Fatal(err)
		}
		if len(context) != 1 || context[0].Role != "system" {
			t.Errorf("natural continuation context=%s", requests[0])
		}
		for _, message := range h.session.Inner().BuildSessionProjection().Messages {
			if message.System == nil {
				t.Errorf("final projection retains %s", message.Role())
			}
		}
		invalid := "turn_end requested continuation without runnable model context"
		if !reflect.DeepEqual(diagnostics, []string{invalid, invalid}) {
			t.Errorf("diagnostics=%v", diagnostics)
		}
	})
	t.Run("dispatches actionable turn_end for synthetic run failures", func(t *testing.T) {
		turnEnds := 0
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(args ...any) (any, error) {
			turnEnds++
			var event map[string]any
			if err := json.Unmarshal([]byte(boundaryJSON(t, args[0])), &event); err != nil {
				t.Error(err)
			}
			if event["outcome"] != "error" {
				t.Errorf("outcome=%v", event["outcome"])
			}
			return boundaryDrafts(false, extension.SessionBoundaryDraft{Type: "custom", CustomType: "failure-boundary", Data: true}), nil
		}}}}
		h := newBoundaryHarness(t, harnessOptions{extension: ext})
		boundaryRecord(t, h, 479, "dispatches actionable turn_end for synthetic run failures")
		h.session.Agent().SetPrepareRequest(func(context.Context, agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
			return nil, errors.New("request preparation failed")
		})
		func() {
			defer func() {
				if failure := recover(); failure != nil {
					t.Errorf("request failure escaped Session prompt instead of emitting a synthetic turn: %v", failure)
				}
			}()
			boundaryPrompt(t, h, "start")
		}()
		if turnEnds != 1 {
			t.Errorf("turnEnds=%d", turnEnds)
		}
		if n := h.provider.callCount(); n != 0 {
			t.Errorf("callCount=%d", n)
		}
		boundaryEntry(t, h, "custom", "failure-boundary")
	})
}

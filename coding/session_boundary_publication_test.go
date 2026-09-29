package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:898-974 finishes Session publication, persistence and pending-custom flushing inside its awaited Agent subscription.
func TestSessionBoundarySubscriptionFinishesBeforeLaterAgentListeners(t *testing.T) {
	var h *recoveryHarness
	queued := false
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"turn_end": {func(...any) (any, error) {
		if queued {
			return nil, nil
		}
		queued = true
		return nil, h.session.SendMessage(extension.CustomMessageRef{CustomType: "ordered", Content: "pending after turn", Display: false}, &extension.SendMessageOptions{TriggerTurn: new(false)})
	}}}}
	h = newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0))
	publicTurnSawPending := false
	h.session.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.TurnEndEvent); ok {
			publicTurnSawPending = len(h.entries("custom_message")) == 0
		}
	})
	persisted, flushed := false, false
	h.session.Agent().Subscribe(func(_ context.Context, event agent.AgentEvent) error {
		switch event := event.(type) {
		case agent.MessageEndEvent:
			if event.Message.Assistant != nil {
				_, persisted = h.session.findPersistedMessageEntryID(event.Message)
			}
		case agent.TurnEndEvent:
			flushed = len(h.entries("custom_message")) == 1 && projectionContains(h, "pending after turn")
		}
		return nil
	})
	boundaryPrompt(t, h, "start")
	if !publicTurnSawPending || !persisted || !flushed {
		t.Fatalf("public pending=%v, later Agent observer persisted=%v flushed=%v", publicTurnSawPending, persisted, flushed)
	}
}

// upstream: packages/coding-agent/src/core/agent-session.ts:815-819 refreshes once before publishing each complete entry, including drafts committed by agent_before_settle.
func TestSessionBeforeSettlePublishesCommittedEntriesAndCanonicalSnapshots(t *testing.T) {
	handled := false
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_before_settle": {func(...any) (any, error) {
		if handled {
			return nil, nil
		}
		handled = true
		return boundaryDrafts(false, extension.SessionBoundaryDraft{Type: "custom", CustomType: "metadata", Data: map[string]any{"source": "snapshot"}}, extension.SessionBoundaryDraft{Type: "custom_message", CustomType: "visible-context", Content: "committed context", Display: false, Details: map[string]any{"present": true}}), nil
	}}}}
	h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0))
	kinds := []string{}
	h.session.Subscribe(func(event agent.AgentEvent) {
		appended, ok := event.(agent.EntryAppendedEvent)
		if !ok {
			return
		}
		var fields struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(appended.Entry, &fields); err != nil {
			t.Error(err)
			return
		}
		stored, found := h.session.Inner().EntryByID(fields.ID)
		if !found || !bytes.Equal(appended.Entry, stored.Raw()) {
			t.Errorf("notification does not contain the complete persisted entry: %s", appended.Entry)
		}
		snapshot := boundaryJSON(t, h.session.Messages())
		canonical := boundaryJSON(t, h.session.Inner().BuildSessionProjection().Messages)
		if snapshot != canonical || !strings.Contains(snapshot, "committed context") {
			t.Errorf("notification preceded canonical refresh: %s", snapshot)
		}
		kinds = append(kinds, fields.Type)
	})
	boundaryPrompt(t, h, "start")
	if len(kinds) != 2 || kinds[0] != "custom" || kinds[1] != "custom_message" {
		t.Fatalf("appended=%v", kinds)
	}
}

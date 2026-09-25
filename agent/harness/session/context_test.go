package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func contextUser(text string) agent.AgentMessage { return userText(text, now) }

func contextAssistant(stopReason ai.StopReason, text string) agent.AgentMessage {
	message := &agent.AssistantMessage{
		Role: agent.RoleAssistant, API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "claude-sonnet-4-5",
		Usage: &ai.Usage{}, StopReason: stopReason, Timestamp: now,
	}
	if stopReason == ai.StopReasonToolUse {
		message.Content = []ai.AssistantContentBlock{ai.ToolCall{ID: "call", Name: "read", Arguments: ai.JsonObject{}}}
	} else {
		message.Content = []ai.AssistantContentBlock{ai.TextContent{Text: text}}
	}
	if stopReason == ai.StopReasonDeferred {
		message.Deferred = &ai.DeferredHandle{Provider: "anthropic", ModelID: "claude-sonnet-4-5", API: ai.APIAnthropicMessages, ID: "job"}
	}
	return agent.AgentMessage{Assistant: message}
}

func contextMessageEntry(id string, parentID *string, seq int64, message agent.AgentMessage) session.Entry {
	return session.Entry{ID: id, ParentID: parentID, Seq: seq, Timestamp: now, Type: session.EntryTypeMessage, Message: message}
}

func buildContext(t *testing.T, entries []session.Entry, options *session.SessionContextBuildOptions) []agent.AgentMessage {
	t.Helper()
	messages, err := session.BuildSessionContext(background, entries, options)
	mustNoErr(t, err)
	return messages
}

func TestSessionContextFiltersNonContextAssistantResponses(t *testing.T) {
	user := contextUser("question")
	stopped := contextAssistant(ai.StopReasonStop, "answer")
	length := contextAssistant(ai.StopReasonLength, "truncated answer")
	toolUse := contextAssistant(ai.StopReasonToolUse, "")
	entries := []session.Entry{
		contextMessageEntry("user", nil, 1, user),
		contextMessageEntry("failed", new("user"), 2, contextAssistant(ai.StopReasonError, "failed")),
		contextMessageEntry("stopped", new("failed"), 3, stopped),
		contextMessageEntry("aborted", new("stopped"), 4, contextAssistant(ai.StopReasonAborted, "aborted")),
		contextMessageEntry("tool-use", new("aborted"), 5, toolUse),
		contextMessageEntry("deferred", new("tool-use"), 6, contextAssistant(ai.StopReasonDeferred, "")),
		contextMessageEntry("length", new("deferred"), 7, length),
	}
	assertJSON(t, buildContext(t, entries, nil), []agent.AgentMessage{user, stopped, toolUse, length})
}

func TestSessionContextProjectsBranchSummariesInBranchOrder(t *testing.T) {
	before := contextMessageEntry("before", nil, 1, contextUser("before summary"))
	summary := session.Entry{ID: "branch-summary", ParentID: new("before"), Seq: 2, Timestamp: now, Type: session.EntryTypeBranchSummary, FromID: new("source-leaf"), Summary: "work on the abandoned branch"}
	after := contextMessageEntry("after", new("branch-summary"), 3, contextUser("after summary"))
	assertJSON(t, buildContext(t, []session.Entry{before, summary, after}, nil), []any{
		contextUser("before summary"),
		map[string]any{"role": "branchSummary", "summary": "work on the abandoned branch", "fromId": "source-leaf", "timestamp": now},
		contextUser("after summary"),
	})
}

func TestSessionContextFiltersRetainedTailWithoutHidingTheCompactionSummary(t *testing.T) {
	user := contextUser("kept user")
	stopped := contextAssistant(ai.StopReasonStop, "kept answer")
	toolUse := contextAssistant(ai.StopReasonToolUse, "")
	length := contextAssistant(ai.StopReasonLength, "kept truncated answer")
	compaction := session.Entry{
		ID: "compaction", Seq: 1, Timestamp: now, Type: session.EntryTypeCompaction, Summary: "summary", TokensBefore: 100,
		RetainedTail: []agent.AgentMessage{
			contextAssistant(ai.StopReasonError, "failed"), user, contextAssistant(ai.StopReasonAborted, "aborted"),
			stopped, contextAssistant(ai.StopReasonDeferred, ""), toolUse, length,
		},
	}
	assertJSON(t, buildContext(t, []session.Entry{compaction}, nil), []any{
		map[string]any{"role": "compactionSummary", "summary": "summary", "tokensBefore": 100, "timestamp": now},
		user, stopped, toolUse, length,
	})
}

func TestSessionContextUsesOnlyTheLatestCompactionAndLaterEntries(t *testing.T) {
	beforeFirst := contextMessageEntry("before-first", nil, 1, contextUser("before first"))
	first := session.Entry{ID: "first-compaction", ParentID: new("before-first"), Seq: 2, Timestamp: now, Type: session.EntryTypeCompaction, Summary: "stale summary", RetainedTail: []agent.AgentMessage{contextUser("stale tail")}, TokensBefore: 100}
	between := contextMessageEntry("between", new("first-compaction"), 3, contextUser("between compactions"))
	latest := session.Entry{ID: "latest-compaction", ParentID: new("between"), Seq: 4, Timestamp: now, Type: session.EntryTypeCompaction, Summary: "latest summary", RetainedTail: []agent.AgentMessage{contextUser("latest tail")}, TokensBefore: 200}
	after := contextMessageEntry("after", new("latest-compaction"), 5, contextUser("after latest"))
	assertJSON(t, buildContext(t, []session.Entry{beforeFirst, first, between, latest, after}, nil), []any{
		map[string]any{"role": "compactionSummary", "summary": "latest summary", "tokensBefore": 200, "timestamp": now},
		contextUser("latest tail"),
		contextUser("after latest"),
	})
}

func TestSessionContextProjectsCustomEntriesThroughProjectorsInBranchOrder(t *testing.T) {
	custom := func(id string, parentID *string, seq int64, customType string) session.Entry {
		return session.Entry{ID: id, ParentID: parentID, Seq: seq, Timestamp: now, Type: session.EntryTypeCustom, CustomType: customType}
	}
	oldCustom := custom("old-custom", nil, 1, "sync")
	compaction := session.Entry{ID: "compaction", ParentID: new("old-custom"), Seq: 2, Timestamp: now, Type: session.EntryTypeCompaction, Summary: "summary", TokensBefore: 100}
	syncCustom := custom("sync-custom", new("compaction"), 3, "sync")
	omitted := custom("omitted-custom", new("sync-custom"), 4, "omitted")
	asyncCustom := custom("async-custom", new("omitted-custom"), 5, "async")
	var projected []string
	project := func(_ context.Context, entry session.Entry) ([]agent.AgentMessage, error) {
		projected = append(projected, entry.ID)
		return []agent.AgentMessage{contextUser("projected:" + entry.ID)}, nil
	}
	messages := buildContext(t, []session.Entry{oldCustom, compaction, syncCustom, omitted, asyncCustom}, &session.SessionContextBuildOptions{
		EntryProjectors: map[string]session.EntryProjector{"sync": project, "async": project},
	})
	if len(projected) != 2 || projected[0] != syncCustom.ID || projected[1] != asyncCustom.ID {
		t.Fatalf("projected = %v", projected)
	}
	assertJSON(t, messages, []any{
		map[string]any{"role": "compactionSummary", "summary": "summary", "tokensBefore": 100, "timestamp": now},
		contextUser("projected:" + syncCustom.ID),
		contextUser("projected:" + asyncCustom.ID),
	})
}

func TestSessionContextPropagatesCustomProjectorFailures(t *testing.T) {
	failure := errors.New("projector failed")
	custom := session.Entry{ID: "custom", Seq: 1, Timestamp: now, Type: session.EntryTypeCustom, CustomType: "broken"}
	_, err := session.BuildSessionContext(background, []session.Entry{custom}, &session.SessionContextBuildOptions{
		EntryProjectors: map[string]session.EntryProjector{"broken": func(context.Context, session.Entry) ([]agent.AgentMessage, error) { return nil, failure }},
	})
	if !errors.Is(err, failure) {
		t.Fatalf("err = %v", err)
	}
}

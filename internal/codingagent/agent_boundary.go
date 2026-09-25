package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// lastAssistantMessage returns the last assistant message in messages, or nil.
// Mirrors coding.lastAssistantMessage; not shared directly because coding
// imports this package.
func lastAssistantMessage(messages []agent.AgentMessage) *agent.AssistantMessage {
	for _, msg := range slices.Backward(messages) {
		if msg.Assistant != nil {
			return msg.Assistant
		}
	}
	return nil
}

// AgentActivityOutcome reports the terminal state of the most recent low-level
// run to an agent_before_settle boundary.
func AgentActivityOutcome(messages []agent.AgentMessage, runErr error) extension.AgentActivityOutcome {
	if last := lastAssistantMessage(messages); last != nil {
		switch last.StopReason {
		case ai.StopReasonAborted:
			return extension.AgentActivityAborted
		case ai.StopReasonError:
			return extension.AgentActivityError
		}
	}
	if runErr != nil {
		return extension.AgentActivityError
	}
	return extension.AgentActivityCompleted
}

// RunAgentBeforeSettle emits the final settlement boundary, commits its durable
// drafts, refreshes the agent transcript, and reports whether one more provider
// run can start.
func RunAgentBeforeSettle(
	ctx context.Context,
	session *Session,
	a *agent.Agent,
	runner *inproc.Runner,
	outcome extension.AgentActivityOutcome,
) (bool, error) {
	if session == nil || a == nil || runner == nil || !runner.HasHandlers(EventAgentBeforeSettle) {
		return a != nil && a.HasQueuedMessages(), nil
	}
	build := func(drafts []extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
		preview, err := cloneBoundarySession(session)
		if err != nil {
			return extension.BoundaryContextPreview{}, err
		}
		if _, err := applyBoundaryDrafts(preview, drafts); err != nil {
			return extension.BoundaryContextPreview{}, err
		}
		return buildBoundaryContext(preview, a), nil
	}
	result, err := runner.EmitBoundary(ctx, EventAgentBeforeSettle, outcome, build)
	if err != nil {
		return false, err
	}
	if _, err := applyBoundaryDrafts(session, result.Entries); err != nil {
		return false, err
	}
	projection := session.BuildSessionProjection()
	a.SetMessages(projection.Messages)
	finalContext := buildBoundaryContext(session, a)
	shouldContinue := result.Continue || a.HasQueuedMessages()
	if shouldContinue && !finalContext.CanContinue {
		if result.Continue {
			runner.EmitError(&extension.ExtensionError{
				ExtensionPath: "<boundary>",
				Event:         EventAgentBeforeSettle,
				Error:         "agent_before_settle requested continuation without runnable model context",
			})
		}
		return false, nil
	}
	return shouldContinue, nil
}

func cloneBoundarySession(session *Session) (*Session, error) {
	header := session.Header()
	preview := NewSession(header.ID, header.CWD)
	leaf := session.LeafID()
	if leaf == nil {
		return preview, nil
	}
	for _, entry := range session.Branch(*leaf) {
		if err := preview.AppendEntry(entry); err != nil {
			return nil, err
		}
	}
	return preview, nil
}

func applyBoundaryDrafts(session *Session, drafts []extension.SessionBoundaryDraft) ([]SessionEntry, error) {
	appended := make([]SessionEntry, 0, len(drafts))
	for _, draft := range drafts {
		var id string
		var err error
		switch draft.Type {
		case "custom":
			id, err = appendBoundaryCustomEntry(session, draft)
		case "custom_message":
			id, err = session.AppendCustomMessage(draft.CustomType, draft.Content, draft.Display, draft.Details)
		case "context_edit":
			var replacement *ContextEditReplacement
			if raw := bytes.TrimSpace(draft.Replacement); len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
				replacement = &ContextEditReplacement{}
				if err = json.Unmarshal(raw, replacement); err != nil {
					err = fmt.Errorf("context_edit replacement: %w", err)
				}
			}
			if err == nil {
				id, err = session.AppendContextEdit(draft.TargetID, replacement)
			}
		case "compaction":
			firstKept := ""
			if draft.FirstKeptEntryID != nil {
				firstKept = *draft.FirstKeptEntryID
			}
			tokensBefore := agent.EstimateContextTokens(session.BuildSessionProjection().Messages).Tokens
			id, err = session.AppendCompaction(draft.Summary, firstKept, tokensBefore, draft.Details, true, draft.Usage)
		default:
			err = fmt.Errorf("unsupported session boundary draft type %q", draft.Type)
		}
		if err != nil {
			return nil, err
		}
		entry, found := session.EntryByID(id)
		if !found {
			return nil, fmt.Errorf("boundary entry %s was not appended", id)
		}
		appended = append(appended, entry)
	}
	return appended, nil
}

func appendBoundaryCustomEntry(session *Session, draft extension.SessionBoundaryDraft) (string, error) {
	id, err := generateEntryID()
	if err != nil {
		return "", err
	}
	entry := CustomEntry{
		SessionEntryBase: SessionEntryBase{
			Type: "custom", ID: id, ParentID: session.LeafID(), Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		},
		CustomType: draft.CustomType,
		Data:       draft.Data,
	}
	if err := session.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

func buildBoundaryContext(session *Session, a *agent.Agent) extension.BoundaryContextPreview {
	projection := session.BuildSessionProjection()
	entries := make([]extension.ProjectedSessionEntry, len(projection.Entries))
	for i, projected := range projection.Entries {
		messages := make([]extension.AgentMessage, len(projected.Messages))
		for j := range projected.Messages {
			messages[j] = projected.Messages[j]
		}
		var sourceEntry any
		if err := json.Unmarshal(projected.SourceEntry.Raw(), &sourceEntry); err != nil {
			sourceEntry = slices.Clone(projected.SourceEntry.Raw())
		}
		entries[i] = extension.ProjectedSessionEntry{SourceEntry: sourceEntry, Messages: messages}
	}
	contextMessages := make([]extension.AgentMessage, len(projection.Messages))
	for i := range projection.Messages {
		contextMessages[i] = projection.Messages[i]
	}
	llm := agent.ConvertToLLM(projection.Messages, a.Model())
	llmMessages := make([]any, len(llm))
	hasNonSystem := false
	finalAssistant := false
	for i, message := range llm {
		llmMessages[i] = message
		_, system := message.(ai.SystemMessage)
		if !system {
			hasNonSystem = true
		}
		_, finalAssistant = message.(ai.AssistantMessage)
	}
	pending := a.PeekQueuedMessages()
	pendingMessages := make([]extension.AgentMessage, len(pending))
	for i := range pending {
		pendingMessages[i] = pending[i]
	}
	contextCanContinue := hasNonSystem && !finalAssistant
	return extension.BoundaryContextPreview{
		ContextEntries:  entries,
		ContextMessages: contextMessages,
		LLMMessages:     llmMessages,
		PendingMessages: pendingMessages,
		CanContinue:     contextCanContinue || finalAssistant && a.HasQueuedMessages(),
	}
}

// Package durableadapter adapts the pi-durable Harness (durable/harness) to the consumer-owned boundaries of the experimental worker services.
package durableadapter

// The adapter from the pi-durable Harness (durable/harness) to the consumer-owned boundaries of the worker services, as packages/coding-agent/src/experimental/services/*-provider.ts use it.

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
	durablechord "github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// Session is a durable Harness with its root conversation. It implements the Harness and conversation boundaries of the worker services and the worker's Harness (Close, TaskGraph, Resume).
type Session struct {
	Harness      harness.Harness
	Conversation harness.Conversation
}

var (
	_ services.AgentHarness              = (*Session)(nil)
	_ services.SessionWorkerConversation = (*Session)(nil)
)

func (session *Session) Close(ctx context.Context) error { return session.Harness.Close(ctx) }
func (session *Session) Resume()                         { session.Harness.Resume() }

func (session *Session) ID() durable.ConversationId { return session.Conversation.Id() }

func (session *Session) Submit(ctx context.Context, content ai.UserContent, whenBusy durable.WhenBusy) (durable.SubmissionId, error) {
	submission, err := session.Conversation.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: content, WhenBusy: whenBusy})
	if err != nil {
		return 0, err
	}
	return submission.Id(), nil
}

func (session *Session) Abort(ctx context.Context) error {
	return session.Conversation.Abort(ctx, nil)
}

func (session *Session) Compact(ctx context.Context, customInstructions *string) (int64, error) {
	id, err := session.Conversation.Compact(ctx, customInstructions)
	return int64(id), err
}

// Configure applies the model and thinking level in one commit.
func (session *Session) Configure(ctx context.Context, configuration services.ConversationConfiguration) error {
	var change harness.AgentChange
	if configuration.Model != nil {
		change.Model = harness.SetTo(durable.ModelRef{Provider: configuration.Model.Provider, ModelId: configuration.Model.ModelId})
	}
	if configuration.ThinkingLevel != nil {
		change.ThinkingLevel = harness.SetTo(*configuration.ThinkingLevel)
	}
	return session.Conversation.Configure(ctx, change)
}

// ViewState returns conversation.viewState as the service host's Chord state, as upstream serves it (transcript-provider.ts:6-13, view.ts:92-104): the host state republishes the durable state's exact operation batches, so it never re-encodes or re-diffs the whole view. ctx bounds only the attachment; the state lives until Dispose.
func (session *Session) ViewState(ctx context.Context) (services.TranscriptViewState, error) {
	source, err := attachViewFrames(ctx, session.Conversation)
	if err != nil {
		return nil, err
	}
	state, err := chord.AttachReplicatedState[harness.ConversationView](source, chord.ReplicatedStateSourceOptions{})
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (session *Session) AbortSubmission(ctx context.Context, id durable.SubmissionId, conversationID durable.ConversationId) (durable.SubmissionAbortResult, error) {
	return session.Harness.AbortSubmission(ctx, id, &conversationID)
}

// Submission reacquires a submission; it is nil when the Harness has none with that ID.
func (session *Session) Submission(ctx context.Context, id durable.SubmissionId) (services.PromptSubmission, error) {
	submission, err := session.Harness.Submission(ctx, id)
	if err != nil || submission == nil {
		return nil, err
	}
	return promptSubmission{harness: session.Harness, submission: submission}, nil
}

type promptSubmission struct {
	harness    harness.Harness
	submission durable.Submission
}

// Wait settles the submission, then reads the first model message of its answer entry.
func (prompt promptSubmission) Wait(ctx context.Context) (services.SettledPrompt, error) {
	settled, err := prompt.submission.Wait(ctx)
	if err != nil {
		return services.SettledPrompt{}, err
	}
	if settled.Status == durable.SubmissionUnanswered {
		reason := ""
		if settled.Reason != nil {
			reason = *settled.Reason
		}
		return services.SettledPrompt{Unanswered: true, Reason: reason}, nil
	}
	if settled.Type != durable.SubmissionTypeInput || settled.Answer == nil {
		return services.SettledPrompt{}, nil
	}
	answer := *settled.Answer
	entry, err := durable.Commit(ctx, prompt.harness, func(tx durable.Tx) (*durable.EntryRecord, error) { return tx.Entry(answer) })
	if err != nil || entry == nil || len(entry.Model) == 0 {
		return services.SettledPrompt{}, err
	}
	return services.SettledPrompt{Answer: entry.Model[0]}, nil
}

// AgentDocument is the conversation's pi.agent document as a replicated state; it is nil when the conversation has none.
func (session *Session) AgentDocument(ctx context.Context, conversationID durable.ConversationId) (services.AgentDocument, error) {
	state, err := session.Harness.DocumentStateErased(ctx, harness.AgentDoc, conversationID)
	if err != nil || state == nil {
		return nil, err
	}
	return &agentDocument{state: state}, nil
}

type agentDocument struct {
	state durable.AttachedReplicatedState[durable.JsonObject]
}

func (document *agentDocument) Value() *services.AgentState {
	encoded, err := json.Marshal(document.state.Value())
	if err != nil {
		return nil
	}
	var agent harness.AgentState
	if json.Unmarshal(encoded, &agent) != nil {
		return nil
	}
	result := &services.AgentState{ThinkingLevel: agent.ThinkingLevel}
	if agent.Model != nil {
		result.Model = &services.ModelRef{Provider: agent.Model.Provider, ModelId: agent.Model.ModelId}
	}
	return result
}

func (document *agentDocument) Subscribe(listener func(context.Context)) func() {
	unsubscribe, err := document.state.Subscribe(func(_ durable.JsonObject, ctx context.Context, delivery durablechord.ReplicatedStateDelivery) {
		if delivery.Kind == durablechord.DeliveryUpdate {
			listener(ctx)
		}
	})
	if err != nil {
		return func() {}
	}
	return unsubscribe
}

func (document *agentDocument) Dispose() { document.state.Dispose() }

// TaskGraph observes the Harness's live tasks; the worker stays open while any exists.
func (session *Session) TaskGraph(ctx context.Context) (services.TaskGraphActivity, error) {
	state, err := session.Harness.TaskGraph(ctx)
	if err != nil {
		return nil, err
	}
	return &taskGraphActivity{state: state}, nil
}

type taskGraphActivity struct {
	state durable.AttachedReplicatedState[harness.TaskGraph]
}

// liveTasks counts the tasks of a graph value; an unreadable graph counts as active so the worker never retires on a value it cannot read.
func liveTasks(graph harness.TaskGraph) (count int, err error) {
	encoded, err := json.Marshal(graph)
	if err != nil {
		return 0, err
	}
	var shape struct {
		Tasks map[string]json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(encoded, &shape); err != nil {
		return 0, err
	}
	return len(shape.Tasks), nil
}

func (activity *taskGraphActivity) Subscribe(listener func(bool)) func() {
	unsubscribe, err := activity.state.Subscribe(func(graph harness.TaskGraph, _ context.Context, _ durablechord.ReplicatedStateDelivery) {
		count, err := liveTasks(graph)
		listener(err != nil || count > 0)
	})
	if err != nil {
		return func() {}
	}
	return unsubscribe
}

func (activity *taskGraphActivity) Dispose() { activity.state.Dispose() }

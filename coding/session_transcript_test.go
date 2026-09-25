package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

type transcriptCaptureProvider struct {
	fakeProvider
	requests []ai.TranscriptContext
}

func (p *transcriptCaptureProvider) Stream(_ context.Context, transcript ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.requests = append(p.requests, transcript)
	message := sessionTestMessage("fake", "done", ai.StopReasonStop, "")
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

func TestSessionResumePreservesInstructionBaseline(t *testing.T) {
	services := newTestServices(t)
	provider := &transcriptCaptureProvider{}
	model := fakeModelWithProvider(provider)
	session, err := NewSession(services, SessionOptions{Model: model, SystemPrompt: "retain these instructions"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	path := session.Path()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := NewSession(services, SessionOptions{Model: model, SystemPrompt: "retain these instructions", ResumePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	if _, err := resumed.Send(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	for i, request := range provider.requests {
		if got := ai.GetCurrentSystemPrompt(request.Messages()); got != "retain these instructions" {
			t.Fatalf("request %d prompt %q", i, got)
		}
		if got := ai.GetCurrentTools(request.Messages()); len(got) == 0 {
			t.Fatalf("request %d lost tools", i)
		}
	}
}

func TestSessionPersistsSystemToolDelta(t *testing.T) {
	services := newTestServices(t)
	provider := &transcriptCaptureProvider{}
	model := fakeModelWithProvider(provider)
	session, err := NewSession(services, SessionOptions{Model: model, SystemPrompt: "instructions"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Send(context.Background(), "declare tools"); err != nil {
		t.Fatal(err)
	}
	session.agent.SetTools(nil)
	if _, err := session.Send(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	path := session.Path()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := NewSession(services, SessionOptions{Model: model, ResumePath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resumed.Close() }()
	messages := resumed.Inner().BuildContext(nil)
	if len(messages) != 6 || messages[0].System == nil || messages[3].System == nil || len(messages[3].System.ToolsRemoved) == 0 {
		t.Fatalf("persisted system delta lost: %+v", messages)
	}
	if messages[1].User == nil || messages[2].Assistant == nil || messages[4].User == nil || messages[5].Assistant == nil {
		t.Fatalf("persisted ordering changed: %+v", messages)
	}
}

func TestRefreshContextBaselineDoesNotShiftRecoveryTargets(t *testing.T) {
	services := newTestServices(t)
	provider := &transcriptCaptureProvider{}
	session, err := NewSession(services, SessionOptions{Model: fakeModelWithProvider(provider), SystemPrompt: "retain instructions"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if _, err := session.Send(context.Background(), "persist baseline"); err != nil {
		t.Fatal(err)
	}
	// Select a branch before the persisted baseline, then append an assistant.
	// This makes refresh restore an in-memory baseline with no source entry.
	messages := session.agent.Messages()
	if len(messages) != 3 || messages[0].System == nil {
		t.Fatalf("first turn: %+v", messages)
	}
	baselineID, ok := session.findPersistedMessageEntryID(messages[0])
	if !ok {
		t.Fatal("baseline entry not indexed")
	}
	entry, ok := session.Inner().EntryByID(baselineID)
	if !ok || entry.Base.ParentID == nil {
		t.Fatal("baseline entry missing parent")
	}
	if err := session.Inner().Fork(*entry.Base.ParentID); err != nil {
		t.Fatal(err)
	}
	assistant := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: ai.StopReasonError, ErrorMessage: "retry"}}
	id, err := session.Inner().AppendMessage(assistant)
	if err != nil {
		t.Fatal(err)
	}
	session.RefreshContext()
	transcript := session.agent.Messages()
	if len(transcript) != 2 || transcript[0].System == nil || transcript[1].Assistant == nil {
		t.Fatalf("restored transcript: %+v", transcript)
	}
	// Force the positional fallback used for messages with replaced pointers.
	session.entryIDsMu.Lock()
	clear(session.entryIDsByMessage)
	session.entryIDsMu.Unlock()
	if got, ok := session.findPersistedMessageEntryID(transcript[1]); !ok || got != id {
		t.Fatalf("assistant entry = %q, %v; want %q", got, ok, id)
	}
	if _, ok := session.findPersistedMessageEntryID(transcript[0]); ok {
		t.Fatal("synthetic baseline acquired a source entry")
	}
}

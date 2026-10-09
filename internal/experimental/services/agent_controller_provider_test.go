package services

// pi: packages/coding-agent/src/experimental/services/agent-controller-provider.ts

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

type fakeAgentConversation struct {
	submitted []ai.UserContent
	whenBusy  []durable.WhenBusy
	err       error
	compactID int64
	customGot *string
}

func (*fakeAgentConversation) ID() durable.ConversationId { return 7 }
func (conversation *fakeAgentConversation) Submit(_ context.Context, content ai.UserContent, whenBusy durable.WhenBusy) (durable.SubmissionId, error) {
	conversation.submitted = append(conversation.submitted, content)
	conversation.whenBusy = append(conversation.whenBusy, whenBusy)
	return 42, conversation.err
}
func (*fakeAgentConversation) Abort(context.Context) error { return nil }
func (conversation *fakeAgentConversation) Compact(_ context.Context, custom *string) (int64, error) {
	conversation.customGot = custom
	return conversation.compactID, conversation.err
}

type fakeSettled struct{ settled SettledPrompt }

func (submission fakeSettled) Wait(context.Context) (SettledPrompt, error) {
	return submission.settled, nil
}

type fakeAgentHarness struct {
	asked     []durable.SubmissionId
	abort     durable.SubmissionAbortResult
	submitted map[durable.SubmissionId]PromptSubmission
}

func (harness *fakeAgentHarness) AbortSubmission(_ context.Context, id durable.SubmissionId, conversation durable.ConversationId) (durable.SubmissionAbortResult, error) {
	if conversation != 7 {
		return "", errors.New("wrong conversation")
	}
	harness.asked = append(harness.asked, id)
	return harness.abort, nil
}
func (harness *fakeAgentHarness) Submission(_ context.Context, id durable.SubmissionId) (PromptSubmission, error) {
	return harness.submitted[id], nil
}
func (*fakeAgentHarness) AgentDocument(context.Context, durable.ConversationId) (AgentDocument, error) {
	return nil, nil
}

// packages/coding-agent/src/experimental/services/agent-controller-provider.ts:18-95: how the controller maps conversation and harness outcomes to presentation results.
func TestAgentControllerProviderMapsOutcomes(t *testing.T) {
	ctx := t.Context()
	conversation := &fakeAgentConversation{compactID: 9}
	harness := &fakeAgentHarness{abort: durable.SubmissionAborted}
	controller := CreateAgentController(harness, conversation)

	// :19-45 prompt rejects while busy; steering and follow-up queue; ids are decimal strings; images follow the text block (:86-90).
	images := []AgentPromptImage{{Type: "image", Data: "AAAA", MimeType: "image/png"}}
	if response, err := controller.Prompt(ctx, AgentPromptRequest{Message: "hi", Images: images}); err != nil || !response.Accepted || response.OperationID == nil || *response.OperationID != "42" {
		t.Fatalf("Prompt = %+v, %v", response, err)
	}
	if response, _ := controller.Steer(ctx, AgentPromptRequest{Message: "s"}); !response.Accepted || response.EntryID == nil || *response.EntryID != "42" {
		t.Fatalf("Steer = %+v", response)
	}
	if response, _ := controller.FollowUp(ctx, AgentPromptRequest{Message: "f"}); !response.Accepted {
		t.Fatalf("FollowUp = %+v", response)
	}
	if want := []durable.WhenBusy{durable.WhenBusyReject, durable.WhenBusySteer, durable.WhenBusyFollowUp}; !reflect.DeepEqual(conversation.whenBusy, want) {
		t.Fatalf("whenBusy = %v, want %v", conversation.whenBusy, want)
	}
	wantContent := ai.UserContentBlocks{ai.TextContent{Text: "hi"}, ai.ImageContent{Data: "AAAA", MimeType: "image/png"}}
	if !reflect.DeepEqual(conversation.submitted[0], ai.UserContent(wantContent)) || !reflect.DeepEqual(conversation.submitted[1], ai.UserText("s")) {
		t.Fatalf("submitted = %#v", conversation.submitted)
	}

	// :40-42, :59-61 and :92-95 compaction and prompts report a failure as a coded error, never as a call error.
	conversation.err = errors.New("disk full")
	if response, err := controller.Prompt(ctx, AgentPromptRequest{Message: "x"}); err != nil || response.Accepted || response.Error == nil || response.Error.Code != "operation_failed" || response.Error.Message != "disk full" {
		t.Fatalf("failed Prompt = %+v, %v", response, err)
	}
	if response, _ := controller.Compact(ctx, AgentCompactionRequest{}); response.Accepted || response.Error == nil {
		t.Fatalf("failed Compact = %+v", response)
	}
	conversation.err = &durable.ConversationBusy{}
	if response, _ := controller.FollowUp(ctx, AgentPromptRequest{Message: "x"}); response.Error == nil || response.Error.Code != "busy" {
		t.Fatalf("busy FollowUp = %+v", response)
	}
	conversation.err = nil
	custom := "short"
	if response, _ := controller.Compact(ctx, AgentCompactionRequest{CustomInstructions: &custom}); !response.Accepted || response.OperationID == nil || *response.OperationID != "9" || conversation.customGot != &custom {
		t.Fatalf("Compact = %+v", response)
	}

	// :46-53 queued cancellation: aborted is cancelled, an unknown entry is not_found, anything else was already consumed; a malformed ID
	// never reaches the harness.
	for result, want := range map[durable.SubmissionAbortResult]string{durable.SubmissionAborted: "cancelled", durable.SubmissionNotFound: "not_found", durable.SubmissionSettled: "already_consumed", durable.SubmissionAlreadyPlaced: "already_consumed"} {
		harness.abort = result
		if got, err := controller.CancelQueued(ctx, "5"); err != nil || got.Outcome != want {
			t.Errorf("CancelQueued after %q = %+v, %v; want %s", result, got, err, want)
		}
	}
	harness.asked = nil
	for _, bad := range []string{"", "0", "05", "-1", "1.5", "1e3", " 5", "5 ", "abc", "9007199254740992", "99999999999999999999"} {
		if got, _ := controller.CancelQueued(ctx, bad); got.Outcome != "not_found" {
			t.Errorf("CancelQueued(%q) = %+v, want not_found", bad, got)
		}
	}
	if len(harness.asked) != 0 {
		t.Fatalf("malformed IDs reached the harness: %v", harness.asked)
	}
	if got, _ := controller.CancelQueued(ctx, "9007199254740991"); got.Outcome == "" || len(harness.asked) != 1 || harness.asked[0] != 9007199254740991 {
		t.Fatalf("the largest safe integer must be accepted: %+v %v", got, harness.asked)
	}
}

// :63-77 waitForPrompt: an unknown or malformed ID is an error, an unanswered prompt reports its reason, and an answered one joins the text blocks of the answer.
func TestAgentControllerProviderWaitForPrompt(t *testing.T) {
	ctx := t.Context()
	harness := &fakeAgentHarness{submitted: map[durable.SubmissionId]PromptSubmission{
		3: fakeSettled{SettledPrompt{Unanswered: true, Reason: "aborted"}},
		4: fakeSettled{SettledPrompt{Answer: ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "a"}, ai.ThinkingContent{Thinking: "hmm"}, ai.TextContent{Text: "b"}}}}},
		5: fakeSettled{SettledPrompt{}},
	}}
	controller := CreateAgentController(harness, &fakeAgentConversation{})
	for _, id := range []string{"99", "03", "x"} {
		if _, err := controller.WaitForPrompt(ctx, id); err == nil || err.Error() != "Unknown prompt: "+id {
			t.Errorf("WaitForPrompt(%q) = %v", id, err)
		}
	}
	if got, err := controller.WaitForPrompt(ctx, "3"); err != nil || got.Status != "unanswered" || got.Reason == nil || *got.Reason != "aborted" || got.Text != nil {
		t.Fatalf("unanswered = %+v, %v", got, err)
	}
	if got, err := controller.WaitForPrompt(ctx, "4"); err != nil || got.Status != "done" || got.Text == nil || *got.Text != "ab" || got.Reason != nil {
		t.Fatalf("answered = %+v, %v", got, err)
	}
	if got, _ := controller.WaitForPrompt(ctx, "5"); got.Status != "done" || got.Text == nil || *got.Text != "" {
		t.Fatalf("settled without an answer = %+v", got)
	}
}

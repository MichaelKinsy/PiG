package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// abortAfterStartProvider streams a start event + partial text, then the
// pre-cancelled context aborts the turn: reproducing "send a message and
// cancel after it started".
type abortAfterStartProvider struct{}

func (abortAfterStartProvider) ID() string   { return "abort-after-start" }
func (abortAfterStartProvider) Close() error { return nil }
func (abortAfterStartProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	partial := sessionTestMessage("abort-after-start", "thinking about it", ai.StopReasonPending, "")
	stream := ai.NewAssistantMessageEventStream()
	if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
		return nil, err
	}
	if err := stream.Push(ai.TextDeltaEvent{ContentIndex: 0, Delta: "thinking about it", Partial: partial}); err != nil {
		return nil, err
	}
	return stream, nil
}

// TestNavigateToUserMessageAfterAbortLoadsEditor reproduces the user flow:
// send "Proceed", cancel after it starts, then /tree to the user message to
// edit it. Selecting the *user* entry (not the aborted-assistant leaf) must
// move the leaf to its parent and surface the message text for editing.
func TestNavigateToUserMessageAfterAbortLoadsEditor(t *testing.T) {
	svcs := newTestServices(t)
	model := fakeModel()
	model.Provider = abortAfterStartProvider{}
	sess, err := NewSession(svcs, SessionOptions{Model: model})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = sess.Send(ctx, "Proceed")

	// Locate the user "Proceed" entry and confirm the leaf is the aborted
	// assistant (its child), i.e. the user message is NOT the leaf.
	var userID string
	for _, e := range sess.inner.Entries() {
		if me, ok := e.AsMessage(); ok && me.Message.User != nil {
			userID = e.Base.ID
		}
	}
	if userID == "" {
		t.Fatal("user message not found in session")
	}
	if leaf := sess.LeafID(); leaf == nil || *leaf == userID {
		t.Fatalf("expected leaf to be the aborted assistant (child of user msg), got %v", leaf)
	}

	res, err := sess.NavigateTree(context.Background(), userID, NavigateTreeOptions{})
	if err != nil {
		t.Fatalf("NavigateTree: %v", err)
	}
	if res.EditorText != "Proceed" {
		t.Errorf("editor text = %q, want %q (message should be loaded for editing)", res.EditorText, "Proceed")
	}
}

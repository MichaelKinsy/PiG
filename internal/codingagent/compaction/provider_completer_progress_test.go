// SPDX-License-Identifier: MIT

package compaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/compactprogress"
)

// progressStream is a summary stream of the given deltas that ends with the full text.
func progressStream(deltas ...string) (agent.StreamFn, *[32]byte) {
	var requestHash [32]byte
	return func(_ context.Context, _ *ai.Model, transcript ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		body, err := json.Marshal(transcript.FrozenContext())
		if err != nil {
			return nil, err
		}
		requestHash = sha256.Sum256(body)
		stream := ai.NewAssistantMessageEventStream()
		partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{}}, Provider: "faux", Model: "faux-1"}
		_ = stream.Push(ai.StartEvent{Partial: partial})
		for _, delta := range deltas {
			_ = stream.Push(ai.TextDeltaEvent{Delta: delta, Partial: partial})
		}
		_ = stream.Push(ai.ThinkingDeltaEvent{Delta: "think", Partial: partial})
		done := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: strings.Join(deltas, "")}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop}
		_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: done})
		return stream, nil
	}, &requestHash
}

// pig divergence (D652): a context without a Progress sends the request the base sent and returns what it returned; one with a Progress sends the same request, returns the same summary, and counts the characters streamed.
func TestProviderCompleterProgressChangesNeitherRequestNorResult(t *testing.T) {
	messages := []agent.AgentMessage{{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "summarize"}}}}}
	run := func(ctx context.Context) (string, [32]byte) {
		t.Helper()
		fn, hash := progressStream(strings.Repeat("a", 10), strings.Repeat("b", 3))
		text, usage, err := ProviderCompleter{StreamFn: fn}.CompleteSimple(ctx, &ai.Model{}, "system", messages, ai.StreamOptions{})
		if err != nil || usage == nil {
			t.Fatalf("CompleteSimple: %v, usage %v", err, usage)
		}
		return text, *hash
	}
	baseText, baseHash := run(context.Background())
	progress := new(compactprogress.Progress)
	text, hash := run(compactprogress.With(context.Background(), progress))
	if text != baseText || hash != baseHash {
		t.Fatalf("with a Progress: text %q hash %x; without: %q %x", text, hash, baseText, baseHash)
	}
	// 10 + 3 text characters and the 5 of a thinking delta, as tokens of four characters rounded up
	if got, want := progress.Tokens(), 5; got != want {
		t.Fatalf("Progress.Tokens() = %d, want %d", got, want)
	}
	// the next request starts again from zero
	fn, _ := progressStream("cc")
	if _, _, err := (ProviderCompleter{StreamFn: fn}).CompleteSimple(compactprogress.With(context.Background(), progress), &ai.Model{}, "system", messages, ai.StreamOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, want := progress.Tokens(), 2; got != want {
		t.Fatalf("Progress.Tokens() after a second request = %d, want %d (reset per request)", got, want)
	}
}

package compaction

// pi: packages/coding-agent/src/core/compaction/compaction.ts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

type compactCtxKey struct{}

func splitTurnPrep() CompactionPreparation {
	return CompactionPreparation{
		FirstKeptEntryID: "keep-1",
		MessagesToSummarize: []agent.AgentMessage{{User: &agent.UserMessage{
			Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "history"}},
		}}},
		TurnPrefixMessages: []agent.AgentMessage{{User: &agent.UserMessage{
			Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "prefix"}},
		}}},
		IsSplitTurn: true,
		Settings:    CompactionSettings{ReserveTokens: 1000},
	}
}

func isPrefixRequest(messages []agent.AgentMessage) bool {
	for _, block := range messages[0].ContentBlocks() {
		if text, ok := block.(ai.TextContent); ok && strings.Contains(text.Text, turnPrefixSummarizationPrompt) {
			return true
		}
	}
	return false
}

// The caller's signal reaches the history summary request and the turn-prefix request of a split turn
// (compaction.ts:984-1009 pass `signal` to both), and aborting it stops the sequence: a request in flight ends with the
// abort and no later request starts.
func TestCompactSignalReachesEverySplitTurnRequestAndAbortsTheSequence(t *testing.T) {
	for _, abortDuring := range []string{"history", "prefix"} {
		t.Run(abortDuring, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), compactCtxKey{}, "caller"))
			defer cancel()
			inFlight := make(chan struct{})
			var requests []string
			completer := simpleCompleterFunc(func(reqCtx context.Context, _ *ai.Model, _ string, messages []agent.AgentMessage, _ ai.StreamOptions) (string, *ai.Usage, error) {
				kind := "history"
				if isPrefixRequest(messages) {
					kind = "prefix"
				}
				requests = append(requests, kind)
				if reqCtx.Value(compactCtxKey{}) != "caller" {
					t.Errorf("%s request context does not derive from the caller context", kind)
				}
				if kind != abortDuring {
					return kind, nil, nil
				}
				close(inFlight)
				<-reqCtx.Done()
				return "", nil, reqCtx.Err()
			})
			done := make(chan error, 1)
			go func() {
				_, err := Compact(ctx, splitTurnPrep(), &ai.Model{}, "", nil, "", "", CompleterStreamFn(completer), nil, nil, ai.RetryCallbacks{}, "")
				done <- err
			}()
			select {
			case <-inFlight:
			case <-time.After(5 * time.Second):
				t.Fatalf("the %s request did not start", abortDuring)
			}
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("Compact succeeded after the abort")
				}
				if abortDuring == "prefix" && err.Error() != "Turn prefix summarization failed: This operation was aborted" {
					t.Fatalf("error = %q", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("Compact did not return after the caller aborted during the %s request", abortDuring)
			}
			want := []string{"history"}
			if abortDuring == "prefix" {
				want = []string{"history", "prefix"}
			}
			if strings.Join(requests, ",") != strings.Join(want, ",") {
				t.Fatalf("requests = %v, want %v", requests, want)
			}
		})
	}
}

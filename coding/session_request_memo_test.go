package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// recordingDeltaProvider answers like scalingDeltaProvider and keeps the request it received.
type recordingDeltaProvider struct {
	scalingDeltaProvider
	mu       *sync.Mutex
	requests *[]string
}

func (p recordingDeltaProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	messages := make([]ai.Message, transcript.Len())
	for i := range messages {
		messages[i] = transcript.At(i)
	}
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	*p.requests = append(*p.requests, string(raw))
	p.mu.Unlock()
	return p.scalingDeltaProvider.Stream(ctx, transcript, options)
}

var requestTimestamp = regexp.MustCompile(`"timestamp":\d+`)

// A Session's requests are the same whatever the agent remembers of earlier conversions: after each of a context edit, a fork back and forward, and a thinking-level change, the next request equals the one an agent that converts the whole history every time builds (agent.ConvertToLLM as its ConvertToLlm).
func TestSessionRequestsMatchTheStandardConversionAfterHistoryOperations(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	run := func(standard bool) []string {
		var mu sync.Mutex
		var requests []string
		// Each answer carries the same durationMs: the stream would otherwise measure it per run, and a loaded host makes the two runs' requests differ by whole milliseconds while the conversion is identical.
		provider := recordingDeltaProvider{scalingDeltaProvider: scalingDeltaProvider{deltas: 2, durationMs: 7}, mu: &mu, requests: &requests}
		model := &ai.Model{ID: "scaling-model", DisplayName: "scaling", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 10_000_000, MaxThinking: ai.ThinkingLevelHigh}}
		s, err := NewSession(services, SessionOptions{Model: model, SessionManager: scalingSessionManager(t, 40), ResourceLoader: NoResources, SkipBuiltinTools: true, SystemPrompt: "Test"})
		if err != nil {
			t.Fatal(err)
		}
		s.Subscribe(func(agent.AgentEvent) {})
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			for event := range s.Events() {
				AcknowledgeEvent(event)
			}
		}()
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
			<-drained
		})
		if standard {
			s.agent.SetConvertToLlm(func(messages []agent.AgentMessage) ([]ai.Message, error) { return agent.ConvertToLLM(messages), nil })
		}
		send := func(step string) {
			if _, err := s.Send(context.Background(), "hello "+step); err != nil {
				t.Fatalf("%s: %v", step, err)
			}
		}
		users := s.SessionManager().GetMessageEntriesWithRole("user")
		send("first")
		send("second")
		// A context edit rewrites one earlier message of the branch.
		if _, err := s.SessionManager().AppendContextEdit(users[1].Base().ID, &icodingagent.ContextEditReplacement{Content: json.RawMessage(`"edited question"`)}); err != nil {
			t.Fatal(err)
		}
		s.refreshContext()
		send("after edit")
		// A fork back to an earlier entry (the history stays over the memo's minimum), then the next turn creates a new branch from it; the last fork leaves a context under the minimum, which the memo forgets.
		if err := s.Fork(users[35].Base().ID); err != nil {
			t.Fatal(err)
		}
		send("after fork back")
		if err := s.SetThinkingLevel(ai.ThinkingHigh); err != nil {
			t.Fatal(err)
		}
		send("after thinking change")
		if err := s.Fork(users[0].Base().ID); err != nil {
			t.Fatal(err)
		}
		send("after fork to the first message")
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(requests))
		for i, raw := range requests {
			out[i] = requestTimestamp.ReplaceAllString(raw, `"timestamp":0`)
		}
		return out
	}
	got, want := run(false), run(true)
	if len(got) != len(want) || len(got) < 6 {
		t.Fatalf("requests: %d with the memo, %d with the standard converter", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("request %d differs:\nmemo: %s\nwant: %s", i+1, got[i], want[i])
		}
	}
	// The edit must reach the provider: otherwise the test proves nothing about invalidation.
	if want := fmt.Sprintf("%q", "edited question"); !regexp.MustCompile(regexp.QuoteMeta("edited question")).MatchString(got[2]) {
		t.Fatalf("request 3 does not carry the edited message %s: %s", want, got[2])
	}
	// The requests are compared with their durations, not with the field removed: the earlier answers of the second request carry the one pinned value.
	if !regexp.MustCompile(`"durationMs":7[,}]`).MatchString(got[1]) {
		t.Fatalf("request 2 does not carry the pinned durationMs: %s", got[1])
	}
}

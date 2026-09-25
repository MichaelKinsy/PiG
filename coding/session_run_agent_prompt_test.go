package coding

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// RunAgentPrompt continues from input queued after a low-level run only once
// the Events consumer has handled every earlier event: upstream's session
// listeners run synchronously before agent.continue(), so the interactive
// mode's compaction_end handler has queued its messages before the retry
// request is built.
func TestRunAgentPromptWaitsForTheEventConsumerBeforeContinuing(t *testing.T) {
	services := newTestServices(t)
	provider := &scriptedProvider{responses: []scriptedResponse{
		fauxReply("first", ai.StopReasonStop, 0),
		fauxReply("second", ai.StopReasonStop, 0),
	}}
	session, err := NewSession(services, SessionOptions{Model: fakeModelWithProvider(provider), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	barrierSeen := make(chan struct{}, 4)
	release := make(chan struct{})
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for event := range session.Events() {
			if _, ok := event.(*sessionEventBarrier); ok {
				barrierSeen <- struct{}{}
				<-release
				AcknowledgeEvent(event)
			}
		}
	}()
	t.Cleanup(func() {
		_ = session.Close()
		<-consumerDone
	})

	done := make(chan error, 1)
	go func() {
		_, err := session.RunAgentPrompt(context.Background(), func(ctx context.Context) ([]agent.AgentMessage, error) {
			messages, err := session.agent.Send(ctx, "start")
			session.agent.FollowUp(agent.AgentMessage{User: &agent.UserMessage{
				Role: agent.RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "queued after the run"}}, Timestamp: time.Now().UnixMilli(),
			}})
			return messages, err
		})
		done <- err
	}()

	select {
	case <-barrierSeen:
	case <-time.After(5 * time.Second):
		t.Fatal("RunAgentPrompt did not wait for the event consumer before continuing")
	}
	if got := provider.callCount(); got != 1 {
		t.Fatalf("model calls before the consumer acknowledged = %d, want 1", got)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunAgentPrompt did not settle")
	}
	if got := provider.callCount(); got != 2 {
		t.Fatalf("model calls = %d, want 2", got)
	}
	provider.mu.Lock()
	second := provider.requests[1]
	provider.mu.Unlock()
	if !strings.Contains(second, "queued after the run") {
		t.Fatalf("continuation request lacks the queued message:\n%s", second)
	}
}

// A completed response whose input already exceeds the window compacts
// without retrying it (agent-session.ts _checkCompaction case 1 with
// stopReason "stop").
func TestCheckCompactionSilentOverflowCompactsWithoutRetry(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "overflow summary"}

	message := &agent.AssistantMessage{
		Role: "assistant", StopReason: "stop", Provider: "fake", ModelID: "fake-1",
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}},
		Usage:   &ai.Usage{Input: 8001, Output: 20}, Timestamp: time.Now().UnixMilli(),
	}
	if _, err := sess.inner.AppendMessage(agent.AgentMessage{Assistant: message}); err != nil {
		t.Fatal(err)
	}
	sess.refreshContext()

	continueRun, err := sess.checkCompaction(context.Background(), lastAssistantMessage(sess.agent.Messages()), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if continueRun {
		t.Fatal("a completed overflowing response must not be retried")
	}
	for _, entry := range sess.inner.Entries() {
		if entry.Base.Type == "context_edit" {
			t.Fatal("a completed response must stay in context, not be omitted")
		}
	}
	var end *agent.CompactionEndEvent
	for _, ev := range drainEvents(t, sess) {
		if e, ok := ev.(agent.CompactionEndEvent); ok {
			end = &e
		}
	}
	if end == nil || end.Reason != "overflow" || end.WillRetry || end.ErrorMessage != "" {
		t.Fatalf("compaction_end = %+v, want a successful overflow compaction without retry", end)
	}
}

// An error response without usage still compacts over the threshold, measured
// from the last valid usage plus the messages after it (agent-session.ts
// _checkCompaction estimate path).
func TestCheckCompactionErrorWithoutUsageEstimatesFromLastUsage(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "threshold summary"}
	model := fakeModel()
	model.Capabilities.ContextWindow = 200000
	sess.model.Store(model)

	now := time.Now().UnixMilli()
	for _, message := range []*agent.AssistantMessage{
		{Role: "assistant", StopReason: "stop", Provider: "fake", ModelID: "fake-1", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "large"}}, Usage: &ai.Usage{Input: 190000, Output: 10}, Timestamp: now},
		{Role: "assistant", StopReason: "error", Provider: "fake", ModelID: "fake-1", ErrorMessage: "boom", Timestamp: now + 1},
	} {
		if _, err := sess.inner.AppendMessage(agent.AgentMessage{Assistant: message}); err != nil {
			t.Fatal(err)
		}
	}
	sess.refreshContext()

	if _, err := sess.checkCompaction(context.Background(), lastAssistantMessage(sess.agent.Messages()), true, nil); err != nil {
		t.Fatal(err)
	}
	var end *agent.CompactionEndEvent
	for _, ev := range drainEvents(t, sess) {
		if e, ok := ev.(agent.CompactionEndEvent); ok {
			end = &e
		}
	}
	if end == nil || end.Reason != "threshold" || end.ErrorMessage != "" {
		t.Fatalf("compaction_end = %+v, want a threshold compaction from the usage estimate", end)
	}
	compacted := false
	for _, entry := range sess.inner.Entries() {
		var compaction icodingagent.CompactionEntry
		if entry.Base.Type == "compaction" && json.Unmarshal(entry.Raw(), &compaction) == nil {
			compacted = true
		}
	}
	if !compacted {
		t.Fatal("no compaction entry was appended")
	}
}

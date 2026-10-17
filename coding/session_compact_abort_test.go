package coding

import (
	"context"
	"fmt"
	"testing"
	"time"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream compact() starts with `await this.abort()`: manual compaction
// aborts the active run instead of waiting for it to finish.
func TestCompactAbortsActiveRun(t *testing.T) {
	svcs := newTestServicesSmallKeep(t)
	provider := &abortableProvider{started: make(chan struct{})}
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(provider), SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	go func() {
		for range sess.Events() { //nolint:revive // drain
		}
	}()
	for i := range 3 {
		if _, err := sess.inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("q%d", i)}}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := sess.inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: fmt.Sprintf("a%d", i)}}, StopReason: ai.StopReasonStop}}); err != nil {
			t.Fatal(err)
		}
	}
	sess.RefreshContext()
	sess.completer = &fakeCompleter{summary: "summary"}

	sent := make(chan struct{})
	go func() {
		defer close(sent)
		_, _ = sess.Send(context.Background(), "long task")
	}()
	<-provider.started
	compacted := make(chan error, 1)
	go func() { _, err := sess.Compact(context.Background(), ""); compacted <- err }()
	select {
	case err := <-compacted:
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Compact waited for the active run instead of aborting it")
	}
	<-sent
	entries := sess.inner.GetEntries()
	if last := entries[len(entries)-1]; last.Base().Type != "compaction" || !abortedRunPersisted(sess) {
		t.Fatalf("last entry %s, aborted run persisted %v; want the aborted run, then the compaction", last.Base().Type, abortedRunPersisted(sess))
	}
}

func abortedRunPersisted(sess *Session) bool {
	for _, entry := range sess.inner.GetEntries() {
		if message, ok := entry.(icodingagent.MessageEntry); ok && message.Message.Assistant != nil && message.Message.Assistant.StopReason == ai.StopReasonAborted {
			return true
		}
	}
	return false
}

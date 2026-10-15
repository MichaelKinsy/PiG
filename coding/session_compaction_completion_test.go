// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi compact() clears manual admission and synchronously emits compaction_end before resolving its Promise.
func TestManualCompactionWaitsForCompletionListener(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := autoQueueSession(t, true)
		suiteCompactionSeed(t, s)
		s.completer = &fakeCompleter{summary: "summary"}
		entered, release := make(chan struct{}), make(chan struct{})
		unsubscribe := s.Subscribe(func(event agent.AgentEvent) {
			if end, ok := event.(agent.CompactionEndEvent); ok && end.Reason == "manual" {
				close(entered)
				<-release
			}
		})
		defer unsubscribe()
		done := make(chan error, 1)
		go func() { _, err := s.Compact(t.Context(), ""); done <- err }()
		<-entered
		synctest.Wait()
		if s.IsCompacting() {
			t.Error("completion listener still owns manual compaction admission")
		}
		completed := false
		select {
		case err := <-done:
			completed = true
			t.Errorf("compact returned before its synchronous listener: %v", err)
		default:
		}
		close(release)
		if !completed {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
	})
}

func BenchmarkSummarizationRequestAuth(b *testing.B) {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	provider := &suiteBearerProvider{auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Faux bearer token", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
		return &ai.AuthResult{Auth: ai.ModelAuth{Headers: ai.ProviderHeadersFromStrings(map[string]string{"Authorization": "Bearer ambient-token"})}}, nil
	}}}}
	model := fakeModelWithProvider(provider)
	session, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := session.Close(); err != nil {
			b.Error(err)
		}
	})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := session.prepareSummarizationRequest(b.Context(), model); err != nil {
			b.Fatal(err)
		}
	}
}

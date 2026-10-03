package durableadapter_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// BenchmarkViewStatePublication measures one durable revision of a long transcript reaching a remote Transcript subscriber: the commit, the view frame, and the published operation batch.
func BenchmarkViewStatePublication(b *testing.B) {
	for _, entries := range []int{10, 1000} {
		b.Run(fmt.Sprintf("entries=%d", entries), func(b *testing.B) {
			opened := durabletest.OpenFauxConversation()
			defer func() { _ = opened.Close(context.Background()) }()
			conversation := opened.Conversation.Conversation
			text := strings.Repeat("transcript text ", 64)
			appendEntries := func(count int) {
				_, err := conversation.Commit(context.Background(), func(tx durable.Tx) (any, error) {
					for range count {
						if _, err := tx.AppendEntry(conversation.Id(), durable.EntryDraft{Kind: "message", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(text)}}}); err != nil {
							return nil, err
						}
					}
					return nil, nil
				})
				if err != nil {
					b.Fatal(err)
				}
			}
			appendEntries(entries)
			state, err := opened.Conversation.ViewState(context.Background())
			if err != nil {
				b.Fatal(err)
			}
			defer state.Dispose()
			provider, err := chord.NewRemoteServiceProvider(chord.SingletonService(services.TranscriptDefinition))
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = provider.Dispose() }()
			if err := chord.Provide[services.Transcript](provider, services.TranscriptDefinition, servedTranscript{state}); err != nil {
				b.Fatal(err)
			}
			published := make(chan struct{}, 1)
			subscription, err := provider.Subscribe(services.TranscriptDefinition.Id(), chord.ServiceSingleton, func(context.Context, chord.ServiceProviderUpdate) {
				published <- struct{}{}
			})
			if err != nil {
				b.Fatal(err)
			}
			if err := subscription.Activate(); err != nil {
				b.Fatal(err)
			}
			defer func() { _ = subscription.Close(context.Background()) }()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				appendEntries(1)
				<-published
			}
		})
	}
}

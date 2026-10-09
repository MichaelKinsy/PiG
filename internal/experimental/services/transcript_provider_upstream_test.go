package services_test

// pi: packages/coding-agent/src/experimental/services/transcript.ts

// pi: packages/coding-agent/src/experimental/services/transcript-provider.ts

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// packages/coding-agent/test/experimental-transcript-provider.test.ts:11
// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: zeroing the results of Conversation.Watch fails it
func TestPortWave08ExperimentalTranscriptProvider(t *testing.T) {
	t.Run("replicates the conversation view as it changes", func(t *testing.T) {
		durable := durabletest.OpenFauxConversation(durabletest.Text("answer"))
		t.Cleanup(func() { _ = durable.Harness.Close(context.Background()) })
		// The served state is conversation.viewState (transcript-provider.ts:6-13), so every view the consumer sees is one exact durable frame: the oracle watch attaches first and sees the same revisions.
		watch, err := durable.Conversation.Conversation.Watch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = watch.Stop() })
		var mu sync.Mutex
		frames := []string{encodeView(t, watch.Value())}
		watch.Start(func(_ context.Context, value services.ConversationView, _ [][]any) error {
			mu.Lock()
			defer mu.Unlock()
			frames = append(frames, encodeView(t, value))
			return nil
		})
		var views []services.ConversationView
		consumer := chord.Facet{Id: "test-transcript-consumer", Setup: func(env *chord.FacetEnvironment) error {
			ref, err := chord.UseService(env, services.TranscriptDefinition)
			if err != nil {
				return err
			}
			return env.OnActivate(func(context.Context) error {
				transcript, err := ref.Get()
				if err != nil {
					return err
				}
				unsubscribe, err := transcript.State().Subscribe(func(value services.ConversationView, _ context.Context, _ chord.ReplicatedStateDelivery) {
					mu.Lock()
					defer mu.Unlock()
					views = append(views, value)
				})
				if err != nil {
					return err
				}
				return env.Own(func(context.Context) error { unsubscribe(); return nil })
			})
		}}
		facet, err := services.CreateTranscriptServiceFacet(t.Context(), durable.Conversation)
		if err != nil {
			t.Fatal(err)
		}
		host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: []chord.Facet{facet, consumer}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = host.Dispose(context.Background()) })
		kinds := func() []string {
			mu.Lock()
			defer mu.Unlock()
			if len(views) == 0 {
				return nil
			}
			kinds := []string{}
			for _, entry := range views[len(views)-1].Entries {
				kinds = append(kinds, entry.Kind)
			}
			return kinds
		}
		mu.Lock()
		first := views[0]
		mu.Unlock()
		if len(first.Entries) != 0 {
			t.Fatalf("first view entries = %v", first.Entries)
		}
		id, err := durable.Conversation.Submit(t.Context(), ai.UserText("question"), "reject")
		if err != nil {
			t.Fatal(err)
		}
		submission, err := durable.Harness.Submission(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := submission.Wait(t.Context()); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		// The oracle watch delivers on its own schedule, so wait until it has caught up with the consumer's last view too.
		caughtUp := func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(views) > 0 && len(frames) > 0 && frames[len(frames)-1] == encodeView(t, views[len(views)-1])
		}
		for !slices.Equal(kinds(), []string{"pi.user", "pi.assistant"}) || !caughtUp() {
			if time.Now().After(deadline) {
				t.Fatalf("view entry kinds = %v", kinds())
			}
			time.Sleep(5 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		seen := make([]string, len(views))
		for index, view := range views {
			seen[index] = encodeView(t, view)
		}
		if !slices.Equal(seen, frames) {
			t.Fatalf("consumer views differ from the durable frames\n got %q\nwant %q", seen, frames)
		}
	})
}

func encodeView(t *testing.T, view services.ConversationView) string {
	t.Helper()
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

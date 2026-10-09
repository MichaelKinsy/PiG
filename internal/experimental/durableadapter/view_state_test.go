package durableadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// The Transcript service serves conversation.viewState directly (packages/coding-agent/src/experimental/services/transcript-provider.ts:6-13): a Chord state attached to the durable view source, whose frames carry the exact operations of each durable revision (packages/durable/src/harness/view.ts:92-104, packages/chord/src/types.ts:73-109). Chord publishes those operations; it never re-diffs the view.

type servedTranscript struct{ state services.TranscriptViewState }

func (served servedTranscript) State() chord.ReplicatedStateOf[services.ConversationView] {
	return served.state
}

func encode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// frameLog records an oracle stream of exact frames: the encoded value and operation batch of each revision.
type frameLog struct {
	mu     sync.Mutex
	values []string
	ops    []string
}

func (log *frameLog) add(value, ops string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.values = append(log.values, value)
	log.ops = append(log.ops, ops)
}

func (log *frameLog) snapshot() (values, ops []string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.values), slices.Clone(log.ops)
}

// watchFrames starts an oracle watch of the root conversation's view. It returns the watch's acquisition value and its frames.
func watchFrames(t *testing.T, opened *durabletest.FauxConversation) (string, *frameLog) {
	t.Helper()
	watch, err := opened.Conversation.Conversation.Watch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = watch.Stop() })
	acquired := encode(t, watch.Value())
	log := &frameLog{}
	watch.Start(func(_ context.Context, value harness.ConversationView, ops []durable.Op) error {
		encodedValue, err := json.Marshal(value)
		if err != nil {
			return err
		}
		encodedOps, err := json.Marshal(ops)
		if err != nil {
			return err
		}
		log.add(string(encodedValue), string(encodedOps))
		return nil
	})
	return acquired, log
}

// publishedOps serves the state through a remote service provider and records the operation batches and sequences it publishes for the state member, as a remote Transcript replica receives them.
func publishedOps(t *testing.T, state services.TranscriptViewState) (*frameLog, func() []int) {
	t.Helper()
	provider, err := chord.NewRemoteServiceProvider(chord.SingletonService(services.TranscriptDefinition))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Dispose() })
	if err := chord.Provide[services.Transcript](provider, services.TranscriptDefinition, servedTranscript{state}); err != nil {
		t.Fatal(err)
	}
	log := &frameLog{}
	var mu sync.Mutex
	var sequences []int
	subscription, err := provider.Subscribe(services.TranscriptDefinition.Id(), chord.ServiceSingleton, func(_ context.Context, update chord.ServiceProviderUpdate) {
		if update.Type != chord.UpdateState || update.Member != "state" {
			return
		}
		encoded, err := json.Marshal(update.Ops)
		if err != nil {
			panic(err)
		}
		log.add("", string(encoded))
		mu.Lock()
		sequences = append(sequences, update.Sequence)
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := subscription.Activate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = subscription.Close(context.Background()) })
	return log, func() []int {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(sequences)
	}
}

func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func prompt(t *testing.T, opened *durabletest.FauxConversation, text string) {
	t.Helper()
	id, err := opened.Conversation.Submit(t.Context(), ai.UserText(text), durable.WhenBusyReject)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := opened.Harness.Submission(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := submission.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestViewState(t *testing.T) {
	t.Run("publishes each durable view revision with its exact operations", func(t *testing.T) {
		opened := durabletest.OpenFauxConversation(durabletest.Text("first answer"), durabletest.Text("second answer"))
		t.Cleanup(func() { _ = opened.Close(context.Background()) })
		acquired, frames := watchFrames(t, opened)
		state, err := opened.Conversation.ViewState(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(state.Dispose)
		if got := encode(t, state.Value()); got != acquired {
			t.Fatalf("hydrated value\n got %s\nwant %s", got, acquired)
		}
		var mu sync.Mutex
		var delivered []string
		unsubscribe, err := state.Subscribe(func(value services.ConversationView, _ context.Context, delivery chord.ReplicatedStateDelivery) {
			encoded, err := json.Marshal(value)
			if err != nil {
				panic(err)
			}
			mu.Lock()
			defer mu.Unlock()
			delivered = append(delivered, string(encoded))
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(unsubscribe)
		published, sequences := publishedOps(t, state)

		prompt(t, opened, "first question")
		prompt(t, opened, "second question")
		if err := opened.Conversation.Conversation.WaitForIdle(t.Context()); err != nil {
			t.Fatal(err)
		}
		eventually(t, "the second answer in the oracle frames", func() bool {
			values, _ := frames.snapshot()
			return len(values) > 0 && containsEntries(t, values[len(values)-1], 4)
		})
		values, ops := frames.snapshot()
		eventually(t, "every oracle frame published", func() bool {
			_, got := published.snapshot()
			return len(got) >= len(ops)
		})
		if _, got := published.snapshot(); !slices.Equal(got, ops) {
			t.Fatalf("published operation batches differ from the durable frames\n got %q\nwant %q", got, ops)
		}
		want := make([]int, len(ops))
		for index := range want {
			want[index] = index + 1
		}
		if got := sequences(); !slices.Equal(got, want) {
			t.Fatalf("published sequences = %v, want %v", got, want)
		}
		// Subscriber delivery trails frame publication, so wait for the last frame to arrive before comparing.
		wantValues := append([]string{acquired}, values...)
		eventually(t, "every durable frame delivered to the subscriber", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(delivered) >= len(wantValues)
		})
		mu.Lock()
		got := slices.Clone(delivered)
		mu.Unlock()
		if !slices.Equal(got, wantValues) {
			t.Fatalf("delivered values differ from the durable frames\n got %q\nwant %q", got, wantValues)
		}
		if got := encode(t, state.Value()); got != values[len(values)-1] {
			t.Fatalf("value\n got %s\nwant %s", got, values[len(values)-1])
		}
	})

	t.Run("publishes every revision's exact operations behind a held subscriber", func(t *testing.T) {
		opened := durabletest.OpenFauxConversation()
		t.Cleanup(func() { _ = opened.Close(context.Background()) })
		state, err := opened.Conversation.ViewState(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(state.Dispose)
		published, _ := publishedOps(t, state)
		// A subscriber that holds its first update holds the source's frame delivery. conversation.viewState is a CommittedStateSource whose attachment buffers every later frame without bound (packages/durable/src/session/observation.ts:SessionSourceAttachment.publish), unlike a watch, which replaces more than 100 pending frames with one root replacement (CommittedWatch.advance). Every revision therefore publishes its own operations, none a root replacement.
		held, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unsubscribe, err := state.Subscribe(func(_ services.ConversationView, _ context.Context, delivery chord.ReplicatedStateDelivery) {
			if delivery.Kind == chord.DeliveryUpdate {
				once.Do(func() {
					close(held)
					<-release
				})
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(unsubscribe)
		levels := []ai.ModelThinkingLevel{ai.ThinkingLow, ai.ThinkingHigh}
		configure := func(index int) {
			level := levels[index%2]
			if err := opened.Conversation.Configure(t.Context(), services.ConversationConfiguration{ThinkingLevel: &level}); err != nil {
				t.Fatal(err)
			}
		}
		// The Session state attachment runs its subscribers on the committing goroutine before the commit returns
		// (observation.ts drains in a microtask before the committer resumes), so the held revision commits off the
		// test goroutine and returns once the subscriber is released.
		heldCommitted := make(chan struct{})
		go func() {
			defer close(heldCommitted)
			level := levels[0]
			if err := opened.Conversation.Configure(t.Context(), services.ConversationConfiguration{ThinkingLevel: &level}); err != nil {
				t.Error(err)
			}
		}()
		select {
		case <-held:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the first update")
		}
		// An odd count ends on the level the held revision does not have, so the newest value differs from it.
		const revisions = 151
		for index := 1; index <= revisions; index++ {
			configure(index)
		}
		close(release)
		<-heldCommitted
		final := encode(t, viewOf(t, opened))
		eventually(t, "the newest revision", func() bool { return encode(t, state.Value()) == final })
		eventually(t, "every revision published", func() bool {
			_, batches := published.snapshot()
			return len(batches) >= revisions+1
		})
		_, batches := published.snapshot()
		if replaced := slices.ContainsFunc(batches, func(batch string) bool { return len(batch) > 6 && batch[:6] == `[["r",` }); replaced {
			t.Fatalf("a root replacement among %d published batches", len(batches))
		}
		if len(batches) != revisions+1 {
			t.Fatalf("%d published batches, want one per revision, %d", len(batches), revisions+1)
		}
		// The next revision's operations apply to the published value.
		configure(revisions + 1)
		next := encode(t, viewOf(t, opened))
		eventually(t, "the revision after the replacement", func() bool { return encode(t, state.Value()) == next })
	})

	t.Run("keeps publishing after the attaching context ends and stops after dispose", func(t *testing.T) {
		opened := durabletest.OpenFauxConversation(durabletest.Text("first answer"), durabletest.Text("second answer"))
		t.Cleanup(func() { _ = opened.Close(context.Background()) })
		ctx, cancel := context.WithCancel(t.Context())
		state, err := opened.Conversation.ViewState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		published, _ := publishedOps(t, state)
		prompt(t, opened, "first question")
		eventually(t, "the first answer", func() bool { return len(state.Value().Entries) == 2 })
		state.Dispose()
		_, before := published.snapshot()
		prompt(t, opened, "second question")
		if err := opened.Conversation.Conversation.WaitForIdle(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := len(viewOf(t, opened).Entries); got != 4 {
			t.Fatalf("durable view has %d entries, want 4", got)
		}
		// Delivery to the subscriber trails publication, so a batch published before Dispose can still arrive after the
		// snapshot. What must not arrive is anything from the revision made after Dispose.
		if _, after := published.snapshot(); len(after) < len(before) {
			t.Fatalf("published log shrank: %d -> %d", len(before), len(after))
		} else {
			for _, ops := range after[len(before):] {
				if strings.Contains(ops, "second question") || strings.Contains(ops, "second answer") {
					t.Fatalf("a batch from after dispose was published: %s", ops)
				}
			}
		}
		if got := len(state.Value().Entries); got != 2 {
			t.Fatalf("disposed state has %d entries, want the last published 2", got)
		}
	})

	t.Run("fails with the cause of an attaching context that already ended", func(t *testing.T) {
		opened := durabletest.OpenFauxConversation()
		t.Cleanup(func() { _ = opened.Close(context.Background()) })
		cause := errors.New("attach cancelled")
		ctx, cancel := context.WithCancelCause(t.Context())
		cancel(cause)
		if _, err := opened.Conversation.ViewState(ctx); !errors.Is(err, cause) {
			t.Fatalf("err = %v, want %v", err, cause)
		}
	})
}

func containsEntries(t *testing.T, encoded string, count int) bool {
	t.Helper()
	var view services.ConversationView
	if err := json.Unmarshal([]byte(encoded), &view); err != nil {
		t.Fatal(err)
	}
	return len(view.Entries) == count
}

func viewOf(t *testing.T, opened *durabletest.FauxConversation) services.ConversationView {
	t.Helper()
	watch, err := opened.Conversation.Conversation.Watch(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = watch.Stop() }()
	return watch.Value()
}

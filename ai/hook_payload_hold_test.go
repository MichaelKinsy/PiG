package ai

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// hookedProviders are the providers that hand a before_provider_request hook a map payload and so hold its member order: Google and Vertex (one request path)
// and pi-messages. Only a request with a hook registered reaches the hold. A build with pig_strip_google_vertex has no Vertex provider and leaves its row out.
func hookedProviders() map[string]Provider {
	mapping := ThinkingLevelMap{ThinkingOff: nil, ThinkingMinimal: nil, ThinkingLow: new("low"), ThinkingMedium: new("medium"), ThinkingHigh: new("high")}
	providers := map[string]Provider{
		"google":      NewGoogleProvider(GoogleConfig{APIKey: "test", Model: "gemini-3.8-flash", ProviderID: "test-google", ThinkingLevelMap: mapping}),
		"pi-messages": NewPiMessagesProvider(PiMessagesConfig{BaseURL: "https://example.invalid", APIKey: "test", Model: "auto", ProviderID: "radius"}),
	}
	if BuiltAPI(APIGoogleVertex) {
		providers["vertex"] = newGoogleVertexTestProvider(GoogleVertexConfig{APIKey: "test", Model: "gemini-3.8-flash", ProviderID: "test-vertex", BaseURL: "https://example.invalid/v1", ThinkingLevelMap: mapping}, nil)
	}
	return providers
}

// requestToHook builds the request of provider up to its onPayload hook and returns what the request returned there. Google builds it before Stream returns;
// pi-messages builds it in the goroutine of its stream, so its payload builder is the call that runs the hook.
func requestToHook(ctx context.Context, provider Provider, transcript TranscriptContext, options StreamOptions) error {
	if piMessages, ok := provider.(*piMessagesProvider); ok {
		_, err := piMessages.payload(transcript, options)
		return err
	}
	_, err := provider.Stream(ctx, transcript, options)
	return err
}

func hookedTranscript() TranscriptContext {
	return NormalizeContext(Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserText("Explain the next step.")}}})
}

func benchmarkHookedRequest(b *testing.B, name string) {
	provider, transcript := hookedProviders()[name], hookedTranscript()
	captured := errors.New("payload captured")
	options := StreamOptions{IsReasoning: true, Thinking: ThinkingLevelMedium, OnPayload: func(any, *Model) (any, error) { return nil, captured }}
	b.ReportAllocs()
	for b.Loop() {
		if err := requestToHook(b.Context(), provider, transcript, options); !errors.Is(err, captured) {
			b.Fatal(err)
		}
	}
}

func BenchmarkPiMessagesHookedRequest(b *testing.B) { benchmarkHookedRequest(b, "pi-messages") }

// A hook that panics, as an extension's handler can, must not leave its payload held: the entry would name an address the collector can hand to a later map,
// which would then be written in the order of a request that is long gone.
func TestHookPayloadIsReleasedWhenTheHookPanics(t *testing.T) {
	for name, provider := range hookedProviders() {
		t.Run(name, func(t *testing.T) {
			var seen map[string]any
			options := StreamOptions{IsReasoning: true, Thinking: ThinkingLevelMedium, OnPayload: func(payload any, _ *Model) (any, error) {
				seen = payload.(map[string]any)
				if _, held, err := MarshalHookPayload(seen); err != nil || !held {
					t.Errorf("the payload is not held inside the hook: held=%v err=%v", held, err)
				}
				panic("handler failed")
			}}
			func() {
				defer func() {
					if recover() == nil {
						t.Error("the hook's panic did not reach the caller")
					}
				}()
				_ = requestToHook(t.Context(), provider, hookedTranscript(), options)
			}()
			if seen == nil {
				t.Fatal("the hook did not run")
			}
			if _, held, _ := MarshalHookPayload(seen); held {
				t.Fatal("a payload whose hook panicked is still held")
			}
		})
	}
}

// A hook that returns, and one that fails, release the payload as well.
func TestHookPayloadIsReleasedWhenTheHookReturns(t *testing.T) {
	payload := map[string]any{"model": "m"}
	order := &HookObjectOrder{Keys: []string{"model"}}
	for _, hookErr := range []error{nil, errors.New("rejected")} {
		_, err := callPayloadHook(func(value any, _ *Model) (any, error) {
			if _, held, _ := MarshalHookPayload(value); !held {
				t.Error("the payload is not held inside the hook")
			}
			return nil, hookErr
		}, payload, order, nil)
		if !errors.Is(err, hookErr) {
			t.Fatalf("err = %v, want %v", err, hookErr)
		}
		if _, held, _ := MarshalHookPayload(payload); held {
			t.Fatalf("the payload is still held after a hook that returned %v", hookErr)
		}
	}
}

// Many requests hold their payloads at once, well past any capacity a registry starts with: every one finds its own order, and none is left at the end.
func TestHookPayloadsHeldAtOnceEachFindTheirOwnOrder(t *testing.T) {
	const requests = 1000
	var holding, written sync.WaitGroup
	holding.Add(requests)
	written.Add(requests)
	allHeld := make(chan struct{})
	failures := make(chan string, requests)
	for i := range requests {
		go func() {
			defer written.Done()
			key := fmt.Sprintf("k%d", i)
			payload := map[string]any{"a": 1, key: 2}
			order := &HookObjectOrder{Keys: []string{key, "a"}}
			_, _ = callPayloadHook(func(value any, _ *Model) (any, error) {
				holding.Done()
				<-allHeld
				encoded, held, err := MarshalHookPayload(value)
				if want := fmt.Sprintf(`{"%s":2,"a":1}`, key); err != nil || !held || string(encoded) != want {
					failures <- fmt.Sprintf("request %d: %s held=%v err=%v, want %s", i, encoded, held, err, want)
				}
				return nil, nil
			}, payload, order, nil)
		}()
	}
	holding.Wait()
	if n := heldHookPayloadCount(); n != requests {
		t.Fatalf("%d payloads held, want %d", n, requests)
	}
	close(allHeld)
	written.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
	if n := heldHookPayloadCount(); n != 0 {
		t.Fatalf("%d payloads still held", n)
	}
}

func heldHookPayloadCount() int {
	hookPayloads.Lock()
	defer hookPayloads.Unlock()
	return len(hookPayloads.held)
}

package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// The classifier modules (api/{openai-decisions,system-one-shared,llama-cpp-classify}.ts over classifier-shared.ts postClassifierRequest) await onPayload,
// then the request inside retryProviderRequest, then onResponse, and map a failure to the result: "aborted" when the caller's signal aborted, "error"
// otherwise. A rejection of either hook ends the call with that error and sends nothing more; a cancellation that arrives while the request is in flight
// or while the retry delay runs ends the call at once with the signal's outcome and makes no further attempt.
func TestClassifiersEndWithTheOutcomeOfTheRequestLifecycle(t *testing.T) {
	classifiers := []struct {
		name     string
		classify ClassifierFunction
		model    ClassifierModel
		request  ClassifierContext
	}{
		{"openai-decisions", ClassifyOpenAIDecisions, openAIDecisionsTestModel(), openAIDecisionsTestContext()},
		{"typesafe-system-one", ClassifyTypesafeSystemOne, typesafeTestModel(), typesafeTestContext()},
		{"llama-cpp", ClassifyLlamaCpp, llamaTestModel(), llamaTestContext()},
	}
	for _, c := range classifiers {
		t.Run(c.name+"/cancelled while the request is in flight", func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered := make(chan struct{}, 8)
			fetch := func(r *http.Request) (*http.Response, error) {
				entered <- struct{}{}
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			done := make(chan ClassifierResult, 1)
			go func() {
				done <- c.classify(ctx, c.model, c.request, ClassifierOptions{APIKey: "secret", Fetch: clsClient(fetch)})
			}()
			<-entered
			cancel()
			if result := <-done; string(result.StopReason) != "aborted" {
				t.Fatalf("stop reason %q (%s), want aborted", result.StopReason, result.ErrorMessage)
			}
		})
		t.Run(c.name+"/cancelled while the retry delay runs", func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var mu sync.Mutex
			attempts := map[string]int{}
			fetch := func(r *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				attempts[r.URL.Path+" "+string(body)]++
				mu.Unlock()
				cancel()
				return clsText(http.StatusServiceUnavailable, "busy", nil), nil
			}
			result := c.classify(ctx, c.model, c.request, ClassifierOptions{APIKey: "secret", Fetch: clsClient(fetch)})
			mu.Lock()
			defer mu.Unlock()
			if string(result.StopReason) != "aborted" || len(attempts) == 0 {
				t.Fatalf("stop reason %q (%s) after %v, want aborted", result.StopReason, result.ErrorMessage, attempts)
			}
			// llama.cpp sends several sibling requests at once; the others make exactly one attempt in all.
			total := 0
			for _, n := range attempts {
				total += n
			}
			if c.name != "llama-cpp" && total != 1 {
				t.Fatalf("%d attempts after the cancellation, want one", total)
			}
		})
		t.Run(c.name+"/onPayload rejection sends nothing", func(t *testing.T) {
			var calls atomic.Int32
			fetch := func(r *http.Request) (*http.Response, error) {
				if resp := llamaPreludeResponse(r); resp != nil {
					return resp, nil
				}
				calls.Add(1)
				return clsJSON(200, `{}`), nil
			}
			result := c.classify(t.Context(), c.model, c.request, ClassifierOptions{APIKey: "secret", Fetch: clsClient(fetch), OnPayload: func(any, ClassifierModel) (any, bool, error) {
				return nil, false, errors.New("payload hook rejected")
			}})
			if string(result.StopReason) != "error" || !strings.Contains(result.ErrorMessage, "payload hook rejected") || calls.Load() != 0 {
				t.Fatalf("stop reason %q (%s) after %d requests", result.StopReason, result.ErrorMessage, calls.Load())
			}
		})
		t.Run(c.name+"/onResponse rejection is the result", func(t *testing.T) {
			fetch := func(r *http.Request) (*http.Response, error) {
				if resp := llamaPreludeResponse(r); resp != nil {
					return resp, nil
				}
				return clsJSON(200, `{}`), nil
			}
			result := c.classify(t.Context(), c.model, c.request, ClassifierOptions{APIKey: "secret", Fetch: clsClient(fetch), OnResponse: func(ProviderResponse, ClassifierModel) error {
				return errors.New("response hook rejected")
			}})
			if string(result.StopReason) != "error" || !strings.Contains(result.ErrorMessage, "response hook rejected") {
				t.Fatalf("stop reason %q (%s)", result.StopReason, result.ErrorMessage)
			}
		})
	}
}

var llamaPreludeTokens atomic.Int32

// llamaPreludeResponse answers the requests llama.cpp's classifier makes before its observed completion (template rendering and label tokenization); other
// classifiers' requests get nil.
func llamaPreludeResponse(r *http.Request) *http.Response {
	switch {
	case strings.HasSuffix(r.URL.Path, "/tokenize"):
		return clsJSON(200, fmt.Sprintf(`{"tokens":[%d]}`, llamaPreludeTokens.Add(1)))
	case strings.HasSuffix(r.URL.Path, "/apply-template"):
		return clsJSON(200, `{"prompt":"p"}`)
	}
	return nil
}

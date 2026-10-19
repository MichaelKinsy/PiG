package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

// pi: packages/ai/src/api/llama-cpp-classify.ts:307, 316, 401 (await Promise.all([...]))

// Promise.all rejects with the first rejection in time and starts no abort: the siblings were issued already and run on under the caller's signal. runJoined keeps the siblings owned: it waits for them, and returns
// the first failure in time (not the first listed task's) with the caller's context untouched.
func TestRunJoinedLetsSiblingsRunToCompletionAfterAFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		early, late := errors.New("early"), errors.New("late")
		release := make(chan struct{})
		var siblingContextErr error
		var siblingFinished bool
		done := make(chan error, 1)
		go func() {
			done <- runJoined(t.Context(),
				func(ctx context.Context) error {
					<-release
					siblingContextErr = ctx.Err()
					siblingFinished = true
					return late
				},
				func(context.Context) error { return early },
			)
		}()
		// The failing task is done and the sibling still waits: nothing may have cancelled it or returned yet.
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("runJoined returned %v while a sibling was still running", err)
		default:
		}
		close(release)
		err := <-done
		if !siblingFinished || siblingContextErr != nil {
			t.Errorf("sibling finished=%v context error=%v; it must run to completion under the caller's context", siblingFinished, siblingContextErr)
		}
		if !errors.Is(err, early) {
			t.Errorf("runJoined = %v, want the first failure in time %v", err, early)
		}
	})
}

// Every request Pi's first wave sends reaches the server whichever of its concurrent calls fails first. The template is the first rejection here, while every label lookup's tokenize requests are in flight; they
// finish under an uncancelled context and nothing is sent afterwards, as in Pi.
func TestLlamaCppClassifyTemplateFailureLeavesLabelLookupsRunning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		var mu sync.Mutex
		var paths []string
		var tokenizeContextErrs []error
		client := clsClient(func(r *http.Request) (*http.Response, error) {
			body := clsBody(t, r)
			mu.Lock()
			paths = append(paths, r.URL.Path)
			mu.Unlock()
			switch r.URL.Path {
			case "/apply-template":
				return clsText(400, `{"error":{"message":"bad template"}}`, nil), nil
			case "/tokenize":
				<-gate
				data, _ := json.Marshal(map[string]any{"tokens": wordTokens(body["content"].(string))})
				mu.Lock()
				tokenizeContextErrs = append(tokenizeContextErrs, r.Context().Err())
				mu.Unlock()
				return clsJSON(200, string(data)), nil
			}
			return clsText(404, "not found", nil), nil
		})
		request := ClassifierContext{State: JsonObject{}, Questions: ClassifierQuestions{{ID: "ok", Question: ClassifierBoolQuestion{Instructions: "OK?"}}}}
		results := make(chan ClassifierResult, 1)
		go func() {
			results <- ClassifyLlamaCpp(t.Context(), llamaTestModel(), request, ClassifierOptions{Fetch: client})
		}()

		// The template has failed and both labels' lookups ("\n" and "\n"+label for Yes and No) are blocked in their requests.
		synctest.Wait()
		want := []string{"/apply-template", "/tokenize", "/tokenize", "/tokenize", "/tokenize"}
		mu.Lock()
		first := slices.Sorted(slices.Values(paths))
		mu.Unlock()
		if !slices.Equal(first, want) {
			t.Fatalf("requests before the lookups answer = %v, want %v", first, want)
		}
		select {
		case result := <-results:
			t.Fatalf("classification ended (%+v) while label lookups were still in flight", result)
		default:
		}

		close(gate)
		result := <-results
		if result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "llama.cpp error (400)") || !strings.Contains(result.ErrorMessage, "bad template") {
			t.Errorf("result = %+v, want the template's 400", result)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(paths) != len(want) {
			t.Errorf("requests = %v, want only the first wave %v", paths, want)
		}
		if len(tokenizeContextErrs) != 4 {
			t.Fatalf("%d lookups answered, want 4", len(tokenizeContextErrs))
		}
		for _, err := range tokenizeContextErrs {
			if err != nil {
				t.Errorf("a label lookup's request was cancelled by its sibling's failure: %v", err)
			}
		}
	})
}

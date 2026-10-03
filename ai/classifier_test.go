package ai

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// JavaScript object key order: array-index keys first in ascending order, then insertion order.
func TestJSObjectKeyOrder(t *testing.T) {
	keys := []string{"b", "10", "a", "2", "01", "1", "4294967295", "4294967294"}
	var got []string
	for _, index := range jsObjectKeyOrder(keys) {
		got = append(got, keys[index])
	}
	if want := []string{"1", "2", "10", "4294967294", "b", "a", "01", "4294967295"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order=%v want %v", got, want)
	}
}

func TestClassifierQuestionsJSONRoundTripKeepsOrder(t *testing.T) {
	questions := ClassifierQuestions{
		{ID: "z", Question: ClassifierBoolQuestion{Instructions: "Yes?", Criteria: ClassifierBoolCriteria{True: "y", False: "n"}}},
		{ID: "a", Question: ClassifierChoiceQuestion{Instructions: "Which?", Criteria: clsChoices("second", "2", "first", "1")}},
		{ID: "m", Question: ClassifierScoreQuestion{Instructions: "How much?", Criteria: []string{"low", "high"}}},
	}
	data, err := json.Marshal(questions)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ClassifierQuestions
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, questions) {
		t.Fatalf("decoded=%+v want %+v (%s)", decoded, questions, data)
	}
}

// retryProviderRequest retries a retryable status up to the budget, and never retries a failure that carries no status
// (.upstream/v0.99.1/packages/ai/src/utils/provider-retry.ts:100-118).
func TestClassifierRetriesOnlyProviderErrors(t *testing.T) {
	model := typesafeTestModel()
	t.Run("retries a 500 up to the default budget", func(t *testing.T) {
		var calls atomic.Int32
		result := ClassifyTypesafeSystemOne(t.Context(), model, typesafeTestContext(), ClassifierOptions{APIKey: "k", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return clsText(500, "down", map[string]string{"retry-after-ms": "0"}), nil
		})})
		if calls.Load() != 3 || result.StopReason != "error" || !strings.Contains(result.ErrorMessage, "System One API error (500)") {
			t.Fatalf("calls=%d result=%+v", calls.Load(), result)
		}
	})
	t.Run("does not retry a transport failure", func(t *testing.T) {
		var calls atomic.Int32
		result := ClassifyTypesafeSystemOne(t.Context(), model, typesafeTestContext(), ClassifierOptions{APIKey: "k", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("connection refused")
		})})
		if calls.Load() != 1 || result.StopReason != "error" {
			t.Fatalf("calls=%d result=%+v", calls.Load(), result)
		}
	})
	t.Run("does not retry a 400", func(t *testing.T) {
		var calls atomic.Int32
		ClassifyTypesafeSystemOne(t.Context(), model, typesafeTestContext(), ClassifierOptions{APIKey: "k", Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return clsText(400, "bad", nil), nil
		})})
		if calls.Load() != 1 {
			t.Fatalf("calls=%d", calls.Load())
		}
	})
	t.Run("fails at once when the server asks for a delay above the cap", func(t *testing.T) {
		var calls atomic.Int32
		result := ClassifyTypesafeSystemOne(t.Context(), model, typesafeTestContext(), ClassifierOptions{APIKey: "k", MaxRetryDelayMs: new(1000), Fetch: clsClient(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return clsText(429, "slow down", map[string]string{"retry-after": "30"}), nil
		})})
		if calls.Load() != 1 || !strings.Contains(result.ErrorMessage, "Server requested 30s retry delay (max: 1s)") {
			t.Fatalf("calls=%d result=%+v", calls.Load(), result)
		}
	})
}

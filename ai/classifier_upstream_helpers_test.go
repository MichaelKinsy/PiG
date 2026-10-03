package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// clsFetch is a fake fetch for classifier tests.
type clsFetch func(*http.Request) (*http.Response, error)

func (f clsFetch) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func clsClient(f clsFetch) *http.Client { return &http.Client{Transport: f} }

// clsJSON is Response.json(body).
func clsJSON(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func clsText(status int, body string, headers map[string]string) *http.Response {
	response := &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{"Content-Type": {"text/plain;charset=UTF-8"}}, Body: io.NopCloser(strings.NewReader(body))}
	for name, value := range headers {
		response.Header.Set(name, value)
	}
	return response
}

func clsBody(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("request body is not a JSON object: %v: %s", err, data)
	}
	return body
}

func clsAnswer(t *testing.T, answers ClassifierAnswers, id string) ClassifierAnswer {
	t.Helper()
	for _, entry := range answers {
		if entry.ID == id {
			return entry.Answer
		}
	}
	t.Fatalf("no answer for %q in %+v", id, answers)
	return nil
}

func clsAnswerIDs(answers ClassifierAnswers) []string {
	ids := make([]string, len(answers))
	for i, entry := range answers {
		ids[i] = entry.ID
	}
	return ids
}

func clsChoices(pairs ...string) []ClassifierChoiceCriterion {
	out := make([]ClassifierChoiceCriterion, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, ClassifierChoiceCriterion{Key: pairs[i], Description: pairs[i+1]})
	}
	return out
}

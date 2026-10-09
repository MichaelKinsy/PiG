package ai

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// packages/ai/src/api/classifier-shared.ts:8-27: an HTTP failure is a plain Error whose name stays "Error" and that carries the response status,
// headers and body (httpError); a timeout sets `error.name = "TimeoutError"` with no status or headers and an empty body (timeoutError).
// Telemetry records the thrown value's name, so the two must differ.
// mutation-checked: a Name that always answers "Error" or "TimeoutError", or keys the timeout on an empty body or missing headers instead of a
// missing status, fails it, and so does an httpError that drops or alters the status, headers or body.
func TestClassifierHTTPErrorNameDistinguishesTimeoutFromHTTPFailure(t *testing.T) {
	model := ClassifierModel{}
	retries := 0
	post := func(options ClassifierOptions) error {
		options.APIKey, options.MaxRetries = "k", &retries
		_, err := PostClassifierRequest(t.Context(), "Label", "http://example.invalid/x", model, map[string]any{}, options, nil)
		return err
	}
	respond := func(response *http.Response) ClassifierOptions {
		return ClassifierOptions{Fetch: clsClient(func(*http.Request) (*http.Response, error) { return response, nil })}
	}

	response := clsText(500, "boom", map[string]string{"x-request-id": "r1"})
	defer func() { _ = response.Body.Close() }()
	httpFailure := post(respond(response))
	var failed *ClassifierHTTPError
	if !errors.As(httpFailure, &failed) || failed.Name() != "Error" {
		t.Fatalf("HTTP failure = %v, name %v; want a ClassifierHTTPError named Error", httpFailure, failed)
	}
	if failed.Status == nil || *failed.Status != 500 || failed.Headers.Get("x-request-id") != "r1" || failed.Body != "boom" || failed.Error() != "Label returned 500" {
		t.Fatalf("HTTP failure = %+v; want status 500, the response headers, body boom and message %q", failed, "Label returned 500")
	}

	// A failed response with an empty body and no header map is still an HTTP failure: its name follows the status, not the body or headers.
	bare := &http.Response{StatusCode: http.StatusServiceUnavailable, Status: http.StatusText(http.StatusServiceUnavailable), Body: io.NopCloser(strings.NewReader(""))}
	bareFailure := post(respond(bare))
	var bareFailed *ClassifierHTTPError
	if !errors.As(bareFailure, &bareFailed) || bareFailed.Name() != "Error" || bareFailed.Status == nil || *bareFailed.Status != 503 || bareFailed.Body != "" {
		t.Fatalf("empty HTTP failure = %v (%+v); want a ClassifierHTTPError named Error with status 503 and an empty body", bareFailure, bareFailed)
	}

	timeout := post(ClassifierOptions{TimeoutMs: new(20), Fetch: clsClient(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})})
	var timedOut *ClassifierHTTPError
	if !errors.As(timeout, &timedOut) || timedOut.Name() != "TimeoutError" || timedOut.Status != nil || timedOut.Headers != nil || timedOut.Body != "" {
		t.Fatalf("timeout = %v (%+v); want a ClassifierHTTPError named TimeoutError without status, headers or body", timeout, timedOut)
	}
	if timedOut.Error() != "Request timed out after 20ms" {
		t.Fatalf("timeout message = %q", timedOut.Error())
	}
}

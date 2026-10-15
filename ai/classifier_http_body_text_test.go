package ai

import (
	"errors"
	"net/http"
	"testing"
)

// classifier-shared.ts:88 builds httpError from `await next.text()`: undici drops two leading byte order marks and replaces each maximal ill-formed
// UTF-8 subpart with one U+FFFD. The expected body is what Node 24 prints for
// `new Response(Buffer.from("\xef\xbb\xbf\xef\xbb\xbfbad \xf0\x9d\x84 end \xc0\xaf!", "latin1")).text()`.
func TestClassifierHTTPErrorBodyIsResponseText(t *testing.T) {
	retries := 0
	response := clsText(400, "\xef\xbb\xbf\xef\xbb\xbfbad \xf0\x9d\x84 end \xc0\xaf!", nil)
	defer func() { _ = response.Body.Close() }()
	_, err := PostClassifierRequest(t.Context(), "Label", "http://example.invalid/x", ClassifierModel{}, map[string]any{}, ClassifierOptions{
		APIKey: "k", MaxRetries: &retries, Fetch: clsClient(func(*http.Request) (*http.Response, error) { return response, nil }),
	}, nil)
	failed, ok := errors.AsType[*ClassifierHTTPError](err)
	if !ok {
		t.Fatalf("error %v, want a ClassifierHTTPError", err)
	}
	if want := "bad \uFFFD end \uFFFD\uFFFD!"; failed.Body != want {
		t.Fatalf("body %q, want %q", failed.Body, want)
	}
}

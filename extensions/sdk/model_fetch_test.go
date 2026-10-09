package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The reference is runtime-node/model-fetch.mjs (Pi's ProviderRequestOptions.fetch, packages/ai/src/types.ts): the extension's fetch
// gets an init.signal that aborts when the host cancels the fetch callback or a fetchRead, when the host closes the response, and when
// the stream ends (disposeFetch); a response that arrives after the stream ended is cancelled and the fetch fails.

func fetchCall(t *testing.T, id string) json.RawMessage {
	t.Helper()
	value, err := json.Marshal(map[string]any{"id": id, "url": "https://fetch.invalid/v1", "method": http.MethodPost, "headers": map[string][]string{"X-Host": {"1"}}, "body": []byte("ping")})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestModelFetchHostCancellationReachesTheRequestContext(t *testing.T) {
	started := make(chan struct{})
	callbacks := &modelStreamCallbacks{fetch: func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	if _, err := callbacks.serveFetch(ctx, "fetch", fetchCall(t, "1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("fetch error = %v, want the cancelled request context", err)
	}
	if len(callbacks.fetches) != 0 {
		t.Fatalf("a cancelled fetch is retained: %v", callbacks.fetches)
	}
}

// blockingBody blocks a Read until its request context ends, as a network response body does.
type blockingBody struct {
	ctx    context.Context
	closed chan struct{}
}

func (b *blockingBody) Read([]byte) (int, error) {
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.closed:
		return 0, errors.New("read on closed body")
	}
}

func (b *blockingBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

func TestModelFetchHostCancellationOfAReadReachesTheRequestContext(t *testing.T) {
	var body *blockingBody
	callbacks := &modelStreamCallbacks{fetch: func(request *http.Request) (*http.Response, error) {
		body = &blockingBody{ctx: request.Context(), closed: make(chan struct{})}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
	}}
	if _, err := callbacks.serveFetch(context.Background(), "fetch", fetchCall(t, "1")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := callbacks.serveFetch(ctx, "fetchRead", json.RawMessage(`{"id":"1","size":16}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("read error = %v, want the cancelled request context", err)
	}
}

func TestModelFetchCloseCancelsTheRequestAndClosesTheBody(t *testing.T) {
	var request *http.Request
	body := &blockingBody{ctx: context.Background(), closed: make(chan struct{})}
	callbacks := &modelStreamCallbacks{fetch: func(r *http.Request) (*http.Response, error) {
		request = r
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
	}}
	if _, err := callbacks.serveFetch(context.Background(), "fetch", fetchCall(t, "1")); err != nil {
		t.Fatal(err)
	}
	if request.Context().Err() != nil {
		t.Fatal("the request context ended while the host still reads the body")
	}
	if _, err := callbacks.serveFetch(context.Background(), "fetchClose", json.RawMessage(`{"id":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if request.Context().Err() == nil {
		t.Fatal("fetchClose left the request context running")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("fetchClose left the body open")
	}
	if _, err := callbacks.serveFetch(context.Background(), "fetchRead", json.RawMessage(`{"id":"1","size":16}`)); err == nil || !strings.Contains(err.Error(), "unknown model fetch response 1") {
		t.Fatalf("read after close = %v", err)
	}
}

func TestModelFetchStreamEndCancelsAFetchInFlightAndClosesItsLateBody(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var request *http.Request
	body := &blockingBody{ctx: context.Background(), closed: make(chan struct{})}
	callbacks := &modelStreamCallbacks{fetch: func(r *http.Request) (*http.Response, error) {
		request = r
		close(started)
		<-release // a fetch that ignores its signal and answers after the stream ended
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: body}, nil
	}}
	done := make(chan error, 1)
	go func() {
		_, err := callbacks.serveFetch(context.Background(), "fetch", fetchCall(t, "1"))
		done <- err
	}()
	<-started
	callbacks.closeFetches()
	if request.Context().Err() == nil {
		t.Fatal("the stream ended and the fetch in flight still runs")
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("a fetch answering after the stream ended succeeded")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("the late response body is left open")
	}
	if callbacks.fetches != nil {
		t.Fatalf("the late response is retained: %v", callbacks.fetches)
	}
	if _, err := callbacks.serveFetch(context.Background(), "fetch", fetchCall(t, "2")); !errors.Is(err, errModelFetchClosed) {
		t.Fatalf("fetch after the stream ended = %v, want %v", err, errModelFetchClosed)
	}
}

func TestModelFetchStatusTextIsTheResponseReasonPhrase(t *testing.T) {
	for _, tc := range []struct {
		code         int
		status, want string
	}{
		{207, "207 Answered", "Answered"},
		{200, "200 OK", "OK"},
		{207, "", ""},
		{207, "207", ""},
	} {
		callbacks := &modelStreamCallbacks{fetch: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.code, Status: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}}
		result, err := callbacks.serveFetch(context.Background(), "fetch", fetchCall(t, "1"))
		if err != nil {
			t.Fatal(err)
		}
		if got := result.(map[string]any)["statusText"]; got != tc.want {
			t.Errorf("Status %q: statusText = %q, want %q", tc.status, got, tc.want)
		}
	}
}

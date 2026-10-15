package ai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// openrouter.ts personalizeOpenRouterCatalog fetches GET /models/user with
// AbortSignal.any([context.signal, AbortSignal.timeout(15_000)]): aborting the refresh's signal aborts the request in flight,
// and the awaited call rejects instead of waiting for the server.
func TestPersonalizeOpenRouterCatalogAbortsTheRequestWhenTheRefreshSignalIsAborted(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		select {
		case <-r.Context().Done():
		case <-release:
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	defer close(release)
	signal, abort := context.WithCancel(t.Context())
	refresh := openRouterUserTestRefresh(server)
	refresh.Signal = signal
	done := make(chan error, 1)
	go func() {
		_, err := PersonalizeOpenRouterCatalog([]AnyModel{openRouterUserTestChat("a")}, refresh)
		done <- err
	}()
	<-arrived
	abort()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want the refresh signal's cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PersonalizeOpenRouterCatalog kept waiting for the server after the refresh signal was aborted")
	}
}

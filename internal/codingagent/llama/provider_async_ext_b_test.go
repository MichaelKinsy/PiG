package llama

// pi: packages/coding-agent/src/extensions/llama/provider.ts

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type cancelAfterTransport struct {
	next http.RoundTripper
	// match picks the response after which the caller's context is cancelled; the cancel runs once, with the body already read, so the client sees a complete response and an aborted signal.
	match  func(*http.Request) bool
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelAfterTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := c.next.RoundTrip(r)
	if err != nil || !c.match(r) {
		return response, err
	}
	data, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	c.once.Do(c.cancel)
	return response, nil
}

// provider.ts:259-262,279 refreshModels returns without publishing when the signal is aborted after the catalog list, after the
// router-autoload probe, or after the props queries (the `if (context.signal.aborted) return` checks), and never starts the next
// request once aborted. The context is cancelled right after the response of the named request arrived complete.
func TestRefreshModelsPublishesNothingAfterAnAbortAtEachCheckpoint(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after func(*http.Request) bool
		// propsAfter is the number of /props requests (the autoload probe and the per-model queries) the refresh may have sent.
		props int
	}{
		{"after the catalog list", func(r *http.Request) bool { return r.URL.Path == "/models" }, 0},
		{"after the autoload probe", func(r *http.Request) bool { return r.URL.Path == "/props" && r.URL.RawQuery == "" }, 1},
		{"after the props queries", func(r *http.Request) bool { return r.URL.Path == "/props" && r.URL.RawQuery != "" }, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var props atomic.Int32
			url := listen(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/models":
					writeJSON(w, map[string]any{"data": []any{
						map[string]any{"id": "qwen", "status": map[string]any{"value": "loaded"}, "meta": map[string]any{"n_ctx": 32768}},
						map[string]any{"id": "idle", "status": map[string]any{"value": "unloaded"}, "source": "preset"},
					}})
				case "/props":
					props.Add(1)
					writeJSON(w, map[string]any{"models_autoload": true})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			transport := http.DefaultClient.Transport
			base := transport
			if base == nil {
				base = http.DefaultTransport
			}
			http.DefaultClient.Transport = &cancelAfterTransport{next: base, match: tc.after, cancel: cancel}
			t.Cleanup(func() { http.DefaultClient.Transport = transport })

			var published atomic.Int32
			controller := CreateLlamaProvider()
			err := controller.Provider.RefreshModels(RefreshModelsContext{
				Ctx:          ctx,
				Credential:   apiKeyCredential("local", url),
				AllowNetwork: true,
				Publish: func(ModelsPublication) (bool, error) {
					published.Add(1)
					return true, nil
				},
			})
			if err != nil {
				t.Errorf("refresh error = %v, want the abort to end the refresh without an error", err)
			}
			if published.Load() != 0 || len(controller.Provider.GetModels()) != 0 {
				t.Errorf("an aborted refresh published %d times and left models %v", published.Load(), modelIDs(controller.Provider.GetModels()))
			}
			if int(props.Load()) > tc.props {
				t.Errorf("props requests = %d, want at most %d: a request started after the abort", props.Load(), tc.props)
			}
		})
	}
}

// provider.ts:268-277 awaits the props queries with Promise.all: the first rejected query rejects the refresh and nothing is
// published for it. provider.ts:241-257 publishes the stored models first and returns without any network request when that
// publication is refused.
func TestRefreshModelsFailsOnAFailedPropsQueryAndStopsWhenTheCachePublicationIsRefused(t *testing.T) {
	url := listen(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/models":
			writeJSON(w, map[string]any{"data": []any{
				map[string]any{"id": "good", "status": map[string]any{"value": "loaded"}},
				map[string]any{"id": "bad", "status": map[string]any{"value": "loaded"}},
			}})
		case r.URL.Query().Get("model") == "bad":
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": map[string]any{"message": "props exploded"}})
		default:
			writeJSON(w, map[string]any{})
		}
	})
	var published atomic.Int32
	publish := func(ModelsPublication) (bool, error) {
		published.Add(1)
		return true, nil
	}
	controller := CreateLlamaProvider()
	err := controller.Provider.RefreshModels(RefreshModelsContext{Ctx: context.Background(), Credential: apiKeyCredential("local", url), AllowNetwork: true, Publish: publish})
	if err == nil || !strings.Contains(err.Error(), "props exploded") {
		t.Fatalf("refresh error = %v, want the failed props query", err)
	}
	if published.Load() != 0 || len(controller.Provider.GetModels()) != 0 {
		t.Errorf("a refresh with a failed props query published %d times and left %v", published.Load(), modelIDs(controller.Provider.GetModels()))
	}

	// The first refresh cancels its other props query when one rejects, and that query's request can reach the server after the refresh has
	// returned, so the refresh that must send nothing is counted by a server of its own: no request of the first can be taken for it.
	var refusedRequests atomic.Int32
	refusedURL := listen(t, func(http.ResponseWriter, *http.Request) { refusedRequests.Add(1) })
	refused := CreateLlamaProvider()
	err = refused.Provider.RefreshModels(RefreshModelsContext{
		Ctx: context.Background(), Credential: apiKeyCredential("local", refusedURL), AllowNetwork: true,
		Stored:  &ai.ModelsStoreEntry{},
		Publish: func(ModelsPublication) (bool, error) { return false, nil },
	})
	if err != nil || refusedRequests.Load() != 0 {
		t.Fatalf("refused cache publication: error %v, %d network requests, want none", err, refusedRequests.Load())
	}
}

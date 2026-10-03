package codingagent

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports .upstream/v0.99.2/packages/coding-agent/test/remote-catalog-provider.test.ts (8 cases).
//
// Upstream spies globalThis.fetch. Go has no global fetch, so each case serves the catalog from an httptest server and passes its
// URL as catalogBaseUrl (the upstream parameter the test sets to "https://pi.dev"). The recorded request is the fetch call.

type catalogResponse struct {
	status  int
	headers map[string]string
	body    string
}

type catalogRequest struct {
	path   string
	query  url.Values
	header http.Header
}

// catalogServer answers requests in order; the last response repeats when sticky.
type catalogServer struct {
	URL string

	mu        sync.Mutex
	responses []catalogResponse
	sticky    bool
	requests  []catalogRequest
	onRequest func(call int) // called before the response, with the 1-based call number
}

func newCatalogServer(t *testing.T, sticky bool, responses ...catalogResponse) *catalogServer {
	t.Helper()
	server := &catalogServer{responses: responses, sticky: sticky}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		server.requests = append(server.requests, catalogRequest{path: r.URL.Path, query: r.URL.Query(), header: r.Header.Clone()})
		call := len(server.requests)
		onRequest := server.onRequest
		var response catalogResponse
		switch {
		case len(server.responses) > 0:
			response = server.responses[0]
			if !server.sticky || len(server.responses) > 1 {
				server.responses = server.responses[1:]
			}
		default:
			server.mu.Unlock()
			t.Errorf("unexpected catalog request %d: %s", call, r.URL)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		server.mu.Unlock()
		if onRequest != nil {
			onRequest(call)
		}
		for name, value := range response.headers {
			w.Header().Set(name, value)
		}
		w.WriteHeader(response.status)
		_, _ = w.Write([]byte(response.body))
	}))
	t.Cleanup(func() {
		httpServer.CloseClientConnections()
		httpServer.Close()
	})
	server.URL = httpServer.URL
	return server
}

func (s *catalogServer) request(index int) catalogRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[index]
}

func (s *catalogServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func catalogJSONResponse(headers map[string]string, models map[string]any) catalogResponse {
	body, err := json.Marshal(models)
	if err != nil {
		panic(err)
	}
	merged := map[string]string{"content-type": "application/json"}
	maps.Copy(merged, headers)
	return catalogResponse{status: http.StatusOK, headers: merged, body: string(body)}
}

// catalogChatModel is the wire form of upstream's model(id).
func catalogChatModel(id string) map[string]any {
	return map[string]any{
		"id": id, "name": id, "api": "openai-completions", "provider": "test-provider", "baseUrl": "https://example.test/v1",
		"reasoning": false, "input": []string{"text"},
		"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
		"contextWindow": 1000, "maxTokens": 100,
	}
}

func catalogStaticModel(id string) *ai.Model {
	return &ai.Model{
		ID: id, DisplayName: id, Input: []string{"text"},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "test-provider", API: "openai-completions", BaseURL: "https://example.test/v1"},
		Capabilities: ai.ModelCapabilities{ContextWindow: 1000, MaxOutputTokens: 100},
	}
}

func remoteCatalogTestProvider(baseURL string, localGeneratedAt *float64) *ai.ModelsProvider {
	unused := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		panic("not used")
	}
	return WithRemoteCatalog(ai.CreateProvider(ai.CreateProviderOptions{
		ID: "test-provider",
		Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Test", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			return &ai.AuthResult{}, nil
		}}},
		Models: []ai.AnyModel{catalogStaticModel("static")},
		API:    &ai.ProviderStreams{Stream: unused, StreamSimple: unused},
	}), baseURL, localGeneratedAt)
}

type refreshOverrides struct {
	allowNetwork *bool
	force        *bool
	signal       context.Context
}

// refreshCatalogProvider is upstream's refreshProvider helper.
func refreshCatalogProvider(t *testing.T, provider *ai.ModelsProvider, store *ai.InMemoryModelsStore, overrides refreshOverrides) error {
	t.Helper()
	ctx := t.Context()
	publish := func(publication ai.ModelsPublication) (bool, error) {
		if publication.Persist == nil && publication.PersistSet {
			if err := store.Delete(ctx, provider.ID); err != nil {
				return false, err
			}
		} else if publication.Persist != nil {
			if err := store.Write(ctx, provider.ID, *publication.Persist); err != nil {
				return false, err
			}
		}
		if publication.Update != nil {
			publication.Update()
		}
		return true, nil
	}
	stored, err := store.Read(ctx, provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	allowNetwork := true
	if overrides.allowNetwork != nil {
		allowNetwork = *overrides.allowNetwork
	}
	signal := overrides.signal
	if signal == nil {
		signal = ctx
	}
	if provider.RefreshModels == nil {
		t.Fatal("provider has no refreshModels")
	}
	return provider.RefreshModels(ai.RefreshModelsContext{Credential: &ai.Credential{Type: ai.CredentialAPIKey}, Stored: stored, Publish: publish, AllowNetwork: allowNetwork, Force: overrides.force, Signal: signal})
}

func catalogModelIDs(t *testing.T, provider *ai.ModelsProvider) []string {
	t.Helper()
	models, err := provider.GetModels()
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

func storedCatalogIDs(t *testing.T, store *ai.InMemoryModelsStore) []string {
	t.Helper()
	entry, err := store.Read(t.Context(), "test-provider")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	if entry == nil {
		return ids
	}
	for _, raw := range entry.Models {
		var model struct{ ID string }
		if err := json.Unmarshal(raw, &model); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, model.ID)
	}
	return ids
}

func requireIDs(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

func requireNoRefreshError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

func TestRemoteCatalogProviderUpstream(t *testing.T) {
	force := new(true)

	// remote-catalog-provider.test.ts:79
	t.Run("parses keyed catalogs, sends version headers, observes the refresh TTL, and supports forced refreshes", func(t *testing.T) {
		server := newCatalogServer(t, true, catalogJSONResponse(nil, map[string]any{"dynamic": catalogChatModel("dynamic")}))
		provider := remoteCatalogTestProvider(server.URL, nil)
		store := ai.NewInMemoryModelsStore()
		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{force: force}))

		requireIDs(t, "models", catalogModelIDs(t, provider), []string{"static", "dynamic"})
		requireIDs(t, "stored models", storedCatalogIDs(t, store), []string{"dynamic"})
		if got := server.count(); got != 2 {
			t.Fatalf("catalog requests = %d, want 2", got)
		}
		first := server.request(0)
		// pig divergence (D65): the product identity is pig/<coding.Version>, where Pi sends pi/<VERSION> (upstream :98).
		if agent := first.header.Get("User-Agent"); !strings.Contains(agent, "pig/"+ai.ProductVersion) {
			t.Fatalf("User-Agent = %q, want it to contain pig/%s", agent, ai.ProductVersion)
		}
		if first.path != "/api/models/providers/test-provider" {
			t.Fatalf("path = %q", first.path)
		}
		var types []string
		for _, modelType := range RemoteCatalogModelTypes {
			types = append(types, string(modelType))
		}
		if got := first.query.Get("types"); got != strings.Join(types, ",") {
			t.Fatalf("types = %q, want %q", got, strings.Join(types, ","))
		}
	})

	// remote-catalog-provider.test.ts:107
	t.Run("overlays image and classifier models and drops unknown model types", func(t *testing.T) {
		chat := catalogChatModel("chat")
		chat["type"] = "chat"
		clip := catalogChatModel("clip")
		clip["type"] = "video"
		server := newCatalogServer(t, true, catalogJSONResponse(nil, map[string]any{
			"chat": chat,
			"flux": map[string]any{
				"type": "image", "id": "flux", "name": "FLUX", "api": "openrouter-images", "provider": "test-provider", "baseUrl": "https://example.test/v1",
				"input": []string{"text"}, "output": []string{"image"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
			},
			"jev": map[string]any{
				"type": "classifier", "id": "jev", "name": "Jev", "api": "typesafe-system-one", "provider": "test-provider", "baseUrl": "https://example.test/v1",
				"input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 64000,
			},
			"clip": clip,
		}))
		provider := remoteCatalogTestProvider(server.URL, nil)
		store := ai.NewInMemoryModelsStore()
		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))

		models := ai.CreateModels(ai.CreateModelsOptions{ModelsStore: store})
		models.SetProvider(provider)
		var ids []string
		for _, model := range models.GetAllModels("test-provider") {
			ids = append(ids, model.ModelID())
		}
		requireIDs(t, "all models", ids, []string{"static", "chat", "flux", "jev"})
		if model := models.GetModelOfType(ai.ModelTypeImage, "test-provider", "flux"); model == nil || model.ModelType() != ai.ModelTypeImage {
			t.Fatalf("image model = %v", model)
		}
		if model := models.GetModelOfType(ai.ModelTypeClassifier, "test-provider", "jev"); model == nil || model.ModelType() != ai.ModelTypeClassifier {
			t.Fatalf("classifier model = %v", model)
		}
		if model := models.GetModel("test-provider", "flux"); model != nil {
			t.Fatalf("chat lookup of an image model = %+v, want none", model)
		}
		requireIDs(t, "stored models", storedCatalogIDs(t, store), []string{"chat", "flux", "jev"})
	})

	// remote-catalog-provider.test.ts:151
	t.Run("prefers the newer of the generated and remote catalogs", func(t *testing.T) {
		localGeneratedAt := float64(time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).UnixMilli())
		httpDate := func(ms float64) string { return time.UnixMilli(int64(ms)).UTC().Format(http.TimeFormat) }
		newerHeader := httpDate(localGeneratedAt + 60_000)
		server := newCatalogServer(t, false,
			catalogJSONResponse(map[string]string{"last-modified": httpDate(localGeneratedAt - 60_000)}, map[string]any{"old": catalogChatModel("old")}),
			catalogJSONResponse(map[string]string{"last-modified": newerHeader}, map[string]any{"newer": catalogChatModel("newer")}),
		)
		provider := remoteCatalogTestProvider(server.URL, &localGeneratedAt)
		store := ai.NewInMemoryModelsStore()

		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
		requireIDs(t, "models after the older catalog", catalogModelIDs(t, provider), []string{"static"})

		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{force: force}))
		requireIDs(t, "models after the newer catalog", catalogModelIDs(t, provider), []string{"static", "newer"})
		stored, err := store.Read(t.Context(), "test-provider")
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := time.Parse(http.TimeFormat, newerHeader)
		if err != nil {
			t.Fatal(err)
		}
		if stored == nil || stored.LastModified == nil || *stored.LastModified != float64(parsed.UnixMilli()) {
			t.Fatalf("stored = %+v, want lastModified %d", stored, parsed.UnixMilli())
		}
	})

	// remote-catalog-provider.test.ts:179
	t.Run("revalidates a stored catalog with its etag and keeps the overlay on 304", func(t *testing.T) {
		server := newCatalogServer(t, false,
			catalogJSONResponse(map[string]string{"etag": `"catalog-1"`}, map[string]any{"dynamic": catalogChatModel("dynamic")}),
			catalogResponse{status: http.StatusNotModified, headers: map[string]string{"etag": `"catalog-1"`}},
		)
		provider := remoteCatalogTestProvider(server.URL, nil)
		store := ai.NewInMemoryModelsStore()

		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
		if _, present := server.request(0).header["If-None-Match"]; present {
			t.Fatalf("first request carries if-none-match: %v", server.request(0).header)
		}
		entry, err := store.Read(t.Context(), "test-provider")
		if err != nil || entry == nil || entry.ETag != `"catalog-1"` {
			t.Fatalf("stored = %+v, %v", entry, err)
		}
		checkedAt := *entry.CheckedAt
		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{force: force}))

		if got := server.request(1).header.Get("If-None-Match"); got != `"catalog-1"` {
			t.Fatalf("second request if-none-match = %q", got)
		}
		requireIDs(t, "models", catalogModelIDs(t, provider), []string{"static", "dynamic"})
		stored, err := store.Read(t.Context(), "test-provider")
		if err != nil || stored == nil {
			t.Fatalf("stored = %+v, %v", stored, err)
		}
		requireIDs(t, "stored models", storedCatalogIDs(t, store), []string{"dynamic"})
		if stored.ETag != `"catalog-1"` || stored.CheckedAt == nil || *stored.CheckedAt < checkedAt {
			t.Fatalf("stored etag/checkedAt = %q/%v, want the same etag and checkedAt >= %v", stored.ETag, stored.CheckedAt, checkedAt)
		}
	})

	// remote-catalog-provider.test.ts:207
	t.Run("drops a stale etag when the overlay becomes unavailable", func(t *testing.T) {
		server := newCatalogServer(t, false,
			catalogJSONResponse(map[string]string{"etag": `"catalog-1"`}, map[string]any{"dynamic": catalogChatModel("dynamic")}),
			catalogResponse{status: http.StatusNotImplemented, body: "not implemented"},
		)
		provider := remoteCatalogTestProvider(server.URL, nil)
		store := ai.NewInMemoryModelsStore()

		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{force: force}))

		entry, err := store.Read(t.Context(), "test-provider")
		if err != nil || entry == nil || entry.ETag != "" {
			t.Fatalf("stored etag = %+v, %v, want none", entry, err)
		}
	})

	// remote-catalog-provider.test.ts:224
	t.Run("keeps the etag and overlay after a transient failure", func(t *testing.T) {
		limited := catalogResponse{status: http.StatusTooManyRequests, body: "rate limited"}
		server := newCatalogServer(t, false,
			catalogJSONResponse(map[string]string{"etag": `"catalog-1"`}, map[string]any{"dynamic": catalogChatModel("dynamic")}),
			limited, limited, limited,
			catalogResponse{status: http.StatusNotModified, headers: map[string]string{"etag": `"catalog-1"`}},
		)
		provider := remoteCatalogTestProvider(server.URL, nil)
		store := ai.NewInMemoryModelsStore()

		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
		if err := refreshCatalogProvider(t, provider, store, refreshOverrides{force: force}); err == nil || !strings.Contains(err.Error(), "429") {
			t.Fatalf("forced refresh error = %v, want one mentioning 429", err)
		}

		stored, err := store.Read(t.Context(), "test-provider")
		if err != nil || stored == nil || stored.ETag != `"catalog-1"` {
			t.Fatalf("stored after the failure = %+v, %v", stored, err)
		}
		requireIDs(t, "stored models", storedCatalogIDs(t, store), []string{"dynamic"})

		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{force: force}))
		if got := server.request(4).header.Get("If-None-Match"); got != `"catalog-1"` {
			t.Fatalf("fifth request if-none-match = %q", got)
		}
		requireIDs(t, "models", catalogModelIDs(t, provider), []string{"static", "dynamic"})
	})

	// remote-catalog-provider.test.ts:254
	t.Run("lets a newer catalog request bypass a stalled older request without stale publication", func(t *testing.T) {
		firstStarted := make(chan struct{})
		releaseFirst := make(chan struct{})
		firstDone := make(chan struct{})
		var once sync.Once
		// The responses pop in arrival order: the stalled first request ends with "older", the second answers "newer".
		server := newCatalogServer(t, false,
			catalogJSONResponse(nil, map[string]any{"older": catalogChatModel("older")}),
			catalogJSONResponse(nil, map[string]any{"newer": catalogChatModel("newer")}),
		)
		server.onRequest = func(call int) {
			if call != 1 {
				return
			}
			once.Do(func() { close(firstStarted) })
			<-releaseFirst
			close(firstDone)
		}
		provider := remoteCatalogTestProvider(server.URL, nil)
		// Every refreshModels call the collection starts must return before the stale-publication check: the superseded call may still be
		// running after Models.Refresh returned.
		var refreshes sync.WaitGroup
		refreshModels := provider.RefreshModels
		provider.RefreshModels = func(refresh ai.RefreshModelsContext) error {
			refreshes.Add(1)
			defer refreshes.Done()
			return refreshModels(refresh)
		}
		store := ai.NewInMemoryModelsStore()
		models := ai.CreateModels(ai.CreateModelsOptions{ModelsStore: store})
		models.SetProvider(provider)

		first := make(chan ai.ModelsRefreshResult, 1)
		go func() {
			first <- models.Refresh(t.Context(), ai.ModelsRefreshOptions{Providers: []string{provider.ID}, Force: force})
		}()
		select {
		case <-firstStarted:
		case <-time.After(10 * time.Second):
			t.Fatal("the first catalog request never started")
		}
		models.Refresh(t.Context(), ai.ModelsRefreshOptions{Providers: []string{provider.ID}, Force: force})
		select {
		case <-first:
		case <-time.After(10 * time.Second):
			t.Fatal("the first refresh stayed stalled behind the newer request")
		}
		requireIDs(t, "models after the newer request", catalogModelIDs(t, provider), []string{"static", "newer"})

		// The stalled first response now completes with a different catalog. Pi's test yields one timer tick; this waits for the
		// server to finish that response and then for every refreshModels call to return.
		close(releaseFirst)
		select {
		case <-firstDone:
		case <-time.After(10 * time.Second):
			t.Fatal("the stalled response never finished")
		}
		drained := make(chan struct{})
		go func() { refreshes.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-time.After(10 * time.Second):
			t.Fatal("the superseded refreshModels call never returned")
		}

		requireIDs(t, "models after the stale response", catalogModelIDs(t, provider), []string{"static", "newer"})
		requireIDs(t, "stored models", storedCatalogIDs(t, store), []string{"newer"})
	})

	// remote-catalog-provider.test.ts:290
	t.Run("treats unimplemented pi.dev catalog routes as an unavailable overlay", func(t *testing.T) {
		server := newCatalogServer(t, true, catalogResponse{status: http.StatusNotImplemented, body: "not implemented"})
		provider := remoteCatalogTestProvider(server.URL, nil)
		store := ai.NewInMemoryModelsStore()

		requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
		requireIDs(t, "models", catalogModelIDs(t, provider), []string{"static"})
		entry, err := store.Read(t.Context(), "test-provider")
		if err != nil || entry == nil || len(entry.Models) != 0 || entry.CheckedAt == nil {
			t.Fatalf("stored = %+v, %v, want no models and a checkedAt", entry, err)
		}
	})
}

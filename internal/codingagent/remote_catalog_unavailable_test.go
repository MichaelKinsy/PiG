package codingagent

import (
	"net/http"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// remote-catalog-provider.ts:158-167 (withRemoteCatalog's refresh): a 404 or 501 is not an error, the overlay is unavailable. Nothing is added to the bundled catalog, the saved overlay of an earlier answer stays as it was, the validators are dropped (etag undefined, lastModified 0) and checkedAt moves, so the next refresh asks again after the freshness window. A refresh that finds no overlay therefore leaves the bundled catalog.
func TestRemoteCatalogUnavailableAnswersLeaveTheBundledCatalog(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusNotImplemented} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Run("nothing saved", func(t *testing.T) {
				server := newCatalogServer(t, true, catalogResponse{status: status, body: "unavailable"})
				provider := remoteCatalogTestProvider(server.URL, nil)
				store := ai.NewInMemoryModelsStore()
				before := time.Now().UnixMilli()
				requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
				requireIDs(t, "models", catalogModelIDs(t, provider), []string{"static"})
				entry, err := store.Read(t.Context(), "test-provider")
				if err != nil || entry == nil || len(entry.Models) != 0 || entry.ETag != "" || entry.LastModified == nil || *entry.LastModified != 0 || entry.CheckedAt == nil || *entry.CheckedAt < float64(before) {
					t.Fatalf("stored = %+v, %v; want no models, no etag, lastModified 0 and a fresh checkedAt", entry, err)
				}
				if server.count() != 1 {
					t.Fatalf("catalog requests = %d, want 1", server.count())
				}
			})
			t.Run("an earlier overlay", func(t *testing.T) {
				server := newCatalogServer(t, false,
					catalogJSONResponse(map[string]string{"etag": `"catalog-1"`}, map[string]any{"dynamic": catalogChatModel("dynamic")}),
					catalogResponse{status: status, body: "unavailable"},
				)
				provider := remoteCatalogTestProvider(server.URL, nil)
				store := ai.NewInMemoryModelsStore()
				requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
				requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{force: new(true)}))
				requireIDs(t, "models", catalogModelIDs(t, provider), []string{"static", "dynamic"})
				requireIDs(t, "stored models", storedCatalogIDs(t, store), []string{"dynamic"})
				if entry, err := store.Read(t.Context(), "test-provider"); err != nil || entry == nil || entry.ETag != "" || entry.LastModified == nil || *entry.LastModified != 0 {
					t.Fatalf("stored = %+v, %v; want the validators dropped", entry, err)
				}
			})
		})
	}
}

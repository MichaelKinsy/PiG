package codingagent

import (
	"net/http"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// remote-catalog-provider.ts:15,93 (REMOTE_CATALOG_REFRESH_INTERVAL_MS = 4 * 60 * 60 * 1000): a checked catalog that
// has a Last-Modified validator stays fresh for four hours, so a refresh inside the interval sends no request and one
// just past it revalidates.
func TestRemoteCatalogStaysFreshForTheFourHourRefreshInterval(t *testing.T) {
	server := newCatalogServer(t, false,
		catalogJSONResponse(map[string]string{"etag": `"catalog-1"`, "last-modified": time.Now().UTC().Add(-48 * time.Hour).Format(http.TimeFormat)}, map[string]any{"dynamic": catalogChatModel("dynamic")}),
		catalogResponse{status: http.StatusNotModified, headers: map[string]string{"etag": `"catalog-1"`}},
	)
	provider := remoteCatalogTestProvider(server.URL, nil)
	store := ai.NewInMemoryModelsStore()
	requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
	if server.count() != 1 {
		t.Fatalf("requests after the first refresh = %d, want 1", server.count())
	}
	entry, err := store.Read(t.Context(), "test-provider")
	if err != nil || entry == nil || entry.LastModified == nil {
		t.Fatalf("stored = %+v, %v", entry, err)
	}
	checkedAgo := func(age time.Duration) {
		t.Helper()
		stale := *entry
		stale.CheckedAt = new(float64(time.Now().Add(-age).UnixMilli()))
		if err := store.Write(t.Context(), "test-provider", stale); err != nil {
			t.Fatal(err)
		}
	}
	checkedAgo(RemoteCatalogRefreshInterval - time.Minute)
	requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
	if server.count() != 1 {
		t.Fatalf("requests inside the interval = %d, want none beyond the first", server.count())
	}
	checkedAgo(RemoteCatalogRefreshInterval + time.Minute)
	requireNoRefreshError(t, refreshCatalogProvider(t, provider, store, refreshOverrides{}))
	if server.count() != 2 {
		t.Fatalf("requests past the interval = %d, want a revalidation", server.count())
	}
	if RemoteCatalogRefreshInterval != 4*time.Hour {
		t.Fatalf("RemoteCatalogRefreshInterval = %v, want 4h", RemoteCatalogRefreshInterval)
	}
}

// renderer.ts:30 (codemodeDuration): under a second is whole milliseconds rounded as Math.round does, from one second
// seconds with one decimal.
func TestCodemodeDurationSwitchesToSecondsAtOneThousandMilliseconds(t *testing.T) {
	for _, c := range []struct {
		ms   float64
		want string
	}{{0, "0ms"}, {999, "999ms"}, {999.4, "999ms"}, {1000, "1.0s"}, {1500, "1.5s"}, {12345, "12.3s"}} {
		ms := c.ms
		if got := codemodeDuration(&ms); got != c.want {
			t.Errorf("codemodeDuration(%v) = %q, want %q", c.ms, got, c.want)
		}
	}
	if got := codemodeDuration(nil); got != "" {
		t.Errorf("codemodeDuration(nil) = %q, want empty", got)
	}
}

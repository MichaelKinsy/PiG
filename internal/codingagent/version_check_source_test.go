package codingagent

import (
	"net/http"
	"net/url"
	"sync"
	"testing"
)

// D64: PiG's version check reads the update manifest of the configured source (the latest GitHub Release for PiG's own builds, D39) and never asks pi-in-go.dev or pi.dev, where Pi's checkForNewPiVersion asks pi.dev/api/latest-version (version-check.ts). The check requests exactly the configured manifest URL, once; with no source configured (no PIG_UPDATE_URL, no update-url sidecar, no baked DefaultUpdateURL) it makes no request at all.
func TestVersionCheckRequestsOnlyTheConfiguredManifest(t *testing.T) {
	const manifestURL = "https://releases.example/pig/latest/update.json"
	record := func() (*http.Client, func() []string) {
		var mu sync.Mutex
		var urls []string
		client := &http.Client{Transport: versionCheckRoundTripper(func(request *http.Request) (*http.Response, error) {
			mu.Lock()
			defer mu.Unlock()
			urls = append(urls, request.URL.String())
			return nil, &url.Error{Op: request.Method, URL: request.URL.String(), Err: http.ErrHandlerTimeout}
		})}
		return client, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), urls...)
		}
	}
	configure := func(t *testing.T) {
		t.Helper()
		t.Setenv("PIG_HOME", t.TempDir())
		t.Setenv("PIG_UPDATE_URL", "")
		t.Setenv("PI_SKIP_VERSION_CHECK", "")
		t.Setenv("PI_OFFLINE", "")
		t.Setenv("PIG_OFFLINE", "")
		previous := DefaultUpdateURL
		DefaultUpdateURL = ""
		t.Cleanup(func() { DefaultUpdateURL = previous })
	}

	t.Run("an environment source", func(t *testing.T) {
		configure(t)
		t.Setenv("PIG_UPDATE_URL", manifestURL)
		client, requested := record()
		if update := CheckForBinaryUpdate(t.Context(), client, "0.1.0"); update != nil {
			t.Fatalf("update = %#v from a failing transport", update)
		}
		if got := requested(); len(got) != 1 || got[0] != manifestURL {
			t.Fatalf("requests = %v, want only %s", got, manifestURL)
		}
	})

	t.Run("a baked source", func(t *testing.T) {
		configure(t)
		DefaultUpdateURL = manifestURL
		client, requested := record()
		CheckForBinaryUpdate(t.Context(), client, "0.1.0")
		if got := requested(); len(got) != 1 || got[0] != manifestURL {
			t.Fatalf("requests = %v, want only %s", got, manifestURL)
		}
	})

	t.Run("no source", func(t *testing.T) {
		configure(t)
		client, requested := record()
		if update := CheckForBinaryUpdate(t.Context(), client, "0.1.0"); update != nil {
			t.Fatalf("update = %#v with no source", update)
		}
		if got := requested(); len(got) != 0 {
			t.Fatalf("requests = %v, want none: with no update source the check asks nobody", got)
		}
	})
}

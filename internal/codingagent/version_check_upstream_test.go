package codingagent

import (
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/coding-agent/test/version-check.test.ts describe "version checks"
// Pig's version check reads the signed update manifest of the configured source (D39) where Pi reads pi.dev/api/latest-version, so the release is an UpdateManifest, the automatic check is CheckForBinaryUpdate and the explicit check is FetchUpdateManifest with Retry.

func versionCheckManifest(version, extra string) string {
	return `{"version":"` + version + `","packageName":"@pi-in-go/pig",` + extra + `"binaries":{"` + platformKey() + `":{"url":"u","sha256":"` + strings.Repeat("0", sha256.Size*2) + `"}}}`
}

type versionCheckRoundTripper func(*http.Request) (*http.Response, error)

func (fn versionCheckRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

// failingClient fails the first failures requests as a dropped connection and sends later ones to base. It counts every request.
func failingClient(base *http.Client, failures int) (*http.Client, *atomic.Int32) {
	var requests atomic.Int32
	return &http.Client{Transport: versionCheckRoundTripper(func(request *http.Request) (*http.Response, error) {
		if int(requests.Add(1)) <= failures {
			return nil, errors.New("fetch failed")
		}
		return base.Transport.RoundTrip(request)
	})}, &requests
}

func TestVersionChecks(t *testing.T) {
	t.Run("compares package versions", func(t *testing.T) { // :28
		for _, tc := range []struct {
			candidate, current string
			want               int
		}{
			{"0.70.6", "0.70.5", 1}, {"0.70.5", "0.70.5", 0}, {"0.70.4", "0.70.5", -1}, {"5.0.0-beta.20", "5.0.0-beta.9", 1},
		} {
			if got := CompareVersions(tc.candidate, tc.current); got != tc.want {
				t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.candidate, tc.current, got, tc.want)
			}
		}
	})

	t.Run("returns only newer versions", func(t *testing.T) { // :37
		server := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(versionCheckManifest("1.2.3", "")))
		}))
		defer server.Close()
		t.Setenv("PIG_UPDATE_URL", server.URL)
		t.Setenv("PI_SKIP_VERSION_CHECK", "")
		if update := CheckForBinaryUpdate(t.Context(), server.Client(), "1.2.3"); update != nil {
			t.Fatalf("an equal version produced %#v", update)
		}
		if update := CheckForBinaryUpdate(t.Context(), server.Client(), "1.2.2"); update == nil || update.LatestVersion != "1.2.3" {
			t.Fatalf("an older version produced %#v, want 1.2.3", update)
		}
	})

	t.Run("sends the identity and accept headers with the version check request", func(t *testing.T) { // :45
		var userAgent, accept string
		server := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userAgent, accept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
			_, _ = w.Write([]byte(versionCheckManifest("1.2.4", "")))
		}))
		defer server.Close()
		manifest, err := FetchUpdateManifest(t.Context(), server.Client(), server.URL)
		if err != nil || manifest.Version != "1.2.4" {
			t.Fatalf("manifest = %#v, err = %v", manifest, err)
		}
		if userAgent != ai.PiUserAgent() || accept != "application/json" {
			t.Errorf("User-Agent = %q, Accept = %q; want %q and application/json", userAgent, accept, ai.PiUserAgent())
		}
	})

	t.Run("retries a transient version request when explicitly requested", func(t *testing.T) { // :61
		server := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(versionCheckManifest("1.2.4", "")))
		}))
		defer server.Close()
		client, requests := failingClient(server.Client(), 2)
		manifest, err := FetchUpdateManifest(t.Context(), client, server.URL, FetchUpdateManifestOptions{Retry: true})
		if err != nil || manifest.Version != "1.2.4" || requests.Load() != 3 {
			t.Fatalf("manifest = %#v, err = %v, requests = %d, want 3", manifest, err, requests.Load())
		}
	})

	t.Run("keeps automatic version checks to one request", func(t *testing.T) { // :73
		server := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(versionCheckManifest("1.2.4", "")))
		}))
		defer server.Close()
		t.Setenv("PIG_UPDATE_URL", server.URL)
		t.Setenv("PI_SKIP_VERSION_CHECK", "")
		client, requests := failingClient(server.Client(), 100)
		if update := CheckForBinaryUpdate(t.Context(), client, "1.2.3"); update != nil || requests.Load() != 1 {
			t.Fatalf("update = %#v, requests = %d, want nil and 1", update, requests.Load())
		}
	})

	t.Run("reports the cause of a network failure", func(t *testing.T) { // :81
		// Node hides errno codes behind "fetch failed", so upstream's formatVersionCheckError lists them: "fetch failed (ETIMEDOUT, ENETUNREACH)". A Go transport error already names its operation and cause, which the explicit update commands print after "could not reach the update source:"; there is no code list to recover.
		server := newSignedManifestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		address := server.URL
		server.Close()
		_, err := FetchUpdateManifest(context.Background(), http.DefaultClient, address)
		// The refusal text is platform-specific (Windows reports "actively refused it"), so assert the dial cause through the error chain.
		var dialErr *net.OpError
		if err == nil || !errors.As(err, &dialErr) || dialErr.Op != "dial" || !strings.Contains(err.Error(), dialErr.Err.Error()) {
			t.Fatalf("error = %v, want one that names the failed dial and its cause", err)
		}
	})

	t.Run("returns the active package metadata from the update manifest", func(t *testing.T) { // :92
		server := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"version":"1.2.4","packageName":"@new-scope/pig","binaries":{}}`))
		}))
		defer server.Close()
		manifest, err := FetchUpdateManifest(t.Context(), server.Client(), server.URL)
		if err != nil || manifest.PackageName != "@new-scope/pig" || manifest.Version != "1.2.4" {
			t.Fatalf("manifest = %#v, err = %v", manifest, err)
		}
	})

	t.Run("returns update notes from the update manifest", func(t *testing.T) { // :107
		server := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(versionCheckManifest("1.2.4", `"notes":" **Read this** ",`)))
		}))
		defer server.Close()
		t.Setenv("PIG_UPDATE_URL", server.URL)
		t.Setenv("PI_SKIP_VERSION_CHECK", "")
		update := CheckForBinaryUpdate(t.Context(), server.Client(), "1.2.3")
		if update == nil || update.Notes != "**Read this**" || update.LatestVersion != "1.2.4" {
			t.Fatalf("update = %#v", update)
		}
	})

	t.Run("skips automatic update calls when version checks are disabled", func(t *testing.T) { // :114
		server := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(versionCheckManifest("1.2.4", "")))
		}))
		defer server.Close()
		t.Setenv("PIG_UPDATE_URL", server.URL)
		t.Setenv("PI_SKIP_VERSION_CHECK", "1")
		client, requests := failingClient(server.Client(), 0)
		if update := CheckForBinaryUpdate(t.Context(), client, "1.2.3"); update != nil || requests.Load() != 0 {
			t.Fatalf("update = %#v, requests = %d, want nil and 0", update, requests.Load())
		}
	})

	t.Run("allows direct update calls when automatic version checks are disabled", func(t *testing.T) { // :123
		server := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(versionCheckManifest("1.2.4", "")))
		}))
		defer server.Close()
		t.Setenv("PI_SKIP_VERSION_CHECK", "1")
		client, requests := failingClient(server.Client(), 0)
		manifest, err := FetchUpdateManifest(t.Context(), client, server.URL)
		if err != nil || manifest.Version != "1.2.4" || requests.Load() != 1 {
			t.Fatalf("manifest = %#v, err = %v, requests = %d", manifest, err, requests.Load())
		}
	})
}

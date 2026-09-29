package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func resolveIndexURL(ref, version string) (string, string, error) {
	reference, err := parseReleaseReference(ref, version)
	return reference.url, reference.version, err
}

func TestDiscoverGitHubVersionPagesNamespaceAndStableVersions(t *testing.T) {
	var pages []string
	client := &http.Client{Transport: releaseRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.github.com" || r.URL.Path != "/repos/acme/piglets/releases" || r.URL.Query().Get("per_page") != "100" {
			return nil, fmt.Errorf("unexpected request %s", r.URL)
		}
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		rows := []map[string]any{}
		switch page {
		case "1":
			// GitHub's maximum page size is an external API denominator, not an observed test count.
			for range 100 {
				rows = append(rows, map[string]any{"tag_name": "other/v99.0.0"})
			}
		case "2":
			rows = append(rows,
				map[string]any{"tag_name": "alpha/v1.0.0"},
				map[string]any{"tag_name": "alpha/v2.0.0"},
				map[string]any{"tag_name": "alpha/v4.0.0", "draft": true},
				map[string]any{"tag_name": "alpha/v3.0.0", "prerelease": true},
				map[string]any{"tag_name": "alpha/v99.0.0-rc.1"},
				map[string]any{"tag_name": "alpha/vgarbage"},
				map[string]any{"tag_name": "v7.0.0"},
			)
		default:
			return nil, fmt.Errorf("unexpected page %s", page)
		}
		data, err := json.Marshal(rows)
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	version, err := discoverGitHubVersion(t.Context(), client, GitHubRelease{Repository: "acme/piglets", TagPrefix: "alpha/"})
	if err != nil || version != "2.0.0" || strings.Join(pages, ",") != "1,2" {
		t.Fatalf("version=%s pages=%v err=%v", version, pages, err)
	}
	version, err = discoverGitHubVersion(t.Context(), client, GitHubRelease{Repository: "acme/piglets"})
	if err != nil || version != "7.0.0" {
		t.Fatalf("unprefixed discovery=%s err=%v", version, err)
	}
}

type releaseRoundTrip func(*http.Request) (*http.Response, error)

func (r releaseRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return r(request) }

func TestPullCancellationAfterDownloadDoesNotPublish(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	key := newKey(t)
	server, indexURL := releaseServer(t, key, releaseSpec{piglet: "alpha", version: "1.0.0", target: testTarget, pigVersion: "pig-test", binary: signedBinary(t, key, "alpha", "1.0.0", testTarget, "pig-test")})
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client := &http.Client{Transport: releaseRoundTrip(func(r *http.Request) (*http.Response, error) {
		response, err := http.DefaultTransport.RoundTrip(r)
		if err == nil && r.URL.Path == "/binary" {
			response.Body = cancelOnClose{ReadCloser: response.Body, cancel: cancel}
		}
		return response, err
	})}
	if _, err := Pull(ctx, indexURL, Options{Client: client, Target: testTarget}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled pull=%v", err)
	}
	installed, errs := ListInstalled()
	if len(installed) != 0 || len(errs) != 0 {
		t.Fatalf("canceled pull published state: %+v %v", installed, errs)
	}
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c cancelOnClose) Close() error { err := c.ReadCloser.Close(); c.cancel(); return err }

func TestUpdateUnprefixedRepositoryCannotInstallAnotherPiglet(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	key := newKey(t)
	assets := map[string][]byte{}
	for name, version := range map[string]string{"alpha": "1.0.0", "beta": "2.0.0"} {
		binary := signedBinary(t, key, name, version, testTarget, "pig-test")
		digest := sha256.Sum256(binary)
		index, err := Sign(Index{Piglet: name, Version: version, PigVersion: "pig-test", SourceRef: "git:github.com/acme/piglets@v" + version, GitHub: &GitHubRelease{Repository: "acme/piglets"}, Binaries: map[string]Binary{testTarget: {URL: "binary", SHA256: hex.EncodeToString(digest[:]), Size: int64(len(binary))}}}, key)
		if err != nil {
			t.Fatal(err)
		}
		prefix := "/acme/piglets/releases/download/v" + version + "/"
		assets[prefix+"piglet-release.json"] = index
		assets[prefix+"binary"] = binary
	}
	options := Options{Target: testTarget, Client: &http.Client{Transport: releaseRoundTrip(func(r *http.Request) (*http.Response, error) {
		data, ok := assets[r.URL.Path]
		if !ok {
			return nil, fmt.Errorf("unexpected request %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}}
	if _, err := Pull(t.Context(), "github:acme/piglets@1.0.0", options); err != nil {
		t.Fatal(err)
	}
	options.Version = "2.0.0"
	if result, err := Update(t.Context(), "alpha", options); err == nil || !strings.Contains(err.Error(), "names beta, requested alpha") {
		t.Fatalf("cross-Piglet update=%+v err=%v", result, err)
	}
	installed, errs := ListInstalled()
	if len(errs) != 0 || len(installed) != 1 || installed[0].Index.Piglet != "alpha" || installed[0].Index.Version != "1.0.0" {
		t.Fatalf("failed update changed installed state: %+v %v", installed, errs)
	}
}

func TestNamedGitHubReferenceEscapesTag(t *testing.T) {
	got, version, err := resolveIndexURL("github:acme/piglets/porter@1.2.3", "")
	if err != nil || got != "https://github.com/acme/piglets/releases/download/porter%2Fv1.2.3/piglet-release.json" || version != "1.2.3" {
		t.Fatalf("got %q %q %v", got, version, err)
	}
}

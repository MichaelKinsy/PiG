package tools

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type toolsRoundTrip func(*http.Request) (*http.Response, error)

func (f toolsRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type trackedBody struct {
	io.Reader
	closed bool
	read   bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	b.read = true
	return b.Reader.Read(p)
}

func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}

func redirectTo(location string) *http.Response {
	return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {location}}, Body: http.NoBody}
}

func latestVersionManager(t *testing.T, respond func(*http.Request) *http.Response) (*ToolsManager, *[]*http.Request) {
	t.Helper()
	var requests []*http.Request
	tm := NewToolsManager(t.TempDir())
	tm.httpClient = &http.Client{Transport: toolsRoundTrip(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)
		return respond(req), nil
	})}
	return tm, &requests
}

// Cases ported from upstream test/tools-manager.test.ts "getLatestVersion".
func TestGetLatestVersionResolvesTheReleasePageRedirect(t *testing.T) {
	tm, requests := latestVersionManager(t, func(*http.Request) *http.Response {
		return redirectTo("https://github.com/sharkdp/fd/releases/tag/v10.4.2")
	})
	got, err := tm.getLatestVersion(context.Background(), "sharkdp/fd")
	if err != nil || got != "10.4.2" {
		t.Fatalf("getLatestVersion = %q, %v; want 10.4.2", got, err)
	}
	if len(*requests) != 1 || (*requests)[0].URL.String() != "https://github.com/sharkdp/fd/releases/latest" {
		t.Fatalf("requests = %v, want one GET of https://github.com/sharkdp/fd/releases/latest", *requests)
	}
}

func TestGetLatestVersionKeepsTagsWithoutAVPrefix(t *testing.T) {
	tm, _ := latestVersionManager(t, func(*http.Request) *http.Response {
		return redirectTo("https://github.com/BurntSushi/ripgrep/releases/tag/15.2.0")
	})
	if got, err := tm.getLatestVersion(context.Background(), "BurntSushi/ripgrep"); err != nil || got != "15.2.0" {
		t.Fatalf("getLatestVersion = %q, %v; want 15.2.0", got, err)
	}
}

func TestGetLatestVersionResolvesRelativeRedirectTargets(t *testing.T) {
	tm, _ := latestVersionManager(t, func(*http.Request) *http.Response {
		return redirectTo("/sharkdp/fd/releases/tag/v10.4.2")
	})
	if got, err := tm.getLatestVersion(context.Background(), "sharkdp/fd"); err != nil || got != "10.4.2" {
		t.Fatalf("getLatestVersion = %q, %v; want 10.4.2", got, err)
	}
}

func TestGetLatestVersionDiscardsTheRedirectResponseBody(t *testing.T) {
	body := &trackedBody{Reader: strings.NewReader("<html></html>")}
	tm, _ := latestVersionManager(t, func(*http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://github.com/sharkdp/fd/releases/tag/v10.4.2"}}, Body: body}
	})
	if got, err := tm.getLatestVersion(context.Background(), "sharkdp/fd"); err != nil || got != "10.4.2" {
		t.Fatalf("getLatestVersion = %q, %v; want 10.4.2", got, err)
	}
	if body.read || !body.closed {
		t.Fatalf("redirect body read=%v closed=%v, want cancellation without reading", body.read, body.closed)
	}
}

func TestGetLatestVersionFailsClearlyWithoutARedirect(t *testing.T) {
	tm, _ := latestVersionManager(t, func(*http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("not found"))}
	})
	_, err := tm.getLatestVersion(context.Background(), "sharkdp/fd")
	if err == nil || err.Error() != "Failed to resolve latest sharkdp/fd release: HTTP 404 without redirect" {
		t.Fatalf("error = %v", err)
	}
}

func TestGetLatestVersionFailsClearlyWhenTheRedirectIsNotAReleaseTag(t *testing.T) {
	tm, _ := latestVersionManager(t, func(*http.Request) *http.Response {
		return redirectTo("https://github.com/login")
	})
	_, err := tm.getLatestVersion(context.Background(), "sharkdp/fd")
	if err == nil || err.Error() != "Failed to resolve latest sharkdp/fd release: unexpected redirect to https://github.com/login" {
		t.Fatalf("error = %v", err)
	}
}

// fetchWithRetry wiring: a transient 503 on the download is retried.
func TestDownloadFileRetriesTransientStatusAndNamesTheURLOnFailure(t *testing.T) {
	calls := 0
	tm, _ := latestVersionManager(t, func(*http.Request) *http.Response {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: http.NoBody}
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("asset"))}
	})
	dest := t.TempDir() + "/asset"
	if err := tm.downloadFile(context.Background(), "https://example.test/asset", dest); err != nil || calls != 2 {
		t.Fatalf("downloadFile err=%v after %d calls, want success after 2", err, calls)
	}

	tm, _ = latestVersionManager(t, func(*http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: http.NoBody}
	})
	err := tm.downloadFile(context.Background(), "https://example.test/missing", dest)
	if err == nil || err.Error() != "Download failed with HTTP 404: https://example.test/missing" {
		t.Fatalf("error = %v", err)
	}
}

package codingagent

// install_telemetry_test.go proves PiG's install/update ping matches upstream
// reportInstallTelemetry (packages/coding-agent/src/modes/interactive/
// interactive-mode.ts:1292-1307) and its two getChangelogForDisplay call
// sites (interactive-mode.ts:1265-1291), pointed at PiG's own endpoint
// instead of pi.dev. Every test sets PIG_INSTALL_TELEMETRY_URL to a local
// httptest server (or leaves telemetry disabled) so nothing ever reaches the
// network, including pi.dev.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// requestCapture receives one *http.Request per accepted call, non-blocking
// so a disabled-path test can assert "no request arrived" without hanging.
func newCapturingServer(t *testing.T) (*httptest.Server, chan *http.Request) {
	t.Helper()
	ch := make(chan *http.Request, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured := r.Clone(r.Context())
		ch <- captured
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server, ch
}

func awaitRequest(t *testing.T, ch chan *http.Request) *http.Request {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for install-telemetry request")
		return nil
	}
}

// fakeRoundTripper intercepts outbound HTTP calls with no socket and no real
// network access.
type fakeRoundTripper func(*http.Request) (*http.Response, error)

func (f fakeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// interceptInstallTelemetryClient swaps http.DefaultClient — the client
// sendInstallTelemetry's detached goroutine uses — for a fake transport that
// counts every request, restoring the original on cleanup.
// sendInstallTelemetry has no client-injection parameter because it runs
// unawaited, matching upstream's fire-and-forget fetch; intercepting the
// default client is the same seam used by the retained reviewer probe for
// whitespace-only offline mode.
func interceptInstallTelemetryClient(t *testing.T) *atomic.Int32 {
	t.Helper()
	old := http.DefaultClient
	var count atomic.Int32
	http.DefaultClient = &http.Client{Transport: fakeRoundTripper(func(*http.Request) (*http.Response, error) {
		count.Add(1)
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = old })
	return &count
}

// assertNoRequestSpawned runs fn inside a synctest bubble, lets synctest.Wait
// run any goroutine fn spawns to completion, then asserts the fake transport
// recorded zero requests. Unlike a non-blocking immediate channel check, this
// does not assume the production gate runs synchronously before any
// goroutine is spawned: it would equally catch a regression that moved the
// gate inside the spawned goroutine (an erroneously-always-async send), since
// synctest.Wait blocks until that goroutine is durably blocked or has
// exited before the assertion runs.
func assertNoRequestSpawned(t *testing.T, fn func()) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		count := interceptInstallTelemetryClient(t)
		fn()
		synctest.Wait()
		if n := count.Load(); n != 0 {
			t.Fatalf("unexpected install-telemetry request(s): got %d, want 0", n)
		}
	})
}

func TestReportInstallTelemetry_SendsOnlyVersionToConfiguredEndpoint(t *testing.T) {
	server, ch := newCapturingServer(t)
	t.Setenv("PIG_INSTALL_TELEMETRY_URL", server.URL+"/api/report-install")
	t.Setenv("PI_OFFLINE", "")
	_ = os.Unsetenv("PI_OFFLINE")
	t.Setenv("PI_TELEMETRY", "")
	_ = os.Unsetenv("PI_TELEMETRY")

	sm := &SettingsManager{}
	reportInstallTelemetry(sm, "0.2.0+0.87.1")

	req := awaitRequest(t, ch)
	if got := req.URL.Path; got != "/api/report-install" {
		t.Errorf("path = %q, want /api/report-install", got)
	}
	q := req.URL.Query()
	if got := q.Get("version"); got != "0.2.0+0.87.1" {
		t.Errorf("version query param = %q, want 0.2.0+0.87.1", got)
	}
	// Upstream sends only the version (and its User-Agent header); no other
	// query parameters, cookies, or body.
	if len(q) != 1 {
		t.Errorf("query params = %v, want exactly {version}", q)
	}
	if req.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", req.Method)
	}
	if ua := req.Header.Get("User-Agent"); ua == "" || ua == "Go-http-client/1.1" {
		t.Errorf("User-Agent = %q, want PiG's own product identity", ua)
	}
}

// CTEL-002 end-to-end: even when the configured endpoint URL itself already
// carries extra query parameters, the wire request must carry only version.
func TestReportInstallTelemetry_DiscardsExtraQueryParamsOnConfiguredEndpoint(t *testing.T) {
	server, ch := newCapturingServer(t)
	t.Setenv("PIG_INSTALL_TELEMETRY_URL", server.URL+"/api/report-install?operator=secret&version=leaked")
	_ = os.Unsetenv("PI_OFFLINE")
	_ = os.Unsetenv("PI_TELEMETRY")

	sm := &SettingsManager{}
	reportInstallTelemetry(sm, "0.2.0")

	req := awaitRequest(t, ch)
	q := req.URL.Query()
	if len(q) != 1 {
		t.Fatalf("query = %v, want exactly {version}: operator=secret must not reach the wire", q)
	}
	if v := q.Get("version"); v != "0.2.0" {
		t.Fatalf("version = %q, want 0.2.0 (not the endpoint's baked-in \"leaked\")", v)
	}
}

func TestReportInstallTelemetry_DefaultURLIsPiInGoDevNotPiDev(t *testing.T) {
	_ = os.Unsetenv("PIG_INSTALL_TELEMETRY_URL")
	got := installTelemetryURL()
	want := "https://pi-in-go.dev/api/report-install"
	if got != want {
		t.Errorf("default install telemetry URL = %q, want %q", got, want)
	}
	if u, err := url.Parse(got); err != nil || u.Host == "pi.dev" {
		t.Errorf("install telemetry must never target pi.dev, got %q", got)
	}
}

func TestReportInstallTelemetry_SettingDisabledSkipsRequest(t *testing.T) {
	t.Setenv("PIG_INSTALL_TELEMETRY_URL", "https://example.invalid/api/report-install")
	_ = os.Unsetenv("PI_OFFLINE")
	_ = os.Unsetenv("PI_TELEMETRY")

	disabled := false
	sm := &SettingsManager{merged: Settings{EnableInstallTelemetry: &disabled}}

	assertNoRequestSpawned(t, func() {
		reportInstallTelemetry(sm, "0.2.0")
	})
}

func TestReportInstallTelemetry_EnvOverrideDisablesEvenWhenSettingIsOn(t *testing.T) {
	t.Setenv("PIG_INSTALL_TELEMETRY_URL", "https://example.invalid/api/report-install")
	_ = os.Unsetenv("PI_OFFLINE")
	t.Setenv("PI_TELEMETRY", "0")

	enabled := true
	sm := &SettingsManager{merged: Settings{EnableInstallTelemetry: &enabled}}

	assertNoRequestSpawned(t, func() {
		reportInstallTelemetry(sm, "0.2.0")
	})
}

func TestReportInstallTelemetry_PIOfflineSkipsEvenWhenTelemetryIsOn(t *testing.T) {
	t.Setenv("PIG_INSTALL_TELEMETRY_URL", "https://example.invalid/api/report-install")
	t.Setenv("PI_OFFLINE", "1")
	_ = os.Unsetenv("PI_TELEMETRY")

	sm := &SettingsManager{}

	assertNoRequestSpawned(t, func() {
		reportInstallTelemetry(sm, "0.2.0")
	})
}

// CTEL-001 (codex review of 8b9142d69): upstream checks `process.env.PI_OFFLINE`
// with JS truthiness — any non-empty string is truthy, including a
// whitespace-only one, since JS never trims it. The prior implementation used
// strings.TrimSpace, which collapsed " " to "" and treated it as absent,
// wrongly sending the ping. This proves the raw (untrimmed) value gates it,
// with zero network and zero waiting: installTelemetryAllowed is pure.
func TestInstallTelemetryAllowed_PIOfflineWhitespaceOnlySuppresses(t *testing.T) {
	t.Setenv("PI_OFFLINE", " ")
	t.Setenv("PI_TELEMETRY", "")
	_ = os.Unsetenv("PI_TELEMETRY")

	sm := &SettingsManager{}
	if installTelemetryAllowed(sm) {
		t.Fatal(`PI_OFFLINE=" " (whitespace only) is JS-truthy and must suppress telemetry, matching upstream`)
	}
}

func TestInstallTelemetryAllowed_PIOfflineEmptyStringDoesNotSuppress(t *testing.T) {
	t.Setenv("PI_OFFLINE", "")
	_ = os.Unsetenv("PI_TELEMETRY")

	sm := &SettingsManager{}
	if !installTelemetryAllowed(sm) {
		t.Fatal(`PI_OFFLINE="" is JS-falsy (same as unset) and must not suppress telemetry`)
	}
}

func TestInstallTelemetryAllowed_PIOfflineUnsetDoesNotSuppress(t *testing.T) {
	_ = os.Unsetenv("PI_OFFLINE")
	_ = os.Unsetenv("PI_TELEMETRY")

	sm := &SettingsManager{}
	if !installTelemetryAllowed(sm) {
		t.Fatal("PI_OFFLINE unset must not suppress telemetry")
	}
}

// CTEL-002 (codex review of 8b9142d69): the prior sendInstallTelemetry took
// target.Query() (whatever PIG_INSTALL_TELEMETRY_URL's configured endpoint
// already carried) and only added "version" to it, so any extra parameter
// baked into the endpoint leaked onto the wire. Upstream sends only
// `?version=<version>`. This is a pure, zero-network, zero-wait test of the
// URL-building step alone.
func TestBuildInstallTelemetryRequestURL_DiscardsExistingQueryParams(t *testing.T) {
	got, err := buildInstallTelemetryRequestURL("https://example.test/api/report-install?operator=secret&version=leaked", "1.0.0")
	if err != nil {
		t.Fatalf("buildInstallTelemetryRequestURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("result is not a valid URL: %v", err)
	}
	q := u.Query()
	if len(q) != 1 {
		t.Fatalf("query = %v, want exactly {version}: operator=secret must not leak", q)
	}
	if v := q.Get("version"); v != "1.0.0" {
		t.Fatalf("version = %q, want 1.0.0 (the caller's version, not whatever the endpoint already had)", v)
	}
}

func TestBuildInstallTelemetryRequestURL_RejectsInvalidEndpoint(t *testing.T) {
	if _, err := buildInstallTelemetryRequestURL("not a url", "1.0.0"); err == nil {
		t.Fatal("expected an error for a schemeless/hostless endpoint")
	}
}

func TestReportInstallTelemetry_NeverContactsPiDotDev(t *testing.T) {
	// Regression guard: without PIG_INSTALL_TELEMETRY_URL and without any
	// network access, the default endpoint host must be pi-in-go.dev.
	// This test does not perform the request (that would hit the network);
	// it locks the constant that governs where every real call goes.
	if defaultInstallTelemetryURL != "https://pi-in-go.dev/api/report-install" {
		t.Fatalf("defaultInstallTelemetryURL changed to %q; must stay on pi-in-go.dev, never pi.dev", defaultInstallTelemetryURL)
	}
}

func TestRecordChangelogVersionAndMaybeReportInstall_FreshInstallPingsAndRecordsNoBanner(t *testing.T) {
	server, ch := newCapturingServer(t)
	t.Setenv("PIG_INSTALL_TELEMETRY_URL", server.URL)
	_ = os.Unsetenv("PI_OFFLINE")
	_ = os.Unsetenv("PI_TELEMETRY")

	sm := NewSettingsManager(t.TempDir(), t.TempDir())
	entries := []ChangelogEntry{{Major: 0, Minor: 2, Patch: 0, Content: "## [0.2.0]\nfirst"}}

	got := recordChangelogVersionAndMaybeReportInstall(sm, "0.2.0", entries)

	if got != nil {
		t.Errorf("fresh install must not show a changelog banner, got %v", got)
	}
	if sm.GetLastChangelogVersion() != "0.2.0" {
		t.Errorf("fresh install must record the current version, got %q", sm.GetLastChangelogVersion())
	}
	req := awaitRequest(t, ch)
	if v := req.URL.Query().Get("version"); v != "0.2.0" {
		t.Errorf("fresh-install ping version = %q, want 0.2.0", v)
	}
}

func TestRecordChangelogVersionAndMaybeReportInstall_UpdateWithNewEntriesPingsAndShowsBanner(t *testing.T) {
	server, ch := newCapturingServer(t)
	t.Setenv("PIG_INSTALL_TELEMETRY_URL", server.URL)
	_ = os.Unsetenv("PI_OFFLINE")
	_ = os.Unsetenv("PI_TELEMETRY")

	sm := NewSettingsManager(t.TempDir(), t.TempDir())
	if err := sm.SetLastChangelogVersion("0.1.0"); err != nil {
		t.Fatalf("SetLastChangelogVersion: %v", err)
	}
	entries := []ChangelogEntry{
		{Major: 0, Minor: 1, Patch: 0, Content: "## [0.1.0]\nold"},
		{Major: 0, Minor: 2, Patch: 0, Content: "## [0.2.0]\nnew"},
	}

	got := recordChangelogVersionAndMaybeReportInstall(sm, "0.2.0", entries)

	if len(got) != 1 || got[0].Content != "## [0.2.0]\nnew" {
		t.Errorf("expected the one new entry, got %v", got)
	}
	if sm.GetLastChangelogVersion() != "0.2.0" {
		t.Errorf("update must record the new version, got %q", sm.GetLastChangelogVersion())
	}
	req := awaitRequest(t, ch)
	if v := req.URL.Query().Get("version"); v != "0.2.0" {
		t.Errorf("update ping version = %q, want 0.2.0", v)
	}
}

func TestRecordChangelogVersionAndMaybeReportInstall_SameVersionNeverPings(t *testing.T) {
	t.Setenv("PIG_INSTALL_TELEMETRY_URL", "https://example.invalid/api/report-install")
	_ = os.Unsetenv("PI_OFFLINE")
	_ = os.Unsetenv("PI_TELEMETRY")

	sm := NewSettingsManager(t.TempDir(), t.TempDir())
	if err := sm.SetLastChangelogVersion("0.2.0"); err != nil {
		t.Fatalf("SetLastChangelogVersion: %v", err)
	}
	entries := []ChangelogEntry{{Major: 0, Minor: 2, Patch: 0, Content: "## [0.2.0]\nsame"}}

	var got []ChangelogEntry
	assertNoRequestSpawned(t, func() {
		got = recordChangelogVersionAndMaybeReportInstall(sm, "0.2.0", entries)
	})

	if got != nil {
		t.Errorf("unchanged version must not show a banner, got %v", got)
	}
}

func TestRecordChangelogVersionAndMaybeReportInstall_VersionBumpWithNoNewEntriesNeverPings(t *testing.T) {
	// Mirrors upstream's `if (newEntries.length > 0)` guard: a version bump
	// whose changelog has nothing newer than lastSeen neither records the
	// version nor pings.
	t.Setenv("PIG_INSTALL_TELEMETRY_URL", "https://example.invalid/api/report-install")
	_ = os.Unsetenv("PI_OFFLINE")
	_ = os.Unsetenv("PI_TELEMETRY")

	sm := NewSettingsManager(t.TempDir(), t.TempDir())
	if err := sm.SetLastChangelogVersion("0.2.0"); err != nil {
		t.Fatalf("SetLastChangelogVersion: %v", err)
	}
	entries := []ChangelogEntry{{Major: 0, Minor: 2, Patch: 0, Content: "## [0.2.0]\nsame"}}

	var got []ChangelogEntry
	assertNoRequestSpawned(t, func() {
		got = recordChangelogVersionAndMaybeReportInstall(sm, "0.2.0-build.5", entries)
	})

	if got != nil {
		t.Errorf("no new entries must not show a banner, got %v", got)
	}
	if sm.GetLastChangelogVersion() != "0.2.0" {
		t.Errorf("version bump with no new entries must not record the new version, got %q", sm.GetLastChangelogVersion())
	}
}

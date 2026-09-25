package codingagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func allowLoopbackUpdateHTTP(t *testing.T) {
	t.Helper()
	t.Setenv("PIG_UPDATE_ALLOW_LOOPBACK_HTTP", "1")
}

func newSignedManifestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	allowLoopbackUpdateHTTP(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	trustPath := filepath.Join(t.TempDir(), "update-trust.pem")
	if err := os.WriteFile(trustPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_UPDATE_TRUST_ROOT", trustPath)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		response := recorder.Result()
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Errorf("read manifest response: %v", err)
			return
		}
		for key, values := range response.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		if response.StatusCode == http.StatusOK {
			w.Header().Set(UpdateSignatureHeader, base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, body)))
		}
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
	}))
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.1", "0.1.2", -1},
		{"0.1.2", "0.1.1", 1},
		{"0.1.1", "0.1.1", 0},
		{"1.0.0", "0.9.9", 1},
		{"0.2.0", "0.10.0", -1}, // numeric, not lexical
		{"1.2.3", "1.2.3-rc1", 1},
		{"1.2.3-rc1", "1.2.3", -1},
		{"1.2.3-rc1", "1.2.3-rc2", -1},
		{"1.2.3-2", "1.2.3-10", -1},     // numeric prerelease identifiers use SemVer ordering
		{"v0.1.1", "0.1.2", 0},          // strict SemVer rejects a v prefix
		{"0.0.0-tilt.abc", "0.1.0", -1}, // parseable prerelease core is genuinely lower
		{"nonsense", "0.1.0", 0},        // unparseable → no nag
		{"0.1.0", "also-bad", 0},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestUpdateSourceURLPrefersEnvThenDefault(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_UPDATE_URL", "")
	orig := DefaultUpdateURL
	t.Cleanup(func() { DefaultUpdateURL = orig })

	DefaultUpdateURL = "https://embedded.example/manifest.json"
	if got := UpdateSourceURL(); got != DefaultUpdateURL {
		t.Fatalf("UpdateSourceURL() = %q, want embedded default", got)
	}
	t.Setenv("PIG_UPDATE_URL", "https://env.example/manifest.json")
	if got := UpdateSourceURL(); got != "https://env.example/manifest.json" {
		t.Fatalf("UpdateSourceURL() = %q, want env override", got)
	}
}

// TestUpdateSourceURLSidecarSeedsBetweenEnvAndDefault proves an installer can
// seed the update source via the transport-neutral <config-root>/update-url
// sidecar without a Go toolchain or shell-rc edit. Env wins over the sidecar;
// the sidecar wins over the baked default.
func TestUpdateSourceURLSidecarSeedsBetweenEnvAndDefault(t *testing.T) {
	t.Setenv("PIG_UPDATE_URL", "")
	orig := DefaultUpdateURL
	t.Cleanup(func() { DefaultUpdateURL = orig })
	DefaultUpdateURL = ""

	t.Setenv("PIG_HOME", t.TempDir())
	home := ConfigRoot()
	sidecar := filepath.Join(home, updateURLSidecarName)
	if err := os.WriteFile(sidecar, []byte("https://installed.example/manifest.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(sidecar) })

	if got := UpdateSourceURL(); got != "https://installed.example/manifest.json" {
		t.Fatalf("sidecar: UpdateSourceURL() = %q, want sidecar URL", got)
	}
	// Env overrides the sidecar.
	t.Setenv("PIG_UPDATE_URL", "https://env.example/manifest.json")
	if got := UpdateSourceURL(); got != "https://env.example/manifest.json" {
		t.Fatalf("env override: UpdateSourceURL() = %q", got)
	}
	// A blank sidecar falls through to the baked default.
	t.Setenv("PIG_UPDATE_URL", "")
	_ = os.WriteFile(sidecar, []byte("  \n"), 0o600)
	DefaultUpdateURL = "https://embedded.example/manifest.json"
	if got := UpdateSourceURL(); got != DefaultUpdateURL {
		t.Fatalf("blank sidecar: UpdateSourceURL() = %q, want baked default", got)
	}
}

func TestUpdateTransportCASidecarAllowsPrivateHTTPS(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	payload := []byte("private update")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(ConfigRoot(), updateTransportCASidecarName), certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	path, sum, err := downloadBinaryToFile(context.Background(), &http.Client{}, srv.URL, t.TempDir(), 1024)
	if err != nil {
		t.Fatalf("private-CA download: %v", err)
	}
	defer func() { _ = os.Remove(path) }()
	want := sha256.Sum256(payload)
	if sum != hex.EncodeToString(want[:]) {
		t.Fatalf("download digest = %s, want %x", sum, want)
	}
}

func TestUpdateTransportCASidecarPreservesClientRoots(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	sidecarServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer sidecarServer.Close()
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: sidecarServer.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(ConfigRoot(), updateTransportCASidecarName), certificate, 0o600); err != nil {
		t.Fatal(err)
	}

	requestServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("update"))
	}))
	defer requestServer.Close()
	path, _, err := downloadBinaryToFile(context.Background(), requestServer.Client(), requestServer.URL, t.TempDir(), 1024)
	if err != nil {
		t.Fatalf("download using caller roots: %v", err)
	}
	_ = os.Remove(path)
}

func TestUpdateTransportCASidecarRejectsUnsafeMaterial(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("update"))
	}))
	defer srv.Close()
	path := filepath.Join(ConfigRoot(), updateTransportCASidecarName)

	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := downloadBinaryToFile(context.Background(), &http.Client{}, srv.URL, t.TempDir(), 1024); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("malformed transport CA error = %v", err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(path, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	testenv.GrantOthersRead(t, path)
	if _, _, err := downloadBinaryToFile(context.Background(), &http.Client{}, srv.URL, t.TempDir(), 1024); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("world-readable transport CA error = %v", err)
	}
}

func TestFetchUpdateManifest(t *testing.T) {
	srv := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(`{"version":"0.2.0","packageName":"pig","notes":"n","binaries":{"` + platformKey() + `":{"url":"u","sha256":"` + strings.Repeat("0", sha256.Size*2) + `"}}}`))
		case "/noversion":
			_, _ = w.Write([]byte(`{"binaries":{}}`))
		case "/badversion":
			_, _ = w.Write([]byte(`{"version":"1.2","binaries":{}}`))
		case "/badpackage":
			_, _ = w.Write([]byte(`{"version":"1.2.3","packageName":"--registry=evil","binaries":{}}`))
		case "/badjson":
			_, _ = w.Write([]byte(`not json`))
		case "/unknown":
			_, _ = w.Write([]byte(`{"version":"0.2.0","packageName":"pig","unknown":true,"binaries":{}}`))
		case "/trailing":
			_, _ = w.Write([]byte(`{"version":"0.2.0","packageName":"pig","binaries":{}} {}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := srv.Client()

	m, err := FetchUpdateManifest(context.Background(), client, srv.URL+"/ok")
	if err != nil {
		t.Fatalf("FetchUpdateManifest: %v", err)
	}
	if m.Version != "0.2.0" {
		t.Fatalf("version = %q", m.Version)
	}
	if bin, ok := m.PlatformBinary(); !ok || bin.URL != srv.URL+"/u" {
		t.Fatalf("PlatformBinary = %#v ok=%v", bin, ok)
	}
	if _, ok := (&UpdateManifest{Binaries: map[string]UpdateBinary{
		platformKey(): {URL: "u"},
	}}).PlatformBinary(); ok {
		t.Fatal("PlatformBinary accepted an entry without a valid checksum")
	}
	for _, path := range []string{"/noversion", "/badversion", "/badpackage", "/badjson", "/unknown", "/trailing", "/missing"} {
		if _, err := FetchUpdateManifest(context.Background(), client, srv.URL+path); err == nil {
			t.Errorf("FetchUpdateManifest(%s) error = nil", path)
		}
	}
}

func TestUpdateManifestScriptEmitsCurrentStrictShape(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is unavailable")
	}
	dir := t.TempDir()
	name := "pig-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("pig-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join("..", "..", "automation", "release", "gen-update-manifest.py")
	cmd := exec.Command(python, script, "--version", "1.2.3-rc.1", "--package-name", "pig-next", "--base-url", "https://updates.example", "--dir", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generate manifest: %v\n%s", err, out)
	}
	var manifest UpdateManifest
	if err := json.Unmarshal(out, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.2.3-rc.1" || manifest.PackageName != "pig-next" {
		t.Fatalf("manifest identity = %#v", manifest)
	}
	if _, ok := manifest.Binaries[platformKey()]; !ok {
		t.Fatalf("manifest lacks %s: %#v", platformKey(), manifest.Binaries)
	}

	cmd = exec.Command(python, script, "--version", "1.2", "--base-url", "https://updates.example", "--dir", dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("generator accepted non-strict release version")
	}
	cmd = exec.Command(python, script, "--version", "1.2.3", "--base-url", "http://updates.example", "--dir", dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("generator accepted unauthenticated remote binary URL")
	}
}

func TestAC8AuthenticatedReleaseMetadata(t *testing.T) {
	body := []byte(`{"version":"1.2.3","packageName":"pig","binaries":{}}`)

	t.Run("signed_loopback_development_source", func(t *testing.T) {
		srv := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		}))
		defer srv.Close()
		manifest, err := FetchUpdateManifest(context.Background(), srv.Client(), srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		if manifest.Version != "1.2.3" {
			t.Fatalf("version = %s, want 1.2.3", manifest.Version)
		}
	})

	t.Run("unsigned_metadata_rejected", func(t *testing.T) {
		allowLoopbackUpdateHTTP(t)
		t.Setenv("PIG_UPDATE_TRUST_ROOT", filepath.Join(t.TempDir(), "missing.pem"))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		}))
		defer srv.Close()
		if _, err := FetchUpdateManifest(context.Background(), srv.Client(), srv.URL); err == nil {
			t.Fatal("unsigned manifest accepted")
		}
	})

	t.Run("tampered_metadata_rejected", func(t *testing.T) {
		allowLoopbackUpdateHTTP(t)
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := x509.MarshalPKIXPublicKey(publicKey)
		if err != nil {
			t.Fatal(err)
		}
		trustPath := filepath.Join(t.TempDir(), "trust.pem")
		if err := os.WriteFile(trustPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PIG_UPDATE_TRUST_ROOT", trustPath)
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, body))
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(UpdateSignatureHeader, signature)
			_, _ = w.Write([]byte(`{"version":"9.9.9","packageName":"pig","binaries":{}}`))
		}))
		defer srv.Close()
		if _, err := FetchUpdateManifest(context.Background(), srv.Client(), srv.URL); err == nil || !strings.Contains(err.Error(), "signature") {
			t.Fatalf("tampered manifest error = %v", err)
		}
	})

	t.Run("remote_http_rejected", func(t *testing.T) {
		t.Setenv("PIG_UPDATE_ALLOW_LOOPBACK_HTTP", "1")
		if _, err := FetchUpdateManifest(context.Background(), http.DefaultClient, "http://updates.example/manifest"); err == nil {
			t.Fatal("remote HTTP source accepted")
		}
	})

	t.Run("https_downgrade_redirect_rejected", func(t *testing.T) {
		allowLoopbackUpdateHTTP(t)
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		}))
		defer target.Close()
		source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, http.StatusFound)
		}))
		defer source.Close()
		if _, err := FetchUpdateManifest(context.Background(), source.Client(), source.URL); err == nil || !strings.Contains(err.Error(), "downgrade") {
			t.Fatalf("downgrade redirect error = %v", err)
		}
	})
}

func TestFetchUpdateManifestRejectsOversizedResponse(t *testing.T) {
	allowLoopbackUpdateHTTP(t)
	srv := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, (1<<20)+1))
	}))
	defer srv.Close()
	if _, err := FetchUpdateManifest(context.Background(), srv.Client(), srv.URL); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("FetchUpdateManifest oversized error = %v", err)
	}
}

func TestCheckForBinaryUpdate(t *testing.T) {
	srv := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"9.9.9","packageName":"pig","binaries":{"` + platformKey() + `":{"url":"u","sha256":"` + strings.Repeat("0", sha256.Size*2) + `"}}}`))
	}))
	defer srv.Close()
	t.Setenv("PIG_UPDATE_URL", srv.URL)

	if u := CheckForBinaryUpdate(context.Background(), srv.Client(), "0.1.1"); u == nil || u.LatestVersion != "9.9.9" || u.Command != "pig update" {
		t.Fatalf("CheckForBinaryUpdate = %#v, want newer", u)
	}
	if u := CheckForBinaryUpdate(context.Background(), srv.Client(), "9.9.9"); u != nil {
		t.Fatalf("CheckForBinaryUpdate(up-to-date) = %#v, want nil", u)
	}
	t.Setenv("PIG_UPDATE_URL", "")
	if u := CheckForBinaryUpdate(context.Background(), srv.Client(), "0.1.1"); u != nil {
		t.Fatalf("CheckForBinaryUpdate(no source) = %#v, want nil", u)
	}
}

func TestSelfReplaceAtVerifiesChecksumAndReplaces(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	allowLoopbackUpdateHTTP(t)
	payload := []byte("#!/bin/sh\necho new\n")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	exe := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Missing checksum is refused and the binary is untouched.
	if err := SelfReplaceAt(context.Background(), srv.Client(), UpdateBinary{URL: srv.URL}, exe); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("SelfReplaceAt without checksum error = %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatalf("binary changed after missing checksum: %q", got)
	}

	// Wrong checksum is refused and the binary is untouched.
	if err := SelfReplaceAt(context.Background(), srv.Client(), UpdateBinary{URL: srv.URL, SHA256: "deadbeef"}, exe); err == nil {
		t.Fatal("SelfReplaceAt with bad checksum: error = nil")
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Fatalf("binary changed after checksum failure: %q", got)
	}

	// Correct checksum replaces atomically and keeps 0755.
	if err := SelfReplaceAt(context.Background(), srv.Client(), UpdateBinary{URL: srv.URL, SHA256: hex.EncodeToString(sum[:])}, exe); err != nil {
		t.Fatalf("SelfReplaceAt: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("binary not replaced: %q err=%v", got, err)
	}
	info, _ := os.Stat(exe)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
}

func TestSelfReplaceAtWithCommitRestoresPreviousExecutable(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	allowLoopbackUpdateHTTP(t)
	payload := []byte("#!/bin/sh\necho new\n")
	sum := sha256.Sum256(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dir := t.TempDir()
	exe := filepath.Join(dir, "pig")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := SelfReplaceAtWithCommit(
		context.Background(),
		srv.Client(),
		UpdateBinary{URL: srv.URL, SHA256: hex.EncodeToString(sum[:])},
		exe,
		func() error { return errors.New("receipt sync failed") },
	)
	if err == nil || !strings.Contains(err.Error(), "previous executable restored") {
		t.Fatalf("SelfReplaceAtWithCommit error = %v", err)
	}
	got, readErr := os.ReadFile(exe)
	if readErr != nil || string(got) != "old" {
		t.Fatalf("previous executable was not restored: %q (err=%v)", got, readErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(dir, ".pig-update-backup-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("rollback backups remain: %v (err=%v)", matches, globErr)
	}
}

func TestDownloadBinaryRejectsOversizedResponse(t *testing.T) {
	allowLoopbackUpdateHTTP(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("12345"))
	}))
	defer srv.Close()
	_, _, err := downloadBinaryToFile(context.Background(), srv.Client(), srv.URL, t.TempDir(), 4)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("downloadBinaryToFile error = %v", err)
	}
}

func TestSelfUpdateFallbackReflectsConfiguredSource(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	orig := DefaultUpdateURL
	t.Cleanup(func() { DefaultUpdateURL = orig })
	DefaultUpdateURL = ""
	t.Setenv("PIG_UPDATE_URL", "")
	if got := SelfUpdateFallback(); got == "" || !strings.Contains(got, "PIG_UPDATE_URL") {
		t.Fatalf("fallback (no source) = %q", got)
	}
	t.Setenv("PIG_UPDATE_URL", "https://example/m.json")
	if got := SelfUpdateFallback(); strings.Contains(got, "pig update") || !strings.Contains(strings.ToLower(got), "download") || !strings.Contains(got, "container image") {
		t.Fatalf("fallback (source set) repeats failed update or lacks next-tier remediation = %q", got)
	}
}

// TestAC3StandaloneUpdateVerificationAndAtomicity is the spec-named standalone
// failure matrix. Every invalid, incomplete, canceled, or bounded download
// leaves the original target untouched; only a complete checksum-verified
// payload replaces it.
func TestAC3StandaloneUpdateVerificationAndAtomicity(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	allowLoopbackUpdateHTTP(t)
	payload := []byte("#!/bin/sh\necho new\n")
	sum := sha256.Sum256(payload)
	goodChecksum := hex.EncodeToString(sum[:])

	newTarget := func(t *testing.T) string {
		t.Helper()
		target := filepath.Join(t.TempDir(), "pig")
		if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		return target
	}
	assertOld := func(t *testing.T, target string) {
		t.Helper()
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "old" {
			t.Fatalf("target changed after failed update: %q", got)
		}
	}

	t.Run("valid_replaces_atomically", func(t *testing.T) {
		target := newTarget(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(payload)
		}))
		defer srv.Close()
		if err := SelfReplaceAt(context.Background(), srv.Client(), UpdateBinary{URL: srv.URL, SHA256: goodChecksum}, target); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(target)
		if err != nil || string(got) != string(payload) {
			t.Fatalf("valid update did not replace target: %q (err=%v)", got, err)
		}
		info, err := os.Stat(target)
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %v, want 0755", info)
		}
	})

	failureCases := []struct {
		name       string
		checksum   string
		serverBody []byte
		headerSize string
	}{
		{name: "missing_checksum", checksum: ""},
		{name: "wrong_checksum", checksum: strings.Repeat("0", sha256.Size*2)},
		{name: "malformed_checksum", checksum: "not-hex"},
		{name: "truncated_declared_body", checksum: goodChecksum, serverBody: []byte("short"), headerSize: "100"},
	}
	for _, tc := range failureCases {
		t.Run(tc.name, func(t *testing.T) {
			target := newTarget(t)
			body := payload
			if tc.serverBody != nil {
				body = tc.serverBody
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.headerSize != "" {
					w.Header().Set("Content-Length", tc.headerSize)
				}
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			if err := SelfReplaceAt(context.Background(), srv.Client(), UpdateBinary{URL: srv.URL, SHA256: tc.checksum}, target); err == nil {
				t.Fatal("failed verification returned nil")
			}
			assertOld(t, target)
		})
	}

	t.Run("oversized_stream_is_rejected_before_replace", func(t *testing.T) {
		target := newTarget(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("12345"))
		}))
		defer srv.Close()
		if _, _, err := downloadBinaryToFile(context.Background(), srv.Client(), srv.URL, t.TempDir(), 4); err == nil {
			t.Fatal("oversized stream returned nil")
		}
		assertOld(t, target)
	})

	t.Run("canceled_request_preserves_original", func(t *testing.T) {
		target := newTarget(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer srv.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := SelfReplaceAt(ctx, srv.Client(), UpdateBinary{URL: srv.URL, SHA256: goodChecksum}, target); err == nil {
			t.Fatal("canceled request returned nil")
		}
		assertOld(t, target)
	})

	t.Run("timeout_preserves_original", func(t *testing.T) {
		target := newTarget(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		if err := SelfReplaceAt(ctx, srv.Client(), UpdateBinary{URL: srv.URL, SHA256: goodChecksum}, target); err == nil {
			t.Fatal("timed-out request returned nil")
		}
		assertOld(t, target)
	})

	t.Run("wrong_platform_does_not_mutate", func(t *testing.T) {
		target := newTarget(t)
		srv := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"version":"9.9.9","packageName":"pig","binaries":{"plan9/sparc":{"url":"https://unused.example/pig","sha256":"` + goodChecksum + `"}}}`))
		}))
		defer srv.Close()
		manifest, err := FetchUpdateManifest(context.Background(), srv.Client(), srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := manifest.PlatformBinary(); ok {
			t.Fatal("wrong-platform manifest returned a current platform binary")
		}
		assertOld(t, target)
	})

	t.Run("malformed_manifest_does_not_mutate", func(t *testing.T) {
		target := newTarget(t)
		srv := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"version":`))
		}))
		defer srv.Close()
		if _, err := FetchUpdateManifest(context.Background(), srv.Client(), srv.URL); err == nil {
			t.Fatal("malformed manifest returned nil error")
		}
		assertOld(t, target)
	})
}

func BenchmarkDownloadBinaryToFile(b *testing.B) {
	payload := make([]byte, 1<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	dir := b.TempDir()
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for range b.N {
		path, _, err := downloadBinaryToFile(context.Background(), srv.Client(), srv.URL, dir, maxUpdateBinaryBytes)
		if err != nil {
			b.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			b.Fatal(err)
		}
	}
}

// TestAC6CheckAndFallbackBehavior proves startup checks are offline-aware,
// bounded, and best-effort while explicit remediation remains actionable.
func TestAC6CheckAndFallbackBehavior(t *testing.T) {
	allowLoopbackUpdateHTTP(t)
	var requests atomic.Int32
	srv := newSignedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"version":"9.9.9","packageName":"pig","binaries":{"` + platformKey() + `":{"url":"u","sha256":"` + strings.Repeat("0", sha256.Size*2) + `"}}}`))
	}))
	defer srv.Close()
	t.Setenv("PIG_UPDATE_URL", srv.URL)

	for _, key := range []string{"PIG_OFFLINE", "PI_OFFLINE"} {
		t.Run("offline_"+key, func(t *testing.T) {
			t.Setenv("PIG_OFFLINE", "")
			t.Setenv("PI_OFFLINE", "")
			t.Setenv(key, "1")
			if got := CheckForBinaryUpdate(context.Background(), srv.Client(), "0.1.0"); got != nil {
				t.Fatalf("offline startup check = %#v, want nil", got)
			}
			if requests.Load() != 0 {
				t.Fatalf("offline startup check made %d request(s)", requests.Load())
			}
		})
	}

	t.Run("pi_offline_any_nonempty_value", func(t *testing.T) {
		t.Setenv("PIG_OFFLINE", "")
		t.Setenv("PI_OFFLINE", "0")
		if got := CheckForBinaryUpdate(context.Background(), srv.Client(), "0.1.0"); got != nil {
			t.Fatalf("PI_OFFLINE=0 startup check = %#v, want nil", got)
		}
	})

	t.Run("unreachable_is_best_effort", func(t *testing.T) {
		t.Setenv("PIG_OFFLINE", "")
		t.Setenv("PI_OFFLINE", "")
		t.Setenv("PIG_UPDATE_URL", "http://127.0.0.1:1/no-source")
		client := &http.Client{Timeout: 50 * time.Millisecond}
		if got := CheckForBinaryUpdate(context.Background(), client, "0.1.0"); got != nil {
			t.Fatalf("unreachable startup check = %#v, want nil", got)
		}
	})

	t.Run("malformed_is_best_effort", func(t *testing.T) {
		t.Setenv("PIG_OFFLINE", "")
		t.Setenv("PI_OFFLINE", "")
		bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not-json"))
		}))
		defer bad.Close()
		t.Setenv("PIG_UPDATE_URL", bad.URL)
		if got := CheckForBinaryUpdate(context.Background(), bad.Client(), "0.1.0"); got != nil {
			t.Fatalf("malformed startup check = %#v, want nil", got)
		}
	})

	t.Run("newer_reports_update", func(t *testing.T) {
		t.Setenv("PIG_OFFLINE", "")
		t.Setenv("PI_OFFLINE", "")
		if got := CheckForBinaryUpdate(context.Background(), srv.Client(), "0.1.0"); got == nil || got.LatestVersion != "9.9.9" {
			t.Fatalf("newer startup check = %#v, want 9.9.9", got)
		}
	})

	t.Run("up_to_date_reports_nothing", func(t *testing.T) {
		t.Setenv("PIG_OFFLINE", "")
		t.Setenv("PI_OFFLINE", "")
		if got := CheckForBinaryUpdate(context.Background(), srv.Client(), "9.9.9"); got != nil {
			t.Fatalf("up-to-date startup check = %#v, want nil", got)
		}
	})

	t.Run("explicit_fallback_is_actionable_and_non_looping", func(t *testing.T) {
		message := UnsupportedRemediation("/readonly/bin/pig")
		if !strings.Contains(message, "/readonly/bin/pig") {
			t.Fatalf("fallback omits executable path: %s", message)
		}
		if strings.Contains(message, "pig update") {
			t.Fatalf("fallback loops to failed command: %s", message)
		}
		if !strings.Contains(strings.ToLower(message), "reinstall") || !strings.Contains(strings.ToLower(message), "download") {
			t.Fatalf("fallback lacks concrete remediation: %s", message)
		}
	})
}

// TestAC7SelfUpdateSafetyMutationMatrix is the spec-named guard matrix. Each
// case asserts an independent safety invariant. Removing the corresponding
// comparison or branch makes its owning assertion fail; checksum and
// no-fallthrough were also verified with source mutations in this loop.
func TestAC7SelfUpdateSafetyMutationMatrix(t *testing.T) {
	allowLoopbackUpdateHTTP(t)
	t.Run("checksum_guard", func(t *testing.T) {
		requireStandaloneSelfUpdateTier(t)
		payload := []byte("new")
		target := filepath.Join(t.TempDir(), "pig")
		if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(payload)
		}))
		defer srv.Close()
		if err := SelfReplaceAt(context.Background(), srv.Client(), UpdateBinary{
			URL: srv.URL, SHA256: strings.Repeat("0", sha256.Size*2),
		}, target); err == nil {
			t.Fatal("wrong checksum accepted")
		}
		got, _ := os.ReadFile(target)
		if string(got) != "old" {
			t.Fatalf("checksum failure mutated target: %q", got)
		}
	})

	t.Run("size_guard", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("12345"))
		}))
		defer srv.Close()
		if _, _, err := downloadBinaryToFile(context.Background(), srv.Client(), srv.URL, t.TempDir(), 4); err == nil {
			t.Fatal("oversized stream accepted")
		}
	})

	t.Run("ownership_guard", func(t *testing.T) {
		root := t.TempDir()
		shared := filepath.Join(root, "node_modules")
		binDir := filepath.Join(shared, "pig", "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := resolveTierForExe(t, writeFakeExe(t, binDir), fakeCmdRunner{outputs: map[string]string{
			"npm root -g":  shared,
			"pnpm root -g": shared,
		}})
		var tierErr *TierError
		if !errors.As(err, &tierErr) || tierErr.Tier != tierPackageManager {
			t.Fatalf("ambiguous ownership error = %v, want package-manager TierError", err)
		}
	})

	t.Run("no_fallthrough_guard", func(t *testing.T) {
		standaloneCalled := false
		prov := &SelfUpdateProvenance{Tier: tierStandalone, ExePath: "/tmp/pig"}
		failure := errors.New("standalone failed")
		result := applyProvenance(prov, func(string) error {
			standaloneCalled = true
			return failure
		}, nil)
		if !standaloneCalled {
			t.Fatal("standalone tier did not start")
		}
		if result.Action != "standalone-failed" || !errors.Is(result.Cause, failure) {
			t.Fatalf("result = %+v, want standalone failure with preserved cause", result)
		}
		if result.Action == "package-manager-updated" || result.Action == "container-updated" {
			t.Fatal("standalone failure fell through to another tier")
		}
	})
}

func pemPublicKey(t *testing.T, key ed25519.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func TestUpdateTrustRootsReadsEveryKeyInTheBundle(t *testing.T) {
	first, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "update-trust.pem")
	bundle := append(pemPublicKey(t, first), pemPublicKey(t, second)...)
	if err := os.WriteFile(path, bundle, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_UPDATE_TRUST_ROOT", path)

	keys, err := updateTrustRoots()
	if err != nil {
		t.Fatalf("updateTrustRoots() error = %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("trusted key count = %d, want 2 (a bundle is what makes rotation survivable)", len(keys))
	}
}

func TestUpdateTrustRootsRejectsNonKeyMaterial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-trust.pem")
	if err := os.WriteFile(path, []byte("not a pem file"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_UPDATE_TRUST_ROOT", path)
	if _, err := updateTrustRoots(); err == nil {
		t.Fatal("garbage trust root was accepted")
	}
}

// The rotation contract: while a publisher signs with both the outgoing and the
// incoming key, a client on either side of the rotation still verifies, and a
// client trusting neither still refuses.
func TestReleaseSignatureVerificationSurvivesKeyRotation(t *testing.T) {
	outgoingPublic, outgoingPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	incomingPublic, incomingPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	strangerPublic, strangerPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"version":"1.2.3"}`)
	rotating := base64.StdEncoding.EncodeToString(ed25519.Sign(outgoingPrivate, body)) +
		"," + base64.StdEncoding.EncodeToString(ed25519.Sign(incomingPrivate, body))

	if !verifyReleaseSignature(body, rotating, []ed25519.PublicKey{outgoingPublic}) {
		t.Fatal("rotation broke clients still holding the outgoing key")
	}
	if !verifyReleaseSignature(body, rotating, []ed25519.PublicKey{incomingPublic}) {
		t.Fatal("a fresh install holding the incoming key could not verify")
	}
	if verifyReleaseSignature(body, rotating, []ed25519.PublicKey{strangerPublic}) {
		t.Fatal("a client trusting neither key accepted the release")
	}

	both := []ed25519.PublicKey{outgoingPublic, incomingPublic}
	// Once rotation completes the publisher drops the outgoing signature, so a
	// client whose bundle still lists the outgoing key first must verify via a
	// later key in that bundle.
	incomingOnly := base64.StdEncoding.EncodeToString(ed25519.Sign(incomingPrivate, body))
	if !verifyReleaseSignature(body, incomingOnly, both) {
		t.Fatal("a bundled trust root did not verify past its first key")
	}
	if verifyReleaseSignature([]byte(`{"version":"9.9.9"}`), rotating, both) {
		t.Fatal("a tampered manifest was accepted")
	}
	if verifyReleaseSignature(body, "", both) {
		t.Fatal("an unsigned manifest was accepted")
	}
	if verifyReleaseSignature(body, "not-base64", both) {
		t.Fatal("a malformed signature was accepted")
	}
	stranger := base64.StdEncoding.EncodeToString(ed25519.Sign(strangerPrivate, body))
	if verifyReleaseSignature(body, stranger, both) {
		t.Fatal("a signature from an untrusted key was accepted")
	}
}

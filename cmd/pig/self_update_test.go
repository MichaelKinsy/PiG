package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func signedManifestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	t.Setenv("PIG_UPDATE_ALLOW_LOOPBACK_HTTP", "1")
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
		w.Header().Set(codingagent.UpdateSignatureHeader, base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, body)))
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
	}))
}

// upToDateManifest serves a manifest whose version is not newer than the
// running test binary's selfUpdateVersion().
func upToDateManifest(t *testing.T) *httptest.Server {
	t.Helper()
	ver := selfUpdateVersion()
	body := `{"version":"` + ver + `","packageName":"pig","binaries":{"` + codingagent.PlatformKey() + `":{"url":"u","sha256":"s"}}}`
	return signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

// newerManifest serves a manifest with a newer version and a real downloadable
// payload whose checksum verifies.
func newerManifest(t *testing.T, payload []byte) (*httptest.Server, *httptest.Server) {
	t.Helper()
	sum := sha256.Sum256(payload)
	binSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	manifest := `{"version":"9.9.9","packageName":"pig","binaries":{"` + codingagent.PlatformKey() + `":{"url":"` + binSrv.URL + `","sha256":"` + hex.EncodeToString(sum[:]) + `"}}}`
	manSrv := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(manifest))
	}))
	return manSrv, binSrv
}

// requireStandaloneSelfUpdateTier skips a test of the standalone tier on
// Windows. There ResolveSelfUpdateTier selects the unsupported tier for a
// standalone pig.exe and applyStandaloneUpdate refuses in-place replacement
// (D39). TestSelfUpdateOnWindowsRefusesReceiptedStandalone covers the Windows
// behavior.
func requireStandaloneSelfUpdateTier(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("D39: a standalone Windows binary is not replaced in place")
	}
}

func seedRunningStandaloneReceipt(t *testing.T, source string) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_UPDATE_URL", source)
	originalVersion := codingagent.InstalledPigVersion
	t.Cleanup(func() { codingagent.InstalledPigVersion = originalVersion })
	codingagent.InstalledPigVersion = PigVersion
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := codingagent.WriteStandaloneReceipt(exe, PigVersion, source); err != nil {
		t.Fatalf("write standalone receipt: %v", err)
	}
}

// Upstream checks the latest version before anything else: an installation it
// cannot update still exits 0 when it is already up to date, and is refused
// only when a newer release exists.
func TestSelfUpdateChecksTheVersionBeforeRefusingAnInstallation(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_INSTALL_TIER", "container")
	current := upToDateManifest(t)
	defer current.Close()
	t.Setenv("PIG_UPDATE_URL", current.URL)
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	if code != 0 || !strings.Contains(stderr, "up to date") {
		t.Fatalf("up-to-date container install: code = %d, want 0 and up to date; stdout=%q stderr=%q", code, stdout, stderr)
	}

	manSrv, binSrv := newerManifest(t, []byte("new"))
	defer manSrv.Close()
	defer binSrv.Close()
	t.Setenv("PIG_UPDATE_URL", manSrv.URL)
	stdout, stderr, code = captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	if code != 1 || strings.Contains(stderr, "up to date") {
		t.Fatalf("outdated container install: code = %d, want 1 and a refusal; stdout=%q stderr=%q", code, stdout, stderr)
	}
}

// TestAC6CheckAndFallbackBehavior_UpToDateReturnsZero proves an explicit
// `pig update` against a manifest reporting the current version exits 0 without
// mutating the binary.
func TestAC6CheckAndFallbackBehavior_UpToDateReturnsZero(t *testing.T) {
	srv := upToDateManifest(t)
	defer srv.Close()
	seedRunningStandaloneReceipt(t, srv.URL)
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "up to date") {
		t.Fatalf("stderr missing up-to-date message:\n%s", stderr)
	}
}

// TestAC6CheckAndFallbackBehavior_NoSourceIsActionable proves an unreceipted
// executable refuses mutation and reports its path instead of treating
// writability as standalone ownership.
func TestAC6CheckAndFallbackBehavior_NoSourceIsActionable(t *testing.T) {
	t.Setenv("PIG_UPDATE_URL", "")
	t.Setenv("PIG_HOME", t.TempDir())
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "cannot self-update this installation") || !strings.Contains(stderr, "Executable:") {
		t.Fatalf("stderr missing unknown-provenance remediation:\n%s", stderr)
	}
}

// TestAC5ImmutableBinaryPathRefusesMutation proves a baked Piglet/Piglet
// Binary refuses in-place mutation and emits the immutable remediation, never
// starting a download.
func TestAC5ImmutableBinaryPathRefusesMutation(t *testing.T) {
	orig := codingagent.PigletBinaryRelease
	t.Cleanup(func() { codingagent.PigletBinaryRelease = orig })
	codingagent.PigletBinaryRelease = "9.9.9"
	// Like upstream, the release is checked first, so a current binary exits 0
	// (TestSelfUpdateChecksTheVersionBeforeRefusingAnInstallation). A newer
	// release reaches the immutable tier, which refuses without downloading.
	downloads := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the immutable binary started a download")
	}))
	defer downloads.Close()
	manifest := `{"version":"99.0.0","packageName":"pig","binaries":{"` + codingagent.PlatformKey() + `":{"url":"` + downloads.URL + `","sha256":"` + strings.Repeat("0", 64) + `"}}}`
	srv := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(manifest))
	}))
	defer srv.Close()
	t.Setenv("PIG_UPDATE_URL", srv.URL)
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	if code != 1 {
		t.Fatalf("code = %d, want 1 for immutable binary", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "immutable") || !strings.Contains(stderr, "cannot be updated in place") {
		t.Fatalf("stderr missing immutable remediation:\n%s", stderr)
	}
}

// TestAC5ContainerPathRefusesMutation proves a container/image install emits
// the authenticated pull/redeploy remediation and never rewrites the running
// image.
func TestAC5ContainerPathRefusesMutation(t *testing.T) {
	t.Setenv("PIG_INSTALL_TIER", "container")
	t.Setenv("PIG_IMAGE_REF", "registry.example/pig:1.0.0")
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	if code != 1 {
		t.Fatalf("code = %d, want 1 for container", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "registry.example/pig:1.0.0") || !strings.Contains(stderr, "re-deploy") {
		t.Fatalf("stderr missing container remediation:\n%s", stderr)
	}
}

// TestAC3StandaloneUpdateVerificationAndAtomicity proves the standalone caller
// downloads, verifies the SHA256, and atomically replaces a target; it uses a
// temporary target rather than overwriting the test runner executable.
func TestAC3StandaloneUpdateVerificationAndAtomicity(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	exe := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("#!/bin/sh\necho updated\n")
	manSrv, binSrv := newerManifest(t, payload)
	defer manSrv.Close()
	defer binSrv.Close()
	seedRunningStandaloneReceipt(t, manSrv.URL)

	if err := applyStandaloneUpdate(exe, false); err != nil {
		t.Fatalf("applyStandaloneUpdate: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("executable not replaced atomically: %q (err=%v)", got, err)
	}
}

func TestAC3PrivateCATransportUpdatesWithoutTLSOverride(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trustDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "update-trust.pem"), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: trustDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	payload := []byte("private-ca-update")
	sum := sha256.Sum256(payload)
	var serverURL string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pig" {
			_, _ = w.Write(payload)
			return
		}
		manifest := []byte(`{"version":"9.9.9","packageName":"pig","binaries":{"` + codingagent.PlatformKey() + `":{"url":"` + serverURL + `/pig","sha256":"` + hex.EncodeToString(sum[:]) + `"}}}`)
		w.Header().Set(codingagent.UpdateSignatureHeader, base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifest)))
		_, _ = w.Write(manifest)
	}))
	defer srv.Close()
	serverURL = srv.URL
	if err := os.WriteFile(filepath.Join(home, "update-ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_UPDATE_URL", srv.URL+"/manifest")
	originalVersion := codingagent.InstalledPigVersion
	t.Cleanup(func() { codingagent.InstalledPigVersion = originalVersion })
	codingagent.InstalledPigVersion = PigVersion
	if err := codingagent.WriteStandaloneReceipt(exe, PigVersion, srv.URL+"/manifest"); err != nil {
		t.Fatal(err)
	}
	if err := applyStandaloneUpdate(exe, false); err != nil {
		t.Fatalf("private-CA standalone update: %v", err)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != string(payload) {
		t.Fatalf("updated executable = %q (err=%v)", got, err)
	}
}

func TestAC3StandaloneReceiptFailureRestoresPreviousExecutable(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	exe := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("new")
	manifestSrv, binarySrv := newerManifest(t, payload)
	defer manifestSrv.Close()
	defer binarySrv.Close()
	blockedHome := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedHome, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", blockedHome)
	t.Setenv("PIG_UPDATE_URL", manifestSrv.URL)
	err := applyStandaloneUpdate(exe, false)
	if err == nil || !strings.Contains(err.Error(), "previous executable restored") {
		t.Fatalf("receipt failure error = %v", err)
	}
	got, readErr := os.ReadFile(exe)
	if readErr != nil || string(got) != "old" {
		t.Fatalf("previous executable was not restored: %q (err=%v)", got, readErr)
	}
}

func TestAC11ExactReleasePlanUsesSignedReplacementPackage(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "manager-args")
	manager := filepath.Join(t.TempDir(), "npm")
	if runtime.GOOS == "windows" {
		manager += ".exe"
	}
	copyTestBinary(t, manager)
	t.Setenv(managerLogEnv, logPath)
	manifest := `{"version":"9.9.9","packageName":"pig-next","binaries":{}}`
	srv := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(manifest))
	}))
	defer srv.Close()
	t.Setenv("PIG_UPDATE_URL", srv.URL)
	prov := &codingagent.SelfUpdateProvenance{
		Tier:         codingagent.TierPackageManager,
		ExePath:      filepath.Join(t.TempDir(), "pig"),
		PackageOwner: codingagent.PackageManagerOwner("npm"),
		PackageName:  "pig",
	}
	if err := applyPackageManagerUpdate(prov, []string{manager}, false); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "pig-next@9.9.9") || !strings.Contains(string(args), "uninstall -g pig") {
		t.Fatalf("package-manager plan did not preserve signed rename:\n%s", args)
	}
}

func TestAC11ForceReinstallsCurrentStandaloneRelease(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	exe := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("current-release-reinstalled")
	sum := sha256.Sum256(payload)
	binSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer binSrv.Close()
	manifest := `{"version":"` + PigVersion + `","packageName":"pig","binaries":{"` + codingagent.PlatformKey() + `":{"url":"` + binSrv.URL + `","sha256":"` + hex.EncodeToString(sum[:]) + `"}}}`
	manifestSrv := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(manifest))
	}))
	defer manifestSrv.Close()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_UPDATE_URL", manifestSrv.URL)
	if err := applyStandaloneUpdate(exe, false); !errors.Is(err, errUpdateNotNeeded) {
		t.Fatalf("non-forced current release error = %v", err)
	}
	if err := applyStandaloneUpdate(exe, true); err != nil {
		t.Fatalf("forced current release: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("forced current release did not replace target: %q (err=%v)", got, err)
	}
}

// TestAC7NoFallbackAfterStandaloneStarts proves that once the standalone tier
// starts and fails (unreachable source), the failure surfaces from the
// standalone tier and the update does not silently succeed via another tier.
// The informational fallback ladder may mention download/container steps; that
// is next-step guidance, not a tier fallthrough. Mutate the dispatcher to
// retry another mutating tier after standalone failure (and succeed) and this
// test goes red.
func TestAC7NoFallbackAfterStandaloneStarts(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	// A source is configured but unreachable: the standalone tier starts (it
	// resolves a writable exe and a configured source) then fails on fetch.
	seedRunningStandaloneReceipt(t, "http://127.0.0.1:1/no-such-server")
	t.Setenv("PIG_INSTALL_TIER", "")
	_, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	if code != 1 {
		t.Fatalf("code = %d, want 1: standalone failure must not succeed via another tier; stderr=%q", code, stderr)
	}
	// The failure is attributed to the standalone tier's source fetch, proving
	// the standalone tier owned the failure rather than being skipped.
	if !strings.Contains(stderr, "could not reach the update source") {
		t.Fatalf("stderr missing standalone-tier failure attribution:\n%s", stderr)
	}
}

// TestAC1UpdateRoutingMatchesPi lives in package_commands_test.go (the spec's
// named target) and covers the full routing matrix plus help. The
// isSelfUpdateTarget assertion is exercised there too.

// TestAC71SelfUpdateSelectsOneProvenTier is the integration acceptance test:
// across standalone, immutable, container, and unsupported provenance, exactly
// one tier is selected and mutation is refused for non-mutable tiers.
func TestAC71SelfUpdateSelectsOneProvenTier(t *testing.T) {
	t.Run("standalone", func(t *testing.T) {
		requireStandaloneSelfUpdateTier(t)
		t.Setenv("PIG_INSTALL_TIER", "")
		seedRunningStandaloneReceipt(t, "https://updates.example/manifest")
		prov, err := codingagent.ResolveSelfUpdateTier()
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if prov.Tier != codingagent.TierStandalone {
			t.Fatalf("tier = %s, want standalone", prov.Tier)
		}
	})
	t.Run("immutable", func(t *testing.T) {
		orig := codingagent.PigletBinaryRelease
		t.Cleanup(func() { codingagent.PigletBinaryRelease = orig })
		codingagent.PigletBinaryRelease = "1.0.0"
		prov, err := codingagent.ResolveSelfUpdateTier()
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if prov.Tier != codingagent.TierImmutableBinary {
			t.Fatalf("tier = %s, want immutable-binary", prov.Tier)
		}
	})
	t.Run("container", func(t *testing.T) {
		t.Setenv("PIG_INSTALL_TIER", "container")
		prov, err := codingagent.ResolveSelfUpdateTier()
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if prov.Tier != codingagent.TierContainer {
			t.Fatalf("tier = %s, want container", prov.Tier)
		}
	})
	t.Run("ambiguous_is_rejected", func(t *testing.T) {
		t.Setenv("PIG_INSTALL_TIER", "bogus")
		if _, err := codingagent.ResolveSelfUpdateTier(); err == nil {
			t.Fatal("ambiguous/unknown ownership resolved without error")
		}
	})
}

// Upstream getSelfUpdatePlan runs the update when the release names another
// package, whatever its version (packageName !== PACKAGE_NAME), so a release
// that renames the package at the current version still installs it. Both
// version checks honor that: the one before the installation tier and the
// one in the package-manager tier, which then replaces pig with the new
// package.
func TestSelfUpdatePlansASameVersionPackageRename(t *testing.T) {
	ver := selfUpdateVersion()
	manifest := func(name string) *httptest.Server {
		body := `{"version":"` + ver + `","packageName":"` + name + `","binaries":{"` + codingagent.PlatformKey() + `":{"url":"u","sha256":"s"}}}`
		return signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
	}
	same := manifest(codingagent.PackageName)
	defer same.Close()
	t.Setenv("PIG_UPDATE_URL", same.URL)
	if _, stderr, code := captureStdoutStderr(t, func() int {
		code, done := checkSelfUpdateVersion(false)
		if !done {
			return -1
		}
		return code
	}); code != 0 || !strings.Contains(stderr, "up to date") {
		t.Fatalf("same package and version: code = %d, want 0 and up to date; stderr=%q", code, stderr)
	}

	renamed := manifest("pig-next")
	defer renamed.Close()
	t.Setenv("PIG_UPDATE_URL", renamed.URL)
	if code, done := checkSelfUpdateVersion(false); done {
		t.Fatalf("same-version package rename short-circuited: code=%d done=%t; upstream plans the rename", code, done)
	}
	log := filepath.Join(t.TempDir(), "manager.log")
	t.Setenv(managerLogEnv, log)
	prov := &codingagent.SelfUpdateProvenance{PackageOwner: "npm", PackageName: codingagent.PackageName, ExePath: filepath.Join(t.TempDir(), "pig")}
	var err error
	captureStdoutStderr(t, func() int {
		err = applyPackageManagerUpdate(prov, []string{os.Args[0]}, false)
		return 0
	})
	if err != nil {
		t.Fatalf("package-manager tier: %v, want the rename installed", err)
	}
	data, readErr := os.ReadFile(log)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if want := "uninstall -g pig\ninstall -g --ignore-scripts --min-release-age=0 pig-next@" + ver + "\n"; string(data) != want {
		t.Fatalf("package manager ran %q, want %q", data, want)
	}
}

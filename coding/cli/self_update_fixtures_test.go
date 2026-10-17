package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
	body := `{"version":"` + ver + `","packageName":"@pi-in-go/pig","binaries":{"` + codingagent.PlatformKey() + `":{"url":"u","sha256":"s"}}}`
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
	manifest := `{"version":"9.9.9","packageName":"@pi-in-go/pig","binaries":{"` + codingagent.PlatformKey() + `":{"url":"` + binSrv.URL + `","sha256":"` + hex.EncodeToString(sum[:]) + `"}}}`
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

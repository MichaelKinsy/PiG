package codingagent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests cover the path script installs update through: a manifest
// published as a static release asset with its signature beside it, a trust
// root built into release binaries, and the release .tar.gz archive as the
// update payload.

type tarEntry struct {
	name     string
	typeflag byte
	body     string
}

func tarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		header := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0o755, Size: int64(len(e.body))}
		if e.typeflag == tar.TypeSymlink {
			header.Linkname, header.Size = "/bin/sh", 0
		}
		if e.typeflag == tar.TypeDir {
			header.Size = 0
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSelfReplaceAtInstallsPigFromReleaseArchive(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	allowLoopbackUpdateHTTP(t)
	newPig := "#!/bin/sh\necho 9.9.9\n"
	archive := tarGz(t,
		tarEntry{"pig-9.9.9-test/", tar.TypeDir, ""},
		tarEntry{"pig-9.9.9-test/LICENSE", tar.TypeReg, "MIT"},
		tarEntry{"pig-9.9.9-test/pig", tar.TypeReg, newPig},
	)
	sum := sha256.Sum256(archive)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	defer srv.Close()
	exe := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	bin := UpdateBinary{URL: srv.URL + "/pig-9.9.9-test.tar.gz", SHA256: strings.Repeat("0", 64)}
	if err := SelfReplaceAt(context.Background(), srv.Client(), bin, exe); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("archive with the wrong digest: error = %v", err)
	}
	bin.SHA256 = hex.EncodeToString(sum[:])
	if err := SelfReplaceAt(context.Background(), srv.Client(), bin, exe); err != nil {
		t.Fatalf("SelfReplaceAt from archive: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != newPig {
		t.Fatalf("executable = %q, %v; want the archive's pig", got, err)
	}
	if info, _ := os.Stat(exe); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".pig-update-*"))
	if len(leftovers) != 0 {
		t.Fatalf("staging files left behind: %v", leftovers)
	}
}

func TestExtractPigFromTarGzRefusesAnythingButOnePigFile(t *testing.T) {
	for name, entries := range map[string][]tarEntry{
		"missing":   {{"pig-1/LICENSE", tar.TypeReg, "MIT"}},
		"symlink":   {{"pig-1/pig", tar.TypeSymlink, ""}},
		"two pigs":  {{"a/pig", tar.TypeReg, "1"}, {"b/pig", tar.TypeReg, "2"}},
		"too deep":  {{"a/b/pig", tar.TypeReg, "1"}},
		"directory": {{"pig-1/pig/", tar.TypeDir, ""}},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			archive := filepath.Join(dir, "a.tar.gz")
			if err := os.WriteFile(archive, tarGz(t, entries...), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := extractPigFromTarGz(archive, dir, maxUpdateBinaryBytes); err == nil {
				t.Fatalf("extracted %s from an archive that should be refused", out)
			}
			matches, _ := filepath.Glob(filepath.Join(dir, ".pig-update-*"))
			if len(matches) != 0 {
				t.Fatalf("refused extraction left %v", matches)
			}
		})
	}
}

func TestFetchUpdateManifestVerifiesADetachedSignature(t *testing.T) {
	allowLoopbackUpdateHTTP(t)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trustPath := filepath.Join(t.TempDir(), "update-trust.pem")
	if err := os.WriteFile(trustPath, pemPublicKey(t, public), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_UPDATE_TRUST_ROOT", trustPath)
	body := []byte(`{"version":"9.9.9","packageName":"pig","binaries":{}}`)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(private, body))
	for name, sig := range map[string]string{
		"valid":   signature,
		"forged":  base64.StdEncoding.EncodeToString(ed25519.Sign(private, []byte("other"))),
		"missing": "",
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/update.json":
					_, _ = w.Write(body)
				case "/update.json.sig":
					if sig == "" {
						http.NotFound(w, r)
						return
					}
					_, _ = fmt.Fprintln(w, sig)
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			manifest, err := FetchUpdateManifest(context.Background(), srv.Client(), srv.URL+"/update.json")
			if name == "valid" {
				if err != nil || manifest.Version != "9.9.9" {
					t.Fatalf("manifest = %#v, err = %v", manifest, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("accepted a manifest with a %s detached signature", name)
			}
		})
	}
}

func TestBuiltInTrustRootAcceptsBase64EncodedPEM(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_UPDATE_TRUST_ROOT", "")
	t.Setenv("PIG_HOME", t.TempDir())
	original := DefaultUpdateTrustRoot
	t.Cleanup(func() { DefaultUpdateTrustRoot = original })
	DefaultUpdateTrustRoot = base64.StdEncoding.EncodeToString(pemPublicKey(t, public))
	keys, err := updateTrustRoots()
	if err != nil || len(keys) != 1 || !keys[0].Equal(public) {
		t.Fatalf("keys = %v, err = %v", keys, err)
	}
	DefaultUpdateTrustRoot = "not base64!"
	if _, err := updateTrustRoots(); err == nil {
		t.Fatal("accepted an undecodable built-in trust root")
	}
}

// docs/site/public/install.sh writes this receipt after a verified install;
// keep the two in step (tests/ci-images covers the installer side).
func TestInstallerReceiptFormatValidates(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	source := "https://github.com/MichaelKinsy/PiG/releases/latest/download/update.json"
	t.Setenv("PIG_UPDATE_URL", source)
	original := InstalledPigVersion
	t.Cleanup(func() { InstalledPigVersion = original })
	InstalledPigVersion = "9.9.9"
	exe := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(exe, []byte("pig binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("pig binary"))
	receipt := fmt.Sprintf("kind=standalone\nexecutable=%s\npig-version=%s\nsha256=%s\nupdate-source=%s\n",
		resolved, "9.9.9", hex.EncodeToString(sum[:]), source)
	if err := os.WriteFile(filepath.Join(home, "install-receipt"), []byte(receipt), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateStandaloneReceipt(exe); err != nil {
		t.Fatalf("installer-format receipt does not validate: %v", err)
	}
}

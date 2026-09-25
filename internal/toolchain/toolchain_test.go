package toolchain

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		mode := int64(0o644)
		if strings.HasSuffix(name, "/go") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
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

// goSite serves an index in the go.dev ?mode=json shape plus one linux/amd64
// archive.
func goSite(t *testing.T, archive []byte, publishedSHA string) *httptest.Server {
	t.Helper()
	return goSiteFor(t, "go1.99.1.linux-amd64.tar.gz", "linux", archive, publishedSHA)
}

// goSiteFor serves an index listing one goos/amd64 archive named filename.
func goSiteFor(t *testing.T, filename, goos string, archive []byte, publishedSHA string) *httptest.Server {
	t.Helper()
	if publishedSHA == "" {
		sum := sha256.Sum256(archive)
		publishedSHA = hex.EncodeToString(sum[:])
	}
	index := []goRelease{{Version: "go1.99.1", Files: []goFile{{
		Filename: filename, OS: goos, Arch: "amd64", Version: "go1.99.1",
		SHA256: publishedSHA, Size: int64(len(archive)), Kind: "archive",
	}}}}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/" && r.URL.Query().Get("mode") == "json":
			_ = json.NewEncoder(w).Encode(index)
		case r.URL.Path == "/"+filename:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestInstallGoVerifiesAndReplacesTheManagedToolchain(t *testing.T) {
	archive := tarGz(t, map[string]string{"go/bin/go": "#!/bin/sh\necho go\n", "go/VERSION": "go1.99.1\n"})
	site := goSite(t, archive, "")
	defer site.Close()
	root := t.TempDir()
	stale := filepath.Join(ManagedGoRoot(root), "stale")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}

	goCommand, err := InstallGo(context.Background(), site.Client(), site.URL+"/", "go1.99.1", "linux", "amd64", root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(ManagedGoRoot(root), "bin", "go"); goCommand != want {
		t.Fatalf("go command = %q, want %q", goCommand, want)
	}
	if version, err := os.ReadFile(filepath.Join(ManagedGoRoot(root), "VERSION")); err != nil || string(version) != "go1.99.1\n" {
		t.Fatalf("VERSION = %q, %v", version, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("the previous managed toolchain was not replaced")
	}
}

// The Windows release is a zip whose go command is go/bin/go.exe; the go
// command InstallGo returns follows the archive's goos, not the host's.
func TestInstallGoInstallsAWindowsZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{"go/bin/go.exe": "MZ", "go/VERSION": "go1.99.1\n"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	site := goSiteFor(t, "go1.99.1.windows-amd64.zip", "windows", buf.Bytes(), "")
	defer site.Close()
	root := t.TempDir()
	goCommand, err := InstallGo(context.Background(), site.Client(), site.URL+"/", "go1.99.1", "windows", "amd64", root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(ManagedGoRoot(root), "bin", "go.exe"); goCommand != want {
		t.Fatalf("go command = %q, want %q", goCommand, want)
	}
	if data, err := os.ReadFile(goCommand); err != nil || string(data) != "MZ" {
		t.Fatalf("installed go.exe = %q, %v", data, err)
	}
}

func TestInstallGoRejectsAChecksumMismatch(t *testing.T) {
	archive := tarGz(t, map[string]string{"go/bin/go": "tampered"})
	site := goSite(t, archive, strings.Repeat("0", 64))
	defer site.Close()
	root := t.TempDir()
	_, err := InstallGo(context.Background(), site.Client(), site.URL+"/", "go1.99.1", "linux", "amd64", root)
	if err == nil || !strings.Contains(err.Error(), "does not match the published") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	if _, statErr := os.Stat(ManagedGoRoot(root)); !os.IsNotExist(statErr) {
		t.Fatal("a rejected download left a managed toolchain behind")
	}
}

func TestInstallGoRejectsEntriesOutsideTheDestination(t *testing.T) {
	archive := tarGz(t, map[string]string{"go/bin/go": "ok", "../escape": "x"})
	site := goSite(t, archive, "")
	defer site.Close()
	root := t.TempDir()
	_, err := InstallGo(context.Background(), site.Client(), site.URL+"/", "go1.99.1", "linux", "amd64", root)
	if err == nil || !strings.Contains(err.Error(), "escapes the destination") {
		t.Fatalf("err = %v, want traversal rejection", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "toolchains", "escape")); !os.IsNotExist(statErr) {
		t.Fatal("a traversal entry was written")
	}
}

func TestInstallGoRejectsUnknownVersionsAndTargets(t *testing.T) {
	site := goSite(t, tarGz(t, map[string]string{"go/bin/go": "ok"}), "")
	defer site.Close()
	for _, tc := range []struct{ version, goos, goarch, want string }{
		{"go1.0.0", "linux", "amd64", "is not in the official index"},
		{"go1.99.1", "plan9", "arm", "publishes no archive for plan9/arm"},
		{"1.99.1", "linux", "amd64", "invalid Go version"},
	} {
		_, err := InstallGo(context.Background(), site.Client(), site.URL+"/", tc.version, tc.goos, tc.goarch, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s %s/%s: err = %v, want %q", tc.version, tc.goos, tc.goarch, err, tc.want)
		}
	}
}

func TestGoFallsBackToTheManagedToolchain(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	t.Setenv("PATH", t.TempDir())
	if _, err := Go(); err == nil || !strings.Contains(err.Error(), "pig setup go") {
		t.Fatalf("err = %v, want the setup hint", err)
	}
	managed := filepath.Join(ManagedGoRoot(root), "bin", exeName("go"))
	if err := os.MkdirAll(filepath.Dir(managed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Go()
	if err != nil || got != managed {
		t.Fatalf("Go() = %q, %v; want %q", got, err, managed)
	}
	if runtime.GOOS == "windows" {
		return
	}
	pathGo := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(pathGo, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(pathGo))
	if got, err := Go(); err != nil || got != pathGo {
		t.Fatalf("Go() with go on PATH = %q, %v; want %q", got, err, pathGo)
	}
}

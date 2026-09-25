package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestArchStr(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"arm64", "aarch64"},
		{"aarch64", "aarch64"},
		{"amd64", "x86_64"},
		{"x64", "x86_64"},
		{"x86_64", "x86_64"},
		{"riscv64", "riscv64"}, // unknown passes through
	}
	for _, tt := range tests {
		if got := archStr(tt.in); got != tt.want {
			t.Errorf("archStr(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestToolsTable_AssetNames(t *testing.T) {
	cases := []struct {
		tool, plat, arch, want string
	}{
		// fd
		{"fd", "darwin", "arm64", "fd-v9.0.0-aarch64-apple-darwin.tar.gz"},
		{"fd", "darwin", "x64", "fd-v9.0.0-x86_64-apple-darwin.tar.gz"},
		{"fd", "linux", "arm64", "fd-v9.0.0-aarch64-unknown-linux-gnu.tar.gz"},
		{"fd", "linux", "x64", "fd-v9.0.0-x86_64-unknown-linux-gnu.tar.gz"},
		{"fd", "win32", "x64", "fd-v9.0.0-x86_64-pc-windows-msvc.zip"},
		{"fd", "win32", "arm64", "fd-v9.0.0-aarch64-pc-windows-msvc.zip"},
		// rg
		{"rg", "darwin", "arm64", "ripgrep-14.0.0-aarch64-apple-darwin.tar.gz"},
		{"rg", "linux", "x64", "ripgrep-14.0.0-x86_64-unknown-linux-musl.tar.gz"},
		{"rg", "linux", "arm64", "ripgrep-14.0.0-aarch64-unknown-linux-gnu.tar.gz"},
		{"rg", "win32", "x64", "ripgrep-14.0.0-x86_64-pc-windows-msvc.zip"},
	}
	versions := map[string]string{"fd": "9.0.0", "rg": "14.0.0"}
	for _, c := range cases {
		cfg := toolsTable[c.tool]
		got := cfg.GetAssetName(versions[c.tool], c.plat, c.arch)
		if got != c.want {
			t.Errorf("%s/%s/%s: got %q, want %q", c.tool, c.plat, c.arch, got, c.want)
		}
	}
	// Unsupported platform returns "".
	if got := toolsTable["fd"].GetAssetName("9.0.0", "freebsd", "x64"); got != "" {
		t.Errorf("fd/freebsd: got %q, want empty", got)
	}
}

func TestIsOfflineModeEnabled(t *testing.T) {
	for _, key := range []string{"PIG_OFFLINE", "PI_OFFLINE"} {
		t.Setenv("PIG_OFFLINE", "")
		t.Setenv("PI_OFFLINE", "")
		t.Setenv(key, "1")
		if !IsOfflineModeEnabled() {
			t.Errorf("%s=1 should enable offline mode", key)
		}
		t.Setenv(key, "true")
		if !IsOfflineModeEnabled() {
			t.Errorf("%s=true should enable offline mode", key)
		}
		t.Setenv(key, "no")
		if IsOfflineModeEnabled() {
			t.Errorf("%s=no should not enable offline mode", key)
		}
		t.Setenv(key, "")
	}
}

func TestLookupToolPath_LocalBinDirWinsOverPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH semantics differ on Windows")
	}
	dir := t.TempDir()
	local := filepath.Join(dir, "fd")
	if err := os.WriteFile(local, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Even when fd is on PATH (it is, in CI), the local copy is preferred.
	got := LookupToolPath("fd", dir)
	if got != local {
		t.Errorf("LookupToolPath(fd, %q) = %q, want %q", dir, got, local)
	}
	// With empty bin dir, must NOT pick up our local file.
	got = LookupToolPath("fd", "")
	if got == local {
		t.Errorf("LookupToolPath(fd, \"\") wrongly returned the local copy %q", got)
	}
}

func TestLookupToolPath_UnknownTool(t *testing.T) {
	if got := LookupToolPath("not-a-real-tool", t.TempDir()); got != "" {
		t.Errorf("LookupToolPath(unknown) = %q, want \"\"", got)
	}
}

// fakeReleaseServer simulates the GitHub API + asset CDN for one tool/version.
type fakeReleaseServer struct {
	*httptest.Server
	tagName    string
	assetName  string
	assetBytes []byte
	apiHits    int
	dlHits     int
}

func newFakeReleaseServer(t *testing.T, repo, tagName, assetName string, asset []byte) *fakeReleaseServer {
	t.Helper()
	f := &fakeReleaseServer{tagName: tagName, assetName: assetName, assetBytes: asset}
	mux := http.NewServeMux()
	mux.HandleFunc(fmt.Sprintf("/%s/releases/latest", repo), func(w http.ResponseWriter, r *http.Request) {
		f.apiHits++
		http.Redirect(w, r, fmt.Sprintf("/%s/releases/tag/%s", repo, tagName), http.StatusFound)
	})
	mux.HandleFunc(fmt.Sprintf("/%s/releases/download/", repo), func(w http.ResponseWriter, r *http.Request) {
		f.dlHits++
		if !strings.HasSuffix(r.URL.Path, "/"+assetName) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(asset)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// buildTarGz produces an in-memory .tar.gz containing one file at relPath
// with the given bytes.
func buildTarGz(t *testing.T, relPath string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     relPath,
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, relPath string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(relPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makeTestManager builds a ToolsManager pointed at a fake registry.
func makeTestManager(t *testing.T, srv *fakeReleaseServer, plat, arch string) *ToolsManager {
	t.Helper()
	dir := t.TempDir()
	tm := NewToolsManager(dir)
	tm.releaseBaseURL = srv.URL
	tm.platformOverride = plat
	tm.archOverride = arch
	return tm
}

func TestDownloadTool_TarGz_NestedBinary(t *testing.T) {
	// Mirrors the canonical fd layout: binary nested under
	// fd-v9.0.0-aarch64-apple-darwin/fd.
	asset := buildTarGz(t, "fd-v9.0.0-aarch64-apple-darwin/fd", []byte("#!/bin/sh\necho fd\n"))
	srv := newFakeReleaseServer(t, "sharkdp/fd", "v9.0.0", "fd-v9.0.0-aarch64-apple-darwin.tar.gz", asset)
	tm := makeTestManager(t, srv, "darwin", "arm64")

	// Use downloadTool directly to bypass PATH short-circuit in GetToolPath.
	path, err := tm.downloadTool(context.Background(), "fd")
	if err != nil {
		t.Fatalf("downloadTool: %v", err)
	}
	want := filepath.Join(tm.BinDir(), "fd")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("binary not at expected location: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(want)
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("binary not marked executable: %v", info.Mode())
		}
	}
	// Archive cleaned up.
	if _, err := os.Stat(filepath.Join(tm.BinDir(), "fd-v9.0.0-aarch64-apple-darwin.tar.gz")); err == nil {
		t.Errorf("archive not removed after extraction")
	}
	// After install, GetToolPath should now return the local copy.
	if got := tm.GetToolPath("fd"); got != want {
		t.Errorf("GetToolPath after install = %q, want %q", got, want)
	}
}

func TestDownloadTool_TarGz_FlatBinary(t *testing.T) {
	// Some archives put the binary at root, not under a nested dir.
	asset := buildTarGz(t, "rg", []byte("#!/bin/sh\necho rg\n"))
	srv := newFakeReleaseServer(t, "BurntSushi/ripgrep", "14.0.0", "ripgrep-14.0.0-aarch64-apple-darwin.tar.gz", asset)
	tm := makeTestManager(t, srv, "darwin", "arm64")

	path, err := tm.downloadTool(context.Background(), "rg")
	if err != nil {
		t.Fatalf("downloadTool: %v", err)
	}
	want := filepath.Join(tm.BinDir(), "rg")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestDownloadTool_Zip(t *testing.T) {
	asset := buildZip(t, "fd-v9.0.0-x86_64-pc-windows-msvc/fd.exe", []byte("MZ\x00"))
	srv := newFakeReleaseServer(t, "sharkdp/fd", "v9.0.0", "fd-v9.0.0-x86_64-pc-windows-msvc.zip", asset)
	tm := makeTestManager(t, srv, "win32", "x64")

	path, err := tm.downloadTool(context.Background(), "fd")
	if err != nil {
		t.Fatalf("downloadTool: %v", err)
	}
	want := filepath.Join(tm.BinDir(), "fd.exe")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func TestDownloadTool_DeeplyNestedBinary(t *testing.T) {
	asset := buildTarGz(t, "weird/sub/dir/fd", []byte("#!/bin/sh\necho deep\n"))
	srv := newFakeReleaseServer(t, "sharkdp/fd", "v9.0.0", "fd-v9.0.0-aarch64-apple-darwin.tar.gz", asset)
	tm := makeTestManager(t, srv, "darwin", "arm64")

	path, err := tm.downloadTool(context.Background(), "fd")
	if err != nil {
		t.Fatalf("downloadTool: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("binary not installed: %v", err)
	}
}

func TestEnsureTool_PathShortCircuit(t *testing.T) {
	// Verify that when the tool is already on PATH (under <agentDir>/bin),
	// EnsureTool returns it without calling the registry.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected hit on registry: %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	dir := t.TempDir()
	tm := NewToolsManager(dir)
	tm.releaseBaseURL = srv.URL
	tm.platformOverride = "darwin"
	tm.archOverride = "arm64"

	// Stage a fake fd inside the bin dir; it should be found by GetToolPath.
	if err := os.MkdirAll(tm.BinDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(tm.BinDir(), "fd")
	if err := os.WriteFile(local, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := tm.EnsureTool(context.Background(), "fd", nil)
	if got != local {
		t.Errorf("EnsureTool = %q, want %q (should short-circuit on local install)", got, local)
	}
}

func TestDownloadTool_GitHubAPIFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	dir := t.TempDir()
	tm := NewToolsManager(dir)
	tm.releaseBaseURL = srv.URL
	tm.platformOverride = "darwin"
	tm.archOverride = "arm64"

	_, err := tm.downloadTool(context.Background(), "fd")
	if err == nil {
		t.Errorf("downloadTool should return error on API failure")
	}
}

func TestDownloadTool_OfflineMode(t *testing.T) {
	t.Setenv("PIG_OFFLINE", "1")
	t.Setenv("PATH", t.TempDir()) // hide system fd so GetToolPath returns ""
	tm := NewToolsManager(t.TempDir())
	tm.platformOverride = "darwin"
	tm.archOverride = "arm64"
	if got := tm.EnsureTool(context.Background(), "fd", nil); got != "" {
		t.Errorf("EnsureTool in offline mode should return empty, got %q", got)
	}
}

func TestDownloadTool_Android(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // hide system fd
	tm := NewToolsManager(t.TempDir())
	tm.platformOverride = "android"
	tm.archOverride = "arm64"
	if got := tm.EnsureTool(context.Background(), "fd", nil); got != "" {
		t.Errorf("EnsureTool on android should return empty, got %q", got)
	}
}

func TestDownloadTool_DarwinX64FdPin(t *testing.T) {
	// Upstream pins fd 10.3.0 for darwin-x64. Verify the asset URL uses
	// 10.3.0, not whatever GitHub reports as latest.
	var requestedAsset string
	mux := http.NewServeMux()
	mux.HandleFunc("/sharkdp/fd/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/sharkdp/fd/releases/tag/v999.0.0", http.StatusFound)
	})
	mux.HandleFunc("/sharkdp/fd/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		requestedAsset = filepath.Base(r.URL.Path)
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(buildTarGz(t, "fd-v10.3.0-x86_64-apple-darwin/fd", []byte("#!/bin/sh\n")))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tm := NewToolsManager(t.TempDir())
	tm.releaseBaseURL = srv.URL
	tm.platformOverride = "darwin"
	tm.archOverride = "x64"

	_, err := tm.downloadTool(context.Background(), "fd")
	if err != nil {
		t.Fatalf("downloadTool: %v", err)
	}
	want := "fd-v10.3.0-x86_64-apple-darwin.tar.gz"
	if requestedAsset != want {
		t.Errorf("requested asset = %q, want %q (darwin/x64 should pin fd 10.3.0)", requestedAsset, want)
	}
}

func TestDownloadTool_ConcurrentDedup(t *testing.T) {
	// Stage a sentinel so GetToolPath returns the local path on subsequent
	// calls; this lets us hit the inflight dedup path on the n-1 racers.
	asset := buildTarGz(t, "fd-v9.0.0-aarch64-apple-darwin/fd", []byte("#!/bin/sh\n"))
	srv := newFakeReleaseServer(t, "sharkdp/fd", "v9.0.0", "fd-v9.0.0-aarch64-apple-darwin.tar.gz", asset)
	tm := makeTestManager(t, srv, "darwin", "arm64")

	// Reach into EnsureTool but bypass the PATH short-circuit by setting
	// PATH to an empty dir for this test's exec.LookPath calls.
	t.Setenv("PATH", t.TempDir())

	const n = 5
	results := make(chan string, n)
	for range n {
		go func() {
			results <- tm.EnsureTool(context.Background(), "fd", nil)
		}()
	}
	for range n {
		if got := <-results; got == "" {
			t.Errorf("concurrent EnsureTool returned empty")
		}
	}
	// Only one download should have happened despite n concurrent callers.
	if srv.dlHits != 1 {
		t.Errorf("expected 1 download, got %d", srv.dlHits)
	}
}

func TestFindBinaryRecursively(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(nested, "fd")
	if err := os.WriteFile(binary, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := findBinaryRecursively(dir, "fd")
	if got != binary {
		t.Errorf("found %q, want %q", got, binary)
	}
	if findBinaryRecursively(dir, "no-such-binary") != "" {
		t.Errorf("should return empty for missing binary")
	}
}

func TestExtractZip_PathTraversalRejected(t *testing.T) {
	asset := buildZip(t, "../../etc/evil", []byte("nope"))
	tmpArchive := filepath.Join(t.TempDir(), "evil.zip")
	if err := os.WriteFile(tmpArchive, asset, 0o644); err != nil {
		t.Fatal(err)
	}
	extractDir := t.TempDir()
	err := extractZip(tmpArchive, extractDir)
	if err == nil {
		t.Fatal("expected error for path-traversing zip entry")
	}
	if !strings.Contains(err.Error(), "escapes extract dir") {
		t.Errorf("error %q does not name the violation", err)
	}
}

func TestExtractTarGz_PathTraversalRejected(t *testing.T) {
	asset := buildTarGz(t, "../../etc/evil", []byte("nope"))
	tmpArchive := filepath.Join(t.TempDir(), "evil.tar.gz")
	if err := os.WriteFile(tmpArchive, asset, 0o644); err != nil {
		t.Fatal(err)
	}
	extractDir := t.TempDir()
	err := extractTarGz(tmpArchive, extractDir)
	if err == nil {
		t.Fatal("expected error for path-traversing tar entry")
	}
}

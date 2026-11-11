// SPDX-License-Identifier: MIT

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
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

func TestInstallArchive(t *testing.T) {
	archive := tarGz(t, map[string]string{"README.md": "docs", "dist/scanner": "#!/bin/sh\necho scanner\n"})
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	dir := t.TempDir()
	bin, cache := filepath.Join(dir, "bin"), filepath.Join(dir, "cache")
	good := config{Tools: []tool{{Name: "scanner", Version: "1", URL: server.URL + "/a.tar.gz", SHA256: sha256Hex(archive)}}}

	if err := install(context.Background(), good, dir, bin, cache, io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(bin, "scanner"))
	if err != nil || !strings.Contains(string(raw), "echo scanner") {
		t.Fatalf("installed %q, err %v", raw, err)
	}
	if info, _ := os.Stat(filepath.Join(bin, "scanner")); info.Mode().Perm()&0o100 == 0 {
		t.Fatal("the installed tool is not executable")
	}
	if err := install(context.Background(), good, dir, bin, cache, io.Discard); err != nil || served != 1 {
		t.Fatalf("a second install downloaded again (%d downloads), err %v", served, err)
	}

	wrong := config{Tools: []tool{{Name: "scanner", Version: "1", URL: server.URL + "/b.tar.gz", SHA256: strings.Repeat("0", 64)}}}
	if err := install(context.Background(), wrong, dir, filepath.Join(dir, "bin2"), cache, io.Discard); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("an archive with another SHA-256 was accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin2", "scanner")); err == nil {
		t.Fatal("a rejected archive left a tool behind")
	}

	missing := config{Tools: []tool{{Name: "other", Version: "1", URL: server.URL + "/a.tar.gz", SHA256: sha256Hex(archive)}}}
	if err := install(context.Background(), missing, dir, filepath.Join(dir, "bin3"), cache, io.Discard); err == nil || !strings.Contains(err.Error(), "holds no file other") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadDoesNotRetryAClientError(t *testing.T) {
	served := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served++
		http.NotFound(w, nil)
	}))
	defer server.Close()
	if _, err := download(context.Background(), server.URL, strings.Repeat("0", 64), t.TempDir()); err == nil || served != 1 {
		t.Fatalf("err %v after %d request(s); a 404 is the answer, not a hiccup", err, served)
	}
}

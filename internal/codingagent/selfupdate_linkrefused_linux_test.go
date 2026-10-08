//go:build linux

package codingagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Android refuses hard links in Termux's prefix, so the rollback backup is a copy there; under Android's common umask 077 the restored pig must keep its 0755 mode.
func TestSelfReplaceAtWithCommitRestoresPreviousExecutableWhereLinksAreRefused(t *testing.T) {
	requireStandaloneSelfUpdateTier(t)
	if !testenv.RunWithHardLinksRefused(t) {
		return
	}
	syscall.Umask(0o077)
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
	if err := os.Chmod(exe, 0o755); err != nil {
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
	if info, statErr := os.Stat(exe); statErr != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("restored executable mode = %v (err=%v), want 0755", info.Mode().Perm(), statErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(dir, ".pig-update-backup-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("rollback backups remain: %v (err=%v)", matches, globErr)
	}
}

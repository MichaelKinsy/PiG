//go:build unix

package ai

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// R22: a store path inside a folder without search permission is an error, not a path (the Lstat error is not a fallback).
func TestR22UnsearchableFolderIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory modes")
	}
	root := t.TempDir()
	folder := filepath.Join(root, "closed")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "auth.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(folder, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(folder, 0o700) })
	got, err := resolveAuthPath(filepath.Join(folder, "auth.json"))
	if err == nil {
		t.Fatalf("resolveAuthPath = %q, nil; want an error", got)
	}
	if got != "" {
		t.Fatalf("resolveAuthPath returned %q with the error", got)
	}
}

// R25: a store the owner made read-only (0400) refuses the write (EACCES, as Pi's writeFileSync does); its bytes stay and no temp file is left.
func TestR25ReadOnlyStoreRefusesWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file modes")
	}
	root := t.TempDir()
	target := filepath.Join(root, "auth.json")
	if err := os.WriteFile(target, []byte(`{"p":{"type":"api_key","key":"fixture-old"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewAuthStorage(target)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(target)
	if err := os.Chmod(target, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(target, 0o600) })
	err = s.Set("p", Credential{Type: CredentialAPIKey, Key: "fixture-new"})
	if err == nil || !strings.Contains(err.Error(), "EACCES") || !strings.Contains(err.Error(), target) {
		t.Fatalf("Set err = %v; want EACCES naming %s", err, target)
	}
	after, _ := os.ReadFile(target)
	if string(before) != string(after) {
		t.Fatal("read-only store changed")
	}
	noTemps(t, root)
}

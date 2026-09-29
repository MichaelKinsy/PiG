package subprocess

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNodeRuntimeMaterializesOncePerContentHash(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(t.TempDir(), "first")
	if err := materializeNodeRuntime(t.Context(), root, first); err != nil {
		t.Fatal(err)
	}
	savedZip, savedDigest := nodeRuntimeZip, nodeRuntimeDigest
	t.Cleanup(func() { nodeRuntimeZip = savedZip; nodeRuntimeDigest = savedDigest })
	unavailable := errors.New("archive should not be opened on a cache hit")
	nodeRuntimeZip = func() (*zip.Reader, error) { return nil, unavailable }
	second := filepath.Join(t.TempDir(), "second")
	if err := materializeNodeRuntime(t.Context(), root, second); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(first, "runtime.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(second, "runtime.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("pruning shared materialization damaged the launcher's owned tree")
	}
	nodeRuntimeZip = savedZip
	if err := materializeNodeRuntime(t.Context(), root, filepath.Join(t.TempDir(), "recreated")); err != nil {
		t.Fatal(err)
	}
	nodeRuntimeZip = func() (*zip.Reader, error) { return nil, unavailable }
	nodeRuntimeDigest = func() []byte { return []byte("changed runtime content") }
	if err := materializeNodeRuntime(t.Context(), root, filepath.Join(t.TempDir(), "changed")); !errors.Is(err, unavailable) {
		t.Fatalf("new runtime reused stale contents: %v", err)
	}
}

func TestNodeRuntimeCopyFallbackDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(source, []byte("runtime bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyNodeRuntimeFile(source, target); err != nil {
		t.Fatal(err)
	}
	if err := copyNodeRuntimeFile(source, target); !errors.Is(err, os.ErrExist) {
		t.Fatalf("fallback overwrote existing destination: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "runtime bytes" {
		t.Fatalf("copied bytes = %q", got)
	}
}

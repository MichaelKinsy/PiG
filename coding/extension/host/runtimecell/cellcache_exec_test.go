//go:build unix

package runtimecell

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A cell runner that the build wrote outside its scratch directory (a Rust build with CARGO_TARGET_DIR set) is copied into the entry, and the host executes the copy as soon as the entry is published. A fork while the copy is open leaves a child that holds the copy's write descriptor until it calls exec, and the host's exec of the runner then fails with "text file busy". The failure was seen as `fork/exec .../runner: text file busy` in TestModelTypesVirtualModelsAcrossSDKs/rust-isolated.
func TestPublishArtifactStartsNoChildWhileCopyingAnExternalArtifact(t *testing.T) {
	finalDir := filepath.Join(t.TempDir(), "cells", "rust", "external")
	var entry PublishedEntry
	payload, err := testenv.CopyFromFIFO(t, func(fifo string) (err error) {
		entry, err = PublishArtifact(context.Background(), finalDir, "runner", "external", "rust", func(string) (string, error) {
			return fifo, nil
		})
		return err
	}, func() {
		testenv.RequireForkLocked(t)
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := os.ReadFile(entry.ArtifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(published, payload) {
		t.Fatalf("published runner has %d bytes, want the %d the compiler wrote", len(published), len(payload))
	}
	if info, err := os.Stat(entry.ArtifactPath); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("published runner is not executable: %v, %v", info, err)
	}
}

// BenchmarkCopyArtifact measures staging a 16 MiB compiler output that sits outside the scratch directory.
func BenchmarkCopyArtifact(b *testing.B) {
	source := filepath.Join(b.TempDir(), "compiled")
	if err := os.WriteFile(source, make([]byte, 16<<20), 0o755); err != nil {
		b.Fatal(err)
	}
	destination := filepath.Join(b.TempDir(), "runner")
	b.ReportAllocs()
	b.SetBytes(16 << 20)
	for b.Loop() {
		if err := copyArtifact(source, destination); err != nil {
			b.Fatal(err)
		}
		if err := os.Remove(destination); err != nil {
			b.Fatal(err)
		}
	}
}

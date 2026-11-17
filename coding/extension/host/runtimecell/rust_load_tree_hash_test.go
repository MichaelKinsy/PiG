// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package runtimecell

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// rustCellFixture is a Rust extension in a folder of its own, with a Cargo manifest and nothing else: a cell whose image the cache holds.
func rustCellFixture(t *testing.T, name string) []RustExtension {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname = \""+name+"\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return []RustExtension{{Name: name + "-ext", Root: root, Package: name, Factory: "new_extension", Hash: "h-" + name}}
}

// writeRustSDKFixture stages a Rust SDK tree of the given number of source files under configRoot and points PIG_HOME at it.
func writeRustSDKFixture(tb testing.TB, configRoot string, sources int) string {
	tb.Helper()
	tb.Setenv("PIG_HOME", configRoot)
	tb.Setenv("PIG_SDK_RS_ROOT", "")
	sdkRoot, err := pigsdklock.StagedSDKDir(configRoot, "rust")
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sdkRoot, "src"), 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdkRoot, "Cargo.toml"), []byte("[package]\nname = \"pig-sdk\"\nversion = \"0.0.0\"\n"), 0o644); err != nil {
		tb.Fatal(err)
	}
	for i := range sources {
		name := filepath.Join(sdkRoot, "src", fmt.Sprintf("m%d.rs", i))
		if i == 0 {
			name = filepath.Join(sdkRoot, "src", "lib.rs")
		}
		if err := os.WriteFile(name, []byte(strings.Repeat("pub fn sdk() {}\n", 400)), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return sdkRoot
}

// The cells of one load look for their built images under keys made from one reading of the Rust SDK tree (WithLoadTreeHashes), as the cells of a Go
// load do: a source of the SDK that is edited after the load's first cell read the tree does not move the key the load's later cells look under.
// The images the cache holds were published under the key of the tree before the edit, so the later cell finds its image, and a Rust cell that
// reads the tree again for each cell does not.
func TestRustCellsOfOneLoadLookForTheirImagesUnderOneReadingOfTheSDK(t *testing.T) {
	configRoot := t.TempDir()
	sdkRoot := writeRustSDKFixture(t, configRoot, 1)
	cacheRoot := filepath.Join(configRoot, "cache")
	cells := map[string][]RustExtension{"cell-a": rustCellFixture(t, "packed_a"), "cell-b": rustCellFixture(t, "packed_b")}
	for key, extensions := range cells {
		normalized, err := normalizeRustExtensions(extensions, true)
		if err != nil {
			t.Fatal(err)
		}
		hash := rustPackedCellHash(cacheRoot, key, normalized, sdkRoot)
		publishTestCellEntry(t, filepath.Join(cacheRoot, "cells", "rust", hash), packedRunnerName(runtime.GOOS, "rust"))
	}
	// A cell that does not find its image goes on to build it with cargo; the bound keeps the failing case short.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	load := WithLoadTreeHashes(ctx)
	first, err := BuildRustPackedCell(load, cacheRoot, "cell-a", cells["cell-a"])
	if err != nil || !first.Cached {
		t.Fatalf("the first cell of the load: %+v, %v; want the image the cache holds", first, err)
	}
	source, err := os.OpenFile(filepath.Join(sdkRoot, "src", "lib.rs"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.WriteString("// edited while the load ran\n"); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := BuildRustPackedCell(load, cacheRoot, "cell-b", cells["cell-b"])
	if err != nil || !second.Cached {
		t.Fatalf("the second cell of the load, after the SDK was edited: %+v, %v; want the image the cache holds, found under the key of the load's reading of the SDK", second, err)
	}
}

// The key from the load's reading of the SDK tree is byte for byte the key from a reading of the cell's own (the one its build publishes under), for
// a cell of one extension and for one cell that packs two.
func TestRustLoadTreeHashGivesTheKeysOfAReadingPerCell(t *testing.T) {
	sdkRoot := writeRustSDKFixture(t, t.TempDir(), 1)
	cacheRoot := t.TempDir()
	one := rustCellFixture(t, "packed_one")
	two := append(rustCellFixture(t, "packed_two"), rustCellFixture(t, "packed_three")...)
	for name, extensions := range map[string][]RustExtension{"one extension": one, "one cell of two": two} {
		normalized, err := normalizeRustExtensions(extensions, true)
		if err != nil {
			t.Fatal(err)
		}
		load := WithLoadTreeHashes(t.Context())
		own := rustPackedCellHash(cacheRoot, "cell", normalized, sdkRoot)
		shared := rustPackedCellHashOfSDK(cacheRoot, "cell", normalized, loadTreeHash(load, sdkRoot))
		if strings.HasPrefix(own, "error:") || shared != own {
			t.Errorf("%s: key %q from the load's reading of the SDK, %q from a reading of its own", name, shared, own)
		}
	}
}

type rustLoadCell struct {
	key        string
	normalized []RustExtension
}

// publishedRustCells publishes the images of the given number of Rust cells under their keys for a staged SDK tree of 40 source files, as the
// startups before a load left them, and returns the cache root, the SDK root and the cells.
func publishedRustCells(tb testing.TB, cells int) (cacheRoot, sdkRoot string, all []rustLoadCell) {
	tb.Helper()
	sdkRoot = writeRustSDKFixture(tb, tb.TempDir(), 40)
	cacheRoot = tb.TempDir()
	for i := range cells {
		root := tb.TempDir()
		name := fmt.Sprintf("packed_%d", i)
		if err := os.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname = \""+name+"\"\nversion = \"0.1.0\"\n"), 0o644); err != nil {
			tb.Fatal(err)
		}
		normalized, err := normalizeRustExtensions([]RustExtension{{Name: name, Root: root, Package: name, Factory: "new_extension", Hash: name}}, true)
		if err != nil {
			tb.Fatal(err)
		}
		key := fmt.Sprintf("cell-%d", i)
		hash := rustPackedCellHash(cacheRoot, key, normalized, sdkRoot)
		artifact := packedRunnerName(runtime.GOOS, "rust")
		if _, err := PublishArtifact(context.Background(), filepath.Join(cacheRoot, "cells", "rust", hash), artifact, hash, "rust", func(scratch string) (string, error) {
			path := filepath.Join(scratch, artifact)
			return path, os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755)
		}); err != nil {
			tb.Fatal(err)
		}
		all = append(all, rustLoadCell{key, normalized})
	}
	return cacheRoot, sdkRoot, all
}

// lookUpRustCells is the lookup of a load for the images of its cells: every cell must be found.
func lookUpRustCells(tb testing.TB, ctx context.Context, cacheRoot string, cells []rustLoadCell) {
	tb.Helper()
	for _, c := range cells {
		if _, ok := reuseRustPackedCell(ctx, cacheRoot, c.key, c.normalized); !ok {
			tb.Fatalf("cell %s: image not reused", c.key)
		}
	}
}

// BenchmarkRustCellsOfOneLoad is the lookup of a load of 13 Rust cells whose images the cache holds, over an SDK tree of 40 source files: "per-cell" is the
// lookup with a reading of the tree for each cell, "per-load" the lookup under WithLoadTreeHashes, which reads it once.
func BenchmarkRustCellsOfOneLoad(b *testing.B) {
	cacheRoot, _, cells := publishedRustCells(b, 13)
	for _, mode := range []string{"per-cell", "per-load"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ctx := context.Background()
				if mode == "per-load" {
					ctx = WithLoadTreeHashes(ctx)
				}
				lookUpRustCells(b, ctx, cacheRoot, cells)
			}
		})
	}
}

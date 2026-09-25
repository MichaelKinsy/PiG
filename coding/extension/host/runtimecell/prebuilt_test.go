package runtimecell

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type fakeResolver struct {
	got PrebuiltRequest
	bin string
	ok  bool
}

func (f *fakeResolver) ResolvePrebuilt(req PrebuiltRequest) (string, bool) {
	f.got = req
	return f.bin, f.ok
}

// A registered resolver that matches must return the prebuilt binary without a
// from-source build. Bogus extension roots + a fresh cache guarantee a build
// would fail, so success can only come from the short-circuit.
func TestBuildGoPackedCell_PrebuiltShortCircuits(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "runner")
	if err := os.WriteFile(bin, []byte("prebuilt"), 0o755); err != nil {
		t.Fatal(err)
	}
	fr := &fakeResolver{bin: bin, ok: true}
	SetPrebuiltResolver(fr)
	t.Cleanup(func() { SetPrebuiltResolver(nil) })

	exts := []GoExtension{{Name: "web-search", Root: filepath.Join(dir, "nope"), ModulePath: "example.com/ws", Package: "example.com/ws", Factory: "", Hash: "h1"}}
	cell, err := BuildGoPackedCell(context.Background(), t.TempDir(), "ws-cell", exts)
	if err != nil {
		t.Fatalf("prebuilt short-circuit should not build: %v", err)
	}
	if cell.BinaryPath != bin || !cell.Cached {
		t.Errorf("cell = %+v, want prebuilt binary %q cached", cell, bin)
	}
	if fr.got.Language != "go" || fr.got.Key != "ws-cell" || fr.got.GOOS != runtime.GOOS || fr.got.GOARCH != runtime.GOARCH {
		t.Errorf("request = %+v, want go/ws-cell/%s/%s", fr.got, runtime.GOOS, runtime.GOARCH)
	}
	if len(fr.got.Extensions) != 1 || fr.got.Extensions[0] != (PrebuiltExtension{Name: "web-search", Hash: "h1"}) {
		t.Errorf("request extensions = %+v, want [{web-search h1}]", fr.got.Extensions)
	}
}

// With no resolver (stock pig) the host must attempt a from-source build, not
// short-circuit. Bogus roots make that attempt fail, proving it was tried.
func TestBuildGoPackedCell_NoResolverBuildsFromSource(t *testing.T) {
	SetPrebuiltResolver(nil)
	exts := []GoExtension{{Name: "web-search", Root: filepath.Join(t.TempDir(), "nope"), ModulePath: "example.com/ws", Package: "example.com/ws", Factory: "Extension", Hash: "h1"}}
	if _, err := BuildGoPackedCell(context.Background(), t.TempDir(), "ws-cell", exts); err == nil {
		t.Fatal("nil resolver must not short-circuit; expected a from-source build failure")
	}
}

// A resolver returning ok=false must fall through to the from-source path.
func TestBuildGoPackedCell_ResolverMissBuildsFromSource(t *testing.T) {
	SetPrebuiltResolver(&fakeResolver{ok: false})
	t.Cleanup(func() { SetPrebuiltResolver(nil) })
	exts := []GoExtension{{Name: "web-search", Root: filepath.Join(t.TempDir(), "nope"), ModulePath: "example.com/ws", Package: "example.com/ws", Factory: "Extension", Hash: "h1"}}
	if _, err := BuildGoPackedCell(context.Background(), t.TempDir(), "ws-cell", exts); err == nil {
		t.Fatal("resolver miss must fall through to a from-source build (expected failure)")
	}
}

// The same seam covers Rust cells, so a toolchain-less host serves an embedded
// Rust cell without cargo.
func TestBuildRustPackedCell_PrebuiltShortCircuits(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "pig-generated-packed-cell")
	if err := os.WriteFile(bin, []byte("prebuilt"), 0o755); err != nil {
		t.Fatal(err)
	}
	fr := &fakeResolver{bin: bin, ok: true}
	SetPrebuiltResolver(fr)
	t.Cleanup(func() { SetPrebuiltResolver(nil) })

	exts := []RustExtension{{Name: "rust-helper", Root: filepath.Join(dir, "nope"), Package: "rust_helper", Crate: "rust_helper", Factory: "", Hash: "r1"}}
	cell, err := BuildRustPackedCell(context.Background(), t.TempDir(), "rust-cell", exts)
	if err != nil {
		t.Fatalf("prebuilt short-circuit should not build: %v", err)
	}
	if cell.BinaryPath != bin || !cell.Cached {
		t.Errorf("cell = %+v, want prebuilt binary %q cached", cell, bin)
	}
	if fr.got.Language != "rust" || fr.got.Key != "rust-cell" {
		t.Errorf("request = %+v, want rust/rust-cell", fr.got)
	}
}

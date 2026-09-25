package cellpack

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// extractedAt is where extraction writes the cell binary embedded at rel:
// Windows starts only files with an executable extension, so there it is
// rel plus .exe.
func extractedAt(base, rel string) string {
	full := filepath.Join(base, filepath.FromSlash(rel))
	if runtime.GOOS == "windows" {
		full += ".exe"
	}
	return full
}

// stageBinary writes a placeholder extracted cell binary and returns the
// manifest-relative path plus the base dir.
func stageBinary(t *testing.T, rel string) string {
	t.Helper()
	base := t.TempDir()
	full := extractedAt(base, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	return base
}

func goReq(key string, exts ...runtimecell.PrebuiltExtension) runtimecell.PrebuiltRequest {
	return runtimecell.PrebuiltRequest{Language: "go", Key: key, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Extensions: exts}
}

func manifestFor(bin string) Manifest {
	return Manifest{Cells: []CellEntry{{
		Language: "go", Key: "go-core", OS: runtime.GOOS, Arch: runtime.GOARCH, Binary: bin,
		Extensions: []ExtEntry{{Name: "context-info", Hash: "h1"}, {Name: "subagent", Hash: "h2"}},
	}}}
}

func TestResolve_ExactMatchHits(t *testing.T) {
	base := stageBinary(t, "cells/go/aaaa/runner")
	r := NewResolver(manifestFor("cells/go/aaaa/runner"), base)

	// Extension order in the request differs from the manifest: must still hit.
	got, ok := r.ResolvePrebuilt(goReq("go-core",
		runtimecell.PrebuiltExtension{Name: "subagent", Hash: "h2"},
		runtimecell.PrebuiltExtension{Name: "context-info", Hash: "h1"}))
	if !ok {
		t.Fatal("exact composition (unordered) should hit")
	}
	if want := extractedAt(base, "cells/go/aaaa/runner"); got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}

func TestResolve_WrongArchMisses(t *testing.T) {
	base := stageBinary(t, "cells/go/aaaa/runner")
	r := NewResolver(manifestFor("cells/go/aaaa/runner"), base)
	req := goReq("go-core",
		runtimecell.PrebuiltExtension{Name: "context-info", Hash: "h1"},
		runtimecell.PrebuiltExtension{Name: "subagent", Hash: "h2"})
	req.GOARCH = "somethingelse"
	if _, ok := r.ResolvePrebuilt(req); ok {
		t.Error("different arch must miss")
	}
}

func TestResolve_DifferentExtensionSetMisses(t *testing.T) {
	base := stageBinary(t, "cells/go/aaaa/runner")
	r := NewResolver(manifestFor("cells/go/aaaa/runner"), base)

	// Missing one extension.
	if _, ok := r.ResolvePrebuilt(goReq("go-core", runtimecell.PrebuiltExtension{Name: "context-info", Hash: "h1"})); ok {
		t.Error("subset of extensions must miss")
	}
	// Extra extension.
	if _, ok := r.ResolvePrebuilt(goReq("go-core",
		runtimecell.PrebuiltExtension{Name: "context-info", Hash: "h1"},
		runtimecell.PrebuiltExtension{Name: "subagent", Hash: "h2"},
		runtimecell.PrebuiltExtension{Name: "web-search", Hash: "h3"})); ok {
		t.Error("superset of extensions must miss")
	}
}

func TestResolve_KnownHashMismatchFailsClosed(t *testing.T) {
	base := stageBinary(t, "cells/go/aaaa/runner")
	r := NewResolver(manifestFor("cells/go/aaaa/runner"), base)
	// Same names, but context-info's content changed (h9 != pinned h1).
	if _, ok := r.ResolvePrebuilt(goReq("go-core",
		runtimecell.PrebuiltExtension{Name: "context-info", Hash: "h9"},
		runtimecell.PrebuiltExtension{Name: "subagent", Hash: "h2"})); ok {
		t.Error("known differing hash must fail closed")
	}
}

func TestResolve_UnknownHashToleratedOnNames(t *testing.T) {
	base := stageBinary(t, "cells/go/aaaa/runner")
	r := NewResolver(manifestFor("cells/go/aaaa/runner"), base)
	// A source-less Piglet Binary's runtime request carries no hashes; match on names.
	if _, ok := r.ResolvePrebuilt(goReq("go-core",
		runtimecell.PrebuiltExtension{Name: "context-info"},
		runtimecell.PrebuiltExtension{Name: "subagent"})); !ok {
		t.Error("unknown request hashes should match on names alone")
	}
}

func TestResolve_MissingExtractedBinaryMisses(t *testing.T) {
	// Manifest references a binary that was never extracted.
	r := NewResolver(manifestFor("cells/go/aaaa/runner"), t.TempDir())
	if _, ok := r.ResolvePrebuilt(goReq("go-core",
		runtimecell.PrebuiltExtension{Name: "context-info", Hash: "h1"},
		runtimecell.PrebuiltExtension{Name: "subagent", Hash: "h2"})); ok {
		t.Error("missing extracted binary must fail closed")
	}
}
